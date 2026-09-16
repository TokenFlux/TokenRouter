// 本文件验证共享凭据构建的等待者隔离及应用退出边界。
package account

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func qoderSessionTestAccount() *Record {
	return &Record{ID: 17, Platform: PlatformQoder, Type: AccountTypeCosy, Credentials: map[string]any{"pat": "fixture"}}
}

func TestQoderSessionsCallerCancelKeepsSharedBuild(t *testing.T) {
	var sessions QoderSessions[*int]
	entered, proceed := make(chan struct{}), make(chan struct{})
	var calls atomic.Int32
	value := 42
	build := func(ctx context.Context, _ *Record) (*int, time.Time, error) {
		if calls.Add(1) == 1 {
			close(entered)
		}
		select {
		case <-proceed:
			return &value, time.Time{}, nil
		case <-ctx.Done():
			return nil, time.Time{}, ctx.Err()
		}
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { _, err := sessions.GetSession(ctx, qoderSessionTestAccount(), build); done <- err }()
	<-entered
	cancel()
	require.ErrorIs(t, <-done, context.Canceled)
	close(proceed)
	got, err := sessions.GetSession(context.Background(), qoderSessionTestAccount(), build)
	require.NoError(t, err)
	require.Same(t, &value, got)
	require.EqualValues(t, 1, calls.Load())
	require.NoError(t, sessions.StopContext(context.Background()))
}

func TestQoderSessionsStopCancelsAndRejectsNewWork(t *testing.T) {
	var sessions QoderSessions[*int]
	entered, finished := make(chan struct{}), make(chan struct{})
	caller := make(chan error, 1)
	go func() {
		_, err := sessions.GetSession(context.Background(), qoderSessionTestAccount(), func(ctx context.Context, _ *Record) (*int, time.Time, error) {
			close(entered)
			<-ctx.Done()
			close(finished)
			return nil, time.Time{}, ctx.Err()
		})
		caller <- err
	}()
	<-entered
	require.NoError(t, sessions.StopContext(context.Background()))
	select {
	case <-finished:
	default:
		t.Fatal("停止返回时构建仍未结束")
	}
	err := <-caller
	require.True(t, errors.Is(err, context.Canceled) || errors.Is(err, ErrQoderSessionsStopped))
	_, err = sessions.GetSession(context.Background(), qoderSessionTestAccount(), func(context.Context, *Record) (*int, time.Time, error) {
		t.Fatal("停止后不应构建")
		return nil, time.Time{}, nil
	})
	require.ErrorIs(t, err, ErrQoderSessionsStopped)
	require.NoError(t, sessions.StopContext(context.Background()))
}

func TestQoderSessionsStopReportsUnfinishedAndRejectsLateCache(t *testing.T) {
	var sessions QoderSessions[*int]
	entered, proceed, completed := make(chan struct{}), make(chan struct{}), make(chan struct{})
	caller := make(chan error, 1)
	go func() {
		_, err := sessions.GetSession(context.Background(), qoderSessionTestAccount(), func(context.Context, *Record) (*int, time.Time, error) {
			close(entered)
			<-proceed
			defer close(completed)
			value := 7
			return &value, time.Time{}, nil
		})
		caller <- err
	}()
	<-entered
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Millisecond)
	defer cancel()
	stopErr := sessions.StopContext(ctx)
	require.ErrorIs(t, stopErr, context.DeadlineExceeded)
	require.Contains(t, stopErr.Error(), "unfinished")
	require.ErrorIs(t, <-caller, ErrQoderSessionsStopped)
	close(proceed)
	<-completed
	// 等待共享构建的登记释放，确保检查覆盖迟到返回后的写回分支。
	require.Eventually(t, func() bool {
		sessions.activity.mu.Lock()
		defer sessions.activity.mu.Unlock()
		return len(sessions.activity.active) == 0
	}, time.Second, time.Millisecond)
	sessions.Mu.Lock()
	require.Empty(t, sessions.Sessions)
	sessions.Mu.Unlock()
	require.Equal(t, stopErr, sessions.StopContext(context.Background()))
}

func TestQoderAuthorizationStopWaitsAndRejectsCompletion(t *testing.T) {
	entered := make(chan struct{})
	auth := &QoderAuthorization[int]{Store: NewQoderAuthorizationStore[int]()}
	auth.Store.Set("fixture", &QoderAuthorizationSession[int]{State: "state", Flow: 1, CreatedAt: time.Now()})
	auth.Complete = func(ctx context.Context, _ int) (*QoderTokenInfo, bool, error) {
		close(entered)
		<-ctx.Done()
		return nil, false, ctx.Err()
	}
	auth.Start()
	auth.Start()
	done := make(chan error, 1)
	go func() { _, err := auth.Poll(context.Background(), "fixture", "state", nil); done <- err }()
	<-entered
	require.NoError(t, auth.StopContext(context.Background()))
	require.ErrorIs(t, <-done, context.Canceled)
	auth.Start()
	_, err := auth.Poll(context.Background(), "fixture", "state", nil)
	require.ErrorContains(t, err, "stopped")
	require.NoError(t, auth.StopContext(context.Background()))
}
