package bot

import (
	"context"
	crand "crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/lnxjedi/gopherbot/robot"
)

const brainCacheFormat = "gopherbot-brain-v3"

type BrainCacheConfig struct {
	Directory string `yaml:"Directory"`
}

func defaultBrainCacheConfig(cfg BrainCacheConfig) BrainCacheConfig {
	if strings.TrimSpace(cfg.Directory) == "" {
		cfg.Directory = filepath.Join("state", "brain-cache")
	}
	return cfg
}

type brainCacheControl struct {
	Format         string                     `json:"format"`
	Provider       robot.BrainBackendIdentity `json:"provider"`
	Complete       bool                       `json:"complete"`
	NextVersion    uint64                     `json:"next_version"`
	CheckpointKey  string                     `json:"checkpoint_key,omitempty"`
	CheckpointVers uint64                     `json:"checkpoint_version,omitempty"`
	CheckpointSum  string                     `json:"checkpoint_checksum,omitempty"`
	ImportedFrom   string                     `json:"imported_from,omitempty"`
	CacheNonce     string                     `json:"cache_nonce,omitempty"`
	ActiveLockID   string                     `json:"active_lock_id,omitempty"`
	LastCloudWrite *brainCacheCloudWrite      `json:"last_cloud_write,omitempty"`
	UpdatedAt      time.Time                  `json:"updated_at"`
}

type brainCacheCloudWrite struct {
	Key       string    `json:"key"`
	Version   uint64    `json:"version"`
	Checksum  string    `json:"checksum,omitempty"`
	Deleted   bool      `json:"deleted"`
	UpdatedAt time.Time `json:"updated_at"`
	SyncedAt  time.Time `json:"synced_at"`
}

type brainCacheMeta struct {
	Format    string    `json:"format"`
	Key       string    `json:"key"`
	Version   uint64    `json:"version"`
	Checksum  string    `json:"checksum"`
	Deleted   bool      `json:"deleted"`
	UpdatedAt time.Time `json:"updated_at"`
	SyncedAt  time.Time `json:"synced_at,omitempty"`
}

type brainCacheOutboxEntry struct {
	Key     string `json:"key"`
	Version uint64 `json:"version"`
	Delete  bool   `json:"delete"`
}

type brainCacheWriteBudget struct {
	Date   string `json:"date"`
	Writes int    `json:"writes"`
}

type cachedBrain struct {
	cfg     BrainCacheConfig
	remote  robot.RemoteBrainBackend
	control brainCacheControl
	policy  robot.BrainSyncPolicy

	mu      sync.Mutex
	wake    chan struct{}
	done    chan struct{}
	stopped bool
	wg      sync.WaitGroup
}

func newLocalCachedBrain(cfg BrainCacheConfig) (*cachedBrain, error) {
	cfg = defaultBrainCacheConfig(cfg)
	cb := &cachedBrain{cfg: cfg}
	if err := cb.openLocalOnly(); err != nil {
		return nil, err
	}
	return cb, nil
}

func newRemoteCachedBrain(cfg BrainCacheConfig, remote robot.RemoteBrainBackend) (*cachedBrain, error) {
	cfg = defaultBrainCacheConfig(cfg)
	cb := &cachedBrain{
		cfg:    cfg,
		remote: remote,
		policy: defaultBrainSyncPolicy(remote.SyncPolicy()),
		wake:   make(chan struct{}, 1),
		done:   make(chan struct{}),
	}
	if err := cb.openRemote(); err != nil {
		remote.Shutdown()
		return nil, err
	}
	cb.wg.Add(1)
	go cb.syncLoop()
	return cb, nil
}

func openBrainCacheForImport(cfg BrainCacheConfig, identity robot.BrainBackendIdentity, importedFrom string, force bool) (*cachedBrain, error) {
	cfg = defaultBrainCacheConfig(cfg)
	if force {
		if err := os.RemoveAll(cfg.Directory); err != nil {
			return nil, err
		}
	}
	cb := &cachedBrain{cfg: cfg}
	if err := cb.ensureDirs(); err != nil {
		return nil, err
	}
	if _, err := os.Stat(cb.controlPath()); err == nil {
		if err := cb.loadControl(identity); err != nil {
			return nil, err
		}
	} else {
		cb.control = brainCacheControl{
			Format:       brainCacheFormat,
			Provider:     identity,
			Complete:     false,
			NextVersion:  1,
			ImportedFrom: importedFrom,
			CacheNonce:   randomBrainCacheID(),
			UpdatedAt:    time.Now().UTC(),
		}
		if err := cb.writeControl(); err != nil {
			return nil, err
		}
	}
	return cb, nil
}

func openExistingBrainCacheAny(cfg BrainCacheConfig) (*cachedBrain, error) {
	cfg = defaultBrainCacheConfig(cfg)
	cb := &cachedBrain{cfg: cfg}
	var control brainCacheControl
	data, err := os.ReadFile(cb.controlPath())
	if err != nil {
		return nil, err
	}
	if err := json.Unmarshal(data, &control); err != nil {
		return nil, err
	}
	if control.Format != brainCacheFormat {
		return nil, fmt.Errorf("unsupported brain cache format %q", control.Format)
	}
	cb.control = control
	if cb.control.CacheNonce == "" {
		cb.control.CacheNonce = randomBrainCacheID()
		cb.control.UpdatedAt = time.Now().UTC()
		_ = cb.writeControl()
	}
	return cb, nil
}

func openExistingBrainCacheForIdentity(cfg BrainCacheConfig, identity robot.BrainBackendIdentity) (*cachedBrain, error) {
	cfg = defaultBrainCacheConfig(cfg)
	cb := &cachedBrain{cfg: cfg}
	if err := cb.loadControl(identity); err != nil {
		return nil, err
	}
	if !cb.control.Complete {
		return nil, fmt.Errorf("local brain cache at %s is incomplete; run gopherbot pull-brain", cfg.Directory)
	}
	return cb, nil
}

func defaultBrainSyncPolicy(policy robot.BrainSyncPolicy) robot.BrainSyncPolicy {
	if policy.CoalesceWindow <= 0 {
		policy.CoalesceWindow = 2 * time.Second
	}
	if policy.FlushOnShutdownMaxDuration <= 0 {
		policy.FlushOnShutdownMaxDuration = 10 * time.Second
	}
	return policy
}

func (b *cachedBrain) openLocalOnly() error {
	if err := b.ensureDirs(); err != nil {
		return err
	}
	controlPath := b.controlPath()
	if _, err := os.Stat(controlPath); errors.Is(err, os.ErrNotExist) {
		b.control = brainCacheControl{
			Format:      brainCacheFormat,
			Provider:    robot.BrainBackendIdentity{Provider: "file", Scope: "local"},
			Complete:    true,
			NextVersion: 1,
			CacheNonce:  randomBrainCacheID(),
			UpdatedAt:   time.Now().UTC(),
		}
		return b.writeControl()
	}
	return b.loadControl(robot.BrainBackendIdentity{Provider: "file", Scope: "local"})
}

func (b *cachedBrain) openRemote() error {
	if b.remote == nil {
		return errors.New("remote cached brain requires remote backend")
	}
	if err := b.ensureDirs(); err != nil {
		return err
	}
	identity := b.remote.Identity()
	controlPath := b.controlPath()
	if _, err := os.Stat(controlPath); errors.Is(err, os.ErrNotExist) {
		return b.hydrateFromV3Remote(identity)
	}
	if err := b.loadControl(identity); err != nil {
		return err
	}
	if !b.control.Complete {
		return fmt.Errorf("local brain cache at %s is incomplete; run gopherbot pull-brain", b.cfg.Directory)
	}
	if b.control.CheckpointKey != "" {
		if err := b.verifyRemoteRecord(b.control.CheckpointKey, b.control.CheckpointVers, b.control.CheckpointSum, false, false); err != nil {
			return fmt.Errorf("verifying brain cache checkpoint: %w", err)
		}
	}
	return nil
}

func (b *cachedBrain) hydrateFromV3Remote(identity robot.BrainBackendIdentity) error {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()

	b.control = brainCacheControl{
		Format:      brainCacheFormat,
		Provider:    identity,
		Complete:    false,
		NextVersion: 1,
		CacheNonce:  randomBrainCacheID(),
		UpdatedAt:   time.Now().UTC(),
	}
	if err := b.writeControl(); err != nil {
		return err
	}
	cursor := ""
	var metas []robot.RemoteBrainRecord
	for {
		page, err := b.remote.ListMetadata(ctx, cursor, 1000)
		if err != nil {
			return fmt.Errorf("listing remote brain metadata: %w", err)
		}
		for _, record := range page.Records {
			if record.Format != brainCacheFormat {
				return fmt.Errorf("remote brain contains v2/unversioned memories; run gopherbot pull-brain")
			}
			metas = append(metas, record)
		}
		if page.NextCursor == "" {
			break
		}
		cursor = page.NextCursor
	}
	sort.Slice(metas, func(i, j int) bool {
		if metas[i].Version == metas[j].Version {
			return metas[i].Key < metas[j].Key
		}
		return metas[i].Version < metas[j].Version
	})
	for _, meta := range metas {
		record, exists, err := b.remote.Get(ctx, meta.Key)
		if err != nil {
			return fmt.Errorf("hydrating remote memory %s: %w", meta.Key, err)
		}
		if !exists {
			continue
		}
		if record.Format != brainCacheFormat {
			return fmt.Errorf("remote brain contains v2/unversioned memory %s; run gopherbot pull-brain", meta.Key)
		}
		localMeta := brainCacheMeta{
			Format:    brainCacheFormat,
			Key:       record.Key,
			Version:   record.Version,
			Checksum:  record.Checksum,
			Deleted:   record.Deleted,
			UpdatedAt: record.UpdatedAt,
			SyncedAt:  time.Now().UTC(),
		}
		if localMeta.UpdatedAt.IsZero() {
			localMeta.UpdatedAt = time.Now().UTC()
		}
		if !record.Deleted {
			if checksumBytes(record.Payload) != record.Checksum {
				return fmt.Errorf("remote memory %s checksum mismatch", record.Key)
			}
			if err := b.writePayload(record.Key, record.Payload); err != nil {
				return err
			}
		}
		if err := b.writeMeta(localMeta); err != nil {
			return err
		}
		if record.Version >= b.control.NextVersion {
			b.control.NextVersion = record.Version + 1
		}
		if record.Version >= b.control.CheckpointVers {
			b.control.CheckpointKey = record.Key
			b.control.CheckpointVers = record.Version
			b.control.CheckpointSum = record.Checksum
		}
	}
	if b.control.NextVersion == 0 {
		b.control.NextVersion = 1
	}
	b.control.Complete = true
	b.control.UpdatedAt = time.Now().UTC()
	return b.writeControl()
}

func (b *cachedBrain) loadControl(identity robot.BrainBackendIdentity) error {
	var control brainCacheControl
	data, err := os.ReadFile(b.controlPath())
	if err != nil {
		return err
	}
	if err := json.Unmarshal(data, &control); err != nil {
		return err
	}
	if control.Format != brainCacheFormat {
		return fmt.Errorf("unsupported brain cache format %q", control.Format)
	}
	if control.Provider != identity {
		return fmt.Errorf("brain cache provider identity mismatch: cache is %s/%s, config is %s/%s; run gopherbot pull-brain or choose another BrainCache.Directory",
			control.Provider.Provider, control.Provider.Scope, identity.Provider, identity.Scope)
	}
	if control.NextVersion == 0 {
		control.NextVersion = 1
	}
	if control.CacheNonce == "" {
		control.CacheNonce = randomBrainCacheID()
		control.UpdatedAt = time.Now().UTC()
		b.control = control
		return b.writeControl()
	}
	b.control = control
	return nil
}

func (b *cachedBrain) Store(key string, blob *[]byte) error {
	if blob == nil {
		empty := []byte{}
		blob = &empty
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.stopped {
		return fmt.Errorf("brain is shutting down; no new writes accepted")
	}
	version := b.reserveVersionLocked()
	now := time.Now().UTC()
	meta := brainCacheMeta{
		Format:    brainCacheFormat,
		Key:       key,
		Version:   version,
		Checksum:  checksumBytes(*blob),
		UpdatedAt: now,
	}
	if err := b.writePayload(key, *blob); err != nil {
		return err
	}
	if err := b.writeMeta(meta); err != nil {
		return err
	}
	if b.remote != nil {
		if err := b.writeOutbox(brainCacheOutboxEntry{Key: key, Version: version}); err != nil {
			return err
		}
		if key == brainLockKey {
			return b.syncKeyLocked(key, false)
		}
		b.signalSync()
	}
	return nil
}

func (b *cachedBrain) importRaw(key string, payload []byte, deleted bool) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	version := b.reserveVersionLocked()
	meta := brainCacheMeta{
		Format:    brainCacheFormat,
		Key:       key,
		Version:   version,
		Checksum:  checksumBytes(payload),
		Deleted:   deleted,
		UpdatedAt: time.Now().UTC(),
	}
	if deleted {
		_ = os.Remove(b.payloadPath(key))
	} else if err := b.writePayload(key, payload); err != nil {
		return err
	}
	if err := b.writeMeta(meta); err != nil {
		return err
	}
	if version >= b.control.CheckpointVers {
		b.control.CheckpointKey = key
		b.control.CheckpointVers = version
		b.control.CheckpointSum = meta.Checksum
	}
	return b.writeControl()
}

func (b *cachedBrain) importV3Record(record robot.RemoteBrainRecord) error {
	if record.Format != brainCacheFormat {
		return fmt.Errorf("cannot import non-v3 brain record %s", record.Key)
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	now := time.Now().UTC()
	if record.UpdatedAt.IsZero() {
		record.UpdatedAt = now
	}
	if record.Checksum == "" && !record.Deleted {
		record.Checksum = checksumBytes(record.Payload)
	}
	if !record.Deleted && checksumBytes(record.Payload) != record.Checksum {
		return fmt.Errorf("remote memory %s checksum mismatch", record.Key)
	}
	meta := brainCacheMeta{
		Format:    brainCacheFormat,
		Key:       record.Key,
		Version:   record.Version,
		Checksum:  record.Checksum,
		Deleted:   record.Deleted,
		UpdatedAt: record.UpdatedAt,
		SyncedAt:  now,
	}
	if record.Deleted {
		_ = os.Remove(b.payloadPath(record.Key))
	} else if err := b.writePayload(record.Key, record.Payload); err != nil {
		return err
	}
	if err := b.writeMeta(meta); err != nil {
		return err
	}
	if record.Version >= b.control.NextVersion {
		b.control.NextVersion = record.Version + 1
	}
	if record.Version >= b.control.CheckpointVers {
		b.control.CheckpointKey = record.Key
		b.control.CheckpointVers = record.Version
		b.control.CheckpointSum = record.Checksum
	}
	return b.writeControl()
}

func (b *cachedBrain) finalizeImport(importedFrom string) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.control.Complete = true
	b.control.ImportedFrom = importedFrom
	b.control.UpdatedAt = time.Now().UTC()
	return b.writeControl()
}

func (b *cachedBrain) Retrieve(key string) (*[]byte, bool, error) {
	if !keyRe.MatchString(key) {
		return nil, false, fmt.Errorf("invalid memory key %q", key)
	}
	meta, exists, err := b.readMeta(key)
	if err != nil || !exists || meta.Deleted {
		return nil, false, err
	}
	payload, err := os.ReadFile(b.payloadPath(key))
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, false, nil
		}
		return nil, false, err
	}
	return &payload, true, nil
}

func (b *cachedBrain) List() ([]string, error) {
	entries, err := os.ReadDir(b.metaDir())
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return []string{}, nil
		}
		return nil, err
	}
	keys := make([]string, 0, len(entries))
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".json") {
			continue
		}
		meta, err := b.readMetaFile(filepath.Join(b.metaDir(), entry.Name()))
		if err != nil {
			return nil, err
		}
		if !meta.Deleted {
			keys = append(keys, meta.Key)
		}
	}
	sort.Strings(keys)
	return keys, nil
}

func (b *cachedBrain) Delete(key string) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	version := b.reserveVersionLocked()
	now := time.Now().UTC()
	meta := brainCacheMeta{
		Format:    brainCacheFormat,
		Key:       key,
		Version:   version,
		Deleted:   true,
		UpdatedAt: now,
	}
	_ = os.Remove(b.payloadPath(key))
	if err := b.writeMeta(meta); err != nil {
		return err
	}
	if b.remote != nil {
		if err := b.writeOutbox(brainCacheOutboxEntry{Key: key, Version: version, Delete: true}); err != nil {
			return err
		}
		if key == brainLockKey {
			return b.syncKeyLocked(key, false)
		}
		b.signalSync()
	}
	return nil
}

func (b *cachedBrain) Flush() error {
	if b.remote == nil {
		return nil
	}
	warnEvery := b.policy.FlushOnShutdownMaxDuration
	if warnEvery <= 0 {
		warnEvery = 10 * time.Second
	}
	lastWarn := time.Now()
	for {
		entry, ok, err := b.nextOutboxEntry()
		if err != nil {
			if time.Since(lastWarn) >= warnEvery {
				Log(robot.Warn, "Brain cache flush is waiting because reading the outbox failed: %v", err)
				lastWarn = time.Now()
			}
			time.Sleep(time.Second)
			continue
		}
		if !ok {
			return nil
		}
		b.mu.Lock()
		err = b.syncKeyLocked(entry.Key, false)
		b.mu.Unlock()
		if err != nil {
			if time.Since(lastWarn) >= warnEvery {
				Log(robot.Warn, "Brain cache flush is waiting for cloud sync of %s: %v", entry.Key, err)
				lastWarn = time.Now()
			}
			time.Sleep(time.Second)
			continue
		}
		if b.policy.MinWriteInterval > 0 {
			time.Sleep(b.policy.MinWriteInterval)
		}
	}
}

func (b *cachedBrain) Shutdown() {
	if b.remote != nil {
		if err := b.Flush(); err != nil {
			Log(robot.Warn, "Brain cache flush during shutdown returned error: %v", err)
		}
	}
	b.mu.Lock()
	if b.stopped {
		b.mu.Unlock()
		return
	}
	b.stopped = true
	b.mu.Unlock()
	if b.remote != nil {
		b.signalSync()
		close(b.done)
		b.wg.Wait()
		b.remote.Shutdown()
	}
}

func (b *cachedBrain) ShutdownWithoutFlush() {
	b.mu.Lock()
	if b.stopped {
		b.mu.Unlock()
		return
	}
	b.stopped = true
	b.mu.Unlock()
	if b.remote != nil {
		close(b.done)
		b.wg.Wait()
		b.remote.Shutdown()
	}
}

func (b *cachedBrain) reserveVersionLocked() uint64 {
	version := b.control.NextVersion
	if version == 0 {
		version = 1
	}
	b.control.NextVersion = version + 1
	b.control.UpdatedAt = time.Now().UTC()
	_ = b.writeControl()
	return version
}

func (b *cachedBrain) syncLoop() {
	defer b.wg.Done()
	for {
		select {
		case <-b.done:
			return
		case <-b.wake:
			if b.policy.CoalesceWindow > 0 {
				select {
				case <-b.done:
					return
				case <-time.After(b.policy.CoalesceWindow):
				}
			}
			b.syncPending()
		}
	}
}

func (b *cachedBrain) syncPending() {
	for {
		entry, ok, err := b.nextOutboxEntry()
		if err != nil {
			Log(robot.Warn, "Reading brain cache outbox: %v", err)
			return
		}
		if !ok {
			return
		}
		b.mu.Lock()
		err = b.syncOutboxEntryLocked(entry, true)
		b.mu.Unlock()
		if err != nil {
			Log(robot.Warn, "Syncing brain memory %s: %v", entry.Key, err)
			return
		}
		if b.policy.MinWriteInterval > 0 {
			select {
			case <-b.done:
				return
			case <-time.After(b.policy.MinWriteInterval):
			}
		}
	}
}

func (b *cachedBrain) syncKeyLocked(key string, enforceBudget bool) error {
	entry, exists, err := b.readOutboxEntry(key)
	if err != nil {
		return err
	}
	if !exists {
		return nil
	}
	return b.syncOutboxEntryLocked(entry, enforceBudget)
}

func (b *cachedBrain) syncOutboxEntryLocked(entry brainCacheOutboxEntry, enforceBudget bool) error {
	if b.remote == nil {
		return nil
	}
	current, exists, err := b.readOutboxEntry(entry.Key)
	if err != nil {
		return err
	}
	if !exists {
		return nil
	}
	if current.Version != entry.Version {
		return nil
	}
	meta, exists, err := b.readMeta(entry.Key)
	if err != nil {
		return err
	}
	if !exists {
		return b.removeOutbox(entry.Key)
	}
	record := robot.RemoteBrainRecord{
		Key:       meta.Key,
		Format:    meta.Format,
		Version:   meta.Version,
		Checksum:  meta.Checksum,
		Deleted:   meta.Deleted,
		UpdatedAt: meta.UpdatedAt,
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if enforceBudget {
		if err := b.checkCloudWriteBudgetLocked(); err != nil {
			return err
		}
	}
	if meta.Deleted {
		if err := b.remote.Delete(ctx, record); err != nil {
			return err
		}
	} else {
		payload, err := os.ReadFile(b.payloadPath(entry.Key))
		if err != nil {
			return err
		}
		record.Payload = payload
		if record.Checksum == "" {
			record.Checksum = checksumBytes(payload)
		}
		if err := b.remote.Put(ctx, record); err != nil {
			return err
		}
	}
	if err := b.recordCloudWriteLocked(); err != nil {
		return err
	}
	now := time.Now().UTC()
	meta.SyncedAt = now
	if err := b.writeMeta(meta); err != nil {
		return err
	}
	controlDirty := false
	if entry.Key != brainLockKey {
		b.control.LastCloudWrite = &brainCacheCloudWrite{
			Key:       meta.Key,
			Version:   meta.Version,
			Checksum:  meta.Checksum,
			Deleted:   meta.Deleted,
			UpdatedAt: meta.UpdatedAt,
			SyncedAt:  now,
		}
		controlDirty = true
	}
	if meta.Version >= b.control.CheckpointVers {
		b.control.CheckpointKey = entry.Key
		b.control.CheckpointVers = meta.Version
		b.control.CheckpointSum = meta.Checksum
		b.control.UpdatedAt = now
		controlDirty = true
	}
	if controlDirty {
		b.control.UpdatedAt = now
		if err := b.writeControl(); err != nil {
			return err
		}
	}
	return b.removeOutbox(entry.Key)
}

func (b *cachedBrain) nextOutboxEntry() (brainCacheOutboxEntry, bool, error) {
	entries, err := b.outboxEntries()
	if err != nil || len(entries) == 0 {
		return brainCacheOutboxEntry{}, false, err
	}
	return entries[0], true, nil
}

func (b *cachedBrain) readOutboxEntry(key string) (brainCacheOutboxEntry, bool, error) {
	data, err := os.ReadFile(b.outboxPath(key))
	if errors.Is(err, os.ErrNotExist) {
		return brainCacheOutboxEntry{}, false, nil
	}
	if err != nil {
		return brainCacheOutboxEntry{}, false, err
	}
	var entry brainCacheOutboxEntry
	if err := json.Unmarshal(data, &entry); err != nil {
		return brainCacheOutboxEntry{}, false, err
	}
	return entry, true, nil
}

func (b *cachedBrain) outboxEntries() ([]brainCacheOutboxEntry, error) {
	files, err := os.ReadDir(b.outboxDir())
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, nil
		}
		return nil, err
	}
	out := make([]brainCacheOutboxEntry, 0, len(files))
	for _, file := range files {
		if file.IsDir() || !strings.HasSuffix(file.Name(), ".json") {
			continue
		}
		data, err := os.ReadFile(filepath.Join(b.outboxDir(), file.Name()))
		if err != nil {
			return nil, err
		}
		var entry brainCacheOutboxEntry
		if err := json.Unmarshal(data, &entry); err != nil {
			return nil, err
		}
		out = append(out, entry)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Version == out[j].Version {
			return out[i].Key < out[j].Key
		}
		return out[i].Version < out[j].Version
	})
	return out, nil
}

func (b *cachedBrain) signalSync() {
	if b.wake == nil {
		return
	}
	select {
	case b.wake <- struct{}{}:
	default:
	}
}

func (b *cachedBrain) ensureDirs() error {
	for _, dir := range []string{b.cfg.Directory, b.dataDir(), b.metaDir(), b.outboxDir()} {
		if err := os.MkdirAll(dir, 0750); err != nil {
			return err
		}
	}
	return nil
}

func (b *cachedBrain) controlPath() string { return filepath.Join(b.cfg.Directory, "control.json") }
func (b *cachedBrain) dataDir() string     { return filepath.Join(b.cfg.Directory, "data") }
func (b *cachedBrain) metaDir() string     { return filepath.Join(b.cfg.Directory, "meta") }
func (b *cachedBrain) outboxDir() string   { return filepath.Join(b.cfg.Directory, "outbox") }

func (b *cachedBrain) payloadPath(key string) string {
	return filepath.Join(b.dataDir(), encodeBrainCacheKey(key)+".blob")
}

func (b *cachedBrain) metaPath(key string) string {
	return filepath.Join(b.metaDir(), encodeBrainCacheKey(key)+".json")
}

func (b *cachedBrain) outboxPath(key string) string {
	return filepath.Join(b.outboxDir(), encodeBrainCacheKey(key)+".json")
}
func (b *cachedBrain) budgetPath() string { return filepath.Join(b.cfg.Directory, "write-budget.json") }

func encodeBrainCacheKey(key string) string {
	return base64.RawURLEncoding.EncodeToString([]byte(key))
}

func checksumBytes(data []byte) string {
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

func randomBrainCacheID() string {
	raw := make([]byte, 16)
	if _, err := crand.Read(raw); err != nil {
		return fmt.Sprintf("%d", time.Now().UnixNano())
	}
	return hex.EncodeToString(raw)
}

func hashBrainCacheNonce(nonce string) string {
	if nonce == "" {
		return ""
	}
	return checksumBytes([]byte(nonce))
}

func (b *cachedBrain) latestDatabaseVersion() uint64 {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.latestDatabaseVersionLocked()
}

func (b *cachedBrain) latestDatabaseVersionLocked() uint64 {
	if b.control.NextVersion == 0 {
		return 0
	}
	return b.control.NextVersion - 1
}

func (b *cachedBrain) cacheNonceHash() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return hashBrainCacheNonce(b.control.CacheNonce)
}

func (b *cachedBrain) activeLockID() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.control.ActiveLockID
}

func (b *cachedBrain) setActiveLockID(lockID string) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.control.ActiveLockID = lockID
	b.control.UpdatedAt = time.Now().UTC()
	return b.writeControl()
}

func (b *cachedBrain) verifyLastCloudWrite() error {
	if b.remote == nil {
		return nil
	}
	b.mu.Lock()
	checkpoint := b.control.LastCloudWrite
	b.mu.Unlock()
	if checkpoint == nil || checkpoint.Key == "" {
		return nil
	}
	return b.verifyRemoteRecord(checkpoint.Key, checkpoint.Version, checkpoint.Checksum, checkpoint.Deleted, true)
}

func (b *cachedBrain) verifyRemoteRecord(key string, version uint64, checksum string, deleted bool, checkDeleted bool) error {
	attempts := b.policy.CheckpointVerifyRetries + 1
	if attempts < 1 {
		attempts = 1
	}
	delay := b.policy.CheckpointVerifyDelay
	var lastErr error
	for attempt := 0; attempt < attempts; attempt++ {
		if attempt > 0 && delay > 0 {
			time.Sleep(delay)
		}
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		record, exists, err := b.remote.Get(ctx, key)
		cancel()
		if err != nil {
			lastErr = err
			continue
		}
		if !exists {
			lastErr = fmt.Errorf("remote memory %s is missing", key)
			continue
		}
		if record.Format != brainCacheFormat {
			lastErr = fmt.Errorf("remote memory %s is not v3-compatible", key)
			continue
		}
		if record.Version == version && record.Checksum == checksum && (!checkDeleted || record.Deleted == deleted) {
			return nil
		}
		lastErr = fmt.Errorf("remote memory %s checkpoint mismatch: local version %d checksum %s deleted %t, remote version %d checksum %s deleted %t",
			key, version, checksum, deleted, record.Version, record.Checksum, record.Deleted)
	}
	return lastErr
}

func (b *cachedBrain) writePayload(key string, payload []byte) error {
	return writeAtomicFile(b.payloadPath(key), payload, 0600)
}

func (b *cachedBrain) writeMeta(meta brainCacheMeta) error {
	data, err := json.MarshalIndent(meta, "", "  ")
	if err != nil {
		return err
	}
	return writeAtomicFile(b.metaPath(meta.Key), data, 0600)
}

func (b *cachedBrain) readMeta(key string) (brainCacheMeta, bool, error) {
	meta, err := b.readMetaFile(b.metaPath(key))
	if errors.Is(err, os.ErrNotExist) {
		return brainCacheMeta{}, false, nil
	}
	return meta, err == nil, err
}

func (b *cachedBrain) readMetaFile(path string) (brainCacheMeta, error) {
	var meta brainCacheMeta
	data, err := os.ReadFile(path)
	if err != nil {
		return meta, err
	}
	if err := json.Unmarshal(data, &meta); err != nil {
		return meta, err
	}
	if meta.Format != brainCacheFormat {
		return meta, fmt.Errorf("unsupported brain cache datum format %q for %s", meta.Format, meta.Key)
	}
	return meta, nil
}

func (b *cachedBrain) writeControl() error {
	data, err := json.MarshalIndent(b.control, "", "  ")
	if err != nil {
		return err
	}
	return writeAtomicFile(b.controlPath(), data, 0600)
}

func (b *cachedBrain) writeOutbox(entry brainCacheOutboxEntry) error {
	data, err := json.MarshalIndent(entry, "", "  ")
	if err != nil {
		return err
	}
	return writeAtomicFile(b.outboxPath(entry.Key), data, 0600)
}

func (b *cachedBrain) removeOutbox(key string) error {
	err := os.Remove(b.outboxPath(key))
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	return err
}

func (b *cachedBrain) checkCloudWriteBudgetLocked() error {
	if b.policy.WriteBudgetPerDay <= 0 {
		return nil
	}
	budget, err := b.readWriteBudget()
	if err != nil {
		return err
	}
	if budget.Writes >= b.policy.WriteBudgetPerDay {
		return fmt.Errorf("cloud brain write budget exhausted (%d/%d for %s); pending memories remain queued",
			budget.Writes, b.policy.WriteBudgetPerDay, budget.Date)
	}
	return nil
}

func (b *cachedBrain) recordCloudWriteLocked() error {
	if b.policy.WriteBudgetPerDay <= 0 {
		return nil
	}
	budget, err := b.readWriteBudget()
	if err != nil {
		return err
	}
	budget.Writes++
	return b.writeWriteBudget(budget)
}

func (b *cachedBrain) readWriteBudget() (brainCacheWriteBudget, error) {
	today := time.Now().UTC().Format("2006-01-02")
	budget := brainCacheWriteBudget{Date: today}
	data, err := os.ReadFile(b.budgetPath())
	if errors.Is(err, os.ErrNotExist) {
		return budget, nil
	}
	if err != nil {
		return brainCacheWriteBudget{}, err
	}
	if err := json.Unmarshal(data, &budget); err != nil {
		return brainCacheWriteBudget{}, err
	}
	if budget.Date != today {
		return brainCacheWriteBudget{Date: today}, nil
	}
	return budget, nil
}

func (b *cachedBrain) writeWriteBudget(budget brainCacheWriteBudget) error {
	data, err := json.MarshalIndent(budget, "", "  ")
	if err != nil {
		return err
	}
	return writeAtomicFile(b.budgetPath(), data, 0600)
}

func writeAtomicFile(path string, data []byte, mode os.FileMode) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0750); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(dir, ".tmp-*")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName)
	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Chmod(mode); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmpName, path)
}
