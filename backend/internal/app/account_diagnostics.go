package app

import (
	schedulerhttp "github.com/TokenFlux/TokenRouter/internal/scheduler/httpapi"
	"github.com/TokenFlux/TokenRouter/internal/service"
)

// provideAccountDiagnostics 保留原诊断装配与唯一调度反馈，执行算法留 S07。
func provideAccountDiagnostics(admin service.AdminService, concurrency *service.ConcurrencyService, limits *service.RateLimitService, gateway *service.GatewayService, openai *service.OpenAIGatewayService) *service.AdvancedSchedulerScoreDiagnosticService {
	core := service.NewAdvancedSchedulerScoreDiagnosticService(admin, concurrency, limits)
	core.SetSchedulingServices(gateway, openai)
	return core
}

// provideSchedulerDiagnosticsHTTP 直接将只读诊断用例装配到 scheduler HTTP。
func provideSchedulerDiagnosticsHTTP(core *service.AdvancedSchedulerScoreDiagnosticService) *schedulerhttp.DiagnosticsHandler {
	return schedulerhttp.NewDiagnosticsHandler(core)
}
