package app

import (
	"context"
	"testing"
	"time"

	"github.com/TokenFlux/TokenRouter/internal/account"
	"github.com/TokenFlux/TokenRouter/internal/app/lifecycle"
	"github.com/TokenFlux/TokenRouter/internal/billing"
	"github.com/TokenFlux/TokenRouter/internal/config"
	"github.com/stretchr/testify/require"
)

type completionRateScopeFixture struct {
	billing.UserGroupRateRepository
	calls int
	value float64
}

func (r *completionRateScopeFixture) GetByUserAndGroup(context.Context, int64, int64) (*float64, error) {
	r.calls++
	value := r.value
	return &value, nil
}

// 两条生产链各保留一份倍率缓存，旧执行端直接使用组合根的同一完成实例。
func TestCompletionRuntimeOwnsIsolatedRatesAndSharedRecorders(t *testing.T) {
	cfg := &config.Config{}
	repo := &completionRateScopeFixture{value: 2}
	rates := provideGatewayBillingRates(repo, cfg)
	health := &accountHealthRuntime{Health: account.NewHealthService(nil, nil, account.HealthOptions{})}
	tasks := lifecycle.NewTasks()
	recorders := ProvideGatewayCompletionRecorders(rates, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, health, nil, tasks, cfg)
	require.Zero(t, repo.calls)
	forward := messageAttemptBindings(nil, nil, nil, nil, nil, nil, nil, recorders, &messageHTTPBindings{}, nil, nil, nil, cfg, nil, nil)
	openai := provideOpenAIAttemptBindings(nil, nil, nil, nil, nil, nil, recorders, nil, nil, nil)
	cache := provideAnthropicPromptCache()
	text := openAITextExecution(cfg, nil, nil, nil, nil, nil, nil, nil, nil, nil, cache, func() time.Duration { return time.Hour }, nil)

	require.Same(t, recorders.Forward, forward.Recorder)
	require.Same(t, recorders.OpenAI, openai.Recorder)
	// 装配提供的摘要缓存由后续请求反复复用，不随完成器查询重建。
	bindings := text.PromptCache
	bindings.Bind(5, 7, "s:a-u:b", "cache-key", "", time.Hour)
	require.Same(t, bindings, text.PromptCache)
	cacheKey, _ := text.PromptCache.Find(5, 7, "s:a-u:b-a:c")
	require.Equal(t, "cache-key", cacheKey)
	require.NotSame(t, recorders.Forward, recorders.OpenAI)
	require.NotSame(t, rates.Forward, rates.OpenAI)
	require.Zero(t, repo.calls)
	require.Equal(t, 2.0, rates.Forward.Resolve(t.Context(), 7, 9, 1))
	repo.value = 3
	require.Equal(t, 3.0, rates.OpenAI.Resolve(t.Context(), 7, 9, 1))
	require.Equal(t, 2.0, rates.Forward.Resolve(t.Context(), 7, 9, 1))
	require.Equal(t, 2, repo.calls)
	require.Same(t, recorders.Forward, forward.Recorder)
	require.Same(t, recorders.OpenAI, openai.Recorder)
	require.NoError(t, tasks.Stop(t.Context()))
}
