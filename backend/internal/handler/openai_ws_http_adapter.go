package handler

import (
	"context"

	"github.com/TokenFlux/TokenRouter/internal/gateway/httpapi/wsentry"

	apikey "github.com/TokenFlux/TokenRouter/internal/apikey"

	gatewayhttp "github.com/TokenFlux/TokenRouter/internal/gateway/httpapi"

	"github.com/TokenFlux/TokenRouter/internal/routing"

	"github.com/TokenFlux/TokenRouter/internal/service"
)

func (h *OpenAIGatewayHandler) NewResponsesWSHTTPHandler() *gatewayhttp.ResponsesWSHandler {
	max := 0
	if h.cfg != nil {
		max = h.cfg.Gateway.OpenAIWS.MaxIngressConnectionsPerAPIKey
	}
	options := gatewayhttp.ResponsesWSOptions{
		MaxIngressConnectionsPerAPIKey: max,
		ReadLimit:                      service.ResolveOpenAIWSClientReadLimitBytes(h.cfg),
		FirstMessageTimeout:            service.ResolveOpenAIWSClientFirstMessageTimeout(h.cfg),
		MaxAccountSwitches:             h.maxAccountSwitches,
	}
	return wsentry.New(options, h.wsEntryBindings())
}

// wsEntryBindings 只为剩余旧入口投影同一组固定原生端口。
func (h *OpenAIGatewayHandler) wsEntryBindings() wsentry.Bindings {
	b := wsentry.Bindings{Common: openAIAttemptBindings(h), Dependencies: h.httpDependencies(), Prompt: h.prompts, CheckFunding: h.billingCacheService.CheckKey, Isolate: h.ensureOpenAISessionIsolation}
	if h.apiKeyService != nil {
		b.Keys = h.apiKeyService
	}
	if source := h.gatewayService; source != nil {
		b.Blocks = source.CyberBlocks()
		b.PlanRoute = func(ctx context.Context, key *apikey.APIKey, model string) routing.RoutePlan {
			return source.PlanRoute(ctx, key.Group, key.GroupID, model)
		}
		b.ReportSelection = source.ReportOpenAIAccountScheduleResultForSelection
		b.Stop429 = source.ShouldStopOpenAIOAuth429Failover
		b.Credential = source.GetRequestCredential
		b.ResolveRouting = source.ResolveOpenAIWSRoutingModelForAccount
		b.BeginPreemption = source.BeginOpenAIWSIngressSessionPreemption
		b.Relay = source.ProxyResponsesWebSocketFromClient
	}
	return b
}
