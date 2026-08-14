package service

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"math"
	"strconv"
	"strings"
	"time"

	"github.com/BrandonVee/TokenRouter/internal/config"
	"github.com/BrandonVee/TokenRouter/internal/pkg/antigravity"
	infraerrors "github.com/BrandonVee/TokenRouter/internal/pkg/errors"
	"github.com/BrandonVee/TokenRouter/internal/pkg/xai"
)

// OmittedSettingKeys 标记调用方载荷中未包含的设置键。
// SystemSettings 是普通结构体，调用方省略的字段会以零值传入，无法与主动清空区分。
// 把键加入此集合可在写入前将其排除，从而保留存储中的原值。
//
// nil 或空集合保留整份文档语义，即写入所有设置键。
type OmittedSettingKeys map[string]struct{}

func (o OmittedSettingKeys) dropFrom(updates map[string]string) {
	for key := range o {
		delete(updates, key)
	}
}

// UpdateSettings 更新系统设置
func (s *SettingService) UpdateSettings(ctx context.Context, settings *SystemSettings) error {
	return s.UpdateSettingsOmitting(ctx, settings, nil)
}

// UpdateSettingsOmitting 持久化系统设置，并保留 omitted 中各键的存储值。
func (s *SettingService) UpdateSettingsOmitting(ctx context.Context, settings *SystemSettings, omitted OmittedSettingKeys) error {
	updates, err := s.buildSystemSettingsUpdates(ctx, settings)
	if err != nil {
		return err
	}
	omitted.dropFrom(updates)

	if err := s.settingRepo.SetMultiple(ctx, updates); err != nil {
		return err
	}
	s.refreshCachedSettingsAfterWrite(ctx, settings, omitted)
	return nil
}

// UpdateSettingsWithAuthSourceDefaults 在一次写入中持久化系统设置与认证来源默认值。
func (s *SettingService) UpdateSettingsWithAuthSourceDefaults(ctx context.Context, settings *SystemSettings, authDefaults *AuthSourceDefaultSettings) error {
	return s.UpdateSettingsWithAuthSourceDefaultsOmitting(ctx, settings, authDefaults, nil)
}

// UpdateSettingsWithAuthSourceDefaultsOmitting 在一次写入中持久化系统设置与认证来源默认值，
// 同时保留 omitted 中各键的存储值。
func (s *SettingService) UpdateSettingsWithAuthSourceDefaultsOmitting(ctx context.Context, settings *SystemSettings, authDefaults *AuthSourceDefaultSettings, omitted OmittedSettingKeys) error {
	updates, err := s.buildSystemSettingsUpdates(ctx, settings)
	if err != nil {
		return err
	}

	authSourceUpdates, err := s.buildAuthSourceDefaultUpdates(ctx, authDefaults)
	if err != nil {
		return err
	}
	for key, value := range authSourceUpdates {
		updates[key] = value
	}
	omitted.dropFrom(updates)

	if err := s.settingRepo.SetMultiple(ctx, updates); err != nil {
		return err
	}
	s.refreshCachedSettingsAfterWrite(ctx, settings, omitted)
	return nil
}

// refreshCachedSettingsAfterWrite 使进程内缓存与刚完成的写入保持一致。
// 部分载荷会把省略字段表示为零值，因此此时必须从存储重建缓存，而不能使用请求结构体。
func (s *SettingService) refreshCachedSettingsAfterWrite(ctx context.Context, settings *SystemSettings, omitted OmittedSettingKeys) {
	if len(omitted) == 0 {
		s.refreshCachedSettings(settings)
		return
	}
	stored, err := s.GetAllSettings(ctx)
	if err != nil {
		slog.Warn("refresh cached settings after partial update failed", "error", err)
		return
	}
	s.refreshCachedSettings(stored)
}

func (s *SettingService) buildSystemSettingsUpdates(ctx context.Context, settings *SystemSettings) (map[string]string, error) {
	if err := s.validateDefaultSubscriptionPlans(ctx, settings.DefaultSubscriptions); err != nil {
		return nil, err
	}
	normalizedWhitelist, err := NormalizeRegistrationEmailSuffixWhitelist(settings.RegistrationEmailSuffixWhitelist)
	if err != nil {
		return nil, infraerrors.BadRequest("INVALID_REGISTRATION_EMAIL_SUFFIX_WHITELIST", err.Error())
	}
	if normalizedWhitelist == nil {
		normalizedWhitelist = []string{}
	}
	settings.RegistrationEmailSuffixWhitelist = normalizedWhitelist
	settings.BalanceUnitName = strings.TrimSpace(settings.BalanceUnitName)
	settings.BalanceUnitSymbol = strings.TrimSpace(settings.BalanceUnitSymbol)
	settings.BalanceIconSVG = strings.TrimSpace(settings.BalanceIconSVG)
	if settings.BalanceUnitName == "" {
		settings.BalanceUnitName = "USD"
	}
	if settings.BalanceUnitSymbol == "" {
		settings.BalanceUnitSymbol = "$"
	}
	if settings.ReasoningPointRMBUnitPrice < 0 {
		settings.ReasoningPointRMBUnitPrice = 0
	}
	if settings.USDExchangeRate < 0 {
		settings.USDExchangeRate = 0
	}
	settings.MarketplaceAvailabilityWindowDays, settings.MarketplaceAvailabilityBucketMinutes = NormalizeMarketplaceAvailabilityWindow(
		settings.MarketplaceAvailabilityWindowDays,
		settings.MarketplaceAvailabilityBucketMinutes,
	)
	normalizedForwardedClientIPHeaders, err := config.NormalizeForwardedClientIPHeaders(settings.ForwardedClientIPHeaders)
	if err != nil {
		return nil, infraerrors.BadRequest("INVALID_FORWARDED_CLIENT_IP_HEADERS", err.Error())
	}
	settings.ForwardedClientIPHeaders = normalizedForwardedClientIPHeaders
	alipaySource, err := normalizeVisibleMethodSettingSource("alipay", settings.PaymentVisibleMethodAlipaySource, settings.PaymentVisibleMethodAlipayEnabled)
	if err != nil {
		return nil, err
	}
	wxpaySource, err := normalizeVisibleMethodSettingSource("wxpay", settings.PaymentVisibleMethodWxpaySource, settings.PaymentVisibleMethodWxpayEnabled)
	if err != nil {
		return nil, err
	}
	if err := s.normalizeAdvancedSchedulerOverrides(settings); err != nil {
		return nil, err
	}
	settings.PaymentVisibleMethodAlipaySource = alipaySource
	settings.PaymentVisibleMethodWxpaySource = wxpaySource
	settings.WeChatConnectAppID = strings.TrimSpace(settings.WeChatConnectAppID)
	settings.WeChatConnectAppSecret = strings.TrimSpace(settings.WeChatConnectAppSecret)
	settings.WeChatConnectOpenAppID = strings.TrimSpace(firstNonEmpty(settings.WeChatConnectOpenAppID, settings.WeChatConnectAppID))
	settings.WeChatConnectOpenAppSecret = strings.TrimSpace(firstNonEmpty(settings.WeChatConnectOpenAppSecret, settings.WeChatConnectAppSecret))
	settings.WeChatConnectMPAppID = strings.TrimSpace(firstNonEmpty(settings.WeChatConnectMPAppID, settings.WeChatConnectAppID))
	settings.WeChatConnectMPAppSecret = strings.TrimSpace(firstNonEmpty(settings.WeChatConnectMPAppSecret, settings.WeChatConnectAppSecret))
	settings.WeChatConnectMobileAppID = strings.TrimSpace(firstNonEmpty(settings.WeChatConnectMobileAppID, settings.WeChatConnectAppID))
	settings.WeChatConnectMobileAppSecret = strings.TrimSpace(firstNonEmpty(settings.WeChatConnectMobileAppSecret, settings.WeChatConnectAppSecret))
	settings.WeChatConnectMode = normalizeWeChatConnectStoredMode(
		settings.WeChatConnectOpenEnabled,
		settings.WeChatConnectMPEnabled,
		settings.WeChatConnectMobileEnabled,
		settings.WeChatConnectMode,
	)
	settings.WeChatConnectScopes = normalizeWeChatConnectScopeSetting(settings.WeChatConnectScopes, settings.WeChatConnectMode)
	settings.WeChatConnectRedirectURL = strings.TrimSpace(settings.WeChatConnectRedirectURL)
	settings.WeChatConnectFrontendRedirectURL = strings.TrimSpace(settings.WeChatConnectFrontendRedirectURL)
	if settings.WeChatConnectFrontendRedirectURL == "" {
		settings.WeChatConnectFrontendRedirectURL = defaultWeChatConnectFrontend
	}
	settings.GitHubOAuthClientID = strings.TrimSpace(settings.GitHubOAuthClientID)
	settings.GitHubOAuthClientSecret = strings.TrimSpace(settings.GitHubOAuthClientSecret)
	settings.GitHubOAuthRedirectURL = strings.TrimSpace(settings.GitHubOAuthRedirectURL)
	settings.GitHubOAuthFrontendRedirectURL = strings.TrimSpace(settings.GitHubOAuthFrontendRedirectURL)
	if settings.GitHubOAuthFrontendRedirectURL == "" {
		settings.GitHubOAuthFrontendRedirectURL = defaultGitHubOAuthFrontend
	}
	settings.GoogleOAuthClientID = strings.TrimSpace(settings.GoogleOAuthClientID)
	settings.GoogleOAuthClientSecret = strings.TrimSpace(settings.GoogleOAuthClientSecret)
	settings.GoogleOAuthRedirectURL = strings.TrimSpace(settings.GoogleOAuthRedirectURL)
	settings.GoogleOAuthFrontendRedirectURL = strings.TrimSpace(settings.GoogleOAuthFrontendRedirectURL)
	if settings.GoogleOAuthFrontendRedirectURL == "" {
		settings.GoogleOAuthFrontendRedirectURL = defaultGoogleOAuthFrontend
	}

	updates := make(map[string]string)

	// 注册设置
	updates[SettingKeyRegistrationEnabled] = strconv.FormatBool(settings.RegistrationEnabled)
	updates[SettingKeyEmailVerifyEnabled] = strconv.FormatBool(settings.EmailVerifyEnabled)
	updates[SettingKeyRegistrationEmailNormalization] = strconv.FormatBool(settings.RegistrationEmailNormalization)
	updates[SettingKeyRegistrationEmailDomainQuotaEnabled] = strconv.FormatBool(settings.RegistrationEmailDomainQuotaEnabled)
	registrationEmailSuffixWhitelistJSON, err := json.Marshal(settings.RegistrationEmailSuffixWhitelist)
	if err != nil {
		return nil, fmt.Errorf("marshal registration email suffix whitelist: %w", err)
	}
	updates[SettingKeyRegistrationEmailSuffixWhitelist] = string(registrationEmailSuffixWhitelistJSON)
	updates[SettingKeyPromoCodeEnabled] = strconv.FormatBool(settings.PromoCodeEnabled)
	updates[SettingKeyPasswordResetEnabled] = strconv.FormatBool(settings.PasswordResetEnabled)
	updates[SettingKeyFrontendURL] = settings.FrontendURL
	updates[SettingKeyInvitationCodeEnabled] = strconv.FormatBool(settings.InvitationCodeEnabled)
	updates[SettingKeyTotpEnabled] = strconv.FormatBool(settings.TotpEnabled)
	updates[SettingKeySessionBindingEnabled] = strconv.FormatBool(settings.SessionBindingEnabled)
	updates[SettingKeyStepUpEnabled] = strconv.FormatBool(settings.StepUpEnabled)
	updates[SettingKeyAuditLogRetentionDays] = strconv.Itoa(settings.AuditLogRetentionDays)
	settings.LoginAgreementMode = normalizeLoginAgreementMode(settings.LoginAgreementMode)
	settings.LoginAgreementUpdatedAt = strings.TrimSpace(settings.LoginAgreementUpdatedAt)
	if settings.LoginAgreementUpdatedAt == "" {
		settings.LoginAgreementUpdatedAt = defaultLoginAgreementDate
	}
	loginAgreementDocumentsJSON, err := marshalLoginAgreementDocuments(settings.LoginAgreementDocuments)
	if err != nil {
		return nil, err
	}
	updates[SettingKeyLoginAgreementEnabled] = strconv.FormatBool(settings.LoginAgreementEnabled)
	updates[SettingKeyLoginAgreementMode] = settings.LoginAgreementMode
	updates[SettingKeyLoginAgreementUpdatedAt] = settings.LoginAgreementUpdatedAt
	updates[SettingKeyLoginAgreementDocuments] = loginAgreementDocumentsJSON

	// 邮件服务设置（只有非空才更新密码）
	updates[SettingKeySMTPHost] = settings.SMTPHost
	updates[SettingKeySMTPPort] = strconv.Itoa(settings.SMTPPort)
	updates[SettingKeySMTPUsername] = settings.SMTPUsername
	if settings.SMTPPassword != "" {
		updates[SettingKeySMTPPassword] = settings.SMTPPassword
	}
	updates[SettingKeySMTPFrom] = settings.SMTPFrom
	updates[SettingKeySMTPFromName] = settings.SMTPFromName
	updates[SettingKeySMTPUseTLS] = strconv.FormatBool(settings.SMTPUseTLS)

	// Cloudflare Turnstile 设置（只有非空才更新密钥）
	updates[SettingKeyTurnstileEnabled] = strconv.FormatBool(settings.TurnstileEnabled)
	updates[SettingKeyTurnstileSiteKey] = settings.TurnstileSiteKey
	if settings.TurnstileSecretKey != "" {
		updates[SettingKeyTurnstileSecretKey] = settings.TurnstileSecretKey
	}

	updates[SettingKeyTencentCaptchaEnabled] = strconv.FormatBool(settings.TencentCaptchaEnabled)
	updates[SettingKeyTencentCaptchaAppID] = settings.TencentCaptchaAppID
	if settings.TencentCaptchaAppSecretKey != "" {
		updates[SettingKeyTencentCaptchaAppSecretKey] = settings.TencentCaptchaAppSecretKey
	}
	if settings.TencentCaptchaCloudSecretID != "" {
		updates[SettingKeyTencentCaptchaCloudSecretID] = settings.TencentCaptchaCloudSecretID
	}
	if settings.TencentCaptchaCloudSecretKey != "" {
		updates[SettingKeyTencentCaptchaCloudSecretKey] = settings.TencentCaptchaCloudSecretKey
	}
	updates[SettingKeyTencentCaptchaRegion] = normalizeTencentCaptchaRegion(settings.TencentCaptchaRegion)
	// 阿里云验证码 2.0 设置（只有非空才更新密钥）
	updates[SettingKeyAliyunCaptchaEnabled] = strconv.FormatBool(settings.AliyunCaptchaEnabled)
	updates[SettingKeyAliyunCaptchaAccessKeyID] = settings.AliyunCaptchaAccessKeyID
	if settings.AliyunCaptchaAccessKeySecret != "" {
		updates[SettingKeyAliyunCaptchaAccessKeySecret] = settings.AliyunCaptchaAccessKeySecret
	}
	updates[SettingKeyAliyunCaptchaSceneID] = settings.AliyunCaptchaSceneID
	updates[SettingKeyAliyunCaptchaPrefix] = settings.AliyunCaptchaPrefix
	updates[SettingKeyAliyunCaptchaRegion] = normalizeAliyunCaptchaRegion(settings.AliyunCaptchaRegion)
	updates[SettingKeyAPIKeyACLTrustForwardedIP] = strconv.FormatBool(settings.APIKeyACLTrustForwardedIP)
	forwardedClientIPHeadersJSON, err := json.Marshal(settings.ForwardedClientIPHeaders)
	if err != nil {
		return nil, fmt.Errorf("marshal forwarded client IP headers: %w", err)
	}
	updates[SettingKeyForwardedClientIPHeaders] = string(forwardedClientIPHeadersJSON)

	// LinuxDo Connect OAuth 登录
	updates[SettingKeyLinuxDoConnectEnabled] = strconv.FormatBool(settings.LinuxDoConnectEnabled)
	updates[SettingKeyLinuxDoConnectClientID] = settings.LinuxDoConnectClientID
	updates[SettingKeyLinuxDoConnectRedirectURL] = settings.LinuxDoConnectRedirectURL
	if settings.LinuxDoConnectClientSecret != "" {
		updates[SettingKeyLinuxDoConnectClientSecret] = settings.LinuxDoConnectClientSecret
	}

	// DingTalk Connect OAuth 登录
	updates[SettingKeyDingTalkConnectEnabled] = strconv.FormatBool(settings.DingTalkConnectEnabled)
	updates[SettingKeyDingTalkConnectClientID] = settings.DingTalkConnectClientID
	updates[SettingKeyDingTalkConnectRedirectURL] = settings.DingTalkConnectRedirectURL
	if settings.DingTalkConnectClientSecret != "" {
		updates[SettingKeyDingTalkConnectClientSecret] = settings.DingTalkConnectClientSecret
	}
	updates[SettingKeyDingTalkConnectCorpRestrictionPolicy] = settings.DingTalkConnectCorpRestrictionPolicy
	updates[SettingKeyDingTalkConnectInternalCorpID] = settings.DingTalkConnectInternalCorpID
	updates[SettingKeyDingTalkConnectBypassRegistration] = strconv.FormatBool(settings.DingTalkConnectBypassRegistration)
	updates[SettingKeyDingTalkConnectSyncCorpEmail] = strconv.FormatBool(settings.DingTalkConnectSyncCorpEmail)
	updates[SettingKeyDingTalkConnectSyncDisplayName] = strconv.FormatBool(settings.DingTalkConnectSyncDisplayName)
	updates[SettingKeyDingTalkConnectSyncDept] = strconv.FormatBool(settings.DingTalkConnectSyncDept)
	updates[SettingKeyDingTalkConnectSyncCorpEmailAttrKey] = settings.DingTalkConnectSyncCorpEmailAttrKey
	updates[SettingKeyDingTalkConnectSyncDisplayNameAttrKey] = settings.DingTalkConnectSyncDisplayNameAttrKey
	updates[SettingKeyDingTalkConnectSyncDeptAttrKey] = settings.DingTalkConnectSyncDeptAttrKey
	updates[SettingKeyDingTalkConnectSyncCorpEmailAttrName] = settings.DingTalkConnectSyncCorpEmailAttrName
	updates[SettingKeyDingTalkConnectSyncDisplayNameAttrName] = settings.DingTalkConnectSyncDisplayNameAttrName
	updates[SettingKeyDingTalkConnectSyncDeptAttrName] = settings.DingTalkConnectSyncDeptAttrName

	// Generic OIDC OAuth 登录
	updates[SettingKeyOIDCConnectEnabled] = strconv.FormatBool(settings.OIDCConnectEnabled)
	updates[SettingKeyOIDCConnectProviderName] = settings.OIDCConnectProviderName
	updates[SettingKeyOIDCConnectClientID] = settings.OIDCConnectClientID
	updates[SettingKeyOIDCConnectIssuerURL] = settings.OIDCConnectIssuerURL
	updates[SettingKeyOIDCConnectDiscoveryURL] = settings.OIDCConnectDiscoveryURL
	updates[SettingKeyOIDCConnectAuthorizeURL] = settings.OIDCConnectAuthorizeURL
	updates[SettingKeyOIDCConnectTokenURL] = settings.OIDCConnectTokenURL
	updates[SettingKeyOIDCConnectUserInfoURL] = settings.OIDCConnectUserInfoURL
	updates[SettingKeyOIDCConnectJWKSURL] = settings.OIDCConnectJWKSURL
	updates[SettingKeyOIDCConnectScopes] = settings.OIDCConnectScopes
	updates[SettingKeyOIDCConnectRedirectURL] = settings.OIDCConnectRedirectURL
	updates[SettingKeyOIDCConnectFrontendRedirectURL] = settings.OIDCConnectFrontendRedirectURL
	updates[SettingKeyOIDCConnectTokenAuthMethod] = settings.OIDCConnectTokenAuthMethod
	updates[SettingKeyOIDCConnectUsePKCE] = strconv.FormatBool(settings.OIDCConnectUsePKCE)
	updates[SettingKeyOIDCConnectValidateIDToken] = strconv.FormatBool(settings.OIDCConnectValidateIDToken)
	updates[SettingKeyOIDCConnectAllowedSigningAlgs] = settings.OIDCConnectAllowedSigningAlgs
	updates[SettingKeyOIDCConnectClockSkewSeconds] = strconv.Itoa(settings.OIDCConnectClockSkewSeconds)
	updates[SettingKeyOIDCConnectRequireEmailVerified] = strconv.FormatBool(settings.OIDCConnectRequireEmailVerified)
	updates[SettingKeyOIDCConnectUserInfoEmailPath] = settings.OIDCConnectUserInfoEmailPath
	updates[SettingKeyOIDCConnectUserInfoIDPath] = settings.OIDCConnectUserInfoIDPath
	updates[SettingKeyOIDCConnectUserInfoUsernamePath] = settings.OIDCConnectUserInfoUsernamePath
	if settings.OIDCConnectClientSecret != "" {
		updates[SettingKeyOIDCConnectClientSecret] = settings.OIDCConnectClientSecret
	}

	// GitHub / Google 邮箱快捷登录配置；密钥留空时保留已有值。
	updates[SettingKeyGitHubOAuthEnabled] = strconv.FormatBool(settings.GitHubOAuthEnabled)
	updates[SettingKeyGitHubOAuthClientID] = settings.GitHubOAuthClientID
	updates[SettingKeyGitHubOAuthRedirectURL] = settings.GitHubOAuthRedirectURL
	updates[SettingKeyGitHubOAuthFrontendRedirectURL] = settings.GitHubOAuthFrontendRedirectURL
	if settings.GitHubOAuthClientSecret != "" {
		updates[SettingKeyGitHubOAuthClientSecret] = settings.GitHubOAuthClientSecret
	}
	updates[SettingKeyGoogleOAuthEnabled] = strconv.FormatBool(settings.GoogleOAuthEnabled)
	updates[SettingKeyGoogleOneTapEnabled] = strconv.FormatBool(settings.GoogleOneTapEnabled)
	updates[SettingKeyGoogleOAuthClientID] = settings.GoogleOAuthClientID
	updates[SettingKeyGoogleOAuthRedirectURL] = settings.GoogleOAuthRedirectURL
	updates[SettingKeyGoogleOAuthFrontendRedirectURL] = settings.GoogleOAuthFrontendRedirectURL
	if settings.GoogleOAuthClientSecret != "" {
		updates[SettingKeyGoogleOAuthClientSecret] = settings.GoogleOAuthClientSecret
	}

	// WeChat Connect OAuth 登录
	updates[SettingKeyWeChatConnectEnabled] = strconv.FormatBool(settings.WeChatConnectEnabled)
	updates[SettingKeyWeChatConnectAppID] = settings.WeChatConnectAppID
	updates[SettingKeyWeChatConnectOpenAppID] = settings.WeChatConnectOpenAppID
	updates[SettingKeyWeChatConnectMPAppID] = settings.WeChatConnectMPAppID
	updates[SettingKeyWeChatConnectMobileAppID] = settings.WeChatConnectMobileAppID
	updates[SettingKeyWeChatConnectOpenEnabled] = strconv.FormatBool(settings.WeChatConnectOpenEnabled)
	updates[SettingKeyWeChatConnectMPEnabled] = strconv.FormatBool(settings.WeChatConnectMPEnabled)
	updates[SettingKeyWeChatConnectMobileEnabled] = strconv.FormatBool(settings.WeChatConnectMobileEnabled)
	updates[SettingKeyWeChatConnectMode] = settings.WeChatConnectMode
	updates[SettingKeyWeChatConnectScopes] = settings.WeChatConnectScopes
	updates[SettingKeyWeChatConnectRedirectURL] = settings.WeChatConnectRedirectURL
	updates[SettingKeyWeChatConnectFrontendRedirectURL] = settings.WeChatConnectFrontendRedirectURL
	if settings.WeChatConnectAppSecret != "" {
		updates[SettingKeyWeChatConnectAppSecret] = settings.WeChatConnectAppSecret
	}
	if settings.WeChatConnectOpenAppSecret != "" {
		updates[SettingKeyWeChatConnectOpenAppSecret] = settings.WeChatConnectOpenAppSecret
	}
	if settings.WeChatConnectMPAppSecret != "" {
		updates[SettingKeyWeChatConnectMPAppSecret] = settings.WeChatConnectMPAppSecret
	}
	if settings.WeChatConnectMobileAppSecret != "" {
		updates[SettingKeyWeChatConnectMobileAppSecret] = settings.WeChatConnectMobileAppSecret
	}

	// OEM设置
	updates[SettingKeySiteName] = settings.SiteName
	updates[SettingKeySiteLogo] = settings.SiteLogo
	updates[SettingKeySiteSubtitle] = settings.SiteSubtitle
	updates[SettingKeySiteNameZh] = settings.SiteNameZh
	updates[SettingKeySiteNameEn] = settings.SiteNameEn
	updates[SettingKeySiteTitleZh] = settings.SiteTitleZh
	updates[SettingKeySiteTitleEn] = settings.SiteTitleEn
	updates[SettingKeySiteSubtitleZh] = settings.SiteSubtitleZh
	updates[SettingKeySiteSubtitleEn] = settings.SiteSubtitleEn
	updates[SettingKeyAPIBaseURL] = settings.APIBaseURL
	updates[SettingKeyContactInfo] = settings.ContactInfo
	updates[SettingKeyDocURL] = settings.DocURL
	updates[SettingKeyHomeContent] = settings.HomeContent
	updates[SettingKeyHideCcsImportButton] = strconv.FormatBool(settings.HideCcsImportButton)
	updates[SettingKeyPurchaseSubscriptionEnabled] = strconv.FormatBool(settings.PurchaseSubscriptionEnabled)
	updates[SettingKeyPurchaseSubscriptionURL] = strings.TrimSpace(settings.PurchaseSubscriptionURL)
	tableDefaultPageSize, tablePageSizeOptions := normalizeTablePreferences(
		settings.TableDefaultPageSize,
		settings.TablePageSizeOptions,
	)
	updates[SettingKeyTableDefaultPageSize] = strconv.Itoa(tableDefaultPageSize)
	tablePageSizeOptionsJSON, err := json.Marshal(tablePageSizeOptions)
	if err != nil {
		return nil, fmt.Errorf("marshal table page size options: %w", err)
	}
	updates[SettingKeyTablePageSizeOptions] = string(tablePageSizeOptionsJSON)
	updates[SettingKeyUsageRankingLimit] = strconv.Itoa(normalizeUsageRankingLimit(settings.UsageRankingLimit))
	updates[SettingKeyCustomMenuItems] = settings.CustomMenuItems
	updates[SettingKeyCustomEndpoints] = settings.CustomEndpoints
	updates[SettingKeyFooterLinks] = settings.FooterLinks
	updates[SettingKeyFooterText] = strings.TrimSpace(settings.FooterText)

	// 默认配置
	updates[SettingKeyDefaultConcurrency] = strconv.Itoa(settings.DefaultConcurrency)
	updates[SettingKeyDefaultBalance] = strconv.FormatFloat(settings.DefaultBalance, 'f', 8, 64)
	settings.AffiliateRebateRate = clampAffiliateRebateRate(settings.AffiliateRebateRate)
	updates[SettingKeyAffiliateRebateRate] = strconv.FormatFloat(settings.AffiliateRebateRate, 'f', 8, 64)
	if settings.AffiliateRebateFreezeHours < 0 {
		settings.AffiliateRebateFreezeHours = AffiliateRebateFreezeHoursDefault
	}
	if settings.AffiliateRebateFreezeHours > AffiliateRebateFreezeHoursMax {
		settings.AffiliateRebateFreezeHours = AffiliateRebateFreezeHoursMax
	}
	updates[SettingKeyAffiliateRebateFreezeHours] = strconv.Itoa(settings.AffiliateRebateFreezeHours)
	if settings.AffiliateRebateDurationDays < 0 {
		settings.AffiliateRebateDurationDays = AffiliateRebateDurationDaysDefault
	}
	if settings.AffiliateRebateDurationDays > AffiliateRebateDurationDaysMax {
		settings.AffiliateRebateDurationDays = AffiliateRebateDurationDaysMax
	}
	updates[SettingKeyAffiliateRebateDurationDays] = strconv.Itoa(settings.AffiliateRebateDurationDays)
	if settings.AffiliateRebatePerInviteeCap < 0 {
		settings.AffiliateRebatePerInviteeCap = AffiliateRebatePerInviteeCapDefault
	}
	updates[SettingKeyAffiliateRebatePerInviteeCap] = strconv.FormatFloat(settings.AffiliateRebatePerInviteeCap, 'f', 8, 64)
	updates[SettingKeyAffiliateAdminRechargeEnabled] = strconv.FormatBool(settings.AdminRechargeRebateEnabled)
	updates[SettingKeyDefaultUserRPMLimit] = strconv.Itoa(settings.DefaultUserRPMLimit)
	if !IsValidUserAPIKeyLimit(settings.DefaultUserAPIKeyLimit) {
		return nil, ErrUserAPIKeyLimitInvalid
	}
	updates[SettingKeyDefaultUserAPIKeyLimit] = strconv.Itoa(settings.DefaultUserAPIKeyLimit)
	defaultSubsJSON, err := json.Marshal(settings.DefaultSubscriptions)
	if err != nil {
		return nil, fmt.Errorf("marshal default subscriptions: %w", err)
	}
	updates[SettingKeyDefaultSubscriptions] = string(defaultSubsJSON)
	updates[SettingKeyBalanceUnitName] = settings.BalanceUnitName
	updates[SettingKeyBalanceUnitSymbol] = settings.BalanceUnitSymbol
	updates[SettingKeyBalanceIconSVG] = settings.BalanceIconSVG
	updates[SettingKeyReasoningPointRMBUnitPrice] = strconv.FormatFloat(settings.ReasoningPointRMBUnitPrice, 'f', 8, 64)
	updates[SettingKeyUSDExchangeRate] = strconv.FormatFloat(settings.USDExchangeRate, 'f', 8, 64)
	updates[SettingKeyMarketplaceAvailabilityWindowDays] = strconv.Itoa(settings.MarketplaceAvailabilityWindowDays)
	updates[SettingKeyMarketplaceAvailabilityBucketMinutes] = strconv.Itoa(settings.MarketplaceAvailabilityBucketMinutes)

	// Model fallback configuration
	updates[SettingKeyEnableModelFallback] = strconv.FormatBool(settings.EnableModelFallback)
	updates[SettingKeyFallbackModelAnthropic] = settings.FallbackModelAnthropic
	updates[SettingKeyFallbackModelOpenAI] = settings.FallbackModelOpenAI
	updates[SettingKeyFallbackModelGemini] = settings.FallbackModelGemini
	updates[SettingKeyFallbackModelAntigravity] = settings.FallbackModelAntigravity
	if model := strings.TrimSpace(settings.GrokDefaultTextModel); model != "" {
		updates[SettingKeyGrokDefaultTextModel] = model
	} else {
		updates[SettingKeyGrokDefaultTextModel] = xai.DefaultTextModel
	}
	updates[SettingKeyGrokCrossClientModelMapEnabled] = strconv.FormatBool(settings.GrokCrossClientModelMapEnabled)
	updates[SettingKeyGrokDefaultBaseURLMode] = normalizeGrokDefaultBaseURLMode(settings.GrokDefaultBaseURLMode)

	// Identity patch configuration (Claude -> Gemini)
	updates[SettingKeyEnableIdentityPatch] = strconv.FormatBool(settings.EnableIdentityPatch)
	updates[SettingKeyIdentityPatchPrompt] = settings.IdentityPatchPrompt

	// Ops monitoring (vNext)
	updates[SettingKeyOpsMonitoringEnabled] = strconv.FormatBool(settings.OpsMonitoringEnabled)
	updates[SettingKeyOpsRealtimeMonitoringEnabled] = strconv.FormatBool(settings.OpsRealtimeMonitoringEnabled)
	if settings.OpsMetricsIntervalSeconds > 0 {
		updates[SettingKeyOpsMetricsIntervalSeconds] = strconv.Itoa(settings.OpsMetricsIntervalSeconds)
	}

	// Claude Code version check
	updates[SettingKeyMinClaudeCodeVersion] = settings.MinClaudeCodeVersion
	updates[SettingKeyMaxClaudeCodeVersion] = settings.MaxClaudeCodeVersion

	// 分组隔离
	updates[SettingKeyAllowUngroupedKeyScheduling] = strconv.FormatBool(settings.AllowUngroupedKeyScheduling)

	// Backend Mode
	updates[SettingKeyBackendModeEnabled] = strconv.FormatBool(settings.BackendModeEnabled)

	// 邀请返利总开关
	updates[SettingKeyAffiliateEnabled] = strconv.FormatBool(settings.AffiliateEnabled)

	// Gateway forwarding behavior
	updates[SettingKeyEnableFingerprintUnification] = strconv.FormatBool(settings.EnableFingerprintUnification)
	updates[SettingKeyEnableMetadataPassthrough] = strconv.FormatBool(settings.EnableMetadataPassthrough)
	updates[SettingKeyEnableCCHSigning] = strconv.FormatBool(settings.EnableCCHSigning)
	if err := ValidateClaudeOAuthSystemPromptBlocksConfig(settings.ClaudeOAuthSystemPromptBlocks); err != nil {
		return nil, err
	}
	updates[SettingKeyEnableClaudeOAuthSystemPromptInjection] = strconv.FormatBool(settings.EnableClaudeOAuthSystemPromptInjection)
	updates[SettingKeyClaudeOAuthSystemPrompt] = settings.ClaudeOAuthSystemPrompt
	updates[SettingKeyClaudeOAuthSystemPromptBlocks] = settings.ClaudeOAuthSystemPromptBlocks
	updates[SettingKeyEnableAnthropicCacheTTL1hInjection] = strconv.FormatBool(settings.EnableAnthropicCacheTTL1hInjection)
	updates[SettingKeyRewriteMessageCacheControl] = strconv.FormatBool(settings.RewriteMessageCacheControl)
	updates[SettingKeyEnableClientDatelineNormalization] = strconv.FormatBool(settings.EnableClientDatelineNormalization)
	updates[SettingKeyAntigravityUserAgentVersion] = antigravity.NormalizeUserAgentVersion(settings.AntigravityUserAgentVersion)
	updates[SettingKeyOpenAICodexUserAgent] = strings.TrimSpace(settings.OpenAICodexUserAgent)
	updates[SettingKeyOpenAIAllowClaudeCodeCodexPlugin] = strconv.FormatBool(settings.OpenAIAllowClaudeCodeCodexPlugin)
	userPromptReplacementConfigJSON, err := userPromptReplacementConfigToRaw(settings.UserPromptReplacementConfig)
	if err != nil {
		return nil, err
	}
	updates[SettingKeyUserPromptReplacementConfig] = userPromptReplacementConfigJSON
	updates[SettingPaymentVisibleMethodAlipaySource] = settings.PaymentVisibleMethodAlipaySource
	updates[SettingPaymentVisibleMethodWxpaySource] = settings.PaymentVisibleMethodWxpaySource
	updates[SettingPaymentVisibleMethodAlipayEnabled] = strconv.FormatBool(settings.PaymentVisibleMethodAlipayEnabled)
	updates[SettingPaymentVisibleMethodWxpayEnabled] = strconv.FormatBool(settings.PaymentVisibleMethodWxpayEnabled)
	updates[SettingKeyAdvancedSchedulerStickyWeightedEnabled] = strconv.FormatBool(settings.AdvancedSchedulerStickyWeightedEnabled)
	updates[SettingKeyAdvancedSchedulerSubscriptionPriorityEnabled] = strconv.FormatBool(settings.AdvancedSchedulerSubscriptionPriorityEnabled)
	updates[SettingKeyAdvancedSchedulerLBTopK] = settings.AdvancedSchedulerLBTopK
	updates[SettingKeyAdvancedSchedulerWeightPriority] = settings.AdvancedSchedulerWeightPriority
	updates[SettingKeyAdvancedSchedulerWeightLoad] = settings.AdvancedSchedulerWeightLoad
	updates[SettingKeyAdvancedSchedulerWeightQueue] = settings.AdvancedSchedulerWeightQueue
	updates[SettingKeyAdvancedSchedulerWeightErrorRate] = settings.AdvancedSchedulerWeightErrorRate
	updates[SettingKeyAdvancedSchedulerWeightTTFT] = settings.AdvancedSchedulerWeightTTFT
	updates[SettingKeyAdvancedSchedulerWeightReset] = settings.AdvancedSchedulerWeightReset
	updates[SettingKeyAdvancedSchedulerWeightQuotaHeadroom] = settings.AdvancedSchedulerWeightQuotaHeadroom
	updates[SettingKeyAdvancedSchedulerWeightPreviousResponse] = settings.AdvancedSchedulerWeightPreviousResponse
	updates[SettingKeyAdvancedSchedulerWeightSessionSticky] = settings.AdvancedSchedulerWeightSessionSticky
	if settings.OpenAIQuotaAutoPauseSettingsSet {
		opsAdvanced, err := s.buildOpsAdvancedSettingsWithQuotaAutoPause(ctx, settings.OpenAIQuotaAutoPauseSettings)
		if err != nil {
			return nil, err
		}
		raw, err := json.Marshal(opsAdvanced)
		if err != nil {
			return nil, fmt.Errorf("marshal ops advanced settings: %w", err)
		}
		updates[SettingKeyOpsAdvancedSettings] = string(raw)
	}

	// 余额、订阅到期与账号限额通知
	updates[SettingKeyBalanceLowNotifyEnabled] = strconv.FormatBool(settings.BalanceLowNotifyEnabled)
	updates[SettingKeyBalanceLowNotifyThreshold] = strconv.FormatFloat(settings.BalanceLowNotifyThreshold, 'f', 8, 64)
	updates[SettingKeyBalanceLowNotifyRechargeURL] = settings.BalanceLowNotifyRechargeURL
	updates[SettingKeySubscriptionExpiryNotifyEnabled] = strconv.FormatBool(settings.SubscriptionExpiryNotifyEnabled)
	updates[SettingKeyAccountQuotaNotifyEnabled] = strconv.FormatBool(settings.AccountQuotaNotifyEnabled)
	updates[SettingKeyAccountQuotaNotifyEmails] = MarshalNotifyEmails(settings.AccountQuotaNotifyEmails)

	// 页面功能开关：控制团队和数据共享相关页面的入口与访问。
	updates[SettingKeyTeamEnabled] = strconv.FormatBool(settings.TeamEnabled)
	updates[SettingKeyDataSharingEnabled] = strconv.FormatBool(settings.DataSharingEnabled)

	// 风控中心总开关：控制菜单入口和网关内容审计是否执行。
	updates[SettingKeyRiskControlEnabled] = strconv.FormatBool(settings.RiskControlEnabled)
	updates[SettingKeyCyberSessionBlockEnabled] = strconv.FormatBool(settings.CyberSessionBlockEnabled)
	if settings.CyberSessionBlockTTLSeconds > 0 {
		updates[SettingKeyCyberSessionBlockTTLSeconds] = strconv.Itoa(settings.CyberSessionBlockTTLSeconds)
	}

	// 系统全局平台限额：整体替换语义（null/缺省 = 不限制）。
	if settings.DefaultPlatformQuotas != nil {
		if err := validateDefaultPlatformQuotaMap(settings.DefaultPlatformQuotas); err != nil {
			return nil, err
		}
		blob, err := json.Marshal(settings.DefaultPlatformQuotas)
		if err != nil {
			return nil, fmt.Errorf("marshal default platform quotas: %w", err)
		}
		updates[SettingKeyDefaultPlatformQuotas] = string(blob)
	}
	if settings.AccountSchedulingThresholds != nil {
		normalized, err := validateAndNormalizeAccountSchedulingThresholds(settings.AccountSchedulingThresholds)
		if err != nil {
			return nil, err
		}
		blob, err := json.Marshal(normalized)
		if err != nil {
			return nil, fmt.Errorf("marshal account scheduling thresholds: %w", err)
		}
		updates[SettingKeyAccountSchedulingThresholds] = string(blob)
	}

	updates[SettingKeyAllowUserViewErrorRequests] = strconv.FormatBool(settings.AllowUserViewErrorRequests)

	return updates, nil
}

func defaultAccountSchedulingThresholds() map[string]int {
	return map[string]int{
		PlatformOpenAI:    100,
		PlatformAnthropic: 100,
		PlatformGrok:      100,
	}
}

func validateAndNormalizeAccountSchedulingThresholds(input map[string]int) (map[string]int, error) {
	normalized := defaultAccountSchedulingThresholds()
	for platform, value := range input {
		allowed := false
		for _, item := range AllowedSchedulingThresholdPlatforms {
			if item == platform {
				allowed = true
				break
			}
		}
		if !allowed {
			return nil, infraerrors.BadRequest("INVALID_ACCOUNT_SCHEDULING_THRESHOLDS", fmt.Sprintf("unknown platform %q", platform))
		}
		if value < 1 || value > 100 {
			return nil, infraerrors.BadRequest("INVALID_ACCOUNT_SCHEDULING_THRESHOLDS", "platform scheduling threshold must be between 1 and 100")
		}
		normalized[platform] = value
	}
	return normalized, nil
}

func parseAccountSchedulingThresholdsSetting(raw string) (map[string]int, error) {
	thresholds := defaultAccountSchedulingThresholds()
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return thresholds, nil
	}
	parsed := map[string]int{}
	if err := json.Unmarshal([]byte(raw), &parsed); err != nil {
		return thresholds, err
	}
	for _, platform := range AllowedSchedulingThresholdPlatforms {
		if value, ok := parsed[platform]; ok {
			thresholds[platform] = boundedIntOrDefault(value, 1, 100, 100)
		}
	}
	return thresholds, nil
}

func boundedIntOrDefault(value, minValue, maxValue, defaultValue int) int {
	if value < minValue || value > maxValue {
		return defaultValue
	}
	return value
}

func cloneAccountSchedulingThresholds(input map[string]int) map[string]int {
	if len(input) == 0 {
		return defaultAccountSchedulingThresholds()
	}
	cloned := make(map[string]int, len(input))
	for key, value := range input {
		cloned[key] = value
	}
	return cloned
}

// validateDefaultPlatformQuotaMap 校验 platform quota map 的合法性：
// 平台名须在 AllowedQuotaPlatforms 白名单内，每个非 nil 上限须 finite 且 >= 0。
// 系统层和 auth-source 层共用此 helper。
func validateDefaultPlatformQuotaMap(m map[string]*DefaultPlatformQuotaSetting) error {
	for platform, pq := range m {
		if !IsAllowedQuotaPlatform(platform) {
			return infraerrors.BadRequest("INVALID_DEFAULT_PLATFORM_QUOTA", fmt.Sprintf("unknown platform %q", platform))
		}
		if pq == nil {
			continue
		}
		for _, v := range []*float64{pq.DailyLimitUSD, pq.WeeklyLimitUSD, pq.MonthlyLimitUSD} {
			if v != nil && (*v < 0 || math.IsNaN(*v) || math.IsInf(*v, 0)) {
				return infraerrors.BadRequest("INVALID_DEFAULT_PLATFORM_QUOTA", "platform quota limit must be a finite non-negative number")
			}
		}
	}
	return nil
}

func (s *SettingService) buildAuthSourceDefaultUpdates(ctx context.Context, settings *AuthSourceDefaultSettings) (map[string]string, error) {
	if settings == nil {
		return nil, nil
	}

	for _, subscriptions := range [][]DefaultSubscriptionSetting{
		settings.Email.Subscriptions,
		settings.LinuxDo.Subscriptions,
		settings.OIDC.Subscriptions,
		settings.WeChat.Subscriptions,
		settings.GitHub.Subscriptions,
		settings.Google.Subscriptions,
		settings.DingTalk.Subscriptions,
	} {
		if err := s.validateDefaultSubscriptionPlans(ctx, subscriptions); err != nil {
			return nil, err
		}
	}

	// 校验各 auth source 的 platform quota map（改动 C：对等系统层校验）
	for _, pgs := range []struct {
		name string
		pq   map[string]*DefaultPlatformQuotaSetting
	}{
		{"email", settings.Email.PlatformQuotas},
		{"linuxdo", settings.LinuxDo.PlatformQuotas},
		{"oidc", settings.OIDC.PlatformQuotas},
		{"wechat", settings.WeChat.PlatformQuotas},
		{"github", settings.GitHub.PlatformQuotas},
		{"google", settings.Google.PlatformQuotas},
		{"dingtalk", settings.DingTalk.PlatformQuotas},
	} {
		if pgs.pq != nil {
			if err := validateDefaultPlatformQuotaMap(pgs.pq); err != nil {
				return nil, err
			}
		}
	}

	updates := make(map[string]string, 36)
	writeProviderDefaultGrantUpdates(updates, emailAuthSourceDefaultKeys, settings.Email)
	writeProviderDefaultGrantUpdates(updates, linuxDoAuthSourceDefaultKeys, settings.LinuxDo)
	writeProviderDefaultGrantUpdates(updates, oidcAuthSourceDefaultKeys, settings.OIDC)
	writeProviderDefaultGrantUpdates(updates, weChatAuthSourceDefaultKeys, settings.WeChat)
	writeProviderDefaultGrantUpdates(updates, gitHubAuthSourceDefaultKeys, settings.GitHub)
	writeProviderDefaultGrantUpdates(updates, googleAuthSourceDefaultKeys, settings.Google)
	writeProviderDefaultGrantUpdates(updates, dingTalkAuthSourceDefaultKeys, settings.DingTalk)
	updates[SettingKeyForceEmailOnThirdPartySignup] = strconv.FormatBool(settings.ForceEmailOnThirdPartySignup)
	return updates, nil
}

func (s *SettingService) refreshCachedSettings(settings *SystemSettings) {
	if settings == nil {
		return
	}

	// 先使 inflight singleflight 失效，再刷新缓存，缩小旧值覆盖新值的竞态窗口
	versionBoundsSF.Forget("version_bounds")
	versionBoundsCache.Store(&cachedVersionBounds{
		min:       settings.MinClaudeCodeVersion,
		max:       settings.MaxClaudeCodeVersion,
		expiresAt: time.Now().Add(versionBoundsCacheTTL).UnixNano(),
	})
	backendModeSF.Forget("backend_mode")
	backendModeCache.Store(&cachedBackendMode{
		value:     settings.BackendModeEnabled,
		expiresAt: time.Now().Add(backendModeCacheTTL).UnixNano(),
	})
	gatewayForwardingSF.Forget("gateway_forwarding")
	gatewayForwardingCache.Store(&cachedGatewayForwardingSettings{
		fingerprintUnification:           settings.EnableFingerprintUnification,
		metadataPassthrough:              settings.EnableMetadataPassthrough,
		cchSigning:                       settings.EnableCCHSigning,
		claudeOAuthSystemPromptInjection: settings.EnableClaudeOAuthSystemPromptInjection,
		claudeOAuthSystemPrompt:          settings.ClaudeOAuthSystemPrompt,
		claudeOAuthSystemPromptBlocks:    settings.ClaudeOAuthSystemPromptBlocks,
		anthropicCacheTTL1hInjection:     settings.EnableAnthropicCacheTTL1hInjection,
		rewriteMessageCacheControl:       settings.RewriteMessageCacheControl,
		clientDatelineNormalization:      settings.EnableClientDatelineNormalization,
		expiresAt:                        time.Now().Add(gatewayForwardingCacheTTL).UnixNano(),
	})
	s.antigravityUAVersionSF.Forget("antigravity_user_agent_version")
	antigravityUserAgentVersion := antigravity.NormalizeUserAgentVersion(settings.AntigravityUserAgentVersion)
	if antigravityUserAgentVersion == "" {
		antigravityUserAgentVersion = antigravity.GetDefaultUserAgentVersion()
	}
	s.antigravityUAVersionCache.Store(&cachedAntigravityUserAgentVersion{
		version:   antigravityUserAgentVersion,
		expiresAt: time.Now().Add(antigravityUserAgentVersionCacheTTL).UnixNano(),
	})
	s.openAICodexUASF.Forget("openai_codex_user_agent")
	codexUA := strings.TrimSpace(settings.OpenAICodexUserAgent)
	if codexUA == "" {
		codexUA = DefaultOpenAICodexUserAgent
	}
	s.openAICodexUACache.Store(&cachedOpenAICodexUserAgent{
		value:     codexUA,
		expiresAt: time.Now().Add(openAICodexUserAgentCacheTTL).UnixNano(),
	})
	userPromptReplacementCache.Store((*compiledUserPromptReplacementConfig)(nil))
	advancedSchedulerSettingSF.Forget("advanced_scheduler_settings")
	advancedSchedulerSettingCache.Store(&cachedAdvancedSchedulerSetting{
		stickyWeightedEnabled:       settings.AdvancedSchedulerStickyWeightedEnabled,
		subscriptionPriorityEnabled: settings.AdvancedSchedulerSubscriptionPriorityEnabled,
		lbTopKOverride:              parsePositiveIntOverride(settings.AdvancedSchedulerLBTopK),
		weightOverrides: parseAdvancedSchedulerWeightOverrides(map[string]string{
			SettingKeyAdvancedSchedulerWeightPriority:         settings.AdvancedSchedulerWeightPriority,
			SettingKeyAdvancedSchedulerWeightLoad:             settings.AdvancedSchedulerWeightLoad,
			SettingKeyAdvancedSchedulerWeightQueue:            settings.AdvancedSchedulerWeightQueue,
			SettingKeyAdvancedSchedulerWeightErrorRate:        settings.AdvancedSchedulerWeightErrorRate,
			SettingKeyAdvancedSchedulerWeightTTFT:             settings.AdvancedSchedulerWeightTTFT,
			SettingKeyAdvancedSchedulerWeightReset:            settings.AdvancedSchedulerWeightReset,
			SettingKeyAdvancedSchedulerWeightQuotaHeadroom:    settings.AdvancedSchedulerWeightQuotaHeadroom,
			SettingKeyAdvancedSchedulerWeightPreviousResponse: settings.AdvancedSchedulerWeightPreviousResponse,
			SettingKeyAdvancedSchedulerWeightSessionSticky:    settings.AdvancedSchedulerWeightSessionSticky,
		}),
		expiresAt: time.Now().Add(advancedSchedulerSettingCacheTTL).UnixNano(),
	})
	// 使配额自动暂停缓存失效，并让下一次读取触发重新加载。
	// 这里无法判断 ops_advanced_settings 是否也被修改，因此采用防御式处理：
	// 写入一个已过期条目，GetOpenAIQuotaAutoPauseSettings 会先返回旧值并触发异步刷新，
	// 不会阻塞后续请求。
	s.openAIQuotaAutoPauseSettingsSF.Forget(openAIQuotaAutoPauseSettingsRefreshKey)
	if settings.OpenAIQuotaAutoPauseSettingsSet {
		s.SetOpenAIQuotaAutoPauseSettings(settings.OpenAIQuotaAutoPauseSettings)
	} else if cached, _ := s.openAIQuotaAutoPauseSettingsCache.Load().(*cachedOpenAIQuotaAutoPauseSettings); cached != nil {
		s.openAIQuotaAutoPauseSettingsCache.Store(&cachedOpenAIQuotaAutoPauseSettings{
			settings:  cached.settings,
			expiresAt: 0,
		})
	}
	accountSchedulingThresholdsSF.Forget(SettingKeyAccountSchedulingThresholds)
	if settings.AccountSchedulingThresholds != nil {
		normalizedThresholds, err := validateAndNormalizeAccountSchedulingThresholds(settings.AccountSchedulingThresholds)
		if err != nil {
			normalizedThresholds = defaultAccountSchedulingThresholds()
		}
		accountSchedulingThresholdsCache.Store(&cachedAccountSchedulingThresholds{
			thresholds: cloneAccountSchedulingThresholds(normalizedThresholds),
			expiresAt:  time.Now().Add(accountSchedulingThresholdsCacheTTL).UnixNano(),
		})
	} else {
		// 请求体部分更新或省略该字段时清除缓存，使下次热点读取从数据库重新加载。
		accountSchedulingThresholdsCache.Store(&cachedAccountSchedulingThresholds{})
	}
	if s.cfg != nil {
		s.cfg.SetForwardedClientIPSettings(settings.APIKeyACLTrustForwardedIP, settings.ForwardedClientIPHeaders)
	}
	s.openAIAllowCodexPluginSF.Forget("openai_allow_codex_plugin_enabled")
	s.openAIAllowCodexPluginCache.Store(&cachedOpenAIAllowCodexPlugin{
		value:     settings.OpenAIAllowClaudeCodeCodexPlugin,
		expiresAt: time.Now().Add(openAIAllowCodexPluginCacheTTL).UnixNano(),
	})
	if s.onUpdate != nil {
		s.onUpdate() // Invalidate cache after settings update
	}
}

// defaultRewriteMessageCacheControl 返回消息 cache_control 改写的默认开关。
func (s *SettingService) defaultRewriteMessageCacheControl() bool {
	return false
}
