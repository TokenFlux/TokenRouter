package handler

import (
	gatewayhttp "github.com/TokenFlux/TokenRouter/internal/gateway/httpapi"
	notificationhttp "github.com/TokenFlux/TokenRouter/internal/notification/httpapi"
	searchhttp "github.com/TokenFlux/TokenRouter/internal/search/httpapi"
	usagehttp "github.com/TokenFlux/TokenRouter/internal/usage/httpapi"

	opshttp "github.com/TokenFlux/TokenRouter/internal/ops/httpapi"

	accounthttp "github.com/TokenFlux/TokenRouter/internal/account/httpapi"

	billinghttpapi "github.com/TokenFlux/TokenRouter/internal/billing/httpapi"
	"github.com/TokenFlux/TokenRouter/internal/config"

	egresshttp "github.com/TokenFlux/TokenRouter/internal/egress/httpapi"
	"github.com/TokenFlux/TokenRouter/internal/handler/admin"

	schedulerhttp "github.com/TokenFlux/TokenRouter/internal/scheduler/httpapi"
	"github.com/TokenFlux/TokenRouter/internal/service"

	sitehttpapi "github.com/TokenFlux/TokenRouter/internal/site/httpapi"
	"github.com/google/wire"
)

// ProvideAdminHandlers creates the AdminHandlers struct
func ProvideAdminHandlers(
	schedulerDiagnostics *schedulerhttp.DiagnosticsHandler,
	accountManagement *accounthttp.ManagementHandler,
	accountOAuthUsage *accounthttp.OAuthUsageHandler,
	accountOllama *accounthttp.OllamaUsageHandler,
	accountCodexImport *accounthttp.CodexImportHandler,
	accountCRS *accounthttp.CRSHandler,
	accountArchive *accounthttp.ArchiveHandler,
	accountTests *accounthttp.TestHandler,
	upstreamUsage *accounthttp.UpstreamUsageHandler,
	dashboardHandler *admin.DashboardHandler,
	userHandler *admin.UserHandler,
	groupHandler *admin.GroupHandler,
	announcementHandler *sitehttpapi.AdminAnnouncementHandler,
	dataManagementHandler *admin.DataManagementHandler,
	backupHandler *admin.BackupHandler,
	oauthHandler *admin.OAuthHandler,
	openaiOAuthHandler *admin.OpenAIOAuthHandler,
	geminiOAuthHandler *admin.GeminiOAuthHandler,
	antigravityOAuthHandler *admin.AntigravityOAuthHandler,
	grokOAuthHandler *accounthttp.GrokOAuthHandler,
	qoderOAuthHandler *admin.QoderOAuthHandler,
	proxyHandler *egresshttp.ProxyHandler,
	redeemHandler *billinghttpapi.AdminRedeemHandler,
	promoHandler *admin.PromoHandler,
	settingHandler *admin.SettingHandler,
	opsHandler *admin.OpsHandler,
	systemHandler *admin.SystemHandler,
	subscriptionHandler *billinghttpapi.AdminSubscriptionHandler,
	usageHandler *admin.UsageHandler,
	userAttributeHandler *admin.UserAttributeHandler,
	errorPassthroughHandler *admin.ErrorPassthroughHandler,
	tlsFingerprintProfileHandler *admin.TLSFingerprintProfileHandler,
	tlsFingerprintRouterHandler *admin.TLSFingerprintRouterHandler,
	apiKeyHandler *admin.AdminAPIKeyHandler,
	scheduledTestHandler *accounthttp.ScheduledTestHandler,
	channelHandler *admin.ChannelHandler,
	contentModerationHandler *admin.ContentModerationHandler,
	paymentHandler *admin.PaymentHandler,
	affiliateHandler *admin.AffiliateHandler,
	codexInviteResetHandler *admin.CodexInviteResetHandler,
	auditLogHandler *admin.AuditLogHandler,
	teamHandler *admin.TeamHandler,
) *AdminHandlers {
	return &AdminHandlers{
		SchedulerDiagnostics: schedulerDiagnostics,
		AccountManagement:    accountManagement,
		AccountOAuthUsage:    accountOAuthUsage,
		AccountOllama:        accountOllama,
		AccountCodexImport:   accountCodexImport,
		AccountCRS:           accountCRS,
		AccountArchive:       accountArchive,
		AccountTests:         accountTests, UpstreamUsage: upstreamUsage,
		Dashboard:             dashboardHandler,
		User:                  userHandler,
		Group:                 groupHandler,
		Announcement:          announcementHandler,
		DataManagement:        dataManagementHandler,
		Backup:                backupHandler,
		OAuth:                 oauthHandler,
		OpenAIOAuth:           openaiOAuthHandler,
		GeminiOAuth:           geminiOAuthHandler,
		AntigravityOAuth:      antigravityOAuthHandler,
		GrokOAuth:             grokOAuthHandler,
		QoderOAuth:            qoderOAuthHandler,
		Proxy:                 proxyHandler,
		Redeem:                redeemHandler,
		Promo:                 promoHandler,
		Setting:               settingHandler,
		Ops:                   opsHandler,
		System:                systemHandler,
		Subscription:          subscriptionHandler,
		Usage:                 usageHandler,
		UserAttribute:         userAttributeHandler,
		ErrorPassthrough:      errorPassthroughHandler,
		TLSFingerprintProfile: tlsFingerprintProfileHandler,
		TLSFingerprintRouter:  tlsFingerprintRouterHandler,
		APIKey:                apiKeyHandler,
		ScheduledTest:         scheduledTestHandler,
		Channel:               channelHandler,
		ContentModeration:     contentModerationHandler,
		Payment:               paymentHandler,
		Affiliate:             affiliateHandler,
		CodexInviteReset:      codexInviteResetHandler,
		AuditLog:              auditLogHandler,
		Team:                  teamHandler,
	}
}

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

// ProvideHandlers creates the Handlers struct
func ProvideHandlers(
	mediaHTTP *gatewayhttp.MediaHandler,
	auxiliaryHTTP *gatewayhttp.AuxiliaryHandler,
	liveHTTP *gatewayhttp.LiveHandler,
	searchHTTP *gatewayhttp.SearchHandler,
	messagesHTTP *gatewayhttp.MessagesHandler,
	modelsHTTP *gatewayhttp.ModelsHandler,
	responsesWSHTTP *gatewayhttp.ResponsesWSHandler,
	openAITextHTTP *gatewayhttp.OpenAITextHandler,
	geminiNativeHTTP *gatewayhttp.GeminiNativeHandler,
	compatibleTextHTTP *gatewayhttp.CompatibleTextHandler,
	qoderCompatibleHTTP *gatewayhttp.QoderCompatibleHandler,
	countTokensHTTP *gatewayhttp.CountTokensHandler,
	searchHandler *searchhttp.Handler,
	publicSettings *sitehttpapi.PublicHandler,
	notificationHandler *notificationhttp.Handler,
	publicUsage *usagehttp.PublicUsageHandler,
	plans *billinghttpapi.PlanHandler,
	quotaHandler *billinghttpapi.QuotaHandler,
	authHandler AuthEndpoints,
	userHandler *UserHandler,
	apiKeyHandler *APIKeyHandler,
	usageHandler *UsageHandler,
	redeemHandler *billinghttpapi.RedeemHandler,
	subscriptionHandler *billinghttpapi.SubscriptionHandler,
	announcementHandler *sitehttpapi.AnnouncementHandler,
	modelMarketplaceHandler *ModelMarketplaceHandler,
	adminHandlers *AdminHandlers,
	gatewayHandler *GatewayHandler,
	openaiGatewayHandler *OpenAIGatewayHandler,
	qoderGatewayHandler *QoderGatewayHandler,
	qoderChat *gatewayhttp.QoderChatHandler,
	settingHandler *SettingHandler,
	totpHandler *TotpHandler,
	passkeyHandler *PasskeyHandler,
	paymentHandler *PaymentHandler,
	paymentWebhookHandler *PaymentWebhookHandler,
	batchImageHandler *BatchImageHandler,
	creativeHandler *CreativeHandler,
	teamHandler *TeamHandler,
	_ *service.IdempotencyCoordinator,
	_ *service.IdempotencyCleanupService,
) *Handlers {
	return &Handlers{
		MediaHTTP:           mediaHTTP,
		AuxiliaryHTTP:       auxiliaryHTTP,
		LiveHTTP:            liveHTTP,
		SearchHTTP:          searchHTTP,
		QoderCompatibleHTTP: qoderCompatibleHTTP,
		CompatibleTextHTTP:  compatibleTextHTTP,
		GeminiNativeHTTP:    geminiNativeHTTP,
		OpenAITextHTTP:      openAITextHTTP,
		ResponsesWSHTTP:     responsesWSHTTP,
		ModelsHTTP:          modelsHTTP,
		MessagesHTTP:        messagesHTTP,
		CountTokensHTTP:     countTokensHTTP,
		Search:              searchHandler,
		PublicSettings:      publicSettings,
		Notification:        notificationHandler,
		PublicUsage:         publicUsage,
		Plans:               plans,
		PlatformQuota:       quotaHandler,
		Auth:                authHandler,
		User:                userHandler,
		APIKey:              apiKeyHandler,
		Usage:               usageHandler,
		Redeem:              redeemHandler,
		Subscription:        subscriptionHandler,
		Announcement:        announcementHandler,
		ModelMarketplace:    modelMarketplaceHandler,
		Admin:               adminHandlers,
		Gateway:             gatewayHandler,
		OpenAIGateway:       openaiGatewayHandler,
		QoderGateway:        qoderGatewayHandler,
		QoderChat:           qoderChat,
		Setting:             settingHandler,
		Totp:                totpHandler,
		Passkey:             passkeyHandler,
		Payment:             paymentHandler,
		PaymentWebhook:      paymentWebhookHandler,
		BatchImage:          batchImageHandler,
		Creative:            creativeHandler,
		Team:                teamHandler,
	}
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
	admin.NewDataManagementHandler,
	admin.NewBackupHandler,
	admin.NewOAuthHandler,
	admin.NewGeminiOAuthHandler,
	admin.NewAntigravityOAuthHandler,
	admin.NewQoderOAuthHandler,
	billinghttpapi.NewAdminRedeemHandler,
	ProvideAdminSettingHandler,
	opshttp.NewOpsHandler,
	ProvideSystemHandler,
	billinghttpapi.NewAdminSubscriptionHandler,
	admin.NewUserAttributeHandler,
	admin.NewAdminAPIKeyHandler,
	admin.NewCodexInviteResetHandler,
	admin.NewTeamHandler,

	// AdminHandlers and Handlers constructors
	ProvideAdminHandlers,
	ProvideHandlers,
)
