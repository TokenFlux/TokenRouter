package scheduler

import (
	"context"

	"github.com/TokenFlux/TokenRouter/internal/scheduler/policy"
)

// ParameterDefaults 是组合根投影的静态参数；不持有完整配置或平台服务。
type ParameterDefaults struct {
	TopK    int
	Weights policy.ScoreWeights
	Runtime policy.RuntimeSettings
}

// DefaultParameters 保留未提供进程配置时的原缺省值。
func DefaultParameters() ParameterDefaults {
	return ParameterDefaults{TopK: 7, Weights: policy.ScoreWeights{Priority: 1, Load: 1, Queue: .7, ErrorRate: .8, TTFT: .5, Previous: 5, SessionSticky: 3}, Runtime: policy.RuntimeSettings{EwmaErrorRateAlpha: DefaultErrorRateAlpha, EwmaTTFTAlpha: DefaultTTFTAlpha, StickyEscape: policy.NormalizeStickyEscape(policy.StickyEscapeConfig{Enabled: true, TtftMs: 15000, ErrorRate: .5})}}
}

// Parameters 将参数来源固定在装配时，唯一缓存仍由 SettingsRuntime 持有。
type Parameters struct {
	defaults ParameterDefaults
	source   RuntimeSettingSource
	runtime  *SettingsRuntime
}

func NewParameters(runtime *SettingsRuntime, source RuntimeSettingSource, defaults ParameterDefaults) *Parameters {
	return &Parameters{runtime: runtime, source: source, defaults: defaults}
}

// Defaults 返回进程投影的值副本；运行时覆盖不修改静态参数。
func (p *Parameters) Defaults() ParameterDefaults {
	if p == nil {
		return DefaultParameters()
	}
	return p.defaults
}

// Runtime 保留动态设置的原读取时机和共享 TTL；独立零值入口只使用缺省值。
func (p *Parameters) Runtime(ctx context.Context) policy.RuntimeSettings {
	if p == nil {
		return (&SettingsRuntime{}).Load(ctx, nil, DefaultParameters().Runtime)
	}
	return p.runtime.Load(ctx, p.source, p.defaults.Runtime)
}

// Effective 在原读取位置应用分组覆盖，不接触分组存储或请求上下文。
func (p *Parameters) Effective(ctx context.Context, overrides policy.GroupAdvancedSchedulerOverrides) policy.EffectiveSettings {
	if ctx == nil {
		ctx = context.Background()
	}
	defaults := p.Defaults()
	return policy.ResolveEffective(defaults.TopK, defaults.Weights, p.Runtime(ctx), overrides)
}

// TopK 与 Weights 保留只读取全局设置的诊断入口。
func (p *Parameters) TopK(ctx context.Context) int {
	base := p.Defaults().TopK
	if base <= 0 {
		base = 7
	}
	if settings := p.Runtime(ctx); settings.LbTopKOverride > 0 {
		return settings.LbTopKOverride
	}
	return base
}
func (p *Parameters) Weights(ctx context.Context) policy.ScoreWeights {
	base := p.Defaults().Weights
	overridden := policy.ApplyGlobalWeightOverrides(base, p.Runtime(ctx).WeightOverrides)
	if !overridden.ValidGlobal() {
		return base
	}
	return overridden
}
