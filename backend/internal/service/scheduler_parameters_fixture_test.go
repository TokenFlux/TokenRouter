package service

import (
	"github.com/TokenFlux/TokenRouter/internal/config"
	"github.com/TokenFlux/TokenRouter/internal/scheduler"
	"github.com/TokenFlux/TokenRouter/internal/scheduler/policy"
)

// schedulerParameterDefaultsForTest 只做进程配置投影，显式零值保持原意。
func schedulerParameterDefaultsForTest(cfg *config.Config) scheduler.ParameterDefaults {
	defaults := scheduler.DefaultParameters()
	if cfg == nil {
		return defaults
	}
	value := cfg.Gateway.AdvancedScheduler
	if value.LBTopK > 0 {
		defaults.TopK = value.LBTopK
	}
	weights := value.ScoreWeights
	defaults.Weights = policy.ScoreWeights{Priority: weights.Priority, Load: weights.Load, Queue: weights.Queue, ErrorRate: weights.ErrorRate, TTFT: weights.TTFT, Reset: weights.Reset, QuotaHeadroom: weights.QuotaHeadroom, Previous: weights.PreviousResponse, SessionSticky: weights.SessionSticky}
	defaults.Runtime.EwmaErrorRateAlpha = value.EWMAErrorRateAlpha
	defaults.Runtime.EwmaTTFTAlpha = value.EWMATTFTAlpha
	defaults.Runtime.StickyEscape = policy.NormalizeStickyEscape(policy.StickyEscapeConfig{Enabled: value.StickyEscapeEnabled, TtftMs: float64(value.StickyEscapeTTFTMs), ErrorRate: value.StickyEscapeErrorRate})
	return defaults
}

// withSchedulerParametersForTest 显式把原夹具的配置和设置替身交给原生参数实现。
func withSchedulerParametersForTest[T interface {
	*GatewayService | *OpenAIGatewayService | *GeminiMessagesCompatService | *AdvancedSchedulerScoreDiagnosticService
}](value T, configs ...*config.Config) T {
	var cfg *config.Config

	var target **scheduler.Parameters
	switch s := any(value).(type) {
	case *GatewayService:
		cfg, target = s.cfg, &s.schedulerParameters
	case *OpenAIGatewayService:
		cfg, target = s.cfg, &s.schedulerParameters
	case *GeminiMessagesCompatService:
		cfg, target = s.cfg, &s.schedulerParameters
	case *AdvancedSchedulerScoreDiagnosticService:
		target = &s.schedulerParameters
	}
	if *target != nil {
		return value
	}
	if len(configs) > 0 {
		cfg = configs[0]
	}
	*target = scheduler.NewParameters(scheduler.NewSettingsRuntime(scheduler.Diagnostics{}), nil, schedulerParameterDefaultsForTest(cfg))
	return value
}
