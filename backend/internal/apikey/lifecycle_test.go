package apikey

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

type lifecycleLookup struct {
	APIKeyRepository
	entered, release chan struct{}
}

func (r *lifecycleLookup) GetByKeyForAuth(context.Context, string) (*APIKey, error) {
	close(r.entered)
	<-r.release
	return nil, ErrAPIKeyNotFound
}

type lifecycleSubscription struct {
	APIKeyCache
	entered, cancelled chan struct{}
}

func (c *lifecycleSubscription) SubscribeAuthCacheInvalidation(ctx context.Context, _ func(string)) error {
	close(c.entered)
	<-ctx.Done()
	close(c.cancelled)
	return ctx.Err()
}

// TestAuthenticationStopWaitsBeforeClosingCaches 验证预算超时不提前关闭仍被在途认证使用的订阅与 L1。
func TestAuthenticationStopWaitsBeforeClosingCaches(t *testing.T) {
	repo := &lifecycleLookup{entered: make(chan struct{}), release: make(chan struct{})}
	var releaseOnce sync.Once
	unblock := func() { releaseOnce.Do(func() { close(repo.release) }) }
	t.Cleanup(unblock)
	cache := &lifecycleSubscription{entered: make(chan struct{}), cancelled: make(chan struct{})}
	opts := &Options{}
	opts.APIKeyAuth.L1Size = 1024
	opts.APIKeyAuth.L1TTLSeconds = 60
	service := NewAPIKeyService(repo, nil, nil, nil, nil, cache, opts)
	t.Cleanup(func() { unblock(); service.Stop() })
	require.Nil(t, service.authCacheL1.Load(), "构造不能启动 Ristretto 或订阅")
	select {
	case <-cache.entered:
		t.Fatal("构造启动了订阅")
	default:
	}
	service.Start()
	first := service.authCacheL1.Load()
	require.NotNil(t, first)
	service.Start()
	require.Same(t, first, service.authCacheL1.Load(), "重复启动不能复制缓存状态")
	require.True(t, first.Set("sentinel", true, 1))
	first.Wait()
	_, present := first.Get("sentinel")
	require.True(t, present, "先确认哨兵实际被缓存接纳")
	select {
	case <-cache.entered:
	case <-time.After(time.Second):
		t.Fatal("订阅未启动")
	}
	result := make(chan error, 1)
	go func() { _, err := service.GetByKey(context.Background(), "sk-lifecycle"); result <- err }()
	select {
	case <-repo.entered:
	case <-time.After(time.Second):
		t.Fatal("认证未进入仓储")
	}
	budget, cancel := context.WithTimeout(context.Background(), 10*time.Millisecond)
	defer cancel()
	require.ErrorIs(t, service.StopContext(budget), context.DeadlineExceeded)
	_, err := service.GetByKey(context.Background(), "sk-new")
	require.ErrorIs(t, err, ErrAuthenticationStopped)
	_, ok := first.Get("sentinel")
	require.True(t, ok, "在途认证退出前不能关闭缓存")
	select {
	case <-cache.cancelled:
		t.Fatal("在途认证退出前取消了订阅")
	default:
	}
	unblock()
	require.ErrorIs(t, <-result, ErrAPIKeyNotFound)
	finish, cancelFinish := context.WithTimeout(context.Background(), time.Second)
	defer cancelFinish()
	require.NoError(t, service.StopContext(finish))
	require.NoError(t, service.StopContext(finish))
	select {
	case <-cache.cancelled:
	default:
		t.Fatal("停止没有等待订阅退出")
	}
	_, ok = first.Get("sentinel")
	require.False(t, ok)
	service.Start()
	require.Same(t, first, service.authCacheL1.Load(), "停止后的实例不得重启")
	service.InvalidateAuthCacheByKey(context.Background(), "sk-after-stop")
	require.ErrorIs(t, service.TouchLastUsed(context.Background(), 1), ErrAuthenticationStopped)
}

// TestAuthenticationConcurrentStartStop 覆盖停止先于启动及重复调用，不允许关闭后创建新订阅。
func TestAuthenticationConcurrentStartStop(t *testing.T) {
	for range 10 {
		service := NewAPIKeyService(nil, nil, nil, nil, nil, nil, nil)
		var callers sync.WaitGroup
		for range 4 {
			callers.Add(1)
			go func() { defer callers.Done(); service.Start(); service.Stop() }()
		}
		callers.Wait()
		require.True(t, service.operations.isStopping())
	}
}

type lifecycleOutbox struct {
	AuthCacheInvalidationOutboxRepository
	entered, release chan struct{}
	claims           atomic.Int64
}

func (r *lifecycleOutbox) Claim(ctx context.Context, _ string, _ int, _ time.Duration) ([]AuthCacheInvalidationEvent, error) {
	r.claims.Add(1)
	close(r.entered)
	<-r.release
	return nil, ctx.Err()
}

// TestOutboxStopWaitsForClaim 模拟忽略取消的旧存储调用，停止预算仍必须有界返回并停止后续认领。
func TestOutboxStopWaitsForClaim(t *testing.T) {
	repo := &lifecycleOutbox{entered: make(chan struct{}), release: make(chan struct{})}
	var once sync.Once
	unblock := func() { once.Do(func() { close(repo.release) }) }
	t.Cleanup(unblock)
	worker := NewAuthCacheInvalidationWorker(repo, &lifecycleSubscription{})
	t.Cleanup(func() { unblock(); worker.Stop() })
	require.Zero(t, repo.claims.Load())
	worker.Start()
	select {
	case <-repo.entered:
	case <-time.After(time.Second):
		t.Fatal("worker 未认领")
	}
	budget, cancel := context.WithTimeout(context.Background(), 10*time.Millisecond)
	defer cancel()
	require.True(t, errors.Is(worker.StopContext(budget), context.DeadlineExceeded))
	worker.Start()
	unblock()
	finish, cancelFinish := context.WithTimeout(context.Background(), time.Second)
	defer cancelFinish()
	require.NoError(t, worker.StopContext(finish))
	require.NoError(t, worker.StopContext(finish))
	require.Equal(t, int64(1), repo.claims.Load())
	require.False(t, worker.running.Load())
}
