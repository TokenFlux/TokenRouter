package site

import (
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
)

// admin_settings.go normalizes site administrator settings into values for a
// shared persistence batch.
// Administrator site settings combine legacy display values with localized
// content and login agreements. Preparation returns string values for one
// settings write, normalizing agreement metadata and pagination choices before
// handing the resulting map to its caller.

// Frontend link storage uses this key so public-facing links and administrator
// editing resolve the same deployment URL through their respective readers.
const (
	SettingKeyFrontendURL = "frontend_url"
)

// AdminSettings contains site display and login agreement settings.
type AdminSettings struct {
	APIBaseURL          string `json:"api_base_url"`           // advertised API base
	ContactInfo         string `json:"contact_info"`           // displayed contact details
	CustomEndpoints     string `json:"custom_endpoints"`       // configured endpoint JSON
	CustomMenuItems     string `json:"custom_menu_items"`      // configured menu JSON
	DocURL              string `json:"doc_url"`                // documentation link URL
	FooterLinks         string `json:"footer_links"`           // configured footer JSON
	FooterText          string `json:"footer_text"`            // additional footer text
	FrontendURL         string `json:"frontend_url"`           // deployment frontend address
	HideCcsImportButton bool   `json:"hide_ccs_import_button"` // hide import action
	HomeContent         string `json:"home_content"`           // homepage HTML or link
	HomeFeaturedModels  string `json:"home_featured_models"`   // featured model JSON
	// LoginAgreementDocuments supplies the agreement list; marshaling normalizes
	// identifiers and blank entries, and an empty normalized list selects built-in documents.
	LoginAgreementDocuments []LoginAgreementDocument `json:"login_agreement_documents"` // configured legal documents
	LoginAgreementEnabled   bool                     `json:"login_agreement_enabled"`   // consent prompt switch
	// LoginAgreementMode selects consent presentation; preparation trims and
	// lowercases the value, selecting the default mode for an unsupported setting.
	LoginAgreementMode string `json:"login_agreement_mode"` // consent presentation mode
	// LoginAgreementUpdatedAt labels the agreement revision; preparation trims
	// it and substitutes the built-in date when the submitted string is blank.
	LoginAgreementUpdatedAt     string `json:"login_agreement_updated_at"`    // agreement revision label
	PurchaseSubscriptionEnabled bool   `json:"purchase_subscription_enabled"` // subscription purchase switch
	PurchaseSubscriptionURL     string `json:"purchase_subscription_url"`     // subscription purchase destination
	SiteLogo                    string `json:"site_logo"`                     // branding image location
	// SiteShareImage is validated by SettingsParticipant when supplied. Its
	// derived descriptor is stored in that same batch, and empty input removes it.
	SiteShareImage string `json:"site_share_image"` // Saved cover image data URL.
	// SiteTexts carries per-field localization updates; nil omits the grouped
	// setting from the returned write map and leaves preparation to its caller.
	SiteTexts LocalizedTexts `json:"site_texts"` // localized source content
	// DefaultLocale is written when nonempty. SettingsParticipant validates the
	// submitted language before this value reaches the string-map preparation step.
	DefaultLocale string `json:"default_locale"` // default display language
	SiteName      string `json:"site_name"`      // legacy branding name
	SiteSubtitle  string `json:"site_subtitle"`  // legacy branding subtitle
	// TableDefaultPageSize and TablePageSizeOptions are normalized together for
	// storage. Out-of-range defaults and unusable option collections select their
	// built-in values; valid options are deduplicated and sorted.
	TableDefaultPageSize int   `json:"table_default_page_size"` // default rows per page
	TablePageSizeOptions []int `json:"table_page_size_options"` // selectable page row counts
}

// PrepareAdminSettings returns normalized string values for the supplied site settings.
// settings is required and its agreement mode and date are updated in place.
// Non-nil SiteTexts and nonempty DefaultLocale add their keys; other fields
// produce values even when empty, so partial-update callers filter the result
// to the submitted fields before persistence.
func PrepareAdminSettings(settings *AdminSettings) (map[string]string, error) {
	updates := map[string]string{}
	if settings.SiteTexts != nil {
		raw, err := json.Marshal(settings.SiteTexts)
		if err != nil {
			return nil, err
		}
		updates[SettingKeySiteTexts] = string(raw)
	}
	if settings.DefaultLocale != "" {
		updates[SettingKeyDefaultLocale] = settings.DefaultLocale
	}
	updates[SettingKeyFrontendURL] = settings.FrontendURL
	settings.LoginAgreementMode = NormalizeLoginAgreementMode(settings.LoginAgreementMode)
	settings.LoginAgreementUpdatedAt = strings.TrimSpace(settings.LoginAgreementUpdatedAt)
	if settings.LoginAgreementUpdatedAt == "" {
		settings.LoginAgreementUpdatedAt = defaultLoginAgreementDate
	}
	loginAgreementDocumentsJSON, err := MarshalLoginAgreementDocuments(settings.LoginAgreementDocuments)
	if err != nil {
		return nil, err
	}
	updates[SettingKeyLoginAgreementEnabled] = strconv.FormatBool(settings.LoginAgreementEnabled)
	updates[SettingKeyLoginAgreementMode] = settings.LoginAgreementMode
	updates[SettingKeyLoginAgreementUpdatedAt] = settings.LoginAgreementUpdatedAt
	updates[SettingKeyLoginAgreementDocuments] = loginAgreementDocumentsJSON
	updates[SettingKeySiteName] = settings.SiteName
	updates[SettingKeySiteLogo] = settings.SiteLogo
	updates[SettingKeySiteSubtitle] = settings.SiteSubtitle
	updates[SettingKeyAPIBaseURL] = settings.APIBaseURL
	updates[SettingKeyContactInfo] = settings.ContactInfo
	updates[SettingKeyDocURL] = settings.DocURL
	updates[SettingKeyHomeContent] = settings.HomeContent
	updates[SettingKeyHideCcsImportButton] = strconv.FormatBool(settings.HideCcsImportButton)
	updates[SettingKeyPurchaseSubscriptionEnabled] = strconv.FormatBool(settings.PurchaseSubscriptionEnabled)
	updates[SettingKeyPurchaseSubscriptionURL] = strings.TrimSpace(settings.PurchaseSubscriptionURL)
	tableDefaultPageSize, tablePageSizeOptions := NormalizeTablePreferences(
		settings.TableDefaultPageSize,
		settings.TablePageSizeOptions,
	)
	updates[SettingKeyTableDefaultPageSize] = strconv.Itoa(tableDefaultPageSize)
	tablePageSizeOptionsJSON, err := json.Marshal(tablePageSizeOptions)
	if err != nil {
		return nil, fmt.Errorf("marshal table page size options: %w", err)
	}
	updates[SettingKeyTablePageSizeOptions] = string(tablePageSizeOptionsJSON)
	updates[SettingKeyCustomMenuItems] = settings.CustomMenuItems
	updates[SettingKeyCustomEndpoints] = settings.CustomEndpoints
	updates[SettingKeyFooterLinks] = settings.FooterLinks
	updates[SettingKeyFooterText] = strings.TrimSpace(settings.FooterText)
	updates[SettingKeyHomeFeaturedModels] = settings.HomeFeaturedModels
	return updates, nil
}
