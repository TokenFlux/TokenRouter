package dto

import (
	"github.com/TokenFlux/TokenRouter/internal/account"
	billinghttp "github.com/TokenFlux/TokenRouter/internal/billing/httpapi"
	"github.com/TokenFlux/TokenRouter/internal/creative"
	gatewaydto "github.com/TokenFlux/TokenRouter/internal/gateway/httpapi/dto"
	"github.com/TokenFlux/TokenRouter/internal/gateway/promptpolicy"
	"github.com/TokenFlux/TokenRouter/internal/identity"
	identitydto "github.com/TokenFlux/TokenRouter/internal/identity/httpapi/dto"
	"github.com/TokenFlux/TokenRouter/internal/payment"
	sitedto "github.com/TokenFlux/TokenRouter/internal/site/httpapi/dto"
)

// UpdateSettingsRequest 只保留综合 HTTP 文档的扁平形状与字段存在性，规则由各所有者解释。
type UpdateSettingsRequest struct {
	// 注册设置
	RegistrationEnabled                 bool                             `json:"registration_enabled"`
	EmailVerifyEnabled                  bool                             `json:"email_verify_enabled"`
	RegistrationEmailSuffixWhitelist    []string                         `json:"registration_email_suffix_whitelist"`
	RegistrationEmailNormalization      bool                             `json:"registration_email_normalization"`
	RegistrationEmailDomainQuotaEnabled *bool                            `json:"registration_email_domain_quota_enabled"` // 省略时保持现值
	UserEmailChangeEnabled              *bool                            `json:"user_email_change_enabled"`               // 省略时保持现值
	PromoCodeEnabled                    bool                             `json:"promo_code_enabled"`
	PasswordResetEnabled                bool                             `json:"password_reset_enabled"`
	FrontendURL                         string                           `json:"frontend_url"`
	InvitationCodeEnabled               bool                             `json:"invitation_code_enabled"`
	TotpEnabled                         bool                             `json:"totp_enabled"`             // TOTP 双因素认证
	SessionBindingEnabled               *bool                            `json:"session_binding_enabled"`  // 会话 IP/UA 绑定（省略=保持现值）
	StepUpEnabled                       *bool                            `json:"step_up_enabled"`          // 敏感操作 step-up 2FA（省略=保持现值）
	AuditLogRetentionDays               int                              `json:"audit_log_retention_days"` // 审计日志保留天数
	LoginAgreementEnabled               bool                             `json:"login_agreement_enabled"`
	LoginAgreementMode                  string                           `json:"login_agreement_mode"`
	LoginAgreementUpdatedAt             string                           `json:"login_agreement_updated_at"`
	LoginAgreementDocuments             []sitedto.LoginAgreementDocument `json:"login_agreement_documents"`

	// 邮件服务设置
	SMTPHost     string `json:"smtp_host"`
	SMTPPort     int    `json:"smtp_port"`
	SMTPUsername string `json:"smtp_username"`
	SMTPPassword string `json:"smtp_password"`
	SMTPFrom     string `json:"smtp_from_email"`
	SMTPFromName string `json:"smtp_from_name"`
	SMTPUseTLS   bool   `json:"smtp_use_tls"`

	// Cloudflare Turnstile 设置
	TurnstileEnabled   bool   `json:"turnstile_enabled"`
	TurnstileSiteKey   string `json:"turnstile_site_key"`
	TurnstileSecretKey string `json:"turnstile_secret_key"`

	// 腾讯天御验证码设置
	TencentCaptchaEnabled        bool   `json:"tencent_captcha_enabled"`
	TencentCaptchaAppID          string `json:"tencent_captcha_app_id"`
	TencentCaptchaAppSecretKey   string `json:"tencent_captcha_app_secret_key"`
	TencentCaptchaCloudSecretID  string `json:"tencent_captcha_cloud_secret_id"`
	TencentCaptchaCloudSecretKey string `json:"tencent_captcha_cloud_secret_key"`
	TencentCaptchaRegion         string `json:"tencent_captcha_region"`

	// 阿里云验证码 2.0 设置
	AliyunCaptchaEnabled         bool   `json:"aliyun_captcha_enabled"`
	AliyunCaptchaAccessKeyID     string `json:"aliyun_captcha_access_key_id"`
	AliyunCaptchaAccessKeySecret string `json:"aliyun_captcha_access_key_secret"`
	AliyunCaptchaSceneID         string `json:"aliyun_captcha_scene_id"`
	AliyunCaptchaPrefix          string `json:"aliyun_captcha_prefix"`
	AliyunCaptchaRegion          string `json:"aliyun_captcha_region"`

	// API Key IP 访问控制设置
	APIKeyACLTrustForwardedIP *bool     `json:"api_key_acl_trust_forwarded_ip"`
	ForwardedClientIPHeaders  *[]string `json:"forwarded_client_ip_headers"`

	// LinuxDo Connect OAuth 登录
	LinuxDoConnectEnabled      bool   `json:"linuxdo_connect_enabled"`
	LinuxDoConnectClientID     string `json:"linuxdo_connect_client_id"`
	LinuxDoConnectClientSecret string `json:"linuxdo_connect_client_secret"`
	LinuxDoConnectRedirectURL  string `json:"linuxdo_connect_redirect_url"`

	// DingTalk Connect OAuth 登录
	DingTalkConnectEnabled                 bool   `json:"dingtalk_connect_enabled"`
	DingTalkConnectClientID                string `json:"dingtalk_connect_client_id"`
	DingTalkConnectClientSecret            string `json:"dingtalk_connect_client_secret"`
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

	// WeChat Connect OAuth 登录
	WeChatConnectEnabled             bool   `json:"wechat_connect_enabled"`
	WeChatConnectAppID               string `json:"wechat_connect_app_id"`
	WeChatConnectAppSecret           string `json:"wechat_connect_app_secret"`
	WeChatConnectOpenAppID           string `json:"wechat_connect_open_app_id"`
	WeChatConnectOpenAppSecret       string `json:"wechat_connect_open_app_secret"`
	WeChatConnectMPAppID             string `json:"wechat_connect_mp_app_id"`
	WeChatConnectMPAppSecret         string `json:"wechat_connect_mp_app_secret"`
	WeChatConnectMobileAppID         string `json:"wechat_connect_mobile_app_id"`
	WeChatConnectMobileAppSecret     string `json:"wechat_connect_mobile_app_secret"`
	WeChatConnectOpenEnabled         bool   `json:"wechat_connect_open_enabled"`
	WeChatConnectMPEnabled           bool   `json:"wechat_connect_mp_enabled"`
	WeChatConnectMobileEnabled       bool   `json:"wechat_connect_mobile_enabled"`
	WeChatConnectMode                string `json:"wechat_connect_mode"`
	WeChatConnectScopes              string `json:"wechat_connect_scopes"`
	WeChatConnectRedirectURL         string `json:"wechat_connect_redirect_url"`
	WeChatConnectFrontendRedirectURL string `json:"wechat_connect_frontend_redirect_url"`

	// Generic OIDC OAuth 登录
	OIDCConnectEnabled              bool   `json:"oidc_connect_enabled"`
	OIDCConnectProviderName         string `json:"oidc_connect_provider_name"`
	OIDCConnectClientID             string `json:"oidc_connect_client_id"`
	OIDCConnectClientSecret         string `json:"oidc_connect_client_secret"`
	OIDCConnectIssuerURL            string `json:"oidc_connect_issuer_url"`
	OIDCConnectDiscoveryURL         string `json:"oidc_connect_discovery_url"`
	OIDCConnectAuthorizeURL         string `json:"oidc_connect_authorize_url"`
	OIDCConnectTokenURL             string `json:"oidc_connect_token_url"`
	OIDCConnectUserInfoURL          string `json:"oidc_connect_userinfo_url"`
	OIDCConnectJWKSURL              string `json:"oidc_connect_jwks_url"`
	OIDCConnectScopes               string `json:"oidc_connect_scopes"`
	OIDCConnectRedirectURL          string `json:"oidc_connect_redirect_url"`
	OIDCConnectFrontendRedirectURL  string `json:"oidc_connect_frontend_redirect_url"`
	OIDCConnectTokenAuthMethod      string `json:"oidc_connect_token_auth_method"`
	OIDCConnectUsePKCE              *bool  `json:"oidc_connect_use_pkce"`
	OIDCConnectValidateIDToken      *bool  `json:"oidc_connect_validate_id_token"`
	OIDCConnectAllowedSigningAlgs   string `json:"oidc_connect_allowed_signing_algs"`
	OIDCConnectClockSkewSeconds     int    `json:"oidc_connect_clock_skew_seconds"`
	OIDCConnectRequireEmailVerified bool   `json:"oidc_connect_require_email_verified"`
	OIDCConnectUserInfoEmailPath    string `json:"oidc_connect_userinfo_email_path"`
	OIDCConnectUserInfoIDPath       string `json:"oidc_connect_userinfo_id_path"`
	OIDCConnectUserInfoUsernamePath string `json:"oidc_connect_userinfo_username_path"`

	// GitHub / Google 邮箱快捷登录
	GitHubOAuthEnabled             bool   `json:"github_oauth_enabled"`
	GitHubOAuthClientID            string `json:"github_oauth_client_id"`
	GitHubOAuthClientSecret        string `json:"github_oauth_client_secret"`
	GitHubOAuthRedirectURL         string `json:"github_oauth_redirect_url"`
	GitHubOAuthFrontendRedirectURL string `json:"github_oauth_frontend_redirect_url"`
	GoogleOAuthEnabled             bool   `json:"google_oauth_enabled"`
	GoogleOneTapEnabled            bool   `json:"google_one_tap_enabled"`
	GoogleOAuthClientID            string `json:"google_oauth_client_id"`
	GoogleOAuthClientSecret        string `json:"google_oauth_client_secret"`
	GoogleOAuthRedirectURL         string `json:"google_oauth_redirect_url"`
	GoogleOAuthFrontendRedirectURL string `json:"google_oauth_frontend_redirect_url"`

	// OEM设置
	SiteName                    string                           `json:"site_name"`
	SiteLogo                    string                           `json:"site_logo"`
	SiteSubtitle                string                           `json:"site_subtitle"`
	SiteNameZh                  string                           `json:"site_name_zh"`
	SiteNameEn                  string                           `json:"site_name_en"`
	SiteTitleZh                 string                           `json:"site_title_zh"`
	SiteTitleEn                 string                           `json:"site_title_en"`
	SiteSubtitleZh              string                           `json:"site_subtitle_zh"`
	SiteSubtitleEn              string                           `json:"site_subtitle_en"`
	APIBaseURL                  string                           `json:"api_base_url"`
	ContactInfo                 string                           `json:"contact_info"`
	DocURL                      string                           `json:"doc_url"`
	HomeContent                 string                           `json:"home_content"`
	HideCcsImportButton         bool                             `json:"hide_ccs_import_button"`
	PurchaseSubscriptionEnabled *bool                            `json:"purchase_subscription_enabled"`
	PurchaseSubscriptionURL     *string                          `json:"purchase_subscription_url"`
	TableDefaultPageSize        int                              `json:"table_default_page_size"`
	TablePageSizeOptions        []int                            `json:"table_page_size_options"`
	UsageRankingLimit           int                              `json:"usage_ranking_limit"`
	UsageRankingEnabled         *bool                            `json:"usage_ranking_enabled"`
	UsageRankingSortBy          *string                          `json:"usage_ranking_sort_by"`
	UsageRankingShowTotalTokens *bool                            `json:"usage_ranking_show_total_tokens"`
	UsageRankingShowRequests    *bool                            `json:"usage_ranking_show_requests"`
	UsageRankingShowActualCost  *bool                            `json:"usage_ranking_show_actual_cost"`
	CustomMenuItems             *[]sitedto.CustomMenuItem        `json:"custom_menu_items"`
	CustomEndpoints             *[]sitedto.CustomEndpoint        `json:"custom_endpoints"`
	FooterLinks                 *[]sitedto.FooterLinkGroup       `json:"footer_links"`
	FooterText                  *string                          `json:"footer_text"`
	HomeFeaturedModels          *[]string                        `json:"home_featured_models"`
	CreativeModelSettings       *[]creative.CreativeModelSetting `json:"creative_model_settings"`
	CreativeWorkerCount         *int                             `json:"creative_worker_count"`

	// 默认配置
	DefaultConcurrency                        int                                       `json:"default_concurrency"`
	DefaultBalance                            float64                                   `json:"default_balance"`
	AffiliateEnabled                          bool                                      `json:"affiliate_enabled"`
	AffiliateRebateRate                       float64                                   `json:"affiliate_rebate_rate"`
	AffiliateRebateFreezeHours                int                                       `json:"affiliate_rebate_freeze_hours"`
	AffiliateRebateDurationDays               int                                       `json:"affiliate_rebate_duration_days"`
	AffiliateRebatePerInviteeCap              float64                                   `json:"affiliate_rebate_per_invitee_cap"`
	AdminRechargeRebateEnabled                *bool                                     `json:"affiliate_admin_recharge_enabled"`
	DefaultUserRPMLimit                       int                                       `json:"default_user_rpm_limit"`
	DefaultUserAPIKeyLimit                    *int                                      `json:"default_user_api_key_limit"`
	DefaultSubscriptions                      []billinghttp.DefaultSubscriptionSetting  `json:"default_subscriptions"`
	BalanceUnitName                           string                                    `json:"balance_unit_name"`
	BalanceUnitSymbol                         string                                    `json:"balance_unit_symbol"`
	BalanceIconSVG                            string                                    `json:"balance_icon_svg"`
	ReasoningPointRMBUnitPrice                *float64                                  `json:"reasoning_point_rmb_unit_price"`
	USDExchangeRate                           *float64                                  `json:"usd_exchange_rate"`
	MarketplaceAvailabilityWindowDays         *int                                      `json:"marketplace_availability_window_days"`
	MarketplaceAvailabilityBucketMinutes      *int                                      `json:"marketplace_availability_bucket_minutes"`
	AuthSourceDefaultEmailBalance             *float64                                  `json:"auth_source_default_email_balance"`
	AuthSourceDefaultEmailConcurrency         *int                                      `json:"auth_source_default_email_concurrency"`
	AuthSourceDefaultEmailSubscriptions       *[]billinghttp.DefaultSubscriptionSetting `json:"auth_source_default_email_subscriptions"`
	AuthSourceDefaultEmailGrantOnSignup       *bool                                     `json:"auth_source_default_email_grant_on_signup"`
	AuthSourceDefaultEmailGrantOnFirstBind    *bool                                     `json:"auth_source_default_email_grant_on_first_bind"`
	AuthSourceDefaultLinuxDoBalance           *float64                                  `json:"auth_source_default_linuxdo_balance"`
	AuthSourceDefaultLinuxDoConcurrency       *int                                      `json:"auth_source_default_linuxdo_concurrency"`
	AuthSourceDefaultLinuxDoSubscriptions     *[]billinghttp.DefaultSubscriptionSetting `json:"auth_source_default_linuxdo_subscriptions"`
	AuthSourceDefaultLinuxDoGrantOnSignup     *bool                                     `json:"auth_source_default_linuxdo_grant_on_signup"`
	AuthSourceDefaultLinuxDoGrantOnFirstBind  *bool                                     `json:"auth_source_default_linuxdo_grant_on_first_bind"`
	AuthSourceDefaultOIDCBalance              *float64                                  `json:"auth_source_default_oidc_balance"`
	AuthSourceDefaultOIDCConcurrency          *int                                      `json:"auth_source_default_oidc_concurrency"`
	AuthSourceDefaultOIDCSubscriptions        *[]billinghttp.DefaultSubscriptionSetting `json:"auth_source_default_oidc_subscriptions"`
	AuthSourceDefaultOIDCGrantOnSignup        *bool                                     `json:"auth_source_default_oidc_grant_on_signup"`
	AuthSourceDefaultOIDCGrantOnFirstBind     *bool                                     `json:"auth_source_default_oidc_grant_on_first_bind"`
	AuthSourceDefaultWeChatBalance            *float64                                  `json:"auth_source_default_wechat_balance"`
	AuthSourceDefaultWeChatConcurrency        *int                                      `json:"auth_source_default_wechat_concurrency"`
	AuthSourceDefaultWeChatSubscriptions      *[]billinghttp.DefaultSubscriptionSetting `json:"auth_source_default_wechat_subscriptions"`
	AuthSourceDefaultWeChatGrantOnSignup      *bool                                     `json:"auth_source_default_wechat_grant_on_signup"`
	AuthSourceDefaultWeChatGrantOnFirstBind   *bool                                     `json:"auth_source_default_wechat_grant_on_first_bind"`
	AuthSourceDefaultGitHubBalance            *float64                                  `json:"auth_source_default_github_balance"`
	AuthSourceDefaultGitHubConcurrency        *int                                      `json:"auth_source_default_github_concurrency"`
	AuthSourceDefaultGitHubSubscriptions      *[]billinghttp.DefaultSubscriptionSetting `json:"auth_source_default_github_subscriptions"`
	AuthSourceDefaultGitHubGrantOnSignup      *bool                                     `json:"auth_source_default_github_grant_on_signup"`
	AuthSourceDefaultGitHubGrantOnFirstBind   *bool                                     `json:"auth_source_default_github_grant_on_first_bind"`
	AuthSourceDefaultGoogleBalance            *float64                                  `json:"auth_source_default_google_balance"`
	AuthSourceDefaultGoogleConcurrency        *int                                      `json:"auth_source_default_google_concurrency"`
	AuthSourceDefaultGoogleSubscriptions      *[]billinghttp.DefaultSubscriptionSetting `json:"auth_source_default_google_subscriptions"`
	AuthSourceDefaultGoogleGrantOnSignup      *bool                                     `json:"auth_source_default_google_grant_on_signup"`
	AuthSourceDefaultGoogleGrantOnFirstBind   *bool                                     `json:"auth_source_default_google_grant_on_first_bind"`
	AuthSourceDefaultDingTalkBalance          *float64                                  `json:"auth_source_default_dingtalk_balance"`
	AuthSourceDefaultDingTalkConcurrency      *int                                      `json:"auth_source_default_dingtalk_concurrency"`
	AuthSourceDefaultDingTalkSubscriptions    *[]billinghttp.DefaultSubscriptionSetting `json:"auth_source_default_dingtalk_subscriptions"`
	AuthSourceDefaultDingTalkGrantOnSignup    *bool                                     `json:"auth_source_default_dingtalk_grant_on_signup"`
	AuthSourceDefaultDingTalkGrantOnFirstBind *bool                                     `json:"auth_source_default_dingtalk_grant_on_first_bind"`
	ForceEmailOnThirdPartySignup              *bool                                     `json:"force_email_on_third_party_signup"`

	// Model fallback configuration
	EnableModelFallback      bool   `json:"enable_model_fallback"`
	FallbackModelAnthropic   string `json:"fallback_model_anthropic"`
	FallbackModelOpenAI      string `json:"fallback_model_openai"`
	FallbackModelGemini      string `json:"fallback_model_gemini"`
	FallbackModelAntigravity string `json:"fallback_model_antigravity"`

	// Grok 模型映射策略采用指针，省略字段时保持现值。
	GrokDefaultTextModel           *string `json:"grok_default_text_model"`
	GrokCrossClientModelMapEnabled *bool   `json:"grok_cross_client_model_map_enabled"`
	GrokDefaultBaseURLMode         *string `json:"grok_default_base_url_mode"`

	// Identity patch configuration (Claude -> Gemini)
	EnableIdentityPatch bool   `json:"enable_identity_patch"`
	IdentityPatchPrompt string `json:"identity_patch_prompt"`

	// Ops monitoring (vNext)
	OpsMonitoringEnabled         *bool `json:"ops_monitoring_enabled"`
	OpsRealtimeMonitoringEnabled *bool `json:"ops_realtime_monitoring_enabled"`
	OpsMetricsIntervalSeconds    *int  `json:"ops_metrics_interval_seconds"`

	MinClaudeCodeVersion string `json:"min_claude_code_version"`
	MaxClaudeCodeVersion string `json:"max_claude_code_version"`

	// 分组隔离
	AllowUngroupedKeyScheduling bool `json:"allow_ungrouped_key_scheduling"`

	// Backend Mode
	BackendModeEnabled bool `json:"backend_mode_enabled"`

	// 网关转发行为
	OpenAITTFTMode                         *string                                   `json:"openai_ttft_mode"`
	EnableFingerprintUnification           *bool                                     `json:"enable_fingerprint_unification"`
	EnableMetadataPassthrough              *bool                                     `json:"enable_metadata_passthrough"`
	EnableCCHSigning                       *bool                                     `json:"enable_cch_signing"`
	EnableClaudeOAuthSystemPromptInjection *bool                                     `json:"enable_claude_oauth_system_prompt_injection"`
	ClaudeOAuthSystemPrompt                *string                                   `json:"claude_oauth_system_prompt"`
	ClaudeOAuthSystemPromptBlocks          *string                                   `json:"claude_oauth_system_prompt_blocks"`
	EnableAnthropicCacheTTL1hInjection     *bool                                     `json:"enable_anthropic_cache_ttl_1h_injection"`
	RewriteMessageCacheControl             *bool                                     `json:"rewrite_message_cache_control"`
	EnableClientDatelineNormalization      *bool                                     `json:"enable_client_dateline_normalization"`
	AntigravityUserAgentVersion            *string                                   `json:"antigravity_user_agent_version"`
	OpenAICodexUserAgent                   *string                                   `json:"openai_codex_user_agent"`
	OpenAIAllowClaudeCodeCodexPlugin       *bool                                     `json:"openai_allow_claude_code_codex_plugin"`
	UserPromptReplacementConfig            *promptpolicy.UserPromptReplacementConfig `json:"user_prompt_replacement_config"`

	// Payment visible method routing
	PaymentVisibleMethodAlipaySource  *string `json:"payment_visible_method_alipay_source"`
	PaymentVisibleMethodWxpaySource   *string `json:"payment_visible_method_wxpay_source"`
	PaymentVisibleMethodAlipayEnabled *bool   `json:"payment_visible_method_alipay_enabled"`
	PaymentVisibleMethodWxpayEnabled  *bool   `json:"payment_visible_method_wxpay_enabled"`

	// 通用高级调度器参数
	AdvancedSchedulerStickyWeightedEnabled       *bool   `json:"advanced_scheduler_sticky_weighted_enabled"`
	AdvancedSchedulerSubscriptionPriorityEnabled *bool   `json:"advanced_scheduler_subscription_priority_enabled"`
	AdvancedSchedulerEWMAErrorRateAlpha          *string `json:"advanced_scheduler_ewma_error_rate_alpha"`
	AdvancedSchedulerEWMATTFTAlpha               *string `json:"advanced_scheduler_ewma_ttft_alpha"`
	AdvancedSchedulerStickyEscapeEnabled         *bool   `json:"advanced_scheduler_sticky_escape_enabled"`
	AdvancedSchedulerStickyEscapeTTFTMs          *string `json:"advanced_scheduler_sticky_escape_ttft_ms"`
	AdvancedSchedulerStickyEscapeErrorRate       *string `json:"advanced_scheduler_sticky_escape_error_rate"`
	AdvancedSchedulerLBTopK                      *string `json:"advanced_scheduler_lb_top_k"`
	AdvancedSchedulerWeightPriority              *string `json:"advanced_scheduler_weight_priority"`
	AdvancedSchedulerWeightLoad                  *string `json:"advanced_scheduler_weight_load"`
	AdvancedSchedulerWeightQueue                 *string `json:"advanced_scheduler_weight_queue"`
	AdvancedSchedulerWeightErrorRate             *string `json:"advanced_scheduler_weight_error_rate"`
	AdvancedSchedulerWeightTTFT                  *string `json:"advanced_scheduler_weight_ttft"`
	AdvancedSchedulerWeightReset                 *string `json:"advanced_scheduler_weight_reset"`
	AdvancedSchedulerWeightQuotaHeadroom         *string `json:"advanced_scheduler_weight_quota_headroom"`
	AdvancedSchedulerWeightPreviousResponse      *string `json:"advanced_scheduler_weight_previous_response"`
	AdvancedSchedulerWeightSessionSticky         *string `json:"advanced_scheduler_weight_session_sticky"`
	// OpenAI 账号配额自动暂停全局默认阈值。使用指针区分旧客户端未提交与显式提交零值。
	OpenAIQuotaAutoPauseSettings *account.QuotaAutoPauseSettings `json:"openai_account_quota_auto_pause"`

	// 余额不足提醒
	BalanceLowNotifyEnabled         *bool                           `json:"balance_low_notify_enabled"`
	BalanceLowNotifyThreshold       *float64                        `json:"balance_low_notify_threshold"`
	BalanceLowNotifyRechargeURL     *string                         `json:"balance_low_notify_recharge_url"`
	SubscriptionExpiryNotifyEnabled *bool                           `json:"subscription_expiry_notify_enabled"`
	AccountQuotaNotifyEnabled       *bool                           `json:"account_quota_notify_enabled"`
	AccountQuotaNotifyEmails        *[]identitydto.NotifyEmailEntry `json:"account_quota_notify_emails"`

	// Payment configuration (integrated into settings, full replace)
	PaymentEnabled                   *bool                     `json:"payment_enabled"`
	PaymentMinAmount                 *float64                  `json:"payment_min_amount"`
	PaymentMaxAmount                 *float64                  `json:"payment_max_amount"`
	PaymentDailyLimit                *float64                  `json:"payment_daily_limit"`
	PaymentOrderTimeoutMin           *int                      `json:"payment_order_timeout_minutes"`
	PaymentMaxPendingOrders          *int                      `json:"payment_max_pending_orders"`
	PaymentEnabledTypes              []string                  `json:"payment_enabled_types"`
	PaymentBalanceDisabled           *bool                     `json:"payment_balance_disabled"`
	PaymentBalanceRechargeMultiplier *float64                  `json:"payment_balance_recharge_multiplier"`
	PaymentSubscriptionUSDToCNYRate  *float64                  `json:"payment_subscription_usd_to_cny_rate"`
	PaymentRechargeFeeRate           *float64                  `json:"payment_recharge_fee_rate"`
	PaymentMethodFees                payment.MethodFeeSettings `json:"payment_method_fees"`
	PaymentLoadBalanceStrat          *string                   `json:"payment_load_balance_strategy"`
	PaymentProductNamePrefix         *string                   `json:"payment_product_name_prefix"`
	PaymentProductNameSuffix         *string                   `json:"payment_product_name_suffix"`
	PaymentHelpImageURL              *string                   `json:"payment_help_image_url"`
	PaymentHelpText                  *string                   `json:"payment_help_text"`

	// Cancel rate limit
	PaymentCancelRateLimitEnabled *bool   `json:"payment_cancel_rate_limit_enabled"`
	PaymentCancelRateLimitMax     *int    `json:"payment_cancel_rate_limit_max"`
	PaymentCancelRateLimitWindow  *int    `json:"payment_cancel_rate_limit_window"`
	PaymentCancelRateLimitUnit    *string `json:"payment_cancel_rate_limit_unit"`
	PaymentCancelRateLimitMode    *string `json:"payment_cancel_rate_limit_window_mode"`

	// 支付宝移动端强制使用二维码支付，不再跳转手机网站支付。
	PaymentAlipayForceQRCode *bool `json:"payment_alipay_force_qrcode"`
	// 移动端使用支付宝当面付预下单，并通过深链接唤起支付宝客户端。
	PaymentAlipayMobilePrecreateDeepLink *bool `json:"payment_alipay_mobile_precreate_deep_link"`

	// 风控中心功能开关
	RiskControlEnabled *bool `json:"risk_control_enabled"`
	// 团队和创作台页面功能开关
	TeamEnabled     *bool `json:"team_enabled"`
	CreativeEnabled *bool `json:"creative_enabled"`

	// cyber 会话屏蔽开关与 TTL
	CyberSessionBlockEnabled    *bool `json:"cyber_session_block_enabled"`
	CyberSessionBlockTTLSeconds *int  `json:"cyber_session_block_ttl_seconds"`

	// OpenAI fast/flex 策略（只在请求显式提供时更新）
	OpenAIFastPolicySettings *gatewaydto.OpenAIFastPolicySettings `json:"openai_fast_policy_settings,omitempty"`

	// 系统全局 platform quota 默认值（整体替换语义：nil = 不修改，non-nil = 整体覆盖）。
	DefaultPlatformQuotas map[string]*identity.DefaultPlatformQuotaSetting `json:"default_platform_quotas"`

	// 各平台账号自动停调阈值（整体替换语义：nil = 不修改，non-nil = 整体覆盖）。
	AccountSchedulingThresholds map[string]int `json:"account_scheduling_thresholds"`

	// auth-source 层 platform quota 覆盖（override 语义：nil = 不修改，non-nil = 整体覆盖该 source 的 quota 配置）。
	AuthSourceEmailPlatformQuotas    map[string]*identity.DefaultPlatformQuotaSetting `json:"auth_source_default_email_platform_quotas"`
	AuthSourceLinuxDoPlatformQuotas  map[string]*identity.DefaultPlatformQuotaSetting `json:"auth_source_default_linuxdo_platform_quotas"`
	AuthSourceOIDCPlatformQuotas     map[string]*identity.DefaultPlatformQuotaSetting `json:"auth_source_default_oidc_platform_quotas"`
	AuthSourceWeChatPlatformQuotas   map[string]*identity.DefaultPlatformQuotaSetting `json:"auth_source_default_wechat_platform_quotas"`
	AuthSourceGitHubPlatformQuotas   map[string]*identity.DefaultPlatformQuotaSetting `json:"auth_source_default_github_platform_quotas"`
	AuthSourceGooglePlatformQuotas   map[string]*identity.DefaultPlatformQuotaSetting `json:"auth_source_default_google_platform_quotas"`
	AuthSourceDingTalkPlatformQuotas map[string]*identity.DefaultPlatformQuotaSetting `json:"auth_source_default_dingtalk_platform_quotas"`

	AllowUserViewErrorRequests *bool `json:"allow_user_view_error_requests"`
}
