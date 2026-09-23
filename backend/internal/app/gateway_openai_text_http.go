package app

import (
	"context"
	"time"

	"github.com/TokenFlux/TokenRouter/internal/apikey"
	"github.com/TokenFlux/TokenRouter/internal/config"
	"github.com/TokenFlux/TokenRouter/internal/gateway/admission"
	"github.com/TokenFlux/TokenRouter/internal/gateway/errorpolicy"
	gatewayhttp "github.com/TokenFlux/TokenRouter/internal/gateway/httpapi"
	"github.com/TokenFlux/TokenRouter/internal/gateway/promptpolicy"
	"github.com/TokenFlux/TokenRouter/internal/gateway/session"
	"github.com/TokenFlux/TokenRouter/internal/handler"
	"github.com/TokenFlux/TokenRouter/internal/moderation"
	"github.com/TokenFlux/TokenRouter/internal/routing"
	"github.com/TokenFlux/TokenRouter/internal/service"
)

// provideOpenAITextHTTP 直接构造原生 HTTP；剩余单次执行器通过单独端口过渡。
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
	legacy *handler.OpenAIGatewayHandler,
	activity *gatewayRequestActivity,
	records GatewayCompletionRecorders,
) *gatewayhttp.OpenAITextHandler {
	legacy.BindCompletionRecorder(records.OpenAI)
	legacy.BindCyberHTTPHandler(cyber)
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
	result := gatewayhttp.NewBoundOpenAITextHandler(options, bindings, prompts, legacy.NewOpenAITextExecutor())
	result.BindRequestActivity(activity.Enter)
	return result
}
