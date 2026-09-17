package app

import (
	"context"

	"github.com/TokenFlux/TokenRouter/internal/account"
	"github.com/TokenFlux/TokenRouter/internal/billing"
	"github.com/TokenFlux/TokenRouter/internal/config"
	"github.com/TokenFlux/TokenRouter/internal/creative"
	"github.com/TokenFlux/TokenRouter/internal/gateway"
	"github.com/TokenFlux/TokenRouter/internal/gateway/promptpolicy"
	"github.com/TokenFlux/TokenRouter/internal/identity"
	"github.com/TokenFlux/TokenRouter/internal/ops"
	"github.com/TokenFlux/TokenRouter/internal/scheduler"
	"github.com/TokenFlux/TokenRouter/internal/server/runtimeconfig"
	"github.com/TokenFlux/TokenRouter/internal/service"
	"github.com/TokenFlux/TokenRouter/internal/settings"
	"github.com/TokenFlux/TokenRouter/internal/settings/composite"
	"github.com/TokenFlux/TokenRouter/internal/upstream/grok"
)

// provideCompositeRuntime 固定原运行实例和发布顺序，完整配置仅在 app 投影。
func provideCompositeRuntime(store *settings.Store, cfg *config.Config, oauth *identity.OAuthSettings, grants *identity.GrantSettings, gatewayRuntime *gateway.RuntimeSettings, gatewayRules *gateway.AdminSettingsRules, defaults *scheduler.AdminDefaults, plans *billing.Plans, legacy *service.SettingService, accountRuntime *account.RuntimeSettings, quota *account.QuotaSettingsCache, forwarded *runtimeconfig.ForwardedSettings, shared *schedulerSharedState, worker *creative.CreativeWorkerRuntime, monitor *ops.OpsService) *composite.Runtime {
	read := composite.ReadOptions{OAuth: oauth, Gateway: *gatewayRules, Scheduler: *defaults, DefaultBalance: func() float64 { return cfg.Default.UserBalance }, DefaultConcurrency: func() int { return cfg.Default.UserConcurrency }, Forwarded: func() runtimeconfig.ForwardedInput {
		value := cfg.ForwardedClientIPSettings()
		return runtimeconfig.ForwardedInput{APIKeyACLTrustForwardedIP: value.TrustForwardedIP, ForwardedClientIPHeaders: value.Headers}
	}, PublishModel: func(model string, enabled bool) {
		grok.SetRuntimeModelMappingOptions(grok.ModelMappingOptions{DefaultText: model, EnableCrossClientMap: enabled})
	}}
	prepare := composite.PrepareOptions{ReadValues: store.GetAll, Gateway: *gatewayRules, Scheduler: *defaults, ValidatePlans: func(ctx context.Context, value []billing.DefaultSubscriptionSetting) error {
		return billing.ValidateDefaultSubscriptionPlans(ctx, value, plans.GetPlan)
	}}
	steps := []composite.Application{
		{Module: "gateway", Apply: func(_ context.Context, s *composite.Snapshot) error {
			legacy.BackendModeSettings().Publish(s.BackendModeEnabled)
			gatewayRuntime.PublishForwarding(s.MinClaudeCodeVersion, s.MaxClaudeCodeVersion, gateway.ForwardingSnapshot{OpenAITTFTMode: gateway.NormalizeOpenAITTFTMode(s.OpenAITTFTMode), FingerprintUnification: s.EnableFingerprintUnification, MetadataPassthrough: s.EnableMetadataPassthrough, CCHSigning: s.EnableCCHSigning, ClaudeOAuthSystemPromptInjection: s.EnableClaudeOAuthSystemPromptInjection, ClaudeOAuthSystemPrompt: s.ClaudeOAuthSystemPrompt, ClaudeOAuthSystemPromptBlocks: s.ClaudeOAuthSystemPromptBlocks, AnthropicCacheTTL1hInjection: s.EnableAnthropicCacheTTL1hInjection, RewriteMessageCacheControl: s.RewriteMessageCacheControl, ClientDatelineNormalization: s.EnableClientDatelineNormalization})
			gatewayRuntime.PublishClientUserAgents(s.AntigravityUserAgentVersion, s.OpenAICodexUserAgent)
			promptpolicy.SharedCache().Store((*promptpolicy.CompiledConfig)(nil))
			return nil
		}},
		{Module: "scheduler", Apply: func(_ context.Context, s *composite.Snapshot) error {
			shared.Settings.Store(scheduler.RuntimeSettingsFromAdmin(s.SchedulerAdminSettings(), *defaults))
			return nil
		}},
		{Module: "account", Apply: func(_ context.Context, s *composite.Snapshot) error {
			quota.Apply(s.OpenAIQuotaAutoPauseSettings, s.OpenAIQuotaAutoPauseSettingsSet)
			accountRuntime.ApplySchedulingThresholds(s.AccountSchedulingThresholds)
			return nil
		}},
		{Module: "server", Apply: func(_ context.Context, s *composite.Snapshot) error {
			forwarded.Apply(runtimeconfig.ForwardedInput{APIKeyACLTrustForwardedIP: s.APIKeyACLTrustForwardedIP, ForwardedClientIPHeaders: s.ForwardedClientIPHeaders})
			return nil
		}},
		{Module: "gateway-client", Apply: func(_ context.Context, s *composite.Snapshot) error {
			gatewayRuntime.PublishCodexPlugin(s.OpenAIAllowClaudeCodeCodexPlugin)
			return nil
		}},
		{Module: "site", Apply: func(_ context.Context, _ *composite.Snapshot) error { store.NotifyUpdated(); return nil }},
		{Module: "creative", Apply: func(_ context.Context, s *composite.Snapshot) error {
			worker.SetWorkerCount(s.CreativeWorkerCount)
			return nil
		}},
		{Module: "ops", Apply: func(_ context.Context, s *composite.Snapshot) error {
			monitor.SetMonitoringEnabled(s.OpsMonitoringEnabled)
			return nil
		}},
	}
	return composite.NewRuntime(store, read, prepare, grants, gatewayRuntime, cfg.Totp.EncryptionKeyConfigured, steps)
}
