package bot

import (
	"encoding/json"
	"fmt"
	"log"
	"sort"
	"strings"
	"sync"
	"time"

	shlex "github.com/anmitsu/go-shlex"
	"github.com/google/uuid"
	"github.com/lnxjedi/gopherbot/robot"
)

const queueUUIDPrefixLen = 36
const queueTimestampMinLen = 12
const queueTimestampMaxLen = 15

const queueDedupeRetention = 140 * time.Second

type parsedQueueBody struct {
	jobUUID   string
	timestamp string
	args      []string
}

type queueHandler struct {
	handler
	provider string
}

type managedQueueProvider struct {
	name      string
	provider  robot.QueueProvider
	stop      chan struct{}
	done      chan struct{}
	running   bool
	stopping  bool
	lastError string
}

var runtimeQueueProviders = struct {
	sync.RWMutex
	initialized bool
	runtimes    map[string]*managedQueueProvider
}{
	runtimes: map[string]*managedQueueProvider{},
}

var queueDedupe = struct {
	sync.Mutex
	seen map[string]time.Time
}{
	seen: map[string]time.Time{},
}

func (h queueHandler) GetQueueConfig(v interface{}) error {
	cfg := getQueueConfigFor(h.provider)
	if cfg == nil {
		return fmt.Errorf("no QueueConfig loaded for queue provider '%s'", h.provider)
	}
	return json.Unmarshal(cfg, v)
}

func (h queueHandler) HandleQueueMessage(msg robot.QueueMessage) robot.QueueDisposition {
	return triggerJobFromQueue(h.provider, msg)
}

func (h queueHandler) Log(l robot.LogLevel, m string, v ...interface{}) {
	p := normalizeProviderName(h.provider)
	if p == "" {
		h.handler.Log(l, m, v...)
		return
	}
	m = "[queue:" + p + "] " + m
	h.handler.Log(l, m, v...)
}

func configuredQueueProviders() []string {
	currentCfg.RLock()
	defer currentCfg.RUnlock()
	return append([]string(nil), currentCfg.queueProviders...)
}

func startQueueProviderRuntimes() {
	providers := configuredQueueProviders()
	runtimeQueueProviders.Lock()
	if runtimeQueueProviders.runtimes == nil {
		runtimeQueueProviders.runtimes = map[string]*managedQueueProvider{}
	}
	runtimeQueueProviders.initialized = true
	runtimeQueueProviders.Unlock()

	for _, provider := range providers {
		if err := startQueueProviderRuntime(provider, botLogger.logger); err != nil {
			Log(robot.Error, "Queue provider '%s' failed to start: %v", provider, err)
		}
	}
}

func startQueueProviderRuntime(provider string, logger *log.Logger) error {
	name := normalizeProviderName(provider)
	if name == "" {
		return fmt.Errorf("invalid empty queue provider name")
	}

	runtimeQueueProviders.Lock()
	mq, ok := runtimeQueueProviders.runtimes[name]
	if !ok || mq == nil {
		mq = &managedQueueProvider{name: name}
		runtimeQueueProviders.runtimes[name] = mq
	}
	if mq.running {
		runtimeQueueProviders.Unlock()
		return nil
	}
	runtimeQueueProviders.Unlock()

	registration, ok := queueProviderRegistration(name)
	if !ok {
		err := fmt.Errorf("no queue provider registered with name '%s'", name)
		recordQueueProviderError(name, err)
		return err
	}
	initialized, err := registration.Initialize(queueHandler{
		handler:  handle,
		provider: name,
	}, logger)
	if err != nil {
		recordQueueProviderError(name, err)
		return err
	}
	if initialized.Provider == nil {
		err := fmt.Errorf("queue provider '%s' returned nil from initializer", name)
		recordQueueProviderError(name, err)
		return err
	}

	stop := make(chan struct{})
	done := make(chan struct{})
	runtimeQueueProviders.Lock()
	mq = runtimeQueueProviders.runtimes[name]
	if mq == nil {
		mq = &managedQueueProvider{name: name}
		runtimeQueueProviders.runtimes[name] = mq
	}
	mq.provider = initialized.Provider
	mq.stop = stop
	mq.done = done
	mq.running = true
	mq.stopping = false
	mq.lastError = ""
	runtimeQueueProviders.Unlock()

	go func(provider string, qp robot.QueueProvider, stop <-chan struct{}, done chan struct{}) {
		qp.Run(stop)

		var shouldLogError bool
		runtimeQueueProviders.Lock()
		if mq, ok := runtimeQueueProviders.runtimes[provider]; ok && mq != nil {
			shouldLogError = !mq.stopping
			mq.running = false
			mq.stopping = false
			if shouldLogError && !state.shuttingDown {
				mq.lastError = "queue provider exited"
			}
		}
		runtimeQueueProviders.Unlock()
		close(done)
		if shouldLogError && !state.shuttingDown {
			Log(robot.Error, "Queue provider '%s' exited unexpectedly", provider)
		} else {
			Log(robot.Info, "Queue provider '%s' stopped", provider)
		}
	}(name, initialized.Provider, stop, done)
	Log(robot.Info, "Queue provider '%s' started", name)
	return nil
}

func recordQueueProviderError(provider string, err error) {
	name := normalizeProviderName(provider)
	if name == "" || err == nil {
		return
	}
	runtimeQueueProviders.Lock()
	mq, ok := runtimeQueueProviders.runtimes[name]
	if !ok || mq == nil {
		mq = &managedQueueProvider{name: name}
		runtimeQueueProviders.runtimes[name] = mq
	}
	mq.lastError = err.Error()
	runtimeQueueProviders.Unlock()
}

func stopQueueProviderRuntime(provider string) error {
	name := normalizeProviderName(provider)
	if name == "" {
		return fmt.Errorf("invalid empty queue provider name")
	}
	runtimeQueueProviders.Lock()
	mq, ok := runtimeQueueProviders.runtimes[name]
	if !ok || mq == nil || !mq.running {
		runtimeQueueProviders.Unlock()
		return nil
	}
	mq.stopping = true
	stop := mq.stop
	done := mq.done
	runtimeQueueProviders.Unlock()

	close(stop)
	<-done
	return nil
}

func shutdownQueueProviderRuntimes() {
	runtimeQueueProviders.RLock()
	providers := make([]string, 0, len(runtimeQueueProviders.runtimes))
	for provider, mq := range runtimeQueueProviders.runtimes {
		if mq != nil && mq.running {
			providers = append(providers, provider)
		}
	}
	runtimeQueueProviders.RUnlock()
	sort.Strings(providers)
	for _, provider := range providers {
		if err := stopQueueProviderRuntime(provider); err != nil {
			Log(robot.Error, "Stopping queue provider '%s': %v", provider, err)
		}
	}
}

func reconcileQueueProviderRuntimes(providers []string) {
	runtimeQueueProviders.RLock()
	initialized := runtimeQueueProviders.initialized
	runtimeQueueProviders.RUnlock()
	if !initialized {
		return
	}

	desired := make(map[string]bool)
	for _, provider := range providers {
		name := normalizeProviderName(provider)
		if name != "" {
			desired[name] = true
		}
	}

	runtimeQueueProviders.RLock()
	current := make([]string, 0, len(runtimeQueueProviders.runtimes))
	for provider := range runtimeQueueProviders.runtimes {
		current = append(current, provider)
	}
	runtimeQueueProviders.RUnlock()
	sort.Strings(current)

	for _, provider := range current {
		_ = stopQueueProviderRuntime(provider)
		runtimeQueueProviders.Lock()
		delete(runtimeQueueProviders.runtimes, provider)
		runtimeQueueProviders.Unlock()
	}
	for provider := range desired {
		if err := startQueueProviderRuntime(provider, botLogger.logger); err != nil {
			Log(robot.Error, "Queue provider '%s' failed to start after reload: %v", provider, err)
		}
	}
}

func parseQueueBody(body []byte) (parsedQueueBody, error) {
	if len(body) < queueUUIDPrefixLen {
		return parsedQueueBody{}, fmt.Errorf("queue body too short: %d byte(s)", len(body))
	}
	id, err := uuid.ParseBytes(body[:queueUUIDPrefixLen])
	if err != nil {
		return parsedQueueBody{}, fmt.Errorf("invalid queue UUID prefix: %w", err)
	}
	if len(body) == queueUUIDPrefixLen || body[queueUUIDPrefixLen] != ':' {
		return parsedQueueBody{}, fmt.Errorf("queue UUID prefix is not followed by a colon")
	}

	timestampStart := queueUUIDPrefixLen + 1
	timestampEnd := timestampStart
	for timestampEnd < len(body) && body[timestampEnd] >= '0' && body[timestampEnd] <= '9' {
		timestampEnd++
	}
	timestampLen := timestampEnd - timestampStart
	if timestampLen < queueTimestampMinLen || timestampLen > queueTimestampMaxLen {
		return parsedQueueBody{}, fmt.Errorf("queue timestamp must be %d-%d digits", queueTimestampMinLen, queueTimestampMaxLen)
	}
	if timestampEnd == len(body) {
		return parsedQueueBody{
			jobUUID:   id.String(),
			timestamp: string(body[timestampStart:timestampEnd]),
		}, nil
	}
	if body[timestampEnd] != ' ' {
		return parsedQueueBody{}, fmt.Errorf("queue timestamp is not followed by a space")
	}
	argText := strings.TrimSpace(string(body[timestampEnd+1:]))
	if argText == "" {
		return parsedQueueBody{
			jobUUID:   id.String(),
			timestamp: string(body[timestampStart:timestampEnd]),
		}, nil
	}
	args, err := shlex.Split(argText, true)
	if err != nil {
		return parsedQueueBody{}, fmt.Errorf("parsing shell-escaped queue arguments: %w", err)
	}
	return parsedQueueBody{
		jobUUID:   id.String(),
		timestamp: string(body[timestampStart:timestampEnd]),
		args:      args,
	}, nil
}

func recordQueueDedupe(jobUUID, timestamp string, now time.Time) bool {
	key := jobUUID + ":" + timestamp
	queueDedupe.Lock()
	defer queueDedupe.Unlock()
	for seenKey, expiresAt := range queueDedupe.seen {
		if !now.Before(expiresAt) {
			delete(queueDedupe.seen, seenKey)
		}
	}
	if expiresAt, ok := queueDedupe.seen[key]; ok && now.Before(expiresAt) {
		return true
	}
	queueDedupe.seen[key] = now.Add(queueDedupeRetention)
	return false
}

func triggerJobFromQueue(provider string, msg robot.QueueMessage) robot.QueueDisposition {
	state.RLock()
	if state.shuttingDown {
		state.RUnlock()
		return robot.QueueRetry
	}
	state.RUnlock()

	parsed, err := parseQueueBody(msg.Body)
	if err != nil {
		Log(robot.Error, "Queue provider '%s' message '%s' rejected: %v (body length %d)", provider, msg.ID, err, len(msg.Body))
		return robot.QueueAck
	}

	currentCfg.RLock()
	cfg := currentCfg.configuration
	tasks := currentCfg.taskList
	protocol := currentCfg.defaultProtocol
	if protocol == "" {
		protocol = currentCfg.protocol
	}
	taskItem := tasks.uuidTriggers[parsed.jobUUID]
	currentCfg.RUnlock()

	if taskItem == nil {
		Log(robot.Error, "Queue provider '%s' message '%s' had no matching job UUID (body length %d)", provider, msg.ID, len(msg.Body))
		return robot.QueueAck
	}
	task, _, job := getTask(taskItem)
	if recordQueueDedupe(parsed.jobUUID, parsed.timestamp, time.Now()) {
		jobName := "<unknown>"
		if task != nil {
			jobName = task.name
		}
		Log(robot.Info, "Queue provider '%s' message '%s' discarded duplicate queue trigger for job '%s'", provider, msg.ID, jobName)
		return robot.QueueAck
	}
	if job == nil {
		Log(robot.Error, "Queue provider '%s' message '%s' matched non-job task '%s'", provider, msg.ID, task.name)
		return robot.QueueAck
	}
	if task.Disabled {
		Log(robot.Error, "Queue provider '%s' message '%s' matched disabled job '%s'", provider, msg.ID, task.name)
		return robot.QueueAck
	}
	if len(parsed.args) < len(job.Arguments) {
		Log(robot.Error, "Queue provider '%s' message '%s' supplied too few arguments for job '%s': %d required but %d given", provider, msg.ID, task.name, len(job.Arguments), len(parsed.args))
		return robot.QueueAck
	}
	for i, jobarg := range job.Arguments {
		if !jobarg.re.MatchString(parsed.args[i]) {
			Log(robot.Error, "Queue provider '%s' message '%s' argument %d for job '%s' did not match configured argument pattern", provider, msg.ID, i+1, task.name)
			return robot.QueueAck
		}
	}

	Log(robot.Info, "Job '%s' triggered from queue provider '%s'", task.name, provider)
	w := &worker{
		Channel:        task.Channel,
		Protocol:       getProtocol(protocol),
		Incoming:       &robot.ConnectorMessage{Protocol: protocol},
		cfg:            cfg,
		id:             getWorkerID(),
		tasks:          tasks,
		automaticTask:  true,
		queueProvider:  provider,
		queueMessageID: msg.ID,
	}
	go w.startPipeline(nil, taskItem, queuedJob, "run", parsed.args...)
	return robot.QueueAck
}
