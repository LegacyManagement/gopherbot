package bot

import (
	"context"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"

	"github.com/lnxjedi/gopherbot/robot"
)

type recordingHistoryLogger struct {
	logs     []string
	lines    []string
	closeCnt int
	finalCnt int
}

func (l *recordingHistoryLogger) Log(line string) {
	l.logs = append(l.logs, line)
}

func (l *recordingHistoryLogger) Line(line string) {
	l.lines = append(l.lines, line)
}

func (l *recordingHistoryLogger) Close() {
	l.closeCnt++
}

func (l *recordingHistoryLogger) Finalize() {
	l.finalCnt++
}

func installFormatCaptureConnector(t *testing.T) *formatCaptureConnector {
	t.Helper()
	originalConnector := interfaces.Connector
	fake := &formatCaptureConnector{}
	interfaces.Connector = fake
	t.Cleanup(func() {
		interfaces.Connector = originalConnector
	})
	return fake
}

func resetActivePipelinesForTest(t *testing.T) {
	t.Helper()
	activePipelines.Lock()
	originalWorkers := activePipelines.i
	originalEIDs := activePipelines.eids
	activePipelines.i = make(map[int]*worker)
	activePipelines.eids = make(map[string]struct{})
	activePipelines.Unlock()
	t.Cleanup(func() {
		activePipelines.Lock()
		activePipelines.i = originalWorkers
		activePipelines.eids = originalEIDs
		activePipelines.Unlock()
	})
}

func TestPipelineLiveLoggerKeepsLiveBufferAfterBaseClose(t *testing.T) {
	base := &recordingHistoryLogger{}
	logger := newPipelineLiveLogger(base)

	logger.Line("before close")
	logger.Close()
	logger.Log("after close")

	data := bufferTailFromReader(logger.Snapshot(), livePipelineLogTruncated, livePipelineLogBufferSize, livePipelineLogLineSize)
	text := string(data)
	if !strings.Contains(text, "before close") {
		t.Fatalf("live snapshot missing pre-close line: %q", text)
	}
	if !strings.Contains(text, "after close") {
		t.Fatalf("live snapshot missing post-close log line: %q", text)
	}
	if len(base.lines) != 1 || base.lines[0] != "before close" {
		t.Fatalf("base lines = %#v, want only pre-close line", base.lines)
	}
	if len(base.logs) != 0 {
		t.Fatalf("base logs = %#v, want no post-close log writes", base.logs)
	}
}

func TestSerializedExternalKillWaitsForActiveAPICall(t *testing.T) {
	w := &worker{}
	if !w.beginSerializedExternalAPICall() {
		t.Fatal("beginSerializedExternalAPICall rejected call without pending kill")
	}

	done := make(chan timeoutInterruptResult, 1)
	go func() {
		done <- w.requestSerializedExternalKill()
	}()

	select {
	case result := <-done:
		t.Fatalf("serialized kill completed while API call was active: %+v", result)
	case <-time.After(50 * time.Millisecond):
	}

	w.finishSerializedExternalAPICall()

	select {
	case result := <-done:
		if !result.manual {
			t.Fatalf("serialized kill result = %+v, want manual with no process", result)
		}
	case <-time.After(time.Second):
		t.Fatal("serialized kill did not complete after API call finished")
	}
}

func TestSerializedExternalAPICallRejectsPendingKill(t *testing.T) {
	w := &worker{}
	w.Lock()
	w.externalKillPending = true
	w.Unlock()

	if w.beginSerializedExternalAPICall() {
		w.finishSerializedExternalAPICall()
		t.Fatal("beginSerializedExternalAPICall allowed call while kill was pending")
	}

	w.Lock()
	ready := w.externalKillResultReady
	result := w.externalKillResult
	w.Unlock()
	if !ready || !result.manual {
		t.Fatalf("pending kill result = ready:%t result:%+v, want ready manual", ready, result)
	}
}

func TestFormatPipelineAgeUsesCompactOperatorUnits(t *testing.T) {
	tests := []struct {
		name string
		age  time.Duration
		want string
	}{
		{name: "seconds", age: 77 * time.Second, want: "77s"},
		{name: "minutes", age: 59 * time.Minute, want: "59m"},
		{name: "hours minutes", age: 3*time.Hour + 30*time.Minute, want: "3h30m"},
		{name: "days hours", age: 2*24*time.Hour + 14*time.Hour, want: "2d14h"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := formatPipelineAge(tt.age); got != tt.want {
				t.Fatalf("formatPipelineAge(%s) = %q, want %q", tt.age, got, tt.want)
			}
		})
	}
}

func TestAdminPSAndGetPipelineLog(t *testing.T) {
	fake := installFormatCaptureConnector(t)
	resetActivePipelinesForTest(t)

	r := makeFormatTestRobot(t)
	adminWorker := getLockedWorker(r.tid)
	adminWorker.id = 1
	adminWorker.pipeName = "builtin-admin"
	adminWorker.taskName = "builtin-admin"
	adminWorker.taskType = "plugin"
	adminWorker.plugCommand = "ps"
	adminWorker.startedAt = time.Now()
	adminWorker.timeZone = time.UTC
	adminWorker.Unlock()

	live := newPipelineLiveLogger(&recordingHistoryLogger{})
	live.Line("inspect ready")
	target := &worker{
		id: 42,
		pipeContext: &pipeContext{
			pipeName:    "adminwatchdog",
			taskName:    "adminwatchdog",
			taskType:    "plugin",
			taskClass:   "Ext",
			plugCommand: "inspect",
			taskArgs:    []string{"demo"},
			startedAt:   time.Now().Add(-2 * time.Second),
			timeZone:    time.UTC,
			liveLogger:  live,
		},
	}
	target.osCmd = &exec.Cmd{Process: &os.Process{Pid: 4321}}
	jobTarget := &worker{
		User: "alice",
		id:   43,
		pipeContext: &pipeContext{
			pipeName:  "nightly",
			jobName:   "nightly",
			taskName:  "archive",
			taskType:  "task",
			taskClass: "Go",
			taskArgs:  []string{"prod"},
			startedAt: time.Now().Add(-3*time.Hour - 30*time.Minute),
			timeZone:  time.UTC,
			ptype:     scheduled,
		},
	}

	activePipelines.Lock()
	activePipelines.i[1] = adminWorker
	activePipelines.i[42] = target
	activePipelines.i[43] = jobTarget
	activePipelines.Unlock()

	admin(r, "ps")
	if !strings.Contains(fake.lastMessage, "private context") {
		t.Fatalf("visible ps did not require private context: %q", fake.lastMessage)
	}

	r.Incoming.HiddenMessage = true
	admin(r, "ps")
	if fake.lastFormat != robot.Fixed {
		t.Fatalf("ps format = %v, want %v", fake.lastFormat, robot.Fixed)
	}
	if !strings.Contains(fake.lastMessage, "Plugins\nWID") {
		t.Fatalf("ps output missing non-verbose header: %q", fake.lastMessage)
	}
	if !strings.Contains(fake.lastMessage, "adminwatchdog") {
		t.Fatalf("ps output missing pipeline row: %q", fake.lastMessage)
	}
	if !strings.Contains(fake.lastMessage, "Jobs\nWID") || !strings.Contains(fake.lastMessage, "nightly") || !strings.Contains(fake.lastMessage, "sched") {
		t.Fatalf("ps output missing job section: %q", fake.lastMessage)
	}
	if strings.Contains(fake.lastMessage, "4321") {
		t.Fatalf("non-verbose ps unexpectedly exposed pid: %q", fake.lastMessage)
	}
	if !strings.Contains(fake.lastMessage, "(use 'ps -v' for more verbose output)") {
		t.Fatalf("ps output missing verbose hint: %q", fake.lastMessage)
	}

	admin(r, "ps", "-v")
	if !strings.Contains(fake.lastMessage, "OSPID") {
		t.Fatalf("verbose ps output missing PID header: %q", fake.lastMessage)
	}
	if !strings.Contains(fake.lastMessage, "4321") {
		t.Fatalf("verbose ps output missing pid: %q", fake.lastMessage)
	}

	admin(r, "getpipelinelog", "42")
	if fake.lastFormat != robot.Fixed {
		t.Fatalf("get-pipeline-log format = %v, want %v", fake.lastFormat, robot.Fixed)
	}
	if !strings.Contains(fake.lastMessage, "Live log for pipeline 42:") {
		t.Fatalf("get-pipeline-log missing heading: %q", fake.lastMessage)
	}
	if !strings.Contains(fake.lastMessage, "inspect ready") {
		t.Fatalf("get-pipeline-log missing buffered content: %q", fake.lastMessage)
	}
}

func TestEmitPipelineTimeOutKillSendsManualInterventionAlertForInProcessWork(t *testing.T) {
	fake := installFormatCaptureConnector(t)

	w := &worker{
		Channel:  "general",
		Protocol: robot.Test,
		Incoming: &robot.ConnectorMessage{Protocol: "test", ChannelName: "general"},
		cfg:      &configuration{defaultMessageFormat: robot.Raw},
		pipeContext: &pipeContext{
			active:          true,
			pipeName:        "go-watchdog",
			taskName:        "go-watchdog",
			taskType:        "plugin",
			taskClass:       "Go",
			plugCommand:     "slow",
			startedAt:       time.Now().Add(-time.Second),
			timeZone:        time.UTC,
			operatorChannel: "general",
			liveLogger:      newPipelineLiveLogger(&recordingHistoryLogger{}),
		},
	}

	w.watchdogGeneration = 1
	w.watchdogPhase = watchdogPhasePrimary
	w.emitPipelineTimeOutKill(watchdogPhasePrimary, 1)

	if !w.timeOutKillSent {
		t.Fatalf("expected kill-threshold marker to be recorded")
	}
	if !w.primaryKillSent {
		t.Fatalf("expected primary kill-threshold marker to be recorded")
	}
	if !w.timeOutKillManual {
		t.Fatalf("expected manual intervention flag for in-process work")
	}
	if !strings.Contains(fake.lastMessage, "manual intervention is required") {
		t.Fatalf("manual intervention alert missing guidance: %q", fake.lastMessage)
	}
}

func TestCleanupTimeoutCanFireAfterPrimaryTimeout(t *testing.T) {
	fake := installFormatCaptureConnector(t)

	w := &worker{
		Channel:  "general",
		Protocol: robot.Test,
		Incoming: &robot.ConnectorMessage{Protocol: "test", ChannelName: "general"},
		cfg:      &configuration{defaultMessageFormat: robot.Raw},
		pipeContext: &pipeContext{
			active:          true,
			pipeName:        "cleanup-watchdog",
			taskName:        "cleanup-task",
			taskType:        "task",
			taskClass:       "Go",
			startedAt:       time.Now().Add(-time.Minute),
			timeZone:        time.UTC,
			operatorChannel: "general",
			liveLogger:      newPipelineLiveLogger(&recordingHistoryLogger{}),
			primaryKillSent: true,
			timeOutKillSent: true,
		},
	}
	w.watchdogGeneration = 3
	w.watchdogPhase = watchdogPhaseCleanup

	w.emitPipelineTimeOutKill(watchdogPhaseCleanup, 3)

	if !w.cleanupKillSent {
		t.Fatalf("expected cleanup kill-threshold marker to be recorded")
	}
	if !strings.Contains(fake.lastMessage, "Pipeline cleanup timeout kill threshold reached") {
		t.Fatalf("cleanup timeout alert missing cleanup title: %q", fake.lastMessage)
	}
}

func TestTimeoutCancelsQueuedExclusiveWaiter(t *testing.T) {
	resetRunQueuesForTest(t)

	wakeUp := make(chan struct{}, 1)
	waitCtx, waitCancel := context.WithCancel(context.Background())
	defer waitCancel()
	tag := "job-ns:configrepo"
	runQueues.Lock()
	runQueues.m[tag] = []chan struct{}{wakeUp}
	runQueues.Unlock()

	w := &worker{
		pipeContext: &pipeContext{
			active:              true,
			watchdogGeneration:  9,
			watchdogPhase:       watchdogPhasePrimary,
			exclusiveWaitTag:    tag,
			exclusiveWaitCh:     wakeUp,
			exclusiveWaitCancel: waitCancel,
		},
	}

	result := interruptPipelineForTimeOut(w, watchdogPhasePrimary, 9)
	if !result.queued {
		t.Fatalf("interruptPipelineForTimeOut queued = false, want true")
	}
	select {
	case <-waitCtx.Done():
	case <-time.After(time.Second):
		t.Fatal("queued Exclusive waiter was not canceled")
	}
	if !w.exclusiveWaitAbort {
		t.Fatal("queued Exclusive waiter was not marked aborted")
	}
	runQueues.Lock()
	queueLen := len(runQueues.m[tag])
	runQueues.Unlock()
	if queueLen != 0 {
		t.Fatalf("exclusive queue length = %d, want 0", queueLen)
	}
}

func TestStaleTimeoutGenerationDoesNotInterruptCurrentPhase(t *testing.T) {
	waitCtx, waitCancel := context.WithCancel(context.Background())
	defer waitCancel()

	w := &worker{
		pipeContext: &pipeContext{
			active:              true,
			watchdogGeneration:  4,
			watchdogPhase:       watchdogPhaseCleanup,
			exclusiveWaitTag:    "job-ns:configrepo",
			exclusiveWaitCh:     make(chan struct{}, 1),
			exclusiveWaitCancel: waitCancel,
		},
	}

	result := interruptPipelineForTimeOut(w, watchdogPhasePrimary, 3)
	if !result.stale {
		t.Fatalf("interruptPipelineForTimeOut stale = false, want true")
	}
	select {
	case <-waitCtx.Done():
		t.Fatal("stale primary timeout canceled current cleanup wait")
	default:
	}
	if w.exclusiveWaitAbort {
		t.Fatal("stale primary timeout marked current cleanup wait aborted")
	}
}

func TestCallTaskCompiledGoPanicWritesStackToLiveLog(t *testing.T) {
	const pluginName = "panic-test-plugin"

	originalHandler, hadHandler := pluginHandlers[pluginName]
	pluginHandlers[pluginName] = robot.PluginHandler{
		Handler: func(robot.Robot, string, ...string) robot.TaskRetVal {
			panic("boom")
		},
	}
	t.Cleanup(func() {
		if hadHandler {
			pluginHandlers[pluginName] = originalHandler
		} else {
			delete(pluginHandlers, pluginName)
		}
	})

	live := newPipelineLiveLogger(&recordingHistoryLogger{})
	w := &worker{
		User:       "alice",
		Channel:    "general",
		Protocol:   robot.Test,
		Incoming:   &robot.ConnectorMessage{Protocol: "test", ChannelName: "general"},
		cfg:        &configuration{alias: '!', botinfo: UserInfo{UserName: "Clu"}, defaultMessageFormat: robot.Raw},
		tasks:      &taskList{t: []interface{}{&Task{name: "namespace"}}, nameMap: map[string]int{}, nameSpaces: map[string]ParameterSet{}, parameterSets: map[string]ParameterSet{}},
		listedUser: true,
		pipeContext: &pipeContext{
			parameters:  map[string]string{"GOPHER_CMDMODE": "alias"},
			environment: map[string]string{},
			logger:      live,
			liveLogger:  live,
			pipeName:    pluginName,
			taskName:    pluginName,
			taskType:    "plugin",
			taskClass:   "Go",
		},
	}

	task := &Plugin{
		Task: &Task{
			name:     pluginName,
			taskType: taskGo,
		},
	}

	errString, ret := w.callTask(task, "explode")
	if ret != robot.MechanismFail {
		t.Fatalf("panic plugin ret = %v, want %v", ret, robot.MechanismFail)
	}
	if !strings.Contains(errString, "recovered from panic in callTask") {
		t.Fatalf("panic plugin errString = %q", errString)
	}

	snapshot := w.liveLogSnapshot()
	if !strings.Contains(snapshot, "boom") {
		t.Fatalf("live log missing panic summary: %q", snapshot)
	}
	if !strings.Contains(snapshot, "stack:") {
		t.Fatalf("live log missing stack trace lines: %q", snapshot)
	}
}
