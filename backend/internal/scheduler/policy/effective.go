// 本文件维护 policy 的所属能力；兼容入口复用唯一实现。
package policy

import (
	fmt "fmt"
	math "math"
)

// EffectiveSettings 是完成全局与分组覆盖合并后的请求级配置。
// 分组字段优先级最高；缺失字段继续继承设置仓库和静态配置的结果。
type EffectiveSettings struct {
	StickyWeightedEnabled       bool
	SubscriptionPriorityEnabled bool
	TopK                        int
	Weights                     ScoreWeights
	Feedback                    FeedbackConfig
	StickyEscape                StickyEscapeConfig
}

// ValidateGroupOverrides 校验分组稀疏覆盖的单字段边界。
// 基础权重允许全部显式设为零，此时 Top-K 使用稳定并列规则并等权抽样。
func ValidateGroupOverrides(overrides GroupAdvancedSchedulerOverrides) error {
	if overrides.LBTopK != nil && *overrides.LBTopK <= 0 {
		return fmt.Errorf("lb_top_k must be a positive integer")
	}
	for _, item := range []struct {
		name  string
		value *float64
	}{{"ewma_error_rate_alpha", overrides.EWMAErrorRateAlpha}, {"ewma_ttft_alpha", overrides.EWMATTFTAlpha}} {
		if item.value == nil {
			continue
		}
		if *item.value <= 0 || *item.value > 1 || math.IsNaN(*item.value) || math.IsInf(*item.value, 0) {
			return fmt.Errorf("%s must be between 0 and 1", item.name)
		}
	}
	if overrides.StickyEscapeTTFTMs != nil && *overrides.StickyEscapeTTFTMs <= 0 {
		return fmt.Errorf("sticky_escape_ttft_ms must be positive")
	}
	if overrides.StickyEscapeErrorRate != nil &&
		(*overrides.StickyEscapeErrorRate < 0 || *overrides.StickyEscapeErrorRate > 1 ||
			math.IsNaN(*overrides.StickyEscapeErrorRate) || math.IsInf(*overrides.StickyEscapeErrorRate, 0)) {
		return fmt.Errorf("sticky_escape_error_rate must be between 0 and 1")
	}

	Weights := []struct {
		name  string
		value *float64
	}{
		{"weight_priority", overrides.WeightPriority},
		{"weight_load", overrides.WeightLoad},
		{"weight_queue", overrides.WeightQueue},
		{"weight_error_rate", overrides.WeightErrorRate},
		{"weight_ttft", overrides.WeightTTFT},
		{"weight_reset", overrides.WeightReset},
		{"weight_quota_headroom", overrides.WeightQuotaHeadroom},
		{"weight_previous_response", overrides.WeightPreviousResponse},
		{"weight_session_sticky", overrides.WeightSessionSticky},
	}
	for _, item := range Weights {
		if item.value == nil {
			continue
		}
		if *item.value < 0 || math.IsNaN(*item.value) || math.IsInf(*item.value, 0) {
			return fmt.Errorf("%s must be a non-negative finite number", item.name)
		}
	}

	return nil
}

// ValidateEffectiveWeights 校验合并后的完整权重。
// 基础权重总和可以为零，但基础和完整总和都必须保持有限，避免评分出现 NaN 或 Inf。
func ValidateEffectiveWeights(Weights ScoreWeights) error {
	values := []struct {
		name  string
		value float64
	}{
		{"weight_priority", Weights.Priority},
		{"weight_load", Weights.Load},
		{"weight_queue", Weights.Queue},
		{"weight_error_rate", Weights.ErrorRate},
		{"weight_ttft", Weights.TTFT},
		{"weight_reset", Weights.Reset},
		{"weight_quota_headroom", Weights.QuotaHeadroom},
		{"weight_previous_response", Weights.Previous},
		{"weight_session_sticky", Weights.SessionSticky},
	}
	for _, item := range values {
		if item.value < 0 || math.IsNaN(item.value) || math.IsInf(item.value, 0) {
			return fmt.Errorf("%s must be a non-negative finite number", item.name)
		}
	}

	resolved := Weights
	if baseSum := resolved.BaseWeightSum(); math.IsNaN(baseSum) || math.IsInf(baseSum, 0) {
		return fmt.Errorf("base-weight sum must be finite")
	}
	if totalSum := resolved.TotalWeightSum(); math.IsNaN(totalSum) || math.IsInf(totalSum, 0) {
		return fmt.Errorf("total-weight sum must be finite")
	}
	return nil
}

// HasWeightOverrides 判断是否需要读取全局权重完成合并校验。
func HasWeightOverrides(overrides GroupAdvancedSchedulerOverrides) bool {
	return overrides.WeightPriority != nil ||
		overrides.WeightLoad != nil ||
		overrides.WeightQueue != nil ||
		overrides.WeightErrorRate != nil ||
		overrides.WeightTTFT != nil ||
		overrides.WeightReset != nil ||
		overrides.WeightQuotaHeadroom != nil ||
		overrides.WeightPreviousResponse != nil ||
		overrides.WeightSessionSticky != nil
}

// ApplyGroupWeightOverrides 只替换分组显式提供的权重字段。
func ApplyGroupWeightOverrides(
	Weights ScoreWeights,
	overrides GroupAdvancedSchedulerOverrides,
) ScoreWeights {
	if overrides.WeightPriority != nil {
		Weights.Priority = *overrides.WeightPriority
	}
	if overrides.WeightLoad != nil {
		Weights.Load = *overrides.WeightLoad
	}
	if overrides.WeightQueue != nil {
		Weights.Queue = *overrides.WeightQueue
	}
	if overrides.WeightErrorRate != nil {
		Weights.ErrorRate = *overrides.WeightErrorRate
	}
	if overrides.WeightTTFT != nil {
		Weights.TTFT = *overrides.WeightTTFT
	}
	if overrides.WeightReset != nil {
		Weights.Reset = *overrides.WeightReset
	}
	if overrides.WeightQuotaHeadroom != nil {
		Weights.QuotaHeadroom = *overrides.WeightQuotaHeadroom
	}
	if overrides.WeightPreviousResponse != nil {
		Weights.Previous = *overrides.WeightPreviousResponse
	}
	if overrides.WeightSessionSticky != nil {
		Weights.SessionSticky = *overrides.WeightSessionSticky
	}
	return Weights
}

// ResolveEffective 以全局生效配置为基线合并分组覆盖。
// 分组显式零值保持生效，不因最终基础权重全零而静默恢复全局参数。
func ResolveEffective(
	baseTopK int,
	baseWeights ScoreWeights,
	global RuntimeSettings,
	overrides GroupAdvancedSchedulerOverrides,
) EffectiveSettings {
	if baseTopK <= 0 {
		baseTopK = 7
	}
	globalTopK := baseTopK
	if global.LbTopKOverride > 0 {
		globalTopK = global.LbTopKOverride
	}
	globalWeights := ApplyGlobalWeightOverrides(baseWeights, global.WeightOverrides)
	if !globalWeights.ValidGlobal() {
		globalWeights = baseWeights
	}

	effective := EffectiveSettings{
		StickyWeightedEnabled:       global.StickyWeightedEnabled,
		SubscriptionPriorityEnabled: global.SubscriptionPriorityEnabled,
		TopK:                        globalTopK,
		Weights:                     globalWeights,
		Feedback:                    NormalizeFeedback(FeedbackConfig{ErrorRateAlpha: global.EwmaErrorRateAlpha, TtftAlpha: global.EwmaTTFTAlpha}),
		StickyEscape:                NormalizeStickyEscape(StickyEscapeConfig{Enabled: global.StickyEscapeEnabled, TtftMs: global.StickyEscapeTTFTMs, ErrorRate: global.StickyEscapeErrorRate}),
	}
	if overrides.StickyWeightedEnabled != nil {
		effective.StickyWeightedEnabled = *overrides.StickyWeightedEnabled
	}
	if overrides.SubscriptionPriorityEnabled != nil {
		effective.SubscriptionPriorityEnabled = *overrides.SubscriptionPriorityEnabled
	}
	if overrides.LBTopK != nil && *overrides.LBTopK > 0 {
		effective.TopK = *overrides.LBTopK
	}
	if overrides.EWMAErrorRateAlpha != nil {
		effective.Feedback.ErrorRateAlpha = *overrides.EWMAErrorRateAlpha
	}
	if overrides.EWMATTFTAlpha != nil {
		effective.Feedback.TtftAlpha = *overrides.EWMATTFTAlpha
	}
	if overrides.StickyEscapeEnabled != nil {
		effective.StickyEscape.Enabled = *overrides.StickyEscapeEnabled
	}
	if overrides.StickyEscapeTTFTMs != nil {
		effective.StickyEscape.TtftMs = float64(*overrides.StickyEscapeTTFTMs)
	}
	if overrides.StickyEscapeErrorRate != nil {
		effective.StickyEscape.ErrorRate = *overrides.StickyEscapeErrorRate
	}
	effective.Feedback = NormalizeFeedback(effective.Feedback)
	effective.StickyEscape = NormalizeStickyEscape(effective.StickyEscape)
	effective.Weights = ApplyGroupWeightOverrides(effective.Weights, overrides)
	if validateErr := ValidateEffectiveWeights(effective.Weights); validateErr != nil {
		// 历史异常数据不能进入评分；仅回退权重，保留分组其它有效覆盖。
		effective.Weights = globalWeights
	}
	return effective
}

type ScoreWeights struct {
	Priority  float64
	Load      float64
	Queue     float64
	ErrorRate float64
	TTFT      float64
	// Reset 倾向「会话窗口最早重置」的账号；0 表示关闭（默认）。
	Reset float64
	// QuotaHeadroom 倾向 Codex 7d 剩余额度更健康的账号；0 表示关闭（默认）。
	QuotaHeadroom float64
	Previous      float64
	SessionSticky float64
}

type RuntimeSettings struct {
	StickyWeightedEnabled       bool
	SubscriptionPriorityEnabled bool
	LbTopKOverride              int
	WeightOverrides             map[string]float64
	EwmaErrorRateAlpha          float64
	EwmaErrorRateAlphaSet       bool
	EwmaTTFTAlpha               float64
	EwmaTTFTAlphaSet            bool
	StickyEscapeEnabled         bool
	StickyEscapeEnabledSet      bool
	StickyEscapeTTFTMs          float64
	StickyEscapeTTFTMsSet       bool
	StickyEscapeErrorRate       float64
	StickyEscapeErrorRateSet    bool
	StickyEscape                StickyEscapeConfig
}

type StickyEscapeConfig struct {
	Enabled   bool
	TtftMs    float64
	ErrorRate float64
}

// NormalizeStickyEscape 保证健康逃逸配置始终使用可执行的边界值。
func NormalizeStickyEscape(value StickyEscapeConfig) StickyEscapeConfig {
	thresholdsUnset := value.TtftMs == 0 && value.ErrorRate == 0
	if !value.Enabled && value.TtftMs == 0 && value.ErrorRate == 0 {
		// 兼容未注册配置结构体时的零值，保持历史默认开启。
		value.Enabled = true
	}
	if value.TtftMs <= 0 || math.IsNaN(value.TtftMs) || math.IsInf(value.TtftMs, 0) {
		value.TtftMs = 15000
	}
	if value.ErrorRate < 0 || value.ErrorRate > 1 || math.IsNaN(value.ErrorRate) || math.IsInf(value.ErrorRate, 0) {
		value.ErrorRate = 0.5
	}
	if thresholdsUnset {
		value.ErrorRate = 0.5
	}
	return value
}

func ApplyGlobalWeightOverrides(
	Weights ScoreWeights,
	overrides map[string]float64,
) ScoreWeights {
	for key, value := range overrides {
		switch key {
		case "priority":
			Weights.Priority = value
		case "load":
			Weights.Load = value
		case "queue":
			Weights.Queue = value
		case "error_rate":
			Weights.ErrorRate = value
		case "ttft":
			Weights.TTFT = value
		case "reset":
			Weights.Reset = value
		case "quota_headroom":
			Weights.QuotaHeadroom = value
		case "previous_response":
			Weights.Previous = value
		case "session_sticky":
			Weights.SessionSticky = value
		}
	}
	return Weights
}

// FeedbackConfig 保存一次请求回写运行时反馈时使用的 EWMA 系数。
// 统计仍按账号共享，但系数由请求最终命中的分组决定。
type FeedbackConfig struct {
	ErrorRateAlpha float64
	TtftAlpha      float64
}

const (
	defaultAdvancedSchedulerErrorRateAlpha = 0.2
	defaultAdvancedSchedulerTTFTAlpha      = 0.2
)

func NormalizeFeedback(value FeedbackConfig) FeedbackConfig {
	if value.ErrorRateAlpha <= 0 || value.ErrorRateAlpha > 1 || math.IsNaN(value.ErrorRateAlpha) || math.IsInf(value.ErrorRateAlpha, 0) {
		value.ErrorRateAlpha = defaultAdvancedSchedulerErrorRateAlpha
	}
	if value.TtftAlpha <= 0 || value.TtftAlpha > 1 || math.IsNaN(value.TtftAlpha) || math.IsInf(value.TtftAlpha, 0) {
		value.TtftAlpha = defaultAdvancedSchedulerTTFTAlpha
	}
	return value
}

// BaseWeightSum 与原配置相同顺序求和，避免浮点组合顺序变化。
func (w ScoreWeights) BaseWeightSum() float64 {
	return w.Priority + w.Load + w.Queue + w.ErrorRate + w.TTFT + w.Reset + w.QuotaHeadroom
}
func (w ScoreWeights) TotalWeightSum() float64 {
	return w.BaseWeightSum() + w.Previous + w.SessionSticky
}

// ValidGlobal 保持全局权重须有正基础和；分组完整校验允许零基础和。
func (w ScoreWeights) ValidGlobal() bool {
	return ValidateEffectiveWeights(w) == nil && w.BaseWeightSum() > 0
}
