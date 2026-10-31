package plugin

import (
	"context"
	"errors"
	"log/slog"
	"slices"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/robfig/cron/v3"

	"github.com/TokenFlux/TokenRouter/plugin-sdk/go/manifest"
)

// tasks.go schedules declared plugin jobs and retains their latest execution
// outcomes in memory. Each task uses the host timezone and an independently
// parsed cron schedule. Registration and resume begin with a future occurrence,
// while due dispatch advances directly from the current clock without replaying
// missed intervals. Runtime admission remains with the manager's ordinary leases.
//
// Active ownership is keyed by plugin identity and task name, independently of
// changing declarations. Reconfiguration therefore cannot overlap a previous
// invocation that has not returned. Cancellation bounds host RPC ownership;
// the SDK additionally guards actual author callbacks after transport timeouts.
// Shutdown seals new dispatch, cancels active work, and waits for both the timer
// loop and admitted calls before releasing lifecycle ownership.

// TaskResult records one completed execution without persisting across host restarts.
type TaskResult struct {
	Version     string    `json:"version"`         // process version used for execution
	ScheduledAt time.Time `json:"scheduled_at"`    // declared occurrence selected for dispatch
	StartedAt   time.Time `json:"started_at"`      // host execution start observation
	DurationMs  int64     `json:"duration_ms"`     // milliseconds
	Error       string    `json:"error,omitempty"` // bounded execution failure description
}

// TaskStatus exposes the next occurrence and latest result of one registered task.
type TaskStatus struct {
	Name      string      `json:"name"`               // manifest task identity within plugin
	NextRunAt time.Time   `json:"next_run_at"`        // next occurrence; zero while suspended
	Running   bool        `json:"running"`            // host invocation has not returned
	LastRun   *TaskResult `json:"last_run,omitempty"` // independently copied latest execution outcome
}

// scheduledTask retains a parsed declaration and mutable scheduling observations.
type scheduledTask struct {
	id          string        // owning plugin installation identity
	version     string        // declaration's expected process version
	declaration manifest.Task // immutable declared schedule and timeout
	schedule    cron.Schedule // parsed host-timezone schedule calculation
	next        time.Time     // next eligible scheduled occurrence
	last        *TaskResult   // immutable latest completed execution result
}

// TaskScheduler owns declared jobs, active calls, and one timer-loop lifetime.
// @project-doc docs/architecture/plugin_system.md#task_scheduling
type TaskScheduler struct {
	runtime Runtime          // manager-controlled process lease admission
	now     func() time.Time // injectable host scheduling clock
	logger  *slog.Logger     // skipped and failed execution diagnostics
	// mu protects tasks, active, and lifecycle ownership. Active entries outlive declaration replacement until their captured invocation actually returns.
	mu      sync.Mutex
	tasks   map[string]*scheduledTask     // plugin and task to scheduling state
	active  map[string]context.CancelFunc // admitted execution cancellation ownership
	started bool                          // timer loop already launched
	stopped bool                          // terminal dispatch shutdown requested
	cancel  context.CancelFunc            // owned timer loop cancellation signal
	done    chan struct{}                 // loop and execution completion signal
	wake    chan struct{}                 // coalesced schedule change notification
	calls   sync.WaitGroup                // admitted host invocation ownership
}

// NewTaskScheduler constructs dormant scheduling with the host clock and logger defaults.
// Registration parses declarations without starting a goroutine. Start establishes
// timer ownership, and Stop permanently prevents another loop or later dispatch.
// A supplied clock must support calls from the scheduler and executing tasks.
func NewTaskScheduler(runtime Runtime, now func() time.Time, logger *slog.Logger) *TaskScheduler {
	if now == nil {
		now = time.Now
	}
	if logger == nil {
		logger = slog.Default()
	}
	return &TaskScheduler{
		runtime: runtime,
		now:     now,
		logger:  logger,
		tasks:   make(map[string]*scheduledTask),
		active:  make(map[string]context.CancelFunc),
		wake:    make(chan struct{}, 1),
	}
}

// Replace publishes all schedules for one validated plugin release together.
// Invalid declarations leave the previous registration unchanged. Matching version,
// schedule, and timeout retain an already future occurrence and latest result.
//
// Parsing happens before publication so a malformed task cannot partially replace
// its neighbors. Removed tasks cancel their active invocation but retain that
// invocation's overlap guard until return. Resumed or changed schedules calculate
// a future occurrence from the current host clock, avoiding catch-up execution
// after disable, restart, or a prolonged administrative version replacement.
func (s *TaskScheduler) Replace(id, version string, declarations []manifest.Task) error {
	parsed := make(map[string]*scheduledTask, len(declarations))
	for _, declaration := range declarations {
		schedule, err := manifest.ParseSchedule(declaration.Schedule)
		if err != nil {
			return ErrConfigInvalid
		}
		key := id + ":" + declaration.Name
		if _, duplicate := parsed[key]; duplicate || declaration.Name == "" || declaration.Timeout() < 1 || declaration.Timeout() > 3600 {
			return ErrConfigInvalid
		}
		// Copy the optional timeout value so later edits to a caller-owned declaration cannot change a registered task's execution deadline.
		timeout := declaration.Timeout()
		declaration.TimeoutSeconds = &timeout
		parsed[key] = &scheduledTask{id: id, version: version, declaration: declaration, schedule: schedule}
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.stopped {
		return nil
	}
	now := s.now().In(time.Local)
	for key, old := range s.tasks {
		if old.id != id {
			continue
		}
		if _, retained := parsed[key]; !retained {
			delete(s.tasks, key)
			if cancel := s.active[key]; cancel != nil {
				cancel()
			}
		}
	}
	for key, task := range parsed {
		task.next = task.schedule.Next(now)
		if old := s.tasks[key]; old != nil {
			task.last = old.last
			if old.version == version && old.declaration.Schedule == task.declaration.Schedule && old.declaration.Timeout() == task.declaration.Timeout() && old.next.After(now) {
				task.next = old.next
			}
		}
		s.tasks[key] = task
	}
	s.signal()
	return nil
}

// Suspend stops future occurrences and cancels active calls while preserving recent results.
// The manager invokes this before draining a process. Active guards remain until
// RPC return so a concurrent resume cannot overlap the same task identity.
func (s *TaskScheduler) Suspend(id string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for key, task := range s.tasks {
		if task.id == id {
			task.next = time.Time{}
			if cancel := s.active[key]; cancel != nil {
				cancel()
			}
		}
	}
	s.signal()
}

// Remove forgets one installation's schedule and in-memory results after draining.
// Already canceled calls keep their guards until return. Their completion cannot
// recreate removed task metadata, keeping uninstall cleanup independent of goroutine timing.
func (s *TaskScheduler) Remove(id string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for key, task := range s.tasks {
		if task.id == id {
			delete(s.tasks, key)
			if cancel := s.active[key]; cancel != nil {
				cancel()
			}
		}
	}
	s.signal()
}

// Status returns detached observations in task-name order for administrator details.
// Suspended tasks retain their last result with a zero next occurrence. Callers
// may change the returned slice or result values without modifying scheduler state.
func (s *TaskScheduler) Status(id string) []TaskStatus {
	s.mu.Lock()
	defer s.mu.Unlock()
	result := make([]TaskStatus, 0)
	for key, task := range s.tasks {
		if task.id != id {
			continue
		}
		status := TaskStatus{Name: task.declaration.Name, NextRunAt: task.next, Running: s.active[key] != nil}
		if task.last != nil {
			copy := *task.last
			status.LastRun = &copy
		}
		result = append(result, status)
	}
	slices.SortFunc(result, func(a, b TaskStatus) int { return strings.Compare(a.Name, b.Name) })
	return result
}

// Start launches one timer loop and rebases registered schedules to future occurrences.
// Repeated starts and starts after Stop do nothing. The worker's context survives
// this callback and owns every subsequently admitted task deadline.
//
// Lifecycle handles are published under the same lock used by dispatch and Stop.
// Shutdown either prevents launch or observes the exact loop it must await.
// The loop waits for admitted calls before closing done, so a canceled transport
// that has not returned remains part of lifecycle completion and overlap exclusion.
func (s *TaskScheduler) Start(context.Context) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.started || s.stopped {
		return nil
	}
	ctx, cancel := context.WithCancel(context.Background())
	s.cancel, s.done, s.started = cancel, make(chan struct{}), true
	for _, task := range s.tasks {
		if !task.next.IsZero() {
			task.next = task.schedule.Next(s.now().In(time.Local))
		}
	}
	// Publish shutdown ownership before launching; the completion signal covers the timer loop and all calls registered before dispatch is sealed.
	go func() {
		defer close(s.done)
		s.run(ctx)
		s.calls.Wait()
	}()
	return nil
}

// Stop seals dispatch, cancels active work, and waits for actual host-call completion.
// A deadline returns without abandoning ownership; repeated Stop calls can wait
// again for the same completion signal. A scheduler that never started stops immediately.
//
// The terminal state is set under the dispatch lock before cancellation, so no
// new WaitGroup registration can race the worker's final wait. Calls already
// dispatched use child contexts of the loop and receive cancellation together.
// Their overlap guards and runtime leases remain owned until those calls return.
func (s *TaskScheduler) Stop(ctx context.Context) error {
	s.mu.Lock()
	s.stopped = true
	if s.cancel != nil {
		s.cancel()
	}
	done := s.done
	s.mu.Unlock()
	if done == nil {
		return nil
	}
	select {
	case <-done:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

// run waits for the earliest occurrence or a coalesced registration change.
// Empty schedules use an idle timer that registration can interrupt immediately.
// Each wake recalculates from the injected clock, preserving host timezone and clock changes.
func (s *TaskScheduler) run(ctx context.Context) {
	for {
		timer := time.NewTimer(s.delay())
		select {
		case <-ctx.Done():
			timer.Stop()
			return
		case <-s.wake:
		case <-timer.C:
		}
		timer.Stop()
		if ctx.Err() != nil {
			return
		}
		s.dispatch(ctx)
	}
}

// delay bounds the next timer by the nearest enabled occurrence.
// An hour is only the idle or clock-recheck ceiling. Registration changes wake
// the loop immediately, and overdue occurrences request immediate dispatch.
func (s *TaskScheduler) delay() time.Duration {
	s.mu.Lock()
	defer s.mu.Unlock()
	delay := time.Hour
	now := s.now()
	for _, task := range s.tasks {
		if !task.next.IsZero() {
			delay = min(delay, max(time.Duration(0), task.next.Sub(now)))
		}
	}
	return delay
}

// dispatch advances due schedules and reserves each free task identity before launch.
// Busy tasks skip the occurrence with a warning. Missed intervals are not queued;
// the next occurrence is calculated from the current host clock.
//
// Active ownership is installed while the dispatch lock still protects the
// declaration and terminal state. Replacement or suspension can then cancel that
// same invocation but cannot create a second concurrent call for its identity.
// WaitGroup registration happens before releasing the lock, preserving Stop's
// guarantee that final completion includes every invocation admitted before shutdown.
func (s *TaskScheduler) dispatch(ctx context.Context) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.stopped {
		return
	}
	now := s.now().In(time.Local)
	for key, task := range s.tasks {
		if task.next.IsZero() || task.next.After(now) {
			continue
		}
		scheduled := task.next
		task.next = task.schedule.Next(now)
		if s.active[key] != nil {
			s.logger.Warn("Plugin task occurrence skipped while previous call is active", "plugin_id", task.id, "task", task.declaration.Name)
			continue
		}
		// Manifest task deadlines are expressed in whole seconds.
		call, cancel := context.WithTimeout(ctx, time.Duration(task.declaration.Timeout())*time.Second)
		s.active[key] = cancel
		// Register before launch so declaration replacement and shutdown observe this task's reservation even before its goroutine reaches runtime admission.
		s.calls.Go(func() { s.execute(call, key, task, scheduled, cancel) })
	}
}

// execute retains one runtime lease until its bounded task RPC returns.
// Unavailable or mismatched generations skip execution and leave the latest result
// unchanged. The SDK separately guards author code that survives transport cancellation.
//
// Completion records the actual executing version and bounded error before
// releasing the task identity. A replacement declaration may inherit this latest
// result, but a removed task is never recreated. Deferred cleanup releases the
// lease and cancellation resources even when admission or execution fails, while
// the scheduler's WaitGroup keeps the invocation visible to terminal shutdown.
func (s *TaskScheduler) execute(ctx context.Context, key string, task *scheduledTask, scheduled time.Time, cancel context.CancelFunc) {
	defer cancel()
	defer func() {
		s.mu.Lock()
		delete(s.active, key)
		s.mu.Unlock()
	}()
	lease, err := s.runtime.Acquire(ctx, task.id)
	if err != nil {
		s.logger.Warn("Plugin task occurrence skipped because its process is unavailable", "plugin_id", task.id, "task", task.declaration.Name, "error", err)
		return
	}
	defer lease.Release()
	if lease.Version != task.version {
		s.logger.Warn("Plugin task occurrence skipped after a version change", "plugin_id", task.id, "task", task.declaration.Name)
		return
	}
	started := s.now()
	err = lease.Conn.RunTask(ctx, task.declaration.Name, scheduled)
	// A backwards host-clock adjustment cannot produce a negative reported duration; monotonic observations retain the usual elapsed interval.
	result := &TaskResult{
		Version:     lease.Version,
		ScheduledAt: scheduled,
		StartedAt:   started,
		DurationMs:  max(int64(0), s.now().Sub(started).Milliseconds()),
	}
	if err != nil {
		result.Error = taskError(err)
		s.logger.Warn("Plugin task execution failed", "plugin_id", task.id, "task", task.declaration.Name, "error", result.Error)
	}
	s.mu.Lock()
	if current := s.tasks[key]; current != nil {
		current.last = result
	}
	s.mu.Unlock()
}

// signal coalesces registration changes without blocking callers that hold the scheduler lock.
func (s *TaskScheduler) signal() {
	select {
	case s.wake <- struct{}{}:
	default:
	}
}

// taskError returns a bounded UTF-8 diagnostic for in-memory administrator observations.
// Cancellation and deadlines use stable descriptions. Other plugin errors retain
// at most 1024 bytes, excluding NUL so later JSON consumers can store the text safely.
func taskError(err error) string {
	if errors.Is(err, context.Canceled) {
		return "Task canceled"
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return "Task timed out"
	}
	value := strings.ReplaceAll(strings.ToValidUTF8(err.Error(), ""), "\x00", "")
	// Keep plugin-supplied diagnostics bounded without splitting the final retained Unicode character in the administrator response.
	if len(value) > 1024 {
		value = value[:1024]
		for !utf8.ValidString(value) {
			value = value[:len(value)-1]
		}
	}
	return strings.Clone(value)
}
