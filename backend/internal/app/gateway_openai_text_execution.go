package app

import (
	"context"
	"time"

	"github.com/TokenFlux/TokenRouter/internal/app/lifecycle"
	"github.com/TokenFlux/TokenRouter/internal/billing"
	"github.com/TokenFlux/TokenRouter/internal/config"
	"github.com/TokenFlux/TokenRouter/internal/egress"
	egressprovider "github.com/TokenFlux/TokenRouter/internal/egress/provider"
	"github.com/TokenFlux/TokenRouter/internal/gateway"
	"github.com/TokenFlux/TokenRouter/internal/gateway/admission"
	gatewayhttp "github.com/TokenFlux/TokenRouter/internal/gateway/httpapi"
	"github.com/TokenFlux/TokenRouter/internal/gateway/promptpolicy"
	gatewayadapter "github.com/TokenFlux/TokenRouter/internal/gateway/provider"
	"github.com/TokenFlux/TokenRouter/internal/gateway/provider/selection"
	"github.com/TokenFlux/TokenRouter/internal/gateway/session"
	"github.com/TokenFlux/TokenRouter/internal/gateway/ws"
	wshttp "github.com/TokenFlux/TokenRouter/internal/gateway/ws/httpapi"
	"github.com/TokenFlux/TokenRouter/internal/infra/httpclient"
	"github.com/TokenFlux/TokenRouter/internal/provider"
	provideradapter "github.com/TokenFlux/TokenRouter/internal/provider/provider"
	"github.com/TokenFlux/TokenRouter/internal/scheduler"
	openaiws "github.com/TokenFlux/TokenRouter/internal/upstream/openai/ws"
)

// gateway_openai_text_execution.go constructs shared OpenAI text, Responses,
// image, auxiliary, and WebSocket execution dependencies.
// OpenAI text, Responses, images, auxiliary calls, and WebSocket execution
// share request construction and response output. The text runtime holds prompt
// and continuation state with callback-based lifetimes, and Codex usage writes
// receive application task tracking after the executor is assembled.

// provideCompactExecutor constructs context recovery with optional startup
// model and logging choices. Nil cfg leaves the Compact executor fields unset,
// allowing its execution path to apply the platform defaults.
func provideCompactExecutor(cfg *config.Config) *gatewayhttp.CompactExecutor {
	value := &gatewayhttp.CompactExecutor{}
	if cfg != nil {
		value.Models = gatewayadapter.CompactModels{Default: cfg.Gateway.OpenAI.CompactModel}
		value.LogBody = cfg.Gateway.UpstreamErrorBodyLog.Enabled
		value.LogBodyMaxBytes = cfg.Gateway.UpstreamErrorBodyLog.MaxBytes
	}
	return value
}

// provideExecutionAgentIdentity combines task coordination, stored identity,
// registration, and connection invalidation. The registration callback targets
// the fixed OpenAI accounts endpoint, and connections supplies invalidation
// when the identity runtime replaces provider state.
func provideExecutionAgentIdentity(tasks *provider.OpenAITaskCoordinator, store gatewayadapter.ExecutionProviderStore, connections *openaiws.OpenAIWSConnections) *gatewayadapter.ExecutionAgentIdentity {
	return gatewayadapter.NewExecutionAgentIdentity(tasks, store, func(ctx context.Context, value *provider.Record) (string, error) {
		return provideradapter.RegisterAgentIdentityTask(ctx, value, "https://auth.openai.com/api/accounts")
	}, connections.InvalidateProvider)
}

// provideAnthropicPromptCache constructs shared prompt-digest bindings with the
// system clock.
func provideAnthropicPromptCache() *session.AnthropicPromptCache {
	return session.NewAnthropicPromptCache(time.Now)
}

// provideOpenAITextExecutor constructs text execution and attaches tracked
// Codex usage writes. choices supplies response sticky TTL and tasks supplies
// background ownership; the returned executor shares resources assembled by
// openAITextExecution.
func provideOpenAITextExecutor(cfg *config.Config, store gatewayadapter.ExecutionProviderStore, identity *gatewayadapter.ExecutionAgentIdentity, credentials *provider.OpenAIExecutionCredentials, transport httpclient.UpstreamTransport, profiles *egressprovider.TLSProfiles, routers *egress.TLSFingerprintRouterService, readers *gatewayadapter.RuntimeReaders, grok *gatewayhttp.GrokExecutor, output *gatewayhttp.OpenAIResponseOutput, cache *session.AnthropicPromptCache, choices *selection.Compatible, compact *gatewayhttp.CompactExecutor, tasks *lifecycle.Tasks) *gatewayhttp.OpenAITextExecutor {
	text := openAITextExecution(cfg, store, identity, credentials, transport, profiles, routers, readers, grok, output, cache, choices.OpenAIHTTPResponseStickyTTL, compact)
	text.CodexUsage.Go = tasks.Go
	return text
}

// provideOpenAIImageBridgePolicy creates response-image policy from optional
// startup configuration. Nil cfg leaves DefaultEnabled false, and request
// execution later combines this default with group and provider image policy.
func provideOpenAIImageBridgePolicy(cfg *config.Config) *gatewayadapter.ResponseImagePolicy {
	policy := &gatewayadapter.ResponseImagePolicy{}
	if cfg != nil {
		policy.DefaultEnabled = cfg.Gateway.OpenAI.Codex.ImageGenerationBridgeEnabled
	}
	return policy
}

// provideOpenAIEncryptedLineage binds shared session state and the selection
// TTL reader.
func provideOpenAIEncryptedLineage(state session.OpenAIWSStateStore, choices *selection.Compatible) *gatewayhttp.OpenAIEncryptedLineage {
	return &gatewayhttp.OpenAIEncryptedLineage{Store: state, TTL: choices.SessionStickyTTL}
}

// provideOpenAIWebSockets constructs WS execution using shared text requests
// and output. text and lineage are required; runtime, connections, prompts,
// selection, image policy, and cache are supplied to the WS executor for its
// own connection and turn handling.
func provideOpenAIWebSockets(runtime *ws.Runtime, cfg *config.Config, connections *openaiws.OpenAIWSConnections, text *gatewayhttp.OpenAITextExecutor, prompts *promptpolicy.Service, choices *selection.Compatible, lineage *gatewayhttp.OpenAIEncryptedLineage, imagePolicy *gatewayadapter.ResponseImagePolicy, cache session.GatewayCache) *wshttp.OpenAIWebSocketExecutor {
	return wshttp.NewOpenAIWebSocketExecutor(wshttp.OpenAIWSDependencies{Runtime: runtime, Options: openAIWSExecutionOptions(cfg), Connections: connections, Requests: text.Requests, Output: text.Output, Grok: text.Grok, FastPolicy: text.FastPolicy, Prompts: prompts, Selection: choices, State: lineage.Store, Lineage: lineage, ImageBridge: imagePolicy, Cache: cache})
}

// provideOpenAIResponses binds Responses execution to the text runtime's
// request and output instances. text is required, and the assembled lineage and
// image policy are retained for continuation and response-image decisions.
func provideOpenAIResponses(text *gatewayhttp.OpenAITextExecutor, choices *selection.Compatible, lineage *gatewayhttp.OpenAIEncryptedLineage, imagePolicy *gatewayadapter.ResponseImagePolicy) *gatewayhttp.OpenAIResponsesExecutor {
	return &gatewayhttp.OpenAIResponsesExecutor{Requests: text.Requests, Output: text.Output, Text: text, Grok: text.Grok, Lineage: lineage, ImageBridge: imagePolicy}
}

// provideOpenAIAuxiliary constructs auxiliary execution using the text
// runtime's requests, output, and Codex observer. text and activity are
// required; the authorization service is passed to auxiliary methods for
// platform queries.
func provideOpenAIAuxiliary(text *gatewayhttp.OpenAITextExecutor, authorization *provider.OpenAIAuthorization, activity *gatewayRequestActivity) *gatewayhttp.OpenAIAuxiliary {
	executor := &gatewayhttp.OpenAIAuxiliary{Requests: text.Requests, Output: text.Output, CodexUsage: text.CodexUsage, Authorization: authorization, Enter: activity.Enter}
	return executor
}

// provideOpenAIImages constructs image execution with shared request, output,
// and activity. The cooldown writer uses the same provider store, and runtime
// cooldown settings are bound when the text request readers are present.
func provideOpenAIImages(text *gatewayhttp.OpenAITextExecutor, activity *gatewayRequestActivity) *gatewayhttp.OpenAIImagesExecutor {
	cooldown := &provideradapter.ImageToolCooldown{Store: text.Requests.Providers}
	if text.Requests.Readers != nil {
		cooldown.Settings = text.Requests.Readers.Provider.GetOpenAIImagesOAuthUnavailableCooldownSettings
	}
	result := &gatewayhttp.OpenAIImagesExecutor{Requests: text.Requests, Output: text.Output, Cooldown: cooldown, Enter: activity.Enter}
	return result
}

// openAITextExecution assembles shared requests, policy, output, and
// continuation dependencies. The supplied prompt cache and TTL callback are
// retained. Optional Grok execution contributes credentials, fast policy,
// transport failure handling, routes, and its ordinary usage-write throttle;
// cfg binds startup request options and forced instructions.
func openAITextExecution(cfg *config.Config, store gatewayadapter.ExecutionProviderStore, identity *gatewayadapter.ExecutionAgentIdentity, credentials *provider.OpenAIExecutionCredentials, transport httpclient.UpstreamTransport, profiles *egressprovider.TLSProfiles, routers *egress.TLSFingerprintRouterService, readers *gatewayadapter.RuntimeReaders, grok *gatewayhttp.GrokExecutor, output *gatewayhttp.OpenAIResponseOutput, prompts *session.AnthropicPromptCache, ttl func() time.Duration, compact *gatewayhttp.CompactExecutor) *gatewayhttp.OpenAITextExecutor {
	policy := &provideradapter.OpenAIProbePolicy{Available: true, DefaultBrowserUserAgent: gateway.DefaultOpenAICodexUserAgent, Profiles: profiles}
	requests := &gatewayhttp.OpenAIRequests{Providers: store, Identity: identity, Credentials: credentials, Transport: transport, Profiles: profiles, Routers: routers, Readers: readers, ClientPolicy: policy}
	if routers != nil {
		policy.Routers = routers
	}
	if readers != nil {
		policy.AllowClaudeCode = readers.Gateway.IsOpenAIAllowClaudeCodeCodexPluginEnabled
		policy.BrowserUserAgent = readers.Gateway.GetOpenAICodexUserAgent
	}
	executor := &gatewayhttp.OpenAITextExecutor{Compact: compact, Requests: requests, Output: output, Grok: grok, Continuation: &session.CompatResponses{TTL: ttl}, PromptCache: prompts, CodexUsage: &provideradapter.CodexUsageObserver{Store: store}, ResponseTTL: ttl}
	if grok != nil {
		requests.Failure = grok.Failure
		requests.GrokRoutes = grok.Routes
		executor.Credentials = grok.Credentials
		executor.FastPolicy = grok.FastPolicy
		if grok.Health != nil {
			executor.CodexUsage.Throttle = grok.Health.Throttle
		}
	}
	if output != nil {
		requests.Turns = output.Turns
	}

	if cfg != nil {
		v := cfg.Security.URLAllowlist
		requests.Options = gatewayhttp.OpenAIRequestOptions{ForceCLI: cfg.Gateway.OpenAI.Codex.ForceCLI, AllowTimeoutHeaders: cfg.Gateway.OpenAI.PassthroughAllowTimeoutHeaders, URLPolicy: egress.OperatorURLPolicy{Enabled: v.Enabled, AllowInsecureHTTP: v.AllowInsecureHTTP, AllowPrivateHosts: v.AllowPrivateHosts, UpstreamHosts: v.UpstreamHosts}}
		policy.ForceCLI = cfg.Gateway.OpenAI.Codex.ForceCLI
		executor.ForcedTemplate = cfg.Gateway.OpenAI.Codex.ForcedInstructionsTemplate
	}
	return executor
}

// provideUnifiedTextExecutor combines platform executors with model-pricing
// admission. When cfg is present it captures message queue mode and wait
// policy; a queue helper is added only when both queue and cfg are supplied.
func provideUnifiedTextExecutor(openai *gatewayhttp.OpenAIResponsesExecutor, anthropic *gatewayhttp.MessagesExecutor, gemini *gatewayhttp.GeminiExecutor, antigravity *gatewayhttp.AntigravityExecutor, qoder *gatewayadapter.QoderRuntime, refresh *provideradapter.QoderRequestRefresh, queue *scheduler.UserMessageQueueService, cfg *config.Config, prices *billing.PriceResolver) *gatewayhttp.UnifiedTextExecutor {
	executor := &gatewayhttp.UnifiedTextExecutor{OpenAI: openai, Anthropic: anthropic, Gemini: gemini, Antigravity: antigravity, Qoder: qoder, QoderRefresh: refresh}
	executor.Pricing = &admission.ModelPricing{Resolver: prices}
	if cfg != nil {
		executor.MessageQueueMode = cfg.Gateway.Anthropic.UserMessageQueue.DefaultMode
		executor.MessageQueueWait = cfg.Gateway.Anthropic.UserMessageQueue.WaitTimeout()
		if queue != nil {
			// Queue heartbeat scheduling converts the configured ping interval from seconds.
			executor.MessageQueue = gatewayhttp.NewUserMsgQueueHelper(queue, gatewayhttp.SSEPingFormatClaude, time.Duration(cfg.Gateway.Streaming.WaitPingIntervalSeconds)*time.Second)
		}
	}
	return executor
}
