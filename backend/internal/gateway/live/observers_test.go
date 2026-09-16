package live

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// 原观察任务退出前，停止不能把依赖报告为可关闭；停止后不得重新登记。
func TestObserverStopCancelsAndWaitsForEnteredTask(t *testing.T) {
	var mu sync.Mutex
	var stopped bool
	var cancels map[string]context.CancelFunc
	var wg sync.WaitGroup
	state := ObserverState{Mutex: &mu, Stopped: &stopped, Cancels: &cancels, Wait: &wg}
	run, finish, ok := state.Begin("observer")
	require.True(t, ok)
	stopCtx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	stopDone := make(chan error, 1)
	go func() { stopDone <- state.Stop(stopCtx) }()
	select {
	case <-run.Done():
	case <-stopCtx.Done():
		t.Fatal("observer context not cancelled")
	}
	select {
	case <-stopDone:
		t.Fatal("stop completed before observer returned")
	default:
	}
	_, _, accepted := state.Begin("late")
	require.False(t, accepted)
	finish()
	require.NoError(t, <-stopDone)
	require.NoError(t, state.Stop(context.Background()))
}
