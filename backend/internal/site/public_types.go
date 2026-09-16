// 站点公开值类型不包含凭据。
package site

type LoginAgreementDocument struct {
	ID        string `json:"id"`
	Title     string `json:"title"`
	ContentMD string `json:"content_md"`
}
type PublicSettings struct {
	RegistrationEnabled                 bool
	EmailVerifyEnabled                  bool
	ForceEmailOnThirdPartySignup        bool
	RegistrationEmailSuffixWhitelist    []string
	RegistrationEmailDomainQuotaEnabled bool
	UserEmailChangeEnabled              bool // 是否允许已有邮箱的用户换绑主邮箱
	PromoCodeEnabled                    bool
	PasswordResetEnabled                bool
	InvitationCodeEnabled               bool
	TotpEnabled                         bool // TOTP 双因素认证
	PasskeyEnabled                      bool // Passkey 认证由部署级 WebAuthn 配置控制
	LoginAgreementEnabled               bool
	LoginAgreementMode                  string
	LoginAgreementUpdatedAt             string
	LoginAgreementRevision              string
	LoginAgreementDocuments             []LoginAgreementDocument
	TurnstileEnabled                    bool
	TurnstileSiteKey                    string
	TencentCaptchaEnabled               bool
	TencentCaptchaAppID                 string
	TencentCaptchaRegion                string
	AliyunCaptchaEnabled                bool
	AliyunCaptchaSceneID                string
	AliyunCaptchaPrefix                 string
	AliyunCaptchaRegion                 string
	SiteName                            string
	SiteLogo                            string
	SiteSubtitle                        string
	SiteNameZh                          string
	SiteNameEn                          string
	SiteTitleZh                         string
	SiteTitleEn                         string
	SiteSubtitleZh                      string
	SiteSubtitleEn                      string
	APIBaseURL                          string
	ContactInfo                         string
	DocURL                              string
	HomeContent                         string
	HideCcsImportButton                 bool

	PurchaseSubscriptionEnabled bool
	PurchaseSubscriptionURL     string
	TableDefaultPageSize        int
	TablePageSizeOptions        []int
	UsageRankingLimit           int
	UsageRankingEnabled         bool
	UsageRankingSortBy          string
	UsageRankingShowTotalTokens bool
	UsageRankingShowRequests    bool
	UsageRankingShowActualCost  bool
	CustomMenuItems             string // JSON array of custom menu items
	CustomEndpoints             string // JSON array of custom endpoints
	FooterLinks                 string // JSON array of footer link groups
	FooterText                  string // Extra footer text (ICP number etc.)
	HomeFeaturedModels          string // JSON array of model IDs featured on the home page

	LinuxDoOAuthEnabled      bool
	DingTalkOAuthEnabled     bool
	WeChatOAuthEnabled       bool
	WeChatOAuthOpenEnabled   bool
	WeChatOAuthMPEnabled     bool
	WeChatOAuthMobileEnabled bool
	BackendModeEnabled       bool
	PaymentEnabled           bool
	TeamEnabled              bool
	TeamSelfServiceEnabled   bool
	// CreativeEnabled 暴露给前端用于控制创作台页面入口与路由守卫。
	CreativeEnabled       bool
	OIDCOAuthEnabled      bool
	OIDCOAuthProviderName string
	GitHubOAuthEnabled    bool
	GoogleOAuthEnabled    bool
	GoogleOneTapEnabled   bool
	GoogleOAuthClientID   string
	Version               string
	BalanceUnitName       string
	BalanceUnitSymbol     string
	BalanceIconSVG        string

	BalanceLowNotifyEnabled   bool
	AccountQuotaNotifyEnabled bool
	// RiskControlEnabled 暴露给前端用于控制风控中心入口显示。
	RiskControlEnabled          bool
	AffiliateEnabled            bool
	BalanceLowNotifyThreshold   float64
	BalanceLowNotifyRechargeURL string

	// 允许终端用户在用量页查看自己的失败请求
	AllowUserViewErrorRequests bool `json:"allow_user_view_error_requests"`
}
