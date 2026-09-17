package service

import (
	"context"

	"github.com/TokenFlux/TokenRouter/internal/audit"

	"github.com/TokenFlux/TokenRouter/internal/gateway/tierpolicy"
	identity "github.com/TokenFlux/TokenRouter/internal/identity"
)

// IsRegistrationEnabled 检查是否开放注册
func (s *SettingService) IsRegistrationEnabled(ctx context.Context) bool {
	return s.IdentitySettings().IsRegistrationEnabled(ctx)
}

// IsEmailVerifyEnabled 检查是否开启邮件验证
func (s *SettingService) IsEmailVerifyEnabled(ctx context.Context) bool {
	return s.IdentitySettings().IsEmailVerifyEnabled(ctx)
}

// IsRegistrationEmailDomainQuotaEnabled 检查是否放行非白名单域名限量注册。
// 设置缺失或读取失败时按关闭处理，保持严格白名单的安全默认。
func (s *SettingService) IsRegistrationEmailDomainQuotaEnabled(ctx context.Context) bool {
	return s.IdentitySettings().IsRegistrationEmailDomainQuotaEnabled(ctx)
}

// IsUserEmailChangeEnabled 检查是否允许已有邮箱身份的用户换绑主邮箱。
// 设置缺失或读取失败时按关闭处理，避免升级后意外放宽账号身份变更权限。
func (s *SettingService) IsUserEmailChangeEnabled(ctx context.Context) bool {
	return s.IdentitySettings().IsUserEmailChangeEnabled(ctx)
}

// GetRegistrationEmailSuffixWhitelist returns normalized registration email suffix whitelist.
func (s *SettingService) GetRegistrationEmailSuffixWhitelist(ctx context.Context) []string {
	return s.IdentitySettings().GetRegistrationEmailSuffixWhitelist(ctx)
}

// IsPromoCodeEnabled 检查是否启用优惠码功能
func (s *SettingService) IsPromoCodeEnabled(ctx context.Context) bool {
	return s.PromotionSettings().IsPromoCodeEnabled(ctx)
}

// IsInvitationCodeEnabled 检查是否启用邀请码注册功能
func (s *SettingService) IsInvitationCodeEnabled(ctx context.Context) bool {
	return s.PromotionSettings().IsInvitationCodeEnabled(ctx)
}

// GetCustomMenuItemsRaw 获取自定义菜单原始 JSON，用于页面 slug 权限校验。
func (s *SettingService) GetCustomMenuItemsRaw(ctx context.Context) string {
	value, err := s.settingRepo.GetValue(ctx, SettingKeyCustomMenuItems)
	if err != nil {
		return "[]"
	}
	return value
}

// IsAffiliateEnabled 检查邀请返利总开关是否开启。
func (s *SettingService) IsAffiliateEnabled(ctx context.Context) bool {
	return s.PromotionSettings().IsAffiliateEnabled(ctx)
}

// IsAffiliateAdminRechargeEnabled 检查管理员加余额是否参与邀请返利。
func (s *SettingService) IsAffiliateAdminRechargeEnabled(ctx context.Context) bool {
	return s.PromotionSettings().IsAffiliateAdminRechargeEnabled(ctx)
}

// GetAffiliateRebateRatePercent 读取全局邀请返利比例，并限制在安全范围内。
func (s *SettingService) GetAffiliateRebateRatePercent(ctx context.Context) float64 {
	return s.PromotionSettings().GetAffiliateRebateRatePercent(ctx)
}

// GetAffiliateRebateFreezeHours 返回返利冻结期，0 表示不冻结。
func (s *SettingService) GetAffiliateRebateFreezeHours(ctx context.Context) int {
	return s.PromotionSettings().GetAffiliateRebateFreezeHours(ctx)
}

// GetAffiliateRebateDurationDays 返回返利有效期，0 表示永久有效。
func (s *SettingService) GetAffiliateRebateDurationDays(ctx context.Context) int {
	return s.PromotionSettings().GetAffiliateRebateDurationDays(ctx)
}

// GetAffiliateRebatePerInviteeCap 返回单个被邀请人的累计返利积分上限，0 表示无上限。
func (s *SettingService) GetAffiliateRebatePerInviteeCap(ctx context.Context) float64 {
	return s.PromotionSettings().GetAffiliateRebatePerInviteeCap(ctx)
}

// IsPasswordResetEnabled 检查是否启用密码重置功能
// 要求：必须同时开启邮件验证
func (s *SettingService) IsPasswordResetEnabled(ctx context.Context) bool {
	return s.IdentitySettings().IsPasswordResetEnabled(ctx)
}

// IsTotpEnabled 检查是否启用 TOTP 双因素认证功能
func (s *SettingService) IsTotpEnabled(ctx context.Context) bool {
	return s.IdentitySettings().IsTotpEnabled(ctx)
}

// IsTotpEncryptionKeyConfigured 检查 TOTP 加密密钥是否已手动配置
// 只有手动配置了密钥才允许在管理后台启用 TOTP 功能
func (s *SettingService) IsTotpEncryptionKeyConfigured() bool {
	return s.cfg.Totp.EncryptionKeyConfigured
}

// IsSessionBindingEnabled 检查会话 IP/UA 绑定是否启用（默认关闭）。
// 开启时会话与登录时的 IP/User-Agent 绑定，任一变化立即失效并撤销该会话。
// 默认关闭：移动网络/多出口 IP 场景下 IP 频繁变化会导致登录后立即掉线。
func (s *SettingService) IsSessionBindingEnabled(ctx context.Context) bool {
	return s.IdentitySettings().IsSessionBindingEnabled(ctx)
}

// IsStepUpEnabled 检查敏感操作 step-up 2FA 门控是否启用（默认关闭）。
// 开启时账号/代理导出、备份创建/下载、S3 配置修改、提升管理员等操作
// 要求当前会话在有效期内完成过 TOTP step-up 验证。
func (s *SettingService) IsStepUpEnabled(ctx context.Context) bool {
	return s.IdentitySettings().IsStepUpEnabled(ctx)
}

// defaultAuditLogRetentionDays 审计日志默认保留天数。
const defaultAuditLogRetentionDays = audit.DefaultRetentionDays

// GetAuditLogRetentionDays 审计日志保留天数（<=0 表示永久保留，仅支持手动清空）。
func (s *SettingService) GetAuditLogRetentionDays(ctx context.Context) int {
	return s.AuditSettings().GetAuditLogRetentionDays(ctx)
}

// parseAuditLogRetentionDays 解析保留天数配置，空/非法值回退默认值。
func parseAuditLogRetentionDays(value string) int { return audit.ParseRetentionDays(value) }

// GetSiteName 获取网站名称
func (s *SettingService) GetSiteName(ctx context.Context) string {
	value, err := s.settingRepo.GetValue(ctx, SettingKeySiteName)
	if err != nil || value == "" {
		return "Sub2API"
	}
	return value
}

// GetDefaultConcurrency 获取默认并发量
func (s *SettingService) GetDefaultConcurrency(ctx context.Context) int {
	return s.GrantSettings().GetDefaultConcurrency(ctx)
}

// GetDefaultBalance 获取默认余额
func (s *SettingService) GetDefaultBalance(ctx context.Context) float64 {
	return s.GrantSettings().GetDefaultBalance(ctx)
}

// GetDefaultUserRPMLimit 获取新用户默认 RPM 限制（0 = 不限制）。未配置则返回 0。
func (s *SettingService) GetDefaultUserRPMLimit(ctx context.Context) int {
	return s.IdentitySettings().GetDefaultUserRPMLimit(ctx)
}

// GetDefaultUserAPIKeyLimit 获取新用户默认 API Key 数量上限，0 表示不限制。
func (s *SettingService) GetDefaultUserAPIKeyLimit(ctx context.Context) int {
	return s.IdentitySettings().GetDefaultUserAPIKeyLimit(ctx)
}

// GetDefaultSubscriptions 获取新用户默认订阅配置列表。
func (s *SettingService) GetDefaultSubscriptions(ctx context.Context) []DefaultSubscriptionSetting {
	return s.GrantSettings().GetDefaultSubscriptions(ctx)
}

func (s *SettingService) GetAuthSourceDefaultSettings(ctx context.Context) (*AuthSourceDefaultSettings, error) {
	return s.GrantSettings().GetAuthSourceDefaultSettings(ctx)
}

func (s *SettingService) ResolveAuthSourceGrantSettings(ctx context.Context, signupSource string, firstBind bool) (ProviderDefaultGrantSettings, bool, error) {
	return s.GrantSettings().ResolveAuthSourceGrantSettings(ctx, signupSource, firstBind)
}

func (s *SettingService) UpdateAuthSourceDefaultSettings(ctx context.Context, settings *AuthSourceDefaultSettings) error {
	return s.GrantSettings().UpdateAuthSourceDefaultSettings(ctx, settings)
}

// IsTurnstileEnabled 检查是否启用 Turnstile 验证
func (s *SettingService) IsTurnstileEnabled(ctx context.Context) bool {
	return s.IdentitySettings().IsTurnstileEnabled(ctx)
}

// GetTurnstileSecretKey 获取 Turnstile Secret Key
func (s *SettingService) GetTurnstileSecretKey(ctx context.Context) string {
	return s.IdentitySettings().GetTurnstileSecretKey(ctx)
}

type TencentCaptchaConfig = identity.TencentCaptchaConfig

type AliyunCaptchaConfig = identity.AliyunCaptchaConfig

type CaptchaProviderConfig = identity.CaptchaProviderConfig

func (s *SettingService) GetCaptchaProviderConfig(ctx context.Context) (CaptchaProviderConfig, error) {
	return s.IdentitySettings().GetCaptchaProviderConfig(ctx)
}

func (s *SettingService) IsTencentCaptchaEnabled(ctx context.Context) bool {
	return s.IdentitySettings().IsTencentCaptchaEnabled(ctx)
}

func (s *SettingService) GetTencentCaptchaConfig(ctx context.Context) TencentCaptchaConfig {
	return s.IdentitySettings().GetTencentCaptchaConfig(ctx)
}

// IsIdentityPatchEnabled 检查是否启用身份补丁（Claude -> Gemini systemInstruction 注入）
func (s *SettingService) IsIdentityPatchEnabled(ctx context.Context) bool {
	value, err := s.settingRepo.GetValue(ctx, SettingKeyEnableIdentityPatch)
	if err != nil {
		// 默认开启，保持兼容
		return true
	}
	return value == "true"
}

// GetIdentityPatchPrompt 获取自定义身份补丁提示词（为空表示使用内置默认模板）
func (s *SettingService) GetIdentityPatchPrompt(ctx context.Context) string {
	value, err := s.settingRepo.GetValue(ctx, SettingKeyIdentityPatchPrompt)
	if err != nil {
		return ""
	}
	return value
}

// GenerateAdminAPIKey 生成新的管理员 API Key
func (s *SettingService) GenerateAdminAPIKey(ctx context.Context) (string, error) {
	return s.IdentitySettings().GenerateAdminAPIKey(ctx)
}

// GetAdminAPIKeyStatus 获取管理员 API Key 状态
// 返回脱敏的 key、是否存在、错误
func (s *SettingService) GetAdminAPIKeyStatus(ctx context.Context) (maskedKey string, exists bool, err error) {
	return s.IdentitySettings().GetAdminAPIKeyStatus(ctx)
}

// GetAdminAPIKey 获取完整的管理员 API Key（仅供内部验证使用）
// 如果未配置返回空字符串和 nil 错误，只有数据库错误时才返回 error
func (s *SettingService) GetAdminAPIKey(ctx context.Context) (string, error) {
	return s.IdentitySettings().GetAdminAPIKey(ctx)
}

// DeleteAdminAPIKey 删除管理员 API Key
func (s *SettingService) DeleteAdminAPIKey(ctx context.Context) error {
	return s.IdentitySettings().DeleteAdminAPIKey(ctx)
}

// IsModelFallbackEnabled 检查是否启用模型兜底机制
func (s *SettingService) IsModelFallbackEnabled(ctx context.Context) bool {
	return s.RoutingSettings().IsModelFallbackEnabled(ctx)
}

// GetFallbackModel 获取指定平台的兜底模型
func (s *SettingService) GetFallbackModel(ctx context.Context, platform string) string {
	return s.RoutingSettings().GetFallbackModel(ctx, platform)
}

// GetOverloadCooldownSettings 获取529过载冷却配置
func (s *SettingService) GetOverloadCooldownSettings(ctx context.Context) (*OverloadCooldownSettings, error) {
	return s.AccountSettings().GetOverloadCooldownSettings(ctx)
}

// SetOverloadCooldownSettings 设置529过载冷却配置
func (s *SettingService) SetOverloadCooldownSettings(ctx context.Context, settings *OverloadCooldownSettings) error {
	return s.AccountSettings().SetOverloadCooldownSettings(ctx, settings)
}

// GetRateLimit429CooldownSettings 获取429默认回避配置
func (s *SettingService) GetRateLimit429CooldownSettings(ctx context.Context) (*RateLimit429CooldownSettings, error) {
	return s.AccountSettings().GetRateLimit429CooldownSettings(ctx)
}

// SetRateLimit429CooldownSettings 设置429默认回避配置
func (s *SettingService) SetRateLimit429CooldownSettings(ctx context.Context, settings *RateLimit429CooldownSettings) error {
	return s.AccountSettings().SetRateLimit429CooldownSettings(ctx, settings)
}

func (s *SettingService) GetOpenAIImagesOAuthUnavailableCooldownSettings(ctx context.Context) (*OpenAIImagesOAuthUnavailableCooldownSettings, error) {
	return s.AccountSettings().GetOpenAIImagesOAuthUnavailableCooldownSettings(ctx)
}

func (s *SettingService) SetOpenAIImagesOAuthUnavailableCooldownSettings(ctx context.Context, settings *OpenAIImagesOAuthUnavailableCooldownSettings) error {
	return s.AccountSettings().SetOpenAIImagesOAuthUnavailableCooldownSettings(ctx, settings)
}

// GetStreamTimeoutSettings 获取流超时处理配置
func (s *SettingService) GetStreamTimeoutSettings(ctx context.Context) (*StreamTimeoutSettings, error) {
	return s.AccountSettings().GetStreamTimeoutSettings(ctx)
}

// IsUngroupedKeySchedulingAllowed 查询是否允许未分组 Key 调度
func (s *SettingService) IsUngroupedKeySchedulingAllowed(ctx context.Context) bool {
	return s.RoutingSettings().IsUngroupedKeySchedulingAllowed(ctx)
}

// GetRectifierSettings 获取请求整流器配置
func (s *SettingService) GetRectifierSettings(ctx context.Context) (*RectifierSettings, error) {
	return s.GatewaySettings().GetRectifierSettings(ctx)
}

// SetRectifierSettings 设置请求整流器配置
func (s *SettingService) SetRectifierSettings(ctx context.Context, settings *RectifierSettings) error {
	return s.GatewaySettings().SetRectifierSettings(ctx, settings)
}

// IsSignatureRectifierEnabled 判断签名整流是否启用（总开关 && 签名子开关）
func (s *SettingService) IsSignatureRectifierEnabled(ctx context.Context) bool {
	return s.GatewaySettings().IsSignatureRectifierEnabled(ctx)
}

// IsBudgetRectifierEnabled 判断 Budget 整流是否启用（总开关 && Budget 子开关）
func (s *SettingService) IsBudgetRectifierEnabled(ctx context.Context) bool {
	return s.GatewaySettings().IsBudgetRectifierEnabled(ctx)
}

// GetBetaPolicySettings 获取 Beta 策略配置
func (s *SettingService) GetBetaPolicySettings(ctx context.Context) (*BetaPolicySettings, error) {
	value, err := s.GatewaySettings().GetBetaPolicySettings(ctx)
	return legacyBetaPolicySettings(value), err
}

// SetBetaPolicySettings 设置 Beta 策略配置
func (s *SettingService) SetBetaPolicySettings(ctx context.Context, settings *BetaPolicySettings) error {
	return s.GatewaySettings().SetBetaPolicySettings(ctx, gatewayBetaPolicySettings(settings))
}

// GetOpenAIFastPolicySettings 获取 OpenAI fast 策略配置
func (s *SettingService) GetOpenAIFastPolicySettings(ctx context.Context) (*OpenAIFastPolicySettings, error) {
	return s.GatewaySettings().GetOpenAIFastPolicySettings(ctx)
}

// SetOpenAIFastPolicySettings 设置 OpenAI fast 策略配置
func (s *SettingService) SetOpenAIFastPolicySettings(ctx context.Context, settings *OpenAIFastPolicySettings) error {
	return s.GatewaySettings().SetOpenAIFastPolicySettings(ctx, settings)
}

// PrepareOpenAIFastPolicySettings 准备纯校验结果，不写入设置或运行状态。
func PrepareOpenAIFastPolicySettings(settings *OpenAIFastPolicySettings) (string, error) {
	return tierpolicy.Prepare(settings)
}

// SetStreamTimeoutSettings 设置流超时处理配置
func (s *SettingService) SetStreamTimeoutSettings(ctx context.Context, settings *StreamTimeoutSettings) error {
	return s.AccountSettings().SetStreamTimeoutSettings(ctx, settings)
}

// GetDefaultPlatformQuotas 读取系统全局 platform quota JSON key，返回全部允许平台 x 3 window 的设置。
// 永远返回包含全部允许 platform key 的 map（值可能为零值/nil 字段，表示"上层未配置 = 不限制"）。
//
// 使用单个 JSON key（default_platform_quotas），一次 DB roundtrip，消除旧 12-KV 格式的 N+1 问题。
// 容错语义：取值失败或 unmarshal 失败 → 返回补齐全部允许平台 key 的空 map（fail-open，注册不被阻断）。
func (s *SettingService) GetDefaultPlatformQuotas(ctx context.Context) (map[string]*DefaultPlatformQuotaSetting, error) {
	return s.GrantSettings().GetDefaultPlatformQuotas(ctx)
}

// GetAccountSchedulingThresholds 返回各平台自动暂停阈值，取值范围为 1 到 100。
// 值为 100 时禁用该平台阈值；热点路径使用 singleflight 缓存。
func (s *SettingService) GetAccountSchedulingThresholds(ctx context.Context) map[string]int {
	return s.AccountSettings().GetAccountSchedulingThresholds(ctx)
}

// GetAuthSourcePlatformQuotas 读取指定 auth source 的 platform quota 覆盖（仅返回有配置的平台，override 语义）。
func (s *SettingService) GetAuthSourcePlatformQuotas(ctx context.Context, source string) map[string]*DefaultPlatformQuotaSetting {
	return s.GrantSettings().GetAuthSourcePlatformQuotas(ctx, source)
}
