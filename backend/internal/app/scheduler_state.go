package app

import (
	"time"

	"github.com/TokenFlux/TokenRouter/internal/config"
	"github.com/TokenFlux/TokenRouter/internal/scheduler"
	"github.com/TokenFlux/TokenRouter/internal/service"
)

// schedulerSharedState 由组合根持有跨平台唯一反馈、运行参数和粘性观测；构造不启动任务。
type schedulerSharedState struct {
	Feedback *scheduler.RuntimeStats
	Settings *scheduler.SettingsRuntime
	Sticky   *scheduler.StickyStats
}

func provideSchedulerSharedState() *schedulerSharedState {
	state := &schedulerSharedState{Feedback: scheduler.NewRuntimeStats(time.Now), Settings: scheduler.NewSettingsRuntime(service.LegacySchedulerDiagnostics()), Sticky: &scheduler.StickyStats{}}
	service.BindSchedulerSettingsRuntime(state.Settings)
	service.BindSchedulerStickyStats(state.Sticky)
	return state
}

// provideLegacyRateLimitService 保留平台健康与供应商限流装配，只注入新调度反馈。
func provideLegacyRateLimitService(
	shared *schedulerSharedState,
	accountRepo service.AccountRepository,
	usageRepo service.UsageLogRepository,
	cfg *config.Config,
	geminiQuotaService *service.GeminiQuotaService,
	tempUnschedCache service.TempUnschedCache,
	timeoutCounterCache service.TimeoutCounterCache,
	openAI403CounterCache service.OpenAI403CounterCache,
	settingService *service.SettingService,
	tokenCacheInvalidator service.TokenCacheInvalidator,
) *service.RateLimitService {
	svc := service.NewRateLimitServiceWithScheduler(accountRepo, usageRepo, cfg, geminiQuotaService, tempUnschedCache, shared.Feedback)
	if healthCache, ok := tempUnschedCache.(service.OpenAIAPIKeyHealthCache); ok {
		svc.SetOpenAIAPIKeyHealthCache(healthCache)
	}
	svc.SetTimeoutCounterCache(timeoutCounterCache)
	svc.SetOpenAI403CounterCache(openAI403CounterCache)
	svc.SetSettingService(settingService)
	svc.SetTokenCacheInvalidator(tokenCacheInvalidator)
	return svc
}
