package scheduler

import (
	"encoding/json"
	"fmt"
	"math"
	"strconv"
	"strings"

	"github.com/TokenFlux/TokenRouter/internal/pkg/apperror"
	"github.com/TokenFlux/TokenRouter/internal/scheduler/policy"
)

// AdminDefaults 只包含调度的启动参数，不接收完整配置或具体网关服务。
type AdminDefaults struct {
	TopK    int
	Weights policy.ConfigScoreWeights
	Process policy.RuntimeSettings
}

// DefaultAdminSettingsDefaults 保留旧无配置构造的默认值。
func DefaultAdminSettingsDefaults() AdminDefaults {
	return AdminDefaults{TopK: 7, Weights: policy.ConfigScoreWeights{Priority: 1, Load: 1, Queue: 0.7, ErrorRate: 0.8, TTFT: 0.5, Reset: 0, QuotaHeadroom: 0, PreviousResponse: 5, SessionSticky: 3}, Process: policy.RuntimeSettings{EwmaErrorRateAlpha: DefaultErrorRateAlpha, EwmaTTFTAlpha: DefaultTTFTAlpha, StickyEscape: policy.NormalizeStickyEscape(policy.StickyEscapeConfig{Enabled: true, TtftMs: 15000, ErrorRate: 0.5})}}
}

func EffectiveAdminWeights(defaults AdminDefaults) policy.ConfigScoreWeights {
	if defaults.Weights.IsValid() {
		return defaults.Weights
	}
	return DefaultAdminSettingsDefaults().Weights
}
func EffectiveAdminTopK(defaults AdminDefaults) string {
	if defaults.TopK > 0 {
		return strconv.Itoa(defaults.TopK)
	}
	return "7"
}

// AdminSettings 是调度管理覆盖值，保留显式空字符串和布尔存在性。
type AdminSettings struct {
	AdvancedSchedulerEWMAErrorRateAlpha          string `json:"advanced_scheduler_ewma_error_rate_alpha"`
	AdvancedSchedulerEWMATTFTAlpha               string `json:"advanced_scheduler_ewma_ttft_alpha"`
	AdvancedSchedulerLBTopK                      string `json:"advanced_scheduler_lb_top_k"`
	AdvancedSchedulerStickyEscapeEnabled         bool   `json:"advanced_scheduler_sticky_escape_enabled"`
	AdvancedSchedulerStickyEscapeEnabledSet      bool   `json:"-"`
	AdvancedSchedulerStickyEscapeErrorRate       string `json:"advanced_scheduler_sticky_escape_error_rate"`
	AdvancedSchedulerStickyEscapeTTFTMs          string `json:"advanced_scheduler_sticky_escape_ttft_ms"`
	AdvancedSchedulerStickyWeightedEnabled       bool   `json:"advanced_scheduler_sticky_weighted_enabled"`
	AdvancedSchedulerSubscriptionPriorityEnabled bool   `json:"advanced_scheduler_subscription_priority_enabled"`
	AdvancedSchedulerWeightErrorRate             string `json:"advanced_scheduler_weight_error_rate"`
	AdvancedSchedulerWeightLoad                  string `json:"advanced_scheduler_weight_load"`
	AdvancedSchedulerWeightPreviousResponse      string `json:"advanced_scheduler_weight_previous_response"`
	AdvancedSchedulerWeightPriority              string `json:"advanced_scheduler_weight_priority"`
	AdvancedSchedulerWeightQueue                 string `json:"advanced_scheduler_weight_queue"`
	AdvancedSchedulerWeightQuotaHeadroom         string `json:"advanced_scheduler_weight_quota_headroom"`
	AdvancedSchedulerWeightReset                 string `json:"advanced_scheduler_weight_reset"`
	AdvancedSchedulerWeightSessionSticky         string `json:"advanced_scheduler_weight_session_sticky"`
	AdvancedSchedulerWeightTTFT                  string `json:"advanced_scheduler_weight_ttft"`
}

// NormalizeAdminSettings 保留每项范围、有效权重和错误 reason。
func NormalizeAdminSettings(settings *AdminSettings, defaults AdminDefaults) error {
	lbTopK, err := NormalizeOptionalPositiveIntString(settings.AdvancedSchedulerLBTopK)
	if err != nil {
		return apperror.BadRequest("INVALID_ADVANCED_SCHEDULER_LB_TOP_K", "advanced scheduler TopK must be a positive integer or empty")
	}
	settings.AdvancedSchedulerLBTopK = lbTopK
	for _, item := range []struct {
		name   string
		target *string
	}{
		{"advanced scheduler error-rate EWMA alpha", &settings.AdvancedSchedulerEWMAErrorRateAlpha},
		{"advanced scheduler TTFT EWMA alpha", &settings.AdvancedSchedulerEWMATTFTAlpha},
	} {
		normalized, err := NormalizeOptionalAlphaString(*item.target)
		if err != nil {
			return apperror.BadRequest("INVALID_ADVANCED_SCHEDULER_EWMA_ALPHA", item.name+" must be greater than 0 and at most 1, or empty")
		}
		*item.target = normalized
	}
	stickyTTFT, err := NormalizeOptionalPositiveIntString(settings.AdvancedSchedulerStickyEscapeTTFTMs)
	if err != nil {
		return apperror.BadRequest("INVALID_ADVANCED_SCHEDULER_STICKY_ESCAPE_TTFT", "advanced scheduler sticky escape TTFT must be a positive integer or empty")
	}
	settings.AdvancedSchedulerStickyEscapeTTFTMs = stickyTTFT
	stickyRate, err := NormalizeOptionalRateString(settings.AdvancedSchedulerStickyEscapeErrorRate)
	if err != nil {
		return apperror.BadRequest("INVALID_ADVANCED_SCHEDULER_STICKY_ESCAPE_ERROR_RATE", "advanced scheduler sticky escape error rate must be between 0 and 1, or empty")
	}
	settings.AdvancedSchedulerStickyEscapeErrorRate = stickyRate

	weights := []*string{
		&settings.AdvancedSchedulerWeightPriority,
		&settings.AdvancedSchedulerWeightLoad,
		&settings.AdvancedSchedulerWeightQueue,
		&settings.AdvancedSchedulerWeightErrorRate,
		&settings.AdvancedSchedulerWeightTTFT,
		&settings.AdvancedSchedulerWeightReset,
		&settings.AdvancedSchedulerWeightQuotaHeadroom,
		&settings.AdvancedSchedulerWeightPreviousResponse,
		&settings.AdvancedSchedulerWeightSessionSticky,
	}
	for _, target := range weights {
		normalized, err := NormalizeOptionalNonNegativeFloatString(*target)
		if err != nil {
			return apperror.BadRequest("INVALID_ADVANCED_SCHEDULER_WEIGHT", "advanced scheduler weights must be non-negative numbers or empty")
		}
		*target = normalized
	}

	// 与 config.Validate 的 "scheduler_score_weights must not all be zero" 保持一致：
	// 覆盖值（空则回退到生效的配置值）叠加后的基础权重和不允许为 0，
	// 否则调度会静默退化为 TopK 内均匀随机。
	effective := EffectiveAdminWeights(defaults)
	resolved := policy.ConfigScoreWeights{
		Priority:         ResolveAdvancedSchedulerWeight(settings.AdvancedSchedulerWeightPriority, effective.Priority),
		Load:             ResolveAdvancedSchedulerWeight(settings.AdvancedSchedulerWeightLoad, effective.Load),
		Queue:            ResolveAdvancedSchedulerWeight(settings.AdvancedSchedulerWeightQueue, effective.Queue),
		ErrorRate:        ResolveAdvancedSchedulerWeight(settings.AdvancedSchedulerWeightErrorRate, effective.ErrorRate),
		TTFT:             ResolveAdvancedSchedulerWeight(settings.AdvancedSchedulerWeightTTFT, effective.TTFT),
		Reset:            ResolveAdvancedSchedulerWeight(settings.AdvancedSchedulerWeightReset, effective.Reset),
		QuotaHeadroom:    ResolveAdvancedSchedulerWeight(settings.AdvancedSchedulerWeightQuotaHeadroom, effective.QuotaHeadroom),
		PreviousResponse: ResolveAdvancedSchedulerWeight(settings.AdvancedSchedulerWeightPreviousResponse, effective.PreviousResponse),
		SessionSticky:    ResolveAdvancedSchedulerWeight(settings.AdvancedSchedulerWeightSessionSticky, effective.SessionSticky),
	}
	if !resolved.IsValid() {
		return apperror.BadRequest("INVALID_ADVANCED_SCHEDULER_WEIGHT", "advanced scheduler weights must have finite non-zero base and total sums")
	}
	return nil
}

func ResolveAdvancedSchedulerWeight(normalized string, fallback float64) float64 {
	if normalized == "" {
		return fallback
	}
	value, err := strconv.ParseFloat(normalized, 64)
	if err != nil {
		return fallback
	}
	return value
}

func NormalizeOptionalPositiveIntString(raw string) (string, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return "", nil
	}
	value, err := strconv.Atoi(raw)
	if err != nil || value <= 0 {
		return "", fmt.Errorf("invalid positive integer")
	}
	return strconv.Itoa(value), nil
}

func NormalizeOptionalNonNegativeFloatString(raw string) (string, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return "", nil
	}
	value, err := strconv.ParseFloat(raw, 64)
	if err != nil || value < 0 || math.IsNaN(value) || math.IsInf(value, 0) {
		return "", fmt.Errorf("invalid non-negative float")
	}
	return strconv.FormatFloat(value, 'f', -1, 64), nil
}

func NormalizeOptionalAlphaString(raw string) (string, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return "", nil
	}
	value, err := strconv.ParseFloat(raw, 64)
	if err != nil || value <= 0 || value > 1 || math.IsNaN(value) || math.IsInf(value, 0) {
		return "", fmt.Errorf("invalid alpha")
	}
	return strconv.FormatFloat(value, 'f', -1, 64), nil
}

func NormalizeOptionalRateString(raw string) (string, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return "", nil
	}
	value, err := strconv.ParseFloat(raw, 64)
	if err != nil || value < 0 || value > 1 || math.IsNaN(value) || math.IsInf(value, 0) {
		return "", fmt.Errorf("invalid rate")
	}
	return strconv.FormatFloat(value, 'f', -1, 64), nil
}

// PrepareAdminSettings 生成原持久化表示，不刷新评分、反馈或缓存。
func PrepareAdminSettings(settings *AdminSettings, defaults AdminDefaults) (map[string]string, error) {
	if err := NormalizeAdminSettings(settings, defaults); err != nil {
		return nil, err
	}
	updates := map[string]string{}
	updates[SettingKeyAdvancedSchedulerStickyWeightedEnabled] = strconv.FormatBool(settings.AdvancedSchedulerStickyWeightedEnabled)
	updates[SettingKeyAdvancedSchedulerSubscriptionPriorityEnabled] = strconv.FormatBool(settings.AdvancedSchedulerSubscriptionPriorityEnabled)
	updates[SettingKeyAdvancedSchedulerEWMAErrorRateAlpha] = settings.AdvancedSchedulerEWMAErrorRateAlpha
	updates[SettingKeyAdvancedSchedulerEWMATTFTAlpha] = settings.AdvancedSchedulerEWMATTFTAlpha
	if settings.AdvancedSchedulerStickyEscapeEnabledSet {
		updates[SettingKeyAdvancedSchedulerStickyEscapeEnabled] = strconv.FormatBool(settings.AdvancedSchedulerStickyEscapeEnabled)
	} else {
		// 开关缺省时保留空值，让进程配置继续提供默认值。
		updates[SettingKeyAdvancedSchedulerStickyEscapeEnabled] = ""
	}
	updates[SettingKeyAdvancedSchedulerStickyEscapeTTFTMs] = settings.AdvancedSchedulerStickyEscapeTTFTMs
	updates[SettingKeyAdvancedSchedulerStickyEscapeErrorRate] = settings.AdvancedSchedulerStickyEscapeErrorRate
	updates[SettingKeyAdvancedSchedulerLBTopK] = settings.AdvancedSchedulerLBTopK
	updates[SettingKeyAdvancedSchedulerWeightPriority] = settings.AdvancedSchedulerWeightPriority
	updates[SettingKeyAdvancedSchedulerWeightLoad] = settings.AdvancedSchedulerWeightLoad
	updates[SettingKeyAdvancedSchedulerWeightQueue] = settings.AdvancedSchedulerWeightQueue
	updates[SettingKeyAdvancedSchedulerWeightErrorRate] = settings.AdvancedSchedulerWeightErrorRate
	updates[SettingKeyAdvancedSchedulerWeightTTFT] = settings.AdvancedSchedulerWeightTTFT
	updates[SettingKeyAdvancedSchedulerWeightReset] = settings.AdvancedSchedulerWeightReset
	updates[SettingKeyAdvancedSchedulerWeightQuotaHeadroom] = settings.AdvancedSchedulerWeightQuotaHeadroom
	updates[SettingKeyAdvancedSchedulerWeightPreviousResponse] = settings.AdvancedSchedulerWeightPreviousResponse
	updates[SettingKeyAdvancedSchedulerWeightSessionSticky] = settings.AdvancedSchedulerWeightSessionSticky
	return updates, nil
}

// ParticipantFields 不把未设置的逃逸开关变成显式 false。
func (value AdminSettings) ParticipantFields() (map[string]json.RawMessage, error) {
	raw, err := json.Marshal(value)
	if err != nil {
		return nil, err
	}
	fields := map[string]json.RawMessage{}
	if err = json.Unmarshal(raw, &fields); err != nil {
		return nil, err
	}
	if !value.AdvancedSchedulerStickyEscapeEnabledSet {
		delete(fields, SettingKeyAdvancedSchedulerStickyEscapeEnabled)
	}
	return fields, nil
}
