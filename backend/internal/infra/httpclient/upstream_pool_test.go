package httpclient

import (
	"errors"
	"io"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// upstreamTestSettings 保留旧 nil 配置的技术参数，用于机制测试夹具。
func upstreamTestSettings() UpstreamSettings {
	return UpstreamSettings{
		MaxIdleConns:          240,
		MaxIdleConnsPerHost:   120,
		MaxConnsPerHost:       240,
		IdleConnTimeout:       90 * time.Second,
		ResponseHeaderTimeout: 300 * time.Second,
	}
}

type poolRoundTripFunc func(*http.Request) (*http.Response, error)

func (f poolRoundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) {
	return f(r)
}

// poolTestOptions 替换实际网络，仍通过正式 Do 执行获取、结果通知和 Body 释放。
func poolTestOptions(id int64, captured **http.Client) UpstreamRequestOptions {
	return UpstreamRequestOptions{AccountID: id, Isolation: "account_proxy", MaxClients: 2, IdleTTL: 15 * time.Minute, Settings: upstreamTestSettings(), PrepareClient: func(client *http.Client) *http.Client {
		if captured != nil {
			*captured = client
		}
		clone := *client
		clone.Transport = poolRoundTripFunc(func(req *http.Request) (*http.Response, error) {
			return &http.Response{
				StatusCode: 200,
				Header:     make(http.Header),
				Body:       io.NopCloser(strings.NewReader("response")),
				Request:    req,
			}, nil
		})
		return &clone
	}}
}
func poolTestRequest(t *testing.T) *http.Request {
	t.Helper()
	req, err := http.NewRequestWithContext(t.Context(), http.MethodGet, "https://example.com/", nil)
	require.NoError(t, err)
	return req
}

// TestUpstreamPoolActiveLimitAndCloseOnce 通过公开执行验证在途保护及重复关闭后的唯一释放。
func TestUpstreamPoolActiveLimitAndCloseOnce(t *testing.T) {
	pool := NewUpstreamPool()
	one := poolTestOptions(1, nil)
	one.MaxClients = 1
	held, err := pool.Do(poolTestRequest(t), one)
	require.NoError(t, err)
	two := poolTestOptions(2, nil)
	two.MaxClients = 1
	_, err = pool.Do(poolTestRequest(t), two)
	require.ErrorIs(t, err, ErrUpstreamClientLimitReached)
	require.NoError(t, held.Body.Close())
	require.NoError(t, held.Body.Close())
	next, err := pool.Do(poolTestRequest(t), two)
	require.NoError(t, err)
	require.NoError(t, next.Body.Close())
}

// TestUpstreamPoolFailureReleasesEntry 验证执行失败立即释放，且通知外层的时序早于释放。
func TestUpstreamPoolFailureReleasesEntry(t *testing.T) {
	pool := NewUpstreamPool()
	opts := poolTestOptions(1, nil)
	opts.MaxClients = 1
	failure := errors.New("transport failed")
	observed := false
	opts.PrepareClient = func(c *http.Client) *http.Client {
		clone := *c
		clone.Transport = poolRoundTripFunc(func(*http.Request) (*http.Response, error) {
			return nil, failure
		})
		return &clone
	}
	// 通知内的第二次获取同样使用单条目上限。
	opts.ObserveResult = func(err error) {
		require.ErrorIs(t, err, failure)
		observed = true
		probe := poolTestOptions(2, nil)
		probe.MaxClients = 1
		_, acquireErr := pool.acquire(probe)
		require.ErrorIs(t, acquireErr, ErrUpstreamClientLimitReached)
	}
	_, err := pool.Do(poolTestRequest(t), opts)
	require.ErrorIs(t, err, failure)
	require.True(t, observed)
	next := poolTestOptions(2, nil)
	next.MaxClients = 1
	resp, err := pool.Do(poolTestRequest(t), next)
	require.NoError(t, err)
	require.NoError(t, resp.Body.Close())
}

// TestUpstreamPoolIsolationAndConfigurationChange 保留账号、代理和配置变化对复用身份的影响。
func TestUpstreamPoolIsolationAndConfigurationChange(t *testing.T) {
	for _, isolation := range []string{"proxy", "account", "account_proxy"} {
		t.Run(isolation, func(t *testing.T) {
			pool := NewUpstreamPool()
			var first, again, other *http.Client
			run := func(id int64, proxy string, capture **http.Client) {
				opts := poolTestOptions(id, capture)
				opts.Isolation = isolation
				opts.ProxyURL = proxy
				resp, err := pool.Do(poolTestRequest(t), opts)
				require.NoError(t, err)
				require.NoError(t, resp.Body.Close())
			}
			run(1, "http://proxy-a:8080", &first)
			run(1, "http://proxy-a:8080/", &again)
			require.Same(t, first, again)
			run(2, "http://proxy-a:8080", &other)
			if isolation == "proxy" {
				require.Same(t, first, other)
			} else {
				require.NotSame(t, first, other)
			}
			run(1, "http://proxy-b:8080", &other)
			require.NotSame(t, first, other)
		})
	}
	pool := NewUpstreamPool()
	var first, second *http.Client
	opts := poolTestOptions(1, &first)
	resp, err := pool.Do(poolTestRequest(t), opts)
	require.NoError(t, err)
	require.NoError(t, resp.Body.Close())
	opts = poolTestOptions(1, &second)
	opts.Settings.ResponseHeaderTimeout = time.Second
	resp, err = pool.Do(poolTestRequest(t), opts)
	require.NoError(t, err)
	require.NoError(t, resp.Body.Close())
	require.NotSame(t, first, second)
}

// TestUpstreamPoolEvictsOldestIdle 保留旧机制测试的确定性时间设置，避免依赖 sleep 验证 LRU。
func TestUpstreamPoolEvictsOldestIdle(t *testing.T) {
	pool := NewUpstreamPool()
	a, err := pool.acquire(poolTestOptions(1, nil))
	require.NoError(t, err)
	b, err := pool.acquire(poolTestOptions(2, nil))
	require.NoError(t, err)
	atomic.StoreInt64(&a.inFlight, 0)
	atomic.StoreInt64(&b.inFlight, 0)
	atomic.StoreInt64(&a.lastUsed, time.Now().Add(-2*time.Minute).UnixNano())
	atomic.StoreInt64(&b.lastUsed, time.Now().Add(-time.Minute).UnixNano())
	_, err = pool.acquire(poolTestOptions(3, nil))
	require.NoError(t, err)
	require.Len(t, pool.clients, 2)
	for _, entry := range pool.clients {
		require.NotSame(t, a, entry)
	}
}

// TestUpstreamPoolIdleTTLDoesNotEvictActive 保留旧 TTL 保护场景，在途条目即使时间过期也不能逐出。
func TestUpstreamPoolIdleTTLDoesNotEvictActive(t *testing.T) {
	pool := NewUpstreamPool()
	opts := poolTestOptions(1, nil)
	opts.IdleTTL = time.Second
	a, err := pool.acquire(opts)
	require.NoError(t, err)
	atomic.StoreInt64(&a.lastUsed, time.Now().Add(-2*time.Minute).UnixNano())
	opts.AccountID = 2
	_, err = pool.acquire(opts)
	require.NoError(t, err)
	found := false
	for _, entry := range pool.clients {
		if entry == a {
			found = true
		}
	}
	require.True(t, found)
}

// TestUpstreamPoolConcurrentRelease 保证并发复用和每个响应的重复关闭不会留下负计数。
func TestUpstreamPoolConcurrentRelease(t *testing.T) {
	pool := NewUpstreamPool()
	var group sync.WaitGroup
	errs := make(chan error, 64)
	for range 16 {
		group.Add(1)
		go func() {
			defer group.Done()
			opts := poolTestOptions(1, nil)
			opts.MaxClients = 1
			resp, err := pool.Do(poolTestRequest(t), opts)
			if err != nil {
				errs <- err
				return
			}
			if err = resp.Body.Close(); err != nil {
				errs <- err
			}
			if err = resp.Body.Close(); err != nil {
				errs <- err
			}
		}()
	}
	group.Wait()
	close(errs)
	for err := range errs {
		require.NoError(t, err)
	}
	opts := poolTestOptions(2, nil)
	opts.MaxClients = 1
	resp, err := pool.Do(poolTestRequest(t), opts)
	require.NoError(t, err)
	require.NoError(t, resp.Body.Close())
}
