package handler

import (
	"context"

	"github.com/TokenFlux/TokenRouter/internal/apikey"
	gatewayhttp "github.com/TokenFlux/TokenRouter/internal/gateway/httpapi"
	"github.com/TokenFlux/TokenRouter/internal/gateway/session"
	"github.com/TokenFlux/TokenRouter/internal/routing"
)

func (h *OpenAIGatewayHandler) NewOpenAITextHTTPHandler() *gatewayhttp.OpenAITextHandler {
	options := gatewayhttp.OpenAITextOptions{}
	var prompt gatewayhttp.MessagesPrompt
	bindings := gatewayhttp.OpenAITextBindings{}
	if h != nil {
		options.ForceCodexCLI = h.cfg != nil && h.cfg.Gateway.ForceCodexCLI
		options.MaxBodyBytes = gatewayMaxBodySize(h.cfg)
		options.MaxSwitches = h.maxAccountSwitches
		options.CompactKeepaliveInterval = h.openAICompactKeepaliveInterval()
		prompt = h.prompts
		bindings.Dependencies = h.httpDependencies()
		bindings.Resources = h.httpResources()
		bindings.ResponseOwner = func() session.HTTPResponseOwnerReader { return h.gatewayService.ResponseStateStore() }
		bindings.Moderation = nativeModerationPort(h.contentModerationService)
		bindings.PlanRoute = func(ctx context.Context, key *apikey.APIKey, model string) routing.RoutePlan {
			return h.gatewayService.PlanRoute(ctx, key.Group, key.GroupID, model)
		}
		bindings.ReplaceModel = h.gatewayService.ReplaceModelInBody
		bindings.Errors = h.errorPassthroughService
		bindings.Funding = h.billingCacheService
		bindings.Cyber = h.NewCyberHTTPHandler()
		bindings.IsolateSession = h.ensureOpenAISessionIsolation
	}
	return gatewayhttp.NewBoundOpenAITextHandler(options, bindings, prompt, h.NewOpenAITextExecutor())
}
