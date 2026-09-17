package creative

import (
	"context"
	"encoding/json"
	"strconv"

	"github.com/TokenFlux/TokenRouter/internal/pkg/apperror"
	"github.com/TokenFlux/TokenRouter/internal/settings"
)

// AdminSettings 只包含本模块的管理开关与运行参数。
type AdminSettings struct {
	CreativeEnabled       bool                   `json:"creative_enabled"`
	CreativeWorkerCount   int                    `json:"creative_worker_count"`
	CreativeModelSettings []CreativeModelSetting `json:"creative_model_settings"`
}

// 本模块继续使用现有设置键。
const (
	SettingKeyCreativeEnabled       = "creative_enabled"
	SettingKeyCreativeWorkerCount   = "creative_worker_count"
	SettingKeyCreativeModelSettings = "creative_model_settings"
)

// PrepareAdminSettings 保留既有编码和合法范围，不启动任务或发布成功通知。
func PrepareAdminSettings(value AdminSettings) (AdminSettings, map[string]string, error) {
	values := map[string]string{}
	values[SettingKeyCreativeEnabled] = strconv.FormatBool(value.CreativeEnabled)
	raw, normalized, err := MarshalCreativeModelSettings(value.CreativeModelSettings)
	if err != nil {
		return value, nil, apperror.BadRequest("INVALID_CREATIVE_MODEL_SETTINGS", err.Error())
	}
	value.CreativeModelSettings = normalized
	if value.CreativeWorkerCount <= 0 {
		value.CreativeWorkerCount = DefaultCreativeWorkerCount
	}
	values[SettingKeyCreativeModelSettings] = raw
	values[SettingKeyCreativeWorkerCount] = strconv.Itoa(value.CreativeWorkerCount)

	return value, values, nil
}

// SettingsParticipant 静态声明写入所有权，只准备已经投影的实际字段。
func SettingsParticipant() settings.Participant {
	fields := []string{"creative_enabled", "creative_worker_count", "creative_model_settings"}
	keys := []string{SettingKeyCreativeEnabled, SettingKeyCreativeWorkerCount, SettingKeyCreativeModelSettings}
	return settings.Participant{Module: "creative", Fields: fields, Keys: keys, Prepare: func(_ context.Context, input settings.Fields, _ map[string]string) (settings.PreparedChange, error) {
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
