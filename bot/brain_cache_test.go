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
	err = brain.syncKeyLocked("first")
	brain.mu.Unlock()
	if err != nil {
		t.Fatalf("sync first: %v", err)
	}
	second := []byte("second")
	if err := brain.Store("second", &second); err != nil {
		t.Fatalf("Store second: %v", err)
	}
	brain.mu.Lock()
	err = brain.syncKeyLocked("second")
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

func stringValue(data *[]byte) string {
	if data == nil {
		return ""
	}
	return string(*data)
}
