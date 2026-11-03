package dto

import (
	"encoding/json"

	billinghttp "github.com/TokenFlux/TokenRouter/internal/billing/httpapi"
	"github.com/TokenFlux/TokenRouter/internal/creative"
	gatewaydto "github.com/TokenFlux/TokenRouter/internal/gateway/httpapi/dto"
	"github.com/TokenFlux/TokenRouter/internal/gateway/promptpolicy"
	"github.com/TokenFlux/TokenRouter/internal/gateway/ws"
	identitydto "github.com/TokenFlux/TokenRouter/internal/identity/httpapi/dto"
	"github.com/TokenFlux/TokenRouter/internal/ops"
	"github.com/TokenFlux/TokenRouter/internal/payment"
	"github.com/TokenFlux/TokenRouter/internal/pkg/locale"
	sitedto "github.com/TokenFlux/TokenRouter/internal/site/httpapi/dto"
)

// settings_views.go defines the JSON response fields for combined
// administrator settings reads.
// Administrator responses show current control values alongside saved editor
// content and computed runtime parameters. Secret configuration is represented
// by presence flags, while public IDs and connection details remain visible for
// editing. The response mapper supplies system settings and a separately read
// payment configuration to this combined representation.
//
// Responses and scheduler fields separate saved overrides from effective values.
// GET additionally intersects monitoring with service availability and fills the
// provider-threshold map; PUT reads committed values for its response. Collections
// assigned by the mapper can retain source storage, while conversion helpers copy
// some outer lists, so in-process callers follow those helpers' ownership rules
// until response serialization completes.

// SystemSettings is the flat administrator response built from system and payment settings.
// Its collection fields can share mapper inputs until serialization; the owning
// conversion helpers describe their copied outer lists and retained nested values.
type SystemSettings struct {
	// ResponsesWS keeps saved override JSON beside ResponsesWSEffective for
	// administrator inspection. Parse can select a current runtime effective value
	// while keeping valid but unresolved override JSON in the response.
	ResponsesWS                         json.RawMessage                  `json:"responses_ws"`                            // raw Responses WebSocket override JSON
	ResponsesWSEffective                ws.Parameters                    `json:"responses_ws_effective"`                  // resolved Responses WebSocket runtime parameters
	RegistrationEnabled                 bool                             `json:"registration_enabled"`                    // new account registration switch
	EmailVerifyEnabled                  bool                             `json:"email_verify_enabled"`                    // registration email verification switch
	RegistrationEmailSuffixWhitelist    []string                         `json:"registration_email_suffix_whitelist"`     // allowed registration email domain suffixes
	RegistrationEmailNormalization      bool                             `json:"registration_email_normalization"`        // registration mailbox normalization switch
	RegistrationEmailDomainQuotaEnabled bool                             `json:"registration_email_domain_quota_enabled"` // outside-whitelist registration quota switch
	UserEmailChangeEnabled              bool                             `json:"user_email_change_enabled"`               // primary email replacement policy switch
	PromoCodeEnabled                    bool                             `json:"promo_code_enabled"`                      // registration promotional code switch
	PasswordResetEnabled                bool                             `json:"password_reset_enabled"`                  // password recovery policy switch
	FrontendURL                         string                           `json:"frontend_url"`                            // notification and redirect frontend address
	InvitationCodeEnabled               bool                             `json:"invitation_code_enabled"`                 // registration invitation requirement switch
	TotpEnabled                         bool                             `json:"totp_enabled"`                            // TOTP authentication availability switch
	TotpEncryptionKeyConfigured         bool                             `json:"totp_encryption_key_configured"`          // startup encryption key presence flag
	SessionBindingEnabled               bool                             `json:"session_binding_enabled"`                 // IP and user-agent session checks
	StepUpEnabled                       bool                             `json:"step_up_enabled"`                         // sensitive operation verification switch
	AuditLogRetentionDays               int                              `json:"audit_log_retention_days"`                // days
	LoginAgreementEnabled               bool                             `json:"login_agreement_enabled"`                 // login agreement acceptance requirement
	LoginAgreementMode                  string                           `json:"login_agreement_mode"`                    // modal or checkbox consent mode
	LoginAgreementUpdatedAt             string                           `json:"login_agreement_updated_at"`              // administrator agreement update marker
	LoginAgreementDocuments             []sitedto.LoginAgreementDocument `json:"login_agreement_documents"`               // editable login agreement document records

	SMTPHost               string `json:"smtp_host"`                // outgoing mail server hostname
	SMTPPort               int    `json:"smtp_port"`                // SMTP connection port number
	SMTPUsername           string `json:"smtp_username"`            // outgoing mail authentication username
	SMTPPasswordConfigured bool   `json:"smtp_password_configured"` // SMTP secret presence display flag
	SMTPFrom               string `json:"smtp_from_email"`          // outgoing mail sender address
	SMTPFromName           string `json:"smtp_from_name"`           // outgoing mail sender display name
	SMTPUseTLS             bool   `json:"smtp_use_tls"`             // SMTP secure connection preference

	// Turnstile, Tencent, Aliyun, and OAuth configured flags expose credential
	// presence from their owning readers. Their secret text stays in the internal
	// Snapshot and is not selected by the response mapper.
	TurnstileEnabled                       bool     `json:"turnstile_enabled"`                           // Cloudflare challenge verification switch
	TurnstileSiteKey                       string   `json:"turnstile_site_key"`                          // public Turnstile widget key
	TurnstileSecretKeyConfigured           bool     `json:"turnstile_secret_key_configured"`             // Turnstile secret presence display flag
	TencentCaptchaEnabled                  bool     `json:"tencent_captcha_enabled"`                     // Tencent challenge verification switch
	TencentCaptchaAppID                    string   `json:"tencent_captcha_app_id"`                      // Tencent CAPTCHA application identifier
	TencentCaptchaAppSecretKeyConfigured   bool     `json:"tencent_captcha_app_secret_key_configured"`   // application secret presence display flag
	TencentCaptchaCloudSecretIDConfigured  bool     `json:"tencent_captcha_cloud_secret_id_configured"`  // cloud credential identifier presence flag
	TencentCaptchaCloudSecretKeyConfigured bool     `json:"tencent_captcha_cloud_secret_key_configured"` // cloud signing secret presence flag
	TencentCaptchaRegion                   string   `json:"tencent_captcha_region"`                      // cn or intl verification region
	AliyunCaptchaEnabled                   bool     `json:"aliyun_captcha_enabled"`                      // Aliyun challenge verification switch
	AliyunCaptchaAccessKeyID               string   `json:"aliyun_captcha_access_key_id"`                // Aliyun verification credential identifier
	AliyunCaptchaAccessKeySecretConfigured bool     `json:"aliyun_captcha_access_key_secret_configured"` // Aliyun secret presence display flag
	AliyunCaptchaSceneID                   string   `json:"aliyun_captcha_scene_id"`                     // Aliyun CAPTCHA scene identifier
	AliyunCaptchaPrefix                    string   `json:"aliyun_captcha_prefix"`                       // Aliyun client widget resource prefix
	AliyunCaptchaRegion                    string   `json:"aliyun_captcha_region"`                       // cn or sgp verification region
	APIKeyACLTrustForwardedIP              bool     `json:"api_key_acl_trust_forwarded_ip"`              // API-key forwarded-address trust switch
	ForwardedClientIPHeaders               []string `json:"forwarded_client_ip_headers"`                 // ordered forwarded-address header names

	LinuxDoConnectEnabled                bool   `json:"linuxdo_connect_enabled"`                  // LinuxDo login availability switch
	LinuxDoConnectClientID               string `json:"linuxdo_connect_client_id"`                // LinuxDo registered OAuth client identifier
	LinuxDoConnectClientSecretConfigured bool   `json:"linuxdo_connect_client_secret_configured"` // LinuxDo token secret presence display flag
	LinuxDoConnectRedirectURL            string `json:"linuxdo_connect_redirect_url"`             // LinuxDo registered backend callback URL

	DingTalkConnectEnabled                 bool   `json:"dingtalk_connect_enabled"`                     // DingTalk login availability switch
	DingTalkConnectClientID                string `json:"dingtalk_connect_client_id"`                   // DingTalk registered OAuth client identifier
	DingTalkConnectClientSecretConfigured  bool   `json:"dingtalk_connect_client_secret_configured"`    // DingTalk token secret presence display flag
	DingTalkConnectRedirectURL             string `json:"dingtalk_connect_redirect_url"`                // DingTalk registered backend callback URL
	DingTalkConnectCorpRestrictionPolicy   string `json:"dingtalk_connect_corp_restriction_policy"`     // selected organization admission policy
	DingTalkConnectInternalCorpID          string `json:"dingtalk_connect_internal_corp_id"`            // configured corporation identifier text
	DingTalkConnectBypassRegistration      bool   `json:"dingtalk_connect_bypass_registration"`         // closed-registration organization bypass switch
	DingTalkConnectSyncCorpEmail           bool   `json:"dingtalk_connect_sync_corp_email"`             // enterprise email attribute synchronization
	DingTalkConnectSyncDisplayName         bool   `json:"dingtalk_connect_sync_display_name"`           // display name attribute synchronization
	DingTalkConnectSyncDept                bool   `json:"dingtalk_connect_sync_dept"`                   // department path attribute synchronization
	DingTalkConnectSyncCorpEmailAttrKey    string `json:"dingtalk_connect_sync_corp_email_attr_key"`    // enterprise email attribute key
	DingTalkConnectSyncDisplayNameAttrKey  string `json:"dingtalk_connect_sync_display_name_attr_key"`  // display name attribute key
	DingTalkConnectSyncDeptAttrKey         string `json:"dingtalk_connect_sync_dept_attr_key"`          // department path attribute key
	DingTalkConnectSyncCorpEmailAttrName   string `json:"dingtalk_connect_sync_corp_email_attr_name"`   // enterprise email attribute label
	DingTalkConnectSyncDisplayNameAttrName string `json:"dingtalk_connect_sync_display_name_attr_name"` // display name attribute label
	DingTalkConnectSyncDeptAttrName        string `json:"dingtalk_connect_sync_dept_attr_name"`         // department path attribute label

	WeChatConnectEnabled                   bool   `json:"wechat_connect_enabled"`                      // WeChat login availability switch
	WeChatConnectAppID                     string `json:"wechat_connect_app_id"`                       // legacy WeChat application identifier
	WeChatConnectAppSecretConfigured       bool   `json:"wechat_connect_app_secret_configured"`        // legacy secret presence display flag
	WeChatConnectOpenAppID                 string `json:"wechat_connect_open_app_id"`                  // open-platform WeChat application identifier
	WeChatConnectOpenAppSecretConfigured   bool   `json:"wechat_connect_open_app_secret_configured"`   // open-platform secret presence display flag
	WeChatConnectMPAppID                   string `json:"wechat_connect_mp_app_id"`                    // public-account WeChat application identifier
	WeChatConnectMPAppSecretConfigured     bool   `json:"wechat_connect_mp_app_secret_configured"`     // public-account secret presence display flag
	WeChatConnectMobileAppID               string `json:"wechat_connect_mobile_app_id"`                // mobile WeChat application identifier
	WeChatConnectMobileAppSecretConfigured bool   `json:"wechat_connect_mobile_app_secret_configured"` // mobile secret presence display flag
	WeChatConnectOpenEnabled               bool   `json:"wechat_connect_open_enabled"`                 // open-platform WeChat login switch
	WeChatConnectMPEnabled                 bool   `json:"wechat_connect_mp_enabled"`                   // public-account WeChat login switch
	WeChatConnectMobileEnabled             bool   `json:"wechat_connect_mobile_enabled"`               // mobile WeChat login switch
	WeChatConnectMode                      string `json:"wechat_connect_mode"`                         // open, mp, or mobile choice
	WeChatConnectScopes                    string `json:"wechat_connect_scopes"`                       // requested WeChat authorization scope text
	WeChatConnectRedirectURL               string `json:"wechat_connect_redirect_url"`                 // WeChat backend web callback URL
	WeChatConnectFrontendRedirectURL       string `json:"wechat_connect_frontend_redirect_url"`        // WeChat frontend authorization completion destination

	OIDCConnectEnabled                bool   `json:"oidc_connect_enabled"`                  // OIDC login availability switch
	OIDCConnectProviderName           string `json:"oidc_connect_provider_name"`            // source login provider display name
	OIDCConnectClientID               string `json:"oidc_connect_client_id"`                // OIDC registered OAuth client identifier
	OIDCConnectClientSecretConfigured bool   `json:"oidc_connect_client_secret_configured"` // OIDC token secret presence display flag
	OIDCConnectIssuerURL              string `json:"oidc_connect_issuer_url"`               // expected signed token issuer URL
	OIDCConnectDiscoveryURL           string `json:"oidc_connect_discovery_url"`            // provider endpoint discovery document URL
	OIDCConnectAuthorizeURL           string `json:"oidc_connect_authorize_url"`            // browser authorization endpoint URL
	OIDCConnectTokenURL               string `json:"oidc_connect_token_url"`                // authorization code exchange endpoint URL
	OIDCConnectUserInfoURL            string `json:"oidc_connect_userinfo_url"`             // optional authenticated profile endpoint URL
	OIDCConnectJWKSURL                string `json:"oidc_connect_jwks_url"`                 // token signature verification key URL
	OIDCConnectScopes                 string `json:"oidc_connect_scopes"`                   // requested authorization scope text
	OIDCConnectRedirectURL            string `json:"oidc_connect_redirect_url"`             // OIDC registered backend callback URL
	OIDCConnectFrontendRedirectURL    string `json:"oidc_connect_frontend_redirect_url"`    // OIDC frontend authorization completion destination
	OIDCConnectTokenAuthMethod        string `json:"oidc_connect_token_auth_method"`        // token endpoint client authentication method
	OIDCConnectUsePKCE                bool   `json:"oidc_connect_use_pkce"`                 // authorization proof-key enablement switch
	OIDCConnectValidateIDToken        bool   `json:"oidc_connect_validate_id_token"`        // signed identity token validation switch
	OIDCConnectAllowedSigningAlgs     string `json:"oidc_connect_allowed_signing_algs"`     // accepted comma-separated token algorithms
	OIDCConnectClockSkewSeconds       int    `json:"oidc_connect_clock_skew_seconds"`       // seconds
	OIDCConnectRequireEmailVerified   bool   `json:"oidc_connect_require_email_verified"`   // new-identity verified-email requirement
	OIDCConnectUserInfoEmailPath      string `json:"oidc_connect_userinfo_email_path"`      // preferred userinfo email field path
	OIDCConnectUserInfoIDPath         string `json:"oidc_connect_userinfo_id_path"`         // preferred userinfo subject field path
	OIDCConnectUserInfoUsernamePath   string `json:"oidc_connect_userinfo_username_path"`   // preferred userinfo username field path

	GitHubOAuthEnabled                bool   `json:"github_oauth_enabled"`                  // GitHub login availability switch
	GitHubOAuthClientID               string `json:"github_oauth_client_id"`                // GitHub registered OAuth client identifier
	GitHubOAuthClientSecretConfigured bool   `json:"github_oauth_client_secret_configured"` // GitHub token secret presence display flag
	GitHubOAuthRedirectURL            string `json:"github_oauth_redirect_url"`             // GitHub registered backend callback URL
	GitHubOAuthFrontendRedirectURL    string `json:"github_oauth_frontend_redirect_url"`    // GitHub frontend authorization completion destination
	GoogleOAuthEnabled                bool   `json:"google_oauth_enabled"`                  // Google login availability switch
	GoogleOneTapEnabled               bool   `json:"google_one_tap_enabled"`                // Google One Tap credential switch
	GoogleOAuthClientID               string `json:"google_oauth_client_id"`                // Google registered OAuth client identifier
	GoogleOAuthClientSecretConfigured bool   `json:"google_oauth_client_secret_configured"` // Google token secret presence display flag
	GoogleOAuthRedirectURL            string `json:"google_oauth_redirect_url"`             // Google registered backend callback URL
	GoogleOAuthFrontendRedirectURL    string `json:"google_oauth_frontend_redirect_url"`    // Google frontend authorization completion destination

	LocalizedSettings           locale.TextUpdates              `json:"localized_settings"`              // localized operational text editor updates
	SiteTexts                   sitedto.LocalizedTexts          `json:"site_texts"`                      // localized site text editor updates
	DefaultLocale               string                          `json:"default_locale"`                  // default user-facing language code
	SiteName                    string                          `json:"site_name"`                       // source site display name
	SiteLogo                    string                          `json:"site_logo"`                       // site logo reference text
	SiteSubtitle                string                          `json:"site_subtitle"`                   // source site subtitle text
	APIBaseURL                  string                          `json:"api_base_url"`                    // displayed gateway API address
	ContactInfo                 string                          `json:"contact_info"`                    // source administrator contact text
	DocURL                      string                          `json:"doc_url"`                         // source documentation destination text
	HomeContent                 string                          `json:"home_content"`                    // source custom homepage content
	HideCcsImportButton         bool                            `json:"hide_ccs_import_button"`          // CCS import control visibility switch
	PurchaseSubscriptionEnabled bool                            `json:"purchase_subscription_enabled"`   // subscription purchase entry switch
	PurchaseSubscriptionURL     string                          `json:"purchase_subscription_url"`       // subscription purchase destination address
	TableDefaultPageSize        int                             `json:"table_default_page_size"`         // default table row count
	TablePageSizeOptions        []int                           `json:"table_page_size_options"`         // selectable table row counts
	UsageRankingLimit           int                             `json:"usage_ranking_limit"`             // maximum user ranking row count
	UsageRankingEnabled         bool                            `json:"usage_ranking_enabled"`           // user usage ranking availability switch
	UsageRankingSortBy          string                          `json:"usage_ranking_sort_by"`           // selected user ranking metric
	UsageRankingShowTotalTokens bool                            `json:"usage_ranking_show_total_tokens"` // ranking token metric visibility
	UsageRankingShowRequests    bool                            `json:"usage_ranking_show_requests"`     // ranking request metric visibility
	UsageRankingShowActualCost  bool                            `json:"usage_ranking_show_actual_cost"`  // ranking cost metric visibility
	CustomMenuItems             []sitedto.CustomMenuItem        `json:"custom_menu_items"`               // custom administrator menu entries
	CustomEndpoints             []sitedto.CustomEndpoint        `json:"custom_endpoints"`                // custom endpoint description entries
	FooterLinks                 []sitedto.FooterLinkGroup       `json:"footer_links"`                    // footer groups and link entries
	FooterText                  string                          `json:"footer_text"`                     // source footer display text
	HomeFeaturedModels          []string                        `json:"home_featured_models"`            // ordered featured model identifier list
	CreativeModelSettings       []creative.CreativeModelSetting `json:"creative_model_settings"`         // Creative group/model operation selections
	CreativeWorkerCount         int                             `json:"creative_worker_count"`           // configured Creative task worker count

	DefaultConcurrency                   int                                      `json:"default_concurrency"`                     // default registration concurrency allowance
	DefaultBalance                       float64                                  `json:"default_balance"`                         // default registration balance grant amount
	BStatsEnabled                        bool                                     `json:"bstats_enabled"`                          // reporting enablement control
	BStatsLogErrors                      bool                                     `json:"bstats_log_errors"`                       // report failure logging
	BStatsLogSentData                    bool                                     `json:"bstats_log_sent_data"`                    // outgoing payload logging
	BStatsLogResponseStatusText          bool                                     `json:"bstats_log_response_status_text"`         // received response logging
	TeamEnabled                          bool                                     `json:"team_enabled"`                            // team runtime availability switch
	CreativeEnabled                      bool                                     `json:"creative_enabled"`                        // Creative Studio runtime access switch
	RiskControlEnabled                   bool                                     `json:"risk_control_enabled"`                    // risk-center and auditing runtime switch
	CyberSessionBlockEnabled             bool                                     `json:"cyber_session_block_enabled"`             // cyber-session blocking runtime switch
	CyberSessionBlockTTLSeconds          int                                      `json:"cyber_session_block_ttl_seconds"`         // seconds
	AffiliateEnabled                     bool                                     `json:"affiliate_enabled"`                       // affiliate program availability switch
	AffiliateRebateRate                  float64                                  `json:"affiliate_rebate_rate"`                   // affiliate order rebate percentage
	AffiliateRebateFreezeHours           int                                      `json:"affiliate_rebate_freeze_hours"`           // hours
	AffiliateRebateDurationDays          int                                      `json:"affiliate_rebate_duration_days"`          // days
	AffiliateRebatePerInviteeCap         float64                                  `json:"affiliate_rebate_per_invitee_cap"`        // maximum rebate per invited user
	AdminRechargeRebateEnabled           bool                                     `json:"affiliate_admin_recharge_enabled"`        // administrator recharge rebate switch
	DefaultUserRPMLimit                  int                                      `json:"default_user_rpm_limit"`                  // default user request-per-minute allowance
	DefaultUserAPIKeyLimit               int                                      `json:"default_user_api_key_limit"`              // default user credential-count allowance
	DefaultSubscriptions                 []billinghttp.DefaultSubscriptionSetting `json:"default_subscriptions"`                   // default granted subscription plan selections
	BalanceUnitName                      string                                   `json:"balance_unit_name"`                       // source balance display unit name
	BalanceUnitSymbol                    string                                   `json:"balance_unit_symbol"`                     // balance display currency symbol
	BalanceIconSVG                       string                                   `json:"balance_icon_svg"`                        // balance display SVG markup
	ReasoningPointRMBUnitPrice           float64                                  `json:"reasoning_point_rmb_unit_price"`          // reasoning-point unit price in RMB
	USDExchangeRate                      float64                                  `json:"usd_exchange_rate"`                       // RMB amount per USD conversion
	MarketplaceAvailabilityWindowDays    int                                      `json:"marketplace_availability_window_days"`    // days
	MarketplaceAvailabilityBucketMinutes int                                      `json:"marketplace_availability_bucket_minutes"` // minutes

	EnableModelFallback      bool   `json:"enable_model_fallback"`      // global fallback model selection switch
	FallbackModelAnthropic   string `json:"fallback_model_anthropic"`   // Anthropic fallback model identifier
	FallbackModelOpenAI      string `json:"fallback_model_openai"`      // OpenAI fallback model identifier
	FallbackModelGemini      string `json:"fallback_model_gemini"`      // Gemini fallback model identifier
	FallbackModelAntigravity string `json:"fallback_model_antigravity"` // Antigravity fallback model identifier

	GrokDefaultTextModel   string `json:"grok_default_text_model"`    // default model for model-omitted requests
	GrokDefaultBaseURLMode string `json:"grok_default_base_url_mode"` // unconfigured Grok endpoint selection mode

	EnableIdentityPatch bool   `json:"enable_identity_patch"` // Claude-to-Gemini identity patch switch
	IdentityPatchPrompt string `json:"identity_patch_prompt"` // configured identity replacement prompt

	// OpsMonitoringEnabled is intersected with installed monitoring availability
	// by GetSettings. UpdateSettings reports the readback runtime switch directly
	// after its committed application path.
	OpsMonitoringEnabled         bool `json:"ops_monitoring_enabled"`          // Ops monitoring runtime switch
	OpsRealtimeMonitoringEnabled bool `json:"ops_realtime_monitoring_enabled"` // Ops realtime collector runtime switch
	OpsMetricsIntervalSeconds    int  `json:"ops_metrics_interval_seconds"`    // seconds

	MinClaudeCodeVersion string `json:"min_claude_code_version"` // minimum admitted Claude Code version
	MaxClaudeCodeVersion string `json:"max_claude_code_version"` // maximum admitted Claude Code version

	BackendModeEnabled bool `json:"backend_mode_enabled"` // administrator panel restriction switch

	TTFTMode                               string                                    `json:"ttft_mode"`                                   // global upstream latency selection
	EnableFingerprintUnification           bool                                      `json:"enable_fingerprint_unification"`              // OAuth fingerprint header normalization switch
	EnableMetadataPassthrough              bool                                      `json:"enable_metadata_passthrough"`                 // client metadata forwarding switch
	EnableCCHSigning                       bool                                      `json:"enable_cch_signing"`                          // deprecated CCH signing setting
	EnableClaudeOAuthSystemPromptInjection bool                                      `json:"enable_claude_oauth_system_prompt_injection"` // Claude OAuth system-block injection switch
	ClaudeOAuthSystemPrompt                string                                    `json:"claude_oauth_system_prompt"`                  // source Claude OAuth extension prompt
	ClaudeOAuthSystemPromptBlocks          string                                    `json:"claude_oauth_system_prompt_blocks"`           // configured Claude OAuth system-block JSON
	EnableAnthropicCacheTTL1hInjection     bool                                      `json:"enable_anthropic_cache_ttl_1h_injection"`     // Anthropic one-hour cache TTL switch
	RewriteMessageCacheControl             bool                                      `json:"rewrite_message_cache_control"`               // message cache-control rewriting switch
	EnableClientDatelineNormalization      bool                                      `json:"enable_client_dateline_normalization"`        // Anthropic dateline normalization switch
	AntigravityUserAgentVersion            string                                    `json:"antigravity_user_agent_version"`              // configured Antigravity user-agent version
	OpenAICodexUserAgent                   string                                    `json:"openai_codex_user_agent"`                     // configured full Codex user-agent text
	OpenAIAllowClaudeCodeCodexPlugin       bool                                      `json:"openai_allow_claude_code_codex_plugin"`       // Claude Code Codex plugin admission
	UserPromptReplacementConfig            *promptpolicy.UserPromptReplacementConfig `json:"user_prompt_replacement_config"`              // administrator prompt replacement rule object

	WebSearchEmulationEnabled bool `json:"web_search_emulation_enabled"` // gateway search tool emulation switch

	PaymentVisibleMethodAlipaySource  string `json:"payment_visible_method_alipay_source"`  // Alipay instance routing source choice
	PaymentVisibleMethodWxpaySource   string `json:"payment_visible_method_wxpay_source"`   // WeChat Pay instance routing source
	PaymentVisibleMethodAlipayEnabled bool   `json:"payment_visible_method_alipay_enabled"` // Alipay purchase-method visibility switch
	PaymentVisibleMethodWxpayEnabled  bool   `json:"payment_visible_method_wxpay_enabled"`  // WeChat Pay purchase-method visibility switch

	// AdvancedScheduler fields expose configured text beside the separate
	// AdvancedSchedulerEffective values returned by the scheduler reader. These
	// values describe parameter selection; groups choose their own scheduler type.
	AdvancedSchedulerStickyWeightedEnabled           bool                                        `json:"advanced_scheduler_sticky_weighted_enabled"`            // sticky result weighting switch
	AdvancedSchedulerSubscriptionPriorityEnabled     bool                                        `json:"advanced_scheduler_subscription_priority_enabled"`      // subscription priority preference switch
	AdvancedSchedulerEWMAErrorRateAlpha              string                                      `json:"advanced_scheduler_ewma_error_rate_alpha"`              // configured error-rate smoothing coefficient
	AdvancedSchedulerEWMATTFTAlpha                   string                                      `json:"advanced_scheduler_ewma_ttft_alpha"`                    // configured TTFT smoothing coefficient
	AdvancedSchedulerStickyEscapeEnabled             bool                                        `json:"advanced_scheduler_sticky_escape_enabled"`              // configured sticky escape enablement
	AdvancedSchedulerStickyEscapeTTFTMs              string                                      `json:"advanced_scheduler_sticky_escape_ttft_ms"`              // configured TTFT escape milliseconds
	AdvancedSchedulerStickyEscapeErrorRate           string                                      `json:"advanced_scheduler_sticky_escape_error_rate"`           // configured error-rate escape threshold
	AdvancedSchedulerLBTopK                          string                                      `json:"advanced_scheduler_lb_top_k"`                           // configured load-balanced candidate count
	AdvancedSchedulerWeightPriority                  string                                      `json:"advanced_scheduler_weight_priority"`                    // configured provider priority score weight
	AdvancedSchedulerWeightLoad                      string                                      `json:"advanced_scheduler_weight_load"`                        // configured provider load score weight
	AdvancedSchedulerWeightQueue                     string                                      `json:"advanced_scheduler_weight_queue"`                       // configured wait queue score weight
	AdvancedSchedulerWeightErrorRate                 string                                      `json:"advanced_scheduler_weight_error_rate"`                  // configured observed error rate score weight
	AdvancedSchedulerWeightTTFT                      string                                      `json:"advanced_scheduler_weight_ttft"`                        // configured observed first-token time score weight
	AdvancedSchedulerWeightReset                     string                                      `json:"advanced_scheduler_weight_reset"`                       // configured quota reset score weight
	AdvancedSchedulerWeightQuotaHeadroom             string                                      `json:"advanced_scheduler_weight_quota_headroom"`              // configured remaining quota score weight
	AdvancedSchedulerWeightPreviousResponse          string                                      `json:"advanced_scheduler_weight_previous_response"`           // configured previous response affinity score weight
	AdvancedSchedulerWeightSessionSticky             string                                      `json:"advanced_scheduler_weight_session_sticky"`              // configured session affinity score weight
	AdvancedSchedulerEffectiveLBTopK                 string                                      `json:"advanced_scheduler_effective_lb_top_k"`                 // resolved load-balanced candidate count
	AdvancedSchedulerEffectiveWeightPriority         string                                      `json:"advanced_scheduler_effective_weight_priority"`          // resolved provider priority score weight
	AdvancedSchedulerEffectiveWeightLoad             string                                      `json:"advanced_scheduler_effective_weight_load"`              // resolved provider load score weight
	AdvancedSchedulerEffectiveWeightQueue            string                                      `json:"advanced_scheduler_effective_weight_queue"`             // resolved wait queue score weight
	AdvancedSchedulerEffectiveWeightErrorRate        string                                      `json:"advanced_scheduler_effective_weight_error_rate"`        // resolved observed error rate score weight
	AdvancedSchedulerEffectiveWeightTTFT             string                                      `json:"advanced_scheduler_effective_weight_ttft"`              // resolved observed first-token time score weight
	AdvancedSchedulerEffectiveWeightReset            string                                      `json:"advanced_scheduler_effective_weight_reset"`             // resolved quota reset score weight
	AdvancedSchedulerEffectiveWeightQuotaHeadroom    string                                      `json:"advanced_scheduler_effective_weight_quota_headroom"`    // resolved remaining quota score weight
	AdvancedSchedulerEffectiveWeightPreviousResponse string                                      `json:"advanced_scheduler_effective_weight_previous_response"` // resolved previous response affinity score weight
	AdvancedSchedulerEffectiveWeightSessionSticky    string                                      `json:"advanced_scheduler_effective_weight_session_sticky"`    // resolved session affinity score weight
	AdvancedSchedulerEffectiveEWMAErrorRateAlpha     string                                      `json:"advanced_scheduler_effective_ewma_error_rate_alpha"`    // resolved error-rate smoothing coefficient
	AdvancedSchedulerEffectiveEWMATTFTAlpha          string                                      `json:"advanced_scheduler_effective_ewma_ttft_alpha"`          // resolved TTFT smoothing coefficient
	AdvancedSchedulerEffectiveStickyEscapeEnabled    bool                                        `json:"advanced_scheduler_effective_sticky_escape_enabled"`    // resolved sticky escape enablement
	AdvancedSchedulerEffectiveStickyEscapeTTFTMs     string                                      `json:"advanced_scheduler_effective_sticky_escape_ttft_ms"`    // resolved TTFT escape milliseconds
	AdvancedSchedulerEffectiveStickyEscapeErrorRate  string                                      `json:"advanced_scheduler_effective_sticky_escape_error_rate"` // resolved error-rate escape threshold
	OpenAIQuotaAutoPauseSettings                     ops.OpsOpenAIProviderQuotaAutoPauseSettings `json:"openai_provider_quota_auto_pause"`                      // global OpenAI quota pause thresholds

	PaymentEnabled                   bool                      `json:"payment_enabled"`                      // payment system availability switch
	PaymentMinAmount                 float64                   `json:"payment_min_amount"`                   // minimum recharge payment amount
	PaymentMaxAmount                 float64                   `json:"payment_max_amount"`                   // maximum recharge payment amount
	PaymentDailyLimit                float64                   `json:"payment_daily_limit"`                  // per-user daily recharge allowance
	PaymentOrderTimeoutMin           int                       `json:"payment_order_timeout_minutes"`        // minutes
	PaymentMaxPendingOrders          int                       `json:"payment_max_pending_orders"`           // per-user pending payment order allowance
	PaymentEnabledTypes              []string                  `json:"payment_enabled_types"`                // enabled payment method type list
	PaymentBalanceDisabled           bool                      `json:"payment_balance_disabled"`             // subscription balance-payment disable switch
	PaymentBalanceRechargeMultiplier float64                   `json:"payment_balance_recharge_multiplier"`  // recharge balance grant multiplier
	PaymentSubscriptionUSDToCNYRate  float64                   `json:"payment_subscription_usd_to_cny_rate"` // subscription USD-to-CNY conversion rate
	PaymentRechargeFeeRate           float64                   `json:"payment_recharge_fee_rate"`            // global recharge fee percentage
	PaymentMethodFees                payment.MethodFeeSettings `json:"payment_method_fees"`                  // per-method recharge fee settings
	PaymentLoadBalanceStrat          string                    `json:"payment_load_balance_strategy"`        // payment instance selection strategy
	PaymentProductNamePrefix         string                    `json:"payment_product_name_prefix"`          // source payment product-name prefix
	PaymentProductNameSuffix         string                    `json:"payment_product_name_suffix"`          // source payment product-name suffix
	PaymentHelpImageURL              string                    `json:"payment_help_image_url"`               // source payment help image reference
	PaymentHelpText                  string                    `json:"payment_help_text"`                    // source payment help display text

	PaymentCancelRateLimitEnabled bool   `json:"payment_cancel_rate_limit_enabled"`     // payment cancellation throttle switch
	PaymentCancelRateLimitMax     int    `json:"payment_cancel_rate_limit_max"`         // allowed cancellations per selected window
	PaymentCancelRateLimitWindow  int    `json:"payment_cancel_rate_limit_window"`      // cancellation window length value
	PaymentCancelRateLimitUnit    string `json:"payment_cancel_rate_limit_unit"`        // minute or hour window unit
	PaymentCancelRateLimitMode    string `json:"payment_cancel_rate_limit_window_mode"` // fixed or sliding window mode

	PaymentAlipayForceQRCode             bool `json:"payment_alipay_force_qrcode"`               // mobile Alipay QR-code preference
	PaymentAlipayMobilePrecreateDeepLink bool `json:"payment_alipay_mobile_precreate_deep_link"` // mobile Alipay precreation link preference

	BalanceLowNotifyEnabled         bool                           `json:"balance_low_notify_enabled"`         // low-balance mail notification switch
	BalanceLowNotifyThreshold       float64                        `json:"balance_low_notify_threshold"`       // low-balance mail trigger amount
	BalanceLowNotifyRechargeURL     string                         `json:"balance_low_notify_recharge_url"`    // source low-balance recharge destination
	SubscriptionExpiryNotifyEnabled bool                           `json:"subscription_expiry_notify_enabled"` // subscription expiry mail notification switch
	ProviderQuotaNotifyEnabled      bool                           `json:"provider_quota_notify_enabled"`      // provider quota mail notification switch
	ProviderQuotaNotifyEmails       []identitydto.NotifyEmailEntry `json:"provider_quota_notify_emails"`       // provider quota mail recipient records

	OpenAIFastPolicySettings *gatewaydto.OpenAIFastPolicySettings `json:"openai_fast_policy_settings,omitempty"` // OpenAI service-tier matching rule object

	// ProviderSchedulingThresholds is filled by GetSettings from the Snapshot
	// map and omitted when nil. The shared response builder leaves it unset,
	// so UpdateSettings does not populate that map in its response.
	ProviderSchedulingThresholds map[string]int `json:"provider_scheduling_thresholds,omitempty"` // platform provider pause threshold percentages

	AllowUserViewErrorRequests bool `json:"allow_user_view_error_requests"` // own failed-request visibility switch
}
