package site

import (
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
)

// AdminSettings 只包含站点呈现与登录协议，不携带认证或资金规则。
type AdminSettings struct {
	APIBaseURL                  string                   `json:"api_base_url"`
	ContactInfo                 string                   `json:"contact_info"`
	CustomEndpoints             string                   `json:"custom_endpoints"`
	CustomMenuItems             string                   `json:"custom_menu_items"`
	DocURL                      string                   `json:"doc_url"`
	FooterLinks                 string                   `json:"footer_links"`
	FooterText                  string                   `json:"footer_text"`
	FrontendURL                 string                   `json:"frontend_url"`
	HideCcsImportButton         bool                     `json:"hide_ccs_import_button"`
	HomeContent                 string                   `json:"home_content"`
	HomeFeaturedModels          string                   `json:"home_featured_models"`
	LoginAgreementDocuments     []LoginAgreementDocument `json:"login_agreement_documents"`
	LoginAgreementEnabled       bool                     `json:"login_agreement_enabled"`
	LoginAgreementMode          string                   `json:"login_agreement_mode"`
	LoginAgreementUpdatedAt     string                   `json:"login_agreement_updated_at"`
	PurchaseSubscriptionEnabled bool                     `json:"purchase_subscription_enabled"`
	PurchaseSubscriptionURL     string                   `json:"purchase_subscription_url"`
	SiteLogo                    string                   `json:"site_logo"`
	SiteName                    string                   `json:"site_name"`
	SiteNameEn                  string                   `json:"site_name_en"`
	SiteNameZh                  string                   `json:"site_name_zh"`
	SiteSubtitle                string                   `json:"site_subtitle"`
	SiteSubtitleEn              string                   `json:"site_subtitle_en"`
	SiteSubtitleZh              string                   `json:"site_subtitle_zh"`
	SiteTitleEn                 string                   `json:"site_title_en"`
	SiteTitleZh                 string                   `json:"site_title_zh"`
	TableDefaultPageSize        int                      `json:"table_default_page_size"`
	TablePageSizeOptions        []int                    `json:"table_page_size_options"`
}

// 站点管理与公开投影沿用同一套持久键。
const (
	SettingKeyFrontendURL = "frontend_url"
)

// PrepareAdminSettings 只准备持久值，保留登录协议与表格配置的原规范化语义。
func PrepareAdminSettings(settings *AdminSettings) (map[string]string, error) {
	updates := map[string]string{}
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
	updates[SettingKeySiteNameZh] = settings.SiteNameZh
	updates[SettingKeySiteNameEn] = settings.SiteNameEn
	updates[SettingKeySiteTitleZh] = settings.SiteTitleZh
	updates[SettingKeySiteTitleEn] = settings.SiteTitleEn
	updates[SettingKeySiteSubtitleZh] = settings.SiteSubtitleZh
	updates[SettingKeySiteSubtitleEn] = settings.SiteSubtitleEn
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
