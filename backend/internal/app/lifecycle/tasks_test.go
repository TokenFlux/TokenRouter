package lifecycle

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// 停止时允许在途任务派生原有子任务，最后一次完成才封闭接收。
func TestTasksStopDrainsChildrenAndRejectsLaterWork(t *testing.T) {
	tasks := NewTasks()
	parent := make(chan struct{})
	child := make(chan struct{})
	spawned := make(chan struct{})
	var completed atomic.Int32
	require.True(t, tasks.Go("parent", func() {
		<-parent
		if !tasks.Go("child", func() { <-child; completed.Add(1) }) {
			t.Error("子任务在 drain 期间被拒绝")
		}
		close(spawned)
	}))
	result := make(chan error, 1)
	go func() { result <- tasks.Stop(context.Background()) }()
	close(parent)
	<-spawned
	select {
	case <-result:
		t.Fatal("子任务尚未完成却已返回")
	default:
	}
	close(child)
	require.NoError(t, <-result)
	require.EqualValues(t, 1, completed.Load())
	require.False(t, tasks.Go("after-stop", func() { t.Error("关闭后启动任务") }))
}

func TestTasksBarrierKeepsLaterConsumerSideEffects(t *testing.T) {
	tasks := NewTasks()
	require.NoError(t, tasks.Wait(context.Background()))
	done := make(chan struct{})
	require.True(t, tasks.Go("later-consumer", func() { close(done) }))
	require.NoError(t, tasks.Wait(context.Background()))
	<-done
	require.NoError(t, tasks.Stop(context.Background()))
}

func TestTasksTimeoutNamesUnfinishedWork(t *testing.T) {
	tasks := NewTasks()
	finish := make(chan struct{})
	require.True(t, tasks.Go("quota-persistence", func() { <-finish }))
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	err := tasks.Stop(ctx)
	require.True(t, errors.Is(err, context.DeadlineExceeded))
	require.Contains(t, err.Error(), "quota-persistence=1")
	close(finish)
	require.NoError(t, tasks.Stop(context.Background()))
}
