package moderation

import (
	"context"
	"encoding/json"
	"strconv"

	"github.com/TokenFlux/TokenRouter/internal/settings"
)

// AdminSettings 只包含本模块的管理开关与运行参数。
type AdminSettings struct {
	RiskControlEnabled          bool `json:"risk_control_enabled"`
	CyberSessionBlockEnabled    bool `json:"cyber_session_block_enabled"`
	CyberSessionBlockTTLSeconds int  `json:"cyber_session_block_ttl_seconds"`
}

// 本模块继续使用现有设置键。
const (
	SettingKeyCyberSessionBlockEnabled    = "cyber_session_block_enabled"
	SettingKeyCyberSessionBlockTTLSeconds = "cyber_session_block_ttl_seconds"
)

// PrepareAdminSettings 保留既有编码和合法范围，不启动任务或发布成功通知。
func PrepareAdminSettings(value AdminSettings) (AdminSettings, map[string]string, error) {
	values := map[string]string{}
	values[SettingKeyRiskControlEnabled] = strconv.FormatBool(value.RiskControlEnabled)
	values[SettingKeyCyberSessionBlockEnabled] = strconv.FormatBool(value.CyberSessionBlockEnabled)
	if value.CyberSessionBlockTTLSeconds > 0 {
		values[SettingKeyCyberSessionBlockTTLSeconds] = strconv.Itoa(value.CyberSessionBlockTTLSeconds)
	}

	return value, values, nil
}

// SettingsParticipant 静态声明写入所有权，只准备已经投影的实际字段。
func SettingsParticipant() settings.Participant {
	fields := []string{"risk_control_enabled", "cyber_session_block_enabled", "cyber_session_block_ttl_seconds"}
	keys := []string{SettingKeyRiskControlEnabled, SettingKeyCyberSessionBlockEnabled, SettingKeyCyberSessionBlockTTLSeconds}
	return settings.Participant{Module: "moderation", Fields: fields, Keys: keys, Prepare: func(_ context.Context, input settings.Fields, _ map[string]string) (settings.PreparedChange, error) {
		if len(input) == 0 {
			return settings.PreparedChange{}, nil
		}
		raw, err := json.Marshal(input)
		if err != nil {
			return settings.PreparedChange{}, err
		}
		var value AdminSettings
		if err = json.Unmarshal(raw, &value); err != nil {
			return settings.PreparedChange{}, err
		}
		_, values, err := PrepareAdminSettings(value)
		if err != nil {
			return settings.PreparedChange{}, err
		}
		for i, field := range fields {
			if _, ok := input[field]; !ok {
				delete(values, keys[i])
			}
		}
		return settings.PreparedChange{Values: values}, nil
	}}
}
