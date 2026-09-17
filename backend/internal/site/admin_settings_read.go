package site

import (
	"strings"

	settingvalues "github.com/TokenFlux/TokenRouter/internal/settings"
)

// AdminReadSettings 只包含本模块在综合管理页的展示投影。
type AdminReadSettings struct {
	APIBaseURL                  string
	ContactInfo                 string
	CustomEndpoints             string
	CustomMenuItems             string
	DocURL                      string
	FooterLinks                 string
	FooterText                  string
	FrontendURL                 string
	HideCcsImportButton         bool
	HomeContent                 string
	HomeFeaturedModels          string
	LoginAgreementDocuments     []LoginAgreementDocument
	LoginAgreementEnabled       bool
	LoginAgreementMode          string
	LoginAgreementUpdatedAt     string
	PurchaseSubscriptionEnabled bool
	PurchaseSubscriptionURL     string
	SiteLogo                    string
	SiteName                    string
	SiteNameEn                  string
	SiteNameZh                  string
	SiteSubtitle                string
	SiteSubtitleEn              string
	SiteSubtitleZh              string
	SiteTitleEn                 string
	SiteTitleZh                 string
	TableDefaultPageSize        int
	TablePageSizeOptions        []int
}

// ReadAdminSettings 解释同一批已读持久值，不新增查询或改变缺省语义。
func ReadAdminSettings(settings map[string]string) *AdminReadSettings {
	loginAgreementDocuments := ParseLoginAgreementDocuments(settings[SettingKeyLoginAgreementDocuments])
	loginAgreementUpdatedAt := strings.TrimSpace(settings[SettingKeyLoginAgreementUpdatedAt])
	if loginAgreementUpdatedAt == "" {
		loginAgreementUpdatedAt = defaultLoginAgreementDate
	}
	result := &AdminReadSettings{}
	result.FrontendURL = settings[SettingKeyFrontendURL]
	result.LoginAgreementEnabled = settings[SettingKeyLoginAgreementEnabled] == "true"
	result.LoginAgreementMode = NormalizeLoginAgreementMode(settings[SettingKeyLoginAgreementMode])
	result.LoginAgreementUpdatedAt = loginAgreementUpdatedAt
	result.LoginAgreementDocuments = loginAgreementDocuments
	result.SiteName = settingvalues.StringOrDefault(settings, SettingKeySiteName, "Sub2API")
	result.SiteLogo = settings[SettingKeySiteLogo]
	result.SiteSubtitle = settingvalues.StringOrDefault(settings, SettingKeySiteSubtitle, "Subscription to API Conversion Platform")
	result.SiteNameZh = settings[SettingKeySiteNameZh]
	result.SiteNameEn = settings[SettingKeySiteNameEn]
	result.SiteTitleZh = settings[SettingKeySiteTitleZh]
	result.SiteTitleEn = settings[SettingKeySiteTitleEn]
	result.SiteSubtitleZh = settings[SettingKeySiteSubtitleZh]
	result.SiteSubtitleEn = settings[SettingKeySiteSubtitleEn]
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
