package identity

import (
	"encoding/json"
	"fmt"
	"strconv"
	"strings"

	"github.com/TokenFlux/TokenRouter/internal/pkg/apperror"
)

// AdminSettings 仅包含身份领域的管理配置，不接收完整系统设置。
type AdminSettings struct {
	AliyunCaptchaAccessKeyID               string   `json:"aliyun_captcha_access_key_id"`
	AliyunCaptchaAccessKeySecret           string   `json:"aliyun_captcha_access_key_secret"`
	AliyunCaptchaEnabled                   bool     `json:"aliyun_captcha_enabled"`
	AliyunCaptchaPrefix                    string   `json:"aliyun_captcha_prefix"`
	AliyunCaptchaRegion                    string   `json:"aliyun_captcha_region"`
	AliyunCaptchaSceneID                   string   `json:"aliyun_captcha_scene_id"`
	DefaultConcurrency                     int      `json:"default_concurrency"`
	DefaultUserAPIKeyLimit                 int      `json:"default_user_api_key_limit"`
	DefaultUserRPMLimit                    int      `json:"default_user_rpm_limit"`
	DingTalkConnectBypassRegistration      bool     `json:"dingtalk_connect_bypass_registration"`
	DingTalkConnectClientID                string   `json:"dingtalk_connect_client_id"`
	DingTalkConnectClientSecret            string   `json:"dingtalk_connect_client_secret"`
	DingTalkConnectCorpRestrictionPolicy   string   `json:"dingtalk_connect_corp_restriction_policy"`
	DingTalkConnectEnabled                 bool     `json:"dingtalk_connect_enabled"`
	DingTalkConnectInternalCorpID          string   `json:"dingtalk_connect_internal_corp_id"`
	DingTalkConnectRedirectURL             string   `json:"dingtalk_connect_redirect_url"`
	DingTalkConnectSyncCorpEmail           bool     `json:"dingtalk_connect_sync_corp_email"`
	DingTalkConnectSyncCorpEmailAttrKey    string   `json:"dingtalk_connect_sync_corp_email_attr_key"`
	DingTalkConnectSyncCorpEmailAttrName   string   `json:"dingtalk_connect_sync_corp_email_attr_name"`
	DingTalkConnectSyncDept                bool     `json:"dingtalk_connect_sync_dept"`
	DingTalkConnectSyncDeptAttrKey         string   `json:"dingtalk_connect_sync_dept_attr_key"`
	DingTalkConnectSyncDeptAttrName        string   `json:"dingtalk_connect_sync_dept_attr_name"`
	DingTalkConnectSyncDisplayName         bool     `json:"dingtalk_connect_sync_display_name"`
	DingTalkConnectSyncDisplayNameAttrKey  string   `json:"dingtalk_connect_sync_display_name_attr_key"`
	DingTalkConnectSyncDisplayNameAttrName string   `json:"dingtalk_connect_sync_display_name_attr_name"`
	EmailVerifyEnabled                     bool     `json:"email_verify_enabled"`
	GitHubOAuthClientID                    string   `json:"github_oauth_client_id"`
	GitHubOAuthClientSecret                string   `json:"github_oauth_client_secret"`
	GitHubOAuthEnabled                     bool     `json:"github_oauth_enabled"`
	GitHubOAuthFrontendRedirectURL         string   `json:"github_oauth_frontend_redirect_url"`
	GitHubOAuthRedirectURL                 string   `json:"github_oauth_redirect_url"`
	GoogleOAuthClientID                    string   `json:"google_oauth_client_id"`
	GoogleOAuthClientSecret                string   `json:"google_oauth_client_secret"`
	GoogleOAuthEnabled                     bool     `json:"google_oauth_enabled"`
	GoogleOAuthFrontendRedirectURL         string   `json:"google_oauth_frontend_redirect_url"`
	GoogleOAuthRedirectURL                 string   `json:"google_oauth_redirect_url"`
	GoogleOneTapEnabled                    bool     `json:"google_one_tap_enabled"`
	LinuxDoConnectClientID                 string   `json:"linuxdo_connect_client_id"`
	LinuxDoConnectClientSecret             string   `json:"linuxdo_connect_client_secret"`
	LinuxDoConnectEnabled                  bool     `json:"linuxdo_connect_enabled"`
	LinuxDoConnectRedirectURL              string   `json:"linuxdo_connect_redirect_url"`
	OIDCConnectAllowedSigningAlgs          string   `json:"oidc_connect_allowed_signing_algs"`
	OIDCConnectAuthorizeURL                string   `json:"oidc_connect_authorize_url"`
	OIDCConnectClientID                    string   `json:"oidc_connect_client_id"`
	OIDCConnectClientSecret                string   `json:"oidc_connect_client_secret"`
	OIDCConnectClockSkewSeconds            int      `json:"oidc_connect_clock_skew_seconds"`
	OIDCConnectDiscoveryURL                string   `json:"oidc_connect_discovery_url"`
	OIDCConnectEnabled                     bool     `json:"oidc_connect_enabled"`
	OIDCConnectFrontendRedirectURL         string   `json:"oidc_connect_frontend_redirect_url"`
	OIDCConnectIssuerURL                   string   `json:"oidc_connect_issuer_url"`
	OIDCConnectJWKSURL                     string   `json:"oidc_connect_jwks_url"`
	OIDCConnectProviderName                string   `json:"oidc_connect_provider_name"`
	OIDCConnectRedirectURL                 string   `json:"oidc_connect_redirect_url"`
	OIDCConnectRequireEmailVerified        bool     `json:"oidc_connect_require_email_verified"`
	OIDCConnectScopes                      string   `json:"oidc_connect_scopes"`
	OIDCConnectTokenAuthMethod             string   `json:"oidc_connect_token_auth_method"`
	OIDCConnectTokenURL                    string   `json:"oidc_connect_token_url"`
	OIDCConnectUsePKCE                     bool     `json:"oidc_connect_use_pkce"`
	OIDCConnectUserInfoEmailPath           string   `json:"oidc_connect_userinfo_email_path"`
	OIDCConnectUserInfoIDPath              string   `json:"oidc_connect_userinfo_id_path"`
	OIDCConnectUserInfoURL                 string   `json:"oidc_connect_userinfo_url"`
	OIDCConnectUserInfoUsernamePath        string   `json:"oidc_connect_userinfo_username_path"`
	OIDCConnectValidateIDToken             bool     `json:"oidc_connect_validate_id_token"`
	PasswordResetEnabled                   bool     `json:"password_reset_enabled"`
	RegistrationEmailDomainQuotaEnabled    bool     `json:"registration_email_domain_quota_enabled"`
	RegistrationEmailNormalization         bool     `json:"registration_email_normalization"`
	RegistrationEmailSuffixWhitelist       []string `json:"registration_email_suffix_whitelist"`
	RegistrationEnabled                    bool     `json:"registration_enabled"`
	SessionBindingEnabled                  bool     `json:"session_binding_enabled"`
	StepUpEnabled                          bool     `json:"step_up_enabled"`
	TencentCaptchaAppID                    string   `json:"tencent_captcha_app_id"`
	TencentCaptchaAppSecretKey             string   `json:"tencent_captcha_app_secret_key"`
	TencentCaptchaCloudSecretID            string   `json:"tencent_captcha_cloud_secret_id"`
	TencentCaptchaCloudSecretKey           string   `json:"tencent_captcha_cloud_secret_key"`
	TencentCaptchaEnabled                  bool     `json:"tencent_captcha_enabled"`
	TencentCaptchaRegion                   string   `json:"tencent_captcha_region"`
	TotpEnabled                            bool     `json:"totp_enabled"`
	TurnstileEnabled                       bool     `json:"turnstile_enabled"`
	TurnstileSecretKey                     string   `json:"turnstile_secret_key"`
	TurnstileSiteKey                       string   `json:"turnstile_site_key"`
	UserEmailChangeEnabled                 bool     `json:"user_email_change_enabled"`
	WeChatConnectAppID                     string   `json:"wechat_connect_app_id"`
	WeChatConnectAppSecret                 string   `json:"wechat_connect_app_secret"`
	WeChatConnectEnabled                   bool     `json:"wechat_connect_enabled"`
	WeChatConnectFrontendRedirectURL       string   `json:"wechat_connect_frontend_redirect_url"`
	WeChatConnectMPAppID                   string   `json:"wechat_connect_mp_app_id"`
	WeChatConnectMPAppSecret               string   `json:"wechat_connect_mp_app_secret"`
	WeChatConnectMPEnabled                 bool     `json:"wechat_connect_mp_enabled"`
	WeChatConnectMobileAppID               string   `json:"wechat_connect_mobile_app_id"`
	WeChatConnectMobileAppSecret           string   `json:"wechat_connect_mobile_app_secret"`
	WeChatConnectMobileEnabled             bool     `json:"wechat_connect_mobile_enabled"`
	WeChatConnectMode                      string   `json:"wechat_connect_mode"`
	WeChatConnectOpenAppID                 string   `json:"wechat_connect_open_app_id"`
	WeChatConnectOpenAppSecret             string   `json:"wechat_connect_open_app_secret"`
	WeChatConnectOpenEnabled               bool     `json:"wechat_connect_open_enabled"`
	WeChatConnectRedirectURL               string   `json:"wechat_connect_redirect_url"`
	WeChatConnectScopes                    string   `json:"wechat_connect_scopes"`
}

// 身份模块拥有这些已有持久键。
const (
	SettingKeyAliyunCaptchaPrefix                    = "aliyun_captcha_prefix"
	SettingKeyDefaultConcurrency                     = "default_concurrency"
	SettingKeyDingTalkConnectSyncCorpEmailAttrName   = "dingtalk_connect_sync_corp_email_attr_name"
	SettingKeyDingTalkConnectSyncDeptAttrName        = "dingtalk_connect_sync_dept_attr_name"
	SettingKeyDingTalkConnectSyncDisplayNameAttrName = "dingtalk_connect_sync_display_name_attr_name"
	SettingKeyTurnstileSiteKey                       = "turnstile_site_key"
)

// PrepareAdminSettings 复用原规范化及密钥保留顺序，只生成值，不执行存储或认证副作用。
func PrepareAdminSettings(settings *AdminSettings) (map[string]string, error) {
	updates := make(map[string]string)
	normalizedWhitelist, err := NormalizeRegistrationEmailSuffixWhitelist(settings.RegistrationEmailSuffixWhitelist)
	if err != nil {
		return nil, apperror.BadRequest("INVALID_REGISTRATION_EMAIL_SUFFIX_WHITELIST", err.Error())
	}
	if normalizedWhitelist == nil {
		normalizedWhitelist = []string{}
	}
	settings.RegistrationEmailSuffixWhitelist = normalizedWhitelist
	settings.WeChatConnectAppID = strings.TrimSpace(settings.WeChatConnectAppID)
	settings.WeChatConnectAppSecret = strings.TrimSpace(settings.WeChatConnectAppSecret)
	settings.WeChatConnectOpenAppID = strings.TrimSpace(oauthSettingsFirstNonEmpty(settings.WeChatConnectOpenAppID, settings.WeChatConnectAppID))
	settings.WeChatConnectOpenAppSecret = strings.TrimSpace(oauthSettingsFirstNonEmpty(settings.WeChatConnectOpenAppSecret, settings.WeChatConnectAppSecret))
	settings.WeChatConnectMPAppID = strings.TrimSpace(oauthSettingsFirstNonEmpty(settings.WeChatConnectMPAppID, settings.WeChatConnectAppID))
	settings.WeChatConnectMPAppSecret = strings.TrimSpace(oauthSettingsFirstNonEmpty(settings.WeChatConnectMPAppSecret, settings.WeChatConnectAppSecret))
	settings.WeChatConnectMobileAppID = strings.TrimSpace(oauthSettingsFirstNonEmpty(settings.WeChatConnectMobileAppID, settings.WeChatConnectAppID))
	settings.WeChatConnectMobileAppSecret = strings.TrimSpace(oauthSettingsFirstNonEmpty(settings.WeChatConnectMobileAppSecret, settings.WeChatConnectAppSecret))
	settings.WeChatConnectMode = SettingsNormalizeWeChatConnectStoredMode(
		settings.WeChatConnectOpenEnabled,
		settings.WeChatConnectMPEnabled,
		settings.WeChatConnectMobileEnabled,
		settings.WeChatConnectMode,
	)
	settings.WeChatConnectScopes = SettingsNormalizeWeChatConnectScopeSetting(settings.WeChatConnectScopes, settings.WeChatConnectMode)
	settings.WeChatConnectRedirectURL = strings.TrimSpace(settings.WeChatConnectRedirectURL)
	settings.WeChatConnectFrontendRedirectURL = strings.TrimSpace(settings.WeChatConnectFrontendRedirectURL)
	if settings.WeChatConnectFrontendRedirectURL == "" {
		settings.WeChatConnectFrontendRedirectURL = OAuthDefaultWeChatConnectFrontend
	}
	settings.GitHubOAuthClientID = strings.TrimSpace(settings.GitHubOAuthClientID)
	settings.GitHubOAuthClientSecret = strings.TrimSpace(settings.GitHubOAuthClientSecret)
	settings.GitHubOAuthRedirectURL = strings.TrimSpace(settings.GitHubOAuthRedirectURL)
	settings.GitHubOAuthFrontendRedirectURL = strings.TrimSpace(settings.GitHubOAuthFrontendRedirectURL)
	if settings.GitHubOAuthFrontendRedirectURL == "" {
		settings.GitHubOAuthFrontendRedirectURL = OAuthDefaultGitHubOAuthFrontend
	}
	settings.GoogleOAuthClientID = strings.TrimSpace(settings.GoogleOAuthClientID)
	settings.GoogleOAuthClientSecret = strings.TrimSpace(settings.GoogleOAuthClientSecret)
	settings.GoogleOAuthRedirectURL = strings.TrimSpace(settings.GoogleOAuthRedirectURL)
	settings.GoogleOAuthFrontendRedirectURL = strings.TrimSpace(settings.GoogleOAuthFrontendRedirectURL)
	if settings.GoogleOAuthFrontendRedirectURL == "" {
		settings.GoogleOAuthFrontendRedirectURL = OAuthDefaultGoogleOAuthFrontend
	}
	updates[SettingKeyRegistrationEnabled] = strconv.FormatBool(settings.RegistrationEnabled)
	updates[SettingKeyEmailVerifyEnabled] = strconv.FormatBool(settings.EmailVerifyEnabled)
	updates[SettingKeyRegistrationEmailNormalization] = strconv.FormatBool(settings.RegistrationEmailNormalization)
	updates[SettingKeyRegistrationEmailDomainQuotaEnabled] = strconv.FormatBool(settings.RegistrationEmailDomainQuotaEnabled)
	updates[SettingKeyUserEmailChangeEnabled] = strconv.FormatBool(settings.UserEmailChangeEnabled)
	registrationEmailSuffixWhitelistJSON, err := json.Marshal(settings.RegistrationEmailSuffixWhitelist)
	if err != nil {
		return nil, fmt.Errorf("marshal registration email suffix whitelist: %w", err)
	}
	updates[SettingKeyRegistrationEmailSuffixWhitelist] = string(registrationEmailSuffixWhitelistJSON)
	updates[SettingKeyPasswordResetEnabled] = strconv.FormatBool(settings.PasswordResetEnabled)
	updates[SettingKeyTotpEnabled] = strconv.FormatBool(settings.TotpEnabled)
	updates[SettingKeySessionBindingEnabled] = strconv.FormatBool(settings.SessionBindingEnabled)
	updates[SettingKeyStepUpEnabled] = strconv.FormatBool(settings.StepUpEnabled)
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
	updates[SettingKeyTencentCaptchaRegion] = NormalizeTencentCaptchaRegion(settings.TencentCaptchaRegion)
	updates[SettingKeyAliyunCaptchaEnabled] = strconv.FormatBool(settings.AliyunCaptchaEnabled)
	updates[SettingKeyAliyunCaptchaAccessKeyID] = settings.AliyunCaptchaAccessKeyID
	if settings.AliyunCaptchaAccessKeySecret != "" {
		updates[SettingKeyAliyunCaptchaAccessKeySecret] = settings.AliyunCaptchaAccessKeySecret
	}
	updates[SettingKeyAliyunCaptchaSceneID] = settings.AliyunCaptchaSceneID
	updates[SettingKeyAliyunCaptchaPrefix] = settings.AliyunCaptchaPrefix
	updates[SettingKeyAliyunCaptchaRegion] = NormalizeAliyunCaptchaRegion(settings.AliyunCaptchaRegion)
	updates[SettingKeyLinuxDoConnectEnabled] = strconv.FormatBool(settings.LinuxDoConnectEnabled)
	updates[SettingKeyLinuxDoConnectClientID] = settings.LinuxDoConnectClientID
	updates[SettingKeyLinuxDoConnectRedirectURL] = settings.LinuxDoConnectRedirectURL
	if settings.LinuxDoConnectClientSecret != "" {
		updates[SettingKeyLinuxDoConnectClientSecret] = settings.LinuxDoConnectClientSecret
	}
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
	updates[SettingKeyDefaultConcurrency] = strconv.Itoa(settings.DefaultConcurrency)
	updates[SettingKeyDefaultUserRPMLimit] = strconv.Itoa(settings.DefaultUserRPMLimit)
	if !IsValidUserAPIKeyLimit(settings.DefaultUserAPIKeyLimit) {
		return nil, ErrUserAPIKeyLimitInvalid
	}
	updates[SettingKeyDefaultUserAPIKeyLimit] = strconv.Itoa(settings.DefaultUserAPIKeyLimit)
	return updates, nil
}
