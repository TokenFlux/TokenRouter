package app

import (
	"github.com/TokenFlux/TokenRouter/internal/gateway/requeststate"
	"github.com/TokenFlux/TokenRouter/internal/routing"
	"github.com/TokenFlux/TokenRouter/internal/routing/capability"

	"github.com/TokenFlux/TokenRouter/internal/ops"

	context "context"

	http "net/http"

	time "time"

	"github.com/TokenFlux/TokenRouter/internal/apikey"
	"github.com/TokenFlux/TokenRouter/internal/billing"

	identitysettings "github.com/TokenFlux/TokenRouter/internal/identity"

	bootstrap "github.com/TokenFlux/TokenRouter/internal/app/bootstrap"

	lifecycle "github.com/TokenFlux/TokenRouter/internal/app/lifecycle"

	config "github.com/TokenFlux/TokenRouter/internal/config"

	gatewayhttp "github.com/TokenFlux/TokenRouter/internal/gateway/httpapi"

	protocol "github.com/TokenFlux/TokenRouter/internal/protocol"

	middleware "github.com/TokenFlux/TokenRouter/internal/server/middleware"

	service "github.com/TokenFlux/TokenRouter/internal/service"

	site "github.com/TokenFlux/TokenRouter/internal/site"

	gin "github.com/gin-gonic/gin"
)

// 以下仅投影旧图需要的绑定；随对应模块迁移删除旧 import。
func provideSecretEncryptor(cfg *config.Config) (identitysettings.SecretEncryptor, error) {
	return bootstrap.NewAESEncryptor(cfg)
}

func provideAnnouncementExpiry(repo site.AnnouncementRepository) *site.AnnouncementExpiryService {
	return site.NewAnnouncementExpiryService(repo, time.Minute)
}
func provideApplication(server *http.Server, manager *lifecycle.Manager, _ *runtimeReady, opsService *ops.OpsService, _ *ops.ErrorLogQueue) *Application {
	lifecycle.TrackRequests(server, manager)
	manager.Register(lifecycle.Hook{Name: "OpsWSRuntime", StartOrder: 983, StopOrder: 17, Stop: func(context.Context) error { opsService.Realtime().Stop(); return nil }})
	return &Application{Server: server, lifecycle: manager}
}

// installLegacyBackground 仅绑定旧调用方的技术完成端口，业务规则不进入 app。
func installLegacyBackground(manager *lifecycle.Manager) *lifecycle.Tasks {
	tasks := lifecycle.NewTasks()
	restore := service.SetBackgroundTaskRunner(tasks)
	manager.Register(lifecycle.Hook{Name: "LegacyBackgroundTasks", StartOrder: 932, StopOrder: 68, Stop: tasks.Stop})
	manager.Register(lifecycle.Hook{Name: "LegacyBackgroundBinding", StartOrder: -998, StopOrder: 850, Stop: func(context.Context) error { restore(); return nil }})
	return tasks
}

func provideGatewayRouteMiddleware(apiKeyAuth middleware.APIKeyAuthMiddleware, apiKeyService *apikey.APIKeyService, subscriptionService *billing.SubscriptionService, opsService *ops.OpsService, routingSettings *routing.RuntimeSettings, cfg *config.Config, queue *ops.ErrorLogQueue) gatewayhttp.RouteMiddleware {
	return gatewayhttp.RouteMiddleware{
		APIKeyAuth: gin.HandlerFunc(apiKeyAuth), GoogleAPIKeyAuth: newGatewayAuthorization(apiKeyService, subscriptionService, cfg, true),
		BodyLimit: middleware.RequestBodyLimit(cfg.Gateway.MaxBodySize), TextBodyLimit: middleware.RequestBodyLimit(cfg.Gateway.TextMaxBodySize), ClientRequestID: middleware.ClientRequestID(), OpsErrorLogger: gatewayhttp.OpsErrorLoggerMiddleware(opsService, queue, provideOpsObservationAccess()), EndpointNormalization: gatewayhttp.InboundEndpointMiddleware(),
		RequireGroupAnthropic: provideGroupAssignmentGuard(routingSettings, middleware.AnthropicErrorWriter), RequireGroupGoogle: provideGroupAssignmentGuard(routingSettings, middleware.GoogleErrorWriter), ForceAntigravity: middleware.ForcePlatform(capability.PlatformAntigravity), ForcedPlatform: middleware.GetForcePlatformFromContext,
		Access: func(c *gin.Context) gatewayhttp.RouteAccess {
			key, ok := middleware.GetAPIKeyFromContext(c)
			if !ok || key == nil {
				return gatewayhttp.RouteAccess{}
			}
			access := gatewayhttp.RouteAccess{Composite: key.IsComposite}
			if key.Group != nil {
				access.HasGroup = true
				access.Platform = key.Group.Platform
				access.AllowedProtocols = key.Group.AllowedProtocols
			}
			return access
		}, InstallClientProtocol: func(c *gin.Context, p protocol.ProtocolID) {
			ctx := requeststate.WithClientProtocol(c.Request.Context(), p)
			if key, ok := middleware.GetAPIKeyFromContext(c); ok && key != nil && key.Group != nil {
				ctx = requeststate.WithGroup(ctx, key.Group)
			}
			c.Request = c.Request.WithContext(ctx)
		}, ObserveBusinessLimit: gatewayhttp.MarkOpsClientBusinessLimited,
	}
}

// provideGroupAssignmentGuard 只绑定原生 Key 投影和旧 Ops 观察端口，规则由 gateway/httpapi 拥有。
func provideGroupAssignmentGuard(settings gatewayhttp.UngroupedKeySettings, writer func(*gin.Context, int, string)) gin.HandlerFunc {
	return gatewayhttp.RequireGroupAssignment(settings, gatewayhttp.GroupAssignmentOptions{Access: gatewayhttp.EffectiveGroupAssignment, WriteError: writer, Rejected: func(c *gin.Context) {
		gatewayhttp.MarkOpsClientBusinessLimited(c, gatewayhttp.OpsClientBusinessLimitedReasonAPIKeyGroupUnassigned)
		middleware.MarkIngressRejected(c, middleware.IngressRejectGroupUnassigned)
	}})
}
