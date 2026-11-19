package config

import (
	"bytes"
	"encoding/json"
	"log/slog"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// TestLoadForBootstrapAllowsMissingJWTSecret clears the JWT environment value
// before bootstrap loading. The call must succeed and return an empty secret,
// leaving persistent secret selection to database bootstrap.
func TestLoadForBootstrapAllowsMissingJWTSecret(t *testing.T) {
	fixture := newConfigFixture(t)

	fixture.Setenv("CONFIG_FILE", "")
	fixture.Setenv("DATA_DIR", "")
	fixture.Setenv("JWT_SECRET", "")

	cfg, err := fixture.LoadForBootstrap()
	if err != nil {
		t.Fatalf("fixture.LoadForBootstrap() error: %v", err)
	}
	if cfg.Auth.JWT.Secret != "" {
		t.Fatalf("fixture.LoadForBootstrap() should keep empty auth.jwt.secret during bootstrap")
	}
}

// TestLoadDefaultOIDCSecurityDefaults reads OIDC bootstrap security flags
// without supplied overrides. PKCE and ID-token validation must be enabled
// while their explicit-presence flags remain false.
func TestLoadDefaultOIDCSecurityDefaults(t *testing.T) {
	fixture := newConfigFixture(t)

	cfg, err := fixture.Load()
	require.NoError(t, err)
	require.True(t, cfg.Auth.OAuth.OIDC.UsePKCE)
	require.True(t, cfg.Auth.OAuth.OIDC.ValidateIDToken)
	require.False(t, cfg.Auth.OAuth.OIDC.UsePKCEExplicit)
	require.False(t, cfg.Auth.OAuth.OIDC.ValidateIDTokenExplicit)
}

// TestLoadDefaultJWTAccessTokenExpireMinutes loads the minute-based token lifetime.
// The default remains one day after the legacy hour override is removed from
// the current configuration schema.
func TestLoadDefaultJWTAccessTokenExpireMinutes(t *testing.T) {
	fixture := newConfigFixture(t)

	cfg, err := fixture.Load()
	if err != nil {
		t.Fatalf("fixture.Load() error: %v", err)
	}

	require.Equal(t, 1440, cfg.Auth.JWT.AccessTokenTTLMinutes)
}

// TestValidateLinuxDoFrontendRedirectURL enables otherwise populated LinuxDo
// settings with a JavaScript callback scheme. Validation must reject the
// frontend redirect and identify that field in its error.
func TestValidateLinuxDoFrontendRedirectURL(t *testing.T) {
	fixture := newConfigFixture(t)

	cfg, err := fixture.Load()
	if err != nil {
		t.Fatalf("fixture.Load() error: %v", err)
	}

	cfg.Auth.OAuth.LinuxDo.Enabled = true
	cfg.Auth.OAuth.LinuxDo.ClientID = "test-client"
	cfg.Auth.OAuth.LinuxDo.ClientSecret = "test-secret"
	cfg.Auth.OAuth.LinuxDo.RedirectURL = "https://example.com/api/v1/auth/oauth/linuxdo/callback"
	cfg.Auth.OAuth.LinuxDo.TokenAuthMethod = "client_secret_post"
	cfg.Auth.OAuth.LinuxDo.UsePKCE = true

	cfg.Auth.OAuth.LinuxDo.FrontendRedirectURL = "javascript:alert(1)"
	err = cfg.Validate()
	if err == nil {
		t.Fatalf("Validate() expected error for javascript scheme, got nil")
	}
	if !strings.Contains(err.Error(), "auth.oauth.linuxdo.frontend_redirect_url") {
		t.Fatalf("Validate() expected frontend_redirect_url error, got: %v", err)
	}
}

// TestValidateLinuxDoAllowsDisablingPKCEForCompatibility supplies enabled
// LinuxDo settings with no client secret, auth method none, and PKCE disabled.
// Validation must accept that supported compatibility configuration.
func TestValidateLinuxDoAllowsDisablingPKCEForCompatibility(t *testing.T) {
	fixture := newConfigFixture(t)

	cfg, err := fixture.Load()
	if err != nil {
		t.Fatalf("fixture.Load() error: %v", err)
	}

	cfg.Auth.OAuth.LinuxDo.Enabled = true
	cfg.Auth.OAuth.LinuxDo.ClientID = "test-client"
	cfg.Auth.OAuth.LinuxDo.ClientSecret = ""
	cfg.Auth.OAuth.LinuxDo.RedirectURL = "https://example.com/api/v1/auth/oauth/linuxdo/callback"
	cfg.Auth.OAuth.LinuxDo.FrontendRedirectURL = "/auth/linuxdo/callback"
	cfg.Auth.OAuth.LinuxDo.TokenAuthMethod = "none"
	cfg.Auth.OAuth.LinuxDo.UsePKCE = false

	err = cfg.Validate()
	if err != nil {
		t.Fatalf("Validate() expected LinuxDo config without PKCE to pass for compatibility, got: %v", err)
	}
}

// TestValidateOIDCScopesMustContainOpenID enables a populated OIDC fixture
// whose scopes omit openid. Validation must return a scope error so the
// required OIDC scope is checked before startup proceeds.
func TestValidateOIDCScopesMustContainOpenID(t *testing.T) {
	fixture := newConfigFixture(t)

	cfg, err := fixture.Load()
	if err != nil {
		t.Fatalf("fixture.Load() error: %v", err)
	}

	cfg.Auth.OAuth.OIDC.Enabled = true
	cfg.Auth.OAuth.OIDC.ClientID = "oidc-client"
	cfg.Auth.OAuth.OIDC.ClientSecret = "oidc-secret"
	cfg.Auth.OAuth.OIDC.IssuerURL = "https://issuer.example.com"
	cfg.Auth.OAuth.OIDC.AuthorizeURL = "https://issuer.example.com/auth"
	cfg.Auth.OAuth.OIDC.TokenURL = "https://issuer.example.com/token"
	cfg.Auth.OAuth.OIDC.JWKSURL = "https://issuer.example.com/jwks"
	cfg.Auth.OAuth.OIDC.RedirectURL = "https://example.com/api/v1/auth/oauth/oidc/callback"
	cfg.Auth.OAuth.OIDC.FrontendRedirectURL = "/auth/oidc/callback"
	cfg.Auth.OAuth.OIDC.Scopes = "profile email"
	cfg.Auth.OAuth.OIDC.UsePKCE = true

	err = cfg.Validate()
	if err == nil {
		t.Fatalf("Validate() expected error when scopes do not include openid, got nil")
	}
	if !strings.Contains(err.Error(), "auth.oauth.oidc.scopes") {
		t.Fatalf("Validate() expected auth.oauth.oidc.scopes error, got: %v", err)
	}
}

// TestValidateOIDCAllowsIssuerOnlyEndpointsWithDiscoveryFallback supplies an
// issuer and required callbacks while individual endpoint fields remain empty.
// Validation must accept the object for consumers that discover endpoints from
// that issuer.
func TestValidateOIDCAllowsIssuerOnlyEndpointsWithDiscoveryFallback(t *testing.T) {
	fixture := newConfigFixture(t)

	cfg, err := fixture.Load()
	if err != nil {
		t.Fatalf("fixture.Load() error: %v", err)
	}

	cfg.Auth.OAuth.OIDC.Enabled = true
	cfg.Auth.OAuth.OIDC.ClientID = "oidc-client"
	cfg.Auth.OAuth.OIDC.ClientSecret = "oidc-secret"
	cfg.Auth.OAuth.OIDC.IssuerURL = "https://issuer.example.com"
	cfg.Auth.OAuth.OIDC.AuthorizeURL = ""
	cfg.Auth.OAuth.OIDC.TokenURL = ""
	cfg.Auth.OAuth.OIDC.JWKSURL = ""
	cfg.Auth.OAuth.OIDC.RedirectURL = "https://example.com/api/v1/auth/oauth/oidc/callback"
	cfg.Auth.OAuth.OIDC.FrontendRedirectURL = "/auth/oidc/callback"
	cfg.Auth.OAuth.OIDC.Scopes = "openid email profile"
	cfg.Auth.OAuth.OIDC.ValidateIDToken = true
	cfg.Auth.OAuth.OIDC.UsePKCE = true

	err = cfg.Validate()
	if err != nil {
		t.Fatalf("Validate() expected issuer-only OIDC config to pass with discovery fallback, got: %v", err)
	}
}

// TestValidateOIDCAllowsExplicitCompatibilityOverridesForPKCEAndIDTokenValidation
// disables both checks and omits signing algorithm and JWKS text in populated
// OIDC settings. Validation must accept those supplied compatibility choices.
func TestValidateOIDCAllowsExplicitCompatibilityOverridesForPKCEAndIDTokenValidation(t *testing.T) {
	fixture := newConfigFixture(t)

	cfg, err := fixture.Load()
	if err != nil {
		t.Fatalf("fixture.Load() error: %v", err)
	}

	cfg.Auth.OAuth.OIDC.Enabled = true
	cfg.Auth.OAuth.OIDC.ClientID = "oidc-client"
	cfg.Auth.OAuth.OIDC.ClientSecret = "oidc-secret"
	cfg.Auth.OAuth.OIDC.IssuerURL = "https://issuer.example.com"
	cfg.Auth.OAuth.OIDC.AuthorizeURL = "https://issuer.example.com/auth"
	cfg.Auth.OAuth.OIDC.TokenURL = "https://issuer.example.com/token"
	cfg.Auth.OAuth.OIDC.UserInfoURL = "https://issuer.example.com/userinfo"
	cfg.Auth.OAuth.OIDC.RedirectURL = "https://example.com/api/v1/auth/oauth/oidc/callback"
	cfg.Auth.OAuth.OIDC.FrontendRedirectURL = "/auth/oidc/callback"
	cfg.Auth.OAuth.OIDC.Scopes = "openid email profile"
	cfg.Auth.OAuth.OIDC.UsePKCE = false
	cfg.Auth.OAuth.OIDC.ValidateIDToken = false
	cfg.Auth.OAuth.OIDC.JWKSURL = ""
	cfg.Auth.OAuth.OIDC.AllowedSigningAlgs = ""

	err = cfg.Validate()
	if err != nil {
		t.Fatalf("Validate() expected OIDC config without PKCE/id_token validation to pass for compatibility, got: %v", err)
	}
}

// TestGenerateJWTSecretDefaultLength requests secret generation with a zero
// byte-count argument. Generation must succeed and return nonempty text,
// exercising default-size selection without asserting the returned encoded
// length.
func TestGenerateJWTSecretDefaultLength(t *testing.T) {
	secret, err := generateJWTSecret(0)
	if err != nil {
		t.Fatalf("generateJWTSecret error: %v", err)
	}
	if secret == "" {
		t.Fatalf("generateJWTSecret returned empty string")
	}
}

// TestValidateConfigWithLinuxDoEnabled populates LinuxDo credentials, endpoint
// URLs, callback paths, auth method, and PKCE along with CSP. Validation must
// accept that combined enabled-provider configuration.
func TestValidateConfigWithLinuxDoEnabled(t *testing.T) {
	fixture := newConfigFixture(t)

	cfg, err := fixture.Load()
	if err != nil {
		t.Fatalf("fixture.Load() error: %v", err)
	}

	cfg.Security.CSP.Enabled = true
	cfg.Security.CSP.Policy = "default-src 'self'"

	cfg.Auth.OAuth.LinuxDo.Enabled = true
	cfg.Auth.OAuth.LinuxDo.ClientID = "client"
	cfg.Auth.OAuth.LinuxDo.ClientSecret = "secret"
	cfg.Auth.OAuth.LinuxDo.AuthorizeURL = "https://example.com/oauth2/authorize"
	cfg.Auth.OAuth.LinuxDo.TokenURL = "https://example.com/oauth2/token"
	cfg.Auth.OAuth.LinuxDo.UserInfoURL = "https://example.com/oauth2/userinfo"
	cfg.Auth.OAuth.LinuxDo.RedirectURL = "https://example.com/api/v1/auth/oauth/linuxdo/callback"
	cfg.Auth.OAuth.LinuxDo.FrontendRedirectURL = "/auth/linuxdo/callback"
	cfg.Auth.OAuth.LinuxDo.TokenAuthMethod = "client_secret_post"
	cfg.Auth.OAuth.LinuxDo.UsePKCE = true

	if err := cfg.Validate(); err != nil {
		t.Fatalf("Validate() unexpected error: %v", err)
	}
}

// TestValidateJWTSecretStrength supplies one known weak-secret spelling and one
// string absent from the warning list. The predicate must recognize the known
// spelling and leave the other string unflagged.
func TestValidateJWTSecretStrength(t *testing.T) {
	if !isWeakJWTSecret("change-me-in-production") {
		t.Fatalf("isWeakJWTSecret should detect weak secret")
	}
	if isWeakJWTSecret("StrongSecretValue") {
		t.Fatalf("isWeakJWTSecret should accept strong secret")
	}
}

// TestGenerateJWTSecretWithLength requests generation from sixteen random bytes
// through the private helper. The call must return no error and nonempty text;
// this fixture does not compare the encoded length.
func TestGenerateJWTSecretWithLength(t *testing.T) {
	secret, err := generateJWTSecret(16)
	if err != nil {
		t.Fatalf("generateJWTSecret error: %v", err)
	}
	if secret == "" {
		t.Fatalf("generateJWTSecret returned empty string")
	}
}

// TestValidateJWTSecret_UTF8Bytes supplies ASCII secrets of thirty-one and
// thirty-two bytes on valid configuration. The shorter secret must report the
// byte minimum, and the exact minimum must validate successfully.
func TestValidateJWTSecret_UTF8Bytes(t *testing.T) {
	fixture := newConfigFixture(t)
	cfg, err := fixture.Load()
	if err != nil {
		t.Fatalf("fixture.Load() error: %v", err)
	}

	// An ASCII fixture gives equal character and byte counts below the minimum.
	// This assertion checks the byte threshold but does not exercise multibyte text.
	cfg.Auth.JWT.Secret = strings.Repeat("a", 31)
	err = cfg.Validate()
	if err == nil {
		t.Fatalf("Validate() should reject 31-byte secret")
	}
	if !strings.Contains(err.Error(), "at least 32 bytes") {
		t.Fatalf("Validate() error = %v", err)
	}

	// Raise the same ASCII fixture to the exact minimum so acceptance contrasts
	// with the preceding short input under otherwise unchanged loaded configuration.
	cfg.Auth.JWT.Secret = strings.Repeat("a", 32)
	err = cfg.Validate()
	if err != nil {
		t.Fatalf("Validate() should accept 32-byte secret: %v", err)
	}
}

// TestValidateDingTalkConfig_Disabled_Skip supplies disabled DingTalk settings
// with the remaining fields unset. Validation must accept that object, allowing
// disabled provider parameters to remain empty in the configuration.
func TestValidateDingTalkConfig_Disabled_Skip(t *testing.T) {
	require.NoError(t, ValidateDingTalkConfig(DingTalkOAuthConfig{Enabled: false}))
}

// TestValidateDingTalkConfig_V4_DingTalkAppKind enables the unsupported
// third-party enterprise app kind. Validation must return the shared app-kind
// sentinel so callers can identify this rejection through errors.Is.
func TestValidateDingTalkConfig_V4_DingTalkAppKind(t *testing.T) {
	err := ValidateDingTalkConfig(DingTalkOAuthConfig{
		Enabled:               true,
		DingTalkAppKind:       "third_party_enterprise_app",
		CorpRestrictionPolicy: "none",
	})
	require.ErrorIs(t, err, ErrDingTalkV4InvalidAppKind)
}

// TestValidateDingTalkConfig_V1_InternalOnlyRequiresInternalAppType combines
// internal_only corporation restriction with public app type. Validation must
// return the shared type-mismatch sentinel despite a supplied corporation
// identifier.
func TestValidateDingTalkConfig_V1_InternalOnlyRequiresInternalAppType(t *testing.T) {
	err := ValidateDingTalkConfig(DingTalkOAuthConfig{
		Enabled:               true,
		DingTalkAppKind:       "internal_app",
		AppType:               "public",
		CorpRestrictionPolicy: "internal_only",
		InternalCorpID:        "dingABC",
	})
	require.ErrorIs(t, err, ErrDingTalkV1AppTypeMismatch)
}

// TestValidateDingTalkConfig_V3_InternalOnlyAllowsEmptyCorpID combines the
// supported internal app kind and type with internal_only restriction.
// Validation must accept an empty corporation identifier in that tested mode
// combination.
func TestValidateDingTalkConfig_V3_InternalOnlyAllowsEmptyCorpID(t *testing.T) {
	err := ValidateDingTalkConfig(DingTalkOAuthConfig{
		Enabled:               true,
		DingTalkAppKind:       "internal_app",
		AppType:               "internal",
		CorpRestrictionPolicy: "internal_only",
		InternalCorpID:        "",
	})
	require.NoError(t, err)
}

// TestValidateDingTalkConfig_HappyPath_None supplies an enabled internal app
// kind with public app type and no corporation restriction. Validation must
// accept that supported combination without requiring further restriction data.
func TestValidateDingTalkConfig_HappyPath_None(t *testing.T) {
	require.NoError(t, ValidateDingTalkConfig(DingTalkOAuthConfig{
		Enabled:               true,
		DingTalkAppKind:       "internal_app",
		AppType:               "public",
		CorpRestrictionPolicy: "none",
	}))
}

// TestValidateWebAuthnConfig supplies production and localhost relying-party
// origins plus missing, malformed, insecure, and mismatched identities.
// Validation must accept the valid cases and identify the expected invalid
// WebAuthn field in other cases.
func TestValidateWebAuthnConfig(t *testing.T) {
	tests := []struct {
		name      string        // subtest display name
		configure func(*Config) // fixture WebAuthn configuration callback
		// wantError supplies the expected error fragment. Empty text makes this case
		// require successful validation instead of an error containing the named fragment.
		wantError string
	}{
		{
			name: "valid production origin",
			configure: func(cfg *Config) {
				cfg.Auth.WebAuthn = WebAuthnConfig{
					Enabled:       true,
					RPDisplayName: "TokenRouter",
					RPID:          "tokenrouter.example.com",
					RPOrigins:     []string{"https://tokenrouter.example.com"},
				}
			},
		},
		{
			name: "valid localhost development origin",
			configure: func(cfg *Config) {
				cfg.Auth.WebAuthn = WebAuthnConfig{
					Enabled:       true,
					RPDisplayName: "TokenRouter Dev",
					RPID:          "localhost",
					RPOrigins:     []string{"http://localhost:5173"},
				}
			},
		},
		{
			name: "missing relying party id",
			configure: func(cfg *Config) {
				cfg.Auth.WebAuthn = WebAuthnConfig{
					Enabled:       true,
					RPDisplayName: "TokenRouter",
					RPOrigins:     []string{"https://tokenrouter.example.com"},
				}
			},
			wantError: "auth.webauthn.rp_id",
		},
		{
			name: "relying party id contains scheme",
			configure: func(cfg *Config) {
				cfg.Auth.WebAuthn = WebAuthnConfig{
					Enabled:       true,
					RPDisplayName: "TokenRouter",
					RPID:          "https://tokenrouter.example.com",
					RPOrigins:     []string{"https://tokenrouter.example.com"},
				}
			},
			wantError: "domain without scheme",
		},
		{
			name: "non-local insecure origin",
			configure: func(cfg *Config) {
				cfg.Auth.WebAuthn = WebAuthnConfig{
					Enabled:       true,
					RPDisplayName: "TokenRouter",
					RPID:          "tokenrouter.example.com",
					RPOrigins:     []string{"http://tokenrouter.example.com"},
				}
			},
			wantError: "must use HTTPS",
		},
		{
			name: "origin outside relying party id",
			configure: func(cfg *Config) {
				cfg.Auth.WebAuthn = WebAuthnConfig{
					Enabled:       true,
					RPDisplayName: "TokenRouter",
					RPID:          "example.com",
					RPOrigins:     []string{"https://example.net"},
				}
			},
			wantError: "not within relying party ID",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			fixture := newConfigFixture(t)
			fixture.Setenv("JWT_SECRET", strings.Repeat("x", 32))
			cfg, err := fixture.Load()
			require.NoError(t, err)
			tt.configure(cfg)

			err = cfg.Validate()
			if tt.wantError == "" {
				require.NoError(t, err)
			} else {
				require.ErrorContains(t, err, tt.wantError)
			}
		})
	}
}

// TestValidateWebAuthnNormalizationOnError supplies padded identities and an
// invalid second origin. Failures before, within, and after WebAuthn must retain
// the corresponding normalization progress while leaving the supplied slice intact.
func TestValidateWebAuthnNormalizationOnError(t *testing.T) {
	tests := []struct {
		name        string        // subtest display name
		configure   func(*Config) // validation failure location selection
		wantError   string        // expected first validation error
		wantName    string        // resulting relying party name
		wantID      string        // resulting relying party identifier
		wantOrigins []string      // resulting normalized origin sequence
	}{
		{
			name:        "earlier logging failure",
			configure:   func(c *Config) { c.Log.Level = "" },
			wantError:   "log.level is required",
			wantName:    "TokenRouter",
			wantID:      "example.com",
			wantOrigins: []string{"https://app.example.com", "https://BAD.EXAMPLE.COM/path", "https://last.example.com"},
		},
		{
			name:        "missing name after initial normalization",
			configure:   func(c *Config) { c.Auth.WebAuthn.RPDisplayName = " " },
			wantError:   "auth.webauthn.rp_display_name is required when passkeys are enabled",
			wantName:    "",
			wantID:      "example.com",
			wantOrigins: []string{"HTTPS://APP.EXAMPLE.COM", "https://BAD.EXAMPLE.COM/path", "HTTPS://LAST.EXAMPLE.COM"},
		},
		{
			name:        "invalid second origin after upfront canonicalization",
			configure:   func(c *Config) {},
			wantError:   "auth.webauthn.rp_origins entry \"https://BAD.EXAMPLE.COM/path\" must not include userinfo, path, query, or fragment",
			wantName:    "TokenRouter",
			wantID:      "example.com",
			wantOrigins: []string{"https://app.example.com", "https://BAD.EXAMPLE.COM/path", "https://last.example.com"},
		},
		{
			name: "later JWT lifetime failure",
			configure: func(c *Config) {
				c.Auth.WebAuthn.RPOrigins = []string{" HTTPS://APP.EXAMPLE.COM ", " ", " HTTPS://LAST.EXAMPLE.COM "}
				c.Auth.JWT.AccessTokenTTLMinutes = 0
			},
			wantError:   "auth.jwt.access_token_ttl_minutes must be positive",
			wantName:    "TokenRouter",
			wantID:      "example.com",
			wantOrigins: []string{"https://app.example.com", "https://last.example.com"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			fixture := newConfigFixture(t)
			cfg, err := fixture.Load()
			require.NoError(t, err)
			cfg.Auth.WebAuthn = WebAuthnConfig{
				Enabled:       true,
				RPDisplayName: " TokenRouter ",
				RPID:          " EXAMPLE.COM ",
				RPOrigins:     []string{" HTTPS://APP.EXAMPLE.COM ", " ", " https://BAD.EXAMPLE.COM/path ", " HTTPS://LAST.EXAMPLE.COM "},
			}
			tt.configure(cfg)
			supplied := cfg.Auth.WebAuthn.RPOrigins
			before := append([]string(nil), supplied...)
			require.EqualError(t, cfg.Validate(), tt.wantError)
			require.Equal(t, tt.wantName, cfg.Auth.WebAuthn.RPDisplayName)
			require.Equal(t, tt.wantID, cfg.Auth.WebAuthn.RPID)
			require.Equal(t, tt.wantOrigins, cfg.Auth.WebAuthn.RPOrigins)
			require.Equal(t, before, supplied)
			require.EqualError(t, cfg.Validate(), tt.wantError)
			require.Equal(t, tt.wantOrigins, cfg.Auth.WebAuthn.RPOrigins)
		})
	}
}

// TestValidateOIDCFirstErrorOrder combines missing credentials and invalid
// authentication controls with enabled token validation. Each correction must
// reveal the next exact failure while leaving the supplied OIDC text unchanged.
func TestValidateOIDCFirstErrorOrder(t *testing.T) {
	fixture := newConfigFixture(t)
	cfg, err := fixture.Load()
	require.NoError(t, err)
	cfg.Auth.OAuth.OIDC = OIDCConnectConfig{
		Enabled:             true,
		IssuerURL:           "https://issuer.example.com",
		RedirectURL:         "https://example.com/callback",
		FrontendRedirectURL: "/auth/callback",
		Scopes:              "profile",
		TokenAuthMethod:     " INVALID ",
		ClockSkewSeconds:    -1,
		ValidateIDToken:     true,
	}
	steps := []struct {
		wantError string        // expected first validation error
		repair    func(*Config) // correction exposing the next failure
	}{
		{
			wantError: "auth.oauth.oidc.client_id is required when auth.oauth.oidc.enabled=true",
			repair: func(c *Config) {
				c.Auth.OAuth.OIDC.ClientID = " client "
			},
		},
		{
			wantError: "auth.oauth.oidc.scopes must contain openid",
			repair: func(c *Config) {
				c.Auth.OAuth.OIDC.Scopes = " OPENID profile "
			},
		},
		{
			wantError: "auth.oauth.oidc.token_auth_method must be one of: client_secret_post/client_secret_basic/none",
			repair: func(c *Config) {
				c.Auth.OAuth.OIDC.TokenAuthMethod = " CLIENT_SECRET_POST "
			},
		},
		{
			wantError: "auth.oauth.oidc.client_secret is required when auth.oauth.oidc.enabled=true and token_auth_method is client_secret_post/client_secret_basic",
			repair: func(c *Config) {
				c.Auth.OAuth.OIDC.ClientSecret = " secret "
			},
		},
		{
			wantError: "auth.oauth.oidc.clock_skew_seconds must be between 0 and 600",
			repair: func(c *Config) {
				c.Auth.OAuth.OIDC.ClockSkewSeconds = 0
			},
		},
		{
			wantError: "auth.oauth.oidc.allowed_signing_algs is required when auth.oauth.oidc.validate_id_token=true",
			repair: func(c *Config) {
				c.Auth.OAuth.OIDC.AllowedSigningAlgs = " RS256 "
			},
		},
	}
	for _, step := range steps {
		require.EqualError(t, cfg.Validate(), step.wantError)
		normalized := cfg.Auth.OAuth.OIDC
		require.EqualError(t, cfg.Validate(), step.wantError)
		require.Equal(t, normalized, cfg.Auth.OAuth.OIDC)
		step.repair(cfg)
	}
	require.NoError(t, cfg.Validate())
	require.Equal(t, "client", cfg.Auth.OAuth.OIDC.ClientID)
	require.Equal(t, "OPENID profile", cfg.Auth.OAuth.OIDC.Scopes)
	require.Equal(t, "client_secret_post", cfg.Auth.OAuth.OIDC.TokenAuthMethod)
	require.Equal(t, "secret", cfg.Auth.OAuth.OIDC.ClientSecret)
	require.Equal(t, "RS256", cfg.Auth.OAuth.OIDC.AllowedSigningAlgs)
}

// TestValidateOIDCWarningOrder supplies HTTP endpoints and long JWT lifetimes.
// OIDC failures must suppress its endpoint warnings, while later failures and
// repeated calls emit those warnings after the preceding domain warnings.
func TestValidateOIDCWarningOrder(t *testing.T) {
	fixture := newConfigFixture(t)
	cfg, err := fixture.Load()
	require.NoError(t, err)
	cfg.Site.FrontendURL = "http://example.com"
	cfg.Auth.JWT.AccessTokenTTLMinutes = 1500
	cfg.Auth.OAuth.OIDC = OIDCConnectConfig{
		Enabled:             true,
		ClientID:            "client",
		TokenAuthMethod:     "none",
		IssuerURL:           "http://issuer.example.com",
		DiscoveryURL:        "http://issuer.example.com/discovery",
		AuthorizeURL:        "http://issuer.example.com/auth",
		TokenURL:            "http://issuer.example.com/token",
		UserInfoURL:         "http://issuer.example.com/userinfo",
		JWKSURL:             "http://issuer.example.com/jwks",
		RedirectURL:         "http://example.com/callback",
		FrontendRedirectURL: "http://example.com/auth/callback",
	}
	cfg.Billing.MinimumBalanceReserve = -1
	var logs bytes.Buffer
	// Capture warnings after loading so validation ordering is measured separately
	// from loader diagnostics, then restore the shared logger before later tests run.
	previous := slog.Default()
	slog.SetDefault(slog.New(slog.NewJSONHandler(&logs, nil)))
	t.Cleanup(func() { slog.SetDefault(previous) })

	want := []string{
		"site.frontend_url",
		"auth.jwt.access_token_ttl_minutes is high; consider shorter expiration for security",
	}
	for call := range 3 {
		logs.Reset()
		if call == 0 {
			require.EqualError(t, cfg.Validate(), "auth.oauth.oidc.scopes must contain openid")
		} else {
			require.EqualError(t, cfg.Validate(), "billing.minimum_balance_reserve must be non-negative")
		}
		var got []string
		decoder := json.NewDecoder(&logs)
		for decoder.More() {
			var record map[string]any
			require.NoError(t, decoder.Decode(&record))
			if field, ok := record["field"].(string); ok {
				got = append(got, field)
			} else {
				message, ok := record["msg"].(string)
				require.True(t, ok)
				got = append(got, message)
			}
		}
		require.Equal(t, want, got)
		if call == 0 {
			cfg.Auth.OAuth.OIDC.Scopes = "openid"
			want = append(want,
				"auth.oauth.oidc.issuer_url",
				"auth.oauth.oidc.discovery_url",
				"auth.oauth.oidc.authorize_url",
				"auth.oauth.oidc.token_url",
				"auth.oauth.oidc.userinfo_url",
				"auth.oauth.oidc.jwks_url",
				"auth.oauth.oidc.redirect_url",
				"auth.oauth.oidc.frontend_redirect_url",
			)
		}
	}
}
