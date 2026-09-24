package app

import (
	"time"

	account "github.com/TokenFlux/TokenRouter/internal/account"
	accountprovider "github.com/TokenFlux/TokenRouter/internal/account/provider"
	"github.com/TokenFlux/TokenRouter/internal/config"
	"github.com/TokenFlux/TokenRouter/internal/egress"
	egressprovider "github.com/TokenFlux/TokenRouter/internal/egress/provider"
	"github.com/TokenFlux/TokenRouter/internal/gateway"
	gatewayhttp "github.com/TokenFlux/TokenRouter/internal/gateway/httpapi"
	"github.com/TokenFlux/TokenRouter/internal/gateway/provider"
	"github.com/TokenFlux/TokenRouter/internal/gateway/session"
	"github.com/TokenFlux/TokenRouter/internal/infra/httpclient"
)

// openAITextExecution 固定注入已构造的资源，动态策略仍按请求读取。
func openAITextExecution(cfg *config.Config, store provider.ExecutionAccountStore, identity *provider.ExecutionAgentIdentity, credentials *account.OpenAIExecutionCredentials, transport httpclient.UpstreamTransport, profiles *egressprovider.TLSProfiles, routers *egress.TLSFingerprintRouterService, readers *provider.RuntimeReaders, grok *gatewayhttp.GrokExecutor, output *gatewayhttp.OpenAIResponseOutput, prompts *session.AnthropicPromptCache, ttl func() time.Duration, compact *gatewayhttp.CompactExecutor) *gatewayhttp.OpenAITextExecutor {
	policy := &accountprovider.OpenAIProbePolicy{Available: true, DefaultBrowserUserAgent: gateway.DefaultOpenAICodexUserAgent, Profiles: profiles}
	requests := &gatewayhttp.OpenAIRequests{Accounts: store, Identity: identity, Credentials: credentials, Transport: transport, Profiles: profiles, Routers: routers, Readers: readers, ClientPolicy: policy}
	if routers != nil {
		policy.Routers = routers
	}
	if readers != nil {
		policy.AllowClaudeCode = readers.Gateway.IsOpenAIAllowClaudeCodeCodexPluginEnabled
		policy.BrowserUserAgent = readers.Gateway.GetOpenAICodexUserAgent
	}
	executor := &gatewayhttp.OpenAITextExecutor{Compact: compact, Requests: requests, Output: output, Grok: grok, Continuation: &session.CompatResponses{TTL: ttl}, PromptCache: prompts, CodexUsage: &accountprovider.CodexUsageObserver{Store: store}, ResponseTTL: ttl}
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
		requests.Options = gatewayhttp.OpenAIRequestOptions{ForceCLI: cfg.Gateway.ForceCodexCLI, AllowTimeoutHeaders: cfg.Gateway.OpenAIPassthroughAllowTimeoutHeaders, URLPolicy: egress.OperatorURLPolicy{Enabled: v.Enabled, AllowInsecureHTTP: v.AllowInsecureHTTP, AllowPrivateHosts: v.AllowPrivateHosts, UpstreamHosts: v.UpstreamHosts}}
		policy.ForceCLI = cfg.Gateway.ForceCodexCLI
		executor.ForcedTemplate = cfg.Gateway.ForcedCodexInstructionsTemplate
	}
	return executor
}
