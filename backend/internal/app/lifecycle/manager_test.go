package lifecycle

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// 生产者退出后，消费者才能排空；数据库必须保留到消费者完成。
func TestManagerOrdersStartAndDrain(t *testing.T) {
	m := New()
	var events []string
	add := func(name string) { events = append(events, name) }
	m.Register(Hook{Name: "db", StopOrder: 100, Stop: func(context.Context) error { add("db-close"); return nil }})
	m.Register(Hook{Name: "producer", StartOrder: 20, StopOrder: 10,
		Start: func(context.Context) error { add("producer-start"); return nil },
		Stop:  func(context.Context) error { add("producer-stop"); return nil }})
	m.Register(Hook{Name: "consumer", StartOrder: 10, StopOrder: 20,
		Start: func(context.Context) error { add("consumer-start"); return nil },
		Stop:  func(context.Context) error { add("consumer-drain"); return nil }})
	require.Empty(t, events)
	require.NoError(t, m.Start(context.Background()))
	require.NoError(t, m.Start(context.Background()))
	require.NoError(t, m.Stop(context.Background()))
	require.NoError(t, m.Stop(context.Background()))
	require.Equal(t, []string{"consumer-start", "producer-start", "producer-stop", "consumer-drain", "db-close"}, events)
}

func TestManagerRollsBackPartialStartAndConstruction(t *testing.T) {
	for _, start := range []bool{false, true} {
		t.Run(map[bool]string{false: "construction", true: "startup"}[start], func(t *testing.T) {
			m := New()
			var stopped []string
			for i, name := range []string{"db", "redis"} {
				m.Register(Hook{Name: name, StartOrder: i, Stop: func(context.Context) error { stopped = append(stopped, name); return nil }})
			}
			failure := errors.New("partial start")
			m.Register(Hook{Name: "partial", StartOrder: 10, Start: func(context.Context) error { return failure }, Stop: func(context.Context) error { stopped = append(stopped, "partial"); return nil }})
			m.Register(Hook{Name: "never", StartOrder: 20, Start: func(context.Context) error { t.Error("unexpected start"); return nil }, Stop: func(context.Context) error { t.Error("unexpected stop"); return nil }})
			if start {
				require.ErrorIs(t, m.Start(context.Background()), failure)
			}
			require.NoError(t, m.Rollback(context.Background()))
			if start {
				require.Equal(t, []string{"partial", "redis", "db"}, stopped)
			} else {
				require.Equal(t, []string{"redis", "db"}, stopped)
			}
		})
	}
}

func TestManagerTimeoutKeepsDependenciesAndSharesResult(t *testing.T) {
	m := New()
	blocked := make(chan struct{})
	defer close(blocked)
	var calls atomic.Int32
	m.Register(Hook{Name: "blocked-drain", StopOrder: 10, Stop: func(context.Context) error { calls.Add(1); <-blocked; return nil }})
	m.Register(Hook{Name: "database", StopOrder: 20, Stop: func(context.Context) error { t.Error("database closed before drain"); return nil }})
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Millisecond)
	defer cancel()
	err := m.Stop(ctx)
	require.ErrorIs(t, err, context.DeadlineExceeded)
	require.Contains(t, err.Error(), "blocked-drain")
	require.Contains(t, err.Error(), "database")
	var wg sync.WaitGroup
	for range 8 {
		wg.Go(func() { require.Equal(t, err, m.Stop(context.Background())) })
	}
	wg.Wait()
	require.EqualValues(t, 1, calls.Load())
}

func TestManagerStopWaitsForPartialStartup(t *testing.T) {
	m := New()
	entered := make(chan struct{})
	finish := make(chan struct{})
	startResult := make(chan error, 1)
	m.Register(Hook{Name: "starting", Start: func(context.Context) error { close(entered); <-finish; return nil }, Stop: func(context.Context) error { t.Error("still starting"); return nil }})
	go func() { startResult <- m.Start(context.Background()) }()
	<-entered
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	require.ErrorIs(t, m.Stop(ctx), context.DeadlineExceeded)
	close(finish)
	require.NoError(t, <-startResult)
}

// 观察输出不能让控制面突破停止预算，也不能推进到依赖资源关闭。
func TestManagerReportingIsBounded(t *testing.T) {
	release := make(chan struct{})
	defer close(release)
	m := New(func(string, string, error) { <-release })
	m.Register(Hook{Name: "worker", StopOrder: 1, Stop: func(context.Context) error { return nil }})
	m.Register(Hook{Name: "database", StopOrder: 2, Stop: func(context.Context) error { t.Error("报告阻塞后仍关闭依赖"); return nil }})
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	err := m.Stop(ctx)
	require.ErrorIs(t, err, context.DeadlineExceeded)
	require.Contains(t, err.Error(), "report worker")
	require.Contains(t, err.Error(), "database")
}
