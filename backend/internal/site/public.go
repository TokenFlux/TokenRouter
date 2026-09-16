// PublicService 统一站点公开投影；来源端口先完成其他模块的资格投影。
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

	"github.com/TokenFlux/TokenRouter/internal/pkg/timezone"
)

type PublicSource interface {
	LoadSitePublicInputs(context.Context) (PublicInputs, error)
	PublicVersion() string
}
type PublicInputs struct {
	Values map[string]string
	Auth   PublicAuth
	Usage  PublicUsage
}
type PublicAuth struct {
	LinuxDo, DingTalk, OIDC, WeChat, WeChatOpen, WeChatMP, WeChatMobile, GitHub, Google, GoogleOneTap, Team, TeamSelfService, Passkey bool
	OIDCName, GoogleClientID, TencentRegion, AliyunRegion                                                                             string
	RegistrationSuffixes                                                                                                              []string
}
type PublicUsage struct {
	Limit                                         int
	Enabled                                       bool
	SortBy                                        string
	ShowTotalTokens, ShowRequests, ShowActualCost bool
}
type PublicService struct{ source PublicSource }

func NewPublicService(source PublicSource) *PublicService { return &PublicService{source: source} }
func (s *PublicService) GetPublicSettings(ctx context.Context) (*PublicSettings, error) {
	input, err := s.source.LoadSitePublicInputs(ctx)
	if err != nil {
		return nil, err
	}
	settings := input.Values

	// Password reset requires email verification to be enabled
	emailVerifyEnabled := settings[SettingKeyEmailVerifyEnabled] == "true"
	passwordResetEnabled := emailVerifyEnabled && settings[SettingKeyPasswordResetEnabled] == "true"
	registrationEmailSuffixWhitelist := input.Auth.RegistrationSuffixes
	tableDefaultPageSize, tablePageSizeOptions := ParseTablePreferences(
		settings[SettingKeyTableDefaultPageSize],
		settings[SettingKeyTablePageSizeOptions],
	)
	usageRanking := input.Usage
	loginAgreementDocuments := ParseLoginAgreementDocuments(settings[SettingKeyLoginAgreementDocuments])
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
		UserEmailChangeEnabled:              settings[SettingKeyUserEmailChangeEnabled] == "true",
		PromoCodeEnabled:                    settings[SettingKeyPromoCodeEnabled] != "false", // 默认启用
		PasswordResetEnabled:                passwordResetEnabled,
		InvitationCodeEnabled:               settings[SettingKeyInvitationCodeEnabled] == "true",
		TeamEnabled:                         input.Auth.Team,
		TeamSelfServiceEnabled:              input.Auth.TeamSelfService,
		CreativeEnabled:                     settings[SettingKeyCreativeEnabled] != "false",
		AffiliateEnabled:                    settings[SettingKeyAffiliateEnabled] == "true",
		TotpEnabled:                         settings[SettingKeyTotpEnabled] == "true",
		PasskeyEnabled:                      input.Auth.Passkey,
		LoginAgreementEnabled:               settings[SettingKeyLoginAgreementEnabled] == "true" && len(loginAgreementDocuments) > 0,
		LoginAgreementMode:                  NormalizeLoginAgreementMode(settings[SettingKeyLoginAgreementMode]),
		LoginAgreementUpdatedAt:             loginAgreementUpdatedAt,
		LoginAgreementRevision:              BuildLoginAgreementRevision(loginAgreementUpdatedAt, loginAgreementDocuments),
		LoginAgreementDocuments:             loginAgreementDocuments,
		TurnstileEnabled:                    settings[SettingKeyTurnstileEnabled] == "true",
		TurnstileSiteKey:                    settings[SettingKeyTurnstileSiteKey],
		TencentCaptchaEnabled:               settings[SettingKeyTencentCaptchaEnabled] == "true",
		TencentCaptchaAppID:                 settings[SettingKeyTencentCaptchaAppID],
		TencentCaptchaRegion:                input.Auth.TencentRegion,
		AliyunCaptchaEnabled:                settings[SettingKeyAliyunCaptchaEnabled] == "true",
		AliyunCaptchaSceneID:                settings[SettingKeyAliyunCaptchaSceneID],
		AliyunCaptchaPrefix:                 settings[SettingKeyAliyunCaptchaPrefix],
		AliyunCaptchaRegion:                 input.Auth.AliyunRegion,
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
		UsageRankingLimit:                   usageRanking.Limit,
		UsageRankingEnabled:                 usageRanking.Enabled,
		UsageRankingSortBy:                  string(usageRanking.SortBy),
		UsageRankingShowTotalTokens:         usageRanking.ShowTotalTokens,
		UsageRankingShowRequests:            usageRanking.ShowRequests,
		UsageRankingShowActualCost:          usageRanking.ShowActualCost,
		CustomMenuItems:                     settings[SettingKeyCustomMenuItems],
		CustomEndpoints:                     settings[SettingKeyCustomEndpoints],
		FooterLinks:                         settings[SettingKeyFooterLinks],
		FooterText:                          settings[SettingKeyFooterText],
		HomeFeaturedModels:                  settings[SettingKeyHomeFeaturedModels],
		LinuxDoOAuthEnabled:                 input.Auth.LinuxDo,
		DingTalkOAuthEnabled:                input.Auth.DingTalk,
		WeChatOAuthEnabled:                  input.Auth.WeChat,
		WeChatOAuthOpenEnabled:              input.Auth.WeChatOpen,
		WeChatOAuthMPEnabled:                input.Auth.WeChatMP,
		WeChatOAuthMobileEnabled:            input.Auth.WeChatMobile,
		BackendModeEnabled:                  settings[SettingKeyBackendModeEnabled] == "true",
		PaymentEnabled:                      settings[SettingPaymentEnabled] == "true",
		OIDCOAuthEnabled:                    input.Auth.OIDC,
		OIDCOAuthProviderName:               input.Auth.OIDCName,
		GitHubOAuthEnabled:                  input.Auth.GitHub,
		GoogleOAuthEnabled:                  input.Auth.Google,
		GoogleOneTapEnabled:                 input.Auth.GoogleOneTap,
		GoogleOAuthClientID:                 input.Auth.GoogleClientID,
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

// getStringOrDefault 获取字符串值或默认值
func (s *PublicService) getStringOrDefault(settings map[string]string, key, defaultValue string) string {
	if value, ok := settings[key]; ok && value != "" {
		return value
	}
	return defaultValue
}

const defaultLoginAgreementMode = "modal"
const defaultLoginAgreementDate = "2026-03-31"

func NormalizeLoginAgreementMode(raw string) string {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "checkbox":
		return "checkbox"
	default:
		return defaultLoginAgreementMode
	}
}
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
func NormalizeLoginAgreementDocumentID(raw string) string {
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
func ParseLoginAgreementDocuments(raw string) []LoginAgreementDocument {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return DefaultLoginAgreementDocuments()
	}
	var docs []LoginAgreementDocument
	if err := json.Unmarshal([]byte(raw), &docs); err != nil {
		return DefaultLoginAgreementDocuments()
	}
	docs = NormalizeLoginAgreementDocuments(docs)
	if len(docs) == 0 {
		return DefaultLoginAgreementDocuments()
	}
	return docs
}
func MarshalLoginAgreementDocuments(docs []LoginAgreementDocument) (string, error) {
	normalized := NormalizeLoginAgreementDocuments(docs)
	if len(normalized) == 0 {
		normalized = DefaultLoginAgreementDocuments()
	}
	b, err := json.Marshal(normalized)
	if err != nil {
		return "", fmt.Errorf("marshal login agreement documents: %w", err)
	}
	return string(b), nil
}
func BuildLoginAgreementRevision(updatedAt string, docs []LoginAgreementDocument) string {
	normalized := NormalizeLoginAgreementDocuments(docs)
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

// GetPublicSettingsForInjection 返回适合 HTML 注入的公开设置。
// 该方法实现 web.PublicSettingsProvider 接口。
func (s *PublicService) GetPublicSettingsForInjection(ctx context.Context) (any, error) {
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
		UserEmailChangeEnabled              bool                     `json:"user_email_change_enabled"` // 是否允许已有邮箱的用户换绑主邮箱
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
		UsageRankingEnabled                 bool                     `json:"usage_ranking_enabled"`
		UsageRankingSortBy                  string                   `json:"usage_ranking_sort_by"`
		UsageRankingShowTotalTokens         bool                     `json:"usage_ranking_show_total_tokens"`
		UsageRankingShowRequests            bool                     `json:"usage_ranking_show_requests"`
		UsageRankingShowActualCost          bool                     `json:"usage_ranking_show_actual_cost"`
		CustomMenuItems                     json.RawMessage          `json:"custom_menu_items"`
		CustomEndpoints                     json.RawMessage          `json:"custom_endpoints"`
		FooterLinks                         json.RawMessage          `json:"footer_links"`
		FooterText                          string                   `json:"footer_text,omitempty"`
		HomeFeaturedModels                  json.RawMessage          `json:"home_featured_models"`
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
		CreativeEnabled                     bool                     `json:"creative_enabled"`
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
func FilterUserVisibleMenuItems(raw string) json.RawMessage {
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
func SafeRawJSONArray(raw string) json.RawMessage {
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
func (s *PublicService) GetFrameSrcOrigins(ctx context.Context) ([]string, error) {
	settings, err := s.GetPublicSettings(ctx)
	if err != nil {
		return nil, err
	}

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

	// home content URL (when home_content is set to a URL for iframe embedding)
	addOrigin(settings.HomeContent)

	// purchase subscription URL
	if settings.PurchaseSubscriptionEnabled {
		addOrigin(settings.PurchaseSubscriptionURL)
	}

	// all custom menu items (including admin-only, since CSP must allow all iframes)
	for _, item := range ParseCustomMenuItemURLs(settings.CustomMenuItems) {
		addOrigin(item)
	}

	return origins, nil
}

// extractOriginFromURL returns the scheme+host origin from rawURL.
// Only http and https schemes are accepted.
func ExtractOriginFromURL(rawURL string) string {
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
func ParseCustomMenuItemURLs(raw string) []string {
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
func NormalizeTablePreferences(defaultPageSize int, options []int) (int, []int) {
	const minPageSize = 5
	const maxPageSize = 1000
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
		defaultPageSize = fallbackPageSize
	}

	if len(normalizedOptions) == 0 {
		normalizedOptions = []int{10, 20, 50}
	}

	return defaultPageSize, normalizedOptions
}
func ParseTablePreferences(defaultPageSizeRaw, optionsRaw string) (int, []int) {
	defaultPageSize := 20
	if v, err := strconv.Atoi(strings.TrimSpace(defaultPageSizeRaw)); err == nil {
		defaultPageSize = v
	}

	var options []int
	if strings.TrimSpace(optionsRaw) != "" {
		_ = json.Unmarshal([]byte(optionsRaw), &options)
	}

	return NormalizeTablePreferences(defaultPageSize, options)
}

const SettingKeyAPIBaseURL = "api_base_url"
const SettingKeyAccountQuotaNotifyEnabled = "account_quota_notify_enabled"
const SettingKeyAffiliateEnabled = "affiliate_enabled"
const SettingKeyAliyunCaptchaEnabled = "aliyun_captcha_enabled"
const SettingKeyAliyunCaptchaPrefix = "aliyun_captcha_prefix"
const SettingKeyAliyunCaptchaSceneID = "aliyun_captcha_scene_id"
const SettingKeyAllowUserViewErrorRequests = "allow_user_view_error_requests"
const SettingKeyBackendModeEnabled = "backend_mode_enabled"
const SettingKeyBalanceIconSVG = "balance_icon_svg"
const SettingKeyBalanceLowNotifyEnabled = "balance_low_notify_enabled"
const SettingKeyBalanceLowNotifyRechargeURL = "balance_low_notify_recharge_url"
const SettingKeyBalanceLowNotifyThreshold = "balance_low_notify_threshold"
const SettingKeyBalanceUnitName = "balance_unit_name"
const SettingKeyBalanceUnitSymbol = "balance_unit_symbol"
const SettingKeyContactInfo = "contact_info"
const SettingKeyCreativeEnabled = "creative_enabled"
const SettingKeyCustomEndpoints = "custom_endpoints"
const SettingKeyCustomMenuItems = "custom_menu_items"
const SettingKeyDocURL = "doc_url"
const SettingKeyEmailVerifyEnabled = "email_verify_enabled"
const SettingKeyFooterLinks = "footer_links"
const SettingKeyFooterText = "footer_text"
const SettingKeyForceEmailOnThirdPartySignup = "force_email_on_third_party_signup"
const SettingKeyHideCcsImportButton = "hide_ccs_import_button"
const SettingKeyHomeContent = "home_content"
const SettingKeyHomeFeaturedModels = "home_featured_models"
const SettingKeyInvitationCodeEnabled = "invitation_code_enabled"
const SettingKeyLoginAgreementDocuments = "login_agreement_documents"
const SettingKeyLoginAgreementEnabled = "login_agreement_enabled"
const SettingKeyLoginAgreementMode = "login_agreement_mode"
const SettingKeyLoginAgreementUpdatedAt = "login_agreement_updated_at"
const SettingKeyPasswordResetEnabled = "password_reset_enabled"
const SettingKeyPromoCodeEnabled = "promo_code_enabled"
const SettingKeyPurchaseSubscriptionEnabled = "purchase_subscription_enabled"
const SettingKeyPurchaseSubscriptionURL = "purchase_subscription_url"
const SettingKeyRegistrationEmailDomainQuotaEnabled = "registration_email_domain_quota_enabled"
const SettingKeyRegistrationEnabled = "registration_enabled"
const SettingKeyRiskControlEnabled = "risk_control_enabled"
const SettingKeySiteLogo = "site_logo"
const SettingKeySiteName = "site_name"
const SettingKeySiteNameEn = "site_name_en"
const SettingKeySiteNameZh = "site_name_zh"
const SettingKeySiteSubtitle = "site_subtitle"
const SettingKeySiteSubtitleEn = "site_subtitle_en"
const SettingKeySiteSubtitleZh = "site_subtitle_zh"
const SettingKeySiteTitleEn = "site_title_en"
const SettingKeySiteTitleZh = "site_title_zh"
const SettingKeyTableDefaultPageSize = "table_default_page_size"
const SettingKeyTablePageSizeOptions = "table_page_size_options"
const SettingKeyTencentCaptchaAppID = "tencent_captcha_app_id"
const SettingKeyTencentCaptchaEnabled = "tencent_captcha_enabled"
const SettingKeyTotpEnabled = "totp_enabled"
const SettingKeyTurnstileEnabled = "turnstile_enabled"
const SettingKeyTurnstileSiteKey = "turnstile_site_key"
const SettingKeyUserEmailChangeEnabled = "user_email_change_enabled"
const SettingPaymentEnabled = "payment_enabled"

func PublicValueKeys() []string {
	return []string{SettingKeyAPIBaseURL, SettingKeyAccountQuotaNotifyEnabled, SettingKeyAffiliateEnabled, SettingKeyAliyunCaptchaEnabled, SettingKeyAliyunCaptchaPrefix, SettingKeyAliyunCaptchaSceneID, SettingKeyAllowUserViewErrorRequests, SettingKeyBackendModeEnabled, SettingKeyBalanceIconSVG, SettingKeyBalanceLowNotifyEnabled, SettingKeyBalanceLowNotifyRechargeURL, SettingKeyBalanceLowNotifyThreshold, SettingKeyBalanceUnitName, SettingKeyBalanceUnitSymbol, SettingKeyContactInfo, SettingKeyCreativeEnabled, SettingKeyCustomEndpoints, SettingKeyCustomMenuItems, SettingKeyDocURL, SettingKeyEmailVerifyEnabled, SettingKeyFooterLinks, SettingKeyFooterText, SettingKeyForceEmailOnThirdPartySignup, SettingKeyHideCcsImportButton, SettingKeyHomeContent, SettingKeyHomeFeaturedModels, SettingKeyInvitationCodeEnabled, SettingKeyLoginAgreementDocuments, SettingKeyLoginAgreementEnabled, SettingKeyLoginAgreementMode, SettingKeyLoginAgreementUpdatedAt, SettingKeyPasswordResetEnabled, SettingKeyPromoCodeEnabled, SettingKeyPurchaseSubscriptionEnabled, SettingKeyPurchaseSubscriptionURL, SettingKeyRegistrationEmailDomainQuotaEnabled, SettingKeyRegistrationEnabled, SettingKeyRiskControlEnabled, SettingKeySiteLogo, SettingKeySiteName, SettingKeySiteNameEn, SettingKeySiteNameZh, SettingKeySiteSubtitle, SettingKeySiteSubtitleEn, SettingKeySiteSubtitleZh, SettingKeySiteTitleEn, SettingKeySiteTitleZh, SettingKeyTableDefaultPageSize, SettingKeyTablePageSizeOptions, SettingKeyTencentCaptchaAppID, SettingKeyTencentCaptchaEnabled, SettingKeyTotpEnabled, SettingKeyTurnstileEnabled, SettingKeyTurnstileSiteKey, SettingKeyUserEmailChangeEnabled, SettingPaymentEnabled}
}
