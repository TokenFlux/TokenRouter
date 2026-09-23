package app

import (
	"time"

	"github.com/TokenFlux/TokenRouter/internal/settings"

	"github.com/TokenFlux/TokenRouter/internal/config"
	logging "github.com/TokenFlux/TokenRouter/internal/infra/telemetry/logging"
	"github.com/TokenFlux/TokenRouter/internal/scheduler"
)

// schedulerSharedState 由组合根持有跨平台唯一反馈、运行参数和粘性观测；构造不启动任务。
type schedulerSharedState struct {
	Feedback   *scheduler.RuntimeStats
	Settings   *scheduler.SettingsRuntime
	Parameters *scheduler.Parameters
	Sticky     *scheduler.StickyStats
}

func provideSchedulerSharedState(cfg *config.Config, source settings.Repository) *schedulerSharedState {
	state := &schedulerSharedState{Feedback: scheduler.NewRuntimeStats(time.Now), Settings: scheduler.NewSettingsRuntime(scheduler.Diagnostics{
		Logf: logging.LegacyPrintf, Event: logging.Event},
	), Sticky: &scheduler.StickyStats{}}
	state.Parameters = scheduler.NewParameters(state.Settings, source, schedulerParameterDefaults(cfg))
	return state
}
