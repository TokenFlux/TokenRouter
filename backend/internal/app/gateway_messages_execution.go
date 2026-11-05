package app

import (
	"context"
	"log/slog"
	"time"

	"github.com/TokenFlux/TokenRouter/internal/app/lifecycle"
	"github.com/TokenFlux/TokenRouter/internal/billing"
	"github.com/TokenFlux/TokenRouter/internal/config"
	"github.com/TokenFlux/TokenRouter/internal/egress"
	egressprovider "github.com/TokenFlux/TokenRouter/internal/egress/provider"
	"github.com/TokenFlux/TokenRouter/internal/gateway/errorpolicy"
	gatewayhttp "github.com/TokenFlux/TokenRouter/internal/gateway/httpapi"
	"github.com/TokenFlux/TokenRouter/internal/gateway/promptpolicy"
	gatewayadapter "github.com/TokenFlux/TokenRouter/internal/gateway/provider"
	"github.com/TokenFlux/TokenRouter/internal/gateway/provider/messageforward"
	"github.com/TokenFlux/TokenRouter/internal/gateway/provider/requestdebug"
	"github.com/TokenFlux/TokenRouter/internal/gateway/searchtools"
	"github.com/TokenFlux/TokenRouter/internal/gateway/telemetry"
	"github.com/TokenFlux/TokenRouter/internal/infra/httpclient"
	"github.com/TokenFlux/TokenRouter/internal/provider"
	provideradapter "github.com/TokenFlux/TokenRouter/internal/provider/provider"
	"github.com/TokenFlux/TokenRouter/internal/routing"
	"github.com/TokenFlux/TokenRouter/internal/settings"
	"github.com/TokenFlux/TokenRouter/internal/upstream/anthropic"
)

// gateway_messages_execution.go constructs Messages execution, error rules,
// debug tracing, and prompt replacement services.
// Messages execution receives platform credentials, model pricing, search,
// health, and transport through one runtime. Startup options control response
// limits and network policy, while runtime readers and the optional trace
// supply request-time settings, model attributes, and debug output.

// provideGatewayErrorRules binds persisted error rules, cache, and gateway
// telemetry.
func provideGatewayErrorRules(repo errorpolicy.ErrorPassthroughRepository, cache errorpolicy.ErrorPassthroughCache) *errorpolicy.ErrorPassthroughService {
	return errorpolicy.NewErrorPassthroughService(repo, cache, telemetry.ErrorRules)
}

// provideGatewayRequestDebug constructs the shared trace from gateway debug
// process settings. Call before lifecycle startup; its registered stop hook
// closes the trace after earlier request and completion shutdown levels.
func provideGatewayRequestDebug(manager *lifecycle.Manager, cfg *config.Config) *requestdebug.Trace {
	trace := requestdebug.New(cfg.Debug.GatewayBodyLogFile, cfg.Debug.ClaudeMimicLog)
	// The trace can be used by in-flight platform and completion work. Register
	// its close after those shutdown levels so their final diagnostics remain writable.
	manager.Register(lifecycle.Hook{Name: "GatewayRequestDebug", StopOrder: 90, Stop: func(context.Context) error { return trace.Close() }})
	return trace
}

// provideMessagesExecution assembles Messages forwarding, compatible
// conversions, and counting dependencies. Optional readers, model
// configuration, trace, and activity add their capabilities when present;
// startup options are captured by messageExecutionOptions and the returned
// executor uses the supplied response-header filter.
// @project-doc docs/architecture/system_architecture.md#dependency_layers
func provideMessagesExecution(credentials *provider.MessageCredentialSource, fingerprint *anthropic.RequestFingerprint, transport httpclient.UpstreamTransport, health *provideradapter.UpstreamHealth, tls *egressprovider.TLSProfiles, readers *gatewayadapter.RuntimeReaders, prices *billing.PriceResolver, search *searchtools.Emulator, activity *gatewayRequestActivity, debug *requestdebug.Trace, providers gatewayadapter.ExecutionProviderStore, deferred *provider.DeferredService, cfg *config.Config, filter *egress.CompiledHeaderFilter, modelConfigs *routing.PricingConfigService) *gatewayhttp.MessagesExecutor {
	deps := messageforward.Dependencies{Credentials: credentials, Fingerprint: fingerprint, Transport: transport, Health: health, TLS: tls, Prices: prices, Search: search, ProviderState: providers, Deferred: deferred}
	if modelConfigs != nil {
		deps.GroupPolicies = modelConfigs
	}
	if debug != nil {
		deps.Debug = debug
	}
	if readers != nil {
		deps.Settings = readers.Gateway
		deps.Attributes = readers.Attributes
	}
	if activity != nil {
		deps.Enter = activity.Enter
	}
	return gatewayhttp.NewMessagesExecutor(messageforward.NewRuntime(deps, messageExecutionOptions(cfg)), filter)
}

// messageExecutionOptions captures response, compatibility, stream, and
// destination settings at assembly. Nil cfg returns the default read limit;
// positive configured bytes replace it, stream values become seconds, and
// URLValidation retains the configured host slice.
func messageExecutionOptions(cfg *config.Config) messageforward.Options {
	options := messageforward.Options{ResponseReadLimit: config.DefaultUpstreamResponseReadMaxBytes}
	if cfg == nil {
		return options
	}
	options.Configured = true
	options.InjectAPIKeyBeta = cfg.Gateway.Anthropic.InjectBetaForAPIKey
	options.LogErrorBody = cfg.Gateway.UpstreamErrorBodyLog.Enabled
	options.LogErrorBodyMaxBytes = cfg.Gateway.UpstreamErrorBodyLog.MaxBytes
	options.FailoverOn400 = cfg.Gateway.Failover.MessagesOn400
	options.GeminiDebugHeaders = cfg.Gateway.Gemini.DebugResponseHeaders
	options.MaxLineSize = cfg.Gateway.MaxSSELineBytes
	// Upstream idle intervals use durations converted from configured seconds.
	options.StreamInterval = time.Duration(cfg.Gateway.Streaming.DataIdleTimeoutSeconds) * time.Second
	if cfg.Gateway.UpstreamResponseMaxBytes > 0 {
		options.ResponseReadLimit = cfg.Gateway.UpstreamResponseMaxBytes
	}
	options.PreserveContentType = !cfg.Gateway.ResponseHeaders.Enabled
	options.URLAllowlistEnabled = cfg.Security.URLAllowlist.Enabled
	options.AllowInsecureHTTP = cfg.Security.URLAllowlist.AllowInsecureHTTP
	options.URLValidation = egress.ValidationOptions{AllowedHosts: cfg.Security.URLAllowlist.UpstreamHosts, RequireAllowlist: true, AllowPrivate: cfg.Security.URLAllowlist.AllowPrivateHosts}
	return options
}

// provideGatewayPromptPolicy binds cached prompt replacement to shared settings
// and diagnostics.
func provideGatewayPromptPolicy(store *settings.Store) *promptpolicy.Service {
	return promptpolicy.New(store, settings.ErrSettingNotFound, slog.Warn)
}
