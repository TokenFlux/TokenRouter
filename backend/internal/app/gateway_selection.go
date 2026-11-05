package app

import (
	"context"
	"log/slog"
	"time"

	"github.com/TokenFlux/TokenRouter/internal/billing"
	"github.com/TokenFlux/TokenRouter/internal/config"
	"github.com/TokenFlux/TokenRouter/internal/egress"
	"github.com/TokenFlux/TokenRouter/internal/gateway"
	"github.com/TokenFlux/TokenRouter/internal/gateway/failover"
	gatewayhttp "github.com/TokenFlux/TokenRouter/internal/gateway/httpapi"
	gatewayadapter "github.com/TokenFlux/TokenRouter/internal/gateway/provider"
	"github.com/TokenFlux/TokenRouter/internal/gateway/provider/selection"
	"github.com/TokenFlux/TokenRouter/internal/gateway/session"
	"github.com/TokenFlux/TokenRouter/internal/infra/telemetry/logging"
	"github.com/TokenFlux/TokenRouter/internal/moderation"
	"github.com/TokenFlux/TokenRouter/internal/provider"
	"github.com/TokenFlux/TokenRouter/internal/provider/postgres"
	provideradapter "github.com/TokenFlux/TokenRouter/internal/provider/provider"
	"github.com/TokenFlux/TokenRouter/internal/routing"
	"github.com/TokenFlux/TokenRouter/internal/routing/capability"
	"github.com/TokenFlux/TokenRouter/internal/scheduler"
	"github.com/TokenFlux/TokenRouter/internal/search"
	"github.com/TokenFlux/TokenRouter/internal/settings"
	"github.com/TokenFlux/TokenRouter/internal/upstream/antigravity"
	"github.com/TokenFlux/TokenRouter/internal/upstream/openai"
	"github.com/TokenFlux/TokenRouter/internal/usage"
	usagepostgres "github.com/TokenFlux/TokenRouter/internal/usage/postgres"
)

// gateway_selection.go constructs gateway provider selection, model
// diagnostics, and runtime policy readers.
// Provider selection shares feedback, group policies, quota gates, containment,
// and sticky-session statistics across execution paths. Assembly captures
// startup scheduling options but binds dynamic readers for platform policy,
// while diagnostic functions distinguish general, compatible, and already-
// resolved routing models.

// gatewayModelAvailability holds diagnostic readers for three gateway model-
// resolution cases.
type gatewayModelAvailability struct {
	// Messages uses the general reader. Compatible resolves compatible models,
	// while Resolved accepts the compatible routing identity; all are built from the same stores.
	Messages   routing.ModelAvailabilityDiagnoser // general request diagnosis
	Compatible routing.ModelAvailabilityDiagnoser // compatible request diagnosis
	Resolved   routing.ModelAvailabilityDiagnoser // resolved routing diagnosis
}

// provideGatewayModelAvailability constructs general and compatible diagnostics
// from persistent provider reads. Nil store leaves the provider source unset;
// Compatible and Resolved use the same compatible reader with different model-
// resolution entry methods.
func provideGatewayModelAvailability(store *postgres.ProviderStore, modelConfigs *routing.PricingConfigService) *gatewayModelAvailability {
	var source gatewayadapter.AvailabilityProviders
	if store != nil {
		source = store
	}
	general := gatewayadapter.NewModelAvailability(source, modelConfigs, false)
	compatible := gatewayadapter.NewModelAvailability(source, modelConfigs, true)
	return &gatewayModelAvailability{
		Messages:   routing.ModelAvailabilityDiagnoserFunc(general.DiagnoseGeneral),
		Compatible: routing.ModelAvailabilityDiagnoserFunc(compatible.DiagnoseCompatible),
		Resolved:   routing.ModelAvailabilityDiagnoserFunc(compatible.DiagnoseCompatibleRouting),
	}
}

// provideGatewayRuntimeReaders binds shared dynamic readers and installs
// platform user-agent resolvers. gatewayRuntime is required for those
// callbacks; platform libraries read the resulting settings on demand,
// including Codex through an independent background context.
func provideGatewayRuntimeReaders(store *settings.Store, gatewayRuntime *gateway.RuntimeSettings, providerRuntime *provider.RuntimeSettings, quota *provider.QuotaSettingsCache, routingRuntime *routing.RuntimeSettings, moderationRuntime *moderation.RuntimeSettings, searchRuntime *search.ConfigService, attributes *routing.ModelAttributeService) *gatewayadapter.RuntimeReaders {
	antigravity.SetUserAgentVersionResolver(gatewayRuntime.GetAntigravityUserAgentVersion)
	openai.SetCodexCanonicalUserAgentResolver(func() string { return gatewayRuntime.GetOpenAICodexUserAgent(context.Background()) })
	readers := &gatewayadapter.RuntimeReaders{Attributes: attributes, Gateway: gatewayRuntime, Provider: providerRuntime, Quota: quota, Routing: routingRuntime, Moderation: moderationRuntime, Search: searchRuntime, Scheduler: store}
	return readers
}

// provideModerationSettings binds gateway moderation policy to the shared
// settings store.
func provideModerationSettings(store *settings.Store) *moderation.RuntimeSettings {
	return moderation.NewRuntimeSettings(store, settings.ErrSettingNotFound)
}

// provideSelectionReads groups provider, group, and snapshot reads for
// selection.
func provideSelectionReads(providers gatewayadapter.ExecutionProviderStore, groups routing.GroupRepository, snapshots selection.Snapshots) selection.Reads {
	return selection.Reads{Providers: providers, Groups: groups, Snapshot: snapshots}
}

// provideSelectionShared binds shared selection feedback, parameters, health,
// and group policy.
func provideSelectionShared(cache session.GatewayCache, concurrency *scheduler.ConcurrencyService, health *provideradapter.UpstreamHealth, modelConfigs *routing.PricingConfigService, shared *schedulerSharedState) selection.Shared {
	return selection.Shared{
		Cache:         cache,
		Concurrency:   concurrency,
		Health:        health,
		GroupPolicies: modelConfigs,
		Parameters:    shared.Parameters,
		Feedback:      shared.Feedback,
	}
}

// selectionOptions returns default options plus routing-debug environment
// interpretation for nil cfg. Configured scheduling and legacy sticky flags
// pass through unchanged; positive session and response TTL seconds replace the
// default lifetimes.
func selectionOptions(cfg *config.Config) selection.Options {
	options := selection.DefaultOptions()
	if cfg == nil {
		return options
	}
	options.DebugRouting = cfg.Debug.ModelRoutingLog
	s := cfg.Scheduling
	options.Scheduling = scheduler.FlowOptions{LoadBatchEnabled: s.LoadBatch.Enabled, PreferSoonestReset: s.PreferSoonestReset, FallbackMaxWaiting: s.AlternateProviderWait.MaxWaiting, StickySessionMaxWaiting: s.StickyProviderWait.MaxWaiting, FallbackSelectionMode: s.AlternateSelectionMode, FallbackWaitTimeout: time.Duration(s.AlternateProviderWait.TimeoutSeconds) * time.Second, StickySessionWaitTimeout: time.Duration(s.StickyProviderWait.TimeoutSeconds) * time.Second}
	ws := cfg.Scheduling.SessionAffinity
	options.ReadLegacySticky = ws.ReadLegacyHash
	options.WriteLegacySticky = ws.DualWriteLegacyHash
	if ws.TTLSeconds > 0 {
		// Session-affinity expiry receives the configured lifetime expressed in seconds.
		options.StickyTTL = time.Duration(ws.TTLSeconds) * time.Second
	}
	if ws.ResponseIDTTLSeconds > 0 {
		// Response affinity receives the configured lifetime expressed in seconds.
		options.ResponseTTL = time.Duration(ws.ResponseIDTTLSeconds) * time.Second
	}
	return options
}

// provideSelectionModelTransient constructs shared model-transient state using
// its default capacity.
func provideSelectionModelTransient() *provider.ModelTransientState {
	return provider.NewModelTransientState(0)
}

// provideSelectionProxyCircuit applies positive thresholds and durations to
// default proxy-circuit settings. Disabled is copied directly when cfg exists,
// while nonpositive window and quarantine seconds retain the constructor
// options' defaults.
func provideSelectionProxyCircuit(cfg *config.Config) *egress.ProxyStreamCircuit {
	options := egress.DefaultProxyStreamCircuitSettings()
	if cfg != nil {
		value := cfg.Gateway.OpenAI.ProxyStreamCircuit
		options.Disabled = !value.Enabled
		if value.FailureThreshold > 0 {
			options.FailureThreshold = value.FailureThreshold
		}
		if value.WindowSeconds > 0 {
			// Failure accumulation uses the configured circuit window converted from seconds.
			options.FailureWindow = time.Duration(value.WindowSeconds) * time.Second
		}
		if value.TTLSeconds > 0 {
			// Quarantine expiry uses the configured circuit lifetime converted from seconds.
			options.QuarantineTTL = time.Duration(value.TTLSeconds) * time.Second
		}
	}
	return egress.NewProxyStreamCircuit(options)
}

// provideGenericSelection binds generic selection to cost guards, RPM,
// sessions, and free-quota gates. nativeUsage supplies statistics, gates
// supplies the configured guard, and window prefetch is enabled only when both
// windows and source are present.
func provideGenericSelection(reads selection.Reads, shared selection.Shared, cfg *config.Config, windows billing.WindowCostCache, source usage.UsageLogRepository, nativeUsage *usagepostgres.Store, rpm scheduler.RPMCache, sessions scheduler.SessionLimitCache, gates *selectionFreeQuotaGates, providers gatewayadapter.ExecutionProviderStore) *selection.Generic {
	stats := usageWindowStats{nativeUsage}
	guard := billing.NewWindowCostGuard(windows, stats, billing.WindowCostGuardOptions{Now: time.Now, Stats: billing.SharedWindowCostMetrics(), Log: func(format string, args ...any) { logging.LegacyPrintf("service.gateway", format, args...) }, Debug: slog.Debug})
	var write func(context.Context, int64, string) error
	if providers != nil {
		write = providers.SetError
	}
	return selection.NewGeneric(selection.GenericDependencies{
		Reads:                   reads,
		Shared:                  shared,
		Window:                  guard,
		WindowPrefetchAvailable: windows != nil && source != nil,
		RPM:                     rpm,
		Sessions:                sessions,
		FreeQuota:               gates.Generic,
		SetProviderError:        write,
	}, selectionOptions(cfg))
}

// provideCompatibleSelection combines generic and Gemini selection with shared
// health and sticky state. state and gates provide required fields; options
// capture startup policy while quota, response state, and runtime blocks remain
// shared dependencies.
func provideCompatibleSelection(generic *selection.Generic, gemini *selection.Gemini, reads selection.Reads, shared selection.Shared, cfg *config.Config, responses session.OpenAIWSStateStore, quota *provider.QuotaSettingsCache, blocks *provider.RuntimeBlockState, transient *provider.ModelTransientState, proxy *egress.ProxyStreamCircuit, state *schedulerSharedState, gates *selectionFreeQuotaGates) *selection.Compatible {
	return selection.NewCompatible(selection.CompatibleDependencies{
		Generic: generic, Gemini: gemini,
		Reads:                reads,
		Shared:               shared,
		Responses:            responses,
		QuotaSettings:        quota,
		RuntimeBlocks:        blocks,
		ModelTransient:       transient,
		ProxyCircuit:         proxy,
		StickyStats:          state.Sticky,
		FreeQuota:            gates.Compatible,
		NewAdvancedFreeQuota: gates.Advanced,
	}, selectionOptions(cfg))
}

// provideGeminiSelection binds candidate reads and shared policy with Gemini
// quota precheck.
func provideGeminiSelection(reads selection.Reads, shared selection.Shared, cfg *config.Config, quota *provider.GeminiPrecheck) *selection.Gemini {
	return selection.NewGemini(selection.GeminiDependencies{Reads: reads, Shared: shared, QuotaPrecheck: quota}, selectionOptions(cfg))
}

// gatewayCompatibilitySnapshot returns current sticky totals and the hit
// fraction. Missing shared state or sticky counters yields an empty snapshot,
// and a zero total keeps the fraction zero; metadata counters remain unset.
func gatewayCompatibilitySnapshot(shared *schedulerSharedState) gatewayhttp.CompatibilityLogSnapshot {
	if shared == nil || shared.Sticky == nil {
		return gatewayhttp.CompatibilityLogSnapshot{}
	}
	total, hit, dual := shared.Sticky.Snapshot()
	rate := float64(0)
	if total > 0 {
		// Compatibility logging displays hits as a fraction of recorded reads.
		rate = float64(hit) / float64(total)
	}
	return gatewayhttp.CompatibilityLogSnapshot{ReadTotal: total, ReadHit: hit, DualWrite: dual, ReadHitRate: rate}
}

// stopOpenAI429 delegates retry-budget decisions with the provider's OAuth
// eligibility. A nil target supplies false eligibility flags; the supplied
// state remains owned by the retry loop whose decisions StopOAuth429 records.
func stopOpenAI429(value *gatewayadapter.ExecutionProvider, status, switches int, state *failover.OAuth429State) bool {
	return failover.StopOAuth429(failover.OAuth429Provider{OpenAI: value != nil && value.View().IsOpenAIOAuthLike(), Grok: value != nil && value.Record.Platform == capability.PlatformGrok && value.Record.Type == capability.ProviderTypeOAuth}, status, switches, state)
}
