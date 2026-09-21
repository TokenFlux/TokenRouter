package identityhttp_test

import (
	"context"
	"time"

	"github.com/TokenFlux/TokenRouter/internal/config"
	"github.com/TokenFlux/TokenRouter/internal/gateway"
	"github.com/TokenFlux/TokenRouter/internal/gateway/admission"
	"github.com/TokenFlux/TokenRouter/internal/identity"
	identityprovider "github.com/TokenFlux/TokenRouter/internal/identity/provider"
	"github.com/TokenFlux/TokenRouter/internal/pkg/timezone"
	"github.com/TokenFlux/TokenRouter/internal/promotion"
	"github.com/TokenFlux/TokenRouter/internal/scheduler"
	"github.com/TokenFlux/TokenRouter/internal/server/runtimeconfig"
	"github.com/TokenFlux/TokenRouter/internal/settings"
	"github.com/TokenFlux/TokenRouter/internal/settings/composite"
	"github.com/TokenFlux/TokenRouter/internal/site"
	"github.com/TokenFlux/TokenRouter/internal/team"
	"github.com/TokenFlux/TokenRouter/internal/upstream/anthropic"
	"github.com/TokenFlux/TokenRouter/internal/upstream/antigravity"
	"github.com/TokenFlux/TokenRouter/internal/upstream/grok"
	"github.com/TokenFlux/TokenRouter/internal/usage"
)

// authSettingsFixture 只装配认证契约所需的原生读取器；数据仍来自同一个替身。
type authSettingsFixture struct {
	*identity.RuntimeSettings
	*identity.GrantSettings
	*site.DisplaySettings
	oauth     *identity.OAuthSettings
	promotion *promotion.RuntimeSettings
	backend   *admission.BackendMode
	public    *site.PublicService
	composite *composite.Runtime
}

func newAuthSettingsFixture(repo settings.Repository, cfg *config.Config) *authSettingsFixture {
	if cfg == nil {
		cfg = &config.Config{}
	}
	store := settings.New(repo)
	oauth := identity.NewOAuthSettings(store, &identity.OAuthSettingsDefaults{LinuxDo: cfg.LinuxDo, DingTalk: cfg.DingTalk, OIDC: cfg.OIDC, WeChat: cfg.WeChat, GitHubOAuth: cfg.GitHubOAuth, GoogleOAuth: cfg.GoogleOAuth}, identityprovider.ResolveSettingsOIDCMetadata)
	grants := identity.NewGrantSettings(store, identity.GrantSettingsOptions{DefaultBalance: cfg.Default.UserBalance, DefaultConcurrency: cfg.Default.UserConcurrency})
	value := &authSettingsFixture{RuntimeSettings: identity.NewRuntimeSettings(store, settings.ErrSettingNotFound), GrantSettings: grants, DisplaySettings: site.NewDisplaySettings(store, func() string { return cfg.Server.FrontendURL }), oauth: oauth, promotion: promotion.NewRuntimeSettings(store), backend: admission.NewBackendMode(store, nil)}
	value.public = site.NewPublicService(site.NewInputSource(store, site.PublicInputOptions{Auth: func(raw map[string]string) site.PublicAuth {
		v := oauth.PublicSettingsFromValues(raw)
		enabled, selfService := team.PublicSettings(raw, cfg.Team.Enabled, cfg.Team.SelfServiceEnabled)
		return site.PublicAuth{LinuxDo: v.LinuxDo, DingTalk: v.DingTalk, OIDC: v.OIDC, OIDCName: v.OIDCName, WeChat: v.WeChat, WeChatOpen: v.WeChatOpen, WeChatMP: v.WeChatMP, WeChatMobile: v.WeChatMobile, GitHub: v.GitHub, Google: v.Google, GoogleOneTap: v.GoogleOneTap, GoogleClientID: v.GoogleClientID, Team: enabled, TeamSelfService: selfService, Passkey: cfg.WebAuthn.Enabled, TencentRegion: v.TencentRegion, AliyunRegion: v.AliyunRegion, RegistrationSuffixes: v.RegistrationSuffixes}
	}, Usage: func(raw map[string]string) site.PublicUsage {
		v := usage.ParseRankingSettings(raw)
		return site.PublicUsage{Limit: v.Limit, Enabled: v.Enabled, SortBy: string(v.SortBy), ShowTotalTokens: v.ShowTotalTokens, ShowRequests: v.ShowRequests, ShowActualCost: v.ShowActualCost}
	}, Version: store.Version}), timezone.NewCalendar(time.Local), "")
	rules := gateway.AdminSettingsRules{GrokDefaultTextModel: grok.DefaultTextModel, NormalizeUserAgentVersion: antigravity.NormalizeUserAgentVersion, ValidateClaudePromptBlocks: anthropic.ValidateClaudeOAuthSystemPromptBlocksConfig}
	read := composite.ReadOptions{OAuth: oauth, Gateway: rules, Scheduler: scheduler.DefaultAdminSettingsDefaults(), DefaultBalance: func() float64 { return cfg.Default.UserBalance }, DefaultConcurrency: func() int { return cfg.Default.UserConcurrency }, Forwarded: func() runtimeconfig.ForwardedInput {
		v := cfg.ForwardedClientIPSettings()
		return runtimeconfig.ForwardedInput{APIKeyACLTrustForwardedIP: v.TrustForwardedIP, ForwardedClientIPHeaders: v.Headers}
	}, PublishModel: func(model string, enabled bool) {
		grok.SetRuntimeModelMappingOptions(grok.ModelMappingOptions{DefaultText: model, EnableCrossClientMap: enabled})
	}}
	value.composite = composite.NewRuntime(store, read, composite.PrepareOptions{}, grants, nil, cfg.Totp.EncryptionKeyConfigured, nil)
	return value
}
func (s *authSettingsFixture) GetDingTalkConnectOAuthConfig(ctx context.Context) (identity.DingTalkRegistrationPolicy, error) {
	v, e := s.oauth.GetDingTalkConnectOAuthConfig(ctx)
	return identity.DingTalkRegistrationPolicy{Enabled: v.Enabled, BypassRegistration: v.BypassRegistration, CorpRestrictionPolicy: v.CorpRestrictionPolicy}, e
}
func (s *authSettingsFixture) IsInvitationCodeEnabled(ctx context.Context) bool {
	return s.promotion.IsInvitationCodeEnabled(ctx)
}
func (s *authSettingsFixture) IsPromoCodeEnabled(ctx context.Context) bool {
	return s.promotion.IsPromoCodeEnabled(ctx)
}
func (s *authSettingsFixture) IsBackendModeEnabled(ctx context.Context) bool {
	return s.backend.Enabled(ctx)
}
func authContractSettings(s *authSettingsFixture) identity.AuthSettings {
	if s == nil {
		return nil
	}
	return s
}
