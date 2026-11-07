package site

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/url"
	"sort"
	"strconv"
	"strings"

	"github.com/TokenFlux/TokenRouter/internal/pkg/locale"
	"github.com/TokenFlux/TokenRouter/internal/pkg/timezone"
)

// public.go resolves public site settings, legal consent revisions, and
// browser injection data.
// Browser settings resolve site copy and navigation for the request language
// while authentication and ranking readers supply their interpreted capability
// values. Configured text keys remain visible as overrides even when their
// selected copy is empty, allowing the browser to distinguish intentional
// blank text from an absent customization.
//
// Consent identity is calculated from normalized source documents and the
// configured date before translated copies are selected. API and HTML adapters
// share resolved settings: the API parses navigation into slices, while HTML
// embeds JSON serialized from the known content fields during resolution. Its
// visibility filter retains complete objects from that resolved JSON. CSP
// collection resolves every supported language so translated iframe destinations
// also contribute their HTTP or HTTPS origins.

const (
	// Consent defaults replace unsupported presentation modes and missing date
	// values, giving public and administrator readers the same initial consent metadata.
	defaultLoginAgreementMode = "modal"
	defaultLoginAgreementDate = "2026-03-31"

	// Public display keys identify values copied by InputSource's allowlist and
	// interpreted here; credentials used by authentication conversion have a broader read list.
	SettingKeyAPIBaseURL                          = "api_base_url"
	SettingKeyProviderQuotaNotifyEnabled          = "provider_quota_notify_enabled"
	SettingKeyAffiliateEnabled                    = "affiliate_enabled"
	SettingKeyAliyunCaptchaEnabled                = "aliyun_captcha_enabled"
	SettingKeyAliyunCaptchaPrefix                 = "aliyun_captcha_prefix"
	SettingKeyAliyunCaptchaSceneID                = "aliyun_captcha_scene_id"
	SettingKeyAllowUserViewErrorRequests          = "allow_user_view_error_requests"
	SettingKeyBackendModeEnabled                  = "backend_mode_enabled"
	SettingKeyBalanceIconSVG                      = "balance_icon_svg"
	SettingKeyBalanceLowNotifyEnabled             = "balance_low_notify_enabled"
	SettingKeyBalanceLowNotifyRechargeURL         = "balance_low_notify_recharge_url"
	SettingKeyBalanceLowNotifyThreshold           = "balance_low_notify_threshold"
	SettingKeyBalanceUnitName                     = "balance_unit_name"
	SettingKeyBalanceUnitSymbol                   = "balance_unit_symbol"
	SettingKeyContactInfo                         = "contact_info"
	SettingKeyCreativeEnabled                     = "creative_enabled"
	SettingKeyCustomEndpoints                     = "custom_endpoints"
	SettingKeyCustomMenuItems                     = "custom_menu_items"
	SettingKeyDocURL                              = "doc_url"
	SettingKeyEmailVerifyEnabled                  = "email_verify_enabled"
	SettingKeyFooterLinks                         = "footer_links"
	SettingKeyFooterText                          = "footer_text"
	SettingKeyForceEmailOnThirdPartySignup        = "force_email_on_third_party_signup"
	SettingKeyHideCcsImportButton                 = "hide_ccs_import_button"
	SettingKeyHomeContent                         = "home_content"
	SettingKeyHomeFeaturedModels                  = "home_featured_models"
	SettingKeyInvitationCodeEnabled               = "invitation_code_enabled"
	SettingKeyLoginAgreementDocuments             = "login_agreement_documents"
	SettingKeyLoginAgreementEnabled               = "login_agreement_enabled"
	SettingKeyLoginAgreementMode                  = "login_agreement_mode"
	SettingKeyLoginAgreementUpdatedAt             = "login_agreement_updated_at"
	SettingKeyPasswordResetEnabled                = "password_reset_enabled"
	SettingKeyPromoCodeEnabled                    = "promo_code_enabled"
	SettingKeyPurchaseSubscriptionEnabled         = "purchase_subscription_enabled"
	SettingKeyPurchaseSubscriptionURL             = "purchase_subscription_url"
	SettingKeyRegistrationEmailDomainQuotaEnabled = "registration_email_domain_quota_enabled"
	SettingKeyRegistrationEnabled                 = "registration_enabled"
	SettingKeyRiskControlEnabled                  = "risk_control_enabled"
	SettingKeySiteLogo                            = "site_logo"
	SettingKeySiteName                            = "site_name"
	SettingKeySiteSubtitle                        = "site_subtitle"
	SettingKeyTableDefaultPageSize                = "table_default_page_size"
	SettingKeyTablePageSizeOptions                = "table_page_size_options"
	SettingKeyTencentCaptchaAppID                 = "tencent_captcha_app_id"
	SettingKeyTencentCaptchaEnabled               = "tencent_captcha_enabled"
	SettingKeyTotpEnabled                         = "totp_enabled"
	SettingKeyTurnstileEnabled                    = "turnstile_enabled"
	SettingKeyTurnstileSiteKey                    = "turnstile_site_key"
	SettingKeyUserEmailChangeEnabled              = "user_email_change_enabled"
	SettingPaymentEnabled                         = "payment_enabled"
)

// PublicSource supplies display strings and module-interpreted browser settings from storage.
type PublicSource interface {
	// LoadSiteShareImage reads the current encoded cover separately from public
	// settings. Empty data means no cover is configured; storage errors propagate
	// so image requests can distinguish missing content from a failed read.
	LoadSiteShareImage(context.Context) (string, error)

	// LoadSitePublicInputs returns one settings input or an error preventing response assembly.
	LoadSitePublicInputs(context.Context) (PublicInputs, error)
	// PublicVersion supplies the build label for the HTML injection response.
	PublicVersion() string
}

// PublicInputs combines public source strings with authentication and usage reader results.
type PublicInputs struct {
	// Values is the display input map. PublicService copies it before resolving
	// text and navigation, leaving a source's raw map available for later reads.
	Values map[string]string
	Auth   PublicAuth  // converted authentication settings
	Usage  PublicUsage // converted ranking settings
}

// PublicAuth contains browser-facing login, team, WebAuthn, and challenge parameters supplied by app.
type PublicAuth struct {
	// Login-provider flags come from identity's supplied-batch conversion, while
	// Team and TeamSelfService also apply deployment switches and Passkey uses
	// deployment WebAuthn availability. PublicService receives these interpreted
	// choices and copies them into the browser response.
	// LinuxDo reports LinuxDo login availability; DingTalk reports DingTalk login
	// availability; OIDC reports OIDC login availability; WeChat reports overall
	// WeChat login availability; WeChatOpen reports open-platform channel availability;
	// WeChatMP reports public-account channel availability; WeChatMobile reports
	// mobile-login channel availability; GitHub reports GitHub login availability;
	// Google reports Google login availability; GoogleOneTap reports browser One
	// Tap availability; Team reports team feature availability; TeamSelfService
	// reports team self-service availability; Passkey reports WebAuthn login availability.
	LinuxDo, DingTalk, OIDC, WeChat, WeChatOpen, WeChatMP, WeChatMobile, GitHub, Google, GoogleOneTap, Team, TeamSelfService, Passkey bool
	// OIDCName supplies the provider's displayed label; GoogleClientID supplies
	// the browser Google identifier; TencentRegion supplies the Tencent SDK region;
	// AliyunRegion supplies the Aliyun SDK region. The same auth converter owns
	// their configured defaults and Google One Tap's client-ID exposure rule.
	OIDCName, GoogleClientID, TencentRegion, AliyunRegion string
	// RegistrationSuffixes supplies parsed permitted domains. PublicService
	// assigns this slice directly to its result, retaining the source's backing collection.
	RegistrationSuffixes []string
}

// PublicUsage contains interpreted ranking controls used by the browser's usage display.
type PublicUsage struct {
	Limit   int  // ranking entry limit
	Enabled bool // ranking display switch
	// SortBy, ShowTotalTokens, ShowRequests, and ShowActualCost describe ranking
	// order and visible columns together. Production usage conversion keeps the
	// sorting column visible even when its stored display switch is disabled.
	SortBy string // ranking sort metric
	// ShowTotalTokens reports token count visibility; ShowRequests reports request
	// count visibility; ShowActualCost reports charged cost visibility.
	ShowTotalTokens, ShowRequests, ShowActualCost bool // token, request, and cost column visibility
}

// PublicService resolves display copy and assembles browser settings for API, HTML, and CSP readers.
type PublicService struct {
	source   PublicSource      // public settings input
	calendar timezone.Calendar // injected server calendar
	// timezoneName labels server time independently of the calendar location.
	// Construction uses the location name only when the supplied label is empty.
	timezoneName string
}

// NewPublicService binds public inputs and the calendar used for server-time display.
// An empty timezoneName uses calendar.Location().String(); a nonempty label is
// retained as supplied, including labels distinct from the calendar's location name.
func NewPublicService(source PublicSource, calendar timezone.Calendar, timezoneName string) *PublicService {
	if timezoneName == "" {
		timezoneName = calendar.Location().String()
	}
	return &PublicService{source: source, calendar: calendar, timezoneName: timezoneName}
}

// ServerTimezone returns the configured label and the injected calendar's current UTC offset.
// The offset is computed when called, so calendars with daylight-saving changes
// can report a different offset across response times while retaining their label.
func (s *PublicService) ServerTimezone() (string, string) {
	return s.timezoneName, s.calendar.UTCOffset(s.calendar.Now())
}

// GetPublicSettings returns resolved browser settings for the language carried by ctx.
// Input-source failures abort assembly. Site strings and navigation resolve in
// a copied settings map, while authentication and usage values come from the
// source's module conversions. Registration suffixes retain the source's supplied slice.
//
// Consent revision uses normalized source documents before display-language
// resolution removes editor metadata. Configured override keys and their
// selected-language metadata survive even when their copy is empty. Password
// reset combines both its setting and email verification; other feature switches
// apply their individual stored-string defaults before adapters encode the result.
func (s *PublicService) GetPublicSettings(ctx context.Context) (*PublicSettings, error) {
	input, err := s.source.LoadSitePublicInputs(ctx)
	if err != nil {
		return nil, err
	}
	settings := ResolveSiteTexts(input.Values, locale.FromContext(ctx))
	ResolveNavigation(settings, locale.FromContext(ctx))

	// Recovery availability combines both switches so the browser advertises
	// password-reset entry only when this public policy also enables email verification.
	emailVerifyEnabled := settings[SettingKeyEmailVerifyEnabled] == "true"
	passwordResetEnabled := emailVerifyEnabled && settings[SettingKeyPasswordResetEnabled] == "true"
	registrationEmailSuffixWhitelist := input.Auth.RegistrationSuffixes
	tableDefaultPageSize, tablePageSizeOptions := ParseTablePreferences(
		settings[SettingKeyTableDefaultPageSize],
		settings[SettingKeyTablePageSizeOptions],
	)
	usageRanking := input.Usage
	loginAgreementDocuments := ParseLoginAgreementDocuments(settings[SettingKeyLoginAgreementDocuments])
	agreementDate := strings.TrimSpace(settings[SettingKeyLoginAgreementUpdatedAt])
	if agreementDate == "" {
		agreementDate = defaultLoginAgreementDate
	}
	// Consent identity uses source copy before document display is localized.
	// Calculating it here makes translation changes independent of the browser's consent version.
	agreementRevision := BuildLoginAgreementRevision(agreementDate, loginAgreementDocuments)
	for i := range loginAgreementDocuments {
		doc := &loginAgreementDocuments[i]
		if doc.Localization != nil {
			copy, actual := doc.Localization.Resolve(locale.FromContext(ctx))
			doc.Resolution = &actual
			doc.Title, doc.ContentMD = copy.Title, copy.ContentMD
			doc.Localization = nil
		} else if settings[SettingKeyLoginAgreementDocuments] == "" && locale.FromContext(ctx) == "en" {
			// Built-in agreement titles are stored as source defaults. Supply their
			// English labels for an unconfigured English response without creating a localized content block.
			doc.Title = map[string]string{"terms": "Terms of Service", "usage-policy": "Usage Policy", "supported-regions": "Supported Countries and Regions", "service-specific-terms": "Service-specific Terms"}[doc.ID]
		}
	}
	loginAgreementUpdatedAt := strings.TrimSpace(settings[SettingKeyLoginAgreementUpdatedAt])
	if loginAgreementUpdatedAt == "" {
		loginAgreementUpdatedAt = defaultLoginAgreementDate
	}

	var balanceLowNotifyThreshold float64
	// Invalid or negative threshold strings leave the public amount at zero.
	// This reader accepts ParseFloat's nonnegative result and does not apply a finite-number check.
	if v, err := strconv.ParseFloat(settings[SettingKeyBalanceLowNotifyThreshold], 64); err == nil && v >= 0 {
		balanceLowNotifyThreshold = v
	}
	balanceUnitName := locale.ResolveSettingText(settings, SettingKeyBalanceUnitName, "USD", locale.FromContext(ctx))
	if balanceUnitName == "" {
		balanceUnitName = "USD"
	}
	balanceUnitSymbol := strings.TrimSpace(settings[SettingKeyBalanceUnitSymbol])
	if balanceUnitSymbol == "" {
		balanceUnitSymbol = "$"
	}

	overrides := []string{}
	textLanguages := map[string]locale.Resolution{}
	for key, content := range ConfiguredSiteTexts(input.Values) {
		overrides = append(overrides, key)
		_, textLanguages[key] = content.Resolve(locale.FromContext(ctx))
	}
	sort.Strings(overrides)
	cover := ReadShareImageMetadata(settings[SettingKeySiteShareImageMeta])
	return &PublicSettings{
		Locale:                              locale.FromContext(ctx),
		SiteTextOverrides:                   overrides,
		TextLanguages:                       textLanguages,
		DefaultLocale:                       locale.Negotiate(settings[SettingKeyDefaultLocale], locale.Default()),
		SiteTitle:                           settings["site_title"],
		RegistrationEnabled:                 settings[SettingKeyRegistrationEnabled] == "true",
		EmailVerifyEnabled:                  emailVerifyEnabled,
		ForceEmailOnThirdPartySignup:        settings[SettingKeyForceEmailOnThirdPartySignup] == "true",
		RegistrationEmailSuffixWhitelist:    registrationEmailSuffixWhitelist,
		RegistrationEmailDomainQuotaEnabled: settings[SettingKeyRegistrationEmailDomainQuotaEnabled] == "true",
		UserEmailChangeEnabled:              settings[SettingKeyUserEmailChangeEnabled] == "true",
		PromoCodeEnabled:                    settings[SettingKeyPromoCodeEnabled] != "false", // Enabled by default.
		PasswordResetEnabled:                passwordResetEnabled,
		InvitationCodeEnabled:               settings[SettingKeyInvitationCodeEnabled] == "true",
		// app's team converter combines runtime settings with deployment feature
		// switches before this response exposes team and self-service availability to browser consumers.
		TeamEnabled:                 input.Auth.Team,
		TeamSelfServiceEnabled:      input.Auth.TeamSelfService,
		CreativeEnabled:             settings[SettingKeyCreativeEnabled] != "false",
		AffiliateEnabled:            settings[SettingKeyAffiliateEnabled] == "true",
		TotpEnabled:                 settings[SettingKeyTotpEnabled] == "true",
		PasskeyEnabled:              input.Auth.Passkey,
		LoginAgreementEnabled:       settings[SettingKeyLoginAgreementEnabled] == "true" && len(loginAgreementDocuments) > 0,
		LoginAgreementMode:          NormalizeLoginAgreementMode(settings[SettingKeyLoginAgreementMode]),
		LoginAgreementUpdatedAt:     loginAgreementUpdatedAt,
		LoginAgreementRevision:      agreementRevision,
		LoginAgreementDocuments:     loginAgreementDocuments,
		TurnstileEnabled:            settings[SettingKeyTurnstileEnabled] == "true",
		TurnstileSiteKey:            settings[SettingKeyTurnstileSiteKey],
		TencentCaptchaEnabled:       settings[SettingKeyTencentCaptchaEnabled] == "true",
		TencentCaptchaAppID:         settings[SettingKeyTencentCaptchaAppID],
		TencentCaptchaRegion:        input.Auth.TencentRegion,
		AliyunCaptchaEnabled:        settings[SettingKeyAliyunCaptchaEnabled] == "true",
		AliyunCaptchaSceneID:        settings[SettingKeyAliyunCaptchaSceneID],
		AliyunCaptchaPrefix:         settings[SettingKeyAliyunCaptchaPrefix],
		AliyunCaptchaRegion:         input.Auth.AliyunRegion,
		SiteName:                    s.getStringOrDefault(settings, SettingKeySiteName, "TokenRouter"),
		SiteLogo:                    settings[SettingKeySiteLogo],
		SiteShareTitle:              settings["site_share_title"],
		SiteShareDescription:        settings["site_share_description"],
		SiteShareImageURL:           shareImagePath(cover),
		SiteShareImageType:          cover.Type,
		SiteShareImageWidth:         cover.Width,
		SiteShareImageHeight:        cover.Height,
		FrontendURL:                 settings[SettingKeyFrontendURL],
		SiteSubtitle:                settings[SettingKeySiteSubtitle],
		APIBaseURL:                  settings[SettingKeyAPIBaseURL],
		ContactInfo:                 settings[SettingKeyContactInfo],
		DocURL:                      settings[SettingKeyDocURL],
		HomeContent:                 settings[SettingKeyHomeContent],
		HideCcsImportButton:         settings[SettingKeyHideCcsImportButton] == "true",
		PurchaseSubscriptionEnabled: settings[SettingKeyPurchaseSubscriptionEnabled] == "true",
		PurchaseSubscriptionURL:     strings.TrimSpace(settings[SettingKeyPurchaseSubscriptionURL]),
		TableDefaultPageSize:        tableDefaultPageSize,
		TablePageSizeOptions:        tablePageSizeOptions,
		UsageRankingLimit:           usageRanking.Limit,
		UsageRankingEnabled:         usageRanking.Enabled,
		UsageRankingSortBy:          usageRanking.SortBy,
		UsageRankingShowTotalTokens: usageRanking.ShowTotalTokens,
		UsageRankingShowRequests:    usageRanking.ShowRequests,
		UsageRankingShowActualCost:  usageRanking.ShowActualCost,
		CustomMenuItems:             settings[SettingKeyCustomMenuItems],
		CustomEndpoints:             settings[SettingKeyCustomEndpoints],
		FooterLinks:                 settings[SettingKeyFooterLinks],
		FooterText:                  settings[SettingKeyFooterText],
		HomeFeaturedModels:          settings[SettingKeyHomeFeaturedModels],
		LinuxDoOAuthEnabled:         input.Auth.LinuxDo,
		DingTalkOAuthEnabled:        input.Auth.DingTalk,
		WeChatOAuthEnabled:          input.Auth.WeChat,
		WeChatOAuthOpenEnabled:      input.Auth.WeChatOpen,
		WeChatOAuthMPEnabled:        input.Auth.WeChatMP,
		WeChatOAuthMobileEnabled:    input.Auth.WeChatMobile,
		BackendModeEnabled:          settings[SettingKeyBackendModeEnabled] == "true",
		PaymentEnabled:              settings[SettingPaymentEnabled] == "true",
		OIDCOAuthEnabled:            input.Auth.OIDC,
		OIDCOAuthProviderName:       locale.ResolveSettingText(settings, "oidc_connect_provider_name", input.Auth.OIDCName, locale.FromContext(ctx)),
		GitHubOAuthEnabled:          input.Auth.GitHub,
		GoogleOAuthEnabled:          input.Auth.Google,
		GoogleOneTapEnabled:         input.Auth.GoogleOneTap,
		GoogleOAuthClientID:         input.Auth.GoogleClientID,
		BalanceUnitName:             balanceUnitName,
		BalanceUnitSymbol:           balanceUnitSymbol,
		BalanceIconSVG:              strings.TrimSpace(settings[SettingKeyBalanceIconSVG]),
		BalanceLowNotifyEnabled:     settings[SettingKeyBalanceLowNotifyEnabled] == "true",
		ProviderQuotaNotifyEnabled:  settings[SettingKeyProviderQuotaNotifyEnabled] == "true",
		RiskControlEnabled:          settings[SettingKeyRiskControlEnabled] == "true",
		BalanceLowNotifyThreshold:   balanceLowNotifyThreshold,
		BalanceLowNotifyRechargeURL: locale.ResolveSettingText(settings, SettingKeyBalanceLowNotifyRechargeURL, "", locale.FromContext(ctx)),
		AllowUserViewErrorRequests:  settings[SettingKeyAllowUserViewErrorRequests] == "true",
	}, nil
}

// getStringOrDefault returns the supplied default for an absent or empty display value.
// Whitespace values remain unchanged because this helper checks exact emptiness.
// Callers provide values from their already resolved settings map and choose
// the default appropriate to that displayed field.
func (s *PublicService) getStringOrDefault(settings map[string]string, key, defaultValue string) string {
	if value, ok := settings[key]; ok && value != "" {
		return value
	}
	return defaultValue
}

// NormalizeLoginAgreementMode returns checkbox for that recognized case-insensitive mode.
// Input is trimmed and lowercased. Empty or unsupported mode names select
// the built-in modal default used by administrator and public settings readers.
func NormalizeLoginAgreementMode(raw string) string {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "checkbox":
		return "checkbox"
	default:
		return defaultLoginAgreementMode
	}
}

// DefaultLoginAgreementDocuments returns a fresh list of the four built-in legal identifiers.
// Each document starts with its source title and an empty Markdown body.
// Callers can select localized labels or load content before presenting individual documents.
func DefaultLoginAgreementDocuments() []LoginAgreementDocument {
	return []LoginAgreementDocument{
		{
			ID:        "terms",
			Title:     "服务条款",
			ContentMD: "",
		},
		{
			ID:        "usage-policy",
			Title:     "使用政策",
			ContentMD: "",
		},
		{
			ID:        "supported-regions",
			Title:     "支持的国家和地区",
			ContentMD: "",
		},
		{
			ID:        "service-specific-terms",
			Title:     "服务特定条款",
			ContentMD: "",
		},
	}
}

// NormalizeLoginAgreementDocumentID returns a lowercase ASCII identifier from a supplied document name.
// It keeps letters and digits, normalizes supported separators, drops other
// characters, and trims edge separators. An empty result lets the caller
// synthesize an identifier from the document's position and source copy.
func NormalizeLoginAgreementDocumentID(raw string) string {
	raw = strings.ToLower(strings.TrimSpace(raw))
	var b strings.Builder
	lastSeparator := false
	// Builder rune writes append to memory and return nil errors. Discard their
	// error results while copying accepted ASCII characters and normalized separators into this identifier.
	for _, r := range raw {
		// Document identifiers use ASCII even when displayed legal titles contain
		// other scripts. The document normalizer supplies a digest ID when filtering leaves no identifier.
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') {
			_, _ = b.WriteRune(r)
			lastSeparator = false
			continue
		}
		if r == '-' || r == '_' || r == ' ' || r == '.' || r == '/' {
			if !lastSeparator && b.Len() > 0 {
				if r == '_' {
					_, _ = b.WriteRune('_')
				} else {
					_, _ = b.WriteRune('-')
				}
				lastSeparator = true
			}
		}
	}
	return strings.Trim(b.String(), "-_")
}

// NormalizeLoginAgreementDocuments returns nonblank documents with normalized unique identifiers.
// Title and Markdown are trimmed; entries with neither are skipped. The outer
// slice is new, while retained Localization pointers stay shared with the input.
// Resolution metadata is omitted from the normalized stored representation.
//
// Missing identifiers use a digest of original position and trimmed source
// copy. Repeated normalized identifiers receive numeric suffixes beginning at
// two, preserving source order while distinguishing entries that would otherwise
// share a consent-document identity. Source content remains paired with its
// existing localization object during this normalization.
func NormalizeLoginAgreementDocuments(docs []LoginAgreementDocument) []LoginAgreementDocument {
	normalized := make([]LoginAgreementDocument, 0, len(docs))
	seen := make(map[string]int, len(docs))
	for i, doc := range docs {
		title := strings.TrimSpace(doc.Title)
		content := strings.TrimSpace(doc.ContentMD)
		if title == "" && content == "" {
			continue
		}
		id := NormalizeLoginAgreementDocumentID(doc.ID)
		if id == "" {
			// Filtering a legacy identifier can leave it empty. Include position and
			// source copy in its synthesized identity so the normalized list can still distinguish that entry.
			sum := sha256.Sum256([]byte(fmt.Sprintf("%d:%s:%s", i, title, content)))
			// Twelve hexadecimal characters keep synthesized document identifiers compact.
			id = hex.EncodeToString(sum[:])[:12]
		}
		baseID := id
		// Distinct entries can normalize to the same name. Assign a numeric
		// suffix before appending so each returned document retains an independently selectable identifier.
		for suffix := 2; seen[id] > 0; suffix++ {
			id = fmt.Sprintf("%s-%d", baseID, suffix)
		}
		seen[id]++
		normalized = append(normalized, LoginAgreementDocument{
			ID:           id,
			Localization: doc.Localization,
			Title:        title,
			ContentMD:    content,
		})
	}
	return normalized
}

// ParseLoginAgreementDocuments returns normalized stored agreements or the built-in legal list.
// Blank input, malformed JSON, and lists with no nonblank normalized documents
// select defaults. Valid entries retain source order and receive unique normalized IDs.
func ParseLoginAgreementDocuments(raw string) []LoginAgreementDocument {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return DefaultLoginAgreementDocuments()
	}
	var docs []LoginAgreementDocument
	// Unreadable agreement JSON cannot supply a usable consent list. Return
	// the built-in identifiers and titles so public and administrator readers share their default documents.
	if err := json.Unmarshal([]byte(raw), &docs); err != nil {
		return DefaultLoginAgreementDocuments()
	}
	docs = NormalizeLoginAgreementDocuments(docs)
	if len(docs) == 0 {
		// A saved list can normalize to no source copy. Reuse the same default
		// legal list selected for missing input instead of publishing an empty agreement collection.
		return DefaultLoginAgreementDocuments()
	}
	return docs
}

// MarshalLoginAgreementDocuments serializes normalized source agreements for the settings write.
// When every entry is blank, it serializes the built-in legal list. Retained
// localization pointers are included by normalization, while resolution metadata is dropped.
func MarshalLoginAgreementDocuments(docs []LoginAgreementDocument) (string, error) {
	normalized := NormalizeLoginAgreementDocuments(docs)
	if len(normalized) == 0 {
		// Empty normalized writes use the reader's built-in document list, keeping
		// saved legal defaults consistent with the list chosen on missing or unusable reads.
		normalized = DefaultLoginAgreementDocuments()
	}
	b, err := json.Marshal(normalized)
	if err != nil {
		return "", fmt.Errorf("marshal login agreement documents: %w", err)
	}
	return string(b), nil
}

// BuildLoginAgreementRevision returns a consent fingerprint from the trimmed date and normalized source copy.
// It normalizes document IDs and source fields before encoding, then drops
// localization and resolution metadata from its copied outer list. Language
// selection and translation edits therefore keep the same consent identity.
//
// The first sixteen hexadecimal SHA-256 characters label that source payload.
// If payload marshaling fails, the digest uses the trimmed date alone. Callers
// calculate this revision before resolving display copy, ensuring translated
// title/body values do not replace source values in the consent calculation.
func BuildLoginAgreementRevision(updatedAt string, docs []LoginAgreementDocument) string {
	normalized := NormalizeLoginAgreementDocuments(docs)
	for i := range normalized {
		// Normalization allocated these outer records. Remove display/editor metadata
		// on that copy so consent hashing leaves the caller's localization pointers attached to its documents.
		normalized[i].Localization = nil
		normalized[i].Resolution = nil
	}
	payload, err := json.Marshal(struct {
		UpdatedAt string                   `json:"updated_at"` // trimmed consent date
		Documents []LoginAgreementDocument `json:"documents"`  // normalized source copies
	}{
		UpdatedAt: strings.TrimSpace(updatedAt),
		Documents: normalized,
	})
	if err != nil {
		// A serialization failure leaves the date as the available revision input.
		// Hash that value so callers still receive a printable fingerprint for their consent metadata.
		payload = []byte(strings.TrimSpace(updatedAt))
	}
	sum := sha256.Sum256(payload)
	// Sixteen hexadecimal characters form the public consent revision label.
	return hex.EncodeToString(sum[:])[:16]
}

// GetLegalDocument returns the matching agreement from settings resolved for the request language.
// Lookup compares the supplied ID exactly with normalized document IDs. A
// missing ID returns ErrPageNotFound, while settings-source failures propagate to the caller.
func (s *PublicService) GetLegalDocument(ctx context.Context, id string) (*LoginAgreementDocument, error) {
	settings, err := s.GetPublicSettings(ctx)
	if err != nil {
		return nil, err
	}
	for _, doc := range settings.LoginAgreementDocuments {
		if doc.ID == id {
			return &doc, nil
		}
	}
	return nil, ErrPageNotFound
}

// GetPublicSettingsForInjection returns public settings for HTML injection,
// with legal document bodies omitted.
// It implements web.PublicSettingsProvider. Source errors abort assembly; the
// returned value uses frontend JSON tags and includes the source's build label
// plus the injected calendar's current UTC offset.
//
// The registration-domain slice retains the input source's storage. Renderers
// leave that slice unchanged; callers that edit domain entries copy it before
// mutation so they do not change data shared with the source.
func (s *PublicService) GetPublicSettingsForInjection(ctx context.Context) (any, error) {
	settings, err := s.GetPublicSettings(ctx)
	if err != nil {
		return nil, err
	}

	for i := range settings.LoginAgreementDocuments {
		// First-screen HTML needs consent titles and revision metadata. Clear only
		// the resolved Markdown bodies here because individual legal reads supply that content on demand.
		settings.LoginAgreementDocuments[i].ContentMD = ""
	}

	// JSON tags match the settings object consumed by the frontend.
	return &struct {
		RegistrationEnabled                 bool                         `json:"registration_enabled"`                    // registration entry switch
		EmailVerifyEnabled                  bool                         `json:"email_verify_enabled"`                    // email verification switch
		ForceEmailOnThirdPartySignup        bool                         `json:"force_email_on_third_party_signup"`       // third-party email requirement
		RegistrationEmailSuffixWhitelist    []string                     `json:"registration_email_suffix_whitelist"`     // permitted registration domains
		RegistrationEmailDomainQuotaEnabled bool                         `json:"registration_email_domain_quota_enabled"` // outside-domain quota switch
		UserEmailChangeEnabled              bool                         `json:"user_email_change_enabled"`               // Allows users with an email address to replace their primary email.
		PromoCodeEnabled                    bool                         `json:"promo_code_enabled"`                      // promo code availability
		PasswordResetEnabled                bool                         `json:"password_reset_enabled"`                  // password reset availability
		InvitationCodeEnabled               bool                         `json:"invitation_code_enabled"`                 // invitation code requirement
		TotpEnabled                         bool                         `json:"totp_enabled"`                            // TOTP availability switch
		PasskeyEnabled                      bool                         `json:"passkey_enabled"`                         // WebAuthn login availability
		LoginAgreementEnabled               bool                         `json:"login_agreement_enabled"`                 // consent prompt availability
		LoginAgreementMode                  string                       `json:"login_agreement_mode"`                    // consent presentation mode
		LoginAgreementUpdatedAt             string                       `json:"login_agreement_updated_at"`              // agreement revision label
		LoginAgreementRevision              string                       `json:"login_agreement_revision"`                // source consent fingerprint
		LoginAgreementDocuments             []LoginAgreementDocument     `json:"login_agreement_documents"`               // selected legal summaries
		TurnstileEnabled                    bool                         `json:"turnstile_enabled"`                       // Turnstile challenge switch
		TurnstileSiteKey                    string                       `json:"turnstile_site_key,omitempty"`            // browser Turnstile identifier
		TencentCaptchaEnabled               bool                         `json:"tencent_captcha_enabled"`                 // Tencent challenge switch
		TencentCaptchaAppID                 string                       `json:"tencent_captcha_app_id,omitempty"`        // browser Tencent identifier
		TencentCaptchaRegion                string                       `json:"tencent_captcha_region,omitempty"`        // Tencent SDK region
		AliyunCaptchaEnabled                bool                         `json:"aliyun_captcha_enabled"`                  // Aliyun challenge switch
		AliyunCaptchaSceneID                string                       `json:"aliyun_captcha_scene_id,omitempty"`       // Aliyun scene identifier
		AliyunCaptchaPrefix                 string                       `json:"aliyun_captcha_prefix,omitempty"`         // Aliyun frontend prefix
		AliyunCaptchaRegion                 string                       `json:"aliyun_captcha_region,omitempty"`         // Aliyun SDK region
		Locale                              string                       `json:"locale"`                                  // selected request language
		SiteTextOverrides                   []string                     `json:"site_text_overrides"`                     // configured text override keys
		TextLanguages                       map[string]locale.Resolution `json:"text_languages"`                          // per-field language resolution
		DefaultLocale                       string                       `json:"default_locale"`                          // configured default language
		SiteTitle                           string                       `json:"site_title"`                              // resolved browser title
		SiteName                            string                       `json:"site_name"`                               // resolved branding name
		SiteLogo                            string                       `json:"site_logo,omitempty"`                     // branding image location
		SiteShareTitle                      string                       `json:"site_share_title"`                        // resolved sharing title
		SiteShareDescription                string                       `json:"site_share_description"`                  // resolved sharing description
		SiteShareImageURL                   string                       `json:"site_share_image_url"`                    // public cover resource path
		SiteShareImageType                  string                       `json:"site_share_image_type"`                   // validated cover MIME type
		SiteShareImageWidth                 int                          `json:"site_share_image_width"`                  // cover width in pixels
		SiteShareImageHeight                int                          `json:"site_share_image_height"`                 // cover height in pixels
		FrontendURL                         string                       `json:"frontend_url"`                            // configured frontend base URL
		SiteSubtitle                        string                       `json:"site_subtitle,omitempty"`                 // resolved branding subtitle
		APIBaseURL                          string                       `json:"api_base_url,omitempty"`                  // advertised API base
		ContactInfo                         string                       `json:"contact_info,omitempty"`                  // resolved contact details
		DocURL                              string                       `json:"doc_url,omitempty"`                       // resolved documentation link
		HomeContent                         string                       `json:"home_content,omitempty"`                  // resolved homepage content
		HideCcsImportButton                 bool                         `json:"hide_ccs_import_button"`                  // hide import action
		PurchaseSubscriptionEnabled         bool                         `json:"purchase_subscription_enabled"`           // subscription purchase switch
		PurchaseSubscriptionURL             string                       `json:"purchase_subscription_url,omitempty"`     // resolved purchase destination
		TableDefaultPageSize                int                          `json:"table_default_page_size"`                 // default rows per page
		TablePageSizeOptions                []int                        `json:"table_page_size_options"`                 // selectable page row counts
		UsageRankingLimit                   int                          `json:"usage_ranking_limit"`                     // ranking entry limit
		UsageRankingEnabled                 bool                         `json:"usage_ranking_enabled"`                   // ranking display switch
		UsageRankingSortBy                  string                       `json:"usage_ranking_sort_by"`                   // ranking sort metric
		UsageRankingShowTotalTokens         bool                         `json:"usage_ranking_show_total_tokens"`         // display total tokens
		UsageRankingShowRequests            bool                         `json:"usage_ranking_show_requests"`             // display request counts
		UsageRankingShowActualCost          bool                         `json:"usage_ranking_show_actual_cost"`          // display charged cost
		CustomMenuItems                     json.RawMessage              `json:"custom_menu_items"`                       // filtered menu JSON
		CustomEndpoints                     json.RawMessage              `json:"custom_endpoints"`                        // resolved endpoint JSON
		FooterLinks                         json.RawMessage              `json:"footer_links"`                            // resolved footer JSON
		FooterText                          string                       `json:"footer_text,omitempty"`                   // resolved footer text
		HomeFeaturedModels                  json.RawMessage              `json:"home_featured_models"`                    // featured model JSON
		LinuxDoOAuthEnabled                 bool                         `json:"linuxdo_oauth_enabled"`                   // LinuxDo login availability
		DingTalkOAuthEnabled                bool                         `json:"dingtalk_oauth_enabled"`                  // DingTalk login availability
		WeChatOAuthEnabled                  bool                         `json:"wechat_oauth_enabled"`                    // WeChat login availability
		WeChatOAuthOpenEnabled              bool                         `json:"wechat_oauth_open_enabled"`               // WeChat open-platform availability
		WeChatOAuthMPEnabled                bool                         `json:"wechat_oauth_mp_enabled"`                 // WeChat public-account availability
		WeChatOAuthMobileEnabled            bool                         `json:"wechat_oauth_mobile_enabled"`             // WeChat mobile-login availability
		BackendModeEnabled                  bool                         `json:"backend_mode_enabled"`                    // backend mode switch
		PaymentEnabled                      bool                         `json:"payment_enabled"`                         // payment entry switch
		TeamEnabled                         bool                         `json:"team_enabled"`                            // team feature availability
		TeamSelfServiceEnabled              bool                         `json:"team_self_service_enabled"`               // team self-service availability
		CreativeEnabled                     bool                         `json:"creative_enabled"`                        // creative studio availability
		OIDCOAuthEnabled                    bool                         `json:"oidc_oauth_enabled"`                      // OIDC login availability
		OIDCOAuthProviderName               string                       `json:"oidc_oauth_provider_name"`                // resolved OIDC label
		GitHubOAuthEnabled                  bool                         `json:"github_oauth_enabled"`                    // GitHub login availability
		GoogleOAuthEnabled                  bool                         `json:"google_oauth_enabled"`                    // Google login availability
		GoogleOneTapEnabled                 bool                         `json:"google_one_tap_enabled"`                  // One Tap availability
		GoogleOAuthClientID                 string                       `json:"google_oauth_client_id"`                  // browser Google identifier
		Version                             string                       `json:"version,omitempty"`                       // source build label
		// ServerTimezone and ServerUTCOffset combine the configured label with the
		// injected calendar's current offset. The offset is computed when building
		// injection so daylight-saving changes can affect later HTML responses.
		ServerTimezone              string  `json:"server_timezone"`                 // configured timezone label
		ServerUTCOffset             string  `json:"server_utc_offset"`               // current UTC offset
		BalanceUnitName             string  `json:"balance_unit_name"`               // localized balance unit
		BalanceUnitSymbol           string  `json:"balance_unit_symbol"`             // displayed unit symbol
		BalanceIconSVG              string  `json:"balance_icon_svg"`                // configured balance icon
		BalanceLowNotifyEnabled     bool    `json:"balance_low_notify_enabled"`      // low-balance email switch
		ProviderQuotaNotifyEnabled  bool    `json:"provider_quota_notify_enabled"`   // provider quota email switch
		RiskControlEnabled          bool    `json:"risk_control_enabled"`            // risk center availability
		AffiliateEnabled            bool    `json:"affiliate_enabled"`               // affiliate entry availability
		BalanceLowNotifyThreshold   float64 `json:"balance_low_notify_threshold"`    // low balance threshold
		BalanceLowNotifyRechargeURL string  `json:"balance_low_notify_recharge_url"` // resolved recharge destination
		AllowUserViewErrorRequests  bool    `json:"allow_user_view_error_requests"`  // failed-request view permission
	}{
		RegistrationEnabled:                 settings.RegistrationEnabled,
		EmailVerifyEnabled:                  settings.EmailVerifyEnabled,
		ForceEmailOnThirdPartySignup:        settings.ForceEmailOnThirdPartySignup,
		RegistrationEmailSuffixWhitelist:    settings.RegistrationEmailSuffixWhitelist,
		RegistrationEmailDomainQuotaEnabled: settings.RegistrationEmailDomainQuotaEnabled,
		UserEmailChangeEnabled:              settings.UserEmailChangeEnabled,
		PromoCodeEnabled:                    settings.PromoCodeEnabled,
		PasswordResetEnabled:                settings.PasswordResetEnabled,
		InvitationCodeEnabled:               settings.InvitationCodeEnabled,
		TotpEnabled:                         settings.TotpEnabled,
		PasskeyEnabled:                      settings.PasskeyEnabled,
		LoginAgreementEnabled:               settings.LoginAgreementEnabled,
		LoginAgreementMode:                  settings.LoginAgreementMode,
		LoginAgreementUpdatedAt:             settings.LoginAgreementUpdatedAt,
		LoginAgreementRevision:              settings.LoginAgreementRevision,
		LoginAgreementDocuments:             settings.LoginAgreementDocuments,
		TurnstileEnabled:                    settings.TurnstileEnabled,
		TurnstileSiteKey:                    settings.TurnstileSiteKey,
		TencentCaptchaEnabled:               settings.TencentCaptchaEnabled,
		TencentCaptchaAppID:                 settings.TencentCaptchaAppID,
		TencentCaptchaRegion:                settings.TencentCaptchaRegion,
		AliyunCaptchaEnabled:                settings.AliyunCaptchaEnabled,
		AliyunCaptchaSceneID:                settings.AliyunCaptchaSceneID,
		AliyunCaptchaPrefix:                 settings.AliyunCaptchaPrefix,
		AliyunCaptchaRegion:                 settings.AliyunCaptchaRegion,
		Locale:                              settings.Locale,
		SiteTextOverrides:                   settings.SiteTextOverrides,
		TextLanguages:                       settings.TextLanguages,
		DefaultLocale:                       settings.DefaultLocale,
		SiteTitle:                           settings.SiteTitle,
		SiteName:                            settings.SiteName,
		SiteLogo:                            settings.SiteLogo,
		SiteShareTitle:                      settings.SiteShareTitle,
		SiteShareDescription:                settings.SiteShareDescription,
		SiteShareImageURL:                   settings.SiteShareImageURL,
		SiteShareImageType:                  settings.SiteShareImageType,
		SiteShareImageWidth:                 settings.SiteShareImageWidth,
		SiteShareImageHeight:                settings.SiteShareImageHeight,
		FrontendURL:                         settings.FrontendURL,
		SiteSubtitle:                        settings.SiteSubtitle,
		APIBaseURL:                          settings.APIBaseURL,
		ContactInfo:                         settings.ContactInfo,
		DocURL:                              settings.DocURL,
		HomeContent:                         settings.HomeContent,
		HideCcsImportButton:                 settings.HideCcsImportButton,
		PurchaseSubscriptionEnabled:         settings.PurchaseSubscriptionEnabled,
		PurchaseSubscriptionURL:             settings.PurchaseSubscriptionURL,
		TableDefaultPageSize:                settings.TableDefaultPageSize,
		TablePageSizeOptions:                settings.TablePageSizeOptions,
		UsageRankingLimit:                   settings.UsageRankingLimit,
		UsageRankingEnabled:                 settings.UsageRankingEnabled,
		UsageRankingSortBy:                  settings.UsageRankingSortBy,
		UsageRankingShowTotalTokens:         settings.UsageRankingShowTotalTokens,
		UsageRankingShowRequests:            settings.UsageRankingShowRequests,
		UsageRankingShowActualCost:          settings.UsageRankingShowActualCost,
		CustomMenuItems:                     FilterUserVisibleMenuItems(settings.CustomMenuItems),
		CustomEndpoints:                     SafeRawJSONArray(settings.CustomEndpoints),
		FooterLinks:                         SafeRawJSONArray(settings.FooterLinks),
		FooterText:                          settings.FooterText,
		HomeFeaturedModels:                  SafeRawJSONArray(settings.HomeFeaturedModels),
		LinuxDoOAuthEnabled:                 settings.LinuxDoOAuthEnabled,
		DingTalkOAuthEnabled:                settings.DingTalkOAuthEnabled,
		WeChatOAuthEnabled:                  settings.WeChatOAuthEnabled,
		WeChatOAuthOpenEnabled:              settings.WeChatOAuthOpenEnabled,
		WeChatOAuthMPEnabled:                settings.WeChatOAuthMPEnabled,
		WeChatOAuthMobileEnabled:            settings.WeChatOAuthMobileEnabled,
		BackendModeEnabled:                  settings.BackendModeEnabled,
		PaymentEnabled:                      settings.PaymentEnabled,
		TeamEnabled:                         settings.TeamEnabled,
		TeamSelfServiceEnabled:              settings.TeamSelfServiceEnabled,
		CreativeEnabled:                     settings.CreativeEnabled,
		OIDCOAuthEnabled:                    settings.OIDCOAuthEnabled,
		OIDCOAuthProviderName:               settings.OIDCOAuthProviderName,
		GitHubOAuthEnabled:                  settings.GitHubOAuthEnabled,
		GoogleOAuthEnabled:                  settings.GoogleOAuthEnabled,
		GoogleOneTapEnabled:                 settings.GoogleOneTapEnabled,
		GoogleOAuthClientID:                 settings.GoogleOAuthClientID,
		Version:                             s.source.PublicVersion(),
		ServerTimezone:                      s.timezoneName,
		ServerUTCOffset:                     s.calendar.UTCOffset(s.calendar.Now()),
		BalanceUnitName:                     settings.BalanceUnitName,
		BalanceUnitSymbol:                   settings.BalanceUnitSymbol,
		BalanceIconSVG:                      settings.BalanceIconSVG,
		BalanceLowNotifyEnabled:             settings.BalanceLowNotifyEnabled,
		ProviderQuotaNotifyEnabled:          settings.ProviderQuotaNotifyEnabled,
		RiskControlEnabled:                  settings.RiskControlEnabled,
		AffiliateEnabled:                    settings.AffiliateEnabled,
		BalanceLowNotifyThreshold:           settings.BalanceLowNotifyThreshold,
		BalanceLowNotifyRechargeURL:         settings.BalanceLowNotifyRechargeURL,
		AllowUserViewErrorRequests:          settings.AllowUserViewErrorRequests,
	}, nil
}

// FilterUserVisibleMenuItems returns non-admin menu entries while retaining their complete JSON fields.
// Empty input and failures in either decode or final encoding produce an
// empty array. Items with other visibility values stay in their input order.
//
// One decode selects entries by visibility, and another retains each full
// object as raw JSON. Filtering by matching index keeps unknown fields attached
// to the corresponding item, so HTML injection can carry configuration fields
// beyond those the visibility selector knows how to interpret.
func FilterUserVisibleMenuItems(raw string) json.RawMessage {
	raw = strings.TrimSpace(raw)
	if raw == "" || raw == "[]" {
		return json.RawMessage("[]")
	}
	var items []struct {
		Visibility string `json:"visibility"` // exact access class
	}
	// Malformed menu JSON leaves injection with an empty menu list; partial
	// visibility results cannot safely identify which complete raw item should be retained.
	if err := json.Unmarshal([]byte(raw), &items); err != nil {
		return json.RawMessage("[]")
	}

	// Decode complete items separately so fields beyond visibility remain paired
	// with their selected item. An unusable full-item list also becomes an empty menu array.
	var fullItems []json.RawMessage
	if err := json.Unmarshal([]byte(raw), &fullItems); err != nil {
		return json.RawMessage("[]")
	}

	var filtered []json.RawMessage
	for i, item := range items {
		if item.Visibility != "admin" {
			filtered = append(filtered, fullItems[i])
		}
	}
	if len(filtered) == 0 {
		return json.RawMessage("[]")
	}
	result, err := json.Marshal(filtered)
	if err != nil {
		// A failed filtered encoding cannot be embedded as valid frontend JSON.
		// Supply an empty menu array so injection keeps a usable serialized settings object.
		return json.RawMessage("[]")
	}
	return result
}

// SafeRawJSONArray returns valid JSON as a RawMessage, or [] for empty or invalid
// input. Valid objects, scalars, and null are also accepted because this helper
// checks JSON validity rather than requiring an array-shaped value from storage.
func SafeRawJSONArray(raw string) json.RawMessage {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return json.RawMessage("[]")
	}
	if json.Valid([]byte(raw)) {
		return json.RawMessage(raw)
	}
	// Invalid JSON cannot be embedded in the settings object's serialized output.
	// Replace it with an empty array while preserving other independently resolved public settings.
	return json.RawMessage("[]")
}

// GetFrameSrcOrigins collects unique HTTP(S) origins across supported languages
// from home content, enabled subscription links, and menu URLs for CSP frame-src.
// Each supported language resolves its settings independently, and any source
// failure aborts collection. Menu origins include administrator entries because
// the same browser policy also serves administrator navigation.
//
// Origins keep their first-encountered order across the language manifest and
// each selected settings view. Deduplication happens after URL-to-origin
// conversion, so different paths at one scheme and host contribute one policy entry.
func (s *PublicService) GetFrameSrcOrigins(ctx context.Context) ([]string, error) {
	seen := make(map[string]struct{})
	var origins []string

	addOrigin := func(rawURL string) {
		if origin := ExtractOriginFromURL(rawURL); origin != "" {
			if _, ok := seen[origin]; !ok {
				seen[origin] = struct{}{}
				origins = append(origins, origin)
			}
		}
	}

	for _, definition := range locale.Definitions() {
		settings, err := s.GetPublicSettings(locale.WithLanguage(ctx, definition.Code))
		if err != nil {
			return nil, err
		}
		// Home content may be an iframe URL.
		addOrigin(settings.HomeContent)

		// Disabled subscription links do not need a frame-src origin.
		if settings.PurchaseSubscriptionEnabled {
			addOrigin(settings.PurchaseSubscriptionURL)
		}

		// Admin menu entries also need their iframe origins allowed by CSP.
		for _, item := range ParseCustomMenuItemURLs(settings.CustomMenuItems) {
			addOrigin(item)
		}
	}

	return origins, nil
}

// ExtractOriginFromURL returns an HTTP or HTTPS scheme and host for CSP origin entries.
// Input is trimmed. Empty, unparsable, relative, or other-scheme values produce
// an empty string; paths, queries, and fragments are excluded from accepted origins.
func ExtractOriginFromURL(rawURL string) string {
	rawURL = strings.TrimSpace(rawURL)
	if rawURL == "" {
		return ""
	}
	u, err := url.Parse(rawURL)
	// Relative homepage content and malformed links supply no network origin.
	// Treat them as absent CSP additions while the existing configured frame policy remains available.
	if err != nil || u.Host == "" {
		return ""
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return ""
	}
	return u.Scheme + "://" + u.Host
}

// ParseCustomMenuItemURLs returns nonempty menu URL strings in their configured order.
// Blank input, an empty array, or decode failure returns nil. It includes
// administrator entries and leaves URL validation to ExtractOriginFromURL during CSP collection.
func ParseCustomMenuItemURLs(raw string) []string {
	raw = strings.TrimSpace(raw)
	if raw == "" || raw == "[]" {
		return nil
	}
	var items []struct {
		URL string `json:"url"` // configured menu destination
	}
	// Malformed navigation cannot supply reliable iframe destinations. Discard
	// partially decoded URLs so a failed menu read contributes no origins to CSP collection.
	if err := json.Unmarshal([]byte(raw), &items); err != nil {
		return nil
	}
	urls := make([]string, 0, len(items))
	for _, item := range items {
		if item.URL != "" {
			urls = append(urls, item.URL)
		}
	}
	return urls
}

// NormalizeTablePreferences returns a usable default page size and sorted unique choices.
// Options outside five through one thousand rows are discarded. An invalid
// default selects twenty, and an empty usable option set selects ten, twenty,
// and fifty; the returned slice is allocated separately from input.
//
// Default and option validity are handled independently. The resulting default
// does not have to occur in the choices, so consumers retain the supplied
// default size even when administrators offer a different set of valid choices.
// Duplicates and order are normalized for browser selectors and stored preferences.
func NormalizeTablePreferences(defaultPageSize int, options []int) (int, []int) {
	// Five rows is the smallest supported pagination preference.
	const minPageSize = 5
	// One thousand rows is the largest supported preference.
	const maxPageSize = 1000
	// Invalid default sizes select twenty rows per page.
	const fallbackPageSize = 20

	seen := make(map[int]struct{}, len(options))
	normalizedOptions := make([]int, 0, len(options))
	for _, option := range options {
		if option < minPageSize || option > maxPageSize {
			continue
		}
		if _, ok := seen[option]; ok {
			continue
		}
		seen[option] = struct{}{}
		normalizedOptions = append(normalizedOptions, option)
	}
	sort.Ints(normalizedOptions)

	if defaultPageSize < minPageSize || defaultPageSize > maxPageSize {
		// An unusable default selects twenty independently of the available option
		// set, which is normalized separately and can retain valid administrator-provided choices.
		defaultPageSize = fallbackPageSize
	}

	if len(normalizedOptions) == 0 {
		// A wholly unusable choice set supplies the standard browser page sizes;
		// the separately normalized default still determines the initially selected row count.
		normalizedOptions = []int{10, 20, 50}
	}

	return defaultPageSize, normalizedOptions
}

// ParseTablePreferences decodes stored pagination strings and applies shared preference normalization.
// Invalid default text keeps twenty before range checks. Option decode errors
// retain any decoded integers; normalization removes unusable entries and supplies
// standard choices when none remain.
func ParseTablePreferences(defaultPageSizeRaw, optionsRaw string) (int, []int) {
	// A missing or failed decimal default uses twenty until the shared normalizer
	// applies range handling, allowing option parsing to proceed independently of that read failure.
	defaultPageSize := 20
	if v, err := strconv.Atoi(strings.TrimSpace(defaultPageSizeRaw)); err == nil {
		defaultPageSize = v
	}

	var options []int
	if strings.TrimSpace(optionsRaw) != "" {
		// Stored option arrays can fail decoding after populating usable integers.
		// Keep that result for the shared range/deduplication rules instead of discarding the whole preference set.
		_ = json.Unmarshal([]byte(optionsRaw), &options)
	}

	return NormalizeTablePreferences(defaultPageSize, options)
}

// PublicValueKeys returns a fresh allowlist of display strings exposed to PublicService.
// InputSource copies these keys from its larger storage batch before returning
// Values, retaining public copy while credential settings stay inputs to authentication conversion.
func PublicValueKeys() []string {
	return []string{
		SettingKeySiteShareImageMeta,
		SettingKeyFrontendURL,
		"balance_unit_name_localized",
		"oidc_connect_provider_name_localized",
		"balance_low_notify_recharge_url_localized",
		SettingKeySiteTexts,
		SettingKeyDefaultLocale,
		SettingKeyAPIBaseURL,
		SettingKeyProviderQuotaNotifyEnabled,
		SettingKeyAffiliateEnabled,
		SettingKeyAliyunCaptchaEnabled,
		SettingKeyAliyunCaptchaPrefix,
		SettingKeyAliyunCaptchaSceneID,
		SettingKeyAllowUserViewErrorRequests,
		SettingKeyBackendModeEnabled,
		SettingKeyBalanceIconSVG,
		SettingKeyBalanceLowNotifyEnabled,
		SettingKeyBalanceLowNotifyRechargeURL,
		SettingKeyBalanceLowNotifyThreshold,
		SettingKeyBalanceUnitName,
		SettingKeyBalanceUnitSymbol,
		SettingKeyContactInfo,
		SettingKeyCreativeEnabled,
		SettingKeyCustomEndpoints,
		SettingKeyCustomMenuItems,
		SettingKeyDocURL,
		SettingKeyEmailVerifyEnabled,
		SettingKeyFooterLinks,
		SettingKeyFooterText,
		SettingKeyForceEmailOnThirdPartySignup,
		SettingKeyHideCcsImportButton,
		SettingKeyHomeContent,
		SettingKeyHomeFeaturedModels,
		SettingKeyInvitationCodeEnabled,
		SettingKeyLoginAgreementDocuments,
		SettingKeyLoginAgreementEnabled,
		SettingKeyLoginAgreementMode,
		SettingKeyLoginAgreementUpdatedAt,
		SettingKeyPasswordResetEnabled,
		SettingKeyPromoCodeEnabled,
		SettingKeyPurchaseSubscriptionEnabled,
		SettingKeyPurchaseSubscriptionURL,
		SettingKeyRegistrationEmailDomainQuotaEnabled,
		SettingKeyRegistrationEnabled,
		SettingKeyRiskControlEnabled,
		SettingKeySiteLogo,
		SettingKeySiteName,
		SettingKeySiteSubtitle,
		SettingKeyTableDefaultPageSize,
		SettingKeyTablePageSizeOptions,
		SettingKeyTencentCaptchaAppID,
		SettingKeyTencentCaptchaEnabled,
		SettingKeyTotpEnabled,
		SettingKeyTurnstileEnabled,
		SettingKeyTurnstileSiteKey,
		SettingKeyUserEmailChangeEnabled,
		SettingPaymentEnabled,
	}
}
