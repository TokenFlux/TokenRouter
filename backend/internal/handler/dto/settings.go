package dto

import (
	"encoding/json"
	"strings"

	"github.com/BrandonVee/TokenRouter/internal/service"
)

// CustomMenuItem represents a user-configured custom menu entry.
type CustomMenuItem struct {
	ID         string `json:"id"`
	Label      string `json:"label"`
	IconSVG    string `json:"icon_svg"`
	URL        string `json:"url"`
	PageSlug   string `json:"page_slug,omitempty"`
	Visibility string `json:"visibility"` // "user" or "admin"
	SortOrder  int    `json:"sort_order"`
}

// CustomEndpoint represents an admin-configured API endpoint for quick copy.
type CustomEndpoint struct {
	Name        string `json:"name"`
	Endpoint    string `json:"endpoint"`
	Description string `json:"description"`
}

// SystemSettings represents the admin settings API response payload.
type SystemSettings struct {
	RegistrationEnabled                 bool                     `json:"registration_enabled"`
	EmailVerifyEnabled                  bool                     `json:"email_verify_enabled"`
	RegistrationEmailSuffixWhitelist    []string                 `json:"registration_email_suffix_whitelist"`
	RegistrationEmailNormalization      bool                     `json:"registration_email_normalization"`
	RegistrationEmailDomainQuotaEnabled bool                     `json:"registration_email_domain_quota_enabled"`
	PromoCodeEnabled                    bool                     `json:"promo_code_enabled"`
	PasswordResetEnabled                bool                     `json:"password_reset_enabled"`
	FrontendURL                         string                   `json:"frontend_url"`
	InvitationCodeEnabled               bool                     `json:"invitation_code_enabled"`
	TotpEnabled                         bool                     `json:"totp_enabled"`                   // TOTP 双因素认证
	TotpEncryptionKeyConfigured         bool                     `json:"totp_encryption_key_configured"` // TOTP 加密密钥是否已配置
	SessionBindingEnabled               bool                     `json:"session_binding_enabled"`        // 会话 IP/UA 绑定
	StepUpEnabled                       bool                     `json:"step_up_enabled"`                // 敏感操作 step-up 2FA
	AuditLogRetentionDays               int                      `json:"audit_log_retention_days"`       // 审计日志保留天数
	LoginAgreementEnabled               bool                     `json:"login_agreement_enabled"`
	LoginAgreementMode                  string                   `json:"login_agreement_mode"`
	LoginAgreementUpdatedAt             string                   `json:"login_agreement_updated_at"`
	LoginAgreementDocuments             []LoginAgreementDocument `json:"login_agreement_documents"`

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

	SiteName                    string            `json:"site_name"`
	SiteLogo                    string            `json:"site_logo"`
	SiteSubtitle                string            `json:"site_subtitle"`
	SiteNameZh                  string            `json:"site_name_zh"`
	SiteNameEn                  string            `json:"site_name_en"`
	SiteTitleZh                 string            `json:"site_title_zh"`
	SiteTitleEn                 string            `json:"site_title_en"`
	SiteSubtitleZh              string            `json:"site_subtitle_zh"`
	SiteSubtitleEn              string            `json:"site_subtitle_en"`
	APIBaseURL                  string            `json:"api_base_url"`
	ContactInfo                 string            `json:"contact_info"`
	DocURL                      string            `json:"doc_url"`
	HomeContent                 string            `json:"home_content"`
	HideCcsImportButton         bool              `json:"hide_ccs_import_button"`
	PurchaseSubscriptionEnabled bool              `json:"purchase_subscription_enabled"`
	PurchaseSubscriptionURL     string            `json:"purchase_subscription_url"`
	TableDefaultPageSize        int               `json:"table_default_page_size"`
	TablePageSizeOptions        []int             `json:"table_page_size_options"`
	UsageRankingLimit           int               `json:"usage_ranking_limit"`
	CustomMenuItems             []CustomMenuItem  `json:"custom_menu_items"`
	CustomEndpoints             []CustomEndpoint  `json:"custom_endpoints"`
	FooterLinks                 []FooterLinkGroup `json:"footer_links"`
	FooterText                  string            `json:"footer_text"`

	DefaultConcurrency                   int                          `json:"default_concurrency"`
	DefaultBalance                       float64                      `json:"default_balance"`
	TeamEnabled                          bool                         `json:"team_enabled"`         // 团队功能页面开关
	DataSharingEnabled                   bool                         `json:"data_sharing_enabled"` // 数据共享页面开关
	RiskControlEnabled                   bool                         `json:"risk_control_enabled"` // 风控中心功能开关
	CyberSessionBlockEnabled             bool                         `json:"cyber_session_block_enabled"`
	CyberSessionBlockTTLSeconds          int                          `json:"cyber_session_block_ttl_seconds"`
	AffiliateEnabled                     bool                         `json:"affiliate_enabled"`
	AffiliateRebateRate                  float64                      `json:"affiliate_rebate_rate"`
	AffiliateRebateFreezeHours           int                          `json:"affiliate_rebate_freeze_hours"`
	AffiliateRebateDurationDays          int                          `json:"affiliate_rebate_duration_days"`
	AffiliateRebatePerInviteeCap         float64                      `json:"affiliate_rebate_per_invitee_cap"`
	AdminRechargeRebateEnabled           bool                         `json:"affiliate_admin_recharge_enabled"`
	DefaultUserRPMLimit                  int                          `json:"default_user_rpm_limit"`
	DefaultUserAPIKeyLimit               int                          `json:"default_user_api_key_limit"`
	DefaultSubscriptions                 []DefaultSubscriptionSetting `json:"default_subscriptions"`
	BalanceUnitName                      string                       `json:"balance_unit_name"`
	BalanceUnitSymbol                    string                       `json:"balance_unit_symbol"`
	BalanceIconSVG                       string                       `json:"balance_icon_svg"`
	ReasoningPointRMBUnitPrice           float64                      `json:"reasoning_point_rmb_unit_price"`
	USDExchangeRate                      float64                      `json:"usd_exchange_rate"`
	MarketplaceAvailabilityWindowDays    int                          `json:"marketplace_availability_window_days"`
	MarketplaceAvailabilityBucketMinutes int                          `json:"marketplace_availability_bucket_minutes"`

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
	EnableFingerprintUnification           bool                                 `json:"enable_fingerprint_unification"`
	EnableMetadataPassthrough              bool                                 `json:"enable_metadata_passthrough"`
	EnableCCHSigning                       bool                                 `json:"enable_cch_signing"`
	EnableClaudeOAuthSystemPromptInjection bool                                 `json:"enable_claude_oauth_system_prompt_injection"`
	ClaudeOAuthSystemPrompt                string                               `json:"claude_oauth_system_prompt"`
	ClaudeOAuthSystemPromptBlocks          string                               `json:"claude_oauth_system_prompt_blocks"`
	EnableAnthropicCacheTTL1hInjection     bool                                 `json:"enable_anthropic_cache_ttl_1h_injection"`
	RewriteMessageCacheControl             bool                                 `json:"rewrite_message_cache_control"`
	EnableClientDatelineNormalization      bool                                 `json:"enable_client_dateline_normalization"`
	AntigravityUserAgentVersion            string                               `json:"antigravity_user_agent_version"`
	OpenAICodexUserAgent                   string                               `json:"openai_codex_user_agent"`
	OpenAIAllowClaudeCodeCodexPlugin       bool                                 `json:"openai_allow_claude_code_codex_plugin"`
	UserPromptReplacementConfig            *service.UserPromptReplacementConfig `json:"user_prompt_replacement_config"`

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
	// OpenAI 账号配额自动暂停全局默认阈值。后端按 0~1 存储，0 表示不启用全局默认阈值。
	OpenAIQuotaAutoPauseSettings service.OpsOpenAIAccountQuotaAutoPauseSettings `json:"openai_account_quota_auto_pause"`

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
	PaymentMethodFees                service.MethodFeeSettings `json:"payment_method_fees"`
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
	BalanceLowNotifyEnabled         bool               `json:"balance_low_notify_enabled"`
	BalanceLowNotifyThreshold       float64            `json:"balance_low_notify_threshold"`
	BalanceLowNotifyRechargeURL     string             `json:"balance_low_notify_recharge_url"`
	SubscriptionExpiryNotifyEnabled bool               `json:"subscription_expiry_notify_enabled"`
	AccountQuotaNotifyEnabled       bool               `json:"account_quota_notify_enabled"`
	AccountQuotaNotifyEmails        []NotifyEmailEntry `json:"account_quota_notify_emails"`

	// OpenAI fast/flex 策略
	OpenAIFastPolicySettings *OpenAIFastPolicySettings `json:"openai_fast_policy_settings,omitempty"`

	// 系统全局默认平台配额（key = platform，nil/缺省 = 不限制）
	DefaultPlatformQuotas map[string]*service.DefaultPlatformQuotaSetting `json:"default_platform_quotas,omitempty"`

	// 系统全局账号自动停调阈值（key = platform，100 = disabled）
	AccountSchedulingThresholds map[string]int `json:"account_scheduling_thresholds,omitempty"`

	// 允许终端用户在用量页查看自己的失败请求
	AllowUserViewErrorRequests bool `json:"allow_user_view_error_requests"`
}

type DefaultSubscriptionSetting struct {
	PlanID int64 `json:"plan_id"`
}

type PublicSettings struct {
	RegistrationEnabled                 bool                     `json:"registration_enabled"`
	EmailVerifyEnabled                  bool                     `json:"email_verify_enabled"`
	ForceEmailOnThirdPartySignup        bool                     `json:"force_email_on_third_party_signup"`
	RegistrationEmailSuffixWhitelist    []string                 `json:"registration_email_suffix_whitelist"`
	RegistrationEmailDomainQuotaEnabled bool                     `json:"registration_email_domain_quota_enabled"`
	PromoCodeEnabled                    bool                     `json:"promo_code_enabled"`
	PasswordResetEnabled                bool                     `json:"password_reset_enabled"`
	InvitationCodeEnabled               bool                     `json:"invitation_code_enabled"`
	TotpEnabled                         bool                     `json:"totp_enabled"` // TOTP 双因素认证
	PasskeyEnabled                      bool                     `json:"passkey_enabled"`
	LoginAgreementEnabled               bool                     `json:"login_agreement_enabled"`
	LoginAgreementMode                  string                   `json:"login_agreement_mode"`
	LoginAgreementUpdatedAt             string                   `json:"login_agreement_updated_at"`
	LoginAgreementRevision              string                   `json:"login_agreement_revision"`
	LoginAgreementDocuments             []LoginAgreementDocument `json:"login_agreement_documents"`
	TurnstileEnabled                    bool                     `json:"turnstile_enabled"`
	TurnstileSiteKey                    string                   `json:"turnstile_site_key"`
	TencentCaptchaEnabled               bool                     `json:"tencent_captcha_enabled"`
	TencentCaptchaAppID                 string                   `json:"tencent_captcha_app_id"`
	TencentCaptchaRegion                string                   `json:"tencent_captcha_region"`
	AliyunCaptchaEnabled                bool                     `json:"aliyun_captcha_enabled"`
	AliyunCaptchaSceneID                string                   `json:"aliyun_captcha_scene_id"`
	AliyunCaptchaPrefix                 string                   `json:"aliyun_captcha_prefix"`
	AliyunCaptchaRegion                 string                   `json:"aliyun_captcha_region"`
	SiteName                            string                   `json:"site_name"`
	SiteLogo                            string                   `json:"site_logo"`
	SiteSubtitle                        string                   `json:"site_subtitle"`
	SiteNameZh                          string                   `json:"site_name_zh"`
	SiteNameEn                          string                   `json:"site_name_en"`
	SiteTitleZh                         string                   `json:"site_title_zh"`
	SiteTitleEn                         string                   `json:"site_title_en"`
	SiteSubtitleZh                      string                   `json:"site_subtitle_zh"`
	SiteSubtitleEn                      string                   `json:"site_subtitle_en"`
	APIBaseURL                          string                   `json:"api_base_url"`
	ContactInfo                         string                   `json:"contact_info"`
	DocURL                              string                   `json:"doc_url"`
	HomeContent                         string                   `json:"home_content"`
	HideCcsImportButton                 bool                     `json:"hide_ccs_import_button"`
	PurchaseSubscriptionEnabled         bool                     `json:"purchase_subscription_enabled"`
	PurchaseSubscriptionURL             string                   `json:"purchase_subscription_url"`
	TableDefaultPageSize                int                      `json:"table_default_page_size"`
	TablePageSizeOptions                []int                    `json:"table_page_size_options"`
	UsageRankingLimit                   int                      `json:"usage_ranking_limit"`
	CustomMenuItems                     []CustomMenuItem         `json:"custom_menu_items"`
	CustomEndpoints                     []CustomEndpoint         `json:"custom_endpoints"`
	FooterLinks                         []FooterLinkGroup        `json:"footer_links"`
	FooterText                          string                   `json:"footer_text"`
	DingTalkOAuthEnabled                bool                     `json:"dingtalk_oauth_enabled"`
	LinuxDoOAuthEnabled                 bool                     `json:"linuxdo_oauth_enabled"`
	WeChatOAuthEnabled                  bool                     `json:"wechat_oauth_enabled"`
	WeChatOAuthOpenEnabled              bool                     `json:"wechat_oauth_open_enabled"`
	WeChatOAuthMPEnabled                bool                     `json:"wechat_oauth_mp_enabled"`
	WeChatOAuthMobileEnabled            bool                     `json:"wechat_oauth_mobile_enabled"`
	OIDCOAuthEnabled                    bool                     `json:"oidc_oauth_enabled"`
	OIDCOAuthProviderName               string                   `json:"oidc_oauth_provider_name"`
	GitHubOAuthEnabled                  bool                     `json:"github_oauth_enabled"`
	GoogleOAuthEnabled                  bool                     `json:"google_oauth_enabled"`
	GoogleOneTapEnabled                 bool                     `json:"google_one_tap_enabled"`
	GoogleOAuthClientID                 string                   `json:"google_oauth_client_id"`
	SoraClientEnabled                   bool                     `json:"sora_client_enabled"`
	BackendModeEnabled                  bool                     `json:"backend_mode_enabled"`
	PaymentEnabled                      bool                     `json:"payment_enabled"`
	TeamEnabled                         bool                     `json:"team_enabled"`
	TeamSelfServiceEnabled              bool                     `json:"team_self_service_enabled"`
	DataSharingEnabled                  bool                     `json:"data_sharing_enabled"`
	Version                             string                   `json:"version"`
	// 服务器全局时区与当前 UTC 偏移，供前端标注高峰计费窗口等服务端本地时间。
	ServerTimezone              string  `json:"server_timezone"`
	ServerUTCOffset             string  `json:"server_utc_offset"`
	BalanceUnitName             string  `json:"balance_unit_name"`
	BalanceUnitSymbol           string  `json:"balance_unit_symbol"`
	BalanceIconSVG              string  `json:"balance_icon_svg"`
	BalanceLowNotifyEnabled     bool    `json:"balance_low_notify_enabled"`
	AccountQuotaNotifyEnabled   bool    `json:"account_quota_notify_enabled"`
	RiskControlEnabled          bool    `json:"risk_control_enabled"` // 风控中心入口开关
	CyberSessionBlockEnabled    bool    `json:"cyber_session_block_enabled"`
	CyberSessionBlockTTLSeconds int     `json:"cyber_session_block_ttl_seconds"`
	AffiliateEnabled            bool    `json:"affiliate_enabled"` // 邀请返利入口开关
	BalanceLowNotifyThreshold   float64 `json:"balance_low_notify_threshold"`
	BalanceLowNotifyRechargeURL string  `json:"balance_low_notify_recharge_url"`

	// 允许终端用户在用量页查看自己的失败请求
	AllowUserViewErrorRequests bool `json:"allow_user_view_error_requests"`
}

type LoginAgreementDocument struct {
	ID        string `json:"id"`
	Title     string `json:"title"`
	ContentMD string `json:"content_md"`
}

// OverloadCooldownSettings 529过载冷却配置 DTO
type OverloadCooldownSettings struct {
	Enabled         bool `json:"enabled"`
	CooldownMinutes int  `json:"cooldown_minutes"`
}

// OpenAI403CooldownSettings OpenAI OAuth 403 冷却配置 DTO
type OpenAI403CooldownSettings struct {
	Enabled                 bool `json:"enabled"`
	CooldownMinutes         int  `json:"cooldown_minutes"`
	ErrorOnThresholdEnabled bool `json:"error_on_threshold_enabled"`
	ThresholdCount          int  `json:"threshold_count"`
	ThresholdWindowMinutes  int  `json:"threshold_window_minutes"`
}

// RateLimit429CooldownSettings 429默认回避配置 DTO
type RateLimit429CooldownSettings struct {
	Enabled         bool `json:"enabled"`
	CooldownSeconds int  `json:"cooldown_seconds"`
}

// PanelRateLimitSettings 面板 API 限流配置 DTO
type PanelRateLimitSettings struct {
	Enabled     bool `json:"enabled"`
	UserRPM     int  `json:"user_rpm"`
	HeavyRPM    int  `json:"heavy_rpm"`
	ExemptAdmin bool `json:"exempt_admin"`
	PublicIPRPM int  `json:"public_ip_rpm"`
}

// StreamTimeoutSettings 流超时处理配置 DTO
type StreamTimeoutSettings struct {
	Enabled                bool   `json:"enabled"`
	Action                 string `json:"action"`
	TempUnschedMinutes     int    `json:"temp_unsched_minutes"`
	ThresholdCount         int    `json:"threshold_count"`
	ThresholdWindowMinutes int    `json:"threshold_window_minutes"`
}

// RectifierSettings 请求整流器配置 DTO
type RectifierSettings struct {
	Enabled                  bool     `json:"enabled"`
	ThinkingSignatureEnabled bool     `json:"thinking_signature_enabled"`
	ThinkingBudgetEnabled    bool     `json:"thinking_budget_enabled"`
	APIKeySignatureEnabled   bool     `json:"apikey_signature_enabled"`
	APIKeySignaturePatterns  []string `json:"apikey_signature_patterns"`
}

// BetaPolicyRule Beta 策略规则 DTO
type BetaPolicyRule struct {
	BetaToken            string   `json:"beta_token"`
	Action               string   `json:"action"`
	Scope                string   `json:"scope"`
	ErrorMessage         string   `json:"error_message,omitempty"`
	ModelWhitelist       []string `json:"model_whitelist,omitempty"`
	FallbackAction       string   `json:"fallback_action,omitempty"`
	FallbackErrorMessage string   `json:"fallback_error_message,omitempty"`
}

// BetaPolicySettings Beta 策略配置 DTO
type BetaPolicySettings struct {
	Rules []BetaPolicyRule `json:"rules"`
}

// OpenAIFastPolicyRule OpenAI fast/flex 策略规则 DTO
type OpenAIFastPolicyRule struct {
	ServiceTier          string   `json:"service_tier"`
	Action               string   `json:"action"`
	Scope                string   `json:"scope"`
	UserIDs              []int64  `json:"user_ids,omitempty"`
	ErrorMessage         string   `json:"error_message,omitempty"`
	ModelWhitelist       []string `json:"model_whitelist,omitempty"`
	FallbackAction       string   `json:"fallback_action,omitempty"`
	FallbackErrorMessage string   `json:"fallback_error_message,omitempty"`
}

// OpenAIFastPolicySettings OpenAI fast 策略配置 DTO
type OpenAIFastPolicySettings struct {
	Rules []OpenAIFastPolicyRule `json:"rules"`
}

// OpenAIOAuthImportAccountDefaults 是 OpenAI OAuth 导入模板允许填充的账号字段 DTO。
type OpenAIOAuthImportAccountDefaults struct {
	Notes              *string  `json:"notes,omitempty"`
	Concurrency        *int     `json:"concurrency,omitempty"`
	Priority           *int     `json:"priority,omitempty"`
	RateMultiplier     *float64 `json:"rate_multiplier,omitempty"`
	ExpiresAt          *int64   `json:"expires_at,omitempty"`
	AutoPauseOnExpired *bool    `json:"auto_pause_on_expired,omitempty"`
}

// OpenAIOAuthImportDefaults 是 OpenAI OAuth 账号导入缺省模板 DTO。
type OpenAIOAuthImportDefaults struct {
	Account     OpenAIOAuthImportAccountDefaults `json:"account,omitempty"`
	Credentials map[string]any                   `json:"credentials,omitempty"`
	Extra       map[string]any                   `json:"extra,omitempty"`
}

// EmailTemplateEventOption 描述可编辑的通知邮件事件。
type EmailTemplateEventOption struct {
	Value       string `json:"value"`
	Label       string `json:"label,omitempty"`
	Description string `json:"description,omitempty"`
	Category    string `json:"category,omitempty"`
	Optional    bool   `json:"optional,omitempty"`
}

// EmailTemplateSummary 是后台邮件模板列表展示的摘要。
type EmailTemplateSummary struct {
	Event     string `json:"event"`
	Locale    string `json:"locale"`
	Subject   string `json:"subject"`
	IsCustom  bool   `json:"is_custom,omitempty"`
	UpdatedAt string `json:"updated_at,omitempty"`
}

// EmailTemplateListResponse 是邮件模板列表接口的响应。
type EmailTemplateListResponse struct {
	Events       []EmailTemplateEventOption `json:"events"`
	Locales      []string                   `json:"locales"`
	Templates    []EmailTemplateSummary     `json:"templates,omitempty"`
	Placeholders []string                   `json:"placeholders,omitempty"`
}

// EmailTemplateDetail 是指定事件和语言的模板详情。
type EmailTemplateDetail struct {
	Event        string   `json:"event"`
	Locale       string   `json:"locale"`
	Subject      string   `json:"subject"`
	HTML         string   `json:"html"`
	IsCustom     bool     `json:"is_custom,omitempty"`
	UpdatedAt    string   `json:"updated_at,omitempty"`
	Placeholders []string `json:"placeholders,omitempty"`
}

// UpdateEmailTemplateRequest 更新模板覆盖内容。
type UpdateEmailTemplateRequest struct {
	Subject string `json:"subject"`
	HTML    string `json:"html"`
}

// PreviewEmailTemplateRequest 预览未保存的模板内容。
type PreviewEmailTemplateRequest struct {
	Event     string            `json:"event"`
	Locale    string            `json:"locale"`
	Subject   string            `json:"subject"`
	HTML      string            `json:"html"`
	Variables map[string]string `json:"variables,omitempty"`
}

// EmailTemplatePreviewResponse 是模板渲染后的预览响应。
type EmailTemplatePreviewResponse struct {
	Subject string `json:"subject"`
	HTML    string `json:"html"`
}

// ParseCustomMenuItems parses a JSON string into a slice of CustomMenuItem.
// Returns empty slice on empty/invalid input.
func ParseCustomMenuItems(raw string) []CustomMenuItem {
	raw = strings.TrimSpace(raw)
	if raw == "" || raw == "[]" {
		return []CustomMenuItem{}
	}
	var items []CustomMenuItem
	if err := json.Unmarshal([]byte(raw), &items); err != nil {
		return []CustomMenuItem{}
	}
	return items
}

// ParseUserVisibleMenuItems parses custom menu items and filters out admin-only entries.
func ParseUserVisibleMenuItems(raw string) []CustomMenuItem {
	items := ParseCustomMenuItems(raw)
	filtered := make([]CustomMenuItem, 0, len(items))
	for _, item := range items {
		if item.Visibility != "admin" {
			filtered = append(filtered, item)
		}
	}
	return filtered
}

// ParseCustomEndpoints parses a JSON string into a slice of CustomEndpoint.
// Returns empty slice on empty/invalid input.
func ParseCustomEndpoints(raw string) []CustomEndpoint {
	raw = strings.TrimSpace(raw)
	if raw == "" || raw == "[]" {
		return []CustomEndpoint{}
	}
	var items []CustomEndpoint
	if err := json.Unmarshal([]byte(raw), &items); err != nil {
		return []CustomEndpoint{}
	}
	return items
}

// FooterLink 首页底栏单条链接。
type FooterLink struct {
	Label string `json:"label"`
	URL   string `json:"url"`
}

// FooterLinkGroup 首页底栏链接分组（一列）。
type FooterLinkGroup struct {
	Title string       `json:"title"`
	Links []FooterLink `json:"links"`
}

// ParseFooterLinks parses a JSON string into a slice of FooterLinkGroup.
// Returns empty slice on empty/invalid input.
func ParseFooterLinks(raw string) []FooterLinkGroup {
	raw = strings.TrimSpace(raw)
	if raw == "" || raw == "[]" {
		return []FooterLinkGroup{}
	}
	var groups []FooterLinkGroup
	if err := json.Unmarshal([]byte(raw), &groups); err != nil {
		return []FooterLinkGroup{}
	}
	return groups
}
