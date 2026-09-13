package app

import (
	"testing"

	"github.com/TokenFlux/TokenRouter/internal/config"
	"github.com/TokenFlux/TokenRouter/internal/service"
	"github.com/stretchr/testify/require"
)

// 实际装配必须把新反馈及兼容参数入口绑定到同一实例。
func TestSchedulerSharedStateBindsLegacyConsumers(t *testing.T) {
	previousSettings, previousSticky := service.SchedulerSettingsRuntime(), service.SchedulerStickyStats()
	t.Cleanup(func() {
		service.BindSchedulerSettingsRuntime(previousSettings)
		service.BindSchedulerStickyStats(previousSticky)
	})
	state := provideSchedulerSharedState()
	limits := provideLegacyRateLimitService(state, nil, nil, &config.Config{}, nil, nil, nil, nil, nil, nil)
	require.Same(t, state.Feedback, limits.SchedulerFeedback())
	require.Same(t, state.Settings, service.SchedulerSettingsRuntime())
	require.Same(t, state.Sticky, service.SchedulerStickyStats())
	state.Feedback.Report(51, false, nil)
	observed, _, _ := limits.SchedulerFeedback().Snapshot(51)
	require.Greater(t, observed, 0.0)
}
