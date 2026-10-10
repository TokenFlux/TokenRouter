package app

// 本文件检查 gateway_completion_billing.go、gateway_message_attempts.go 与 gateway_openai_attempts.go 共用的记录器。

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/TokenFlux/TokenRouter/internal/app/lifecycle"
	"github.com/TokenFlux/TokenRouter/internal/billing"
	"github.com/TokenFlux/TokenRouter/internal/config"
	"github.com/TokenFlux/TokenRouter/internal/provider"
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

// TestCompletionRuntimeOwnsIsolatedRatesAndSharedRecorders 检查两种完成流程各有独立倍率缓存，执行入口共用 app 构造的完成实例。
func TestCompletionRuntimeOwnsIsolatedRatesAndSharedRecorders(t *testing.T) {
	cfg := &config.Config{}
	repo := &completionRateScopeFixture{value: 2}
	rates := provideGatewayBillingRates(repo, cfg)
	health := &providerHealthRuntime{Health: provider.NewHealthService(nil, nil, provider.HealthOptions{})}
	tasks := lifecycle.NewTasks()
	recorders := ProvideGatewayCompletionRecorders(nil, rates, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, health, nil, tasks, cfg)
	require.Zero(t, repo.calls)
	prices := &billing.PriceResolver{}
	forward := messageAttemptBindings(nil, nil, nil, nil, nil, nil, nil, recorders, &messageHTTPBindings{}, nil, nil, nil, cfg, nil, nil, nil, nil, prices)
	require.Same(t, prices, forward.Pricing.Resolver)
	openai := provideOpenAIAttemptBindings(nil, nil, nil, nil, nil, nil, recorders, nil, nil, nil, nil, nil, nil, nil, nil, nil)
	cache := provideAnthropicPromptCache()
	text := openAITextExecution(cfg, nil, nil, nil, nil, nil, nil, nil, nil, nil, cache, func() time.Duration { return time.Hour }, nil)

	require.Same(t, recorders.Forward, forward.Recorder)
	require.Same(t, recorders.OpenAI, openai.Recorder)
	// 后续请求复用装配时创建的摘要缓存。
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
