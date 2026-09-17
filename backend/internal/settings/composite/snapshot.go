// Package composite 保存综合管理接口的跨模块值投影，不承载配置规则或运行状态。
package composite

import (
	"github.com/TokenFlux/TokenRouter/internal/account"
	"github.com/TokenFlux/TokenRouter/internal/billing"
	"github.com/TokenFlux/TokenRouter/internal/creative"
	"github.com/TokenFlux/TokenRouter/internal/gateway/promptpolicy"
	"github.com/TokenFlux/TokenRouter/internal/identity/contact"
	"github.com/TokenFlux/TokenRouter/internal/site"
)

// Snapshot 保留综合读取、部分写入合并和旧消费者需要的字段形状。
type Snapshot struct {
	RegistrationEnabled                 bool
	EmailVerifyEnabled                  bool
	RegistrationEmailSuffixWhitelist    []string
	RegistrationEmailNormalization      bool
	RegistrationEmailDomainQuotaEnabled bool // 非白名单域名按主域名限量注册，默认关闭
	UserEmailChangeEnabled              bool // 已有邮箱身份的用户是否可以换绑主邮箱，默认关闭
	PromoCodeEnabled                    bool
	PasswordResetEnabled                bool
	FrontendURL                         string
	InvitationCodeEnabled               bool
	TotpEnabled                         bool // TOTP 双因素认证
	SessionBindingEnabled               bool // 会话 IP/UA 绑定（变更即失效）
	StepUpEnabled                       bool // 敏感操作 step-up 2FA 门控
	AuditLogRetentionDays               int  // 审计日志保留天数（<=0 永久保留）
	LoginAgreementEnabled               bool
	LoginAgreementMode                  string
	LoginAgreementUpdatedAt             string
	LoginAgreementDocuments             []site.LoginAgreementDocument

	SMTPHost               string
	SMTPPort               int
	SMTPUsername           string
	SMTPPassword           string
	SMTPPasswordConfigured bool
	SMTPFrom               string
	SMTPFromName           string
	SMTPUseTLS             bool

	TurnstileEnabled                       bool
	TurnstileSiteKey                       string
	TurnstileSecretKey                     string
	TurnstileSecretKeyConfigured           bool
	TencentCaptchaEnabled                  bool
	TencentCaptchaAppID                    string
	TencentCaptchaAppSecretKey             string
	TencentCaptchaAppSecretKeyConfigured   bool
	TencentCaptchaCloudSecretID            string
	TencentCaptchaCloudSecretIDConfigured  bool
	TencentCaptchaCloudSecretKey           string
	TencentCaptchaCloudSecretKeyConfigured bool
	TencentCaptchaRegion                   string
	AliyunCaptchaEnabled                   bool
	AliyunCaptchaAccessKeyID               string
	AliyunCaptchaAccessKeySecret           string
	AliyunCaptchaAccessKeySecretConfigured bool
	AliyunCaptchaSceneID                   string
	AliyunCaptchaPrefix                    string
	AliyunCaptchaRegion                    string
	APIKeyACLTrustForwardedIP              bool
	ForwardedClientIPHeaders               []string

	// LinuxDo Connect OAuth 登录
	LinuxDoConnectEnabled                bool
	LinuxDoConnectClientID               string
	LinuxDoConnectClientSecret           string
	LinuxDoConnectClientSecretConfigured bool
	LinuxDoConnectRedirectURL            string

	// DingTalk Connect OAuth 登录
	DingTalkConnectEnabled                 bool
	DingTalkConnectClientID                string
	DingTalkConnectClientSecret            string
	DingTalkConnectClientSecretConfigured  bool
	DingTalkConnectRedirectURL             string
	DingTalkConnectCorpRestrictionPolicy   string
	DingTalkConnectInternalCorpID          string
	DingTalkConnectBypassRegistration      bool
	DingTalkConnectSyncCorpEmail           bool
	DingTalkConnectSyncDisplayName         bool
	DingTalkConnectSyncDept                bool
	DingTalkConnectSyncCorpEmailAttrKey    string
	DingTalkConnectSyncDisplayNameAttrKey  string
	DingTalkConnectSyncDeptAttrKey         string
	DingTalkConnectSyncCorpEmailAttrName   string
	DingTalkConnectSyncDisplayNameAttrName string
	DingTalkConnectSyncDeptAttrName        string

	// WeChat Connect OAuth 登录
	WeChatConnectEnabled                   bool
	WeChatConnectAppID                     string
	WeChatConnectAppSecret                 string
	WeChatConnectAppSecretConfigured       bool
	WeChatConnectOpenAppID                 string
	WeChatConnectOpenAppSecret             string
	WeChatConnectOpenAppSecretConfigured   bool
	WeChatConnectMPAppID                   string
	WeChatConnectMPAppSecret               string
	WeChatConnectMPAppSecretConfigured     bool
	WeChatConnectMobileAppID               string
	WeChatConnectMobileAppSecret           string
	WeChatConnectMobileAppSecretConfigured bool
	WeChatConnectOpenEnabled               bool
	WeChatConnectMPEnabled                 bool
	WeChatConnectMobileEnabled             bool
	WeChatConnectMode                      string
	WeChatConnectScopes                    string
	WeChatConnectRedirectURL               string
	WeChatConnectFrontendRedirectURL       string

	// Generic OIDC OAuth 登录
	OIDCConnectEnabled                bool
	OIDCConnectProviderName           string
	OIDCConnectClientID               string
	OIDCConnectClientSecret           string
	OIDCConnectClientSecretConfigured bool
	OIDCConnectIssuerURL              string
	OIDCConnectDiscoveryURL           string
	OIDCConnectAuthorizeURL           string
	OIDCConnectTokenURL               string
	OIDCConnectUserInfoURL            string
	OIDCConnectJWKSURL                string
	OIDCConnectScopes                 string
	OIDCConnectRedirectURL            string
	OIDCConnectFrontendRedirectURL    string
	OIDCConnectTokenAuthMethod        string
	OIDCConnectUsePKCE                bool
	OIDCConnectValidateIDToken        bool
	OIDCConnectAllowedSigningAlgs     string
	OIDCConnectClockSkewSeconds       int
	OIDCConnectRequireEmailVerified   bool
	OIDCConnectUserInfoEmailPath      string
	OIDCConnectUserInfoIDPath         string
	OIDCConnectUserInfoUsernamePath   string

	// GitHub / Google 邮箱快捷登录
	GitHubOAuthEnabled                bool
	GitHubOAuthClientID               string
	GitHubOAuthClientSecret           string
	GitHubOAuthClientSecretConfigured bool
	GitHubOAuthRedirectURL            string
	GitHubOAuthFrontendRedirectURL    string
	GoogleOAuthEnabled                bool
	GoogleOneTapEnabled               bool
	GoogleOAuthClientID               string
	GoogleOAuthClientSecret           string
	GoogleOAuthClientSecretConfigured bool
	GoogleOAuthRedirectURL            string
	GoogleOAuthFrontendRedirectURL    string

	SiteName                    string
	SiteLogo                    string
	SiteSubtitle                string
	SiteNameZh                  string
	SiteNameEn                  string
	SiteTitleZh                 string
	SiteTitleEn                 string
	SiteSubtitleZh              string
	SiteSubtitleEn              string
	APIBaseURL                  string
	ContactInfo                 string
	DocURL                      string
	HomeContent                 string
	HideCcsImportButton         bool
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
	// CreativeModelSettings 是创作台允许使用的全局分组+模型+能力白名单 JSON。
	CreativeModelSettings []creative.CreativeModelSetting
	// CreativeWorkerCount 是创作台任务 worker 数量，缺失时回退默认值。
	CreativeWorkerCount int

	DefaultConcurrency int
	DefaultBalance     float64
	// TeamEnabled 控制团队功能页面的入口与访问。
	TeamEnabled bool
	// CreativeEnabled 控制创作台页面入口与 API 访问（进程配置 creative.enabled 仍为前置条件）。
	CreativeEnabled bool
	// RiskControlEnabled 控制风控中心入口和网关内容审计总开关。
	RiskControlEnabled                   bool
	CyberSessionBlockEnabled             bool
	CyberSessionBlockTTLSeconds          int
	AffiliateEnabled                     bool
	AffiliateRebateRate                  float64
	AffiliateRebateFreezeHours           int
	AffiliateRebateDurationDays          int
	AffiliateRebatePerInviteeCap         float64
	AdminRechargeRebateEnabled           bool
	DefaultUserRPMLimit                  int
	DefaultUserAPIKeyLimit               int
	DefaultSubscriptions                 []billing.DefaultSubscriptionSetting
	BalanceUnitName                      string
	BalanceUnitSymbol                    string
	BalanceIconSVG                       string
	ReasoningPointRMBUnitPrice           float64
	USDExchangeRate                      float64
	MarketplaceAvailabilityWindowDays    int
	MarketplaceAvailabilityBucketMinutes int

	// Model fallback configuration
	EnableModelFallback      bool   `json:"enable_model_fallback"`
	FallbackModelAnthropic   string `json:"fallback_model_anthropic"`
	FallbackModelOpenAI      string `json:"fallback_model_openai"`
	FallbackModelGemini      string `json:"fallback_model_gemini"`
	FallbackModelAntigravity string `json:"fallback_model_antigravity"`

	// Identity patch configuration (Claude -> Gemini)
	EnableIdentityPatch bool   `json:"enable_identity_patch"`
	IdentityPatchPrompt string `json:"identity_patch_prompt"`

	// Grok 模型映射策略；账号映射为空时使用这里的默认值。
	GrokDefaultTextModel           string `json:"grok_default_text_model"`
	GrokCrossClientModelMapEnabled bool   `json:"grok_cross_client_model_map_enabled"`
	GrokDefaultBaseURLMode         string `json:"grok_default_base_url_mode"`

	// Ops monitoring (vNext)
	OpsMonitoringEnabled         bool
	OpsRealtimeMonitoringEnabled bool
	OpsMetricsIntervalSeconds    int

	// Claude Code version check
	MinClaudeCodeVersion string
	MaxClaudeCodeVersion string

	// 分组隔离：允许未分组 Key 调度（默认 false → 403）
	AllowUngroupedKeyScheduling bool

	// Backend 模式：禁用用户注册和自助服务，仅管理员可登录
	BackendModeEnabled bool

	// Gateway forwarding behavior
	OpenAITTFTMode                         string // Responses first_token_ms 统计口径（默认 semantic）
	EnableFingerprintUnification           bool   // 是否统一 OAuth 账号的指纹头（默认 true）
	EnableMetadataPassthrough              bool   // 是否透传客户端原始 metadata（默认 false）
	EnableCCHSigning                       bool   // 已废弃 no-op：新版 CLI 取消 cch 签名后网关不再注入/签名 cch，开关无效果
	EnableClaudeOAuthSystemPromptInjection bool   // 是否对 Claude OAuth mimic 路径注入 Claude Code system blocks（默认 true）
	ClaudeOAuthSystemPrompt                string // Claude OAuth mimic 路径注入的通用扩展 system prompt；空值使用内置默认
	ClaudeOAuthSystemPromptBlocks          string // Claude OAuth mimic 路径注入的 system blocks JSON 配置；空值使用内置默认
	EnableAnthropicCacheTTL1hInjection     bool   // 是否对 Anthropic OAuth/SetupToken 请求体注入 1h cache_control ttl（默认 false）
	EnableClientDatelineNormalization      bool   // 是否对 Anthropic OAuth/SetupToken 请求体做客户端 dateline 归一化（默认 true）
	RewriteMessageCacheControl             bool   // 是否改写 messages[*].content[*].cache_control（默认 false）
	AntigravityUserAgentVersion            string // Antigravity 上游 User-Agent 版本号；空值使用配置/默认值
	OpenAICodexUserAgent                   string // OpenAI Codex 上游完整 User-Agent；空值使用内置 TUI 默认
	OpenAIAllowClaudeCodeCodexPlugin       bool   // 全局开关：是否额外放行 Claude Code 的 Codex 插件（默认 false）
	UserPromptReplacementConfig            *promptpolicy.UserPromptReplacementConfig

	// Web Search Emulation
	WebSearchEmulationEnabled bool // 是否启用 web search 模拟

	// Payment visible method routing
	PaymentVisibleMethodAlipaySource  string
	PaymentVisibleMethodWxpaySource   string
	PaymentVisibleMethodAlipayEnabled bool
	PaymentVisibleMethodWxpayEnabled  bool

	// 通用高级调度器参数；是否启用由分组 scheduler_type 决定。
	AdvancedSchedulerStickyWeightedEnabled       bool
	AdvancedSchedulerSubscriptionPriorityEnabled bool
	AdvancedSchedulerEWMAErrorRateAlpha          string
	AdvancedSchedulerEWMATTFTAlpha               string
	AdvancedSchedulerStickyEscapeEnabled         bool
	// AdvancedSchedulerStickyEscapeEnabledSet 标记开关是否由数据库显式设置，用于诊断继承来源。
	AdvancedSchedulerStickyEscapeEnabledSet          bool
	AdvancedSchedulerStickyEscapeTTFTMs              string
	AdvancedSchedulerStickyEscapeErrorRate           string
	AdvancedSchedulerLBTopK                          string
	AdvancedSchedulerWeightPriority                  string
	AdvancedSchedulerWeightLoad                      string
	AdvancedSchedulerWeightQueue                     string
	AdvancedSchedulerWeightErrorRate                 string
	AdvancedSchedulerWeightTTFT                      string
	AdvancedSchedulerWeightReset                     string
	AdvancedSchedulerWeightQuotaHeadroom             string
	AdvancedSchedulerWeightPreviousResponse          string
	AdvancedSchedulerWeightSessionSticky             string
	AdvancedSchedulerEffectiveLBTopK                 string
	AdvancedSchedulerEffectiveWeightPriority         string
	AdvancedSchedulerEffectiveWeightLoad             string
	AdvancedSchedulerEffectiveWeightQueue            string
	AdvancedSchedulerEffectiveWeightErrorRate        string
	AdvancedSchedulerEffectiveWeightTTFT             string
	AdvancedSchedulerEffectiveWeightReset            string
	AdvancedSchedulerEffectiveWeightQuotaHeadroom    string
	AdvancedSchedulerEffectiveWeightPreviousResponse string
	AdvancedSchedulerEffectiveWeightSessionSticky    string
	AdvancedSchedulerEffectiveEWMAErrorRateAlpha     string
	AdvancedSchedulerEffectiveEWMATTFTAlpha          string
	AdvancedSchedulerEffectiveStickyEscapeEnabled    bool
	AdvancedSchedulerEffectiveStickyEscapeTTFTMs     string
	AdvancedSchedulerEffectiveStickyEscapeErrorRate  string
	// OpenAIQuotaAutoPauseSettings 是 OpenAI 账号配额自动暂停的全局默认阈值，存储在 ops_advanced_settings 中。
	OpenAIQuotaAutoPauseSettings account.QuotaAutoPauseSettings
	// OpenAIQuotaAutoPauseSettingsSet 标记本次系统设置更新是否显式带了配额自动暂停配置，避免旧客户端误覆盖。
	OpenAIQuotaAutoPauseSettingsSet bool

	// 余额不足提醒
	BalanceLowNotifyEnabled     bool
	BalanceLowNotifyThreshold   float64
	BalanceLowNotifyRechargeURL string

	// 订阅到期提醒
	SubscriptionExpiryNotifyEnabled bool

	// 账号限额通知
	AccountQuotaNotifyEnabled bool
	AccountQuotaNotifyEmails  []contact.Entry

	// 系统全局默认平台配额（key = platform，nil/缺省 = 不限制）
	DefaultPlatformQuotas map[string]*billing.DefaultPlatformQuotaSetting `json:"default_platform_quotas"`

	// 系统全局账号自动停调阈值（key = platform，100 = disabled）
	AccountSchedulingThresholds map[string]int `json:"account_scheduling_thresholds"`

	// 允许终端用户在用量页查看自己的失败请求
	AllowUserViewErrorRequests bool
}
