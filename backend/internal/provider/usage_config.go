package provider

import (
	"encoding/json"
	"strings"

	"github.com/TokenFlux/TokenRouter/internal/egress"
)

// 持久化键通过常量和别名供各平台共用。
const (
	CNUsageMonitorSnapshotExtraKey   = "cn_usage_monitor_snapshot"
	UpstreamUsageQueryExtraKey       = "upstream_usage_query"
	OllamaCloudUsageSnapshotExtraKey = "ollama_cloud_usage_snapshot"
)

// EffectiveUpstreamUsageConfig 解析提供商的生效配置。缺少配置时使用安全的默认适配器。
func EffectiveUpstreamUsageConfig(provider *Record) (UpstreamUsageQueryConfig, error) {
	config := UpstreamUsageQueryConfig{Enabled: provider == nil || provider.Platform != PlatformJev, Adapter: UpstreamUsageDefaultAdapter}
	if adapter := CNUpstreamUsageAdapterName(provider); adapter != "" {
		config.Adapter = adapter
	}
	if provider == nil || provider.Extra == nil {
		return config, nil
	}
	raw, exists := provider.Extra[UpstreamUsageQueryExtraKey]
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
		if !provider.IsCNProvider() {
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
// 保存时清除历史记录中的敏感字段和任意请求模板。
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

func CNUpstreamUsageAdapterName(provider *Record) string {
	if provider == nil || !provider.IsCNProvider() {
		return ""
	}
	if provider.IsCodingPlan() {
		switch provider.Platform {
		case PlatformKimi:
			return UpstreamUsageAdapterKimiCoding
		case PlatformZhipu:
			return UpstreamUsageAdapterZhipuCoding
		default:
			return ""
		}
	}
	switch provider.Platform {
	case PlatformKimi:
		return UpstreamUsageAdapterKimiBalance
	case PlatformDeepseek:
		return UpstreamUsageAdapterDeepseekBalance
	default:
		// 智谱 payg 没有公开余额协议，手动查询保持明确“不支持”而不发请求。
		return ""
	}
}
