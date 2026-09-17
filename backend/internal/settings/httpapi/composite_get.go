package httpapi

import (
	"github.com/TokenFlux/TokenRouter/internal/gateway/tierpolicy"
	"github.com/TokenFlux/TokenRouter/internal/identity"
	"github.com/TokenFlux/TokenRouter/internal/payment"
	"github.com/TokenFlux/TokenRouter/internal/site"

	billinghttp "github.com/TokenFlux/TokenRouter/internal/billing/httpapi"
	gatewaydto "github.com/TokenFlux/TokenRouter/internal/gateway/httpapi/dto"
	identitydto "github.com/TokenFlux/TokenRouter/internal/identity/httpapi/dto"
	settingsdto "github.com/TokenFlux/TokenRouter/internal/settings/httpapi/dto"
	sitedto "github.com/TokenFlux/TokenRouter/internal/site/httpapi/dto"

	"encoding/json"
	"log/slog"
	"strings"

	response "github.com/TokenFlux/TokenRouter/internal/server/httpx"

	"github.com/gin-gonic/gin"
)

func (h *Handler) GetSettings(c *gin.Context) {
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

	// Check if ops monitoring is enabled (respects deployment ops.enabled)
	opsEnabled := h.opsService != nil && h.opsService.IsMonitoringEnabled(c.Request.Context())
	defaultSubscriptions := make([]billinghttp.DefaultSubscriptionSetting, 0, len(settings.DefaultSubscriptions))
	for _, sub := range settings.DefaultSubscriptions {
		defaultSubscriptions = append(defaultSubscriptions, billinghttp.DefaultSubscriptionSetting{
			PlanID: sub.PlanID,
		})
	}

	// Load payment config
	var paymentCfg *payment.PaymentConfig
	if h.paymentConfigService != nil {
		paymentCfg, _ = h.paymentConfigService.GetPaymentConfig(c.Request.Context())
	}
	if paymentCfg == nil {
		paymentCfg = &payment.PaymentConfig{}
	}

	payload := settingsdto.SystemSettings{
		RegistrationEnabled:                              settings.RegistrationEnabled,
		EmailVerifyEnabled:                               settings.EmailVerifyEnabled,
		RegistrationEmailSuffixWhitelist:                 settings.RegistrationEmailSuffixWhitelist,
		RegistrationEmailNormalization:                   settings.RegistrationEmailNormalization,
		RegistrationEmailDomainQuotaEnabled:              settings.RegistrationEmailDomainQuotaEnabled,
		UserEmailChangeEnabled:                           settings.UserEmailChangeEnabled,
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
		UsageRankingEnabled:                              settings.UsageRankingEnabled,
		UsageRankingSortBy:                               settings.UsageRankingSortBy,
		UsageRankingShowTotalTokens:                      settings.UsageRankingShowTotalTokens,
		UsageRankingShowRequests:                         settings.UsageRankingShowRequests,
		UsageRankingShowActualCost:                       settings.UsageRankingShowActualCost,
		CustomMenuItems:                                  sitedto.ParseCustomMenuItems(settings.CustomMenuItems),
		CustomEndpoints:                                  sitedto.ParseCustomEndpoints(settings.CustomEndpoints),
		FooterLinks:                                      sitedto.ParseFooterLinks(settings.FooterLinks),
		FooterText:                                       settings.FooterText,
		HomeFeaturedModels:                               sitedto.ParseHomeFeaturedModels(settings.HomeFeaturedModels),
		DefaultConcurrency:                               settings.DefaultConcurrency,
		DefaultBalance:                                   settings.DefaultBalance,
		TeamEnabled:                                      settings.TeamEnabled,
		CreativeEnabled:                                  settings.CreativeEnabled,
		CreativeModelSettings:                            settings.CreativeModelSettings,
		CreativeWorkerCount:                              settings.CreativeWorkerCount,
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
		OpenAITTFTMode:                                   settings.OpenAITTFTMode,
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
		AdvancedSchedulerEWMAErrorRateAlpha:              settings.AdvancedSchedulerEWMAErrorRateAlpha,
		AdvancedSchedulerEWMATTFTAlpha:                   settings.AdvancedSchedulerEWMATTFTAlpha,
		AdvancedSchedulerStickyEscapeEnabled:             settings.AdvancedSchedulerStickyEscapeEnabled,
		AdvancedSchedulerStickyEscapeTTFTMs:              settings.AdvancedSchedulerStickyEscapeTTFTMs,
		AdvancedSchedulerStickyEscapeErrorRate:           settings.AdvancedSchedulerStickyEscapeErrorRate,
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
		AdvancedSchedulerEffectiveEWMAErrorRateAlpha:     settings.AdvancedSchedulerEffectiveEWMAErrorRateAlpha,
		AdvancedSchedulerEffectiveEWMATTFTAlpha:          settings.AdvancedSchedulerEffectiveEWMATTFTAlpha,
		AdvancedSchedulerEffectiveStickyEscapeEnabled:    settings.AdvancedSchedulerEffectiveStickyEscapeEnabled,
		AdvancedSchedulerEffectiveStickyEscapeTTFTMs:     settings.AdvancedSchedulerEffectiveStickyEscapeTTFTMs,
		AdvancedSchedulerEffectiveStickyEscapeErrorRate:  settings.AdvancedSchedulerEffectiveStickyEscapeErrorRate,
		OpenAIQuotaAutoPauseSettings:                     settings.OpenAIQuotaAutoPauseSettings,
		BalanceLowNotifyEnabled:                          settings.BalanceLowNotifyEnabled,
		BalanceLowNotifyThreshold:                        settings.BalanceLowNotifyThreshold,
		BalanceLowNotifyRechargeURL:                      settings.BalanceLowNotifyRechargeURL,
		SubscriptionExpiryNotifyEnabled:                  settings.SubscriptionExpiryNotifyEnabled,
		AccountQuotaNotifyEnabled:                        settings.AccountQuotaNotifyEnabled,
		AccountQuotaNotifyEmails:                         identitydto.NotifyEmailEntriesFromIdentity(settings.AccountQuotaNotifyEmails),
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
func openaiFastPolicySettingsToDTO(s *tierpolicy.OpenAIFastPolicySettings) *gatewaydto.OpenAIFastPolicySettings {
	if s == nil {
		return nil
	}
	rules := make([]gatewaydto.OpenAIFastPolicyRule, len(s.Rules))
	for i, r := range s.Rules {
		rules[i] = gatewaydto.OpenAIFastPolicyRule(r)
	}
	return &gatewaydto.OpenAIFastPolicySettings{Rules: rules}
}

// OpenaiFastPolicySettingsFromDTO converts dto -> service for OpenAI fast policy.
//
// 规范化 ServiceTier：在 DTO 进入 service 层之前统一把空字符串归一为
// tierpolicy.OpenAIFastTierAny ("all")，避免管理员保存时空串与 "all" 同时
// 表达"匹配任意 tier"造成数据库取值的二义性。其它非空值原样透传，由
// tierpolicy.Prepare 负责合法值校验。
func OpenaiFastPolicySettingsFromDTO(s *gatewaydto.OpenAIFastPolicySettings) *tierpolicy.OpenAIFastPolicySettings {
	if s == nil {
		return nil
	}
	rules := make([]tierpolicy.OpenAIFastPolicyRule, len(s.Rules))
	for i, r := range s.Rules {
		rules[i] = tierpolicy.OpenAIFastPolicyRule(r)
		tier := strings.ToLower(strings.TrimSpace(rules[i].ServiceTier))
		if tier == "" {
			tier = tierpolicy.OpenAIFastTierAny
		}
		rules[i].ServiceTier = tier
	}
	return &tierpolicy.OpenAIFastPolicySettings{Rules: rules}
}

func loginAgreementDocumentsToDTO(items []site.LoginAgreementDocument) []sitedto.LoginAgreementDocument {
	result := make([]sitedto.LoginAgreementDocument, 0, len(items))
	for _, item := range items {
		result = append(result, sitedto.LoginAgreementDocument{
			ID:        item.ID,
			Title:     item.Title,
			ContentMD: item.ContentMD,
		})
	}
	return result
}

func loginAgreementDocumentsToService(items []sitedto.LoginAgreementDocument) []site.LoginAgreementDocument {
	result := make([]site.LoginAgreementDocument, 0, len(items))
	for _, item := range items {
		title := strings.TrimSpace(item.Title)
		content := strings.TrimSpace(item.ContentMD)
		if title == "" && content == "" {
			continue
		}
		result = append(result, site.LoginAgreementDocument{
			ID:        strings.TrimSpace(item.ID),
			Title:     title,
			ContentMD: content,
		})
	}
	return result
}

func systemSettingsResponseData(settings settingsdto.SystemSettings, authSourceDefaults *identity.AuthSourceDefaultSettings) map[string]any {
	if settings.PaymentMethodFees == nil {
		settings.PaymentMethodFees = payment.MethodFeeSettings{}
	}

	data := make(map[string]any)
	raw, err := json.Marshal(settings)
	if err == nil {
		_ = json.Unmarshal(raw, &data)
	}
	if authSourceDefaults == nil {
		authSourceDefaults = &identity.AuthSourceDefaultSettings{}
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
