package app

import (
	"context"

	"github.com/TokenFlux/TokenRouter/internal/audit"
	"github.com/TokenFlux/TokenRouter/internal/billing"
	"github.com/TokenFlux/TokenRouter/internal/bstats"
	"github.com/TokenFlux/TokenRouter/internal/config"
	"github.com/TokenFlux/TokenRouter/internal/creative"
	"github.com/TokenFlux/TokenRouter/internal/gateway"
	"github.com/TokenFlux/TokenRouter/internal/gateway/admission"
	"github.com/TokenFlux/TokenRouter/internal/gateway/promptpolicy"
	"github.com/TokenFlux/TokenRouter/internal/gateway/ws"
	"github.com/TokenFlux/TokenRouter/internal/identity"
	"github.com/TokenFlux/TokenRouter/internal/moderation"
	"github.com/TokenFlux/TokenRouter/internal/notification"
	"github.com/TokenFlux/TokenRouter/internal/ops"
	"github.com/TokenFlux/TokenRouter/internal/payment"
	"github.com/TokenFlux/TokenRouter/internal/promotion"
	"github.com/TokenFlux/TokenRouter/internal/provider"
	"github.com/TokenFlux/TokenRouter/internal/routing"
	"github.com/TokenFlux/TokenRouter/internal/scheduler"
	"github.com/TokenFlux/TokenRouter/internal/server/runtimeconfig"
	"github.com/TokenFlux/TokenRouter/internal/settings"
	"github.com/TokenFlux/TokenRouter/internal/settings/composite"
	settingshttp "github.com/TokenFlux/TokenRouter/internal/settings/httpapi"
	"github.com/TokenFlux/TokenRouter/internal/site"
	"github.com/TokenFlux/TokenRouter/internal/team"
	"github.com/TokenFlux/TokenRouter/internal/upstream/grok"
	"github.com/TokenFlux/TokenRouter/internal/usage"
)

// settings_composite_runtime.go assembles combined settings preparation and the
// ordered application callbacks used after saving. One runtime receives
// module-owned preparers and callbacks that publish WS parameters before
// gateway settings and notify site readers before dynamic worker changes. The
// participant registry supplies payment provider refresh.

// provideCompositeSettingsHTTP joins combined settings and module-specific
// administration capabilities.
func provideCompositeSettingsHTTP(source *composite.Runtime, registry *settings.Registry, monitor *ops.OpsService, pay *payment.ConfigService, turnstile *identity.TurnstileService, aliyun *identity.AliyunCaptchaService, attributes *identity.UserAttributeService, totp *identity.TotpService, users *identity.UserService, creative *creative.Public) *settingshttp.Handler {
	return settingshttp.NewHandler(settingshttp.HandlerOptions{Settings: source, Participants: registry, Monitoring: monitor, Payment: pay, Turnstile: turnstile, Aliyun: aliyun, Attributes: attributes, Totp: totp, User: users, Creative: creative})
}

// provideCompositeReadOptions binds settings interpretation and current process
// default readers. cfg, gatewayRules, and defaults are required; forwarded
// headers come from cfg's published values, while Grok default-model
// publication uses the platform setter.
func provideCompositeReadOptions(cfg *config.Config, oauth *identity.OAuthSettings, gatewayRules *gateway.AdminSettingsRules, defaults *scheduler.AdminDefaults, sockets *ws.Runtime) *composite.ReadOptions {
	return &composite.ReadOptions{ResponsesWS: sockets, OAuth: oauth, Gateway: *gatewayRules, Scheduler: *defaults, DefaultBalance: func() float64 { return cfg.UserDefaults.Balance }, DefaultConcurrency: func() int { return cfg.UserDefaults.Concurrency }, Forwarded: func() runtimeconfig.ForwardedInput {
		value := cfg.ForwardedClientIPSettings()
		return runtimeconfig.ForwardedInput{APIKeyACLTrustForwardedIP: value.TrustForwardedIP, ForwardedClientIPHeaders: value.Headers}
	}, PublishModel: func(model string) {
		grok.SetRuntimeDefaultTextModel(model)
	}}
}

// provideCompositeRuntime binds preparation and ordered post-save application
// callbacks. Metrics reconcile first so a saved opt-out reaches the worker
// before other runtime updates. The WS step can return its publication error;
// subsequent steps publish gateway, scheduler, provider, forwarded-IP, and client settings, then
// notify site and adjust creative workers plus Ops monitoring. Persistence and
// application-error handling remain owned by composite.Runtime.
func provideCompositeRuntime(store *settings.Store, cfg *config.Config, read *composite.ReadOptions, grants *identity.GrantSettings, gatewayRuntime *gateway.RuntimeSettings, gatewayRules *gateway.AdminSettingsRules, defaults *scheduler.AdminDefaults, plans *billing.Plans, backendMode *admission.BackendMode, providerRuntime *provider.RuntimeSettings, quota *provider.QuotaSettingsCache, forwarded *runtimeconfig.ForwardedSettings, shared *schedulerSharedState, worker *creative.CreativeWorkerRuntime, monitor *ops.OpsService, metrics *bstats.Service) *composite.Runtime {
	prepare := composite.PrepareOptions{ReadValues: store.GetAll, Gateway: *gatewayRules, Scheduler: *defaults, ValidatePlans: func(ctx context.Context, value []billing.DefaultSubscriptionSetting) error {
		return billing.ValidateDefaultSubscriptionPlans(ctx, value, plans.GetPlan)
	}}
	steps := []composite.Application{
		{Module: "bstats", Apply: func(ctx context.Context, s *composite.Snapshot) error {
			return metrics.Apply(ctx, s.BStatsConfig())
		}},
		{Module: "responses-ws", Apply: func(_ context.Context, s *composite.Snapshot) error {
			if read.ResponsesWS != nil {
				return read.ResponsesWS.Publish(string(s.ResponsesWS))
			}
			return nil
		}},
		{Module: "gateway", Apply: func(_ context.Context, s *composite.Snapshot) error {
			backendMode.Publish(s.BackendModeEnabled)
			gatewayRuntime.PublishForwarding(s.MinClaudeCodeVersion, s.MaxClaudeCodeVersion, gateway.ForwardingSnapshot{TTFTMode: gateway.NormalizeTTFTMode(s.TTFTMode), FingerprintUnification: s.EnableFingerprintUnification, MetadataPassthrough: s.EnableMetadataPassthrough, CCHSigning: s.EnableCCHSigning, ClaudeOAuthSystemPromptInjection: s.EnableClaudeOAuthSystemPromptInjection, ClaudeOAuthSystemPrompt: s.ClaudeOAuthSystemPrompt, ClaudeOAuthSystemPromptBlocks: s.ClaudeOAuthSystemPromptBlocks, AnthropicCacheTTL1hInjection: s.EnableAnthropicCacheTTL1hInjection, RewriteMessageCacheControl: s.RewriteMessageCacheControl, ClientDatelineNormalization: s.EnableClientDatelineNormalization})
			gatewayRuntime.PublishClientUserAgents(s.AntigravityUserAgentVersion, s.OpenAICodexUserAgent)
			// Gateway settings have been published for later requests. Invalidate the
			// compiled prompt cache so its next read rebuilds rules from the updated settings values.
			promptpolicy.SharedCache().Store((*promptpolicy.CompiledConfig)(nil))
			return nil
		}},
		{Module: "scheduler", Apply: func(_ context.Context, s *composite.Snapshot) error {
			shared.Settings.Store(scheduler.RuntimeSettingsFromAdmin(s.SchedulerAdminSettings(), *defaults))
			return nil
		}},
		{Module: "provider", Apply: func(_ context.Context, s *composite.Snapshot) error {
			quota.Apply(s.OpenAIQuotaAutoPauseSettings, s.OpenAIQuotaAutoPauseSettingsSet)
			providerRuntime.ApplySchedulingThresholds(s.ProviderSchedulingThresholds)
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
	return composite.NewRuntime(store, *read, prepare, grants, gatewayRuntime, cfg.Security.SecretEncryptionKeyConfigured, steps)
}

// provideSettingsParticipants constructs the module registry from static field
// and key ownership. Registry errors propagate to application construction, and
// WS participant defaults come from the supplied runtime rather than a separate
// settings snapshot.
func provideSettingsParticipants(runtime *payment.Runtime, grants *identity.GrantSettings, schedulerDefaults *scheduler.AdminDefaults, gatewayRules *gateway.AdminSettingsRules, sockets *ws.Runtime) (*settings.Registry, error) {
	return settings.NewRegistry(staticSettingsParticipants(runtime, grants, schedulerDefaults, gatewayRules, sockets.Defaults())...)
}

// staticSettingsParticipants returns preparers for the combined settings
// registry in their listed order. No socket argument uses built-in WS defaults;
// the first supplied argument replaces them, and additional socket arguments
// are ignored.
func staticSettingsParticipants(runtime *payment.Runtime, grants *identity.GrantSettings, schedulerDefaults *scheduler.AdminDefaults, gatewayRules *gateway.AdminSettingsRules, socketDefaults ...ws.Parameters) []settings.Participant {
	defaults := ws.DefaultParameters()
	if len(socketDefaults) > 0 {
		defaults = socketDefaults[0]
	}
	return []settings.Participant{bstats.SettingsParticipant(), ws.SettingsParticipant(defaults), settings.LocalizationParticipant(), identity.SettingsParticipant(grants), team.SettingsParticipant(), moderation.SettingsParticipant(), creative.SettingsParticipant(), runtimeconfig.SettingsParticipant(), billing.SettingsParticipant(), scheduler.SettingsParticipant(*schedulerDefaults), routing.SettingsParticipant(), site.SettingsParticipant(), provider.SettingsParticipant(), ops.SettingsParticipant(), payment.VisibleSettingsParticipant(), notification.SMTPSettingsParticipant(), promotion.SettingsParticipant(), usage.SettingsParticipant(), audit.SettingsParticipant(), gateway.FastSettingsParticipant(), gateway.AdminSettingsParticipant(*gatewayRules), payment.SettingsParticipant(runtime.RefreshProvidersChecked)}
}
