package audit

import (
	"context"
	"encoding/json"
	"strconv"

	"github.com/TokenFlux/TokenRouter/internal/settings"
)

// PrepareRetentionDays 保留存储层原整数格式，不把读取容错规则扩大到直接写入。
func PrepareRetentionDays(days int) string { return strconv.Itoa(days) }

// SettingsParticipant 只准备审计保留期，没有清理或队列副作用。
func SettingsParticipant() settings.Participant {
	return settings.Participant{Module: "audit", Fields: []string{"audit_log_retention_days"}, Keys: []string{SettingKeyAuditLogRetentionDays}, Prepare: func(_ context.Context, input settings.Fields, _ map[string]string) (settings.PreparedChange, error) {
		raw, ok := input["audit_log_retention_days"]
		if !ok {
			return settings.PreparedChange{}, nil
		}
		var days int
		if err := json.Unmarshal(raw, &days); err != nil {
			return settings.PreparedChange{}, err
		}
		return settings.PreparedChange{Values: map[string]string{SettingKeyAuditLogRetentionDays: PrepareRetentionDays(days)}}, nil
	}}
}
