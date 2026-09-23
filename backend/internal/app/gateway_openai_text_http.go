package app

import (
	"context"
	"time"

	"github.com/TokenFlux/TokenRouter/internal/gateway/httpapi/openaiattempt"
	textflow "github.com/TokenFlux/TokenRouter/internal/gateway/text"

	"github.com/TokenFlux/TokenRouter/internal/apikey"
	"github.com/TokenFlux/TokenRouter/internal/config"
	"github.com/TokenFlux/TokenRouter/internal/gateway/admission"
	"github.com/TokenFlux/TokenRouter/internal/gateway/errorpolicy"
	gatewayhttp "github.com/TokenFlux/TokenRouter/internal/gateway/httpapi"
	"github.com/TokenFlux/TokenRouter/internal/gateway/promptpolicy"
	"github.com/TokenFlux/TokenRouter/internal/gateway/session"
	"github.com/TokenFlux/TokenRouter/internal/moderation"
	"github.com/TokenFlux/TokenRouter/internal/routing"
	"github.com/TokenFlux/TokenRouter/internal/service"
)

// provideOpenAITextHTTP 直接组合原生 HTTP 与固定单次运行时，不经过旧 Handler。
func provideOpenAITextHTTP(
	source *service.OpenAIGatewayService,
	funding *admission.FundingAdmission,
	keys *apikey.APIKeyService,
	resources *gatewayhttp.OpenAIHTTPResources,
	cyber *gatewayhttp.CyberHandler,
	rules *errorpolicy.ErrorPassthroughService,
	moderator *moderation.ContentModerationService,
	prompts *promptpolicy.Service,
	cfg *config.Config,
	runtime *openaiattempt.Runtime,
	activity *gatewayRequestActivity,
) *gatewayhttp.OpenAITextHandler {
	options := gatewayhttp.OpenAITextOptions{MaxSwitches: 3}
	if cfg != nil {
		options.ForceCodexCLI = cfg.Gateway.ForceCodexCLI
		options.MaxBodyBytes = cfg.Gateway.MaxBodySize
		if cfg.Gateway.MaxAccountSwitches > 0 {
			options.MaxSwitches = cfg.Gateway.MaxAccountSwitches
		}
		if cfg.Gateway.StreamKeepaliveInterval > 0 {
			options.CompactKeepaliveInterval = time.Duration(cfg.Gateway.StreamKeepaliveInterval) * time.Second
		}
	}
	var moderationPort gatewayhttp.ModerationPort
	if moderator != nil {
		moderationPort = moderator
	}
	bindings := gatewayhttp.OpenAITextBindings{
		Dependencies: gatewayhttp.OpenAIDependencies{
			Handler:     true,
			Gateway:     source != nil,
			Funding:     funding != nil,
			Keys:        keys != nil,
			Concurrency: resources != nil && resources.Concurrency != nil && resources.Concurrency.Service() != nil,
		},
		Resources:     resources,
		ResponseOwner: func() session.HTTPResponseOwnerReader { return source.ResponseStateStore() },
		Moderation:    moderationPort,
		PlanRoute: func(ctx context.Context, key *apikey.APIKey, model string) routing.RoutePlan {
			return source.PlanRoute(ctx, key.Group, key.GroupID, model)
		},
		ReplaceModel:   source.ReplaceModelInBody,
		Errors:         rules,
		Funding:        funding,
		Cyber:          cyber,
		IsolateSession: source.EnsureSessionIsolation,
	}
	result := gatewayhttp.NewBoundOpenAITextHandler(options, bindings, prompts, textflow.NewResponsesExecutor(runtime, textflow.ResponseOptions{MaxSwitches: options.MaxSwitches}, textflow.ResponseOptions{MaxSwitches: options.MaxSwitches, FirstOutputBudget: true}))
	result.BindRequestActivity(activity.Enter)
	return result
}
