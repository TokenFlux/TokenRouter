package timingwheel

import (
	"context"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"github.com/zeromicro/go-zero/core/collection"
)

// 回调已经进入执行时，取消必须等待它完成并阻止 recurring 再入队。
func TestWheelCancelWaitsAndPreventsRearm(t *testing.T) {
	original := newTimingWheel
	newTimingWheel = func(_ time.Duration, _ int, execute collection.Execute) (*collection.TimingWheel, error) {
		return original(time.Millisecond, 64, execute)
	}
	t.Cleanup(func() { newTimingWheel = original })
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
	go func() { wheel.CancelAndWait("flush"); close(canceled) }()
	select {
	case <-canceled:
		t.Fatal("回调仍在执行")
	case <-time.After(10 * time.Millisecond):
	}
	close(release)
	<-canceled
	time.Sleep(10 * time.Millisecond)
	require.EqualValues(t, 1, calls.Load())
	wheel.Stop()
}

func TestWheelShutdownTimeoutReportsIncompleteCallback(t *testing.T) {
	original := newTimingWheel
	newTimingWheel = func(_ time.Duration, _ int, execute collection.Execute) (*collection.TimingWheel, error) {
		return original(time.Millisecond, 64, execute)
	}
	t.Cleanup(func() { newTimingWheel = original })
	wheel := New()
	require.NoError(t, wheel.Start())
	entered := make(chan struct{})
	release := make(chan struct{})
	wheel.Schedule("busy", time.Millisecond, func() { close(entered); <-release })
	<-entered
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Millisecond)
	defer cancel()
	require.ErrorIs(t, wheel.Shutdown(ctx), context.DeadlineExceeded)
	wheel.Schedule("late", time.Millisecond, func() { t.Error("停止后执行了任务") })
	close(release)
	require.NoError(t, wheel.Shutdown(context.Background()))
}
