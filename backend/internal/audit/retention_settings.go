package audit

import (
	"context"
	"strconv"
	"strings"
)

// DefaultRetentionDays 与存储键保持原值。
const DefaultRetentionDays = 180
const SettingKeyAuditLogRetentionDays = "audit_log_retention_days"

// RetentionSettingsStore 只读取审计生命周期所需配置。
type RetentionSettingsStore interface {
	GetValue(context.Context, string) (string, error)
}

// RetentionSettings 拥有保留期解释，不读取其它业务设置。
type RetentionSettings struct{ settingRepo RetentionSettingsStore }

// NewRetentionSettings 构造不执行 I/O。
func NewRetentionSettings(repo RetentionSettingsStore) *RetentionSettings {
	return &RetentionSettings{settingRepo: repo}
}

// GetAuditLogRetentionDays 保留原保留期读取及永久保留语义。
func (s *RetentionSettings) GetAuditLogRetentionDays(ctx context.Context) int {
	value, err := s.settingRepo.GetValue(ctx, SettingKeyAuditLogRetentionDays)
	if err != nil {
		return DefaultRetentionDays
	}
	return ParseRetentionDays(value)
}

// ParseRetentionDays 对已有持久值执行原容错规则。
func ParseRetentionDays(value string) int {
	value = strings.TrimSpace(value)
	if value == "" {
		return DefaultRetentionDays
	}
	n, err := strconv.Atoi(value)
	if err != nil {
		return DefaultRetentionDays
	}
	if n < 0 {
		return 0
	}
	return n
}
