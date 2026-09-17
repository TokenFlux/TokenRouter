package composite

import "github.com/TokenFlux/TokenRouter/internal/identity"

// IdentityAdminSettings 仅转换所属模块值，不读取设置或发布状态。
func (s *Snapshot) IdentityAdminSettings() identity.AdminSettings {
	return identity.AdminSettings{
		AliyunCaptchaAccessKeyID:               s.AliyunCaptchaAccessKeyID,
		AliyunCaptchaAccessKeySecret:           s.AliyunCaptchaAccessKeySecret,
		AliyunCaptchaEnabled:                   s.AliyunCaptchaEnabled,
		AliyunCaptchaPrefix:                    s.AliyunCaptchaPrefix,
		AliyunCaptchaRegion:                    s.AliyunCaptchaRegion,
		AliyunCaptchaSceneID:                   s.AliyunCaptchaSceneID,
		DefaultConcurrency:                     s.DefaultConcurrency,
		DefaultUserAPIKeyLimit:                 s.DefaultUserAPIKeyLimit,
		DefaultUserRPMLimit:                    s.DefaultUserRPMLimit,
		DingTalkConnectBypassRegistration:      s.DingTalkConnectBypassRegistration,
		DingTalkConnectClientID:                s.DingTalkConnectClientID,
		DingTalkConnectClientSecret:            s.DingTalkConnectClientSecret,
		DingTalkConnectCorpRestrictionPolicy:   s.DingTalkConnectCorpRestrictionPolicy,
		DingTalkConnectEnabled:                 s.DingTalkConnectEnabled,
		DingTalkConnectInternalCorpID:          s.DingTalkConnectInternalCorpID,
		DingTalkConnectRedirectURL:             s.DingTalkConnectRedirectURL,
		DingTalkConnectSyncCorpEmail:           s.DingTalkConnectSyncCorpEmail,
		DingTalkConnectSyncCorpEmailAttrKey:    s.DingTalkConnectSyncCorpEmailAttrKey,
		DingTalkConnectSyncCorpEmailAttrName:   s.DingTalkConnectSyncCorpEmailAttrName,
		DingTalkConnectSyncDept:                s.DingTalkConnectSyncDept,
		DingTalkConnectSyncDeptAttrKey:         s.DingTalkConnectSyncDeptAttrKey,
		DingTalkConnectSyncDeptAttrName:        s.DingTalkConnectSyncDeptAttrName,
		DingTalkConnectSyncDisplayName:         s.DingTalkConnectSyncDisplayName,
		DingTalkConnectSyncDisplayNameAttrKey:  s.DingTalkConnectSyncDisplayNameAttrKey,
		DingTalkConnectSyncDisplayNameAttrName: s.DingTalkConnectSyncDisplayNameAttrName,
		EmailVerifyEnabled:                     s.EmailVerifyEnabled,
		GitHubOAuthClientID:                    s.GitHubOAuthClientID,
		GitHubOAuthClientSecret:                s.GitHubOAuthClientSecret,
		GitHubOAuthEnabled:                     s.GitHubOAuthEnabled,
		GitHubOAuthFrontendRedirectURL:         s.GitHubOAuthFrontendRedirectURL,
		GitHubOAuthRedirectURL:                 s.GitHubOAuthRedirectURL,
		GoogleOAuthClientID:                    s.GoogleOAuthClientID,
		GoogleOAuthClientSecret:                s.GoogleOAuthClientSecret,
		GoogleOAuthEnabled:                     s.GoogleOAuthEnabled,
		GoogleOAuthFrontendRedirectURL:         s.GoogleOAuthFrontendRedirectURL,
		GoogleOAuthRedirectURL:                 s.GoogleOAuthRedirectURL,
		GoogleOneTapEnabled:                    s.GoogleOneTapEnabled,
		LinuxDoConnectClientID:                 s.LinuxDoConnectClientID,
		LinuxDoConnectClientSecret:             s.LinuxDoConnectClientSecret,
		LinuxDoConnectEnabled:                  s.LinuxDoConnectEnabled,
		LinuxDoConnectRedirectURL:              s.LinuxDoConnectRedirectURL,
		OIDCConnectAllowedSigningAlgs:          s.OIDCConnectAllowedSigningAlgs,
		OIDCConnectAuthorizeURL:                s.OIDCConnectAuthorizeURL,
		OIDCConnectClientID:                    s.OIDCConnectClientID,
		OIDCConnectClientSecret:                s.OIDCConnectClientSecret,
		OIDCConnectClockSkewSeconds:            s.OIDCConnectClockSkewSeconds,
		OIDCConnectDiscoveryURL:                s.OIDCConnectDiscoveryURL,
		OIDCConnectEnabled:                     s.OIDCConnectEnabled,
		OIDCConnectFrontendRedirectURL:         s.OIDCConnectFrontendRedirectURL,
		OIDCConnectIssuerURL:                   s.OIDCConnectIssuerURL,
		OIDCConnectJWKSURL:                     s.OIDCConnectJWKSURL,
		OIDCConnectProviderName:                s.OIDCConnectProviderName,
		OIDCConnectRedirectURL:                 s.OIDCConnectRedirectURL,
		OIDCConnectRequireEmailVerified:        s.OIDCConnectRequireEmailVerified,
		OIDCConnectScopes:                      s.OIDCConnectScopes,
		OIDCConnectTokenAuthMethod:             s.OIDCConnectTokenAuthMethod,
		OIDCConnectTokenURL:                    s.OIDCConnectTokenURL,
		OIDCConnectUsePKCE:                     s.OIDCConnectUsePKCE,
		OIDCConnectUserInfoEmailPath:           s.OIDCConnectUserInfoEmailPath,
		OIDCConnectUserInfoIDPath:              s.OIDCConnectUserInfoIDPath,
		OIDCConnectUserInfoURL:                 s.OIDCConnectUserInfoURL,
		OIDCConnectUserInfoUsernamePath:        s.OIDCConnectUserInfoUsernamePath,
		OIDCConnectValidateIDToken:             s.OIDCConnectValidateIDToken,
		PasswordResetEnabled:                   s.PasswordResetEnabled,
		RegistrationEmailDomainQuotaEnabled:    s.RegistrationEmailDomainQuotaEnabled,
		RegistrationEmailNormalization:         s.RegistrationEmailNormalization,
		RegistrationEmailSuffixWhitelist:       s.RegistrationEmailSuffixWhitelist,
		RegistrationEnabled:                    s.RegistrationEnabled,
		SessionBindingEnabled:                  s.SessionBindingEnabled,
		StepUpEnabled:                          s.StepUpEnabled,
		TencentCaptchaAppID:                    s.TencentCaptchaAppID,
		TencentCaptchaAppSecretKey:             s.TencentCaptchaAppSecretKey,
		TencentCaptchaCloudSecretID:            s.TencentCaptchaCloudSecretID,
		TencentCaptchaCloudSecretKey:           s.TencentCaptchaCloudSecretKey,
		TencentCaptchaEnabled:                  s.TencentCaptchaEnabled,
		TencentCaptchaRegion:                   s.TencentCaptchaRegion,
		TotpEnabled:                            s.TotpEnabled,
		TurnstileEnabled:                       s.TurnstileEnabled,
		TurnstileSecretKey:                     s.TurnstileSecretKey,
		TurnstileSiteKey:                       s.TurnstileSiteKey,
		UserEmailChangeEnabled:                 s.UserEmailChangeEnabled,
		WeChatConnectAppID:                     s.WeChatConnectAppID,
		WeChatConnectAppSecret:                 s.WeChatConnectAppSecret,
		WeChatConnectEnabled:                   s.WeChatConnectEnabled,
		WeChatConnectFrontendRedirectURL:       s.WeChatConnectFrontendRedirectURL,
		WeChatConnectMPAppID:                   s.WeChatConnectMPAppID,
		WeChatConnectMPAppSecret:               s.WeChatConnectMPAppSecret,
		WeChatConnectMPEnabled:                 s.WeChatConnectMPEnabled,
		WeChatConnectMobileAppID:               s.WeChatConnectMobileAppID,
		WeChatConnectMobileAppSecret:           s.WeChatConnectMobileAppSecret,
		WeChatConnectMobileEnabled:             s.WeChatConnectMobileEnabled,
		WeChatConnectMode:                      s.WeChatConnectMode,
		WeChatConnectOpenAppID:                 s.WeChatConnectOpenAppID,
		WeChatConnectOpenAppSecret:             s.WeChatConnectOpenAppSecret,
		WeChatConnectOpenEnabled:               s.WeChatConnectOpenEnabled,
		WeChatConnectRedirectURL:               s.WeChatConnectRedirectURL,
		WeChatConnectScopes:                    s.WeChatConnectScopes,
	}
}

// ApplyIdentityAdminSettings 仅转换所属模块值，不读取设置或发布状态。
func (s *Snapshot) ApplyIdentityAdminSettings(value identity.AdminSettings) {
	s.AliyunCaptchaAccessKeyID = value.AliyunCaptchaAccessKeyID
	s.AliyunCaptchaAccessKeySecret = value.AliyunCaptchaAccessKeySecret
	s.AliyunCaptchaEnabled = value.AliyunCaptchaEnabled
	s.AliyunCaptchaPrefix = value.AliyunCaptchaPrefix
	s.AliyunCaptchaRegion = value.AliyunCaptchaRegion
	s.AliyunCaptchaSceneID = value.AliyunCaptchaSceneID
	s.DefaultConcurrency = value.DefaultConcurrency
	s.DefaultUserAPIKeyLimit = value.DefaultUserAPIKeyLimit
	s.DefaultUserRPMLimit = value.DefaultUserRPMLimit
	s.DingTalkConnectBypassRegistration = value.DingTalkConnectBypassRegistration
	s.DingTalkConnectClientID = value.DingTalkConnectClientID
	s.DingTalkConnectClientSecret = value.DingTalkConnectClientSecret
	s.DingTalkConnectCorpRestrictionPolicy = value.DingTalkConnectCorpRestrictionPolicy
	s.DingTalkConnectEnabled = value.DingTalkConnectEnabled
	s.DingTalkConnectInternalCorpID = value.DingTalkConnectInternalCorpID
	s.DingTalkConnectRedirectURL = value.DingTalkConnectRedirectURL
	s.DingTalkConnectSyncCorpEmail = value.DingTalkConnectSyncCorpEmail
	s.DingTalkConnectSyncCorpEmailAttrKey = value.DingTalkConnectSyncCorpEmailAttrKey
	s.DingTalkConnectSyncCorpEmailAttrName = value.DingTalkConnectSyncCorpEmailAttrName
	s.DingTalkConnectSyncDept = value.DingTalkConnectSyncDept
	s.DingTalkConnectSyncDeptAttrKey = value.DingTalkConnectSyncDeptAttrKey
	s.DingTalkConnectSyncDeptAttrName = value.DingTalkConnectSyncDeptAttrName
	s.DingTalkConnectSyncDisplayName = value.DingTalkConnectSyncDisplayName
	s.DingTalkConnectSyncDisplayNameAttrKey = value.DingTalkConnectSyncDisplayNameAttrKey
	s.DingTalkConnectSyncDisplayNameAttrName = value.DingTalkConnectSyncDisplayNameAttrName
	s.EmailVerifyEnabled = value.EmailVerifyEnabled
	s.GitHubOAuthClientID = value.GitHubOAuthClientID
	s.GitHubOAuthClientSecret = value.GitHubOAuthClientSecret
	s.GitHubOAuthEnabled = value.GitHubOAuthEnabled
	s.GitHubOAuthFrontendRedirectURL = value.GitHubOAuthFrontendRedirectURL
	s.GitHubOAuthRedirectURL = value.GitHubOAuthRedirectURL
	s.GoogleOAuthClientID = value.GoogleOAuthClientID
	s.GoogleOAuthClientSecret = value.GoogleOAuthClientSecret
	s.GoogleOAuthEnabled = value.GoogleOAuthEnabled
	s.GoogleOAuthFrontendRedirectURL = value.GoogleOAuthFrontendRedirectURL
	s.GoogleOAuthRedirectURL = value.GoogleOAuthRedirectURL
	s.GoogleOneTapEnabled = value.GoogleOneTapEnabled
	s.LinuxDoConnectClientID = value.LinuxDoConnectClientID
	s.LinuxDoConnectClientSecret = value.LinuxDoConnectClientSecret
	s.LinuxDoConnectEnabled = value.LinuxDoConnectEnabled
	s.LinuxDoConnectRedirectURL = value.LinuxDoConnectRedirectURL
	s.OIDCConnectAllowedSigningAlgs = value.OIDCConnectAllowedSigningAlgs
	s.OIDCConnectAuthorizeURL = value.OIDCConnectAuthorizeURL
	s.OIDCConnectClientID = value.OIDCConnectClientID
	s.OIDCConnectClientSecret = value.OIDCConnectClientSecret
	s.OIDCConnectClockSkewSeconds = value.OIDCConnectClockSkewSeconds
	s.OIDCConnectDiscoveryURL = value.OIDCConnectDiscoveryURL
	s.OIDCConnectEnabled = value.OIDCConnectEnabled
	s.OIDCConnectFrontendRedirectURL = value.OIDCConnectFrontendRedirectURL
	s.OIDCConnectIssuerURL = value.OIDCConnectIssuerURL
	s.OIDCConnectJWKSURL = value.OIDCConnectJWKSURL
	s.OIDCConnectProviderName = value.OIDCConnectProviderName
	s.OIDCConnectRedirectURL = value.OIDCConnectRedirectURL
	s.OIDCConnectRequireEmailVerified = value.OIDCConnectRequireEmailVerified
	s.OIDCConnectScopes = value.OIDCConnectScopes
	s.OIDCConnectTokenAuthMethod = value.OIDCConnectTokenAuthMethod
	s.OIDCConnectTokenURL = value.OIDCConnectTokenURL
	s.OIDCConnectUsePKCE = value.OIDCConnectUsePKCE
	s.OIDCConnectUserInfoEmailPath = value.OIDCConnectUserInfoEmailPath
	s.OIDCConnectUserInfoIDPath = value.OIDCConnectUserInfoIDPath
	s.OIDCConnectUserInfoURL = value.OIDCConnectUserInfoURL
	s.OIDCConnectUserInfoUsernamePath = value.OIDCConnectUserInfoUsernamePath
	s.OIDCConnectValidateIDToken = value.OIDCConnectValidateIDToken
	s.PasswordResetEnabled = value.PasswordResetEnabled
	s.RegistrationEmailDomainQuotaEnabled = value.RegistrationEmailDomainQuotaEnabled
	s.RegistrationEmailNormalization = value.RegistrationEmailNormalization
	s.RegistrationEmailSuffixWhitelist = value.RegistrationEmailSuffixWhitelist
	s.RegistrationEnabled = value.RegistrationEnabled
	s.SessionBindingEnabled = value.SessionBindingEnabled
	s.StepUpEnabled = value.StepUpEnabled
	s.TencentCaptchaAppID = value.TencentCaptchaAppID
	s.TencentCaptchaAppSecretKey = value.TencentCaptchaAppSecretKey
	s.TencentCaptchaCloudSecretID = value.TencentCaptchaCloudSecretID
	s.TencentCaptchaCloudSecretKey = value.TencentCaptchaCloudSecretKey
	s.TencentCaptchaEnabled = value.TencentCaptchaEnabled
	s.TencentCaptchaRegion = value.TencentCaptchaRegion
	s.TotpEnabled = value.TotpEnabled
	s.TurnstileEnabled = value.TurnstileEnabled
	s.TurnstileSecretKey = value.TurnstileSecretKey
	s.TurnstileSiteKey = value.TurnstileSiteKey
	s.UserEmailChangeEnabled = value.UserEmailChangeEnabled
	s.WeChatConnectAppID = value.WeChatConnectAppID
	s.WeChatConnectAppSecret = value.WeChatConnectAppSecret
	s.WeChatConnectEnabled = value.WeChatConnectEnabled
	s.WeChatConnectFrontendRedirectURL = value.WeChatConnectFrontendRedirectURL
	s.WeChatConnectMPAppID = value.WeChatConnectMPAppID
	s.WeChatConnectMPAppSecret = value.WeChatConnectMPAppSecret
	s.WeChatConnectMPEnabled = value.WeChatConnectMPEnabled
	s.WeChatConnectMobileAppID = value.WeChatConnectMobileAppID
	s.WeChatConnectMobileAppSecret = value.WeChatConnectMobileAppSecret
	s.WeChatConnectMobileEnabled = value.WeChatConnectMobileEnabled
	s.WeChatConnectMode = value.WeChatConnectMode
	s.WeChatConnectOpenAppID = value.WeChatConnectOpenAppID
	s.WeChatConnectOpenAppSecret = value.WeChatConnectOpenAppSecret
	s.WeChatConnectOpenEnabled = value.WeChatConnectOpenEnabled
	s.WeChatConnectRedirectURL = value.WeChatConnectRedirectURL
	s.WeChatConnectScopes = value.WeChatConnectScopes
}

// ApplyIdentityAdminReadSettings 仅转换所属模块值，不读取设置或发布状态。
func (s *Snapshot) ApplyIdentityAdminReadSettings(value *identity.AdminReadSettings) {
	s.AliyunCaptchaAccessKeyID = value.AliyunCaptchaAccessKeyID
	s.AliyunCaptchaAccessKeySecret = value.AliyunCaptchaAccessKeySecret
	s.AliyunCaptchaAccessKeySecretConfigured = value.AliyunCaptchaAccessKeySecretConfigured
	s.AliyunCaptchaEnabled = value.AliyunCaptchaEnabled
	s.AliyunCaptchaPrefix = value.AliyunCaptchaPrefix
	s.AliyunCaptchaRegion = value.AliyunCaptchaRegion
	s.AliyunCaptchaSceneID = value.AliyunCaptchaSceneID
	s.DefaultConcurrency = value.DefaultConcurrency
	s.DefaultUserAPIKeyLimit = value.DefaultUserAPIKeyLimit
	s.DefaultUserRPMLimit = value.DefaultUserRPMLimit
	s.DingTalkConnectBypassRegistration = value.DingTalkConnectBypassRegistration
	s.DingTalkConnectClientID = value.DingTalkConnectClientID
	s.DingTalkConnectClientSecret = value.DingTalkConnectClientSecret
	s.DingTalkConnectClientSecretConfigured = value.DingTalkConnectClientSecretConfigured
	s.DingTalkConnectCorpRestrictionPolicy = value.DingTalkConnectCorpRestrictionPolicy
	s.DingTalkConnectEnabled = value.DingTalkConnectEnabled
	s.DingTalkConnectInternalCorpID = value.DingTalkConnectInternalCorpID
	s.DingTalkConnectRedirectURL = value.DingTalkConnectRedirectURL
	s.DingTalkConnectSyncCorpEmail = value.DingTalkConnectSyncCorpEmail
	s.DingTalkConnectSyncCorpEmailAttrKey = value.DingTalkConnectSyncCorpEmailAttrKey
	s.DingTalkConnectSyncCorpEmailAttrName = value.DingTalkConnectSyncCorpEmailAttrName
	s.DingTalkConnectSyncDept = value.DingTalkConnectSyncDept
	s.DingTalkConnectSyncDeptAttrKey = value.DingTalkConnectSyncDeptAttrKey
	s.DingTalkConnectSyncDeptAttrName = value.DingTalkConnectSyncDeptAttrName
	s.DingTalkConnectSyncDisplayName = value.DingTalkConnectSyncDisplayName
	s.DingTalkConnectSyncDisplayNameAttrKey = value.DingTalkConnectSyncDisplayNameAttrKey
	s.DingTalkConnectSyncDisplayNameAttrName = value.DingTalkConnectSyncDisplayNameAttrName
	s.EmailVerifyEnabled = value.EmailVerifyEnabled
	s.GitHubOAuthClientID = value.GitHubOAuthClientID
	s.GitHubOAuthClientSecret = value.GitHubOAuthClientSecret
	s.GitHubOAuthClientSecretConfigured = value.GitHubOAuthClientSecretConfigured
	s.GitHubOAuthEnabled = value.GitHubOAuthEnabled
	s.GitHubOAuthFrontendRedirectURL = value.GitHubOAuthFrontendRedirectURL
	s.GitHubOAuthRedirectURL = value.GitHubOAuthRedirectURL
	s.GoogleOAuthClientID = value.GoogleOAuthClientID
	s.GoogleOAuthClientSecret = value.GoogleOAuthClientSecret
	s.GoogleOAuthClientSecretConfigured = value.GoogleOAuthClientSecretConfigured
	s.GoogleOAuthEnabled = value.GoogleOAuthEnabled
	s.GoogleOAuthFrontendRedirectURL = value.GoogleOAuthFrontendRedirectURL
	s.GoogleOAuthRedirectURL = value.GoogleOAuthRedirectURL
	s.GoogleOneTapEnabled = value.GoogleOneTapEnabled
	s.LinuxDoConnectClientID = value.LinuxDoConnectClientID
	s.LinuxDoConnectClientSecret = value.LinuxDoConnectClientSecret
	s.LinuxDoConnectClientSecretConfigured = value.LinuxDoConnectClientSecretConfigured
	s.LinuxDoConnectEnabled = value.LinuxDoConnectEnabled
	s.LinuxDoConnectRedirectURL = value.LinuxDoConnectRedirectURL
	s.OIDCConnectAllowedSigningAlgs = value.OIDCConnectAllowedSigningAlgs
	s.OIDCConnectAuthorizeURL = value.OIDCConnectAuthorizeURL
	s.OIDCConnectClientID = value.OIDCConnectClientID
	s.OIDCConnectClientSecret = value.OIDCConnectClientSecret
	s.OIDCConnectClientSecretConfigured = value.OIDCConnectClientSecretConfigured
	s.OIDCConnectClockSkewSeconds = value.OIDCConnectClockSkewSeconds
	s.OIDCConnectDiscoveryURL = value.OIDCConnectDiscoveryURL
	s.OIDCConnectEnabled = value.OIDCConnectEnabled
	s.OIDCConnectFrontendRedirectURL = value.OIDCConnectFrontendRedirectURL
	s.OIDCConnectIssuerURL = value.OIDCConnectIssuerURL
	s.OIDCConnectJWKSURL = value.OIDCConnectJWKSURL
	s.OIDCConnectProviderName = value.OIDCConnectProviderName
	s.OIDCConnectRedirectURL = value.OIDCConnectRedirectURL
	s.OIDCConnectRequireEmailVerified = value.OIDCConnectRequireEmailVerified
	s.OIDCConnectScopes = value.OIDCConnectScopes
	s.OIDCConnectTokenAuthMethod = value.OIDCConnectTokenAuthMethod
	s.OIDCConnectTokenURL = value.OIDCConnectTokenURL
	s.OIDCConnectUsePKCE = value.OIDCConnectUsePKCE
	s.OIDCConnectUserInfoEmailPath = value.OIDCConnectUserInfoEmailPath
	s.OIDCConnectUserInfoIDPath = value.OIDCConnectUserInfoIDPath
	s.OIDCConnectUserInfoURL = value.OIDCConnectUserInfoURL
	s.OIDCConnectUserInfoUsernamePath = value.OIDCConnectUserInfoUsernamePath
	s.OIDCConnectValidateIDToken = value.OIDCConnectValidateIDToken
	s.PasswordResetEnabled = value.PasswordResetEnabled
	s.RegistrationEmailDomainQuotaEnabled = value.RegistrationEmailDomainQuotaEnabled
	s.RegistrationEmailNormalization = value.RegistrationEmailNormalization
	s.RegistrationEmailSuffixWhitelist = value.RegistrationEmailSuffixWhitelist
	s.RegistrationEnabled = value.RegistrationEnabled
	s.SessionBindingEnabled = value.SessionBindingEnabled
	s.StepUpEnabled = value.StepUpEnabled
	s.TencentCaptchaAppID = value.TencentCaptchaAppID
	s.TencentCaptchaAppSecretKey = value.TencentCaptchaAppSecretKey
	s.TencentCaptchaAppSecretKeyConfigured = value.TencentCaptchaAppSecretKeyConfigured
	s.TencentCaptchaCloudSecretID = value.TencentCaptchaCloudSecretID
	s.TencentCaptchaCloudSecretIDConfigured = value.TencentCaptchaCloudSecretIDConfigured
	s.TencentCaptchaCloudSecretKey = value.TencentCaptchaCloudSecretKey
	s.TencentCaptchaCloudSecretKeyConfigured = value.TencentCaptchaCloudSecretKeyConfigured
	s.TencentCaptchaEnabled = value.TencentCaptchaEnabled
	s.TencentCaptchaRegion = value.TencentCaptchaRegion
	s.TotpEnabled = value.TotpEnabled
	s.TurnstileEnabled = value.TurnstileEnabled
	s.TurnstileSecretKey = value.TurnstileSecretKey
	s.TurnstileSecretKeyConfigured = value.TurnstileSecretKeyConfigured
	s.TurnstileSiteKey = value.TurnstileSiteKey
	s.UserEmailChangeEnabled = value.UserEmailChangeEnabled
	s.WeChatConnectAppID = value.WeChatConnectAppID
	s.WeChatConnectAppSecret = value.WeChatConnectAppSecret
	s.WeChatConnectAppSecretConfigured = value.WeChatConnectAppSecretConfigured
	s.WeChatConnectEnabled = value.WeChatConnectEnabled
	s.WeChatConnectFrontendRedirectURL = value.WeChatConnectFrontendRedirectURL
	s.WeChatConnectMPAppID = value.WeChatConnectMPAppID
	s.WeChatConnectMPAppSecret = value.WeChatConnectMPAppSecret
	s.WeChatConnectMPAppSecretConfigured = value.WeChatConnectMPAppSecretConfigured
	s.WeChatConnectMPEnabled = value.WeChatConnectMPEnabled
	s.WeChatConnectMobileAppID = value.WeChatConnectMobileAppID
	s.WeChatConnectMobileAppSecret = value.WeChatConnectMobileAppSecret
	s.WeChatConnectMobileAppSecretConfigured = value.WeChatConnectMobileAppSecretConfigured
	s.WeChatConnectMobileEnabled = value.WeChatConnectMobileEnabled
	s.WeChatConnectMode = value.WeChatConnectMode
	s.WeChatConnectOpenAppID = value.WeChatConnectOpenAppID
	s.WeChatConnectOpenAppSecret = value.WeChatConnectOpenAppSecret
	s.WeChatConnectOpenAppSecretConfigured = value.WeChatConnectOpenAppSecretConfigured
	s.WeChatConnectOpenEnabled = value.WeChatConnectOpenEnabled
	s.WeChatConnectRedirectURL = value.WeChatConnectRedirectURL
	s.WeChatConnectScopes = value.WeChatConnectScopes
}
