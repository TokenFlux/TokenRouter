package app

import "github.com/TokenFlux/TokenRouter/internal/service"

// provideAccountDiagnostics 保留原诊断装配与唯一调度反馈，执行算法留 S07。
func provideAccountDiagnostics(admin service.AdminService, concurrency *service.ConcurrencyService, limits *service.RateLimitService, gateway *service.GatewayService, openai *service.OpenAIGatewayService) *service.AdvancedSchedulerScoreDiagnosticService {
	core := service.NewAdvancedSchedulerScoreDiagnosticService(admin, concurrency, limits)
	core.SetSchedulingServices(gateway, openai)
	return core
}
