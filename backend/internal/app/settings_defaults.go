package app

import (
	"context"

	"github.com/TokenFlux/TokenRouter/internal/billing"
	"github.com/TokenFlux/TokenRouter/internal/config"
	"github.com/TokenFlux/TokenRouter/internal/gateway"
	gatewayprovider "github.com/TokenFlux/TokenRouter/internal/gateway/provider"
	"github.com/TokenFlux/TokenRouter/internal/identity"
	identityprovider "github.com/TokenFlux/TokenRouter/internal/identity/provider"
	"github.com/TokenFlux/TokenRouter/internal/ops"
	"github.com/TokenFlux/TokenRouter/internal/provider"
	"github.com/TokenFlux/TokenRouter/internal/scheduler"
	"github.com/TokenFlux/TokenRouter/internal/scheduler/policy"
	"github.com/TokenFlux/TokenRouter/internal/server/runtimeconfig"
	"github.com/TokenFlux/TokenRouter/internal/settings"
	"github.com/TokenFlux/TokenRouter/internal/upstream/anthropic"
	"github.com/TokenFlux/TokenRouter/internal/upstream/antigravity"
	"github.com/TokenFlux/TokenRouter/internal/upstream/grok"
)

// settings_defaults.go constructs module settings readers and administration
// defaults from application configuration. Settings readers combine database
// values with startup options interpreted by their owning modules. OAuth,
// signup grants, forwarded-IP trust, scheduler defaults, gateway platform
// rules, and quota parsing receive their application dependencies here while
// later read and publication timing stays with each reader.

// provideOAuthSettings supplies optional startup OAuth defaults and OIDC
// metadata discovery. Nil cfg leaves defaults absent; configured platform
// options are copied into the defaults object retained by the shared identity
// reader.
func provideOAuthSettings(store *settings.Store, cfg *config.Config) *identity.OAuthSettings {
	var defaults *identity.OAuthSettingsDefaults
	if cfg != nil {
		defaults = &identity.OAuthSettingsDefaults{LinuxDo: cfg.Auth.OAuth.LinuxDo, DingTalk: cfg.Auth.OAuth.DingTalk.IdentityDefaults(), OIDC: cfg.Auth.OAuth.OIDC, WeChat: cfg.Auth.OAuth.WeChat.IdentityDefaults(), GitHubOAuth: cfg.Auth.OAuth.GitHub, GoogleOAuth: cfg.Auth.OAuth.Google}
	}
	return identity.NewOAuthSettings(store, defaults, identityprovider.ResolveSettingsOIDCMetadata)
}

// provideGrantSettings binds signup balance/concurrency defaults and
// subscription-plan validation. Nil cfg leaves numeric options at zero, while
// configured values are copied; ValidatePlans reads the supplied plan service
// when grants are checked.
func provideGrantSettings(store *settings.Store, cfg *config.Config, plans *billing.Plans) *identity.GrantSettings {
	options := identity.GrantSettingsOptions{ValidatePlans: func(ctx context.Context, items []identity.DefaultSubscriptionSetting) error {
		return identity.ValidateDefaultSubscriptionPlans(ctx, items, plans.GetPlan)
	}}
	if cfg != nil {
		options.DefaultBalance = cfg.UserDefaults.Balance
		options.DefaultConcurrency = cfg.UserDefaults.Concurrency
	}
	return identity.NewGrantSettings(store, options)
}

// provideForwardedSettings binds required cfg to client-IP trust, header reads,
// and publication. Initial trust and trusted-proxy presence are captured, while
// callbacks read and publish cfg's current forwarded client-IP settings.
func provideForwardedSettings(store *settings.Store, cfg *config.Config) *runtimeconfig.ForwardedSettings {
	return runtimeconfig.NewForwardedSettings(store, runtimeconfig.ForwardedSettingsOptions{InitialTrust: cfg.Server.ClientIP.TrustForwardedHeaders, TrustedProxiesConfigured: cfg.Server.ClientIP.TrustedProxiesConfigured, Headers: func() []string { return cfg.ForwardedClientIPSettings().Headers }, Publish: cfg.SetForwardedClientIPSettings})
}

// provideSchedulerAdminDefaults copies process score settings and normalized
// sticky escape into administration defaults. Nil cfg returns built-in inputs;
// supplied Top-K and weights replace them directly, including configured zero
// values.
func provideSchedulerAdminDefaults(cfg *config.Config) *scheduler.AdminDefaults {
	value := scheduler.DefaultAdminSettingsDefaults()
	if cfg != nil {
		source := cfg.Scheduling.Advanced
		value.TopK = source.LBTopK
		value.Weights = source.ScoreWeights
		value.Process.EwmaErrorRateAlpha = source.EWMAErrorRateAlpha
		value.Process.EwmaTTFTAlpha = source.EWMATTFTAlpha
		value.Process.StickyEscape = policy.NormalizeStickyEscape(policy.StickyEscapeConfig{Enabled: source.StickyEscapeEnabled, TtftMs: float64(source.StickyEscapeTTFTMs), ErrorRate: source.StickyEscapeErrorRate})
	}
	return &value
}

// provideGatewayAdminRules binds platform model defaults, User-Agent
// normalization, and Claude prompt validation.
func provideGatewayAdminRules() *gateway.AdminSettingsRules {
	return &gateway.AdminSettingsRules{GrokDefaultTextModel: grok.DefaultTextModel, NormalizeUserAgentVersion: antigravity.NormalizeUserAgentVersion, ValidateClaudePromptBlocks: anthropic.ValidateClaudeOAuthSystemPromptBlocksConfig}
}

// provideGatewaySettings constructs runtime forwarding policy with
// missing-setting and platform defaults.
func provideGatewaySettings(store *settings.Store) *gateway.RuntimeSettings {
	return gateway.NewRuntimeSettings(store, settings.ErrSettingNotFound, func() *gateway.BetaPolicySettings {
		return gatewayprovider.GatewayBetaPolicy(anthropic.DefaultBetaPolicySettings())
	}, gateway.ClientSettingsOptions{NormalizeUserAgentVersion: antigravity.NormalizeUserAgentVersion, DefaultUserAgentVersion: antigravity.GetDefaultUserAgentVersion})
}

// provideQuotaSettings binds shared quota JSON reads to the Ops runtime parser.
func provideQuotaSettings(store *settings.Store) *provider.QuotaSettingsCache {
	return provider.NewQuotaSettingsCache(store, settings.ErrSettingNotFound, ops.ParseRuntimeQuotaAutoPauseSettings)
}
