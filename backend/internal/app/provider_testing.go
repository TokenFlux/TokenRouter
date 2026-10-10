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
	openaiprotocol "github.com/TokenFlux/TokenRouter/internal/protocol/openai"
	"github.com/TokenFlux/TokenRouter/internal/provider"
	providerhttp "github.com/TokenFlux/TokenRouter/internal/provider/httpapi"
	providerpostgres "github.com/TokenFlux/TokenRouter/internal/provider/postgres"
	provideradapter "github.com/TokenFlux/TokenRouter/internal/provider/provider"
	"github.com/TokenFlux/TokenRouter/internal/upstream/antigravity"
	openaiws "github.com/TokenFlux/TokenRouter/internal/upstream/openai/ws"
	"github.com/TokenFlux/TokenRouter/internal/upstream/qoder"
)

// provideAgentTaskCoordinator 让所有持久提供商任务入口共享同一进程内按提供商锁。
func provideAgentTaskCoordinator() *provider.OpenAITaskCoordinator {
	return &provider.OpenAITaskCoordinator{}
}

// provideProviderExpiry 为每分钟执行的过期检查注入提供商存储，并登记维护任务的启停。
func provideProviderExpiry(store *providerpostgres.ProviderStore) *provider.ExpiryService {
	return provider.NewExpiryService(store, provider.ExpiryOptions{Interval: time.Minute, Now: time.Now, Observe: log.Printf})
}

// provideProviderProbeTasks 为探测绑定任务协调器和凭据条件写入函数。
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

// provideScheduledTests 构造提供商定时测试用例，定时任务由生命周期管理器启动。
func provideScheduledTests(plans provider.ScheduledTestPlanRepository, results provider.ScheduledTestResultRepository) *provider.ScheduledTestService {
	return provider.NewScheduledTestService(plans, results, provider.ScheduledTestOptions{Now: time.Now, NextRun: provideradapter.NextScheduledTestRun})
}

func provideScheduledTestRunner(plans provider.ScheduledTestPlanRepository, scheduled *provider.ScheduledTestService, tests *provider.TestService, recovery *provider.RecoveryService, cfg *config.Config) *provider.ScheduledTestRunnerService {
	location := time.Local
	if parsed, err := time.LoadLocation(cfg.Timezone); err == nil && parsed != nil {
		location = parsed
	}
	return provider.NewScheduledTestRunnerService(plans, scheduled, tests, provider.ScheduledRunnerOptions{Schedule: provideradapter.NewScheduledCron(location), Now: time.Now, NextRun: provideradapter.NextScheduledTestRun, Offset: 10 * time.Second, Observe: func(format string, args ...any) {
		logging.LegacyPrintf("service.scheduled_test_runner", format, args...)
	}, Recover: recovery.RecoverProviderAfterSuccessfulTest})
}

// provideProviderTests 绑定提供商存储和平台测试目标，供后台任务与 HTTP 共用。
func provideProviderTests(store *providerpostgres.ProviderStore, geminiToken *provider.GeminiTokenSource, claudeToken *provider.ClaudeTokenSource, grokToken *provider.GrokTokenSource, ag *provideradapter.AntigravityProbe, transport httpclient.UpstreamTransport, cfg *config.Config, profiles *egressprovider.TLSProfiles, routers *egress.TLSFingerprintRouterService, settings *gateway.RuntimeSettings, tasks *provideradapter.ProbeTasks, manager *lifecycle.Manager) *provider.TestService {
	urlPolicy := egress.OperatorURLPolicy{Enabled: cfg.Security.URLAllowlist.Enabled, AllowInsecureHTTP: cfg.Security.URLAllowlist.AllowInsecureHTTP, AllowPrivateHosts: cfg.Security.URLAllowlist.AllowPrivateHosts, UpstreamHosts: slices.Clone(cfg.Security.URLAllowlist.UpstreamHosts)}
	probePolicy := &provideradapter.OpenAIProbePolicy{Available: true, ForceCLI: cfg.Gateway.ForceCodexCLI, Read: store.GetByID, AllowClaudeCode: settings.IsOpenAIAllowClaudeCodeCodexPluginEnabled, BrowserUserAgent: settings.GetOpenAICodexUserAgent, DefaultBrowserUserAgent: gateway.DefaultOpenAICodexUserAgent, Routers: routers, Profiles: profiles, ManualProfiles: profiles}
	openaiTest := &provideradapter.OpenAIProviderTest{Store: store, Transport: transport, ValidateURL: urlPolicy.Validate, Prepare: probePolicy.Prepare, ApplyRouting: probePolicy.ApplyTestRouting, ResolveTLS: probePolicy.ResolveTestTLS, EnsureTask: tasks.Ensure}
	geminiTest := &provideradapter.GeminiProviderTest{Tokens: geminiToken, Transport: transport, Profiles: profiles, ValidateURL: urlPolicy.Validate}
	anthropicTest := &provideradapter.AnthropicProviderTest{Tokens: claudeToken, Transport: transport, Profiles: profiles, Store: store, ValidateURL: urlPolicy.Validate}
	// 管理测试使用独立的会话缓存，并登记关闭操作。
	qoderSessions := provideradapter.NewQoderTokenProvider(qoder.SessionBuilder{})
	qoderSessions.SetHTTPUpstream(transport, profiles)
	manager.Register(lifecycle.Hook{Name: "ProviderTestQoderSessions", StopOrder: 26, Stop: qoderSessions.StopContext})
	targets := &provideradapter.TestTargets{
		Jev:  &provideradapter.JevProviderTest{Transport: transport, ValidateURL: urlPolicy.Validate},
		Read: store.GetByID, OpenAI: openaiTest, Gemini: geminiTest, Anthropic: anthropicTest,
		Qoder: &provideradapter.QoderProviderTest{Sessions: qoderSessions, Client: qoder.NewClient(qoder.APIBaseURL), Transport: transport, Profiles: profiles, RewriteModel: openaiprotocol.ReplaceModelInBody},
		Grok:  &provideradapter.GrokProviderTest{Tokens: grokToken, Transport: transport, Store: store, OperatorValidator: urlPolicy.Validate, DefaultBaseURL: gatewayprovider.GrokDefaultBaseURLReader(settings)},
		CN:    &provideradapter.CNProviderTest{Transport: transport, Profiles: profiles, Store: store, ValidateURL: urlPolicy.Validate, Responses: openaiTest},
		Antigravity: &provideradapter.AntigravityProviderTest{Probe: func(ctx context.Context, value *provider.Record, request provider.PreparedTestRequest) (*antigravity.TestConnectionResult, error) {
			return ag.Execute(ctx, value, request)
		}},
	}
	core := provider.NewTestService(targets, provider.TestOptions{Now: time.Now, Error: func(message string) { log.Printf("Provider test error: %s", message) }, WriteError: func(err error) { log.Printf("failed to write SSE event: %v", err) }})
	return core
}

// provideProviderTestHTTP 与后台任务共用测试用例，测试成功后调用健康恢复函数。
func provideProviderTestHTTP(core *provider.TestService, recovery *provider.RecoveryService) *providerhttp.TestHandler {
	return providerhttp.NewTestHandler(core, func(ctx context.Context, id int64) error {
		_, err := recovery.RecoverProviderAfterSuccessfulTest(ctx, id)
		return err
	})
}
