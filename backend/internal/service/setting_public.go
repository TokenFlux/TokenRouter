// 站点公开实现已迁移；此处只保留来源投影及历史入口。
package service

import (
	"context"
	"fmt"
	slog "log/slog"
	"strings"

	"github.com/TokenFlux/TokenRouter/internal/site"
)

// GetPublicSettings 获取公开设置（无需登录）
func (s *SettingService) LoadSitePublicInputs(ctx context.Context) (site.PublicInputs, error) {
	keys := []string{
		SettingKeyRegistrationEnabled,
		SettingKeyEmailVerifyEnabled,
		SettingKeyForceEmailOnThirdPartySignup,
		SettingKeyRegistrationEmailSuffixWhitelist,
		SettingKeyRegistrationEmailDomainQuotaEnabled,
		SettingKeyUserEmailChangeEnabled,
		SettingKeyPromoCodeEnabled,
		SettingKeyPasswordResetEnabled,
		SettingKeyInvitationCodeEnabled,
		SettingKeyAffiliateEnabled,
		SettingKeyTotpEnabled,
		SettingKeyLoginAgreementEnabled,
		SettingKeyLoginAgreementMode,
		SettingKeyLoginAgreementUpdatedAt,
		SettingKeyLoginAgreementDocuments,
		SettingKeyTurnstileEnabled,
		SettingKeyTurnstileSiteKey,
		SettingKeyTencentCaptchaEnabled,
		SettingKeyTencentCaptchaAppID,
		SettingKeyTencentCaptchaRegion,
		SettingKeyAliyunCaptchaEnabled,
		SettingKeyAliyunCaptchaSceneID,
		SettingKeyAliyunCaptchaPrefix,
		SettingKeyAliyunCaptchaRegion,
		SettingKeyAPIKeyACLTrustForwardedIP,
		SettingKeySiteName,
		SettingKeySiteLogo,
		SettingKeySiteSubtitle,
		SettingKeySiteNameZh,
		SettingKeySiteNameEn,
		SettingKeySiteTitleZh,
		SettingKeySiteTitleEn,
		SettingKeySiteSubtitleZh,
		SettingKeySiteSubtitleEn,
		SettingKeyAPIBaseURL,
		SettingKeyContactInfo,
		SettingKeyDocURL,
		SettingKeyHomeContent,
		SettingKeyHideCcsImportButton,
		SettingKeyPurchaseSubscriptionEnabled,
		SettingKeyPurchaseSubscriptionURL,
		SettingKeyTableDefaultPageSize,
		SettingKeyTablePageSizeOptions,
		SettingKeyUsageRankingLimit,
		SettingKeyUsageRankingEnabled,
		SettingKeyUsageRankingSortBy,
		SettingKeyUsageRankingShowTotalTokens,
		SettingKeyUsageRankingShowRequests,
		SettingKeyUsageRankingShowActualCost,
		SettingKeyCustomMenuItems,
		SettingKeyCustomEndpoints,
		SettingKeyFooterLinks,
		SettingKeyFooterText,
		SettingKeyHomeFeaturedModels,
		SettingKeyLinuxDoConnectEnabled,
		SettingKeyDingTalkConnectEnabled,
		SettingKeyWeChatConnectEnabled,
		SettingKeyWeChatConnectAppID,
		SettingKeyWeChatConnectAppSecret,
		SettingKeyWeChatConnectOpenAppID,
		SettingKeyWeChatConnectOpenAppSecret,
		SettingKeyWeChatConnectMPAppID,
		SettingKeyWeChatConnectMPAppSecret,
		SettingKeyWeChatConnectMobileAppID,
		SettingKeyWeChatConnectMobileAppSecret,
		SettingKeyWeChatConnectOpenEnabled,
		SettingKeyWeChatConnectMPEnabled,
		SettingKeyWeChatConnectMobileEnabled,
		SettingKeyWeChatConnectMode,
		SettingKeyWeChatConnectScopes,
		SettingKeyWeChatConnectRedirectURL,
		SettingKeyWeChatConnectFrontendRedirectURL,
		SettingKeyBackendModeEnabled,
		SettingPaymentEnabled,
		SettingKeyOIDCConnectEnabled,
		SettingKeyOIDCConnectProviderName,
		SettingKeyGitHubOAuthEnabled,
		SettingKeyGitHubOAuthClientID,
		SettingKeyGitHubOAuthClientSecret,
		SettingKeyGitHubOAuthRedirectURL,
		SettingKeyGitHubOAuthFrontendRedirectURL,
		SettingKeyGoogleOAuthEnabled,
		SettingKeyGoogleOneTapEnabled,
		SettingKeyGoogleOAuthClientID,
		SettingKeyGoogleOAuthClientSecret,
		SettingKeyGoogleOAuthRedirectURL,
		SettingKeyGoogleOAuthFrontendRedirectURL,
		SettingKeyBalanceUnitName,
		SettingKeyBalanceUnitSymbol,
		SettingKeyBalanceIconSVG,
		SettingKeyBalanceLowNotifyEnabled,
		SettingKeyBalanceLowNotifyThreshold,
		SettingKeyBalanceLowNotifyRechargeURL,
		SettingKeyAccountQuotaNotifyEnabled,
		SettingKeyTeamEnabled,
		SettingKeyCreativeEnabled,
		SettingKeyRiskControlEnabled,
		SettingKeyAllowUserViewErrorRequests,
	}

	settings, err := s.settingRepo.GetMultiple(ctx, keys)
	if err != nil {
		return site.PublicInputs{}, fmt.Errorf("get public settings: %w", err)
	}

	linuxDoEnabled := false
	if raw, ok := settings[SettingKeyLinuxDoConnectEnabled]; ok {
		linuxDoEnabled = raw == "true"
	} else {
		linuxDoEnabled = s.cfg != nil && s.cfg.LinuxDo.Enabled
	}
	dingTalkEnabled := false
	if raw, ok := settings[SettingKeyDingTalkConnectEnabled]; ok {
		dingTalkEnabled = raw == "true"
	} else {
		dingTalkEnabled = s.cfg != nil && s.cfg.DingTalk.Enabled
	}
	oidcEnabled := false
	if raw, ok := settings[SettingKeyOIDCConnectEnabled]; ok {
		oidcEnabled = raw == "true"
	} else {
		oidcEnabled = s.cfg != nil && s.cfg.OIDC.Enabled
	}
	oidcProviderName := strings.TrimSpace(settings[SettingKeyOIDCConnectProviderName])
	if oidcProviderName == "" && s.cfg != nil {
		oidcProviderName = strings.TrimSpace(s.cfg.OIDC.ProviderName)
	}
	if oidcProviderName == "" {
		oidcProviderName = "OIDC"
	}
	weChatEnabled, weChatOpenEnabled, weChatMPEnabled, weChatMobileEnabled := s.weChatOAuthCapabilitiesFromSettings(settings)
	gitHubOAuthEnabled := s.emailOAuthPublicEnabled(settings, "github")
	googleOAuthEnabled := s.emailOAuthPublicEnabled(settings, "google")
	googleOAuthConfig := s.effectiveEmailOAuthConfig(settings, "google")
	googleOneTapEnabled := settings[SettingKeyGoogleOneTapEnabled] == "true" && googleOAuthEnabled
	googleOAuthClientID := ""
	if googleOneTapEnabled {
		googleOAuthClientID = strings.TrimSpace(googleOAuthConfig.ClientID)
	}

	ranking := parseUsageRankingSettings(settings)
	values := make(map[string]string)
	for _, key := range site.PublicValueKeys() {
		values[key] = settings[key]
	}
	return site.PublicInputs{Values: values, Auth: site.PublicAuth{
		LinuxDo: linuxDoEnabled, DingTalk: dingTalkEnabled, OIDC: oidcEnabled, OIDCName: oidcProviderName,
		WeChat: weChatEnabled, WeChatOpen: weChatOpenEnabled, WeChatMP: weChatMPEnabled, WeChatMobile: weChatMobileEnabled,
		GitHub: gitHubOAuthEnabled, Google: googleOAuthEnabled, GoogleOneTap: googleOneTapEnabled, GoogleClientID: googleOAuthClientID,
		Team: settings[SettingKeyTeamEnabled] != "false" && (s.cfg == nil || s.cfg.Team.Enabled), TeamSelfService: s.cfg == nil || s.cfg.Team.SelfServiceEnabled,
		Passkey:              s.cfg != nil && s.cfg.WebAuthn.Enabled,
		RegistrationSuffixes: ParseRegistrationEmailSuffixWhitelist(settings[SettingKeyRegistrationEmailSuffixWhitelist]),
		TencentRegion:        normalizeTencentCaptchaRegion(settings[SettingKeyTencentCaptchaRegion]), AliyunRegion: normalizeAliyunCaptchaRegion(settings[SettingKeyAliyunCaptchaRegion]),
	}, Usage: site.PublicUsage{Limit: ranking.Limit, Enabled: ranking.Enabled, SortBy: string(ranking.SortBy), ShowTotalTokens: ranking.ShowTotalTokens, ShowRequests: ranking.ShowRequests, ShowActualCost: ranking.ShowActualCost}}, nil
}

// GetFrontendURL 获取前端基础URL（数据库优先，fallback 到配置文件）
func (s *SettingService) GetFrontendURL(ctx context.Context) string {
	val, err := s.settingRepo.GetValue(ctx, SettingKeyFrontendURL)
	if err == nil && strings.TrimSpace(val) != "" {
		return strings.TrimSpace(val)
	}
	return s.cfg.Server.FrontendURL
}

// IsUserErrorViewAllowed 读取用户侧失败请求展示开关。
// 读取失败时默认关闭，避免公开未确认的错误日志数据。
func (s *SettingService) IsUserErrorViewAllowed(ctx context.Context) bool {
	vals, err := s.settingRepo.GetMultiple(ctx, []string{SettingKeyAllowUserViewErrorRequests})
	if err != nil {
		slog.Warn("failed to get allow_user_view_error_requests setting, defaulting to false", "error", err)
		return false
	}
	return vals[SettingKeyAllowUserViewErrorRequests] == "true"
}
func (s *SettingService) PublicVersion() string { return s.settingRuntime().Version() }
func (s *SettingService) sitePublic() *site.PublicService {
	if s.publicSite != nil {
		return s.publicSite
	}
	return site.NewPublicService(s)
}
func (s *SettingService) SetSitePublic(p *site.PublicService) { s.publicSite = p }
func (s *SettingService) GetPublicSettings(ctx context.Context) (*PublicSettings, error) {
	return s.sitePublic().GetPublicSettings(ctx)
}
func normalizeLoginAgreementMode(raw string) string { return site.NormalizeLoginAgreementMode(raw) }
func defaultLoginAgreementDocuments() []LoginAgreementDocument {
	return site.DefaultLoginAgreementDocuments()
}

func parseLoginAgreementDocuments(raw string) []LoginAgreementDocument {
	return site.ParseLoginAgreementDocuments(raw)
}
func marshalLoginAgreementDocuments(docs []LoginAgreementDocument) (string, error) {
	return site.MarshalLoginAgreementDocuments(docs)
}

func (s *SettingService) GetPublicSettingsForInjection(ctx context.Context) (any, error) {
	return s.sitePublic().GetPublicSettingsForInjection(ctx)
}

func (s *SettingService) GetFrameSrcOrigins(ctx context.Context) ([]string, error) {
	return s.sitePublic().GetFrameSrcOrigins(ctx)
}
