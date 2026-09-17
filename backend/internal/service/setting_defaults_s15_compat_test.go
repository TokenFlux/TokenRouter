package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"

	xai "github.com/TokenFlux/TokenRouter/internal/upstream/grok"
)

// 以下初始化入口仅供历史配置断言保留，生产初始化由 app/bootstrap 拥有。
// InitializeDefaultSettings 初始化默认设置
func (s *SettingService) InitializeDefaultSettings(ctx context.Context) error {
	// 检查是否已有设置
	_, err := s.settingRepo.GetValue(ctx, SettingKeyRegistrationEnabled)
	if err == nil {
		// 已有设置，不需要初始化
		return nil
	}
	if !errors.Is(err, ErrSettingNotFound) {
		return fmt.Errorf("check existing settings: %w", err)
	}

	oidcUsePKCEDefault := true
	oidcValidateIDTokenDefault := true
	if s != nil && s.cfg != nil {
		if s.cfg.OIDC.UsePKCEExplicit {
			oidcUsePKCEDefault = s.cfg.OIDC.UsePKCE
		}
		if s.cfg.OIDC.ValidateIDTokenExplicit {
			oidcValidateIDTokenDefault = s.cfg.OIDC.ValidateIDToken
		}
	}
	loginAgreementDocumentsJSON, err := marshalLoginAgreementDocuments(defaultLoginAgreementDocuments())
	if err != nil {
		return err
	}
	forwardedClientIPHeaders := []string{}
	if s != nil && s.cfg != nil {
		forwardedClientIPHeaders = s.cfg.ForwardedClientIPSettings().Headers
	}
	forwardedClientIPHeadersJSON, err := json.Marshal(forwardedClientIPHeaders)
	if err != nil {
		return fmt.Errorf("marshal default forwarded client IP headers: %w", err)
	}

	// 初始化默认设置
	defaults := map[string]string{
		SettingKeyRegistrationEnabled:                       "true",
		SettingKeyEmailVerifyEnabled:                        "false",
		SettingKeyRegistrationEmailSuffixWhitelist:          "[]",
		SettingKeyRegistrationEmailNormalization:            "false",
		SettingKeyRegistrationEmailDomainQuotaEnabled:       "false",
		SettingKeyUserEmailChangeEnabled:                    "false",
		SettingKeyPromoCodeEnabled:                          "true", // 默认启用优惠码功能
		SettingKeyAffiliateEnabled:                          strconv.FormatBool(AffiliateEnabledDefault),
		SettingKeyAffiliateRebateRate:                       strconv.FormatFloat(AffiliateRebateRateDefault, 'f', 8, 64),
		SettingKeyAffiliateRebateFreezeHours:                strconv.Itoa(AffiliateRebateFreezeHoursDefault),
		SettingKeyAffiliateRebateDurationDays:               strconv.Itoa(AffiliateRebateDurationDaysDefault),
		SettingKeyAffiliateRebatePerInviteeCap:              strconv.FormatFloat(AffiliateRebatePerInviteeCapDefault, 'f', 8, 64),
		SettingKeyAffiliateAdminRechargeEnabled:             strconv.FormatBool(AdminRechargeRebateEnabledDefault),
		SettingKeyLoginAgreementEnabled:                     "false",
		SettingKeyLoginAgreementMode:                        defaultLoginAgreementMode,
		SettingKeyLoginAgreementUpdatedAt:                   defaultLoginAgreementDate,
		SettingKeyLoginAgreementDocuments:                   loginAgreementDocumentsJSON,
		SettingKeyAPIKeyACLTrustForwardedIP:                 "true",
		SettingKeyForwardedClientIPHeaders:                  string(forwardedClientIPHeadersJSON),
		settingKeyForwardedClientIPModeV2:                   "true",
		SettingKeySiteName:                                  "Sub2API",
		SettingKeySiteLogo:                                  "",
		SettingKeySiteNameZh:                                "",
		SettingKeySiteNameEn:                                "",
		SettingKeySiteTitleZh:                               "",
		SettingKeySiteTitleEn:                               "",
		SettingKeySiteSubtitleZh:                            "",
		SettingKeySiteSubtitleEn:                            "",
		SettingKeyPurchaseSubscriptionEnabled:               "false",
		SettingKeyPurchaseSubscriptionURL:                   "",
		SettingKeyTableDefaultPageSize:                      "20",
		SettingKeyTablePageSizeOptions:                      "[10,20,50,100]",
		SettingKeyUsageRankingLimit:                         strconv.Itoa(DefaultUsageRankingLimit),
		SettingKeyUsageRankingEnabled:                       "true",
		SettingKeyUsageRankingSortBy:                        string(UsageRankingSortByTotalTokens),
		SettingKeyUsageRankingShowTotalTokens:               "true",
		SettingKeyUsageRankingShowRequests:                  "true",
		SettingKeyUsageRankingShowActualCost:                "true",
		SettingKeyCustomMenuItems:                           "[]",
		SettingKeyCustomEndpoints:                           "[]",
		SettingKeyFooterLinks:                               "[]",
		SettingKeyFooterText:                                "",
		SettingKeyHomeFeaturedModels:                        "[]",
		SettingKeyCreativeModelSettings:                     "[]",
		SettingKeyCreativeWorkerCount:                       strconv.Itoa(DefaultCreativeWorkerCount),
		SettingKeyWeChatConnectEnabled:                      "false",
		SettingKeyWeChatConnectAppID:                        "",
		SettingKeyWeChatConnectAppSecret:                    "",
		SettingKeyWeChatConnectOpenAppID:                    "",
		SettingKeyWeChatConnectOpenAppSecret:                "",
		SettingKeyWeChatConnectMPAppID:                      "",
		SettingKeyWeChatConnectMPAppSecret:                  "",
		SettingKeyWeChatConnectMobileAppID:                  "",
		SettingKeyWeChatConnectMobileAppSecret:              "",
		SettingKeyWeChatConnectOpenEnabled:                  "false",
		SettingKeyWeChatConnectMPEnabled:                    "false",
		SettingKeyWeChatConnectMobileEnabled:                "false",
		SettingKeyWeChatConnectMode:                         "open",
		SettingKeyWeChatConnectScopes:                       "snsapi_login",
		SettingKeyWeChatConnectRedirectURL:                  "",
		SettingKeyWeChatConnectFrontendRedirectURL:          defaultWeChatConnectFrontend,
		SettingKeyGitHubOAuthEnabled:                        "false",
		SettingKeyGitHubOAuthClientID:                       "",
		SettingKeyGitHubOAuthClientSecret:                   "",
		SettingKeyGitHubOAuthRedirectURL:                    "",
		SettingKeyGitHubOAuthFrontendRedirectURL:            defaultGitHubOAuthFrontend,
		SettingKeyGoogleOAuthEnabled:                        "false",
		SettingKeyGoogleOneTapEnabled:                       "false",
		SettingKeyGoogleOAuthClientID:                       "",
		SettingKeyGoogleOAuthClientSecret:                   "",
		SettingKeyGoogleOAuthRedirectURL:                    "",
		SettingKeyGoogleOAuthFrontendRedirectURL:            defaultGoogleOAuthFrontend,
		SettingKeyOIDCConnectEnabled:                        "false",
		SettingKeyOIDCConnectProviderName:                   "OIDC",
		SettingKeyOIDCConnectClientID:                       "",
		SettingKeyOIDCConnectClientSecret:                   "",
		SettingKeyOIDCConnectIssuerURL:                      "",
		SettingKeyOIDCConnectDiscoveryURL:                   "",
		SettingKeyOIDCConnectAuthorizeURL:                   "",
		SettingKeyOIDCConnectTokenURL:                       "",
		SettingKeyOIDCConnectUserInfoURL:                    "",
		SettingKeyOIDCConnectJWKSURL:                        "",
		SettingKeyOIDCConnectScopes:                         "openid email profile",
		SettingKeyOIDCConnectRedirectURL:                    "",
		SettingKeyOIDCConnectFrontendRedirectURL:            "/auth/oidc/callback",
		SettingKeyOIDCConnectTokenAuthMethod:                "client_secret_post",
		SettingKeyOIDCConnectUsePKCE:                        strconv.FormatBool(oidcUsePKCEDefault),
		SettingKeyOIDCConnectValidateIDToken:                strconv.FormatBool(oidcValidateIDTokenDefault),
		SettingKeyOIDCConnectAllowedSigningAlgs:             "RS256,ES256,PS256",
		SettingKeyOIDCConnectClockSkewSeconds:               "120",
		SettingKeyOIDCConnectRequireEmailVerified:           "false",
		SettingKeyOIDCConnectUserInfoEmailPath:              "",
		SettingKeyOIDCConnectUserInfoIDPath:                 "",
		SettingKeyOIDCConnectUserInfoUsernamePath:           "",
		SettingKeyDefaultConcurrency:                        strconv.Itoa(s.cfg.Default.UserConcurrency),
		SettingKeyDefaultBalance:                            strconv.FormatFloat(s.cfg.Default.UserBalance, 'f', 8, 64),
		SettingKeyDefaultUserRPMLimit:                       "0",
		SettingKeyDefaultUserAPIKeyLimit:                    strconv.Itoa(DefaultUserAPIKeyLimit),
		SettingKeyDefaultSubscriptions:                      "[]",
		SettingKeyBalanceUnitName:                           "USD",
		SettingKeyBalanceUnitSymbol:                         "$",
		SettingKeyBalanceIconSVG:                            "",
		SettingKeyReasoningPointRMBUnitPrice:                "0",
		SettingKeyUSDExchangeRate:                           "0",
		SettingKeyMarketplaceAvailabilityWindowDays:         strconv.Itoa(DefaultMarketplaceAvailabilityWindowDays),
		SettingKeyMarketplaceAvailabilityBucketMinutes:      strconv.Itoa(DefaultMarketplaceAvailabilityBucketMinutes),
		SettingKeyAuthSourceDefaultEmailBalance:             "0",
		SettingKeyAuthSourceDefaultEmailConcurrency:         "5",
		SettingKeyAuthSourceDefaultEmailSubscriptions:       "[]",
		SettingKeyAuthSourceDefaultEmailGrantOnSignup:       "false",
		SettingKeyAuthSourceDefaultEmailGrantOnFirstBind:    "false",
		SettingKeyAuthSourceDefaultLinuxDoBalance:           "0",
		SettingKeyAuthSourceDefaultLinuxDoConcurrency:       "5",
		SettingKeyAuthSourceDefaultLinuxDoSubscriptions:     "[]",
		SettingKeyAuthSourceDefaultLinuxDoGrantOnSignup:     "false",
		SettingKeyAuthSourceDefaultLinuxDoGrantOnFirstBind:  "false",
		SettingKeyAuthSourceDefaultOIDCBalance:              "0",
		SettingKeyAuthSourceDefaultOIDCConcurrency:          "5",
		SettingKeyAuthSourceDefaultOIDCSubscriptions:        "[]",
		SettingKeyAuthSourceDefaultOIDCGrantOnSignup:        "false",
		SettingKeyAuthSourceDefaultOIDCGrantOnFirstBind:     "false",
		SettingKeyAuthSourceDefaultWeChatBalance:            "0",
		SettingKeyAuthSourceDefaultWeChatConcurrency:        "5",
		SettingKeyAuthSourceDefaultWeChatSubscriptions:      "[]",
		SettingKeyAuthSourceDefaultWeChatGrantOnSignup:      "false",
		SettingKeyAuthSourceDefaultWeChatGrantOnFirstBind:   "false",
		SettingKeyAuthSourceDefaultGitHubBalance:            "0",
		SettingKeyAuthSourceDefaultGitHubConcurrency:        "5",
		SettingKeyAuthSourceDefaultGitHubSubscriptions:      "[]",
		SettingKeyAuthSourceDefaultGitHubGrantOnSignup:      "false",
		SettingKeyAuthSourceDefaultGitHubGrantOnFirstBind:   "false",
		SettingKeyAuthSourceDefaultGoogleBalance:            "0",
		SettingKeyAuthSourceDefaultGoogleConcurrency:        "5",
		SettingKeyAuthSourceDefaultGoogleSubscriptions:      "[]",
		SettingKeyAuthSourceDefaultGoogleGrantOnSignup:      "false",
		SettingKeyAuthSourceDefaultGoogleGrantOnFirstBind:   "false",
		SettingKeyAuthSourceDefaultDingTalkBalance:          "0",
		SettingKeyAuthSourceDefaultDingTalkConcurrency:      "5",
		SettingKeyAuthSourceDefaultDingTalkSubscriptions:    "[]",
		SettingKeyAuthSourceDefaultDingTalkGrantOnSignup:    "false",
		SettingKeyAuthSourceDefaultDingTalkGrantOnFirstBind: "false",
		SettingKeyForceEmailOnThirdPartySignup:              "false",
		SettingKeySMTPPort:                                  "587",
		SettingKeySMTPUseTLS:                                "false",
		// 模型回退默认值
		SettingKeyEnableModelFallback:      "false",
		SettingKeyFallbackModelAnthropic:   "claude-3-5-sonnet-20241022",
		SettingKeyFallbackModelOpenAI:      "gpt-4o",
		SettingKeyFallbackModelGemini:      "gemini-2.5-pro",
		SettingKeyFallbackModelAntigravity: "gemini-2.5-pro",
		// Identity patch defaults
		SettingKeyEnableIdentityPatch: "true",
		SettingKeyIdentityPatchPrompt: "",

		// Grok 模型映射和默认上游策略。
		SettingKeyGrokDefaultTextModel:           xai.DefaultTextModel,
		SettingKeyGrokCrossClientModelMapEnabled: "true",
		SettingKeyGrokDefaultBaseURLMode:         GrokDefaultBaseURLModeCLI,

		// Ops monitoring defaults (vNext)
		SettingKeyOpsMonitoringEnabled:         "true",
		SettingKeyOpsRealtimeMonitoringEnabled: "true",
		SettingKeyOpsMetricsIntervalSeconds:    "60",

		// Claude Code version check (default: empty = disabled)
		SettingKeyMinClaudeCodeVersion: "",
		SettingKeyMaxClaudeCodeVersion: "",

		// 分组隔离（默认不允许未分组 Key 调度）
		SettingKeyAllowUngroupedKeyScheduling:                  "false",
		SettingKeyEnableAnthropicCacheTTL1hInjection:           "false",
		SettingKeyRewriteMessageCacheControl:                   strconv.FormatBool(s.defaultRewriteMessageCacheControl()),
		SettingKeyEnableClientDatelineNormalization:            "true",
		SettingKeyAntigravityUserAgentVersion:                  "",
		SettingKeyOpenAICodexUserAgent:                         "",
		SettingKeyUserPromptReplacementConfig:                  defaultUserPromptReplacementConfigJSON(),
		SettingPaymentVisibleMethodAlipaySource:                "",
		SettingPaymentVisibleMethodWxpaySource:                 "",
		SettingPaymentVisibleMethodAlipayEnabled:               "false",
		SettingPaymentVisibleMethodWxpayEnabled:                "false",
		SettingKeyAdvancedSchedulerStickyWeightedEnabled:       "false",
		SettingKeyAdvancedSchedulerSubscriptionPriorityEnabled: "false",
		SettingKeyAdvancedSchedulerEWMAErrorRateAlpha:          "",
		SettingKeyAdvancedSchedulerEWMATTFTAlpha:               "",
		SettingKeyAdvancedSchedulerStickyEscapeEnabled:         "",
		SettingKeyAdvancedSchedulerStickyEscapeTTFTMs:          "",
		SettingKeyAdvancedSchedulerStickyEscapeErrorRate:       "",
		SettingKeyAdvancedSchedulerLBTopK:                      "",
		SettingKeyAdvancedSchedulerWeightPriority:              "",
		SettingKeyAdvancedSchedulerWeightLoad:                  "",
		SettingKeyAdvancedSchedulerWeightQueue:                 "",
		SettingKeyAdvancedSchedulerWeightErrorRate:             "",
		SettingKeyAdvancedSchedulerWeightTTFT:                  "",
		SettingKeyAdvancedSchedulerWeightReset:                 "",
		SettingKeyAdvancedSchedulerWeightQuotaHeadroom:         "",
		SettingKeyAdvancedSchedulerWeightPreviousResponse:      "",
		SettingKeyAdvancedSchedulerWeightSessionSticky:         "",

		// 页面功能开关默认开启，保持升级前已有功能的可见性。
		SettingKeyTeamEnabled:     "true",
		SettingKeyCreativeEnabled: "true",

		// 风控中心默认关闭，避免升级后未配置审计 Key 时影响现有请求。
		SettingKeyRiskControlEnabled:          "false",
		SettingKeyCyberSessionBlockEnabled:    "false",
		SettingKeyCyberSessionBlockTTLSeconds: "3600",
		SettingKeyAllowUserViewErrorRequests:  "false",
	}

	return s.settingRepo.SetMultiple(ctx, defaults)
}
