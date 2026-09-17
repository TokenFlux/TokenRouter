package app

import (
	context "context"
	http "net/http"
	time "time"

	"github.com/TokenFlux/TokenRouter/internal/apikey"
	"github.com/TokenFlux/TokenRouter/internal/billing"

	"github.com/TokenFlux/TokenRouter/internal/scheduler"

	gatewaysettings "github.com/TokenFlux/TokenRouter/internal/gateway"

	identitysettings "github.com/TokenFlux/TokenRouter/internal/identity"

	"github.com/TokenFlux/TokenRouter/internal/audit"
	"github.com/TokenFlux/TokenRouter/internal/usage"

	accountsettings "github.com/TokenFlux/TokenRouter/internal/account"

	"github.com/TokenFlux/TokenRouter/internal/promotion"
	"github.com/TokenFlux/TokenRouter/internal/server/runtimeconfig"

	bootstrap "github.com/TokenFlux/TokenRouter/internal/app/bootstrap"
	lifecycle "github.com/TokenFlux/TokenRouter/internal/app/lifecycle"
	config "github.com/TokenFlux/TokenRouter/internal/config"
	gatewayhttp "github.com/TokenFlux/TokenRouter/internal/gateway/httpapi"
	handler "github.com/TokenFlux/TokenRouter/internal/handler"
	ctxkey "github.com/TokenFlux/TokenRouter/internal/pkg/ctxkey"
	protocol "github.com/TokenFlux/TokenRouter/internal/protocol"
	repository "github.com/TokenFlux/TokenRouter/internal/repository"
	middleware "github.com/TokenFlux/TokenRouter/internal/server/middleware"
	service "github.com/TokenFlux/TokenRouter/internal/service"
	settings "github.com/TokenFlux/TokenRouter/internal/settings"
	site "github.com/TokenFlux/TokenRouter/internal/site"
	gin "github.com/gin-gonic/gin"
)

// 以下仅投影旧图需要的绑定；随对应模块迁移删除旧 import。
func providePrivacyClientFactory() service.PrivacyClientFactory {
	return repository.CreatePrivacyReqClient
}

func provideSecretEncryptor(cfg *config.Config) (service.SecretEncryptor, error) {
	return bootstrap.NewAESEncryptor(cfg)
}
func provideSettingsStore(repo service.SettingRepository) *settings.Store { return settings.New(repo) }

func provideAnnouncementExpiry(repo site.AnnouncementRepository) *site.AnnouncementExpiryService {
	return site.NewAnnouncementExpiryService(repo, time.Minute)
}
func provideApplication(server *http.Server, manager *lifecycle.Manager, _ *runtimeReady, opsService *service.OpsService, _ *errorQueueReady) *Application {
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

func provideGatewayRouteMiddleware(apiKeyAuth middleware.APIKeyAuthMiddleware, apiKeyService *apikey.APIKeyService, subscriptionService *billing.SubscriptionService, opsService *service.OpsService, settingService *service.SettingService, cfg *config.Config) gatewayhttp.RouteMiddleware {
	return gatewayhttp.RouteMiddleware{
		APIKeyAuth: gin.HandlerFunc(apiKeyAuth), GoogleAPIKeyAuth: newGatewayAuthorization(apiKeyService, subscriptionService, cfg, true),
		BodyLimit: middleware.RequestBodyLimit(cfg.Gateway.MaxBodySize), TextBodyLimit: middleware.RequestBodyLimit(cfg.Gateway.TextMaxBodySize), ClientRequestID: middleware.ClientRequestID(), OpsErrorLogger: handler.OpsErrorLoggerMiddleware(opsService), EndpointNormalization: handler.InboundEndpointMiddleware(),
		RequireGroupAnthropic: provideGroupAssignmentGuard(settingService.RoutingSettings(), middleware.AnthropicErrorWriter), RequireGroupGoogle: provideGroupAssignmentGuard(settingService.RoutingSettings(), middleware.GoogleErrorWriter), ForceAntigravity: middleware.ForcePlatform(service.PlatformAntigravity), ForcedPlatform: middleware.GetForcePlatformFromContext,
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
			ctx := service.WithClientProtocol(c.Request.Context(), p)
			if key, ok := middleware.GetAPIKeyFromContext(c); ok && key != nil && key.Group != nil {
				ctx = context.WithValue(ctx, ctxkey.Group, key.Group)
			}
			c.Request = c.Request.WithContext(ctx)
		}, ObserveBusinessLimit: service.MarkOpsClientBusinessLimited,
	}
}

// providePanelSettings 过渡装配返回旧入口持有的唯一新实例，S16 删除旧入口后直接构造。
func providePanelSettings(s *service.SettingService) *runtimeconfig.PanelSettings {
	return s.PanelSettings()
}

// providePromotionSettings 过渡期间提供唯一推广设置读取器。
func providePromotionSettings(s *service.SettingService) *promotion.RuntimeSettings {
	return s.PromotionSettings()
}

// provideAccountSettings 让管理端点和旧消费者共享账号模块的唯一配置实例。
func provideAccountSettings(s *service.SettingService) *accountsettings.RuntimeSettings {
	return s.AccountSettings()
}

// provideUsageSettings 与 provideAuditSettings 只投影同实例的领域读取器。
func provideUsageSettings(s *service.SettingService) *usage.RuntimeSettings { return s.UsageSettings() }
func provideAuditSettings(s *service.SettingService) *audit.RetentionSettings {
	return s.AuditSettings()
}

// provideIdentitySettings 让身份 HTTP 和旧消费者共享同一动态设置读取器。
func provideIdentitySettings(s *service.SettingService) *identitysettings.RuntimeSettings {
	return s.IdentitySettings()
}

// provideGatewaySettings 只投影唯一网关规则实例，不创建第二份状态。

// provideLegacySettings 先绑定原生配置用例，再让旧聚合的剩余消费者取得它。
func provideLegacySettings(repo service.SettingRepository, paymentConfig *service.PaymentConfigService, proxies service.ProxyRepository, cfg *config.Config, oauth *identitysettings.OAuthSettings, grants *identitysettings.GrantSettings, forwarded *runtimeconfig.ForwardedSettings, schedulerDefaults *scheduler.AdminDefaults, gatewayRules *gatewaysettings.AdminSettingsRules, gatewayRuntime *gatewaysettings.RuntimeSettings, quotaSettings *accountsettings.QuotaSettingsCache) *service.SettingService {
	legacy := service.ProvideSettingService(repo, paymentConfig, proxies, cfg)
	legacy.SetOAuthSettings(oauth)
	legacy.SetGrantSettings(grants)
	legacy.SetForwardedSettings(forwarded)
	legacy.SetSchedulerAdminDefaults(schedulerDefaults)
	legacy.SetGatewayAdminRules(gatewayRules)
	legacy.SetGatewaySettings(gatewayRuntime)
	legacy.SetQuotaSettings(quotaSettings)
	return legacy
}

// provideGroupAssignmentGuard 只绑定原生 Key 投影和旧 Ops 观察端口，规则由 gateway/httpapi 拥有。
func provideGroupAssignmentGuard(settings gatewayhttp.UngroupedKeySettings, writer func(*gin.Context, int, string)) gin.HandlerFunc {
	return gatewayhttp.RequireGroupAssignment(settings, gatewayhttp.GroupAssignmentOptions{Access: gatewayhttp.EffectiveGroupAssignment, WriteError: writer, Rejected: func(c *gin.Context) {
		service.MarkOpsClientBusinessLimited(c, service.OpsClientBusinessLimitedReasonAPIKeyGroupUnassigned)
		middleware.MarkIngressRejected(c, middleware.IngressRejectGroupUnassigned)
	}})
}
