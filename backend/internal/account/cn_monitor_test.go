package account

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

type cnLifecycleStore struct {
	CNMonitorStore
	reads            atomic.Int32
	started, release chan struct{}
	ignore           bool
}

func (s *cnLifecycleStore) ListByPlatform(ctx context.Context, _ string) ([]Record, error) {
	if s.reads.Add(1) == 1 && s.started != nil {
		close(s.started)
	}
	if s.ignore {
		<-s.release
	} else if s.started != nil {
		<-ctx.Done()
	}
	return nil, nil
}

type cnNoQueries struct{ CNMonitorQueries }

// 原构造无后台上下文及停止后不能重启的断言跟随生命周期所有者迁入本包。
func TestS06CNMonitorStopPreventsLaterStart(t *testing.T) {
	store := &cnLifecycleStore{}
	disabled := NewCNUsageMonitor(store, cnNoQueries{}, CNMonitorOptions{})
	disabled.Start()
	require.Nil(t, disabled.cancel, "默认关闭时不得创建后台上下文")
	monitor := NewCNUsageMonitor(store, cnNoQueries{}, CNMonitorOptions{Enabled: true, Interval: time.Millisecond})
	monitor.Stop()
	monitor.Start()
	require.Nil(t, monitor.cancel)
	require.Zero(t, store.reads.Load())
	monitor.RunOnce(context.Background())
	require.Zero(t, store.reads.Load())
}
func TestCNMonitorWaitsFirstIntervalAndStopsInflight(t *testing.T) {
	store := &cnLifecycleStore{started: make(chan struct{})}
	monitor := NewCNUsageMonitor(store, cnNoQueries{}, CNMonitorOptions{Enabled: true, Interval: 30 * time.Millisecond})
	require.Zero(t, store.reads.Load())
	monitor.Start()
	monitor.Start()
	select {
	case <-store.started:
		t.Fatal("没有等待完整首周期")
	case <-time.After(5 * time.Millisecond):
	}
	waitUsageSignal(t, store.started)
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	require.NoError(t, monitor.StopContext(ctx))
	require.NoError(t, monitor.StopContext(ctx))
	reads := store.reads.Load()
	monitor.Start()
	monitor.RunOnce(context.Background())
	require.Equal(t, reads, store.reads.Load())
}
func TestCNMonitorStopReportsBlockedStorage(t *testing.T) {
	store := &cnLifecycleStore{started: make(chan struct{}), release: make(chan struct{}), ignore: true}
	monitor := NewCNUsageMonitor(store, cnNoQueries{}, CNMonitorOptions{Enabled: true, Interval: time.Millisecond})
	monitor.Start()
	waitUsageSignal(t, store.started)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Millisecond)
	defer cancel()
	err := monitor.StopContext(ctx)
	require.ErrorIs(t, err, context.DeadlineExceeded)
	require.Contains(t, err.Error(), "CN usage monitor")
	close(store.release)
	require.Same(t, err, monitor.StopContext(context.Background()))
}

type cnLeaseFixture struct {
	ok         bool
	err        error
	key, owner string
	ttl        time.Duration
	released   bool
}

func (s *cnLeaseFixture) TryAcquireLeaderLock(_ context.Context, key, owner string, ttl time.Duration) (bool, error) {
	s.key = key
	s.owner = owner
	s.ttl = ttl
	return s.ok, s.err
}
func (s *cnLeaseFixture) ReleaseLeaderLock(_ context.Context, key, owner string) error {
	s.released = key == s.key && owner == s.owner
	return nil
}
func TestCNMonitorLeasePreservesFallbackPolicy(t *testing.T) {
	for _, mode := range []string{"success", "contended", "redis_error", "no_backend"} {
		t.Run(mode, func(t *testing.T) {
			lock := &cnLeaseFixture{ok: mode == "success"}
			if mode == "redis_error" {
				lock.err = errors.New("redis unavailable")
			}
			dbCalls := 0
			o := CNMonitorOptions{InstanceID: "fixture", RoundTimeout: time.Minute, Leader: lock, Advisory: func(context.Context, string) (func(), bool) { dbCalls++; return func() {}, true }}
			if mode == "no_backend" {
				o.Leader = nil
				o.Advisory = nil
			}
			m := NewCNUsageMonitor(nil, nil, o)
			release, ok := m.acquireLease(context.Background())
			if mode == "contended" {
				require.False(t, ok)
				require.Nil(t, release)
				require.Zero(t, dbCalls)
				return
			}
			require.True(t, ok)
			release()
			if mode == "success" {
				require.True(t, lock.released)
				require.Equal(t, "cn:usage:monitor:leader", lock.key)
				require.Equal(t, "fixture", lock.owner)
				require.Equal(t, 90*time.Second, lock.ttl)
				require.Zero(t, dbCalls)
			}
			if mode == "redis_error" {
				require.Equal(t, 1, dbCalls)
			}
		})
	}
}
