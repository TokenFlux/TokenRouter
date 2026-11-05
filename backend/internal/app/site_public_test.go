package app

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"

	"github.com/TokenFlux/TokenRouter/internal/config"
	"github.com/TokenFlux/TokenRouter/internal/creative"
	"github.com/TokenFlux/TokenRouter/internal/identity"
	"github.com/TokenFlux/TokenRouter/internal/pkg/timezone"
	"github.com/TokenFlux/TokenRouter/internal/promotion"
	settingscore "github.com/TokenFlux/TokenRouter/internal/settings"
	"github.com/TokenFlux/TokenRouter/internal/site"
	sitehttp "github.com/TokenFlux/TokenRouter/internal/site/httpapi"
	"github.com/TokenFlux/TokenRouter/internal/team"
	"github.com/TokenFlux/TokenRouter/internal/usage"
)

// Public settings expose interpreted authentication and feature choices through
// both HTTP and initial-page data. These scenarios check OIDC/WeChat/Google
// capability defaults, registration policy, table preferences, and user-facing
// switches, including complete Google client configuration while keeping its
// secret out of the serialized public shape.

// settingHandlerPublicRepoStub supplies batch-only values for public settings
// HTTP tests.
type settingHandlerPublicRepoStub struct {
	values map[string]string // stored public input values
}

// settingPublicRepoStub supplies public setting snapshots with an optional read
// failure.
type settingPublicRepoStub struct {
	// values supplies returned strings when err is nil. A nonnil err suppresses
	// those entries and makes GetMultiple return nil with that same error.
	values map[string]string // stored public input values
	err    error             // injected snapshot failure
}

// Get rejects unsupported record reads in the public HTTP fixture.
func (s *settingHandlerPublicRepoStub) Get(ctx context.Context, key string) (*settingscore.Setting, error) {
	panic("unexpected Get call")
}

// GetValue rejects unsupported single-key reads in the public HTTP fixture.
func (s *settingHandlerPublicRepoStub) GetValue(ctx context.Context, key string) (string, error) {
	panic("unexpected GetValue call")
}

// Set rejects unexpected writes in the public HTTP read fixture.
func (s *settingHandlerPublicRepoStub) Set(ctx context.Context, key, value string) error {
	panic("unexpected Set call")
}

// GetMultiple returns selected public fixture values in a new map.
func (s *settingHandlerPublicRepoStub) GetMultiple(ctx context.Context, keys []string) (map[string]string, error) {
	out := make(map[string]string, len(keys))
	for _, key := range keys {
		if value, ok := s.values[key]; ok {
			out[key] = value
		}
	}
	return out, nil
}

// SetMultiple rejects batch writes in the public settings HTTP fixture.
func (s *settingHandlerPublicRepoStub) SetMultiple(ctx context.Context, settings map[string]string) error {
	panic("unexpected SetMultiple call")
}

// GetAll rejects full snapshots outside the public HTTP batch-read path.
func (s *settingHandlerPublicRepoStub) GetAll(ctx context.Context) (map[string]string, error) {
	panic("unexpected GetAll call")
}

// Delete rejects removal from the public settings HTTP fixture.
func (s *settingHandlerPublicRepoStub) Delete(ctx context.Context, key string) error {
	panic("unexpected Delete call")
}

// TestSettingHandler_GetPublicSettings_ExposesForceEmailOnThirdPartySignup
// enables force-email policy in stored public input. It checks HTTP OK,
// successful panel code, and the serialized third-party signup flag.
func TestSettingHandler_GetPublicSettings_ExposesForceEmailOnThirdPartySignup(t *testing.T) {
	repo := &settingHandlerPublicRepoStub{
		values: map[string]string{
			identity.SettingKeyForceEmailOnThirdPartySignup: "true",
		},
	}
	h := sitehttp.NewPublicHandler(newSitePublicSettingsFixture(repo, &config.Config{}), "test-version")

	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodGet, "/api/v1/settings/public", nil)

	h.GetPublicSettings(c)

	require.Equal(t, http.StatusOK, recorder.Code)

	var resp struct {
		Code int `json:"code"` // panel result code
		Data struct {
			// third-party signup email requirement
			ForceEmailOnThirdPartySignup bool `json:"force_email_on_third_party_signup"`
		} `json:"data"` // public settings payload
	}
	require.NoError(t, json.Unmarshal(recorder.Body.Bytes(), &resp))
	require.Equal(t, 0, resp.Code)
	require.True(t, resp.Data.ForceEmailOnThirdPartySignup)
}

// TestSettingHandler_GetPublicSettings_ExposesAffiliateEnabled enables
// affiliate policy through stored input. It checks the public HTTP envelope
// succeeds and exposes the enabled affiliate flag in its payload.
func TestSettingHandler_GetPublicSettings_ExposesAffiliateEnabled(t *testing.T) {
	repo := &settingHandlerPublicRepoStub{
		values: map[string]string{
			promotion.SettingKeyAffiliateEnabled: "true",
		},
	}
	h := sitehttp.NewPublicHandler(newSitePublicSettingsFixture(repo, &config.Config{}), "test-version")

	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodGet, "/api/v1/settings/public", nil)

	h.GetPublicSettings(c)

	require.Equal(t, http.StatusOK, recorder.Code)

	var resp struct {
		Code int `json:"code"` // panel result code
		Data struct {
			// affiliate public feature flag
			AffiliateEnabled bool `json:"affiliate_enabled"`
		} `json:"data"` // public settings payload
	}
	require.NoError(t, json.Unmarshal(recorder.Body.Bytes(), &resp))
	require.Equal(t, 0, resp.Code)
	require.True(t, resp.Data.AffiliateEnabled)
}

// TestSettingHandler_GetPublicSettings_ExposesTencentCaptchaConfiguration
// supplies enabled captcha, app ID, and international region. It checks the
// public HTTP payload exposes those three values with a successful panel
// envelope.
func TestSettingHandler_GetPublicSettings_ExposesTencentCaptchaConfiguration(t *testing.T) {
	repo := &settingHandlerPublicRepoStub{
		values: map[string]string{
			identity.SettingKeyTencentCaptchaEnabled: "true",
			identity.SettingKeyTencentCaptchaAppID:   "123456789",
			identity.SettingKeyTencentCaptchaRegion:  identity.TencentCaptchaRegionINTL,
		},
	}
	h := sitehttp.NewPublicHandler(newSitePublicSettingsFixture(repo, &config.Config{}), "test-version")

	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodGet, "/api/v1/settings/public", nil)

	h.GetPublicSettings(c)

	require.Equal(t, http.StatusOK, recorder.Code)

	var resp struct {
		Code int `json:"code"` // panel result code
		Data struct {
			// Tencent captcha activation flag
			TencentCaptchaEnabled bool `json:"tencent_captcha_enabled"`
			// public captcha app identity
			TencentCaptchaAppID string `json:"tencent_captcha_app_id"`
			// selected captcha service region
			TencentCaptchaRegion string `json:"tencent_captcha_region"`
		} `json:"data"` // public settings payload
	}
	require.NoError(t, json.Unmarshal(recorder.Body.Bytes(), &resp))
	require.Equal(t, 0, resp.Code)
	require.True(t, resp.Data.TencentCaptchaEnabled)
	require.Equal(t, "123456789", resp.Data.TencentCaptchaAppID)
	require.Equal(t, identity.TencentCaptchaRegionINTL, resp.Data.TencentCaptchaRegion)
}

// TestSettingHandler_GetPublicSettings_ExposesWeChatOAuthModeCapabilities
// supplies stored MP credentials and Open/MP flags. It checks the public HTTP
// payload reports overall WeChat OAuth plus both enabled web capabilities.
func TestSettingHandler_GetPublicSettings_ExposesWeChatOAuthModeCapabilities(t *testing.T) {
	h := sitehttp.NewPublicHandler(newSitePublicSettingsFixture(&settingHandlerPublicRepoStub{
		values: map[string]string{
			identity.SettingKeyWeChatConnectEnabled:             "true",
			identity.SettingKeyWeChatConnectAppID:               "wx-mp-app",
			identity.SettingKeyWeChatConnectAppSecret:           "wx-mp-secret",
			identity.SettingKeyWeChatConnectMode:                "mp",
			identity.SettingKeyWeChatConnectScopes:              "snsapi_base",
			identity.SettingKeyWeChatConnectOpenEnabled:         "true",
			identity.SettingKeyWeChatConnectMPEnabled:           "true",
			identity.SettingKeyWeChatConnectRedirectURL:         "https://api.example.com/api/v1/auth/oauth/wechat/callback",
			identity.SettingKeyWeChatConnectFrontendRedirectURL: "/auth/wechat/callback",
		},
	}, &config.Config{}), "test-version")

	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodGet, "/api/v1/settings/public", nil)

	h.GetPublicSettings(c)

	require.Equal(t, http.StatusOK, recorder.Code)

	var resp struct {
		Code int `json:"code"` // panel result code
		Data struct {
			// web WeChat availability flag
			WeChatOAuthEnabled bool `json:"wechat_oauth_enabled"`
			// Open login availability flag
			WeChatOAuthOpenEnabled bool `json:"wechat_oauth_open_enabled"`
			// MP login availability flag
			WeChatOAuthMPEnabled bool `json:"wechat_oauth_mp_enabled"`
		} `json:"data"` // public settings payload
	}
	require.NoError(t, json.Unmarshal(recorder.Body.Bytes(), &resp))
	require.Equal(t, 0, resp.Code)
	require.True(t, resp.Data.WeChatOAuthEnabled)
	require.True(t, resp.Data.WeChatOAuthOpenEnabled)
	require.True(t, resp.Data.WeChatOAuthMPEnabled)
}

// Get rejects unsupported record reads in the public service fixture.
func (s *settingPublicRepoStub) Get(ctx context.Context, key string) (*settingscore.Setting, error) {
	panic("unexpected Get call")
}

// GetValue rejects single-key reads outside the public service batch path.
func (s *settingPublicRepoStub) GetValue(ctx context.Context, key string) (string, error) {
	panic("unexpected GetValue call")
}

// Set rejects mutation in the public service read fixture.
func (s *settingPublicRepoStub) Set(ctx context.Context, key, value string) error {
	panic("unexpected Set call")
}

// GetMultiple returns nil with the injected error when public reading fails.
// Otherwise it allocates a map of requested present values, leaving missing
// keys absent for owning-domain defaults.
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

// SetMultiple rejects batch mutation in the public service fixture.
func (s *settingPublicRepoStub) SetMultiple(ctx context.Context, settings map[string]string) error {
	panic("unexpected SetMultiple call")
}

// GetAll rejects unsupported full-snapshot access in the public service
// fixture.
func (s *settingPublicRepoStub) GetAll(ctx context.Context) (map[string]string, error) {
	panic("unexpected GetAll call")
}

// Delete rejects stored-value removal in the public service fixture.
func (s *settingPublicRepoStub) Delete(ctx context.Context, key string) error {
	panic("unexpected Delete call")
}

// TestSettingService_GetPublicSettings_ExposesRegistrationEmailSuffixWhitelist
// supplies mixed-case, spaced, invalid, and empty suffix patterns. It checks
// normalized valid suffixes and the enabled per-domain registration quota flag
// in public data.
func TestSettingService_GetPublicSettings_ExposesRegistrationEmailSuffixWhitelist(t *testing.T) {
	repo := &settingPublicRepoStub{
		values: map[string]string{
			identity.SettingKeyRegistrationEnabled:                 "true",
			identity.SettingKeyEmailVerifyEnabled:                  "true",
			identity.SettingKeyRegistrationEmailSuffixWhitelist:    `["@EXAMPLE.com"," @foo.bar ","*.EDU.CN","@invalid_domain",""]`,
			identity.SettingKeyRegistrationEmailDomainQuotaEnabled: "true",
		},
	}
	svc := newSitePublicSettingsFixture(repo, &config.Config{})

	settings, err := svc.GetPublicSettings(context.Background())
	require.NoError(t, err)
	require.Equal(t, []string{"@example.com", "@foo.bar", "*.edu.cn"}, settings.RegistrationEmailSuffixWhitelist)
	require.True(t, settings.RegistrationEmailDomainQuotaEnabled)
}

// TestSettingService_GetPublicSettings_ExposesTablePreferences supplies
// page-size defaults and choices in stored input. It checks both interpreted
// values are returned through the public settings service.
func TestSettingService_GetPublicSettings_ExposesTablePreferences(t *testing.T) {
	repo := &settingPublicRepoStub{
		values: map[string]string{
			site.SettingKeyTableDefaultPageSize: "50",
			site.SettingKeyTablePageSizeOptions: "[20,50,100]",
		},
	}
	svc := newSitePublicSettingsFixture(repo, &config.Config{})

	settings, err := svc.GetPublicSettings(context.Background())
	require.NoError(t, err)
	require.Equal(t, 50, settings.TableDefaultPageSize)
	require.Equal(t, []int{20, 50, 100}, settings.TablePageSizeOptions)
}

// TestSettingService_GetPublicSettings_ExposesForceEmailOnThirdPartySignup
// supplies an enabled force-email setting. It checks the public service returns
// that third-party signup choice with no read error.
func TestSettingService_GetPublicSettings_ExposesForceEmailOnThirdPartySignup(t *testing.T) {
	repo := &settingPublicRepoStub{
		values: map[string]string{
			identity.SettingKeyForceEmailOnThirdPartySignup: "true",
		},
	}
	svc := newSitePublicSettingsFixture(repo, &config.Config{})

	settings, err := svc.GetPublicSettings(context.Background())
	require.NoError(t, err)
	require.True(t, settings.ForceEmailOnThirdPartySignup)
}

// TestSettingService_GetPublicSettings_ExposesAffiliateEnabled supplies the
// affiliate feature flag. It checks the public service resolves the enabled
// value consumed by navigation and page access presentation.
func TestSettingService_GetPublicSettings_ExposesAffiliateEnabled(t *testing.T) {
	repo := &settingPublicRepoStub{
		values: map[string]string{
			promotion.SettingKeyAffiliateEnabled: "true",
		},
	}
	svc := newSitePublicSettingsFixture(repo, &config.Config{})

	settings, err := svc.GetPublicSettings(context.Background())
	require.NoError(t, err)
	require.True(t, settings.AffiliateEnabled)
}

// TestSettingService_GetPublicSettings_ExposesPageFeatureFlags stores disabled
// team and creative choices with team enabled in configuration. It checks
// stored flags override the startup team input and disable both public
// features.
func TestSettingService_GetPublicSettings_ExposesPageFeatureFlags(t *testing.T) {
	repo := &settingPublicRepoStub{
		values: map[string]string{
			team.SettingKeyTeamEnabled:         "false",
			creative.SettingKeyCreativeEnabled: "false",
		},
	}
	svc := newSitePublicSettingsFixture(repo, &config.Config{Team: config.TeamConfig{Enabled: true}})

	settings, err := svc.GetPublicSettings(context.Background())
	require.NoError(t, err)
	require.False(t, settings.TeamEnabled)
	require.False(t, settings.CreativeEnabled)
}

// TestSettingService_GetPublicSettings_CreativeEnabledDefaultsTrue leaves
// creative settings absent. It checks the public service and serialized
// initial-page payload both use the enabled creative default.
func TestSettingService_GetPublicSettings_CreativeEnabledDefaultsTrue(t *testing.T) {
	repo := &settingPublicRepoStub{values: map[string]string{}}
	svc := newSitePublicSettingsFixture(repo, &config.Config{})

	settings, err := svc.GetPublicSettings(context.Background())
	require.NoError(t, err)
	require.True(t, settings.CreativeEnabled)

	// HTML injection and the public settings API both default creative to enabled.
	payload, err := svc.GetPublicSettingsForInjection(context.Background())
	require.NoError(t, err)
	encoded, err := json.Marshal(payload)
	require.NoError(t, err)
	require.Contains(t, string(encoded), `"creative_enabled":true`)
}

// TestSettingService_GetPublicSettings_ExposesAllowUserViewErrorRequests
// enables user error-request visibility in stored input. It checks the resolved
// public settings expose that choice without a read error.
func TestSettingService_GetPublicSettings_ExposesAllowUserViewErrorRequests(t *testing.T) {
	repo := &settingPublicRepoStub{
		values: map[string]string{
			usage.SettingKeyAllowUserViewErrorRequests: "true",
		},
	}
	svc := newSitePublicSettingsFixture(repo, &config.Config{})

	settings, err := svc.GetPublicSettings(context.Background())
	require.NoError(t, err)
	require.True(t, settings.AllowUserViewErrorRequests)
}

// TestSettingService_GetPublicSettingsForInjection_ExposesPublicFeatureFlags
// combines stored switches with team and Passkey configuration. It checks
// serialized initial-page data retains each expected enabled or disabled value
// for the frontend.
func TestSettingService_GetPublicSettingsForInjection_ExposesPublicFeatureFlags(t *testing.T) {
	repo := &settingPublicRepoStub{
		values: map[string]string{
			promotion.SettingKeyAffiliateEnabled:                   "true",
			identity.SettingKeyForceEmailOnThirdPartySignup:        "true",
			identity.SettingKeyRegistrationEmailDomainQuotaEnabled: "true",
			identity.SettingKeyUserEmailChangeEnabled:              "true",
			usage.SettingKeyAllowUserViewErrorRequests:             "true",
			team.SettingKeyTeamEnabled:                             "true",
			creative.SettingKeyCreativeEnabled:                     "false",
		},
	}
	svc := newSitePublicSettingsFixture(repo, &config.Config{
		Team: config.TeamConfig{Enabled: true},
		Auth: config.AuthConfig{WebAuthn: config.WebAuthnConfig{Enabled: true}},
	})

	payload, err := svc.GetPublicSettingsForInjection(context.Background())
	require.NoError(t, err)

	encoded, err := json.Marshal(payload)
	require.NoError(t, err)

	var settings struct {
		// affiliate public feature flag
		AffiliateEnabled bool `json:"affiliate_enabled"`
		// third-party signup email requirement
		ForceEmailOnThirdPartySignup bool `json:"force_email_on_third_party_signup"`
		// user error visibility flag
		AllowUserViewErrorRequests bool `json:"allow_user_view_error_requests"`
		// public team availability flag
		TeamEnabled bool `json:"team_enabled"`
		// public creative availability flag
		CreativeEnabled bool `json:"creative_enabled"`
		// Passkey public availability flag
		PasskeyEnabled bool `json:"passkey_enabled"`
		// registration domain quota flag
		RegistrationEmailDomainQuotaEnabled bool `json:"registration_email_domain_quota_enabled"`
		// user email change flag
		UserEmailChangeEnabled bool `json:"user_email_change_enabled"`
	}
	require.NoError(t, json.Unmarshal(encoded, &settings))
	require.True(t, settings.AffiliateEnabled)
	require.True(t, settings.ForceEmailOnThirdPartySignup)
	require.True(t, settings.AllowUserViewErrorRequests)
	require.True(t, settings.TeamEnabled)
	require.False(t, settings.CreativeEnabled)
	require.True(t, settings.PasskeyEnabled)
	require.True(t, settings.RegistrationEmailDomainQuotaEnabled)
	require.True(t, settings.UserEmailChangeEnabled)
}

// TestSettingService_GetPublicSettings_ExposesWeChatOAuthModeCapabilities
// resolves stored MP credentials with both web mode flags. It checks overall
// WeChat OAuth, Open, and MP availability are enabled in the public service
// result.
func TestSettingService_GetPublicSettings_ExposesWeChatOAuthModeCapabilities(t *testing.T) {
	svc := newSitePublicSettingsFixture(&settingPublicRepoStub{
		values: map[string]string{
			identity.SettingKeyWeChatConnectEnabled:             "true",
			identity.SettingKeyWeChatConnectAppID:               "wx-mp-app",
			identity.SettingKeyWeChatConnectAppSecret:           "wx-mp-secret",
			identity.SettingKeyWeChatConnectMode:                "mp",
			identity.SettingKeyWeChatConnectScopes:              "snsapi_base",
			identity.SettingKeyWeChatConnectOpenEnabled:         "true",
			identity.SettingKeyWeChatConnectMPEnabled:           "true",
			identity.SettingKeyWeChatConnectRedirectURL:         "https://api.example.com/api/v1/auth/oauth/wechat/callback",
			identity.SettingKeyWeChatConnectFrontendRedirectURL: "/auth/wechat/callback",
		},
	}, &config.Config{})

	settings, err := svc.GetPublicSettings(context.Background())
	require.NoError(t, err)
	require.True(t, settings.WeChatOAuthEnabled)
	require.True(t, settings.WeChatOAuthOpenEnabled)
	require.True(t, settings.WeChatOAuthMPEnabled)
}

// TestSettingService_GetPublicSettings_DoesNotExposeMobileOnlyWeChatAsWebOAuthAvailable
// supplies a complete mobile-only credential set. It checks mobile capability
// is exposed while overall web, Open, and MP OAuth availability stay disabled.
func TestSettingService_GetPublicSettings_DoesNotExposeMobileOnlyWeChatAsWebOAuthAvailable(t *testing.T) {
	svc := newSitePublicSettingsFixture(&settingPublicRepoStub{
		values: map[string]string{
			identity.SettingKeyWeChatConnectEnabled:             "true",
			identity.SettingKeyWeChatConnectMobileEnabled:       "true",
			identity.SettingKeyWeChatConnectMode:                "mobile",
			identity.SettingKeyWeChatConnectMobileAppID:         "wx-mobile-app",
			identity.SettingKeyWeChatConnectMobileAppSecret:     "wx-mobile-secret",
			identity.SettingKeyWeChatConnectFrontendRedirectURL: "/auth/wechat/callback",
		},
	}, &config.Config{})

	settings, err := svc.GetPublicSettings(context.Background())
	require.NoError(t, err)
	require.False(t, settings.WeChatOAuthEnabled)
	require.False(t, settings.WeChatOAuthOpenEnabled)
	require.False(t, settings.WeChatOAuthMPEnabled)
	require.True(t, settings.WeChatOAuthMobileEnabled)
}

// TestSettingService_GetPublicSettings_FallsBackToConfigForWeChatOAuthCapabilities
// leaves database values absent with complete startup Open credentials. It
// checks Open web OAuth is enabled while MP and mobile availability remain
// false.
func TestSettingService_GetPublicSettings_FallsBackToConfigForWeChatOAuthCapabilities(t *testing.T) {
	svc := newSitePublicSettingsFixture(&settingPublicRepoStub{values: map[string]string{}}, &config.Config{Auth: config.AuthConfig{OAuth: config.OAuthProvidersConfig{WeChat: config.WeChatOAuthConfig{
		Enabled:             true,
		OpenEnabled:         true,
		OpenAppID:           "wx-open-config",
		OpenAppSecret:       "wx-open-secret",
		FrontendRedirectURL: "/auth/wechat/config-callback",
	}}}})

	settings, err := svc.GetPublicSettings(context.Background())
	require.NoError(t, err)
	require.True(t, settings.WeChatOAuthEnabled)
	require.True(t, settings.WeChatOAuthOpenEnabled)
	require.False(t, settings.WeChatOAuthMPEnabled)
	require.False(t, settings.WeChatOAuthMobileEnabled)
}

// TestSettingService_GetPublicSettings_ExposesEffectiveGoogleOneTapConfig
// supplies complete enabled Google OAuth and One Tap. It checks public client
// ID and enabled flags, then verifies JSON contains the client identity and
// excludes its secret.
func TestSettingService_GetPublicSettings_ExposesEffectiveGoogleOneTapConfig(t *testing.T) {
	svc := newSitePublicSettingsFixture(&settingPublicRepoStub{values: map[string]string{
		identity.SettingKeyGoogleOneTapEnabled:            "true",
		identity.SettingKeyGoogleOAuthEnabled:             "true",
		identity.SettingKeyGoogleOAuthClientID:            "google-web-client",
		identity.SettingKeyGoogleOAuthClientSecret:        "google-client-secret",
		identity.SettingKeyGoogleOAuthRedirectURL:         "https://app.example/api/v1/auth/oauth/google/callback",
		identity.SettingKeyGoogleOAuthFrontendRedirectURL: "/auth/oauth/callback",
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

// TestSettingService_GetPublicSettings_DisablesGoogleOneTapWhenOAuthIsIncomplete
// omits the secret from otherwise enabled Google configuration. It checks both
// public enabled flags become false and the client ID is hidden.
func TestSettingService_GetPublicSettings_DisablesGoogleOneTapWhenOAuthIsIncomplete(t *testing.T) {
	svc := newSitePublicSettingsFixture(&settingPublicRepoStub{values: map[string]string{
		identity.SettingKeyGoogleOneTapEnabled:    "true",
		identity.SettingKeyGoogleOAuthEnabled:     "true",
		identity.SettingKeyGoogleOAuthClientID:    "google-web-client",
		identity.SettingKeyGoogleOAuthRedirectURL: "https://app.example/api/v1/auth/oauth/google/callback",
	}}, &config.Config{})

	settings, err := svc.GetPublicSettings(context.Background())
	require.NoError(t, err)
	require.False(t, settings.GoogleOAuthEnabled)
	require.False(t, settings.GoogleOneTapEnabled)
	require.Empty(t, settings.GoogleOAuthClientID)
}

// TestSettingService_GetGoogleOneTapConfigRequiresIndependentSwitch provides
// complete OAuth credentials with One Tap disabled. It checks the direct reader
// rejects first, then returns configured credentials after its separate switch
// is enabled.
func TestSettingService_GetGoogleOneTapConfigRequiresIndependentSwitch(t *testing.T) {
	values := map[string]string{
		identity.SettingKeyGoogleOneTapEnabled:            "false",
		identity.SettingKeyGoogleOAuthEnabled:             "true",
		identity.SettingKeyGoogleOAuthClientID:            "google-web-client",
		identity.SettingKeyGoogleOAuthClientSecret:        "google-client-secret",
		identity.SettingKeyGoogleOAuthRedirectURL:         "https://app.example/api/v1/auth/oauth/google/callback",
		identity.SettingKeyGoogleOAuthFrontendRedirectURL: "/auth/oauth/callback",
	}
	svc := newOAuthSettingsFixture(&settingPublicRepoStub{values: values}, &config.Config{})

	_, err := svc.GetGoogleOneTapConfig(context.Background())
	require.Error(t, err)

	values[identity.SettingKeyGoogleOneTapEnabled] = "true"
	settings, err := svc.GetGoogleOneTapConfig(context.Background())
	require.NoError(t, err)
	require.Equal(t, "google-web-client", settings.ClientID)
	require.Equal(t, "google-client-secret", settings.ClientSecret)
}

// newSitePublicSettingsFixture constructs OAuth and public data readers over
// the same Store. The supplied configuration is required by production site
// assembly, and date handling uses a calendar captured from time.Local.
func newSitePublicSettingsFixture(repo settingscore.Repository, cfg *config.Config) *site.PublicService {
	store := settingscore.New(repo)
	return provideSitePublic(store, provideOAuthSettings(store, cfg), cfg, timezone.NewCalendar(time.Local))
}
