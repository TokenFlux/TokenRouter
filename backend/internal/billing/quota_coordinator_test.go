package billing

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// TestQuotaCoordinatorWaitCancellationAndCleanup 验证取消等待不会移除仍被使用的锁。
func TestQuotaCoordinatorWaitCancellationAndCleanup(t *testing.T) {
	coordinator := NewQuotaCoordinator()
	unlock, err := coordinator.Acquire(context.Background(), 7)
	require.NoError(t, err)
	ctx, cancel := context.WithCancel(context.Background())
	finished := make(chan error, 1)
	go func() {
		_, err := coordinator.Acquire(ctx, 7)
		finished <- err
	}()
	cancel()
	require.ErrorIs(t, <-finished, context.Canceled)
	coordinator.mu.Lock()
	require.Len(t, coordinator.users, 1)
	coordinator.mu.Unlock()
	other, err := coordinator.Acquire(context.Background(), 8)
	require.NoError(t, err, "不同用户不受阻塞")
	other()
	unlock()
	unlock()
	coordinator.mu.Lock()
	defer coordinator.mu.Unlock()
	require.Empty(t, coordinator.users)
}

// TestQuotaCoordinatorSerializesUsersAndOrdersBatches 验证反向输入不会死锁，同一用户始终只有一个持有者。
func TestQuotaCoordinatorSerializesUsersAndOrdersBatches(t *testing.T) {
	coordinator := NewQuotaCoordinator()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	var active atomic.Int32
	var overlaps atomic.Int32
	var failures atomic.Int32
	var wg sync.WaitGroup
	for i := 0; i < 40; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			ids := []int64{7, 8, 7}
			if i%2 == 0 {
				ids = []int64{8, 7}
			}
			unlock, err := coordinator.Acquire(ctx, ids...)
			if err != nil {
				failures.Add(1)
				return
			}
			if active.Add(1) != 1 {
				overlaps.Add(1)
			}
			active.Add(-1)
			unlock()
		}(i)
	}
	wg.Wait()
	require.Zero(t, failures.Load())
	require.Zero(t, overlaps.Load())
	require.Empty(t, coordinator.users)
}
