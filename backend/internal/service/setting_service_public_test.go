//go:build unit

package service

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/BrandonVee/TokenRouter/internal/config"
	"github.com/stretchr/testify/require"
)

type settingPublicRepoStub struct {
	values map[string]string
	err    error
}

func (s *settingPublicRepoStub) Get(ctx context.Context, key string) (*Setting, error) {
	panic("unexpected Get call")
}

func (s *settingPublicRepoStub) GetValue(ctx context.Context, key string) (string, error) {
	panic("unexpected GetValue call")
}

func (s *settingPublicRepoStub) Set(ctx context.Context, key, value string) error {
	panic("unexpected Set call")
}

func (s *settingPublicRepoStub) GetMultiple(ctx context.Context, keys []string) (map[string]string, error) {
	if s.err != nil {
		return nil, s.err
	}
	out := make(map[string]string, len(keys))
	for _, key := range keys {
		if value, ok := s.values[key]; ok {
			out[key] = value
		}
	}
	return out, nil
}

func (s *settingPublicRepoStub) SetMultiple(ctx context.Context, settings map[string]string) error {
	panic("unexpected SetMultiple call")
}

func (s *settingPublicRepoStub) GetAll(ctx context.Context) (map[string]string, error) {
	panic("unexpected GetAll call")
}

func (s *settingPublicRepoStub) Delete(ctx context.Context, key string) error {
	panic("unexpected Delete call")
}

func TestSettingService_GetPublicSettings_ExposesRegistrationEmailSuffixWhitelist(t *testing.T) {
	repo := &settingPublicRepoStub{
		values: map[string]string{
			SettingKeyRegistrationEnabled:                 "true",
			SettingKeyEmailVerifyEnabled:                  "true",
			SettingKeyRegistrationEmailSuffixWhitelist:    `["@EXAMPLE.com"," @foo.bar ","*.EDU.CN","@invalid_domain",""]`,
			SettingKeyRegistrationEmailDomainQuotaEnabled: "true",
		},
	}
	svc := NewSettingService(repo, &config.Config{})

	settings, err := svc.GetPublicSettings(context.Background())
	require.NoError(t, err)
	require.Equal(t, []string{"@example.com", "@foo.bar", "*.edu.cn"}, settings.RegistrationEmailSuffixWhitelist)
	require.True(t, settings.RegistrationEmailDomainQuotaEnabled)
}

func TestSettingService_GetPublicSettings_ExposesTablePreferences(t *testing.T) {
	repo := &settingPublicRepoStub{
		values: map[string]string{
			SettingKeyTableDefaultPageSize: "50",
			SettingKeyTablePageSizeOptions: "[20,50,100]",
		},
	}
	svc := NewSettingService(repo, &config.Config{})

	settings, err := svc.GetPublicSettings(context.Background())
	require.NoError(t, err)
	require.Equal(t, 50, settings.TableDefaultPageSize)
	require.Equal(t, []int{20, 50, 100}, settings.TablePageSizeOptions)
}

func TestSettingService_GetPublicSettings_ExposesForceEmailOnThirdPartySignup(t *testing.T) {
	repo := &settingPublicRepoStub{
		values: map[string]string{
			SettingKeyForceEmailOnThirdPartySignup: "true",
		},
	}
	svc := NewSettingService(repo, &config.Config{})

	settings, err := svc.GetPublicSettings(context.Background())
	require.NoError(t, err)
	require.True(t, settings.ForceEmailOnThirdPartySignup)
}

// 公开配置必须透传邀请返利开关，否则前端侧栏和路由守卫会把入口隐藏。
func TestSettingService_GetPublicSettings_ExposesAffiliateEnabled(t *testing.T) {
	repo := &settingPublicRepoStub{
		values: map[string]string{
			SettingKeyAffiliateEnabled: "true",
		},
	}
	svc := NewSettingService(repo, &config.Config{})

	settings, err := svc.GetPublicSettings(context.Background())
	require.NoError(t, err)
	require.True(t, settings.AffiliateEnabled)
}

// 页面开关必须在公开设置中明确返回，供侧边栏和路由守卫共用。
func TestSettingService_GetPublicSettings_ExposesPageFeatureFlags(t *testing.T) {
	repo := &settingPublicRepoStub{
		values: map[string]string{
			SettingKeyTeamEnabled:        "false",
			SettingKeyDataSharingEnabled: "false",
		},
	}
	svc := NewSettingService(repo, &config.Config{Team: config.TeamConfig{Enabled: true}})

	settings, err := svc.GetPublicSettings(context.Background())
	require.NoError(t, err)
	require.False(t, settings.TeamEnabled)
	require.False(t, settings.DataSharingEnabled)
}

func TestSettingService_GetPublicSettings_ExposesAllowUserViewErrorRequests(t *testing.T) {
	repo := &settingPublicRepoStub{
		values: map[string]string{
			SettingKeyAllowUserViewErrorRequests: "true",
		},
	}
	svc := NewSettingService(repo, &config.Config{})

	settings, err := svc.GetPublicSettings(context.Background())
	require.NoError(t, err)
	require.True(t, settings.AllowUserViewErrorRequests)
}

// HTML 首屏注入配置要与 /settings/public 保持一致，避免刷新后菜单先按旧默认值渲染。
func TestSettingService_GetPublicSettingsForInjection_ExposesPublicFeatureFlags(t *testing.T) {
	repo := &settingPublicRepoStub{
		values: map[string]string{
			SettingKeyAffiliateEnabled:                    "true",
			SettingKeyForceEmailOnThirdPartySignup:        "true",
			SettingKeyRegistrationEmailDomainQuotaEnabled: "true",
			SettingKeyAllowUserViewErrorRequests:          "true",
			SettingKeyTeamEnabled:                         "true",
			SettingKeyDataSharingEnabled:                  "false",
		},
	}
	svc := NewSettingService(repo, &config.Config{
		Team:     config.TeamConfig{Enabled: true},
		WebAuthn: config.WebAuthnConfig{Enabled: true},
	})

	payload, err := svc.GetPublicSettingsForInjection(context.Background())
	require.NoError(t, err)

	encoded, err := json.Marshal(payload)
	require.NoError(t, err)

	var settings struct {
		AffiliateEnabled                    bool `json:"affiliate_enabled"`
		ForceEmailOnThirdPartySignup        bool `json:"force_email_on_third_party_signup"`
		AllowUserViewErrorRequests          bool `json:"allow_user_view_error_requests"`
		TeamEnabled                         bool `json:"team_enabled"`
		DataSharingEnabled                  bool `json:"data_sharing_enabled"`
		PasskeyEnabled                      bool `json:"passkey_enabled"`
		RegistrationEmailDomainQuotaEnabled bool `json:"registration_email_domain_quota_enabled"`
	}
	require.NoError(t, json.Unmarshal(encoded, &settings))
	require.True(t, settings.AffiliateEnabled)
	require.True(t, settings.ForceEmailOnThirdPartySignup)
	require.True(t, settings.AllowUserViewErrorRequests)
	require.True(t, settings.TeamEnabled)
	require.False(t, settings.DataSharingEnabled)
	require.True(t, settings.PasskeyEnabled)
	require.True(t, settings.RegistrationEmailDomainQuotaEnabled)
}

func TestSettingService_GetPublicSettings_ExposesWeChatOAuthModeCapabilities(t *testing.T) {
	svc := NewSettingService(&settingPublicRepoStub{
		values: map[string]string{
			SettingKeyWeChatConnectEnabled:             "true",
			SettingKeyWeChatConnectAppID:               "wx-mp-app",
			SettingKeyWeChatConnectAppSecret:           "wx-mp-secret",
			SettingKeyWeChatConnectMode:                "mp",
			SettingKeyWeChatConnectScopes:              "snsapi_base",
			SettingKeyWeChatConnectOpenEnabled:         "true",
			SettingKeyWeChatConnectMPEnabled:           "true",
			SettingKeyWeChatConnectRedirectURL:         "https://api.example.com/api/v1/auth/oauth/wechat/callback",
			SettingKeyWeChatConnectFrontendRedirectURL: "/auth/wechat/callback",
		},
	}, &config.Config{})

	settings, err := svc.GetPublicSettings(context.Background())
	require.NoError(t, err)
	require.True(t, settings.WeChatOAuthEnabled)
	require.True(t, settings.WeChatOAuthOpenEnabled)
	require.True(t, settings.WeChatOAuthMPEnabled)
}

func TestSettingService_GetPublicSettings_DoesNotExposeMobileOnlyWeChatAsWebOAuthAvailable(t *testing.T) {
	svc := NewSettingService(&settingPublicRepoStub{
		values: map[string]string{
			SettingKeyWeChatConnectEnabled:             "true",
			SettingKeyWeChatConnectMobileEnabled:       "true",
			SettingKeyWeChatConnectMode:                "mobile",
			SettingKeyWeChatConnectMobileAppID:         "wx-mobile-app",
			SettingKeyWeChatConnectMobileAppSecret:     "wx-mobile-secret",
			SettingKeyWeChatConnectFrontendRedirectURL: "/auth/wechat/callback",
		},
	}, &config.Config{})

	settings, err := svc.GetPublicSettings(context.Background())
	require.NoError(t, err)
	require.False(t, settings.WeChatOAuthEnabled)
	require.False(t, settings.WeChatOAuthOpenEnabled)
	require.False(t, settings.WeChatOAuthMPEnabled)
	require.True(t, settings.WeChatOAuthMobileEnabled)
}

func TestSettingService_GetPublicSettings_FallsBackToConfigForWeChatOAuthCapabilities(t *testing.T) {
	svc := NewSettingService(&settingPublicRepoStub{values: map[string]string{}}, &config.Config{
		WeChat: config.WeChatConnectConfig{
			Enabled:             true,
			OpenEnabled:         true,
			OpenAppID:           "wx-open-config",
			OpenAppSecret:       "wx-open-secret",
			FrontendRedirectURL: "/auth/wechat/config-callback",
		},
	})

	settings, err := svc.GetPublicSettings(context.Background())
	require.NoError(t, err)
	require.True(t, settings.WeChatOAuthEnabled)
	require.True(t, settings.WeChatOAuthOpenEnabled)
	require.False(t, settings.WeChatOAuthMPEnabled)
	require.False(t, settings.WeChatOAuthMobileEnabled)
}

func TestSettingService_GetPublicSettings_ExposesEffectiveGoogleOneTapConfig(t *testing.T) {
	svc := NewSettingService(&settingPublicRepoStub{values: map[string]string{
		SettingKeyGoogleOneTapEnabled:            "true",
		SettingKeyGoogleOAuthEnabled:             "true",
		SettingKeyGoogleOAuthClientID:            "google-web-client",
		SettingKeyGoogleOAuthClientSecret:        "google-client-secret",
		SettingKeyGoogleOAuthRedirectURL:         "https://app.example/api/v1/auth/oauth/google/callback",
		SettingKeyGoogleOAuthFrontendRedirectURL: "/auth/oauth/callback",
	}}, &config.Config{})

	settings, err := svc.GetPublicSettings(context.Background())
	require.NoError(t, err)
	require.True(t, settings.GoogleOAuthEnabled)
	require.True(t, settings.GoogleOneTapEnabled)
	require.Equal(t, "google-web-client", settings.GoogleOAuthClientID)

	raw, err := json.Marshal(settings)
	require.NoError(t, err)
	require.Contains(t, string(raw), "google-web-client")
	require.NotContains(t, string(raw), "google-client-secret")
}

func TestSettingService_GetPublicSettings_DisablesGoogleOneTapWhenOAuthIsIncomplete(t *testing.T) {
	svc := NewSettingService(&settingPublicRepoStub{values: map[string]string{
		SettingKeyGoogleOneTapEnabled:    "true",
		SettingKeyGoogleOAuthEnabled:     "true",
		SettingKeyGoogleOAuthClientID:    "google-web-client",
		SettingKeyGoogleOAuthRedirectURL: "https://app.example/api/v1/auth/oauth/google/callback",
	}}, &config.Config{})

	settings, err := svc.GetPublicSettings(context.Background())
	require.NoError(t, err)
	require.False(t, settings.GoogleOAuthEnabled)
	require.False(t, settings.GoogleOneTapEnabled)
	require.Empty(t, settings.GoogleOAuthClientID)
}

func TestSettingService_GetGoogleOneTapConfigRequiresIndependentSwitch(t *testing.T) {
	values := map[string]string{
		SettingKeyGoogleOneTapEnabled:            "false",
		SettingKeyGoogleOAuthEnabled:             "true",
		SettingKeyGoogleOAuthClientID:            "google-web-client",
		SettingKeyGoogleOAuthClientSecret:        "google-client-secret",
		SettingKeyGoogleOAuthRedirectURL:         "https://app.example/api/v1/auth/oauth/google/callback",
		SettingKeyGoogleOAuthFrontendRedirectURL: "/auth/oauth/callback",
	}
	svc := NewSettingService(&settingPublicRepoStub{values: values}, &config.Config{})

	_, err := svc.GetGoogleOneTapConfig(context.Background())
	require.Error(t, err)

	values[SettingKeyGoogleOneTapEnabled] = "true"
	settings, err := svc.GetGoogleOneTapConfig(context.Background())
	require.NoError(t, err)
	require.Equal(t, "google-web-client", settings.ClientID)
	require.Equal(t, "google-client-secret", settings.ClientSecret)
}
