// 本文件维护 account 的所属能力；兼容入口复用唯一实现。
package account

import (
	fmt "fmt"
	infraerrors "github.com/TokenFlux/TokenRouter/internal/pkg/apperror"
	capability "github.com/TokenFlux/TokenRouter/internal/routing/capability"
	maps "maps"
)

const UpstreamProtocolsKey = "upstream_protocols"

func ProtocolAuthMode(account *Record) string {
	if account.IsOpenAIPersonalAccessToken() {
		return OpenAIAuthModePersonalAccessToken
	}
	if account.IsOpenAIAgentIdentity() {
		return OpenAIAuthModeAgentIdentity
	}
	return ""
}

// NativeProtocolOptions 复用认证模式判定，目录接口不接收任何实际凭据。
func (a *Record) NativeProtocolOptions() []capability.ProtocolID {
	if a == nil {
		return []capability.ProtocolID{}
	}
	return capability.NativeProtocolOptions(a.Platform, a.Type, ProtocolAuthMode(a))
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

// UpstreamProtocols 读取统一结构；缺字段的旧记录只在兼容边界推导默认值。
func (a *Record) UpstreamProtocols() []capability.ProtocolID {
	return a.UpstreamProtocolsForLegacy(a.ConfiguredAPIProtocol())
}

// UpstreamProtocolsForLegacy 仅供旧转发副本显式传入解析后的协议变体，S15 删除。
func (a *Record) UpstreamProtocolsForLegacy(legacyMode string) []capability.ProtocolID {
	if a == nil {
		return []capability.ProtocolID{}
	}
	if raw, exists := a.Credentials[UpstreamProtocolsKey]; exists {
		protocols, err := ParseProtocolSet(raw)
		if err != nil {
			return []capability.ProtocolID{}
		}
		return protocols
	}
	return a.LegacyUpstreamProtocols(legacyMode)
}

// NormalizeAccountProtocols 是创建、编辑和导入的统一保存校验；空数组明确关闭新调用。
// @project-doc docs/interfaces/protocol_capabilities.md#account_native_protocols
func NormalizeAccountProtocols(account *Record) error {
	if account == nil || account.IsCredentialShadow() {
		return nil
	}
	protocols := account.UpstreamProtocols()
	if raw, exists := account.Credentials[UpstreamProtocolsKey]; exists {
		var err error
		protocols, err = ParseProtocolSet(raw)
		if err != nil {
			return infraerrors.BadRequest("UPSTREAM_PROTOCOLS_INVALID", err.Error())
		}
	}
	normalized, err := capability.NormalizeNativeProtocols(capability.AccountProtocols{Platform: account.Platform, Type: account.Type, AuthMode: ProtocolAuthMode(account), Enabled: protocols})
	if err != nil {
		return infraerrors.BadRequest("UPSTREAM_PROTOCOLS_INVALID", err.Error())
	}
	account.Credentials = maps.Clone(account.Credentials)
	if account.Credentials == nil {
		account.Credentials = map[string]any{}
	}
	account.Credentials[UpstreamProtocolsKey] = normalized
	MigrateLegacyProtocolCredentials(account)
	return nil
}
