package app

import (
	"context"

	"github.com/TokenFlux/TokenRouter/internal/apikey"
	"github.com/TokenFlux/TokenRouter/internal/config"
	"github.com/TokenFlux/TokenRouter/internal/gateway/admission"
	gatewayhttp "github.com/TokenFlux/TokenRouter/internal/gateway/httpapi"
	"github.com/TokenFlux/TokenRouter/internal/gateway/httpapi/openaiattempt"
	"github.com/TokenFlux/TokenRouter/internal/gateway/httpapi/wsentry"
	"github.com/TokenFlux/TokenRouter/internal/gateway/promptpolicy"
	"github.com/TokenFlux/TokenRouter/internal/gateway/session"
	"github.com/TokenFlux/TokenRouter/internal/routing"
	"github.com/TokenFlux/TokenRouter/internal/service"
)

// provideResponsesWSHTTP 直接绑定 WS 用例，共享同一尝试支持、生命周期与动态数据读取。
func provideResponsesWSHTTP(
	source *service.OpenAIGatewayService,
	funding *admission.FundingAdmission,
	keys *apikey.APIKeyService,
	common openaiattempt.Bindings,
	prompt *promptpolicy.Service,
	blocks *session.CyberBlocks,
	cfg *config.Config,
	activity *gatewayRequestActivity,
) *gatewayhttp.ResponsesWSHandler {
	options := gatewayhttp.ResponsesWSOptions{
		MaxAccountSwitches:  3,
		ReadLimit:           service.ResolveOpenAIWSClientReadLimitBytes(cfg),
		FirstMessageTimeout: service.ResolveOpenAIWSClientFirstMessageTimeout(cfg),
	}
	if cfg != nil {
		options.MaxIngressConnectionsPerAPIKey = cfg.Gateway.OpenAIWS.MaxIngressConnectionsPerAPIKey
		if cfg.Gateway.MaxAccountSwitches > 0 {
			options.MaxAccountSwitches = cfg.Gateway.MaxAccountSwitches
		}
	}
	b := wsentry.Bindings{
		Common: common,
		Prompt: prompt,
		Blocks: blocks,
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
			return source.PlanRoute(ctx, key.Group, key.GroupID, model)
		}
		b.Isolate = source.EnsureSessionIsolation
		b.ReportSelection = source.ReportOpenAIAccountScheduleResultForSelection
		b.Stop429 = source.ShouldStopOpenAIOAuth429Failover
		b.Credential = source.GetRequestCredential
		b.ResolveRouting = source.ResolveOpenAIWSRoutingModelForAccount
		b.BeginPreemption = source.BeginOpenAIWSIngressSessionPreemption
		b.Relay = source.ProxyResponsesWebSocketFromClient
	}
	result := wsentry.New(options, b)
	result.BindRequestActivity(activity.Enter)
	return result
}
