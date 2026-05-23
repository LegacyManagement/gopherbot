package bot

import (
	"testing"
	"time"

	"github.com/lnxjedi/gopherbot/robot"
)

type promptTestResult struct {
	reply string
	ret   robot.RetVal
}

func TestPromptTimeoutForContext(t *testing.T) {
	tests := []struct {
		name     string
		protocol string
		task     *Task
		want     time.Duration
	}{
		{
			name:     "default timeout for non-interactive protocol",
			protocol: "slack",
			task:     &Task{taskType: taskGo},
			want:     replyTimeout,
		},
		{
			name:     "default timeout for ssh non-interpreter task",
			protocol: "ssh",
			task:     &Task{taskType: taskExternal, Path: "plugin.py"},
			want:     replyTimeout,
		},
		{
			name:     "extended timeout for ssh compiled Go task",
			protocol: "ssh",
			task:     &Task{taskType: taskGo},
			want:     interactivePromptTimeout,
		},
		{
			name:     "extended timeout for terminal external lua task",
			protocol: "terminal",
			task:     &Task{taskType: taskExternal, Path: "plugin.lua"},
			want:     interactivePromptTimeout,
		},
		{
			name:     "extension check is case-insensitive",
			protocol: "terminal",
			task:     &Task{taskType: taskExternal, Path: "plugin.JS"},
			want:     interactivePromptTimeout,
		},
		{
			name:     "extended timeout for terminal gsh task",
			protocol: "terminal",
			task:     &Task{taskType: taskExternal, Path: "plugin.gsh"},
			want:     interactivePromptTimeout,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			r := Robot{
				Message: &robot.Message{
					Protocol: getProtocol(tc.protocol),
					Incoming: &robot.ConnectorMessage{Protocol: tc.protocol},
				},
				pipeContext: &pipeContext{currentTask: tc.task},
			}
			got := promptTimeoutForContext(r, tc.task)
			if got != tc.want {
				t.Fatalf("promptTimeoutForContext() = %s, want %s", got, tc.want)
			}
		})
	}
}

func TestPromptInternalInterruptedOnShutdownSignal(t *testing.T) {
	prevConnector := interfaces.Connector
	interfaces.Connector = &fakeRuntimeConnector{}
	t.Cleanup(func() {
		interfaces.Connector = prevConnector
	})

	replies.Lock()
	replies.m = make(map[replyMatcher][]replyWaiter)
	replies.Unlock()
	t.Cleanup(func() {
		replies.Lock()
		replies.m = make(map[replyMatcher][]replyWaiter)
		replies.Unlock()
	})

	resetPromptShutdownSignal()
	t.Cleanup(resetPromptShutdownSignal)

	task := &Task{name: "test-prompt", taskType: taskGo}
	r := Robot{
		Message: &robot.Message{
			User:     "alice",
			Channel:  "",
			Protocol: robot.SSH,
			Incoming: &robot.ConnectorMessage{Protocol: "ssh"},
		},
		maps:        &userChanMaps{},
		pipeContext: &pipeContext{currentTask: task},
	}

	type promptResult struct {
		reply string
		ret   robot.RetVal
	}
	resCh := make(chan promptResult, 1)
	go func() {
		rep, ret := r.promptInternal("YesNo", "alice", "", "", "Proceed?")
		resCh <- promptResult{reply: rep, ret: ret}
	}()

	m := replyMatcher{protocol: "ssh", user: "alice", channel: "", thread: ""}
	waitFor(t, "prompt waiter registered", func() bool {
		replies.Lock()
		_, ok := replies.m[m]
		replies.Unlock()
		return ok
	})

	triggerPromptShutdownSignal()

	select {
	case res := <-resCh:
		if res.ret != robot.Interrupted {
			t.Fatalf("promptInternal() ret = %s, want %s", res.ret, robot.Interrupted)
		}
		if res.reply != "" {
			t.Fatalf("promptInternal() reply = %q, want empty", res.reply)
		}
	case <-time.After(1 * time.Second):
		t.Fatal("timed out waiting for promptInternal to return after shutdown signal")
	}

	waitFor(t, "prompt waiter removed", func() bool {
		replies.Lock()
		_, ok := replies.m[m]
		replies.Unlock()
		return !ok
	})
}

func TestPromptInternalMatcherIsProtocolScoped(t *testing.T) {
	prevConnector := interfaces.Connector
	interfaces.Connector = &fakeRuntimeConnector{}
	t.Cleanup(func() {
		interfaces.Connector = prevConnector
	})

	replies.Lock()
	replies.m = make(map[replyMatcher][]replyWaiter)
	replies.Unlock()
	t.Cleanup(func() {
		replies.Lock()
		replies.m = make(map[replyMatcher][]replyWaiter)
		replies.Unlock()
	})

	resetPromptShutdownSignal()
	t.Cleanup(resetPromptShutdownSignal)

	task := &Task{name: "test-protocol-scope", taskType: taskGo}
	r := Robot{
		Message: &robot.Message{
			User:     "alice",
			Channel:  "",
			Protocol: robot.SSH,
			Incoming: &robot.ConnectorMessage{Protocol: "ssh"},
		},
		maps:        &userChanMaps{},
		pipeContext: &pipeContext{currentTask: task},
	}

	resCh := make(chan robot.RetVal, 1)
	go func() {
		_, ret := r.promptInternal("YesNo", "alice", "", "", "Proceed?")
		resCh <- ret
	}()

	sshMatcher := replyMatcher{protocol: "ssh", user: "alice", channel: "", thread: ""}
	waitFor(t, "ssh prompt waiter registered", func() bool {
		replies.Lock()
		_, ok := replies.m[sshMatcher]
		replies.Unlock()
		return ok
	})

	replies.Lock()
	_, slackFound := replies.m[replyMatcher{protocol: "slack", user: "alice", channel: "", thread: ""}]
	replies.Unlock()
	if slackFound {
		t.Fatal("reply waiter unexpectedly matched slack protocol key")
	}

	triggerPromptShutdownSignal()
	select {
	case ret := <-resCh:
		if ret != robot.Interrupted {
			t.Fatalf("promptInternal() ret = %s, want %s", ret, robot.Interrupted)
		}
	case <-time.After(1 * time.Second):
		t.Fatal("timed out waiting for promptInternal to return after shutdown signal")
	}
}

func TestPromptInternalUsesProtocolScopedUserResolution(t *testing.T) {
	fc := &fakeRuntimeConnector{}
	prevConnector := interfaces.Connector
	interfaces.Connector = fc
	t.Cleanup(func() {
		interfaces.Connector = prevConnector
	})

	replies.Lock()
	replies.m = make(map[replyMatcher][]replyWaiter)
	replies.Unlock()
	t.Cleanup(func() {
		replies.Lock()
		replies.m = make(map[replyMatcher][]replyWaiter)
		replies.Unlock()
	})

	resetPromptShutdownSignal()
	t.Cleanup(resetPromptShutdownSignal)

	task := &Task{name: "test-protocol-user-resolution", taskType: taskGo}
	r := Robot{
		Message: &robot.Message{
			User:     "alice",
			Channel:  "",
			Protocol: robot.Slack,
			Incoming: &robot.ConnectorMessage{Protocol: "slack"},
		},
		maps: &userChanMaps{
			user: map[string]*DirectoryUser{
				"alice": {UserName: "alice"},
			},
			userProto: map[string]map[string]*UserInfo{
				"slack": {
					"alice": {UserName: "alice", UserID: "U123SLACK"},
				},
			},
		},
		pipeContext: &pipeContext{currentTask: task},
	}

	resCh := make(chan robot.RetVal, 1)
	go func() {
		_, ret := r.promptInternal("YesNo", "alice", "", "", "Proceed?")
		resCh <- ret
	}()

	waitFor(t, "prompt sent with protocol-specific user id", func() bool {
		fc.mu.Lock()
		defer fc.mu.Unlock()
		return fc.userCalls > 0 && fc.lastUser == "<U123SLACK>"
	})

	triggerPromptShutdownSignal()
	select {
	case ret := <-resCh:
		if ret != robot.Interrupted {
			t.Fatalf("promptInternal() ret = %s, want %s", ret, robot.Interrupted)
		}
	case <-time.After(1 * time.Second):
		t.Fatal("timed out waiting for promptInternal to return after shutdown signal")
	}
}

func TestInterruptReplyWaitersForTask(t *testing.T) {
	replies.Lock()
	replies.m = make(map[replyMatcher][]replyWaiter)
	replies.Unlock()
	t.Cleanup(func() {
		replies.Lock()
		replies.m = make(map[replyMatcher][]replyWaiter)
		replies.Unlock()
	})

	matcher := replyMatcher{protocol: "ssh", user: "alice", channel: "general", thread: ""}
	firstCh := make(chan reply, 1)
	secondCh := make(chan reply, 1)
	replies.Lock()
	replies.m[matcher] = []replyWaiter{
		{re: stockReplies["YesNo"], replyChannel: firstCh, tid: 101},
		{re: stockReplies["YesNo"], replyChannel: secondCh, tid: 202},
	}
	replies.Unlock()

	if !interruptReplyWaitersForTask(101) {
		t.Fatal("interruptReplyWaitersForTask(101) = false, want true")
	}

	select {
	case got := <-firstCh:
		if got.disposition != replyInterrupted {
			t.Fatalf("first waiter disposition = %v, want %v", got.disposition, replyInterrupted)
		}
	default:
		t.Fatal("first waiter did not receive interruption reply")
	}

	select {
	case got := <-secondCh:
		if got.disposition != retryPrompt {
			t.Fatalf("second waiter disposition = %v, want %v", got.disposition, retryPrompt)
		}
	default:
		t.Fatal("second waiter did not receive retry prompt")
	}

	replies.Lock()
	_, exists := replies.m[matcher]
	replies.Unlock()
	if exists {
		t.Fatal("matcher should be removed after targeted interruption")
	}
}

func TestPromptForReplyRetriesQueuedPromptAfterContention(t *testing.T) {
	fc := &fakeRuntimeConnector{}
	prevConnector := interfaces.Connector
	interfaces.Connector = fc
	t.Cleanup(func() {
		interfaces.Connector = prevConnector
	})

	replies.Lock()
	replies.m = make(map[replyMatcher][]replyWaiter)
	replies.Unlock()
	t.Cleanup(func() {
		replies.Lock()
		replies.m = make(map[replyMatcher][]replyWaiter)
		replies.Unlock()
	})

	resetPromptShutdownSignal()
	t.Cleanup(resetPromptShutdownSignal)

	task := &Task{name: "test-prompt-contention", taskType: taskGo}
	makePromptRobot := func(tid int) Robot {
		return Robot{
			tid: tid,
			Message: &robot.Message{
				User:     "alice",
				Channel:  "general",
				Protocol: robot.Test,
				Incoming: &robot.ConnectorMessage{Protocol: "test"},
			},
			pipeContext: &pipeContext{currentTask: task},
		}
	}

	firstCh := make(chan promptTestResult, 1)
	secondCh := make(chan promptTestResult, 1)
	first := makePromptRobot(101)
	second := makePromptRobot(202)
	matcher := replyMatcher{protocol: "test", user: "alice", channel: "general", thread: ""}

	go func() {
		reply, ret := first.PromptForReply("YesNo", "First prompt?")
		firstCh <- promptTestResult{reply: reply, ret: ret}
	}()
	waitForPromptWaiters(t, matcher, 1)
	waitForSentPrompt(t, fc, 1, "First prompt?")

	go func() {
		reply, ret := second.PromptForReply("YesNo", "Second prompt?")
		secondCh <- promptTestResult{reply: reply, ret: ret}
	}()
	waitForPromptWaiters(t, matcher, 2)
	waitForSentPrompt(t, fc, 1, "First prompt?")

	if got := deliverPromptReply(t, matcher, "yes"); got != 2 {
		t.Fatalf("delivered reply to %d waiter(s), want 2", got)
	}
	assertPromptResult(t, "first prompt", firstCh, "yes", robot.Ok)

	waitForPromptWaiters(t, matcher, 1)
	waitForSentPrompt(t, fc, 2, "Second prompt?")
	if got := deliverPromptReply(t, matcher, "yes"); got != 1 {
		t.Fatalf("delivered reply to %d waiter(s), want 1", got)
	}
	assertPromptResult(t, "second prompt", secondCh, "yes", robot.Ok)
}

func waitForPromptWaiters(t *testing.T, matcher replyMatcher, want int) {
	t.Helper()
	waitFor(t, "prompt waiters", func() bool {
		replies.Lock()
		got := len(replies.m[matcher])
		replies.Unlock()
		return got == want
	})
}

func waitForSentPrompt(t *testing.T, fc *fakeRuntimeConnector, wantCalls int, wantMessage string) {
	t.Helper()
	waitFor(t, "sent prompt", func() bool {
		fc.mu.Lock()
		defer fc.mu.Unlock()
		return fc.userChannelCalls == wantCalls && fc.lastMessage == wantMessage
	})
}

func deliverPromptReply(t *testing.T, matcher replyMatcher, text string) int {
	t.Helper()
	replies.Lock()
	waiters, ok := replies.m[matcher]
	if ok {
		delete(replies.m, matcher)
	}
	replies.Unlock()
	if !ok {
		t.Fatalf("no waiters registered for matcher %#v", matcher)
	}
	for i, rep := range waiters {
		if i == 0 {
			rep.replyChannel <- reply{matched: rep.re.MatchString(text), disposition: replied, rep: text}
			continue
		}
		rep.replyChannel <- reply{matched: false, disposition: retryPrompt}
	}
	return len(waiters)
}

func assertPromptResult(t *testing.T, desc string, ch <-chan promptTestResult, wantReply string, wantRet robot.RetVal) {
	t.Helper()
	select {
	case got := <-ch:
		if got.ret != wantRet {
			t.Fatalf("%s ret = %s, want %s", desc, got.ret, wantRet)
		}
		if got.reply != wantReply {
			t.Fatalf("%s reply = %q, want %q", desc, got.reply, wantReply)
		}
	case <-time.After(1 * time.Second):
		t.Fatalf("timed out waiting for %s result", desc)
	}
}

func TestAnyStringStockReplyMatchesPastedSecrets(t *testing.T) {
	re := stockReplies["AnyString"]
	if re == nil {
		t.Fatal("stockReplies[AnyString] is nil")
	}
	for _, input := range []string{
		"Forky-EAT:Lunker@SmashedBUMBLETS",
		"token-with:punctuation@and/slashes",
	} {
		if !re.MatchString(input) {
			t.Fatalf("AnyString did not match %q", input)
		}
	}
}
