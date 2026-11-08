package site

import (
	"strings"

	settingvalues "github.com/TokenFlux/TokenRouter/internal/settings"
)

// admin_settings_read.go interprets stored site settings for the administrator
// content editor.
// The settings editor needs source content and localization state together.
// Stored strings are interpreted with editor defaults for branding, consent,
// and pagination, while raw navigation JSON remains available for later
// preparation against the same stored content.

// AdminReadSettings contains source display values and localization state for administrator editing.
type AdminReadSettings struct {
	APIBaseURL          string // advertised API base
	ContactInfo         string // displayed contact details
	CustomEndpoints     string // configured endpoint JSON
	CustomMenuItems     string // configured menu JSON
	DocURL              string // documentation link URL
	FooterLinks         string // configured footer JSON
	FooterText          string // additional footer text
	FrontendURL         string // deployment frontend address
	HideCcsImportButton bool   // hide import action
	HomeContent         string // homepage HTML or link
	HomeFeaturedModels  string // featured model JSON
	// LoginAgreementDocuments carries normalized stored documents; missing,
	// malformed, or wholly blank lists are replaced by the built-in agreement list.
	LoginAgreementDocuments []LoginAgreementDocument // configured legal documents
	LoginAgreementEnabled   bool                     // consent prompt switch
	// LoginAgreementMode contains the supported presentation mode; the reader
	// replaces unsupported stored strings with NormalizeLoginAgreementMode's default consent presentation.
	LoginAgreementMode string // consent presentation mode
	// LoginAgreementUpdatedAt is the trimmed stored revision label; a blank
	// value selects the built-in date used by administrator and public readers.
	LoginAgreementUpdatedAt     string         // agreement revision label
	PurchaseSubscriptionEnabled bool           // subscription purchase switch
	PurchaseSubscriptionURL     string         // subscription purchase destination
	SiteLogo                    string         // branding image location
	SiteShareImage              string         `json:"site_share_image"` // Saved cover image data URL.
	SiteTexts                   LocalizedTexts `json:"site_texts"`       // localized source content
	// DefaultLocale contains the stored default language; an empty string selects
	// en here, while any nonempty stored spelling remains available to the editor.
	DefaultLocale string `json:"default_locale"` // default display language
	// SiteName is the legacy source branding name. An empty stored value uses
	// TokenRouter, while localized edits remain separately available through SiteTexts.
	SiteName string // legacy branding name
	// SiteSubtitle is the legacy source subtitle; an empty stored value uses
	// the built-in subtitle independently of the editable grouped localization content.
	SiteSubtitle string // legacy branding subtitle
	// TableDefaultPageSize and TablePageSizeOptions are the normalized pagination
	// values returned by ParseTablePreferences, so invalid stored values become
	// usable defaults while valid options retain sorted unique sizes.
	TableDefaultPageSize int   // default rows per page
	TablePageSizeOptions []int // selectable page row counts
}

// ReadAdminSettings returns administrator display settings parsed from one stored-value map.
// Missing branding, agreement metadata, default locale, and invalid pagination
// values select their reader defaults. Raw navigation and frontend URL strings
// remain source values, while the subscription purchase URL is trimmed.
func ReadAdminSettings(settings map[string]string) *AdminReadSettings {
	loginAgreementDocuments := ParseLoginAgreementDocuments(settings[SettingKeyLoginAgreementDocuments])
	loginAgreementUpdatedAt := strings.TrimSpace(settings[SettingKeyLoginAgreementUpdatedAt])
	if loginAgreementUpdatedAt == "" {
		loginAgreementUpdatedAt = defaultLoginAgreementDate
	}
	result := &AdminReadSettings{}
	result.SiteTexts = ParseLocalizedTexts(settings)
	result.DefaultLocale = settings[SettingKeyDefaultLocale]
	if result.DefaultLocale == "" {
		result.DefaultLocale = "en"
	}
	result.FrontendURL = settings[SettingKeyFrontendURL]
	result.LoginAgreementEnabled = settings[SettingKeyLoginAgreementEnabled] == "true"
	result.LoginAgreementMode = NormalizeLoginAgreementMode(settings[SettingKeyLoginAgreementMode])
	result.LoginAgreementUpdatedAt = loginAgreementUpdatedAt
	result.LoginAgreementDocuments = loginAgreementDocuments
	result.SiteName = settingvalues.StringOrDefault(settings, SettingKeySiteName, "TokenRouter")
	result.SiteLogo = settings[SettingKeySiteLogo]
	result.SiteShareImage = settings[SettingKeySiteShareImage]
	result.SiteSubtitle = settingvalues.StringOrDefault(settings, SettingKeySiteSubtitle, "Subscription to API Conversion Platform")
	result.APIBaseURL = settings[SettingKeyAPIBaseURL]
	result.ContactInfo = settings[SettingKeyContactInfo]
	result.DocURL = settings[SettingKeyDocURL]
	result.HomeContent = settings[SettingKeyHomeContent]
	result.HideCcsImportButton = settings[SettingKeyHideCcsImportButton] == "true"
	result.PurchaseSubscriptionEnabled = settings[SettingKeyPurchaseSubscriptionEnabled] == "true"
	result.PurchaseSubscriptionURL = strings.TrimSpace(settings[SettingKeyPurchaseSubscriptionURL])
	result.CustomMenuItems = settings[SettingKeyCustomMenuItems]
	result.CustomEndpoints = settings[SettingKeyCustomEndpoints]
	result.FooterLinks = settings[SettingKeyFooterLinks]
	result.FooterText = settings[SettingKeyFooterText]
	result.HomeFeaturedModels = settings[SettingKeyHomeFeaturedModels]
	result.TableDefaultPageSize, result.TablePageSizeOptions = ParseTablePreferences(
		settings[SettingKeyTableDefaultPageSize],
		settings[SettingKeyTablePageSizeOptions],
	)
	return result
}
