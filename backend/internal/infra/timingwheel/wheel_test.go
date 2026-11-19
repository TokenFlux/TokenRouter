package timingwheel

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// Wheel fixtures verify startup failure recovery, pre-Start task queueing,
// one-shot and recurring execution, cancellation waiting without rearming, and
// bounded shutdown timeouts followed by subsequent completion waits.

// TestNewTimingWheelService_InitFail_NoPanicAndReturnError replaces startup
// validation with an error before Start. It requires an error and a normal
// Stop afterward, protecting cleanup when activation fails.
func TestNewTimingWheelService_InitFail_NoPanicAndReturnError(t *testing.T) {
	// This serial fixture replaces startup validation for its failure check and
	// restores the default hook before another test starts a wheel.
	original := startWheel
	t.Cleanup(func() { startWheel = original })

	startWheel = func() error {
		return errors.New("boom")
	}

	svc := New()
	err := svc.Start()
	if err == nil {
		t.Fatalf("expected Start error, got nil")
	}
	svc.Stop() // Cleanup must be safe after initialization fails.
}

// TestNewTimingWheelService_Success starts a newly constructed wheel and then
// stops it. It requires a non-nil service and successful Start, protecting
// ordinary initialization and completion of its cleanup path.
func TestNewTimingWheelService_Success(t *testing.T) {
	svc := New()
	err := svc.Start()
	if err != nil {
		t.Fatalf("expected nil Start error, got: %v", err)
	}
	if svc == nil {
		t.Fatalf("expected non-nil wheel service")
	}
	svc.Stop()
}

// TestTimingWheelService_ScheduleBeforeStart_ArmsOnStart queues a named task
// before calling Start and also checks nonpositive delay rejection. The valid
// queued callback runs once Start activates timer arming, while nonpositive
// delays do not panic or fire.
func TestTimingWheelService_ScheduleBeforeStart_ArmsOnStart(t *testing.T) {
	svc := New()
	ch := make(chan struct{}, 1)
	svc.Schedule("queued", 15*time.Millisecond, func() { ch <- struct{}{} })
	svc.Schedule("invalid-delay", 0, func() { t.Error("nonpositive delay should not execute") })

	require.NoError(t, svc.Start())
	defer svc.Stop()

	select {
	case <-ch:
	case <-time.After(500 * time.Millisecond):
		t.Fatalf("timed out waiting for pre-Start queued task")
	}
}

// TestTimingWheelService_Schedule_ExecutesOnce schedules a short-delay
// one-shot callback with a buffered notification. It requires one execution
// within the observation budget and no second notification afterward.
func TestTimingWheelService_Schedule_ExecutesOnce(t *testing.T) {
	svc := New()
	require.NoError(t, svc.Start())
	defer svc.Stop()

	ch := make(chan struct{}, 1)
	svc.Schedule("once", 20*time.Millisecond, func() { ch <- struct{}{} })

	select {
	case <-ch:
	case <-time.After(500 * time.Millisecond):
		t.Fatalf("timed out waiting for one-shot task")
	}

	select {
	case <-ch:
		t.Fatalf("one-shot task must not execute twice")
	case <-time.After(80 * time.Millisecond):
	}
}

// TestTimingWheelService_Cancel_PreventsExecution registers a delayed
// one-shot and cancels it immediately. It requires no callback notification
// during a bounded observation window, protecting cancellation before
// admission.
func TestTimingWheelService_Cancel_PreventsExecution(t *testing.T) {
	svc := New()
	require.NoError(t, svc.Start())
	defer svc.Stop()

	ch := make(chan struct{}, 1)
	svc.Schedule("cancel", 80*time.Millisecond, func() { ch <- struct{}{} })
	svc.Cancel("cancel")

	select {
	case <-ch:
		t.Fatalf("canceled task must not execute")
	case <-time.After(160 * time.Millisecond):
	}
}

// TestTimingWheelService_Schedule_AfterStop_LogsError stops the wheel before
// registering a one-shot callback that would fail the test. It requires the
// scheduling call to return normally and leave that callback unexecuted.
func TestTimingWheelService_Schedule_AfterStop_LogsError(t *testing.T) {
	svc := New()
	require.NoError(t, svc.Start())
	svc.Stop()

	svc.Schedule("after-stop", 50*time.Millisecond, func() {
		t.Fatal("stopped wheel must not execute scheduled callback")
	})
}

// TestTimingWheelService_ScheduleRecurring_AfterStop_LogsError stops the
// wheel before registering recurring work that would fail the test. It
// requires registration to return normally and leave the rejected callback
// unexecuted.
func TestTimingWheelService_ScheduleRecurring_AfterStop_LogsError(t *testing.T) {
	svc := New()
	require.NoError(t, svc.Start())
	svc.Stop()

	svc.ScheduleRecurring("after-stop-rec", 50*time.Millisecond, func() {
		t.Fatal("stopped wheel must not execute recurring callback")
	})
}

// TestTimingWheelService_Stop_Idempotent starts a wheel and calls Stop twice
// in succession. It requires both calls to complete normally, protecting
// reuse of terminal shutdown state and its shared completion signal.
func TestTimingWheelService_Stop_Idempotent(t *testing.T) {
	svc := New()
	require.NoError(t, svc.Start())
	svc.Stop()
	svc.Stop() // Repeated Stop calls are safe.
}

// TestTimingWheelService_ScheduleRecurring_ExecutesMultipleTimes observes a
// short-delay recurring callback with an atomic counter. It requires at least
// two completed observations before the deadline, protecting normal rearming
// after callback completion.
func TestTimingWheelService_ScheduleRecurring_ExecutesMultipleTimes(t *testing.T) {
	svc := New()
	require.NoError(t, svc.Start())
	defer svc.Stop()

	var count int32
	svc.ScheduleRecurring("rec", 15*time.Millisecond, func() { atomic.AddInt32(&count, 1) })

	deadline := time.Now().Add(500 * time.Millisecond)
	for atomic.LoadInt32(&count) < 2 && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	if atomic.LoadInt32(&count) < 2 {
		t.Fatalf("expected recurring task to execute at least twice, got %d", atomic.LoadInt32(&count))
	}
}

// TestWheelCancelWaitsAndPreventsRearm holds a recurring callback behind a
// gate while launching cancellation. It requires no cancellation completion
// during the observation window, completion after gate release, and one
// callback after a further interval for detecting rearm.
func TestWheelCancelWaitsAndPreventsRearm(t *testing.T) {
	wheel := New()
	require.NoError(t, wheel.Start())
	entered := make(chan struct{})
	release := make(chan struct{})
	var calls atomic.Int32
	wheel.ScheduleRecurring("flush", time.Millisecond, func() {
		if calls.Add(1) == 1 {
			close(entered)
		}
		<-release
	})
	<-entered
	canceled := make(chan struct{})
	// Run cancellation independently while the admitted callback is gated, so
	// its completion signal can establish that waiting lasts until callback release.
	go func() { wheel.CancelAndWait("flush"); close(canceled) }()
	select {
	case <-canceled:
		t.Fatal("CancelAndWait returned while callback was still running")
	case <-time.After(10 * time.Millisecond):
	}
	// Release the admitted callback before joining cancellation, then allow a
	// short further interval to detect any recurring timer that escaped removal.
	close(release)
	<-canceled
	time.Sleep(10 * time.Millisecond)
	require.EqualValues(t, 1, calls.Load())
	wheel.Stop()
}

// TestWheelShutdownTimeoutReportsIncompleteCallback holds an admitted
// callback beyond the shutdown budget. It requires a deadline error,
// rejection of later scheduling, and successful repeated shutdown after the
// callback gate is released.
func TestWheelShutdownTimeoutReportsIncompleteCallback(t *testing.T) {
	wheel := New()
	require.NoError(t, wheel.Start())
	entered := make(chan struct{})
	release := make(chan struct{})
	wheel.Schedule("busy", time.Millisecond, func() { close(entered); <-release })
	<-entered
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Millisecond)
	defer cancel()
	require.ErrorIs(t, wheel.Shutdown(ctx), context.DeadlineExceeded)
	wheel.Schedule("late", time.Millisecond, func() { t.Error("scheduled callback executed after Shutdown") })
	close(release)
	require.NoError(t, wheel.Shutdown(context.Background()))
}
