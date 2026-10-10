package identity

import (
	"strconv"
	"strings"

	"github.com/TokenFlux/TokenRouter/internal/identity/authconfig"
)

// AdminReadSettings 包含身份管理设置和凭据是否已配置的标志，公开端点使用 PublicAuthSettings。
type AdminReadSettings struct {
	AdminSettings
	AliyunCaptchaAccessKeySecretConfigured bool
	DingTalkConnectClientSecretConfigured  bool
	GitHubOAuthClientSecretConfigured      bool
	GoogleOAuthClientSecretConfigured      bool
	LinuxDoConnectClientSecretConfigured   bool
	OIDCConnectClientSecretConfigured      bool
	TencentCaptchaAppSecretKeyConfigured   bool
	TencentCaptchaCloudSecretIDConfigured  bool
	TencentCaptchaCloudSecretKeyConfigured bool
	TurnstileSecretKeyConfigured           bool
	WeChatConnectAppSecretConfigured       bool
	WeChatConnectMPAppSecretConfigured     bool
	WeChatConnectMobileAppSecretConfigured bool
	WeChatConnectOpenAppSecretConfigured   bool
}

// ReadAdminSettings 解析传入的身份设置，按各提供方规则处理缺省和空值。
func (s *OAuthSettings) ReadAdminSettings(settings map[string]string, defaultConcurrency func() int) *AdminReadSettings {
	emailVerifyEnabled := settings[SettingKeyEmailVerifyEnabled] == "true"
	result := &AdminReadSettings{}
	result.RegistrationEnabled = settings[SettingKeyRegistrationEnabled] == "true"
	result.EmailVerifyEnabled = emailVerifyEnabled
	result.RegistrationEmailSuffixWhitelist = ParseRegistrationEmailSuffixWhitelist(settings[SettingKeyRegistrationEmailSuffixWhitelist])
	result.RegistrationEmailNormalization = settings[SettingKeyRegistrationEmailNormalization] == "true"
	result.RegistrationEmailDomainQuotaEnabled = settings[SettingKeyRegistrationEmailDomainQuotaEnabled] == "true"
	result.UserEmailChangeEnabled = settings[SettingKeyUserEmailChangeEnabled] == "true"
	result.PasswordResetEnabled = emailVerifyEnabled && settings[SettingKeyPasswordResetEnabled] == "true"
	result.TotpEnabled = settings[SettingKeyTotpEnabled] == "true"
	result.SessionBindingEnabled = settings[SettingKeySessionBindingEnabled] == "true"
	result.StepUpEnabled = settings[SettingKeyStepUpEnabled] == "true"
	result.TurnstileEnabled = settings[SettingKeyTurnstileEnabled] == "true"
	result.TurnstileSiteKey = settings[SettingKeyTurnstileSiteKey]
	result.TurnstileSecretKeyConfigured = settings[SettingKeyTurnstileSecretKey] != ""
	result.TencentCaptchaEnabled = settings[SettingKeyTencentCaptchaEnabled] == "true"
	result.TencentCaptchaAppID = settings[SettingKeyTencentCaptchaAppID]
	result.TencentCaptchaAppSecretKeyConfigured = settings[SettingKeyTencentCaptchaAppSecretKey] != ""
	result.TencentCaptchaCloudSecretIDConfigured = settings[SettingKeyTencentCaptchaCloudSecretID] != ""
	result.TencentCaptchaCloudSecretKeyConfigured = settings[SettingKeyTencentCaptchaCloudSecretKey] != ""
	result.TencentCaptchaRegion = NormalizeTencentCaptchaRegion(settings[SettingKeyTencentCaptchaRegion])
	result.AliyunCaptchaEnabled = settings[SettingKeyAliyunCaptchaEnabled] == "true"
	result.AliyunCaptchaAccessKeyID = settings[SettingKeyAliyunCaptchaAccessKeyID]
	result.AliyunCaptchaAccessKeySecretConfigured = settings[SettingKeyAliyunCaptchaAccessKeySecret] != ""
	result.AliyunCaptchaSceneID = settings[SettingKeyAliyunCaptchaSceneID]
	result.AliyunCaptchaPrefix = settings[SettingKeyAliyunCaptchaPrefix]
	result.AliyunCaptchaRegion = NormalizeAliyunCaptchaRegion(settings[SettingKeyAliyunCaptchaRegion])
	result.DefaultUserAPIKeyLimit = DefaultUserAPIKeyLimit
	if concurrency, err := strconv.Atoi(settings[SettingKeyDefaultConcurrency]); err == nil {
		result.DefaultConcurrency = concurrency
	} else {
		result.DefaultConcurrency = defaultConcurrency()
	}
	if rpm, err := strconv.Atoi(settings[SettingKeyDefaultUserRPMLimit]); err == nil && rpm >= 0 {
		result.DefaultUserRPMLimit = rpm
	}
	if limit, err := strconv.Atoi(settings[SettingKeyDefaultUserAPIKeyLimit]); err == nil && IsValidUserAPIKeyLimit(limit) {
		result.DefaultUserAPIKeyLimit = limit
	}
	result.TurnstileSecretKey = settings[SettingKeyTurnstileSecretKey]
	result.TencentCaptchaAppSecretKey = settings[SettingKeyTencentCaptchaAppSecretKey]
	result.TencentCaptchaCloudSecretID = settings[SettingKeyTencentCaptchaCloudSecretID]
	result.TencentCaptchaCloudSecretKey = settings[SettingKeyTencentCaptchaCloudSecretKey]
	result.AliyunCaptchaAccessKeySecret = settings[SettingKeyAliyunCaptchaAccessKeySecret]
	linuxDoBase := authconfig.LinuxDoConnectConfig{}
	if s.defaults != nil {
		linuxDoBase = s.defaults.LinuxDo
	}
	if raw, ok := settings[SettingKeyLinuxDoConnectEnabled]; ok {
		result.LinuxDoConnectEnabled = raw == "true"
	} else {
		result.LinuxDoConnectEnabled = linuxDoBase.Enabled
	}
	if v, ok := settings[SettingKeyLinuxDoConnectClientID]; ok && strings.TrimSpace(v) != "" {
		result.LinuxDoConnectClientID = strings.TrimSpace(v)
	} else {
		result.LinuxDoConnectClientID = linuxDoBase.ClientID
	}
	if v, ok := settings[SettingKeyLinuxDoConnectRedirectURL]; ok && strings.TrimSpace(v) != "" {
		result.LinuxDoConnectRedirectURL = strings.TrimSpace(v)
	} else {
		result.LinuxDoConnectRedirectURL = linuxDoBase.RedirectURL
	}
	result.LinuxDoConnectClientSecret = strings.TrimSpace(settings[SettingKeyLinuxDoConnectClientSecret])
	if result.LinuxDoConnectClientSecret == "" {
		result.LinuxDoConnectClientSecret = strings.TrimSpace(linuxDoBase.ClientSecret)
	}
	result.LinuxDoConnectClientSecretConfigured = result.LinuxDoConnectClientSecret != ""
	dingTalkBase := authconfig.DingTalkConnectConfig{}
	if s.defaults != nil {
		dingTalkBase = s.defaults.DingTalk
	}
	if raw, ok := settings[SettingKeyDingTalkConnectEnabled]; ok {
		result.DingTalkConnectEnabled = raw == "true"
	} else {
		result.DingTalkConnectEnabled = dingTalkBase.Enabled
	}
	if v, ok := settings[SettingKeyDingTalkConnectClientID]; ok && strings.TrimSpace(v) != "" {
		result.DingTalkConnectClientID = strings.TrimSpace(v)
	} else {
		result.DingTalkConnectClientID = dingTalkBase.ClientID
	}
	if v, ok := settings[SettingKeyDingTalkConnectRedirectURL]; ok && strings.TrimSpace(v) != "" {
		result.DingTalkConnectRedirectURL = strings.TrimSpace(v)
	} else {
		result.DingTalkConnectRedirectURL = dingTalkBase.RedirectURL
	}
	result.DingTalkConnectClientSecret = strings.TrimSpace(settings[SettingKeyDingTalkConnectClientSecret])
	if result.DingTalkConnectClientSecret == "" {
		result.DingTalkConnectClientSecret = strings.TrimSpace(dingTalkBase.ClientSecret)
	}
	result.DingTalkConnectClientSecretConfigured = result.DingTalkConnectClientSecret != ""
	if v, ok := settings[SettingKeyDingTalkConnectCorpRestrictionPolicy]; ok && strings.TrimSpace(v) != "" {
		result.DingTalkConnectCorpRestrictionPolicy = strings.TrimSpace(v)
	} else {
		result.DingTalkConnectCorpRestrictionPolicy = dingTalkBase.CorpRestrictionPolicy
	}
	result.DingTalkConnectCorpRestrictionPolicy = SettingsCoerceDeprecatedDingTalkCorpPolicy(result.DingTalkConnectCorpRestrictionPolicy)
	if v, ok := settings[SettingKeyDingTalkConnectInternalCorpID]; ok && strings.TrimSpace(v) != "" {
		result.DingTalkConnectInternalCorpID = strings.TrimSpace(v)
	} else {
		result.DingTalkConnectInternalCorpID = dingTalkBase.InternalCorpID
	}
	if v, ok := settings[SettingKeyDingTalkConnectBypassRegistration]; ok && strings.TrimSpace(v) != "" {
		result.DingTalkConnectBypassRegistration = strings.EqualFold(strings.TrimSpace(v), "true")
	} else {
		result.DingTalkConnectBypassRegistration = dingTalkBase.BypassRegistration
	}
	if result.DingTalkConnectCorpRestrictionPolicy != "internal_only" {
		result.DingTalkConnectBypassRegistration = false
	}
	if v, ok := settings[SettingKeyDingTalkConnectSyncCorpEmail]; ok && strings.TrimSpace(v) != "" {
		result.DingTalkConnectSyncCorpEmail = strings.EqualFold(strings.TrimSpace(v), "true")
	} else {
		result.DingTalkConnectSyncCorpEmail = dingTalkBase.SyncCorpEmail
	}
	if v, ok := settings[SettingKeyDingTalkConnectSyncDisplayName]; ok && strings.TrimSpace(v) != "" {
		result.DingTalkConnectSyncDisplayName = strings.EqualFold(strings.TrimSpace(v), "true")
	} else {
		result.DingTalkConnectSyncDisplayName = dingTalkBase.SyncDisplayName
	}
	if v, ok := settings[SettingKeyDingTalkConnectSyncDept]; ok && strings.TrimSpace(v) != "" {
		result.DingTalkConnectSyncDept = strings.EqualFold(strings.TrimSpace(v), "true")
	} else {
		result.DingTalkConnectSyncDept = dingTalkBase.SyncDept
	}
	if result.DingTalkConnectCorpRestrictionPolicy != "internal_only" {
		result.DingTalkConnectSyncCorpEmail = false
		result.DingTalkConnectSyncDisplayName = false
		result.DingTalkConnectSyncDept = false
	}
	result.DingTalkConnectSyncCorpEmailAttrKey = oauthSettingsFirstNonEmpty(
		settings[SettingKeyDingTalkConnectSyncCorpEmailAttrKey],
		dingTalkBase.SyncCorpEmailAttrKey,
		"dingtalk_email",
	)
	result.DingTalkConnectSyncDisplayNameAttrKey = oauthSettingsFirstNonEmpty(
		settings[SettingKeyDingTalkConnectSyncDisplayNameAttrKey],
		dingTalkBase.SyncDisplayNameAttrKey,
		"dingtalk_name",
	)
	result.DingTalkConnectSyncDeptAttrKey = oauthSettingsFirstNonEmpty(
		settings[SettingKeyDingTalkConnectSyncDeptAttrKey],
		dingTalkBase.SyncDeptAttrKey,
		"dingtalk_department",
	)
	result.DingTalkConnectSyncCorpEmailAttrName = oauthSettingsFirstNonEmpty(
		settings[SettingKeyDingTalkConnectSyncCorpEmailAttrName],
		dingTalkBase.SyncCorpEmailAttrName,
		"钉钉企业邮箱",
	)
	result.DingTalkConnectSyncDisplayNameAttrName = oauthSettingsFirstNonEmpty(
		settings[SettingKeyDingTalkConnectSyncDisplayNameAttrName],
		dingTalkBase.SyncDisplayNameAttrName,
		"钉钉姓名",
	)
	result.DingTalkConnectSyncDeptAttrName = oauthSettingsFirstNonEmpty(
		settings[SettingKeyDingTalkConnectSyncDeptAttrName],
		dingTalkBase.SyncDeptAttrName,
		"钉钉部门",
	)
	oidcBase := authconfig.OIDCConnectConfig{}
	if s.defaults != nil {
		oidcBase = s.defaults.OIDC
	}
	if raw, ok := settings[SettingKeyOIDCConnectEnabled]; ok {
		result.OIDCConnectEnabled = raw == "true"
	} else {
		result.OIDCConnectEnabled = oidcBase.Enabled
	}
	result.OIDCConnectProviderName = oauthSettingsFirstNonEmpty(settings[SettingKeyOIDCConnectProviderName], oidcBase.ProviderName)
	if result.OIDCConnectProviderName == "" {
		result.OIDCConnectProviderName = "OIDC"
	}
	result.OIDCConnectClientID = oauthSettingsFirstNonEmpty(settings[SettingKeyOIDCConnectClientID], oidcBase.ClientID)
	result.OIDCConnectIssuerURL = oauthSettingsFirstNonEmpty(settings[SettingKeyOIDCConnectIssuerURL], oidcBase.IssuerURL)
	result.OIDCConnectDiscoveryURL = oauthSettingsFirstNonEmpty(settings[SettingKeyOIDCConnectDiscoveryURL], oidcBase.DiscoveryURL)
	result.OIDCConnectAuthorizeURL = oauthSettingsFirstNonEmpty(settings[SettingKeyOIDCConnectAuthorizeURL], oidcBase.AuthorizeURL)
	result.OIDCConnectTokenURL = oauthSettingsFirstNonEmpty(settings[SettingKeyOIDCConnectTokenURL], oidcBase.TokenURL)
	result.OIDCConnectUserInfoURL = oauthSettingsFirstNonEmpty(settings[SettingKeyOIDCConnectUserInfoURL], oidcBase.UserInfoURL)
	result.OIDCConnectJWKSURL = oauthSettingsFirstNonEmpty(settings[SettingKeyOIDCConnectJWKSURL], oidcBase.JWKSURL)
	result.OIDCConnectScopes = oauthSettingsFirstNonEmpty(settings[SettingKeyOIDCConnectScopes], oidcBase.Scopes)
	result.OIDCConnectRedirectURL = oauthSettingsFirstNonEmpty(settings[SettingKeyOIDCConnectRedirectURL], oidcBase.RedirectURL)
	result.OIDCConnectFrontendRedirectURL = oauthSettingsFirstNonEmpty(
		settings[SettingKeyOIDCConnectFrontendRedirectURL],
		oidcBase.FrontendRedirectURL,
	)
	if v, ok := settings[SettingKeyOIDCConnectTokenAuthMethod]; ok && strings.TrimSpace(v) != "" {
		result.OIDCConnectTokenAuthMethod = strings.ToLower(strings.TrimSpace(v))
	} else {
		result.OIDCConnectTokenAuthMethod = strings.ToLower(strings.TrimSpace(oidcBase.TokenAuthMethod))
	}
	if raw, ok := settings[SettingKeyOIDCConnectUsePKCE]; ok {
		result.OIDCConnectUsePKCE = raw == "true"
	} else {
		result.OIDCConnectUsePKCE = SettingsOidcUsePKCECompatibilityDefault(oidcBase)
	}
	if raw, ok := settings[SettingKeyOIDCConnectValidateIDToken]; ok {
		result.OIDCConnectValidateIDToken = raw == "true"
	} else {
		result.OIDCConnectValidateIDToken = SettingsOidcValidateIDTokenCompatibilityDefault(oidcBase)
	}
	result.OIDCConnectAllowedSigningAlgs = oauthSettingsFirstNonEmpty(
		settings[SettingKeyOIDCConnectAllowedSigningAlgs],
		oidcBase.AllowedSigningAlgs,
	)
	clockSkewSet := false
	if raw, ok := settings[SettingKeyOIDCConnectClockSkewSeconds]; ok && strings.TrimSpace(raw) != "" {
		if parsed, err := strconv.Atoi(strings.TrimSpace(raw)); err == nil {
			result.OIDCConnectClockSkewSeconds = parsed
			clockSkewSet = true
		}
	}
	if !clockSkewSet {
		result.OIDCConnectClockSkewSeconds = oidcBase.ClockSkewSeconds
	}
	if !clockSkewSet && result.OIDCConnectClockSkewSeconds == 0 {
		result.OIDCConnectClockSkewSeconds = 120
	}
	if raw, ok := settings[SettingKeyOIDCConnectRequireEmailVerified]; ok {
		result.OIDCConnectRequireEmailVerified = raw == "true"
	} else {
		result.OIDCConnectRequireEmailVerified = oidcBase.RequireEmailVerified
	}
	if v, ok := settings[SettingKeyOIDCConnectUserInfoEmailPath]; ok {
		result.OIDCConnectUserInfoEmailPath = strings.TrimSpace(v)
	} else {
		result.OIDCConnectUserInfoEmailPath = strings.TrimSpace(oidcBase.UserInfoEmailPath)
	}
	if v, ok := settings[SettingKeyOIDCConnectUserInfoIDPath]; ok {
		result.OIDCConnectUserInfoIDPath = strings.TrimSpace(v)
	} else {
		result.OIDCConnectUserInfoIDPath = strings.TrimSpace(oidcBase.UserInfoIDPath)
	}
	if v, ok := settings[SettingKeyOIDCConnectUserInfoUsernamePath]; ok {
		result.OIDCConnectUserInfoUsernamePath = strings.TrimSpace(v)
	} else {
		result.OIDCConnectUserInfoUsernamePath = strings.TrimSpace(oidcBase.UserInfoUsernamePath)
	}
	result.OIDCConnectClientSecret = strings.TrimSpace(settings[SettingKeyOIDCConnectClientSecret])
	if result.OIDCConnectClientSecret == "" {
		result.OIDCConnectClientSecret = strings.TrimSpace(oidcBase.ClientSecret)
	}
	result.OIDCConnectClientSecretConfigured = result.OIDCConnectClientSecret != ""
	gitHubEffective := s.EffectiveEmailOAuthConfig(settings, "github")
	result.GitHubOAuthEnabled = gitHubEffective.Enabled
	result.GitHubOAuthClientID = strings.TrimSpace(gitHubEffective.ClientID)
	result.GitHubOAuthClientSecret = strings.TrimSpace(gitHubEffective.ClientSecret)
	result.GitHubOAuthClientSecretConfigured = result.GitHubOAuthClientSecret != ""
	result.GitHubOAuthRedirectURL = strings.TrimSpace(gitHubEffective.RedirectURL)
	result.GitHubOAuthFrontendRedirectURL = strings.TrimSpace(gitHubEffective.FrontendRedirectURL)
	googleEffective := s.EffectiveEmailOAuthConfig(settings, "google")
	result.GoogleOAuthEnabled = googleEffective.Enabled
	result.GoogleOneTapEnabled = settings[SettingKeyGoogleOneTapEnabled] == "true"
	result.GoogleOAuthClientID = strings.TrimSpace(googleEffective.ClientID)
	result.GoogleOAuthClientSecret = strings.TrimSpace(googleEffective.ClientSecret)
	result.GoogleOAuthClientSecretConfigured = result.GoogleOAuthClientSecret != ""
	result.GoogleOAuthRedirectURL = strings.TrimSpace(googleEffective.RedirectURL)
	result.GoogleOAuthFrontendRedirectURL = strings.TrimSpace(googleEffective.FrontendRedirectURL)
	weChatEffective := s.EffectiveWeChatConnectOAuthConfig(settings)
	result.WeChatConnectEnabled = weChatEffective.Enabled
	result.WeChatConnectAppID = weChatEffective.LegacyAppID
	result.WeChatConnectAppSecret = weChatEffective.LegacyAppSecret
	result.WeChatConnectAppSecretConfigured = weChatEffective.LegacyAppSecret != ""
	result.WeChatConnectOpenAppID = weChatEffective.OpenAppID
	result.WeChatConnectOpenAppSecret = weChatEffective.OpenAppSecret
	result.WeChatConnectOpenAppSecretConfigured = weChatEffective.OpenAppSecret != ""
	result.WeChatConnectMPAppID = weChatEffective.MPAppID
	result.WeChatConnectMPAppSecret = weChatEffective.MPAppSecret
	result.WeChatConnectMPAppSecretConfigured = weChatEffective.MPAppSecret != ""
	result.WeChatConnectMobileAppID = weChatEffective.MobileAppID
	result.WeChatConnectMobileAppSecret = weChatEffective.MobileAppSecret
	result.WeChatConnectMobileAppSecretConfigured = weChatEffective.MobileAppSecret != ""
	result.WeChatConnectOpenEnabled = weChatEffective.OpenEnabled
	result.WeChatConnectMPEnabled = weChatEffective.MPEnabled
	result.WeChatConnectMobileEnabled = weChatEffective.MobileEnabled
	result.WeChatConnectMode = weChatEffective.Mode
	result.WeChatConnectScopes = weChatEffective.Scopes
	result.WeChatConnectRedirectURL = weChatEffective.RedirectURL
	result.WeChatConnectFrontendRedirectURL = weChatEffective.FrontendRedirectURL
	return result
}
