//go:build integration

package provider

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/TokenFlux/TokenRouter/internal/search"
	"github.com/TokenFlux/TokenRouter/internal/search/rediscache"
	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/require"
	tcredis "github.com/testcontainers/testcontainers-go/modules/redis"
)

// countingExecutor 核对取消后的尝试与代理故障分类，实际第一条请求仍通过 HTTP 执行。
type countingExecutor struct {
	*Executor
	calls, classifications atomic.Int64
}

func (e *countingExecutor) Search(ctx context.Context, cfg ProviderConfig, req SearchRequest) (*SearchResponse, error) {
	e.calls.Add(1)
	return e.Executor.Search(ctx, cfg, req)
}
func (e *countingExecutor) IsProxyError(err error) bool {
	e.classifications.Add(1)
	return e.Executor.IsProxyError(err)
}
func TestS10SearchRedisReservations(t *testing.T) {
	setup, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	container, err := tcredis.Run(setup, "redis:8.4-alpine")
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, container.Terminate(context.Background())) })
	address, err := container.ConnectionString(setup)
	require.NoError(t, err)
	opts, err := redis.ParseURL(address)
	require.NoError(t, err)
	rdb := redis.NewClient(opts)
	defer func() { _ = rdb.Close() }()
	for _, kind := range []string{"success", "failure", "cancel"} {
		t.Run(kind, func(t *testing.T) {
			require.NoError(t, rdb.FlushDB(context.Background()).Err())
			entered := make(chan struct{})
			release := make(chan struct{})
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if kind == "cancel" {
					close(entered)
					<-release
				}
				if kind == "failure" {
					w.WriteHeader(500)
					return
				}
				_, _ = w.Write([]byte(`{"web":{"results":[{"url":"https://fixture.invalid","title":"fixture","description":"fixture"}]}}`))
			}))
			defer server.Close()
			old := *braveSearchURL
			request, err := http.NewRequest(http.MethodGet, server.URL, nil)
			require.NoError(t, err)
			*braveSearchURL = *request.URL
			defer func() { *braveSearchURL = old }()
			executor := &countingExecutor{Executor: NewExecutor()}
			executor.clientCache[""] = server.Client()
			configs := []ProviderConfig{{Type: "brave", APIKey: "fixture", QuotaLimit: 10}}
			if kind == "cancel" {
				configs = append(configs, ProviderConfig{Type: "tavily", APIKey: "fixture"})
			}
			work := search.NewWorkGroup()
			manager := search.NewManager(configs, rediscache.New(rdb), executor, work)
			ctx, stop := context.WithCancel(context.Background())
			defer stop()
			done := make(chan error, 1)
			go func() { _, _, err := manager.SearchWithBestProvider(ctx, SearchRequest{Query: "fixture"}); done <- err }()
			if kind == "cancel" {
				<-entered
				used, err := manager.GetUsage(context.Background(), "brave")
				require.NoError(t, err)
				require.Equal(t, int64(1), used)
				stop()
			}
			err = <-done
			switch kind {
			case "cancel":
				close(release)
				require.ErrorIs(t, err, context.Canceled)
				require.Equal(t, int64(1), executor.calls.Load())
				require.Zero(t, executor.classifications.Load())
			case "failure":
				require.Error(t, err)
			default:
				require.NoError(t, err)
			}
			used, err := manager.GetUsage(context.Background(), "brave")
			require.NoError(t, err)
			if kind == "success" {
				require.Equal(t, int64(1), used)
			} else {
				require.Zero(t, used)
			}
			require.NoError(t, work.Stop(context.Background()))
			manager.Retire()
		})
	}
	t.Run("repair-missing-ttl-and-reset", func(t *testing.T) {
		require.NoError(t, rdb.FlushDB(context.Background()).Err())
		require.NoError(t, rdb.Set(context.Background(), "websearch:quota:brave", int64(2), 0).Err())
		state := rediscache.New(rdb)
		value, err := state.Increment(context.Background(), "brave", time.Hour)
		require.NoError(t, err)
		require.Equal(t, int64(3), value)
		ttl, err := rdb.TTL(context.Background(), "websearch:quota:brave").Result()
		require.NoError(t, err)
		require.Greater(t, ttl, 59*time.Minute)
		require.NoError(t, state.Reset(context.Background(), "brave"))
		value, err = state.Usage(context.Background(), "brave")
		require.NoError(t, err)
		require.Zero(t, value)
	})
}
