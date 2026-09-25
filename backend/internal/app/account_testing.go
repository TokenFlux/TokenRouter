// 本文件维护 app 的所属能力；兼容入口复用唯一实现。
package app

import (
	"context"
	"log"
	"slices"
	"time"

	gatewayprovider "github.com/TokenFlux/TokenRouter/internal/gateway/provider"
	"github.com/TokenFlux/TokenRouter/internal/infra/httpclient"

	"github.com/TokenFlux/TokenRouter/internal/account"
	accounthttp "github.com/TokenFlux/TokenRouter/internal/account/httpapi"
	accountpostgres "github.com/TokenFlux/TokenRouter/internal/account/postgres"
	"github.com/TokenFlux/TokenRouter/internal/account/provider"
	"github.com/TokenFlux/TokenRouter/internal/app/lifecycle"
	"github.com/TokenFlux/TokenRouter/internal/config"
	"github.com/TokenFlux/TokenRouter/internal/egress"
	egressprovider "github.com/TokenFlux/TokenRouter/internal/egress/provider"
	"github.com/TokenFlux/TokenRouter/internal/gateway"
	"github.com/TokenFlux/TokenRouter/internal/gateway/media"
	"github.com/TokenFlux/TokenRouter/internal/gateway/provider/modelidentity"
	openaiprotocol "github.com/TokenFlux/TokenRouter/internal/protocol/openai"
	"github.com/TokenFlux/TokenRouter/internal/routing/capability"
	"github.com/TokenFlux/TokenRouter/internal/upstream/antigravity"
	"github.com/TokenFlux/TokenRouter/internal/upstream/openai"
	"github.com/TokenFlux/TokenRouter/internal/upstream/qoder"
)

// provideAccountTests 直接绑定原生账号和平台目标，后台与 HTTP 共用同一入口。
func provideAccountTests(store *accountpostgres.AccountStore, geminiToken *account.GeminiTokenSource, claudeToken *account.ClaudeTokenSource, grokToken *account.GrokTokenSource, ag *provider.AntigravityProbe, transport httpclient.UpstreamTransport, cfg *config.Config, profiles *egressprovider.TLSProfiles, routers *egress.TLSFingerprintRouterService, settings *gateway.RuntimeSettings, tasks *provider.ProbeTasks, manager *lifecycle.Manager) *account.TestService {
	urlPolicy := egress.OperatorURLPolicy{Enabled: cfg.Security.URLAllowlist.Enabled, AllowInsecureHTTP: cfg.Security.URLAllowlist.AllowInsecureHTTP, AllowPrivateHosts: cfg.Security.URLAllowlist.AllowPrivateHosts, UpstreamHosts: slices.Clone(cfg.Security.URLAllowlist.UpstreamHosts)}
	probePolicy := &provider.OpenAIProbePolicy{Available: true, ForceCLI: cfg.Gateway.ForceCodexCLI, Read: store.GetByID, AllowClaudeCode: settings.IsOpenAIAllowClaudeCodeCodexPluginEnabled, BrowserUserAgent: settings.GetOpenAICodexUserAgent, DefaultBrowserUserAgent: gateway.DefaultOpenAICodexUserAgent, Routers: routers, Profiles: profiles, ManualProfiles: profiles}
	openaiTest := &provider.OpenAIAccountTest{Store: store, Transport: transport, ValidateURL: urlPolicy.Validate, Prepare: probePolicy.Prepare, ApplyRouting: probePolicy.ApplyTestRouting, ResolveTLS: probePolicy.ResolveTestTLS, EnsureTask: tasks.Ensure, ModelRules: openai.CodexModelRules{ImageOnly: media.IsImageGenerationModel, LastSegment: capability.LastOpenAIModelSegment, CanonicalAlias: capability.CanonicalizeOpenAIModelAliasSpelling, KnownModel: modelidentity.NormalizeOpenAI, SupportsEffort: capability.OpenAIModelSupportsReasoningEffort}}
	geminiTest := &provider.GeminiAccountTest{Tokens: geminiToken, Transport: transport, Profiles: profiles, ValidateURL: urlPolicy.Validate}
	anthropicTest := &provider.AnthropicAccountTest{Tokens: claudeToken, Transport: transport, Profiles: profiles, Store: store, ValidateURL: urlPolicy.Validate}
	// 保留管理测试独立的会话作用域，并明确登记其停止拥有者。
	qoderSessions := provider.NewQoderTokenProvider(qoder.SessionBuilder{})
	qoderSessions.SetHTTPUpstream(transport, profiles)
	manager.Register(lifecycle.Hook{Name: "AccountTestQoderSessions", StopOrder: 26, Stop: qoderSessions.StopContext})
	targets := &provider.TestTargets{Read: store.GetByID, OpenAI: openaiTest, Gemini: geminiTest, Anthropic: anthropicTest,
		Qoder: &provider.QoderAccountTest{Sessions: qoderSessions, Client: qoder.NewClient(qoder.APIBaseURL), Transport: transport, Profiles: profiles, RewriteModel: openaiprotocol.ReplaceModelInBody},
		Grok:  &provider.GrokAccountTest{Tokens: grokToken, Transport: transport, Store: store, OperatorValidator: urlPolicy.Validate, DefaultBaseURL: gatewayprovider.GrokDefaultBaseURLReader(settings)},
		CN:    &provider.CNAccountTest{Transport: transport, Profiles: profiles, Store: store, ValidateURL: urlPolicy.Validate, Responses: openaiTest},
		Antigravity: &provider.AntigravityAccountTest{Gemini: geminiTest, Anthropic: anthropicTest, Probe: func(ctx context.Context, value *account.Record, request account.PreparedTestRequest) (*antigravity.TestConnectionResult, error) {
			return ag.Execute(ctx, value, request)
		}},
	}
	core := account.NewTestService(targets, account.TestOptions{Now: time.Now, Error: func(message string) { log.Printf("Account test error: %s", message) }, WriteError: func(err error) { log.Printf("failed to write SSE event: %v", err) }})
	return core
}

// provideAccountTestHTTP 与后台复用唯一测试用例，成功恢复仍调用原健康端口。
func provideAccountTestHTTP(core *account.TestService, recovery *account.RecoveryService) *accounthttp.TestHandler {
	return accounthttp.NewTestHandler(core, func(ctx context.Context, id int64) error {
		_, err := recovery.RecoverAccountAfterSuccessfulTest(ctx, id)
		return err
	})
}
