package identity

import (
	"context"
	"encoding/json"

	"github.com/TokenFlux/TokenRouter/internal/settings"
)

// SettingsParticipant 在静态装配中声明身份字段与密钥所有权，省略字段不会被零值覆盖。
func SettingsParticipant(grants *GrantSettings) settings.Participant {
	fields := []string{"aliyun_captcha_access_key_id", "aliyun_captcha_access_key_secret", "aliyun_captcha_enabled", "aliyun_captcha_prefix", "aliyun_captcha_region", "aliyun_captcha_scene_id", "default_concurrency", "default_user_api_key_limit", "default_user_rpm_limit", "dingtalk_connect_bypass_registration", "dingtalk_connect_client_id", "dingtalk_connect_client_secret", "dingtalk_connect_corp_restriction_policy", "dingtalk_connect_enabled", "dingtalk_connect_internal_corp_id", "dingtalk_connect_redirect_url", "dingtalk_connect_sync_corp_email", "dingtalk_connect_sync_corp_email_attr_key", "dingtalk_connect_sync_corp_email_attr_name", "dingtalk_connect_sync_dept", "dingtalk_connect_sync_dept_attr_key", "dingtalk_connect_sync_dept_attr_name", "dingtalk_connect_sync_display_name", "dingtalk_connect_sync_display_name_attr_key", "dingtalk_connect_sync_display_name_attr_name", "email_verify_enabled", "github_oauth_client_id", "github_oauth_client_secret", "github_oauth_enabled", "github_oauth_frontend_redirect_url", "github_oauth_redirect_url", "google_oauth_client_id", "google_oauth_client_secret", "google_oauth_enabled", "google_oauth_frontend_redirect_url", "google_oauth_redirect_url", "google_one_tap_enabled", "linuxdo_connect_client_id", "linuxdo_connect_client_secret", "linuxdo_connect_enabled", "linuxdo_connect_redirect_url", "oidc_connect_allowed_signing_algs", "oidc_connect_authorize_url", "oidc_connect_client_id", "oidc_connect_client_secret", "oidc_connect_clock_skew_seconds", "oidc_connect_discovery_url", "oidc_connect_enabled", "oidc_connect_frontend_redirect_url", "oidc_connect_issuer_url", "oidc_connect_jwks_url", "oidc_connect_provider_name", "oidc_connect_redirect_url", "oidc_connect_require_email_verified", "oidc_connect_scopes", "oidc_connect_token_auth_method", "oidc_connect_token_url", "oidc_connect_use_pkce", "oidc_connect_userinfo_email_path", "oidc_connect_userinfo_id_path", "oidc_connect_userinfo_url", "oidc_connect_userinfo_username_path", "oidc_connect_validate_id_token", "password_reset_enabled", "registration_email_domain_quota_enabled", "registration_email_normalization", "registration_email_suffix_whitelist", "registration_enabled", "session_binding_enabled", "step_up_enabled", "tencent_captcha_app_id", "tencent_captcha_app_secret_key", "tencent_captcha_cloud_secret_id", "tencent_captcha_cloud_secret_key", "tencent_captcha_enabled", "tencent_captcha_region", "totp_enabled", "turnstile_enabled", "turnstile_secret_key", "turnstile_site_key", "user_email_change_enabled", "wechat_connect_app_id", "wechat_connect_app_secret", "wechat_connect_enabled", "wechat_connect_frontend_redirect_url", "wechat_connect_mp_app_id", "wechat_connect_mp_app_secret", "wechat_connect_mp_enabled", "wechat_connect_mobile_app_id", "wechat_connect_mobile_app_secret", "wechat_connect_mobile_enabled", "wechat_connect_mode", "wechat_connect_open_app_id", "wechat_connect_open_app_secret", "wechat_connect_open_enabled", "wechat_connect_redirect_url", "wechat_connect_scopes"}
	keys := []string{SettingKeyAliyunCaptchaAccessKeyID, SettingKeyAliyunCaptchaAccessKeySecret, SettingKeyAliyunCaptchaEnabled, SettingKeyAliyunCaptchaPrefix, SettingKeyAliyunCaptchaRegion, SettingKeyAliyunCaptchaSceneID, SettingKeyDefaultConcurrency, SettingKeyDefaultUserAPIKeyLimit, SettingKeyDefaultUserRPMLimit, SettingKeyDingTalkConnectBypassRegistration, SettingKeyDingTalkConnectClientID, SettingKeyDingTalkConnectClientSecret, SettingKeyDingTalkConnectCorpRestrictionPolicy, SettingKeyDingTalkConnectEnabled, SettingKeyDingTalkConnectInternalCorpID, SettingKeyDingTalkConnectRedirectURL, SettingKeyDingTalkConnectSyncCorpEmail, SettingKeyDingTalkConnectSyncCorpEmailAttrKey, SettingKeyDingTalkConnectSyncCorpEmailAttrName, SettingKeyDingTalkConnectSyncDept, SettingKeyDingTalkConnectSyncDeptAttrKey, SettingKeyDingTalkConnectSyncDeptAttrName, SettingKeyDingTalkConnectSyncDisplayName, SettingKeyDingTalkConnectSyncDisplayNameAttrKey, SettingKeyDingTalkConnectSyncDisplayNameAttrName, SettingKeyEmailVerifyEnabled, SettingKeyGitHubOAuthClientID, SettingKeyGitHubOAuthClientSecret, SettingKeyGitHubOAuthEnabled, SettingKeyGitHubOAuthFrontendRedirectURL, SettingKeyGitHubOAuthRedirectURL, SettingKeyGoogleOAuthClientID, SettingKeyGoogleOAuthClientSecret, SettingKeyGoogleOAuthEnabled, SettingKeyGoogleOAuthFrontendRedirectURL, SettingKeyGoogleOAuthRedirectURL, SettingKeyGoogleOneTapEnabled, SettingKeyLinuxDoConnectClientID, SettingKeyLinuxDoConnectClientSecret, SettingKeyLinuxDoConnectEnabled, SettingKeyLinuxDoConnectRedirectURL, SettingKeyOIDCConnectAllowedSigningAlgs, SettingKeyOIDCConnectAuthorizeURL, SettingKeyOIDCConnectClientID, SettingKeyOIDCConnectClientSecret, SettingKeyOIDCConnectClockSkewSeconds, SettingKeyOIDCConnectDiscoveryURL, SettingKeyOIDCConnectEnabled, SettingKeyOIDCConnectFrontendRedirectURL, SettingKeyOIDCConnectIssuerURL, SettingKeyOIDCConnectJWKSURL, SettingKeyOIDCConnectProviderName, SettingKeyOIDCConnectRedirectURL, SettingKeyOIDCConnectRequireEmailVerified, SettingKeyOIDCConnectScopes, SettingKeyOIDCConnectTokenAuthMethod, SettingKeyOIDCConnectTokenURL, SettingKeyOIDCConnectUsePKCE, SettingKeyOIDCConnectUserInfoEmailPath, SettingKeyOIDCConnectUserInfoIDPath, SettingKeyOIDCConnectUserInfoURL, SettingKeyOIDCConnectUserInfoUsernamePath, SettingKeyOIDCConnectValidateIDToken, SettingKeyPasswordResetEnabled, SettingKeyRegistrationEmailDomainQuotaEnabled, SettingKeyRegistrationEmailNormalization, SettingKeyRegistrationEmailSuffixWhitelist, SettingKeyRegistrationEnabled, SettingKeySessionBindingEnabled, SettingKeyStepUpEnabled, SettingKeyTencentCaptchaAppID, SettingKeyTencentCaptchaAppSecretKey, SettingKeyTencentCaptchaCloudSecretID, SettingKeyTencentCaptchaCloudSecretKey, SettingKeyTencentCaptchaEnabled, SettingKeyTencentCaptchaRegion, SettingKeyTotpEnabled, SettingKeyTurnstileEnabled, SettingKeyTurnstileSecretKey, SettingKeyTurnstileSiteKey, SettingKeyUserEmailChangeEnabled, SettingKeyWeChatConnectAppID, SettingKeyWeChatConnectAppSecret, SettingKeyWeChatConnectEnabled, SettingKeyWeChatConnectFrontendRedirectURL, SettingKeyWeChatConnectMPAppID, SettingKeyWeChatConnectMPAppSecret, SettingKeyWeChatConnectMPEnabled, SettingKeyWeChatConnectMobileAppID, SettingKeyWeChatConnectMobileAppSecret, SettingKeyWeChatConnectMobileEnabled, SettingKeyWeChatConnectMode, SettingKeyWeChatConnectOpenAppID, SettingKeyWeChatConnectOpenAppSecret, SettingKeyWeChatConnectOpenEnabled, SettingKeyWeChatConnectRedirectURL, SettingKeyWeChatConnectScopes}
	fields = append(fields, AuthSourceSettingKeys()...)
	keys = append(keys, AuthSourceSettingKeys()...)
	return settings.Participant{Module: "identity", Fields: fields, Keys: keys, Prepare: func(ctx context.Context, input settings.Fields, _ map[string]string) (settings.PreparedChange, error) {
		if len(input) == 0 {
			return settings.PreparedChange{}, nil
		}
		raw, err := json.Marshal(input)
		if err != nil {
			return settings.PreparedChange{}, err
		}
		var value AdminSettings
		if err = json.Unmarshal(raw, &value); err != nil {
			return settings.PreparedChange{}, err
		}
		values, err := PrepareAdminSettings(&value)
		if err != nil {
			return settings.PreparedChange{}, err
		}
		grantValues, err := grants.prepareAuthSourceFields(ctx, input)
		if err != nil {
			return settings.PreparedChange{}, err
		}
		for key, value := range grantValues {
			values[key] = value
		}
		for i, field := range fields {
			if _, ok := input[field]; !ok {
				delete(values, keys[i])
			}
		}
		return settings.PreparedChange{Values: values}, nil
	}}
}
