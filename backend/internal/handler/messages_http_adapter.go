package handler

import (
	"context"

	"github.com/TokenFlux/TokenRouter/internal/apikey"
	gatewayhttp "github.com/TokenFlux/TokenRouter/internal/gateway/httpapi"
	"github.com/TokenFlux/TokenRouter/internal/routing"
	"github.com/TokenFlux/TokenRouter/internal/service"
)

// NewMessagesHTTPHandler 仅保留尚未迁完测试的构造委托，生产不经过此入口。
func (h *GatewayHandler) NewMessagesHTTPHandler() *gatewayhttp.MessagesHandler {
	bindings := h.messagesBindings()
	return gatewayhttp.NewBoundMessagesHandler(gatewayhttp.MessagesHTTPOptions{MaxBodyBytes: gatewayMaxBodySize(h.cfg), MaxSwitches: h.maxAccountSwitches, MaxGeminiSwitches: h.maxAccountSwitchesGemini}, bindings, h.prompts, h.concurrencyHelper, h.NewMessagesExecutor())
}

// messagesBindings 仅供尚未迁完的测试构造器投影依赖。
func (h *GatewayHandler) messagesBindings() gatewayhttp.MessagesBindings {
	return gatewayhttp.MessagesBindings{
		PlanRoute: func(ctx context.Context, key *apikey.APIKey, model string) routing.RoutePlan {
			var id *int64
			if key != nil {
				id = key.GroupID
			}
			return h.gatewayService.PlanRoute(ctx, service.APIKeyRouteGroup(key), id, model)
		},
		ClientVersions: h.runtimeSettings.GetClaudeCodeVersionBounds, Funding: h.billingCacheService,
		Moderation: nativeModerationPort(h.contentModerationService), Errors: h.errorPassthroughService,
		IsolateSession: h.ensureGatewaySessionIsolation, ObserveCompatibility: h.maybeLogCompatibilityFallbackMetrics,
		CachedSession: h.gatewayService.GetCachedSessionAccountID,
	}
}
