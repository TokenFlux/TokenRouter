package config

import (
	"bytes"
	"encoding/json"
	"fmt"
	"log/slog"
	"math"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/stretchr/testify/require"
)

// TestLoadDefaultModelsListReadMaxBytes loads configuration with the standard
// fixture JWT secret. The model-list body limit must equal the named default
// used for upstream model responses.
func TestLoadDefaultModelsListReadMaxBytes(t *testing.T) {
	fixture := newConfigFixture(t)
	cfg, err := fixture.Load()
	require.NoError(t, err)
	require.Equal(t, DefaultModelsListReadMaxBytes, cfg.Gateway.ModelsListResponseMaxBytes)
}

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

// TestLoadAdvancedSchedulerStickyEscapeZeroBoundaries supplies zero error-rate
// and TTFT thresholds through YAML and environment sources. Loading must retain
// zero error rate while reporting the invalid zero TTFT limit.
func TestLoadAdvancedSchedulerStickyEscapeZeroBoundaries(t *testing.T) {
	t.Run("YAML 保留显式零错误率", func(t *testing.T) {
		fixture := newConfigFixture(t)
		configFile := filepath.Join(t.TempDir(), "config.yaml")
		require.NoError(t, os.WriteFile(configFile, []byte("gateway:\n  advanced_scheduler:\n    sticky_escape_error_rate: 0\n"), 0o600))
		fixture.Setenv("CONFIG_FILE", configFile)

		cfg, err := fixture.Load()
		require.NoError(t, err)
		require.Zero(t, cfg.Scheduling.Advanced.StickyEscapeErrorRate)
	})

	t.Run("环境变量保留显式零错误率", func(t *testing.T) {
		fixture := newConfigFixture(t)
		fixture.Setenv("GATEWAY_ADVANCED_SCHEDULER_STICKY_ESCAPE_ERROR_RATE", "0")

		cfg, err := fixture.Load()
		require.NoError(t, err)
		require.Zero(t, cfg.Scheduling.Advanced.StickyEscapeErrorRate)
	})

	for _, source := range []string{"yaml", "env"} {
		t.Run(source+" 拒绝显式零 TTFT", func(t *testing.T) {
			fixture := newConfigFixture(t)
			if source == "yaml" {
				configFile := filepath.Join(t.TempDir(), "config.yaml")
				require.NoError(t, os.WriteFile(configFile, []byte("gateway:\n  advanced_scheduler:\n    sticky_escape_ttft_ms: 0\n"), 0o600))
				fixture.Setenv("CONFIG_FILE", configFile)
			} else {
				fixture.Setenv("GATEWAY_ADVANCED_SCHEDULER_STICKY_ESCAPE_TTFT_MS", "0")
			}

			_, err := fixture.Load()
			require.ErrorContains(t, err, "scheduling.advanced.sticky_escape_ttft_ms")
		})
	}
}

// TestLoadAdvancedSchedulerEWMAAlphaFromYAMLAndEnvironment supplies separate
// smoothing values through YAML and environment input. Both feedback alpha
// fields must receive their expected values, including the valid upper endpoint
// one.
func TestLoadAdvancedSchedulerEWMAAlphaFromYAMLAndEnvironment(t *testing.T) {
	t.Run("YAML", func(t *testing.T) {
		fixture := newConfigFixture(t)
		configFile := filepath.Join(t.TempDir(), "config.yaml")
		require.NoError(t, os.WriteFile(configFile, []byte("gateway:\n  advanced_scheduler:\n    ewma_error_rate_alpha: 0.35\n    ewma_ttft_alpha: 1\n"), 0o600))
		fixture.Setenv("CONFIG_FILE", configFile)

		cfg, err := fixture.Load()
		require.NoError(t, err)
		require.Equal(t, 0.35, cfg.Scheduling.Advanced.EWMAErrorRateAlpha)
		require.Equal(t, 1.0, cfg.Scheduling.Advanced.EWMATTFTAlpha)
	})

	t.Run("环境变量", func(t *testing.T) {
		fixture := newConfigFixture(t)
		fixture.Setenv("GATEWAY_ADVANCED_SCHEDULER_EWMA_ERROR_RATE_ALPHA", "0.05")
		fixture.Setenv("GATEWAY_ADVANCED_SCHEDULER_EWMA_TTFT_ALPHA", "0.9")

		cfg, err := fixture.Load()
		require.NoError(t, err)
		require.Equal(t, 0.05, cfg.Scheduling.Advanced.EWMAErrorRateAlpha)
		require.Equal(t, 0.9, cfg.Scheduling.Advanced.EWMATTFTAlpha)
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

// TestLoadDefaultSchedulingConfig inspects sticky and alternative wait budgets,
// queue sizes, batched load cache duration, and cleanup interval. Default
// loading must return the expected values with batching enabled.
func TestLoadDefaultSchedulingConfig(t *testing.T) {
	fixture := newConfigFixture(t)

	cfg, err := fixture.Load()
	if err != nil {
		t.Fatalf("fixture.Load() error: %v", err)
	}

	if cfg.Scheduling.StickyProviderWait.MaxWaiting != 3 {
		t.Fatalf("StickySessionMaxWaiting = %d, want 3", cfg.Scheduling.StickyProviderWait.MaxWaiting)
	}
	if cfg.Scheduling.StickyProviderWait.TimeoutSeconds != 120 {
		t.Fatalf("StickySessionWaitTimeout = %v, want 120s", cfg.Scheduling.StickyProviderWait.TimeoutSeconds)
	}
	if cfg.Scheduling.AlternateProviderWait.TimeoutSeconds != 30 {
		t.Fatalf("FallbackWaitTimeout = %v, want 30s", cfg.Scheduling.AlternateProviderWait.TimeoutSeconds)
	}
	if cfg.Scheduling.AlternateProviderWait.MaxWaiting != 100 {
		t.Fatalf("FallbackMaxWaiting = %d, want 100", cfg.Scheduling.AlternateProviderWait.MaxWaiting)
	}
	if !cfg.Scheduling.LoadBatch.Enabled {
		t.Fatalf("LoadBatchEnabled = false, want true")
	}
	if cfg.Scheduling.LoadBatch.CacheTTLMS != 200 {
		t.Fatalf("LoadBatchCacheTTLMS = %d, want 200", cfg.Scheduling.LoadBatch.CacheTTLMS)
	}
	if cfg.Scheduling.SlotCleanupIntervalSeconds != 30 {
		t.Fatalf("SlotCleanupInterval = %v, want 30s", cfg.Scheduling.SlotCleanupIntervalSeconds)
	}
}

// TestLoadDefaultOpenAIFirstOutputTimeoutsDisabled loads the default OpenAI
// semantic-output budgets. Both ordinary and high-effort timeout fields must
// remain zero, leaving those configured first-output deadlines disabled.
func TestLoadDefaultOpenAIFirstOutputTimeoutsDisabled(t *testing.T) {
	fixture := newConfigFixture(t)

	cfg, err := fixture.Load()
	require.NoError(t, err)
	require.Zero(t, cfg.Gateway.OpenAI.FirstOutputTimeoutSeconds)
	require.Zero(t, cfg.Gateway.OpenAI.HighEffortFirstOutputTimeoutSeconds)
}

// TestValidateOpenAIFirstOutputTimeoutMinimum changes a valid configuration to
// the thirty-second ordinary first-output threshold. Validation must accept
// that positive minimum so the endpoint of the permitted range remains usable.
func TestValidateOpenAIFirstOutputTimeoutMinimum(t *testing.T) {
	fixture := newConfigFixture(t)
	cfg, err := fixture.Load()
	require.NoError(t, err)
	cfg.Gateway.OpenAI.FirstOutputTimeoutSeconds = 30
	require.NoError(t, cfg.Validate())
}

// TestLoadDefaultOpenAIWSConfig inspects connection scaling, pool limits,
// ingress budgets, sticky compatibility flags, metadata, and scheduler
// defaults. Each observed field must match the supplied default expectations
// after loading.
func TestLoadDefaultOpenAIWSConfig(t *testing.T) {
	fixture := newConfigFixture(t)

	cfg, err := fixture.Load()
	if err != nil {
		t.Fatalf("fixture.Load() error: %v", err)
	}

	if !cfg.Gateway.OpenAI.ResponsesWS.DynamicMaxConnsByProviderConcurrencyEnabled {
		t.Fatalf("Gateway.OpenAIWS.DynamicMaxConnsByProviderConcurrencyEnabled = false, want true")
	}
	if cfg.Gateway.OpenAI.ResponsesWS.OAuthMaxConnsFactor != 1.0 {
		t.Fatalf("Gateway.OpenAIWS.OAuthMaxConnsFactor = %v, want 1.0", cfg.Gateway.OpenAI.ResponsesWS.OAuthMaxConnsFactor)
	}
	if cfg.Gateway.OpenAI.ResponsesWS.APIKeyMaxConnsFactor != 1.0 {
		t.Fatalf("Gateway.OpenAIWS.APIKeyMaxConnsFactor = %v, want 1.0", cfg.Gateway.OpenAI.ResponsesWS.APIKeyMaxConnsFactor)
	}
	if cfg.Scheduling.SessionAffinity.TTLSeconds != 3600 {
		t.Fatalf("Gateway.OpenAIWS.StickySessionTTLSeconds = %d, want 3600", cfg.Scheduling.SessionAffinity.TTLSeconds)
	}
	if !cfg.Scheduling.Advanced.StickyEscapeEnabled {
		t.Fatalf("Gateway.AdvancedScheduler.StickyEscapeEnabled = false, want true")
	}
	if cfg.Scheduling.Advanced.StickyEscapeTTFTMs != 15000 {
		t.Fatalf("Gateway.AdvancedScheduler.StickyEscapeTTFTMs = %d, want 15000", cfg.Scheduling.Advanced.StickyEscapeTTFTMs)
	}
	if cfg.Scheduling.Advanced.StickyEscapeErrorRate != 0.5 {
		t.Fatalf("Gateway.AdvancedScheduler.StickyEscapeErrorRate = %v, want 0.5", cfg.Scheduling.Advanced.StickyEscapeErrorRate)
	}
	if cfg.Scheduling.Advanced.EWMAErrorRateAlpha != 0.2 {
		t.Fatalf("Gateway.AdvancedScheduler.EWMAErrorRateAlpha = %v, want 0.2", cfg.Scheduling.Advanced.EWMAErrorRateAlpha)
	}
	if cfg.Scheduling.Advanced.EWMATTFTAlpha != 0.2 {
		t.Fatalf("Gateway.AdvancedScheduler.EWMATTFTAlpha = %v, want 0.2", cfg.Scheduling.Advanced.EWMATTFTAlpha)
	}
	if !cfg.Scheduling.SessionAffinity.ReadLegacyHash {
		t.Fatalf("Gateway.OpenAIWS.SessionHashReadOldFallback = false, want true")
	}
	if !cfg.Scheduling.SessionAffinity.DualWriteLegacyHash {
		t.Fatalf("Gateway.OpenAIWS.SessionHashDualWriteOld = false, want true")
	}
	if cfg.Scheduling.SessionAffinity.ResponseIDTTLSeconds != 3600 {
		t.Fatalf("Gateway.OpenAIWS.StickyResponseIDTTLSeconds = %d, want 3600", cfg.Scheduling.SessionAffinity.ResponseIDTTLSeconds)
	}

	if cfg.Gateway.OpenAI.ResponsesWS.PrewarmCooldownMS != 300 {
		t.Fatalf("Gateway.OpenAIWS.PrewarmCooldownMS = %d, want 300", cfg.Gateway.OpenAI.ResponsesWS.PrewarmCooldownMS)
	}
	if cfg.Gateway.OpenAI.ResponsesWS.ClientReadLimitBytes != 64*1024*1024 {
		t.Fatalf("Gateway.OpenAIWS.ClientReadLimitBytes = %d, want %d", cfg.Gateway.OpenAI.ResponsesWS.ClientReadLimitBytes, 64*1024*1024)
	}

	if cfg.Gateway.OpenAI.ResponsesWS.HTTPBridgeThresholdBytes != 15*1024*1024 {
		t.Fatalf("Gateway.OpenAIWS.HTTPBridgeThresholdBytes = %d, want %d", cfg.Gateway.OpenAI.ResponsesWS.HTTPBridgeThresholdBytes, 15*1024*1024)
	}

	if cfg.Scheduling.Advanced.ScoreWeights.QuotaHeadroom != 0 {
		t.Fatalf("Gateway.AdvancedScheduler.ScoreWeights.QuotaHeadroom = %v, want 0", cfg.Scheduling.Advanced.ScoreWeights.QuotaHeadroom)
	}

	if cfg.Gateway.OpenAI.ResponsesWS.ClientFirstMessageTimeoutSeconds != DefaultOpenAIWSClientFirstMessageTimeoutSeconds {
		t.Fatalf(
			"Gateway.OpenAIWS.ClientFirstMessageTimeoutSeconds = %d, want %d",
			cfg.Gateway.OpenAI.ResponsesWS.ClientFirstMessageTimeoutSeconds,
			DefaultOpenAIWSClientFirstMessageTimeoutSeconds,
		)
	}
	if cfg.Gateway.OpenAI.ResponsesWS.IngressInterTurnIdleTimeoutSeconds != 300 {
		t.Fatalf("Gateway.OpenAIWS.IngressInterTurnIdleTimeoutSeconds = %d, want 300", cfg.Gateway.OpenAI.ResponsesWS.IngressInterTurnIdleTimeoutSeconds)
	}
	if cfg.Gateway.OpenAI.ResponsesWS.MaxIngressConnectionsPerAPIKey != 64 {
		t.Fatalf("Gateway.OpenAIWS.MaxIngressConnectionsPerAPIKey = %d, want 64", cfg.Gateway.OpenAI.ResponsesWS.MaxIngressConnectionsPerAPIKey)
	}
}

// TestLoadIgnoresRemovedOpenAIWSSwitch supplies the retired force_http
// environment setting. Loading must still succeed with the default provider
// connection limit, showing that this removed field does not replace that
// limit.
func TestLoadIgnoresRemovedOpenAIWSSwitch(t *testing.T) {
	fixture := newConfigFixture(t)
	fixture.Setenv("GATEWAY_OPENAI_WS_FORCE_HTTP", "true")

	cfg, err := fixture.Load()
	require.NoError(t, err)
	require.Equal(t, 128, cfg.Gateway.OpenAI.ResponsesWS.MaxConnsPerProvider)
}

// TestLoadDefaultOpenAIHTTP2Enabled reads the default transport preference and
// proxy-stream isolation settings. HTTP/2 preference, permitted HTTP/1
// recovery, and the expected enabled circuit thresholds must remain configured.
func TestLoadDefaultOpenAIHTTP2Enabled(t *testing.T) {
	fixture := newConfigFixture(t)

	cfg, err := fixture.Load()
	require.NoError(t, err)
	require.True(t, cfg.Gateway.OpenAI.HTTP2.Enabled)
	require.True(t, cfg.Gateway.OpenAI.HTTP2.AllowProxyFallbackToHTTP1)
	require.True(t, cfg.Gateway.OpenAI.ProxyStreamCircuit.Enabled)
	require.Equal(t, 2, cfg.Gateway.OpenAI.ProxyStreamCircuit.FailureThreshold)
	require.Equal(t, 60, cfg.Gateway.OpenAI.ProxyStreamCircuit.WindowSeconds)
	require.Equal(t, 600, cfg.Gateway.OpenAI.ProxyStreamCircuit.TTLSeconds)
}

// TestLoadDefaultOpenAIResponseHeaderTimeoutUnlimited reads OpenAI header-wait
// settings from default configuration. The field must remain zero, keeping its
// disabled timeout distinguishable from the generic gateway header budget.
func TestLoadDefaultOpenAIResponseHeaderTimeoutUnlimited(t *testing.T) {
	fixture := newConfigFixture(t)

	cfg, err := fixture.Load()
	require.NoError(t, err)
	require.Equal(t, 0, cfg.Gateway.OpenAI.ResponseHeaderTimeoutSeconds)
}

// TestLoadOpenAIWSStickyTTLCompatibility supplies zero for the current
// response-ID lifetime and a positive retired lifetime. Loading must use the
// retired 7200-second value to fill the nonpositive current field.
func TestLoadOpenAIWSStickyTTLCompatibility(t *testing.T) {
	fixture := newConfigFixture(t)
	fixture.Setenv("GATEWAY_OPENAI_WS_STICKY_RESPONSE_ID_TTL_SECONDS", "0")
	fixture.Setenv("GATEWAY_OPENAI_WS_STICKY_PREVIOUS_RESPONSE_TTL_SECONDS", "7200")

	cfg, err := fixture.Load()
	if err != nil {
		t.Fatalf("fixture.Load() error: %v", err)
	}

	if cfg.Scheduling.SessionAffinity.ResponseIDTTLSeconds != 7200 {
		t.Fatalf("StickyResponseIDTTLSeconds = %d, want 7200", cfg.Scheduling.SessionAffinity.ResponseIDTTLSeconds)
	}
}

// TestLoadDefaultIdempotencyConfig reads the default missing-key observation
// flag and record retention values. Loading must enable observation and retain
// distinct ordinary and system-operation lifetimes.
func TestLoadDefaultIdempotencyConfig(t *testing.T) {
	fixture := newConfigFixture(t)

	cfg, err := fixture.Load()
	if err != nil {
		t.Fatalf("fixture.Load() error: %v", err)
	}

	if !cfg.Idempotency.ObserveOnly {
		t.Fatalf("Idempotency.ObserveOnly = false, want true")
	}
	if cfg.Idempotency.DefaultTTLSeconds != 86400 {
		t.Fatalf("Idempotency.DefaultTTLSeconds = %d, want 86400", cfg.Idempotency.DefaultTTLSeconds)
	}
	if cfg.Idempotency.SystemOperationTTLSeconds != 3600 {
		t.Fatalf("Idempotency.SystemOperationTTLSeconds = %d, want 3600", cfg.Idempotency.SystemOperationTTLSeconds)
	}
}

// TestLoadBatchImageRetiredSettings supplies obsolete flags and unused limits
// through both supported configuration sources. Loading ignores those values
// while retaining the active queue defaults and validating configured Vertex storage.
func TestLoadBatchImageRetiredSettings(t *testing.T) {
	for _, enabled := range []string{"false", "true"} {
		t.Run(enabled, func(t *testing.T) {
			fixture := newConfigFixture(t)
			configFile := filepath.Join(t.TempDir(), "config.yaml")
			content := "batch_image:\n  enabled: " + enabled + "\n  queue_enabled: " + enabled + "\n  vertex_enabled: " + enabled + "\n  max_items_per_job_trial: -1\n  output_retention_max_days: -1\n  idempotency_key_prefix: unused\n  vertex_input_retention_hours: -1\n  vertex_output_retention_hours: -1\n"
			require.NoError(t, os.WriteFile(configFile, []byte(content), 0o600))
			fixture.Setenv("CONFIG_FILE", configFile)
			fixture.Setenv("BATCH_IMAGE_ENABLED", enabled)
			fixture.Setenv("BATCH_IMAGE_QUEUE_ENABLED", enabled)
			fixture.Setenv("BATCH_IMAGE_VERTEX_ENABLED", enabled)
			cfg, err := fixture.Load()
			require.NoError(t, err)
			require.NotEmpty(t, cfg.BatchImage.Queue.ReadyKey)
			require.Positive(t, cfg.BatchImage.Queue.JobLockTTLSeconds)
			require.Empty(t, cfg.BatchImage.Vertex.ManagedGCSBucket)
			cfg.BatchImage.Vertex.ManagedGCSBucket = "gs://invalid-bucket"
			require.ErrorContains(t, cfg.Validate(), "batch_image.vertex.managed_gcs_bucket")
			cfg.BatchImage.Vertex.ManagedGCSBucket = "managed-bucket"
			cfg.BatchImage.Vertex.ManagedGCSPrefix = "shared-prefix"
			require.ErrorContains(t, cfg.Validate(), "batch_image.vertex.managed_gcs_prefix")
		})
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

// TestLoadForcedCodexInstructionsTemplate supplies a YAML template path whose
// file contains an existing-instructions placeholder. Loading must retain the
// selected path and cache the complete file text for request-time rendering.
func TestLoadForcedCodexInstructionsTemplate(t *testing.T) {
	fixture := newConfigFixture(t)

	tempDir := t.TempDir()
	templatePath := filepath.Join(tempDir, "codex-instructions.md.tmpl")
	configPath := filepath.Join(tempDir, "config.yaml")

	require.NoError(t, os.WriteFile(templatePath, []byte("server-prefix\n\n{{ .ExistingInstructions }}"), 0o644))
	yamlSafePath := filepath.ToSlash(templatePath)
	require.NoError(t, os.WriteFile(configPath, []byte("gateway:\n  forced_codex_instructions_template_file: \""+yamlSafePath+"\"\n"), 0o644))
	fixture.Setenv("DATA_DIR", tempDir)

	cfg, err := fixture.Load()
	require.NoError(t, err)
	require.Equal(t, yamlSafePath, cfg.Gateway.OpenAI.Codex.ForcedInstructionsTemplateFile)
	require.Equal(t, "server-prefix\n\n{{ .ExistingInstructions }}", cfg.Gateway.OpenAI.Codex.ForcedInstructionsTemplate)
}

// TestLoadDefaultSecurityToggles inspects URL allowlist switches, configured
// supplier hosts, and response-header filtering. Loading must expose the
// expected disabled allowlist, permitted private/HTTP options, host entries,
// and enabled header filter.
func TestLoadDefaultSecurityToggles(t *testing.T) {
	fixture := newConfigFixture(t)

	cfg, err := fixture.Load()
	if err != nil {
		t.Fatalf("fixture.Load() error: %v", err)
	}

	if cfg.Security.URLAllowlist.Enabled {
		t.Fatalf("URLAllowlist.Enabled = true, want false")
	}
	if !cfg.Security.URLAllowlist.AllowInsecureHTTP {
		t.Fatalf("URLAllowlist.AllowInsecureHTTP = false, want true")
	}
	if !cfg.Security.URLAllowlist.AllowPrivateHosts {
		t.Fatalf("URLAllowlist.AllowPrivateHosts = false, want true")
	}
	// Default upstream hosts include the Chinese suppliers' official endpoints.
	for _, host := range []string{
		"api.kimi.com",
		"api.moonshot.ai",
		"api.moonshot.cn",
		"open.bigmodel.cn",
		"api.z.ai",
		"api.deepseek.com",
	} {
		require.Contains(t, cfg.Security.URLAllowlist.UpstreamHosts, host)
	}
	if !cfg.Gateway.ResponseHeaders.Enabled {
		t.Fatalf("ResponseHeaders.Enabled = false, want true")
	}
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

// TestLoadDefaultDatabaseSSLMode reads the normal startup PostgreSQL settings.
// The TLS mode must default to prefer, keeping this loader default observable
// separately from the installer's disable default.
func TestLoadDefaultDatabaseSSLMode(t *testing.T) {
	fixture := newConfigFixture(t)

	cfg, err := fixture.Load()
	if err != nil {
		t.Fatalf("fixture.Load() error: %v", err)
	}

	if cfg.Database.SSLMode != "prefer" {
		t.Fatalf("Database.SSLMode = %q, want %q", cfg.Database.SSLMode, "prefer")
	}
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

// TestLoadDefaultDashboardCacheConfig reads the enabled flag, Redis key prefix,
// freshness lifetime, total lifetime, and refresh budget. Default loading must
// return the exact dashboard values used by the fixture.
func TestLoadDefaultDashboardCacheConfig(t *testing.T) {
	fixture := newConfigFixture(t)

	cfg, err := fixture.Load()
	if err != nil {
		t.Fatalf("fixture.Load() error: %v", err)
	}

	if !cfg.Usage.DashboardCache.Enabled {
		t.Fatalf("Dashboard.Enabled = false, want true")
	}
	if cfg.Usage.DashboardCache.KeyPrefix != "tokenrouter:" {
		t.Fatalf("Dashboard.KeyPrefix = %q, want %q", cfg.Usage.DashboardCache.KeyPrefix, "tokenrouter:")
	}
	if cfg.Usage.DashboardCache.StatsFreshTTLSeconds != 15 {
		t.Fatalf("Dashboard.StatsFreshTTLSeconds = %d, want 15", cfg.Usage.DashboardCache.StatsFreshTTLSeconds)
	}
	if cfg.Usage.DashboardCache.StatsTTLSeconds != 30 {
		t.Fatalf("Dashboard.StatsTTLSeconds = %d, want 30", cfg.Usage.DashboardCache.StatsTTLSeconds)
	}
	if cfg.Usage.DashboardCache.StatsRefreshTimeoutSeconds != 30 {
		t.Fatalf("Dashboard.StatsRefreshTimeoutSeconds = %d, want 30", cfg.Usage.DashboardCache.StatsRefreshTimeoutSeconds)
	}
}

// TestValidateDashboardCacheConfigEnabled sets freshness longer than the total
// cache lifetime on an enabled configuration. Validation must reject that
// relationship and name the freshness field in its error.
func TestValidateDashboardCacheConfigEnabled(t *testing.T) {
	fixture := newConfigFixture(t)

	cfg, err := fixture.Load()
	if err != nil {
		t.Fatalf("fixture.Load() error: %v", err)
	}

	cfg.Usage.DashboardCache.Enabled = true
	cfg.Usage.DashboardCache.StatsFreshTTLSeconds = 10
	cfg.Usage.DashboardCache.StatsTTLSeconds = 5
	err = cfg.Validate()
	if err == nil {
		t.Fatalf("Validate() expected error for stats_fresh_ttl_seconds > stats_ttl_seconds, got nil")
	}
	if !strings.Contains(err.Error(), "usage.dashboard_cache.stats_fresh_ttl_seconds") {
		t.Fatalf("Validate() expected stats_fresh_ttl_seconds error, got: %v", err)
	}
}

// TestValidateDashboardCacheConfigDisabled disables caching while supplying a
// negative total lifetime. Validation must still reject the negative field,
// retaining numeric checks even when the feature is disabled.
func TestValidateDashboardCacheConfigDisabled(t *testing.T) {
	fixture := newConfigFixture(t)

	cfg, err := fixture.Load()
	if err != nil {
		t.Fatalf("fixture.Load() error: %v", err)
	}

	cfg.Usage.DashboardCache.Enabled = false
	cfg.Usage.DashboardCache.StatsTTLSeconds = -1
	err = cfg.Validate()
	if err == nil {
		t.Fatalf("Validate() expected error for negative stats_ttl_seconds, got nil")
	}
	if !strings.Contains(err.Error(), "usage.dashboard_cache.stats_ttl_seconds") {
		t.Fatalf("Validate() expected stats_ttl_seconds error, got: %v", err)
	}
}

// TestLoadDefaultDashboardAggregationConfig reads polling, lookback, backfill,
// and raw and aggregate retention defaults. Loading must retain the expected
// values, with aggregation enabled and full backfill disabled.
func TestLoadDefaultDashboardAggregationConfig(t *testing.T) {
	fixture := newConfigFixture(t)

	cfg, err := fixture.Load()
	if err != nil {
		t.Fatalf("fixture.Load() error: %v", err)
	}

	if !cfg.Usage.Aggregation.Enabled {
		t.Fatalf("DashboardAgg.Enabled = false, want true")
	}
	if cfg.Usage.Aggregation.IntervalSeconds != 60 {
		t.Fatalf("DashboardAgg.IntervalSeconds = %d, want 60", cfg.Usage.Aggregation.IntervalSeconds)
	}
	if cfg.Usage.Aggregation.LookbackSeconds != 120 {
		t.Fatalf("DashboardAgg.LookbackSeconds = %d, want 120", cfg.Usage.Aggregation.LookbackSeconds)
	}
	if cfg.Usage.Aggregation.BackfillEnabled {
		t.Fatalf("DashboardAgg.BackfillEnabled = true, want false")
	}
	if cfg.Usage.Aggregation.BackfillMaxDays != 31 {
		t.Fatalf("DashboardAgg.BackfillMaxDays = %d, want 31", cfg.Usage.Aggregation.BackfillMaxDays)
	}
	if cfg.Usage.Retention.LogsDays != 90 {
		t.Fatalf("DashboardAgg.Retention.UsageLogsDays = %d, want 90", cfg.Usage.Retention.LogsDays)
	}
	if cfg.Usage.Retention.BillingDedupDays != 365 {
		t.Fatalf("DashboardAgg.Retention.UsageBillingDedupDays = %d, want 365", cfg.Usage.Retention.BillingDedupDays)
	}
	if cfg.Usage.Aggregation.HourlyRetentionDays != 180 {
		t.Fatalf("DashboardAgg.Retention.HourlyDays = %d, want 180", cfg.Usage.Aggregation.HourlyRetentionDays)
	}
	if cfg.Usage.Aggregation.DailyRetentionDays != 730 {
		t.Fatalf("DashboardAgg.Retention.DailyDays = %d, want 730", cfg.Usage.Aggregation.DailyRetentionDays)
	}
	if cfg.Usage.Aggregation.RecomputeDays != 2 {
		t.Fatalf("DashboardAgg.RecomputeDays = %d, want 2", cfg.Usage.Aggregation.RecomputeDays)
	}
}

// TestValidateDashboardAggregationConfigDisabled disables aggregation and
// supplies a negative polling interval. Validation must reject the negative
// duration and identify that configuration field despite the disabled job.
func TestValidateDashboardAggregationConfigDisabled(t *testing.T) {
	fixture := newConfigFixture(t)

	cfg, err := fixture.Load()
	if err != nil {
		t.Fatalf("fixture.Load() error: %v", err)
	}

	cfg.Usage.Aggregation.Enabled = false
	cfg.Usage.Aggregation.IntervalSeconds = -1
	err = cfg.Validate()
	if err == nil {
		t.Fatalf("Validate() expected error for negative usage.aggregation.interval_seconds, got nil")
	}
	if !strings.Contains(err.Error(), "usage.aggregation.interval_seconds") {
		t.Fatalf("Validate() expected interval_seconds error, got: %v", err)
	}
}

// TestValidateDashboardAggregationBackfillMaxDays enables full backfill with a
// zero permitted day range. Validation must reject that relationship and
// identify the backfill range before a backfill can be configured.
func TestValidateDashboardAggregationBackfillMaxDays(t *testing.T) {
	fixture := newConfigFixture(t)

	cfg, err := fixture.Load()
	if err != nil {
		t.Fatalf("fixture.Load() error: %v", err)
	}

	cfg.Usage.Aggregation.BackfillEnabled = true
	cfg.Usage.Aggregation.BackfillMaxDays = 0
	err = cfg.Validate()
	if err == nil {
		t.Fatalf("Validate() expected error for usage.aggregation.backfill_max_days, got nil")
	}
	if !strings.Contains(err.Error(), "usage.aggregation.backfill_max_days") {
		t.Fatalf("Validate() expected backfill_max_days error, got: %v", err)
	}
}

// TestLoadDefaultUsageCleanupConfig inspects enabled state, range, batch size,
// polling interval, and task budget. Loading must retain the expected cleanup
// defaults, including its thirty-minute task timeout.
func TestLoadDefaultUsageCleanupConfig(t *testing.T) {
	fixture := newConfigFixture(t)

	cfg, err := fixture.Load()
	if err != nil {
		t.Fatalf("fixture.Load() error: %v", err)
	}

	if !cfg.Usage.CleanupTasks.Enabled {
		t.Fatalf("UsageCleanup.Enabled = false, want true")
	}
	if cfg.Usage.CleanupTasks.MaxRangeDays != 31 {
		t.Fatalf("UsageCleanup.MaxRangeDays = %d, want 31", cfg.Usage.CleanupTasks.MaxRangeDays)
	}
	if cfg.Usage.CleanupTasks.BatchSize != 5000 {
		t.Fatalf("UsageCleanup.BatchSize = %d, want 5000", cfg.Usage.CleanupTasks.BatchSize)
	}
	if cfg.Usage.CleanupTasks.WorkerIntervalSeconds != 10 {
		t.Fatalf("UsageCleanup.WorkerIntervalSeconds = %d, want 10", cfg.Usage.CleanupTasks.WorkerIntervalSeconds)
	}
	if cfg.Usage.CleanupTasks.TaskTimeoutSeconds != 1800 {
		t.Fatalf("UsageCleanup.TaskTimeoutSeconds = %d, want 1800", cfg.Usage.CleanupTasks.TaskTimeoutSeconds)
	}
}

// TestValidateUsageCleanupConfigEnabled enables cleanup with a zero allowed
// date range. Validation must reject that value and identify the range field
// required for an active cleanup executor.
func TestValidateUsageCleanupConfigEnabled(t *testing.T) {
	fixture := newConfigFixture(t)

	cfg, err := fixture.Load()
	if err != nil {
		t.Fatalf("fixture.Load() error: %v", err)
	}

	cfg.Usage.CleanupTasks.Enabled = true
	cfg.Usage.CleanupTasks.MaxRangeDays = 0
	err = cfg.Validate()
	if err == nil {
		t.Fatalf("Validate() expected error for usage.cleanup_tasks.max_range_days, got nil")
	}
	if !strings.Contains(err.Error(), "usage.cleanup_tasks.max_range_days") {
		t.Fatalf("Validate() expected max_range_days error, got: %v", err)
	}
}

// TestValidateUsageCleanupConfigDisabled leaves cleanup disabled while
// supplying a negative batch size. Validation must reject the negative value
// and identify its field for the inactive configuration as well.
func TestValidateUsageCleanupConfigDisabled(t *testing.T) {
	fixture := newConfigFixture(t)

	cfg, err := fixture.Load()
	if err != nil {
		t.Fatalf("fixture.Load() error: %v", err)
	}

	cfg.Usage.CleanupTasks.Enabled = false
	cfg.Usage.CleanupTasks.BatchSize = -1
	err = cfg.Validate()
	if err == nil {
		t.Fatalf("Validate() expected error for usage.cleanup_tasks.batch_size, got nil")
	}
	if !strings.Contains(err.Error(), "usage.cleanup_tasks.batch_size") {
		t.Fatalf("Validate() expected batch_size error, got: %v", err)
	}
}

// TestConfigAddressHelpers constructs server, Redis, and PostgreSQL settings
// with empty and populated passwords and zones. Address text and DSN fragments
// must reflect the selected port, password omission, and timezone behavior.
func TestConfigAddressHelpers(t *testing.T) {
	server := ServerConfig{Host: "127.0.0.1", Port: 9000}
	if server.Address() != "127.0.0.1:9000" {
		t.Fatalf("ServerConfig.Address() = %q", server.Address())
	}

	dbCfg := DatabaseConfig{
		Host:     "localhost",
		Port:     5432,
		User:     "postgres",
		Password: "",
		DBName:   "tokenrouter",
		SSLMode:  "disable",
	}
	if !strings.Contains(dbCfg.DSN(), "password=") {
	} else {
		t.Fatalf("DatabaseConfig.DSN() should not include password when empty")
	}

	dbCfg.Password = "secret"
	if !strings.Contains(dbCfg.DSN(), "password=secret") {
		t.Fatalf("DatabaseConfig.DSN() missing password")
	}

	dbCfg.Password = ""
	if strings.Contains(dbCfg.DSNWithTimezone("UTC"), "password=") {
		t.Fatalf("DatabaseConfig.DSNWithTimezone() should omit password when empty")
	}

	if !strings.Contains(dbCfg.DSNWithTimezone(""), "TimeZone=Asia/Shanghai") {
		t.Fatalf("DatabaseConfig.DSNWithTimezone() should use default timezone")
	}
	if !strings.Contains(dbCfg.DSNWithTimezone("UTC"), "TimeZone=UTC") {
		t.Fatalf("DatabaseConfig.DSNWithTimezone() should use provided timezone")
	}

	redis := RedisConfig{Host: "redis", Port: 6379}
	if redis.Address() != "redis:6379" {
		t.Fatalf("RedisConfig.Address() = %q", redis.Address())
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

// TestValidateOpsCleanupScheduleRequired enables Ops cleanup with an empty
// schedule string. Validation must reject the configuration and identify the
// schedule field required before periodic cleanup can be configured.
func TestValidateOpsCleanupScheduleRequired(t *testing.T) {
	fixture := newConfigFixture(t)

	cfg, err := fixture.Load()
	if err != nil {
		t.Fatalf("fixture.Load() error: %v", err)
	}
	cfg.Ops.Cleanup.Enabled = true
	cfg.Ops.Cleanup.Schedule = ""
	err = cfg.Validate()
	if err == nil {
		t.Fatalf("Validate() expected error for ops.cleanup.schedule")
	}
	if !strings.Contains(err.Error(), "ops.cleanup.schedule") {
		t.Fatalf("Validate() expected ops.cleanup.schedule error, got: %v", err)
	}
}

// TestValidateConcurrencyPingInterval supplies a three-second queue keepalive
// interval on otherwise valid settings. Validation must reject the value and
// name the concurrency ping field in its error.
func TestValidateConcurrencyPingInterval(t *testing.T) {
	fixture := newConfigFixture(t)

	cfg, err := fixture.Load()
	if err != nil {
		t.Fatalf("fixture.Load() error: %v", err)
	}
	cfg.Gateway.Streaming.WaitPingIntervalSeconds = 3
	err = cfg.Validate()
	if err == nil {
		t.Fatalf("Validate() expected error for gateway.streaming.wait_ping_interval_seconds")
	}
	if !strings.Contains(err.Error(), "gateway.streaming.wait_ping_interval_seconds") {
		t.Fatalf("Validate() expected gateway.streaming.wait_ping_interval_seconds error, got: %v", err)
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

// TestDatabaseDSNWithTimezone_WithPassword supplies PostgreSQL credentials and
// an explicit UTC timezone. The resulting DSN text must contain the password
// and zone parameters, protecting both populated-field formatting paths.
func TestDatabaseDSNWithTimezone_WithPassword(t *testing.T) {
	d := &DatabaseConfig{
		Host:     "localhost",
		Port:     5432,
		User:     "u",
		Password: "p",
		DBName:   "db",
		SSLMode:  "prefer",
	}
	got := d.DSNWithTimezone("UTC")
	if !strings.Contains(got, "password=p") {
		t.Fatalf("DSNWithTimezone should include password: %q", got)
	}
	if !strings.Contains(got, "TimeZone=UTC") {
		t.Fatalf("DSNWithTimezone should include TimeZone=UTC: %q", got)
	}
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

// TestValidateConfigErrors starts each case from loaded defaults and mutates
// one field or a coupled setting relationship. Validation must return an error
// containing the named field fragment for each supplied invalid configuration.
func TestValidateConfigErrors(t *testing.T) {
	buildValid := func(t *testing.T) *Config {
		t.Helper()
		fixture := newConfigFixture(t)
		cfg, err := fixture.Load()
		if err != nil {
			t.Fatalf("fixture.Load() error: %v", err)
		}
		return cfg
	}

	cases := []struct {
		name    string        // subtest display name
		mutate  func(*Config) // fixture configuration mutation callback
		wantErr string        // expected validation error fragment
	}{
		{
			name:    "server read header timeout",
			mutate:  func(c *Config) { c.Server.ReadHeaderTimeoutSeconds = 0 },
			wantErr: "server.read_header_timeout_seconds",
		},
		{
			name:    "server max header bytes too small",
			mutate:  func(c *Config) { c.Server.MaxHeaderBytes = 4096 },
			wantErr: "server.max_header_bytes",
		},
		{
			name:    "server max request body size",
			mutate:  func(c *Config) { c.Server.MaxRequestBodyBytes = -1 },
			wantErr: "server.max_request_body_bytes",
		},
		{
			name: "h2c zero concurrent streams",
			mutate: func(c *Config) {
				c.Server.H2C.Enabled = true
				c.Server.H2C.MaxConcurrentStreams = 0
			},
			wantErr: "server.h2c.max_concurrent_streams",
		},
		{
			name: "h2c oversized read frame",
			mutate: func(c *Config) {
				c.Server.H2C.Enabled = true
				c.Server.H2C.MaxReadFrameBytes = 16 * 1024 * 1024
			},
			wantErr: "server.h2c.max_read_frame_bytes",
		},
		{
			name: "invalid auth abuse threshold too small",
			mutate: func(c *Config) {
				c.APIKey.InvalidKeyAbuse.Threshold = 9
			},
			wantErr: "api_key.invalid_key_abuse.threshold",
		},
		{
			name: "invalid auth abuse capacity too small",
			mutate: func(c *Config) {
				c.APIKey.InvalidKeyAbuse.Capacity = 255
			},
			wantErr: "api_key.invalid_key_abuse.capacity",
		},
		{
			name:    "jwt secret required",
			mutate:  func(c *Config) { c.Auth.JWT.Secret = "" },
			wantErr: "auth.jwt.secret is required",
		},
		{
			name:    "jwt secret min bytes",
			mutate:  func(c *Config) { c.Auth.JWT.Secret = strings.Repeat("a", 31) },
			wantErr: "auth.jwt.secret must be at least 32 bytes",
		},
		{
			name:    "jwt expire hour positive",
			mutate:  func(c *Config) { c.Auth.JWT.AccessTokenTTLMinutes = 0 },
			wantErr: "auth.jwt.access_token_ttl_minutes must be positive",
		},
		{
			name:    "jwt access token expire minutes non-negative",
			mutate:  func(c *Config) { c.Auth.JWT.AccessTokenTTLMinutes = -1 },
			wantErr: "auth.jwt.access_token_ttl_minutes must be positive",
		},
		{
			name:    "csp policy required",
			mutate:  func(c *Config) { c.Security.CSP.Enabled = true; c.Security.CSP.Policy = "" },
			wantErr: "security.csp.policy",
		},
		{
			name: "linuxdo client id required",
			mutate: func(c *Config) {
				c.Auth.OAuth.LinuxDo.Enabled = true
				c.Auth.OAuth.LinuxDo.UsePKCE = true
				c.Auth.OAuth.LinuxDo.ClientID = ""
			},
			wantErr: "auth.oauth.linuxdo.client_id",
		},
		{
			name: "linuxdo token auth method",
			mutate: func(c *Config) {
				c.Auth.OAuth.LinuxDo.Enabled = true
				c.Auth.OAuth.LinuxDo.UsePKCE = true
				c.Auth.OAuth.LinuxDo.ClientID = "client"
				c.Auth.OAuth.LinuxDo.ClientSecret = "secret"
				c.Auth.OAuth.LinuxDo.AuthorizeURL = "https://example.com/authorize"
				c.Auth.OAuth.LinuxDo.TokenURL = "https://example.com/token"
				c.Auth.OAuth.LinuxDo.UserInfoURL = "https://example.com/userinfo"
				c.Auth.OAuth.LinuxDo.RedirectURL = "https://example.com/callback"
				c.Auth.OAuth.LinuxDo.FrontendRedirectURL = "/auth/callback"
				c.Auth.OAuth.LinuxDo.TokenAuthMethod = "invalid"
			},
			wantErr: "auth.oauth.linuxdo.token_auth_method",
		},
		{
			name:    "billing circuit breaker threshold",
			mutate:  func(c *Config) { c.Billing.BalanceCheckCircuitBreaker.FailureThreshold = 0 },
			wantErr: "billing.balance_check_circuit_breaker.failure_threshold",
		},
		{
			name:    "billing circuit breaker reset",
			mutate:  func(c *Config) { c.Billing.BalanceCheckCircuitBreaker.ResetTimeoutSeconds = 0 },
			wantErr: "billing.balance_check_circuit_breaker.reset_timeout_seconds",
		},
		{
			name:    "billing circuit breaker half open",
			mutate:  func(c *Config) { c.Billing.BalanceCheckCircuitBreaker.HalfOpenRequests = 0 },
			wantErr: "billing.balance_check_circuit_breaker.half_open_requests",
		},
		{
			name:    "billing minimum balance reserve",
			mutate:  func(c *Config) { c.Billing.MinimumBalanceReserve = -0.01 },
			wantErr: "billing.minimum_balance_reserve",
		},
		{
			name:    "database max open conns",
			mutate:  func(c *Config) { c.Database.MaxOpenConns = 0 },
			wantErr: "database.max_open_conns",
		},
		{
			name:    "database max lifetime",
			mutate:  func(c *Config) { c.Database.ConnMaxLifetimeMinutes = -1 },
			wantErr: "database.conn_max_lifetime_minutes",
		},
		{
			name:    "database idle exceeds open",
			mutate:  func(c *Config) { c.Database.MaxIdleConns = c.Database.MaxOpenConns + 1 },
			wantErr: "database.max_idle_conns cannot exceed",
		},
		{
			name:    "redis dial timeout",
			mutate:  func(c *Config) { c.Redis.DialTimeoutSeconds = 0 },
			wantErr: "redis.dial_timeout_seconds",
		},
		{
			name:    "redis read timeout",
			mutate:  func(c *Config) { c.Redis.ReadTimeoutSeconds = 0 },
			wantErr: "redis.read_timeout_seconds",
		},
		{
			name:    "redis write timeout",
			mutate:  func(c *Config) { c.Redis.WriteTimeoutSeconds = 0 },
			wantErr: "redis.write_timeout_seconds",
		},
		{
			name:    "redis pool size",
			mutate:  func(c *Config) { c.Redis.PoolSize = 0 },
			wantErr: "redis.pool_size",
		},
		{
			name:    "redis idle exceeds pool",
			mutate:  func(c *Config) { c.Redis.MinIdleConns = c.Redis.PoolSize + 1 },
			wantErr: "redis.min_idle_conns cannot exceed",
		},
		{
			name:    "dashboard cache disabled negative",
			mutate:  func(c *Config) { c.Usage.DashboardCache.Enabled = false; c.Usage.DashboardCache.StatsTTLSeconds = -1 },
			wantErr: "usage.dashboard_cache.stats_ttl_seconds",
		},
		{
			name: "dashboard cache fresh ttl positive",
			mutate: func(c *Config) {
				c.Usage.DashboardCache.Enabled = true
				c.Usage.DashboardCache.StatsFreshTTLSeconds = 0
			},
			wantErr: "usage.dashboard_cache.stats_fresh_ttl_seconds",
		},
		{
			name:    "dashboard aggregation enabled interval",
			mutate:  func(c *Config) { c.Usage.Aggregation.Enabled = true; c.Usage.Aggregation.IntervalSeconds = 0 },
			wantErr: "usage.aggregation.interval_seconds",
		},
		{
			name: "dashboard aggregation backfill positive",
			mutate: func(c *Config) {
				c.Usage.Aggregation.Enabled = true
				c.Usage.Aggregation.BackfillEnabled = true
				c.Usage.Aggregation.BackfillMaxDays = 0
			},
			wantErr: "usage.aggregation.backfill_max_days",
		},
		{
			name:    "dashboard aggregation retention",
			mutate:  func(c *Config) { c.Usage.Aggregation.Enabled = true; c.Usage.Retention.LogsDays = 0 },
			wantErr: "usage.retention.logs_days",
		},
		{
			name: "dashboard aggregation dedup retention",
			mutate: func(c *Config) {
				c.Usage.Aggregation.Enabled = true
				c.Usage.Retention.BillingDedupDays = 0
			},
			wantErr: "usage.retention.billing_dedup_days",
		},
		{
			name: "dashboard aggregation dedup retention smaller than usage logs",
			mutate: func(c *Config) {
				c.Usage.Aggregation.Enabled = true
				c.Usage.Retention.LogsDays = 30
				c.Usage.Retention.BillingDedupDays = 29
			},
			wantErr: "usage.retention.billing_dedup_days",
		},
		{
			name:    "dashboard aggregation disabled interval",
			mutate:  func(c *Config) { c.Usage.Aggregation.Enabled = false; c.Usage.Aggregation.IntervalSeconds = -1 },
			wantErr: "usage.aggregation.interval_seconds",
		},
		{
			name:    "usage cleanup max range",
			mutate:  func(c *Config) { c.Usage.CleanupTasks.Enabled = true; c.Usage.CleanupTasks.MaxRangeDays = 0 },
			wantErr: "usage.cleanup_tasks.max_range_days",
		},
		{
			name:    "usage cleanup worker interval",
			mutate:  func(c *Config) { c.Usage.CleanupTasks.Enabled = true; c.Usage.CleanupTasks.WorkerIntervalSeconds = 0 },
			wantErr: "usage.cleanup_tasks.worker_interval_seconds",
		},
		{
			name:    "usage cleanup batch size",
			mutate:  func(c *Config) { c.Usage.CleanupTasks.Enabled = true; c.Usage.CleanupTasks.BatchSize = 0 },
			wantErr: "usage.cleanup_tasks.batch_size",
		},
		{
			name:    "usage cleanup disabled negative",
			mutate:  func(c *Config) { c.Usage.CleanupTasks.Enabled = false; c.Usage.CleanupTasks.BatchSize = -1 },
			wantErr: "usage.cleanup_tasks.batch_size",
		},
		{
			name:    "gateway max body size",
			mutate:  func(c *Config) { c.Gateway.MaxRequestBodyBytes = 0 },
			wantErr: "gateway.max_request_body_bytes",
		},
		{
			name:    "gateway text body exceeds media body",
			mutate:  func(c *Config) { c.Gateway.TextMaxRequestBodyBytes = c.Gateway.MaxRequestBodyBytes + 1 },
			wantErr: "gateway.text_max_request_body_bytes",
		},
		{
			name:    "gateway models list read limit",
			mutate:  func(c *Config) { c.Gateway.ModelsListResponseMaxBytes = 0 },
			wantErr: "gateway.models_list_response_max_bytes",
		},
		{
			name:    "gateway response header timeout",
			mutate:  func(c *Config) { c.Gateway.Transport.ResponseHeaderTimeoutSeconds = -1 },
			wantErr: "gateway.transport.response_header_timeout_seconds",
		},
		{
			name:    "gateway openai response header timeout",
			mutate:  func(c *Config) { c.Gateway.OpenAI.ResponseHeaderTimeoutSeconds = -1 },
			wantErr: "gateway.openai.response_header_timeout_seconds",
		},
		{
			name:    "gateway openai first output timeout below minimum",
			mutate:  func(c *Config) { c.Gateway.OpenAI.FirstOutputTimeoutSeconds = 29 },
			wantErr: "gateway.openai.first_output_timeout_seconds",
		},
		{
			name:    "gateway openai high effort first output timeout too large",
			mutate:  func(c *Config) { c.Gateway.OpenAI.HighEffortFirstOutputTimeoutSeconds = 1801 },
			wantErr: "gateway.openai.high_effort_first_output_timeout_seconds",
		},
		{
			name:    "gateway max idle conns",
			mutate:  func(c *Config) { c.Gateway.Transport.MaxIdleConns = 0 },
			wantErr: "gateway.transport.max_idle_conns",
		},
		{
			name:    "gateway max idle conns per host",
			mutate:  func(c *Config) { c.Gateway.Transport.MaxIdleConnsPerHost = 0 },
			wantErr: "gateway.transport.max_idle_conns_per_host",
		},
		{
			name:    "gateway idle timeout",
			mutate:  func(c *Config) { c.Gateway.Transport.IdleConnTimeoutSeconds = 0 },
			wantErr: "gateway.transport.idle_conn_timeout_seconds",
		},
		{
			name:    "gateway max upstream clients",
			mutate:  func(c *Config) { c.Gateway.Transport.ClientCache.MaxClients = 0 },
			wantErr: "gateway.transport.client_cache.max_clients",
		},
		{
			name:    "gateway client idle ttl",
			mutate:  func(c *Config) { c.Gateway.Transport.ClientCache.IdleTTLSeconds = 0 },
			wantErr: "gateway.transport.client_cache.idle_ttl_seconds",
		},
		{
			name:    "gateway concurrency slot ttl",
			mutate:  func(c *Config) { c.Scheduling.ConcurrencySlotTTLMinutes = 0 },
			wantErr: "scheduling.concurrency_slot_ttl_minutes",
		},
		{
			name:    "gateway max conns per host",
			mutate:  func(c *Config) { c.Gateway.Transport.MaxConnsPerHost = -1 },
			wantErr: "gateway.transport.max_conns_per_host",
		},
		{
			name:    "gateway connection isolation",
			mutate:  func(c *Config) { c.Gateway.Transport.ConnectionPoolIsolation = "invalid" },
			wantErr: "gateway.transport.connection_pool_isolation",
		},
		{
			name:    "gateway stream keepalive range",
			mutate:  func(c *Config) { c.Gateway.Streaming.KeepaliveIntervalSeconds = 4 },
			wantErr: "gateway.streaming.keepalive_interval_seconds",
		},
		{
			name:    "gateway openai ws oauth max conns factor",
			mutate:  func(c *Config) { c.Gateway.OpenAI.ResponsesWS.OAuthMaxConnsFactor = 0 },
			wantErr: "gateway.openai.responses_ws.oauth_max_conns_factor",
		},
		{
			name:    "gateway openai ws apikey max conns factor",
			mutate:  func(c *Config) { c.Gateway.OpenAI.ResponsesWS.APIKeyMaxConnsFactor = 0 },
			wantErr: "gateway.openai.responses_ws.api_key_max_conns_factor",
		},
		{
			name:    "gateway openai http2 fallback threshold",
			mutate:  func(c *Config) { c.Gateway.OpenAI.HTTP2.FallbackErrorThreshold = -1 },
			wantErr: "gateway.openai.http2.fallback_error_threshold",
		},
		{
			name:    "gateway openai http2 fallback window",
			mutate:  func(c *Config) { c.Gateway.OpenAI.HTTP2.FallbackWindowSeconds = -1 },
			wantErr: "gateway.openai.http2.fallback_window_seconds",
		},
		{
			name:    "gateway openai http2 fallback ttl",
			mutate:  func(c *Config) { c.Gateway.OpenAI.HTTP2.FallbackTTLSeconds = -1 },
			wantErr: "gateway.openai.http2.fallback_ttl_seconds",
		},
		{
			name:    "gateway openai proxy stream circuit threshold",
			mutate:  func(c *Config) { c.Gateway.OpenAI.ProxyStreamCircuit.FailureThreshold = -1 },
			wantErr: "gateway.openai.proxy_stream_circuit.failure_threshold",
		},
		{
			name:    "gateway openai proxy stream circuit window",
			mutate:  func(c *Config) { c.Gateway.OpenAI.ProxyStreamCircuit.WindowSeconds = -1 },
			wantErr: "gateway.openai.proxy_stream_circuit.window_seconds",
		},
		{
			name:    "gateway openai proxy stream circuit ttl",
			mutate:  func(c *Config) { c.Gateway.OpenAI.ProxyStreamCircuit.TTLSeconds = -1 },
			wantErr: "gateway.openai.proxy_stream_circuit.ttl_seconds",
		},
		{
			name:    "gateway stream data interval range",
			mutate:  func(c *Config) { c.Gateway.Streaming.DataIdleTimeoutSeconds = 5 },
			wantErr: "gateway.streaming.data_idle_timeout_seconds",
		},
		{
			name:    "gateway stream data interval negative",
			mutate:  func(c *Config) { c.Gateway.Streaming.DataIdleTimeoutSeconds = -1 },
			wantErr: "gateway.streaming.data_idle_timeout_seconds must be non-negative",
		},
		{
			name:    "gateway image stream keepalive range",
			mutate:  func(c *Config) { c.Gateway.Images.StreamKeepaliveIntervalSeconds = 4 },
			wantErr: "gateway.images.stream_keepalive_interval_seconds",
		},
		{
			name:    "gateway image stream keepalive negative",
			mutate:  func(c *Config) { c.Gateway.Images.StreamKeepaliveIntervalSeconds = -1 },
			wantErr: "gateway.images.stream_keepalive_interval_seconds must be non-negative",
		},
		{
			name:    "gateway image nonstream keepalive range",
			mutate:  func(c *Config) { c.Gateway.Images.NonstreamKeepaliveIntervalSeconds = 4 },
			wantErr: "gateway.images.nonstream_keepalive_interval_seconds",
		},
		{
			name:    "gateway image nonstream keepalive negative",
			mutate:  func(c *Config) { c.Gateway.Images.NonstreamKeepaliveIntervalSeconds = -1 },
			wantErr: "gateway.images.nonstream_keepalive_interval_seconds must be non-negative",
		},
		{
			name:    "gateway image stream data interval range",
			mutate:  func(c *Config) { c.Gateway.Images.StreamDataIdleTimeoutSeconds = 30 },
			wantErr: "gateway.images.stream_data_idle_timeout_seconds",
		},
		{
			name:    "gateway image stream data interval negative",
			mutate:  func(c *Config) { c.Gateway.Images.StreamDataIdleTimeoutSeconds = -1 },
			wantErr: "gateway.images.stream_data_idle_timeout_seconds must be non-negative",
		},
		{
			name:    "gateway image concurrency max negative",
			mutate:  func(c *Config) { c.Gateway.Images.Concurrency.MaxConcurrentRequests = -1 },
			wantErr: "gateway.images.concurrency.max_concurrent_requests must be non-negative",
		},
		{
			name:    "gateway image concurrency overflow mode invalid",
			mutate:  func(c *Config) { c.Gateway.Images.Concurrency.OverflowMode = "queue" },
			wantErr: "gateway.images.concurrency.overflow_mode",
		},
		{
			name:    "gateway image concurrency wait timeout negative",
			mutate:  func(c *Config) { c.Gateway.Images.Concurrency.WaitTimeoutSeconds = -1 },
			wantErr: "gateway.images.concurrency.wait_timeout_seconds must be non-negative",
		},
		{
			name:    "gateway image concurrency max waiting negative",
			mutate:  func(c *Config) { c.Gateway.Images.Concurrency.MaxWaitingRequests = -1 },
			wantErr: "gateway.images.concurrency.max_waiting_requests must be non-negative",
		},
		{
			name:    "gateway max line size",
			mutate:  func(c *Config) { c.Gateway.MaxSSELineBytes = 1024 },
			wantErr: "gateway.max_sse_line_bytes must be at least",
		},
		{
			name:    "gateway max line size negative",
			mutate:  func(c *Config) { c.Gateway.MaxSSELineBytes = -1 },
			wantErr: "gateway.max_sse_line_bytes must be non-negative",
		},
		{
			name:    "gateway usage record worker count",
			mutate:  func(c *Config) { c.Usage.RecordQueue.WorkerCount = 0 },
			wantErr: "usage.record_queue.worker_count",
		},
		{
			name:    "gateway usage record queue size",
			mutate:  func(c *Config) { c.Usage.RecordQueue.QueueSize = 0 },
			wantErr: "usage.record_queue.queue_size",
		},
		{
			name:    "gateway usage record timeout",
			mutate:  func(c *Config) { c.Usage.RecordQueue.TaskTimeoutSeconds = 0 },
			wantErr: "usage.record_queue.task_timeout_seconds",
		},
		{
			name:    "gateway usage record overflow policy",
			mutate:  func(c *Config) { c.Usage.RecordQueue.OverflowPolicy = "invalid" },
			wantErr: "usage.record_queue.overflow_policy",
		},
		{
			name:    "gateway usage record sample percent range",
			mutate:  func(c *Config) { c.Usage.RecordQueue.OverflowSamplePercent = 101 },
			wantErr: "usage.record_queue.overflow_sample_percent",
		},
		{
			name: "gateway usage record sample percent required for sample policy",
			mutate: func(c *Config) {
				c.Usage.RecordQueue.OverflowPolicy = UsageRecordOverflowPolicySample
				c.Usage.RecordQueue.OverflowSamplePercent = 0
			},
			wantErr: "usage.record_queue.overflow_sample_percent must be positive",
		},
		{
			name: "gateway usage record auto scale max gte min",
			mutate: func(c *Config) {
				c.Usage.RecordQueue.AutoScaleMinWorkers = 256
				c.Usage.RecordQueue.AutoScaleMaxWorkers = 128
			},
			wantErr: "usage.record_queue.auto_scale_max_workers",
		},
		{
			name: "gateway usage record worker in auto scale range",
			mutate: func(c *Config) {
				c.Usage.RecordQueue.AutoScaleMinWorkers = 200
				c.Usage.RecordQueue.AutoScaleMaxWorkers = 300
				c.Usage.RecordQueue.WorkerCount = 128
			},
			wantErr: "usage.record_queue.worker_count must be between auto_scale_min_workers and auto_scale_max_workers",
		},
		{
			name: "gateway usage record auto scale queue thresholds order",
			mutate: func(c *Config) {
				c.Usage.RecordQueue.AutoScaleUpQueuePercent = 50
				c.Usage.RecordQueue.AutoScaleDownQueuePercent = 50
			},
			wantErr: "usage.record_queue.auto_scale_down_queue_percent must be less",
		},
		{
			name:    "gateway usage record auto scale up step",
			mutate:  func(c *Config) { c.Usage.RecordQueue.AutoScaleUpStep = 0 },
			wantErr: "usage.record_queue.auto_scale_up_step",
		},
		{
			name:    "gateway usage record auto scale interval",
			mutate:  func(c *Config) { c.Usage.RecordQueue.AutoScaleCheckIntervalSeconds = 0 },
			wantErr: "usage.record_queue.auto_scale_check_interval_seconds",
		},
		{
			name:    "gateway user group rate cache ttl",
			mutate:  func(c *Config) { c.Billing.GroupRateCacheTTLSeconds = 0 },
			wantErr: "billing.group_rate_cache_ttl_seconds",
		},
		{
			name:    "gateway scheduling sticky waiting",
			mutate:  func(c *Config) { c.Scheduling.StickyProviderWait.MaxWaiting = 0 },
			wantErr: "scheduling.sticky_provider_wait.max_waiting",
		},
		{
			name:    "gateway scheduling load batch cache ttl",
			mutate:  func(c *Config) { c.Scheduling.LoadBatch.CacheTTLMS = -1 },
			wantErr: "scheduling.load_batch.cache_ttl_ms",
		},
		{
			name:    "gateway scheduling outbox poll",
			mutate:  func(c *Config) { c.Scheduling.Snapshot.OutboxPollIntervalSeconds = 0 },
			wantErr: "scheduling.snapshot.outbox_poll_interval_seconds",
		},
		{
			name:    "gateway scheduling outbox failures",
			mutate:  func(c *Config) { c.Scheduling.Snapshot.OutboxLagRebuildFailures = 0 },
			wantErr: "scheduling.snapshot.outbox_lag_rebuild_failures",
		},
		{
			name: "gateway outbox lag rebuild",
			mutate: func(c *Config) {
				c.Scheduling.Snapshot.OutboxLagWarnSeconds = 10
				c.Scheduling.Snapshot.OutboxLagRebuildSeconds = 5
			},
			wantErr: "scheduling.snapshot.outbox_lag_rebuild_seconds",
		},
		{
			name:    "log level invalid",
			mutate:  func(c *Config) { c.Log.Level = "trace" },
			wantErr: "log.level",
		},
		{
			name:    "log format invalid",
			mutate:  func(c *Config) { c.Log.Format = "plain" },
			wantErr: "log.format",
		},
		{
			name: "log output disabled",
			mutate: func(c *Config) {
				c.Log.Output.ToStdout = false
				c.Log.Output.ToFile = false
			},
			wantErr: "log.output.to_stdout and log.output.to_file cannot both be false",
		},
		{
			name:    "log rotation size",
			mutate:  func(c *Config) { c.Log.Rotation.MaxSizeMB = 0 },
			wantErr: "log.rotation.max_size_mb",
		},
		{
			name: "log sampling enabled invalid",
			mutate: func(c *Config) {
				c.Log.Sampling.Enabled = true
				c.Log.Sampling.Initial = 0
			},
			wantErr: "log.sampling.initial",
		},
		{
			name:    "ops cleanup retention",
			mutate:  func(c *Config) { c.Ops.Cleanup.ErrorLogRetentionDays = -1 },
			wantErr: "ops.cleanup.error_log_retention_days",
		},
		{
			name:    "ops cleanup system log retention",
			mutate:  func(c *Config) { c.Ops.Cleanup.SystemLogRetentionDays = -1 },
			wantErr: "ops.cleanup.system_log_retention_days",
		},
		{
			name:    "ops cleanup minute retention",
			mutate:  func(c *Config) { c.Ops.Cleanup.MinuteMetricsRetentionDays = -1 },
			wantErr: "ops.cleanup.minute_metrics_retention_days",
		},
		{
			name:    "ops cleanup batch size",
			mutate:  func(c *Config) { c.Ops.Cleanup.BatchSize = 99 },
			wantErr: "ops.cleanup.batch_size",
		},
		{
			name:    "ops cleanup batch pause",
			mutate:  func(c *Config) { c.Ops.Cleanup.BatchPauseMS = 2001 },
			wantErr: "ops.cleanup.batch_pause_ms",
		},
	}

	for _, tt := range cases {
		t.Run(tt.name, func(t *testing.T) {
			cfg := buildValid(t)
			tt.mutate(cfg)
			err := cfg.Validate()
			if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
				t.Fatalf("Validate() error = %v, want %q", err, tt.wantErr)
			}
		})
	}
}

// TestValidateConfig_OpenAIWSRules supplies invalid WS limits, EWMA values,
// scheduler weights, and sticky thresholds plus valid single-weight cases.
// Validation must retain old-TTL compatibility, reject invalid values, and
// accept the tested scoring choices.
func TestValidateConfig_OpenAIWSRules(t *testing.T) {
	buildValid := func(t *testing.T) *Config {
		t.Helper()
		fixture := newConfigFixture(t)
		cfg, err := fixture.Load()
		require.NoError(t, err)
		return cfg
	}

	cases := []struct {
		name    string        // subtest display name
		mutate  func(*Config) // fixture configuration mutation callback
		wantErr string        // expected validation error fragment
	}{
		{
			name:    "max_conns_per_provider 必须为正数",
			mutate:  func(c *Config) { c.Gateway.OpenAI.ResponsesWS.MaxConnsPerProvider = 0 },
			wantErr: "gateway.openai.responses_ws.max_conns_per_provider",
		},
		{
			name:    "client_first_message_timeout_seconds 必须为正数",
			mutate:  func(c *Config) { c.Gateway.OpenAI.ResponsesWS.ClientFirstMessageTimeoutSeconds = 0 },
			wantErr: "gateway.openai.responses_ws.client_first_message_timeout_seconds",
		},
		{
			name:    "client_first_message_timeout_seconds 不能为负数",
			mutate:  func(c *Config) { c.Gateway.OpenAI.ResponsesWS.ClientFirstMessageTimeoutSeconds = -1 },
			wantErr: "gateway.openai.responses_ws.client_first_message_timeout_seconds",
		},
		{
			name:    "ingress_inter_turn_idle_timeout_seconds 不能为负数",
			mutate:  func(c *Config) { c.Gateway.OpenAI.ResponsesWS.IngressInterTurnIdleTimeoutSeconds = -1 },
			wantErr: "gateway.openai.responses_ws.ingress_inter_turn_idle_timeout_seconds",
		},
		{
			name:    "max_ingress_connections_per_api_key 不能为负数",
			mutate:  func(c *Config) { c.Gateway.OpenAI.ResponsesWS.MaxIngressConnectionsPerAPIKey = -1 },
			wantErr: "gateway.openai.responses_ws.max_ingress_connections_per_api_key",
		},
		{
			name:    "min_idle_per_provider 不能为负数",
			mutate:  func(c *Config) { c.Gateway.OpenAI.ResponsesWS.MinIdlePerProvider = -1 },
			wantErr: "gateway.openai.responses_ws.min_idle_per_provider",
		},
		{
			name:    "max_idle_per_provider 不能为负数",
			mutate:  func(c *Config) { c.Gateway.OpenAI.ResponsesWS.MaxIdlePerProvider = -1 },
			wantErr: "gateway.openai.responses_ws.max_idle_per_provider",
		},
		{
			name: "min_idle_per_provider 不能大于 max_idle_per_provider",
			mutate: func(c *Config) {
				c.Gateway.OpenAI.ResponsesWS.MinIdlePerProvider = 3
				c.Gateway.OpenAI.ResponsesWS.MaxIdlePerProvider = 2
			},
			wantErr: "gateway.openai.responses_ws.min_idle_per_provider must be <= max_idle_per_provider",
		},
		{
			name: "max_idle_per_provider 不能大于 max_conns_per_provider",
			mutate: func(c *Config) {
				c.Gateway.OpenAI.ResponsesWS.MaxConnsPerProvider = 2
				c.Gateway.OpenAI.ResponsesWS.MinIdlePerProvider = 1
				c.Gateway.OpenAI.ResponsesWS.MaxIdlePerProvider = 3
			},
			wantErr: "gateway.openai.responses_ws.max_idle_per_provider must be <= max_conns_per_provider",
		},
		{
			name:    "dial_timeout_seconds 必须为正数",
			mutate:  func(c *Config) { c.Gateway.OpenAI.ResponsesWS.DialTimeoutSeconds = 0 },
			wantErr: "gateway.openai.responses_ws.dial_timeout_seconds",
		},
		{
			name:    "read_timeout_seconds 必须为正数",
			mutate:  func(c *Config) { c.Gateway.OpenAI.ResponsesWS.ReadTimeoutSeconds = 0 },
			wantErr: "gateway.openai.responses_ws.read_timeout_seconds",
		},
		{
			name:    "write_timeout_seconds 必须为正数",
			mutate:  func(c *Config) { c.Gateway.OpenAI.ResponsesWS.WriteTimeoutSeconds = 0 },
			wantErr: "gateway.openai.responses_ws.write_timeout_seconds",
		},
		{
			name:    "pool_target_utilization 必须在 (0,1]",
			mutate:  func(c *Config) { c.Gateway.OpenAI.ResponsesWS.PoolTargetUtilization = 0 },
			wantErr: "gateway.openai.responses_ws.pool_target_utilization",
		},
		{
			name:    "queue_limit_per_conn 必须为正数",
			mutate:  func(c *Config) { c.Gateway.OpenAI.ResponsesWS.QueueLimitPerConn = 0 },
			wantErr: "gateway.openai.responses_ws.queue_limit_per_conn",
		},

		{
			name:    "lb_top_k 必须为正数",
			mutate:  func(c *Config) { c.Scheduling.Advanced.LBTopK = 0 },
			wantErr: "scheduling.advanced.lb_top_k",
		},
		{
			name:    "error rate EWMA alpha 必须大于 0",
			mutate:  func(c *Config) { c.Scheduling.Advanced.EWMAErrorRateAlpha = 0 },
			wantErr: "scheduling.advanced.ewma_error_rate_alpha",
		},
		{
			name:    "TTFT EWMA alpha 不能大于 1",
			mutate:  func(c *Config) { c.Scheduling.Advanced.EWMATTFTAlpha = 1.1 },
			wantErr: "scheduling.advanced.ewma_ttft_alpha",
		},
		{
			name:    "error rate EWMA alpha 不能为 NaN",
			mutate:  func(c *Config) { c.Scheduling.Advanced.EWMAErrorRateAlpha = math.NaN() },
			wantErr: "scheduling.advanced.ewma_error_rate_alpha",
		},
		{
			name:    "TTFT EWMA alpha 不能为 Inf",
			mutate:  func(c *Config) { c.Scheduling.Advanced.EWMATTFTAlpha = math.Inf(1) },
			wantErr: "scheduling.advanced.ewma_ttft_alpha",
		},
		{
			name:    "sticky_session_ttl_seconds 必须为正数",
			mutate:  func(c *Config) { c.Scheduling.SessionAffinity.TTLSeconds = 0 },
			wantErr: "scheduling.session_affinity.ttl_seconds",
		},
		{
			name:    "scheduler_score_weights 不能为负数",
			mutate:  func(c *Config) { c.Scheduling.Advanced.ScoreWeights.Queue = -0.1 },
			wantErr: "scheduling.advanced.score_weights.* must be non-negative",
		},
		{
			name:    "scheduler_score_weights quota_headroom 不能为负数",
			mutate:  func(c *Config) { c.Scheduling.Advanced.ScoreWeights.QuotaHeadroom = -0.1 },
			wantErr: "scheduling.advanced.score_weights.* must be non-negative",
		},
		{
			name:    "scheduler_score_weights reset 不能为负数",
			mutate:  func(c *Config) { c.Scheduling.Advanced.ScoreWeights.Reset = -0.1 },
			wantErr: "scheduling.advanced.score_weights.* must be non-negative",
		},
		{
			name:    "scheduler_score_weights 不能为 NaN",
			mutate:  func(c *Config) { c.Scheduling.Advanced.ScoreWeights.PreviousResponse = math.NaN() },
			wantErr: "scheduling.advanced.score_weights.* must be non-negative and finite",
		},
		{
			name:    "scheduler_score_weights 不能为 Inf",
			mutate:  func(c *Config) { c.Scheduling.Advanced.ScoreWeights.Reset = math.Inf(1) },
			wantErr: "scheduling.advanced.score_weights.* must be non-negative and finite",
		},
		{
			name: "scheduler_score_weights 总和不能溢出",
			mutate: func(c *Config) {
				c.Scheduling.Advanced.ScoreWeights.Priority = math.MaxFloat64
				c.Scheduling.Advanced.ScoreWeights.Load = math.MaxFloat64
			},
			wantErr: "scheduling.advanced.score_weights base-weight sum must be finite",
		},
		{
			name: "scheduler_score_weights 含 sticky 总和不能溢出",
			mutate: func(c *Config) {
				c.Scheduling.Advanced.ScoreWeights.Priority = math.MaxFloat64
				c.Scheduling.Advanced.ScoreWeights.PreviousResponse = math.MaxFloat64
			},
			wantErr: "scheduling.advanced.score_weights total-weight sum must be finite",
		},
		{
			name: "scheduler_score_weights 不能全为 0",
			mutate: func(c *Config) {
				c.Scheduling.Advanced.ScoreWeights.Priority = 0
				c.Scheduling.Advanced.ScoreWeights.Load = 0
				c.Scheduling.Advanced.ScoreWeights.Queue = 0
				c.Scheduling.Advanced.ScoreWeights.ErrorRate = 0
				c.Scheduling.Advanced.ScoreWeights.TTFT = 0
			},
			wantErr: "scheduling.advanced.score_weights must not all be zero",
		},
		{
			name:    "sticky_escape_ttft_ms 必须为正数",
			mutate:  func(c *Config) { c.Scheduling.Advanced.StickyEscapeTTFTMs = 0 },
			wantErr: "scheduling.advanced.sticky_escape_ttft_ms",
		},
		{
			name:    "sticky_escape_error_rate 不能小于 0",
			mutate:  func(c *Config) { c.Scheduling.Advanced.StickyEscapeErrorRate = -0.1 },
			wantErr: "scheduling.advanced.sticky_escape_error_rate",
		},
		{
			name:    "sticky_escape_error_rate 不能大于 1",
			mutate:  func(c *Config) { c.Scheduling.Advanced.StickyEscapeErrorRate = 1.1 },
			wantErr: "scheduling.advanced.sticky_escape_error_rate",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			cfg := buildValid(t)
			tc.mutate(cfg)

			err := cfg.Validate()
			require.Error(t, err)
			require.Contains(t, err.Error(), tc.wantErr)
		})
	}

	t.Run("quota_headroom 可作为唯一有效调度权重", func(t *testing.T) {
		cfg := buildValid(t)
		cfg.Scheduling.Advanced.ScoreWeights.Priority = 0
		cfg.Scheduling.Advanced.ScoreWeights.Load = 0
		cfg.Scheduling.Advanced.ScoreWeights.Queue = 0
		cfg.Scheduling.Advanced.ScoreWeights.ErrorRate = 0
		cfg.Scheduling.Advanced.ScoreWeights.TTFT = 0
		cfg.Scheduling.Advanced.ScoreWeights.Reset = 0
		cfg.Scheduling.Advanced.ScoreWeights.QuotaHeadroom = 0.1

		require.NoError(t, cfg.Validate())
	})

	t.Run("reset 可作为唯一有效调度权重", func(t *testing.T) {
		cfg := buildValid(t)
		cfg.Scheduling.Advanced.ScoreWeights.Priority = 0
		cfg.Scheduling.Advanced.ScoreWeights.Load = 0
		cfg.Scheduling.Advanced.ScoreWeights.Queue = 0
		cfg.Scheduling.Advanced.ScoreWeights.ErrorRate = 0
		cfg.Scheduling.Advanced.ScoreWeights.TTFT = 0
		cfg.Scheduling.Advanced.ScoreWeights.Reset = 0.1

		require.NoError(t, cfg.Validate())
	})
}

// TestValidateGatewayFirstErrorAndMutationOrder combines errors before, within,
// and after gateway validation. Each repair must expose the next error while
// preserving the warning sequence and only mutations reached before that error.
func TestValidateGatewayFirstErrorAndMutationOrder(t *testing.T) {
	fixture := newConfigFixture(t)
	cfg, err := fixture.Load()
	require.NoError(t, err)
	maxBodySize := cfg.Gateway.MaxRequestBodyBytes
	maxConnsPerProvider := cfg.Gateway.OpenAI.ResponsesWS.MaxConnsPerProvider
	cfg.Auth.JWT.AccessTokenTTLMinutes = 1500
	cfg.Idempotency.CleanupBatchSize = 0
	cfg.Gateway.MaxRequestBodyBytes = 0
	cfg.Gateway.OpenAI.Live.MaxSessionDurationSeconds = -1
	cfg.Gateway.Transport.ConnectionPoolIsolation = "invalid"
	cfg.Gateway.Transport.IdleConnTimeoutSeconds = 181
	cfg.Gateway.Transport.ClientCache.MaxClients = 0
	cfg.Scheduling.SessionAffinity.ResponseIDTTLSeconds = 1234
	cfg.Gateway.OpenAI.ResponsesWS.MaxConnsPerProvider = 0
	cfg.Scheduling.Snapshot.OutboxLagWarnSeconds = 2
	cfg.Scheduling.Snapshot.OutboxLagRebuildSeconds = 1
	cfg.Gateway.Streaming.WaitPingIntervalSeconds = 0
	cfg.Scheduling.GrokFreeQuota.Enabled = true
	cfg.Scheduling.GrokFreeQuota.TokenLimit = 0

	var logs bytes.Buffer
	// Capture only validation warnings, then restore the shared logger so later
	// serial fixtures cannot accidentally keep writing into this test's buffer.
	previous := slog.Default()
	slog.SetDefault(slog.New(slog.NewJSONHandler(&logs, nil)))
	t.Cleanup(func() { slog.SetDefault(previous) })

	steps := []struct {
		wantError   string        // expected first validation error
		wantLive    int           // retained Live duration seconds
		wantWarning bool          // gateway idle warning emitted
		repair      func(*Config) // correction exposing subsequent validation failure
	}{
		{
			wantError: "idempotency.cleanup_batch_size must be positive",
			wantLive:  -1,
			repair:    func(c *Config) { c.Idempotency.CleanupBatchSize = 1 },
		},
		{
			wantError: "gateway.max_request_body_bytes must be positive",
			wantLive:  -1,
			repair:    func(c *Config) { c.Gateway.MaxRequestBodyBytes = maxBodySize },
		},
		{
			wantError: "gateway.transport.connection_pool_isolation must be one of: proxy/provider/provider_proxy",
			wantLive:  3600,
			repair:    func(c *Config) { c.Gateway.Transport.ConnectionPoolIsolation = ConnectionPoolIsolationProvider },
		},
		{
			wantError:   "gateway.transport.client_cache.max_clients must be positive",
			wantLive:    3600,
			wantWarning: true,
			repair:      func(c *Config) { c.Gateway.Transport.ClientCache.MaxClients = 1 },
		},
		{
			wantError:   "gateway.openai.responses_ws.max_conns_per_provider must be positive",
			wantLive:    3600,
			wantWarning: true,
			repair:      func(c *Config) { c.Gateway.OpenAI.ResponsesWS.MaxConnsPerProvider = maxConnsPerProvider },
		},
		{
			wantError:   "scheduling.snapshot.outbox_lag_rebuild_seconds must be >= outbox_lag_warn_seconds",
			wantLive:    3600,
			wantWarning: true,
			repair:      func(c *Config) { c.Scheduling.Snapshot.OutboxLagRebuildSeconds = 2 },
		},
		{
			wantError:   "gateway.streaming.wait_ping_interval_seconds must be between 5-30 seconds",
			wantLive:    3600,
			wantWarning: true,
			repair:      func(c *Config) { c.Gateway.Streaming.WaitPingIntervalSeconds = 5 },
		},
		{
			wantError:   "scheduling.grok_free_quota.token_limit must be positive",
			wantLive:    3600,
			wantWarning: true,
			repair:      func(c *Config) { c.Scheduling.GrokFreeQuota.TokenLimit = 1 },
		},
		{
			wantLive:    3600,
			wantWarning: true,
		},
	}
	for _, step := range steps {
		logs.Reset()
		err := cfg.Validate()
		if step.wantError == "" {
			require.NoError(t, err)
		} else {
			require.EqualError(t, err, step.wantError)
		}
		require.Equal(t, step.wantLive, cfg.Gateway.OpenAI.Live.MaxSessionDurationSeconds)
		require.Equal(t, 1234, cfg.Scheduling.SessionAffinity.ResponseIDTTLSeconds)
		require.Equal(t, 181, cfg.Gateway.Transport.IdleConnTimeoutSeconds)
		wantMessages := []string{"auth.jwt.access_token_ttl_minutes is high; consider shorter expiration for security"}
		if step.wantWarning {
			wantMessages = append(wantMessages, "gateway.transport.idle_conn_timeout_seconds is high; consider 60-120 seconds for better connection reuse")
		}
		var messages []string
		decoder := json.NewDecoder(&logs)
		for decoder.More() {
			var record map[string]any
			require.NoError(t, decoder.Decode(&record))
			require.Equal(t, "WARN", record["level"])
			message, ok := record["msg"].(string)
			require.True(t, ok)
			messages = append(messages, message)
		}
		require.Equal(t, wantMessages, messages)
		if step.repair != nil {
			step.repair(cfg)
		}
	}
}

// TestValidateGatewayNonfiniteValues supplies NaN and both infinities to
// gateway floating-point controls. Validation must retain each field's existing
// comparison-only or explicitly finite rule, including accepted nonfinite values.
func TestValidateGatewayNonfiniteValues(t *testing.T) {
	fields := []struct {
		name      string                 // gateway configuration field path
		set       func(*Config, float64) // field-specific value assignment callback
		acceptNaN bool                   // existing NaN acceptance behavior
		acceptInf bool                   // existing positive-infinity acceptance behavior
	}{
		{
			name:      "gateway.openai.responses_ws.oauth_max_conns_factor",
			set:       func(c *Config, value float64) { c.Gateway.OpenAI.ResponsesWS.OAuthMaxConnsFactor = value },
			acceptNaN: true,
			acceptInf: true,
		},
		{
			name:      "gateway.openai.responses_ws.api_key_max_conns_factor",
			set:       func(c *Config, value float64) { c.Gateway.OpenAI.ResponsesWS.APIKeyMaxConnsFactor = value },
			acceptNaN: true,
			acceptInf: true,
		},
		{
			name:      "gateway.openai.responses_ws.pool_target_utilization",
			set:       func(c *Config, value float64) { c.Gateway.OpenAI.ResponsesWS.PoolTargetUtilization = value },
			acceptNaN: true,
		},
		{
			name: "scheduling.advanced.ewma_error_rate_alpha",
			set:  func(c *Config, value float64) { c.Scheduling.Advanced.EWMAErrorRateAlpha = value },
		},
		{
			name: "scheduling.advanced.ewma_ttft_alpha",
			set:  func(c *Config, value float64) { c.Scheduling.Advanced.EWMATTFTAlpha = value },
		},
		{
			name: "scheduling.advanced.score_weights.*",
			set:  func(c *Config, value float64) { c.Scheduling.Advanced.ScoreWeights.Priority = value },
		},
		{
			name:      "scheduling.advanced.sticky_escape_error_rate",
			set:       func(c *Config, value float64) { c.Scheduling.Advanced.StickyEscapeErrorRate = value },
			acceptNaN: true,
		},
	}
	for _, field := range fields {
		for _, sample := range []struct {
			name   string  // floating-point input classification
			value  float64 // supplied nonfinite floating-point value
			accept bool    // expected successful validation result
		}{
			{"NaN", math.NaN(), field.acceptNaN},
			{"positive infinity", math.Inf(1), field.acceptInf},
			{"negative infinity", math.Inf(-1), false},
		} {
			t.Run(field.name+"/"+sample.name, func(t *testing.T) {
				fixture := newConfigFixture(t)
				cfg, err := fixture.Load()
				require.NoError(t, err)
				field.set(cfg, sample.value)
				err = cfg.Validate()
				if sample.accept {
					require.NoError(t, err)
				} else {
					require.ErrorContains(t, err, field.name)
				}
			})
		}
	}
}

// TestValidateGatewayPoolBoundaries checks inclusive idle capacity and worker
// ranges alongside disabled autoscaling. Invalid ordinary pool settings must
// still fail while inactive scaling settings retain their current acceptance.
func TestValidateGatewayPoolBoundaries(t *testing.T) {
	cases := []struct {
		name      string        // boundary scenario display name
		configure func(*Config) // supplied pool and worker settings
		wantError string        // expected first validation error
	}{
		{
			name: "idle counts equal total capacity",
			configure: func(c *Config) {
				c.Gateway.OpenAI.ResponsesWS.MaxConnsPerProvider = 1
				c.Gateway.OpenAI.ResponsesWS.MinIdlePerProvider = 1
				c.Gateway.OpenAI.ResponsesWS.MaxIdlePerProvider = 1
				c.Gateway.OpenAI.ResponsesWS.PoolTargetUtilization = 1
			},
		},
		{
			name: "zero idle counts and unlimited HTTP connections",
			configure: func(c *Config) {
				c.Gateway.OpenAI.ResponsesWS.MinIdlePerProvider = 0
				c.Gateway.OpenAI.ResponsesWS.MaxIdlePerProvider = 0
				c.Gateway.Transport.MaxConnsPerHost = 0
			},
		},
		{
			name: "idle minimum fails before idle capacity",
			configure: func(c *Config) {
				c.Gateway.OpenAI.ResponsesWS.MaxConnsPerProvider = 1
				c.Gateway.OpenAI.ResponsesWS.MinIdlePerProvider = 3
				c.Gateway.OpenAI.ResponsesWS.MaxIdlePerProvider = 2
			},
			wantError: "gateway.openai.responses_ws.min_idle_per_provider must be <= max_idle_per_provider",
		},
		{
			name: "workers equal both scaling limits",
			configure: func(c *Config) {
				c.Usage.RecordQueue.AutoScaleEnabled = true
				c.Usage.RecordQueue.WorkerCount = 1
				c.Usage.RecordQueue.AutoScaleMinWorkers = 1
				c.Usage.RecordQueue.AutoScaleMaxWorkers = 1
				c.Usage.RecordQueue.AutoScaleUpQueuePercent = 1
				c.Usage.RecordQueue.AutoScaleDownQueuePercent = 0
				c.Usage.RecordQueue.AutoScaleCooldownSeconds = 0
			},
		},
		{
			name: "disabled scaling retains ordinary worker validation",
			configure: func(c *Config) {
				c.Usage.RecordQueue.AutoScaleEnabled = false
				c.Usage.RecordQueue.WorkerCount = 0
				c.Usage.RecordQueue.AutoScaleMinWorkers = -1
			},
			wantError: "usage.record_queue.worker_count must be positive",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			fixture := newConfigFixture(t)
			cfg, err := fixture.Load()
			require.NoError(t, err)
			tc.configure(cfg)
			err = cfg.Validate()
			if tc.wantError == "" {
				require.NoError(t, err)
			} else {
				require.EqualError(t, err, tc.wantError)
			}
		})
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

// TestLoad_DefaultGatewayUsageRecordConfig inspects worker count, queue
// capacity, task timeout, overflow behavior, and autoscaling parameters.
// Default loading must retain the exact execution and scaling values asserted
// by the fixture.
func TestLoad_DefaultGatewayUsageRecordConfig(t *testing.T) {
	fixture := newConfigFixture(t)
	cfg, err := fixture.Load()
	if err != nil {
		t.Fatalf("fixture.Load() error: %v", err)
	}
	if cfg.Usage.RecordQueue.WorkerCount != 128 {
		t.Fatalf("worker_count = %d, want 128", cfg.Usage.RecordQueue.WorkerCount)
	}
	if cfg.Usage.RecordQueue.QueueSize != 16384 {
		t.Fatalf("queue_size = %d, want 16384", cfg.Usage.RecordQueue.QueueSize)
	}
	if cfg.Usage.RecordQueue.TaskTimeoutSeconds != 5 {
		t.Fatalf("task_timeout_seconds = %d, want 5", cfg.Usage.RecordQueue.TaskTimeoutSeconds)
	}
	if cfg.Usage.RecordQueue.OverflowPolicy != UsageRecordOverflowPolicySync {
		t.Fatalf("overflow_policy = %s, want %s", cfg.Usage.RecordQueue.OverflowPolicy, UsageRecordOverflowPolicySync)
	}
	if cfg.Usage.RecordQueue.OverflowSamplePercent != 10 {
		t.Fatalf("overflow_sample_percent = %d, want 10", cfg.Usage.RecordQueue.OverflowSamplePercent)
	}
	if !cfg.Usage.RecordQueue.AutoScaleEnabled {
		t.Fatalf("auto_scale_enabled = false, want true")
	}
	if cfg.Usage.RecordQueue.AutoScaleMinWorkers != 128 {
		t.Fatalf("auto_scale_min_workers = %d, want 128", cfg.Usage.RecordQueue.AutoScaleMinWorkers)
	}
	if cfg.Usage.RecordQueue.AutoScaleMaxWorkers != 512 {
		t.Fatalf("auto_scale_max_workers = %d, want 512", cfg.Usage.RecordQueue.AutoScaleMaxWorkers)
	}
	if cfg.Usage.RecordQueue.AutoScaleUpQueuePercent != 70 {
		t.Fatalf("auto_scale_up_queue_percent = %d, want 70", cfg.Usage.RecordQueue.AutoScaleUpQueuePercent)
	}
	if cfg.Usage.RecordQueue.AutoScaleDownQueuePercent != 15 {
		t.Fatalf("auto_scale_down_queue_percent = %d, want 15", cfg.Usage.RecordQueue.AutoScaleDownQueuePercent)
	}
	if cfg.Usage.RecordQueue.AutoScaleUpStep != 32 {
		t.Fatalf("auto_scale_up_step = %d, want 32", cfg.Usage.RecordQueue.AutoScaleUpStep)
	}
	if cfg.Usage.RecordQueue.AutoScaleDownStep != 16 {
		t.Fatalf("auto_scale_down_step = %d, want 16", cfg.Usage.RecordQueue.AutoScaleDownStep)
	}
	if cfg.Usage.RecordQueue.AutoScaleCheckIntervalSeconds != 3 {
		t.Fatalf("auto_scale_check_interval_seconds = %d, want 3", cfg.Usage.RecordQueue.AutoScaleCheckIntervalSeconds)
	}
	if cfg.Usage.RecordQueue.AutoScaleCooldownSeconds != 10 {
		t.Fatalf("auto_scale_cooldown_seconds = %d, want 10", cfg.Usage.RecordQueue.AutoScaleCooldownSeconds)
	}
}

// TestLoad_DefaultGatewayImageStreamConfig compares ordinary and image stream
// budgets with image keepalive and concurrency defaults. Loading must keep the
// image data gap longer, nonstream keepalive disabled, and separate image
// concurrency disabled.
func TestLoad_DefaultGatewayImageStreamConfig(t *testing.T) {
	fixture := newConfigFixture(t)
	cfg, err := fixture.Load()
	if err != nil {
		t.Fatalf("fixture.Load() error: %v", err)
	}
	if cfg.Gateway.Streaming.DataIdleTimeoutSeconds != 180 {
		t.Fatalf("stream_data_interval_timeout = %d, want 180", cfg.Gateway.Streaming.DataIdleTimeoutSeconds)
	}
	if cfg.Gateway.Streaming.KeepaliveIntervalSeconds != 10 {
		t.Fatalf("stream_keepalive_interval = %d, want 10", cfg.Gateway.Streaming.KeepaliveIntervalSeconds)
	}
	if cfg.Gateway.Images.StreamDataIdleTimeoutSeconds != 900 {
		t.Fatalf("image_stream_data_interval_timeout = %d, want 900", cfg.Gateway.Images.StreamDataIdleTimeoutSeconds)
	}
	if cfg.Gateway.Images.StreamKeepaliveIntervalSeconds != 10 {
		t.Fatalf("image_stream_keepalive_interval = %d, want 10", cfg.Gateway.Images.StreamKeepaliveIntervalSeconds)
	}
	if cfg.Gateway.Images.NonstreamKeepaliveIntervalSeconds != 0 {
		t.Fatalf("image_nonstream_keepalive_interval = %d, want 0", cfg.Gateway.Images.NonstreamKeepaliveIntervalSeconds)
	}
	if cfg.Gateway.Images.Concurrency.Enabled {
		t.Fatalf("image_concurrency.enabled = true, want false")
	}
	if cfg.Gateway.Images.Concurrency.MaxConcurrentRequests != 0 {
		t.Fatalf("image_concurrency.max_concurrent_requests = %d, want 0", cfg.Gateway.Images.Concurrency.MaxConcurrentRequests)
	}
	if cfg.Gateway.Images.Concurrency.OverflowMode != ImageConcurrencyOverflowModeReject {
		t.Fatalf("image_concurrency.overflow_mode = %q, want %q", cfg.Gateway.Images.Concurrency.OverflowMode, ImageConcurrencyOverflowModeReject)
	}
	if cfg.Gateway.Images.Concurrency.WaitTimeoutSeconds != 30 {
		t.Fatalf("image_concurrency.wait_timeout_seconds = %d, want 30", cfg.Gateway.Images.Concurrency.WaitTimeoutSeconds)
	}
	if cfg.Gateway.Images.Concurrency.MaxWaitingRequests != 100 {
		t.Fatalf("image_concurrency.max_waiting_requests = %d, want 100", cfg.Gateway.Images.Concurrency.MaxWaitingRequests)
	}
	if cfg.Gateway.Images.StreamDataIdleTimeoutSeconds <= cfg.Gateway.Streaming.DataIdleTimeoutSeconds {
		t.Fatalf("image stream timeout = %d, want greater than ordinary stream timeout %d", cfg.Gateway.Images.StreamDataIdleTimeoutSeconds, cfg.Gateway.Streaming.DataIdleTimeoutSeconds)
	}
}

// TestLoadIgnoresRetiredSubscriptionMaintenance supplies malformed retired
// worker limits alongside a current server setting. Loading must retain the
// current port and still reject a later invalid JWT lifetime through active
// validation.
func TestLoadIgnoresRetiredSubscriptionMaintenance(t *testing.T) {
	fixture := newConfigFixture(t)
	configFile := filepath.Join(t.TempDir(), "config.yaml")
	require.NoError(t, os.WriteFile(configFile, []byte("subscription_maintenance:\n  worker_count: -1\n  queue_size: -1\nserver:\n  port: 8091\n"), 0o600))
	fixture.Setenv("CONFIG_FILE", configFile)
	fixture.Setenv("SUBSCRIPTION_MAINTENANCE_WORKER_COUNT", "-2")
	fixture.Setenv("SUBSCRIPTION_MAINTENANCE_QUEUE_SIZE", "-2")
	cfg, err := fixture.Load()
	require.NoError(t, err)
	require.Equal(t, 8091, cfg.Server.Port)
	cfg.Auth.JWT.AccessTokenTTLMinutes = 0
	require.ErrorContains(t, cfg.Validate(), "auth.jwt.access_token_ttl_minutes")
}

// TestLoadIgnoresRetiredOpenAIWSFallbackCooldown supplies a negative retired
// cooldown variable. Loading must succeed and keep the default WS dialing
// budget, showing the tested current field is unaffected.
func TestLoadIgnoresRetiredOpenAIWSFallbackCooldown(t *testing.T) {
	fixture := newConfigFixture(t)
	fixture.Setenv("GATEWAY_OPENAI_WS_FALLBACK_COOLDOWN_SECONDS", "-2")
	cfg, err := fixture.Load()
	require.NoError(t, err)
	require.Equal(t, 10, cfg.Gateway.OpenAI.ResponsesWS.DialTimeoutSeconds)
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
			wantName:    " TokenRouter ",
			wantID:      " EXAMPLE.COM ",
			wantOrigins: []string{" HTTPS://APP.EXAMPLE.COM ", " ", " https://BAD.EXAMPLE.COM/path ", " HTTPS://LAST.EXAMPLE.COM "},
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
			name:        "invalid second origin after first canonicalization",
			configure:   func(c *Config) {},
			wantError:   "auth.webauthn.rp_origins entry \"https://BAD.EXAMPLE.COM/path\" must not include userinfo, path, query, or fragment",
			wantName:    "TokenRouter",
			wantID:      "example.com",
			wantOrigins: []string{"https://app.example.com", "https://BAD.EXAMPLE.COM/path", "HTTPS://LAST.EXAMPLE.COM"},
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
		before := cfg.Auth.OAuth.OIDC
		require.EqualError(t, cfg.Validate(), step.wantError)
		require.Equal(t, before, cfg.Auth.OAuth.OIDC)
		step.repair(cfg)
	}
	before := cfg.Auth.OAuth.OIDC
	require.NoError(t, cfg.Validate())
	require.Equal(t, before, cfg.Auth.OAuth.OIDC)
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
