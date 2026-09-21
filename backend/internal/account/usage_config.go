// 本文件维护 account 的所属能力；兼容入口复用唯一实现。
package account

import (
	json "encoding/json"
	strings "strings"

	egress "github.com/TokenFlux/TokenRouter/internal/egress"
)

// EffectiveUpstreamUsageConfig 解析账号的生效配置。缺少配置时使用安全的默认适配器。
func EffectiveUpstreamUsageConfig(account *Record) (UpstreamUsageQueryConfig, error) {
	config := UpstreamUsageQueryConfig{Enabled: true, Adapter: UpstreamUsageDefaultAdapter}
	if adapter := CNUpstreamUsageAdapterName(account); adapter != "" {
		config.Adapter = adapter
	}
	if account == nil || account.Extra == nil {
		return config, nil
	}
	raw, exists := account.Extra[UpstreamUsageQueryExtraKey]
	if !exists || raw == nil {
		return config, nil
	}
	object, ok := UpstreamUsageConfigMap(raw)
	if !ok {
		return UpstreamUsageQueryConfig{}, ErrUpstreamUsageConfigInvalid
	}
	if value, exists := object["enabled"]; exists {
		parsed, ok := value.(bool)
		if !ok {
			return UpstreamUsageQueryConfig{}, ErrUpstreamUsageConfigInvalid
		}
		config.Enabled = parsed
	}
	if value, exists := object["adapter"]; exists {
		parsed, ok := value.(string)
		if !ok || strings.TrimSpace(parsed) == "" {
			return UpstreamUsageQueryConfig{}, ErrUpstreamUsageConfigInvalid
		}
		if !account.IsCNProvider() {
			config.Adapter = strings.TrimSpace(parsed)
		}
	}
	if value, exists := object["base_url"]; exists {
		parsed, ok := value.(string)
		if !ok {
			return UpstreamUsageQueryConfig{}, ErrUpstreamUsageConfigInvalid
		}
		config.BaseURL = strings.TrimSpace(parsed)
	}
	for key := range object {
		if key != "enabled" && key != "adapter" && key != "base_url" {
			return UpstreamUsageQueryConfig{}, ErrUpstreamUsageConfigInvalid
		}
	}
	if !IsKnownUpstreamUsageAdapter(config.Adapter) {
		return UpstreamUsageQueryConfig{}, ErrUpstreamUsageUnsupported
	}
	if config.BaseURL != "" {
		if err := egress.ValidateUsageBaseURLFormat(config.BaseURL); err != nil {
			return UpstreamUsageQueryConfig{}, ErrUpstreamUsageConfigInvalid.WithCause(err)
		}
	}
	return config, nil
}

// NormalizeUpstreamUsageExtra 校验并规范化创建/更新请求中的查询配置。
func NormalizeUpstreamUsageExtra(extra map[string]any) error {
	if extra == nil {
		return nil
	}
	raw, exists := extra[UpstreamUsageQueryExtraKey]
	if !exists || raw == nil {
		return nil
	}
	object, ok := UpstreamUsageConfigMap(raw)
	if !ok {
		return ErrUpstreamUsageConfigInvalid
	}
	normalized := map[string]any{
		"enabled": true,
		"adapter": UpstreamUsageDefaultAdapter,
	}
	if value, exists := object["enabled"]; exists {
		parsed, ok := value.(bool)
		if !ok {
			return ErrUpstreamUsageConfigInvalid
		}
		normalized["enabled"] = parsed
	}
	if value, exists := object["adapter"]; exists {
		parsed, ok := value.(string)
		if !ok || !IsKnownUpstreamUsageAdapter(strings.TrimSpace(parsed)) {
			return ErrUpstreamUsageConfigInvalid
		}
		normalized["adapter"] = strings.TrimSpace(parsed)
	}
	if value, exists := object["base_url"]; exists {
		parsed, ok := value.(string)
		if !ok {
			return ErrUpstreamUsageConfigInvalid
		}
		parsed = strings.TrimSpace(parsed)
		if parsed != "" {
			if err := egress.ValidateUsageBaseURLFormat(parsed); err != nil {
				return ErrUpstreamUsageConfigInvalid.WithCause(err)
			}
			normalized["base_url"] = parsed
		}
	}
	// 不接受任何可能把凭据或任意请求模板带入 Extra 的字段。
	for key := range object {
		if key != "enabled" && key != "adapter" && key != "base_url" {
			return ErrUpstreamUsageConfigInvalid
		}
	}
	extra[UpstreamUsageQueryExtraKey] = normalized
	return nil
}

// NormalizedUpstreamUsageConfigValue 复制并规范化已有配置。
// 旧记录若含敏感字段或任意请求模板，不得在一次无关编辑中被重新写回。
func NormalizedUpstreamUsageConfigValue(value any) (any, bool) {
	if value == nil {
		return nil, false
	}
	extra := map[string]any{UpstreamUsageQueryExtraKey: value}
	if err := NormalizeUpstreamUsageExtra(extra); err != nil {
		return nil, false
	}
	normalized, ok := extra[UpstreamUsageQueryExtraKey]
	return normalized, ok
}

func UpstreamUsageConfigMap(raw any) (map[string]any, bool) {
	if object, ok := raw.(map[string]any); ok {
		return object, true
	}
	payload, err := json.Marshal(raw)
	if err != nil {
		return nil, false
	}
	var object map[string]any
	if err := json.Unmarshal(payload, &object); err != nil {
		return nil, false
	}
	return object, object != nil
}

func IsKnownUpstreamUsageAdapter(name string) bool {
	for _, registration := range usageAdapterCatalog {
		if registration.Name == name {
			return true
		}
	}
	return false
}

func CNUpstreamUsageAdapterName(account *Record) string {
	if account == nil || !account.IsCNProvider() {
		return ""
	}
	if account.IsCodingPlan() {
		switch account.Platform {
		case PlatformKimi:
			return UpstreamUsageAdapterKimiCoding
		case PlatformZhipu:
			return UpstreamUsageAdapterZhipuCoding
		default:
			return ""
		}
	}
	switch account.Platform {
	case PlatformKimi:
		return UpstreamUsageAdapterKimiBalance
	case PlatformDeepseek:
		return UpstreamUsageAdapterDeepseekBalance
	default:
		// 智谱 payg 没有公开余额协议，手动查询保持明确“不支持”而不发请求。
		return ""
	}
}
