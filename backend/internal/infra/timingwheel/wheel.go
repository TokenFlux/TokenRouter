package timingwheel

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/TokenFlux/TokenRouter/internal/infra/telemetry/logging"
)

// wheel.go schedules named callbacks and coordinates replacement,
// cancellation, and shutdown using runtime timers.
// Named callbacks can be registered before the wheel starts and can outlive
// cancellation once execution has begun. Each registration gets a distinct
// task pointer; dispatch and rearming compare that pointer with the current
// name entry, preventing an old callback from rearming over a replacement that
// reused its name. Recurring delay starts again after callback completion.
//
// A single mutex protects admission, timer handles, identity checks, and
// terminal shutdown state. Execution registers both per-task and wheel-wide
// wait counts before unlocking, while cancellation stops any pending timer and
// removes the task before waiting. Shutdown stops pending timers and rejects
// future admission before waiting for active callbacks, with context
// cancellation bounding the wait while admitted work finishes on its own.

var (
	// startWheel validates wheel startup before queued tasks arm; serial failure
	// fixtures replace it and restore it before another test starts a wheel.
	startWheel = defaultStartWheel
	// newTimer arms one delayed callback; serial tests replace it to force timer
	// submission failures and restore it during cleanup.
	newTimer = defaultNewTimer
)

// timerHandle stops one scheduled runtime timer before or after firing.
type timerHandle interface {
	// Stop prevents the timer from firing and reports whether an active timer was stopped.
	Stop() bool
}

// task identifies one named callback registration, its armed timer, and its admitted execution count.
type task struct {
	name      string        // registered timer lookup name
	delay     time.Duration // delay after registration or completion
	recurring bool          // rearm after callback completion
	// timer holds the latest armed timer handle under Wheel.mu so replacement,
	// cancellation, and shutdown can stop pending callbacks promptly.
	timer timerHandle
	// fn runs outside the wheel mutex and may continue after cancellation.
	// Callers provide any cancellation needed by the callback's own work.
	fn func()
	// wg counts callbacks admitted for this task under Wheel.mu before a
	// cancellation removes its identity; CancelAndWait waits after that removal.
	wg sync.WaitGroup
}

// Wheel schedules named callbacks after Start. Task identity prevents canceled
// callbacks from rearming over a replacement with the same name.
type Wheel struct {
	// mu coordinates started, tasks, stopped, wg admission, and stopDone
	// publication. Execution registers its counts before unlocking; terminal
	// shutdown closes admission before starting the shared completion wait.
	mu       sync.Mutex       // lifecycle and admission mutex
	started  bool             // timer arming activation flag
	tasks    map[string]*task // current registration by name
	stopped  bool             // terminal admission rejection flag
	wg       sync.WaitGroup   // wheel-wide admitted callback count
	stopDone chan struct{}    // shared shutdown completion signal
}

// New constructs an unstarted wheel with an empty named task map.
func New() *Wheel { return &Wheel{tasks: make(map[string]*task)} }

// Start activates timer arming and arms any queued task registrations.
// Repeated calls after successful start return nil; after shutdown, Start
// returns an error. Startup failure leaves the wheel unstarted so another
// Start attempt or Stop can proceed safely.
func (w *Wheel) Start() error {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.stopped {
		return fmt.Errorf("timing wheel already stopped")
	}
	if w.started {
		return nil
	}
	if err := startWheel(); err != nil {
		return fmt.Errorf("failed to create timing wheel: %w", err)
	}
	w.started = true
	for _, t := range w.tasks {
		w.arm(t)
	}
	logging.LegacyPrintf("service.timing_wheel", "%s", "[TimingWheel] Started")
	return nil
}

// Schedule registers a one-shot fn under name, replacing the current
// registration with that name. Before Start it queues the task; after
// shutdown it logs rejection. Replacement does not wait for any already
// active old callback.
func (w *Wheel) Schedule(name string, delay time.Duration, fn func()) {
	w.schedule(name, delay, fn, false)
}

// ScheduleRecurring registers fn to run again after each completed callback
// and delay. It replaces the current named registration, queues before Start,
// and logs rejection after shutdown; already active replaced work can still
// finish.
func (w *Wheel) ScheduleRecurring(name string, delay time.Duration, fn func()) {
	w.schedule(name, delay, fn, true)
}

// schedule replaces the named task registration under the wheel mutex and
// arms it when started. Each replacement stops the prior task's pending timer
// and receives a distinct task pointer, so previously dispatched or active
// callbacks cannot remove or rearm the replacement.
//
// A stopped wheel logs rejection and keeps terminal state. Nonpositive delays
// or timer errors are logged while the task remains registered. Replacing a
// task does not stop an already running function, and callers use cancellation
// or shutdown waiting when they need completion.
func (w *Wheel) schedule(name string, delay time.Duration, fn func(), recurring bool) {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.stopped {
		logging.LegacyPrintf("service.timing_wheel", "[TimingWheel] SetTimer failed for %q: stopped", name)
		return
	}
	if prev := w.tasks[name]; prev != nil && prev.timer != nil {
		prev.timer.Stop()
		prev.timer = nil
	}
	// Publish a fresh registration pointer before arming its timer, so a
	// previously dispatched callback fails the identity check even when it shares this name.
	t := &task{name: name, delay: delay, fn: fn, recurring: recurring}
	w.tasks[name] = t
	if w.started {
		w.arm(t)
	}
}

// arm submits t for asynchronous dispatch through a runtime timer. The caller
// holds w.mu so an immediately firing timer blocks at execute's lock until
// t.timer is recorded; timer errors are logged while the task registration
// remains in tasks.
func (w *Wheel) arm(t *task) {
	if t.timer != nil {
		t.timer.Stop()
		t.timer = nil
	}
	// Failed timer submission leaves the named registration in tasks. Report
	// the failure here because the public scheduling calls return no admission error.
	timer, err := newTimer(t.delay, func() { w.execute(t) })
	if err != nil {
		logging.LegacyPrintf("service.timing_wheel", "[TimingWheel] SetTimer failed for %q: %v", t.name, err)
		return
	}
	t.timer = timer
}

// execute admits t only if it is still the current named registration and the
// wheel is running. It registers both wait counts under the mutex before
// invoking fn outside that mutex; nil functions simply complete.
//
// After execution, a second identity and shutdown check decides whether to
// rearm recurring work or remove the one-shot registration. Cancellation or
// replacement during fn therefore prevents this old task from changing a
// successor's timer. Deferred wait-count releases still run when fn panics,
// but normal rearming and removal are skipped.
func (w *Wheel) execute(t *task) {
	w.mu.Lock()
	if w.stopped || w.tasks[t.name] != t {
		w.mu.Unlock()
		return
	}
	t.timer = nil
	// Register active work before releasing admission's lock so cancellation
	// and shutdown cannot begin waiting before this accepted callback is counted.
	w.wg.Add(1)
	t.wg.Add(1)
	w.mu.Unlock()
	defer w.wg.Done()
	defer t.wg.Done()
	if t.fn != nil {
		t.fn()
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.stopped || w.tasks[t.name] != t {
		return
	}
	// The callback can cancel or replace its registration while running. The
	// preceding identity check keeps rearming or deletion tied to this same task.
	if t.recurring {
		w.arm(t)
	} else {
		delete(w.tasks, t.name)
	}
}

// Cancel removes the current registration for name and prevents it from
// rearming. Already admitted callbacks may still finish; callers needing
// their completion use CancelAndWait rather than assuming removal stops
// active work.
func (w *Wheel) Cancel(name string) { w.cancel(name) }

// CancelAndWait removes name's current registration and waits for callbacks
// already admitted for that task. Calling it from the same callback deadlocks
// on its own count; a later replacement with the same name is a separate
// registration.
func (w *Wheel) CancelAndWait(name string) {
	if t := w.cancel(name); t != nil {
		t.wg.Wait()
	}
}

// cancel removes name's current registration under the wheel mutex and stops
// its pending timer before returning the task pointer. A dispatched callback
// must pass execute's identity check, while already admitted work retains its
// wait count.
//
// Stopping an already fired timer is harmless because deleting the task
// identity prevents a racing callback from entering normal execution. A
// returned task identifies the registration present at cancellation, allowing
// CancelAndWait to wait for that task; replacements scheduled afterward have
// separate identity and execution counts.
func (w *Wheel) cancel(name string) *task {
	w.mu.Lock()
	defer w.mu.Unlock()
	t := w.tasks[name]
	// Invalidate identity before stopping the timer; an already dispatched callback
	// then fails execute's admission check even if Stop returns false.
	delete(w.tasks, name)
	if t != nil && t.timer != nil {
		t.timer.Stop()
		t.timer = nil
	}
	return t
}

// Stop performs terminal shutdown and waits for active callbacks using
// background context. It can be repeated, but an active callback calling Stop
// cannot complete while shutdown waits for that callback.
func (w *Wheel) Stop() { _ = w.Shutdown(context.Background()) }

// Shutdown permanently rejects scheduling and waits for admitted callbacks,
// returning ctx's error if its wait budget ends. Pass a non-nil context. The
// first call stops pending task timers, clears queued task identities, and
// starts one wait goroutine after admission closes under the mutex.
//
// Later calls share the same completion channel and can wait again after a
// timeout. Cancellation affects waiting rather than callback execution;
// callbacks retain responsibility for their own cancellation and return.
// Calling from an admitted callback waits on its own outstanding count.
func (w *Wheel) Shutdown(ctx context.Context) error {
	w.mu.Lock()
	if w.stopDone == nil {
		// Close admission before starting Wait, so no accepted execution can add
		// a new wait count after shutdown has begun sharing its completion signal.
		w.stopped = true
		w.stopDone = make(chan struct{})
		for _, t := range w.tasks {
			if t.timer != nil {
				t.timer.Stop()
				t.timer = nil
			}
		}
		clear(w.tasks)
		go func() { w.wg.Wait(); close(w.stopDone) }()
	}
	done := w.stopDone
	w.mu.Unlock()
	select {
	case <-done:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

// defaultStartWheel reports immediate readiness for runtime timer scheduling.
func defaultStartWheel() error { return nil }

// defaultNewTimer arms fn after a positive delay using Go's runtime timer heap.
// Nonpositive delays return an error so invalid intervals are logged rather than firing immediately.
func defaultNewTimer(delay time.Duration, fn func()) (timerHandle, error) {
	if delay <= 0 {
		return nil, fmt.Errorf("delay must be positive")
	}
	return time.AfterFunc(delay, fn), nil
}
