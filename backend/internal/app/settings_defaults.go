package app

import (
	"context"

	gatewayprovider "github.com/TokenFlux/TokenRouter/internal/gateway/provider"

	"github.com/TokenFlux/TokenRouter/internal/account"
	"github.com/TokenFlux/TokenRouter/internal/ops"

	"github.com/TokenFlux/TokenRouter/internal/gateway"
	"github.com/TokenFlux/TokenRouter/internal/upstream/anthropic"
	"github.com/TokenFlux/TokenRouter/internal/upstream/antigravity"
	"github.com/TokenFlux/TokenRouter/internal/upstream/grok"

	"github.com/TokenFlux/TokenRouter/internal/scheduler"
	"github.com/TokenFlux/TokenRouter/internal/scheduler/policy"

	"github.com/TokenFlux/TokenRouter/internal/server/runtimeconfig"

	"github.com/TokenFlux/TokenRouter/internal/billing"
	"github.com/TokenFlux/TokenRouter/internal/config"
	"github.com/TokenFlux/TokenRouter/internal/identity"
	identityprovider "github.com/TokenFlux/TokenRouter/internal/identity/provider"
	"github.com/TokenFlux/TokenRouter/internal/settings"
)

// provideOAuthSettings 只投影认证所需启动字段，核心不会接收完整配置。
func provideOAuthSettings(store *settings.Store, cfg *config.Config) *identity.OAuthSettings {
	var defaults *identity.OAuthSettingsDefaults
	if cfg != nil {
		defaults = &identity.OAuthSettingsDefaults{LinuxDo: cfg.LinuxDo, DingTalk: cfg.DingTalk, OIDC: cfg.OIDC, WeChat: cfg.WeChat, GitHubOAuth: cfg.GitHubOAuth, GoogleOAuth: cfg.GoogleOAuth}
	}
	return identity.NewOAuthSettings(store, defaults, identityprovider.ResolveSettingsOIDCMetadata)
}

// provideGrantSettings 直接绑定 billing 的只读套餐查询，不经过支付聚合。
func provideGrantSettings(store *settings.Store, cfg *config.Config, plans *billing.Plans) *identity.GrantSettings {
	options := identity.GrantSettingsOptions{ValidatePlans: func(ctx context.Context, items []identity.DefaultSubscriptionSetting) error {
		return identity.ValidateDefaultSubscriptionPlans(ctx, items, plans.GetPlan)
	}}
	if cfg != nil {
		options.DefaultBalance = cfg.Default.UserBalance
		options.DefaultConcurrency = cfg.Default.UserConcurrency
	}
	return identity.NewGrantSettings(store, options)
}

// provideForwardedSettings 将启动可信代理配置与唯一运行状态投影给 server。
func provideForwardedSettings(store *settings.Store, cfg *config.Config) *runtimeconfig.ForwardedSettings {
	return runtimeconfig.NewForwardedSettings(store, runtimeconfig.ForwardedSettingsOptions{InitialTrust: cfg.Security.TrustForwardedIPForAPIKeyACL, TrustedProxiesConfigured: cfg.Server.TrustedProxiesConfigured, Headers: func() []string { return cfg.ForwardedClientIPSettings().Headers }, Publish: cfg.SetForwardedClientIPSettings})
}

// provideSchedulerAdminDefaults 只投影调度所需进程值，规范化由 scheduler/policy 拥有。
func provideSchedulerAdminDefaults(cfg *config.Config) *scheduler.AdminDefaults {
	value := scheduler.DefaultAdminSettingsDefaults()
	if cfg != nil {
		source := cfg.Gateway.AdvancedScheduler
		value.TopK = source.LBTopK
		value.Weights = source.ScoreWeights
		value.Process.EwmaErrorRateAlpha = source.EWMAErrorRateAlpha
		value.Process.EwmaTTFTAlpha = source.EWMATTFTAlpha
		value.Process.StickyEscape = policy.NormalizeStickyEscape(policy.StickyEscapeConfig{Enabled: source.StickyEscapeEnabled, TtftMs: float64(source.StickyEscapeTTFTMs), ErrorRate: source.StickyEscapeErrorRate})
	}
	return &value
}

// provideGatewayAdminRules 投影实际平台规则，不创建额外客户端或状态。
func provideGatewayAdminRules() *gateway.AdminSettingsRules {
	return &gateway.AdminSettingsRules{GrokDefaultTextModel: grok.DefaultTextModel, NormalizeUserAgentVersion: antigravity.NormalizeUserAgentVersion, ValidateClaudePromptBlocks: anthropic.ValidateClaudeOAuthSystemPromptBlocksConfig}
}

// provideGatewaySettings 直接构造唯一运行实例，平台默认值只做结构投影。
func provideGatewaySettings(store *settings.Store) *gateway.RuntimeSettings {
	return gateway.NewRuntimeSettings(store, settings.ErrSettingNotFound, func() *gateway.BetaPolicySettings {
		return gatewayprovider.GatewayBetaPolicy(anthropic.DefaultBetaPolicySettings())
	}, gateway.ClientSettingsOptions{NormalizeUserAgentVersion: antigravity.NormalizeUserAgentVersion, DefaultUserAgentVersion: antigravity.GetDefaultUserAgentVersion})
}

// provideQuotaSettings 将共享 JSON 的 Ops 解释投影给账号，不复制缓存或使用旧 SettingsService。
func provideQuotaSettings(store *settings.Store) *account.QuotaSettingsCache {
	return account.NewQuotaSettingsCache(store, settings.ErrSettingNotFound, ops.ParseRuntimeQuotaAutoPauseSettings)
}
