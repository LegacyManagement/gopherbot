package bot

import "testing"

func TestExternalAPICallerIDRefreshSurvivesPriorTaskCleanup(t *testing.T) {
	w := &worker{
		id:          getWorkerID(),
		pipeContext: &pipeContext{},
	}
	w.registerActive(nil)
	eid := w.eid

	var r1, r2 Robot
	t.Cleanup(func() {
		deregisterExternalAPICaller(eid)
		if r1.tid != 0 {
			deregisterWorker(r1.tid)
		}
		if r2.tid != 0 {
			deregisterWorker(r2.tid)
		}
		activePipelines.Lock()
		delete(activePipelines.i, w.id)
		delete(activePipelines.eids, eid)
		activePipelines.Unlock()
	})

	r1 = w.makeRobot()
	w.registerWorker(r1.tid)
	registerExternalAPIRobot(eid, r1)

	r2 = w.makeRobot()
	w.registerWorker(r2.tid)
	registerExternalAPIRobot(eid, r2)

	// Simulate the old task goroutine cleaning up after the next task has
	// already refreshed the pipeline-scoped external API context.
	deregisterWorker(r1.tid)

	taskLookup.RLock()
	got, ok := taskLookup.e[eid]
	taskLookup.RUnlock()
	if !ok {
		t.Fatalf("caller ID %q was removed by prior task cleanup", eid)
	}
	if got.tid != r2.tid {
		t.Fatalf("caller ID mapped to tid %d, want current task tid %d", got.tid, r2.tid)
	}
	if gotWorker := workerForRobotAPI(got); gotWorker != w {
		t.Fatalf("caller ID mapped to worker %p, want %p", gotWorker, w)
	}

	w.deregister()

	taskLookup.RLock()
	_, ok = taskLookup.e[eid]
	taskLookup.RUnlock()
	if ok {
		t.Fatalf("caller ID %q remained after pipeline deregister", eid)
	}
}
