package site

import (
	"github.com/TokenFlux/TokenRouter/internal/pkg/locale"
	"github.com/TokenFlux/TokenRouter/internal/site/content"
)

// public_types.go defines resolved browser settings and the legal-document
// types shared by site consumers.
// Browser settings expose resolved site copy alongside the feature choices
// supplied by authentication and usage readers. Navigation remains stored JSON
// at this service layer so API and HTML adapters can apply their respective
// array mapping and filtering rules.

type (
	// LoginAgreementCopy is the title and Markdown body stored as one language copy.
	LoginAgreementCopy = content.LoginAgreementCopy
	// LoginAgreementDocument identifies legal copy and carries its optional localization metadata.
	LoginAgreementDocument = content.LoginAgreementDocument
	// PublicSettings contains resolved browser display values and module-supplied capability choices.
	PublicSettings struct {
		Locale                       string                       `json:"locale"`              // selected request language
		SiteTextOverrides            []string                     `json:"site_text_overrides"` // configured text override keys
		TextLanguages                map[string]locale.Resolution `json:"text_languages"`      // per-field language resolution
		DefaultLocale                string                       `json:"default_locale"`      // configured default language
		SiteTitle                    string                       `json:"site_title"`          // resolved browser title
		RegistrationEnabled          bool                         // registration entry switch
		EmailVerifyEnabled           bool                         // email verification switch
		ForceEmailOnThirdPartySignup bool                         // third-party email requirement
		// RegistrationEmailSuffixWhitelist shares PublicAuth.RegistrationSuffixes
		// storage. Callers copy the slice before changing domain entries independently
		// of the input source that supplied the authentication settings.
		RegistrationEmailSuffixWhitelist    []string // permitted registration domains
		RegistrationEmailDomainQuotaEnabled bool     // outside-domain quota switch
		UserEmailChangeEnabled              bool     // Allows users with an email address to replace their primary email.
		// PromoCodeEnabled defaults to enabled unless its stored setting is
		// exactly false; the browser receives the public reader's interpreted result.
		PromoCodeEnabled bool // promo code availability
		// PasswordResetEnabled requires both email verification and password-reset
		// settings to equal true, so the public reader combines those switches for display.
		PasswordResetEnabled  bool // password reset availability
		InvitationCodeEnabled bool // invitation code requirement
		TotpEnabled           bool // TOTP two-factor authentication.
		// PasskeyEnabled comes from the app's deployment-level WebAuthn switch;
		// the public input converter supplies it independently of stored site strings.
		PasskeyEnabled bool // WebAuthn login availability
		// LoginAgreementEnabled, LoginAgreementMode, LoginAgreementUpdatedAt,
		// LoginAgreementRevision, and LoginAgreementDocuments describe one consent
		// prompt. Revision is calculated from source copy before document language
		// resolution, keeping translations from changing that consent identity.
		LoginAgreementEnabled   bool                     // consent prompt availability
		LoginAgreementMode      string                   // consent presentation mode
		LoginAgreementUpdatedAt string                   // agreement revision label
		LoginAgreementRevision  string                   // source consent fingerprint
		LoginAgreementDocuments []LoginAgreementDocument // selected legal copies
		TurnstileEnabled        bool                     // Turnstile challenge switch
		TurnstileSiteKey        string                   // browser Turnstile identifier
		TencentCaptchaEnabled   bool                     // Tencent challenge switch
		TencentCaptchaAppID     string                   // browser Tencent identifier
		TencentCaptchaRegion    string                   // Tencent SDK region
		AliyunCaptchaEnabled    bool                     // Aliyun challenge switch
		AliyunCaptchaSceneID    string                   // Aliyun scene identifier
		AliyunCaptchaPrefix     string                   // Aliyun frontend prefix
		AliyunCaptchaRegion     string                   // Aliyun SDK region
		SiteName                string                   // resolved branding name
		SiteLogo                string                   // branding image location
		SiteShareTitle          string                   `json:"site_share_title"`        // resolved sharing title
		SiteShareDescription    string                   `json:"site_share_description"`  // resolved sharing description
		SiteShareImageURL       string                   `json:"site_share_image_url"`    // public cover resource path
		SiteShareImageType      string                   `json:"site_share_image_type"`   // validated cover MIME type
		SiteShareImageWidth     int                      `json:"site_share_image_width"`  // cover width in pixels
		SiteShareImageHeight    int                      `json:"site_share_image_height"` // cover height in pixels
		FrontendURL             string                   `json:"frontend_url"`            // configured frontend base URL
		SiteSubtitle            string                   // resolved branding subtitle
		APIBaseURL              string                   // advertised API base
		ContactInfo             string                   // resolved contact details
		DocURL                  string                   // resolved documentation link
		HomeContent             string                   // resolved homepage content
		HideCcsImportButton     bool                     // hide import action

		PurchaseSubscriptionEnabled bool   // subscription purchase switch
		PurchaseSubscriptionURL     string // resolved purchase destination
		// TableDefaultPageSize and TablePageSizeOptions are normalized for browser
		// pagination: invalid values use built-in defaults, and valid choices are
		// deduplicated and sorted before this response is constructed.
		TableDefaultPageSize        int    // default rows per page
		TablePageSizeOptions        []int  // selectable page row counts
		UsageRankingLimit           int    // ranking entry limit
		UsageRankingEnabled         bool   // ranking display switch
		UsageRankingSortBy          string // ranking sort metric
		UsageRankingShowTotalTokens bool   // display total tokens
		UsageRankingShowRequests    bool   // display request counts
		UsageRankingShowActualCost  bool   // display charged cost
		CustomMenuItems             string // resolved menu JSON
		CustomEndpoints             string // resolved endpoint JSON
		FooterLinks                 string // resolved footer JSON
		FooterText                  string // Extra footer text (ICP number etc.)
		HomeFeaturedModels          string // JSON array of model IDs featured on the home page

		LinuxDoOAuthEnabled      bool // LinuxDo login availability
		DingTalkOAuthEnabled     bool // DingTalk login availability
		WeChatOAuthEnabled       bool // WeChat login availability
		WeChatOAuthOpenEnabled   bool // WeChat open-platform availability
		WeChatOAuthMPEnabled     bool // WeChat public-account availability
		WeChatOAuthMobileEnabled bool // WeChat mobile-login availability
		BackendModeEnabled       bool // backend mode switch
		PaymentEnabled           bool // payment entry switch
		TeamEnabled              bool // team feature availability
		TeamSelfServiceEnabled   bool // team self-service availability
		// CreativeEnabled controls the frontend studio entry and route guard;
		// the public reader enables it unless the stored setting is exactly false.
		CreativeEnabled       bool   // creative studio availability
		OIDCOAuthEnabled      bool   // OIDC login availability
		OIDCOAuthProviderName string // resolved OIDC label
		GitHubOAuthEnabled    bool   // GitHub login availability
		GoogleOAuthEnabled    bool   // Google login availability
		// GoogleOneTapEnabled and GoogleOAuthClientID come from the identity
		// converter together. Production conversion exposes a client ID only when
		// One Tap is enabled with an available Google OAuth configuration.
		GoogleOneTapEnabled bool   // One Tap availability
		GoogleOAuthClientID string // browser Google identifier
		// Version remains empty in GetPublicSettings. The HTTP handler supplies its
		// own response label, and HTML injection calls PublicSource.PublicVersion for
		// its separately assembled response.
		Version           string // service build label field
		BalanceUnitName   string // localized balance unit
		BalanceUnitSymbol string // displayed unit symbol
		BalanceIconSVG    string // configured balance icon

		BalanceLowNotifyEnabled    bool // low-balance email switch
		ProviderQuotaNotifyEnabled bool // provider quota email switch
		// RiskControlEnabled controls the browser risk-control entry from the
		// public stored switch; backend risk checks are owned by their request handlers.
		RiskControlEnabled          bool    // risk center availability
		AffiliateEnabled            bool    // affiliate entry availability
		BalanceLowNotifyThreshold   float64 // low balance threshold
		BalanceLowNotifyRechargeURL string  // resolved recharge destination

		// AllowUserViewErrorRequests exposes the stored permission switch for the
		// user's failed-request view, allowing the browser to display that usage-page entry.
		AllowUserViewErrorRequests bool `json:"allow_user_view_error_requests"` // failed-request view permission
	}
)
