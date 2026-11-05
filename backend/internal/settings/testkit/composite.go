package testkit

import (
	"context"
	"log/slog"
	"time"

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
	"github.com/TokenFlux/TokenRouter/internal/provider"
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

// composite.go assembles module settings readers and runtime publishers for
// integration fixtures.
// Combined settings tests use the real module readers, normalization, and runtime
// publication callbacks around a fixture repository. The assembly holds startup
// configuration for later reads and gives tests control over plan lookup and
// worker-count observations; it creates these dependencies before requests perform
// persistence or trigger runtime application.
//
// Save prepares a supplied Snapshot, then commits its values with callbacks
// that use that prepared object. Production Runtime instead constructs callbacks
// that read committed settings before application. Keeping this distinction visible
// lets fixture tests assert normalization and presence handling while separate
// Runtime tests protect readback and publication ordering.

// PlanReader supplies saved subscription plans for billing validation in composite fixtures.
type PlanReader interface {
	// GetByID loads the plan selected for a default subscription. Billing
	// validation requires a nonnil result with a positive ID, and lookup errors
	// prevent preparing the requested grant values for persistence.
	GetByID(context.Context, int64) (*billing.SubscriptionPlan, error)
}

// Composite exposes the real module instances and assembly options used by tests.
type Composite struct {
	// Applications is the list used by Save. Runtime copies the outer list at
	// construction; callbacks and captured module pointers remain shared by their
	// registered values, so tests install dependencies before concurrent use.
	Applications []composite.Application          // fixture post-commit application steps
	Runtime      *composite.Runtime               // committed-readback settings runtime instance
	Store        *settings.Store                  // shared fixture settings store
	Read         composite.ReadOptions            // module read options for snapshots
	Prepare      composite.PrepareOptions         // normalization and validation options
	Gateway      *gateway.RuntimeSettings         // gateway setting reader and publisher
	Provider     *provider.RuntimeSettings        // provider runtime threshold setting reader
	Quota        *provider.QuotaSettingsCache     // OpenAI quota threshold cache
	Identity     *identity.RuntimeSettings        // identity runtime policy setting reader
	Promotion    *promotion.RuntimeSettings       // promotion and rebate setting reader
	Backend      *admission.BackendMode           // backend-mode cached admission reader
	Forwarded    *runtimeconfig.ForwardedSettings // forwarded-IP runtime settings publisher
	Scheduler    *scheduler.SettingsRuntime       // shared scheduler runtime settings publisher
	Defaults     scheduler.AdminDefaults          // deployment scheduler parameter defaults
	Public       *site.PublicService              // public settings response reader
	// Plans is consulted when default subscriptions are validated. Nil performs
	// billing's duplicate checks with no plan lookup; an installed reader must
	// return a nonnil plan with a positive ID and remain stable during preparation.
	Plans PlanReader // subscription plan lookup dependency
	// WorkerChanged is optional and read by the Creative application callback.
	// Configure it before Save can invoke that callback; publication and its
	// captured fixture fields have no extra synchronization in this assembly.
	WorkerChanged func(int) // worker count observation callback
}

// NewComposite assembles real module readers and application callbacks around
// repo for settings tests. Nil cfg becomes an empty configuration; the supplied
// configuration remains available to captured defaults and forwarded-IP readers,
// so callers keep its fields stable except through their coordinated publishers.
//
// The fixture creates a shared Store, normalization options, runtime caches, and
// public reader before any Save. Plan lookup and worker observations are configured
// on the returned fixture before use. No storage read or task start occurs in
// construction, but later Parse calls can publish the global Grok model through
// the installed callback, and Save invokes its ordered runtime applications.
func NewComposite(repo settings.Repository, cfg *config.Config) *Composite {
	if cfg == nil {
		cfg = &config.Config{}
	}
	store := settings.New(repo)
	readers := gatewaytestkit.RuntimeReaders(store)
	defaults := scheduler.DefaultAdminSettingsDefaults()
	source := cfg.Scheduling.Advanced
	defaults.TopK = source.LBTopK
	defaults.Weights = source.ScoreWeights
	defaults.Process.EwmaErrorRateAlpha = source.EWMAErrorRateAlpha
	defaults.Process.EwmaTTFTAlpha = source.EWMATTFTAlpha
	// Convert the deployment TTFT value to the runtime policy's floating-point
	// milliseconds before normalization, keeping cadence and error-rate settings in their named units.
	defaults.Process.StickyEscape = policy.NormalizeStickyEscape(policy.StickyEscapeConfig{Enabled: source.StickyEscapeEnabled, TtftMs: float64(source.StickyEscapeTTFTMs), ErrorRate: source.StickyEscapeErrorRate})
	result := &Composite{Store: store, Gateway: readers.Gateway, Provider: readers.Provider, Quota: readers.Quota, Identity: identity.NewRuntimeSettings(store, settings.ErrSettingNotFound), Promotion: promotion.NewRuntimeSettings(store), Backend: admission.NewBackendMode(store, slog.Warn), Defaults: defaults, Scheduler: scheduler.NewSettingsRuntime(scheduler.Diagnostics{})}
	oauth := identity.NewOAuthSettings(store, &identity.OAuthSettingsDefaults{LinuxDo: cfg.Auth.OAuth.LinuxDo, DingTalk: cfg.Auth.OAuth.DingTalk.IdentityDefaults(), OIDC: cfg.Auth.OAuth.OIDC, WeChat: cfg.Auth.OAuth.WeChat.IdentityDefaults(), GitHubOAuth: cfg.Auth.OAuth.GitHub, GoogleOAuth: cfg.Auth.OAuth.Google}, identityprovider.ResolveSettingsOIDCMetadata)
	validate := func(ctx context.Context, items []billing.DefaultSubscriptionSetting) error {
		if result.Plans == nil {
			return billing.ValidateDefaultSubscriptionPlans(ctx, items, nil)
		}
		return billing.ValidateDefaultSubscriptionPlans(ctx, items, result.Plans.GetByID)
	}
	grants := identity.NewGrantSettings(store, identity.GrantSettingsOptions{DefaultBalance: cfg.UserDefaults.Balance, DefaultConcurrency: cfg.UserDefaults.Concurrency, ValidatePlans: validate})
	rules := gateway.AdminSettingsRules{GrokDefaultTextModel: grok.DefaultTextModel, NormalizeUserAgentVersion: antigravity.NormalizeUserAgentVersion, ValidateClaudePromptBlocks: anthropic.ValidateClaudeOAuthSystemPromptBlocksConfig}
	result.Forwarded = runtimeconfig.NewForwardedSettings(store, runtimeconfig.ForwardedSettingsOptions{InitialTrust: cfg.Server.ClientIP.TrustForwardedHeaders, TrustedProxiesConfigured: cfg.Server.ClientIP.TrustedProxiesConfigured, Headers: func() []string { return cfg.ForwardedClientIPSettings().Headers }, Publish: cfg.SetForwardedClientIPSettings})
	result.Read = composite.ReadOptions{OAuth: oauth, Gateway: rules, Scheduler: defaults, DefaultBalance: func() float64 { return cfg.UserDefaults.Balance }, DefaultConcurrency: func() int { return cfg.UserDefaults.Concurrency }, Forwarded: func() runtimeconfig.ForwardedInput {
		value := cfg.ForwardedClientIPSettings()
		return runtimeconfig.ForwardedInput{APIKeyACLTrustForwardedIP: value.TrustForwardedIP, ForwardedClientIPHeaders: value.Headers}
	}, PublishModel: func(model string) {
		grok.SetRuntimeDefaultTextModel(model)
	}}
	result.Prepare = composite.PrepareOptions{ReadValues: store.GetAll, Gateway: rules, Scheduler: defaults, ValidatePlans: validate}
	steps := []composite.Application{
		{Module: "gateway", Apply: func(_ context.Context, s *composite.Snapshot) error {
			// Publish admission, forwarding, and user-agent values from this prepared
			// snapshot before invalidating prompt compilation for later fixture requests.
			result.Backend.Publish(s.BackendModeEnabled)
			result.Gateway.PublishForwarding(s.MinClaudeCodeVersion, s.MaxClaudeCodeVersion, gateway.ForwardingSnapshot{TTFTMode: gateway.NormalizeTTFTMode(s.TTFTMode), FingerprintUnification: s.EnableFingerprintUnification, MetadataPassthrough: s.EnableMetadataPassthrough, CCHSigning: s.EnableCCHSigning, ClaudeOAuthSystemPromptInjection: s.EnableClaudeOAuthSystemPromptInjection, ClaudeOAuthSystemPrompt: s.ClaudeOAuthSystemPrompt, ClaudeOAuthSystemPromptBlocks: s.ClaudeOAuthSystemPromptBlocks, AnthropicCacheTTL1hInjection: s.EnableAnthropicCacheTTL1hInjection, RewriteMessageCacheControl: s.RewriteMessageCacheControl, ClientDatelineNormalization: s.EnableClientDatelineNormalization})
			result.Gateway.PublishClientUserAgents(s.AntigravityUserAgentVersion, s.OpenAICodexUserAgent)
			// Reset compiled prompt replacement after gateway publication so the next
			// fixture request recompiles against the settings just committed by this update.
			promptpolicy.SharedCache().Store((*promptpolicy.CompiledConfig)(nil))
			return nil
		}},
		{Module: "scheduler", Apply: func(_ context.Context, s *composite.Snapshot) error {
			result.Scheduler.Store(scheduler.RuntimeSettingsFromAdmin(s.SchedulerAdminSettings(), defaults))
			return nil
		}},
		{Module: "provider", Apply: func(_ context.Context, s *composite.Snapshot) error {
			result.Quota.Apply(s.OpenAIQuotaAutoPauseSettings, s.OpenAIQuotaAutoPauseSettingsSet)
			result.Provider.ApplySchedulingThresholds(s.ProviderSchedulingThresholds)
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
	result.Runtime = composite.NewRuntime(store, result.Read, result.Prepare, grants, result.Gateway, cfg.Security.SecretEncryptionKeyConfigured, steps)
	result.Public = site.NewPublicService(site.NewInputSource(store, site.PublicInputOptions{Auth: func(raw map[string]string) site.PublicAuth {
		value := oauth.PublicSettingsFromValues(raw)
		enabled, selfService := team.PublicSettings(raw, cfg.Team.Enabled, cfg.Team.SelfServiceEnabled)
		return site.PublicAuth{LinuxDo: value.LinuxDo, DingTalk: value.DingTalk, OIDC: value.OIDC, OIDCName: value.OIDCName, WeChat: value.WeChat, WeChatOpen: value.WeChatOpen, WeChatMP: value.WeChatMP, WeChatMobile: value.WeChatMobile, GitHub: value.GitHub, Google: value.Google, GoogleOneTap: value.GoogleOneTap, GoogleClientID: value.GoogleClientID, Team: enabled, TeamSelfService: selfService, Passkey: cfg.Auth.WebAuthn.Enabled, TencentRegion: value.TencentRegion, AliyunRegion: value.AliyunRegion, RegistrationSuffixes: value.RegistrationSuffixes}
	}, Usage: func(raw map[string]string) site.PublicUsage {
		v := usage.ParseRankingSettings(raw)
		return site.PublicUsage{Limit: v.Limit, Enabled: v.Enabled, SortBy: string(v.SortBy), ShowTotalTokens: v.ShowTotalTokens, ShowRequests: v.ShowRequests, ShowActualCost: v.ShowActualCost}
	}, Version: store.Version}), timezone.NewCalendar(time.Local), "")
	return result
}

// Save prepares the supplied nonnil Snapshot, persists its combined values, and
// invokes fixture application steps with that prepared object. It holds a Store
// update session through these phases and closes it before returning.
//
// Preparation can normalize the caller's Snapshot before an error. The fixture
// callbacks capture that same object, so callers keep it stable until the session
// finishes; they differ from Runtime's committed-readback application callbacks.
// Persistence failure leaves those steps uncalled, while application errors follow
// UpdateSession's persisted=true reporting and continuation rules.
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
	// Bind each application to the prepared object and its presence flags for
	// fixture assertions; production Runtime application callbacks obtain their own committed readback.
	for _, step := range f.Applications {
		changes = append(changes, settings.PreparedChange{Module: step.Module, Apply: func(ctx context.Context) error {
			return step.Apply(ctx, value)
		}})
	}
	return session.Commit(changes...)
}
