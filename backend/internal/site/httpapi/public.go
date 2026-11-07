package httpapi

import (
	"github.com/gin-gonic/gin"

	response "github.com/TokenFlux/TokenRouter/internal/server/httpx"
	"github.com/TokenFlux/TokenRouter/internal/site"
	"github.com/TokenFlux/TokenRouter/internal/site/httpapi/dto"
)

// public.go returns public browser settings and language-selected legal
// documents.
// Browser initialization needs typed display settings and navigation arrays,
// while individual legal reads return the requested language's agreement copy.
// The public settings response adds the handler's build label and current
// server timezone information after the domain service resolves its values.

// PublicHandler writes browser settings and localized legal documents through the site service.
type PublicHandler struct {
	settingService *site.PublicService // localized public settings service
	// version is the build label supplied to this HTTP handler; HTML injection
	// obtains its version independently from the service's PublicSource callback.
	version string
}

// NewPublicHandler binds the public site service and build label for API responses.
func NewPublicHandler(s *site.PublicService, version string) *PublicHandler {
	return &PublicHandler{settingService: s, version: version}
}

// publicLoginAgreementDocumentsToDTO returns agreement identities and their selected display copy.
// Each output value contains ID, Title, and ContentMD; localization editor state
// and resolution pointers stay unset in this settings-list response shape.
func publicLoginAgreementDocumentsToDTO(items []site.LoginAgreementDocument) []dto.LoginAgreementDocument {
	result := make([]dto.LoginAgreementDocument, 0, len(items))
	for _, item := range items {
		result = append(result, dto.LoginAgreementDocument{
			ID:        item.ID,
			Title:     item.Title,
			ContentMD: item.ContentMD,
		})
	}
	return result
}

// GetPublicSettings returns public site settings.
// The service resolves language and capability values, while this handler parses
// navigation JSON into arrays, removes admin-only menu entries, and adds its
// supplied build label plus the service's current server timezone and offset.
// GET /api/v1/settings/public
func (h *PublicHandler) GetPublicSettings(c *gin.Context) {
	settings, err := h.settingService.GetPublicSettings(c.Request.Context())
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}

	zone, offset := h.settingService.ServerTimezone()
	response.Success(c, dto.PublicSettings{
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
		LoginAgreementDocuments:             publicLoginAgreementDocumentsToDTO(settings.LoginAgreementDocuments),
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
		CustomMenuItems:                     dto.ParseUserVisibleMenuItems(settings.CustomMenuItems),
		CustomEndpoints:                     dto.ParseCustomEndpoints(settings.CustomEndpoints),
		FooterLinks:                         dto.ParseFooterLinks(settings.FooterLinks),
		FooterText:                          settings.FooterText,
		HomeFeaturedModels:                  dto.ParseHomeFeaturedModels(settings.HomeFeaturedModels),
		DingTalkOAuthEnabled:                settings.DingTalkOAuthEnabled,
		LinuxDoOAuthEnabled:                 settings.LinuxDoOAuthEnabled,
		WeChatOAuthEnabled:                  settings.WeChatOAuthEnabled,
		WeChatOAuthOpenEnabled:              settings.WeChatOAuthOpenEnabled,
		WeChatOAuthMPEnabled:                settings.WeChatOAuthMPEnabled,
		WeChatOAuthMobileEnabled:            settings.WeChatOAuthMobileEnabled,
		OIDCOAuthEnabled:                    settings.OIDCOAuthEnabled,
		OIDCOAuthProviderName:               settings.OIDCOAuthProviderName,
		GitHubOAuthEnabled:                  settings.GitHubOAuthEnabled,
		GoogleOAuthEnabled:                  settings.GoogleOAuthEnabled,
		GoogleOneTapEnabled:                 settings.GoogleOneTapEnabled,
		GoogleOAuthClientID:                 settings.GoogleOAuthClientID,
		BackendModeEnabled:                  settings.BackendModeEnabled,
		PaymentEnabled:                      settings.PaymentEnabled,
		TeamEnabled:                         settings.TeamEnabled,
		TeamSelfServiceEnabled:              settings.TeamSelfServiceEnabled,
		CreativeEnabled:                     settings.CreativeEnabled,
		Version:                             h.version,
		ServerTimezone:                      zone,
		ServerUTCOffset:                     offset,
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
	})
}

// GetLegalDocument returns an agreement selected for the request context's language.
// Any service error becomes a document-not-found response. Successful reads
// add Vary: Accept-Language and keep the selected document's resolution metadata.
func (h *PublicHandler) GetLegalDocument(c *gin.Context) {
	doc, err := h.settingService.GetLegalDocument(c.Request.Context(), c.Param("id"))
	if err != nil {
		// This endpoint reports unavailable legal copy through one not-found
		// response, including storage-read failures, so callers receive the endpoint's
		// fixed document-unavailable message for every unsuccessful service lookup.
		response.NotFound(c, "Document not found.")
		return
	}
	c.Header("Vary", "Accept-Language")
	response.Success(c, doc)
}
