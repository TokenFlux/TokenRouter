package account

import (
	"strings"

	"github.com/TokenFlux/TokenRouter/internal/routing/capability"
)

// GetAnthropicAPIKeyAuthScheme 返回 Anthropic API Key 账号转发上游时使用的认证头方案。
func (a *Record) GetAnthropicAPIKeyAuthScheme() string {
	if a == nil || a.Type != capability.AccountTypeAPIKey {
		return AnthropicAPIKeyAuthSchemeXAPIKey
	}
	if a.Platform != capability.PlatformAnthropic && !a.IsCNProvider() {
		return AnthropicAPIKeyAuthSchemeXAPIKey
	}

	switch strings.TrimSpace(a.GetExtraString(anthropicAPIKeyAuthSchemeExtraKey)) {
	case AnthropicAPIKeyAuthSchemeAuthorizationBearer:
		return AnthropicAPIKeyAuthSchemeAuthorizationBearer
	default:
		return AnthropicAPIKeyAuthSchemeXAPIKey
	}
}

const (
	anthropicAPIKeyAuthSchemeExtraKey            = "anthropic_apikey_auth_scheme"
	AnthropicAPIKeyAuthSchemeXAPIKey             = "x_api_key"
	AnthropicAPIKeyAuthSchemeAuthorizationBearer = "authorization_bearer"
)
