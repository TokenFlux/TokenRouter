package service

import (
	"github.com/TokenFlux/TokenRouter/internal/creative"
	"github.com/TokenFlux/TokenRouter/internal/gateway"
	"github.com/TokenFlux/TokenRouter/internal/moderation"
	"github.com/TokenFlux/TokenRouter/internal/routing"
	"github.com/TokenFlux/TokenRouter/internal/server/runtimeconfig"
	"github.com/TokenFlux/TokenRouter/internal/site"
	"github.com/TokenFlux/TokenRouter/internal/team"

	"github.com/TokenFlux/TokenRouter/internal/audit"
	"github.com/TokenFlux/TokenRouter/internal/identity"
	"github.com/TokenFlux/TokenRouter/internal/usage"

	"github.com/TokenFlux/TokenRouter/internal/gateway/promptpolicy"
	"github.com/TokenFlux/TokenRouter/internal/ops"
	"github.com/TokenFlux/TokenRouter/internal/promotion"

	accountcore "github.com/TokenFlux/TokenRouter/internal/account"
	"github.com/TokenFlux/TokenRouter/internal/billing"
	"github.com/TokenFlux/TokenRouter/internal/domain"
	"github.com/TokenFlux/TokenRouter/internal/scheduler"
)

// Status constants
const (
	StatusActive   = domain.StatusActive
	StatusDisabled = domain.StatusDisabled
	StatusError    = domain.StatusError
	StatusUnused   = domain.StatusUnused
	StatusUsed     = domain.StatusUsed
	StatusExpired  = domain.StatusExpired
)

// Role constants
const (
	RoleAdmin = domain.RoleAdmin
	RoleUser  = domain.RoleUser
)

const (
	// DefaultUserAPIKeyLimit 是新用户 API Key 数量上限的内置默认值。
	DefaultUserAPIKeyLimit = domain.DefaultUserAPIKeyLimit
	// MaxUserAPIKeyLimit 是数据库能够保存的用户 API Key 数量上限最大值。
	MaxUserAPIKeyLimit = domain.MaxUserAPIKeyLimit
)

// Affiliate rebate settings
const (
	AffiliateRebateRateDefault          = promotion.AffiliateRebateRateDefault
	AffiliateRebateRateMin              = promotion.AffiliateRebateRateMin
	AffiliateRebateRateMax              = promotion.AffiliateRebateRateMax
	AffiliateEnabledDefault             = promotion.AffiliateEnabledDefault
	AffiliateRebateFreezeHoursDefault   = promotion.AffiliateRebateFreezeHoursDefault
	AffiliateRebateFreezeHoursMax       = promotion.AffiliateRebateFreezeHoursMax
	AffiliateRebateDurationDaysDefault  = promotion.AffiliateRebateDurationDaysDefault
	AffiliateRebateDurationDaysMax      = promotion.AffiliateRebateDurationDaysMax
	AffiliateRebatePerInviteeCapDefault = promotion.AffiliateRebatePerInviteeCapDefault
	AdminRechargeRebateEnabledDefault   = promotion.AdminRechargeRebateEnabledDefault
)

// Platform constants
const (
	PlatformAnthropic   = domain.PlatformAnthropic
	PlatformOpenAI      = domain.PlatformOpenAI
	PlatformGemini      = domain.PlatformGemini
	PlatformAntigravity = domain.PlatformAntigravity
	PlatformQoder       = domain.PlatformQoder
	PlatformGrok        = domain.PlatformGrok
	PlatformKimi        = domain.PlatformKimi
	PlatformZhipu       = domain.PlatformZhipu
	PlatformDeepseek    = domain.PlatformDeepseek
)

// 账号接入模式（国产供应商）：按量付费 vs Coding Plan。
const (
	AccountModePayG   = domain.AccountModePayG
	AccountModeCoding = domain.AccountModeCoding
)

// 上游 API 协议（国产供应商）：决定转发端点与格式，与接入模式正交。
const (
	APIProtocolChatCompletions = domain.APIProtocolChatCompletions
	APIProtocolAnthropic       = domain.APIProtocolAnthropic
	APIProtocolResponses       = domain.APIProtocolResponses
	APIProtocolAdaptive        = domain.APIProtocolAdaptive
)

// 国产 OpenAI 兼容供应商各模式的默认 base_url。
// 与前端 credentialsBuilder.ts 中的预设保持一致。
const (
	DefaultKimiPayGBaseURL    = accountcore.DefaultKimiPayGBaseURL
	DefaultKimiCodingBaseURL  = accountcore.DefaultKimiCodingBaseURL
	DefaultZhipuPayGBaseURL   = accountcore.DefaultZhipuPayGBaseURL
	DefaultZhipuCodingBaseURL = accountcore.DefaultZhipuCodingBaseURL
	DefaultDeepseekBaseURL    = accountcore.DefaultDeepseekBaseURL
)

// 国产供应商 Anthropic 协议端点的默认 base_url（上游路径为 {base}/v1/messages）。
// 与前端 credentialsBuilder.ts 中的预设保持一致。
const (
	DefaultKimiPayGAnthropicBaseURL   = accountcore.DefaultKimiPayGAnthropicBaseURL
	DefaultKimiCodingAnthropicBaseURL = accountcore.DefaultKimiCodingAnthropicBaseURL
	DefaultZhipuAnthropicBaseURL      = accountcore.DefaultZhipuAnthropicBaseURL
	DefaultDeepseekAnthropicBaseURL   = accountcore.DefaultDeepseekAnthropicBaseURL
)

// IsCNProvider 报告 platform 是否为国产 OpenAI 兼容供应商（kimi/zhipu/deepseek）。
func IsCNProvider(platform string) bool { return accountcore.IsCNProvider(platform) }

// AllowedQuotaPlatforms 委托唯一平台额度目录。
var AllowedQuotaPlatforms = billing.AllowedQuotaPlatforms

// AllowedSchedulingThresholdPlatforms 保留设置入口的同一允许集合。
var AllowedSchedulingThresholdPlatforms = accountcore.AllowedSchedulingThresholdPlatforms

func IsAllowedQuotaPlatform(s string) bool { return billing.IsAllowedQuotaPlatform(s) }

// Account type constants
const (
	AccountTypeOAuth          = domain.AccountTypeOAuth          // OAuth类型账号（full scope: profile + inference）
	AccountTypeSetupToken     = domain.AccountTypeSetupToken     // Setup Token类型账号（inference only scope）
	AccountTypeAPIKey         = domain.AccountTypeAPIKey         // API Key类型账号
	AccountTypeUpstream       = domain.AccountTypeUpstream       // 上游透传类型账号（通过 Base URL + API Key 连接上游）
	AccountTypeBedrock        = domain.AccountTypeBedrock        // AWS Bedrock 类型账号（通过 SigV4 签名或 API Key 连接 Bedrock，由 credentials.auth_mode 区分）
	AccountTypeServiceAccount = domain.AccountTypeServiceAccount // Google Service Account 类型账号（用于 Vertex AI）
	AccountTypeCosy           = domain.AccountTypeCosy           // Qoder COSY 协议账号
)

// Redeem type constants
const (
	RedeemTypeBalance          = domain.RedeemTypeBalance
	RedeemTypeConcurrency      = domain.RedeemTypeConcurrency
	RedeemTypeSubscription     = domain.RedeemTypeSubscription
	RedeemTypeInvitation       = domain.RedeemTypeInvitation
	RedeemTypeAffiliateBalance = "affiliate_balance"
)

// PromoCode status constants
const (
	PromoCodeStatusActive   = domain.PromoCodeStatusActive
	PromoCodeStatusDisabled = domain.PromoCodeStatusDisabled
)

// Admin adjustment type constants
const (
	AdjustmentTypeAdminBalance     = domain.AdjustmentTypeAdminBalance     // 管理员调整余额
	AdjustmentTypeAdminConcurrency = domain.AdjustmentTypeAdminConcurrency // 管理员调整并发数
)

// Group subscription type constants
const (
	SubscriptionTypeStandard     = domain.SubscriptionTypeStandard     // 标准计费模式（按余额扣费）
	SubscriptionTypeSubscription = domain.SubscriptionTypeSubscription // 订阅模式（按限额控制）
)

// Subscription status constants
const (
	SubscriptionStatusActive    = domain.SubscriptionStatusActive
	SubscriptionStatusPending   = domain.SubscriptionStatusPending
	SubscriptionStatusExpired   = domain.SubscriptionStatusExpired
	SubscriptionStatusSuspended = domain.SubscriptionStatusSuspended
	// SubscriptionStatusRevoked 是软删除订阅的 API 展示态，不写入 status 字段。
	SubscriptionStatusRevoked = "revoked"
)

// LinuxDoConnectSyntheticEmailDomain 是 LinuxDo Connect 用户的合成邮箱后缀（RFC 保留域名）。
const LinuxDoConnectSyntheticEmailDomain = "@linuxdo-connect.invalid"

// OIDCConnectSyntheticEmailDomain 是 OIDC 用户的合成邮箱后缀（RFC 保留域名）。
const OIDCConnectSyntheticEmailDomain = "@oidc-connect.invalid"

// WeChatConnectSyntheticEmailDomain 是 WeChat Connect 用户的合成邮箱后缀（RFC 保留域名）。
const WeChatConnectSyntheticEmailDomain = "@wechat-connect.invalid"

// DingTalkConnectSyntheticEmailDomain 是 DingTalk Connect 用户的合成邮箱后缀（RFC 保留域名）。
const DingTalkConnectSyntheticEmailDomain = "@dingtalk-connect.invalid"

// Setting keys
const (
	// 注册设置
	SettingKeyRegistrationEnabled              = identity.SettingKeyRegistrationEnabled              // 是否开放注册
	SettingKeyEmailVerifyEnabled               = identity.SettingKeyEmailVerifyEnabled               // 是否开启邮件验证
	SettingKeyRegistrationEmailSuffixWhitelist = identity.SettingKeyRegistrationEmailSuffixWhitelist // 注册邮箱后缀白名单（JSON 数组）
	SettingKeyRegistrationEmailNormalization   = identity.SettingKeyRegistrationEmailNormalization   // 注册邮箱地址归一化唯一性开关
	// 白名单非空时，是否放行非白名单域名按主域名限量注册；默认关闭并严格执行白名单。
	SettingKeyRegistrationEmailDomainQuotaEnabled = identity.SettingKeyRegistrationEmailDomainQuotaEnabled
	SettingKeyUserEmailChangeEnabled              = identity.SettingKeyUserEmailChangeEnabled         // 是否允许已有邮箱身份的用户换绑主邮箱
	SettingKeyPromoCodeEnabled                    = promotion.SettingKeyPromoCodeEnabled              // 是否启用优惠码功能
	SettingKeyPasswordResetEnabled                = identity.SettingKeyPasswordResetEnabled           // 是否启用忘记密码功能（需要先开启邮件验证）
	SettingKeyFrontendURL                         = site.SettingKeyFrontendURL                        // 前端基础URL，用于生成密码重置、团队邀请等邮件外部链接
	SettingKeyInvitationCodeEnabled               = promotion.SettingKeyInvitationCodeEnabled         // 是否启用邀请码注册
	SettingKeyAffiliateEnabled                    = promotion.SettingKeyAffiliateEnabled              // 邀请返利功能总开关
	SettingKeyAffiliateRebateRate                 = promotion.SettingKeyAffiliateRebateRate           // 邀请返利比例（百分比）
	SettingKeyAffiliateRebateFreezeHours          = promotion.SettingKeyAffiliateRebateFreezeHours    // 返利冻结期（小时，0=不冻结）
	SettingKeyAffiliateRebateDurationDays         = promotion.SettingKeyAffiliateRebateDurationDays   // 返利有效期（天，0=永久）
	SettingKeyAffiliateRebatePerInviteeCap        = promotion.SettingKeyAffiliateRebatePerInviteeCap  // 单个被邀请人的累计返利积分上限（0=无上限）
	SettingKeyAffiliateAdminRechargeEnabled       = promotion.SettingKeyAffiliateAdminRechargeEnabled // 管理员充值是否产生返利
	SettingKeyTeamEnabled                         = team.SettingKeyTeamEnabled                        // 是否显示团队功能相关页面
	SettingKeyCreativeEnabled                     = creative.SettingKeyCreativeEnabled                // 创作台功能开关
	SettingKeyCreativeModelSettings               = creative.SettingKeyCreativeModelSettings          // 创作台生图模型与能力白名单（JSON）
	SettingKeyCreativeWorkerCount                 = creative.SettingKeyCreativeWorkerCount            // 创作台 worker 数量（正整数）
	SettingKeyRiskControlEnabled                  = moderation.SettingKeyRiskControlEnabled           // 是否启用风控中心入口与内容审计链路
	SettingKeyCyberSessionBlockEnabled            = moderation.SettingKeyCyberSessionBlockEnabled     // cyber_policy 命中后的会话本地屏蔽开关
	SettingKeyCyberSessionBlockTTLSeconds         = moderation.SettingKeyCyberSessionBlockTTLSeconds  // cyber_policy 会话本地屏蔽时长（秒）
	SettingKeyContentModerationConfig             = "content_moderation_config"                       // 内容审计配置（JSON）
	SettingKeyLoginAgreementEnabled               = site.SettingKeyLoginAgreementEnabled              // 登录前是否要求同意条款
	SettingKeyLoginAgreementMode                  = site.SettingKeyLoginAgreementMode                 // 条款确认展示模式：modal / checkbox
	SettingKeyLoginAgreementUpdatedAt             = site.SettingKeyLoginAgreementUpdatedAt            // 条款更新日期（展示用）
	SettingKeyLoginAgreementDocuments             = site.SettingKeyLoginAgreementDocuments            // 条款文档列表（JSON，Markdown 内容）

	// 邮件服务设置
	SettingKeySMTPHost     = "smtp_host"      // SMTP服务器地址
	SettingKeySMTPPort     = "smtp_port"      // SMTP端口
	SettingKeySMTPUsername = "smtp_username"  // SMTP用户名
	SettingKeySMTPPassword = "smtp_password"  // SMTP密码（加密存储）
	SettingKeySMTPFrom     = "smtp_from"      // 发件人地址
	SettingKeySMTPFromName = "smtp_from_name" // 发件人名称
	SettingKeySMTPUseTLS   = "smtp_use_tls"   // 是否使用TLS

	// Cloudflare Turnstile 设置
	SettingKeyTurnstileEnabled   = identity.SettingKeyTurnstileEnabled   // 是否启用 Turnstile 验证
	SettingKeyTurnstileSiteKey   = identity.SettingKeyTurnstileSiteKey   // Turnstile Site Key
	SettingKeyTurnstileSecretKey = identity.SettingKeyTurnstileSecretKey // Turnstile Secret Key

	// 腾讯天御验证码设置
	SettingKeyTencentCaptchaEnabled        = identity.SettingKeyTencentCaptchaEnabled
	SettingKeyTencentCaptchaAppID          = identity.SettingKeyTencentCaptchaAppID
	SettingKeyTencentCaptchaAppSecretKey   = identity.SettingKeyTencentCaptchaAppSecretKey
	SettingKeyTencentCaptchaCloudSecretID  = identity.SettingKeyTencentCaptchaCloudSecretID
	SettingKeyTencentCaptchaCloudSecretKey = identity.SettingKeyTencentCaptchaCloudSecretKey
	SettingKeyTencentCaptchaRegion         = identity.SettingKeyTencentCaptchaRegion // 站点："cn"|"intl"，决定前端 SDK 脚本与服务端接入点

	// 阿里云验证码 2.0 设置（与 Turnstile、腾讯天御互斥，同一时间仅可启用一家）
	SettingKeyAliyunCaptchaEnabled         = identity.SettingKeyAliyunCaptchaEnabled         // 是否启用阿里云验证码
	SettingKeyAliyunCaptchaAccessKeyID     = identity.SettingKeyAliyunCaptchaAccessKeyID     // 阿里云 AccessKey ID
	SettingKeyAliyunCaptchaAccessKeySecret = identity.SettingKeyAliyunCaptchaAccessKeySecret // 阿里云 AccessKey Secret
	SettingKeyAliyunCaptchaSceneID         = identity.SettingKeyAliyunCaptchaSceneID         // 验证场景 ID（所有认证流程共用）
	SettingKeyAliyunCaptchaPrefix          = identity.SettingKeyAliyunCaptchaPrefix          // 身份标，前端 SDK 初始化用
	SettingKeyAliyunCaptchaRegion          = identity.SettingKeyAliyunCaptchaRegion          // 地域："cn"|"sgp"，决定前端脚本区域与服务端接入点

	// API Key IP 访问控制设置
	SettingKeyAPIKeyACLTrustForwardedIP = runtimeconfig.SettingKeyAPIKeyACLTrustForwardedIP // API Key IP 白/黑名单是否信任转发 IP
	SettingKeyForwardedClientIPHeaders  = runtimeconfig.SettingKeyForwardedClientIPHeaders  // 自定义 CDN 客户端 IP 请求头（JSON 数组）
	settingKeyForwardedClientIPModeV2   = runtimeconfig.SettingKeyForwardedClientIPModeV2

	// TOTP 双因素认证设置
	SettingKeyTotpEnabled = identity.SettingKeyTotpEnabled // 是否启用 TOTP 2FA 功能

	// 会话安全设置
	SettingKeySessionBindingEnabled = identity.SettingKeySessionBindingEnabled // 会话 IP/UA 绑定（变更即失效），默认关闭

	// 敏感操作 step-up 2FA 设置
	SettingKeyStepUpEnabled = identity.SettingKeyStepUpEnabled // 敏感操作（导出/备份/S3配置/提升管理员等）要求 step-up 2FA，默认关闭

	// 面板 API 限流设置（JSON：PanelRateLimitSettings）
	SettingKeyPanelRateLimitSettings = "panel_rate_limit_settings"

	// 操作审计日志设置
	SettingKeyAuditLogRetentionDays = audit.SettingKeyAuditLogRetentionDays // 审计日志保留天数（<=0 永久保留），默认 180

	// LinuxDo Connect OAuth 登录设置
	SettingKeyLinuxDoConnectEnabled      = identity.SettingKeyLinuxDoConnectEnabled
	SettingKeyLinuxDoConnectClientID     = identity.SettingKeyLinuxDoConnectClientID
	SettingKeyLinuxDoConnectClientSecret = identity.SettingKeyLinuxDoConnectClientSecret
	SettingKeyLinuxDoConnectRedirectURL  = identity.SettingKeyLinuxDoConnectRedirectURL

	// DingTalk Connect OAuth 登录设置
	SettingKeyDingTalkConnectEnabled                 = identity.SettingKeyDingTalkConnectEnabled
	SettingKeyDingTalkConnectClientID                = identity.SettingKeyDingTalkConnectClientID
	SettingKeyDingTalkConnectClientSecret            = identity.SettingKeyDingTalkConnectClientSecret
	SettingKeyDingTalkConnectRedirectURL             = identity.SettingKeyDingTalkConnectRedirectURL
	SettingKeyDingTalkConnectCorpRestrictionPolicy   = identity.SettingKeyDingTalkConnectCorpRestrictionPolicy
	SettingKeyDingTalkConnectInternalCorpID          = identity.SettingKeyDingTalkConnectInternalCorpID
	SettingKeyDingTalkConnectBypassRegistration      = identity.SettingKeyDingTalkConnectBypassRegistration
	SettingKeyDingTalkConnectSyncCorpEmail           = identity.SettingKeyDingTalkConnectSyncCorpEmail
	SettingKeyDingTalkConnectSyncDisplayName         = identity.SettingKeyDingTalkConnectSyncDisplayName
	SettingKeyDingTalkConnectSyncDept                = identity.SettingKeyDingTalkConnectSyncDept
	SettingKeyDingTalkConnectSyncCorpEmailAttrKey    = identity.SettingKeyDingTalkConnectSyncCorpEmailAttrKey
	SettingKeyDingTalkConnectSyncDisplayNameAttrKey  = identity.SettingKeyDingTalkConnectSyncDisplayNameAttrKey
	SettingKeyDingTalkConnectSyncDeptAttrKey         = identity.SettingKeyDingTalkConnectSyncDeptAttrKey
	SettingKeyDingTalkConnectSyncCorpEmailAttrName   = identity.SettingKeyDingTalkConnectSyncCorpEmailAttrName
	SettingKeyDingTalkConnectSyncDisplayNameAttrName = identity.SettingKeyDingTalkConnectSyncDisplayNameAttrName
	SettingKeyDingTalkConnectSyncDeptAttrName        = identity.SettingKeyDingTalkConnectSyncDeptAttrName

	// WeChat Connect OAuth 登录设置
	SettingKeyWeChatConnectEnabled             = identity.SettingKeyWeChatConnectEnabled
	SettingKeyWeChatConnectAppID               = identity.SettingKeyWeChatConnectAppID
	SettingKeyWeChatConnectAppSecret           = identity.SettingKeyWeChatConnectAppSecret
	SettingKeyWeChatConnectOpenAppID           = identity.SettingKeyWeChatConnectOpenAppID
	SettingKeyWeChatConnectOpenAppSecret       = identity.SettingKeyWeChatConnectOpenAppSecret
	SettingKeyWeChatConnectMPAppID             = identity.SettingKeyWeChatConnectMPAppID
	SettingKeyWeChatConnectMPAppSecret         = identity.SettingKeyWeChatConnectMPAppSecret
	SettingKeyWeChatConnectMobileAppID         = identity.SettingKeyWeChatConnectMobileAppID
	SettingKeyWeChatConnectMobileAppSecret     = identity.SettingKeyWeChatConnectMobileAppSecret
	SettingKeyWeChatConnectOpenEnabled         = identity.SettingKeyWeChatConnectOpenEnabled
	SettingKeyWeChatConnectMPEnabled           = identity.SettingKeyWeChatConnectMPEnabled
	SettingKeyWeChatConnectMobileEnabled       = identity.SettingKeyWeChatConnectMobileEnabled
	SettingKeyWeChatConnectMode                = identity.SettingKeyWeChatConnectMode
	SettingKeyWeChatConnectScopes              = identity.SettingKeyWeChatConnectScopes
	SettingKeyWeChatConnectRedirectURL         = identity.SettingKeyWeChatConnectRedirectURL
	SettingKeyWeChatConnectFrontendRedirectURL = identity.SettingKeyWeChatConnectFrontendRedirectURL

	// Generic OIDC OAuth 登录设置
	SettingKeyOIDCConnectEnabled              = identity.SettingKeyOIDCConnectEnabled
	SettingKeyOIDCConnectProviderName         = identity.SettingKeyOIDCConnectProviderName
	SettingKeyOIDCConnectClientID             = identity.SettingKeyOIDCConnectClientID
	SettingKeyOIDCConnectClientSecret         = identity.SettingKeyOIDCConnectClientSecret
	SettingKeyOIDCConnectIssuerURL            = identity.SettingKeyOIDCConnectIssuerURL
	SettingKeyOIDCConnectDiscoveryURL         = identity.SettingKeyOIDCConnectDiscoveryURL
	SettingKeyOIDCConnectAuthorizeURL         = identity.SettingKeyOIDCConnectAuthorizeURL
	SettingKeyOIDCConnectTokenURL             = identity.SettingKeyOIDCConnectTokenURL
	SettingKeyOIDCConnectUserInfoURL          = identity.SettingKeyOIDCConnectUserInfoURL
	SettingKeyOIDCConnectJWKSURL              = identity.SettingKeyOIDCConnectJWKSURL
	SettingKeyOIDCConnectScopes               = identity.SettingKeyOIDCConnectScopes
	SettingKeyOIDCConnectRedirectURL          = identity.SettingKeyOIDCConnectRedirectURL
	SettingKeyOIDCConnectFrontendRedirectURL  = identity.SettingKeyOIDCConnectFrontendRedirectURL
	SettingKeyOIDCConnectTokenAuthMethod      = identity.SettingKeyOIDCConnectTokenAuthMethod
	SettingKeyOIDCConnectUsePKCE              = identity.SettingKeyOIDCConnectUsePKCE
	SettingKeyOIDCConnectValidateIDToken      = identity.SettingKeyOIDCConnectValidateIDToken
	SettingKeyOIDCConnectAllowedSigningAlgs   = identity.SettingKeyOIDCConnectAllowedSigningAlgs
	SettingKeyOIDCConnectClockSkewSeconds     = identity.SettingKeyOIDCConnectClockSkewSeconds
	SettingKeyOIDCConnectRequireEmailVerified = identity.SettingKeyOIDCConnectRequireEmailVerified
	SettingKeyOIDCConnectUserInfoEmailPath    = identity.SettingKeyOIDCConnectUserInfoEmailPath
	SettingKeyOIDCConnectUserInfoIDPath       = identity.SettingKeyOIDCConnectUserInfoIDPath
	SettingKeyOIDCConnectUserInfoUsernamePath = identity.SettingKeyOIDCConnectUserInfoUsernamePath

	// GitHub/Google 邮箱快捷登录设置
	SettingKeyGitHubOAuthEnabled             = identity.SettingKeyGitHubOAuthEnabled
	SettingKeyGitHubOAuthClientID            = identity.SettingKeyGitHubOAuthClientID
	SettingKeyGitHubOAuthClientSecret        = identity.SettingKeyGitHubOAuthClientSecret
	SettingKeyGitHubOAuthRedirectURL         = identity.SettingKeyGitHubOAuthRedirectURL
	SettingKeyGitHubOAuthFrontendRedirectURL = identity.SettingKeyGitHubOAuthFrontendRedirectURL
	SettingKeyGoogleOAuthEnabled             = identity.SettingKeyGoogleOAuthEnabled
	SettingKeyGoogleOneTapEnabled            = identity.SettingKeyGoogleOneTapEnabled
	SettingKeyGoogleOAuthClientID            = identity.SettingKeyGoogleOAuthClientID
	SettingKeyGoogleOAuthClientSecret        = identity.SettingKeyGoogleOAuthClientSecret
	SettingKeyGoogleOAuthRedirectURL         = identity.SettingKeyGoogleOAuthRedirectURL
	SettingKeyGoogleOAuthFrontendRedirectURL = identity.SettingKeyGoogleOAuthFrontendRedirectURL

	// OEM设置
	SettingKeySiteName                    = site.SettingKeySiteName                    // 网站名称
	SettingKeySiteLogo                    = site.SettingKeySiteLogo                    // 网站Logo (base64)
	SettingKeySiteSubtitle                = site.SettingKeySiteSubtitle                // 网站副标题
	SettingKeySiteNameZh                  = site.SettingKeySiteNameZh                  // 站点名称（中文）
	SettingKeySiteNameEn                  = site.SettingKeySiteNameEn                  // 站点名称（英文）
	SettingKeySiteTitleZh                 = site.SettingKeySiteTitleZh                 // 站点标题（中文）
	SettingKeySiteTitleEn                 = site.SettingKeySiteTitleEn                 // 站点标题（英文）
	SettingKeySiteSubtitleZh              = site.SettingKeySiteSubtitleZh              // 站点副标题（中文）
	SettingKeySiteSubtitleEn              = site.SettingKeySiteSubtitleEn              // 站点副标题（英文）
	SettingKeyAPIBaseURL                  = site.SettingKeyAPIBaseURL                  // API端点地址（用于客户端配置和导入）
	SettingKeyContactInfo                 = site.SettingKeyContactInfo                 // 客服联系方式
	SettingKeyDocURL                      = site.SettingKeyDocURL                      // 文档链接
	SettingKeyHomeContent                 = site.SettingKeyHomeContent                 // 首页内容（支持 Markdown/HTML，或 URL 作为 iframe src）
	SettingKeyHideCcsImportButton         = site.SettingKeyHideCcsImportButton         // 是否隐藏 API Keys 页面的导入 CCS 按钮
	SettingKeyPurchaseSubscriptionEnabled = site.SettingKeyPurchaseSubscriptionEnabled // 是否展示"购买订阅"页面入口
	SettingKeyPurchaseSubscriptionURL     = site.SettingKeyPurchaseSubscriptionURL     // "购买订阅"页面 URL（作为 iframe src）
	SettingKeyTableDefaultPageSize        = site.SettingKeyTableDefaultPageSize        // 表格默认每页条数
	SettingKeyTablePageSizeOptions        = site.SettingKeyTablePageSizeOptions        // 表格可选每页条数（JSON 数组）
	SettingKeyCustomMenuItems             = site.SettingKeyCustomMenuItems             // 自定义菜单项（JSON 数组）
	SettingKeyCustomEndpoints             = site.SettingKeyCustomEndpoints             // 自定义端点列表（JSON 数组）
	SettingKeyFooterLinks                 = site.SettingKeyFooterLinks                 // 首页底栏链接分组（JSON 数组）
	SettingKeyFooterText                  = site.SettingKeyFooterText                  // 首页底栏附加文本（备案号等，支持多行）
	SettingKeyHomeFeaturedModels          = site.SettingKeyHomeFeaturedModels          // 首页展示的模型 ID 列表（JSON 数组，按顺序展示）
)

const (
	// 用户侧用量排行设置
	SettingKeyUsageRankingLimit           = usage.SettingKeyUsageRankingLimit           // 用户侧用量排行显示名次上限
	SettingKeyUsageRankingEnabled         = usage.SettingKeyUsageRankingEnabled         // 用户侧用量排行是否启用
	SettingKeyUsageRankingSortBy          = usage.SettingKeyUsageRankingSortBy          // 用户侧用量排行排序依据
	SettingKeyUsageRankingShowTotalTokens = usage.SettingKeyUsageRankingShowTotalTokens // 用户侧用量排行是否显示总 Token
	SettingKeyUsageRankingShowRequests    = usage.SettingKeyUsageRankingShowRequests    // 用户侧用量排行是否显示请求数
	SettingKeyUsageRankingShowActualCost  = usage.SettingKeyUsageRankingShowActualCost  // 用户侧用量排行是否显示实际消费
)

const (
	// 默认配置
	SettingKeyDefaultConcurrency                   = identity.SettingKeyDefaultConcurrency                  // 新用户默认并发量
	SettingKeyDefaultBalance                       = billing.SettingKeyDefaultBalance                       // 新用户默认余额
	SettingKeyDefaultSubscriptions                 = billing.SettingKeyDefaultSubscriptions                 // 新用户默认订阅列表（JSON）
	SettingKeyDefaultUserRPMLimit                  = identity.SettingKeyDefaultUserRPMLimit                 // 新用户默认 RPM 限制（0 = 不限制）
	SettingKeyDefaultUserAPIKeyLimit               = identity.SettingKeyDefaultUserAPIKeyLimit              // 新用户默认 API Key 数量上限（0 = 不限制）
	SettingKeyBalanceUnitName                      = billing.SettingKeyBalanceUnitName                      // 内部余额展示名称
	SettingKeyBalanceUnitSymbol                    = billing.SettingKeyBalanceUnitSymbol                    // 内部余额展示符号
	SettingKeyBalanceIconSVG                       = billing.SettingKeyBalanceIconSVG                       // 内部余额展示 SVG 图标
	SettingKeyReasoningPointRMBUnitPrice           = billing.SettingKeyReasoningPointRMBUnitPrice           // 推理积分人民币单价
	SettingKeyUSDExchangeRate                      = billing.SettingKeyUSDExchangeRate                      // 美元兑人民币汇率（1 USD = N CNY）
	SettingKeyMarketplaceAvailabilityWindowDays    = routing.SettingKeyMarketplaceAvailabilityWindowDays    // 模型广场可用率展示天数
	SettingKeyMarketplaceAvailabilityBucketMinutes = routing.SettingKeyMarketplaceAvailabilityBucketMinutes // 模型广场可用率每根柱子的分钟数

	// 第三方认证来源默认授予配置
	SettingKeyAuthSourceDefaultEmailBalance             = identity.SettingKeyAuthSourceDefaultEmailBalance
	SettingKeyAuthSourceDefaultEmailConcurrency         = identity.SettingKeyAuthSourceDefaultEmailConcurrency
	SettingKeyAuthSourceDefaultEmailSubscriptions       = identity.SettingKeyAuthSourceDefaultEmailSubscriptions
	SettingKeyAuthSourceDefaultEmailGrantOnSignup       = identity.SettingKeyAuthSourceDefaultEmailGrantOnSignup
	SettingKeyAuthSourceDefaultEmailGrantOnFirstBind    = identity.SettingKeyAuthSourceDefaultEmailGrantOnFirstBind
	SettingKeyAuthSourceDefaultLinuxDoBalance           = identity.SettingKeyAuthSourceDefaultLinuxDoBalance
	SettingKeyAuthSourceDefaultLinuxDoConcurrency       = identity.SettingKeyAuthSourceDefaultLinuxDoConcurrency
	SettingKeyAuthSourceDefaultLinuxDoSubscriptions     = identity.SettingKeyAuthSourceDefaultLinuxDoSubscriptions
	SettingKeyAuthSourceDefaultLinuxDoGrantOnSignup     = identity.SettingKeyAuthSourceDefaultLinuxDoGrantOnSignup
	SettingKeyAuthSourceDefaultLinuxDoGrantOnFirstBind  = identity.SettingKeyAuthSourceDefaultLinuxDoGrantOnFirstBind
	SettingKeyAuthSourceDefaultOIDCBalance              = identity.SettingKeyAuthSourceDefaultOIDCBalance
	SettingKeyAuthSourceDefaultOIDCConcurrency          = identity.SettingKeyAuthSourceDefaultOIDCConcurrency
	SettingKeyAuthSourceDefaultOIDCSubscriptions        = identity.SettingKeyAuthSourceDefaultOIDCSubscriptions
	SettingKeyAuthSourceDefaultOIDCGrantOnSignup        = identity.SettingKeyAuthSourceDefaultOIDCGrantOnSignup
	SettingKeyAuthSourceDefaultOIDCGrantOnFirstBind     = identity.SettingKeyAuthSourceDefaultOIDCGrantOnFirstBind
	SettingKeyAuthSourceDefaultWeChatBalance            = identity.SettingKeyAuthSourceDefaultWeChatBalance
	SettingKeyAuthSourceDefaultWeChatConcurrency        = identity.SettingKeyAuthSourceDefaultWeChatConcurrency
	SettingKeyAuthSourceDefaultWeChatSubscriptions      = identity.SettingKeyAuthSourceDefaultWeChatSubscriptions
	SettingKeyAuthSourceDefaultWeChatGrantOnSignup      = identity.SettingKeyAuthSourceDefaultWeChatGrantOnSignup
	SettingKeyAuthSourceDefaultWeChatGrantOnFirstBind   = identity.SettingKeyAuthSourceDefaultWeChatGrantOnFirstBind
	SettingKeyAuthSourceDefaultGitHubBalance            = identity.SettingKeyAuthSourceDefaultGitHubBalance
	SettingKeyAuthSourceDefaultGitHubConcurrency        = identity.SettingKeyAuthSourceDefaultGitHubConcurrency
	SettingKeyAuthSourceDefaultGitHubSubscriptions      = identity.SettingKeyAuthSourceDefaultGitHubSubscriptions
	SettingKeyAuthSourceDefaultGitHubGrantOnSignup      = identity.SettingKeyAuthSourceDefaultGitHubGrantOnSignup
	SettingKeyAuthSourceDefaultGitHubGrantOnFirstBind   = identity.SettingKeyAuthSourceDefaultGitHubGrantOnFirstBind
	SettingKeyAuthSourceDefaultGoogleBalance            = identity.SettingKeyAuthSourceDefaultGoogleBalance
	SettingKeyAuthSourceDefaultGoogleConcurrency        = identity.SettingKeyAuthSourceDefaultGoogleConcurrency
	SettingKeyAuthSourceDefaultGoogleSubscriptions      = identity.SettingKeyAuthSourceDefaultGoogleSubscriptions
	SettingKeyAuthSourceDefaultGoogleGrantOnSignup      = identity.SettingKeyAuthSourceDefaultGoogleGrantOnSignup
	SettingKeyAuthSourceDefaultGoogleGrantOnFirstBind   = identity.SettingKeyAuthSourceDefaultGoogleGrantOnFirstBind
	SettingKeyAuthSourceDefaultDingTalkBalance          = identity.SettingKeyAuthSourceDefaultDingTalkBalance
	SettingKeyAuthSourceDefaultDingTalkConcurrency      = identity.SettingKeyAuthSourceDefaultDingTalkConcurrency
	SettingKeyAuthSourceDefaultDingTalkSubscriptions    = identity.SettingKeyAuthSourceDefaultDingTalkSubscriptions
	SettingKeyAuthSourceDefaultDingTalkGrantOnSignup    = identity.SettingKeyAuthSourceDefaultDingTalkGrantOnSignup
	SettingKeyAuthSourceDefaultDingTalkGrantOnFirstBind = identity.SettingKeyAuthSourceDefaultDingTalkGrantOnFirstBind
	SettingKeyForceEmailOnThirdPartySignup              = identity.SettingKeyForceEmailOnThirdPartySignup

	// 管理员 API Key
	SettingKeyAdminAPIKey = identity.SettingKeyAdminAPIKey // 全局管理员 API Key（用于外部系统集成）

	// Gemini 配额策略（JSON）
	SettingKeyGeminiQuotaPolicy = accountcore.GeminiQuotaPolicySettingKey

	// Model fallback settings
	SettingKeyEnableModelFallback      = routing.SettingKeyEnableModelFallback
	SettingKeyFallbackModelAnthropic   = routing.SettingKeyFallbackModelAnthropic
	SettingKeyFallbackModelOpenAI      = routing.SettingKeyFallbackModelOpenAI
	SettingKeyFallbackModelGemini      = routing.SettingKeyFallbackModelGemini
	SettingKeyFallbackModelAntigravity = routing.SettingKeyFallbackModelAntigravity

	// Request identity patch (Claude -> Gemini systemInstruction injection)
	SettingKeyEnableIdentityPatch = gateway.SettingKeyEnableIdentityPatch
	SettingKeyIdentityPatchPrompt = gateway.SettingKeyIdentityPatchPrompt

	// Grok 默认模型、跨客户端映射与文本上游区域策略。
	SettingKeyGrokDefaultTextModel           = gateway.SettingKeyGrokDefaultTextModel
	SettingKeyGrokCrossClientModelMapEnabled = gateway.SettingKeyGrokCrossClientModelMapEnabled
	SettingKeyGrokDefaultBaseURLMode         = gateway.SettingKeyGrokDefaultBaseURLMode

	// =========================
	// Ops Monitoring (vNext)
	// =========================

	// SettingKeyOpsMonitoringEnabled is a DB-backed soft switch to enable/disable ops module at runtime.
	SettingKeyOpsMonitoringEnabled = ops.SettingKeyOpsMonitoringEnabled

	// SettingKeyOpsRealtimeMonitoringEnabled controls realtime features (e.g. WS/QPS push).
	SettingKeyOpsRealtimeMonitoringEnabled = ops.SettingKeyOpsRealtimeMonitoringEnabled

	// SettingKeyPreAggregationSettings 保存用量与运维预聚合的统一运行时配置。
	SettingKeyPreAggregationSettings = "pre_aggregation_settings"

	// SettingKeyOpsEmailNotificationConfig stores JSON config for ops email notifications.
	SettingKeyOpsEmailNotificationConfig = ops.SettingKeyOpsEmailNotificationConfig

	// SettingKeyOpsAlertRuntimeSettings stores JSON config for ops alert evaluator runtime settings.
	SettingKeyOpsAlertRuntimeSettings = ops.SettingKeyOpsAlertRuntimeSettings

	// SettingKeyOpsMetricsIntervalSeconds controls the ops metrics collector interval (>=60).
	SettingKeyOpsMetricsIntervalSeconds = ops.SettingKeyOpsMetricsIntervalSeconds

	// SettingKeyOpsAdvancedSettings stores JSON config for ops advanced settings (data retention, aggregation).
	SettingKeyOpsAdvancedSettings = ops.SettingKeyOpsAdvancedSettings

	// SettingKeyOpsRuntimeLogConfig stores JSON config for runtime log settings.
	SettingKeyOpsRuntimeLogConfig = ops.SettingKeyOpsRuntimeLogConfig

	// SettingKeyOllamaCloudUsageSettings 保存可选的全局刷新开关和刷新周期。
	SettingKeyOllamaCloudUsageSettings = "ollama_cloud_usage_settings"

	// =========================
	// Overload Cooldown (529)
	// =========================

	// SettingKeyOverloadCooldownSettings stores JSON config for 529 overload cooldown handling.
	SettingKeyOverloadCooldownSettings = accountcore.SettingKeyOverloadCooldownSettings

	// SettingKeyOpenAI403CooldownSettings stores JSON config for OpenAI OAuth 403 cooldown handling.
	SettingKeyOpenAI403CooldownSettings = accountcore.SettingKeyOpenAI403CooldownSettings

	// SettingKeyRateLimit429CooldownSettings stores JSON config for 429 fallback cooldown handling.
	SettingKeyRateLimit429CooldownSettings = accountcore.SettingKeyRateLimit429CooldownSettings
	// SettingKeyOpenAIImagesOAuthUnavailableCooldownSettings stores the cooldown applied when the OAuth image tool is unavailable.
	SettingKeyOpenAIImagesOAuthUnavailableCooldownSettings = accountcore.SettingKeyOpenAIImagesOAuthUnavailableCooldownSettings
	// SettingKeyOpenAIAPIKeyHealthBreakerSettings stores the opt-in OpenAI pool API-key breaker config.
	SettingKeyOpenAIAPIKeyHealthBreakerSettings = accountcore.SettingKeyOpenAIAPIKeyHealthBreakerSettings

	// =========================
	// Stream Timeout Handling
	// =========================

	// SettingKeyStreamTimeoutSettings stores JSON config for stream timeout handling.
	SettingKeyStreamTimeoutSettings = accountcore.SettingKeyStreamTimeoutSettings

	// =========================
	// Request Rectifier (请求整流器)
	// =========================

	// SettingKeyRectifierSettings stores JSON config for rectifier settings (thinking signature + budget).
	SettingKeyRectifierSettings = gateway.SettingKeyRectifierSettings

	// =========================
	// Beta Policy Settings
	// =========================

	// SettingKeyBetaPolicySettings stores JSON config for beta policy rules.
	SettingKeyBetaPolicySettings = gateway.SettingKeyBetaPolicySettings

	// SettingKeyOpenAIFastPolicySettings stores JSON config for OpenAI
	// service_tier (fast/flex) policy rules. Mirrors BetaPolicySettings but
	// targets OpenAI's body-level service_tier field instead of Claude's
	// anthropic-beta header.
	SettingKeyOpenAIFastPolicySettings = gateway.SettingKeyOpenAIFastPolicySettings

	// SettingKeyOpenAIOAuthImportDefaults 保存 OpenAI OAuth 账号导入时的缺省模板。
	SettingKeyOpenAIOAuthImportDefaults = accountcore.SettingKeyOpenAIOAuthImportDefaults

	// =========================
	// Claude Code Version Check
	// =========================

	// SettingKeyMinClaudeCodeVersion 最低 Claude Code 版本号要求 (semver, 如 "2.1.0"，空值=不检查)
	SettingKeyMinClaudeCodeVersion = gateway.SettingKeyMinClaudeCodeVersion

	// SettingKeyMaxClaudeCodeVersion 最高 Claude Code 版本号限制 (semver, 如 "3.0.0"，空值=不检查)
	SettingKeyMaxClaudeCodeVersion = gateway.SettingKeyMaxClaudeCodeVersion

	// SettingKeyAllowUngroupedKeyScheduling 允许未分组 API Key 调度（默认 false：未分组 Key 返回 403）
	SettingKeyAllowUngroupedKeyScheduling = routing.SettingKeyAllowUngroupedKeyScheduling
	// SettingKeyAdvancedSchedulerStickyWeightedEnabled 控制高级调度器的粘性加权。
	SettingKeyAdvancedSchedulerStickyWeightedEnabled = scheduler.SettingKeyAdvancedSchedulerStickyWeightedEnabled
	// SettingKeyAdvancedSchedulerSubscriptionPriorityEnabled 控制可用时的订阅账号优先级。
	SettingKeyAdvancedSchedulerSubscriptionPriorityEnabled = scheduler.SettingKeyAdvancedSchedulerSubscriptionPriorityEnabled
	// SettingKeyAdvancedSchedulerEWMAErrorRateAlpha 控制错误率 EWMA 的平滑系数。
	SettingKeyAdvancedSchedulerEWMAErrorRateAlpha = scheduler.SettingKeyAdvancedSchedulerEWMAErrorRateAlpha
	// SettingKeyAdvancedSchedulerEWMATTFTAlpha 控制首 token 延迟 EWMA 的平滑系数。
	SettingKeyAdvancedSchedulerEWMATTFTAlpha = scheduler.SettingKeyAdvancedSchedulerEWMATTFTAlpha
	// SettingKeyAdvancedSchedulerStickyEscapeEnabled 控制健康度恶化时是否允许逃逸粘性账号。
	SettingKeyAdvancedSchedulerStickyEscapeEnabled = scheduler.SettingKeyAdvancedSchedulerStickyEscapeEnabled
	// SettingKeyAdvancedSchedulerStickyEscapeTTFTMs 控制触发粘性逃逸的 TTFT 阈值。
	SettingKeyAdvancedSchedulerStickyEscapeTTFTMs = scheduler.SettingKeyAdvancedSchedulerStickyEscapeTTFTMs
	// SettingKeyAdvancedSchedulerStickyEscapeErrorRate 控制触发粘性逃逸的错误率阈值。
	SettingKeyAdvancedSchedulerStickyEscapeErrorRate  = scheduler.SettingKeyAdvancedSchedulerStickyEscapeErrorRate
	SettingKeyAdvancedSchedulerLBTopK                 = scheduler.SettingKeyAdvancedSchedulerLBTopK
	SettingKeyAdvancedSchedulerWeightPriority         = scheduler.SettingKeyAdvancedSchedulerWeightPriority
	SettingKeyAdvancedSchedulerWeightLoad             = scheduler.SettingKeyAdvancedSchedulerWeightLoad
	SettingKeyAdvancedSchedulerWeightQueue            = scheduler.SettingKeyAdvancedSchedulerWeightQueue
	SettingKeyAdvancedSchedulerWeightErrorRate        = scheduler.SettingKeyAdvancedSchedulerWeightErrorRate
	SettingKeyAdvancedSchedulerWeightTTFT             = scheduler.SettingKeyAdvancedSchedulerWeightTTFT
	SettingKeyAdvancedSchedulerWeightReset            = scheduler.SettingKeyAdvancedSchedulerWeightReset
	SettingKeyAdvancedSchedulerWeightQuotaHeadroom    = scheduler.SettingKeyAdvancedSchedulerWeightQuotaHeadroom
	SettingKeyAdvancedSchedulerWeightPreviousResponse = scheduler.SettingKeyAdvancedSchedulerWeightPreviousResponse
	SettingKeyAdvancedSchedulerWeightSessionSticky    = scheduler.SettingKeyAdvancedSchedulerWeightSessionSticky

	// SettingKeyBackendModeEnabled Backend 模式：禁用用户注册和自助服务，仅管理员可登录
	SettingKeyBackendModeEnabled = gateway.SettingKeyBackendModeEnabled

	// Gateway Forwarding Behavior
	// SettingKeyOpenAITTFTMode 控制 Responses first_token_ms 的统计口径。
	SettingKeyOpenAITTFTMode = gateway.SettingKeyOpenAITTFTMode
	OpenAITTFTModeSemantic   = gateway.OpenAITTFTModeSemantic
	OpenAITTFTModeVisible    = gateway.OpenAITTFTModeVisible
	// SettingKeyEnableFingerprintUnification 是否统一 OAuth 账号的 X-Stainless-* 指纹头（默认 true）
	SettingKeyEnableFingerprintUnification = gateway.SettingKeyEnableFingerprintUnification
	// SettingKeyEnableMetadataPassthrough 是否透传客户端原始 metadata.user_id（默认 false）
	SettingKeyEnableMetadataPassthrough = gateway.SettingKeyEnableMetadataPassthrough
	// SettingKeyEnableCCHSigning 已废弃（no-op）：新版 Claude Code CLI 已取消 cch 签名字段，
	// 网关随之不再注入/签名 cch（见 buildBillingAttributionText）。保留该 key 仅为向后兼容，
	// 开关不再产生任何效果。
	SettingKeyEnableCCHSigning = gateway.SettingKeyEnableCCHSigning
	// SettingKeyEnableClaudeOAuthSystemPromptInjection 是否对 Claude OAuth mimic 路径注入 Claude Code system blocks（默认 true）
	SettingKeyEnableClaudeOAuthSystemPromptInjection = gateway.SettingKeyEnableClaudeOAuthSystemPromptInjection
	// SettingKeyClaudeOAuthSystemPrompt Claude OAuth mimic 路径注入的通用扩展 system prompt（空值使用内置默认）
	SettingKeyClaudeOAuthSystemPrompt = gateway.SettingKeyClaudeOAuthSystemPrompt
	// SettingKeyClaudeOAuthSystemPromptBlocks Claude OAuth mimic 路径注入的 system blocks JSON 配置（空值使用内置默认）
	SettingKeyClaudeOAuthSystemPromptBlocks = gateway.SettingKeyClaudeOAuthSystemPromptBlocks
	// SettingKeyEnableAnthropicCacheTTL1hInjection 是否对 Anthropic OAuth/SetupToken 请求体注入 1h cache_control ttl（默认 false）
	SettingKeyEnableAnthropicCacheTTL1hInjection = gateway.SettingKeyEnableAnthropicCacheTTL1hInjection
	// SettingKeyEnableClientDatelineNormalization 是否对 Anthropic OAuth/SetupToken 账号
	// 的 /v1/messages 请求体做客户端 dateline 归一化（默认 true）。
	// 归一化把 system prompt / <system-reminder> 块中 "Today's date is …" 语句里的
	// 非 ASCII 撇号与 "/" 日期分隔符还原为 ASCII 撇号 + "-" 分隔符，抹除某些客户端
	// 在检测到非官方 base URL 时注入的 3 bit 隐写指纹。仅适用于 Anthropic OAuth/SetupToken
	// 账号；API Key 账号不受影响。
	SettingKeyEnableClientDatelineNormalization = gateway.SettingKeyEnableClientDatelineNormalization
	// SettingKeyRewriteMessageCacheControl 是否改写 messages[*].content[*].cache_control（默认 false）
	SettingKeyRewriteMessageCacheControl = gateway.SettingKeyRewriteMessageCacheControl
	// SettingKeyAntigravityUserAgentVersion Antigravity 上游 User-Agent 版本号（空值使用环境变量/默认值）
	SettingKeyAntigravityUserAgentVersion = gateway.SettingKeyAntigravityUserAgentVersion
	// SettingKeyOpenAICodexUserAgent OpenAI Codex 完整 User-Agent（空值使用内置默认）
	// 当客户端 UA 被识别为浏览器（Chrome/Firefox/Safari/Edge 等）时，转发给 OpenAI 上游前会替换为此值，
	// 用于避免 Cloudflare 对浏览器型 UA 的质询拦截。
	SettingKeyOpenAICodexUserAgent = gateway.SettingKeyOpenAICodexUserAgent
	// SettingKeyOpenAIAllowClaudeCodeCodexPlugin 全局开关：是否额外放行 Claude Code 的 Codex 插件（默认 false）。
	// 仅在账号 codex_cli_only 开启时生效；开启后无需逐账号配置 codex_cli_only_allowed_clients。
	SettingKeyOpenAIAllowClaudeCodeCodexPlugin = gateway.SettingKeyOpenAIAllowClaudeCodeCodexPlugin
	// SettingKeyUserPromptReplacementConfig 用户提示词替换规则配置（JSON）。
	SettingKeyUserPromptReplacementConfig = promptpolicy.SettingKeyUserPromptReplacementConfig

	// 余额不足提醒
	SettingKeyBalanceLowNotifyEnabled     = billing.SettingKeyBalanceLowNotifyEnabled     // 全局开关
	SettingKeyBalanceLowNotifyThreshold   = billing.SettingKeyBalanceLowNotifyThreshold   // 默认阈值（USD）
	SettingKeyBalanceLowNotifyRechargeURL = billing.SettingKeyBalanceLowNotifyRechargeURL // 充值页面 URL

	// 订阅到期提醒
	SettingKeySubscriptionExpiryNotifyEnabled = billing.SettingKeySubscriptionExpiryNotifyEnabled // 订阅到期提醒全局开关，默认开启

	// 账号限额通知
	SettingKeyAccountQuotaNotifyEnabled = "account_quota_notify_enabled" // 全局开关
	SettingKeyAccountQuotaNotifyEmails  = "account_quota_notify_emails"  // 管理员通知邮箱列表（JSON 数组）

	// Web Search Emulation
	SettingKeyWebSearchEmulationConfig = "web_search_emulation_config" // JSON 配置
)

// SettingKeyDefaultPlatformQuotas —— 系统全局：每用户 × 平台日/周/月 USD 上限（JSON）。
// 值为 map[platform]{daily,weekly,monthly}，null/缺省 = 不限制；0 = 禁用；>0 = USD 上限。
const SettingKeyDefaultPlatformQuotas = billing.SettingKeyDefaultPlatformQuotas

// SettingKeyAccountSchedulingThresholds —— 系统全局：按平台自动停调阈值（JSON map）。
// 值为 map[platform]percent，1..100；100 = 禁用该平台自动停调。
const SettingKeyAccountSchedulingThresholds = "account_scheduling_thresholds"

// SettingKeyAuthSourcePlatformQuotas 返回某 auth source 的 platform quota JSON key。
// 形如 auth_source_default_{source}_platform_quotas
func SettingKeyAuthSourcePlatformQuotas(source string) string {
	return identity.SettingKeyAuthSourcePlatformQuotas(source)
}

// QuotaDimension constants for spark shadow accounts.
const (
	QuotaDimensionGlobal = accountcore.QuotaDimensionGlobal
	QuotaDimensionSpark  = accountcore.QuotaDimensionSpark
)

// AdminAPIKeyPrefix is the prefix for admin API keys (distinct from user "sk-" keys).
const AdminAPIKeyPrefix = identity.AdminAPIKeyPrefix

// SettingKeyAllowUserViewErrorRequests 控制终端用户是否能在用量页查看自己的失败请求。
// 默认关闭，需要管理员显式开启。
const SettingKeyAllowUserViewErrorRequests = usage.SettingKeyAllowUserViewErrorRequests
