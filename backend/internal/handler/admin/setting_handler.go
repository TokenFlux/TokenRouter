package admin

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"log/slog"
	"regexp"
	"strings"

	"github.com/BrandonVee/TokenRouter/internal/handler/dto"
	"github.com/BrandonVee/TokenRouter/internal/pkg/response"
	"github.com/BrandonVee/TokenRouter/internal/service"

	"github.com/gin-gonic/gin"
)

// semverPattern 预编译 semver 格式校验正则
var semverPattern = regexp.MustCompile(`^\d+\.\d+\.\d+$`)

// menuItemIDPattern validates custom menu item IDs: alphanumeric, hyphens, underscores only.
var menuItemIDPattern = regexp.MustCompile(`^[a-zA-Z0-9_-]+$`)

// generateMenuItemID generates a short random hex ID for a custom menu item.
func generateMenuItemID() (string, error) {
	b := make([]byte, 8)
	if _, err := rand.Read(b); err != nil {
		return "", fmt.Errorf("generate menu item ID: %w", err)
	}
	return hex.EncodeToString(b), nil
}

func scopesContainOpenID(scopes string) bool {
	for _, scope := range strings.Fields(strings.ToLower(strings.TrimSpace(scopes))) {
		if scope == "openid" {
			return true
		}
	}
	return false
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if trimmed := strings.TrimSpace(value); trimmed != "" {
			return trimmed
		}
	}
	return ""
}

// SettingHandler 系统设置处理器
type SettingHandler struct {
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
func (h *SettingHandler) GetSettings(c *gin.Context) {
	settings, err := h.settingService.GetAllSettings(c.Request.Context())
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	authSourceDefaults, err := h.settingService.GetAuthSourceDefaultSettings(c.Request.Context())
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}

	// Check if ops monitoring is enabled (respects config.ops.enabled)
	opsEnabled := h.opsService != nil && h.opsService.IsMonitoringEnabled(c.Request.Context())
	defaultSubscriptions := make([]dto.DefaultSubscriptionSetting, 0, len(settings.DefaultSubscriptions))
	for _, sub := range settings.DefaultSubscriptions {
		defaultSubscriptions = append(defaultSubscriptions, dto.DefaultSubscriptionSetting{
			PlanID: sub.PlanID,
		})
	}

	// Load payment config
	var paymentCfg *service.PaymentConfig
	if h.paymentConfigService != nil {
		paymentCfg, _ = h.paymentConfigService.GetPaymentConfig(c.Request.Context())
	}
	if paymentCfg == nil {
		paymentCfg = &service.PaymentConfig{}
	}

	payload := dto.SystemSettings{
		RegistrationEnabled:                              settings.RegistrationEnabled,
		EmailVerifyEnabled:                               settings.EmailVerifyEnabled,
		RegistrationEmailSuffixWhitelist:                 settings.RegistrationEmailSuffixWhitelist,
		RegistrationEmailNormalization:                   settings.RegistrationEmailNormalization,
		RegistrationEmailDomainQuotaEnabled:              settings.RegistrationEmailDomainQuotaEnabled,
		PromoCodeEnabled:                                 settings.PromoCodeEnabled,
		PasswordResetEnabled:                             settings.PasswordResetEnabled,
		FrontendURL:                                      settings.FrontendURL,
		InvitationCodeEnabled:                            settings.InvitationCodeEnabled,
		TotpEnabled:                                      settings.TotpEnabled,
		TotpEncryptionKeyConfigured:                      h.settingService.IsTotpEncryptionKeyConfigured(),
		SessionBindingEnabled:                            settings.SessionBindingEnabled,
		StepUpEnabled:                                    settings.StepUpEnabled,
		AuditLogRetentionDays:                            settings.AuditLogRetentionDays,
		LoginAgreementEnabled:                            settings.LoginAgreementEnabled,
		LoginAgreementMode:                               settings.LoginAgreementMode,
		LoginAgreementUpdatedAt:                          settings.LoginAgreementUpdatedAt,
		LoginAgreementDocuments:                          loginAgreementDocumentsToDTO(settings.LoginAgreementDocuments),
		SMTPHost:                                         settings.SMTPHost,
		SMTPPort:                                         settings.SMTPPort,
		SMTPUsername:                                     settings.SMTPUsername,
		SMTPPasswordConfigured:                           settings.SMTPPasswordConfigured,
		SMTPFrom:                                         settings.SMTPFrom,
		SMTPFromName:                                     settings.SMTPFromName,
		SMTPUseTLS:                                       settings.SMTPUseTLS,
		TurnstileEnabled:                                 settings.TurnstileEnabled,
		TurnstileSiteKey:                                 settings.TurnstileSiteKey,
		TurnstileSecretKeyConfigured:                     settings.TurnstileSecretKeyConfigured,
		TencentCaptchaEnabled:                            settings.TencentCaptchaEnabled,
		TencentCaptchaAppID:                              settings.TencentCaptchaAppID,
		TencentCaptchaAppSecretKeyConfigured:             settings.TencentCaptchaAppSecretKeyConfigured,
		TencentCaptchaCloudSecretIDConfigured:            settings.TencentCaptchaCloudSecretIDConfigured,
		TencentCaptchaCloudSecretKeyConfigured:           settings.TencentCaptchaCloudSecretKeyConfigured,
		TencentCaptchaRegion:                             settings.TencentCaptchaRegion,
		AliyunCaptchaEnabled:                             settings.AliyunCaptchaEnabled,
		AliyunCaptchaAccessKeyID:                         settings.AliyunCaptchaAccessKeyID,
		AliyunCaptchaAccessKeySecretConfigured:           settings.AliyunCaptchaAccessKeySecretConfigured,
		AliyunCaptchaSceneID:                             settings.AliyunCaptchaSceneID,
		AliyunCaptchaPrefix:                              settings.AliyunCaptchaPrefix,
		AliyunCaptchaRegion:                              settings.AliyunCaptchaRegion,
		APIKeyACLTrustForwardedIP:                        settings.APIKeyACLTrustForwardedIP,
		ForwardedClientIPHeaders:                         settings.ForwardedClientIPHeaders,
		LinuxDoConnectEnabled:                            settings.LinuxDoConnectEnabled,
		LinuxDoConnectClientID:                           settings.LinuxDoConnectClientID,
		LinuxDoConnectClientSecretConfigured:             settings.LinuxDoConnectClientSecretConfigured,
		LinuxDoConnectRedirectURL:                        settings.LinuxDoConnectRedirectURL,
		DingTalkConnectEnabled:                           settings.DingTalkConnectEnabled,
		DingTalkConnectClientID:                          settings.DingTalkConnectClientID,
		DingTalkConnectClientSecretConfigured:            settings.DingTalkConnectClientSecretConfigured,
		DingTalkConnectRedirectURL:                       settings.DingTalkConnectRedirectURL,
		DingTalkConnectCorpRestrictionPolicy:             settings.DingTalkConnectCorpRestrictionPolicy,
		DingTalkConnectInternalCorpID:                    settings.DingTalkConnectInternalCorpID,
		DingTalkConnectBypassRegistration:                settings.DingTalkConnectBypassRegistration,
		DingTalkConnectSyncCorpEmail:                     settings.DingTalkConnectSyncCorpEmail,
		DingTalkConnectSyncDisplayName:                   settings.DingTalkConnectSyncDisplayName,
		DingTalkConnectSyncDept:                          settings.DingTalkConnectSyncDept,
		DingTalkConnectSyncCorpEmailAttrKey:              settings.DingTalkConnectSyncCorpEmailAttrKey,
		DingTalkConnectSyncDisplayNameAttrKey:            settings.DingTalkConnectSyncDisplayNameAttrKey,
		DingTalkConnectSyncDeptAttrKey:                   settings.DingTalkConnectSyncDeptAttrKey,
		DingTalkConnectSyncCorpEmailAttrName:             settings.DingTalkConnectSyncCorpEmailAttrName,
		DingTalkConnectSyncDisplayNameAttrName:           settings.DingTalkConnectSyncDisplayNameAttrName,
		DingTalkConnectSyncDeptAttrName:                  settings.DingTalkConnectSyncDeptAttrName,
		WeChatConnectEnabled:                             settings.WeChatConnectEnabled,
		WeChatConnectAppID:                               settings.WeChatConnectAppID,
		WeChatConnectAppSecretConfigured:                 settings.WeChatConnectAppSecretConfigured,
		WeChatConnectOpenAppID:                           settings.WeChatConnectOpenAppID,
		WeChatConnectOpenAppSecretConfigured:             settings.WeChatConnectOpenAppSecretConfigured,
		WeChatConnectMPAppID:                             settings.WeChatConnectMPAppID,
		WeChatConnectMPAppSecretConfigured:               settings.WeChatConnectMPAppSecretConfigured,
		WeChatConnectMobileAppID:                         settings.WeChatConnectMobileAppID,
		WeChatConnectMobileAppSecretConfigured:           settings.WeChatConnectMobileAppSecretConfigured,
		WeChatConnectOpenEnabled:                         settings.WeChatConnectOpenEnabled,
		WeChatConnectMPEnabled:                           settings.WeChatConnectMPEnabled,
		WeChatConnectMobileEnabled:                       settings.WeChatConnectMobileEnabled,
		WeChatConnectMode:                                settings.WeChatConnectMode,
		WeChatConnectScopes:                              settings.WeChatConnectScopes,
		WeChatConnectRedirectURL:                         settings.WeChatConnectRedirectURL,
		WeChatConnectFrontendRedirectURL:                 settings.WeChatConnectFrontendRedirectURL,
		OIDCConnectEnabled:                               settings.OIDCConnectEnabled,
		OIDCConnectProviderName:                          settings.OIDCConnectProviderName,
		OIDCConnectClientID:                              settings.OIDCConnectClientID,
		OIDCConnectClientSecretConfigured:                settings.OIDCConnectClientSecretConfigured,
		OIDCConnectIssuerURL:                             settings.OIDCConnectIssuerURL,
		OIDCConnectDiscoveryURL:                          settings.OIDCConnectDiscoveryURL,
		OIDCConnectAuthorizeURL:                          settings.OIDCConnectAuthorizeURL,
		OIDCConnectTokenURL:                              settings.OIDCConnectTokenURL,
		OIDCConnectUserInfoURL:                           settings.OIDCConnectUserInfoURL,
		OIDCConnectJWKSURL:                               settings.OIDCConnectJWKSURL,
		OIDCConnectScopes:                                settings.OIDCConnectScopes,
		OIDCConnectRedirectURL:                           settings.OIDCConnectRedirectURL,
		OIDCConnectFrontendRedirectURL:                   settings.OIDCConnectFrontendRedirectURL,
		OIDCConnectTokenAuthMethod:                       settings.OIDCConnectTokenAuthMethod,
		OIDCConnectUsePKCE:                               settings.OIDCConnectUsePKCE,
		OIDCConnectValidateIDToken:                       settings.OIDCConnectValidateIDToken,
		OIDCConnectAllowedSigningAlgs:                    settings.OIDCConnectAllowedSigningAlgs,
		OIDCConnectClockSkewSeconds:                      settings.OIDCConnectClockSkewSeconds,
		OIDCConnectRequireEmailVerified:                  settings.OIDCConnectRequireEmailVerified,
		OIDCConnectUserInfoEmailPath:                     settings.OIDCConnectUserInfoEmailPath,
		OIDCConnectUserInfoIDPath:                        settings.OIDCConnectUserInfoIDPath,
		OIDCConnectUserInfoUsernamePath:                  settings.OIDCConnectUserInfoUsernamePath,
		GitHubOAuthEnabled:                               settings.GitHubOAuthEnabled,
		GitHubOAuthClientID:                              settings.GitHubOAuthClientID,
		GitHubOAuthClientSecretConfigured:                settings.GitHubOAuthClientSecretConfigured,
		GitHubOAuthRedirectURL:                           settings.GitHubOAuthRedirectURL,
		GitHubOAuthFrontendRedirectURL:                   settings.GitHubOAuthFrontendRedirectURL,
		GoogleOAuthEnabled:                               settings.GoogleOAuthEnabled,
		GoogleOneTapEnabled:                              settings.GoogleOneTapEnabled,
		GoogleOAuthClientID:                              settings.GoogleOAuthClientID,
		GoogleOAuthClientSecretConfigured:                settings.GoogleOAuthClientSecretConfigured,
		GoogleOAuthRedirectURL:                           settings.GoogleOAuthRedirectURL,
		GoogleOAuthFrontendRedirectURL:                   settings.GoogleOAuthFrontendRedirectURL,
		SiteName:                                         settings.SiteName,
		SiteLogo:                                         settings.SiteLogo,
		SiteSubtitle:                                     settings.SiteSubtitle,
		SiteNameZh:                                       settings.SiteNameZh,
		SiteNameEn:                                       settings.SiteNameEn,
		SiteTitleZh:                                      settings.SiteTitleZh,
		SiteTitleEn:                                      settings.SiteTitleEn,
		SiteSubtitleZh:                                   settings.SiteSubtitleZh,
		SiteSubtitleEn:                                   settings.SiteSubtitleEn,
		APIBaseURL:                                       settings.APIBaseURL,
		ContactInfo:                                      settings.ContactInfo,
		DocURL:                                           settings.DocURL,
		HomeContent:                                      settings.HomeContent,
		HideCcsImportButton:                              settings.HideCcsImportButton,
		PurchaseSubscriptionEnabled:                      settings.PurchaseSubscriptionEnabled,
		PurchaseSubscriptionURL:                          settings.PurchaseSubscriptionURL,
		TableDefaultPageSize:                             settings.TableDefaultPageSize,
		TablePageSizeOptions:                             settings.TablePageSizeOptions,
		UsageRankingLimit:                                settings.UsageRankingLimit,
		CustomMenuItems:                                  dto.ParseCustomMenuItems(settings.CustomMenuItems),
		CustomEndpoints:                                  dto.ParseCustomEndpoints(settings.CustomEndpoints),
		FooterLinks:                                      dto.ParseFooterLinks(settings.FooterLinks),
		FooterText:                                       settings.FooterText,
		DefaultConcurrency:                               settings.DefaultConcurrency,
		DefaultBalance:                                   settings.DefaultBalance,
		TeamEnabled:                                      settings.TeamEnabled,
		DataSharingEnabled:                               settings.DataSharingEnabled,
		RiskControlEnabled:                               settings.RiskControlEnabled,
		CyberSessionBlockEnabled:                         settings.CyberSessionBlockEnabled,
		CyberSessionBlockTTLSeconds:                      settings.CyberSessionBlockTTLSeconds,
		AffiliateEnabled:                                 settings.AffiliateEnabled,
		AffiliateRebateRate:                              settings.AffiliateRebateRate,
		AffiliateRebateFreezeHours:                       settings.AffiliateRebateFreezeHours,
		AffiliateRebateDurationDays:                      settings.AffiliateRebateDurationDays,
		AffiliateRebatePerInviteeCap:                     settings.AffiliateRebatePerInviteeCap,
		AdminRechargeRebateEnabled:                       settings.AdminRechargeRebateEnabled,
		DefaultUserRPMLimit:                              settings.DefaultUserRPMLimit,
		DefaultUserAPIKeyLimit:                           settings.DefaultUserAPIKeyLimit,
		DefaultSubscriptions:                             defaultSubscriptions,
		BalanceUnitName:                                  settings.BalanceUnitName,
		BalanceUnitSymbol:                                settings.BalanceUnitSymbol,
		BalanceIconSVG:                                   settings.BalanceIconSVG,
		ReasoningPointRMBUnitPrice:                       settings.ReasoningPointRMBUnitPrice,
		USDExchangeRate:                                  settings.USDExchangeRate,
		MarketplaceAvailabilityWindowDays:                settings.MarketplaceAvailabilityWindowDays,
		MarketplaceAvailabilityBucketMinutes:             settings.MarketplaceAvailabilityBucketMinutes,
		EnableModelFallback:                              settings.EnableModelFallback,
		FallbackModelAnthropic:                           settings.FallbackModelAnthropic,
		FallbackModelOpenAI:                              settings.FallbackModelOpenAI,
		FallbackModelGemini:                              settings.FallbackModelGemini,
		FallbackModelAntigravity:                         settings.FallbackModelAntigravity,
		GrokDefaultTextModel:                             settings.GrokDefaultTextModel,
		GrokCrossClientModelMapEnabled:                   settings.GrokCrossClientModelMapEnabled,
		GrokDefaultBaseURLMode:                           settings.GrokDefaultBaseURLMode,
		EnableIdentityPatch:                              settings.EnableIdentityPatch,
		IdentityPatchPrompt:                              settings.IdentityPatchPrompt,
		OpsMonitoringEnabled:                             opsEnabled && settings.OpsMonitoringEnabled,
		OpsRealtimeMonitoringEnabled:                     settings.OpsRealtimeMonitoringEnabled,
		OpsMetricsIntervalSeconds:                        settings.OpsMetricsIntervalSeconds,
		MinClaudeCodeVersion:                             settings.MinClaudeCodeVersion,
		MaxClaudeCodeVersion:                             settings.MaxClaudeCodeVersion,
		AllowUngroupedKeyScheduling:                      settings.AllowUngroupedKeyScheduling,
		BackendModeEnabled:                               settings.BackendModeEnabled,
		EnableFingerprintUnification:                     settings.EnableFingerprintUnification,
		EnableMetadataPassthrough:                        settings.EnableMetadataPassthrough,
		EnableCCHSigning:                                 settings.EnableCCHSigning,
		EnableClaudeOAuthSystemPromptInjection:           settings.EnableClaudeOAuthSystemPromptInjection,
		ClaudeOAuthSystemPrompt:                          settings.ClaudeOAuthSystemPrompt,
		ClaudeOAuthSystemPromptBlocks:                    settings.ClaudeOAuthSystemPromptBlocks,
		EnableAnthropicCacheTTL1hInjection:               settings.EnableAnthropicCacheTTL1hInjection,
		RewriteMessageCacheControl:                       settings.RewriteMessageCacheControl,
		EnableClientDatelineNormalization:                settings.EnableClientDatelineNormalization,
		AntigravityUserAgentVersion:                      settings.AntigravityUserAgentVersion,
		OpenAICodexUserAgent:                             settings.OpenAICodexUserAgent,
		OpenAIAllowClaudeCodeCodexPlugin:                 settings.OpenAIAllowClaudeCodeCodexPlugin,
		UserPromptReplacementConfig:                      settings.UserPromptReplacementConfig,
		WebSearchEmulationEnabled:                        settings.WebSearchEmulationEnabled,
		PaymentVisibleMethodAlipaySource:                 settings.PaymentVisibleMethodAlipaySource,
		PaymentVisibleMethodWxpaySource:                  settings.PaymentVisibleMethodWxpaySource,
		PaymentVisibleMethodAlipayEnabled:                settings.PaymentVisibleMethodAlipayEnabled,
		PaymentVisibleMethodWxpayEnabled:                 settings.PaymentVisibleMethodWxpayEnabled,
		AdvancedSchedulerStickyWeightedEnabled:           settings.AdvancedSchedulerStickyWeightedEnabled,
		AdvancedSchedulerSubscriptionPriorityEnabled:     settings.AdvancedSchedulerSubscriptionPriorityEnabled,
		AdvancedSchedulerLBTopK:                          settings.AdvancedSchedulerLBTopK,
		AdvancedSchedulerWeightPriority:                  settings.AdvancedSchedulerWeightPriority,
		AdvancedSchedulerWeightLoad:                      settings.AdvancedSchedulerWeightLoad,
		AdvancedSchedulerWeightQueue:                     settings.AdvancedSchedulerWeightQueue,
		AdvancedSchedulerWeightErrorRate:                 settings.AdvancedSchedulerWeightErrorRate,
		AdvancedSchedulerWeightTTFT:                      settings.AdvancedSchedulerWeightTTFT,
		AdvancedSchedulerWeightReset:                     settings.AdvancedSchedulerWeightReset,
		AdvancedSchedulerWeightQuotaHeadroom:             settings.AdvancedSchedulerWeightQuotaHeadroom,
		AdvancedSchedulerWeightPreviousResponse:          settings.AdvancedSchedulerWeightPreviousResponse,
		AdvancedSchedulerWeightSessionSticky:             settings.AdvancedSchedulerWeightSessionSticky,
		AdvancedSchedulerEffectiveLBTopK:                 settings.AdvancedSchedulerEffectiveLBTopK,
		AdvancedSchedulerEffectiveWeightPriority:         settings.AdvancedSchedulerEffectiveWeightPriority,
		AdvancedSchedulerEffectiveWeightLoad:             settings.AdvancedSchedulerEffectiveWeightLoad,
		AdvancedSchedulerEffectiveWeightQueue:            settings.AdvancedSchedulerEffectiveWeightQueue,
		AdvancedSchedulerEffectiveWeightErrorRate:        settings.AdvancedSchedulerEffectiveWeightErrorRate,
		AdvancedSchedulerEffectiveWeightTTFT:             settings.AdvancedSchedulerEffectiveWeightTTFT,
		AdvancedSchedulerEffectiveWeightReset:            settings.AdvancedSchedulerEffectiveWeightReset,
		AdvancedSchedulerEffectiveWeightQuotaHeadroom:    settings.AdvancedSchedulerEffectiveWeightQuotaHeadroom,
		AdvancedSchedulerEffectiveWeightPreviousResponse: settings.AdvancedSchedulerEffectiveWeightPreviousResponse,
		AdvancedSchedulerEffectiveWeightSessionSticky:    settings.AdvancedSchedulerEffectiveWeightSessionSticky,
		OpenAIQuotaAutoPauseSettings:                     settings.OpenAIQuotaAutoPauseSettings,
		BalanceLowNotifyEnabled:                          settings.BalanceLowNotifyEnabled,
		BalanceLowNotifyThreshold:                        settings.BalanceLowNotifyThreshold,
		BalanceLowNotifyRechargeURL:                      settings.BalanceLowNotifyRechargeURL,
		SubscriptionExpiryNotifyEnabled:                  settings.SubscriptionExpiryNotifyEnabled,
		AccountQuotaNotifyEnabled:                        settings.AccountQuotaNotifyEnabled,
		AccountQuotaNotifyEmails:                         dto.NotifyEmailEntriesFromService(settings.AccountQuotaNotifyEmails),
		PaymentEnabled:                                   paymentCfg.Enabled,
		PaymentMinAmount:                                 paymentCfg.MinAmount,
		PaymentMaxAmount:                                 paymentCfg.MaxAmount,
		PaymentDailyLimit:                                paymentCfg.DailyLimit,
		PaymentOrderTimeoutMin:                           paymentCfg.OrderTimeoutMin,
		PaymentMaxPendingOrders:                          paymentCfg.MaxPendingOrders,
		PaymentEnabledTypes:                              paymentCfg.EnabledTypes,
		PaymentBalanceDisabled:                           paymentCfg.BalanceDisabled,
		PaymentBalanceRechargeMultiplier:                 paymentCfg.BalanceRechargeMultiplier,
		PaymentSubscriptionUSDToCNYRate:                  paymentCfg.SubscriptionUSDToCNYRate,
		PaymentRechargeFeeRate:                           paymentCfg.RechargeFeeRate,
		PaymentMethodFees:                                paymentCfg.MethodFees,
		PaymentLoadBalanceStrat:                          paymentCfg.LoadBalanceStrategy,
		PaymentProductNamePrefix:                         paymentCfg.ProductNamePrefix,
		PaymentProductNameSuffix:                         paymentCfg.ProductNameSuffix,
		PaymentHelpImageURL:                              paymentCfg.HelpImageURL,
		PaymentHelpText:                                  paymentCfg.HelpText,
		PaymentCancelRateLimitEnabled:                    paymentCfg.CancelRateLimitEnabled,
		PaymentCancelRateLimitMax:                        paymentCfg.CancelRateLimitMax,
		PaymentCancelRateLimitWindow:                     paymentCfg.CancelRateLimitWindow,
		PaymentCancelRateLimitUnit:                       paymentCfg.CancelRateLimitUnit,
		PaymentCancelRateLimitMode:                       paymentCfg.CancelRateLimitMode,
		PaymentAlipayForceQRCode:                         paymentCfg.AlipayForceQRCode,
		PaymentAlipayMobilePrecreateDeepLink:             paymentCfg.AlipayMobilePrecreateDeepLink,
		AccountSchedulingThresholds:                      settings.AccountSchedulingThresholds,
		AllowUserViewErrorRequests:                       settings.AllowUserViewErrorRequests,
	}

	// OpenAI fast policy (stored under a dedicated setting key)
	if fastPolicy, err := h.settingService.GetOpenAIFastPolicySettings(c.Request.Context()); err != nil {
		slog.Error("openai_fast_policy_settings_get_failed", "error", err)
	} else if fastPolicy != nil {
		payload.OpenAIFastPolicySettings = openaiFastPolicySettingsToDTO(fastPolicy)
	}

	// 默认平台限额（JSON map）
	if platformQuotas, err := h.settingService.GetDefaultPlatformQuotas(c.Request.Context()); err != nil {
		slog.Error("default_platform_quotas_get_failed", "error", err)
	} else {
		payload.DefaultPlatformQuotas = platformQuotas
	}

	response.Success(c, systemSettingsResponseData(payload, authSourceDefaults))
}

// openaiFastPolicySettingsToDTO converts service -> dto for OpenAI fast policy.
func openaiFastPolicySettingsToDTO(s *service.OpenAIFastPolicySettings) *dto.OpenAIFastPolicySettings {
	if s == nil {
		return nil
	}
	rules := make([]dto.OpenAIFastPolicyRule, len(s.Rules))
	for i, r := range s.Rules {
		rules[i] = dto.OpenAIFastPolicyRule(r)
	}
	return &dto.OpenAIFastPolicySettings{Rules: rules}
}

// openaiFastPolicySettingsFromDTO converts dto -> service for OpenAI fast policy.
//
// 规范化 ServiceTier：在 DTO 进入 service 层之前统一把空字符串归一为
// service.OpenAIFastTierAny ("all")，避免管理员保存时空串与 "all" 同时
// 表达"匹配任意 tier"造成数据库取值的二义性。其它非空值原样透传，由
// service.SetOpenAIFastPolicySettings 负责合法值校验。
func openaiFastPolicySettingsFromDTO(s *dto.OpenAIFastPolicySettings) *service.OpenAIFastPolicySettings {
	if s == nil {
		return nil
	}
	rules := make([]service.OpenAIFastPolicyRule, len(s.Rules))
	for i, r := range s.Rules {
		rules[i] = service.OpenAIFastPolicyRule(r)
		tier := strings.ToLower(strings.TrimSpace(rules[i].ServiceTier))
		if tier == "" {
			tier = service.OpenAIFastTierAny
		}
		rules[i].ServiceTier = tier
	}
	return &service.OpenAIFastPolicySettings{Rules: rules}
}

func loginAgreementDocumentsToDTO(items []service.LoginAgreementDocument) []dto.LoginAgreementDocument {
	result := make([]dto.LoginAgreementDocument, 0, len(items))
	for _, item := range items {
		result = append(result, dto.LoginAgreementDocument{
			ID:        item.ID,
			Title:     item.Title,
			ContentMD: item.ContentMD,
		})
	}
	return result
}

func loginAgreementDocumentsToService(items []dto.LoginAgreementDocument) []service.LoginAgreementDocument {
	result := make([]service.LoginAgreementDocument, 0, len(items))
	for _, item := range items {
		title := strings.TrimSpace(item.Title)
		content := strings.TrimSpace(item.ContentMD)
		if title == "" && content == "" {
			continue
		}
		result = append(result, service.LoginAgreementDocument{
			ID:        strings.TrimSpace(item.ID),
			Title:     title,
			ContentMD: content,
		})
	}
	return result
}

func systemSettingsResponseData(settings dto.SystemSettings, authSourceDefaults *service.AuthSourceDefaultSettings) map[string]any {
	if settings.PaymentMethodFees == nil {
		settings.PaymentMethodFees = service.MethodFeeSettings{}
	}

	data := make(map[string]any)
	raw, err := json.Marshal(settings)
	if err == nil {
		_ = json.Unmarshal(raw, &data)
	}
	if authSourceDefaults == nil {
		authSourceDefaults = &service.AuthSourceDefaultSettings{}
	}

	data["auth_source_default_email_balance"] = authSourceDefaults.Email.Balance
	data["auth_source_default_email_concurrency"] = authSourceDefaults.Email.Concurrency
	data["auth_source_default_email_subscriptions"] = authSourceDefaults.Email.Subscriptions
	data["auth_source_default_email_grant_on_signup"] = authSourceDefaults.Email.GrantOnSignup
	data["auth_source_default_email_grant_on_first_bind"] = authSourceDefaults.Email.GrantOnFirstBind
	data["auth_source_default_linuxdo_balance"] = authSourceDefaults.LinuxDo.Balance
	data["auth_source_default_linuxdo_concurrency"] = authSourceDefaults.LinuxDo.Concurrency
	data["auth_source_default_linuxdo_subscriptions"] = authSourceDefaults.LinuxDo.Subscriptions
	data["auth_source_default_linuxdo_grant_on_signup"] = authSourceDefaults.LinuxDo.GrantOnSignup
	data["auth_source_default_linuxdo_grant_on_first_bind"] = authSourceDefaults.LinuxDo.GrantOnFirstBind
	data["auth_source_default_dingtalk_balance"] = authSourceDefaults.DingTalk.Balance
	data["auth_source_default_dingtalk_concurrency"] = authSourceDefaults.DingTalk.Concurrency
	data["auth_source_default_dingtalk_subscriptions"] = authSourceDefaults.DingTalk.Subscriptions
	data["auth_source_default_dingtalk_grant_on_signup"] = authSourceDefaults.DingTalk.GrantOnSignup
	data["auth_source_default_dingtalk_grant_on_first_bind"] = authSourceDefaults.DingTalk.GrantOnFirstBind
	data["auth_source_default_oidc_balance"] = authSourceDefaults.OIDC.Balance
	data["auth_source_default_oidc_concurrency"] = authSourceDefaults.OIDC.Concurrency
	data["auth_source_default_oidc_subscriptions"] = authSourceDefaults.OIDC.Subscriptions
	data["auth_source_default_oidc_grant_on_signup"] = authSourceDefaults.OIDC.GrantOnSignup
	data["auth_source_default_oidc_grant_on_first_bind"] = authSourceDefaults.OIDC.GrantOnFirstBind
	data["auth_source_default_wechat_balance"] = authSourceDefaults.WeChat.Balance
	data["auth_source_default_wechat_concurrency"] = authSourceDefaults.WeChat.Concurrency
	data["auth_source_default_wechat_subscriptions"] = authSourceDefaults.WeChat.Subscriptions
	data["auth_source_default_wechat_grant_on_signup"] = authSourceDefaults.WeChat.GrantOnSignup
	data["auth_source_default_wechat_grant_on_first_bind"] = authSourceDefaults.WeChat.GrantOnFirstBind
	data["auth_source_default_github_balance"] = authSourceDefaults.GitHub.Balance
	data["auth_source_default_github_concurrency"] = authSourceDefaults.GitHub.Concurrency
	data["auth_source_default_github_subscriptions"] = authSourceDefaults.GitHub.Subscriptions
	data["auth_source_default_github_grant_on_signup"] = authSourceDefaults.GitHub.GrantOnSignup
	data["auth_source_default_github_grant_on_first_bind"] = authSourceDefaults.GitHub.GrantOnFirstBind
	data["auth_source_default_google_balance"] = authSourceDefaults.Google.Balance
	data["auth_source_default_google_concurrency"] = authSourceDefaults.Google.Concurrency
	data["auth_source_default_google_subscriptions"] = authSourceDefaults.Google.Subscriptions
	data["auth_source_default_google_grant_on_signup"] = authSourceDefaults.Google.GrantOnSignup
	data["auth_source_default_google_grant_on_first_bind"] = authSourceDefaults.Google.GrantOnFirstBind
	data["auth_source_default_email_platform_quotas"] = authSourceDefaults.Email.PlatformQuotas
	data["auth_source_default_linuxdo_platform_quotas"] = authSourceDefaults.LinuxDo.PlatformQuotas
	data["auth_source_default_oidc_platform_quotas"] = authSourceDefaults.OIDC.PlatformQuotas
	data["auth_source_default_wechat_platform_quotas"] = authSourceDefaults.WeChat.PlatformQuotas
	data["auth_source_default_github_platform_quotas"] = authSourceDefaults.GitHub.PlatformQuotas
	data["auth_source_default_google_platform_quotas"] = authSourceDefaults.Google.PlatformQuotas
	data["auth_source_default_dingtalk_platform_quotas"] = authSourceDefaults.DingTalk.PlatformQuotas
	data["force_email_on_third_party_signup"] = authSourceDefaults.ForceEmailOnThirdPartySignup

	return data
}

// GetOpenAI403CooldownSettings 获取 OpenAI OAuth 403 冷却配置
// GET /api/v1/admin/settings/openai-403-cooldown
func (h *SettingHandler) GetOpenAI403CooldownSettings(c *gin.Context) {
	settings, err := h.settingService.GetOpenAI403CooldownSettings(c.Request.Context())
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}

	response.Success(c, dto.OpenAI403CooldownSettings{
		Enabled:                 settings.Enabled,
		CooldownMinutes:         settings.CooldownMinutes,
		ErrorOnThresholdEnabled: settings.ErrorOnThresholdEnabled,
		ThresholdCount:          settings.ThresholdCount,
		ThresholdWindowMinutes:  settings.ThresholdWindowMinutes,
	})
}

// GetOpenAIOAuthImportDefaults 获取 OpenAI OAuth 账号导入缺省模板
// GET /api/v1/admin/settings/openai-oauth-import-defaults
func (h *SettingHandler) GetOpenAIOAuthImportDefaults(c *gin.Context) {
	settings, err := h.settingService.GetOpenAIOAuthImportDefaults(c.Request.Context())
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}

	response.Success(c, openAIOAuthImportDefaultsToDTO(settings))
}

// UpdateOpenAI403CooldownSettings 更新 OpenAI OAuth 403 冷却配置
// PUT /api/v1/admin/settings/openai-403-cooldown
func (h *SettingHandler) UpdateOpenAI403CooldownSettings(c *gin.Context) {
	var req UpdateOpenAI403CooldownSettingsRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, "Invalid request: "+err.Error())
		return
	}

	defaults := service.DefaultOpenAI403CooldownSettings()
	errorOnThresholdEnabled := defaults.ErrorOnThresholdEnabled
	if req.ErrorOnThresholdEnabled != nil {
		errorOnThresholdEnabled = *req.ErrorOnThresholdEnabled
	}
	thresholdCount := defaults.ThresholdCount
	if req.ThresholdCount != nil {
		thresholdCount = *req.ThresholdCount
	}
	thresholdWindowMinutes := defaults.ThresholdWindowMinutes
	if req.ThresholdWindowMinutes != nil {
		thresholdWindowMinutes = *req.ThresholdWindowMinutes
	}

	settings := &service.OpenAI403CooldownSettings{
		Enabled:                 req.Enabled,
		CooldownMinutes:         req.CooldownMinutes,
		ErrorOnThresholdEnabled: errorOnThresholdEnabled,
		ThresholdCount:          thresholdCount,
		ThresholdWindowMinutes:  thresholdWindowMinutes,
	}

	if err := h.settingService.SetOpenAI403CooldownSettings(c.Request.Context(), settings); err != nil {
		response.BadRequest(c, err.Error())
		return
	}

	updatedSettings, err := h.settingService.GetOpenAI403CooldownSettings(c.Request.Context())
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}

	response.Success(c, dto.OpenAI403CooldownSettings{
		Enabled:                 updatedSettings.Enabled,
		CooldownMinutes:         updatedSettings.CooldownMinutes,
		ErrorOnThresholdEnabled: updatedSettings.ErrorOnThresholdEnabled,
		ThresholdCount:          updatedSettings.ThresholdCount,
		ThresholdWindowMinutes:  updatedSettings.ThresholdWindowMinutes,
	})
}

// UpdateOpenAIOAuthImportDefaults 更新 OpenAI OAuth 账号导入缺省模板
// PUT /api/v1/admin/settings/openai-oauth-import-defaults
func (h *SettingHandler) UpdateOpenAIOAuthImportDefaults(c *gin.Context) {
	var raw map[string]json.RawMessage
	if err := c.ShouldBindJSON(&raw); err != nil {
		response.BadRequest(c, "Invalid request: "+err.Error())
		return
	}
	if raw == nil {
		response.BadRequest(c, "request body must be an object")
		return
	}
	if err := validateOpenAIOAuthImportDefaultsRequest(raw); err != nil {
		response.BadRequest(c, err.Error())
		return
	}

	data, err := json.Marshal(raw)
	if err != nil {
		response.BadRequest(c, "Invalid request: "+err.Error())
		return
	}

	var req dto.OpenAIOAuthImportDefaults
	if err := json.Unmarshal(data, &req); err != nil {
		response.BadRequest(c, "Invalid request: "+err.Error())
		return
	}

	if err := h.settingService.SetOpenAIOAuthImportDefaults(c.Request.Context(), openAIOAuthImportDefaultsFromDTO(&req)); err != nil {
		response.BadRequest(c, err.Error())
		return
	}

	updatedSettings, err := h.settingService.GetOpenAIOAuthImportDefaults(c.Request.Context())
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}

	response.Success(c, openAIOAuthImportDefaultsToDTO(updatedSettings))
}

func equalUserPromptReplacementConfig(a, b *service.UserPromptReplacementConfig) bool {
	// 用户提示词替换是嵌套 JSON 配置，序列化后比较可避免遗漏 rules 内字段。
	rawA, _ := json.Marshal(a)
	rawB, _ := json.Marshal(b)
	return string(rawA) == string(rawB)
}

func openAIOAuthImportDefaultsFromDTO(s *dto.OpenAIOAuthImportDefaults) *service.OpenAIOAuthImportDefaults {
	if s == nil {
		return nil
	}
	return &service.OpenAIOAuthImportDefaults{
		Account: service.OpenAIOAuthImportAccountDefaults{
			Notes:              s.Account.Notes,
			Concurrency:        s.Account.Concurrency,
			Priority:           s.Account.Priority,
			RateMultiplier:     s.Account.RateMultiplier,
			ExpiresAt:          s.Account.ExpiresAt,
			AutoPauseOnExpired: s.Account.AutoPauseOnExpired,
		},
		Credentials: s.Credentials,
		Extra:       s.Extra,
	}
}

func openAIOAuthImportDefaultsToDTO(s *service.OpenAIOAuthImportDefaults) dto.OpenAIOAuthImportDefaults {
	if s == nil {
		return dto.OpenAIOAuthImportDefaults{}
	}
	return dto.OpenAIOAuthImportDefaults{
		Account: dto.OpenAIOAuthImportAccountDefaults{
			Notes:              s.Account.Notes,
			Concurrency:        s.Account.Concurrency,
			Priority:           s.Account.Priority,
			RateMultiplier:     s.Account.RateMultiplier,
			ExpiresAt:          s.Account.ExpiresAt,
			AutoPauseOnExpired: s.Account.AutoPauseOnExpired,
		},
		Credentials: s.Credentials,
		Extra:       s.Extra,
	}
}

func openAIOAuthImportRawObject(raw map[string]json.RawMessage, section string) (map[string]json.RawMessage, bool, error) {
	value, ok := raw[section]
	if !ok || string(value) == "null" {
		return nil, false, nil
	}

	var fields map[string]json.RawMessage
	if err := json.Unmarshal(value, &fields); err != nil || fields == nil {
		if err == nil {
			err = fmt.Errorf("%s must be an object", section)
		}
		return nil, false, err
	}
	return fields, true, nil
}

func rejectOpenAIOAuthImportFields(raw map[string]json.RawMessage, section string, forbidden map[string]struct{}) error {
	fields, ok, err := openAIOAuthImportRawObject(raw, section)
	if err != nil || !ok {
		return err
	}
	for key := range fields {
		normalized := strings.ToLower(strings.TrimSpace(key))
		if _, ok := forbidden[normalized]; ok {
			return fmt.Errorf("%s.%s is not allowed in import defaults", section, key)
		}
	}
	return nil
}

func validateOpenAIOAuthImportDefaultsRequest(raw map[string]json.RawMessage) error {
	allowedRoot := map[string]struct{}{
		"account":     {},
		"credentials": {},
		"extra":       {},
	}
	for key := range raw {
		normalized := strings.ToLower(strings.TrimSpace(key))
		if normalized != key {
			return fmt.Errorf("%s is not allowed in import defaults", key)
		}
		if _, ok := allowedRoot[normalized]; !ok {
			return fmt.Errorf("%s is not allowed in import defaults", key)
		}
	}

	if err := validateOpenAIOAuthImportObject(raw, "account", map[string]struct{}{
		"notes":                 {},
		"concurrency":           {},
		"priority":              {},
		"rate_multiplier":       {},
		"expires_at":            {},
		"auto_pause_on_expired": {},
	}); err != nil {
		return err
	}
	if err := rejectOpenAIOAuthImportFields(raw, "credentials", map[string]struct{}{
		"access_token":            {},
		"refresh_token":           {},
		"id_token":                {},
		"expires_at":              {},
		"email":                   {},
		"client_id":               {},
		"chatgpt_account_id":      {},
		"chatgpt_user_id":         {},
		"organization_id":         {},
		"plan_type":               {},
		"subscription_expires_at": {},
	}); err != nil {
		return err
	}
	return rejectOpenAIOAuthImportFields(raw, "extra", map[string]struct{}{
		"email": {},
		"name":  {},
	})
}

func validateOpenAIOAuthImportObject(raw map[string]json.RawMessage, section string, allowed map[string]struct{}) error {
	fields, ok, err := openAIOAuthImportRawObject(raw, section)
	if err != nil || !ok {
		return err
	}
	for key := range fields {
		normalized := strings.ToLower(strings.TrimSpace(key))
		if normalized != key {
			return fmt.Errorf("%s.%s is not allowed in import defaults", section, key)
		}
		if _, ok := allowed[normalized]; !ok {
			return fmt.Errorf("%s.%s is not allowed in import defaults", section, key)
		}
	}
	return nil
}

// UpdateOpenAI403CooldownSettingsRequest 更新 OpenAI OAuth 403 冷却配置请求
type UpdateOpenAI403CooldownSettingsRequest struct {
	Enabled                 bool  `json:"enabled"`
	CooldownMinutes         int   `json:"cooldown_minutes"`
	ErrorOnThresholdEnabled *bool `json:"error_on_threshold_enabled"`
	ThresholdCount          *int  `json:"threshold_count"`
	ThresholdWindowMinutes  *int  `json:"threshold_window_minutes"`
}

// markdownMenuSlugPattern 校验自定义 Markdown 页面 slug，需与页面读取接口保持一致。
var markdownMenuSlugPattern = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9_-]*$`)
