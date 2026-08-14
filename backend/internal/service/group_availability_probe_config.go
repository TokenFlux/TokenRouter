package service

import (
	"errors"
	"strings"

	infraerrors "github.com/TokenFlux/TokenRouter/internal/pkg/errors"
)

const (
	defaultGroupAvailabilityProbeIntervalMinutes = 30
	defaultGroupAvailabilityProbeTimeoutSeconds  = 30
	defaultGroupAvailabilityProbeMaxRetries      = 3
	minGroupAvailabilityProbeIntervalMinutes     = 1
	maxGroupAvailabilityProbeIntervalMinutes     = 1440
	minGroupAvailabilityProbeTimeoutSeconds      = 5
	maxGroupAvailabilityProbeTimeoutSeconds      = 120
	minGroupAvailabilityProbeMaxRetries          = 0
	maxGroupAvailabilityProbeMaxRetries          = 10
	maxGroupAvailabilityProbeUserAgentLength     = 512
)

const invalidGroupAvailabilityProbeConfigReason = "INVALID_AVAILABILITY_PROBE_CONFIG"

// normalizeGroupAvailabilityProbeConfig 统一清洗分组主动探测配置。
// 未启用时只保留 enabled=false，避免无效模型和提示词长期堆积在 JSON 字段里。
func normalizeGroupAvailabilityProbeConfig(cfg GroupAvailabilityProbeConfig) (GroupAvailabilityProbeConfig, error) {
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
	if len(cfg.UserAgent) > maxGroupAvailabilityProbeUserAgentLength {
		return GroupAvailabilityProbeConfig{}, errors.New("availability_probe_config.user_agent is too long")
	}
	if hasInvalidHTTPHeaderValueByte(cfg.UserAgent) {
		return GroupAvailabilityProbeConfig{}, errors.New("availability_probe_config.user_agent contains invalid header characters")
	}

	if cfg.IntervalMinutes == 0 {
		cfg.IntervalMinutes = defaultGroupAvailabilityProbeIntervalMinutes
	}
	if cfg.IntervalMinutes < minGroupAvailabilityProbeIntervalMinutes || cfg.IntervalMinutes > maxGroupAvailabilityProbeIntervalMinutes {
		return GroupAvailabilityProbeConfig{}, errors.New("availability_probe_config.interval_minutes must be between 1 and 1440")
	}

	if cfg.TimeoutSeconds == 0 {
		cfg.TimeoutSeconds = defaultGroupAvailabilityProbeTimeoutSeconds
	}
	if cfg.TimeoutSeconds < minGroupAvailabilityProbeTimeoutSeconds || cfg.TimeoutSeconds > maxGroupAvailabilityProbeTimeoutSeconds {
		return GroupAvailabilityProbeConfig{}, errors.New("availability_probe_config.timeout_seconds must be between 5 and 120")
	}

	// 指针用于区分旧配置缺失字段与管理员显式设置 0 次重试。
	maxRetries := defaultGroupAvailabilityProbeMaxRetries
	if cfg.MaxRetries != nil {
		maxRetries = *cfg.MaxRetries
	}
	if maxRetries < minGroupAvailabilityProbeMaxRetries || maxRetries > maxGroupAvailabilityProbeMaxRetries {
		return GroupAvailabilityProbeConfig{}, errors.New("availability_probe_config.max_retries must be between 0 and 10")
	}
	cfg.MaxRetries = &maxRetries

	return cfg, nil
}

// normalizeGroupAvailabilityProbeConfigForAdminWrite 将管理端输入错误转换为稳定的 HTTP 400 契约。
func normalizeGroupAvailabilityProbeConfigForAdminWrite(cfg GroupAvailabilityProbeConfig) (GroupAvailabilityProbeConfig, error) {
	normalized, err := normalizeGroupAvailabilityProbeConfig(cfg)
	if err != nil {
		return GroupAvailabilityProbeConfig{}, infraerrors.BadRequest(invalidGroupAvailabilityProbeConfigReason, err.Error())
	}
	return normalized, nil
}

// hasInvalidHTTPHeaderValueByte 拒绝控制字符，避免保存后在发送 User-Agent header 时失败。
func hasInvalidHTTPHeaderValueByte(value string) bool {
	for i := 0; i < len(value); i++ {
		b := value[i]
		if b < 0x20 || b == 0x7f {
			return true
		}
	}
	return false
}
