package server

import (
	"github.com/TokenFlux/TokenRouter/internal/config"
	"github.com/TokenFlux/TokenRouter/internal/handler"
	middleware2 "github.com/TokenFlux/TokenRouter/internal/server/middleware"
	"github.com/TokenFlux/TokenRouter/internal/server/routes"
	"github.com/TokenFlux/TokenRouter/internal/service"

	"github.com/gin-gonic/gin"
)

// RouterRuntime 仅包含由应用装配好的 HTTP 行为。
type RouterRuntime struct {
	ProtocolCatalog gin.HandlerFunc
	FrameOrigins    func() []string
	Frontend        gin.HandlerFunc
	AuthLimiter     *middleware2.RateLimiter
	PanelLimiter    *middleware2.PanelRateLimiter
}

// SetupRouter 配置路由器中间件和路由
func SetupRouter(
	r *gin.Engine,
	handlers *handler.Handlers,
	jwtAuth middleware2.JWTAuthMiddleware,
	adminAuth middleware2.AdminAuthMiddleware,
	apiKeyAuth middleware2.APIKeyAuthMiddleware,
	auditLog middleware2.AuditLogMiddleware,
	stepUpAuth middleware2.StepUpAuthMiddleware,
	apiKeyService *service.APIKeyService,
	subscriptionService *service.SubscriptionService,
	opsService *service.OpsService,
	settingService *service.SettingService,
	cfg *config.Config,
	runtime *RouterRuntime,
) *gin.Engine {
	middleware2.SetIngressRejectRecorder(opsService)
	// 应用中间件
	r.Use(middleware2.RequestLogger())
	// 将客户端 IP + UA 注入 request context，供 token 签发/会话绑定/审计日志统一读取。
	// 解析模式按请求快照：兼容开关开启时信任原始转发头，关闭时使用 server.trusted_proxies。
	r.Use(middleware2.SessionBindingContext(cfg))
	r.Use(middleware2.Logger())
	r.Use(middleware2.CORS(cfg.CORS))
	r.Use(middleware2.SecurityHeaders(cfg.Security.CSP, runtime.FrameOrigins))
	r.Use(middleware2.ServerTiming(cfg.Server.EnableServerTiming))

	// Serve embedded frontend with settings injection if available
	if runtime.Frontend != nil {
		r.Use(runtime.Frontend)
	}

	// 注册路由
	registerRoutes(r, handlers, jwtAuth, adminAuth, apiKeyAuth, auditLog, stepUpAuth, apiKeyService, subscriptionService, opsService, settingService, cfg, runtime)

	return r
}

// registerRoutes 注册所有 HTTP 路由
func registerRoutes(
	r *gin.Engine,
	h *handler.Handlers,
	jwtAuth middleware2.JWTAuthMiddleware,
	adminAuth middleware2.AdminAuthMiddleware,
	apiKeyAuth middleware2.APIKeyAuthMiddleware,
	auditLog middleware2.AuditLogMiddleware,
	stepUpAuth middleware2.StepUpAuthMiddleware,
	apiKeyService *service.APIKeyService,
	subscriptionService *service.SubscriptionService,
	opsService *service.OpsService,
	settingService *service.SettingService,
	cfg *config.Config,
	runtime *RouterRuntime,
) {
	// 通用路由（健康检查、状态等）
	routes.RegisterCommonRoutes(r)

	// API v1
	v1 := r.Group("/api/v1")

	// 面板 API 限流器：认证接口按用户 ID、公开接口按安全客户端 IP，
	// 防止高频刷管理面接口打爆数据库（阈值可在系统设置中调整）。
	panelRateLimiter := runtime.PanelLimiter

	// 注册各模块路由
	routes.RegisterAuthRoutes(v1, h, jwtAuth, auditLog, runtime.AuthLimiter, settingService, panelRateLimiter)
	routes.RegisterUserRoutes(v1, h, jwtAuth, auditLog, stepUpAuth, settingService, panelRateLimiter)
	routes.RegisterAdminRoutes(v1, h, adminAuth, auditLog, stepUpAuth, panelRateLimiter, runtime.ProtocolCatalog)
	routes.RegisterGatewayRoutes(r, h, apiKeyAuth, apiKeyService, subscriptionService, opsService, settingService, cfg)
	routes.RegisterPaymentRoutes(v1, h.Payment, h.PaymentWebhook, h.Admin.Payment, h.Plans, jwtAuth, adminAuth, auditLog, settingService, panelRateLimiter)
	handler.RegisterPageRoutes(v1, cfg.Pricing.DataDir, gin.HandlerFunc(jwtAuth), gin.HandlerFunc(adminAuth), settingService)
}
