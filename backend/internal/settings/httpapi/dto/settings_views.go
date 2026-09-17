package dto

import (
	billinghttp "github.com/TokenFlux/TokenRouter/internal/billing/httpapi"
	creative "github.com/TokenFlux/TokenRouter/internal/creative"
	gatewaydto "github.com/TokenFlux/TokenRouter/internal/gateway/httpapi/dto"
	promptpolicy "github.com/TokenFlux/TokenRouter/internal/gateway/promptpolicy"
	identity "github.com/TokenFlux/TokenRouter/internal/identity"
	identitydto "github.com/TokenFlux/TokenRouter/internal/identity/httpapi/dto"
	ops "github.com/TokenFlux/TokenRouter/internal/ops"
	payment "github.com/TokenFlux/TokenRouter/internal/payment"
	sitedto "github.com/TokenFlux/TokenRouter/internal/site/httpapi/dto"
)

// SystemSettings 保留现有 HTTP JSON 字段与省略语义。
type SystemSettings struct {
	RegistrationEnabled                 bool                             `json:"registration_enabled"`
	EmailVerifyEnabled                  bool                             `json:"email_verify_enabled"`
	RegistrationEmailSuffixWhitelist    []string                         `json:"registration_email_suffix_whitelist"`
	RegistrationEmailNormalization      bool                             `json:"registration_email_normalization"`
	RegistrationEmailDomainQuotaEnabled bool                             `json:"registration_email_domain_quota_enabled"`
	UserEmailChangeEnabled              bool                             `json:"user_email_change_enabled"` // 是否允许已有邮箱的用户换绑主邮箱
	PromoCodeEnabled                    bool                             `json:"promo_code_enabled"`
	PasswordResetEnabled                bool                             `json:"password_reset_enabled"`
	FrontendURL                         string                           `json:"frontend_url"`
	InvitationCodeEnabled               bool                             `json:"invitation_code_enabled"`
	TotpEnabled                         bool                             `json:"totp_enabled"`                   // TOTP 双因素认证
	TotpEncryptionKeyConfigured         bool                             `json:"totp_encryption_key_configured"` // TOTP 加密密钥是否已配置
	SessionBindingEnabled               bool                             `json:"session_binding_enabled"`        // 会话 IP/UA 绑定
	StepUpEnabled                       bool                             `json:"step_up_enabled"`                // 敏感操作 step-up 2FA
	AuditLogRetentionDays               int                              `json:"audit_log_retention_days"`       // 审计日志保留天数
	LoginAgreementEnabled               bool                             `json:"login_agreement_enabled"`
	LoginAgreementMode                  string                           `json:"login_agreement_mode"`
	LoginAgreementUpdatedAt             string                           `json:"login_agreement_updated_at"`
	LoginAgreementDocuments             []sitedto.LoginAgreementDocument `json:"login_agreement_documents"`

	SMTPHost               string `json:"smtp_host"`
	SMTPPort               int    `json:"smtp_port"`
	SMTPUsername           string `json:"smtp_username"`
	SMTPPasswordConfigured bool   `json:"smtp_password_configured"`
	SMTPFrom               string `json:"smtp_from_email"`
	SMTPFromName           string `json:"smtp_from_name"`
	SMTPUseTLS             bool   `json:"smtp_use_tls"`

	TurnstileEnabled                       bool     `json:"turnstile_enabled"`
	TurnstileSiteKey                       string   `json:"turnstile_site_key"`
	TurnstileSecretKeyConfigured           bool     `json:"turnstile_secret_key_configured"`
	TencentCaptchaEnabled                  bool     `json:"tencent_captcha_enabled"`
	TencentCaptchaAppID                    string   `json:"tencent_captcha_app_id"`
	TencentCaptchaAppSecretKeyConfigured   bool     `json:"tencent_captcha_app_secret_key_configured"`
	TencentCaptchaCloudSecretIDConfigured  bool     `json:"tencent_captcha_cloud_secret_id_configured"`
	TencentCaptchaCloudSecretKeyConfigured bool     `json:"tencent_captcha_cloud_secret_key_configured"`
	TencentCaptchaRegion                   string   `json:"tencent_captcha_region"`
	AliyunCaptchaEnabled                   bool     `json:"aliyun_captcha_enabled"`
	AliyunCaptchaAccessKeyID               string   `json:"aliyun_captcha_access_key_id"`
	AliyunCaptchaAccessKeySecretConfigured bool     `json:"aliyun_captcha_access_key_secret_configured"`
	AliyunCaptchaSceneID                   string   `json:"aliyun_captcha_scene_id"`
	AliyunCaptchaPrefix                    string   `json:"aliyun_captcha_prefix"`
	AliyunCaptchaRegion                    string   `json:"aliyun_captcha_region"`
	APIKeyACLTrustForwardedIP              bool     `json:"api_key_acl_trust_forwarded_ip"`
	ForwardedClientIPHeaders               []string `json:"forwarded_client_ip_headers"`

	LinuxDoConnectEnabled                bool   `json:"linuxdo_connect_enabled"`
	LinuxDoConnectClientID               string `json:"linuxdo_connect_client_id"`
	LinuxDoConnectClientSecretConfigured bool   `json:"linuxdo_connect_client_secret_configured"`
	LinuxDoConnectRedirectURL            string `json:"linuxdo_connect_redirect_url"`

	DingTalkConnectEnabled                 bool   `json:"dingtalk_connect_enabled"`
	DingTalkConnectClientID                string `json:"dingtalk_connect_client_id"`
	DingTalkConnectClientSecretConfigured  bool   `json:"dingtalk_connect_client_secret_configured"`
	DingTalkConnectRedirectURL             string `json:"dingtalk_connect_redirect_url"`
	DingTalkConnectCorpRestrictionPolicy   string `json:"dingtalk_connect_corp_restriction_policy"`
	DingTalkConnectInternalCorpID          string `json:"dingtalk_connect_internal_corp_id"`
	DingTalkConnectBypassRegistration      bool   `json:"dingtalk_connect_bypass_registration"`
	DingTalkConnectSyncCorpEmail           bool   `json:"dingtalk_connect_sync_corp_email"`
	DingTalkConnectSyncDisplayName         bool   `json:"dingtalk_connect_sync_display_name"`
	DingTalkConnectSyncDept                bool   `json:"dingtalk_connect_sync_dept"`
	DingTalkConnectSyncCorpEmailAttrKey    string `json:"dingtalk_connect_sync_corp_email_attr_key"`
	DingTalkConnectSyncDisplayNameAttrKey  string `json:"dingtalk_connect_sync_display_name_attr_key"`
	DingTalkConnectSyncDeptAttrKey         string `json:"dingtalk_connect_sync_dept_attr_key"`
	DingTalkConnectSyncCorpEmailAttrName   string `json:"dingtalk_connect_sync_corp_email_attr_name"`
	DingTalkConnectSyncDisplayNameAttrName string `json:"dingtalk_connect_sync_display_name_attr_name"`
	DingTalkConnectSyncDeptAttrName        string `json:"dingtalk_connect_sync_dept_attr_name"`

	WeChatConnectEnabled                   bool   `json:"wechat_connect_enabled"`
	WeChatConnectAppID                     string `json:"wechat_connect_app_id"`
	WeChatConnectAppSecretConfigured       bool   `json:"wechat_connect_app_secret_configured"`
	WeChatConnectOpenAppID                 string `json:"wechat_connect_open_app_id"`
	WeChatConnectOpenAppSecretConfigured   bool   `json:"wechat_connect_open_app_secret_configured"`
	WeChatConnectMPAppID                   string `json:"wechat_connect_mp_app_id"`
	WeChatConnectMPAppSecretConfigured     bool   `json:"wechat_connect_mp_app_secret_configured"`
	WeChatConnectMobileAppID               string `json:"wechat_connect_mobile_app_id"`
	WeChatConnectMobileAppSecretConfigured bool   `json:"wechat_connect_mobile_app_secret_configured"`
	WeChatConnectOpenEnabled               bool   `json:"wechat_connect_open_enabled"`
	WeChatConnectMPEnabled                 bool   `json:"wechat_connect_mp_enabled"`
	WeChatConnectMobileEnabled             bool   `json:"wechat_connect_mobile_enabled"`
	WeChatConnectMode                      string `json:"wechat_connect_mode"`
	WeChatConnectScopes                    string `json:"wechat_connect_scopes"`
	WeChatConnectRedirectURL               string `json:"wechat_connect_redirect_url"`
	WeChatConnectFrontendRedirectURL       string `json:"wechat_connect_frontend_redirect_url"`

	OIDCConnectEnabled                bool   `json:"oidc_connect_enabled"`
	OIDCConnectProviderName           string `json:"oidc_connect_provider_name"`
	OIDCConnectClientID               string `json:"oidc_connect_client_id"`
	OIDCConnectClientSecretConfigured bool   `json:"oidc_connect_client_secret_configured"`
	OIDCConnectIssuerURL              string `json:"oidc_connect_issuer_url"`
	OIDCConnectDiscoveryURL           string `json:"oidc_connect_discovery_url"`
	OIDCConnectAuthorizeURL           string `json:"oidc_connect_authorize_url"`
	OIDCConnectTokenURL               string `json:"oidc_connect_token_url"`
	OIDCConnectUserInfoURL            string `json:"oidc_connect_userinfo_url"`
	OIDCConnectJWKSURL                string `json:"oidc_connect_jwks_url"`
	OIDCConnectScopes                 string `json:"oidc_connect_scopes"`
	OIDCConnectRedirectURL            string `json:"oidc_connect_redirect_url"`
	OIDCConnectFrontendRedirectURL    string `json:"oidc_connect_frontend_redirect_url"`
	OIDCConnectTokenAuthMethod        string `json:"oidc_connect_token_auth_method"`
	OIDCConnectUsePKCE                bool   `json:"oidc_connect_use_pkce"`
	OIDCConnectValidateIDToken        bool   `json:"oidc_connect_validate_id_token"`
	OIDCConnectAllowedSigningAlgs     string `json:"oidc_connect_allowed_signing_algs"`
	OIDCConnectClockSkewSeconds       int    `json:"oidc_connect_clock_skew_seconds"`
	OIDCConnectRequireEmailVerified   bool   `json:"oidc_connect_require_email_verified"`
	OIDCConnectUserInfoEmailPath      string `json:"oidc_connect_userinfo_email_path"`
	OIDCConnectUserInfoIDPath         string `json:"oidc_connect_userinfo_id_path"`
	OIDCConnectUserInfoUsernamePath   string `json:"oidc_connect_userinfo_username_path"`

	GitHubOAuthEnabled                bool   `json:"github_oauth_enabled"`
	GitHubOAuthClientID               string `json:"github_oauth_client_id"`
	GitHubOAuthClientSecretConfigured bool   `json:"github_oauth_client_secret_configured"`
	GitHubOAuthRedirectURL            string `json:"github_oauth_redirect_url"`
	GitHubOAuthFrontendRedirectURL    string `json:"github_oauth_frontend_redirect_url"`
	GoogleOAuthEnabled                bool   `json:"google_oauth_enabled"`
	GoogleOneTapEnabled               bool   `json:"google_one_tap_enabled"`
	GoogleOAuthClientID               string `json:"google_oauth_client_id"`
	GoogleOAuthClientSecretConfigured bool   `json:"google_oauth_client_secret_configured"`
	GoogleOAuthRedirectURL            string `json:"google_oauth_redirect_url"`
	GoogleOAuthFrontendRedirectURL    string `json:"google_oauth_frontend_redirect_url"`

	SiteName                    string                          `json:"site_name"`
	SiteLogo                    string                          `json:"site_logo"`
	SiteSubtitle                string                          `json:"site_subtitle"`
	SiteNameZh                  string                          `json:"site_name_zh"`
	SiteNameEn                  string                          `json:"site_name_en"`
	SiteTitleZh                 string                          `json:"site_title_zh"`
	SiteTitleEn                 string                          `json:"site_title_en"`
	SiteSubtitleZh              string                          `json:"site_subtitle_zh"`
	SiteSubtitleEn              string                          `json:"site_subtitle_en"`
	APIBaseURL                  string                          `json:"api_base_url"`
	ContactInfo                 string                          `json:"contact_info"`
	DocURL                      string                          `json:"doc_url"`
	HomeContent                 string                          `json:"home_content"`
	HideCcsImportButton         bool                            `json:"hide_ccs_import_button"`
	PurchaseSubscriptionEnabled bool                            `json:"purchase_subscription_enabled"`
	PurchaseSubscriptionURL     string                          `json:"purchase_subscription_url"`
	TableDefaultPageSize        int                             `json:"table_default_page_size"`
	TablePageSizeOptions        []int                           `json:"table_page_size_options"`
	UsageRankingLimit           int                             `json:"usage_ranking_limit"`
	UsageRankingEnabled         bool                            `json:"usage_ranking_enabled"`
	UsageRankingSortBy          string                          `json:"usage_ranking_sort_by"`
	UsageRankingShowTotalTokens bool                            `json:"usage_ranking_show_total_tokens"`
	UsageRankingShowRequests    bool                            `json:"usage_ranking_show_requests"`
	UsageRankingShowActualCost  bool                            `json:"usage_ranking_show_actual_cost"`
	CustomMenuItems             []sitedto.CustomMenuItem        `json:"custom_menu_items"`
	CustomEndpoints             []sitedto.CustomEndpoint        `json:"custom_endpoints"`
	FooterLinks                 []sitedto.FooterLinkGroup       `json:"footer_links"`
	FooterText                  string                          `json:"footer_text"`
	HomeFeaturedModels          []string                        `json:"home_featured_models"`
	CreativeModelSettings       []creative.CreativeModelSetting `json:"creative_model_settings"`
	CreativeWorkerCount         int                             `json:"creative_worker_count"`

	DefaultConcurrency                   int                                      `json:"default_concurrency"`
	DefaultBalance                       float64                                  `json:"default_balance"`
	TeamEnabled                          bool                                     `json:"team_enabled"`         // 团队功能页面开关
	CreativeEnabled                      bool                                     `json:"creative_enabled"`     // 创作台功能开关
	RiskControlEnabled                   bool                                     `json:"risk_control_enabled"` // 风控中心功能开关
	CyberSessionBlockEnabled             bool                                     `json:"cyber_session_block_enabled"`
	CyberSessionBlockTTLSeconds          int                                      `json:"cyber_session_block_ttl_seconds"`
	AffiliateEnabled                     bool                                     `json:"affiliate_enabled"`
	AffiliateRebateRate                  float64                                  `json:"affiliate_rebate_rate"`
	AffiliateRebateFreezeHours           int                                      `json:"affiliate_rebate_freeze_hours"`
	AffiliateRebateDurationDays          int                                      `json:"affiliate_rebate_duration_days"`
	AffiliateRebatePerInviteeCap         float64                                  `json:"affiliate_rebate_per_invitee_cap"`
	AdminRechargeRebateEnabled           bool                                     `json:"affiliate_admin_recharge_enabled"`
	DefaultUserRPMLimit                  int                                      `json:"default_user_rpm_limit"`
	DefaultUserAPIKeyLimit               int                                      `json:"default_user_api_key_limit"`
	DefaultSubscriptions                 []billinghttp.DefaultSubscriptionSetting `json:"default_subscriptions"`
	BalanceUnitName                      string                                   `json:"balance_unit_name"`
	BalanceUnitSymbol                    string                                   `json:"balance_unit_symbol"`
	BalanceIconSVG                       string                                   `json:"balance_icon_svg"`
	ReasoningPointRMBUnitPrice           float64                                  `json:"reasoning_point_rmb_unit_price"`
	USDExchangeRate                      float64                                  `json:"usd_exchange_rate"`
	MarketplaceAvailabilityWindowDays    int                                      `json:"marketplace_availability_window_days"`
	MarketplaceAvailabilityBucketMinutes int                                      `json:"marketplace_availability_bucket_minutes"`

	// Model fallback configuration
	EnableModelFallback      bool   `json:"enable_model_fallback"`
	FallbackModelAnthropic   string `json:"fallback_model_anthropic"`
	FallbackModelOpenAI      string `json:"fallback_model_openai"`
	FallbackModelGemini      string `json:"fallback_model_gemini"`
	FallbackModelAntigravity string `json:"fallback_model_antigravity"`

	// Grok 模型映射策略；账号映射为空时使用这里的默认值。
	GrokDefaultTextModel           string `json:"grok_default_text_model"`
	GrokCrossClientModelMapEnabled bool   `json:"grok_cross_client_model_map_enabled"`
	GrokDefaultBaseURLMode         string `json:"grok_default_base_url_mode"`

	// Identity patch configuration (Claude -> Gemini)
	EnableIdentityPatch bool   `json:"enable_identity_patch"`
	IdentityPatchPrompt string `json:"identity_patch_prompt"`

	// Ops monitoring (vNext)
	OpsMonitoringEnabled         bool `json:"ops_monitoring_enabled"`
	OpsRealtimeMonitoringEnabled bool `json:"ops_realtime_monitoring_enabled"`
	OpsMetricsIntervalSeconds    int  `json:"ops_metrics_interval_seconds"`

	MinClaudeCodeVersion string `json:"min_claude_code_version"`
	MaxClaudeCodeVersion string `json:"max_claude_code_version"`

	// 分组隔离
	AllowUngroupedKeyScheduling bool `json:"allow_ungrouped_key_scheduling"`

	// Backend Mode
	BackendModeEnabled bool `json:"backend_mode_enabled"`

	// 网关转发行为
	OpenAITTFTMode                         string                                    `json:"openai_ttft_mode"`
	EnableFingerprintUnification           bool                                      `json:"enable_fingerprint_unification"`
	EnableMetadataPassthrough              bool                                      `json:"enable_metadata_passthrough"`
	EnableCCHSigning                       bool                                      `json:"enable_cch_signing"`
	EnableClaudeOAuthSystemPromptInjection bool                                      `json:"enable_claude_oauth_system_prompt_injection"`
	ClaudeOAuthSystemPrompt                string                                    `json:"claude_oauth_system_prompt"`
	ClaudeOAuthSystemPromptBlocks          string                                    `json:"claude_oauth_system_prompt_blocks"`
	EnableAnthropicCacheTTL1hInjection     bool                                      `json:"enable_anthropic_cache_ttl_1h_injection"`
	RewriteMessageCacheControl             bool                                      `json:"rewrite_message_cache_control"`
	EnableClientDatelineNormalization      bool                                      `json:"enable_client_dateline_normalization"`
	AntigravityUserAgentVersion            string                                    `json:"antigravity_user_agent_version"`
	OpenAICodexUserAgent                   string                                    `json:"openai_codex_user_agent"`
	OpenAIAllowClaudeCodeCodexPlugin       bool                                      `json:"openai_allow_claude_code_codex_plugin"`
	UserPromptReplacementConfig            *promptpolicy.UserPromptReplacementConfig `json:"user_prompt_replacement_config"`

	// Web Search Emulation
	WebSearchEmulationEnabled bool `json:"web_search_emulation_enabled"`

	// Payment visible method routing
	PaymentVisibleMethodAlipaySource  string `json:"payment_visible_method_alipay_source"`
	PaymentVisibleMethodWxpaySource   string `json:"payment_visible_method_wxpay_source"`
	PaymentVisibleMethodAlipayEnabled bool   `json:"payment_visible_method_alipay_enabled"`
	PaymentVisibleMethodWxpayEnabled  bool   `json:"payment_visible_method_wxpay_enabled"`

	// 通用高级调度器参数；是否启用由分组 scheduler_type 决定。
	AdvancedSchedulerStickyWeightedEnabled           bool   `json:"advanced_scheduler_sticky_weighted_enabled"`
	AdvancedSchedulerSubscriptionPriorityEnabled     bool   `json:"advanced_scheduler_subscription_priority_enabled"`
	AdvancedSchedulerEWMAErrorRateAlpha              string `json:"advanced_scheduler_ewma_error_rate_alpha"`
	AdvancedSchedulerEWMATTFTAlpha                   string `json:"advanced_scheduler_ewma_ttft_alpha"`
	AdvancedSchedulerStickyEscapeEnabled             bool   `json:"advanced_scheduler_sticky_escape_enabled"`
	AdvancedSchedulerStickyEscapeTTFTMs              string `json:"advanced_scheduler_sticky_escape_ttft_ms"`
	AdvancedSchedulerStickyEscapeErrorRate           string `json:"advanced_scheduler_sticky_escape_error_rate"`
	AdvancedSchedulerLBTopK                          string `json:"advanced_scheduler_lb_top_k"`
	AdvancedSchedulerWeightPriority                  string `json:"advanced_scheduler_weight_priority"`
	AdvancedSchedulerWeightLoad                      string `json:"advanced_scheduler_weight_load"`
	AdvancedSchedulerWeightQueue                     string `json:"advanced_scheduler_weight_queue"`
	AdvancedSchedulerWeightErrorRate                 string `json:"advanced_scheduler_weight_error_rate"`
	AdvancedSchedulerWeightTTFT                      string `json:"advanced_scheduler_weight_ttft"`
	AdvancedSchedulerWeightReset                     string `json:"advanced_scheduler_weight_reset"`
	AdvancedSchedulerWeightQuotaHeadroom             string `json:"advanced_scheduler_weight_quota_headroom"`
	AdvancedSchedulerWeightPreviousResponse          string `json:"advanced_scheduler_weight_previous_response"`
	AdvancedSchedulerWeightSessionSticky             string `json:"advanced_scheduler_weight_session_sticky"`
	AdvancedSchedulerEffectiveLBTopK                 string `json:"advanced_scheduler_effective_lb_top_k"`
	AdvancedSchedulerEffectiveWeightPriority         string `json:"advanced_scheduler_effective_weight_priority"`
	AdvancedSchedulerEffectiveWeightLoad             string `json:"advanced_scheduler_effective_weight_load"`
	AdvancedSchedulerEffectiveWeightQueue            string `json:"advanced_scheduler_effective_weight_queue"`
	AdvancedSchedulerEffectiveWeightErrorRate        string `json:"advanced_scheduler_effective_weight_error_rate"`
	AdvancedSchedulerEffectiveWeightTTFT             string `json:"advanced_scheduler_effective_weight_ttft"`
	AdvancedSchedulerEffectiveWeightReset            string `json:"advanced_scheduler_effective_weight_reset"`
	AdvancedSchedulerEffectiveWeightQuotaHeadroom    string `json:"advanced_scheduler_effective_weight_quota_headroom"`
	AdvancedSchedulerEffectiveWeightPreviousResponse string `json:"advanced_scheduler_effective_weight_previous_response"`
	AdvancedSchedulerEffectiveWeightSessionSticky    string `json:"advanced_scheduler_effective_weight_session_sticky"`
	AdvancedSchedulerEffectiveEWMAErrorRateAlpha     string `json:"advanced_scheduler_effective_ewma_error_rate_alpha"`
	AdvancedSchedulerEffectiveEWMATTFTAlpha          string `json:"advanced_scheduler_effective_ewma_ttft_alpha"`
	AdvancedSchedulerEffectiveStickyEscapeEnabled    bool   `json:"advanced_scheduler_effective_sticky_escape_enabled"`
	AdvancedSchedulerEffectiveStickyEscapeTTFTMs     string `json:"advanced_scheduler_effective_sticky_escape_ttft_ms"`
	AdvancedSchedulerEffectiveStickyEscapeErrorRate  string `json:"advanced_scheduler_effective_sticky_escape_error_rate"`
	// OpenAI 账号配额自动暂停全局默认阈值。后端按 0~1 存储，0 表示不启用全局默认阈值。
	OpenAIQuotaAutoPauseSettings ops.OpsOpenAIAccountQuotaAutoPauseSettings `json:"openai_account_quota_auto_pause"`

	// Payment configuration
	PaymentEnabled                   bool                      `json:"payment_enabled"`
	PaymentMinAmount                 float64                   `json:"payment_min_amount"`
	PaymentMaxAmount                 float64                   `json:"payment_max_amount"`
	PaymentDailyLimit                float64                   `json:"payment_daily_limit"`
	PaymentOrderTimeoutMin           int                       `json:"payment_order_timeout_minutes"`
	PaymentMaxPendingOrders          int                       `json:"payment_max_pending_orders"`
	PaymentEnabledTypes              []string                  `json:"payment_enabled_types"`
	PaymentBalanceDisabled           bool                      `json:"payment_balance_disabled"`
	PaymentBalanceRechargeMultiplier float64                   `json:"payment_balance_recharge_multiplier"`
	PaymentSubscriptionUSDToCNYRate  float64                   `json:"payment_subscription_usd_to_cny_rate"`
	PaymentRechargeFeeRate           float64                   `json:"payment_recharge_fee_rate"`
	PaymentMethodFees                payment.MethodFeeSettings `json:"payment_method_fees"`
	PaymentLoadBalanceStrat          string                    `json:"payment_load_balance_strategy"`
	PaymentProductNamePrefix         string                    `json:"payment_product_name_prefix"`
	PaymentProductNameSuffix         string                    `json:"payment_product_name_suffix"`
	PaymentHelpImageURL              string                    `json:"payment_help_image_url"`
	PaymentHelpText                  string                    `json:"payment_help_text"`

	// Cancel rate limit
	PaymentCancelRateLimitEnabled bool   `json:"payment_cancel_rate_limit_enabled"`
	PaymentCancelRateLimitMax     int    `json:"payment_cancel_rate_limit_max"`
	PaymentCancelRateLimitWindow  int    `json:"payment_cancel_rate_limit_window"`
	PaymentCancelRateLimitUnit    string `json:"payment_cancel_rate_limit_unit"`
	PaymentCancelRateLimitMode    string `json:"payment_cancel_rate_limit_window_mode"`

	// 支付宝移动端强制使用二维码支付，不再跳转手机网站支付。
	PaymentAlipayForceQRCode bool `json:"payment_alipay_force_qrcode"`
	// 移动端使用支付宝当面付预下单，并通过深链接唤起支付宝客户端。
	PaymentAlipayMobilePrecreateDeepLink bool `json:"payment_alipay_mobile_precreate_deep_link"`

	// 余额、订阅到期与账号限额通知
	BalanceLowNotifyEnabled         bool                           `json:"balance_low_notify_enabled"`
	BalanceLowNotifyThreshold       float64                        `json:"balance_low_notify_threshold"`
	BalanceLowNotifyRechargeURL     string                         `json:"balance_low_notify_recharge_url"`
	SubscriptionExpiryNotifyEnabled bool                           `json:"subscription_expiry_notify_enabled"`
	AccountQuotaNotifyEnabled       bool                           `json:"account_quota_notify_enabled"`
	AccountQuotaNotifyEmails        []identitydto.NotifyEmailEntry `json:"account_quota_notify_emails"`

	// OpenAI fast/flex 策略
	OpenAIFastPolicySettings *gatewaydto.OpenAIFastPolicySettings `json:"openai_fast_policy_settings,omitempty"`

	// 系统全局默认平台配额（key = platform，nil/缺省 = 不限制）
	DefaultPlatformQuotas map[string]*identity.DefaultPlatformQuotaSetting `json:"default_platform_quotas,omitempty"`

	// 系统全局账号自动停调阈值（key = platform，100 = disabled）
	AccountSchedulingThresholds map[string]int `json:"account_scheduling_thresholds,omitempty"`

	// 允许终端用户在用量页查看自己的失败请求
	AllowUserViewErrorRequests bool `json:"allow_user_view_error_requests"`
}
