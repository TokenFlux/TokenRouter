package dto

import (
	"github.com/TokenFlux/TokenRouter/internal/pkg/locale"
	"github.com/TokenFlux/TokenRouter/internal/site/content"
)

// public.go defines the public settings response with localized navigation and
// server timezone fields.
// Browser settings carry typed navigation arrays and localized display values
// using the public API's JSON field names. Timezone labels and the current
// offset accompany those values so the browser can identify server-local times
// when displaying time-sensitive settings.

// LocalizedTexts is the per-field revisioned editing format for administrator site copy.
type LocalizedTexts = locale.TextUpdates

type (
	// LoginAgreementDocument identifies selected legal copy in public settings and document reads.
	LoginAgreementDocument = content.LoginAgreementDocument
	// PublicSettings is the browser API representation of feature choices and display settings.
	PublicSettings struct {
		Locale                              string                       `json:"locale"`                                  // selected request language
		SiteTextOverrides                   []string                     `json:"site_text_overrides"`                     // configured text override keys
		TextLanguages                       map[string]locale.Resolution `json:"text_languages"`                          // per-field language resolution
		DefaultLocale                       string                       `json:"default_locale"`                          // configured default language
		SiteTitle                           string                       `json:"site_title"`                              // resolved browser title
		RegistrationEnabled                 bool                         `json:"registration_enabled"`                    // registration entry switch
		EmailVerifyEnabled                  bool                         `json:"email_verify_enabled"`                    // email verification switch
		ForceEmailOnThirdPartySignup        bool                         `json:"force_email_on_third_party_signup"`       // third-party email requirement
		RegistrationEmailSuffixWhitelist    []string                     `json:"registration_email_suffix_whitelist"`     // permitted registration domains
		RegistrationEmailDomainQuotaEnabled bool                         `json:"registration_email_domain_quota_enabled"` // outside-domain quota switch
		UserEmailChangeEnabled              bool                         `json:"user_email_change_enabled"`               // allow users with an email identity to replace their primary email
		PromoCodeEnabled                    bool                         `json:"promo_code_enabled"`                      // promo code availability
		PasswordResetEnabled                bool                         `json:"password_reset_enabled"`                  // password reset availability
		InvitationCodeEnabled               bool                         `json:"invitation_code_enabled"`                 // invitation code requirement
		TotpEnabled                         bool                         `json:"totp_enabled"`                            // TOTP two-factor authentication
		// PasskeyEnabled comes from the deployment WebAuthn switch carried through
		// the public service; this response field reports availability to browser login UI.
		PasskeyEnabled bool `json:"passkey_enabled"` // WebAuthn login availability
		// LoginAgreementEnabled, LoginAgreementMode, LoginAgreementUpdatedAt,
		// LoginAgreementRevision, and LoginAgreementDocuments describe the consent
		// prompt selected by the service. Its revision follows source copy, while
		// document titles and bodies can display another language.
		LoginAgreementEnabled       bool                     `json:"login_agreement_enabled"`         // consent prompt availability
		LoginAgreementMode          string                   `json:"login_agreement_mode"`            // consent presentation mode
		LoginAgreementUpdatedAt     string                   `json:"login_agreement_updated_at"`      // agreement revision label
		LoginAgreementRevision      string                   `json:"login_agreement_revision"`        // source consent fingerprint
		LoginAgreementDocuments     []LoginAgreementDocument `json:"login_agreement_documents"`       // selected legal copies
		TurnstileEnabled            bool                     `json:"turnstile_enabled"`               // Turnstile challenge switch
		TurnstileSiteKey            string                   `json:"turnstile_site_key"`              // browser Turnstile identifier
		TencentCaptchaEnabled       bool                     `json:"tencent_captcha_enabled"`         // Tencent challenge switch
		TencentCaptchaAppID         string                   `json:"tencent_captcha_app_id"`          // browser Tencent identifier
		TencentCaptchaRegion        string                   `json:"tencent_captcha_region"`          // Tencent SDK region
		AliyunCaptchaEnabled        bool                     `json:"aliyun_captcha_enabled"`          // Aliyun challenge switch
		AliyunCaptchaSceneID        string                   `json:"aliyun_captcha_scene_id"`         // Aliyun scene identifier
		AliyunCaptchaPrefix         string                   `json:"aliyun_captcha_prefix"`           // Aliyun frontend prefix
		AliyunCaptchaRegion         string                   `json:"aliyun_captcha_region"`           // Aliyun SDK region
		SiteName                    string                   `json:"site_name"`                       // resolved branding name
		SiteLogo                    string                   `json:"site_logo"`                       // branding image location
		SiteShareTitle              string                   `json:"site_share_title"`                // resolved sharing title
		SiteShareDescription        string                   `json:"site_share_description"`          // resolved sharing description
		SiteShareImageURL           string                   `json:"site_share_image_url"`            // public cover resource path
		SiteShareImageType          string                   `json:"site_share_image_type"`           // validated cover MIME type
		SiteShareImageWidth         int                      `json:"site_share_image_width"`          // cover width in pixels
		SiteShareImageHeight        int                      `json:"site_share_image_height"`         // cover height in pixels
		FrontendURL                 string                   `json:"frontend_url"`                    // configured frontend base URL
		SiteSubtitle                string                   `json:"site_subtitle"`                   // resolved branding subtitle
		APIBaseURL                  string                   `json:"api_base_url"`                    // advertised API base
		ContactInfo                 string                   `json:"contact_info"`                    // resolved contact details
		DocURL                      string                   `json:"doc_url"`                         // resolved documentation link
		HomeContent                 string                   `json:"home_content"`                    // resolved homepage content
		HideCcsImportButton         bool                     `json:"hide_ccs_import_button"`          // hide import action
		PurchaseSubscriptionEnabled bool                     `json:"purchase_subscription_enabled"`   // subscription purchase switch
		PurchaseSubscriptionURL     string                   `json:"purchase_subscription_url"`       // resolved purchase destination
		TableDefaultPageSize        int                      `json:"table_default_page_size"`         // default rows per page
		TablePageSizeOptions        []int                    `json:"table_page_size_options"`         // selectable page row counts
		UsageRankingLimit           int                      `json:"usage_ranking_limit"`             // ranking entry limit
		UsageRankingEnabled         bool                     `json:"usage_ranking_enabled"`           // ranking display switch
		UsageRankingSortBy          string                   `json:"usage_ranking_sort_by"`           // ranking sort metric
		UsageRankingShowTotalTokens bool                     `json:"usage_ranking_show_total_tokens"` // display total tokens
		UsageRankingShowRequests    bool                     `json:"usage_ranking_show_requests"`     // display request counts
		UsageRankingShowActualCost  bool                     `json:"usage_ranking_show_actual_cost"`  // display charged cost
		CustomMenuItems             []CustomMenuItem         `json:"custom_menu_items"`               // public navigation entries
		CustomEndpoints             []CustomEndpoint         `json:"custom_endpoints"`                // copyable endpoint entries
		FooterLinks                 []FooterLinkGroup        `json:"footer_links"`                    // resolved footer columns
		FooterText                  string                   `json:"footer_text"`                     // resolved footer text
		HomeFeaturedModels          []string                 `json:"home_featured_models"`            // featured model identifiers
		DingTalkOAuthEnabled        bool                     `json:"dingtalk_oauth_enabled"`          // DingTalk login availability
		LinuxDoOAuthEnabled         bool                     `json:"linuxdo_oauth_enabled"`           // LinuxDo login availability
		WeChatOAuthEnabled          bool                     `json:"wechat_oauth_enabled"`            // WeChat login availability
		WeChatOAuthOpenEnabled      bool                     `json:"wechat_oauth_open_enabled"`       // WeChat open-platform availability
		WeChatOAuthMPEnabled        bool                     `json:"wechat_oauth_mp_enabled"`         // WeChat public-account availability
		WeChatOAuthMobileEnabled    bool                     `json:"wechat_oauth_mobile_enabled"`     // WeChat mobile-login availability
		OIDCOAuthEnabled            bool                     `json:"oidc_oauth_enabled"`              // OIDC login availability
		OIDCOAuthProviderName       string                   `json:"oidc_oauth_provider_name"`        // resolved OIDC label
		GitHubOAuthEnabled          bool                     `json:"github_oauth_enabled"`            // GitHub login availability
		GoogleOAuthEnabled          bool                     `json:"google_oauth_enabled"`            // Google login availability
		GoogleOneTapEnabled         bool                     `json:"google_one_tap_enabled"`          // One Tap availability
		GoogleOAuthClientID         string                   `json:"google_oauth_client_id"`          // browser Google identifier
		BackendModeEnabled          bool                     `json:"backend_mode_enabled"`            // backend mode switch
		PaymentEnabled              bool                     `json:"payment_enabled"`                 // payment entry switch
		TeamEnabled                 bool                     `json:"team_enabled"`                    // team feature availability
		TeamSelfServiceEnabled      bool                     `json:"team_self_service_enabled"`       // team self-service availability
		CreativeEnabled             bool                     `json:"creative_enabled"`                // creative studio availability
		Version                     string                   `json:"version"`                         // supplied build label
		// ServerTimezone and ServerUTCOffset come from the service's injected
		// calendar and configured label. The offset is read at response time so
		// daylight-saving changes in that calendar are reflected in browser settings.
		ServerTimezone             string `json:"server_timezone"`               // configured timezone label
		ServerUTCOffset            string `json:"server_utc_offset"`             // current UTC offset
		BalanceUnitName            string `json:"balance_unit_name"`             // localized balance unit
		BalanceUnitSymbol          string `json:"balance_unit_symbol"`           // displayed unit symbol
		BalanceIconSVG             string `json:"balance_icon_svg"`              // configured balance icon
		BalanceLowNotifyEnabled    bool   `json:"balance_low_notify_enabled"`    // low-balance email switch
		ProviderQuotaNotifyEnabled bool   `json:"provider_quota_notify_enabled"` // provider quota email switch
		RiskControlEnabled         bool   `json:"risk_control_enabled"`          // risk center entry
		// CyberSessionBlockEnabled and CyberSessionBlockTTLSeconds remain zero
		// values in PublicHandler.GetPublicSettings, which does not assign these
		// response fields when building the public response.
		CyberSessionBlockEnabled    bool    `json:"cyber_session_block_enabled"`     // session-block feature switch
		CyberSessionBlockTTLSeconds int     `json:"cyber_session_block_ttl_seconds"` // seconds
		AffiliateEnabled            bool    `json:"affiliate_enabled"`               // affiliate rebate entry
		BalanceLowNotifyThreshold   float64 `json:"balance_low_notify_threshold"`    // low balance threshold
		BalanceLowNotifyRechargeURL string  `json:"balance_low_notify_recharge_url"` // resolved recharge destination

		// AllowUserViewErrorRequests carries the service's failed-request permission
		// switch so the browser can show the account's usage-page error view.
		AllowUserViewErrorRequests bool `json:"allow_user_view_error_requests"` // failed-request view permission
	}
)
