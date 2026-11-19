package config

import (
	"fmt"
	"math"
	"net/netip"
	"os"
	"path/filepath"
	"strings"
)

// runtime_domains.go defines PostgreSQL, Redis, billing, payment, batch-image,
// Creative Studio, Ops, usage, idempotency, model-catalog, and team
// configuration types, connection-string builders, and read-only validators.
// Keeping storage pools, background queues, and retention bounds together lets
// startup validation verify cross-field invariants such as idle-versus-open
// pool caps and deduplication-versus-usage-log retention windows.

// DatabaseConfig configures database connections and their pool.
// Pool limits control connection reuse and resource consumption.
type DatabaseConfig struct {
	Host     string `mapstructure:"host"`     // PostgreSQL server host text
	Port     int    `mapstructure:"port"`     // PostgreSQL connection port number
	User     string `mapstructure:"user"`     // PostgreSQL authentication username text
	Password string `mapstructure:"password"` // PostgreSQL authentication secret text
	DBName   string `mapstructure:"dbname"`   // target PostgreSQL database name
	SSLMode  string `mapstructure:"sslmode"`  // libpq TLS mode text
	// MaxOpenConns and MaxIdleConns bound one shared SQL pool. Startup validation
	// requires a positive open capacity and keeps the nonnegative idle limit
	// within that capacity before database/sql receives them.
	MaxOpenConns int `mapstructure:"max_open_conns"` // open SQL connection capacity
	MaxIdleConns int `mapstructure:"max_idle_conns"` // retained idle SQL connection limit
	// ConnMaxLifetimeMinutes limits a connection's total lifetime in minutes.
	ConnMaxLifetimeMinutes int `mapstructure:"conn_max_lifetime_minutes"`
	// ConnMaxIdleTimeMinutes limits a connection's idle lifetime in minutes.
	ConnMaxIdleTimeMinutes int `mapstructure:"conn_max_idle_time_minutes"`
}

// RedisConfig holds Redis credentials, pool limits, and configured socket
// operation budgets.
type RedisConfig struct {
	Host     string `mapstructure:"host"`     // Redis server host text
	Port     int    `mapstructure:"port"`     // Redis connection port number
	Username string `mapstructure:"username"` // Redis authentication username text
	Password string `mapstructure:"password"` // Redis authentication secret text
	DB       int    `mapstructure:"db"`       // Redis logical database number
	// connection establishment budget seconds
	DialTimeoutSeconds int `mapstructure:"dial_timeout_seconds"`
	// socket read operation seconds
	ReadTimeoutSeconds int `mapstructure:"read_timeout_seconds"`
	// socket write operation seconds
	WriteTimeoutSeconds int `mapstructure:"write_timeout_seconds"`
	// PoolSize and MinIdleConns configure one Redis connection pool. Startup
	// validation requires positive capacity and keeps its nonnegative ready-idle
	// minimum within that capacity before client construction.
	PoolSize int `mapstructure:"pool_size"` // Redis connection pool capacity
	// ready idle Redis connection minimum
	MinIdleConns int `mapstructure:"min_idle_conns"`
	// EnableTLS enables TLS connections.
	EnableTLS bool `mapstructure:"enable_tls"`
}

// BillingConfig supplies pre-forwarding balance reserve and billing
// circuit-breaker deployment settings.
type BillingConfig struct {
	BalanceCheckCircuitBreaker BillingBalanceCircuitBreakerConfig `mapstructure:"balance_check_circuit_breaker"` // billing balance check circuit breaker settings
	// new-user billing rate multiplier
	FallbackRateMultiplier float64 `mapstructure:"fallback_rate_multiplier"`
	// GroupRateCacheTTLSeconds supplies the retained lifetime of user/group
	// rate multipliers in seconds. Startup requires a positive value before the
	// rate cache uses this process default.
	GroupRateCacheTTLSeconds int `mapstructure:"group_rate_cache_ttl_seconds"`
	// MinimumBalanceReserve is the conservative balance threshold before
	// balance-funded forwarding.
	// Zero requires only balance > 0.
	MinimumBalanceReserve float64 `mapstructure:"minimum_balance_reserve"`
}

// BillingBalanceCircuitBreakerConfig bounds balance-check failures and admission during billing circuit recovery.
type BillingBalanceCircuitBreakerConfig struct {
	// billing circuit availability switch
	Enabled bool `mapstructure:"enabled"`
	// failure count opening the circuit
	FailureThreshold int `mapstructure:"failure_threshold"`
	// half-open probe admission count
	HalfOpenRequests int `mapstructure:"half_open_requests"`
	// open-circuit reset delay seconds
	ResetTimeoutSeconds int `mapstructure:"reset_timeout_seconds"`
}

// PaymentConfig supplies the explicit signing input for payment-resume token construction.
type PaymentConfig struct {
	ResumeSigningKey string `mapstructure:"resume_signing_key"` // Explicit payment resume signing key.
}

// BatchImageConfig holds deployment inputs for batch image limits, storage,
// queueing, and cleanup.
type BatchImageConfig struct {
	// cleanup job batch size
	CleanupBatchSize int `mapstructure:"cleanup_batch_size"`
	// batch cleanup polling minutes
	CleanupIntervalMinutes int `mapstructure:"cleanup_interval_minutes"`
	// default requested image size text
	DefaultImageSize string `mapstructure:"default_image_size"`
	// default output image MIME type
	DefaultResponseMimeType string `mapstructure:"default_response_mime_type"`
	// terminal job input retention hours
	InputRetentionAfterTerminalHours int `mapstructure:"input_retention_after_terminal_hours"`
	// download request byte count limit
	MaxDownloadBytesPerRequest int64 `mapstructure:"max_download_bytes_per_request"`
	// per-user download admission limit
	MaxDownloadConcurrencyPerUser int `mapstructure:"max_download_concurrency_per_user"`
	// download request budget seconds
	MaxDownloadDurationSeconds int `mapstructure:"max_download_duration_seconds"`
	// maximum bundled ZIP item count
	MaxDownloadItemsZip int `mapstructure:"max_download_items_zip"`
	// ordinary job item count limit
	MaxItemsPerJob int `mapstructure:"max_items_per_job"`
	// per-item output image count limit
	MaxOutputImagesPerItem int `mapstructure:"max_output_images_per_item"`
	// total output image count limit
	MaxOutputImagesPerJob int `mapstructure:"max_output_images_per_job"`
	// MaxPromptBytesPerItem limits the UTF-8 bytes of each trimmed batch prompt.
	MaxPromptBytesPerItem int `mapstructure:"max_prompt_bytes_per_item"`
	// expanded reference image count limit
	MaxReferenceImagesPerJob int `mapstructure:"max_reference_images_per_job"`
	// expanded inline reference byte limit
	MaxReferenceInlineBytesPerJob int `mapstructure:"max_reference_inline_bytes_per_job"`
	// terminal job output retention hours
	OutputRetentionAfterTerminalHours int                    `mapstructure:"output_retention_after_terminal_hours"`
	Queue                             TaskQueueConfig        `mapstructure:"queue"`  // batch image queue settings
	Vertex                            BatchImageVertexConfig `mapstructure:"vertex"` // batch image vertex settings
}

// BatchImageVertexConfig selects managed Vertex batch storage, object prefixes, and endpoint overrides.
type BatchImageVertexConfig struct {
	// Vertex batch endpoint override URL
	BatchPredictionBaseURL string `mapstructure:"batch_prediction_base_url"`
	// Cloud Storage endpoint override URL
	GCSBaseURL string `mapstructure:"gcs_base_url"`
	// Vertex batch region identifier
	Location string `mapstructure:"location"`
	// ManagedGCSBucket is a server-owned bucket for batch JSONL input/output.
	// Disable Cloud Storage soft delete on this bucket to avoid retaining deleted
	// batch objects.
	ManagedGCSBucket string `mapstructure:"managed_gcs_bucket"`
	// ManagedGCSPrefix identifies managed object paths and must contain
	// {batch_id} while Vertex is enabled. That placeholder separates objects
	// belonging to different batch jobs in the managed bucket.
	ManagedGCSPrefix string `mapstructure:"managed_gcs_prefix"`
	// Vertex batch project identifier
	ProjectID string `mapstructure:"project_id"`
}

// CreativeConfig supplies studio input limits and process defaults for its
// worker queue.
type CreativeConfig struct {
	// default requested image size text
	DefaultImageSize string `mapstructure:"default_image_size"`
	// Enabled controls Creative Studio HTTP API availability.
	Enabled bool `mapstructure:"enabled"`
	// ExecuteTimeoutSeconds bounds one upstream execution attempt.
	ExecuteTimeoutSeconds int `mapstructure:"execute_timeout_seconds"`
	// MaxAssetBytes limits each uploaded source image or mask in bytes.
	MaxAssetBytes int64 `mapstructure:"max_asset_bytes"`
	// MaxExecuteAttempts limits attempted upstream executions after transient
	// provider errors. The count includes the initial execution before any retry,
	// and enabled startup validation requires a positive value.
	MaxExecuteAttempts int `mapstructure:"max_execute_attempts"`
	// MaxPromptChars limits prompt text by Unicode rune count in creative.Public
	// validation. It differs from the batch image prompt field, whose consumer
	// counts trimmed bytes.
	MaxPromptChars int `mapstructure:"max_prompt_chars"`
	// MaxTotalInputBytes limits the total input size of one task in bytes.
	MaxTotalInputBytes int64           `mapstructure:"max_total_input_bytes"`
	Queue              TaskQueueConfig `mapstructure:"queue"` // creative queue settings
	// QueueWorkerEnabled enables the Creative Studio worker queue.
	QueueWorkerEnabled bool `mapstructure:"queue_worker_enabled"`
	// TransientTTLSeconds is the retention for input payloads and temporary output.
	TransientTTLSeconds int `mapstructure:"transient_ttl_seconds"`
}

// TaskQueueConfig names Redis queues and bounds task locking, retries, and recovery timing.
type TaskQueueConfig struct {
	// Redis active-queue key name
	ActiveKey string `mapstructure:"active_key"`
	// ordinary requeue delay seconds
	DefaultRequeueDelaySeconds int `mapstructure:"default_requeue_delay_seconds"`
	// Redis delayed-queue key name
	DelayedKey string `mapstructure:"delayed_key"`
	// delayed-task mover batch count
	DelayedMoveLimit int `mapstructure:"delayed_move_limit"`
	// delayed-task mover interval seconds
	DelayedMoverIntervalSeconds int `mapstructure:"delayed_mover_interval_seconds"`
	// provider-error retry delay seconds
	ErrorRetryDelaySeconds int `mapstructure:"error_retry_delay_seconds"`
	// Redis active-task key prefix
	InflightKeyPrefix string `mapstructure:"inflight_key_prefix"`
	// active-task marker lifetime seconds
	InflightTTLSeconds int `mapstructure:"inflight_ttl_seconds"`
	// job-lock lifetime in seconds
	JobLockTTLSeconds int `mapstructure:"job_lock_ttl_seconds"`
	// occupied job-lock retry seconds
	LockConflictDelaySeconds int `mapstructure:"lock_conflict_delay_seconds"`
	// Redis job-lock key prefix
	LockKeyPrefix string `mapstructure:"lock_key_prefix"`
	// Redis ready-queue key name
	ReadyKey string `mapstructure:"ready_key"`
	// active-task recovery batch count
	RecoverLimit int `mapstructure:"recover_limit"`
	// active-task recovery interval seconds
	RecoveryIntervalSeconds int `mapstructure:"recovery_interval_seconds"`
	// active-task recovery threshold seconds
	StaleActiveAfterSeconds int `mapstructure:"stale_active_after_seconds"`
}

// OpsConfig supplies deployment availability, cleanup, aggregation, and
// collector cache defaults.
type OpsConfig struct {
	Aggregation OpsAggregationConfig `mapstructure:"aggregation"` // ops aggregation settings
	Cleanup     OpsCleanupConfig     `mapstructure:"cleanup"`     // ops cleanup settings
	// Enabled controls whether ops features should run.
	//
	// The database setting ops_monitoring_enabled controls runtime availability
	// when this deployment-level switch is enabled.
	Enabled   bool               `mapstructure:"enabled"`
	WebSocket OpsWebSocketConfig `mapstructure:"websocket"` // ops websocket settings
}

// OpsCleanupConfig supplies the schedule, batch pacing, and retention of Ops
// datasets.
type OpsCleanupConfig struct {
	Enabled   bool   `mapstructure:"enabled"`    // Ops cleanup job availability switch
	Schedule  string `mapstructure:"schedule"`   // Ops cleanup cron expression
	BatchSize int    `mapstructure:"batch_size"` // deleted rows per cleanup batch
	// cleanup batch pause milliseconds
	BatchPauseMS int `mapstructure:"batch_pause_ms"`

	// ErrorLogRetentionDays, SystemLogRetentionDays, MinuteMetricsRetentionDays,
	// and HourlyMetricsRetentionDays supply day windows for their corresponding
	// cleanup datasets. They default to thirty days; the cleanup consumer
	// interprets zero as clearing that target dataset.
	// error log retention days
	ErrorLogRetentionDays int `mapstructure:"error_log_retention_days"`
	// system log retention days
	SystemLogRetentionDays int `mapstructure:"system_log_retention_days"`
	// minute metrics retention days
	MinuteMetricsRetentionDays int `mapstructure:"minute_metrics_retention_days"`
	// hourly metrics retention days
	HourlyMetricsRetentionDays int `mapstructure:"hourly_metrics_retention_days"`
}

// OpsAggregationConfig supplies deployment availability for operational metrics
// preaggregation jobs.
type OpsAggregationConfig struct {
	Enabled bool `mapstructure:"enabled"` // Ops aggregation job availability switch
}

// OpsWebSocketConfig bounds administrator WebSocket admission and defines handshake proxy trust.
type OpsWebSocketConfig struct {
	MaxConns       int      `mapstructure:"max_conns"`        // Global websocket connection admission limit.
	MaxConnsPerIP  int      `mapstructure:"max_conns_per_ip"` // Per-address admission limit; zero disables it.
	OriginPolicy   string   `mapstructure:"origin_policy"`    // strict or permissive missing-Origin policy.
	TrustProxy     bool     `mapstructure:"trust_proxy"`      // Trust forwarded addresses from configured peers.
	TrustedProxies []string `mapstructure:"trusted_proxies"`  // Trusted peer addresses and CIDR ranges.
}

// UsageConfig groups request-record queues, dashboard caches, aggregation, retention, cleanup, and export tasks.
type UsageConfig struct {
	Aggregation    UsageAggregationConfig   `mapstructure:"aggregation"`     // usage aggregation settings
	CleanupTasks   UsageCleanupConfig       `mapstructure:"cleanup_tasks"`   // usage cleanup tasks settings
	DashboardCache DashboardCacheConfig     `mapstructure:"dashboard_cache"` // usage dashboard cache settings
	ExportTasks    UsageExportConfig        `mapstructure:"export_tasks"`    // usage export tasks settings
	RecordQueue    GatewayUsageRecordConfig `mapstructure:"record_queue"`    // usage record queue settings
	Retention      UsageRetentionConfig     `mapstructure:"retention"`       // usage retention settings
}

// UsageAggregationConfig sets background aggregation cadence, backfill limits, and aggregate retention periods.
type UsageAggregationConfig struct {
	// BackfillEnabled permits full backfills.
	BackfillEnabled bool `mapstructure:"backfill_enabled"`
	// BackfillMaxDays limits the time span of a backfill.
	BackfillMaxDays int `mapstructure:"backfill_max_days"`
	// daily aggregate retention days
	DailyRetentionDays int `mapstructure:"daily_retention_days"`
	// Enabled enables preaggregation jobs.
	Enabled bool `mapstructure:"enabled"`
	// hourly aggregate retention days
	HourlyRetentionDays int `mapstructure:"hourly_retention_days"`
	// aggregate refresh interval seconds
	IntervalSeconds int `mapstructure:"interval_seconds"`
	LookbackSeconds int `mapstructure:"lookback_seconds"` // recent data lookback seconds
	// RecomputeDays is the number of recent days recomputed at startup.
	RecomputeDays int `mapstructure:"recompute_days"`
}

// UsageCleanupConfig supplies background cleanup range, batching, polling, and
// execution budgets.
type UsageCleanupConfig struct {
	// Enabled enables the cleanup executor.
	Enabled bool `mapstructure:"enabled"`
	// MaxRangeDays limits the time span of one cleanup task.
	MaxRangeDays int `mapstructure:"max_range_days"`
	// BatchSize limits rows deleted per batch.
	BatchSize int `mapstructure:"batch_size"`
	// cleanup task polling interval seconds
	WorkerIntervalSeconds int `mapstructure:"worker_interval_seconds"`
	// cleanup task execution budget seconds
	TaskTimeoutSeconds int `mapstructure:"task_timeout_seconds"`
}

// UsageExportConfig supplies background CSV export storage, retention, active-task
// quotas, cursor batching, worker concurrency, polling, and execution budgets.
type UsageExportConfig struct {
	// Enabled enables background usage export task creation and worker execution.
	Enabled bool `mapstructure:"enabled"`
	// Dir selects the local directory holding generated CSV artifacts; normalization
	// derives <DATA_DIR or storage.dir or ./data>/exports/usage when omitted.
	Dir string `mapstructure:"dir"`
	// RetentionHours sets how long completed CSV artifacts stay downloadable before expiration cleanup.
	RetentionHours int `mapstructure:"retention_hours"`
	// MaxActivePerUser and MaxActivePerAdmin cap concurrent pending or running export
	// tasks per regular user and per administrator respectively.
	MaxActivePerUser  int `mapstructure:"max_active_per_user"`  // per-user active task cap
	MaxActivePerAdmin int `mapstructure:"max_active_per_admin"` // per-admin active task cap
	// BatchSize limits usage log rows scanned per keyset cursor batch.
	BatchSize int `mapstructure:"batch_size"`
	// WorkerConcurrency sets the number of background export workers in the process.
	WorkerConcurrency int `mapstructure:"worker_concurrency"`
	// export worker sweep interval seconds
	WorkerIntervalSeconds int `mapstructure:"worker_interval_seconds"`
	// export task execution budget seconds
	TaskTimeoutSeconds int `mapstructure:"task_timeout_seconds"`
}

// UsageRetentionConfig supplies independent retention periods for usage logs and billing deduplication.
type UsageRetentionConfig struct {
	// billing deduplication retention days
	BillingDedupDays int `mapstructure:"billing_dedup_days"`
	// LogsDays and UsageBillingDedupDays retain raw usage and billing
	// deduplication records in days. Startup validation keeps deduplication
	// retention at least as long as raw-record retention when both are positive,
	// including disabled aggregation settings.
	LogsDays int `mapstructure:"logs_days"` // raw usage record retention days
}

// DashboardCacheConfig supplies Redis statistics freshness, total retention,
// and refresh timeout settings.
type DashboardCacheConfig struct {
	// Enabled enables dashboard caching.
	Enabled bool `mapstructure:"enabled"`
	// KeyPrefix isolates Redis keys across deployments.
	KeyPrefix string `mapstructure:"key_prefix"`
	// StatsFreshTTLSeconds and StatsTTLSeconds separate the fresh period from
	// total Redis retention. Enabled startup validation requires positive values
	// and bounds freshness by total lifetime, allowing retained statistics to
	// outlive their fresh period.
	// statistics freshness duration seconds
	StatsFreshTTLSeconds int `mapstructure:"stats_fresh_ttl_seconds"`
	// total statistics retention seconds
	StatsTTLSeconds int `mapstructure:"stats_ttl_seconds"`
	// background refresh budget seconds
	StatsRefreshTimeoutSeconds int `mapstructure:"stats_refresh_timeout_seconds"`
}

// IdempotencyConfig supplies record lifetimes, observation mode, and
// coordinator maintenance limits.
type IdempotencyConfig struct {
	// CleanupBatchSize limits the records removed in one cleanup batch.
	CleanupBatchSize int `mapstructure:"cleanup_batch_size"`
	// expired-record cleanup interval seconds
	CleanupIntervalSeconds int `mapstructure:"cleanup_interval_seconds"`
	// ordinary write record retention seconds
	DefaultTTLSeconds int `mapstructure:"default_ttl_seconds"`
	// failed-operation retry delay seconds
	FailedRetryBackoffSeconds int `mapstructure:"failed_retry_backoff_seconds"`
	// MaxStoredResponseBytes limits persisted response bodies in bytes.
	MaxStoredResponseBytes int `mapstructure:"max_stored_response_bytes"`
	// ObserveOnly permits a missing request key even for commands requiring one.
	// Present keys still use the coordinator; false allows those command
	// requirements to reject missing keys.
	ObserveOnly bool `mapstructure:"observe_only"`
	// processing record lock timeout seconds
	ProcessingTimeoutSeconds int `mapstructure:"processing_timeout_seconds"`
	// system operation record retention seconds
	SystemOperationTTLSeconds int `mapstructure:"system_operation_ttl_seconds"`
}

// ModelCatalogConfig selects remote catalog data, custom supplements, and periodic source checks.
type ModelCatalogConfig struct {
	// Model price and attribute catalog URL in models.dev format.
	CatalogURL string `mapstructure:"catalog_url"`
	// Catalog and local-file check interval, in minutes.
	CheckIntervalMinutes int `mapstructure:"check_interval_minutes"`
	// SupplementFile names the optional custom supplement used alongside catalog and
	// embedded data. Empty text skips that custom source, and normalization
	// migrates only missing recognized packaged paths in memory.
	SupplementFile string `mapstructure:"supplement_file"`
}

// TeamConfig sets the default availability and limits of team features.
type TeamConfig struct {
	// initial team membership limit
	DefaultMaxMembers int  `mapstructure:"default_max_members"`
	Enabled           bool `mapstructure:"enabled"` // deployment team availability flag
	// user team creation availability flag
	SelfServiceEnabled bool `mapstructure:"self_service_enabled"`
}

// UserDefaultsConfig supplies fallback concurrency and balance for administrator-managed user grants.
type UserDefaultsConfig struct {
	// new-user initial balance amount
	Balance float64 `mapstructure:"balance"`
	// new-user concurrent request limit
	Concurrency int `mapstructure:"concurrency"`
}

// RequestLogConfig sets retention in days for request summaries.
type RequestLogConfig struct {
	RetentionDays int `mapstructure:"retention_days"` // request summary retention days
}

// DSN formats nonnil PostgreSQL settings as an interpolated libpq keyword
// string. Empty password omits that parameter, and remaining fields are
// inserted without quoting, so callers supply values valid in this DSN
// representation.
func (d *DatabaseConfig) DSN() string {
	// Empty keyword password text would run into the next interpolated DSN field.
	// Omit the parameter so libpq can parse the remaining unquoted connection
	// values as intended.
	if d.Password == "" {
		return fmt.Sprintf(
			"host=%s port=%d user=%s dbname=%s sslmode=%s",
			d.Host, d.Port, d.User, d.DBName, d.SSLMode,
		)
	}
	return fmt.Sprintf(
		"host=%s port=%d user=%s password=%s dbname=%s sslmode=%s",
		d.Host, d.Port, d.User, d.Password, d.DBName, d.SSLMode,
	)
}

// DSNWithTimezone formats the interpolated libpq settings with the supplied
// timezone, using Asia/Shanghai for empty tz. Empty password is omitted, and
// caller-supplied field text must be valid for unquoted keyword DSN input.
func (d *DatabaseConfig) DSNWithTimezone(tz string) string {
	if tz == "" {
		tz = "Asia/Shanghai"
	}
	// Empty keyword password text would run into the next interpolated DSN field.
	// Omit the parameter so libpq can parse the remaining unquoted connection
	// values as intended.
	if d.Password == "" {
		return fmt.Sprintf(
			"host=%s port=%d user=%s dbname=%s sslmode=%s TimeZone=%s",
			d.Host, d.Port, d.User, d.DBName, d.SSLMode, tz,
		)
	}
	return fmt.Sprintf(
		"host=%s port=%d user=%s password=%s dbname=%s sslmode=%s TimeZone=%s",
		d.Host, d.Port, d.User, d.Password, d.DBName, d.SSLMode, tz,
	)
}

// Address formats the nonnil Redis settings as host text followed by a colon
// and port. The host is retained as supplied without IPv6 bracketing, so
// callers provide address spelling accepted by the client.
func (r *RedisConfig) Address() string {
	return fmt.Sprintf("%s:%d", r.Host, r.Port)
}

// normalize trims the configured export directory and derives a fallback path under
// DATA_DIR, storage.dir, or ./data when omitted. Explicit directory values retain
// precedence and no filesystem probes or directory creations happen during load.
func (e *UsageExportConfig) normalize(dataDir string) {
	e.Dir = strings.TrimSpace(e.Dir)
	if e.Dir == "" {
		base := strings.TrimSpace(os.Getenv("DATA_DIR"))
		if base == "" {
			base = strings.TrimSpace(dataDir)
		}
		if base == "" {
			base = "./data"
		}
		e.Dir = filepath.Join(base, "exports", "usage")
	}
}

// validate checks numeric bounds on usage export task settings. Enabled execution
// requires positive retention, active-task caps, batch size, worker concurrency,
// sweep interval, and task timeout; disabled execution rejects negative values.
func (e UsageExportConfig) validate() error {
	if e.Enabled {
		if e.RetentionHours <= 0 {
			return fmt.Errorf("usage.export_tasks.retention_hours must be positive")
		}
		if e.MaxActivePerUser <= 0 {
			return fmt.Errorf("usage.export_tasks.max_active_per_user must be positive")
		}
		if e.MaxActivePerAdmin <= 0 {
			return fmt.Errorf("usage.export_tasks.max_active_per_admin must be positive")
		}
		if e.BatchSize <= 0 {
			return fmt.Errorf("usage.export_tasks.batch_size must be positive")
		}
		if e.WorkerConcurrency <= 0 {
			return fmt.Errorf("usage.export_tasks.worker_concurrency must be positive")
		}
		if e.WorkerIntervalSeconds <= 0 {
			return fmt.Errorf("usage.export_tasks.worker_interval_seconds must be positive")
		}
		if e.TaskTimeoutSeconds <= 0 {
			return fmt.Errorf("usage.export_tasks.task_timeout_seconds must be positive")
		}
		return nil
	}
	if e.RetentionHours < 0 {
		return fmt.Errorf("usage.export_tasks.retention_hours must be non-negative")
	}
	if e.MaxActivePerUser < 0 {
		return fmt.Errorf("usage.export_tasks.max_active_per_user must be non-negative")
	}
	if e.MaxActivePerAdmin < 0 {
		return fmt.Errorf("usage.export_tasks.max_active_per_admin must be non-negative")
	}
	if e.BatchSize < 0 {
		return fmt.Errorf("usage.export_tasks.batch_size must be non-negative")
	}
	if e.WorkerConcurrency < 0 {
		return fmt.Errorf("usage.export_tasks.worker_concurrency must be non-negative")
	}
	if e.WorkerIntervalSeconds < 0 {
		return fmt.Errorf("usage.export_tasks.worker_interval_seconds must be non-negative")
	}
	if e.TaskTimeoutSeconds < 0 {
		return fmt.Errorf("usage.export_tasks.task_timeout_seconds must be non-negative")
	}
	return nil
}

// validateOpsWebSocket checks Ops WebSocket origin policy, connection caps, and
// trusted proxy address or CIDR entries.
func (c *Config) validateOpsWebSocket() error {
	if c.Ops.WebSocket.OriginPolicy != "strict" && c.Ops.WebSocket.OriginPolicy != "permissive" {
		return fmt.Errorf("ops.websocket.origin_policy must be strict or permissive")
	}
	if c.Ops.WebSocket.MaxConns <= 0 || c.Ops.WebSocket.MaxConns > math.MaxInt32 {
		return fmt.Errorf("ops.websocket.max_conns must be between 1 and 2147483647")
	}
	if c.Ops.WebSocket.MaxConnsPerIP < 0 || c.Ops.WebSocket.MaxConnsPerIP > math.MaxInt32 {
		return fmt.Errorf("ops.websocket.max_conns_per_ip must be between 0 and 2147483647")
	}
	for _, raw := range c.Ops.WebSocket.TrustedProxies {
		if _, err := netip.ParseAddr(raw); err == nil {
			continue
		}
		if _, err := netip.ParsePrefix(raw); err != nil {
			return fmt.Errorf("ops.websocket.trusted_proxies must contain IP addresses or CIDRs")
		}
	}
	return nil
}

// validateRequestLog checks non-negative request summary retention days.
func (c *Config) validateRequestLog() error {
	if c.RequestLog.RetentionDays < 0 {
		return fmt.Errorf("request_log.retention_days must be non-negative")
	}
	return nil
}

// validateRuntimeDomains checks billing, database, Redis, batch-image, Creative
// Studio, usage cache/aggregation/cleanup/export, and idempotency settings in
// startup order.
func (c *Config) validateRuntimeDomains() error {
	if c.Billing.BalanceCheckCircuitBreaker.Enabled {
		if c.Billing.BalanceCheckCircuitBreaker.FailureThreshold <= 0 {
			return fmt.Errorf("billing.balance_check_circuit_breaker.failure_threshold must be positive")
		}
		if c.Billing.BalanceCheckCircuitBreaker.ResetTimeoutSeconds <= 0 {
			return fmt.Errorf("billing.balance_check_circuit_breaker.reset_timeout_seconds must be positive")
		}
		if c.Billing.BalanceCheckCircuitBreaker.HalfOpenRequests <= 0 {
			return fmt.Errorf("billing.balance_check_circuit_breaker.half_open_requests must be positive")
		}
	}
	if c.Billing.MinimumBalanceReserve < 0 {
		return fmt.Errorf("billing.minimum_balance_reserve must be non-negative")
	}
	if c.Database.MaxOpenConns <= 0 {
		return fmt.Errorf("database.max_open_conns must be positive")
	}
	if c.Database.MaxIdleConns < 0 {
		return fmt.Errorf("database.max_idle_conns must be non-negative")
	}
	// Idle connections belong to the open pool. Keep their retained maximum within
	// its total capacity before configuring the shared SQL pool for application
	// storage.
	if c.Database.MaxIdleConns > c.Database.MaxOpenConns {
		return fmt.Errorf("database.max_idle_conns cannot exceed database.max_open_conns")
	}
	if c.Database.ConnMaxLifetimeMinutes < 0 {
		return fmt.Errorf("database.conn_max_lifetime_minutes must be non-negative")
	}
	if c.Database.ConnMaxIdleTimeMinutes < 0 {
		return fmt.Errorf("database.conn_max_idle_time_minutes must be non-negative")
	}
	if c.Redis.DialTimeoutSeconds <= 0 {
		return fmt.Errorf("redis.dial_timeout_seconds must be positive")
	}
	if c.Redis.ReadTimeoutSeconds <= 0 {
		return fmt.Errorf("redis.read_timeout_seconds must be positive")
	}
	if c.Redis.WriteTimeoutSeconds <= 0 {
		return fmt.Errorf("redis.write_timeout_seconds must be positive")
	}
	if c.Redis.PoolSize <= 0 {
		return fmt.Errorf("redis.pool_size must be positive")
	}
	if c.Redis.MinIdleConns < 0 {
		return fmt.Errorf("redis.min_idle_conns must be non-negative")
	}
	// The ready idle minimum is part of the Redis pool. Bound that minimum by
	// configured capacity before the client creates or replenishes its idle
	// connections.
	if c.Redis.MinIdleConns > c.Redis.PoolSize {
		return fmt.Errorf("redis.min_idle_conns cannot exceed redis.pool_size")
	}
	// Batch workers start with the application, so queue settings must support
	// processing and recovery even when every group currently closes new submissions.
	if strings.TrimSpace(c.BatchImage.Queue.ReadyKey) == "" {
		return fmt.Errorf("batch_image.queue.ready_key must not be empty")
	}
	if strings.TrimSpace(c.BatchImage.Queue.DelayedKey) == "" {
		return fmt.Errorf("batch_image.queue.delayed_key must not be empty")
	}
	if strings.TrimSpace(c.BatchImage.Queue.ActiveKey) == "" {
		return fmt.Errorf("batch_image.queue.active_key must not be empty")
	}
	if strings.TrimSpace(c.BatchImage.Queue.InflightKeyPrefix) == "" {
		return fmt.Errorf("batch_image.queue.inflight_key_prefix must not be empty")
	}
	if strings.TrimSpace(c.BatchImage.Queue.LockKeyPrefix) == "" {
		return fmt.Errorf("batch_image.queue.lock_key_prefix must not be empty")
	}
	if c.BatchImage.Queue.InflightTTLSeconds <= 0 {
		return fmt.Errorf("batch_image.queue.inflight_ttl_seconds must be positive")
	}
	if c.BatchImage.Queue.JobLockTTLSeconds <= 0 {
		return fmt.Errorf("batch_image.queue.job_lock_ttl_seconds must be positive")
	}
	if c.BatchImage.Queue.StaleActiveAfterSeconds <= 0 {
		return fmt.Errorf("batch_image.queue.stale_active_after_seconds must be positive")
	}
	if c.BatchImage.Queue.DelayedMoveLimit <= 0 {
		return fmt.Errorf("batch_image.queue.delayed_move_limit must be positive")
	}
	if c.BatchImage.Queue.RecoverLimit <= 0 {
		return fmt.Errorf("batch_image.queue.recover_limit must be positive")
	}

	// Managed Vertex objects require a bucket name and a batch-specific prefix.
	// Validate the per-batch identity placeholder before these settings are passed
	// to the managed batch adapter.
	if strings.TrimSpace(c.BatchImage.Vertex.ManagedGCSBucket) != "" {
		if strings.Contains(c.BatchImage.Vertex.ManagedGCSBucket, "://") {
			return fmt.Errorf("batch_image.vertex.managed_gcs_bucket must be a bucket name, not a URI")
		}
		if strings.TrimSpace(c.BatchImage.Vertex.Location) == "" {
			return fmt.Errorf("batch_image.vertex.location must not be empty when a Vertex bucket is configured")
		}
		if strings.TrimSpace(c.BatchImage.Vertex.ManagedGCSPrefix) == "" {
			return fmt.Errorf("batch_image.vertex.managed_gcs_prefix must not be empty when a Vertex bucket is configured")
		}
		if !strings.Contains(c.BatchImage.Vertex.ManagedGCSPrefix, "{batch_id}") {
			return fmt.Errorf("batch_image.vertex.managed_gcs_prefix must contain {batch_id}")
		}
	}
	// Creative input limits are checked when its API is enabled, while queue
	// requirements are checked by their separate flag. Keep total input capacity
	// at least large enough for one allowed asset.
	if c.Creative.Enabled {
		if c.Creative.TransientTTLSeconds <= 0 {
			return fmt.Errorf("creative.transient_ttl_seconds must be positive")
		}
		if c.Creative.MaxAssetBytes <= 0 {
			return fmt.Errorf("creative.max_asset_bytes must be positive")
		}
		if c.Creative.MaxTotalInputBytes <= 0 {
			return fmt.Errorf("creative.max_total_input_bytes must be positive")
		}
		if c.Creative.MaxTotalInputBytes < c.Creative.MaxAssetBytes {
			return fmt.Errorf("creative.max_total_input_bytes cannot be smaller than creative.max_asset_bytes")
		}
		if c.Creative.MaxPromptChars <= 0 {
			return fmt.Errorf("creative.max_prompt_chars must be positive")
		}
	}
	if c.Creative.QueueWorkerEnabled {
		if strings.TrimSpace(c.Creative.Queue.ReadyKey) == "" {
			return fmt.Errorf("creative.queue.ready_key must not be empty")
		}
		if strings.TrimSpace(c.Creative.Queue.DelayedKey) == "" {
			return fmt.Errorf("creative.queue.delayed_key must not be empty")
		}
		if strings.TrimSpace(c.Creative.Queue.ActiveKey) == "" {
			return fmt.Errorf("creative.queue.active_key must not be empty")
		}
		if strings.TrimSpace(c.Creative.Queue.InflightKeyPrefix) == "" {
			return fmt.Errorf("creative.queue.inflight_key_prefix must not be empty")
		}
		if strings.TrimSpace(c.Creative.Queue.LockKeyPrefix) == "" {
			return fmt.Errorf("creative.queue.lock_key_prefix must not be empty")
		}
		if c.Creative.Queue.InflightTTLSeconds <= 0 {
			return fmt.Errorf("creative.queue.inflight_ttl_seconds must be positive")
		}
		if c.Creative.Queue.JobLockTTLSeconds <= 0 {
			return fmt.Errorf("creative.queue.job_lock_ttl_seconds must be positive")
		}
		if c.Creative.Queue.StaleActiveAfterSeconds <= 0 {
			return fmt.Errorf("creative.queue.stale_active_after_seconds must be positive")
		}
		if c.Creative.Queue.DelayedMoveLimit <= 0 {
			return fmt.Errorf("creative.queue.delayed_move_limit must be positive")
		}
		if c.Creative.Queue.RecoverLimit <= 0 {
			return fmt.Errorf("creative.queue.recover_limit must be positive")
		}
	}
	if c.Creative.Enabled {
		if c.Creative.ExecuteTimeoutSeconds <= 0 {
			return fmt.Errorf("creative.execute_timeout_seconds must be positive")
		}
		if c.Creative.MaxExecuteAttempts < 1 {
			return fmt.Errorf("creative.max_execute_attempts must be at least 1")
		}
	}
	// Enabled dashboard caching needs positive lifetimes and a refresh budget.
	// Keep freshness within total Redis lifetime; disabled settings can be zero
	// but retain nonnegative checks below.
	if c.Usage.DashboardCache.Enabled {
		if c.Usage.DashboardCache.StatsFreshTTLSeconds <= 0 {
			return fmt.Errorf("usage.dashboard_cache.stats_fresh_ttl_seconds must be positive")
		}
		if c.Usage.DashboardCache.StatsTTLSeconds <= 0 {
			return fmt.Errorf("usage.dashboard_cache.stats_ttl_seconds must be positive")
		}
		if c.Usage.DashboardCache.StatsRefreshTimeoutSeconds <= 0 {
			return fmt.Errorf("usage.dashboard_cache.stats_refresh_timeout_seconds must be positive")
		}
		if c.Usage.DashboardCache.StatsFreshTTLSeconds > c.Usage.DashboardCache.StatsTTLSeconds {
			return fmt.Errorf("usage.dashboard_cache.stats_fresh_ttl_seconds must be <= usage.dashboard_cache.stats_ttl_seconds")
		}
	} else {
		if c.Usage.DashboardCache.StatsFreshTTLSeconds < 0 {
			return fmt.Errorf("usage.dashboard_cache.stats_fresh_ttl_seconds must be non-negative")
		}
		if c.Usage.DashboardCache.StatsTTLSeconds < 0 {
			return fmt.Errorf("usage.dashboard_cache.stats_ttl_seconds must be non-negative")
		}
		if c.Usage.DashboardCache.StatsRefreshTimeoutSeconds < 0 {
			return fmt.Errorf("usage.dashboard_cache.stats_refresh_timeout_seconds must be non-negative")
		}
	}
	// Enabled aggregation needs positive retained datasets, and billing
	// deduplication must survive at least as long as raw usage records. Inactive
	// settings retain nonnegative and paired-retention checks below.
	if c.Usage.Aggregation.Enabled {
		if c.Usage.Aggregation.IntervalSeconds <= 0 {
			return fmt.Errorf("usage.aggregation.interval_seconds must be positive")
		}
		if c.Usage.Aggregation.LookbackSeconds < 0 {
			return fmt.Errorf("usage.aggregation.lookback_seconds must be non-negative")
		}
		if c.Usage.Aggregation.BackfillMaxDays < 0 {
			return fmt.Errorf("usage.aggregation.backfill_max_days must be non-negative")
		}
		if c.Usage.Aggregation.BackfillEnabled && c.Usage.Aggregation.BackfillMaxDays == 0 {
			return fmt.Errorf("usage.aggregation.backfill_max_days must be positive")
		}
		if c.Usage.Retention.LogsDays <= 0 {
			return fmt.Errorf("usage.retention.logs_days must be positive")
		}
		if c.Usage.Retention.BillingDedupDays <= 0 {
			return fmt.Errorf("usage.retention.billing_dedup_days must be positive")
		}
		if c.Usage.Retention.BillingDedupDays < c.Usage.Retention.LogsDays {
			return fmt.Errorf("usage.retention.billing_dedup_days must be greater than or equal to usage_logs_days")
		}
		if c.Usage.Aggregation.HourlyRetentionDays <= 0 {
			return fmt.Errorf("usage.aggregation.hourly_retention_days must be positive")
		}
		if c.Usage.Aggregation.DailyRetentionDays <= 0 {
			return fmt.Errorf("usage.aggregation.daily_retention_days must be positive")
		}
		if c.Usage.Aggregation.RecomputeDays < 0 {
			return fmt.Errorf("usage.aggregation.recompute_days must be non-negative")
		}
	} else {
		if c.Usage.Aggregation.IntervalSeconds < 0 {
			return fmt.Errorf("usage.aggregation.interval_seconds must be non-negative")
		}
		if c.Usage.Aggregation.LookbackSeconds < 0 {
			return fmt.Errorf("usage.aggregation.lookback_seconds must be non-negative")
		}
		if c.Usage.Aggregation.BackfillMaxDays < 0 {
			return fmt.Errorf("usage.aggregation.backfill_max_days must be non-negative")
		}
		if c.Usage.Retention.LogsDays < 0 {
			return fmt.Errorf("usage.retention.logs_days must be non-negative")
		}
		if c.Usage.Retention.BillingDedupDays < 0 {
			return fmt.Errorf("usage.retention.billing_dedup_days must be non-negative")
		}
		if c.Usage.Retention.BillingDedupDays > 0 &&
			c.Usage.Retention.LogsDays > 0 &&
			c.Usage.Retention.BillingDedupDays < c.Usage.Retention.LogsDays {
			return fmt.Errorf("usage.retention.billing_dedup_days must be greater than or equal to usage_logs_days")
		}
		if c.Usage.Aggregation.HourlyRetentionDays < 0 {
			return fmt.Errorf("usage.aggregation.hourly_retention_days must be non-negative")
		}
		if c.Usage.Aggregation.DailyRetentionDays < 0 {
			return fmt.Errorf("usage.aggregation.daily_retention_days must be non-negative")
		}
		if c.Usage.Aggregation.RecomputeDays < 0 {
			return fmt.Errorf("usage.aggregation.recompute_days must be non-negative")
		}
	}
	if c.Usage.CleanupTasks.Enabled {
		if c.Usage.CleanupTasks.MaxRangeDays <= 0 {
			return fmt.Errorf("usage.cleanup_tasks.max_range_days must be positive")
		}
		if c.Usage.CleanupTasks.BatchSize <= 0 {
			return fmt.Errorf("usage.cleanup_tasks.batch_size must be positive")
		}
		if c.Usage.CleanupTasks.WorkerIntervalSeconds <= 0 {
			return fmt.Errorf("usage.cleanup_tasks.worker_interval_seconds must be positive")
		}
		if c.Usage.CleanupTasks.TaskTimeoutSeconds <= 0 {
			return fmt.Errorf("usage.cleanup_tasks.task_timeout_seconds must be positive")
		}
	} else {
		if c.Usage.CleanupTasks.MaxRangeDays < 0 {
			return fmt.Errorf("usage.cleanup_tasks.max_range_days must be non-negative")
		}
		if c.Usage.CleanupTasks.BatchSize < 0 {
			return fmt.Errorf("usage.cleanup_tasks.batch_size must be non-negative")
		}
		if c.Usage.CleanupTasks.WorkerIntervalSeconds < 0 {
			return fmt.Errorf("usage.cleanup_tasks.worker_interval_seconds must be non-negative")
		}
		if c.Usage.CleanupTasks.TaskTimeoutSeconds < 0 {
			return fmt.Errorf("usage.cleanup_tasks.task_timeout_seconds must be non-negative")
		}
	}
	if err := c.Usage.ExportTasks.validate(); err != nil {
		return err
	}
	if c.Idempotency.DefaultTTLSeconds <= 0 {
		return fmt.Errorf("idempotency.default_ttl_seconds must be positive")
	}
	if c.Idempotency.SystemOperationTTLSeconds <= 0 {
		return fmt.Errorf("idempotency.system_operation_ttl_seconds must be positive")
	}
	if c.Idempotency.ProcessingTimeoutSeconds <= 0 {
		return fmt.Errorf("idempotency.processing_timeout_seconds must be positive")
	}
	if c.Idempotency.FailedRetryBackoffSeconds <= 0 {
		return fmt.Errorf("idempotency.failed_retry_backoff_seconds must be positive")
	}
	if c.Idempotency.MaxStoredResponseBytes <= 0 {
		return fmt.Errorf("idempotency.max_stored_response_bytes must be positive")
	}
	if c.Idempotency.CleanupIntervalSeconds <= 0 {
		return fmt.Errorf("idempotency.cleanup_interval_seconds must be positive")
	}
	if c.Idempotency.CleanupBatchSize <= 0 {
		return fmt.Errorf("idempotency.cleanup_batch_size must be positive")
	}
	return nil
}

// validateOpsCleanup checks Ops dataset retention bounds, cleanup schedule
// presence, batch size, and batch pause limits.
func (c *Config) validateOpsCleanup() error {
	if c.Ops.Cleanup.ErrorLogRetentionDays < 0 {
		return fmt.Errorf("ops.cleanup.error_log_retention_days must be non-negative")
	}
	if c.Ops.Cleanup.SystemLogRetentionDays < 0 {
		return fmt.Errorf("ops.cleanup.system_log_retention_days must be non-negative")
	}
	if c.Ops.Cleanup.MinuteMetricsRetentionDays < 0 {
		return fmt.Errorf("ops.cleanup.minute_metrics_retention_days must be non-negative")
	}
	if c.Ops.Cleanup.HourlyMetricsRetentionDays < 0 {
		return fmt.Errorf("ops.cleanup.hourly_metrics_retention_days must be non-negative")
	}
	if c.Ops.Cleanup.Enabled && strings.TrimSpace(c.Ops.Cleanup.Schedule) == "" {
		return fmt.Errorf("ops.cleanup.schedule is required when ops.cleanup.enabled=true")
	}
	// Ops cleanup deletes bounded row batches when a dataset has a positive
	// retention window.
	if c.Ops.Cleanup.BatchSize < 100 || c.Ops.Cleanup.BatchSize > 5000 {
		return fmt.Errorf("ops.cleanup.batch_size must be between 100 and 5000")
	}
	// The pause paces successive deletion batches in milliseconds during
	// retained-data cleanup.
	if c.Ops.Cleanup.BatchPauseMS < 1 || c.Ops.Cleanup.BatchPauseMS > 2000 {
		return fmt.Errorf("ops.cleanup.batch_pause_ms must be between 1 and 2000")
	}
	return nil
}
