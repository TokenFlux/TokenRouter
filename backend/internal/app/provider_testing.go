package app

import (
	"context"
	"log"
	"log/slog"
	"slices"
	"time"

	"github.com/TokenFlux/TokenRouter/internal/app/lifecycle"
	"github.com/TokenFlux/TokenRouter/internal/config"
	"github.com/TokenFlux/TokenRouter/internal/egress"
	egressprovider "github.com/TokenFlux/TokenRouter/internal/egress/provider"
	"github.com/TokenFlux/TokenRouter/internal/gateway"
	gatewayprovider "github.com/TokenFlux/TokenRouter/internal/gateway/provider"
	"github.com/TokenFlux/TokenRouter/internal/infra/httpclient"
	"github.com/TokenFlux/TokenRouter/internal/infra/telemetry/logging"
	"github.com/TokenFlux/TokenRouter/internal/modelcatalog"
	catalogprovider "github.com/TokenFlux/TokenRouter/internal/modelcatalog/provider"
	openaiprotocol "github.com/TokenFlux/TokenRouter/internal/protocol/openai"
	"github.com/TokenFlux/TokenRouter/internal/provider"
	providerhttp "github.com/TokenFlux/TokenRouter/internal/provider/httpapi"
	providerpostgres "github.com/TokenFlux/TokenRouter/internal/provider/postgres"
	provideradapter "github.com/TokenFlux/TokenRouter/internal/provider/provider"
	"github.com/TokenFlux/TokenRouter/internal/routing"
	"github.com/TokenFlux/TokenRouter/internal/upstream/antigravity"
	openaiws "github.com/TokenFlux/TokenRouter/internal/upstream/openai/ws"
	"github.com/TokenFlux/TokenRouter/internal/upstream/qoder"
)

// provider_testing.go assembles provider test targets, scheduled runners, and
// shared agent identity preparation. Immediate, scheduled, and group
// availability tests share production provider targets and typed test events.
// Test assembly captures operator URL policy and platform credentials, while
// administrator Qoder probes own a separate session provider whose stop hook
// runs after periodic test producers stop.

// provideAgentTaskCoordinator constructs shared process-local agent
// registration coordination for persisted providers.
func provideAgentTaskCoordinator() *provider.OpenAITaskCoordinator {
	return &provider.OpenAITaskCoordinator{}
}

// provideProviderExpiry constructs one-minute provider expiry maintenance with
// shared storage and clock.
func provideProviderExpiry(store *providerpostgres.ProviderStore) *provider.ExpiryService {
	return provider.NewExpiryService(store, provider.ExpiryOptions{Interval: time.Minute, Now: time.Now, Observe: log.Printf})
}

// provideProviderProbeTasks binds agent identity tasks to conditional provider
// writes. It shares coordinator locks and connection invalidation, while
// registration uses the fixed OpenAI accounts endpoint and persistence retains
// its mutation error.
func provideProviderProbeTasks(store *providerpostgres.ProviderStore, connections *openaiws.OpenAIWSConnections, coordinator *provider.OpenAITaskCoordinator) *provideradapter.ProbeTasks {
	return &provideradapter.ProbeTasks{Coordinator: coordinator, Options: provider.OpenAITaskOptions{
		Read: store.GetByID,
		Register: func(ctx context.Context, value *provider.Record) (string, error) {
			return provideradapter.RegisterAgentIdentityTask(ctx, value, "https://auth.openai.com/api/accounts")
		},
		Persist: func(ctx context.Context, value *provider.Record, credentials map[string]any) error {
			_, err := provider.PersistCredentials(ctx, store, value, credentials, slog.Warn)
			return err
		},
		Invalidate: connections.InvalidateProvider,
	}}
}

// provideScheduledTests binds scheduled plan and result operations with clock
// and next-run calculation. Background execution is constructed by
// provideScheduledTestRunner and started later through the application's
// lifecycle registration.
func provideScheduledTests(plans provider.ScheduledTestPlanRepository, results provider.ScheduledTestResultRepository) *provider.ScheduledTestService {
	return provider.NewScheduledTestService(plans, results, provider.ScheduledTestOptions{Now: time.Now, NextRun: provideradapter.NextScheduledTestRun})
}

// provideScheduledTestRunner binds periodic tests and successful-test recovery
// using required cfg. Valid configured timezone replaces time.Local; failed
// loading retains the process location, and the runner receives a ten-second
// execution offset.
func provideScheduledTestRunner(plans provider.ScheduledTestPlanRepository, scheduled *provider.ScheduledTestService, tests *provider.TestService, recovery *provider.RecoveryService, cfg *config.Config) *provider.ScheduledTestRunnerService {
	location := time.Local
	// Preserve the initialized process location when configured zone loading fails.
	// The cron adapter then still receives a concrete location for its next-run calculations.
	if parsed, err := time.LoadLocation(cfg.Timezone); err == nil && parsed != nil {
		location = parsed
	}
	return provider.NewScheduledTestRunnerService(plans, scheduled, tests, provider.ScheduledRunnerOptions{Schedule: provideradapter.NewScheduledCron(location), Now: time.Now, NextRun: provideradapter.NextScheduledTestRun, Offset: 10 * time.Second, Observe: func(format string, args ...any) {
		logging.LegacyPrintf("service.scheduled_test_runner", format, args...)
	}, Recover: recovery.RecoverProviderAfterSuccessfulTest})
}

// provideProviderTests constructs shared platform test targets and their
// operator URL policy. cfg and manager are required; a nonnil catalog supplies
// capability snapshots. The attribute service resolves the requested group and
// final model, while Qoder tests receive a separate session provider
// with its own registered stop.
func provideProviderTests(store *providerpostgres.ProviderStore, geminiToken *provider.GeminiTokenSource, claudeToken *provider.ClaudeTokenSource, grokToken *provider.GrokTokenSource, ag *provideradapter.AntigravityProbe, transport httpclient.UpstreamTransport, cfg *config.Config, profiles *egressprovider.TLSProfiles, routers *egress.TLSFingerprintRouterService, settings *gateway.RuntimeSettings, tasks *provideradapter.ProbeTasks, manager *lifecycle.Manager, catalog *catalogprovider.Service, modelAttributes *routing.ModelAttributeService, system *provideradapter.SystemUsageTransport) *provider.TestService {
	if system != nil {
		transport = system
	}

	urlPolicy := egress.OperatorURLPolicy{Enabled: cfg.Security.URLAllowlist.Enabled, AllowInsecureHTTP: cfg.Security.URLAllowlist.AllowInsecureHTTP, AllowPrivateHosts: cfg.Security.URLAllowlist.AllowPrivateHosts, UpstreamHosts: slices.Clone(cfg.Security.URLAllowlist.UpstreamHosts)}
	probePolicy := &provideradapter.OpenAIProbePolicy{Available: true, ForceCLI: cfg.Gateway.OpenAI.Codex.ForceCLI, Read: store.GetByID, AllowClaudeCode: settings.IsOpenAIAllowClaudeCodeCodexPluginEnabled, BrowserUserAgent: settings.GetOpenAICodexUserAgent, DefaultBrowserUserAgent: gateway.DefaultOpenAICodexUserAgent, Routers: routers, Profiles: profiles, ManualProfiles: profiles}
	// Assemblies that supply only a catalogue still resolve default capabilities.
	// Production supplies the profile service so tests can load their selected group.
	if modelAttributes == nil && catalog != nil {
		modelAttributes = &routing.ModelAttributeService{Catalog: routing.ModelAttributeCatalog{View: catalog.CapabilityView}}
	}
	probeAttributes := func(ctx context.Context, value *provider.Record, groupID int64, model, protocol string) (modelcatalog.Resolution, error) {
		route := provider.TestProtocolRouteFromContext(ctx)
		if route.UpstreamProtocol != "" {
			protocol = string(route.UpstreamProtocol)
		}
		target := value.ModelAttributeTarget(model, protocol)
		target.ClientProtocol = protocol
		if route.ClientProtocol != "" {
			target.ClientProtocol = string(route.ClientProtocol)
		}
		session := routing.ModelAttributesFromContext(ctx)
		if session == nil {
			session = modelAttributes.NewSession()
		}
		return session.Resolve(ctx, groupID, target)
	}
	openaiTest := &provideradapter.OpenAIProviderTest{Attributes: probeAttributes, Store: store, Transport: transport, ValidateURL: urlPolicy.Validate, Prepare: probePolicy.Prepare, ApplyRouting: probePolicy.ApplyTestRouting, ResolveTLS: probePolicy.ResolveTestTLS, EnsureTask: tasks.Ensure}
	geminiTest := &provideradapter.GeminiProviderTest{Attributes: probeAttributes, Tokens: geminiToken, Transport: transport, Profiles: profiles, ValidateURL: urlPolicy.Validate}
	anthropicTest := &provideradapter.AnthropicProviderTest{Attributes: probeAttributes, Tokens: claudeToken, Transport: transport, Profiles: profiles, Store: store, ValidateURL: urlPolicy.Validate}
	// Administrator probes build Qoder sessions in their own provider. Register
	// its stop after periodic test producers so shared session builds finish before transport cleanup.
	qoderSessions := provideradapter.NewQoderTokenProvider(qoder.SessionBuilder{})
	qoderSessions.SetHTTPUpstream(transport, profiles)
	manager.Register(lifecycle.Hook{Name: "ProviderTestQoderSessions", StopOrder: 26, Stop: qoderSessions.StopContext})
	targets := &provideradapter.TestTargets{
		Jev:  &provideradapter.JevProviderTest{Attributes: probeAttributes, Transport: transport, ValidateURL: urlPolicy.Validate},
		Read: store.GetByID, OpenAI: openaiTest, Gemini: geminiTest, Anthropic: anthropicTest,
		Qoder:  &provideradapter.QoderProviderTest{Attributes: probeAttributes, Sessions: qoderSessions, Client: qoder.NewClient(qoder.APIBaseURL), Transport: transport, Profiles: profiles, RewriteModel: openaiprotocol.ReplaceModelInBody},
		Grok:   &provideradapter.GrokProviderTest{Attributes: probeAttributes, Tokens: grokToken, Transport: transport, Store: store, OperatorValidator: urlPolicy.Validate, DefaultBaseURL: gatewayprovider.GrokDefaultBaseURLReader(settings)},
		Native: &provideradapter.NativeProtocolTest{Attributes: probeAttributes, Transport: transport, Profiles: profiles, Store: store, ValidateURL: urlPolicy.Validate, Responses: openaiTest},
		Antigravity: &provideradapter.AntigravityProviderTest{Probe: func(ctx context.Context, value *provider.Record, request provider.PreparedTestRequest) (*antigravity.TestConnectionResult, error) {
			probe := *ag
			if system != nil {
				retry := *ag.Retry
				retry.Do = system.Do
				probe.Retry = &retry
			}
			probe.Attributes = probeAttributes
			return probe.Execute(ctx, value, request)
		}},
	}
	core := provider.NewTestService(targets, provider.TestOptions{Now: time.Now, PrepareContext: func(ctx context.Context) context.Context {
		if routing.ModelAttributesFromContext(ctx) != nil {
			return ctx
		}
		return routing.WithModelAttributes(ctx, modelAttributes)
	}, Error: func(message string) { log.Printf("Provider test error: %s", message) }, WriteError: func(err error) { log.Printf("failed to write SSE event: %v", err) }})
	return core
}

// provideProviderTestHTTP exposes the shared test service with successful-test
// recovery. The recovery callback discards the returned recovery description
// and returns its error for the HTTP handler to interpret.
func provideProviderTestHTTP(core *provider.TestService, recovery *provider.RecoveryService) *providerhttp.TestHandler {
	return providerhttp.NewTestHandler(core, func(ctx context.Context, id int64) error {
		_, err := recovery.RecoverProviderAfterSuccessfulTest(ctx, id)
		return err
	})
}
