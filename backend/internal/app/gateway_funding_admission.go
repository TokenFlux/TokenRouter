package app

import (
	"github.com/TokenFlux/TokenRouter/internal/billing"
	"github.com/TokenFlux/TokenRouter/internal/config"
	"github.com/TokenFlux/TokenRouter/internal/gateway/admission"
	"github.com/TokenFlux/TokenRouter/internal/infra/telemetry/logging"
	"github.com/TokenFlux/TokenRouter/internal/scheduler"
)

// provideFundingAdmission 复用唯一权益缓存和现有 RPM 存储，模式仍在资金检查后读取。
func provideFundingAdmission(funds *billing.Eligibility, rpm scheduler.UserRPMCache, rates billing.UserGroupRateRepository, cfg *config.Config) *admission.FundingAdmission {
	limiter := scheduler.NewRPMAdmission(rpm, rates, scheduler.Diagnostics{Logf: logging.LegacyPrintf})
	return admission.NewFundingAdmission(funds, limiter, func() bool { return cfg.RunMode == config.RunModeSimple })
}
