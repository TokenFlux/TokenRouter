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

// withSchedulerParametersForTest 只为仍有真实执行行为的 OpenAI 夹具装配原生选择端口。
func withSchedulerParametersForTest[T interface {
	*OpenAIGatewayService
}](value T, configs ...*config.Config) T {
	if source, ok := any(value).(*OpenAIGatewayService); ok {
		bindCompatibleSelectionFixture(source)
	}
	return value
}
