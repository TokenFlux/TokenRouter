package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// TestLoadTimezonePrecedence combines a file zone with TIMEZONE, TZ, and absent
// values. The decoded zone must follow standard TZ, legacy environment, file,
// and default precedence in the supplied cases.
func TestLoadTimezonePrecedence(t *testing.T) {
	tests := []struct {
		name         string // subtest display name
		fileTimezone string // supplied YAML timezone name
		timezoneEnv  string // supplied legacy timezone value
		tzEnv        string // supplied standard timezone value
		want         string // expected decoded configuration value
	}{
		{name: "默认值", want: "Asia/Shanghai"},
		{name: "配置文件", fileTimezone: "Europe/London", want: "Europe/London"},
		{name: "兼容环境变量", fileTimezone: "Europe/London", timezoneEnv: "UTC", want: "UTC"},
		{name: "标准环境变量优先", fileTimezone: "Europe/London", timezoneEnv: "UTC", tzEnv: "America/New_York", want: "America/New_York"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			fixture := newConfigFixture(t)
			fixture.Setenv("TIMEZONE", tt.timezoneEnv)
			fixture.Setenv("TZ", tt.tzEnv)
			if tt.fileTimezone != "" {
				configFile := filepath.Join(t.TempDir(), "config.yaml")
				require.NoError(t, os.WriteFile(configFile, []byte("timezone: "+tt.fileTimezone+"\n"), 0o600))
				fixture.Setenv("CONFIG_FILE", configFile)
			}

			cfg, err := fixture.Load()
			require.NoError(t, err)
			require.Equal(t, tt.want, cfg.Timezone)
		})
	}
}

// TestNormalizeStringSlice supplies padded entries, empty strings,
// whitespace-only strings, and nil input. Returned entries must remain ordered
// after trimming and blank removal, while nil input must remain nil.
func TestNormalizeStringSlice(t *testing.T) {
	values := normalizeStringSlice([]string{" a ", "", "b", "   ", "c"})
	if len(values) != 3 || values[0] != "a" || values[1] != "b" || values[2] != "c" {
		t.Fatalf("normalizeStringSlice() unexpected result: %#v", values)
	}
	if normalizeStringSlice(nil) != nil {
		t.Fatalf("normalizeStringSlice(nil) expected nil slice")
	}
}

// TestProvideConfig supplies the standard reset fixture and invokes bootstrap
// configuration loading. The call must succeed, exercising the entry point used
// before database-backed JWT secret selection.
func TestProvideConfig(t *testing.T) {
	fixture := newConfigFixture(t)
	if _, err := fixture.LoadForBootstrap(); err != nil {
		t.Fatalf("fixture.LoadForBootstrap() error: %v", err)
	}
}

// TestValidateConfig_AutoScaleDisabledIgnoreAutoScaleFields disables
// autoscaling and supplies otherwise invalid autoscaling-only settings with a
// positive worker count. Validation must accept those inactive controls while
// keeping ordinary worker settings usable.
func TestValidateConfig_AutoScaleDisabledIgnoreAutoScaleFields(t *testing.T) {
	fixture := newConfigFixture(t)
	cfg, err := fixture.Load()
	if err != nil {
		t.Fatalf("fixture.Load() error: %v", err)
	}

	cfg.Usage.RecordQueue.AutoScaleEnabled = false
	cfg.Usage.RecordQueue.WorkerCount = 64

	// The disabled control should leave inactive scaling limits unchecked. Supply
	// invalid values across those fields while retaining the separately validated positive worker count.
	cfg.Usage.RecordQueue.AutoScaleMinWorkers = 0
	cfg.Usage.RecordQueue.AutoScaleMaxWorkers = 0
	cfg.Usage.RecordQueue.AutoScaleUpQueuePercent = 0
	cfg.Usage.RecordQueue.AutoScaleDownQueuePercent = 100
	cfg.Usage.RecordQueue.AutoScaleUpStep = 0
	cfg.Usage.RecordQueue.AutoScaleDownStep = 0
	cfg.Usage.RecordQueue.AutoScaleCheckIntervalSeconds = 0
	cfg.Usage.RecordQueue.AutoScaleCooldownSeconds = -1

	if err := cfg.Validate(); err != nil {
		t.Fatalf("Validate() should ignore auto scale fields when disabled: %v", err)
	}
}

// TestValidateDomainFirstErrorOrder combines invalid logging and authentication
// inputs with neighboring startup checks. The first error must follow the
// startup sequence even when another domain is also invalid.
func TestValidateDomainFirstErrorOrder(t *testing.T) {
	tests := []struct {
		name      string        // subtest display name
		configure func(*Config) // simultaneous invalid configuration inputs
		wantError string        // expected first validation error
	}{
		{
			name: "JWT secret before logging",
			configure: func(c *Config) {
				c.Auth.JWT.Secret = ""
				c.Log.Level = ""
			},
			wantError: "auth.jwt.secret is required",
		},
		{
			name: "Gemini before WebAuthn",
			configure: func(c *Config) {
				c.Provider.Gemini.OAuth.ClientID = "client"
				c.Provider.Gemini.OAuth.ClientSecret = ""
				c.Auth.WebAuthn = WebAuthnConfig{Enabled: true}
			},
			wantError: "provider.gemini.oauth.client_id and provider.gemini.oauth.client_secret must be both set or both empty",
		},
		{
			name: "frontend URL before WebAuthn",
			configure: func(c *Config) {
				c.Site.FrontendURL = "https://user@example.com"
				c.Auth.WebAuthn = WebAuthnConfig{Enabled: true}
			},
			wantError: "site.frontend_url invalid: must not include userinfo",
		},
		{
			name: "WebAuthn before JWT lifetime",
			configure: func(c *Config) {
				c.Auth.WebAuthn = WebAuthnConfig{Enabled: true}
				c.Auth.JWT.AccessTokenTTLMinutes = 0
			},
			wantError: "auth.webauthn.rp_display_name is required when passkeys are enabled",
		},
		{
			name: "LinuxDo before OIDC",
			configure: func(c *Config) {
				c.Auth.OAuth.LinuxDo.Enabled = true
				c.Auth.OAuth.LinuxDo.ClientID = ""
				c.Auth.OAuth.OIDC = OIDCConnectConfig{Enabled: true}
			},
			wantError: "auth.oauth.linuxdo.client_id is required when auth.oauth.linuxdo.enabled=true",
		},
		{
			name: "OIDC before billing",
			configure: func(c *Config) {
				c.Auth.OAuth.OIDC = OIDCConnectConfig{Enabled: true}
				c.Billing.MinimumBalanceReserve = -1
			},
			wantError: "auth.oauth.oidc.client_id is required when auth.oauth.oidc.enabled=true",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			fixture := newConfigFixture(t)
			cfg, err := fixture.Load()
			require.NoError(t, err)
			tt.configure(cfg)
			require.EqualError(t, cfg.Validate(), tt.wantError)
		})
	}
}

// TestValidateAfterBootstrapNormalizesAgain loads enabled authentication while
// deferring JWT selection. Supplying the persisted secret must permit repeated
// validation, retain normalized domain values, and republish static forwarding
// settings.
func TestValidateAfterBootstrapNormalizesAgain(t *testing.T) {
	fixture := newConfigFixture(t)
	fixture.Setenv("JWT_SECRET", "")
	fixture.SetValue("auth.webauthn.enabled", true)
	fixture.SetValue("auth.webauthn.rp_display_name", " TokenRouter ")
	fixture.SetValue("auth.webauthn.rp_id", " EXAMPLE.COM ")
	fixture.SetValue("auth.webauthn.rp_origins", []string{" HTTPS://APP.EXAMPLE.COM "})
	fixture.SetValue("auth.oauth.oidc.enabled", true)
	fixture.SetValue("auth.oauth.oidc.client_id", "client")
	fixture.SetValue("auth.oauth.oidc.client_secret", "secret")
	fixture.SetValue("auth.oauth.oidc.issuer_url", "https://issuer.example.com")
	fixture.SetValue("auth.oauth.oidc.redirect_url", "https://example.com/callback")
	fixture.SetValue("auth.oauth.oidc.frontend_redirect_url", "/auth/callback")
	cfg, err := fixture.LoadForBootstrap()
	require.NoError(t, err)
	require.Empty(t, cfg.Auth.JWT.Secret)
	require.Equal(t, "TokenRouter", cfg.Auth.WebAuthn.RPDisplayName)
	require.Equal(t, "example.com", cfg.Auth.WebAuthn.RPID)
	require.Equal(t, []string{"https://app.example.com"}, cfg.Auth.WebAuthn.RPOrigins)

	webAuthn := cfg.Auth.WebAuthn
	oidc := cfg.Auth.OAuth.OIDC
	logging := cfg.Log
	forwarding := cfg.ForwardedClientIPSettings()
	cfg.Auth.JWT.Secret = strings.Repeat("persisted", 4)
	for range 2 {
		cfg.SetForwardedClientIPSettings(!cfg.Server.ClientIP.TrustForwardedHeaders, []string{"X-Runtime-IP"})
		require.NoError(t, cfg.Validate())
		require.Equal(t, webAuthn, cfg.Auth.WebAuthn)
		require.Equal(t, oidc, cfg.Auth.OAuth.OIDC)
		require.Equal(t, logging, cfg.Log)
		require.Equal(t, forwarding, cfg.ForwardedClientIPSettings())
	}
}
