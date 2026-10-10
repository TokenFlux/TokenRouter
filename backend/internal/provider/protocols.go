package provider

import (
	"fmt"
	"maps"

	infraerrors "github.com/TokenFlux/TokenRouter/internal/pkg/apperror"
	"github.com/TokenFlux/TokenRouter/internal/routing/capability"
)

const UpstreamProtocolsKey = "upstream_protocols"

func ProtocolAuthMode(provider *Record) string {
	if provider.IsOpenAIPersonalAccessToken() {
		return OpenAIAuthModePersonalAccessToken
	}
	if provider.IsOpenAIAgentIdentity() {
		return OpenAIAuthModeAgentIdentity
	}
	return ""
}

// NativeProtocolOptions 复用认证模式判定，目录接口不接收任何实际凭据。
func (r *Record) NativeProtocolOptions() []capability.ProtocolID {
	if r == nil {
		return []capability.ProtocolID{}
	}
	return capability.NativeProtocolOptions(r.Platform, r.Type, ProtocolAuthMode(r))
}

func ParseProtocolSet(raw any) ([]capability.ProtocolID, error) {
	out := []capability.ProtocolID{}
	switch value := raw.(type) {
	case []capability.ProtocolID:
		out = append(out, value...)
	case []string:
		for _, item := range value {
			out = append(out, capability.ProtocolID(item))
		}
	case []any:
		for _, item := range value {
			text, ok := item.(string)
			if !ok {
				return nil, fmt.Errorf("upstream_protocols must contain strings")
			}
			out = append(out, capability.ProtocolID(text))
		}
	default:
		return nil, fmt.Errorf("upstream_protocols must be an array")
	}
	return out, nil
}

// UpstreamProtocols 读取协议集合，历史记录缺少该字段时推导默认值。
func (r *Record) UpstreamProtocols() []capability.ProtocolID {
	return r.UpstreamProtocolsForLegacy(r.ConfiguredAPIProtocol())
}

// UpstreamProtocolsForLegacy 读取配置的协议集合，缺失时按传入的协议变体推导默认值。
func (r *Record) UpstreamProtocolsForLegacy(legacyMode string) []capability.ProtocolID {
	if r == nil {
		return []capability.ProtocolID{}
	}
	if raw, exists := r.Credentials[UpstreamProtocolsKey]; exists {
		protocols, err := ParseProtocolSet(raw)
		if err != nil {
			return []capability.ProtocolID{}
		}
		return protocols
	}
	return r.LegacyUpstreamProtocols(legacyMode)
}

// NormalizeProviderProtocols 是创建、编辑和导入的统一保存校验；空数组明确关闭新调用。
// @project-doc docs/interfaces/protocol_capabilities.md#account_native_protocols
func NormalizeProviderProtocols(provider *Record) error {
	if provider == nil || provider.IsCredentialShadow() {
		return nil
	}
	if err := ValidateJevCredentials(provider); err != nil {
		return err
	}
	protocols := provider.UpstreamProtocols()
	if raw, exists := provider.Credentials[UpstreamProtocolsKey]; exists {
		var err error
		protocols, err = ParseProtocolSet(raw)
		if err != nil {
			return infraerrors.BadRequest("UPSTREAM_PROTOCOLS_INVALID", err.Error())
		}
	}
	normalized, err := capability.NormalizeNativeProtocols(capability.ProviderProtocols{Platform: provider.Platform, Type: provider.Type, AuthMode: ProtocolAuthMode(provider), Enabled: protocols})
	if err != nil {
		return infraerrors.BadRequest("UPSTREAM_PROTOCOLS_INVALID", err.Error())
	}
	provider.Credentials = maps.Clone(provider.Credentials)
	if provider.Credentials == nil {
		provider.Credentials = map[string]any{}
	}
	provider.Credentials[UpstreamProtocolsKey] = normalized
	if err := normalizeResponsesWS(provider); err != nil {
		return err
	}
	MigrateLegacyProtocolCredentials(provider)
	return nil
}
