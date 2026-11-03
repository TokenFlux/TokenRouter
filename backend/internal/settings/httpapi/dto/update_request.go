package dto

import (
	"encoding/json"

	billinghttp "github.com/TokenFlux/TokenRouter/internal/billing/httpapi"
	"github.com/TokenFlux/TokenRouter/internal/creative"
	gatewaydto "github.com/TokenFlux/TokenRouter/internal/gateway/httpapi/dto"
	"github.com/TokenFlux/TokenRouter/internal/gateway/promptpolicy"
	identitydto "github.com/TokenFlux/TokenRouter/internal/identity/httpapi/dto"
	"github.com/TokenFlux/TokenRouter/internal/payment"
	"github.com/TokenFlux/TokenRouter/internal/pkg/locale"
	"github.com/TokenFlux/TokenRouter/internal/provider"
	sitedto "github.com/TokenFlux/TokenRouter/internal/site/httpapi/dto"
)

// update_request.go defines the submitted JSON fields for combined
// administrator settings updates.
// Combined settings requests retain field presence alongside their decoded Go
// values. The handler reads a raw JSON map before binding this struct, filters
// omitted non-pointer fields after normalization, and merges many pointer fields
// with the prior Snapshot. A false, zero, empty text, or empty collection can
// therefore carry a submitted update when its owning field permits that value.
//
// JSON null and omission both yield nil for pointers. Registered participants
// receive JSON regenerated from normalized fields; ResponsesWS carries its
// submitted JSON value through that encoding. Security, collection, and payment
// fields have consumer-specific merging and validation rules, while empty secret
// text commonly keeps saved credentials. The handler prepares all module changes
// before committing the resulting batch and then builds its response from saved
// values.

// UpdateSettingsRequest is the decoded flat input used with a separate raw-presence map.
// Pointer nil values can represent omission or JSON null, and UpdateSettings
// merges them according to each owning module's preparation rules.
type UpdateSettingsRequest struct {
	// ResponsesWS retains submitted JSON bytes, including null, while omission
	// leaves a nil value. The registered WebSocket participant validates supplied
	// objects and combines them with its deployment defaults.
	ResponsesWS                      json.RawMessage `json:"responses_ws,omitempty"`              // raw Responses WebSocket override JSON
	RegistrationEnabled              bool            `json:"registration_enabled"`                // new account registration switch
	EmailVerifyEnabled               bool            `json:"email_verify_enabled"`                // registration email verification switch
	RegistrationEmailSuffixWhitelist []string        `json:"registration_email_suffix_whitelist"` // allowed registration email domain suffixes
	RegistrationEmailNormalization   bool            `json:"registration_email_normalization"`    // registration mailbox normalization switch
	// RegistrationEmailDomainQuotaEnabled and UserEmailChangeEnabled merge with
	// the prior Snapshot when nil. A nonnil false is a supplied switch value,
	// while non-pointer fields use the handler's raw-presence filtering.
	RegistrationEmailDomainQuotaEnabled *bool  `json:"registration_email_domain_quota_enabled"` // outside-whitelist registration quota switch
	UserEmailChangeEnabled              *bool  `json:"user_email_change_enabled"`               // primary email replacement policy switch
	PromoCodeEnabled                    bool   `json:"promo_code_enabled"`                      // registration promotional code switch
	PasswordResetEnabled                bool   `json:"password_reset_enabled"`                  // password recovery policy switch
	FrontendURL                         string `json:"frontend_url"`                            // notification and redirect frontend address
	InvitationCodeEnabled               bool   `json:"invitation_code_enabled"`                 // registration invitation requirement switch
	TotpEnabled                         bool   `json:"totp_enabled"`                            // TOTP authentication availability switch
	// SessionBindingEnabled and StepUpEnabled retain prior switches when nil.
	// Enabling step-up requires the actor's current TOTP session, and disabling an
	// existing step-up switch requires its unconditional verification gate.
	SessionBindingEnabled   *bool  `json:"session_binding_enabled"`    // IP and user-agent session checks
	StepUpEnabled           *bool  `json:"step_up_enabled"`            // sensitive operation verification switch
	AuditLogRetentionDays   int    `json:"audit_log_retention_days"`   // days
	LoginAgreementEnabled   bool   `json:"login_agreement_enabled"`    // login agreement acceptance requirement
	LoginAgreementMode      string `json:"login_agreement_mode"`       // modal or checkbox consent mode
	LoginAgreementUpdatedAt string `json:"login_agreement_updated_at"` // administrator agreement update marker
	// LoginAgreementDocuments is converted to selected source records before
	// validation. An empty converted list retains prior documents, including a
	// supplied [] or records with no selected title/body.
	LoginAgreementDocuments []sitedto.LoginAgreementDocument `json:"login_agreement_documents"` // editable login agreement document records

	SMTPHost     string `json:"smtp_host"`       // outgoing mail server hostname
	SMTPPort     int    `json:"smtp_port"`       // SMTP connection port number
	SMTPUsername string `json:"smtp_username"`   // outgoing mail authentication username
	SMTPPassword string `json:"smtp_password"`   // internal SMTP authentication secret
	SMTPFrom     string `json:"smtp_from_email"` // outgoing mail sender address
	SMTPFromName string `json:"smtp_from_name"`  // outgoing mail sender display name
	SMTPUseTLS   bool   `json:"smtp_use_tls"`    // SMTP secure connection preference

	TurnstileEnabled   bool   `json:"turnstile_enabled"`    // Cloudflare challenge verification switch
	TurnstileSiteKey   string `json:"turnstile_site_key"`   // public Turnstile widget key
	TurnstileSecretKey string `json:"turnstile_secret_key"` // internal Turnstile verification secret

	TencentCaptchaEnabled        bool   `json:"tencent_captcha_enabled"`          // Tencent challenge verification switch
	TencentCaptchaAppID          string `json:"tencent_captcha_app_id"`           // Tencent CAPTCHA application identifier
	TencentCaptchaAppSecretKey   string `json:"tencent_captcha_app_secret_key"`   // Tencent application verification secret
	TencentCaptchaCloudSecretID  string `json:"tencent_captcha_cloud_secret_id"`  // Tencent cloud credential identifier
	TencentCaptchaCloudSecretKey string `json:"tencent_captcha_cloud_secret_key"` // Tencent cloud signing secret
	TencentCaptchaRegion         string `json:"tencent_captcha_region"`           // cn or intl verification region

	AliyunCaptchaEnabled         bool   `json:"aliyun_captcha_enabled"`           // Aliyun challenge verification switch
	AliyunCaptchaAccessKeyID     string `json:"aliyun_captcha_access_key_id"`     // Aliyun verification credential identifier
	AliyunCaptchaAccessKeySecret string `json:"aliyun_captcha_access_key_secret"` // Aliyun verification credential secret
	AliyunCaptchaSceneID         string `json:"aliyun_captcha_scene_id"`          // Aliyun CAPTCHA scene identifier
	AliyunCaptchaPrefix          string `json:"aliyun_captcha_prefix"`            // Aliyun client widget resource prefix
	AliyunCaptchaRegion          string `json:"aliyun_captcha_region"`            // cn or sgp verification region

	// APIKeyACLTrustForwardedIP and ForwardedClientIPHeaders are normalized as
	// one forwarded-address configuration. Nil inputs use prior readback; unsafe
	// stored header state can be rewritten to disabled and empty even when omitted.
	APIKeyACLTrustForwardedIP *bool     `json:"api_key_acl_trust_forwarded_ip"` // API-key forwarded-address trust switch
	ForwardedClientIPHeaders  *[]string `json:"forwarded_client_ip_headers"`    // ordered forwarded-address header names

	LinuxDoConnectEnabled      bool   `json:"linuxdo_connect_enabled"`       // LinuxDo login availability switch
	LinuxDoConnectClientID     string `json:"linuxdo_connect_client_id"`     // LinuxDo registered OAuth client identifier
	LinuxDoConnectClientSecret string `json:"linuxdo_connect_client_secret"` // LinuxDo internal token exchange secret
	LinuxDoConnectRedirectURL  string `json:"linuxdo_connect_redirect_url"`  // LinuxDo registered backend callback URL

	DingTalkConnectEnabled                 bool   `json:"dingtalk_connect_enabled"`                     // DingTalk login availability switch
	DingTalkConnectClientID                string `json:"dingtalk_connect_client_id"`                   // DingTalk registered OAuth client identifier
	DingTalkConnectClientSecret            string `json:"dingtalk_connect_client_secret"`               // DingTalk internal token exchange secret
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

	WeChatConnectEnabled             bool   `json:"wechat_connect_enabled"`               // WeChat login availability switch
	WeChatConnectAppID               string `json:"wechat_connect_app_id"`                // legacy WeChat application identifier
	WeChatConnectAppSecret           string `json:"wechat_connect_app_secret"`            // legacy WeChat exchange secret
	WeChatConnectOpenAppID           string `json:"wechat_connect_open_app_id"`           // open-platform WeChat application identifier
	WeChatConnectOpenAppSecret       string `json:"wechat_connect_open_app_secret"`       // open-platform WeChat exchange secret
	WeChatConnectMPAppID             string `json:"wechat_connect_mp_app_id"`             // public-account WeChat application identifier
	WeChatConnectMPAppSecret         string `json:"wechat_connect_mp_app_secret"`         // public-account WeChat exchange secret
	WeChatConnectMobileAppID         string `json:"wechat_connect_mobile_app_id"`         // mobile WeChat application identifier
	WeChatConnectMobileAppSecret     string `json:"wechat_connect_mobile_app_secret"`     // mobile WeChat exchange secret
	WeChatConnectOpenEnabled         bool   `json:"wechat_connect_open_enabled"`          // open-platform WeChat login switch
	WeChatConnectMPEnabled           bool   `json:"wechat_connect_mp_enabled"`            // public-account WeChat login switch
	WeChatConnectMobileEnabled       bool   `json:"wechat_connect_mobile_enabled"`        // mobile WeChat login switch
	WeChatConnectMode                string `json:"wechat_connect_mode"`                  // open, mp, or mobile choice
	WeChatConnectScopes              string `json:"wechat_connect_scopes"`                // requested WeChat authorization scope text
	WeChatConnectRedirectURL         string `json:"wechat_connect_redirect_url"`          // WeChat backend web callback URL
	WeChatConnectFrontendRedirectURL string `json:"wechat_connect_frontend_redirect_url"` // WeChat frontend authorization completion destination

	OIDCConnectEnabled             bool   `json:"oidc_connect_enabled"`               // OIDC login availability switch
	OIDCConnectProviderName        string `json:"oidc_connect_provider_name"`         // source login provider display name
	OIDCConnectClientID            string `json:"oidc_connect_client_id"`             // OIDC registered OAuth client identifier
	OIDCConnectClientSecret        string `json:"oidc_connect_client_secret"`         // OIDC internal token exchange secret
	OIDCConnectIssuerURL           string `json:"oidc_connect_issuer_url"`            // expected signed token issuer URL
	OIDCConnectDiscoveryURL        string `json:"oidc_connect_discovery_url"`         // provider endpoint discovery document URL
	OIDCConnectAuthorizeURL        string `json:"oidc_connect_authorize_url"`         // browser authorization endpoint URL
	OIDCConnectTokenURL            string `json:"oidc_connect_token_url"`             // authorization code exchange endpoint URL
	OIDCConnectUserInfoURL         string `json:"oidc_connect_userinfo_url"`          // optional authenticated profile endpoint URL
	OIDCConnectJWKSURL             string `json:"oidc_connect_jwks_url"`              // token signature verification key URL
	OIDCConnectScopes              string `json:"oidc_connect_scopes"`                // requested authorization scope text
	OIDCConnectRedirectURL         string `json:"oidc_connect_redirect_url"`          // OIDC registered backend callback URL
	OIDCConnectFrontendRedirectURL string `json:"oidc_connect_frontend_redirect_url"` // OIDC frontend authorization completion destination
	OIDCConnectTokenAuthMethod     string `json:"oidc_connect_token_auth_method"`     // token endpoint client authentication method
	// OIDCConnectUsePKCE and OIDCConnectValidateIDToken use presence-aware security
	// write defaults before enabled-provider validation. The enabled handler branch
	// applies nonnil overrides, including false; the other branch retains those
	// compatibility defaults even when pointer values were submitted.
	OIDCConnectUsePKCE            *bool  `json:"oidc_connect_use_pkce"`             // authorization proof-key enablement switch
	OIDCConnectValidateIDToken    *bool  `json:"oidc_connect_validate_id_token"`    // signed identity token validation switch
	OIDCConnectAllowedSigningAlgs string `json:"oidc_connect_allowed_signing_algs"` // accepted comma-separated token algorithms
	// OIDCConnectClockSkewSeconds uses a saved value when zero in the enabled
	// handler branch, then 120 when both are zero. The runtime settings reader
	// separately permits zero skew as a stored configuration value.
	OIDCConnectClockSkewSeconds     int    `json:"oidc_connect_clock_skew_seconds"`     // seconds
	OIDCConnectRequireEmailVerified bool   `json:"oidc_connect_require_email_verified"` // new-identity verified-email requirement
	OIDCConnectUserInfoEmailPath    string `json:"oidc_connect_userinfo_email_path"`    // preferred userinfo email field path
	OIDCConnectUserInfoIDPath       string `json:"oidc_connect_userinfo_id_path"`       // preferred userinfo subject field path
	OIDCConnectUserInfoUsernamePath string `json:"oidc_connect_userinfo_username_path"` // preferred userinfo username field path

	GitHubOAuthEnabled             bool   `json:"github_oauth_enabled"`               // GitHub login availability switch
	GitHubOAuthClientID            string `json:"github_oauth_client_id"`             // GitHub registered OAuth client identifier
	GitHubOAuthClientSecret        string `json:"github_oauth_client_secret"`         // GitHub internal token exchange secret
	GitHubOAuthRedirectURL         string `json:"github_oauth_redirect_url"`          // GitHub registered backend callback URL
	GitHubOAuthFrontendRedirectURL string `json:"github_oauth_frontend_redirect_url"` // GitHub frontend authorization completion destination
	GoogleOAuthEnabled             bool   `json:"google_oauth_enabled"`               // Google login availability switch
	GoogleOneTapEnabled            bool   `json:"google_one_tap_enabled"`             // Google One Tap credential switch
	GoogleOAuthClientID            string `json:"google_oauth_client_id"`             // Google registered OAuth client identifier
	GoogleOAuthClientSecret        string `json:"google_oauth_client_secret"`         // Google internal token exchange secret
	GoogleOAuthRedirectURL         string `json:"google_oauth_redirect_url"`          // Google registered backend callback URL
	GoogleOAuthFrontendRedirectURL string `json:"google_oauth_frontend_redirect_url"` // Google frontend authorization completion destination

	LocalizedSettings   locale.TextUpdates     `json:"localized_settings"`     // localized operational text editor updates
	SiteTexts           sitedto.LocalizedTexts `json:"site_texts"`             // localized site text editor updates
	DefaultLocale       string                 `json:"default_locale"`         // default user-facing language code
	SiteName            string                 `json:"site_name"`              // source site display name
	SiteLogo            string                 `json:"site_logo"`              // site logo reference text
	SiteSubtitle        string                 `json:"site_subtitle"`          // source site subtitle text
	APIBaseURL          string                 `json:"api_base_url"`           // displayed gateway API address
	ContactInfo         string                 `json:"contact_info"`           // source administrator contact text
	DocURL              string                 `json:"doc_url"`                // source documentation destination text
	HomeContent         string                 `json:"home_content"`           // source custom homepage content
	HideCcsImportButton bool                   `json:"hide_ccs_import_button"` // CCS import control visibility switch
	// PurchaseSubscriptionEnabled and PurchaseSubscriptionURL use the prior
	// Snapshot when nil. Enabled purchase requires a nonempty absolute HTTP(S) URL,
	// while supplied URLs are validated even when the entry is disabled.
	PurchaseSubscriptionEnabled *bool   `json:"purchase_subscription_enabled"` // subscription purchase entry switch
	PurchaseSubscriptionURL     *string `json:"purchase_subscription_url"`     // subscription purchase destination address
	TableDefaultPageSize        int     `json:"table_default_page_size"`       // default table row count
	TablePageSizeOptions        []int   `json:"table_page_size_options"`       // selectable table row counts
	UsageRankingLimit           int     `json:"usage_ranking_limit"`           // maximum user ranking row count
	// UsageRankingEnabled, UsageRankingSortBy, UsageRankingShowTotalTokens,
	// UsageRankingShowRequests, and UsageRankingShowActualCost merge with prior
	// ranking choices. The selected metric must remain visible after normalization.
	UsageRankingEnabled         *bool                      `json:"usage_ranking_enabled"`           // user usage ranking availability switch
	UsageRankingSortBy          *string                    `json:"usage_ranking_sort_by"`           // selected user ranking metric
	UsageRankingShowTotalTokens *bool                      `json:"usage_ranking_show_total_tokens"` // ranking token metric visibility
	UsageRankingShowRequests    *bool                      `json:"usage_ranking_show_requests"`     // ranking request metric visibility
	UsageRankingShowActualCost  *bool                      `json:"usage_ranking_show_actual_cost"`  // ranking cost metric visibility
	CustomMenuItems             *[]sitedto.CustomMenuItem  `json:"custom_menu_items"`               // custom administrator menu entries
	CustomEndpoints             *[]sitedto.CustomEndpoint  `json:"custom_endpoints"`                // custom endpoint description entries
	FooterLinks                 *[]sitedto.FooterLinkGroup `json:"footer_links"`                    // footer groups and link entries
	FooterText                  *string                    `json:"footer_text"`                     // source footer display text
	HomeFeaturedModels          *[]string                  `json:"home_featured_models"`            // ordered featured model identifier list
	// CreativeModelSettings uses nil to retain prior selections, while an empty
	// list clears them. An installed model reader can normalize capabilities before
	// module validation and batch preparation.
	CreativeModelSettings *[]creative.CreativeModelSetting `json:"creative_model_settings"` // Creative group/model operation selections
	// CreativeWorkerCount retains the prior count when nil. A supplied count
	// must be positive before the handler can prepare it for the task publisher.
	CreativeWorkerCount *int `json:"creative_worker_count"` // configured Creative task worker count

	DefaultConcurrency           int     `json:"default_concurrency"`              // default registration concurrency allowance
	DefaultBalance               float64 `json:"default_balance"`                  // default registration balance grant amount
	AffiliateEnabled             bool    `json:"affiliate_enabled"`                // affiliate program availability switch
	AffiliateRebateRate          float64 `json:"affiliate_rebate_rate"`            // affiliate order rebate percentage
	AffiliateRebateFreezeHours   int     `json:"affiliate_rebate_freeze_hours"`    // hours
	AffiliateRebateDurationDays  int     `json:"affiliate_rebate_duration_days"`   // days
	AffiliateRebatePerInviteeCap float64 `json:"affiliate_rebate_per_invitee_cap"` // maximum rebate per invited user
	AdminRechargeRebateEnabled   *bool   `json:"affiliate_admin_recharge_enabled"` // administrator recharge rebate switch
	DefaultUserRPMLimit          int     `json:"default_user_rpm_limit"`           // default user request-per-minute allowance
	// DefaultUserAPIKeyLimit keeps prior state when nil. A supplied value must
	// fit zero through MaxUserAPIKeyLimit before the identity participant prepares it.
	DefaultUserAPIKeyLimit               *int                                     `json:"default_user_api_key_limit"`              // default user credential-count allowance
	DefaultSubscriptions                 []billinghttp.DefaultSubscriptionSetting `json:"default_subscriptions"`                   // default granted subscription plan selections
	BalanceUnitName                      string                                   `json:"balance_unit_name"`                       // source balance display unit name
	BalanceUnitSymbol                    string                                   `json:"balance_unit_symbol"`                     // balance display currency symbol
	BalanceIconSVG                       string                                   `json:"balance_icon_svg"`                        // balance display SVG markup
	ReasoningPointRMBUnitPrice           *float64                                 `json:"reasoning_point_rmb_unit_price"`          // reasoning-point unit price in RMB
	USDExchangeRate                      *float64                                 `json:"usd_exchange_rate"`                       // RMB amount per USD conversion
	MarketplaceAvailabilityWindowDays    *int                                     `json:"marketplace_availability_window_days"`    // days
	MarketplaceAvailabilityBucketMinutes *int                                     `json:"marketplace_availability_bucket_minutes"` // minutes
	// The AuthSourceDefault fields for Email, LinuxDo, OIDC, WeChat, GitHub,
	// Google, and DingTalk share the same merge rule across their Balance,
	// Concurrency, Subscriptions, GrantOnSignup, and GrantOnFirstBind values.
	// Nil retains the respective prior grant; supplied false, zero, or empty
	// lists remain selected for identity preparation.
	AuthSourceDefaultEmailBalance             *float64                                  `json:"auth_source_default_email_balance"`                // Email balance grant amount
	AuthSourceDefaultEmailConcurrency         *int                                      `json:"auth_source_default_email_concurrency"`            // Email concurrency grant allowance
	AuthSourceDefaultEmailSubscriptions       *[]billinghttp.DefaultSubscriptionSetting `json:"auth_source_default_email_subscriptions"`          // Email default granted subscription plan selections
	AuthSourceDefaultEmailGrantOnSignup       *bool                                     `json:"auth_source_default_email_grant_on_signup"`        // Email signup grant application switch
	AuthSourceDefaultEmailGrantOnFirstBind    *bool                                     `json:"auth_source_default_email_grant_on_first_bind"`    // Email first-identity-bind grant application switch
	AuthSourceDefaultLinuxDoBalance           *float64                                  `json:"auth_source_default_linuxdo_balance"`              // LinuxDo balance grant amount
	AuthSourceDefaultLinuxDoConcurrency       *int                                      `json:"auth_source_default_linuxdo_concurrency"`          // LinuxDo concurrency grant allowance
	AuthSourceDefaultLinuxDoSubscriptions     *[]billinghttp.DefaultSubscriptionSetting `json:"auth_source_default_linuxdo_subscriptions"`        // LinuxDo default granted subscription plan selections
	AuthSourceDefaultLinuxDoGrantOnSignup     *bool                                     `json:"auth_source_default_linuxdo_grant_on_signup"`      // LinuxDo signup grant application switch
	AuthSourceDefaultLinuxDoGrantOnFirstBind  *bool                                     `json:"auth_source_default_linuxdo_grant_on_first_bind"`  // LinuxDo first-identity-bind grant application switch
	AuthSourceDefaultOIDCBalance              *float64                                  `json:"auth_source_default_oidc_balance"`                 // OIDC balance grant amount
	AuthSourceDefaultOIDCConcurrency          *int                                      `json:"auth_source_default_oidc_concurrency"`             // OIDC concurrency grant allowance
	AuthSourceDefaultOIDCSubscriptions        *[]billinghttp.DefaultSubscriptionSetting `json:"auth_source_default_oidc_subscriptions"`           // OIDC default granted subscription plan selections
	AuthSourceDefaultOIDCGrantOnSignup        *bool                                     `json:"auth_source_default_oidc_grant_on_signup"`         // OIDC signup grant application switch
	AuthSourceDefaultOIDCGrantOnFirstBind     *bool                                     `json:"auth_source_default_oidc_grant_on_first_bind"`     // OIDC first-identity-bind grant application switch
	AuthSourceDefaultWeChatBalance            *float64                                  `json:"auth_source_default_wechat_balance"`               // WeChat balance grant amount
	AuthSourceDefaultWeChatConcurrency        *int                                      `json:"auth_source_default_wechat_concurrency"`           // WeChat concurrency grant allowance
	AuthSourceDefaultWeChatSubscriptions      *[]billinghttp.DefaultSubscriptionSetting `json:"auth_source_default_wechat_subscriptions"`         // WeChat default granted subscription plan selections
	AuthSourceDefaultWeChatGrantOnSignup      *bool                                     `json:"auth_source_default_wechat_grant_on_signup"`       // WeChat signup grant application switch
	AuthSourceDefaultWeChatGrantOnFirstBind   *bool                                     `json:"auth_source_default_wechat_grant_on_first_bind"`   // WeChat first-identity-bind grant application switch
	AuthSourceDefaultGitHubBalance            *float64                                  `json:"auth_source_default_github_balance"`               // GitHub balance grant amount
	AuthSourceDefaultGitHubConcurrency        *int                                      `json:"auth_source_default_github_concurrency"`           // GitHub concurrency grant allowance
	AuthSourceDefaultGitHubSubscriptions      *[]billinghttp.DefaultSubscriptionSetting `json:"auth_source_default_github_subscriptions"`         // GitHub default granted subscription plan selections
	AuthSourceDefaultGitHubGrantOnSignup      *bool                                     `json:"auth_source_default_github_grant_on_signup"`       // GitHub signup grant application switch
	AuthSourceDefaultGitHubGrantOnFirstBind   *bool                                     `json:"auth_source_default_github_grant_on_first_bind"`   // GitHub first-identity-bind grant application switch
	AuthSourceDefaultGoogleBalance            *float64                                  `json:"auth_source_default_google_balance"`               // Google balance grant amount
	AuthSourceDefaultGoogleConcurrency        *int                                      `json:"auth_source_default_google_concurrency"`           // Google concurrency grant allowance
	AuthSourceDefaultGoogleSubscriptions      *[]billinghttp.DefaultSubscriptionSetting `json:"auth_source_default_google_subscriptions"`         // Google default granted subscription plan selections
	AuthSourceDefaultGoogleGrantOnSignup      *bool                                     `json:"auth_source_default_google_grant_on_signup"`       // Google signup grant application switch
	AuthSourceDefaultGoogleGrantOnFirstBind   *bool                                     `json:"auth_source_default_google_grant_on_first_bind"`   // Google first-identity-bind grant application switch
	AuthSourceDefaultDingTalkBalance          *float64                                  `json:"auth_source_default_dingtalk_balance"`             // DingTalk balance grant amount
	AuthSourceDefaultDingTalkConcurrency      *int                                      `json:"auth_source_default_dingtalk_concurrency"`         // DingTalk concurrency grant allowance
	AuthSourceDefaultDingTalkSubscriptions    *[]billinghttp.DefaultSubscriptionSetting `json:"auth_source_default_dingtalk_subscriptions"`       // DingTalk default granted subscription plan selections
	AuthSourceDefaultDingTalkGrantOnSignup    *bool                                     `json:"auth_source_default_dingtalk_grant_on_signup"`     // DingTalk signup grant application switch
	AuthSourceDefaultDingTalkGrantOnFirstBind *bool                                     `json:"auth_source_default_dingtalk_grant_on_first_bind"` // DingTalk first-identity-bind grant application switch
	ForceEmailOnThirdPartySignup              *bool                                     `json:"force_email_on_third_party_signup"`                // third-party signup email completion switch

	EnableModelFallback      bool   `json:"enable_model_fallback"`      // global fallback model selection switch
	FallbackModelAnthropic   string `json:"fallback_model_anthropic"`   // Anthropic fallback model identifier
	FallbackModelOpenAI      string `json:"fallback_model_openai"`      // OpenAI fallback model identifier
	FallbackModelGemini      string `json:"fallback_model_gemini"`      // Gemini fallback model identifier
	FallbackModelAntigravity string `json:"fallback_model_antigravity"` // Antigravity fallback model identifier

	// GrokDefaultTextModel and GrokDefaultBaseURLMode keep prior values when nil.
	// A supplied string is trimmed before gateway preparation validates its model
	// and endpoint selection rules for later requests.
	GrokDefaultTextModel   *string `json:"grok_default_text_model"`    // default model for model-omitted requests
	GrokDefaultBaseURLMode *string `json:"grok_default_base_url_mode"` // unconfigured Grok endpoint selection mode

	EnableIdentityPatch bool   `json:"enable_identity_patch"` // Claude-to-Gemini identity patch switch
	IdentityPatchPrompt string `json:"identity_patch_prompt"` // configured identity replacement prompt

	OpsMonitoringEnabled         *bool `json:"ops_monitoring_enabled"`          // Ops monitoring runtime switch
	OpsRealtimeMonitoringEnabled *bool `json:"ops_realtime_monitoring_enabled"` // Ops realtime collector runtime switch
	// OpsMetricsIntervalSeconds retains prior cadence when nil. A supplied
	// value is clamped to sixty through 3,600 seconds before participant serialization.
	OpsMetricsIntervalSeconds *int `json:"ops_metrics_interval_seconds"` // seconds

	MinClaudeCodeVersion string `json:"min_claude_code_version"` // minimum admitted Claude Code version
	MaxClaudeCodeVersion string `json:"max_claude_code_version"` // maximum admitted Claude Code version

	BackendModeEnabled bool `json:"backend_mode_enabled"` // administrator panel restriction switch

	TTFTMode                               *string `json:"ttft_mode"`                                   // global upstream latency selection
	EnableFingerprintUnification           *bool   `json:"enable_fingerprint_unification"`              // OAuth fingerprint header normalization switch
	EnableMetadataPassthrough              *bool   `json:"enable_metadata_passthrough"`                 // client metadata forwarding switch
	EnableCCHSigning                       *bool   `json:"enable_cch_signing"`                          // deprecated CCH signing setting
	EnableClaudeOAuthSystemPromptInjection *bool   `json:"enable_claude_oauth_system_prompt_injection"` // Claude OAuth system-block injection switch
	ClaudeOAuthSystemPrompt                *string `json:"claude_oauth_system_prompt"`                  // source Claude OAuth extension prompt
	ClaudeOAuthSystemPromptBlocks          *string `json:"claude_oauth_system_prompt_blocks"`           // configured Claude OAuth system-block JSON
	EnableAnthropicCacheTTL1hInjection     *bool   `json:"enable_anthropic_cache_ttl_1h_injection"`     // Anthropic one-hour cache TTL switch
	RewriteMessageCacheControl             *bool   `json:"rewrite_message_cache_control"`               // message cache-control rewriting switch
	EnableClientDatelineNormalization      *bool   `json:"enable_client_dateline_normalization"`        // Anthropic dateline normalization switch
	AntigravityUserAgentVersion            *string `json:"antigravity_user_agent_version"`              // configured Antigravity user-agent version
	OpenAICodexUserAgent                   *string `json:"openai_codex_user_agent"`                     // configured full Codex user-agent text
	OpenAIAllowClaudeCodeCodexPlugin       *bool   `json:"openai_allow_claude_code_codex_plugin"`       // Claude Code Codex plugin admission
	// UserPromptReplacementConfig retains the prior rule object when nil.
	// A supplied object and its rule slice remain shared with the prepared Snapshot
	// until gateway validation and serialization finish.
	UserPromptReplacementConfig *promptpolicy.UserPromptReplacementConfig `json:"user_prompt_replacement_config"` // administrator prompt replacement rule object

	PaymentVisibleMethodAlipaySource  *string `json:"payment_visible_method_alipay_source"`  // Alipay instance routing source choice
	PaymentVisibleMethodWxpaySource   *string `json:"payment_visible_method_wxpay_source"`   // WeChat Pay instance routing source
	PaymentVisibleMethodAlipayEnabled *bool   `json:"payment_visible_method_alipay_enabled"` // Alipay purchase-method visibility switch
	PaymentVisibleMethodWxpayEnabled  *bool   `json:"payment_visible_method_wxpay_enabled"`  // WeChat Pay purchase-method visibility switch

	AdvancedSchedulerStickyWeightedEnabled       *bool   `json:"advanced_scheduler_sticky_weighted_enabled"`       // sticky result weighting switch
	AdvancedSchedulerSubscriptionPriorityEnabled *bool   `json:"advanced_scheduler_subscription_priority_enabled"` // subscription priority preference switch
	AdvancedSchedulerEWMAErrorRateAlpha          *string `json:"advanced_scheduler_ewma_error_rate_alpha"`         // configured error-rate smoothing coefficient
	AdvancedSchedulerEWMATTFTAlpha               *string `json:"advanced_scheduler_ewma_ttft_alpha"`               // configured TTFT smoothing coefficient
	AdvancedSchedulerStickyEscapeEnabled         *bool   `json:"advanced_scheduler_sticky_escape_enabled"`         // configured sticky escape enablement
	AdvancedSchedulerStickyEscapeTTFTMs          *string `json:"advanced_scheduler_sticky_escape_ttft_ms"`         // configured TTFT escape milliseconds
	AdvancedSchedulerStickyEscapeErrorRate       *string `json:"advanced_scheduler_sticky_escape_error_rate"`      // configured error-rate escape threshold
	AdvancedSchedulerLBTopK                      *string `json:"advanced_scheduler_lb_top_k"`                      // configured load-balanced candidate count
	AdvancedSchedulerWeightPriority              *string `json:"advanced_scheduler_weight_priority"`               // configured provider priority score weight
	AdvancedSchedulerWeightLoad                  *string `json:"advanced_scheduler_weight_load"`                   // configured provider load score weight
	AdvancedSchedulerWeightQueue                 *string `json:"advanced_scheduler_weight_queue"`                  // configured wait queue score weight
	AdvancedSchedulerWeightErrorRate             *string `json:"advanced_scheduler_weight_error_rate"`             // configured observed error rate score weight
	AdvancedSchedulerWeightTTFT                  *string `json:"advanced_scheduler_weight_ttft"`                   // configured observed first-token time score weight
	AdvancedSchedulerWeightReset                 *string `json:"advanced_scheduler_weight_reset"`                  // configured quota reset score weight
	AdvancedSchedulerWeightQuotaHeadroom         *string `json:"advanced_scheduler_weight_quota_headroom"`         // configured remaining quota score weight
	AdvancedSchedulerWeightPreviousResponse      *string `json:"advanced_scheduler_weight_previous_response"`      // configured previous response affinity score weight
	AdvancedSchedulerWeightSessionSticky         *string `json:"advanced_scheduler_weight_session_sticky"`         // configured session affinity score weight
	// OpenAIQuotaAutoPauseSettings retains saved thresholds when nil. A supplied
	// zero disables that threshold default, and its nonnil object sets the presence
	// flag used for the wider Ops JSON merge.
	OpenAIQuotaAutoPauseSettings *provider.QuotaAutoPauseSettings `json:"openai_provider_quota_auto_pause"` // global OpenAI quota pause thresholds

	BalanceLowNotifyEnabled         *bool                           `json:"balance_low_notify_enabled"`         // low-balance mail notification switch
	BalanceLowNotifyThreshold       *float64                        `json:"balance_low_notify_threshold"`       // low-balance mail trigger amount
	BalanceLowNotifyRechargeURL     *string                         `json:"balance_low_notify_recharge_url"`    // source low-balance recharge destination
	SubscriptionExpiryNotifyEnabled *bool                           `json:"subscription_expiry_notify_enabled"` // subscription expiry mail notification switch
	ProviderQuotaNotifyEnabled      *bool                           `json:"provider_quota_notify_enabled"`      // provider quota mail notification switch
	ProviderQuotaNotifyEmails       *[]identitydto.NotifyEmailEntry `json:"provider_quota_notify_emails"`       // provider quota mail recipient records

	// Payment pointer fields use nil for omitted or null input. The payment
	// participant skips literal null before preparing its partial configuration;
	// PaymentEnabledTypes and PaymentMethodFees preserve supplied empty collections
	// as clearing instructions while nil collections keep prior settings.
	PaymentEnabled                   *bool                     `json:"payment_enabled"`                      // payment system availability switch
	PaymentMinAmount                 *float64                  `json:"payment_min_amount"`                   // minimum recharge payment amount
	PaymentMaxAmount                 *float64                  `json:"payment_max_amount"`                   // maximum recharge payment amount
	PaymentDailyLimit                *float64                  `json:"payment_daily_limit"`                  // per-user daily recharge allowance
	PaymentOrderTimeoutMin           *int                      `json:"payment_order_timeout_minutes"`        // minutes
	PaymentMaxPendingOrders          *int                      `json:"payment_max_pending_orders"`           // per-user pending payment order allowance
	PaymentEnabledTypes              []string                  `json:"payment_enabled_types"`                // enabled payment method type list
	PaymentBalanceDisabled           *bool                     `json:"payment_balance_disabled"`             // subscription balance-payment disable switch
	PaymentBalanceRechargeMultiplier *float64                  `json:"payment_balance_recharge_multiplier"`  // recharge balance grant multiplier
	PaymentSubscriptionUSDToCNYRate  *float64                  `json:"payment_subscription_usd_to_cny_rate"` // subscription USD-to-CNY conversion rate
	PaymentRechargeFeeRate           *float64                  `json:"payment_recharge_fee_rate"`            // global recharge fee percentage
	PaymentMethodFees                payment.MethodFeeSettings `json:"payment_method_fees"`                  // per-method recharge fee settings
	PaymentLoadBalanceStrat          *string                   `json:"payment_load_balance_strategy"`        // payment instance selection strategy
	PaymentProductNamePrefix         *string                   `json:"payment_product_name_prefix"`          // source payment product-name prefix
	PaymentProductNameSuffix         *string                   `json:"payment_product_name_suffix"`          // source payment product-name suffix
	PaymentHelpImageURL              *string                   `json:"payment_help_image_url"`               // source payment help image reference
	PaymentHelpText                  *string                   `json:"payment_help_text"`                    // source payment help display text

	PaymentCancelRateLimitEnabled *bool   `json:"payment_cancel_rate_limit_enabled"`     // payment cancellation throttle switch
	PaymentCancelRateLimitMax     *int    `json:"payment_cancel_rate_limit_max"`         // allowed cancellations per selected window
	PaymentCancelRateLimitWindow  *int    `json:"payment_cancel_rate_limit_window"`      // cancellation window length value
	PaymentCancelRateLimitUnit    *string `json:"payment_cancel_rate_limit_unit"`        // minute or hour window unit
	PaymentCancelRateLimitMode    *string `json:"payment_cancel_rate_limit_window_mode"` // fixed or sliding window mode

	PaymentAlipayForceQRCode             *bool `json:"payment_alipay_force_qrcode"`               // mobile Alipay QR-code preference
	PaymentAlipayMobilePrecreateDeepLink *bool `json:"payment_alipay_mobile_precreate_deep_link"` // mobile Alipay precreation link preference

	RiskControlEnabled *bool `json:"risk_control_enabled"` // risk-center and auditing runtime switch
	// BStats controls retain their stored values when omitted or null. Explicit
	// false changes only that control; identity is never accepted through this DTO.
	BStatsEnabled               *bool `json:"bstats_enabled"`                  // reporting enablement control
	BStatsLogErrors             *bool `json:"bstats_log_errors"`               // report failure logging
	BStatsLogSentData           *bool `json:"bstats_log_sent_data"`            // outgoing payload logging
	BStatsLogResponseStatusText *bool `json:"bstats_log_response_status_text"` // received response logging
	TeamEnabled                 *bool `json:"team_enabled"`                    // team runtime availability switch
	CreativeEnabled             *bool `json:"creative_enabled"`                // Creative Studio runtime access switch

	CyberSessionBlockEnabled *bool `json:"cyber_session_block_enabled"` // cyber-session blocking runtime switch
	// CyberSessionBlockTTLSeconds retains prior lifetime when nil. A supplied
	// nonpositive value is rejected before moderation can prepare a persistent TTL.
	CyberSessionBlockTTLSeconds *int `json:"cyber_session_block_ttl_seconds"` // seconds

	OpenAIFastPolicySettings *gatewaydto.OpenAIFastPolicySettings `json:"openai_fast_policy_settings,omitempty"` // OpenAI service-tier matching rule object

	// ProviderSchedulingThresholds retains saved state when nil. A supplied
	// map replaces supported platform percentages after provider validation; 100
	// selects disabled automatic pausing for that platform.
	ProviderSchedulingThresholds map[string]int `json:"provider_scheduling_thresholds"` // platform provider pause threshold percentages

	AllowUserViewErrorRequests *bool `json:"allow_user_view_error_requests"` // own failed-request visibility switch
}
