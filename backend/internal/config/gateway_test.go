package config

import (
	"bytes"
	"encoding/json"
	"log/slog"
	"math"
	"os"
	"path/filepath"
	"strings"
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
			wantLive:  3600,
			repair:    func(c *Config) { c.Idempotency.CleanupBatchSize = 1 },
		},
		{
			wantError: "gateway.max_request_body_bytes must be positive",
			wantLive:  3600,
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
