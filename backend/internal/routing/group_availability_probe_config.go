// 本文件维护 routing 的所属能力；兼容入口复用唯一实现。
package routing

import (
	"errors"
	"strings"

	infraerrors "github.com/TokenFlux/TokenRouter/internal/pkg/apperror"
)

const (
	DefaultGroupAvailabilityProbeIntervalMinutes = 30
	DefaultGroupAvailabilityProbeTimeoutSeconds  = 30
	DefaultGroupAvailabilityProbeMaxRetries      = 3
	MinGroupAvailabilityProbeIntervalMinutes     = 1
	MaxGroupAvailabilityProbeIntervalMinutes     = 1440
	MinGroupAvailabilityProbeTimeoutSeconds      = 5
	MaxGroupAvailabilityProbeTimeoutSeconds      = 120
	MinGroupAvailabilityProbeMaxRetries          = 0
	MaxGroupAvailabilityProbeMaxRetries          = 10
	MaxGroupAvailabilityProbeUserAgentLength     = 512
)

const InvalidGroupAvailabilityProbeConfigReason = "INVALID_AVAILABILITY_PROBE_CONFIG"

// NormalizeGroupAvailabilityProbeConfig 统一清洗分组主动探测配置。
// 未启用时只保留 enabled=false，避免无效模型和提示词长期堆积在 JSON 字段里。
func NormalizeGroupAvailabilityProbeConfig(cfg GroupAvailabilityProbeConfig) (GroupAvailabilityProbeConfig, error) {
	if !cfg.Enabled {
		return GroupAvailabilityProbeConfig{}, nil
	}

	cfg.ModelID = strings.TrimSpace(cfg.ModelID)
	cfg.Prompt = strings.TrimSpace(cfg.Prompt)
	cfg.UserAgent = strings.TrimSpace(cfg.UserAgent)
	if cfg.ModelID == "" {
		return GroupAvailabilityProbeConfig{}, errors.New("availability_probe_config.model_id is required when enabled")
	}
	if cfg.Prompt == "" {
		return GroupAvailabilityProbeConfig{}, errors.New("availability_probe_config.prompt is required when enabled")
	}
	if len(cfg.UserAgent) > MaxGroupAvailabilityProbeUserAgentLength {
		return GroupAvailabilityProbeConfig{}, errors.New("availability_probe_config.user_agent is too long")
	}
	if HasInvalidHTTPHeaderValueByte(cfg.UserAgent) {
		return GroupAvailabilityProbeConfig{}, errors.New("availability_probe_config.user_agent contains invalid header characters")
	}

	if cfg.IntervalMinutes == 0 {
		cfg.IntervalMinutes = DefaultGroupAvailabilityProbeIntervalMinutes
	}
	if cfg.IntervalMinutes < MinGroupAvailabilityProbeIntervalMinutes || cfg.IntervalMinutes > MaxGroupAvailabilityProbeIntervalMinutes {
		return GroupAvailabilityProbeConfig{}, errors.New("availability_probe_config.interval_minutes must be between 1 and 1440")
	}

	if cfg.TimeoutSeconds == 0 {
		cfg.TimeoutSeconds = DefaultGroupAvailabilityProbeTimeoutSeconds
	}
	if cfg.TimeoutSeconds < MinGroupAvailabilityProbeTimeoutSeconds || cfg.TimeoutSeconds > MaxGroupAvailabilityProbeTimeoutSeconds {
		return GroupAvailabilityProbeConfig{}, errors.New("availability_probe_config.timeout_seconds must be between 5 and 120")
	}

	// 指针用于区分旧配置缺失字段与管理员显式设置 0 次重试。
	maxRetries := DefaultGroupAvailabilityProbeMaxRetries
	if cfg.MaxRetries != nil {
		maxRetries = *cfg.MaxRetries
	}
	if maxRetries < MinGroupAvailabilityProbeMaxRetries || maxRetries > MaxGroupAvailabilityProbeMaxRetries {
		return GroupAvailabilityProbeConfig{}, errors.New("availability_probe_config.max_retries must be between 0 and 10")
	}
	cfg.MaxRetries = &maxRetries

	return cfg, nil
}

// NormalizeGroupAvailabilityProbeConfigForAdminWrite 将管理端输入错误转换为稳定的 HTTP 400 契约。
func NormalizeGroupAvailabilityProbeConfigForAdminWrite(cfg GroupAvailabilityProbeConfig) (GroupAvailabilityProbeConfig, error) {
	normalized, err := NormalizeGroupAvailabilityProbeConfig(cfg)
	if err != nil {
		return GroupAvailabilityProbeConfig{}, infraerrors.BadRequest(InvalidGroupAvailabilityProbeConfigReason, err.Error())
	}
	return normalized, nil
}

// HasInvalidHTTPHeaderValueByte 拒绝控制字符，避免保存后在发送 User-Agent header 时失败。
func HasInvalidHTTPHeaderValueByte(value string) bool {
	for i := 0; i < len(value); i++ {
		b := value[i]
		if b < 0x20 || b == 0x7f {
			return true
		}
	}
	return false
}
