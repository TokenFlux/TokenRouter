package openai

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// 构造只取得可释放资源，后台任务在拥有者按需 Start 后才运行。
func TestWSPoolConstructionDoesNotStartWorkers(t *testing.T) {
	pool := NewWSConnPool(nil)
	t.Cleanup(pool.Close)
	require.False(t, pool.started)
	finished := make(chan struct{})
	go func() {
		pool.workerWg.Wait()
		close(finished)
	}()
	select {
	case <-finished:
	case <-time.After(time.Second):
		t.Fatal("构造期间启动了后台 worker")
	}
}

// 并发重复 Start 共享同一启动屏障；关闭后的入口不能再次创建连接或 worker。
func TestWSPoolRepeatedStartAndClose(t *testing.T) {
	pool := NewWSConnPool(nil)
	var callers sync.WaitGroup
	for range 16 {
		callers.Go(pool.Start)
	}
	callers.Wait()
	require.True(t, pool.started)
	for range 16 {
		callers.Go(pool.Close)
	}
	callers.Wait()
	pool.Start()
	require.True(t, pool.closed)
	_, err := pool.Acquire(context.Background(), WSAcquireRequest{
		Account: &WSPoolAccount{ID: 1, Type: "oauth", Concurrency: 1},
		WSURL:   "wss://example.invalid/responses",
	})
	require.ErrorIs(t, err, ErrWSConnClosed)
}

// 尚未启用的按需资源同样允许关闭，之后不能由迟到请求重新开启。
func TestWSPoolCloseBeforeStart(t *testing.T) {
	pool := NewWSConnPool(nil)
	pool.Close()
	pool.Start()
	require.False(t, pool.started)
	require.True(t, pool.closed)
}
