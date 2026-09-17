package identity

import (
	"context"
	"fmt"
	"log/slog"
	"slices"
	"strconv"
	"strings"

	"github.com/TokenFlux/TokenRouter/internal/identity/authconfig"
	"github.com/TokenFlux/TokenRouter/internal/pkg/apperror"
)

// OAuthSettingsDefaults 只包含身份认证的启动缺省，完整 config 不进入核心。
type OAuthSettingsDefaults struct {
	LinuxDo     authconfig.LinuxDoConnectConfig
	DingTalk    authconfig.DingTalkConnectConfig
	OIDC        authconfig.OIDCConnectConfig
	WeChat      authconfig.WeChatConnectConfig
	GitHubOAuth authconfig.EmailOAuthProviderConfig
	GoogleOAuth authconfig.EmailOAuthProviderConfig
}

// OAuthSettingsStore 保留原批量读取时点。
type OAuthSettingsStore interface {
	GetMultiple(context.Context, []string) (map[string]string, error)
}

// OAuthSettings 解释动态覆盖及兼容安全默认，发现文档通过外部端口读取。
type OAuthSettings struct {
	settingRepo     OAuthSettingsStore
	defaults        *OAuthSettingsDefaults
	resolveMetadata func(context.Context, string) (*OAuthProviderMetadata, error)
}

// NewOAuthSettings 构造没有 I/O；接收默认值副本，防止输出污染启动配置。
func NewOAuthSettings(repo OAuthSettingsStore, defaults *OAuthSettingsDefaults, resolver func(context.Context, string) (*OAuthProviderMetadata, error)) *OAuthSettings {
	var snapshot *OAuthSettingsDefaults
	if defaults != nil {
		value := *defaults
		value.DingTalk.AttributeSyncFields = slices.Clone(value.DingTalk.AttributeSyncFields)
		snapshot = &value
	}
	return &OAuthSettings{settingRepo: repo, defaults: snapshot, resolveMetadata: resolver}
}

// 身份模块拥有动态键和默认值，旧格式保持不变。
const (
	SettingKeyDingTalkConnectBypassRegistration     = "dingtalk_connect_bypass_registration"
	SettingKeyDingTalkConnectClientID               = "dingtalk_connect_client_id"
	SettingKeyDingTalkConnectClientSecret           = "dingtalk_connect_client_secret"
	SettingKeyDingTalkConnectCorpRestrictionPolicy  = "dingtalk_connect_corp_restriction_policy"
	SettingKeyDingTalkConnectInternalCorpID         = "dingtalk_connect_internal_corp_id"
	SettingKeyDingTalkConnectRedirectURL            = "dingtalk_connect_redirect_url"
	SettingKeyDingTalkConnectSyncCorpEmail          = "dingtalk_connect_sync_corp_email"
	SettingKeyDingTalkConnectSyncCorpEmailAttrKey   = "dingtalk_connect_sync_corp_email_attr_key"
	SettingKeyDingTalkConnectSyncDept               = "dingtalk_connect_sync_dept"
	SettingKeyDingTalkConnectSyncDeptAttrKey        = "dingtalk_connect_sync_dept_attr_key"
	SettingKeyDingTalkConnectSyncDisplayName        = "dingtalk_connect_sync_display_name"
	SettingKeyDingTalkConnectSyncDisplayNameAttrKey = "dingtalk_connect_sync_display_name_attr_key"
	SettingKeyGitHubOAuthClientID                   = "github_oauth_client_id"
	SettingKeyGitHubOAuthClientSecret               = "github_oauth_client_secret"
	SettingKeyGitHubOAuthEnabled                    = "github_oauth_enabled"
	SettingKeyGitHubOAuthFrontendRedirectURL        = "github_oauth_frontend_redirect_url"
	SettingKeyGitHubOAuthRedirectURL                = "github_oauth_redirect_url"
	SettingKeyGoogleOAuthClientID                   = "google_oauth_client_id"
	SettingKeyGoogleOAuthClientSecret               = "google_oauth_client_secret"
	SettingKeyGoogleOAuthEnabled                    = "google_oauth_enabled"
	SettingKeyGoogleOAuthFrontendRedirectURL        = "google_oauth_frontend_redirect_url"
	SettingKeyGoogleOAuthRedirectURL                = "google_oauth_redirect_url"
	SettingKeyGoogleOneTapEnabled                   = "google_one_tap_enabled"
	SettingKeyLinuxDoConnectClientID                = "linuxdo_connect_client_id"
	SettingKeyLinuxDoConnectClientSecret            = "linuxdo_connect_client_secret"
	SettingKeyLinuxDoConnectRedirectURL             = "linuxdo_connect_redirect_url"
	SettingKeyOIDCConnectAllowedSigningAlgs         = "oidc_connect_allowed_signing_algs"
	SettingKeyOIDCConnectAuthorizeURL               = "oidc_connect_authorize_url"
	SettingKeyOIDCConnectClientID                   = "oidc_connect_client_id"
	SettingKeyOIDCConnectClientSecret               = "oidc_connect_client_secret"
	SettingKeyOIDCConnectClockSkewSeconds           = "oidc_connect_clock_skew_seconds"
	SettingKeyOIDCConnectDiscoveryURL               = "oidc_connect_discovery_url"
	SettingKeyOIDCConnectFrontendRedirectURL        = "oidc_connect_frontend_redirect_url"
	SettingKeyOIDCConnectIssuerURL                  = "oidc_connect_issuer_url"
	SettingKeyOIDCConnectJWKSURL                    = "oidc_connect_jwks_url"
	SettingKeyOIDCConnectProviderName               = "oidc_connect_provider_name"
	SettingKeyOIDCConnectRedirectURL                = "oidc_connect_redirect_url"
	SettingKeyOIDCConnectRequireEmailVerified       = "oidc_connect_require_email_verified"
	SettingKeyOIDCConnectScopes                     = "oidc_connect_scopes"
	SettingKeyOIDCConnectTokenAuthMethod            = "oidc_connect_token_auth_method"
	SettingKeyOIDCConnectTokenURL                   = "oidc_connect_token_url"
	SettingKeyOIDCConnectUsePKCE                    = "oidc_connect_use_pkce"
	SettingKeyOIDCConnectUserInfoEmailPath          = "oidc_connect_userinfo_email_path"
	SettingKeyOIDCConnectUserInfoIDPath             = "oidc_connect_userinfo_id_path"
	SettingKeyOIDCConnectUserInfoURL                = "oidc_connect_userinfo_url"
	SettingKeyOIDCConnectUserInfoUsernamePath       = "oidc_connect_userinfo_username_path"
	SettingKeyOIDCConnectValidateIDToken            = "oidc_connect_validate_id_token"
	SettingKeyWeChatConnectAppID                    = "wechat_connect_app_id"
	SettingKeyWeChatConnectAppSecret                = "wechat_connect_app_secret"
	SettingKeyWeChatConnectFrontendRedirectURL      = "wechat_connect_frontend_redirect_url"
	SettingKeyWeChatConnectMPAppID                  = "wechat_connect_mp_app_id"
	SettingKeyWeChatConnectMPAppSecret              = "wechat_connect_mp_app_secret"
	SettingKeyWeChatConnectMobileAppID              = "wechat_connect_mobile_app_id"
	SettingKeyWeChatConnectMobileAppSecret          = "wechat_connect_mobile_app_secret"
	SettingKeyWeChatConnectOpenAppID                = "wechat_connect_open_app_id"
	SettingKeyWeChatConnectOpenAppSecret            = "wechat_connect_open_app_secret"
	SettingKeyWeChatConnectRedirectURL              = "wechat_connect_redirect_url"
	SettingKeyWeChatConnectScopes                   = "wechat_connect_scopes"
	OAuthDefaultGitHubOAuthAuthorize                = "https://github.com/login/oauth/authorize"
	OAuthDefaultGitHubOAuthEmails                   = "https://api.github.com/user/emails"
	OAuthDefaultGitHubOAuthFrontend                 = "/auth/oauth/callback"
	OAuthDefaultGitHubOAuthScopes                   = "read:user user:email"
	OAuthDefaultGitHubOAuthToken                    = "https://github.com/login/oauth/access_token"
	OAuthDefaultGitHubOAuthUserInfo                 = "https://api.github.com/user"
	OAuthDefaultGoogleOAuthAuthorize                = "https://accounts.google.com/o/oauth2/v2/auth"
	OAuthDefaultGoogleOAuthFrontend                 = "/auth/oauth/callback"
	OAuthDefaultGoogleOAuthScopes                   = "openid email profile"
	OAuthDefaultGoogleOAuthToken                    = "https://oauth2.googleapis.com/token"
	OAuthDefaultGoogleOAuthUserInfo                 = "https://openidconnect.googleapis.com/v1/userinfo"
	OAuthDefaultWeChatConnectFrontend               = "/auth/wechat/callback"
	OAuthDefaultWeChatConnectScopes                 = "snsapi_login"
)

// OAuthProviderMetadata 是发现文档返回的纯值。
type OAuthProviderMetadata struct {
	AuthorizationEndpoint string `json:"authorization_endpoint"`
	TokenEndpoint         string `json:"token_endpoint"`
	UserInfoEndpoint      string `json:"userinfo_endpoint"`
	JWKSURI               string `json:"jwks_uri"`
}

func SettingsCoerceDingTalkCorpPolicyForWrite(policy string) string {
	return SettingsCoerceDeprecatedDingTalkCorpPolicy(policy)
}

func SettingsCoerceDeprecatedDingTalkCorpPolicy(policy string) string {
	if policy == "whitelist" {
		slog.Warn("dingtalk: corp_restriction_policy=whitelist is deprecated and unsupported, coercing to none",
			"hint", "re-save DingTalk settings in admin UI to clear this warning")
		return "none"
	}
	return policy
}

func SettingsDefaultWeChatConnectScopeForMode(mode string) string {
	switch NormalizeWeChatConnectModeSetting(mode) {
	case "mp":
		return "snsapi_userinfo"
	case "mobile":
		return ""
	}
	return OAuthDefaultWeChatConnectScopes
}

func SettingsNormalizeWeChatConnectScopeSetting(raw, mode string) string {
	switch NormalizeWeChatConnectModeSetting(mode) {
	case "mp":
		switch strings.TrimSpace(raw) {
		case "snsapi_base":
			return "snsapi_base"
		case "snsapi_userinfo":
			return "snsapi_userinfo"
		default:
			return SettingsDefaultWeChatConnectScopeForMode(mode)
		}
	case "mobile":
		return ""
	default:
		return OAuthDefaultWeChatConnectScopes
	}
}

func SettingsNormalizeWeChatConnectStoredMode(openEnabled, mpEnabled, mobileEnabled bool, mode string) string {
	mode = NormalizeWeChatConnectModeSetting(mode)
	switch mode {
	case "open":
		if openEnabled {
			return "open"
		}
	case "mp":
		if mpEnabled {
			return "mp"
		}
	case "mobile":
		if mobileEnabled {
			return "mobile"
		}
	}
	switch {
	case openEnabled:
		return "open"
	case mpEnabled:
		return "mp"
	case mobileEnabled:
		return "mobile"
	default:
		return mode
	}
}

func SettingsMergeWeChatConnectCapabilitySettings(settings map[string]string, base authconfig.WeChatConnectConfig, enabled bool, mode string) (bool, bool, bool) {
	mode = NormalizeWeChatConnectModeSetting(oauthSettingsFirstNonEmpty(mode, base.Mode))
	rawOpen, hasOpen := settings[SettingKeyWeChatConnectOpenEnabled]
	rawMP, hasMP := settings[SettingKeyWeChatConnectMPEnabled]
	rawMobile, hasMobile := settings[SettingKeyWeChatConnectMobileEnabled]
	openConfigured := hasOpen && strings.TrimSpace(rawOpen) != ""
	mpConfigured := hasMP && strings.TrimSpace(rawMP) != ""
	mobileConfigured := hasMobile && strings.TrimSpace(rawMobile) != ""

	if openConfigured || mpConfigured || mobileConfigured {
		openEnabled := strings.TrimSpace(rawOpen) == "true"
		mpEnabled := strings.TrimSpace(rawMP) == "true"
		mobileEnabled := strings.TrimSpace(rawMobile) == "true"
		_, enabledConfigured := settings[SettingKeyWeChatConnectEnabled]
		if !enabledConfigured &&
			enabled &&
			!openEnabled &&
			!mpEnabled &&
			!mobileEnabled &&
			(base.OpenEnabled || base.MPEnabled || base.MobileEnabled) {
			return base.OpenEnabled, base.MPEnabled, base.MobileEnabled
		}
		return openEnabled, mpEnabled, mobileEnabled
	}
	if !enabled {
		return false, false, false
	}
	if base.OpenEnabled || base.MPEnabled || base.MobileEnabled {
		return base.OpenEnabled, base.MPEnabled, base.MobileEnabled
	}
	return ParseWeChatConnectCapabilitySettings(settings, enabled, mode)
}

func (s *OAuthSettings) EffectiveWeChatConnectOAuthConfig(settings map[string]string) WeChatConnectOAuthConfig {
	base := authconfig.WeChatConnectConfig{}
	if s != nil && s.defaults != nil {
		base = s.defaults.WeChat
	}

	enabled := base.Enabled
	if raw, ok := settings[SettingKeyWeChatConnectEnabled]; ok {
		enabled = strings.TrimSpace(raw) == "true"
	}

	legacyAppID := strings.TrimSpace(oauthSettingsFirstNonEmpty(
		settings[SettingKeyWeChatConnectAppID],
		base.AppID,
		base.OpenAppID,
		base.MPAppID,
		base.MobileAppID,
	))
	legacyAppSecret := strings.TrimSpace(oauthSettingsFirstNonEmpty(
		settings[SettingKeyWeChatConnectAppSecret],
		base.AppSecret,
		base.OpenAppSecret,
		base.MPAppSecret,
		base.MobileAppSecret,
	))
	openAppID := strings.TrimSpace(oauthSettingsFirstNonEmpty(settings[SettingKeyWeChatConnectOpenAppID], base.OpenAppID, legacyAppID))
	openAppSecret := strings.TrimSpace(oauthSettingsFirstNonEmpty(settings[SettingKeyWeChatConnectOpenAppSecret], base.OpenAppSecret, legacyAppSecret))
	mpAppID := strings.TrimSpace(oauthSettingsFirstNonEmpty(settings[SettingKeyWeChatConnectMPAppID], base.MPAppID, legacyAppID))
	mpAppSecret := strings.TrimSpace(oauthSettingsFirstNonEmpty(settings[SettingKeyWeChatConnectMPAppSecret], base.MPAppSecret, legacyAppSecret))
	mobileAppID := strings.TrimSpace(oauthSettingsFirstNonEmpty(settings[SettingKeyWeChatConnectMobileAppID], base.MobileAppID, legacyAppID))
	mobileAppSecret := strings.TrimSpace(oauthSettingsFirstNonEmpty(settings[SettingKeyWeChatConnectMobileAppSecret], base.MobileAppSecret, legacyAppSecret))

	modeRaw := oauthSettingsFirstNonEmpty(settings[SettingKeyWeChatConnectMode], base.Mode)
	openEnabled, mpEnabled, mobileEnabled := SettingsMergeWeChatConnectCapabilitySettings(settings, base, enabled, modeRaw)
	mode := SettingsNormalizeWeChatConnectStoredMode(openEnabled, mpEnabled, mobileEnabled, modeRaw)

	return WeChatConnectOAuthConfig{
		Enabled:             enabled,
		LegacyAppID:         legacyAppID,
		LegacyAppSecret:     legacyAppSecret,
		OpenAppID:           openAppID,
		OpenAppSecret:       openAppSecret,
		MPAppID:             mpAppID,
		MPAppSecret:         mpAppSecret,
		MobileAppID:         mobileAppID,
		MobileAppSecret:     mobileAppSecret,
		OpenEnabled:         openEnabled,
		MPEnabled:           mpEnabled,
		MobileEnabled:       mobileEnabled,
		Mode:                mode,
		Scopes:              SettingsNormalizeWeChatConnectScopeSetting(oauthSettingsFirstNonEmpty(settings[SettingKeyWeChatConnectScopes], base.Scopes), mode),
		RedirectURL:         strings.TrimSpace(oauthSettingsFirstNonEmpty(settings[SettingKeyWeChatConnectRedirectURL], base.RedirectURL)),
		FrontendRedirectURL: strings.TrimSpace(oauthSettingsFirstNonEmpty(settings[SettingKeyWeChatConnectFrontendRedirectURL], base.FrontendRedirectURL, OAuthDefaultWeChatConnectFrontend)),
	}
}

func SettingsDefaultWeChatConnectScopesForMode(mode string) string {
	return SettingsDefaultWeChatConnectScopeForMode(mode)
}

func (s *OAuthSettings) ParseWeChatConnectOAuthConfig(settings map[string]string) (WeChatConnectOAuthConfig, error) {
	cfg := s.EffectiveWeChatConnectOAuthConfig(settings)

	if !cfg.Enabled || (!cfg.OpenEnabled && !cfg.MPEnabled) {
		return WeChatConnectOAuthConfig{}, apperror.NotFound("OAUTH_DISABLED", "wechat oauth is disabled")
	}
	if cfg.OpenEnabled {
		if cfg.AppIDForMode("open") == "" {
			return WeChatConnectOAuthConfig{}, apperror.InternalServer("OAUTH_CONFIG_INVALID", "wechat oauth pc app id not configured")
		}
		if cfg.AppSecretForMode("open") == "" {
			return WeChatConnectOAuthConfig{}, apperror.InternalServer("OAUTH_CONFIG_INVALID", "wechat oauth pc app secret not configured")
		}
	}
	if cfg.MPEnabled {
		if cfg.AppIDForMode("mp") == "" {
			return WeChatConnectOAuthConfig{}, apperror.InternalServer("OAUTH_CONFIG_INVALID", "wechat oauth official account app id not configured")
		}
		if cfg.AppSecretForMode("mp") == "" {
			return WeChatConnectOAuthConfig{}, apperror.InternalServer("OAUTH_CONFIG_INVALID", "wechat oauth official account app secret not configured")
		}
	}
	if cfg.MobileEnabled {
		if cfg.AppIDForMode("mobile") == "" {
			return WeChatConnectOAuthConfig{}, apperror.InternalServer("OAUTH_CONFIG_INVALID", "wechat oauth mobile app id not configured")
		}
		if cfg.AppSecretForMode("mobile") == "" {
			return WeChatConnectOAuthConfig{}, apperror.InternalServer("OAUTH_CONFIG_INVALID", "wechat oauth mobile app secret not configured")
		}
	}
	if v := strings.TrimSpace(cfg.RedirectURL); v != "" {
		if err := authconfig.ValidateAbsoluteHTTPURL(v); err != nil {
			return WeChatConnectOAuthConfig{}, apperror.InternalServer("OAUTH_CONFIG_INVALID", "wechat oauth redirect url invalid")
		}
	}
	if err := authconfig.ValidateFrontendRedirectURL(cfg.FrontendRedirectURL); err != nil {
		return WeChatConnectOAuthConfig{}, apperror.InternalServer("OAUTH_CONFIG_INVALID", "wechat oauth frontend redirect url invalid")
	}
	return cfg, nil
}

func (s *OAuthSettings) WeChatOAuthCapabilitiesFromSettings(settings map[string]string) (bool, bool, bool, bool) {
	cfg := s.EffectiveWeChatConnectOAuthConfig(settings)
	if !cfg.Enabled {
		return false, false, false, false
	}

	openReady := cfg.OpenEnabled && cfg.AppIDForMode("open") != "" && cfg.AppSecretForMode("open") != ""
	mpReady := cfg.MPEnabled && cfg.AppIDForMode("mp") != "" && cfg.AppSecretForMode("mp") != ""
	mobileReady := cfg.MobileEnabled && cfg.AppIDForMode("mobile") != "" && cfg.AppSecretForMode("mobile") != ""

	return openReady || mpReady, openReady, mpReady, mobileReady
}

func SettingsMergeEmailOAuthBaseConfig(base, override authconfig.EmailOAuthProviderConfig) authconfig.EmailOAuthProviderConfig {
	base.Enabled = override.Enabled
	if strings.TrimSpace(override.ClientID) != "" {
		base.ClientID = strings.TrimSpace(override.ClientID)
	}
	if strings.TrimSpace(override.ClientSecret) != "" {
		base.ClientSecret = strings.TrimSpace(override.ClientSecret)
	}
	if strings.TrimSpace(override.AuthorizeURL) != "" {
		base.AuthorizeURL = strings.TrimSpace(override.AuthorizeURL)
	}
	if strings.TrimSpace(override.TokenURL) != "" {
		base.TokenURL = strings.TrimSpace(override.TokenURL)
	}
	if strings.TrimSpace(override.UserInfoURL) != "" {
		base.UserInfoURL = strings.TrimSpace(override.UserInfoURL)
	}
	if strings.TrimSpace(override.EmailsURL) != "" {
		base.EmailsURL = strings.TrimSpace(override.EmailsURL)
	}
	if strings.TrimSpace(override.Scopes) != "" {
		base.Scopes = strings.TrimSpace(override.Scopes)
	}
	if strings.TrimSpace(override.RedirectURL) != "" {
		base.RedirectURL = strings.TrimSpace(override.RedirectURL)
	}
	if strings.TrimSpace(override.FrontendRedirectURL) != "" {
		base.FrontendRedirectURL = strings.TrimSpace(override.FrontendRedirectURL)
	}
	return base
}

func (s *OAuthSettings) EmailOAuthPublicEnabled(settings map[string]string, provider string) bool {
	cfg := s.EffectiveEmailOAuthConfig(settings, provider)
	return cfg.Enabled &&
		strings.TrimSpace(cfg.ClientID) != "" &&
		strings.TrimSpace(cfg.ClientSecret) != "" &&
		strings.TrimSpace(cfg.RedirectURL) != ""
}

func (s *OAuthSettings) EffectiveEmailOAuthConfig(settings map[string]string, provider string) authconfig.EmailOAuthProviderConfig {
	base := s.BaseEmailOAuthConfig(provider)
	enabledKey, clientIDKey, clientSecretKey, redirectURLKey, frontendRedirectURLKey := SettingsEmailOAuthSettingKeys(provider)
	if enabledKey == "" {
		return base
	}
	if raw, ok := settings[enabledKey]; ok {
		base.Enabled = raw == "true"
	}
	if v, ok := settings[clientIDKey]; ok && strings.TrimSpace(v) != "" {
		base.ClientID = strings.TrimSpace(v)
	}
	if v, ok := settings[clientSecretKey]; ok && strings.TrimSpace(v) != "" {
		base.ClientSecret = strings.TrimSpace(v)
	}
	if v, ok := settings[redirectURLKey]; ok && strings.TrimSpace(v) != "" {
		base.RedirectURL = strings.TrimSpace(v)
	}
	if v, ok := settings[frontendRedirectURLKey]; ok && strings.TrimSpace(v) != "" {
		base.FrontendRedirectURL = strings.TrimSpace(v)
	}
	return base
}

func SettingsOidcUsePKCECompatibilityDefault(base authconfig.OIDCConnectConfig) bool {
	if base.UsePKCEExplicit {
		return base.UsePKCE
	}
	return true
}

func SettingsOidcValidateIDTokenCompatibilityDefault(base authconfig.OIDCConnectConfig) bool {
	if base.ValidateIDTokenExplicit {
		return base.ValidateIDToken
	}
	return true
}

func SettingsOidcCompatibilityWriteDefault(base authconfig.OIDCConnectConfig, configured bool, raw string, explicit bool, explicitValue bool) bool {
	if configured {
		return strings.TrimSpace(raw) == "true"
	}
	if explicit {
		return explicitValue
	}
	return false
}

func (s *OAuthSettings) OIDCSecurityWriteDefaults(ctx context.Context) (bool, bool, error) {
	rawSettings, err := s.settingRepo.GetMultiple(ctx, []string{
		SettingKeyOIDCConnectUsePKCE,
		SettingKeyOIDCConnectValidateIDToken,
	})
	if err != nil {
		return false, false, fmt.Errorf("get oidc security write defaults: %w", err)
	}

	base := authconfig.OIDCConnectConfig{}
	if s != nil && s.defaults != nil {
		base = s.defaults.OIDC
	}

	rawUsePKCE, hasUsePKCE := rawSettings[SettingKeyOIDCConnectUsePKCE]
	rawValidateIDToken, hasValidateIDToken := rawSettings[SettingKeyOIDCConnectValidateIDToken]

	return SettingsOidcCompatibilityWriteDefault(base, hasUsePKCE, rawUsePKCE, base.UsePKCEExplicit, base.UsePKCE),
		SettingsOidcCompatibilityWriteDefault(base, hasValidateIDToken, rawValidateIDToken, base.ValidateIDTokenExplicit, base.ValidateIDToken),
		nil
}

func (s *OAuthSettings) GetEmailOAuthProviderConfig(ctx context.Context, provider string) (authconfig.EmailOAuthProviderConfig, error) {
	if s == nil || s.defaults == nil {
		return authconfig.EmailOAuthProviderConfig{}, apperror.ServiceUnavailable("CONFIG_NOT_READY", "config not loaded")
	}
	provider = strings.ToLower(strings.TrimSpace(provider))
	if provider != "github" && provider != "google" {
		return authconfig.EmailOAuthProviderConfig{}, apperror.NotFound("OAUTH_PROVIDER_NOT_FOUND", "oauth provider not found")
	}

	enabledKey, clientIDKey, clientSecretKey, redirectURLKey, frontendRedirectURLKey := SettingsEmailOAuthSettingKeys(provider)
	keys := []string{enabledKey, clientIDKey, clientSecretKey, redirectURLKey, frontendRedirectURLKey}
	settings, err := s.settingRepo.GetMultiple(ctx, keys)
	if err != nil {
		return authconfig.EmailOAuthProviderConfig{}, fmt.Errorf("get email oauth settings: %w", err)
	}

	effective := s.EffectiveEmailOAuthConfig(settings, provider)
	if !effective.Enabled {
		return authconfig.EmailOAuthProviderConfig{}, apperror.NotFound("OAUTH_DISABLED", "oauth login is disabled")
	}
	if strings.TrimSpace(effective.ClientID) == "" {
		return authconfig.EmailOAuthProviderConfig{}, apperror.InternalServer("OAUTH_CONFIG_INVALID", "oauth client id not configured")
	}
	if strings.TrimSpace(effective.ClientSecret) == "" {
		return authconfig.EmailOAuthProviderConfig{}, apperror.InternalServer("OAUTH_CONFIG_INVALID", "oauth client secret not configured")
	}
	if strings.TrimSpace(effective.AuthorizeURL) == "" {
		return authconfig.EmailOAuthProviderConfig{}, apperror.InternalServer("OAUTH_CONFIG_INVALID", "oauth authorize url not configured")
	}
	if strings.TrimSpace(effective.TokenURL) == "" {
		return authconfig.EmailOAuthProviderConfig{}, apperror.InternalServer("OAUTH_CONFIG_INVALID", "oauth token url not configured")
	}
	if strings.TrimSpace(effective.UserInfoURL) == "" {
		return authconfig.EmailOAuthProviderConfig{}, apperror.InternalServer("OAUTH_CONFIG_INVALID", "oauth userinfo url not configured")
	}
	if provider == "github" && strings.TrimSpace(effective.EmailsURL) == "" {
		return authconfig.EmailOAuthProviderConfig{}, apperror.InternalServer("OAUTH_CONFIG_INVALID", "oauth emails url not configured")
	}
	if strings.TrimSpace(effective.RedirectURL) == "" {
		return authconfig.EmailOAuthProviderConfig{}, apperror.InternalServer("OAUTH_CONFIG_INVALID", "oauth redirect url not configured")
	}
	if strings.TrimSpace(effective.FrontendRedirectURL) == "" {
		return authconfig.EmailOAuthProviderConfig{}, apperror.InternalServer("OAUTH_CONFIG_INVALID", "oauth frontend redirect url not configured")
	}
	if err := authconfig.ValidateAbsoluteHTTPURL(effective.AuthorizeURL); err != nil {
		return authconfig.EmailOAuthProviderConfig{}, apperror.InternalServer("OAUTH_CONFIG_INVALID", "oauth authorize url invalid")
	}
	if err := authconfig.ValidateAbsoluteHTTPURL(effective.TokenURL); err != nil {
		return authconfig.EmailOAuthProviderConfig{}, apperror.InternalServer("OAUTH_CONFIG_INVALID", "oauth token url invalid")
	}
	if err := authconfig.ValidateAbsoluteHTTPURL(effective.UserInfoURL); err != nil {
		return authconfig.EmailOAuthProviderConfig{}, apperror.InternalServer("OAUTH_CONFIG_INVALID", "oauth userinfo url invalid")
	}
	if strings.TrimSpace(effective.EmailsURL) != "" {
		if err := authconfig.ValidateAbsoluteHTTPURL(effective.EmailsURL); err != nil {
			return authconfig.EmailOAuthProviderConfig{}, apperror.InternalServer("OAUTH_CONFIG_INVALID", "oauth emails url invalid")
		}
	}
	if err := authconfig.ValidateAbsoluteHTTPURL(effective.RedirectURL); err != nil {
		return authconfig.EmailOAuthProviderConfig{}, apperror.InternalServer("OAUTH_CONFIG_INVALID", "oauth redirect url invalid")
	}
	if err := authconfig.ValidateFrontendRedirectURL(effective.FrontendRedirectURL); err != nil {
		return authconfig.EmailOAuthProviderConfig{}, apperror.InternalServer("OAUTH_CONFIG_INVALID", "oauth frontend redirect url invalid")
	}
	return effective, nil
}

func (s *OAuthSettings) GetGoogleOneTapConfig(ctx context.Context) (authconfig.EmailOAuthProviderConfig, error) {
	if s == nil || s.settingRepo == nil {
		return authconfig.EmailOAuthProviderConfig{}, apperror.ServiceUnavailable("CONFIG_NOT_READY", "config not loaded")
	}
	settings, err := s.settingRepo.GetMultiple(ctx, []string{SettingKeyGoogleOneTapEnabled})
	if err != nil {
		return authconfig.EmailOAuthProviderConfig{}, fmt.Errorf("get google one tap setting: %w", err)
	}
	if settings[SettingKeyGoogleOneTapEnabled] != "true" {
		return authconfig.EmailOAuthProviderConfig{}, apperror.NotFound("OAUTH_DISABLED", "google one tap is disabled")
	}
	return s.GetEmailOAuthProviderConfig(ctx, "google")
}

func (s *OAuthSettings) GetLinuxDoConnectOAuthConfig(ctx context.Context) (authconfig.LinuxDoConnectConfig, error) {
	if s == nil || s.defaults == nil {
		return authconfig.LinuxDoConnectConfig{}, apperror.ServiceUnavailable("CONFIG_NOT_READY", "config not loaded")
	}

	effective := s.defaults.LinuxDo

	keys := []string{
		SettingKeyLinuxDoConnectEnabled,
		SettingKeyLinuxDoConnectClientID,
		SettingKeyLinuxDoConnectClientSecret,
		SettingKeyLinuxDoConnectRedirectURL,
	}
	settings, err := s.settingRepo.GetMultiple(ctx, keys)
	if err != nil {
		return authconfig.LinuxDoConnectConfig{}, fmt.Errorf("get linuxdo connect settings: %w", err)
	}

	if raw, ok := settings[SettingKeyLinuxDoConnectEnabled]; ok {
		effective.Enabled = raw == "true"
	}
	if v, ok := settings[SettingKeyLinuxDoConnectClientID]; ok && strings.TrimSpace(v) != "" {
		effective.ClientID = strings.TrimSpace(v)
	}
	if v, ok := settings[SettingKeyLinuxDoConnectClientSecret]; ok && strings.TrimSpace(v) != "" {
		effective.ClientSecret = strings.TrimSpace(v)
	}
	if v, ok := settings[SettingKeyLinuxDoConnectRedirectURL]; ok && strings.TrimSpace(v) != "" {
		effective.RedirectURL = strings.TrimSpace(v)
	}
	if !effective.Enabled {
		return authconfig.LinuxDoConnectConfig{}, apperror.NotFound("OAUTH_DISABLED", "oauth login is disabled")
	}

	// 基础健壮性校验（避免把用户重定向到一个必然失败或不安全的 OAuth 流程里）。
	if strings.TrimSpace(effective.ClientID) == "" {
		return authconfig.LinuxDoConnectConfig{}, apperror.InternalServer("OAUTH_CONFIG_INVALID", "oauth client id not configured")
	}
	if strings.TrimSpace(effective.AuthorizeURL) == "" {
		return authconfig.LinuxDoConnectConfig{}, apperror.InternalServer("OAUTH_CONFIG_INVALID", "oauth authorize url not configured")
	}
	if strings.TrimSpace(effective.TokenURL) == "" {
		return authconfig.LinuxDoConnectConfig{}, apperror.InternalServer("OAUTH_CONFIG_INVALID", "oauth token url not configured")
	}
	if strings.TrimSpace(effective.UserInfoURL) == "" {
		return authconfig.LinuxDoConnectConfig{}, apperror.InternalServer("OAUTH_CONFIG_INVALID", "oauth userinfo url not configured")
	}
	if strings.TrimSpace(effective.RedirectURL) == "" {
		return authconfig.LinuxDoConnectConfig{}, apperror.InternalServer("OAUTH_CONFIG_INVALID", "oauth redirect url not configured")
	}
	if strings.TrimSpace(effective.FrontendRedirectURL) == "" {
		return authconfig.LinuxDoConnectConfig{}, apperror.InternalServer("OAUTH_CONFIG_INVALID", "oauth frontend redirect url not configured")
	}

	if err := authconfig.ValidateAbsoluteHTTPURL(effective.AuthorizeURL); err != nil {
		return authconfig.LinuxDoConnectConfig{}, apperror.InternalServer("OAUTH_CONFIG_INVALID", "oauth authorize url invalid")
	}
	if err := authconfig.ValidateAbsoluteHTTPURL(effective.TokenURL); err != nil {
		return authconfig.LinuxDoConnectConfig{}, apperror.InternalServer("OAUTH_CONFIG_INVALID", "oauth token url invalid")
	}
	if err := authconfig.ValidateAbsoluteHTTPURL(effective.UserInfoURL); err != nil {
		return authconfig.LinuxDoConnectConfig{}, apperror.InternalServer("OAUTH_CONFIG_INVALID", "oauth userinfo url invalid")
	}
	if err := authconfig.ValidateAbsoluteHTTPURL(effective.RedirectURL); err != nil {
		return authconfig.LinuxDoConnectConfig{}, apperror.InternalServer("OAUTH_CONFIG_INVALID", "oauth redirect url invalid")
	}
	if err := authconfig.ValidateFrontendRedirectURL(effective.FrontendRedirectURL); err != nil {
		return authconfig.LinuxDoConnectConfig{}, apperror.InternalServer("OAUTH_CONFIG_INVALID", "oauth frontend redirect url invalid")
	}

	method := strings.ToLower(strings.TrimSpace(effective.TokenAuthMethod))
	switch method {
	case "", "client_secret_post", "client_secret_basic":
		if strings.TrimSpace(effective.ClientSecret) == "" {
			return authconfig.LinuxDoConnectConfig{}, apperror.InternalServer("OAUTH_CONFIG_INVALID", "oauth client secret not configured")
		}
	case "none":
	default:
		return authconfig.LinuxDoConnectConfig{}, apperror.InternalServer("OAUTH_CONFIG_INVALID", "oauth token_auth_method invalid")
	}

	return effective, nil
}

func (s *OAuthSettings) GetDingTalkConnectOAuthConfig(ctx context.Context) (authconfig.DingTalkConnectConfig, error) {
	if s == nil || s.defaults == nil {
		return authconfig.DingTalkConnectConfig{}, apperror.ServiceUnavailable("CONFIG_NOT_READY", "config not loaded")
	}

	effective := s.defaults.DingTalk
	effective.AttributeSyncFields = slices.Clone(effective.AttributeSyncFields)

	keys := []string{
		SettingKeyDingTalkConnectEnabled,
		SettingKeyDingTalkConnectClientID,
		SettingKeyDingTalkConnectClientSecret,
		SettingKeyDingTalkConnectRedirectURL,
		SettingKeyDingTalkConnectCorpRestrictionPolicy,
		SettingKeyDingTalkConnectInternalCorpID,
		SettingKeyDingTalkConnectBypassRegistration,
		SettingKeyDingTalkConnectSyncCorpEmail,
		SettingKeyDingTalkConnectSyncDisplayName,
		SettingKeyDingTalkConnectSyncDept,
		SettingKeyDingTalkConnectSyncCorpEmailAttrKey,
		SettingKeyDingTalkConnectSyncDisplayNameAttrKey,
		SettingKeyDingTalkConnectSyncDeptAttrKey,
	}
	settings, err := s.settingRepo.GetMultiple(ctx, keys)
	if err != nil {
		return authconfig.DingTalkConnectConfig{}, fmt.Errorf("get dingtalk connect settings: %w", err)
	}

	if raw, ok := settings[SettingKeyDingTalkConnectEnabled]; ok {
		effective.Enabled = raw == "true"
	}
	if v, ok := settings[SettingKeyDingTalkConnectClientID]; ok && strings.TrimSpace(v) != "" {
		effective.ClientID = strings.TrimSpace(v)
	}
	if v, ok := settings[SettingKeyDingTalkConnectClientSecret]; ok && strings.TrimSpace(v) != "" {
		effective.ClientSecret = strings.TrimSpace(v)
	}
	if v, ok := settings[SettingKeyDingTalkConnectRedirectURL]; ok && strings.TrimSpace(v) != "" {
		effective.RedirectURL = strings.TrimSpace(v)
	}
	if v, ok := settings[SettingKeyDingTalkConnectCorpRestrictionPolicy]; ok && strings.TrimSpace(v) != "" {
		effective.CorpRestrictionPolicy = strings.TrimSpace(v)
	}
	effective.CorpRestrictionPolicy = SettingsCoerceDeprecatedDingTalkCorpPolicy(effective.CorpRestrictionPolicy)
	if v, ok := settings[SettingKeyDingTalkConnectInternalCorpID]; ok && strings.TrimSpace(v) != "" {
		effective.InternalCorpID = strings.TrimSpace(v)
	}
	if v, ok := settings[SettingKeyDingTalkConnectBypassRegistration]; ok && strings.TrimSpace(v) != "" {
		effective.BypassRegistration = strings.EqualFold(strings.TrimSpace(v), "true")
	}
	// bypass_registration 仅在 internal_only 模式下有意义；其它策略下强制 false，
	// 以保证 OAuth callback 看到的 effective config 永远是一致状态。
	if effective.CorpRestrictionPolicy != "internal_only" {
		effective.BypassRegistration = false
	}

	if v, ok := settings[SettingKeyDingTalkConnectSyncCorpEmail]; ok && strings.TrimSpace(v) != "" {
		effective.SyncCorpEmail = strings.EqualFold(strings.TrimSpace(v), "true")
	}
	if v, ok := settings[SettingKeyDingTalkConnectSyncDisplayName]; ok && strings.TrimSpace(v) != "" {
		effective.SyncDisplayName = strings.EqualFold(strings.TrimSpace(v), "true")
	}
	if v, ok := settings[SettingKeyDingTalkConnectSyncDept]; ok && strings.TrimSpace(v) != "" {
		effective.SyncDept = strings.EqualFold(strings.TrimSpace(v), "true")
	}
	// 身份同步三开关仅在 internal_only 模式下有意义；其它策略强制 false。
	if effective.CorpRestrictionPolicy != "internal_only" {
		effective.SyncCorpEmail = false
		effective.SyncDisplayName = false
		effective.SyncDept = false
	}

	// 身份同步目标 attr key（DB 空 → fallback 默认值）
	if v := strings.TrimSpace(settings[SettingKeyDingTalkConnectSyncCorpEmailAttrKey]); v != "" {
		effective.SyncCorpEmailAttrKey = v
	}
	if effective.SyncCorpEmailAttrKey == "" {
		effective.SyncCorpEmailAttrKey = "dingtalk_email"
	}
	if v := strings.TrimSpace(settings[SettingKeyDingTalkConnectSyncDisplayNameAttrKey]); v != "" {
		effective.SyncDisplayNameAttrKey = v
	}
	if effective.SyncDisplayNameAttrKey == "" {
		effective.SyncDisplayNameAttrKey = "dingtalk_name"
	}
	if v := strings.TrimSpace(settings[SettingKeyDingTalkConnectSyncDeptAttrKey]); v != "" {
		effective.SyncDeptAttrKey = v
	}
	if effective.SyncDeptAttrKey == "" {
		effective.SyncDeptAttrKey = "dingtalk_department"
	}

	if !effective.Enabled {
		return authconfig.DingTalkConnectConfig{}, apperror.NotFound("OAUTH_DISABLED", "dingtalk oauth login is disabled")
	}

	// 基础健壮性校验（避免把用户重定向到一个必然失败或不安全的 OAuth 流程里）。
	if strings.TrimSpace(effective.ClientID) == "" {
		return authconfig.DingTalkConnectConfig{}, apperror.InternalServer("OAUTH_CONFIG_INVALID", "dingtalk oauth client id not configured")
	}
	if strings.TrimSpace(effective.AuthorizeURL) == "" {
		return authconfig.DingTalkConnectConfig{}, apperror.InternalServer("OAUTH_CONFIG_INVALID", "dingtalk oauth authorize url not configured")
	}
	if strings.TrimSpace(effective.TokenURL) == "" {
		return authconfig.DingTalkConnectConfig{}, apperror.InternalServer("OAUTH_CONFIG_INVALID", "dingtalk oauth token url not configured")
	}
	if strings.TrimSpace(effective.UserInfoURL) == "" {
		return authconfig.DingTalkConnectConfig{}, apperror.InternalServer("OAUTH_CONFIG_INVALID", "dingtalk oauth userinfo url not configured")
	}
	if strings.TrimSpace(effective.RedirectURL) == "" {
		return authconfig.DingTalkConnectConfig{}, apperror.InternalServer("OAUTH_CONFIG_INVALID", "dingtalk oauth redirect url not configured")
	}
	if strings.TrimSpace(effective.FrontendRedirectURL) == "" {
		return authconfig.DingTalkConnectConfig{}, apperror.InternalServer("OAUTH_CONFIG_INVALID", "dingtalk oauth frontend redirect url not configured")
	}

	if err := authconfig.ValidateAbsoluteHTTPURL(effective.AuthorizeURL); err != nil {
		return authconfig.DingTalkConnectConfig{}, apperror.InternalServer("OAUTH_CONFIG_INVALID", "dingtalk oauth authorize url invalid")
	}
	if err := authconfig.ValidateAbsoluteHTTPURL(effective.TokenURL); err != nil {
		return authconfig.DingTalkConnectConfig{}, apperror.InternalServer("OAUTH_CONFIG_INVALID", "dingtalk oauth token url invalid")
	}
	if err := authconfig.ValidateAbsoluteHTTPURL(effective.UserInfoURL); err != nil {
		return authconfig.DingTalkConnectConfig{}, apperror.InternalServer("OAUTH_CONFIG_INVALID", "dingtalk oauth userinfo url invalid")
	}
	if err := authconfig.ValidateAbsoluteHTTPURL(effective.RedirectURL); err != nil {
		return authconfig.DingTalkConnectConfig{}, apperror.InternalServer("OAUTH_CONFIG_INVALID", "dingtalk oauth redirect url invalid")
	}
	if err := authconfig.ValidateFrontendRedirectURL(effective.FrontendRedirectURL); err != nil {
		return authconfig.DingTalkConnectConfig{}, apperror.InternalServer("OAUTH_CONFIG_INVALID", "dingtalk oauth frontend redirect url invalid")
	}
	if strings.TrimSpace(effective.ClientSecret) == "" {
		return authconfig.DingTalkConnectConfig{}, apperror.InternalServer("OAUTH_CONFIG_INVALID", "dingtalk oauth client secret not configured")
	}

	// 镜像 admin handler 行为：internal_only policy 隐式要求 AppType=internal
	if effective.CorpRestrictionPolicy == "internal_only" {
		effective.AppType = "internal"
	}

	if err := authconfig.ValidateDingTalkConfig(effective); err != nil {
		return authconfig.DingTalkConnectConfig{}, apperror.InternalServer("OAUTH_CONFIG_INVALID", err.Error())
	}

	return effective, nil
}

func (s *OAuthSettings) GetWeChatConnectOAuthConfig(ctx context.Context) (WeChatConnectOAuthConfig, error) {
	keys := []string{
		SettingKeyWeChatConnectEnabled,
		SettingKeyWeChatConnectAppID,
		SettingKeyWeChatConnectAppSecret,
		SettingKeyWeChatConnectOpenAppID,
		SettingKeyWeChatConnectOpenAppSecret,
		SettingKeyWeChatConnectMPAppID,
		SettingKeyWeChatConnectMPAppSecret,
		SettingKeyWeChatConnectMobileAppID,
		SettingKeyWeChatConnectMobileAppSecret,
		SettingKeyWeChatConnectOpenEnabled,
		SettingKeyWeChatConnectMPEnabled,
		SettingKeyWeChatConnectMobileEnabled,
		SettingKeyWeChatConnectMode,
		SettingKeyWeChatConnectScopes,
		SettingKeyWeChatConnectRedirectURL,
		SettingKeyWeChatConnectFrontendRedirectURL,
	}
	settings, err := s.settingRepo.GetMultiple(ctx, keys)
	if err != nil {
		return WeChatConnectOAuthConfig{}, fmt.Errorf("get wechat connect settings: %w", err)
	}
	return s.ParseWeChatConnectOAuthConfig(settings)
}

func (s *OAuthSettings) GetOIDCConnectOAuthConfig(ctx context.Context) (authconfig.OIDCConnectConfig, error) {
	if s == nil || s.defaults == nil {
		return authconfig.OIDCConnectConfig{}, apperror.ServiceUnavailable("CONFIG_NOT_READY", "config not loaded")
	}

	effective := s.defaults.OIDC

	keys := []string{
		SettingKeyOIDCConnectEnabled,
		SettingKeyOIDCConnectProviderName,
		SettingKeyOIDCConnectClientID,
		SettingKeyOIDCConnectClientSecret,
		SettingKeyOIDCConnectIssuerURL,
		SettingKeyOIDCConnectDiscoveryURL,
		SettingKeyOIDCConnectAuthorizeURL,
		SettingKeyOIDCConnectTokenURL,
		SettingKeyOIDCConnectUserInfoURL,
		SettingKeyOIDCConnectJWKSURL,
		SettingKeyOIDCConnectScopes,
		SettingKeyOIDCConnectRedirectURL,
		SettingKeyOIDCConnectFrontendRedirectURL,
		SettingKeyOIDCConnectTokenAuthMethod,
		SettingKeyOIDCConnectUsePKCE,
		SettingKeyOIDCConnectValidateIDToken,
		SettingKeyOIDCConnectAllowedSigningAlgs,
		SettingKeyOIDCConnectClockSkewSeconds,
		SettingKeyOIDCConnectRequireEmailVerified,
		SettingKeyOIDCConnectUserInfoEmailPath,
		SettingKeyOIDCConnectUserInfoIDPath,
		SettingKeyOIDCConnectUserInfoUsernamePath,
	}
	settings, err := s.settingRepo.GetMultiple(ctx, keys)
	if err != nil {
		return authconfig.OIDCConnectConfig{}, fmt.Errorf("get oidc connect settings: %w", err)
	}

	if raw, ok := settings[SettingKeyOIDCConnectEnabled]; ok {
		effective.Enabled = raw == "true"
	}
	if v, ok := settings[SettingKeyOIDCConnectProviderName]; ok && strings.TrimSpace(v) != "" {
		effective.ProviderName = strings.TrimSpace(v)
	}
	if v, ok := settings[SettingKeyOIDCConnectClientID]; ok && strings.TrimSpace(v) != "" {
		effective.ClientID = strings.TrimSpace(v)
	}
	if v, ok := settings[SettingKeyOIDCConnectClientSecret]; ok && strings.TrimSpace(v) != "" {
		effective.ClientSecret = strings.TrimSpace(v)
	}
	if v, ok := settings[SettingKeyOIDCConnectIssuerURL]; ok && strings.TrimSpace(v) != "" {
		effective.IssuerURL = strings.TrimSpace(v)
	}
	if v, ok := settings[SettingKeyOIDCConnectDiscoveryURL]; ok && strings.TrimSpace(v) != "" {
		effective.DiscoveryURL = strings.TrimSpace(v)
	}
	if v, ok := settings[SettingKeyOIDCConnectAuthorizeURL]; ok && strings.TrimSpace(v) != "" {
		effective.AuthorizeURL = strings.TrimSpace(v)
	}
	if v, ok := settings[SettingKeyOIDCConnectTokenURL]; ok && strings.TrimSpace(v) != "" {
		effective.TokenURL = strings.TrimSpace(v)
	}
	if v, ok := settings[SettingKeyOIDCConnectUserInfoURL]; ok && strings.TrimSpace(v) != "" {
		effective.UserInfoURL = strings.TrimSpace(v)
	}
	if v, ok := settings[SettingKeyOIDCConnectJWKSURL]; ok && strings.TrimSpace(v) != "" {
		effective.JWKSURL = strings.TrimSpace(v)
	}
	if v, ok := settings[SettingKeyOIDCConnectScopes]; ok && strings.TrimSpace(v) != "" {
		effective.Scopes = strings.TrimSpace(v)
	}
	if v, ok := settings[SettingKeyOIDCConnectRedirectURL]; ok && strings.TrimSpace(v) != "" {
		effective.RedirectURL = strings.TrimSpace(v)
	}
	if v, ok := settings[SettingKeyOIDCConnectFrontendRedirectURL]; ok && strings.TrimSpace(v) != "" {
		effective.FrontendRedirectURL = strings.TrimSpace(v)
	}
	if v, ok := settings[SettingKeyOIDCConnectTokenAuthMethod]; ok && strings.TrimSpace(v) != "" {
		effective.TokenAuthMethod = strings.ToLower(strings.TrimSpace(v))
	}
	if raw, ok := settings[SettingKeyOIDCConnectUsePKCE]; ok {
		effective.UsePKCE = raw == "true"
	} else {
		effective.UsePKCE = SettingsOidcUsePKCECompatibilityDefault(effective)
	}
	if raw, ok := settings[SettingKeyOIDCConnectValidateIDToken]; ok {
		effective.ValidateIDToken = raw == "true"
	} else {
		effective.ValidateIDToken = SettingsOidcValidateIDTokenCompatibilityDefault(effective)
	}
	if v, ok := settings[SettingKeyOIDCConnectAllowedSigningAlgs]; ok && strings.TrimSpace(v) != "" {
		effective.AllowedSigningAlgs = strings.TrimSpace(v)
	}
	if raw, ok := settings[SettingKeyOIDCConnectClockSkewSeconds]; ok && strings.TrimSpace(raw) != "" {
		if parsed, parseErr := strconv.Atoi(strings.TrimSpace(raw)); parseErr == nil {
			effective.ClockSkewSeconds = parsed
		}
	}
	if raw, ok := settings[SettingKeyOIDCConnectRequireEmailVerified]; ok {
		effective.RequireEmailVerified = raw == "true"
	}
	if v, ok := settings[SettingKeyOIDCConnectUserInfoEmailPath]; ok {
		effective.UserInfoEmailPath = strings.TrimSpace(v)
	}
	if v, ok := settings[SettingKeyOIDCConnectUserInfoIDPath]; ok {
		effective.UserInfoIDPath = strings.TrimSpace(v)
	}
	if v, ok := settings[SettingKeyOIDCConnectUserInfoUsernamePath]; ok {
		effective.UserInfoUsernamePath = strings.TrimSpace(v)
	}

	if !effective.Enabled {
		return authconfig.OIDCConnectConfig{}, apperror.NotFound("OAUTH_DISABLED", "oauth login is disabled")
	}
	if strings.TrimSpace(effective.ProviderName) == "" {
		effective.ProviderName = "OIDC"
	}
	if strings.TrimSpace(effective.ClientID) == "" {
		return authconfig.OIDCConnectConfig{}, apperror.InternalServer("OAUTH_CONFIG_INVALID", "oauth client id not configured")
	}
	if strings.TrimSpace(effective.IssuerURL) == "" {
		return authconfig.OIDCConnectConfig{}, apperror.InternalServer("OAUTH_CONFIG_INVALID", "oauth issuer url not configured")
	}
	if strings.TrimSpace(effective.RedirectURL) == "" {
		return authconfig.OIDCConnectConfig{}, apperror.InternalServer("OAUTH_CONFIG_INVALID", "oauth redirect url not configured")
	}
	if strings.TrimSpace(effective.FrontendRedirectURL) == "" {
		return authconfig.OIDCConnectConfig{}, apperror.InternalServer("OAUTH_CONFIG_INVALID", "oauth frontend redirect url not configured")
	}
	if !SettingsScopesContainOpenID(effective.Scopes) {
		return authconfig.OIDCConnectConfig{}, apperror.InternalServer("OAUTH_CONFIG_INVALID", "oauth scopes must contain openid")
	}
	if effective.ClockSkewSeconds < 0 || effective.ClockSkewSeconds > 600 {
		return authconfig.OIDCConnectConfig{}, apperror.InternalServer("OAUTH_CONFIG_INVALID", "oauth clock skew must be between 0 and 600")
	}

	if err := authconfig.ValidateAbsoluteHTTPURL(effective.IssuerURL); err != nil {
		return authconfig.OIDCConnectConfig{}, apperror.InternalServer("OAUTH_CONFIG_INVALID", "oauth issuer url invalid")
	}

	discoveryURL := strings.TrimSpace(effective.DiscoveryURL)
	if discoveryURL == "" {
		discoveryURL = SettingsOidcDefaultDiscoveryURL(effective.IssuerURL)
		effective.DiscoveryURL = discoveryURL
	}
	if discoveryURL != "" {
		if err := authconfig.ValidateAbsoluteHTTPURL(discoveryURL); err != nil {
			return authconfig.OIDCConnectConfig{}, apperror.InternalServer("OAUTH_CONFIG_INVALID", "oauth discovery url invalid")
		}
	}

	needsDiscovery := strings.TrimSpace(effective.AuthorizeURL) == "" ||
		strings.TrimSpace(effective.TokenURL) == "" ||
		(effective.ValidateIDToken && strings.TrimSpace(effective.JWKSURL) == "")
	if needsDiscovery && discoveryURL != "" {
		metadata, resolveErr := s.resolveMetadata(ctx, discoveryURL)
		if resolveErr != nil {
			return authconfig.OIDCConnectConfig{}, apperror.InternalServer("OAUTH_CONFIG_INVALID", "oauth discovery resolve failed").WithCause(resolveErr)
		}
		if strings.TrimSpace(effective.AuthorizeURL) == "" {
			effective.AuthorizeURL = strings.TrimSpace(metadata.AuthorizationEndpoint)
		}
		if strings.TrimSpace(effective.TokenURL) == "" {
			effective.TokenURL = strings.TrimSpace(metadata.TokenEndpoint)
		}
		if strings.TrimSpace(effective.UserInfoURL) == "" {
			effective.UserInfoURL = strings.TrimSpace(metadata.UserInfoEndpoint)
		}
		if strings.TrimSpace(effective.JWKSURL) == "" {
			effective.JWKSURL = strings.TrimSpace(metadata.JWKSURI)
		}
	}

	if strings.TrimSpace(effective.AuthorizeURL) == "" {
		return authconfig.OIDCConnectConfig{}, apperror.InternalServer("OAUTH_CONFIG_INVALID", "oauth authorize url not configured")
	}
	if strings.TrimSpace(effective.TokenURL) == "" {
		return authconfig.OIDCConnectConfig{}, apperror.InternalServer("OAUTH_CONFIG_INVALID", "oauth token url not configured")
	}
	if err := authconfig.ValidateAbsoluteHTTPURL(effective.AuthorizeURL); err != nil {
		return authconfig.OIDCConnectConfig{}, apperror.InternalServer("OAUTH_CONFIG_INVALID", "oauth authorize url invalid")
	}
	if err := authconfig.ValidateAbsoluteHTTPURL(effective.TokenURL); err != nil {
		return authconfig.OIDCConnectConfig{}, apperror.InternalServer("OAUTH_CONFIG_INVALID", "oauth token url invalid")
	}
	if v := strings.TrimSpace(effective.UserInfoURL); v != "" {
		if err := authconfig.ValidateAbsoluteHTTPURL(v); err != nil {
			return authconfig.OIDCConnectConfig{}, apperror.InternalServer("OAUTH_CONFIG_INVALID", "oauth userinfo url invalid")
		}
	}
	if effective.ValidateIDToken {
		if strings.TrimSpace(effective.JWKSURL) == "" {
			return authconfig.OIDCConnectConfig{}, apperror.InternalServer("OAUTH_CONFIG_INVALID", "oauth jwks url not configured")
		}
		if strings.TrimSpace(effective.AllowedSigningAlgs) == "" {
			return authconfig.OIDCConnectConfig{}, apperror.InternalServer("OAUTH_CONFIG_INVALID", "oauth signing algs not configured")
		}
	}
	if v := strings.TrimSpace(effective.JWKSURL); v != "" {
		if err := authconfig.ValidateAbsoluteHTTPURL(v); err != nil {
			return authconfig.OIDCConnectConfig{}, apperror.InternalServer("OAUTH_CONFIG_INVALID", "oauth jwks url invalid")
		}
	}
	if err := authconfig.ValidateAbsoluteHTTPURL(effective.RedirectURL); err != nil {
		return authconfig.OIDCConnectConfig{}, apperror.InternalServer("OAUTH_CONFIG_INVALID", "oauth redirect url invalid")
	}
	if err := authconfig.ValidateFrontendRedirectURL(effective.FrontendRedirectURL); err != nil {
		return authconfig.OIDCConnectConfig{}, apperror.InternalServer("OAUTH_CONFIG_INVALID", "oauth frontend redirect url invalid")
	}

	method := strings.ToLower(strings.TrimSpace(effective.TokenAuthMethod))
	switch method {
	case "", "client_secret_post", "client_secret_basic":
		if strings.TrimSpace(effective.ClientSecret) == "" {
			return authconfig.OIDCConnectConfig{}, apperror.InternalServer("OAUTH_CONFIG_INVALID", "oauth client secret not configured")
		}
	case "none":
	default:
		return authconfig.OIDCConnectConfig{}, apperror.InternalServer("OAUTH_CONFIG_INVALID", "oauth token_auth_method invalid")
	}

	return effective, nil
}

func SettingsScopesContainOpenID(scopes string) bool {
	for _, scope := range strings.Fields(strings.ToLower(strings.TrimSpace(scopes))) {
		if scope == "openid" {
			return true
		}
	}
	return false
}

func SettingsOidcDefaultDiscoveryURL(issuerURL string) string {
	issuerURL = strings.TrimSpace(issuerURL)
	if issuerURL == "" {
		return ""
	}
	return strings.TrimRight(issuerURL, "/") + "/.well-known/openid-configuration"
}

func (s *OAuthSettings) BaseEmailOAuthConfig(provider string) authconfig.EmailOAuthProviderConfig {
	switch strings.ToLower(strings.TrimSpace(provider)) {
	case "github":
		base := authconfig.EmailOAuthProviderConfig{
			AuthorizeURL:        OAuthDefaultGitHubOAuthAuthorize,
			TokenURL:            OAuthDefaultGitHubOAuthToken,
			UserInfoURL:         OAuthDefaultGitHubOAuthUserInfo,
			EmailsURL:           OAuthDefaultGitHubOAuthEmails,
			Scopes:              OAuthDefaultGitHubOAuthScopes,
			FrontendRedirectURL: OAuthDefaultGitHubOAuthFrontend,
		}
		if s != nil && s.defaults != nil {
			return SettingsMergeEmailOAuthBaseConfig(base, s.defaults.GitHubOAuth)
		}
		return base
	case "google":
		base := authconfig.EmailOAuthProviderConfig{
			AuthorizeURL:        OAuthDefaultGoogleOAuthAuthorize,
			TokenURL:            OAuthDefaultGoogleOAuthToken,
			UserInfoURL:         OAuthDefaultGoogleOAuthUserInfo,
			Scopes:              OAuthDefaultGoogleOAuthScopes,
			FrontendRedirectURL: OAuthDefaultGoogleOAuthFrontend,
		}
		if s != nil && s.defaults != nil {
			return SettingsMergeEmailOAuthBaseConfig(base, s.defaults.GoogleOAuth)
		}
		return base
	default:
		return authconfig.EmailOAuthProviderConfig{}
	}
}

func SettingsEmailOAuthSettingKeys(provider string) (enabled, clientID, clientSecret, redirectURL, frontendRedirectURL string) {
	switch strings.ToLower(strings.TrimSpace(provider)) {
	case "github":
		return SettingKeyGitHubOAuthEnabled,
			SettingKeyGitHubOAuthClientID,
			SettingKeyGitHubOAuthClientSecret,
			SettingKeyGitHubOAuthRedirectURL,
			SettingKeyGitHubOAuthFrontendRedirectURL
	case "google":
		return SettingKeyGoogleOAuthEnabled,
			SettingKeyGoogleOAuthClientID,
			SettingKeyGoogleOAuthClientSecret,
			SettingKeyGoogleOAuthRedirectURL,
			SettingKeyGoogleOAuthFrontendRedirectURL
	default:
		return "", "", "", "", ""
	}
}

type WeChatConnectOAuthConfig struct {
	Enabled             bool
	LegacyAppID         string
	LegacyAppSecret     string
	OpenAppID           string
	OpenAppSecret       string
	MPAppID             string
	MPAppSecret         string
	MobileAppID         string
	MobileAppSecret     string
	OpenEnabled         bool
	MPEnabled           bool
	MobileEnabled       bool
	Mode                string
	Scopes              string
	RedirectURL         string
	FrontendRedirectURL string
}

func (cfg WeChatConnectOAuthConfig) SupportsMode(mode string) bool {
	switch NormalizeWeChatConnectModeSetting(mode) {
	case "mp":
		return cfg.MPEnabled
	case "mobile":
		return cfg.MobileEnabled
	default:
		return cfg.OpenEnabled
	}
}

func (cfg WeChatConnectOAuthConfig) ScopeForMode(mode string) string {
	switch NormalizeWeChatConnectModeSetting(mode) {
	case "mp":
		return SettingsNormalizeWeChatConnectScopeSetting(cfg.Scopes, "mp")
	case "mobile":
		return ""
	}
	return SettingsDefaultWeChatConnectScopeForMode("open")
}

func (cfg WeChatConnectOAuthConfig) AppIDForMode(mode string) string {
	switch NormalizeWeChatConnectModeSetting(mode) {
	case "mp":
		return strings.TrimSpace(oauthSettingsFirstNonEmpty(cfg.MPAppID, cfg.LegacyAppID))
	case "mobile":
		return strings.TrimSpace(oauthSettingsFirstNonEmpty(cfg.MobileAppID, cfg.LegacyAppID))
	}
	return strings.TrimSpace(oauthSettingsFirstNonEmpty(cfg.OpenAppID, cfg.LegacyAppID))
}

func (cfg WeChatConnectOAuthConfig) AppSecretForMode(mode string) string {
	switch NormalizeWeChatConnectModeSetting(mode) {
	case "mp":
		return strings.TrimSpace(oauthSettingsFirstNonEmpty(cfg.MPAppSecret, cfg.LegacyAppSecret))
	case "mobile":
		return strings.TrimSpace(oauthSettingsFirstNonEmpty(cfg.MobileAppSecret, cfg.LegacyAppSecret))
	}
	return strings.TrimSpace(oauthSettingsFirstNonEmpty(cfg.OpenAppSecret, cfg.LegacyAppSecret))
}

// oauthSettingsFirstNonEmpty 保留原首个非空值优先级。
func oauthSettingsFirstNonEmpty(values ...string) string {
	for _, value := range values {
		if value = strings.TrimSpace(value); value != "" {
			return value
		}
	}
	return ""
}
