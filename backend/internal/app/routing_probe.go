package app

import (
	"time"

	"github.com/TokenFlux/TokenRouter/internal/account"

	"github.com/TokenFlux/TokenRouter/internal/config"
	"github.com/TokenFlux/TokenRouter/internal/gateway/provider/selection"
	"github.com/TokenFlux/TokenRouter/internal/infra/telemetry/logging"
	"github.com/TokenFlux/TokenRouter/internal/routing"
	"github.com/TokenFlux/TokenRouter/internal/routing/provider"
	"github.com/google/uuid"
)

// provideGroupProbeRunner 注入原时区、实例租约标识和唯一执行器；cron 在生命周期 Start 才启动。
func provideGroupProbeRunner(repo routing.GroupAvailabilityProbeRepository, tests *account.TestService, gateway *selection.Generic, openai *selection.Compatible, gemini *selection.Gemini, cfg *config.Config) *routing.GroupAvailabilityProbeRunnerService {
	location := time.Local
	if cfg != nil {
		if parsed, err := time.LoadLocation(cfg.Timezone); err == nil && parsed != nil {
			location = parsed
		}
	}
	executor := selection.NewProbe(tests, gateway, openai, gemini)
	return routing.NewGroupAvailabilityProbeRunnerService(repo, executor, routing.GroupProbeOptions{InstanceID: uuid.NewString(), Now: time.Now, Schedule: provider.NewGroupProbeSchedule(location), Observe: func(format string, args ...any) {
		logging.LegacyPrintf("service.group_availability_probe", format, args...)
	}})
}
