package service

import "github.com/TokenFlux/TokenRouter/internal/domain"

const (
	ProtocolAnthropicMessages     = domain.ProtocolAnthropicMessages
	ProtocolOpenAIResponses       = domain.ProtocolOpenAIResponses
	ProtocolOpenAIChatCompletions = domain.ProtocolOpenAIChatCompletions
	ProtocolGeminiGenerateContent = domain.ProtocolGeminiGenerateContent
)

// ProtocolAccountProfile 描述可展示的原生选项，不包含令牌或账号标识。
type ProtocolAccountProfile struct {
	Platform  string                `json:"platform"`
	Type      string                `json:"type"`
	AuthMode  string                `json:"auth_mode"`
	Protocols []GroupClientProtocol `json:"protocols"`
}

type ProtocolGroupProfile struct {
	Platform         string                                        `json:"platform"`
	Protocols        []GroupClientProtocol                         `json:"protocols"`
	Defaults         []GroupClientProtocol                         `json:"defaults"`
	FallbackTargets  map[GroupClientProtocol][]GroupClientProtocol `json:"fallback_targets"`
	DefaultFallbacks map[GroupClientProtocol]GroupClientProtocol   `json:"default_fallbacks"`
}

// AdminProtocolCatalog 是前后端共用的唯一目录投影。
func AdminProtocolCatalog() any {
	accounts := []ProtocolAccountProfile{}
	groups := []ProtocolGroupProfile{}
	for _, platform := range []string{PlatformAnthropic, PlatformOpenAI, PlatformGemini, PlatformAntigravity, PlatformGrok, PlatformQoder, PlatformKimi, PlatformZhipu, PlatformDeepseek} {
		for _, accountType := range []string{AccountTypeOAuth, AccountTypeSetupToken, AccountTypeAPIKey, AccountTypeUpstream, AccountTypeBedrock, AccountTypeServiceAccount, AccountTypeCosy} {
			modes := []string{""}
			if platform == PlatformOpenAI && accountType == AccountTypeOAuth {
				modes = append(modes, OpenAIAuthModePersonalAccessToken, OpenAIAuthModeAgentIdentity)
			}
			for _, mode := range modes {
				accounts = append(accounts, ProtocolAccountProfile{platform, accountType, mode, domain.NativeProtocolOptions(platform, accountType, mode)})
			}
		}
		supported := domain.SupportedGroupClientProtocols(platform)
		targets := map[GroupClientProtocol][]GroupClientProtocol{}
		for _, source := range supported {
			targets[source] = domain.ProtocolFallbackTargets(platform, source)
		}
		groups = append(groups, ProtocolGroupProfile{platform, supported, domain.DefaultGroupClientProtocols(platform), targets, DefaultProtocolFallbacks(platform)})
	}
	return struct {
		Protocols           []domain.Protocol           `json:"protocols"`
		Accounts            []ProtocolAccountProfile    `json:"accounts"`
		Groups              []ProtocolGroupProfile      `json:"groups"`
		AuxiliaryOperations []domain.AuxiliaryOperation `json:"auxiliary_operations"`
	}{domain.ProtocolCatalog(), accounts, groups, domain.AuxiliaryOperations()}
}
