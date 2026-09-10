package httpclient

import (
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/require"
)

// redirectTestServer 用真实重定向统计目标访问，避免只检查回调字段而漏掉执行行为。
func redirectTestServer(t *testing.T) (string, *atomic.Int64) {
	t.Helper()
	hits := new(atomic.Int64)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/start" {
			http.Redirect(w, r, "/target", http.StatusFound)
			return
		}
		hits.Add(1)
		w.WriteHeader(http.StatusNoContent)
	}))
	t.Cleanup(server.Close)
	return server.URL + "/start", hits
}

// TestUpstreamPoolRedirectPolicyIsPerRequest 覆盖缓存命中后的策略切换、清空和失败释放。
func TestUpstreamPoolRedirectPolicyIsPerRequest(t *testing.T) {
	target, hits := redirectTestServer(t)
	pool := NewUpstreamPool()
	blocked := errors.New("redirect blocked for this request")
	var previous *http.Client
	for _, step := range []struct {
		name  string
		block bool
	}{
		{name: "default"},
		{name: "block_after_default", block: true},
		{name: "clear_after_block"},
		{name: "block_again", block: true},
	} {
		t.Run(step.name, func(t *testing.T) {
			opts := UpstreamRequestOptions{Isolation: "account", AccountID: 1, MaxClients: 1}
			if step.block {
				opts.CheckRedirect = func(*http.Request, []*http.Request) error {
					return blocked
				}
			}
			var current *http.Client
			opts.PrepareClient = func(client *http.Client) *http.Client {
				current = client
				return client
			}
			before := hits.Load()
			req, err := http.NewRequestWithContext(t.Context(), http.MethodGet, target, nil)
			require.NoError(t, err)
			resp, err := pool.Do(req, opts)
			if resp != nil {
				require.NoError(t, resp.Body.Close())
			}
			if step.block {
				require.ErrorIs(t, err, blocked)
				require.Equal(t, before, hits.Load(), "被拒绝的重定向不能访问目标")
			} else {
				require.NoError(t, err)
				require.Equal(t, http.StatusNoContent, resp.StatusCode)
				require.Equal(t, before+1, hits.Load())
			}
			if previous != nil {
				require.NotSame(t, previous, current, "每个请求应有独立的客户端策略")
				require.Same(t, previous.Transport, current.Transport, "切换策略仍复用同一 transport")
			}
			previous = current
		})
	}

	// 最后一次重定向失败必须释放在途计数，单条目池才能接纳另一个账号。
	req, err := http.NewRequestWithContext(t.Context(), http.MethodGet, target, nil)
	require.NoError(t, err)
	resp, err := pool.Do(req, UpstreamRequestOptions{Isolation: "account", AccountID: 2, MaxClients: 1})
	require.NoError(t, err)
	require.NoError(t, resp.Body.Close())
}

// TestUpstreamPoolPrepareClientOverridesRequestPolicy 保留外层适配的覆盖顺序，并隔离其修改。
func TestUpstreamPoolPrepareClientOverridesRequestPolicy(t *testing.T) {
	target, hits := redirectTestServer(t)
	pool := NewUpstreamPool()
	opts := UpstreamRequestOptions{
		CheckRedirect: func(*http.Request, []*http.Request) error {
			return errors.New("base policy must be overridden")
		},
		PrepareClient: func(client *http.Client) *http.Client {
			require.NotNil(t, client.CheckRedirect, "基础回调应在适配前设置")
			client.CheckRedirect = func(*http.Request, []*http.Request) error {
				return http.ErrUseLastResponse
			}
			return client
		},
	}
	req, err := http.NewRequestWithContext(t.Context(), http.MethodGet, target, nil)
	require.NoError(t, err)
	resp, err := pool.Do(req, opts)
	require.NoError(t, err)
	require.NoError(t, resp.Body.Close())
	require.Equal(t, http.StatusFound, resp.StatusCode)
	require.Zero(t, hits.Load())

	// 下一次默认请求不得继承适配器对前一个请求客户端所做的修改。
	req, err = http.NewRequestWithContext(t.Context(), http.MethodGet, target, nil)
	require.NoError(t, err)
	resp, err = pool.Do(req, UpstreamRequestOptions{})
	require.NoError(t, err)
	require.NoError(t, resp.Body.Close())
	require.Equal(t, http.StatusNoContent, resp.StatusCode)
	require.Equal(t, int64(1), hits.Load())
}

// TestUpstreamPoolConcurrentRedirectPolicies 验证共享 transport 下的并发策略互不污染。
func TestUpstreamPoolConcurrentRedirectPolicies(t *testing.T) {
	target, hits := redirectTestServer(t)
	pool := NewUpstreamPool()
	blocked := errors.New("redirect blocked")
	const requests = 24
	var group sync.WaitGroup
	errs := make(chan error, requests)
	start := make(chan struct{})
	for i := range requests {
		group.Go(func() {
			<-start
			opts := UpstreamRequestOptions{}
			if i%2 == 0 {
				opts.CheckRedirect = func(*http.Request, []*http.Request) error {
					return blocked
				}
			}
			req, err := http.NewRequestWithContext(t.Context(), http.MethodGet, target, nil)
			if err != nil {
				errs <- err
				return
			}
			resp, err := pool.Do(req, opts)
			if resp != nil {
				if closeErr := resp.Body.Close(); closeErr != nil {
					errs <- closeErr
					return
				}
			}
			if i%2 == 0 {
				if !errors.Is(err, blocked) {
					errs <- fmt.Errorf("请求 %d 应阻断重定向，实际错误：%v", i, err)
				}
			} else if err != nil {
				errs <- err
			}
		})
	}
	close(start)
	group.Wait()
	close(errs)
	for err := range errs {
		require.NoError(t, err)
	}
	require.Equal(t, int64(requests/2), hits.Load())
}
