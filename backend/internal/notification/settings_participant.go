package notification

import (
	"context"
	"encoding/json"
	"strconv"
	"strings"

	"github.com/TokenFlux/TokenRouter/internal/settings"
)

// AdminSMTPSettings 是管理文档的明确投影，不承载用户或通知事件。
type AdminSMTPSettings struct {
	SMTPHost     string `json:"smtp_host"`
	SMTPPort     int    `json:"smtp_port"`
	SMTPUsername string `json:"smtp_username"`
	SMTPPassword string `json:"smtp_password"`
	SMTPFrom     string `json:"smtp_from_email"`
	SMTPFromName string `json:"smtp_from_name"`
	SMTPUseTLS   bool   `json:"smtp_use_tls"`
}

// NormalizeAdminSMTPSettings 保留空主机保护和原管理端规范化；不访问存储或发送邮件。
func NormalizeAdminSMTPSettings(current, next AdminSMTPSettings) AdminSMTPSettings {
	next.SMTPHost = strings.TrimSpace(next.SMTPHost)
	next.SMTPUsername = strings.TrimSpace(next.SMTPUsername)
	next.SMTPPassword = strings.TrimSpace(next.SMTPPassword)
	next.SMTPFrom = strings.TrimSpace(next.SMTPFrom)
	next.SMTPFromName = strings.TrimSpace(next.SMTPFromName)
	if next.SMTPPort <= 0 {
		next.SMTPPort = 587
	}
	if next.SMTPHost == "" && current.SMTPHost != "" {
		next.SMTPHost = current.SMTPHost
		next.SMTPPort = current.SMTPPort
		next.SMTPUsername = current.SMTPUsername
		next.SMTPFrom = current.SMTPFrom
		next.SMTPFromName = current.SMTPFromName
		next.SMTPUseTLS = current.SMTPUseTLS
	}
	return next
}

// PrepareSMTPSettings 保留独立存储规则：只有非空密码才写入，不新增通知或发送副作用。
func PrepareSMTPSettings(value AdminSMTPSettings) map[string]string {
	result := map[string]string{
		SettingKeySMTPHost: value.SMTPHost, SettingKeySMTPPort: strconv.Itoa(value.SMTPPort),
		SettingKeySMTPUsername: value.SMTPUsername, SettingKeySMTPFrom: value.SMTPFrom,
		SettingKeySMTPFromName: value.SMTPFromName, SettingKeySMTPUseTLS: strconv.FormatBool(value.SMTPUseTLS),
	}
	if value.SMTPPassword != "" {
		result[SettingKeySMTPPassword] = value.SMTPPassword
	}
	return result
}

// SMTPSettingsParticipant 接受已规范化的管理投影，仅生成实际提供字段的持久值。
func SMTPSettingsParticipant() settings.Participant {
	fields := []string{"smtp_host", "smtp_port", "smtp_username", "smtp_password", "smtp_from_email", "smtp_from_name", "smtp_use_tls"}
	keys := []string{SettingKeySMTPHost, SettingKeySMTPPort, SettingKeySMTPUsername, SettingKeySMTPPassword, SettingKeySMTPFrom, SettingKeySMTPFromName, SettingKeySMTPUseTLS}
	return settings.Participant{Module: "notification", Fields: fields, Keys: keys, Prepare: func(_ context.Context, input settings.Fields, _ map[string]string) (settings.PreparedChange, error) {
		if len(input) == 0 {
			return settings.PreparedChange{}, nil
		}
		encoded, err := json.Marshal(input)
		if err != nil {
			return settings.PreparedChange{}, err
		}
		var value AdminSMTPSettings
		if err = json.Unmarshal(encoded, &value); err != nil {
			return settings.PreparedChange{}, err
		}
		values := PrepareSMTPSettings(value)
		for i, field := range fields {
			if _, ok := input[field]; !ok {
				delete(values, keys[i])
			}
		}
		return settings.PreparedChange{Values: values}, nil
	}}
}
