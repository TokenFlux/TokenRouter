package plugin

import (
	"context"
	"io"
	"log/slog"
	"sync"
	"testing"
	"testing/synctest"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/TokenFlux/TokenRouter/plugin-sdk/go/manifest"
)

// taskRuntimeFixture records admission and supplies an observable task transport.
type taskRuntimeFixture struct {
	// mu protects observed requests and releases across scheduled calls; version, failure, and run are configured only while calls are quiescent.
	mu       sync.Mutex
	requests []int64                          // scheduled timestamps received by transport
	acquired int                              // observed runtime admission attempts
	released int                              // observed completed lease releases
	version  string                           // admitted process release identity
	failure  error                            // configured runtime admission failure
	run      func(context.Context, int) error // configured execution callback by ordinal
}

// Acquire supplies a new lease or the configured process-unavailable result.
func (f *taskRuntimeFixture) Acquire(context.Context, string) (*Lease, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.acquired++
	if f.failure != nil {
		return nil, f.failure
	}
	return NewLease(&Conn{Tasks: f}, f.version, func() {
		f.mu.Lock()
		f.released++
		f.mu.Unlock()
	}), nil
}

// RunTask records the scheduled timestamp and invokes the configured transport behavior.
// The callback runs outside the observation lock. Tests may hold it past context
// cancellation to verify that host overlap ownership ends only on actual return.
func (f *taskRuntimeFixture) RunTask(ctx context.Context, _ string, scheduled time.Time) error {
	f.mu.Lock()
	f.requests = append(f.requests, scheduled.Unix())
	ordinal := len(f.requests)
	run := f.run
	f.mu.Unlock()
	if run != nil {
		if err := run(ctx, ordinal); err != nil {
			return err
		}
	}
	return nil
}

// snapshot copies counters and timestamps for assertions without retaining fixture locks.
func (f *taskRuntimeFixture) snapshot() ([]int64, int, int) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]int64(nil), f.requests...), f.acquired, f.released
}

// TestTaskSchedulerCronAndDetachedResults starts one five-field schedule under the synthetic host clock and advances to its first occurrence.
// The task receives the declared timestamp, releases its runtime lease, and publishes independently copied execution results for administrator inspection.
func TestTaskSchedulerCronAndDetachedResults(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		runtime := &taskRuntimeFixture{version: "1.0.0"}
		scheduler := NewTaskScheduler(runtime, time.Now, slog.New(slog.NewTextHandler(io.Discard, nil)))
		require.NoError(t, scheduler.Replace("test.tasks", "1.0.0", []manifest.Task{{Name: "minute", Schedule: "* * * * *"}}))
		require.NoError(t, scheduler.Start(t.Context()))
		t.Cleanup(func() { require.NoError(t, scheduler.Stop(context.Background())) })
		synctest.Wait()
		next := scheduler.Status("test.tasks")[0].NextRunAt
		time.Sleep(time.Until(next))
		synctest.Wait()
		requests, acquired, released := runtime.snapshot()
		require.Equal(t, []int64{next.Unix()}, requests)
		require.Equal(t, 1, acquired)
		require.Equal(t, 1, released)
		status := scheduler.Status("test.tasks")
		require.False(t, status[0].Running)
		require.Equal(t, next, status[0].LastRun.ScheduledAt)
		require.Equal(t, "1.0.0", status[0].LastRun.Version)
		status[0].LastRun.Error = "caller-owned mutation"
		require.Empty(t, scheduler.Status("test.tasks")[0].LastRun.Error)
	})
}

// TestTaskSchedulerTimeoutDoesNotReleaseBusyIdentity holds a transport call beyond its deadline and replaces the task declaration while it remains active.
// Later occurrences skip that same identity; actual return releases admission, while suspend and resume retain results without replaying missed intervals.
func TestTaskSchedulerTimeoutDoesNotReleaseBusyIdentity(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		release := make(chan struct{})
		var original context.Context
		runtime := &taskRuntimeFixture{version: "1.0.0", run: func(ctx context.Context, ordinal int) error {
			if ordinal != 1 {
				return nil
			}
			original = ctx
			<-release
			return ctx.Err()
		}}
		timeout := 1
		declaration := []manifest.Task{{Name: "slow", Schedule: "@every 1m", TimeoutSeconds: &timeout}}
		scheduler := NewTaskScheduler(runtime, time.Now, slog.New(slog.NewTextHandler(io.Discard, nil)))
		require.NoError(t, scheduler.Replace("test.tasks", "1.0.0", declaration))
		require.NoError(t, scheduler.Start(t.Context()))
		var releasedBarrier bool
		t.Cleanup(func() {
			if !releasedBarrier {
				close(release)
			}
			require.NoError(t, scheduler.Stop(context.Background()))
		})
		time.Sleep(time.Minute)
		synctest.Wait()
		require.NotNil(t, original)
		time.Sleep(2 * time.Second)
		synctest.Wait()
		require.ErrorIs(t, original.Err(), context.DeadlineExceeded)
		require.NoError(t, scheduler.Replace("test.tasks", "1.0.0", declaration))
		time.Sleep(2 * time.Minute)
		synctest.Wait()
		requests, acquired, released := runtime.snapshot()
		require.Len(t, requests, 1)
		require.Equal(t, 1, acquired)
		require.Zero(t, released)
		require.True(t, scheduler.Status("test.tasks")[0].Running)
		close(release)
		releasedBarrier = true
		synctest.Wait()
		require.False(t, scheduler.Status("test.tasks")[0].Running)
		require.Equal(t, "Task timed out", scheduler.Status("test.tasks")[0].LastRun.Error)
		scheduler.Suspend("test.tasks")
		require.True(t, scheduler.Status("test.tasks")[0].NextRunAt.IsZero())
		time.Sleep(5 * time.Minute)
		synctest.Wait()
		requests, _, _ = runtime.snapshot()
		require.Len(t, requests, 1)
		require.NoError(t, scheduler.Replace("test.tasks", "1.0.0", declaration))
		time.Sleep(time.Minute)
		synctest.Wait()
		requests, _, released = runtime.snapshot()
		require.Len(t, requests, 2)
		require.Equal(t, 2, released)
		require.Empty(t, scheduler.Status("test.tasks")[0].LastRun.Error)
	})
}

// TestTaskSchedulerSkipsUnavailableAndMismatchedGenerations advances a declared job while runtime admission fails and then returns an older release.
// Both occurrences skip transport, the acquired old-generation lease is released, and no misleading successful execution result is published.
func TestTaskSchedulerSkipsUnavailableAndMismatchedGenerations(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		runtime := &taskRuntimeFixture{version: "0.9.0", failure: ErrPluginUnavailable}
		scheduler := NewTaskScheduler(runtime, nil, slog.New(slog.NewTextHandler(io.Discard, nil)))
		require.NoError(t, scheduler.Replace("test.tasks", "1.0.0", []manifest.Task{{Name: "task", Schedule: "@every 1m"}}))
		// Advance while the scheduler is dormant; startup must rebase its declaration instead of executing the old missed occurrence immediately.
		time.Sleep(5 * time.Minute)
		require.NoError(t, scheduler.Start(t.Context()))
		t.Cleanup(func() { require.NoError(t, scheduler.Stop(context.Background())) })
		synctest.Wait()
		_, acquired, _ := runtime.snapshot()
		require.Zero(t, acquired)
		time.Sleep(time.Minute)
		synctest.Wait()
		runtime.mu.Lock()
		runtime.failure = nil
		runtime.mu.Unlock()
		time.Sleep(time.Minute)
		synctest.Wait()
		requests, acquired, released := runtime.snapshot()
		require.Empty(t, requests)
		require.Equal(t, 2, acquired)
		require.Equal(t, 1, released)
		require.Nil(t, scheduler.Status("test.tasks")[0].LastRun)
	})
}

// TestTaskSchedulerInvalidReplacementPreservesRegistration supplies a malformed schedule alongside a valid replacement after registering an existing task.
// Validation fails before publication, preserving the complete prior declaration and allowing removal to clear its in-memory status without starting work.
func TestTaskSchedulerInvalidReplacementPreservesRegistration(t *testing.T) {
	scheduler := NewTaskScheduler(&taskRuntimeFixture{}, nil, nil)
	require.NoError(t, scheduler.Replace("test.tasks", "1.0.0", []manifest.Task{{Name: "kept", Schedule: "@every 1m"}}))
	err := scheduler.Replace("test.tasks", "2.0.0", []manifest.Task{{Name: "new", Schedule: "@every 1m"}, {Name: "bad", Schedule: "@hourly"}})
	require.ErrorIs(t, err, ErrConfigInvalid)
	status := scheduler.Status("test.tasks")
	require.Len(t, status, 1)
	require.Equal(t, "kept", status[0].Name)
	scheduler.Remove("test.tasks")
	require.Empty(t, scheduler.Status("test.tasks"))
}

// TestTaskSchedulerShutdownWaitsForActualReturn holds a canceled transport call past a shutdown deadline and removes its task metadata.
// The timed-out stop retains ownership, later return completes shutdown, and delayed completion cannot recreate an uninstalled task's result or schedule.
func TestTaskSchedulerShutdownWaitsForActualReturn(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		release := make(chan struct{})
		var releaseOnce sync.Once
		runtime := &taskRuntimeFixture{version: "1.0.0", run: func(ctx context.Context, _ int) error {
			<-ctx.Done()
			<-release
			return ctx.Err()
		}}
		scheduler := NewTaskScheduler(runtime, nil, slog.New(slog.NewTextHandler(io.Discard, nil)))
		require.NoError(t, scheduler.Replace("test.tasks", "1.0.0", []manifest.Task{{Name: "held", Schedule: "@every 1m"}}))
		require.NoError(t, scheduler.Start(t.Context()))
		t.Cleanup(func() {
			releaseOnce.Do(func() { close(release) })
			require.NoError(t, scheduler.Stop(context.Background()))
		})
		time.Sleep(time.Minute)
		synctest.Wait()
		budget, cancel := context.WithTimeout(t.Context(), time.Second)
		defer cancel()
		require.ErrorIs(t, scheduler.Stop(budget), context.DeadlineExceeded)
		require.True(t, scheduler.Status("test.tasks")[0].Running)
		scheduler.Remove("test.tasks")
		releaseOnce.Do(func() { close(release) })
		require.NoError(t, scheduler.Stop(t.Context()))
		require.NoError(t, scheduler.Start(t.Context()))
		require.Empty(t, scheduler.Status("test.tasks"))
		_, acquired, released := runtime.snapshot()
		require.Equal(t, acquired, released)
	})
}
