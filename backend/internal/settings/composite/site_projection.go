package composite

import "github.com/TokenFlux/TokenRouter/internal/site"

// site_projection.go transfers site display and content settings through the
// combined administrator snapshot.
// Site settings collect display text, legal documents, and navigation JSON
// for administrator reads and updates. Extraction passes those values to site
// preparation, then normalization assigns the selected result back into the
// combined Snapshot; table-choice slices, legal records, and localized editor
// maps keep their supplied nested references during these value transfers.

// SiteAdminSettings returns display, legal, and navigation values for site
// preparation. TablePageSizeOptions, LoginAgreementDocuments, and SiteTexts retain
// the Snapshot's collection references, including nested localization pointers,
// so callers keep them stable while preparing the batch.
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
		SiteShareImage:              s.SiteShareImage,
		SiteTexts:                   s.SiteTexts,
		DefaultLocale:               s.DefaultLocale,
		SiteName:                    s.SiteName,
		SiteSubtitle:                s.SiteSubtitle,
		TableDefaultPageSize:        s.TableDefaultPageSize,
		TablePageSizeOptions:        s.TablePageSizeOptions,
	}
}

// ApplySiteAdminSettings writes normalized site fields into the Snapshot. Its
// table choices, legal documents, and localized text map retain the supplied
// value's references, allowing subsequent persistence to read that prepared
// representation before the caller changes it.
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
	s.SiteShareImage = value.SiteShareImage
	s.SiteTexts = value.SiteTexts
	s.DefaultLocale = value.DefaultLocale
	s.SiteName = value.SiteName
	s.SiteSubtitle = value.SiteSubtitle
	s.TableDefaultPageSize = value.TableDefaultPageSize
	s.TablePageSizeOptions = value.TablePageSizeOptions
}

// ApplySiteAdminReadSettings transfers a nonnil site reader result for display
// and later partial merging. The table choices, legal records, and SiteTexts
// map remain shared with that result; these assignments introduce no additional
// copy of their nested localization values.
func (s *Snapshot) ApplySiteAdminReadSettings(value *site.AdminReadSettings) {
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
	s.SiteShareImage = value.SiteShareImage
	s.SiteTexts = value.SiteTexts
	s.DefaultLocale = value.DefaultLocale
	s.SiteName = value.SiteName
	s.SiteSubtitle = value.SiteSubtitle
	s.TableDefaultPageSize = value.TableDefaultPageSize
	s.TablePageSizeOptions = value.TablePageSizeOptions
}
