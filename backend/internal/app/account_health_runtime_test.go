package app

import (
	"context"
	"testing"

	"github.com/TokenFlux/TokenRouter/internal/config"
	"github.com/stretchr/testify/require"
)

type healthRuntimeCounter struct{ resets []int64 }

func (*healthRuntimeCounter) IncrementOpenAI403Count(context.Context, int64, int) (int64, error) {
	return 1, nil
}

func (p *healthRuntimeCounter) ResetOpenAI403Count(_ context.Context, id int64) error {
	p.resets = append(p.resets, id)
	return nil
}

// 原生运行时先完整构造，再将同一实例交给旧执行端；构造不能查询空存储。
func TestAccountHealthRuntimePublishesOneNativeGraph(t *testing.T) {
	cfg := &config.Config{}
	counter := &healthRuntimeCounter{}
	runtime := provideAccountHealthRuntime(nil, nil, cfg, nil, nil, counter, nil, provideAccountRuntimeState())
	legacy := provideLegacyRateLimitService(nil, nil, cfg, nil, nil, counter, nil, nil, runtime)
	require.Empty(t, counter.resets)
	require.Same(t, runtime.Health, legacy.HealthCore())
	require.Same(t, runtime.Recovery, provideAccountRecovery(runtime))
	require.Same(t, runtime.Recovery, legacy.RecoveryCore())
	require.Same(t, runtime.Observer, legacy.UpstreamHealth())
	require.Same(t, runtime.Observer.Limits, legacy.RateLimitObserver())
	require.Same(t, runtime.Observer.Team, legacy.TeamLinkedHealth())
	require.Same(t, runtime.Health, runtime.Observer.Limits.Health)
	require.Same(t, runtime.Health, runtime.Observer.Models.Health)
	legacy.ResetOpenAI403Counter(t.Context(), 7)
	runtime.Health.ResetForbiddenCounter(t.Context(), 9)
	require.Equal(t, []int64{7, 9}, counter.resets)
}
