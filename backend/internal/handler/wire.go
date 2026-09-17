package handler

import (
	opshttp "github.com/TokenFlux/TokenRouter/internal/ops/httpapi"

	billinghttpapi "github.com/TokenFlux/TokenRouter/internal/billing/httpapi"
	"github.com/TokenFlux/TokenRouter/internal/config"

	"github.com/TokenFlux/TokenRouter/internal/handler/admin"

	"github.com/TokenFlux/TokenRouter/internal/service"

	sitehttpapi "github.com/TokenFlux/TokenRouter/internal/site/httpapi"
	"github.com/google/wire"
)

// ProvideOpenAIGatewayHandler 创建并注入 Grok 媒体资格探测器。
func ProvideOpenAIGatewayHandler(
	gatewayService *service.OpenAIGatewayService,
	concurrencyService *service.ConcurrencyService,
	billingCacheService *service.BillingCacheService,
	apiKeyService *service.APIKeyService,
	usageRecordWorkerPool *service.UsageRecordWorkerPool,
	errorPassthroughService *service.ErrorPassthroughService,
	contentModerationService *service.ContentModerationService,
	opsService *service.OpsService,
	grokQuotaService *service.GrokQuotaService,
	cfg *config.Config,
) *OpenAIGatewayHandler {
	h := NewOpenAIGatewayHandler(gatewayService, concurrencyService, billingCacheService, apiKeyService,
		usageRecordWorkerPool, errorPassthroughService, contentModerationService, opsService, cfg)
	h.grokMediaEligibilityProber = grokQuotaService
	return h
}

// ProvideSystemHandler creates admin.SystemHandler with UpdateService
func ProvideSystemHandler(updateService *service.UpdateService, lockService *service.SystemOperationLockService, restarter admin.RestartRequester) *admin.SystemHandler {
	return admin.NewSystemHandler(updateService, lockService, restarter)
}

// ProvideSettingHandler creates SettingHandler with version from BuildInfo
func ProvideSettingHandler(settingService *service.SettingService, buildInfo BuildInfo, notificationEmailService *service.NotificationEmailService) *SettingHandler {
	h := NewSettingHandler(settingService, buildInfo.Version)
	h.SetNotificationEmailService(notificationEmailService)
	return h
}

// ProvideAdminSettingHandler 创建带通知邮件模板 API 的后台设置处理器。
func ProvideAdminSettingHandler(settingService *service.SettingService, emailService *service.EmailService, turnstileService *service.TurnstileService, aliyunCaptchaService *service.AliyunCaptchaService, opsService *service.OpsService, paymentConfigService *service.PaymentConfigService, paymentService *service.PaymentService, userAttributeService *service.UserAttributeService, notificationEmailService *service.NotificationEmailService, totpService *service.TotpService, userService *service.UserService, preAggregationSettings *service.PreAggregationSettingsService, dashboardAggregation *service.DashboardAggregationService, opsAggregation *service.OpsAggregationService, creativeModelReader *service.CreativePublicService) *admin.SettingHandler {
	h := admin.NewSettingHandler(settingService, emailService, turnstileService, opsService, paymentConfigService, paymentService, userAttributeService)
	h.SetNotificationEmailService(notificationEmailService)
	h.SetAliyunCaptchaService(aliyunCaptchaService)
	h.SetStepUpDeps(totpService, userService)
	h.SetPreAggregationDeps(preAggregationSettings, dashboardAggregation, opsAggregation)
	h.SetCreativeModelReader(creativeModelReader)
	return h
}

// ProvideTLSFingerprintProfileHandler 注入页面可控的 TLS 指纹收集器。
func ProvideTLSFingerprintProfileHandler(profileService *service.TLSFingerprintProfileService, collector *service.TLSFingerprintCollectorService) *admin.TLSFingerprintProfileHandler {
	return admin.NewTLSFingerprintProfileHandler(profileService, collector)
}

// ProvideAPIKeyHandler creates APIKeyHandler and injects optional group capacity display data.
func ProvideAPIKeyHandler(apiKeyService *service.APIKeyService, groupCapacityService *service.GroupCapacityService) *APIKeyHandler {
	handler := NewAPIKeyHandler(apiKeyService)
	handler.SetGroupCapacityService(groupCapacityService)
	return handler
}

// ProviderSet is the Wire provider set for all handlers
var ProviderSet = wire.NewSet(
	billinghttpapi.NewPlanHandler,
	// Top-level handlers
	NewUserHandler,
	ProvideAPIKeyHandler,
	billinghttpapi.NewRedeemHandler,
	billinghttpapi.NewSubscriptionHandler,
	sitehttpapi.NewAnnouncementHandler,
	NewGatewayHandler,
	ProvideOpenAIGatewayHandler,
	NewQoderGatewayHandler,
	NewTotpHandler,
	NewPasskeyHandler,
	ProvideSettingHandler,
	NewTeamHandler,

	// Admin handlers
	admin.NewUserHandler,
	sitehttpapi.NewAdminAnnouncementHandler,
	admin.NewOAuthHandler,
	admin.NewGeminiOAuthHandler,
	admin.NewAntigravityOAuthHandler,
	admin.NewQoderOAuthHandler,
	billinghttpapi.NewAdminRedeemHandler,
	ProvideAdminSettingHandler,
	opshttp.NewOpsHandler,
	billinghttpapi.NewAdminSubscriptionHandler,
	admin.NewUserAttributeHandler,
	admin.NewAdminAPIKeyHandler,
	admin.NewCodexInviteResetHandler,
	admin.NewTeamHandler,
)
