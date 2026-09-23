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

// 原生运行时完整构造后发布同一实例；构造不能查询空存储。
func TestAccountHealthRuntimePublishesOneNativeGraph(t *testing.T) {
	cfg := &config.Config{}
	counter := &healthRuntimeCounter{}
	runtime := provideAccountHealthRuntime(nil, nil, cfg, nil, nil, counter, nil, provideAccountRuntimeState())
	observer := provideUpstreamHealth(runtime)
	require.Empty(t, counter.resets)
	require.Same(t, runtime.Health, observer.Core)
	require.Same(t, runtime.Recovery, provideAccountRecovery(runtime))
	require.Same(t, runtime.Observer, observer)
	require.Same(t, runtime.Observer.Limits, observer.Limits)
	require.Same(t, runtime.Observer.Team, observer.Team)
	require.Same(t, runtime.Health, runtime.Observer.Limits.Health)
	require.Same(t, runtime.Health, runtime.Observer.Models.Health)
	observer.Core.ResetForbiddenCounter(t.Context(), 7)
	runtime.Health.ResetForbiddenCounter(t.Context(), 9)
	require.Equal(t, []int64{7, 9}, counter.resets)
}
