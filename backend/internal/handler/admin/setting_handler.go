package admin

import (
	accountSettingsHTTP "github.com/TokenFlux/TokenRouter/internal/account/httpapi"
	"github.com/TokenFlux/TokenRouter/internal/creative"
	creativehttp "github.com/TokenFlux/TokenRouter/internal/creative/httpapi"
	runtimesettings "github.com/TokenFlux/TokenRouter/internal/settings"

	"context"

	"github.com/TokenFlux/TokenRouter/internal/service"

	"github.com/gin-gonic/gin"
)

// SettingHandler 系统设置处理器
type SettingHandler struct {
	settingsParticipants     *runtimesettings.Registry
	settingService           *service.SettingService
	emailService             *service.EmailService
	turnstileService         *service.TurnstileService
	aliyunCaptchaService     *service.AliyunCaptchaService
	opsService               *service.OpsService
	paymentConfigService     *service.PaymentConfigService
	paymentService           *service.PaymentService
	userAttributeService     *service.UserAttributeService
	notificationEmailService *service.NotificationEmailService
	totpService              *service.TotpService
	userService              *service.UserService
	preAggregationSettings   *service.PreAggregationSettingsService
	dashboardAggregation     *service.DashboardAggregationService
	opsAggregation           *service.OpsAggregationService
	creativeModelReader      interface {
		ListCreativeModelCandidates(context.Context) ([]service.CreativeModelCandidate, error)
	}
}

// SetPreAggregationDeps 注入统一预聚合设置、用量任务和运维任务。
func (h *SettingHandler) SetPreAggregationDeps(settings *service.PreAggregationSettingsService, dashboard *service.DashboardAggregationService, ops *service.OpsAggregationService) {
	if h == nil {
		return
	}
	h.preAggregationSettings = settings
	h.dashboardAggregation = dashboard
	h.opsAggregation = ops
}

// SetCreativeModelReader 注入创作台模型候选读取服务，并保持既有构造函数签名不变。
func (h *SettingHandler) SetCreativeModelReader(reader interface {
	ListCreativeModelCandidates(context.Context) ([]service.CreativeModelCandidate, error)
}) {
	if h == nil {
		return
	}
	h.creativeModelReader = reader
}

// NewSettingHandler 创建系统设置处理器
func NewSettingHandler(settingService *service.SettingService, emailService *service.EmailService, turnstileService *service.TurnstileService, opsService *service.OpsService, paymentConfigService *service.PaymentConfigService, paymentService *service.PaymentService, userAttributeService *service.UserAttributeService) *SettingHandler {
	return &SettingHandler{
		settingService:       settingService,
		emailService:         emailService,
		turnstileService:     turnstileService,
		opsService:           opsService,
		paymentConfigService: paymentConfigService,
		paymentService:       paymentService,
		userAttributeService: userAttributeService,
	}
}

// ListCreativeModelCandidates 返回管理员配置创作台白名单时可选择的模型候选。
// 该接口只挂在管理员设置路由下，不受用户创作台开关影响。
func (h *SettingHandler) ListCreativeModelCandidates(c *gin.Context) {
	h.creativeSettingsHTTP().ListCreativeModelCandidates(c)
}

// GetCreativeWorkerStatus 返回创作台任务 worker 池状态快照，供管理端展示当前使用情况。
// GET /api/v1/admin/settings/creative-worker-status
func (h *SettingHandler) GetCreativeWorkerStatus(c *gin.Context) {
	h.creativeSettingsHTTP().GetCreativeWorkerStatus(c)
}

// SetNotificationEmailService 注入通知邮件模板服务，并保持既有构造函数签名不变。
func (h *SettingHandler) SetNotificationEmailService(notificationEmailService *service.NotificationEmailService) {
	h.notificationEmailService = notificationEmailService
}

// SetAliyunCaptchaService 注入阿里云验证码凭据校验服务，并保持现有单元测试使用的构造函数签名不变。
func (h *SettingHandler) SetAliyunCaptchaService(aliyunCaptchaService *service.AliyunCaptchaService) {
	h.aliyunCaptchaService = aliyunCaptchaService
}

// SetStepUpDeps 注入 step-up 开关转换所需的服务，同时保持现有单元测试使用的构造函数签名不变。
// 开启开关要求操作者已启用 TOTP，关闭开关本身也必须通过 step-up 门禁。
func (h *SettingHandler) SetStepUpDeps(totpService *service.TotpService, userService *service.UserService) {
	h.totpService = totpService
	h.userService = userService
}

// GetSettings 获取所有系统设置
// GET /api/v1/admin/settings
func (h *SettingHandler) GetSettings(c *gin.Context) { h.compositeHTTP().GetSettings(c) }

// GetOpenAI403CooldownSettings 获取 OpenAI OAuth 403 冷却配置
// GET /api/v1/admin/settings/openai-403-cooldown
func (h *SettingHandler) GetOpenAI403CooldownSettings(c *gin.Context) {
	accountSettingsHTTP.NewRuntimeSettingsHandler(h.settingService.AccountSettings()).GetOpenAI403CooldownSettings(c)
}

// GetOpenAIOAuthImportDefaults 获取 OpenAI OAuth 账号导入缺省模板
// GET /api/v1/admin/settings/openai-oauth-import-defaults
func (h *SettingHandler) GetOpenAIOAuthImportDefaults(c *gin.Context) {
	accountSettingsHTTP.NewRuntimeSettingsHandler(h.settingService.AccountSettings()).GetOpenAIOAuthImportDefaults(c)
}

// UpdateOpenAI403CooldownSettings 更新 OpenAI OAuth 403 冷却配置
// PUT /api/v1/admin/settings/openai-403-cooldown
func (h *SettingHandler) UpdateOpenAI403CooldownSettings(c *gin.Context) {
	accountSettingsHTTP.NewRuntimeSettingsHandler(h.settingService.AccountSettings()).UpdateOpenAI403CooldownSettings(c)
}

// UpdateOpenAIOAuthImportDefaults 更新 OpenAI OAuth 账号导入缺省模板
// PUT /api/v1/admin/settings/openai-oauth-import-defaults
func (h *SettingHandler) UpdateOpenAIOAuthImportDefaults(c *gin.Context) {
	accountSettingsHTTP.NewRuntimeSettingsHandler(h.settingService.AccountSettings()).UpdateOpenAIOAuthImportDefaults(c)
}

// UpdateOpenAI403CooldownSettingsRequest 更新 OpenAI OAuth 403 冷却配置请求
type UpdateOpenAI403CooldownSettingsRequest = accountSettingsHTTP.UpdateOpenAI403CooldownSettingsRequest

// creativeSettingsHTTP 只为尚未清零的旧构造投影状态回调。
func (h *SettingHandler) creativeSettingsHTTP() *creativehttp.SettingsHandler {
	var status func() creative.CreativeWorkerStatus
	if h != nil && h.settingService != nil {
		status = h.settingService.CreativeWorkerStatus
	}
	if h == nil {
		return creativehttp.NewSettingsHandler(nil, status)
	}
	return creativehttp.NewSettingsHandler(h.creativeModelReader, status)
}
