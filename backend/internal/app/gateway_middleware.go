package app

import (
	"github.com/gin-gonic/gin"

	"github.com/TokenFlux/TokenRouter/internal/apikey"
	keyhttp "github.com/TokenFlux/TokenRouter/internal/apikey/httpapi"
	"github.com/TokenFlux/TokenRouter/internal/billing"
	"github.com/TokenFlux/TokenRouter/internal/config"
	gatewayhttp "github.com/TokenFlux/TokenRouter/internal/gateway/httpapi"
	"github.com/TokenFlux/TokenRouter/internal/gateway/requeststate"
	"github.com/TokenFlux/TokenRouter/internal/ops"
	"github.com/TokenFlux/TokenRouter/internal/protocol"
	"github.com/TokenFlux/TokenRouter/internal/routing/capability"
	"github.com/TokenFlux/TokenRouter/internal/server/middleware"
)

// provideGatewayRouteMiddleware 为网关路由绑定鉴权、分组检查和请求记录中间件。
func provideGatewayRouteMiddleware(
	apiKeyAuth keyhttp.APIKeyAuthMiddleware,
	apiKeyService *apikey.APIKeyService,
	subscriptionService *billing.SubscriptionService,
	opsService *ops.OpsService,
	cfg *config.Config,
	queue *ops.ErrorLogQueue,
	clients *messageHTTPBindings,
) gatewayhttp.RouteMiddleware {
	var clientFallback gatewayhttp.ClientGroupFallbackResolver
	if clients != nil {
		clientFallback = clients.bindings.ClientGroupFallback
	}
	return gatewayhttp.RouteMiddleware{
		ClientGroupFallback:   clientFallback,
		APIKeyAuth:            gin.HandlerFunc(apiKeyAuth),
		GoogleAPIKeyAuth:      newGatewayAuthorization(apiKeyService, subscriptionService, cfg, true),
		BodyLimit:             middleware.RequestBodyLimit(cfg.Gateway.MaxBodySize),
		TextBodyLimit:         middleware.RequestBodyLimit(cfg.Gateway.TextMaxBodySize),
		ClientRequestID:       middleware.ClientRequestID(),
		OpsErrorLogger:        gatewayhttp.OpsErrorLoggerMiddleware(opsService, queue, provideOpsObservationAccess()),
		EndpointNormalization: gatewayhttp.InboundEndpointMiddleware(),
		RequireGroupAnthropic: provideGroupAssignmentGuard(gatewayhttp.AnthropicErrorWriter),
		RequireGroupGoogle:    provideGroupAssignmentGuard(gatewayhttp.GoogleErrorWriter),
		ForceAntigravity:      keyhttp.ForcePlatform(capability.PlatformAntigravity),
		ForcedPlatform:        keyhttp.GetForcePlatformFromContext,
		Access: func(c *gin.Context) gatewayhttp.RouteAccess {
			key, ok := keyhttp.GetAPIKeyFromContext(c)
			if !ok || key == nil {
				return gatewayhttp.RouteAccess{}
			}
			access := gatewayhttp.RouteAccess{Composite: key.IsComposite}
			if key.Group != nil {
				access.HasGroup = true
				access.AllowedProtocols = key.Group.AllowedProtocols
			}
			return access
		},
		InstallClientProtocol: func(c *gin.Context, p protocol.ProtocolID) {
			ctx := requeststate.WithClientProtocol(c.Request.Context(), p)
			if key, ok := keyhttp.GetAPIKeyFromContext(c); ok && key != nil && key.Group != nil {
				ctx = requeststate.WithGroup(ctx, key.Group)
			}
			c.Request = c.Request.WithContext(ctx)
		},
		ObserveBusinessLimit: gatewayhttp.MarkOpsClientBusinessLimited,
	}
}

// provideGroupAssignmentGuard 为分组检查绑定 Key 读取和拒绝请求的记录函数。
func provideGroupAssignmentGuard(writer func(*gin.Context, int, string)) gin.HandlerFunc {
	return gatewayhttp.RequireGroupAssignment(gatewayhttp.GroupAssignmentOptions{
		Access:     gatewayhttp.EffectiveGroupAssignment,
		WriteError: writer,
		Rejected: func(c *gin.Context) {
			gatewayhttp.MarkOpsClientBusinessLimited(c, gatewayhttp.OpsClientBusinessLimitedReasonAPIKeyGroupUnassigned)
			middleware.MarkIngressRejected(c, middleware.IngressRejectGroupUnassigned)
		},
	})
}
