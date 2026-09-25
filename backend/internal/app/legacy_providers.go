package app

import (
	keyhttp "github.com/TokenFlux/TokenRouter/internal/apikey/httpapi"
	"github.com/TokenFlux/TokenRouter/internal/gateway/requeststate"
	"github.com/TokenFlux/TokenRouter/internal/routing"
	"github.com/TokenFlux/TokenRouter/internal/routing/capability"

	"github.com/TokenFlux/TokenRouter/internal/ops"

	"context"

	"net/http"

	"time"

	"github.com/TokenFlux/TokenRouter/internal/apikey"
	"github.com/TokenFlux/TokenRouter/internal/billing"

	identitysettings "github.com/TokenFlux/TokenRouter/internal/identity"

	"github.com/TokenFlux/TokenRouter/internal/app/bootstrap"

	"github.com/TokenFlux/TokenRouter/internal/app/lifecycle"

	"github.com/TokenFlux/TokenRouter/internal/config"

	gatewayhttp "github.com/TokenFlux/TokenRouter/internal/gateway/httpapi"

	"github.com/TokenFlux/TokenRouter/internal/protocol"

	"github.com/TokenFlux/TokenRouter/internal/server/middleware"

	"github.com/TokenFlux/TokenRouter/internal/site"

	"github.com/gin-gonic/gin"
)

// 以下构造函数将基础设施参数和模块接口绑定到应用依赖图。
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

// installBackgroundTasks 登记唯一任务拥有者，由各消费者显式接收，不安装全局绑定。
func installBackgroundTasks(manager *lifecycle.Manager) *lifecycle.Tasks {
	tasks := lifecycle.NewTasks()
	manager.Register(lifecycle.Hook{Name: "ApplicationBackgroundTasks", StartOrder: 932, StopOrder: 68, Stop: tasks.Stop})
	return tasks
}

func provideGatewayRouteMiddleware(apiKeyAuth keyhttp.APIKeyAuthMiddleware, apiKeyService *apikey.APIKeyService, subscriptionService *billing.SubscriptionService, opsService *ops.OpsService, routingSettings *routing.RuntimeSettings, cfg *config.Config, queue *ops.ErrorLogQueue) gatewayhttp.RouteMiddleware {
	return gatewayhttp.RouteMiddleware{
		APIKeyAuth: gin.HandlerFunc(apiKeyAuth), GoogleAPIKeyAuth: newGatewayAuthorization(apiKeyService, subscriptionService, cfg, true),
		BodyLimit: middleware.RequestBodyLimit(cfg.Gateway.MaxBodySize), TextBodyLimit: middleware.RequestBodyLimit(cfg.Gateway.TextMaxBodySize), ClientRequestID: middleware.ClientRequestID(), OpsErrorLogger: gatewayhttp.OpsErrorLoggerMiddleware(opsService, queue, provideOpsObservationAccess()), EndpointNormalization: gatewayhttp.InboundEndpointMiddleware(),
		RequireGroupAnthropic: provideGroupAssignmentGuard(routingSettings, gatewayhttp.AnthropicErrorWriter), RequireGroupGoogle: provideGroupAssignmentGuard(routingSettings, gatewayhttp.GoogleErrorWriter), ForceAntigravity: keyhttp.ForcePlatform(capability.PlatformAntigravity), ForcedPlatform: keyhttp.GetForcePlatformFromContext,
		Access: func(c *gin.Context) gatewayhttp.RouteAccess {
			key, ok := keyhttp.GetAPIKeyFromContext(c)
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
			if key, ok := keyhttp.GetAPIKeyFromContext(c); ok && key != nil && key.Group != nil {
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
