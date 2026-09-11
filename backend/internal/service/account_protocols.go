package service

import (
	"fmt"
	"maps"

	"github.com/TokenFlux/TokenRouter/internal/domain"
	infraerrors "github.com/TokenFlux/TokenRouter/internal/pkg/errors"
	"github.com/TokenFlux/TokenRouter/internal/routing/capability"
)

const upstreamProtocolsKey = "upstream_protocols"

func protocolAuthMode(account *Account) string {
	if account.IsOpenAIPersonalAccessToken() {
		return OpenAIAuthModePersonalAccessToken
	}
	if account.IsOpenAIAgentIdentity() {
		return OpenAIAuthModeAgentIdentity
	}
	return ""
}

// NativeProtocolOptions 复用认证模式判定，目录接口不接收任何实际凭据。
func (a *Account) NativeProtocolOptions() []domain.ProtocolID {
	if a == nil {
		return []domain.ProtocolID{}
	}
	return domain.NativeProtocolOptions(a.Platform, a.Type, protocolAuthMode(a))
}

func parseProtocolSet(raw any) ([]domain.ProtocolID, error) {
	out := []domain.ProtocolID{}
	switch value := raw.(type) {
	case []domain.ProtocolID:
		out = append(out, value...)
	case []string:
		for _, item := range value {
			out = append(out, domain.ProtocolID(item))
		}
	case []any:
		for _, item := range value {
			text, ok := item.(string)
			if !ok {
				return nil, fmt.Errorf("upstream_protocols must contain strings")
			}
			out = append(out, domain.ProtocolID(text))
		}
	default:
		return nil, fmt.Errorf("upstream_protocols must be an array")
	}
	return out, nil
}

// UpstreamProtocols 读取统一结构；缺字段的旧记录只在兼容边界推导默认值。
func (a *Account) UpstreamProtocols() []domain.ProtocolID {
	if a == nil {
		return []domain.ProtocolID{}
	}
	if raw, exists := a.Credentials[upstreamProtocolsKey]; exists {
		protocols, err := parseProtocolSet(raw)
		if err != nil {
			return []domain.ProtocolID{}
		}
		return protocols
	}
	return a.legacyUpstreamProtocols()
}

// NormalizeAccountProtocols 是创建、编辑和导入的统一保存校验；空数组明确关闭新调用。
// @project-doc docs/interfaces/protocol_capabilities.md#account_native_protocols
func NormalizeAccountProtocols(account *Account) error {
	if account == nil || account.IsCredentialShadow() {
		return nil
	}
	protocols := account.UpstreamProtocols()
	if raw, exists := account.Credentials[upstreamProtocolsKey]; exists {
		var err error
		protocols, err = parseProtocolSet(raw)
		if err != nil {
			return infraerrors.BadRequest("UPSTREAM_PROTOCOLS_INVALID", err.Error())
		}
	}
	normalized, err := capability.NormalizeNativeProtocols(capability.AccountProtocols{Platform: account.Platform, Type: account.Type, AuthMode: protocolAuthMode(account), Enabled: protocols})
	if err != nil {
		return infraerrors.BadRequest("UPSTREAM_PROTOCOLS_INVALID", err.Error())
	}
	account.Credentials = maps.Clone(account.Credentials)
	if account.Credentials == nil {
		account.Credentials = map[string]any{}
	}
	account.Credentials[upstreamProtocolsKey] = normalized
	migrateLegacyProtocolCredentials(account)
	return nil
}
