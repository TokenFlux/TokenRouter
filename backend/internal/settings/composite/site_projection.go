package composite

import "github.com/TokenFlux/TokenRouter/internal/site"

// SiteAdminSettings 仅转换所属模块值，不读取设置或发布状态。
func (s *Snapshot) SiteAdminSettings() site.AdminSettings {
	return site.AdminSettings{
		APIBaseURL:                  s.APIBaseURL,
		ContactInfo:                 s.ContactInfo,
		CustomEndpoints:             s.CustomEndpoints,
		CustomMenuItems:             s.CustomMenuItems,
		DocURL:                      s.DocURL,
		FooterLinks:                 s.FooterLinks,
		FooterText:                  s.FooterText,
		FrontendURL:                 s.FrontendURL,
		HideCcsImportButton:         s.HideCcsImportButton,
		HomeContent:                 s.HomeContent,
		HomeFeaturedModels:          s.HomeFeaturedModels,
		LoginAgreementDocuments:     s.LoginAgreementDocuments,
		LoginAgreementEnabled:       s.LoginAgreementEnabled,
		LoginAgreementMode:          s.LoginAgreementMode,
		LoginAgreementUpdatedAt:     s.LoginAgreementUpdatedAt,
		PurchaseSubscriptionEnabled: s.PurchaseSubscriptionEnabled,
		PurchaseSubscriptionURL:     s.PurchaseSubscriptionURL,
		SiteLogo:                    s.SiteLogo,
		SiteName:                    s.SiteName,
		SiteNameEn:                  s.SiteNameEn,
		SiteNameZh:                  s.SiteNameZh,
		SiteSubtitle:                s.SiteSubtitle,
		SiteSubtitleEn:              s.SiteSubtitleEn,
		SiteSubtitleZh:              s.SiteSubtitleZh,
		SiteTitleEn:                 s.SiteTitleEn,
		SiteTitleZh:                 s.SiteTitleZh,
		TableDefaultPageSize:        s.TableDefaultPageSize,
		TablePageSizeOptions:        s.TablePageSizeOptions,
	}
}

// ApplySiteAdminSettings 仅转换所属模块值，不读取设置或发布状态。
func (s *Snapshot) ApplySiteAdminSettings(value site.AdminSettings) {
	s.APIBaseURL = value.APIBaseURL
	s.ContactInfo = value.ContactInfo
	s.CustomEndpoints = value.CustomEndpoints
	s.CustomMenuItems = value.CustomMenuItems
	s.DocURL = value.DocURL
	s.FooterLinks = value.FooterLinks
	s.FooterText = value.FooterText
	s.FrontendURL = value.FrontendURL
	s.HideCcsImportButton = value.HideCcsImportButton
	s.HomeContent = value.HomeContent
	s.HomeFeaturedModels = value.HomeFeaturedModels
	s.LoginAgreementDocuments = value.LoginAgreementDocuments
	s.LoginAgreementEnabled = value.LoginAgreementEnabled
	s.LoginAgreementMode = value.LoginAgreementMode
	s.LoginAgreementUpdatedAt = value.LoginAgreementUpdatedAt
	s.PurchaseSubscriptionEnabled = value.PurchaseSubscriptionEnabled
	s.PurchaseSubscriptionURL = value.PurchaseSubscriptionURL
	s.SiteLogo = value.SiteLogo
	s.SiteName = value.SiteName
	s.SiteNameEn = value.SiteNameEn
	s.SiteNameZh = value.SiteNameZh
	s.SiteSubtitle = value.SiteSubtitle
	s.SiteSubtitleEn = value.SiteSubtitleEn
	s.SiteSubtitleZh = value.SiteSubtitleZh
	s.SiteTitleEn = value.SiteTitleEn
	s.SiteTitleZh = value.SiteTitleZh
	s.TableDefaultPageSize = value.TableDefaultPageSize
	s.TablePageSizeOptions = value.TablePageSizeOptions
}
