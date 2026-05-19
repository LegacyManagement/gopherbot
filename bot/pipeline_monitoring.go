package bot

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"strings"
	"sync"
	"time"

	"github.com/lnxjedi/gopherbot/robot"
	"github.com/lnxjedi/gopherbot/v2/modules/linebuffer"
	"golang.org/x/sys/unix"
)

const (
	livePipelineLogBufferSize = 64 * 1024
	livePipelineLogLineSize   = 2048
	livePipelineLogTruncated  = "<... truncated>"
)

type pipelineWatchdogPhase string

const (
	watchdogPhaseNone    pipelineWatchdogPhase = ""
	watchdogPhasePrimary pipelineWatchdogPhase = "primary"
	watchdogPhaseCleanup pipelineWatchdogPhase = "cleanup"
)

type pipelineLiveLogger struct {
	base       robot.HistoryLogger
	live       *linebuffer.Buffer
	mu         sync.Mutex
	baseClosed bool
}

func newPipelineLiveLogger(base robot.HistoryLogger) *pipelineLiveLogger {
	return &pipelineLiveLogger{
		base: base,
		live: linebuffer.New(livePipelineLogBufferSize, livePipelineLogLineSize, livePipelineLogTruncated),
	}
}

func (l *pipelineLiveLogger) Log(line string) {
	tsLine := fmt.Sprintf("%s %s", time.Now().Format("Jan 2 15:04:05"), line)
	l.live.WriteLine(tsLine)
	l.mu.Lock()
	defer l.mu.Unlock()
	if !l.baseClosed {
		l.base.Log(line)
	}
}

func (l *pipelineLiveLogger) Line(line string) {
	l.live.WriteLine(line)
	l.mu.Lock()
	defer l.mu.Unlock()
	if !l.baseClosed {
		l.base.Line(line)
	}
}

func (l *pipelineLiveLogger) Close() {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.baseClosed {
		return
	}
	l.baseClosed = true
	l.base.Close()
}

func (l *pipelineLiveLogger) Finalize() {
	l.live.Close()
	l.mu.Lock()
	baseClosed := l.baseClosed
	if !l.baseClosed {
		l.baseClosed = true
	}
	l.mu.Unlock()
	if !baseClosed {
		l.base.Close()
	}
	l.base.Finalize()
}

func (l *pipelineLiveLogger) Snapshot() io.Reader {
	return l.live.Snapshot()
}

func determinePipelineOperatorChannel(cfg *configuration, task *Task, isJob bool) string {
	if cfg == nil {
		return ""
	}
	if isJob {
		return strings.TrimSpace(task.Channel)
	}
	return strings.TrimSpace(cfg.defaultJobChannel)
}

func formatPipelineClock(ts time.Time, loc *time.Location) string {
	if ts.IsZero() {
		return "unknown"
	}
	if loc != nil {
		ts = ts.In(loc)
	}
	return ts.Format("Jan 2 15:04:05")
}

func formatPipelineAge(d time.Duration) string {
	if d < 0 {
		d = 0
	}
	seconds := int64(d.Round(time.Second) / time.Second)
	if seconds < 0 {
		seconds = 0
	}
	if seconds < 120 {
		return fmt.Sprintf("%ds", seconds)
	}
	minutes := seconds / 60
	if minutes < 60 {
		return fmt.Sprintf("%dm", minutes)
	}
	hours := minutes / 60
	if hours < 24 {
		return fmt.Sprintf("%dh%dm", hours, minutes%60)
	}
	days := hours / 24
	return fmt.Sprintf("%dd%dh", days, hours%24)
}

func bufferTailFromReader(logReader io.Reader, trunc string, buffsize, linesize int) []byte {
	tail := linebuffer.New(buffsize, linesize, trunc)
	scanner := bufio.NewScanner(logReader)
	for scanner.Scan() {
		tail.WriteLine(scanner.Text())
	}
	tail.Close()
	tailReader, _ := tail.Reader()
	buff, _ := io.ReadAll(tailReader)
	return buff
}

func (w *worker) liveLogBuffer() *pipelineLiveLogger {
	w.Lock()
	defer w.Unlock()
	return w.liveLogger
}

func (w *worker) liveLogSnapshot() string {
	logger := w.liveLogBuffer()
	if logger == nil {
		return ""
	}
	data, _ := io.ReadAll(logger.Snapshot())
	return strings.TrimSpace(string(data))
}

func (w *worker) liveLogExcerpt() string {
	logger := w.liveLogBuffer()
	if logger == nil {
		return ""
	}
	data := bufferTailFromReader(logger.Snapshot(), " ...", tailBody, tailLine)
	return strings.TrimSpace(string(data))
}

func (w *worker) startPipelineWatchdog(phase pipelineWatchdogPhase, base time.Time) {
	w.Lock()
	timeOuts := w.timeOuts
	if base.IsZero() {
		base = time.Now()
	}
	if !timeOuts.any() {
		w.Unlock()
		return
	}
	ctx, cancel := context.WithCancel(context.Background())
	w.watchdogGeneration++
	generation := w.watchdogGeneration
	w.watchdogPhase = phase
	w.watchdogCancel = cancel
	w.Unlock()

	schedule := func(delay time.Duration, fn func()) {
		if delay <= 0 {
			go fn()
			return
		}
		go func() {
			timer := time.NewTimer(delay)
			defer timer.Stop()
			select {
			case <-ctx.Done():
				return
			case <-timer.C:
				fn()
			}
		}()
	}

	if timeOuts.Warn > 0 {
		schedule(time.Until(base.Add(timeOuts.Warn)), func() {
			w.emitPipelineTimeOutWarn(phase, generation)
		})
	}
	if timeOuts.Kill > 0 {
		schedule(time.Until(base.Add(timeOuts.Kill)), func() {
			w.emitPipelineTimeOutKill(phase, generation)
		})
	}
}

func (w *worker) stopPipelineWatchdog() {
	w.Lock()
	cancel := w.watchdogCancel
	w.watchdogCancel = nil
	w.watchdogGeneration++
	w.watchdogPhase = watchdogPhaseNone
	w.Unlock()
	if cancel != nil {
		cancel()
	}
}

type timeoutInterruptResult struct {
	killed bool
	pid    int
	err    error
	manual bool
	queued bool
	stale  bool
}

func removeExclusiveWaiter(tag string, wakeUp chan struct{}) {
	if tag == "" || wakeUp == nil {
		return
	}
	runQueues.Lock()
	queue, exists := runQueues.m[tag]
	if exists {
		for i, queued := range queue {
			if queued == wakeUp {
				queue = append(queue[:i], queue[i+1:]...)
				runQueues.m[tag] = queue
				break
			}
		}
	}
	runQueues.Unlock()
}

func interruptPipelineForTimeOut(worker *worker, phase pipelineWatchdogPhase, generation uint64) timeoutInterruptResult {
	var pid int
	var activeTaskTID int
	var rpcCancel context.CancelFunc
	var exclusiveWaitCancel context.CancelFunc
	var exclusiveWaitTag string
	var exclusiveWaitCh chan struct{}
	worker.Lock()
	if !worker.active || worker.watchdogGeneration != generation || worker.watchdogPhase != phase {
		worker.Unlock()
		return timeoutInterruptResult{stale: true}
	}
	if worker.osCmd != nil {
		pid = worker.osCmd.Process.Pid
	}
	activeTaskTID = worker.activeTaskTID
	rpcCancel = worker.rpcCancel
	exclusiveWaitCancel = worker.exclusiveWaitCancel
	exclusiveWaitTag = worker.exclusiveWaitTag
	exclusiveWaitCh = worker.exclusiveWaitCh
	if exclusiveWaitCancel != nil {
		worker.exclusiveWaitAbort = true
	}
	worker.Unlock()

	if rpcCancel != nil {
		rpcCancel()
	}
	_ = interruptReplyWaitersForTask(activeTaskTID)
	if exclusiveWaitCancel != nil {
		removeExclusiveWaiter(exclusiveWaitTag, exclusiveWaitCh)
		exclusiveWaitCancel()
		return timeoutInterruptResult{queued: true}
	}

	if pid == 0 {
		return timeoutInterruptResult{manual: true}
	}
	if err := unix.Kill(-pid, unix.SIGKILL); err != nil && err != unix.ESRCH {
		return timeoutInterruptResult{pid: pid, err: err}
	}
	return timeoutInterruptResult{killed: true, pid: pid}
}

func (w *worker) beginPipelineTimeOutWarn(phase pipelineWatchdogPhase, generation uint64) (*pipelineLiveLogger, bool) {
	w.Lock()
	defer w.Unlock()
	if !w.active || w.watchdogGeneration != generation || w.watchdogPhase != phase {
		return nil, false
	}
	switch phase {
	case watchdogPhasePrimary:
		if w.primaryWarnSent {
			return nil, false
		}
		w.primaryWarnSent = true
	case watchdogPhaseCleanup:
		if w.cleanupWarnSent {
			return nil, false
		}
		w.cleanupWarnSent = true
	default:
		return nil, false
	}
	w.timeOutWarnSent = true
	return w.liveLogger, true
}

func (w *worker) beginPipelineTimeOutKill(phase pipelineWatchdogPhase, generation uint64) (*pipelineLiveLogger, bool) {
	w.Lock()
	defer w.Unlock()
	if !w.active || w.watchdogGeneration != generation || w.watchdogPhase != phase {
		return nil, false
	}
	switch phase {
	case watchdogPhasePrimary:
		if w.primaryKillSent {
			return nil, false
		}
		w.primaryKillSent = true
	case watchdogPhaseCleanup:
		if w.cleanupKillSent {
			return nil, false
		}
		w.cleanupKillSent = true
	default:
		return nil, false
	}
	w.timeOutKillSent = true
	return w.liveLogger, true
}

func (w *worker) recordPipelineTimeOutKillResult(phase pipelineWatchdogPhase, result timeoutInterruptResult) {
	w.Lock()
	defer w.Unlock()
	switch phase {
	case watchdogPhasePrimary:
		w.primaryKillManual = result.manual
	case watchdogPhaseCleanup:
		w.cleanupKillManual = result.manual
	}
	w.timeOutKillManual = result.manual
}

func (w *worker) clearStalePipelineTimeOutKill(phase pipelineWatchdogPhase) {
	w.Lock()
	defer w.Unlock()
	switch phase {
	case watchdogPhasePrimary:
		w.primaryKillSent = false
	case watchdogPhaseCleanup:
		w.cleanupKillSent = false
	}
	w.timeOutKillSent = w.primaryKillSent || w.cleanupKillSent
}

func timeoutAlertPrefix(phase pipelineWatchdogPhase) string {
	if phase == watchdogPhaseCleanup {
		return "Pipeline cleanup"
	}
	return "Pipeline"
}

func (w *worker) formatPipelineAlert(title string, extra ...string) string {
	w.Lock()
	startedAt := w.startedAt
	timeZone := w.timeZone
	pipeName := w.pipeName
	taskName := w.taskName
	taskType := w.taskType
	command := w.plugCommand
	args := append([]string(nil), w.taskArgs...)
	wid := w.id
	w.Unlock()

	lines := []string{title}
	lines = append(lines, fmt.Sprintf("WID: `%d`", wid))
	lines = append(lines, fmt.Sprintf("Pipeline: `%s`", pipeName))
	if taskName != "" {
		current := taskName
		if taskType == "plugin" && command != "" {
			current = fmt.Sprintf("%s/%s", taskName, command)
		}
		if len(args) > 0 {
			current += " " + strings.Join(args, " ")
		}
		lines = append(lines, fmt.Sprintf("Current task: `%s`", current))
	}
	lines = append(lines, fmt.Sprintf("Started: `%s`", formatPipelineClock(startedAt, timeZone)))
	lines = append(lines, fmt.Sprintf("Age: `%s`", formatPipelineAge(time.Since(startedAt))))
	lines = append(lines, extra...)
	excerpt := w.liveLogExcerpt()
	if excerpt != "" {
		lines = append(lines, "Recent log:")
		lines = append(lines, "```")
		lines = append(lines, excerpt)
		lines = append(lines, "```")
	}
	return strings.Join(lines, "\n")
}

func (w *worker) sendPipelineAlert(message string) {
	w.Lock()
	channel := strings.TrimSpace(w.operatorChannel)
	w.Unlock()
	if channel == "" {
		Log(robot.Warn, "No operator channel configured for pipeline '%s'; skipping admin alert", w.pipeName)
		return
	}
	r := w.makeRobot()
	if ret := r.MessageFormat(robot.BasicMarkdown).SendChannelMessage(channel, message); ret != robot.Ok {
		Log(robot.Warn, "Unable to send pipeline alert for '%s' to channel '%s': %s", w.pipeName, channel, ret)
	}
}

func (w *worker) emitPipelineTimeOutWarn(phase pipelineWatchdogPhase, generation uint64) {
	logger, ok := w.beginPipelineTimeOutWarn(phase, generation)
	if !ok {
		return
	}
	if logger != nil {
		logger.Line("*** timeout - warn threshold reached")
	}
	w.sendPipelineAlert(w.formatPipelineAlert(timeoutAlertPrefix(phase)+" timeout warning", "The configured warn threshold has been reached."))
}

func (w *worker) emitPipelineTimeOutKill(phase pipelineWatchdogPhase, generation uint64) {
	logger, ok := w.beginPipelineTimeOutKill(phase, generation)
	if !ok {
		return
	}
	if logger != nil {
		logger.Line("*** timeout - kill threshold reached")
	}
	result := interruptPipelineForTimeOut(w, phase, generation)
	if result.stale {
		w.clearStalePipelineTimeOutKill(phase)
		return
	}
	w.recordPipelineTimeOutKillResult(phase, result)
	title := timeoutAlertPrefix(phase) + " timeout kill threshold reached"
	if result.err != nil {
		w.sendPipelineAlert(w.formatPipelineAlert(
			timeoutAlertPrefix(phase)+" timeout kill failed",
			fmt.Sprintf("The engine tried to kill the active process for this pipeline and got: `%v`", result.err),
		))
		return
	}
	if result.queued {
		w.sendPipelineAlert(w.formatPipelineAlert(
			title,
			"The engine canceled this pipeline while it was waiting for an Exclusive lock.",
		))
		return
	}
	if result.manual {
		w.sendPipelineAlert(w.formatPipelineAlert(
			title,
			"This pipeline is currently running in-process Go work, so manual intervention is required.",
		))
		return
	}
	w.sendPipelineAlert(w.formatPipelineAlert(
		title,
		fmt.Sprintf("The engine sent a kill signal to process `%d`.", result.pid),
	))
}

func (w *worker) emitPipelineFailureAlert(ret robot.TaskRetVal, errString string) {
	w.Lock()
	if !w.executedPrimaryTask || w.primaryKillSent || w.cleanupKillSent {
		w.Unlock()
		return
	}
	w.Unlock()
	title := fmt.Sprintf("Pipeline failure: exit code %d (%s)", ret, ret)
	extra := []string{}
	if strings.TrimSpace(errString) != "" {
		extra = append(extra, fmt.Sprintf("Failure detail: `%s`", strings.TrimSpace(errString)))
	}
	w.sendPipelineAlert(w.formatPipelineAlert(title, extra...))
}
