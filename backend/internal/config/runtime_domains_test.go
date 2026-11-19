package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

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

// TestLoadDefaultUsageExportConfig inspects default export task settings and
// directory derivation precedence. Loading must retain the 24-hour retention,
// active-task caps, batch size, worker count, and DATA_DIR fallback path.
func TestLoadDefaultUsageExportConfig(t *testing.T) {
	t.Setenv("DATA_DIR", "/tmp/tr-export-data")
	fixture := newConfigFixture(t)

	cfg, err := fixture.Load()
	require.NoError(t, err)
	require.True(t, cfg.Usage.ExportTasks.Enabled)
	require.Equal(t, filepath.Join("/tmp/tr-export-data", "exports", "usage"), cfg.Usage.ExportTasks.Dir)
	require.Equal(t, 24, cfg.Usage.ExportTasks.RetentionHours)
	require.Equal(t, 2, cfg.Usage.ExportTasks.MaxActivePerUser)
	require.Equal(t, 5, cfg.Usage.ExportTasks.MaxActivePerAdmin)
	require.Equal(t, 2000, cfg.Usage.ExportTasks.BatchSize)
	require.Equal(t, 2, cfg.Usage.ExportTasks.WorkerConcurrency)
	require.Equal(t, 10, cfg.Usage.ExportTasks.WorkerIntervalSeconds)
	require.Equal(t, 3600, cfg.Usage.ExportTasks.TaskTimeoutSeconds)
}

// TestValidateUsageExportConfigBounds checks enabled and disabled validation
// rules on UsageExportConfig. Active executors reject zero quotas or budgets,
// while disabled configurations still reject negative numeric fields.
func TestValidateUsageExportConfigBounds(t *testing.T) {
	fixture := newConfigFixture(t)
	cfg, err := fixture.Load()
	require.NoError(t, err)

	cfg.Usage.ExportTasks.Enabled = true
	cfg.Usage.ExportTasks.MaxActivePerUser = 0
	err = cfg.Validate()
	require.ErrorContains(t, err, "usage.export_tasks.max_active_per_user")

	cfg.Usage.ExportTasks.MaxActivePerUser = 2
	cfg.Usage.ExportTasks.Enabled = false
	cfg.Usage.ExportTasks.RetentionHours = -1
	err = cfg.Validate()
	require.ErrorContains(t, err, "usage.export_tasks.retention_hours")
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
