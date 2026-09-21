// Package testkit 只装配综合设置契约使用的原生模块，不拥有设置规则或运行缓存。
package testkit

import (
	"context"
	"log/slog"
	"time"

	"github.com/TokenFlux/TokenRouter/internal/account"
	"github.com/TokenFlux/TokenRouter/internal/billing"
	"github.com/TokenFlux/TokenRouter/internal/config"
	"github.com/TokenFlux/TokenRouter/internal/gateway"
	"github.com/TokenFlux/TokenRouter/internal/gateway/admission"
	"github.com/TokenFlux/TokenRouter/internal/gateway/promptpolicy"
	gatewaytestkit "github.com/TokenFlux/TokenRouter/internal/gateway/testkit"
	"github.com/TokenFlux/TokenRouter/internal/identity"
	identityprovider "github.com/TokenFlux/TokenRouter/internal/identity/provider"
	"github.com/TokenFlux/TokenRouter/internal/pkg/timezone"
	"github.com/TokenFlux/TokenRouter/internal/promotion"
	"github.com/TokenFlux/TokenRouter/internal/scheduler"
	"github.com/TokenFlux/TokenRouter/internal/scheduler/policy"
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

// PlanReader 是测试替身提供的套餐只读端口，校验仍调用 billing。
type PlanReader interface {
	GetByID(context.Context, int64) (*billing.SubscriptionPlan, error)
}

// Composite 暴露被测试的真实实例与装配选项，测试直接调用所属能力。
type Composite struct {
	Applications  []composite.Application
	Runtime       *composite.Runtime
	Store         *settings.Store
	Read          composite.ReadOptions
	Prepare       composite.PrepareOptions
	Gateway       *gateway.RuntimeSettings
	Account       *account.RuntimeSettings
	Quota         *account.QuotaSettingsCache
	Identity      *identity.RuntimeSettings
	Promotion     *promotion.RuntimeSettings
	Backend       *admission.BackendMode
	Forwarded     *runtimeconfig.ForwardedSettings
	Scheduler     *scheduler.SettingsRuntime
	Defaults      scheduler.AdminDefaults
	Public        *site.PublicService
	Plans         PlanReader
	WorkerChanged func(int)
}

// NewComposite 不读写存储，不启动 worker；所有解释和缓存均使用实际所属模块。
func NewComposite(repo settings.Repository, cfg *config.Config) *Composite {
	if cfg == nil {
		cfg = &config.Config{}
	}
	store := settings.New(repo)
	readers := gatewaytestkit.RuntimeReaders(store)
	defaults := scheduler.DefaultAdminSettingsDefaults()
	source := cfg.Gateway.AdvancedScheduler
	defaults.TopK = source.LBTopK
	defaults.Weights = source.ScoreWeights
	defaults.Process.EwmaErrorRateAlpha = source.EWMAErrorRateAlpha
	defaults.Process.EwmaTTFTAlpha = source.EWMATTFTAlpha
	defaults.Process.StickyEscape = policy.NormalizeStickyEscape(policy.StickyEscapeConfig{Enabled: source.StickyEscapeEnabled, TtftMs: float64(source.StickyEscapeTTFTMs), ErrorRate: source.StickyEscapeErrorRate})
	result := &Composite{Store: store, Gateway: readers.Gateway, Account: readers.Account, Quota: readers.Quota, Identity: identity.NewRuntimeSettings(store, settings.ErrSettingNotFound), Promotion: promotion.NewRuntimeSettings(store), Backend: admission.NewBackendMode(store, slog.Warn), Defaults: defaults, Scheduler: scheduler.NewSettingsRuntime(scheduler.Diagnostics{})}
	oauth := identity.NewOAuthSettings(store, &identity.OAuthSettingsDefaults{LinuxDo: cfg.LinuxDo, DingTalk: cfg.DingTalk, OIDC: cfg.OIDC, WeChat: cfg.WeChat, GitHubOAuth: cfg.GitHubOAuth, GoogleOAuth: cfg.GoogleOAuth}, identityprovider.ResolveSettingsOIDCMetadata)
	validate := func(ctx context.Context, items []billing.DefaultSubscriptionSetting) error {
		if result.Plans == nil {
			return billing.ValidateDefaultSubscriptionPlans(ctx, items, nil)
		}
		return billing.ValidateDefaultSubscriptionPlans(ctx, items, result.Plans.GetByID)
	}
	grants := identity.NewGrantSettings(store, identity.GrantSettingsOptions{DefaultBalance: cfg.Default.UserBalance, DefaultConcurrency: cfg.Default.UserConcurrency, ValidatePlans: validate})
	rules := gateway.AdminSettingsRules{GrokDefaultTextModel: grok.DefaultTextModel, NormalizeUserAgentVersion: antigravity.NormalizeUserAgentVersion, ValidateClaudePromptBlocks: anthropic.ValidateClaudeOAuthSystemPromptBlocksConfig}
	result.Forwarded = runtimeconfig.NewForwardedSettings(store, runtimeconfig.ForwardedSettingsOptions{InitialTrust: cfg.Security.TrustForwardedIPForAPIKeyACL, TrustedProxiesConfigured: cfg.Server.TrustedProxiesConfigured, Headers: func() []string { return cfg.ForwardedClientIPSettings().Headers }, Publish: cfg.SetForwardedClientIPSettings})
	result.Read = composite.ReadOptions{OAuth: oauth, Gateway: rules, Scheduler: defaults, DefaultBalance: func() float64 { return cfg.Default.UserBalance }, DefaultConcurrency: func() int { return cfg.Default.UserConcurrency }, Forwarded: func() runtimeconfig.ForwardedInput {
		value := cfg.ForwardedClientIPSettings()
		return runtimeconfig.ForwardedInput{APIKeyACLTrustForwardedIP: value.TrustForwardedIP, ForwardedClientIPHeaders: value.Headers}
	}, PublishModel: func(model string, enabled bool) {
		grok.SetRuntimeModelMappingOptions(grok.ModelMappingOptions{DefaultText: model, EnableCrossClientMap: enabled})
	}}
	result.Prepare = composite.PrepareOptions{ReadValues: store.GetAll, Gateway: rules, Scheduler: defaults, ValidatePlans: validate}
	steps := []composite.Application{
		{Module: "gateway", Apply: func(_ context.Context, s *composite.Snapshot) error {
			result.Backend.Publish(s.BackendModeEnabled)
			result.Gateway.PublishForwarding(s.MinClaudeCodeVersion, s.MaxClaudeCodeVersion, gateway.ForwardingSnapshot{OpenAITTFTMode: gateway.NormalizeOpenAITTFTMode(s.OpenAITTFTMode), FingerprintUnification: s.EnableFingerprintUnification, MetadataPassthrough: s.EnableMetadataPassthrough, CCHSigning: s.EnableCCHSigning, ClaudeOAuthSystemPromptInjection: s.EnableClaudeOAuthSystemPromptInjection, ClaudeOAuthSystemPrompt: s.ClaudeOAuthSystemPrompt, ClaudeOAuthSystemPromptBlocks: s.ClaudeOAuthSystemPromptBlocks, AnthropicCacheTTL1hInjection: s.EnableAnthropicCacheTTL1hInjection, RewriteMessageCacheControl: s.RewriteMessageCacheControl, ClientDatelineNormalization: s.EnableClientDatelineNormalization})
			result.Gateway.PublishClientUserAgents(s.AntigravityUserAgentVersion, s.OpenAICodexUserAgent)
			promptpolicy.SharedCache().Store((*promptpolicy.CompiledConfig)(nil))
			return nil
		}},
		{Module: "scheduler", Apply: func(_ context.Context, s *composite.Snapshot) error {
			result.Scheduler.Store(scheduler.RuntimeSettingsFromAdmin(s.SchedulerAdminSettings(), defaults))
			return nil
		}},
		{Module: "account", Apply: func(_ context.Context, s *composite.Snapshot) error {
			result.Quota.Apply(s.OpenAIQuotaAutoPauseSettings, s.OpenAIQuotaAutoPauseSettingsSet)
			result.Account.ApplySchedulingThresholds(s.AccountSchedulingThresholds)
			return nil
		}},
		{Module: "server", Apply: func(_ context.Context, s *composite.Snapshot) error {
			result.Forwarded.Apply(runtimeconfig.ForwardedInput{APIKeyACLTrustForwardedIP: s.APIKeyACLTrustForwardedIP, ForwardedClientIPHeaders: s.ForwardedClientIPHeaders})
			return nil
		}},
		{Module: "gateway-client", Apply: func(_ context.Context, s *composite.Snapshot) error {
			result.Gateway.PublishCodexPlugin(s.OpenAIAllowClaudeCodeCodexPlugin)
			return nil
		}},
		{Module: "site", Apply: func(context.Context, *composite.Snapshot) error { store.NotifyUpdated(); return nil }},
		{Module: "creative", Apply: func(_ context.Context, s *composite.Snapshot) error {
			if result.WorkerChanged != nil {
				result.WorkerChanged(s.CreativeWorkerCount)
			}
			return nil
		}},
	}
	result.Applications = steps
	result.Runtime = composite.NewRuntime(store, result.Read, result.Prepare, grants, result.Gateway, cfg.Totp.EncryptionKeyConfigured, steps)
	result.Public = site.NewPublicService(site.NewInputSource(store, site.PublicInputOptions{Auth: func(raw map[string]string) site.PublicAuth {
		value := oauth.PublicSettingsFromValues(raw)
		enabled, selfService := team.PublicSettings(raw, cfg.Team.Enabled, cfg.Team.SelfServiceEnabled)
		return site.PublicAuth{LinuxDo: value.LinuxDo, DingTalk: value.DingTalk, OIDC: value.OIDC, OIDCName: value.OIDCName, WeChat: value.WeChat, WeChatOpen: value.WeChatOpen, WeChatMP: value.WeChatMP, WeChatMobile: value.WeChatMobile, GitHub: value.GitHub, Google: value.Google, GoogleOneTap: value.GoogleOneTap, GoogleClientID: value.GoogleClientID, Team: enabled, TeamSelfService: selfService, Passkey: cfg.WebAuthn.Enabled, TencentRegion: value.TencentRegion, AliyunRegion: value.AliyunRegion, RegistrationSuffixes: value.RegistrationSuffixes}
	}, Usage: func(raw map[string]string) site.PublicUsage {
		v := usage.ParseRankingSettings(raw)
		return site.PublicUsage{Limit: v.Limit, Enabled: v.Enabled, SortBy: string(v.SortBy), ShowTotalTokens: v.ShowTotalTokens, ShowRequests: v.ShowRequests, ShowActualCost: v.ShowActualCost}
	}, Version: store.Version}), timezone.NewCalendar(time.Local), "")
	return result
}

// Save 让原独立更新断言执行同一准备、单次提交及发布能力；不提供旧聚合 getter。
func (f *Composite) Save(ctx context.Context, value *composite.Snapshot) error {
	session, err := f.Runtime.BeginSettingsUpdate(ctx)
	if err != nil {
		return err
	}
	defer session.Close()
	values, err := composite.Prepare(session.Context(), value, f.Prepare)
	if err != nil {
		return err
	}
	changes := []settings.PreparedChange{{Module: "contract-input", Values: values}}
	// 原独立更新断言携带准备态的显式发布标记；HTTP 回读契约由 Runtime 单独验证。
	for _, step := range f.Applications {
		step := step
		changes = append(changes, settings.PreparedChange{Module: step.Module, Apply: func(ctx context.Context) error {
			return step.Apply(ctx, value)
		}})
	}
	return session.Commit(changes...)
}
