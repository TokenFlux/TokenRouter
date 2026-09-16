// 公开 API DTO 与 embed 注入字段保持各自原有形状。
package dto

type LoginAgreementDocument struct {
	ID        string `json:"id"`
	Title     string `json:"title"`
	ContentMD string `json:"content_md"`
}
type PublicSettings struct {
	RegistrationEnabled                 bool                     `json:"registration_enabled"`
	EmailVerifyEnabled                  bool                     `json:"email_verify_enabled"`
	ForceEmailOnThirdPartySignup        bool                     `json:"force_email_on_third_party_signup"`
	RegistrationEmailSuffixWhitelist    []string                 `json:"registration_email_suffix_whitelist"`
	RegistrationEmailDomainQuotaEnabled bool                     `json:"registration_email_domain_quota_enabled"`
	UserEmailChangeEnabled              bool                     `json:"user_email_change_enabled"` // 是否允许已有邮箱的用户换绑主邮箱
	PromoCodeEnabled                    bool                     `json:"promo_code_enabled"`
	PasswordResetEnabled                bool                     `json:"password_reset_enabled"`
	InvitationCodeEnabled               bool                     `json:"invitation_code_enabled"`
	TotpEnabled                         bool                     `json:"totp_enabled"` // TOTP 双因素认证
	PasskeyEnabled                      bool                     `json:"passkey_enabled"`
	LoginAgreementEnabled               bool                     `json:"login_agreement_enabled"`
	LoginAgreementMode                  string                   `json:"login_agreement_mode"`
	LoginAgreementUpdatedAt             string                   `json:"login_agreement_updated_at"`
	LoginAgreementRevision              string                   `json:"login_agreement_revision"`
	LoginAgreementDocuments             []LoginAgreementDocument `json:"login_agreement_documents"`
	TurnstileEnabled                    bool                     `json:"turnstile_enabled"`
	TurnstileSiteKey                    string                   `json:"turnstile_site_key"`
	TencentCaptchaEnabled               bool                     `json:"tencent_captcha_enabled"`
	TencentCaptchaAppID                 string                   `json:"tencent_captcha_app_id"`
	TencentCaptchaRegion                string                   `json:"tencent_captcha_region"`
	AliyunCaptchaEnabled                bool                     `json:"aliyun_captcha_enabled"`
	AliyunCaptchaSceneID                string                   `json:"aliyun_captcha_scene_id"`
	AliyunCaptchaPrefix                 string                   `json:"aliyun_captcha_prefix"`
	AliyunCaptchaRegion                 string                   `json:"aliyun_captcha_region"`
	SiteName                            string                   `json:"site_name"`
	SiteLogo                            string                   `json:"site_logo"`
	SiteSubtitle                        string                   `json:"site_subtitle"`
	SiteNameZh                          string                   `json:"site_name_zh"`
	SiteNameEn                          string                   `json:"site_name_en"`
	SiteTitleZh                         string                   `json:"site_title_zh"`
	SiteTitleEn                         string                   `json:"site_title_en"`
	SiteSubtitleZh                      string                   `json:"site_subtitle_zh"`
	SiteSubtitleEn                      string                   `json:"site_subtitle_en"`
	APIBaseURL                          string                   `json:"api_base_url"`
	ContactInfo                         string                   `json:"contact_info"`
	DocURL                              string                   `json:"doc_url"`
	HomeContent                         string                   `json:"home_content"`
	HideCcsImportButton                 bool                     `json:"hide_ccs_import_button"`
	PurchaseSubscriptionEnabled         bool                     `json:"purchase_subscription_enabled"`
	PurchaseSubscriptionURL             string                   `json:"purchase_subscription_url"`
	TableDefaultPageSize                int                      `json:"table_default_page_size"`
	TablePageSizeOptions                []int                    `json:"table_page_size_options"`
	UsageRankingLimit                   int                      `json:"usage_ranking_limit"`
	UsageRankingEnabled                 bool                     `json:"usage_ranking_enabled"`
	UsageRankingSortBy                  string                   `json:"usage_ranking_sort_by"`
	UsageRankingShowTotalTokens         bool                     `json:"usage_ranking_show_total_tokens"`
	UsageRankingShowRequests            bool                     `json:"usage_ranking_show_requests"`
	UsageRankingShowActualCost          bool                     `json:"usage_ranking_show_actual_cost"`
	CustomMenuItems                     []CustomMenuItem         `json:"custom_menu_items"`
	CustomEndpoints                     []CustomEndpoint         `json:"custom_endpoints"`
	FooterLinks                         []FooterLinkGroup        `json:"footer_links"`
	FooterText                          string                   `json:"footer_text"`
	HomeFeaturedModels                  []string                 `json:"home_featured_models"`
	DingTalkOAuthEnabled                bool                     `json:"dingtalk_oauth_enabled"`
	LinuxDoOAuthEnabled                 bool                     `json:"linuxdo_oauth_enabled"`
	WeChatOAuthEnabled                  bool                     `json:"wechat_oauth_enabled"`
	WeChatOAuthOpenEnabled              bool                     `json:"wechat_oauth_open_enabled"`
	WeChatOAuthMPEnabled                bool                     `json:"wechat_oauth_mp_enabled"`
	WeChatOAuthMobileEnabled            bool                     `json:"wechat_oauth_mobile_enabled"`
	OIDCOAuthEnabled                    bool                     `json:"oidc_oauth_enabled"`
	OIDCOAuthProviderName               string                   `json:"oidc_oauth_provider_name"`
	GitHubOAuthEnabled                  bool                     `json:"github_oauth_enabled"`
	GoogleOAuthEnabled                  bool                     `json:"google_oauth_enabled"`
	GoogleOneTapEnabled                 bool                     `json:"google_one_tap_enabled"`
	GoogleOAuthClientID                 string                   `json:"google_oauth_client_id"`
	BackendModeEnabled                  bool                     `json:"backend_mode_enabled"`
	PaymentEnabled                      bool                     `json:"payment_enabled"`
	TeamEnabled                         bool                     `json:"team_enabled"`
	TeamSelfServiceEnabled              bool                     `json:"team_self_service_enabled"`
	CreativeEnabled                     bool                     `json:"creative_enabled"`
	Version                             string                   `json:"version"`
	// 服务器全局时区与当前 UTC 偏移，供前端标注高峰计费窗口等服务端本地时间。
	ServerTimezone              string  `json:"server_timezone"`
	ServerUTCOffset             string  `json:"server_utc_offset"`
	BalanceUnitName             string  `json:"balance_unit_name"`
	BalanceUnitSymbol           string  `json:"balance_unit_symbol"`
	BalanceIconSVG              string  `json:"balance_icon_svg"`
	BalanceLowNotifyEnabled     bool    `json:"balance_low_notify_enabled"`
	AccountQuotaNotifyEnabled   bool    `json:"account_quota_notify_enabled"`
	RiskControlEnabled          bool    `json:"risk_control_enabled"` // 风控中心入口开关
	CyberSessionBlockEnabled    bool    `json:"cyber_session_block_enabled"`
	CyberSessionBlockTTLSeconds int     `json:"cyber_session_block_ttl_seconds"`
	AffiliateEnabled            bool    `json:"affiliate_enabled"` // 邀请返利入口开关
	BalanceLowNotifyThreshold   float64 `json:"balance_low_notify_threshold"`
	BalanceLowNotifyRechargeURL string  `json:"balance_low_notify_recharge_url"`

	// 允许终端用户在用量页查看自己的失败请求
	AllowUserViewErrorRequests bool `json:"allow_user_view_error_requests"`
}
