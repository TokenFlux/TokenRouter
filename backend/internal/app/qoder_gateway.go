package app

import (
	"time"

	"github.com/TokenFlux/TokenRouter/internal/app/lifecycle"

	"github.com/TokenFlux/TokenRouter/internal/app/legacybridge"
	"github.com/TokenFlux/TokenRouter/internal/gateway"
	gatewayhttp "github.com/TokenFlux/TokenRouter/internal/gateway/httpapi"
	"github.com/TokenFlux/TokenRouter/internal/handler"
	"github.com/TokenFlux/TokenRouter/internal/service"
)

// provideQoderChat 装配唯一 Chat 编排，旧 handler 只提供待迁请求/完成适配。
func provideQoderChat(g *service.GatewayService, q *service.QoderGatewayService, c *service.ConcurrencyService, b *service.BillingCacheService, k *service.APIKeyService, h *handler.QoderGatewayHandler, r *service.ErrorPassthroughService, manager *lifecycle.Manager) *gatewayhttp.QoderChatHandler {
	activity := lifecycle.NewOperations("QoderRequestsAndAttempts")
	manager.Register(lifecycle.Hook{Name: "QoderRequestsAndAttempts", StopOrder: 15, Stop: activity.StopContext})
	q.BindAttemptActivity(activity.Enter)
	h.BindRequestActivity(activity.Enter)
	bridge := legacybridge.QoderChat{Gateway: g, Qoder: q, Concurrency: c, Billing: b, Keys: k, HTTP: h, ErrorRules: r}
	result := &gatewayhttp.QoderChatHandler{UseCase: gateway.NewQoderUseCase(3, 30*time.Second), Prepare: bridge.Prepare, Failure: h.QoderClientFailure, Preflight: bridge.Preflight}
	result.UseCase.Enter = activity.Enter
	h.BindChatHandler(result)
	return result
}
