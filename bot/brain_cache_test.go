package bot

import (
	"context"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/lnxjedi/gopherbot/robot"
)

type testRemoteBrain struct {
	identity robot.BrainBackendIdentity
	policy   robot.BrainSyncPolicy
	records  map[string]robot.RemoteBrainRecord

	getCalls    int
	listCalls   int
	putCalls    int
	deleteCalls int
}

func newTestRemote(records map[string]robot.RemoteBrainRecord) *testRemoteBrain {
	copied := make(map[string]robot.RemoteBrainRecord, len(records))
	for key, record := range records {
		copied[key] = record
	}
	return &testRemoteBrain{
		identity: robot.BrainBackendIdentity{Provider: "test", Scope: "scope"},
		records:  copied,
	}
}

func (r *testRemoteBrain) Identity() robot.BrainBackendIdentity {
	return r.identity
}

func (r *testRemoteBrain) SyncPolicy() robot.BrainSyncPolicy {
	return r.policy
}

func (r *testRemoteBrain) Get(ctx context.Context, key string) (robot.RemoteBrainRecord, bool, error) {
	r.getCalls++
	record, ok := r.records[key]
	return record, ok, nil
}

func (r *testRemoteBrain) Put(ctx context.Context, record robot.RemoteBrainRecord) error {
	r.putCalls++
	r.records[record.Key] = record
	return nil
}

func (r *testRemoteBrain) Delete(ctx context.Context, tombstone robot.RemoteBrainRecord) error {
	r.deleteCalls++
	tombstone.Deleted = true
	r.records[tombstone.Key] = tombstone
	return nil
}

func (r *testRemoteBrain) ListMetadata(ctx context.Context, cursor string, limit int) (robot.RemoteBrainPage, error) {
	r.listCalls++
	keys := make([]string, 0, len(r.records))
	for key := range r.records {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	records := make([]robot.RemoteBrainRecord, 0, len(keys))
	for _, key := range keys {
		record := r.records[key]
		record.Payload = nil
		records = append(records, record)
	}
	return robot.RemoteBrainPage{Records: records}, nil
}

func (r *testRemoteBrain) Shutdown() {}

type staleFirstGetRemote struct {
	*testRemoteBrain
	key       string
	stale     robot.RemoteBrainRecord
	usedStale bool
}

func (r *staleFirstGetRemote) Get(ctx context.Context, key string) (robot.RemoteBrainRecord, bool, error) {
	r.getCalls++
	if key == r.key && !r.usedStale {
		r.usedStale = true
		return r.stale, true, nil
	}
	record, ok := r.records[key]
	return record, ok, nil
}

func TestLocalBrainCachePersistsAndDeletes(t *testing.T) {
	dir := t.TempDir()
	brain, err := newLocalCachedBrain(BrainCacheConfig{Directory: dir})
	if err != nil {
		t.Fatalf("newLocalCachedBrain: %v", err)
	}
	payload := []byte("hello")
	if err := brain.Store("alpha", &payload); err != nil {
		t.Fatalf("Store: %v", err)
	}
	got, exists, err := brain.Retrieve("alpha")
	if err != nil {
		t.Fatalf("Retrieve: %v", err)
	}
	if !exists || got == nil || string(*got) != "hello" {
		t.Fatalf("Retrieve got exists=%v payload=%q", exists, stringValue(got))
	}
	keys, err := brain.List()
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(keys) != 1 || keys[0] != "alpha" {
		t.Fatalf("List got %v", keys)
	}
	if err := brain.Delete("alpha"); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	brain.Shutdown()

	reopened, err := newLocalCachedBrain(BrainCacheConfig{Directory: dir})
	if err != nil {
		t.Fatalf("reopen local cache: %v", err)
	}
	defer reopened.Shutdown()
	if _, exists, err := reopened.Retrieve("alpha"); err != nil || exists {
		t.Fatalf("deleted key after reopen exists=%v err=%v", exists, err)
	}
}

func TestRemoteBrainCacheHydratesV3ThenUsesCheckpointOnRestart(t *testing.T) {
	payload := []byte("one")
	remote := newTestRemote(map[string]robot.RemoteBrainRecord{
		"alpha": {
			Key:       "alpha",
			Payload:   payload,
			Format:    brainCacheFormat,
			Version:   7,
			Checksum:  checksumBytes(payload),
			UpdatedAt: time.Now().UTC(),
		},
	})
	dir := t.TempDir()
	brain, err := newRemoteCachedBrain(BrainCacheConfig{Directory: dir}, remote)
	if err != nil {
		t.Fatalf("newRemoteCachedBrain: %v", err)
	}
	if remote.listCalls != 1 {
		t.Fatalf("first start list calls = %d, want 1", remote.listCalls)
	}
	got, exists, err := brain.Retrieve("alpha")
	if err != nil {
		t.Fatalf("Retrieve: %v", err)
	}
	if !exists || got == nil || string(*got) != "one" {
		t.Fatalf("hydrated payload exists=%v payload=%q", exists, stringValue(got))
	}
	brain.Shutdown()

	restartRemote := newTestRemote(remote.records)
	restarted, err := newRemoteCachedBrain(BrainCacheConfig{Directory: dir}, restartRemote)
	if err != nil {
		t.Fatalf("restart newRemoteCachedBrain: %v", err)
	}
	defer restarted.Shutdown()
	if restartRemote.listCalls != 0 {
		t.Fatalf("restart list calls = %d, want 0", restartRemote.listCalls)
	}
	if restartRemote.getCalls != 1 {
		t.Fatalf("restart checkpoint get calls = %d, want 1", restartRemote.getCalls)
	}
}

func TestRemoteBrainCacheCheckpointUsesProviderRetryPolicy(t *testing.T) {
	payload := []byte("one")
	remote := newTestRemote(map[string]robot.RemoteBrainRecord{
		"alpha": {
			Key:       "alpha",
			Payload:   payload,
			Format:    brainCacheFormat,
			Version:   7,
			Checksum:  checksumBytes(payload),
			UpdatedAt: time.Now().UTC(),
		},
	})
	dir := t.TempDir()
	brain, err := newRemoteCachedBrain(BrainCacheConfig{Directory: dir}, remote)
	if err != nil {
		t.Fatalf("newRemoteCachedBrain: %v", err)
	}
	brain.Shutdown()

	restartBase := newTestRemote(remote.records)
	restartBase.policy = robot.BrainSyncPolicy{CheckpointVerifyRetries: 1}
	restartRemote := &staleFirstGetRemote{
		testRemoteBrain: restartBase,
		key:             "alpha",
		stale: robot.RemoteBrainRecord{
			Key:      "alpha",
			Format:   brainCacheFormat,
			Version:  6,
			Checksum: "stale",
		},
	}
	restarted, err := newRemoteCachedBrain(BrainCacheConfig{Directory: dir}, restartRemote)
	if err != nil {
		t.Fatalf("restart with retrying checkpoint: %v", err)
	}
	defer restarted.Shutdown()
	if restartRemote.getCalls != 2 {
		t.Fatalf("restart checkpoint get calls = %d, want 2", restartRemote.getCalls)
	}
}

func TestRemoteBrainCacheRejectsV2RemoteOnFirstStart(t *testing.T) {
	remote := newTestRemote(map[string]robot.RemoteBrainRecord{
		"legacy": {Key: "legacy"},
	})
	_, err := newRemoteCachedBrain(BrainCacheConfig{Directory: t.TempDir()}, remote)
	if err == nil {
		t.Fatal("expected v2/unversioned remote startup error")
	}
	if !strings.Contains(err.Error(), "pull-brain") {
		t.Fatalf("startup error %q does not mention pull-brain", err)
	}
}

func TestRemoteBrainCacheWriteBudgetLeavesPendingOutbox(t *testing.T) {
	remote := newTestRemote(nil)
	remote.policy = robot.BrainSyncPolicy{
		WriteBudgetPerDay: 1,
		CoalesceWindow:    time.Hour,
	}
	brain, err := newRemoteCachedBrain(BrainCacheConfig{Directory: t.TempDir()}, remote)
	if err != nil {
		t.Fatalf("newRemoteCachedBrain: %v", err)
	}
	defer brain.Shutdown()
	first := []byte("first")
	if err := brain.Store("first", &first); err != nil {
		t.Fatalf("Store first: %v", err)
	}
	brain.mu.Lock()
	err = brain.syncKeyLocked("first", true)
	brain.mu.Unlock()
	if err != nil {
		t.Fatalf("sync first: %v", err)
	}
	second := []byte("second")
	if err := brain.Store("second", &second); err != nil {
		t.Fatalf("Store second: %v", err)
	}
	brain.mu.Lock()
	err = brain.syncKeyLocked("second", true)
	brain.mu.Unlock()
	if err == nil {
		t.Fatal("expected write budget error")
	}
	if remote.putCalls != 1 {
		t.Fatalf("remote writes = %d, want 1", remote.putCalls)
	}
	if _, ok, err := brain.nextOutboxEntry(); err != nil {
		t.Fatalf("nextOutboxEntry: %v", err)
	} else if !ok {
		t.Fatal("expected pending outbox entry after budget exhaustion")
	}
}

func TestRemoteBrainCacheCoalescesRepeatedWritesToSameKey(t *testing.T) {
	remote := newTestRemote(nil)
	remote.policy = robot.BrainSyncPolicy{CoalesceWindow: time.Hour}
	brain, err := newRemoteCachedBrain(BrainCacheConfig{Directory: t.TempDir()}, remote)
	if err != nil {
		t.Fatalf("newRemoteCachedBrain: %v", err)
	}
	defer brain.Shutdown()
	first := []byte("first")
	if err := brain.Store("grocery_list", &first); err != nil {
		t.Fatalf("Store first: %v", err)
	}
	second := []byte("second")
	if err := brain.Store("grocery_list", &second); err != nil {
		t.Fatalf("Store second: %v", err)
	}
	if err := brain.Flush(); err != nil {
		t.Fatalf("Flush: %v", err)
	}
	if remote.putCalls != 1 {
		t.Fatalf("remote writes = %d, want 1", remote.putCalls)
	}
	record := remote.records["grocery_list"]
	if string(record.Payload) != "second" {
		t.Fatalf("remote payload = %q, want second", string(record.Payload))
	}
	if record.Version != 2 {
		t.Fatalf("remote version = %d, want 2", record.Version)
	}
}

func TestRemoteBrainCacheFlushBypassesNormalWriteBudget(t *testing.T) {
	remote := newTestRemote(nil)
	remote.policy = robot.BrainSyncPolicy{
		WriteBudgetPerDay: 1,
		CoalesceWindow:    time.Hour,
	}
	brain, err := newRemoteCachedBrain(BrainCacheConfig{Directory: t.TempDir()}, remote)
	if err != nil {
		t.Fatalf("newRemoteCachedBrain: %v", err)
	}
	defer brain.Shutdown()
	first := []byte("first")
	if err := brain.Store("first", &first); err != nil {
		t.Fatalf("Store first: %v", err)
	}
	brain.mu.Lock()
	err = brain.syncKeyLocked("first", true)
	brain.mu.Unlock()
	if err != nil {
		t.Fatalf("sync first: %v", err)
	}
	second := []byte("second")
	if err := brain.Store("second", &second); err != nil {
		t.Fatalf("Store second: %v", err)
	}
	if err := brain.Flush(); err != nil {
		t.Fatalf("Flush: %v", err)
	}
	if remote.putCalls != 2 {
		t.Fatalf("remote writes = %d, want 2", remote.putCalls)
	}
	if _, ok, err := brain.nextOutboxEntry(); err != nil {
		t.Fatalf("nextOutboxEntry: %v", err)
	} else if ok {
		t.Fatal("expected no pending outbox entries after Flush")
	}
}

func TestRemoteBrainCacheTracksLastCloudWriteExcludingLock(t *testing.T) {
	remote := newTestRemote(nil)
	remote.policy = robot.BrainSyncPolicy{CoalesceWindow: time.Hour}
	brain, err := newRemoteCachedBrain(BrainCacheConfig{Directory: t.TempDir()}, remote)
	if err != nil {
		t.Fatalf("newRemoteCachedBrain: %v", err)
	}
	defer brain.Shutdown()
	payload := []byte("data")
	if err := brain.Store("alpha", &payload); err != nil {
		t.Fatalf("Store alpha: %v", err)
	}
	if err := brain.Flush(); err != nil {
		t.Fatalf("Flush alpha: %v", err)
	}
	if brain.control.LastCloudWrite == nil || brain.control.LastCloudWrite.Key != "alpha" {
		t.Fatalf("LastCloudWrite = %#v, want alpha", brain.control.LastCloudWrite)
	}
	lockPayload := []byte("lock")
	if err := brain.Store(brainLockKey, &lockPayload); err != nil {
		t.Fatalf("Store lock: %v", err)
	}
	if brain.control.LastCloudWrite == nil || brain.control.LastCloudWrite.Key != "alpha" {
		t.Fatalf("lock write changed LastCloudWrite to %#v", brain.control.LastCloudWrite)
	}
}

func TestBrainLockReleasedVersionDetectsStaleLocalCache(t *testing.T) {
	brain, err := newLocalCachedBrain(BrainCacheConfig{Directory: t.TempDir()})
	if err != nil {
		t.Fatalf("newLocalCachedBrain: %v", err)
	}
	defer brain.Shutdown()
	interfaces.brain = brain
	defer func() { interfaces.brain = nil }()
	brain.control.NextVersion = 5
	if err := validateReleasedBrainLock(instanceLockData{State: brainLockReleased, DatabaseVersion: 4}); err != nil {
		t.Fatalf("released version equal to local latest should pass: %v", err)
	}
	if err := validateReleasedBrainLock(instanceLockData{State: brainLockReleased, DatabaseVersion: 5}); err == nil {
		t.Fatal("expected stale local cache error")
	}
}

func TestBrainLockReclaimRequiresMatchingLocalNonceAndLockID(t *testing.T) {
	brain, err := newLocalCachedBrain(BrainCacheConfig{Directory: t.TempDir()})
	if err != nil {
		t.Fatalf("newLocalCachedBrain: %v", err)
	}
	defer brain.Shutdown()
	interfaces.brain = brain
	defer func() { interfaces.brain = nil }()
	if err := brain.setActiveLockID("lock-1"); err != nil {
		t.Fatalf("setActiveLockID: %v", err)
	}
	lock := instanceLockData{
		State:          brainLockHeld,
		LockID:         "lock-1",
		CacheNonceHash: brain.cacheNonceHash(),
	}
	if !canReclaimHeldBrainLock(lock) {
		t.Fatal("expected matching lock id and nonce to be reclaimable")
	}
	lock.CacheNonceHash = "different"
	if canReclaimHeldBrainLock(lock) {
		t.Fatal("expected mismatched nonce to block reclaim")
	}
}

func stringValue(data *[]byte) string {
	if data == nil {
		return ""
	}
	return string(*data)
}
