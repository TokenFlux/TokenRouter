package app

import (
	"context"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/TokenFlux/TokenRouter/internal/apikey"
	keyhttp "github.com/TokenFlux/TokenRouter/internal/apikey/httpapi"
	"github.com/TokenFlux/TokenRouter/internal/billing"
	"github.com/TokenFlux/TokenRouter/internal/config"
	"github.com/TokenFlux/TokenRouter/internal/gateway"
	gatewayhttp "github.com/TokenFlux/TokenRouter/internal/gateway/httpapi"
	"github.com/TokenFlux/TokenRouter/internal/gateway/requeststate"
	"github.com/TokenFlux/TokenRouter/internal/ops"
	"github.com/TokenFlux/TokenRouter/internal/protocol"
	"github.com/TokenFlux/TokenRouter/internal/routing"
	"github.com/TokenFlux/TokenRouter/internal/routing/capability"
	"github.com/TokenFlux/TokenRouter/internal/server/middleware"
)

// gateway_middleware.go constructs gateway route middleware for authorization,
// request context, diagnostics, and group assignment.
// Gateway route registration shares authentication and group checks across
// endpoint families. The bound middleware installs model attributes and the
// final group into request context, while protocol-specific guards record
// group-assignment rejection before platform handlers begin execution.

// provideGatewayRouteMiddleware binds authentication, body limits, diagnostics,
// and request context installation. cfg is required; optional client bindings
// supply group fallback, and each route later chooses the protocol-specific
// guards and middleware from this assembled value.
func provideGatewayRouteMiddleware(
	apiKeyAuth keyhttp.APIKeyAuthMiddleware,
	apiKeyService *apikey.APIKeyService,
	subscriptionService *billing.SubscriptionService,
	opsService *ops.OpsService,
	cfg *config.Config,
	queue *ops.ErrorLogQueue,
	clients *messageHTTPBindings,
	attributes *routing.ModelAttributeService,
	settings *gateway.RuntimeSettings,
) gatewayhttp.RouteMiddleware {
	var clientFallback gatewayhttp.ClientGroupFallbackResolver
	if clients != nil {
		clientFallback = clients.bindings.ClientGroupFallback
	}
	var ttftMode func(context.Context) string
	if settings != nil {
		ttftMode = settings.GetTTFTMode
	}
	return gatewayhttp.RouteMiddleware{
		TTFTMode: ttftMode,
		// Gateway configuration stores whole seconds; the middleware owns the
		// downstream idle timer and the independent upstream inactivity cutoff.
		DownstreamKeepalive: gatewayhttp.DownstreamSSEKeepalive(time.Duration(cfg.Gateway.Streaming.KeepaliveIntervalSeconds)*time.Second, time.Duration(cfg.Gateway.Streaming.DataIdleTimeoutSeconds)*time.Second),
		ModelAttributes: func(c *gin.Context) {
			c.Request = c.Request.WithContext(routing.WithModelAttributes(c.Request.Context(), attributes))
			c.Next()
		},
		ClientGroupFallback:   clientFallback,
		APIKeyAuth:            gin.HandlerFunc(apiKeyAuth),
		GoogleAPIKeyAuth:      newGatewayAuthorization(apiKeyService, subscriptionService, cfg, true),
		BodyLimit:             middleware.RequestBodyLimit(cfg.Gateway.MaxRequestBodyBytes),
		TextBodyLimit:         middleware.RequestBodyLimit(cfg.Gateway.TextMaxRequestBodyBytes),
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

// provideGroupAssignmentGuard constructs group rejection using the supplied
// protocol error writer. Rejected calls are recorded as both an Ops client-
// business limit and an ingress group-assignment rejection before the error
// is written and the Gin chain is aborted.
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
