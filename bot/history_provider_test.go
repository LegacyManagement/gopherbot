package bot

import (
	"encoding/json"
	"testing"

	"github.com/lnxjedi/gopherbot/robot"
)

func TestGetHistoryProviderFallsBackToMem(t *testing.T) {
	prevHistory := interfaces.history
	prevMem := memHistories
	prevHistoryConfig := historyConfig
	t.Cleanup(func() {
		interfaces.history = prevHistory
		memHistories = prevMem
		historyConfig = prevHistoryConfig
	})

	interfaces.history = nil
	memHistories = nil
	historyConfig = nil

	hprovider := getHistoryProvider()
	if hprovider == nil {
		t.Fatal("expected fallback history provider, got nil")
	}
	if memHistories == nil {
		t.Fatal("expected mem history provider to initialize")
	}
	if hprovider != memHistories {
		t.Fatalf("expected mem history provider fallback, got %T", hprovider)
	}

	logger, err := hprovider.NewLog("test-fallback", 1, 0)
	if err != nil {
		t.Fatalf("creating fallback history log: %v", err)
	}
	logger.Log("hello")
	logger.Close()
	logger.Finalize()
}

func setupHistoryBrainTest(t *testing.T) *memBrain {
	t.Helper()

	prevBrain := interfaces.brain
	prevHistory := interfaces.history
	prevMem := memHistories
	prevHistoryConfig := historyConfig
	testBrain := &memBrain{memories: make(map[string]*[]byte)}
	interfaces.brain = testBrain
	interfaces.history = mhprovider(handler{})
	t.Cleanup(func() {
		interfaces.brain = prevBrain
		interfaces.history = prevHistory
		memHistories = prevMem
		historyConfig = prevHistoryConfig
	})

	cryptKey.Lock()
	oldKey := append([]byte(nil), cryptKey.key...)
	oldInitialized := cryptKey.initialized
	oldInitializing := cryptKey.initializing
	cryptKey.key = []byte("0123456789abcdef0123456789abcdef")
	cryptKey.initialized = true
	cryptKey.initializing = false
	cryptKey.Unlock()
	t.Cleanup(func() {
		cryptKey.Lock()
		cryptKey.key = oldKey
		cryptKey.initialized = oldInitialized
		cryptKey.initializing = oldInitializing
		cryptKey.Unlock()
	})

	done := make(chan struct{})
	go func() {
		runBrain()
		close(done)
	}()
	t.Cleanup(func() {
		brainQuit()
		<-done
	})

	return testBrain
}

func TestNewLoggerWithNoRetainedLogsDoesNotStoreHistoryMemory(t *testing.T) {
	testBrain := setupHistoryBrainTest(t)

	logger, _, ref, idx := newLogger("lists", "abc123", "", 42, 0)
	if logger == nil {
		t.Fatal("expected logger")
	}
	logger.Close()
	logger.Finalize()

	if idx != 42 {
		t.Fatalf("idx = %d, want worker fallback index 42", idx)
	}
	if ref != "" {
		t.Fatalf("ref = %q, want empty ref without retained logs", ref)
	}
	if _, ok := testBrain.memories[histPrefix+"lists"]; ok {
		t.Fatalf("unexpected history memory stored for keep=0")
	}
	if _, ok := testBrain.memories[histLookup]; ok {
		t.Fatalf("unexpected history lookup memory stored for keep=0")
	}
}

func TestNewLoggerWithRetainedLogsStoresHistoryMemory(t *testing.T) {
	testBrain := setupHistoryBrainTest(t)

	logger, _, ref, idx := newLogger("nightly", "abc123", "main", 42, 2)
	if logger == nil {
		t.Fatal("expected logger")
	}
	logger.Close()
	logger.Finalize()

	if idx != 0 {
		t.Fatalf("idx = %d, want first retained history index 0", idx)
	}
	if ref != "abc123" {
		t.Fatalf("ref = %q, want abc123", ref)
	}

	var ph pipeHistory
	_, exists, ret := checkoutDatum(histPrefix+"nightly", &ph, false)
	if ret != robot.Ok || !exists {
		t.Fatalf("checkout retained history ret=%v exists=%t, want Ok/true", ret, exists)
	}
	if ph.NextIndex != 1 {
		t.Fatalf("NextIndex = %d, want 1", ph.NextIndex)
	}
	if len(ph.Histories) != 1 {
		t.Fatalf("len(Histories) = %d, want 1", len(ph.Histories))
	}
	if ph.Histories[0].LogIndex != 0 || ph.Histories[0].Ref != "abc123" || ph.Histories[0].Descriptor != "main" {
		data, _ := json.Marshal(ph.Histories[0])
		t.Fatalf("unexpected history entry: %s", data)
	}

	var lookup map[string]historyLookup
	_, exists, ret = checkoutDatum(histLookup, &lookup, false)
	if ret != robot.Ok || !exists {
		t.Fatalf("checkout history lookup ret=%v exists=%t, want Ok/true", ret, exists)
	}
	if got := lookup["abc123"]; got != (historyLookup{Tag: "nightly", Index: 0}) {
		t.Fatalf("lookup[abc123] = %#v, want nightly/0", got)
	}

	if _, ok := testBrain.memories[histPrefix+"nightly"]; !ok {
		t.Fatalf("expected retained history memory in brain")
	}
	if _, ok := testBrain.memories[histLookup]; !ok {
		t.Fatalf("expected history lookup memory in brain")
	}
}
