package runtimeconfig

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"

	"github.com/TokenFlux/TokenRouter/internal/pkg/apperror"
	"github.com/TokenFlux/TokenRouter/internal/server/clientip/policy"
	"github.com/TokenFlux/TokenRouter/internal/settings"
)

// 客户端地址配置保持原存储键和版本标记。
const (
	SettingKeyForwardedClientIPModeV2   = "forwarded_client_ip_mode_v2_migrated"
	SettingKeyAPIKeyACLTrustForwardedIP = "api_key_acl_trust_forwarded_ip"
	SettingKeyForwardedClientIPHeaders  = "forwarded_client_ip_headers"
)

// ForwardedSettingsRepository 只提供客户端地址设置的批量读取与兼容写入。
type ForwardedSettingsRepository interface {
	GetMultiple(context.Context, []string) (map[string]string, error)
	SetMultiple(context.Context, map[string]string) error
}

// ForwardedSettingsOptions 由 app 投影启动状态和唯一的运行发布能力。
type ForwardedSettingsOptions struct {
	InitialTrust             bool
	TrustedProxiesConfigured bool
	Headers                  func() []string
	Publish                  func(bool, []string)
}

// ForwardedSettings 负责旧版本兼容与技术发布，不接收完整 config。
type ForwardedSettings struct {
	settingRepo ForwardedSettingsRepository
	options     ForwardedSettingsOptions
}

// NewForwardedSettings 构造不回源，启动时由 app 显式加载。
func NewForwardedSettings(repo ForwardedSettingsRepository, options ForwardedSettingsOptions) *ForwardedSettings {
	return &ForwardedSettings{settingRepo: repo, options: options}
}

// ForwardedInput 只保存客户端地址管理字段。
type ForwardedInput struct {
	APIKeyACLTrustForwardedIP bool     `json:"api_key_acl_trust_forwarded_ip"`
	ForwardedClientIPHeaders  []string `json:"forwarded_client_ip_headers"`
}

// PrepareForwardedSettings 使用同一名称校验和 JSON 编码，不发布运行状态。
func PrepareForwardedSettings(value ForwardedInput) (ForwardedInput, map[string]string, error) {
	headers, err := policy.NormalizeForwardedClientIPHeaders(value.ForwardedClientIPHeaders)
	if err != nil {
		return value, nil, apperror.BadRequest("INVALID_FORWARDED_CLIENT_IP_HEADERS", err.Error())
	}
	value.ForwardedClientIPHeaders = headers
	raw, err := json.Marshal(headers)
	if err != nil {
		return value, nil, fmt.Errorf("marshal forwarded client IP headers: %w", err)
	}
	return value, map[string]string{SettingKeyAPIKeyACLTrustForwardedIP: strconv.FormatBool(value.APIKeyACLTrustForwardedIP), SettingKeyForwardedClientIPHeaders: string(raw)}, nil
}

// Apply 只发布已经确定的值，保持与其余缓存和通知的原装配顺序。
func (s *ForwardedSettings) Apply(value ForwardedInput) {
	s.options.Publish(value.APIKeyACLTrustForwardedIP, value.ForwardedClientIPHeaders)
}

// SettingsParticipant 将客户端地址设置纳入唯一原子写入，不自行增加广播。
func SettingsParticipant() settings.Participant {
	return settings.Participant{Module: "server", Fields: []string{"api_key_acl_trust_forwarded_ip", "forwarded_client_ip_headers"}, Keys: []string{SettingKeyAPIKeyACLTrustForwardedIP, SettingKeyForwardedClientIPHeaders}, Prepare: func(_ context.Context, input settings.Fields, _ map[string]string) (settings.PreparedChange, error) {
		if len(input) == 0 {
			return settings.PreparedChange{}, nil
		}
		var value ForwardedInput
		raw, err := json.Marshal(input)
		if err != nil {
			return settings.PreparedChange{}, err
		}
		if err = json.Unmarshal(raw, &value); err != nil {
			return settings.PreparedChange{}, err
		}
		_, values, err := PrepareForwardedSettings(value)
		if err != nil {
			return settings.PreparedChange{}, err
		}
		for key := range values {
			if _, ok := input[key]; !ok {
				delete(values, key)
			}
		}
		return settings.PreparedChange{Values: values}, nil
	}}
}

// ParseForwardedHeaders 保留非法 JSON、null 和非法名称时的明确失败。
func ParseForwardedHeaders(value string) ([]string, error) {
	var headers []string
	if err := json.Unmarshal([]byte(value), &headers); err != nil {
		return nil, fmt.Errorf("parse forwarded_client_ip_headers: %w", err)
	}
	if headers == nil {
		return nil, fmt.Errorf("parse forwarded_client_ip_headers: value must be a JSON array")
	}
	normalized, err := policy.NormalizeForwardedClientIPHeaders(headers)
	if err != nil {
		return nil, fmt.Errorf("parse forwarded_client_ip_headers: %w", err)
	}
	return normalized, nil
}

// LoadForwardedClientIPSettings 保留原迁移标记、可信代理兼容与失败关闭。
func (s *ForwardedSettings) LoadForwardedClientIPSettings(ctx context.Context) error {
	if s == nil || s.settingRepo == nil {
		return nil
	}

	values, err := s.settingRepo.GetMultiple(ctx, []string{
		SettingKeyAPIKeyACLTrustForwardedIP,
		SettingKeyForwardedClientIPHeaders,
		SettingKeyForwardedClientIPModeV2,
	})
	if err != nil {
		s.options.Publish(false, nil)
		return fmt.Errorf("get forwarded client ip settings: %w", err)
	}

	enabled := s.options.InitialTrust
	headers := s.options.Headers()
	storedValue, hasStoredValue := values[SettingKeyAPIKeyACLTrustForwardedIP]
	if hasStoredValue {
		enabled = storedValue == "true"
	}

	var headersErr error
	if storedHeaders, ok := values[SettingKeyForwardedClientIPHeaders]; ok {
		headers, headersErr = ParseForwardedHeaders(storedHeaders)
		if headersErr != nil {
			enabled = false
			headers = []string{}
			headersErr = fmt.Errorf("load forwarded client ip headers: %w", headersErr)
		}
	}

	updates := make(map[string]string)
	if _, hasStoredHeaders := values[SettingKeyForwardedClientIPHeaders]; !hasStoredHeaders {
		headersJSON, marshalErr := json.Marshal(headers)
		if marshalErr != nil {
			headers = []string{}
			headersErr = errors.Join(headersErr, fmt.Errorf("marshal forwarded client ip headers: %w", marshalErr))
			headersJSON = []byte("[]")
		}
		updates[SettingKeyForwardedClientIPHeaders] = string(headersJSON)
	}
	if values[SettingKeyForwardedClientIPModeV2] != "true" {
		updates[SettingKeyForwardedClientIPModeV2] = "true"
		// 本迁移之前的新安装会默认持久化 false；仅在未配置可信代理策略时恢复兼容模式。
		if headersErr == nil && hasStoredValue && !enabled && !s.options.TrustedProxiesConfigured {
			enabled = true
			updates[SettingKeyAPIKeyACLTrustForwardedIP] = "true"
		}
	}
	if len(updates) > 0 {
		if err := s.settingRepo.SetMultiple(ctx, updates); err != nil {
			s.options.Publish(enabled, headers)
			return errors.Join(headersErr, fmt.Errorf("migrate forwarded client ip setting: %w", err))
		}
	}

	s.options.Publish(enabled, headers)
	return headersErr
}
