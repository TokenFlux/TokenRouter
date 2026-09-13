package app

import (
	"github.com/TokenFlux/TokenRouter/internal/app/legacybridge"
	"github.com/TokenFlux/TokenRouter/internal/config"
	"github.com/TokenFlux/TokenRouter/internal/infra/telemetry/logging"
	"github.com/TokenFlux/TokenRouter/internal/routing"
	"github.com/TokenFlux/TokenRouter/internal/routing/provider"
	"github.com/TokenFlux/TokenRouter/internal/service"
	"github.com/google/uuid"
	"time"
)

// provideGroupProbeRunner 注入原时区、实例租约标识和唯一执行器；cron 在生命周期 Start 才启动。
func provideGroupProbeRunner(repo routing.GroupAvailabilityProbeRepository, tests *service.AccountTestService, gateway *service.GatewayService, openai *service.OpenAIGatewayService, gemini *service.GeminiMessagesCompatService, cfg *config.Config) *routing.GroupAvailabilityProbeRunnerService {
	location := time.Local
	if cfg != nil {
		if parsed, err := time.LoadLocation(cfg.Timezone); err == nil && parsed != nil {
			location = parsed
		}
	}
	executor := legacybridge.RoutingProbe{Execution: service.NewGroupProbeExecution(tests, gateway, openai, gemini)}
	return routing.NewGroupAvailabilityProbeRunnerService(repo, executor, routing.GroupProbeOptions{InstanceID: uuid.NewString(), Now: time.Now, Schedule: provider.NewGroupProbeSchedule(location), Observe: func(format string, args ...any) {
		logging.LegacyPrintf("service.group_availability_probe", format, args...)
	}})
}
