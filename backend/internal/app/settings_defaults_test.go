package app

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/TokenFlux/TokenRouter/internal/config"
	"github.com/TokenFlux/TokenRouter/internal/identity"
	settingscore "github.com/TokenFlux/TokenRouter/internal/settings"
)

// OIDC and WeChat settings combine stored overrides with startup configuration.
// Batch-only fixtures make the reader's chosen access path visible, while the
// scenarios check discovery endpoints, explicitly disabled security flags,
// missing-value defaults, and distinct WeChat mode credentials and migration
// compatibility.

// settingOIDCRepoStub supplies selected values through the OIDC batch-read
// path.
type settingOIDCRepoStub struct {
	values map[string]string // stored OAuth setting values
}

// settingWeChatRepoStub supplies stored WeChat overrides and missing-setting
// identity.
type settingWeChatRepoStub struct {
	values map[string]string // stored OAuth setting values
}

// Get rejects unexpected record reads in the batch-only OIDC fixture.
func (s *settingOIDCRepoStub) Get(ctx context.Context, key string) (*settingscore.Setting, error) {
	panic("unexpected Get call")
}

// GetValue rejects unexpected single-key reads in the OIDC fixture.
func (s *settingOIDCRepoStub) GetValue(ctx context.Context, key string) (string, error) {
	panic("unexpected GetValue call")
}

// Set rejects writes in the read-only OIDC settings fixture.
func (s *settingOIDCRepoStub) Set(ctx context.Context, key, value string) error {
	panic("unexpected Set call")
}

// GetMultiple returns a new map containing requested fixture values that exist.
// Missing keys are omitted, allowing startup/default OIDC interpretation to
// distinguish absence from explicitly configured false values.
func (s *settingOIDCRepoStub) GetMultiple(ctx context.Context, keys []string) (map[string]string, error) {
	out := make(map[string]string, len(keys))
	for _, key := range keys {
		if value, ok := s.values[key]; ok {
			out[key] = value
		}
	}
	return out, nil
}

// SetMultiple rejects batch mutations in the read-only OIDC fixture.
func (s *settingOIDCRepoStub) SetMultiple(ctx context.Context, settings map[string]string) error {
	panic("unexpected SetMultiple call")
}

// GetAll rejects full snapshots outside the OIDC batch-read fixture.
func (s *settingOIDCRepoStub) GetAll(ctx context.Context) (map[string]string, error) {
	panic("unexpected GetAll call")
}

// Delete rejects setting removal from the read-only OIDC fixture.
func (s *settingOIDCRepoStub) Delete(ctx context.Context, key string) error {
	panic("unexpected Delete call")
}

// TestGetOIDCConnectOAuthConfig_ResolvesEndpointsFromIssuerDiscovery supplies
// an issuer and a local discovery response. It checks one discovery hit and
// resolved discovery, authorization, token, userinfo, and JWKS endpoint URLs.
func TestGetOIDCConnectOAuthConfig_ResolvesEndpointsFromIssuerDiscovery(t *testing.T) {
	var discoveryHits int
	var baseURL string

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/issuer/.well-known/openid-configuration" {
			http.NotFound(w, r)
			return
		}
		discoveryHits++
		w.Header().Set("Content-Type", "application/json")
		// Supply controlled discovery JSON to the reader under test. The fixture
		// discards server write errors and asserts the actual request count and resolved endpoint values afterward.
		_, _ = fmt.Fprintf(w, `{
			"authorization_endpoint":"%s/issuer/protocol/openid-connect/auth",
			"token_endpoint":"%s/issuer/protocol/openid-connect/token",
			"userinfo_endpoint":"%s/issuer/protocol/openid-connect/userinfo",
			"jwks_uri":"%s/issuer/protocol/openid-connect/certs"
		}`, baseURL, baseURL, baseURL, baseURL)
	}))
	defer srv.Close()
	baseURL = srv.URL

	cfg := &config.Config{Auth: config.AuthConfig{OAuth: config.OAuthProvidersConfig{OIDC: config.OIDCConnectConfig{
		Enabled:             true,
		ProviderName:        "OIDC",
		ClientID:            "oidc-client",
		ClientSecret:        "oidc-secret",
		IssuerURL:           srv.URL + "/issuer",
		RedirectURL:         "https://example.com/api/v1/auth/oauth/oidc/callback",
		FrontendRedirectURL: "/auth/oidc/callback",
		Scopes:              "openid email profile",
		TokenAuthMethod:     "client_secret_post",
		ValidateIDToken:     true,
		AllowedSigningAlgs:  "RS256",
		ClockSkewSeconds:    120,
	}}}}

	repo := &settingOIDCRepoStub{values: map[string]string{}}
	svc := newOAuthSettingsFixture(repo, cfg)

	got, err := svc.GetOIDCConnectOAuthConfig(context.Background())
	require.NoError(t, err)
	require.Equal(t, 1, discoveryHits)
	require.Equal(t, srv.URL+"/issuer/.well-known/openid-configuration", got.DiscoveryURL)
	require.Equal(t, srv.URL+"/issuer/protocol/openid-connect/auth", got.AuthorizeURL)
	require.Equal(t, srv.URL+"/issuer/protocol/openid-connect/token", got.TokenURL)
	require.Equal(t, srv.URL+"/issuer/protocol/openid-connect/userinfo", got.UserInfoURL)
	require.Equal(t, srv.URL+"/issuer/protocol/openid-connect/certs", got.JWKSURL)
}

// TestSettingService_ParseSettings_PreservesOptionalOIDCCompatibilityFlags
// supplies explicit false PKCE and ID-token validation settings. The
// administrator read view must retain both disabled values from that supplied
// snapshot.
func TestSettingService_ParseSettings_PreservesOptionalOIDCCompatibilityFlags(t *testing.T) {
	svc := newOAuthSettingsFixture(&settingOIDCRepoStub{values: map[string]string{}}, &config.Config{})

	got := readOAuthAdminSettings(svc, map[string]string{
		identity.SettingKeyOIDCConnectEnabled:         "true",
		identity.SettingKeyOIDCConnectUsePKCE:         "false",
		identity.SettingKeyOIDCConnectValidateIDToken: "false",
	})

	require.False(t, got.OIDCConnectUsePKCE)
	require.False(t, got.OIDCConnectValidateIDToken)
}

// TestSettingService_ParseSettings_DefaultsOIDCSecurityFlagsToSafeConfigValues
// omits stored security flags with explicit enabled configuration defaults. It
// checks administrator parsing retains PKCE and ID-token validation from those
// startup values.
func TestSettingService_ParseSettings_DefaultsOIDCSecurityFlagsToSafeConfigValues(t *testing.T) {
	svc := newOAuthSettingsFixture(&settingOIDCRepoStub{values: map[string]string{}}, &config.Config{
		Auth: config.AuthConfig{OAuth: config.OAuthProvidersConfig{OIDC: config.OIDCConnectConfig{
			UsePKCE:                 true,
			UsePKCEExplicit:         true,
			ValidateIDToken:         true,
			ValidateIDTokenExplicit: true,
		}}},
	})

	got := readOAuthAdminSettings(svc, map[string]string{
		identity.SettingKeyOIDCConnectEnabled: "true",
	})

	require.True(t, got.OIDCConnectUsePKCE)
	require.True(t, got.OIDCConnectValidateIDToken)
}

// TestSettingService_ParseSettings_DefaultsOIDCCompatibilityFlagsToSafeDefaultsWhenSettingsMissing
// omits database flags and configuration explicitness markers. It checks the
// administrator view still resolves enabled PKCE and ID-token validation for
// this fixture.
func TestSettingService_ParseSettings_DefaultsOIDCCompatibilityFlagsToSafeDefaultsWhenSettingsMissing(t *testing.T) {
	svc := newOAuthSettingsFixture(&settingOIDCRepoStub{values: map[string]string{}}, &config.Config{Auth: config.AuthConfig{OAuth: config.OAuthProvidersConfig{OIDC: config.OIDCConnectConfig{
		UsePKCE:         true,
		ValidateIDToken: true,
	}}}})

	got := readOAuthAdminSettings(svc, map[string]string{
		identity.SettingKeyOIDCConnectEnabled: "true",
	})

	require.True(t, got.OIDCConnectUsePKCE)
	require.True(t, got.OIDCConnectValidateIDToken)
}

// TestGetOIDCConnectOAuthConfig_AllowsCompatibilityFlagsToDisablePKCEAndIDTokenValidation
// supplies complete OAuth options with stored false security flags. It checks
// the request-time reader returns both disabled values without a configuration
// error.
func TestGetOIDCConnectOAuthConfig_AllowsCompatibilityFlagsToDisablePKCEAndIDTokenValidation(t *testing.T) {
	cfg := &config.Config{Auth: config.AuthConfig{OAuth: config.OAuthProvidersConfig{OIDC: config.OIDCConnectConfig{
		Enabled:             true,
		ProviderName:        "OIDC",
		ClientID:            "oidc-client",
		ClientSecret:        "oidc-secret",
		IssuerURL:           "https://issuer.example.com",
		AuthorizeURL:        "https://issuer.example.com/auth",
		TokenURL:            "https://issuer.example.com/token",
		UserInfoURL:         "https://issuer.example.com/userinfo",
		RedirectURL:         "https://example.com/api/v1/auth/oauth/oidc/callback",
		FrontendRedirectURL: "/auth/oidc/callback",
		Scopes:              "openid email profile",
		TokenAuthMethod:     "client_secret_post",
	}}}}

	repo := &settingOIDCRepoStub{values: map[string]string{
		identity.SettingKeyOIDCConnectEnabled:         "true",
		identity.SettingKeyOIDCConnectUsePKCE:         "false",
		identity.SettingKeyOIDCConnectValidateIDToken: "false",
	}}
	svc := newOAuthSettingsFixture(repo, cfg)

	got, err := svc.GetOIDCConnectOAuthConfig(context.Background())
	require.NoError(t, err)
	require.False(t, got.UsePKCE)
	require.False(t, got.ValidateIDToken)
}

// TestGetOIDCConnectOAuthConfig_DefaultsToSecureFlagsWhenSettingsMissing
// supplies complete OAuth configuration and explicit true security defaults. It
// checks missing database flags retain enabled PKCE and ID-token validation in
// the request-time reader.
func TestGetOIDCConnectOAuthConfig_DefaultsToSecureFlagsWhenSettingsMissing(t *testing.T) {
	cfg := &config.Config{
		Auth: config.AuthConfig{OAuth: config.OAuthProvidersConfig{OIDC: config.OIDCConnectConfig{
			Enabled:                 true,
			ProviderName:            "OIDC",
			ClientID:                "oidc-client",
			ClientSecret:            "oidc-secret",
			IssuerURL:               "https://issuer.example.com",
			AuthorizeURL:            "https://issuer.example.com/auth",
			TokenURL:                "https://issuer.example.com/token",
			UserInfoURL:             "https://issuer.example.com/userinfo",
			JWKSURL:                 "https://issuer.example.com/jwks",
			RedirectURL:             "https://example.com/api/v1/auth/oauth/oidc/callback",
			FrontendRedirectURL:     "/auth/oidc/callback",
			Scopes:                  "openid email profile",
			TokenAuthMethod:         "client_secret_post",
			UsePKCE:                 true,
			UsePKCEExplicit:         true,
			ValidateIDToken:         true,
			ValidateIDTokenExplicit: true,
			AllowedSigningAlgs:      "RS256",
			ClockSkewSeconds:        120,
		}}},
	}

	repo := &settingOIDCRepoStub{values: map[string]string{
		identity.SettingKeyOIDCConnectEnabled: "true",
	}}
	svc := newOAuthSettingsFixture(repo, cfg)

	got, err := svc.GetOIDCConnectOAuthConfig(context.Background())
	require.NoError(t, err)
	require.True(t, got.UsePKCE)
	require.True(t, got.ValidateIDToken)
}

// TestGetOIDCConnectOAuthConfig_DefaultsCompatibilityFlagsToSafeValuesWhenSettingsMissing
// supplies enabled flags without their explicitness markers. It checks missing
// stored values resolve both PKCE and ID-token validation to true.
func TestGetOIDCConnectOAuthConfig_DefaultsCompatibilityFlagsToSafeValuesWhenSettingsMissing(t *testing.T) {
	cfg := &config.Config{Auth: config.AuthConfig{OAuth: config.OAuthProvidersConfig{OIDC: config.OIDCConnectConfig{
		Enabled:             true,
		ProviderName:        "OIDC",
		ClientID:            "oidc-client",
		ClientSecret:        "oidc-secret",
		IssuerURL:           "https://issuer.example.com",
		AuthorizeURL:        "https://issuer.example.com/auth",
		TokenURL:            "https://issuer.example.com/token",
		UserInfoURL:         "https://issuer.example.com/userinfo",
		JWKSURL:             "https://issuer.example.com/jwks",
		RedirectURL:         "https://example.com/api/v1/auth/oauth/oidc/callback",
		FrontendRedirectURL: "/auth/oidc/callback",
		Scopes:              "openid email profile",
		TokenAuthMethod:     "client_secret_post",
		UsePKCE:             true,
		ValidateIDToken:     true,
		AllowedSigningAlgs:  "RS256",
		ClockSkewSeconds:    120,
	}}}}

	repo := &settingOIDCRepoStub{values: map[string]string{
		identity.SettingKeyOIDCConnectEnabled: "true",
	}}
	svc := newOAuthSettingsFixture(repo, cfg)

	got, err := svc.GetOIDCConnectOAuthConfig(context.Background())
	require.NoError(t, err)
	require.True(t, got.UsePKCE)
	require.True(t, got.ValidateIDToken)
}

// readOAuthAdminSettings interprets the supplied raw values with a zero
// concurrency-default callback.
func readOAuthAdminSettings(source *identity.OAuthSettings, values map[string]string) *identity.AdminReadSettings {
	return source.ReadAdminSettings(values, func() int { return 0 })
}

// Get rejects unsupported record access in the WeChat settings fixture.
func (s *settingWeChatRepoStub) Get(context.Context, string) (*settingscore.Setting, error) {
	panic("unexpected Get call")
}

// GetValue returns a configured WeChat value or ErrSettingNotFound for absence.
// The string map is read synchronously, preserving the sentinel used by
// production settings interpretation to apply startup defaults.
func (s *settingWeChatRepoStub) GetValue(_ context.Context, key string) (string, error) {
	if value, ok := s.values[key]; ok {
		return value, nil
	}
	return "", settingscore.ErrSettingNotFound
}

// Set rejects unexpected writes in the WeChat read fixture.
func (s *settingWeChatRepoStub) Set(context.Context, string, string) error {
	panic("unexpected Set call")
}

// GetMultiple returns a fresh map of requested stored WeChat values.
func (s *settingWeChatRepoStub) GetMultiple(_ context.Context, keys []string) (map[string]string, error) {
	out := make(map[string]string, len(keys))
	for _, key := range keys {
		if value, ok := s.values[key]; ok {
			out[key] = value
		}
	}
	return out, nil
}

// SetMultiple rejects unexpected batch writes in the WeChat fixture.
func (s *settingWeChatRepoStub) SetMultiple(context.Context, map[string]string) error {
	panic("unexpected SetMultiple call")
}

// GetAll rejects unsupported full-snapshot reads in the WeChat fixture.
func (s *settingWeChatRepoStub) GetAll(context.Context) (map[string]string, error) {
	panic("unexpected GetAll call")
}

// Delete rejects unexpected removal from the WeChat settings fixture.
func (s *settingWeChatRepoStub) Delete(context.Context, string) error {
	panic("unexpected Delete call")
}

// TestSettingService_GetWeChatConnectOAuthConfig_UsesDatabaseOverrides supplies
// persisted MP credentials and enabled mode flags. It checks the resolved
// app/secret, scopes, mode, capabilities, API callback, and frontend callback
// reflect those overrides.
func TestSettingService_GetWeChatConnectOAuthConfig_UsesDatabaseOverrides(t *testing.T) {
	repo := &settingWeChatRepoStub{
		values: map[string]string{
			identity.SettingKeyWeChatConnectEnabled:             "true",
			identity.SettingKeyWeChatConnectAppID:               "wx-db-app",
			identity.SettingKeyWeChatConnectAppSecret:           "wx-db-secret",
			identity.SettingKeyWeChatConnectMode:                "mp",
			identity.SettingKeyWeChatConnectScopes:              "snsapi_base",
			identity.SettingKeyWeChatConnectOpenEnabled:         "true",
			identity.SettingKeyWeChatConnectMPEnabled:           "true",
			identity.SettingKeyWeChatConnectRedirectURL:         "https://api.example.com/api/v1/auth/oauth/wechat/callback",
			identity.SettingKeyWeChatConnectFrontendRedirectURL: "/auth/wechat/callback",
		},
	}
	svc := newOAuthSettingsFixture(repo, &config.Config{})

	got, err := svc.GetWeChatConnectOAuthConfig(context.Background())
	require.NoError(t, err)
	require.True(t, got.Enabled)
	require.Equal(t, "wx-db-app", got.AppIDForMode("mp"))
	require.Equal(t, "wx-db-secret", got.AppSecretForMode("mp"))
	require.True(t, got.OpenEnabled)
	require.True(t, got.MPEnabled)
	require.Equal(t, "mp", got.Mode)
	require.Equal(t, "snsapi_base", got.Scopes)
	require.Equal(t, "https://api.example.com/api/v1/auth/oauth/wechat/callback", got.RedirectURL)
	require.Equal(t, "/auth/wechat/callback", got.FrontendRedirectURL)
}

// TestSettingService_GetWeChatConnectOAuthConfig_FallsBackToConfigWhenDatabaseEmpty
// supplies no stored WeChat values and separate startup mode credentials. It
// checks enabled modes, app/secret choices, configured frontend callback, and
// the absent API redirect.
func TestSettingService_GetWeChatConnectOAuthConfig_FallsBackToConfigWhenDatabaseEmpty(t *testing.T) {
	repo := &settingWeChatRepoStub{values: map[string]string{}}
	svc := newOAuthSettingsFixture(repo, &config.Config{Auth: config.AuthConfig{OAuth: config.OAuthProvidersConfig{WeChat: config.WeChatOAuthConfig{
		Enabled:             true,
		OpenEnabled:         true,
		MPEnabled:           true,
		Mode:                "open",
		OpenAppID:           "wx-open-config",
		OpenAppSecret:       "wx-open-secret",
		MPAppID:             "wx-mp-config",
		MPAppSecret:         "wx-mp-secret",
		FrontendRedirectURL: "/auth/wechat/config-callback",
	}}}})

	got, err := svc.GetWeChatConnectOAuthConfig(context.Background())
	require.NoError(t, err)
	require.True(t, got.Enabled)
	require.True(t, got.OpenEnabled)
	require.True(t, got.MPEnabled)
	require.Equal(t, "wx-open-config", got.AppIDForMode("open"))
	require.Equal(t, "wx-open-secret", got.AppSecretForMode("open"))
	require.Equal(t, "wx-mp-config", got.AppIDForMode("mp"))
	require.Equal(t, "wx-mp-secret", got.AppSecretForMode("mp"))
	require.Equal(t, "/auth/wechat/config-callback", got.FrontendRedirectURL)
	require.Empty(t, got.RedirectURL)
}

// TestSettingService_GetWeChatConnectOAuthConfig_IgnoresSyntheticDisabledCapabilitiesFromMigration118
// supplies stored false capability flags alongside complete startup mode
// credentials. It checks the resolved enabled capabilities and mode app IDs
// retain the configuration fallback for this compatibility case.
func TestSettingService_GetWeChatConnectOAuthConfig_IgnoresSyntheticDisabledCapabilitiesFromMigration118(t *testing.T) {
	repo := &settingWeChatRepoStub{
		values: map[string]string{
			identity.SettingKeyWeChatConnectOpenEnabled: "false",
			identity.SettingKeyWeChatConnectMPEnabled:   "false",
		},
	}
	svc := newOAuthSettingsFixture(repo, &config.Config{Auth: config.AuthConfig{OAuth: config.OAuthProvidersConfig{WeChat: config.WeChatOAuthConfig{
		Enabled:             true,
		OpenEnabled:         true,
		MPEnabled:           true,
		Mode:                "open",
		OpenAppID:           "wx-open-config",
		OpenAppSecret:       "wx-open-secret",
		MPAppID:             "wx-mp-config",
		MPAppSecret:         "wx-mp-secret",
		FrontendRedirectURL: "/auth/wechat/config-callback",
	}}}})

	got, err := svc.GetWeChatConnectOAuthConfig(context.Background())
	require.NoError(t, err)
	require.True(t, got.Enabled)
	require.True(t, got.OpenEnabled)
	require.True(t, got.MPEnabled)
	require.Equal(t, "wx-open-config", got.AppIDForMode("open"))
	require.Equal(t, "wx-mp-config", got.AppIDForMode("mp"))
}

// TestSettingService_ParseSettings_FallsBackToConfigForWeChatAdminView parses
// an empty settings snapshot with startup Open mode credentials. It checks the
// administrator enabled flags, app ID, configured-secret marker, frontend
// callback, mode, and login scope.
func TestSettingService_ParseSettings_FallsBackToConfigForWeChatAdminView(t *testing.T) {
	svc := newOAuthSettingsFixture(&settingWeChatRepoStub{values: map[string]string{}}, &config.Config{Auth: config.AuthConfig{OAuth: config.OAuthProvidersConfig{WeChat: config.WeChatOAuthConfig{
		Enabled:             true,
		OpenEnabled:         true,
		Mode:                "open",
		OpenAppID:           "wx-open-config",
		OpenAppSecret:       "wx-open-secret",
		FrontendRedirectURL: "/auth/wechat/config-callback",
	}}}})

	got := readOAuthAdminSettings(svc, map[string]string{})
	require.True(t, got.WeChatConnectEnabled)
	require.True(t, got.WeChatConnectOpenEnabled)
	require.Equal(t, "wx-open-config", got.WeChatConnectOpenAppID)
	require.True(t, got.WeChatConnectOpenAppSecretConfigured)
	require.Equal(t, "/auth/wechat/config-callback", got.WeChatConnectFrontendRedirectURL)
	require.Equal(t, "open", got.WeChatConnectMode)
	require.Equal(t, "snsapi_login", got.WeChatConnectScopes)
}
