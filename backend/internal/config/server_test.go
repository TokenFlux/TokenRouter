package config

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/stretchr/testify/require"
)

// TestLoadServerTimingConfig loads the default server settings and then
// supplies ENABLE_SERVER_TIMING. Timing must start disabled and become enabled
// through that explicitly bound environment variable.
func TestLoadServerTimingConfig(t *testing.T) {
	t.Run("disabled by default", func(t *testing.T) {
		fixture := newConfigFixture(t)
		cfg, err := fixture.Load()
		require.NoError(t, err)
		require.False(t, cfg.Server.EnableServerTiming)
	})

	t.Run("enabled by exact environment variable", func(t *testing.T) {
		fixture := newConfigFixture(t)
		fixture.Setenv("ENABLE_SERVER_TIMING", "true")
		cfg, err := fixture.Load()
		require.NoError(t, err)
		require.True(t, cfg.Server.EnableServerTiming)
	})
}

// TestLoadHTTPIngressSafetyDefaults inspects default header budgets, proxy
// presence, forwarding trust, text-body size, and invalid-auth limits. The
// decoded values must match those defaults and keep an absent trusted list
// distinguishable.
func TestLoadHTTPIngressSafetyDefaults(t *testing.T) {
	fixture := newConfigFixture(t)
	cfg, err := fixture.Load()
	require.NoError(t, err)
	require.Equal(t, 10, cfg.Server.ReadHeaderTimeoutSeconds)
	require.Equal(t, 64*1024, cfg.Server.MaxHeaderBytes)
	require.Empty(t, cfg.Server.ClientIP.TrustedProxies)
	require.False(t, cfg.Server.ClientIP.TrustedProxiesConfigured)
	require.True(t, cfg.TrustForwardedHeaders())
	require.Equal(t, int64(32*1024*1024), cfg.Gateway.TextMaxRequestBodyBytes)
	require.True(t, cfg.APIKey.InvalidKeyAbuse.Enabled)
	require.Equal(t, 120, cfg.APIKey.InvalidKeyAbuse.Threshold)
	require.Equal(t, 16384, cfg.APIKey.InvalidKeyAbuse.Capacity)
}

// TestNormalizeForwardedClientIPHeaders supplies padded and differently cased
// names plus an invalid field spelling. Normalization must retain ordered
// canonical unique names and return the expected error for malformed syntax.
func TestNormalizeForwardedClientIPHeaders(t *testing.T) {
	headers, err := NormalizeForwardedClientIPHeaders([]string{
		" x-cdn-client-ip ",
		"X-CDN-CLIENT-IP",
		"true-client-ip",
	})
	require.NoError(t, err)
	require.Equal(t, []string{"X-Cdn-Client-Ip", "True-Client-Ip"}, headers)

	_, err = NormalizeForwardedClientIPHeaders([]string{"X Invalid"})
	require.ErrorContains(t, err, "invalid HTTP header field name")
}

// TestNormalizeForwardedClientIPHeadersLimit supplies one more distinct source
// name than the configured maximum. Normalization must report the unique-header
// limit instead of accepting an unbounded discovery list.
func TestNormalizeForwardedClientIPHeadersLimit(t *testing.T) {
	headers := make([]string, 0, MaxForwardedClientIPHeaders+1)
	for i := 0; i <= MaxForwardedClientIPHeaders; i++ {
		headers = append(headers, fmt.Sprintf("X-CDN-IP-%d", i))
	}

	_, err := NormalizeForwardedClientIPHeaders(headers)
	require.ErrorContains(t, err, "at most 16 unique names")
}

// TestLoadForwardedClientIPHeadersNormalizesAndSnapshots loads duplicate and
// padded header names and mutates the returned list. Later snapshots must
// retain the canonical stored values, protecting independent caller ownership
// of header storage.
func TestLoadForwardedClientIPHeadersNormalizesAndSnapshots(t *testing.T) {
	fixture := newConfigFixture(t)
	fixture.SetValue("server.client_ip.forwarded_headers", []string{" x-cdn-ip ", "X-CDN-IP", "true-client-ip"})

	cfg, err := fixture.Load()
	require.NoError(t, err)
	snapshot := cfg.ForwardedClientIPSettings()
	require.Equal(t, []string{"X-Cdn-Ip", "True-Client-Ip"}, snapshot.Headers)

	snapshot.Headers[0] = "X-Mutated"
	require.Equal(t, []string{"X-Cdn-Ip", "True-Client-Ip"}, cfg.ForwardedClientIPSettings().Headers)
}

// TestForwardedClientIPSettingsConcurrentPublication initializes the live
// pointer before concurrent writers publish two whole policies and readers
// inspect them. Every observation must match one complete flag/header pair,
// with no mixed result.
func TestForwardedClientIPSettingsConcurrentPublication(t *testing.T) {
	cfg := &Config{}
	cfg.SetForwardedClientIPSettings(true, []string{"X-Public-A"})

	// Repeat reads and publications to expose mixed flag/header observations.
	const iterations = 2000
	// Initialize live storage before the gate releases writers and readers. Each
	// reader reports at most one error into a buffer sized for all reader goroutines.
	start := make(chan struct{})
	errCh := make(chan error, 8)
	var wg sync.WaitGroup

	for _, settings := range []ForwardedClientIPSettings{
		{TrustForwardedIP: true, Headers: []string{"X-Public-A"}},
		{TrustForwardedIP: false, Headers: []string{"X-Public-B"}},
	} {
		wg.Go(func() {
			<-start
			for range iterations {
				cfg.SetForwardedClientIPSettings(settings.TrustForwardedIP, settings.Headers)
			}
		})
	}

	for range cap(errCh) {
		wg.Go(func() {
			<-start
			for range iterations {
				snapshot := cfg.ForwardedClientIPSettings()
				validA := snapshot.TrustForwardedIP && len(snapshot.Headers) == 1 && snapshot.Headers[0] == "X-Public-A"
				validB := !snapshot.TrustForwardedIP && len(snapshot.Headers) == 1 && snapshot.Headers[0] == "X-Public-B"
				if !validA && !validB {
					errCh <- fmt.Errorf("observed inconsistent forwarded IP settings: %+v", snapshot)
					return
				}
			}
		})
	}

	// Release every worker, then wait before closing error publication. The final
	// range can consume all reader errors after no goroutine can send another one.
	close(start)
	wg.Wait()
	close(errCh)
	for err := range errCh {
		require.NoError(t, err)
	}
}

// TestLoadRejectsInvalidForwardedClientIPHeader supplies malformed header text
// through Viper configuration. Loading must return an error naming the header
// list, protecting rejection of this invalid input at the loader check.
func TestLoadRejectsInvalidForwardedClientIPHeader(t *testing.T) {
	fixture := newConfigFixture(t)
	fixture.SetValue("server.client_ip.forwarded_headers", []string{"X Invalid"})

	_, err := fixture.Load()
	require.ErrorContains(t, err, "server.client_ip.forwarded_headers")
}

// TestLoadExplicitEmptyTrustedProxiesEnablesConfiguredMode supplies an empty
// trusted-proxy list through Viper. Loading must retain emptiness and set the
// presence flag, separating this input from an omitted policy.
func TestLoadExplicitEmptyTrustedProxiesEnablesConfiguredMode(t *testing.T) {
	fixture := newConfigFixture(t)
	fixture.SetValue("server.client_ip.trusted_proxies", []string{})

	cfg, err := fixture.Load()
	require.NoError(t, err)
	require.Empty(t, cfg.Server.ClientIP.TrustedProxies)
	require.True(t, cfg.Server.ClientIP.TrustedProxiesConfigured)
}

// TestLoadExplicitTrustedProxiesEnablesConfiguredMode supplies one trusted
// loopback range through Viper. Loading must retain the range and mark the
// trusted-proxy policy as configured for downstream address selection.
func TestLoadExplicitTrustedProxiesEnablesConfiguredMode(t *testing.T) {
	fixture := newConfigFixture(t)
	fixture.SetValue("server.client_ip.trusted_proxies", []string{"127.0.0.1/32"})

	cfg, err := fixture.Load()
	require.NoError(t, err)
	require.Equal(t, []string{"127.0.0.1/32"}, cfg.Server.ClientIP.TrustedProxies)
	require.True(t, cfg.Server.ClientIP.TrustedProxiesConfigured)
}

// TestLoadTrustedProxiesPresenceFromYAML supplies omitted, empty, and populated
// lists through temporary YAML files. Decoded entries and the presence flag
// must distinguish all three tested configuration shapes.
func TestLoadTrustedProxiesPresenceFromYAML(t *testing.T) {
	tests := []struct {
		name       string   // subtest display name
		yaml       string   // supplied YAML configuration text
		want       []string // expected decoded configuration value
		configured bool     // expected explicit proxy presence
	}{
		{name: "absent", yaml: "server:\n  mode: debug\n", configured: false},
		{name: "explicit empty", yaml: "server:\n  trusted_proxies: []\n", want: []string{}, configured: true},
		{
			name:       "populated",
			yaml:       "server:\n  trusted_proxies:\n    - 127.0.0.1/32\n    - ::1/128\n",
			want:       []string{"127.0.0.1/32", "::1/128"},
			configured: true,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			fixture := newConfigFixture(t)
			configDir := t.TempDir()
			require.NoError(t, os.WriteFile(filepath.Join(configDir, "config.yaml"), []byte(test.yaml), 0o600))
			fixture.Setenv("DATA_DIR", configDir)

			cfg, err := fixture.Load()
			require.NoError(t, err)
			require.Equal(t, test.want, cfg.Server.ClientIP.TrustedProxies)
			require.Equal(t, test.configured, cfg.Server.ClientIP.TrustedProxiesConfigured)
		})
	}
}

// TestLoadTrustedProxiesFromConfigFile selects a named YAML file containing a
// listener and multiple trusted ranges. Loading must read that file and retain
// its host, range order, and configured flag.
func TestLoadTrustedProxiesFromConfigFile(t *testing.T) {
	fixture := newConfigFixture(t)
	configFile := filepath.Join(t.TempDir(), "reporter-config.yaml")
	require.NoError(t, os.WriteFile(configFile, []byte(`server:
  host: 192.0.2.10
  trusted_proxies:
    - 127.0.0.1/32
    - 10.0.0.0/8
    - 172.16.0.0/12
    - 192.168.0.0/16
`), 0o600))
	fixture.Setenv("CONFIG_FILE", configFile)

	cfg, err := fixture.Load()
	require.NoError(t, err)
	require.Equal(t, "192.0.2.10", cfg.Server.Host)
	require.Equal(t, []string{"127.0.0.1/32", "10.0.0.0/8", "172.16.0.0/12", "192.168.0.0/16"}, cfg.Server.ClientIP.TrustedProxies)
	require.True(t, cfg.Server.ClientIP.TrustedProxiesConfigured)
}

// TestLoadDefaultServerMode reads the default listener settings from the
// fixture loader. Its Gin mode must remain release, protecting the configured
// default before application HTTP server construction.
func TestLoadDefaultServerMode(t *testing.T) {
	fixture := newConfigFixture(t)

	cfg, err := fixture.Load()
	if err != nil {
		t.Fatalf("fixture.Load() error: %v", err)
	}

	if cfg.Server.Mode != "release" {
		t.Fatalf("Server.Mode = %q, want %q", cfg.Server.Mode, "release")
	}
}

// TestValidateAbsoluteHTTPURL supplies an absolute HTTPS URL, empty and
// relative text, another scheme, and a fragment. The valid endpoint must pass,
// while the tested invalid forms must return errors.
func TestValidateAbsoluteHTTPURL(t *testing.T) {
	if err := ValidateAbsoluteHTTPURL("https://example.com/path"); err != nil {
		t.Fatalf("ValidateAbsoluteHTTPURL valid url error: %v", err)
	}
	if err := ValidateAbsoluteHTTPURL(""); err == nil {
		t.Fatalf("ValidateAbsoluteHTTPURL should reject empty url")
	}
	if err := ValidateAbsoluteHTTPURL("/relative"); err == nil {
		t.Fatalf("ValidateAbsoluteHTTPURL should reject relative url")
	}
	if err := ValidateAbsoluteHTTPURL("ftp://example.com"); err == nil {
		t.Fatalf("ValidateAbsoluteHTTPURL should reject ftp scheme")
	}
	if err := ValidateAbsoluteHTTPURL("https://example.com/#frag"); err == nil {
		t.Fatalf("ValidateAbsoluteHTTPURL should reject fragment")
	}
}

// TestValidateServerFrontendURL supplies a host, optional path, query,
// userinfo, and relative forms on a valid configuration. Validation must accept
// host/path values and reject the tested unsafe frontend-base shapes.
func TestValidateServerFrontendURL(t *testing.T) {
	fixture := newConfigFixture(t)

	cfg, err := fixture.Load()
	if err != nil {
		t.Fatalf("fixture.Load() error: %v", err)
	}

	cfg.Site.FrontendURL = "https://example.com"
	if err := cfg.Validate(); err != nil {
		t.Fatalf("Validate() frontend_url valid error: %v", err)
	}

	cfg.Site.FrontendURL = "https://example.com/path"
	if err := cfg.Validate(); err != nil {
		t.Fatalf("Validate() frontend_url with path valid error: %v", err)
	}

	cfg.Site.FrontendURL = "https://example.com?utm=1"
	if err := cfg.Validate(); err == nil {
		t.Fatalf("Validate() should reject site.frontend_url with query")
	}

	cfg.Site.FrontendURL = "https://user:pass@example.com"
	if err := cfg.Validate(); err == nil {
		t.Fatalf("Validate() should reject site.frontend_url with userinfo")
	}

	cfg.Site.FrontendURL = "/relative"
	if err := cfg.Validate(); err == nil {
		t.Fatalf("Validate() should reject relative site.frontend_url")
	}
}

// TestValidateFrontendRedirectURL supplies a single-slash callback, an absolute
// HTTPS callback, and malformed or unsupported alternatives. The accepted forms
// must pass, while protocol-relative and JavaScript forms must fail.
func TestValidateFrontendRedirectURL(t *testing.T) {
	if err := ValidateFrontendRedirectURL("/auth/callback"); err != nil {
		t.Fatalf("ValidateFrontendRedirectURL relative error: %v", err)
	}
	if err := ValidateFrontendRedirectURL("https://example.com/auth"); err != nil {
		t.Fatalf("ValidateFrontendRedirectURL absolute error: %v", err)
	}
	if err := ValidateFrontendRedirectURL("example.com/path"); err == nil {
		t.Fatalf("ValidateFrontendRedirectURL should reject non-absolute url")
	}
	if err := ValidateFrontendRedirectURL("//evil.com"); err == nil {
		t.Fatalf("ValidateFrontendRedirectURL should reject // prefix")
	}
	if err := ValidateFrontendRedirectURL("javascript:alert(1)"); err == nil {
		t.Fatalf("ValidateFrontendRedirectURL should reject javascript scheme")
	}
}

// TestWarnIfInsecureURL calls the warning helper with HTTP, non-HTTP, and
// malformed address text. The test exercises warning and ignored-parse paths
// for completion; it does not capture or compare logger output.
func TestWarnIfInsecureURL(t *testing.T) {
	warnIfInsecureURL("test", "http://example.com")
	warnIfInsecureURL("test", "bad://url")
	warnIfInsecureURL("test", "://invalid")
}

// TestValidateAbsoluteHTTPURLMissingHost supplies an HTTPS scheme whose host is
// absent. Validation must return an error so a scheme alone cannot be accepted
// as a usable absolute endpoint shape.
func TestValidateAbsoluteHTTPURLMissingHost(t *testing.T) {
	if err := ValidateAbsoluteHTTPURL("https://"); err == nil {
		t.Fatalf("ValidateAbsoluteHTTPURL should reject missing host")
	}
}

// TestValidateFrontendRedirectURLInvalidChars supplies an embedded line break,
// an HTTP scheme with no host, and a mailto value. Every tested form must fail
// callback validation.
func TestValidateFrontendRedirectURLInvalidChars(t *testing.T) {
	if err := ValidateFrontendRedirectURL("/auth/\ncallback"); err == nil {
		t.Fatalf("ValidateFrontendRedirectURL should reject invalid chars")
	}
	if err := ValidateFrontendRedirectURL("http://"); err == nil {
		t.Fatalf("ValidateFrontendRedirectURL should reject missing host")
	}
	if err := ValidateFrontendRedirectURL("mailto:user@example.com"); err == nil {
		t.Fatalf("ValidateFrontendRedirectURL should reject mailto")
	}
}

// TestWarnIfInsecureURLHTTPS calls the warning helper with an HTTPS endpoint.
// The test exercises successful parsing and completion on that secure-scheme
// path, while leaving logging assertions to other consumers.
func TestWarnIfInsecureURLHTTPS(t *testing.T) {
	warnIfInsecureURL("secure", "https://example.com")
}

// TestValidateConfig_LogRequiredAndRotationBounds clears logging enums and
// supplies negative rotation or disabled-sampling values. Each serial case must
// identify its invalid field in the returned validation error.
func TestValidateConfig_LogRequiredAndRotationBounds(t *testing.T) {
	fixture := newConfigFixture(t)

	cases := []struct {
		name    string        // subtest display name
		mutate  func(*Config) // fixture configuration mutation callback
		wantErr string        // expected validation error fragment
	}{
		{
			name: "log level required",
			mutate: func(c *Config) {
				c.Log.Level = ""
			},
			wantErr: "log.level is required",
		},
		{
			name: "log format required",
			mutate: func(c *Config) {
				c.Log.Format = ""
			},
			wantErr: "log.format is required",
		},
		{
			name: "log stacktrace required",
			mutate: func(c *Config) {
				c.Log.StacktraceLevel = ""
			},
			wantErr: "log.stacktrace_level is required",
		},
		{
			name: "log max backups non-negative",
			mutate: func(c *Config) {
				c.Log.Rotation.MaxBackups = -1
			},
			wantErr: "log.rotation.max_backups must be non-negative",
		},
		{
			name: "log max age non-negative",
			mutate: func(c *Config) {
				c.Log.Rotation.MaxAgeDays = -1
			},
			wantErr: "log.rotation.max_age_days must be non-negative",
		},
		{
			name: "sampling thereafter non-negative when disabled",
			mutate: func(c *Config) {
				c.Log.Sampling.Enabled = false
				c.Log.Sampling.Thereafter = -1
			},
			wantErr: "log.sampling.thereafter must be non-negative",
		},
	}

	for _, tt := range cases {
		t.Run(tt.name, func(t *testing.T) {
			cfg, err := fixture.Load()
			if err != nil {
				t.Fatalf("fixture.Load() error: %v", err)
			}
			tt.mutate(cfg)
			err = cfg.Validate()
			if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
				t.Fatalf("Validate() error = %v, want %q", err, tt.wantErr)
			}
		})
	}
}

// TestNormalizeProxyProbeURLs supplies a padded endpoint and uppercase parser
// alongside a second query URL. Normalization must trim the endpoint, lowercase
// the parser, and retain target order and query text.
func TestNormalizeProxyProbeURLs(t *testing.T) {
	t.Parallel()

	got, err := normalizeProxyProbeURLs([]ProbeURLConfig{
		{URL: " https://chatgpt.com/cdn-cgi/trace ", Parser: " CHATGPT-TRACE "},
		{URL: "https://api64.ipify.org?format=json", Parser: "ipify"},
	})
	require.NoError(t, err)
	require.Equal(t, []ProbeURLConfig{
		{URL: "https://chatgpt.com/cdn-cgi/trace", Parser: "chatgpt-trace"},
		{URL: "https://api64.ipify.org?format=json", Parser: "ipify"},
	}, got)
}

// TestNormalizeProxyProbeURLsRejectsInvalidEntries supplies absent URL/parser
// values, an unknown parser, a relative path, and FTP text. Each fixture must
// return the expected entry validation fragment.
func TestNormalizeProxyProbeURLsRejectsInvalidEntries(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string         // subtest display name
		target  ProbeURLConfig // supplied proxy probe target
		wantErr string         // expected validation error fragment
	}{
		{name: "missing URL", target: ProbeURLConfig{Parser: "ipify"}, wantErr: "url is required"},
		{name: "missing parser", target: ProbeURLConfig{URL: "https://example.com"}, wantErr: "parser is required"},
		{name: "unknown parser", target: ProbeURLConfig{URL: "https://example.com", Parser: "ip_api"}, wantErr: "unsupported parser"},
		{name: "relative URL", target: ProbeURLConfig{URL: "/cdn-cgi/trace", Parser: "chatgpt-trace"}, wantErr: "invalid url"},
		{name: "unsupported scheme", target: ProbeURLConfig{URL: "ftp://example.com/file", Parser: "ipify"}, wantErr: "scheme must be http or https"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			_, err := normalizeProxyProbeURLs([]ProbeURLConfig{tt.target})
			require.ErrorContains(t, err, tt.wantErr)
		})
	}
}

// TestValidateLogFirstErrorOrder supplies invalid logging controls together
// with an incomplete Gemini credential pair. Repairing one failure at a time
// must expose the next exact error before validation reaches Gemini.
func TestValidateLogFirstErrorOrder(t *testing.T) {
	fixture := newConfigFixture(t)
	cfg, err := fixture.Load()
	require.NoError(t, err)
	cfg.Log.Level = "invalid"
	cfg.Log.Format = "invalid"
	cfg.Log.StacktraceLevel = "invalid"
	cfg.Log.Output.ToStdout = false
	cfg.Log.Output.ToFile = false
	cfg.Log.Rotation.MaxSizeMB = 0
	cfg.Log.Rotation.MaxBackups = -1
	cfg.Log.Rotation.MaxAgeDays = -1
	cfg.Log.Sampling.Enabled = true
	cfg.Log.Sampling.Initial = 0
	cfg.Log.Sampling.Thereafter = 0
	cfg.Provider.Gemini.OAuth.ClientID = "client"
	cfg.Provider.Gemini.OAuth.ClientSecret = ""

	steps := []struct {
		wantError string        // expected first validation error
		repair    func(*Config) // correction exposing the next failure
	}{
		{
			wantError: "log.level must be one of: debug/info/warn/error",
			repair: func(c *Config) {
				c.Log.Level = "info"
			},
		},
		{
			wantError: "log.format must be one of: json/console",
			repair: func(c *Config) {
				c.Log.Format = "json"
			},
		},
		{
			wantError: "log.stacktrace_level must be one of: none/error/fatal",
			repair: func(c *Config) {
				c.Log.StacktraceLevel = "none"
			},
		},
		{
			wantError: "log.output.to_stdout and log.output.to_file cannot both be false",
			repair: func(c *Config) {
				c.Log.Output.ToStdout = true
			},
		},
		{
			wantError: "log.rotation.max_size_mb must be positive",
			repair: func(c *Config) {
				c.Log.Rotation.MaxSizeMB = 1
			},
		},
		{
			wantError: "log.rotation.max_backups must be non-negative",
			repair: func(c *Config) {
				c.Log.Rotation.MaxBackups = 0
			},
		},
		{
			wantError: "log.rotation.max_age_days must be non-negative",
			repair: func(c *Config) {
				c.Log.Rotation.MaxAgeDays = 0
			},
		},
		{
			wantError: "log.sampling.initial must be positive when sampling is enabled",
			repair: func(c *Config) {
				c.Log.Sampling.Initial = 1
			},
		},
		{
			wantError: "log.sampling.thereafter must be positive when sampling is enabled",
			repair: func(c *Config) {
				c.Log.Sampling.Thereafter = 1
			},
		},
		{
			wantError: "provider.gemini.oauth.client_id and provider.gemini.oauth.client_secret must be both set or both empty",
			repair: func(c *Config) {
				c.Provider.Gemini.OAuth.ClientID = ""
			},
		},
	}
	for _, step := range steps {
		require.EqualError(t, cfg.Validate(), step.wantError)
		step.repair(cfg)
	}
	require.NoError(t, cfg.Validate())

	cfg.Log.Sampling.Enabled = false
	cfg.Log.Sampling.Initial = -1
	cfg.Log.Sampling.Thereafter = -1
	require.EqualError(t, cfg.Validate(), "log.sampling.initial must be non-negative")
	cfg.Log.Sampling.Initial = 0
	require.EqualError(t, cfg.Validate(), "log.sampling.thereafter must be non-negative")
}
