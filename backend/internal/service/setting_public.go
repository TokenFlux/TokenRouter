package service

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/url"
	"strconv"
	"strings"

	"github.com/TokenFlux/TokenRouter/internal/pkg/timezone"
)

func normalizeLoginAgreementMode(raw string) string {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "checkbox":
		return "checkbox"
	default:
		return defaultLoginAgreementMode
	}
}

func defaultLoginAgreementDocuments() []LoginAgreementDocument {
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

func normalizeLoginAgreementDocumentID(raw string) string {
	raw = strings.ToLower(strings.TrimSpace(raw))
	var b strings.Builder
	lastSeparator := false
	for _, r := range raw {
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

func normalizeLoginAgreementDocuments(docs []LoginAgreementDocument) []LoginAgreementDocument {
	normalized := make([]LoginAgreementDocument, 0, len(docs))
	seen := make(map[string]int, len(docs))
	for i, doc := range docs {
		title := strings.TrimSpace(doc.Title)
		content := strings.TrimSpace(doc.ContentMD)
		if title == "" && content == "" {
			continue
		}
		id := normalizeLoginAgreementDocumentID(doc.ID)
		if id == "" {
			sum := sha256.Sum256([]byte(fmt.Sprintf("%d:%s:%s", i, title, content)))
			id = hex.EncodeToString(sum[:])[:12]
		}
		baseID := id
		for suffix := 2; seen[id] > 0; suffix++ {
			id = fmt.Sprintf("%s-%d", baseID, suffix)
		}
		seen[id]++
		normalized = append(normalized, LoginAgreementDocument{
			ID:        id,
			Title:     title,
			ContentMD: content,
		})
	}
	return normalized
}

func parseLoginAgreementDocuments(raw string) []LoginAgreementDocument {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return defaultLoginAgreementDocuments()
	}
	var docs []LoginAgreementDocument
	if err := json.Unmarshal([]byte(raw), &docs); err != nil {
		return defaultLoginAgreementDocuments()
	}
	docs = normalizeLoginAgreementDocuments(docs)
	if len(docs) == 0 {
		return defaultLoginAgreementDocuments()
	}
	return docs
}

func marshalLoginAgreementDocuments(docs []LoginAgreementDocument) (string, error) {
	normalized := normalizeLoginAgreementDocuments(docs)
	if len(normalized) == 0 {
		normalized = defaultLoginAgreementDocuments()
	}
	b, err := json.Marshal(normalized)
	if err != nil {
		return "", fmt.Errorf("marshal login agreement documents: %w", err)
	}
	return string(b), nil
}

func buildLoginAgreementRevision(updatedAt string, docs []LoginAgreementDocument) string {
	normalized := normalizeLoginAgreementDocuments(docs)
	payload, err := json.Marshal(struct {
		UpdatedAt string                   `json:"updated_at"`
		Documents []LoginAgreementDocument `json:"documents"`
	}{
		UpdatedAt: strings.TrimSpace(updatedAt),
		Documents: normalized,
	})
	if err != nil {
		payload = []byte(strings.TrimSpace(updatedAt))
	}
	sum := sha256.Sum256(payload)
	return hex.EncodeToString(sum[:])[:16]
}

// GetFrontendURL 获取前端基础URL（数据库优先，fallback 到配置文件）
func (s *SettingService) GetFrontendURL(ctx context.Context) string {
	val, err := s.settingRepo.GetValue(ctx, SettingKeyFrontendURL)
	if err == nil && strings.TrimSpace(val) != "" {
		return strings.TrimSpace(val)
	}
	return s.cfg.Server.FrontendURL
}

// GetPublicSettings 获取公开设置（无需登录）
func (s *SettingService) GetPublicSettings(ctx context.Context) (*PublicSettings, error) {
	keys := []string{
		SettingKeyRegistrationEnabled,
		SettingKeyEmailVerifyEnabled,
		SettingKeyForceEmailOnThirdPartySignup,
		SettingKeyRegistrationEmailSuffixWhitelist,
		SettingKeyRegistrationEmailDomainQuotaEnabled,
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
		SettingKeyCustomMenuItems,
		SettingKeyCustomEndpoints,
		SettingKeyFooterLinks,
		SettingKeyFooterText,
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
		SettingKeyDataSharingEnabled,
		SettingKeyRiskControlEnabled,
		SettingKeyAllowUserViewErrorRequests,
	}

	settings, err := s.settingRepo.GetMultiple(ctx, keys)
	if err != nil {
		return nil, fmt.Errorf("get public settings: %w", err)
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

	// Password reset requires email verification to be enabled
	emailVerifyEnabled := settings[SettingKeyEmailVerifyEnabled] == "true"
	passwordResetEnabled := emailVerifyEnabled && settings[SettingKeyPasswordResetEnabled] == "true"
	registrationEmailSuffixWhitelist := ParseRegistrationEmailSuffixWhitelist(
		settings[SettingKeyRegistrationEmailSuffixWhitelist],
	)
	tableDefaultPageSize, tablePageSizeOptions := parseTablePreferences(
		settings[SettingKeyTableDefaultPageSize],
		settings[SettingKeyTablePageSizeOptions],
	)
	usageRankingLimit := normalizeUsageRankingLimitString(settings[SettingKeyUsageRankingLimit])
	loginAgreementDocuments := parseLoginAgreementDocuments(settings[SettingKeyLoginAgreementDocuments])
	loginAgreementUpdatedAt := strings.TrimSpace(settings[SettingKeyLoginAgreementUpdatedAt])
	if loginAgreementUpdatedAt == "" {
		loginAgreementUpdatedAt = defaultLoginAgreementDate
	}

	var balanceLowNotifyThreshold float64
	if v, err := strconv.ParseFloat(settings[SettingKeyBalanceLowNotifyThreshold], 64); err == nil && v >= 0 {
		balanceLowNotifyThreshold = v
	}
	balanceUnitName := strings.TrimSpace(settings[SettingKeyBalanceUnitName])
	if balanceUnitName == "" {
		balanceUnitName = "USD"
	}
	balanceUnitSymbol := strings.TrimSpace(settings[SettingKeyBalanceUnitSymbol])
	if balanceUnitSymbol == "" {
		balanceUnitSymbol = "$"
	}

	// 团队数据库开关与部署级开关需同时开启，避免在线设置绕过部署限制。
	return &PublicSettings{
		RegistrationEnabled:                 settings[SettingKeyRegistrationEnabled] == "true",
		EmailVerifyEnabled:                  emailVerifyEnabled,
		ForceEmailOnThirdPartySignup:        settings[SettingKeyForceEmailOnThirdPartySignup] == "true",
		RegistrationEmailSuffixWhitelist:    registrationEmailSuffixWhitelist,
		RegistrationEmailDomainQuotaEnabled: settings[SettingKeyRegistrationEmailDomainQuotaEnabled] == "true",
		PromoCodeEnabled:                    settings[SettingKeyPromoCodeEnabled] != "false", // 默认启用
		PasswordResetEnabled:                passwordResetEnabled,
		InvitationCodeEnabled:               settings[SettingKeyInvitationCodeEnabled] == "true",
		TeamEnabled:                         settings[SettingKeyTeamEnabled] != "false" && (s.cfg == nil || s.cfg.Team.Enabled),
		TeamSelfServiceEnabled:              s.cfg == nil || s.cfg.Team.SelfServiceEnabled,
		DataSharingEnabled:                  settings[SettingKeyDataSharingEnabled] != "false",
		AffiliateEnabled:                    settings[SettingKeyAffiliateEnabled] == "true",
		TotpEnabled:                         settings[SettingKeyTotpEnabled] == "true",
		PasskeyEnabled:                      s.cfg != nil && s.cfg.WebAuthn.Enabled,
		LoginAgreementEnabled:               settings[SettingKeyLoginAgreementEnabled] == "true" && len(loginAgreementDocuments) > 0,
		LoginAgreementMode:                  normalizeLoginAgreementMode(settings[SettingKeyLoginAgreementMode]),
		LoginAgreementUpdatedAt:             loginAgreementUpdatedAt,
		LoginAgreementRevision:              buildLoginAgreementRevision(loginAgreementUpdatedAt, loginAgreementDocuments),
		LoginAgreementDocuments:             loginAgreementDocuments,
		TurnstileEnabled:                    settings[SettingKeyTurnstileEnabled] == "true",
		TurnstileSiteKey:                    settings[SettingKeyTurnstileSiteKey],
		TencentCaptchaEnabled:               settings[SettingKeyTencentCaptchaEnabled] == "true",
		TencentCaptchaAppID:                 settings[SettingKeyTencentCaptchaAppID],
		TencentCaptchaRegion:                normalizeTencentCaptchaRegion(settings[SettingKeyTencentCaptchaRegion]),
		AliyunCaptchaEnabled:                settings[SettingKeyAliyunCaptchaEnabled] == "true",
		AliyunCaptchaSceneID:                settings[SettingKeyAliyunCaptchaSceneID],
		AliyunCaptchaPrefix:                 settings[SettingKeyAliyunCaptchaPrefix],
		AliyunCaptchaRegion:                 normalizeAliyunCaptchaRegion(settings[SettingKeyAliyunCaptchaRegion]),
		SiteName:                            s.getStringOrDefault(settings, SettingKeySiteName, "Sub2API"),
		SiteLogo:                            settings[SettingKeySiteLogo],
		SiteSubtitle:                        s.getStringOrDefault(settings, SettingKeySiteSubtitle, "Subscription to API Conversion Platform"),
		SiteNameZh:                          settings[SettingKeySiteNameZh],
		SiteNameEn:                          settings[SettingKeySiteNameEn],
		SiteTitleZh:                         settings[SettingKeySiteTitleZh],
		SiteTitleEn:                         settings[SettingKeySiteTitleEn],
		SiteSubtitleZh:                      settings[SettingKeySiteSubtitleZh],
		SiteSubtitleEn:                      settings[SettingKeySiteSubtitleEn],
		APIBaseURL:                          settings[SettingKeyAPIBaseURL],
		ContactInfo:                         settings[SettingKeyContactInfo],
		DocURL:                              settings[SettingKeyDocURL],
		HomeContent:                         settings[SettingKeyHomeContent],
		HideCcsImportButton:                 settings[SettingKeyHideCcsImportButton] == "true",
		PurchaseSubscriptionEnabled:         settings[SettingKeyPurchaseSubscriptionEnabled] == "true",
		PurchaseSubscriptionURL:             strings.TrimSpace(settings[SettingKeyPurchaseSubscriptionURL]),
		TableDefaultPageSize:                tableDefaultPageSize,
		TablePageSizeOptions:                tablePageSizeOptions,
		UsageRankingLimit:                   usageRankingLimit,
		CustomMenuItems:                     settings[SettingKeyCustomMenuItems],
		CustomEndpoints:                     settings[SettingKeyCustomEndpoints],
		FooterLinks:                         settings[SettingKeyFooterLinks],
		FooterText:                          settings[SettingKeyFooterText],
		LinuxDoOAuthEnabled:                 linuxDoEnabled,
		DingTalkOAuthEnabled:                dingTalkEnabled,
		WeChatOAuthEnabled:                  weChatEnabled,
		WeChatOAuthOpenEnabled:              weChatOpenEnabled,
		WeChatOAuthMPEnabled:                weChatMPEnabled,
		WeChatOAuthMobileEnabled:            weChatMobileEnabled,
		BackendModeEnabled:                  settings[SettingKeyBackendModeEnabled] == "true",
		PaymentEnabled:                      settings[SettingPaymentEnabled] == "true",
		OIDCOAuthEnabled:                    oidcEnabled,
		OIDCOAuthProviderName:               oidcProviderName,
		GitHubOAuthEnabled:                  gitHubOAuthEnabled,
		GoogleOAuthEnabled:                  googleOAuthEnabled,
		GoogleOneTapEnabled:                 googleOneTapEnabled,
		GoogleOAuthClientID:                 googleOAuthClientID,
		BalanceUnitName:                     balanceUnitName,
		BalanceUnitSymbol:                   balanceUnitSymbol,
		BalanceIconSVG:                      strings.TrimSpace(settings[SettingKeyBalanceIconSVG]),
		BalanceLowNotifyEnabled:             settings[SettingKeyBalanceLowNotifyEnabled] == "true",
		AccountQuotaNotifyEnabled:           settings[SettingKeyAccountQuotaNotifyEnabled] == "true",
		RiskControlEnabled:                  settings[SettingKeyRiskControlEnabled] == "true",
		BalanceLowNotifyThreshold:           balanceLowNotifyThreshold,
		BalanceLowNotifyRechargeURL:         settings[SettingKeyBalanceLowNotifyRechargeURL],
		AllowUserViewErrorRequests:          settings[SettingKeyAllowUserViewErrorRequests] == "true",
	}, nil
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

// GetPublicSettingsForInjection 返回适合 HTML 注入的公开设置。
// 该方法实现 web.PublicSettingsProvider 接口。
func (s *SettingService) GetPublicSettingsForInjection(ctx context.Context) (any, error) {
	settings, err := s.GetPublicSettings(ctx)
	if err != nil {
		return nil, err
	}

	// Return a struct that matches the frontend's expected format
	return &struct {
		RegistrationEnabled                 bool                     `json:"registration_enabled"`
		EmailVerifyEnabled                  bool                     `json:"email_verify_enabled"`
		ForceEmailOnThirdPartySignup        bool                     `json:"force_email_on_third_party_signup"`
		RegistrationEmailSuffixWhitelist    []string                 `json:"registration_email_suffix_whitelist"`
		RegistrationEmailDomainQuotaEnabled bool                     `json:"registration_email_domain_quota_enabled"`
		PromoCodeEnabled                    bool                     `json:"promo_code_enabled"`
		PasswordResetEnabled                bool                     `json:"password_reset_enabled"`
		InvitationCodeEnabled               bool                     `json:"invitation_code_enabled"`
		TotpEnabled                         bool                     `json:"totp_enabled"`
		PasskeyEnabled                      bool                     `json:"passkey_enabled"`
		LoginAgreementEnabled               bool                     `json:"login_agreement_enabled"`
		LoginAgreementMode                  string                   `json:"login_agreement_mode"`
		LoginAgreementUpdatedAt             string                   `json:"login_agreement_updated_at"`
		LoginAgreementRevision              string                   `json:"login_agreement_revision"`
		LoginAgreementDocuments             []LoginAgreementDocument `json:"login_agreement_documents"`
		TurnstileEnabled                    bool                     `json:"turnstile_enabled"`
		TurnstileSiteKey                    string                   `json:"turnstile_site_key,omitempty"`
		TencentCaptchaEnabled               bool                     `json:"tencent_captcha_enabled"`
		TencentCaptchaAppID                 string                   `json:"tencent_captcha_app_id,omitempty"`
		TencentCaptchaRegion                string                   `json:"tencent_captcha_region,omitempty"`
		AliyunCaptchaEnabled                bool                     `json:"aliyun_captcha_enabled"`
		AliyunCaptchaSceneID                string                   `json:"aliyun_captcha_scene_id,omitempty"`
		AliyunCaptchaPrefix                 string                   `json:"aliyun_captcha_prefix,omitempty"`
		AliyunCaptchaRegion                 string                   `json:"aliyun_captcha_region,omitempty"`
		SiteName                            string                   `json:"site_name"`
		SiteLogo                            string                   `json:"site_logo,omitempty"`
		SiteSubtitle                        string                   `json:"site_subtitle,omitempty"`
		SiteNameZh                          string                   `json:"site_name_zh,omitempty"`
		SiteNameEn                          string                   `json:"site_name_en,omitempty"`
		SiteTitleZh                         string                   `json:"site_title_zh,omitempty"`
		SiteTitleEn                         string                   `json:"site_title_en,omitempty"`
		SiteSubtitleZh                      string                   `json:"site_subtitle_zh,omitempty"`
		SiteSubtitleEn                      string                   `json:"site_subtitle_en,omitempty"`
		APIBaseURL                          string                   `json:"api_base_url,omitempty"`
		ContactInfo                         string                   `json:"contact_info,omitempty"`
		DocURL                              string                   `json:"doc_url,omitempty"`
		HomeContent                         string                   `json:"home_content,omitempty"`
		HideCcsImportButton                 bool                     `json:"hide_ccs_import_button"`
		PurchaseSubscriptionEnabled         bool                     `json:"purchase_subscription_enabled"`
		PurchaseSubscriptionURL             string                   `json:"purchase_subscription_url,omitempty"`
		TableDefaultPageSize                int                      `json:"table_default_page_size"`
		TablePageSizeOptions                []int                    `json:"table_page_size_options"`
		UsageRankingLimit                   int                      `json:"usage_ranking_limit"`
		CustomMenuItems                     json.RawMessage          `json:"custom_menu_items"`
		CustomEndpoints                     json.RawMessage          `json:"custom_endpoints"`
		FooterLinks                         json.RawMessage          `json:"footer_links"`
		FooterText                          string                   `json:"footer_text,omitempty"`
		LinuxDoOAuthEnabled                 bool                     `json:"linuxdo_oauth_enabled"`
		DingTalkOAuthEnabled                bool                     `json:"dingtalk_oauth_enabled"`
		WeChatOAuthEnabled                  bool                     `json:"wechat_oauth_enabled"`
		WeChatOAuthOpenEnabled              bool                     `json:"wechat_oauth_open_enabled"`
		WeChatOAuthMPEnabled                bool                     `json:"wechat_oauth_mp_enabled"`
		WeChatOAuthMobileEnabled            bool                     `json:"wechat_oauth_mobile_enabled"`
		BackendModeEnabled                  bool                     `json:"backend_mode_enabled"`
		PaymentEnabled                      bool                     `json:"payment_enabled"`
		TeamEnabled                         bool                     `json:"team_enabled"`
		TeamSelfServiceEnabled              bool                     `json:"team_self_service_enabled"`
		DataSharingEnabled                  bool                     `json:"data_sharing_enabled"`
		OIDCOAuthEnabled                    bool                     `json:"oidc_oauth_enabled"`
		OIDCOAuthProviderName               string                   `json:"oidc_oauth_provider_name"`
		GitHubOAuthEnabled                  bool                     `json:"github_oauth_enabled"`
		GoogleOAuthEnabled                  bool                     `json:"google_oauth_enabled"`
		GoogleOneTapEnabled                 bool                     `json:"google_one_tap_enabled"`
		GoogleOAuthClientID                 string                   `json:"google_oauth_client_id"`
		Version                             string                   `json:"version,omitempty"`
		// 服务器全局时区与当前 UTC 偏移，供前端标注高峰计费窗口等服务端本地时间。
		ServerTimezone              string  `json:"server_timezone"`
		ServerUTCOffset             string  `json:"server_utc_offset"`
		BalanceUnitName             string  `json:"balance_unit_name"`
		BalanceUnitSymbol           string  `json:"balance_unit_symbol"`
		BalanceIconSVG              string  `json:"balance_icon_svg"`
		BalanceLowNotifyEnabled     bool    `json:"balance_low_notify_enabled"`
		AccountQuotaNotifyEnabled   bool    `json:"account_quota_notify_enabled"`
		RiskControlEnabled          bool    `json:"risk_control_enabled"`
		AffiliateEnabled            bool    `json:"affiliate_enabled"`
		BalanceLowNotifyThreshold   float64 `json:"balance_low_notify_threshold"`
		BalanceLowNotifyRechargeURL string  `json:"balance_low_notify_recharge_url"`
		AllowUserViewErrorRequests  bool    `json:"allow_user_view_error_requests"`
	}{
		RegistrationEnabled:                 settings.RegistrationEnabled,
		EmailVerifyEnabled:                  settings.EmailVerifyEnabled,
		ForceEmailOnThirdPartySignup:        settings.ForceEmailOnThirdPartySignup,
		RegistrationEmailSuffixWhitelist:    settings.RegistrationEmailSuffixWhitelist,
		RegistrationEmailDomainQuotaEnabled: settings.RegistrationEmailDomainQuotaEnabled,
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
		SiteName:                            settings.SiteName,
		SiteLogo:                            settings.SiteLogo,
		SiteSubtitle:                        settings.SiteSubtitle,
		SiteNameZh:                          settings.SiteNameZh,
		SiteNameEn:                          settings.SiteNameEn,
		SiteTitleZh:                         settings.SiteTitleZh,
		SiteTitleEn:                         settings.SiteTitleEn,
		SiteSubtitleZh:                      settings.SiteSubtitleZh,
		SiteSubtitleEn:                      settings.SiteSubtitleEn,
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
		CustomMenuItems:                     filterUserVisibleMenuItems(settings.CustomMenuItems),
		CustomEndpoints:                     safeRawJSONArray(settings.CustomEndpoints),
		FooterLinks:                         safeRawJSONArray(settings.FooterLinks),
		FooterText:                          settings.FooterText,
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
		DataSharingEnabled:                  settings.DataSharingEnabled,
		OIDCOAuthEnabled:                    settings.OIDCOAuthEnabled,
		OIDCOAuthProviderName:               settings.OIDCOAuthProviderName,
		GitHubOAuthEnabled:                  settings.GitHubOAuthEnabled,
		GoogleOAuthEnabled:                  settings.GoogleOAuthEnabled,
		GoogleOneTapEnabled:                 settings.GoogleOneTapEnabled,
		GoogleOAuthClientID:                 settings.GoogleOAuthClientID,
		Version:                             s.version,
		ServerTimezone:                      timezone.Name(),
		ServerUTCOffset:                     timezone.UTCOffset(),
		BalanceUnitName:                     settings.BalanceUnitName,
		BalanceUnitSymbol:                   settings.BalanceUnitSymbol,
		BalanceIconSVG:                      settings.BalanceIconSVG,
		BalanceLowNotifyEnabled:             settings.BalanceLowNotifyEnabled,
		AccountQuotaNotifyEnabled:           settings.AccountQuotaNotifyEnabled,
		RiskControlEnabled:                  settings.RiskControlEnabled,
		AffiliateEnabled:                    settings.AffiliateEnabled,
		BalanceLowNotifyThreshold:           settings.BalanceLowNotifyThreshold,
		BalanceLowNotifyRechargeURL:         settings.BalanceLowNotifyRechargeURL,
		AllowUserViewErrorRequests:          settings.AllowUserViewErrorRequests,
	}, nil
}

// filterUserVisibleMenuItems filters out admin-only menu items from a raw JSON
// array string, returning only items with visibility != "admin".
func filterUserVisibleMenuItems(raw string) json.RawMessage {
	raw = strings.TrimSpace(raw)
	if raw == "" || raw == "[]" {
		return json.RawMessage("[]")
	}
	var items []struct {
		Visibility string `json:"visibility"`
	}
	if err := json.Unmarshal([]byte(raw), &items); err != nil {
		return json.RawMessage("[]")
	}

	// Parse full items to preserve all fields
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
		return json.RawMessage("[]")
	}
	return result
}

// safeRawJSONArray returns raw as json.RawMessage if it's valid JSON, otherwise "[]".
func safeRawJSONArray(raw string) json.RawMessage {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return json.RawMessage("[]")
	}
	if json.Valid([]byte(raw)) {
		return json.RawMessage(raw)
	}
	return json.RawMessage("[]")
}

// GetFrameSrcOrigins returns deduplicated http(s) origins from home_content URL,
// purchase_subscription_url, and all custom_menu_items URLs. Used by the router layer for CSP frame-src injection.
func (s *SettingService) GetFrameSrcOrigins(ctx context.Context) ([]string, error) {
	settings, err := s.GetPublicSettings(ctx)
	if err != nil {
		return nil, err
	}

	seen := make(map[string]struct{})
	var origins []string

	addOrigin := func(rawURL string) {
		if origin := extractOriginFromURL(rawURL); origin != "" {
			if _, ok := seen[origin]; !ok {
				seen[origin] = struct{}{}
				origins = append(origins, origin)
			}
		}
	}

	// home content URL (when home_content is set to a URL for iframe embedding)
	addOrigin(settings.HomeContent)

	// purchase subscription URL
	if settings.PurchaseSubscriptionEnabled {
		addOrigin(settings.PurchaseSubscriptionURL)
	}

	// all custom menu items (including admin-only, since CSP must allow all iframes)
	for _, item := range parseCustomMenuItemURLs(settings.CustomMenuItems) {
		addOrigin(item)
	}

	return origins, nil
}

// extractOriginFromURL returns the scheme+host origin from rawURL.
// Only http and https schemes are accepted.
func extractOriginFromURL(rawURL string) string {
	rawURL = strings.TrimSpace(rawURL)
	if rawURL == "" {
		return ""
	}
	u, err := url.Parse(rawURL)
	if err != nil || u.Host == "" {
		return ""
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return ""
	}
	return u.Scheme + "://" + u.Host
}

// parseCustomMenuItemURLs extracts URLs from a raw JSON array of custom menu items.
func parseCustomMenuItemURLs(raw string) []string {
	raw = strings.TrimSpace(raw)
	if raw == "" || raw == "[]" {
		return nil
	}
	var items []struct {
		URL string `json:"url"`
	}
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
