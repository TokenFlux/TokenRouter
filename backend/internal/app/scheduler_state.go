package app

import (
	"time"

	gatewayprovider "github.com/TokenFlux/TokenRouter/internal/gateway/provider"

	"github.com/TokenFlux/TokenRouter/internal/account"
	"github.com/TokenFlux/TokenRouter/internal/config"
	logging "github.com/TokenFlux/TokenRouter/internal/infra/telemetry/logging"
	"github.com/TokenFlux/TokenRouter/internal/scheduler"
	"github.com/TokenFlux/TokenRouter/internal/service"
	usage "github.com/TokenFlux/TokenRouter/internal/usage"
)

// schedulerSharedState 由组合根持有跨平台唯一反馈、运行参数和粘性观测；构造不启动任务。
type schedulerSharedState struct {
	Feedback *scheduler.RuntimeStats
	Settings *scheduler.SettingsRuntime
	Sticky   *scheduler.StickyStats
}

func provideSchedulerSharedState() *schedulerSharedState {
	state := &schedulerSharedState{Feedback: scheduler.NewRuntimeStats(time.Now), Settings: scheduler.NewSettingsRuntime(scheduler.Diagnostics{
		Logf: logging.LegacyPrintf, Event: logging.Event},
	), Sticky: &scheduler.StickyStats{}}
	service.BindSchedulerSettingsRuntime(state.Settings)
	service.BindSchedulerStickyStats(state.Sticky)
	return state
}

// provideLegacyRateLimitService 保留平台健康与供应商限流装配，只注入新调度反馈。
func provideLegacyRateLimitService(
	shared *schedulerSharedState,
	accountRepo service.AccountRepository,
	usageRepo usage.UsageLogRepository,
	cfg *config.Config,
	geminiQuotaService *account.GeminiQuotaService,
	tempUnschedCache account.TempUnschedCache,
	timeoutCounterCache account.TimeoutCounterCache,
	openAI403CounterCache account.OpenAI403CounterCache,
	settingService *gatewayprovider.RuntimeReaders,
	tokenCacheInvalidator account.TokenCacheInvalidator,
) *service.RateLimitService {
	svc := service.NewRateLimitServiceWithScheduler(accountRepo, usageRepo, cfg, geminiQuotaService, tempUnschedCache, shared.Feedback)
	if healthCache, ok := tempUnschedCache.(account.OpenAIAPIKeyHealthCache); ok {
		svc.SetOpenAIAPIKeyHealthCache(healthCache)
	}
	svc.SetTimeoutCounterCache(timeoutCounterCache)
	svc.SetOpenAI403CounterCache(openAI403CounterCache)
	svc.SetSettingService(settingService)
	svc.SetTokenCacheInvalidator(tokenCacheInvalidator)
	return svc
}
