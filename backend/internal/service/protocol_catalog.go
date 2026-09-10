package service

import "github.com/TokenFlux/TokenRouter/internal/domain"

// ProtocolAccountProfile 描述可展示的原生选项，不包含令牌或账号标识。
type ProtocolAccountProfile struct {
	Platform  string              `json:"platform"`
	Type      string              `json:"type"`
	AuthMode  string              `json:"auth_mode"`
	Protocols []domain.ProtocolID `json:"protocols"`
}

type ProtocolGroupProfile struct {
	Platform         string                                    `json:"platform"`
	Protocols        []domain.ProtocolID                       `json:"protocols"`
	Defaults         []domain.ProtocolID                       `json:"defaults"`
	FallbackTargets  map[domain.ProtocolID][]domain.ProtocolID `json:"fallback_targets"`
	DefaultFallbacks map[domain.ProtocolID]domain.ProtocolID   `json:"default_fallbacks"`
}

// ProtocolCatalogResponse 显式描述目录响应，便于调用方和契约测试检查完整结构。
type ProtocolCatalogResponse struct {
	Protocols           []domain.Protocol           `json:"protocols"`
	Accounts            []ProtocolAccountProfile    `json:"accounts"`
	Groups              []ProtocolGroupProfile      `json:"groups"`
	AuxiliaryOperations []domain.AuxiliaryOperation `json:"auxiliary_operations"`
}

// AdminProtocolCatalog 是前后端共用的唯一目录投影。
func AdminProtocolCatalog() ProtocolCatalogResponse {
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
		targets := map[domain.ProtocolID][]domain.ProtocolID{}
		for _, source := range supported {
			targets[source] = domain.ProtocolFallbackTargets(platform, source)
		}
		groups = append(groups, ProtocolGroupProfile{platform, supported, domain.DefaultGroupClientProtocols(platform), targets, domain.DefaultProtocolFallbacks(platform)})
	}
	return ProtocolCatalogResponse{
		Protocols: domain.ProtocolCatalog(), Accounts: accounts, Groups: groups,
		AuxiliaryOperations: domain.AuxiliaryOperations(),
	}
}
