package app

import (
	"context"
	"time"

	"github.com/TokenFlux/TokenRouter/internal/apikey"
	"github.com/TokenFlux/TokenRouter/internal/billing"
	"github.com/TokenFlux/TokenRouter/internal/config"
	"github.com/TokenFlux/TokenRouter/internal/gateway/admission"
	gatewayhttp "github.com/TokenFlux/TokenRouter/internal/gateway/httpapi"
	"github.com/TokenFlux/TokenRouter/internal/gateway/httpapi/openaiattempt"
	"github.com/TokenFlux/TokenRouter/internal/gateway/promptpolicy"
	gatewayprovider "github.com/TokenFlux/TokenRouter/internal/gateway/provider"
	"github.com/TokenFlux/TokenRouter/internal/gateway/provider/selection"
	"github.com/TokenFlux/TokenRouter/internal/gateway/session"
	wshttp "github.com/TokenFlux/TokenRouter/internal/gateway/ws/httpapi"
	"github.com/TokenFlux/TokenRouter/internal/routing"
)

// gateway_openai_ws_http.go constructs Responses WebSocket ingress with shared
// attempt support and runtime session settings.
// Responses WS handlers reuse OpenAI attempt support while resolving online
// parameters from the WS runtime. Sticky binding reads the current session TTL
// when that runtime is available, and each handler shares pricing, credential
// execution, and application request activity.

// provideResponsesWSHTTP constructs ingress with shared attempts, pricing, and
// activity. An available source supplies the online runtime in options;
// activity and common.Support are required, while responsesWSBindings describes
// optional execution and funding capabilities.
func provideResponsesWSHTTP(
	source *wshttp.OpenAIWebSocketExecutor, credentials *gatewayhttp.RequestCredentialExecutor,
	funding *admission.FundingAdmission,
	keys *apikey.APIKeyService,
	common openaiattempt.Bindings,
	prompt *promptpolicy.Service,
	blocks *session.CyberBlocks,
	cfg *config.Config,
	activity *gatewayRequestActivity,
	choices *selection.Compatible, planner *gatewayprovider.RoutePlanner,
	subscriptions *billing.SubscriptionService,
	prices *billing.PriceResolver,
) *wshttp.ResponsesWSHandler {
	options := responsesWSOptions(cfg)
	if source != nil {
		options.Runtime = source.Runtime
	}
	b := responsesWSBindings(source, credentials, funding, keys, common, prompt, blocks, choices, planner, subscriptions)
	b.Pricing = &admission.ModelPricing{Resolver: prices}
	result := wshttp.New(options, b)
	result.BindRequestActivity(activity.Enter)
	return result
}

// responsesWSOptions supplies default reads, first-message budget, and three
// provider switches. Positive configured switches replace the default, and the
// per-Key ingress limit is copied directly when configuration exists.
func responsesWSOptions(cfg *config.Config) wshttp.ResponsesWSOptions {
	options := wshttp.ResponsesWSOptions{
		MaxProviderSwitches: 3,
		ReadLimit:           wshttp.ResolveOpenAIWSClientReadLimitBytes(openAIWSExecutionOptions(cfg)),
		FirstMessageTimeout: wshttp.ResolveOpenAIWSClientFirstMessageTimeout(openAIWSExecutionOptions(cfg)),
	}
	if cfg != nil {
		options.MaxIngressConnectionsPerAPIKey = cfg.Gateway.OpenAI.ResponsesWS.MaxIngressConnectionsPerAPIKey
		options.ForceCodexCLI = cfg.Gateway.OpenAI.Codex.ForceCLI
		if cfg.Gateway.Failover.MaxProviderSwitches > 0 {
			options.MaxProviderSwitches = cfg.Gateway.Failover.MaxProviderSwitches
		}
	}
	return options
}

// responsesWSBindings combines online execution, common attempt support, and
// reauthorization reads. common.Support is required; when source exists,
// credential and selection callbacks are bound, and sticky TTL prefers
// source.Runtime's snapshot over the selection default.
func responsesWSBindings(source *wshttp.OpenAIWebSocketExecutor, credentials *gatewayhttp.RequestCredentialExecutor, funding *admission.FundingAdmission, keys *apikey.APIKeyService, common openaiattempt.Bindings, prompt *promptpolicy.Service, blocks *session.CyberBlocks, choices *selection.Compatible, planner *gatewayprovider.RoutePlanner, subscriptions admission.SubscriptionReader) wshttp.Bindings {
	b := wshttp.Bindings{
		Common:        common,
		Subscriptions: subscriptions,
		Prompt:        prompt,
		Blocks:        blocks,
		Dependencies: gatewayhttp.OpenAIDependencies{
			Handler:     true,
			Gateway:     source != nil,
			Funding:     funding != nil,
			Keys:        keys != nil,
			Concurrency: common.Support.Concurrency != nil && common.Support.Concurrency.Service() != nil,
		},
	}
	if keys != nil {
		b.Keys = keys
	}
	if funding != nil {
		b.CheckFunding = funding.CheckKey
	}
	if source != nil {
		b.PlanRoute = func(ctx context.Context, key *apikey.APIKey, model string) routing.RoutePlan {
			return planner.PlanKey(ctx, key, model)
		}
		b.Isolate = source.EnsureSessionIsolation
		b.BindSticky = func(ctx context.Context, groupID *int64, hash string, id int64) error {
			ttl := choices.SessionStickyTTL()
			// Runtime settings can change after handler assembly. Read the current
			// TTL for this binding so new affinity writes use the published session lifetime.
			if source.Runtime != nil {
				// Published session lifetimes are seconds, converted here for sticky expiry.
				ttl = time.Duration(source.Runtime.Snapshot().StickySessionTTLSeconds) * time.Second
			}
			return choices.BindStickySessionWithTTL(ctx, groupID, hash, id, ttl)
		}
		b.ReportSelection = common.Selection.ReportSelection
		b.Stop429 = stopOpenAI429
		b.Credential = credentials.Resolve
		b.ResolveRouting = choices.ResolveOpenAIWSRoutingModelForProvider
		b.BeginPreemption = source.BeginOpenAIWSIngressSessionPreemption
		b.Relay = source.ProxyResponsesWebSocketFromClient
	}
	return b
}
