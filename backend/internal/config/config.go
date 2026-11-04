package config

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"log/slog"
	"math"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"slices"
	"strings"
	"sync/atomic"
	"time"

	"github.com/spf13/viper"

	"github.com/TokenFlux/TokenRouter/internal/identity/authconfig"
	schedulerpolicy "github.com/TokenFlux/TokenRouter/internal/scheduler/policy"
	ippolicy "github.com/TokenFlux/TokenRouter/internal/server/clientip/policy"
	"github.com/TokenFlux/TokenRouter/internal/server/httpconfig"
)

// config.go loads, normalizes, and validates process configuration and
// publishes forwarded-IP settings.
// Startup configuration can distinguish an omitted key from a deliberately
// supplied zero, false, or empty list. Loading registers Viper decode keys,
// resolves file and environment precedence, and maps selected retired inputs
// before the values reach module options. Trusted-proxy and forwarded-header
// lists separately record environment presence so emptiness can replace
// configured data.
//
// Validation mutates selected values while checking coupled startup
// requirements, and an error can follow those mutations. Forwarded-IP policy
// has another lifetime: copied trust/header pairs are atomically published
// after the cell is initialized, while their static startup fields remain
// available to validation. Bootstrap loading can defer JWT secret selection to
// persistent storage; generated TOTP input is marked separately from a
// configured encryption key.

const (
	// These policy strings select how completion work handles failed queue admission.
	// Inline execution, percentage selection, or dropped work have different
	// recorded outcomes.
	UsageRecordOverflowPolicyDrop   = "drop"   // discarded overflow work mode
	UsageRecordOverflowPolicySample = "sample" // percentage inline overflow mode
	UsageRecordOverflowPolicySync   = "sync"   // inline overflow execution mode

	// DefaultCSPPolicy is the default Content-Security-Policy.
	// SecurityHeaders replaces __CSP_NONCE__ with a per-request nonce.
	DefaultCSPPolicy = httpconfig.DefaultCSPPolicy

	// Upstream HTTP connection pools can be shared by proxy, provider, or both.
	// Pool isolation determines connection reuse and resource consumption.
	// ConnectionPoolIsolationProxy shares a pool among providers using the same proxy.
	// This reduces pool count when many providers use a few proxies.
	ConnectionPoolIsolationProxy = "proxy"
	// ConnectionPoolIsolationProvider gives each provider a separate pool.
	ConnectionPoolIsolationProvider = "provider"
	// ConnectionPoolIsolationProviderProxy isolates each provider/proxy pair by default.
	// Requests using the same provider and proxy share a pool.
	ConnectionPoolIsolationProviderProxy = "provider_proxy"

	// DefaultUpstreamResponseReadMaxBytes limits non-streaming upstream response bodies.
	// The 128 MiB limit accommodates large base64-encoded image responses.
	// Override it with gateway.upstream_response_read_max_bytes.
	DefaultUpstreamResponseReadMaxBytes int64 = 128 * 1024 * 1024

	// DefaultModelsListReadMaxBytes limits upstream model-list response bodies.
	// Override it with gateway.models_list_read_max_bytes.
	DefaultModelsListReadMaxBytes int64 = 8 * 1024 * 1024

	// These values supply WeChat Open-mode and frontend callback defaults while
	// normalization resolves omitted deployment text and maps supported legacy
	// credentials.
	defaultWeChatConnectMode             = authconfig.DefaultWeChatConnectMode
	defaultWeChatConnectScopes           = authconfig.DefaultWeChatConnectScopes
	defaultWeChatConnectFrontendRedirect = "/auth/wechat/callback"

	// MaxForwardedClientIPHeaders shares the discovery-source limit with the policy
	// normalizer used by both process configuration and persisted runtime settings.
	MaxForwardedClientIPHeaders = ippolicy.MaxForwardedClientIPHeaders

	// These modes choose immediate rejection or queue waiting when separate image
	// admission reaches its process request capacity in the image limiter.
	ImageConcurrencyOverflowModeReject = "reject" // immediate image-slot rejection mode
	ImageConcurrencyOverflowModeWait   = "wait"   // queued image-slot admission mode

	// DefaultOpenAIWSClientFirstMessageTimeoutSeconds supplies the deployment ingress
	// budget for the first client message before a Responses WebSocket turn begins.
	DefaultOpenAIWSClientFirstMessageTimeoutSeconds = 30
)

var (
	// ErrDingTalkV1AppTypeMismatch shares identity validation's sentinel for an
	// app type conflicting with its configured enterprise restriction in enabled
	// login settings.
	ErrDingTalkV1AppTypeMismatch = authconfig.ErrDingTalkV1AppTypeMismatch

	// ErrDingTalkV4InvalidAppKind shares identity validation's sentinel for an
	// unsupported application kind, allowing facade callers to retain errors.Is identity.
	ErrDingTalkV4InvalidAppKind = authconfig.ErrDingTalkV4InvalidAppKind
)

// RequestLogConfig sets retention in days for request summaries.
type RequestLogConfig struct {
	RetentionDays int `mapstructure:"retention_days"` // request summary retention days
}

// Config holds process startup settings decoded from Viper and supplied to
// application construction. Load normalizes and validates these values, while
// the forwarded-IP accessors separately publish copied live policy snapshots.
type Config struct {
	MCPOAuthSigningSecret string `mapstructure:"-" json:"-"` // Bootstrap-resolved hexadecimal OAuth signing key.

	// request summary retention settings
	RequestLog RequestLogConfig `mapstructure:"request_log"`
	// HTTP ingress and listener settings
	Server ServerConfig `mapstructure:"server"`
	Log    LogConfig    `mapstructure:"log"` // process logging output settings
	// browser cross-origin response settings
	CORS CORSConfig `mapstructure:"cors"`
	// URL and response security settings
	Security SecurityConfig `mapstructure:"security"`
	// forwarding balance protection settings
	Billing BillingConfig `mapstructure:"billing"`
	// captcha deployment requirement settings
	Turnstile TurnstileConfig `mapstructure:"turnstile"`
	// PostgreSQL connection and pool settings
	Database DatabaseConfig `mapstructure:"database"`
	// Redis connection and pool settings
	Redis RedisConfig `mapstructure:"redis"`
	// operational monitoring process settings
	Ops OpsConfig `mapstructure:"ops"`
	JWT JWTConfig `mapstructure:"jwt"` // identity token signing settings
	// secret encryption deployment settings
	Totp TotpConfig `mapstructure:"totp"`
	// passkey relying-party deployment settings
	WebAuthn WebAuthnConfig `mapstructure:"webauthn"`
	// LinuxDo login deployment settings
	LinuxDo LinuxDoConnectConfig `mapstructure:"linuxdo_connect"`
	// WeChat login deployment settings
	WeChat WeChatConnectConfig `mapstructure:"wechat_connect"`
	// OIDC login deployment settings
	OIDC OIDCConnectConfig `mapstructure:"oidc_connect"`
	// DingTalk login deployment settings
	DingTalk DingTalkConnectConfig `mapstructure:"dingtalk_connect"`
	// GitHub login deployment settings
	GitHubOAuth EmailOAuthProviderConfig `mapstructure:"github_oauth"`
	// Google login deployment settings
	GoogleOAuth EmailOAuthProviderConfig `mapstructure:"google_oauth"`
	// initial user and billing defaults
	Default DefaultConfig `mapstructure:"default"`
	// provider cooldown duration settings
	RateLimit RateLimitConfig `mapstructure:"rate_limit"`
	// model catalog source settings
	Pricing PricingConfig `mapstructure:"pricing"`
	// request forwarding process defaults
	Gateway GatewayConfig `mapstructure:"gateway"`
	// API-key authentication cache settings
	APIKeyAuth APIKeyAuthCacheConfig `mapstructure:"api_key_auth_cache"`
	// subscription authentication cache settings
	SubscriptionCache SubscriptionCacheConfig `mapstructure:"subscription_cache"`
	// dashboard Redis cache settings
	Dashboard DashboardCacheConfig `mapstructure:"dashboard_cache"`
	// dashboard aggregation job settings
	DashboardAgg DashboardAggregationConfig `mapstructure:"dashboard_aggregation"`
	// usage record cleanup settings
	UsageCleanup UsageCleanupConfig `mapstructure:"usage_cleanup"`
	// slot-wait keepalive settings
	Concurrency ConcurrencyConfig `mapstructure:"concurrency"`
	// OAuth refresh worker settings
	TokenRefresh TokenRefreshConfig `mapstructure:"token_refresh"`
	// Timezone selects the calendar zone initialized by app/bootstrap after
	// loading. TZ precedes TIMEZONE, selected YAML, and Asia/Shanghai; bootstrap
	// rejects an invalid IANA name before publishing time.Local.
	Timezone string `mapstructure:"timezone"`
	// Gemini OAuth and quota settings
	Gemini GeminiConfig `mapstructure:"gemini"`
	// GitHub release access settings
	Update UpdateConfig `mapstructure:"update"`
	// write coordination process defaults
	Idempotency IdempotencyConfig `mapstructure:"idempotency"`
	// batch image process defaults
	BatchImage BatchImageConfig `mapstructure:"batch_image"`
	// creative studio process defaults
	Creative CreativeConfig `mapstructure:"creative"`
	// team availability and membership defaults
	Team    TeamConfig    `mapstructure:"team"`
	Plugins PluginsConfig `mapstructure:"plugins"` // single-instance plugin runtime settings
}

// TeamConfig sets the default availability and limits of team features.
type TeamConfig struct {
	Enabled bool `mapstructure:"enabled"` // deployment team availability flag
	// user team creation availability flag
	SelfServiceEnabled bool `mapstructure:"self_service_enabled"`
	// initial team membership limit
	DefaultMemberLimit int `mapstructure:"default_member_limit"`
}

// LogConfig holds process logging thresholds, labels, sinks, rotation, and
// sampling controls.
type LogConfig struct {
	Level string `mapstructure:"level"` // minimum process log severity
	// console or JSON encoding selection
	Format string `mapstructure:"format"`
	// record service label text
	ServiceName string `mapstructure:"service_name"`
	Environment string `mapstructure:"env"`    // record environment label text
	Caller      bool   `mapstructure:"caller"` // caller location inclusion flag
	// stacktrace inclusion severity text
	StacktraceLevel string `mapstructure:"stacktrace_level"`
	// stdout and file sink selection
	Output LogOutputConfig `mapstructure:"output"`
	// file rotation and retention settings
	Rotation LogRotationConfig `mapstructure:"rotation"`
	// repeated-message sampling settings
	Sampling LogSamplingConfig `mapstructure:"sampling"`
}

// LogOutputConfig selects stdout and file sinks plus the optional file
// destination.
type LogOutputConfig struct {
	ToStdout bool `mapstructure:"to_stdout"` // standard output sink switch
	ToFile   bool `mapstructure:"to_file"`   // rotating file sink switch
	// FilePath selects the rotating log destination. Empty text lets
	// logging.buildFileCore resolve its default path before creating the parent
	// directory and file writer.
	FilePath string `mapstructure:"file_path"`
}

// LogRotationConfig supplies lumberjack file rotation sizes, retention,
// compression, and naming time.
type LogRotationConfig struct {
	MaxSizeMB  int  `mapstructure:"max_size_mb"`  // rotation size in MiB
	MaxBackups int  `mapstructure:"max_backups"`  // retained rotated file count
	MaxAgeDays int  `mapstructure:"max_age_days"` // rotated file retention days
	Compress   bool `mapstructure:"compress"`     // rotated file compression switch
	LocalTime  bool `mapstructure:"local_time"`   // local-time filename timestamp switch
}

// LogSamplingConfig supplies repeated-message admission counts to the
// configured Zap sampler.
type LogSamplingConfig struct {
	Enabled bool `mapstructure:"enabled"` // repeated-message sampling switch
	// Initial and Thereafter configure repeated messages in the logging sampler's
	// one-second tick. Initial admits the first messages and Thereafter selects
	// the stride after that count; Enabled selects whether sampling runs.
	Initial    int `mapstructure:"initial"`    // initial messages admitted per tick
	Thereafter int `mapstructure:"thereafter"` // subsequent message sampling stride
}

// GeminiConfig holds process OAuth client input and Gemini quota policy
// overrides.
type GeminiConfig struct {
	OAuth GeminiOAuthConfig `mapstructure:"oauth"` // process OAuth client settings
	Quota GeminiQuotaConfig `mapstructure:"quota"` // static quota policy settings
}

// GeminiOAuthConfig supplies optional OAuth client credentials and requested
// scope text.
type GeminiOAuthConfig struct {
	// ClientID and ClientSecret form the configured OAuth client pair. Validate
	// requires both trimmed values to be supplied or both empty, so the consuming
	// client can apply its default credential choice.
	ClientID     string `mapstructure:"client_id"`     // configured OAuth client identifier
	ClientSecret string `mapstructure:"client_secret"` // configured OAuth client secret
	Scopes       string `mapstructure:"scopes"`        // requested OAuth scope text
}

// GeminiQuotaConfig supplies static tier overrides and a JSON policy to Gemini
// quota construction.
type GeminiQuotaConfig struct {
	// static overrides by tier identifier
	Tiers  map[string]GeminiTierQuotaConfig `mapstructure:"tiers"`
	Policy string                           `mapstructure:"policy"` // static JSON quota policy
}

// GeminiTierQuotaConfig carries optional daily quota and cooldown values for a
// named tier.
type GeminiTierQuotaConfig struct {
	// ProRPD is applied when nonnil by the provider quota policy. It overrides a
	// positive shared daily limit when present, otherwise the Pro limit; -1
	// denotes unlimited and values below -1 clamp to zero.
	ProRPD *int64 `mapstructure:"pro_rpd" json:"pro_rpd"`
	// FlashRPD overrides the Flash daily limit when nonnil and the policy has no
	// positive shared limit. A ProRPD update runs first and can change that
	// shared-limit condition before Flash is considered.
	FlashRPD *int64 `mapstructure:"flash_rpd" json:"flash_rpd"`
	// CooldownMinutes replaces the tier cooldown when nonnil. The policy clamps
	// negative minutes to zero before converting the selected value to its stored
	// duration.
	CooldownMinutes *int `mapstructure:"cooldown_minutes" json:"cooldown_minutes"`
}

// UpdateConfig contains the optional proxy address used for GitHub release
// access.
type UpdateConfig struct {
	// ProxyURL is the proxy address for GitHub access.
	// Supported schemes are http, https, socks5, and socks5h.
	// Examples: "http://127.0.0.1:7890", "socks5://127.0.0.1:1080".
	ProxyURL string `mapstructure:"proxy_url"`
}

// IdempotencyConfig supplies record lifetimes, observation mode, and
// coordinator maintenance limits.
type IdempotencyConfig struct {
	// ObserveOnly permits a missing request key even for commands requiring one.
	// Present keys still use the coordinator; false allows those command
	// requirements to reject missing keys.
	ObserveOnly bool `mapstructure:"observe_only"`
	// ordinary write record retention seconds
	DefaultTTLSeconds int `mapstructure:"default_ttl_seconds"`
	// system operation record retention seconds
	SystemOperationTTLSeconds int `mapstructure:"system_operation_ttl_seconds"`
	// processing record lock timeout seconds
	ProcessingTimeoutSeconds int `mapstructure:"processing_timeout_seconds"`
	// failed-operation retry delay seconds
	FailedRetryBackoffSeconds int `mapstructure:"failed_retry_backoff_seconds"`
	// MaxStoredResponseLen limits persisted response bodies in bytes.
	MaxStoredResponseLen int `mapstructure:"max_stored_response_len"`
	// expired-record cleanup interval seconds
	CleanupIntervalSeconds int `mapstructure:"cleanup_interval_seconds"`
	// CleanupBatchSize limits the records removed in one cleanup batch.
	CleanupBatchSize int `mapstructure:"cleanup_batch_size"`
}

// BatchImageConfig holds deployment inputs for batch image limits, storage,
// queueing, and cleanup.
type BatchImageConfig struct {
	// ordinary job item count limit
	MaxItemsPerJobDefault int `mapstructure:"max_items_per_job_default"`
	// total output image count limit
	MaxOutputImagesPerJob int `mapstructure:"max_output_images_per_job"`
	// per-item output image count limit
	MaxOutputImagesPerItem int `mapstructure:"max_output_images_per_item"`
	// MaxPromptCharsPerItem supplies the batch prompt length limit.
	// batchimage.Public validates trimmed prompt bytes with len, so this field
	// counts bytes despite its characters-oriented name.
	MaxPromptCharsPerItem int `mapstructure:"max_prompt_chars_per_item"`
	// expanded reference image count limit
	MaxReferenceImagesPerJob int `mapstructure:"max_reference_images_per_job"`
	// expanded inline reference byte limit
	MaxReferenceInlineBytesPerJob int `mapstructure:"max_reference_inline_bytes_per_job"`
	// default output image MIME type
	DefaultResponseMimeType string `mapstructure:"default_response_mime_type"`
	// default requested image size text
	DefaultImageSize string `mapstructure:"default_image_size"`
	// maximum bundled ZIP item count
	MaxDownloadItemsZip int `mapstructure:"max_download_items_zip"`
	// download request byte count limit
	MaxDownloadBytesPerRequest int64 `mapstructure:"max_download_bytes_per_request"`
	// download request budget seconds
	MaxDownloadDurationSeconds int `mapstructure:"max_download_duration_seconds"`
	// per-user download admission limit
	MaxDownloadConcurrencyPerUser int `mapstructure:"max_download_concurrency_per_user"`
	// terminal job input retention hours
	InputRetentionAfterTerminalHours int `mapstructure:"input_retention_after_terminal_hours"`
	// terminal job output retention hours
	OutputRetentionAfterTerminalHours int `mapstructure:"output_retention_after_terminal_hours"`
	// batch cleanup polling minutes
	CleanupIntervalMinutes int `mapstructure:"cleanup_interval_minutes"`
	// cleanup job batch size
	CleanupBatchSize int `mapstructure:"cleanup_batch_size"`
	// Redis ready-queue key name
	QueueReadyKey string `mapstructure:"queue_ready_key"`
	// Redis delayed-queue key name
	QueueDelayedKey string `mapstructure:"queue_delayed_key"`
	// Redis active-queue key name
	QueueActiveKey string `mapstructure:"queue_active_key"`
	// Redis active-task key prefix
	InflightKeyPrefix string `mapstructure:"inflight_key_prefix"`
	// Redis job-lock key prefix
	LockKeyPrefix string `mapstructure:"lock_key_prefix"`
	// active-task marker lifetime seconds
	InflightTTLSeconds int `mapstructure:"inflight_ttl_seconds"`
	// job-lock lifetime in seconds
	JobLockTTLSeconds int `mapstructure:"job_lock_ttl_seconds"`
	// ordinary requeue delay seconds
	DefaultRequeueDelaySeconds int `mapstructure:"default_requeue_delay_seconds"`
	// provider-error retry delay seconds
	ErrorRetryDelaySeconds int `mapstructure:"error_retry_delay_seconds"`
	// occupied job-lock retry seconds
	LockConflictDelaySeconds int `mapstructure:"lock_conflict_delay_seconds"`
	// active-task recovery threshold seconds
	StaleActiveAfterSeconds int `mapstructure:"stale_active_after_seconds"`
	// delayed-task mover interval seconds
	DelayedMoverIntervalSeconds int `mapstructure:"delayed_mover_interval_seconds"`
	// active-task recovery interval seconds
	RecoveryIntervalSeconds int `mapstructure:"recovery_interval_seconds"`
	// delayed-task mover batch count
	DelayedMoveLimit int `mapstructure:"delayed_move_limit"`
	// active-task recovery batch count
	RecoverLimit int `mapstructure:"recover_limit"`
	// Vertex batch project identifier
	VertexProjectID string `mapstructure:"vertex_project_id"`
	// Vertex batch region identifier
	VertexLocation string `mapstructure:"vertex_location"`
	// VertexManagedGCSBucket is a server-owned bucket for batch JSONL input/output.
	// Disable Cloud Storage soft delete on this bucket to avoid retaining deleted
	// batch objects.
	VertexManagedGCSBucket string `mapstructure:"vertex_managed_gcs_bucket"`
	// VertexManagedGCSPrefix identifies managed object paths and must contain
	// {batch_id} while Vertex is enabled. That placeholder separates objects
	// belonging to different batch jobs in the managed bucket.
	VertexManagedGCSPrefix string `mapstructure:"vertex_managed_gcs_prefix"`
	// Vertex batch endpoint override URL
	VertexBatchPredictionBaseURL string `mapstructure:"vertex_batch_prediction_base_url"`
	// Cloud Storage endpoint override URL
	VertexGCSBaseURL string `mapstructure:"vertex_gcs_base_url"`
}

// CreativeConfig supplies studio input limits and process defaults for its
// worker queue.
type CreativeConfig struct {
	// Enabled controls Creative Studio HTTP API availability.
	Enabled bool `mapstructure:"enabled"`
	// QueueEnabled enables the Creative Studio worker queue.
	QueueEnabled bool `mapstructure:"queue_enabled"`
	// TransientTTLSeconds is the retention for input payloads and temporary output.
	TransientTTLSeconds int `mapstructure:"transient_ttl_seconds"`
	// MaxAssetBytes limits each uploaded source image or mask in bytes.
	MaxAssetBytes int64 `mapstructure:"max_asset_bytes"`
	// MaxTotalInputBytes limits the total input size of one task in bytes.
	MaxTotalInputBytes int64 `mapstructure:"max_total_input_bytes"`
	// MaxPromptChars limits prompt text by Unicode rune count in creative.Public
	// validation. It differs from the batch image prompt field, whose consumer
	// counts trimmed bytes.
	MaxPromptChars int `mapstructure:"max_prompt_chars"`
	// default output image MIME type
	DefaultResponseMimeType string `mapstructure:"default_response_mime_type"`
	// default requested image size text
	DefaultImageSize string `mapstructure:"default_image_size"`
	// Redis ready-queue key name
	QueueReadyKey string `mapstructure:"queue_ready_key"`
	// Redis delayed-queue key name
	QueueDelayedKey string `mapstructure:"queue_delayed_key"`
	// Redis active-queue key name
	QueueActiveKey string `mapstructure:"queue_active_key"`
	// Redis active-run key prefix
	InflightKeyPrefix string `mapstructure:"inflight_key_prefix"`
	// Redis run-lock key prefix
	LockKeyPrefix string `mapstructure:"lock_key_prefix"`
	// active-run marker lifetime seconds
	InflightTTLSeconds int `mapstructure:"inflight_ttl_seconds"`
	// run-lock lifetime in seconds
	JobLockTTLSeconds int `mapstructure:"job_lock_ttl_seconds"`
	// ordinary requeue delay seconds
	DefaultRequeueDelaySeconds int `mapstructure:"default_requeue_delay_seconds"`
	// provider-error retry delay seconds
	ErrorRetryDelaySeconds int `mapstructure:"error_retry_delay_seconds"`
	// occupied run-lock retry seconds
	LockConflictDelaySeconds int `mapstructure:"lock_conflict_delay_seconds"`
	// active-run recovery threshold seconds
	StaleActiveAfterSeconds int `mapstructure:"stale_active_after_seconds"`
	// delayed-run mover interval seconds
	DelayedMoverIntervalSeconds int `mapstructure:"delayed_mover_interval_seconds"`
	// active-run recovery interval seconds
	RecoveryIntervalSeconds int `mapstructure:"recovery_interval_seconds"`
	// delayed-run mover batch count
	DelayedMoveLimit int `mapstructure:"delayed_move_limit"`
	// active-run recovery batch count
	RecoverLimit int `mapstructure:"recover_limit"`
	// ExecuteTimeoutSeconds bounds one upstream execution attempt.
	ExecuteTimeoutSeconds int `mapstructure:"execute_timeout_seconds"`
	// MaxExecuteAttempts limits attempted upstream executions after transient
	// provider errors. The count includes the initial execution before any retry,
	// and enabled startup validation requires a positive value.
	MaxExecuteAttempts int `mapstructure:"max_execute_attempts"`
}

// LinuxDoConnectConfig aliases the identity module's LinuxDo OAuth deployment
// settings.
type LinuxDoConnectConfig = authconfig.LinuxDoConnectConfig

// WeChatConnectConfig aliases identity settings for Open, MP, and Mobile WeChat
// login.
type WeChatConnectConfig = authconfig.WeChatConnectConfig

// OIDCConnectConfig aliases identity settings for issuer discovery and OIDC
// login validation.
type OIDCConnectConfig = authconfig.OIDCConnectConfig

// DingTalkConnectConfig aliases identity settings for DingTalk credentials and
// corporation restrictions.
type DingTalkConnectConfig = authconfig.DingTalkConnectConfig

// EmailOAuthProviderConfig configures email-based OAuth login such as GitHub and Google.
type EmailOAuthProviderConfig = authconfig.EmailOAuthProviderConfig

// TokenRefreshConfig supplies polling, admission, retry, and timeout defaults
// for token refresh workers.
type TokenRefreshConfig struct {
	// Enabled controls automatic token refresh.
	Enabled bool `mapstructure:"enabled"`
	// Interval between refresh checks, in minutes.
	CheckIntervalMinutes int `mapstructure:"check_interval_minutes"`
	// Hours before expiry at which a token becomes eligible for refresh.
	RefreshBeforeExpiryHours float64 `mapstructure:"refresh_before_expiry_hours"`
	// MaxRetries supplies the total attempted refresh exchanges, including the
	// first request. provider.RefreshTuning.Retries selects one attempt for
	// nonpositive input and caps positive counts at ten.
	MaxRetries int `mapstructure:"max_retries"`
	// RetryBackoffSeconds supplies the initial retry delay in seconds.
	// provider.RefreshTuning disables waiting for nonpositive input; positive
	// delays undergo exponential growth, provider/attempt jitter, and a
	// thirty-second final cap.
	RetryBackoffSeconds int `mapstructure:"retry_backoff_seconds"`
	// CandidatePageSize, ProviderConcurrency, ProviderQPS, and
	// ProviderFailureThreshold become bounded maintenance limits through
	// provider.RefreshTuning. Its accessors replace nonpositive values with
	// matching defaults and cap positive values at each owning ceiling.
	// database candidate page size
	CandidatePageSize int `mapstructure:"candidate_page_size"`
	// occupied attempts per platform
	ProviderConcurrency int `mapstructure:"provider_concurrency"`
	ProviderQPS         int `mapstructure:"provider_qps"` // exchange starts per platform second
	// consecutive platform failure trip count
	ProviderFailureThreshold int `mapstructure:"provider_failure_threshold"`
	// AttemptTimeoutSeconds supplies each refresh exchange budget when the caller
	// has no positive override. provider.RefreshTuning uses fifteen seconds for
	// nonpositive input, caps configured positive budgets at five minutes, and can
	// shorten them for a distributed lock lease.
	AttemptTimeoutSeconds int `mapstructure:"attempt_timeout_seconds"`
	// CycleTimeoutSeconds bounds a complete candidate scan, including its page
	// processing. provider.RefreshTuning selects four minutes for nonpositive
	// input and caps positive values at one hour.
	CycleTimeoutSeconds int `mapstructure:"cycle_timeout_seconds"`
}

// PricingConfig supplies catalog download and local supplement inputs for model
// pricing construction.
type PricingConfig struct {
	// Model price and attribute catalog URL in models.dev format.
	RemoteURL string `mapstructure:"remote_url"`
	DataDir   string `mapstructure:"data_dir"` // Local data directory.
	// FallbackFile names the optional custom supplement used alongside catalog and
	// embedded data. Empty text skips that custom source, and normalization
	// migrates only missing recognized packaged paths in memory.
	FallbackFile string `mapstructure:"fallback_file"`
	// Catalog and local-file check interval, in minutes.
	CheckIntervalMinutes int `mapstructure:"check_interval_minutes"`
}

// ServerConfig holds listener, ingress, trusted-proxy, and optional collector
// settings for server construction.
type ServerConfig struct {
	Host string `mapstructure:"host"` // listener host address text
	Port int    `mapstructure:"port"` // listener TCP port number
	// Gin debug or release mode
	Mode               string `mapstructure:"mode"`
	EnableServerTiming bool   `mapstructure:"enable_server_timing"` // Return Server-Timing for admin page requests.
	FrontendURL        string `mapstructure:"frontend_url"`         // Default frontend address for email links and MCP OAuth.
	ReadHeaderTimeout  int    `mapstructure:"read_header_timeout"`  // Request-header read timeout, in seconds.
	MaxHeaderBytes     int    `mapstructure:"max_header_bytes"`     // Header size limit in bytes; maps to the HTTP/2 header-list limit.
	IdleTimeout        int    `mapstructure:"idle_timeout"`         // Idle connection timeout, in seconds.
	// TrustedProxies is the ordered startup list of proxy IPs and CIDRs.
	// TrustedProxiesConfigured distinguishes omission from supplied emptiness,
	// while server/clientip interprets that presence when selecting address
	// handling.
	TrustedProxies []string `mapstructure:"trusted_proxies"`
	// TrustedProxiesConfigured records operator presence independently of list
	// length. An explicitly empty YAML or environment list selects configured
	// proxy handling, and this derived flag is excluded from file decoding.
	TrustedProxiesConfigured bool                          `mapstructure:"-" json:"-" yaml:"-"`
	MaxRequestBodySize       int64                         `mapstructure:"max_request_body_size"`     // Global request-body size limit, in bytes.
	H2C                      H2CConfig                     `mapstructure:"h2c"`                       // HTTP/2 cleartext settings.
	TLSFingerprintCollector  TLSFingerprintCollectorConfig `mapstructure:"tls_fingerprint_collector"` // TLS fingerprint collector settings.
}

// TLSFingerprintCollectorConfig supplies listen settings and session limits for
// fingerprint capture.
type TLSFingerprintCollectorConfig struct {
	Host                 string `mapstructure:"host"`                    // Collector listen address.
	Port                 int    `mapstructure:"port"`                    // Collector listen port.
	PublicBaseURL        string `mapstructure:"public_base_url"`         // Client-accessible collection URL prefix.
	CertFile             string `mapstructure:"cert_file"`               // Optional server certificate path.
	KeyFile              string `mapstructure:"key_file"`                // Optional server private-key path.
	SessionTTLSeconds    int    `mapstructure:"session_ttl_seconds"`     // Collection session lifetime, in seconds.
	MaxRecordsPerSession int    `mapstructure:"max_records_per_session"` // Maximum collection records retained per session.
}

// H2CConfig supplies HTTP/2 cleartext stream, frame, and receive-buffer
// settings.
type H2CConfig struct {
	// HTTP/2 cleartext listener switch
	Enabled                      bool   `mapstructure:"enabled"`
	MaxConcurrentStreams         uint32 `mapstructure:"max_concurrent_streams"`           // Maximum concurrent streams.
	IdleTimeout                  int    `mapstructure:"idle_timeout"`                     // Idle timeout, in seconds.
	MaxReadFrameSize             int    `mapstructure:"max_read_frame_size"`              // Maximum frame size, in bytes.
	MaxUploadBufferPerConnection int    `mapstructure:"max_upload_buffer_per_connection"` // Upload buffer per connection, in bytes.
	MaxUploadBufferPerStream     int    `mapstructure:"max_upload_buffer_per_stream"`     // Upload buffer per stream, in bytes.
}

// CORSConfig aliases the HTTP configuration values consumed by server CORS
// middleware.
type CORSConfig = httpconfig.CORSConfig

// WebAuthnConfig defines this deployment's fixed WebAuthn relying-party identity.
// RPID and RPOrigins must not be inferred from untrusted Host or Origin headers.
type WebAuthnConfig struct {
	Enabled bool `mapstructure:"enabled"` // passkey login availability switch
	// browser relying-party display name
	RPDisplayName string `mapstructure:"rp_display_name"`
	// RPID is the configured relying-party domain used for passkey checks. Enabled
	// validation normalizes it and requires each accepted RPOrigins host to equal
	// it or be its subdomain.
	RPID string `mapstructure:"rp_id"`
	// RPOrigins lists configured browser origins for this relying party. Enabled
	// validation permits HTTPS or local loopback HTTP and replaces accepted
	// entries with normalized scheme/host text before identity construction.
	RPOrigins []string `mapstructure:"rp_origins"`
}

// ForwardedClientIPSettings pairs one live forwarding trust flag with its
// ordered header names.
type ForwardedClientIPSettings struct {
	// TrustForwardedIP and Headers form one policy observation. Config publishes
	// them together in an immutable object and returns a copied header list so
	// readers can retain the pair independently.
	TrustForwardedIP bool     // raw forwarding-header trust switch
	Headers          []string // ordered client-address header names
}

// SecurityConfig holds outbound checks, response policies, proxy controls, and
// startup/live forwarding settings.
type SecurityConfig struct {
	// outbound host admission settings
	URLAllowlist URLAllowlistConfig `mapstructure:"url_allowlist"`
	// upstream response header filtering settings
	ResponseHeaders ResponseHeaderConfig `mapstructure:"response_headers"`
	CSP             CSPConfig            `mapstructure:"csp"` // browser content security policy
	// auxiliary proxy failure handling settings
	ProxyFallback ProxyFallbackConfig `mapstructure:"proxy_fallback"`
	// proxy endpoint probe settings
	ProxyProbe ProxyProbeConfig `mapstructure:"proxy_probe"`
	// TrustForwardedIPForAPIKeyACL lets raw forwarding headers determine client IPs.
	// When disabled, client IP consumers rely on server.trusted_proxies.
	TrustForwardedIPForAPIKeyACL bool `mapstructure:"trust_forwarded_ip_for_api_key_acl"`
	// TrustForwardedIPConfigured records that the operator supplied the switch,
	// so an upgrade can tell an explicit false from the secure default.
	TrustForwardedIPConfigured bool `mapstructure:"-" json:"-" yaml:"-"`
	// startup client-address header names
	ForwardedClientIPHeaders []string `mapstructure:"forwarded_client_ip_headers" json:"forwarded_client_ip_headers" yaml:"forwarded_client_ip_headers"`
	// forwardedClientIPSettingsLive points to the lazily initialized atomic
	// publication cell used by Config policy accessors. Initialize the pointer
	// before concurrent access; subsequent stores replace immutable copied policy
	// objects.
	forwardedClientIPSettingsLive *atomic.Pointer[ForwardedClientIPSettings] `mapstructure:"-" json:"-" yaml:"-"`
}

// URLAllowlistConfig supplies host patterns and private or HTTP admission
// choices for URL checks.
type URLAllowlistConfig struct {
	Enabled bool `mapstructure:"enabled"` // outbound allowlist check switch
	// permitted upstream hostname patterns
	UpstreamHosts []string `mapstructure:"upstream_hosts"`
	// permitted catalog hostname patterns
	PricingHosts []string `mapstructure:"pricing_hosts"`
	// permitted CRS hostname patterns
	CRSHosts []string `mapstructure:"crs_hosts"`
	// private target address admission switch
	AllowPrivateHosts bool `mapstructure:"allow_private_hosts"`
	// AllowInsecureHTTP permits HTTP in format-only operator URL checks when the
	// allowlist is disabled. setDefaults enables this flag; enabled
	// OperatorURLPolicy instead applies its HTTPS allowlist check.
	AllowInsecureHTTP bool `mapstructure:"allow_insecure_http"`
}

// ResponseHeaderConfig supplies allowed and removed header lists to upstream
// response filtering.
type ResponseHeaderConfig struct {
	// configurable response header filtering switch
	Enabled bool `mapstructure:"enabled"`
	// extra permitted response header names
	AdditionalAllowed []string `mapstructure:"additional_allowed"`
	// forced removed response header names
	ForceRemove []string `mapstructure:"force_remove"`
}

// CSPConfig aliases the HTTP content-security-policy values used by server
// response middleware.
type CSPConfig = httpconfig.CSPConfig

// ProxyFallbackConfig supplies the auxiliary-service choice after proxy
// construction fails.
type ProxyFallbackConfig struct {
	// AllowDirectOnError permits direct connections after auxiliary-service proxy
	// initialization fails.
	// It applies to these auxiliary services:
	//   - GitHub Release update checks
	//   - Pricing data downloads
	// AI gateway connections, including Claude, OpenAI, Gemini, and Antigravity,
	// return proxy errors rather than connecting directly.
	// The default is false to prevent proxy misconfiguration from exposing the server IP.
	AllowDirectOnError bool `mapstructure:"allow_direct_on_error"`
}

// ProxyProbeConfig supplies configured probe endpoints and the unsupported
// TLS-skip input.
type ProxyProbeConfig struct {
	// InsecureSkipVerify reaches proxy probe client construction as supplied.
	// True makes httpclient.GetClient reject the probe, so this option cannot
	// disable certificate verification.
	InsecureSkipVerify bool `mapstructure:"insecure_skip_verify"`
	// URLs lists probe endpoints in priority order; empty uses the built-in list.
	URLs []ProbeURLConfig `mapstructure:"urls"`
}

// ProbeURLConfig describes a proxy-egress probe endpoint and its response parser.
type ProbeURLConfig struct {
	URL    string `mapstructure:"url"`    // proxy probe endpoint URL
	Parser string `mapstructure:"parser"` // ip-api, ipify, or chatgpt-trace parser
}

// BillingConfig supplies pre-forwarding balance reserve and billing
// circuit-breaker deployment settings.
type BillingConfig struct {
	// billing failure circuit settings
	CircuitBreaker CircuitBreakerConfig `mapstructure:"circuit_breaker"`
	// MinimumBalanceReserve is the conservative balance threshold before
	// balance-funded forwarding.
	// Zero requires only balance > 0.
	MinimumBalanceReserve float64 `mapstructure:"minimum_balance_reserve"`
}

// CircuitBreakerConfig supplies failure counts and recovery admission limits to
// billing protection.
type CircuitBreakerConfig struct {
	// billing circuit availability switch
	Enabled bool `mapstructure:"enabled"`
	// failure count opening the circuit
	FailureThreshold int `mapstructure:"failure_threshold"`
	// open-circuit reset delay seconds
	ResetTimeoutSeconds int `mapstructure:"reset_timeout_seconds"`
	// half-open probe admission count
	HalfOpenRequests int `mapstructure:"half_open_requests"`
}

// ConcurrencyConfig supplies the SSE keepalive interval used during
// concurrency-slot waiting.
type ConcurrencyConfig struct {
	// PingInterval is the SSE ping interval while waiting for concurrency, in seconds.
	PingInterval int `mapstructure:"ping_interval"`
}

// ImageConcurrencyConfig supplies separate image-request admission and
// queue-wait deployment limits.
type ImageConcurrencyConfig struct {
	// Enabled selects the separate image admission limiter. Its deployment default
	// is false, while the remaining fields describe the process capacity and
	// overflow choices passed to image handling.
	Enabled bool `mapstructure:"enabled"`
	// MaxConcurrentRequests supplies the process image-slot limit. Zero is
	// unlimited in the image admission consumer, and a negative configured value
	// is rejected during startup validation.
	MaxConcurrentRequests int `mapstructure:"max_concurrent_requests"`
	// OverflowMode selects reject or wait when the image concurrency limit is reached.
	OverflowMode string `mapstructure:"overflow_mode"`
	// WaitTimeoutSeconds supplies the image-slot wait budget in seconds. It is
	// relevant when OverflowMode selects wait, alongside the queued-request limit
	// used by image admission.
	WaitTimeoutSeconds int `mapstructure:"wait_timeout_seconds"`
	// MaxWaitingRequests limits queued image requests in this process when
	// overflow_mode is wait.
	MaxWaitingRequests int `mapstructure:"max_waiting_requests"`
}

// GatewayConfig supplies process defaults for forwarding limits, provider
// selection, and completion work.
type GatewayConfig struct {
	// ResponseHeaderTimeout is the generic upstream header budget in seconds.
	// Zero disables it; OpenAI and Grok profile selection uses their separate
	// fields. This deadline ends when headers arrive, before streaming duration.
	ResponseHeaderTimeout int `mapstructure:"response_header_timeout"`
	// OpenAIResponseHeaderTimeout bounds OpenAI/Codex header waits in seconds;
	// zero disables it.
	// The default permits long upstream queue waits independently of the generic timeout.
	OpenAIResponseHeaderTimeout int `mapstructure:"openai_response_header_timeout"`
	// GrokResponseHeaderTimeout bounds xAI/Grok response-header waits in seconds.
	// With configured transport options, zero disables this deadline; only absent
	// options select the transport's 120-second Grok default. The generic gateway
	// header timeout is replaced during Grok profile selection.
	GrokResponseHeaderTimeout int `mapstructure:"grok_response_header_timeout"`
	// OpenAIFirstOutputTimeoutSeconds and
	// OpenAIHighEffortFirstOutputTimeoutSeconds feed
	// OpenAIResponseOutput.FirstOutputTimeout for HTTP and WS Responses. A
	// nonpositive ordinary budget disables the deadline before the override is
	// considered; with a positive ordinary budget, high, xhigh, and max reasoning
	// requests use a positive override or retain the ordinary value.
	// ordinary first-output deadline seconds
	OpenAIFirstOutputTimeoutSeconds int `mapstructure:"openai_first_output_timeout_seconds"`
	// high-effort first-output override seconds
	OpenAIHighEffortFirstOutputTimeoutSeconds int `mapstructure:"openai_high_effort_first_output_timeout_seconds"`
	// Maximum gateway request-body size, in bytes.
	MaxBodySize int64 `mapstructure:"max_body_size"`
	// TextMaxBodySize limits bodies on endpoints that disallow inline image or
	// video payloads.
	TextMaxBodySize int64 `mapstructure:"text_max_body_size"`
	// Non-streaming upstream response limit in bytes, bounding memory used by body reads.
	UpstreamResponseReadMaxBytes int64 `mapstructure:"upstream_response_read_max_bytes"`
	// Upstream model-list response limit, in bytes.
	ModelsListReadMaxBytes int64 `mapstructure:"models_list_read_max_bytes"`
	// Proxy-probe response limit, in bytes.
	ProxyProbeResponseReadMaxBytes int64 `mapstructure:"proxy_probe_response_read_max_bytes"`
	// Log Gemini response headers for debugging; disabled by default to limit log volume.
	GeminiDebugResponseHeaders bool `mapstructure:"gemini_debug_response_headers"`
	// ConnectionPoolIsolation selects proxy, provider, or provider_proxy isolation.
	ConnectionPoolIsolation string `mapstructure:"connection_pool_isolation"`
	// ForceCodexCLI treats OpenAI /v1/responses requests as Codex CLI requests.
	// Use when a gateway removes or rewrites User-Agent; disabled by default.
	ForceCodexCLI bool `mapstructure:"force_codex_cli"`
	// CodexImageGenerationBridgeEnabled injects the image_generation tool and
	// bridge instructions into Codex
	// /v1/responses.
	// Disabled by default; requests already containing the tool still follow group
	// capabilities.
	CodexImageGenerationBridgeEnabled bool `mapstructure:"codex_image_generation_bridge_enabled"`
	// ForcedCodexInstructionsTemplateFile is the template that supplies final
	// Codex instructions.
	// The rendered template replaces instructions; include {{ .ExistingInstructions }} to retain converted client system text.
	ForcedCodexInstructionsTemplateFile string `mapstructure:"forced_codex_instructions_template_file"`
	// ForcedCodexInstructionsTemplate caches the template read at startup.
	// It is excluded from configuration decoding so requests can use it without
	// disk reads.
	ForcedCodexInstructionsTemplate string `mapstructure:"-"`
	// OpenAICompactModel supplies the global model for explicit compact requests
	// when provider mapping misses.
	OpenAICompactModel string `mapstructure:"openai_compact_model"`
	// OpenAIPassthroughAllowTimeoutHeaders permits client timeout headers in
	// OpenAI passthrough mode.
	// Disabled by default so headers such as x-stainless-timeout do not
	// prematurely end streams.
	OpenAIPassthroughAllowTimeoutHeaders bool `mapstructure:"openai_passthrough_allow_timeout_headers"`
	// OpenAIWS configures OpenAI Responses WebSocket connections.
	OpenAIWS GatewayOpenAIWSConfig `mapstructure:"openai_ws"`
	// Live configures ChatGPT Frameless Live sessions.
	Live GatewayLiveConfig `mapstructure:"live"`
	// AdvancedScheduler configures scheduling across platforms.
	AdvancedScheduler GatewayAdvancedSchedulerConfig `mapstructure:"advanced_scheduler"`
	// OpenAIHTTP2 prefers HTTP/2 and can fall back to HTTP/1.1 for incompatible proxies.
	OpenAIHTTP2 GatewayOpenAIHTTP2Config `mapstructure:"openai_http2"`
	// OpenAIProxyStreamCircuit configures Responses SSE proxy-disconnection isolation.
	OpenAIProxyStreamCircuit GatewayOpenAIProxyStreamCircuitConfig `mapstructure:"openai_proxy_stream_circuit"`
	// ImageConcurrency supplies a process image-admission policy separate from
	// ordinary request concurrency. Its Enabled field defaults false, and the
	// image consumer uses its capacity and overflow settings.
	ImageConcurrency ImageConcurrencyConfig `mapstructure:"image_concurrency"`

	// MaxIdleConns, MaxIdleConnsPerHost, and MaxConnsPerHost supply global pool
	// limits. transport.Client.resolvePoolSettings replaces all three with a
	// positive provider concurrency for provider or provider_proxy isolation;
	// other pools retain these configured limits.
	// MaxIdleConns limits idle connections across all hosts.
	MaxIdleConns int `mapstructure:"max_idle_conns"`
	// MaxIdleConnsPerHost limits idle connections retained per host for reuse.
	MaxIdleConnsPerHost int `mapstructure:"max_idle_conns_per_host"`
	// MaxConnsPerHost bounds active and idle connections to one host when the
	// global limits apply. Zero leaves that host limit unlimited while the idle
	// limits still govern retained connections.
	MaxConnsPerHost int `mapstructure:"max_conns_per_host"`
	// IdleConnTimeoutSeconds is the idle connection timeout.
	IdleConnTimeoutSeconds int `mapstructure:"idle_conn_timeout_seconds"`
	// MaxUpstreamClients limits cached upstream HTTP clients.
	// Pool isolation creates clients for distinct provider/proxy identities.
	// Exceeding the cache limit evicts the least recently used client.
	// Size this limit for the expected active provider/proxy identities.
	MaxUpstreamClients int `mapstructure:"max_upstream_clients"`
	// ClientIdleTTLSeconds is the idle threshold for client reclamation.
	// Clients unused beyond this threshold become eligible for reclamation.
	// Choose the threshold based on request frequency.
	ClientIdleTTLSeconds int `mapstructure:"client_idle_ttl_seconds"`
	// ConcurrencySlotTTLMinutes is the concurrency slot lifetime.
	// Set it longer than the longest LLM request so active slots do not expire.
	ConcurrencySlotTTLMinutes int `mapstructure:"concurrency_slot_ttl_minutes"`
	// SessionIdleTimeoutMinutes is the session idle timeout; the default is five minutes.
	// It applies to session-count limits for Anthropic OAuth and SetupToken providers.
	// Sessions idle past the timeout are released.
	SessionIdleTimeoutMinutes int `mapstructure:"session_idle_timeout_minutes"`

	// StreamDataIntervalTimeout is the allowed ordinary stream data gap in
	// seconds. Zero disables the gap deadline; positive values reach the stream
	// consumer separately from its keepalive interval.
	StreamDataIntervalTimeout int `mapstructure:"stream_data_interval_timeout"`
	// StreamKeepaliveInterval supplies downstream text SSE idle heartbeat intervals in
	// seconds. Zero disables these keepalives, while the separate data-gap timeout
	// can still bound an inactive upstream stream.
	StreamKeepaliveInterval int `mapstructure:"stream_keepalive_interval"`
	// ImageStreamDataIntervalTimeout supplies the image stream data-gap limit in
	// seconds. Zero disables that deadline; image generation uses this budget
	// separately from ordinary text stream timing.
	ImageStreamDataIntervalTimeout int `mapstructure:"image_stream_data_interval_timeout"`
	// ImageStreamKeepaliveInterval supplies heartbeat seconds during image
	// response-header waits and SSE reads. Zero disables those pings, while the
	// independent image data-gap timeout remains a separate stream liveness choice.
	ImageStreamKeepaliveInterval int `mapstructure:"image_stream_keepalive_interval"`
	// ImageNonstreamKeepaliveInterval supplies keepalive seconds for nonstreaming
	// image JSON responses. Zero disables that behavior, and the setting remains
	// separate from image SSE keepalive timing.
	ImageNonstreamKeepaliveInterval int `mapstructure:"image_nonstream_keepalive_interval"`
	// MaxLineSize limits an upstream SSE line in bytes; zero uses the default.
	MaxLineSize int `mapstructure:"max_line_size"`

	// Log summaries of upstream error response bodies.
	LogUpstreamErrorBody bool `mapstructure:"log_upstream_error_body"`
	// Maximum logged upstream error-body bytes; longer bodies are truncated.
	LogUpstreamErrorBodyMaxBytes int `mapstructure:"log_upstream_error_body_max_bytes"`

	// Add required anthropic-beta values for API-key providers when absent from
	// the client; disabled by default.
	InjectBetaForAPIKey bool `mapstructure:"inject_beta_for_apikey"`

	// FailoverOn400 permits provider switching for the selected recoverable HTTP
	// 400 cases interpreted by gateway error handling. It defaults false and does
	// not classify every 400 response as retryable.
	FailoverOn400 bool `mapstructure:"failover_on_400"`

	// Maximum provider switches after upstream errors.
	MaxProviderSwitches int `mapstructure:"max_provider_switches"`
	// Separate maximum provider switches for Gemini.
	MaxProviderSwitchesGemini int `mapstructure:"max_provider_switches_gemini"`

	// AntigravityFallbackCooldownMinutes supplies the 429 cooldown when the
	// adapter cannot parse a reset time. The application passes its minute value
	// to the Antigravity fallback duration selection.
	AntigravityFallbackCooldownMinutes int `mapstructure:"antigravity_fallback_cooldown_minutes"`

	// Scheduling configures provider selection and waiting.
	Scheduling GatewaySchedulingConfig `mapstructure:"scheduling"`

	// TLSFingerprint configures upstream TLS handshake fingerprints.
	TLSFingerprint TLSFingerprintConfig `mapstructure:"tls_fingerprint"`

	// UsageRecord configures the bounded asynchronous usage-record queue and workers.
	UsageRecord GatewayUsageRecordConfig `mapstructure:"usage_record"`

	// UserGroupRateCacheTTLSeconds supplies the retained lifetime of user/group
	// rate multipliers in seconds. Startup requires a positive value before the
	// rate cache uses this process default.
	UserGroupRateCacheTTLSeconds int `mapstructure:"user_group_rate_cache_ttl_seconds"`
	// ModelsListCacheTTLSeconds accepts a retired cache lifetime in seconds and
	// remains validated from ten through thirty. Current catalog queries read
	// their provider configuration directly and do not consume this field.
	ModelsListCacheTTLSeconds int `mapstructure:"models_list_cache_ttl_seconds"`

	// UserMessageQueue configures user-message serialization.
	// It serializes role:"user" messages per provider and applies RPM-based delay.
	UserMessageQueue UserMessageQueueConfig `mapstructure:"user_message_queue"`

	// Grok configures Grok/xAI scheduling and the free-tier quota gate.
	Grok GatewayGrokConfig `mapstructure:"grok"`

	// UsageMonitor configures balance and quota polling for Kimi, Zhipu, and
	// DeepSeek. The mapstructure key keeps existing deployment configuration
	// valid.
	UsageMonitor GatewayUsageMonitorConfig `mapstructure:"cn_providers"`
}

// GatewayGrokConfig supplies Grok password authorization and its free-tier
// rolling token gate. The gate applies to OAuth providers marked free and stops
// new scheduling at a configured percentage of its nominal window allowance.
// Window duration is measured in hours.
//
// Quota observations are cached for scheduling consumers. A cache miss permits
// scheduling while statistics refresh in the background, so the request does
// not wait for the usage database to produce those observations.
type GatewayGrokConfig struct {
	// PasswordAuthEnabled enables the optional password-to-SSO OAuth flow;
	// disabled by default.
	// POST /admin/grok/oauth/password performs authorization only when enabled.
	PasswordAuthEnabled bool `mapstructure:"password_auth_enabled"`
	// FreeQuotaSoftGateEnabled gates Grok OAuth providers marked free using local
	// rolling usage.
	FreeQuotaSoftGateEnabled bool `mapstructure:"free_quota_soft_gate_enabled"`
	// FreeQuotaTokenLimit is the nominal token allowance for the rolling window.
	FreeQuotaTokenLimit int64 `mapstructure:"free_quota_token_limit"`
	// FreeQuotaSoftGatePercent is the allowance percentage that stops new scheduling.
	FreeQuotaSoftGatePercent int `mapstructure:"free_quota_soft_gate_percent"`
	// FreeQuotaWindowHours is the local usage window length.
	FreeQuotaWindowHours int `mapstructure:"free_quota_window_hours"`
	// FreeQuotaStatsCacheSeconds is the quota-statistics cache TTL; requests do
	// not wait for usage_logs.
	// A cache miss permits scheduling and triggers an asynchronous refresh.
	FreeQuotaStatsCacheSeconds int `mapstructure:"free_quota_stats_cache_seconds"`
}

// GatewayUsageMonitorConfig configures periodic balance and quota probes for
// Kimi, Zhipu, and DeepSeek API-key providers. Disabled by default; when
// enabled it probes active API-key providers. Manual queries run independently.
type GatewayUsageMonitorConfig struct {
	// MonitorEnabled selects periodic supplier probes, while the deployment
	// default leaves that monitor disabled. Administrator-initiated usage queries
	// remain separate from this background availability choice.
	MonitorEnabled bool `mapstructure:"monitor_enabled"`
	// supplier low-balance observation threshold
	BalanceThreshold float64 `mapstructure:"balance_threshold"`
	// supplier probe round interval minutes
	IntervalMinutes int `mapstructure:"interval_minutes"`
	// simultaneous supplier probe limit
	Concurrency int `mapstructure:"concurrency"`
	// per-supplier probe budget seconds
	ProbeTimeoutSeconds int `mapstructure:"probe_timeout_seconds"`
	// whole supplier round budget seconds
	RoundTimeoutSeconds int `mapstructure:"round_timeout_seconds"`
}

// GatewayLiveConfig supplies the hard session duration used by Frameless Live
// forwarding.
type GatewayLiveConfig struct {
	// MaxSessionDurationSeconds supplies the Live session cap in seconds. Validate
	// replaces a nonpositive value with one hour before application construction
	// passes it to Live forwarding.
	MaxSessionDurationSeconds int `mapstructure:"max_session_duration_seconds"`
}

// GatewayOpenAIHTTP2Config configures OpenAI upstream HTTP versions.
// HTTP/2 is preferred by default, with policy-controlled HTTP/1.1 fallback for
// incompatible proxies.
type GatewayOpenAIHTTP2Config struct {
	// Enabled enables the OpenAI HTTP/2 preference.
	Enabled bool `mapstructure:"enabled"`
	// AllowProxyFallbackToHTTP1 permits recognized proxy H2 compatibility errors
	// to select HTTP/1.1 while H2 preference is Enabled. FallbackErrorThreshold
	// counts failures within FallbackWindowSeconds; FallbackTTLSeconds controls
	// the resulting recovery period. The transport policy replaces nonpositive
	// counts and durations with its corresponding defaults.
	// proxy HTTP/1.1 recovery switch
	AllowProxyFallbackToHTTP1 bool `mapstructure:"allow_proxy_fallback_to_http1"`
	// compatibility failure trigger count
	FallbackErrorThreshold int `mapstructure:"fallback_error_threshold"`
	// compatibility failure window seconds
	FallbackWindowSeconds int `mapstructure:"fallback_window_seconds"`
	// HTTP/1.1 recovery lifetime seconds
	FallbackTTLSeconds int `mapstructure:"fallback_ttl_seconds"`
}

// GatewayOpenAIProxyStreamCircuitConfig configures bounded, process-local
// Responses SSE proxy isolation.
type GatewayOpenAIProxyStreamCircuitConfig struct {
	// Disabled turns off the process proxy-disconnection isolation consumer. Its
	// default false leaves that consumer enabled, with the configured window and
	// count determining when isolation is selected.
	Disabled bool `mapstructure:"disabled"`
	// FailureThreshold counts eligible proxy disconnections within WindowSeconds,
	// and TTLSeconds controls the resulting isolation period.
	// egress.NewProxyStreamCircuit replaces nonpositive counts and durations with
	// its corresponding defaults; Disabled independently bypasses circuit
	// recording.
	// proxy disconnection trigger count
	FailureThreshold int `mapstructure:"failure_threshold"`
	// proxy disconnection window seconds
	WindowSeconds int `mapstructure:"window_seconds"`
	TTLSeconds    int `mapstructure:"ttl_seconds"` // proxy isolation retention seconds
}

// UserMessageQueueConfig configures serial user-message delivery
// for Anthropic OAuth and SetupToken providers.
type UserMessageQueueConfig struct {
	// Mode and Enabled express current and retired queue choices. Recognized
	// serialize or throttle Mode values win; an absent or unsupported Mode uses
	// Enabled, with true selecting serialization. Serialize holds a provider lock
	// and applies RPM delay; throttle applies delay while allowing concurrent
	// requests.
	Mode    string `mapstructure:"mode"`    // serialize, throttle, or empty choice
	Enabled bool   `mapstructure:"enabled"` // retired serialization availability switch
	// LockTTLMs is the serial lock lifetime in milliseconds; set it longer than
	// the longest request.
	LockTTLMs int `mapstructure:"lock_ttl_ms"`
	// WaitTimeoutMs bounds lock acquisition in milliseconds.
	WaitTimeoutMs int `mapstructure:"wait_timeout_ms"`
	// MinDelayMs and MaxDelayMs bound RPM-based delay interpolation in
	// milliseconds. scheduler.CalculateRPMAwareDelay replaces nonpositive values
	// with its defaults and swaps inverted bounds before calculating the delay and
	// applying jitter.
	// lower RPM-delay interpolation milliseconds
	MinDelayMs int `mapstructure:"min_delay_ms"`
	// upper RPM-delay interpolation milliseconds
	MaxDelayMs int `mapstructure:"max_delay_ms"`
	// CleanupIntervalSeconds supplies the orphaned message-lock cleanup cadence.
	// Zero disables this periodic cleanup choice, while message admission and its
	// lock lifetime retain their own settings.
	CleanupIntervalSeconds int `mapstructure:"cleanup_interval_seconds"`
}

// GatewayOpenAIWSConfig supplies deployment defaults for Responses WS,
// overridable by admin settings.
type GatewayOpenAIWSConfig struct {
	// ingress first-message wait seconds
	ClientFirstMessageTimeoutSeconds int `mapstructure:"client_first_message_timeout_seconds"`
	// ingress between-turn idle seconds
	IngressInterTurnIdleTimeoutSeconds int `mapstructure:"ingress_inter_turn_idle_timeout_seconds"`
	// per-key ingress connection limit
	MaxIngressConnectionsPerAPIKey int `mapstructure:"max_ingress_connections_per_api_key"`
	// ingress previous-response recovery switch
	IngressPreviousResponseRecoveryEnabled bool `mapstructure:"ingress_previous_response_recovery_enabled"`
	// client message read-limit bytes
	ClientReadLimitBytes int64 `mapstructure:"client_read_limit_bytes"`
	// large message HTTP-bridge threshold bytes
	HTTPBridgeThresholdBytes int64 `mapstructure:"http_bridge_threshold_bytes"`
	// MaxConnsPerProvider, MinIdlePerProvider, and MaxIdlePerProvider describe one
	// provider pool. Startup validation keeps the idle minimum within the idle
	// maximum and keeps that maximum within total capacity before pool
	// construction.
	// provider connection pool capacity
	MaxConnsPerProvider int `mapstructure:"max_conns_per_provider"`
	// provider idle connection minimum
	MinIdlePerProvider int `mapstructure:"min_idle_per_provider"`
	// provider idle connection maximum
	MaxIdlePerProvider int `mapstructure:"max_idle_per_provider"`
	// concurrency-derived pool capacity switch
	DynamicMaxConnsByProviderConcurrencyEnabled bool `mapstructure:"dynamic_max_conns_by_provider_concurrency_enabled"`
	// OAuth pool concurrency multiplier
	OAuthMaxConnsFactor float64 `mapstructure:"oauth_max_conns_factor"`
	// API-key pool concurrency multiplier
	APIKeyMaxConnsFactor float64 `mapstructure:"apikey_max_conns_factor"`
	// upstream connection dialing seconds
	DialTimeoutSeconds int `mapstructure:"dial_timeout_seconds"`
	// upstream frame reading seconds
	ReadTimeoutSeconds int `mapstructure:"read_timeout_seconds"`
	// upstream frame writing seconds
	WriteTimeoutSeconds int `mapstructure:"write_timeout_seconds"`
	// pool target occupation fraction
	PoolTargetUtilization float64 `mapstructure:"pool_target_utilization"`
	// per-connection queued request limit
	QueueLimitPerConn int `mapstructure:"queue_limit_per_conn"`
	// pool prewarming cooldown milliseconds
	PrewarmCooldownMS int `mapstructure:"prewarm_cooldown_ms"`
	// session affinity retention seconds
	StickySessionTTLSeconds int `mapstructure:"sticky_session_ttl_seconds"`
	// legacy session affinity read switch
	SessionHashReadOldFallback bool `mapstructure:"session_hash_read_old_fallback"`
	// legacy session affinity write switch
	SessionHashDualWriteOld bool `mapstructure:"session_hash_dual_write_old"`
	// session metadata forwarding switch
	MetadataBridgeEnabled bool `mapstructure:"metadata_bridge_enabled"`
	// response-ID affinity retention seconds
	StickyResponseIDTTLSeconds int `mapstructure:"sticky_response_id_ttl_seconds"`
	// StickyPreviousResponseTTLSeconds is the compatibility lifetime read by load
	// and Validate. A positive value fills StickyResponseIDTTLSeconds when that
	// current field is nonpositive; otherwise the current value wins.
	StickyPreviousResponseTTLSeconds int `mapstructure:"sticky_previous_response_ttl_seconds"`
}

// GatewayAdvancedSchedulerScoreWeights weights provider scores in the advanced scheduler.
type GatewayAdvancedSchedulerScoreWeights = schedulerpolicy.ConfigScoreWeights

// GatewayAdvancedSchedulerConfig supplies cross-platform candidate scoring and
// affinity-escape defaults.
type GatewayAdvancedSchedulerConfig struct {
	// LBTopK is the number of highest-scoring candidates retained before weighted
	// selection.
	LBTopK int `mapstructure:"lb_top_k"`
	// ScoreWeights weights candidate scores in the advanced scheduler.
	ScoreWeights GatewayAdvancedSchedulerScoreWeights `mapstructure:"score_weights"`
	// EWMAErrorRateAlpha and EWMATTFTAlpha weight the latest error or first-token
	// latency observation against retained history. Startup requires finite
	// coefficients in (0,1], and larger coefficients give recent observations more
	// weight.
	// error observation smoothing coefficient
	EWMAErrorRateAlpha float64 `mapstructure:"ewma_error_rate_alpha"`
	// TTFT observation smoothing coefficient
	EWMATTFTAlpha float64 `mapstructure:"ewma_ttft_alpha"`
	// StickyEscapeEnabled lets scheduler policy bypass session affinity based on
	// configured TTFT or error-rate observations. It controls that health-based
	// escape choice alongside its two separately configured thresholds.
	StickyEscapeEnabled bool `mapstructure:"sticky_escape_enabled"`
	// StickyEscapeTTFTMs is the TTFT EWMA threshold in milliseconds for skipping
	// affinity.
	StickyEscapeTTFTMs int `mapstructure:"sticky_escape_ttft_ms"`
	// StickyEscapeErrorRate supplies the error-rate EWMA threshold for
	// health-based affinity escape. Startup range checks accept zero, and
	// schedulerpolicy.NormalizeStickyEscape handles nonfinite or out-of-range
	// values passed to it.
	StickyEscapeErrorRate float64 `mapstructure:"sticky_escape_error_rate"`
}

// GatewayUsageRecordConfig supplies completion queue admission, task budgets,
// and worker scaling controls.
type GatewayUsageRecordConfig struct {
	// WorkerCount supplies initial completion worker concurrency whether
	// autoscaling is enabled or disabled. When AutoScaleEnabled is true, Validate
	// requires it within the configured minimum and maximum before constructing
	// the worker pool.
	WorkerCount int `mapstructure:"worker_count"`
	// QueueSize is the bounded queue capacity.
	QueueSize int `mapstructure:"queue_size"`
	// completion task timeout seconds
	TaskTimeoutSeconds int `mapstructure:"task_timeout_seconds"`
	// OverflowPolicy selects handling after queue admission fails: sync runs
	// inline, sample may run inline by percentage, and drop discards work.
	// Pool-stop rejection is handled separately by the completion consumer.
	OverflowPolicy string `mapstructure:"overflow_policy"`
	// OverflowSamplePercent is the percentage written synchronously under sample
	// policy, from 1 to 100.
	OverflowSamplePercent int `mapstructure:"overflow_sample_percent"`

	// AutoScaleEnabled enables worker autoscaling.
	AutoScaleEnabled bool `mapstructure:"auto_scale_enabled"`
	// AutoScaleMinWorkers and AutoScaleMaxWorkers bound the initial WorkerCount
	// and subsequent autoscaling while AutoScaleEnabled is true. Startup
	// validation rejects an inverted range or an initial count outside it.
	// autoscaling worker count minimum
	AutoScaleMinWorkers int `mapstructure:"auto_scale_min_workers"`
	// autoscaling worker count maximum
	AutoScaleMaxWorkers int `mapstructure:"auto_scale_max_workers"`
	// AutoScaleUpQueuePercent and AutoScaleDownQueuePercent set separated
	// occupancy thresholds for growth and shrinkage. When AutoScaleEnabled is
	// true, Validate requires the shrinkage threshold below the growth threshold,
	// leaving a gap between those decisions.
	// queue occupancy growth percentage
	AutoScaleUpQueuePercent int `mapstructure:"auto_scale_up_queue_percent"`
	// queue occupancy shrinkage percentage
	AutoScaleDownQueuePercent int `mapstructure:"auto_scale_down_queue_percent"`
	// AutoScaleUpStep and AutoScaleDownStep supply positive worker-count changes
	// for their respective scaling directions. When AutoScaleEnabled is true,
	// Validate checks both before completion workers respond to queue
	// observations.
	// workers added per adjustment
	AutoScaleUpStep int `mapstructure:"auto_scale_up_step"`
	// workers removed per adjustment
	AutoScaleDownStep int `mapstructure:"auto_scale_down_step"`
	// AutoScaleCheckIntervalSeconds supplies observation cadence, and
	// AutoScaleCooldownSeconds separates adjustment decisions, both in seconds.
	// When AutoScaleEnabled is true, Validate requires a positive observation
	// interval and permits a zero cooldown.
	// scaling observation cadence seconds
	AutoScaleCheckIntervalSeconds int `mapstructure:"auto_scale_check_interval_seconds"`
	// inter-adjustment cooldown in seconds
	AutoScaleCooldownSeconds int `mapstructure:"auto_scale_cooldown_seconds"`
}

// TLSFingerprintConfig supplies global availability and named upstream
// ClientHello profiles. The transport selects a named profile before
// tlsfingerprint builds its supported extension values and protocol lists.
type TLSFingerprintConfig struct {
	// Enabled enables TLS fingerprinting globally.
	Enabled bool `mapstructure:"enabled"`
	// Profiles contains named TLS fingerprint templates.
	// Keys are profile names such as "claude_cli_v2" or "chrome_120".
	Profiles map[string]TLSProfileConfig `mapstructure:"profiles"`
}

// TLSProfileConfig supplies one TLS fingerprint's configured parameters.
// CipherSuites, Curves, PointFormats, SignatureAlgorithms, ALPNProtocols,
// SupportedVersions, KeyShareGroups, PSKModes, and Extensions use their
// respective defaults in tlsfingerprint.buildClientHelloSpecFromProfile when
// empty; the fingerprint collector can supply captured values.
//
// Those defaults include http/1.1 ALPN, TLS 1.3 and 1.2 versions, X25519 key
// share, and the extension order defined by
// tlsfingerprint.defaultExtensionOrder. Explicit nonempty lists retain their
// configured ordering for the dialer's ClientHello construction.
type TLSProfileConfig struct {
	Name string `mapstructure:"name"` // Name is the profile display name.
	// EnableGREASE enables GREASE extensions for profiles such as Chrome.
	EnableGREASE bool `mapstructure:"enable_grease"`
	// CipherSuites lists TLS cipher suite IDs.
	CipherSuites []uint16 `mapstructure:"cipher_suites"`
	// Curves lists supported elliptic curve IDs.
	Curves []uint16 `mapstructure:"curves"`
	// PointFormats supplies configured point-format identifiers. The dialer
	// narrows each uint16 value to one byte when constructing the ClientHello
	// supported-points extension, so upper bits are discarded.
	PointFormats []uint16 `mapstructure:"point_formats"`
	// SignatureAlgorithms lists signature algorithm IDs.
	SignatureAlgorithms []uint16 `mapstructure:"signature_algorithms"`
	// ALPNProtocols lists protocols such as ["h2", "http/1.1"].
	ALPNProtocols []string `mapstructure:"alpn_protocols"`
	// SupportedVersions lists TLS versions, such as [0x0304, 0x0303] for TLS 1.3 and 1.2.
	SupportedVersions []uint16 `mapstructure:"supported_versions"`
	// KeyShareGroups lists curves sent in Key Share, such as [29] for X25519.
	KeyShareGroups []uint16 `mapstructure:"key_share_groups"`
	// PSKModes supplies configured pre-shared-key exchange identifiers. The dialer
	// narrows each uint16 value to one byte when constructing the exchange-modes
	// extension; empty uses its DHE default.
	PSKModes []uint16 `mapstructure:"psk_modes"`
	// Extensions supplies the TLS extension IDs in wire order. Empty uses
	// tlsfingerprint.defaultExtensionOrder; GREASE IDs create GREASE entries,
	// while unrecognized ordinary IDs produce empty-data GenericExtension entries.
	Extensions []uint16 `mapstructure:"extensions"`
}

// GatewaySchedulingConfig configures provider selection, waiting, and snapshot refresh.
type GatewaySchedulingConfig struct {
	// StickySessionMaxWaiting and StickySessionWaitTimeout bound the queue for an
	// already selected sticky provider. The first limits waiting requests; the
	// second supplies their acquisition duration, and both require positive
	// startup values.
	// sticky-provider waiter count limit
	StickySessionMaxWaiting int `mapstructure:"sticky_session_max_waiting"`
	// sticky-provider wait duration
	StickySessionWaitTimeout time.Duration `mapstructure:"sticky_session_wait_timeout"`

	// FallbackWaitTimeout and FallbackMaxWaiting bound waiting after selection of
	// an alternative provider. They supply the acquisition duration and queue
	// capacity independently of the sticky-provider queue limits.
	// alternative-provider wait duration
	FallbackWaitTimeout time.Duration `mapstructure:"fallback_wait_timeout"`
	// alternative-provider waiter count limit
	FallbackMaxWaiting int `mapstructure:"fallback_max_waiting"`

	// Selection mode: "last_used" orders by last use by default; "random"
	// randomizes selection.
	FallbackSelectionMode string `mapstructure:"fallback_selection_mode"`

	// PreferSoonestReset makes load-aware selection favor providers whose session
	// windows reset soonest.
	// This consumes allowances near reset before those with more time remaining.
	// Disabled by default, using priority, load ratio, and LRU ordering.
	PreferSoonestReset bool `mapstructure:"prefer_soonest_reset"`

	// batched load observation switch
	LoadBatchEnabled bool `mapstructure:"load_batch_enabled"`
	// LoadBatchCacheTTLMS supplies provider-load cache lifetime in milliseconds to
	// scheduler.ConcurrencyService. Zero disables and clears that cache; the
	// batched observation selection switch remains independently configured.
	LoadBatchCacheTTLMS int `mapstructure:"load_batch_cache_ttl_ms"`
	// MGET chunk size when reading snapshot buckets.
	SnapshotMGetChunkSize int `mapstructure:"snapshot_mget_chunk_size"`
	// Cache write chunk size when rebuilding snapshots.
	SnapshotWriteChunkSize int `mapstructure:"snapshot_write_chunk_size"`

	// SlotCleanupInterval supplies periodic expired-slot cleanup timing. Zero
	// disables that cleanup loop, while admitted slots retain their separately
	// configured TTL and normal release behavior.
	SlotCleanupInterval time.Duration `mapstructure:"slot_cleanup_interval"`

	// DbFallbackEnabled permits scheduler storage reads when cached scheduling
	// data cannot supply trusted candidates. The timeout and per-process QPS
	// fields then constrain those permitted database reads.
	DbFallbackEnabled bool `mapstructure:"db_fallback_enabled"`
	// DbFallbackTimeoutSeconds adds a timeout to permitted scheduler database
	// reads when positive. Zero leaves the caller context budget in control, and
	// values are supplied in seconds.
	DbFallbackTimeoutSeconds int `mapstructure:"db_fallback_timeout_seconds"`
	// DbFallbackMaxQPS supplies the per-process scheduler database-read rate
	// limit. Zero leaves it unlimited, while DbFallbackEnabled still determines
	// whether cache-miss database reads can run.
	DbFallbackMaxQPS int `mapstructure:"db_fallback_max_qps"`

	// Outbox polling and rebuild thresholds.
	// Outbox polling interval, in seconds.
	OutboxPollIntervalSeconds int `mapstructure:"outbox_poll_interval_seconds"`
	// OutboxLagWarnSeconds enables age warnings when positive. Positive
	// OutboxLagRebuildSeconds enables the separate age trigger, and
	// OutboxLagRebuildFailures counts eligible degraded observations before a
	// rebuild. SnapshotService pauses that count while rebuilds or their lag
	// retries are active; zero age thresholds disable their respective triggers.
	// outbox age warning threshold seconds
	OutboxLagWarnSeconds int `mapstructure:"outbox_lag_warn_seconds"`
	// outbox age rebuild threshold seconds
	OutboxLagRebuildSeconds int `mapstructure:"outbox_lag_rebuild_seconds"`
	// eligible excessive-age observation count
	OutboxLagRebuildFailures int `mapstructure:"outbox_lag_rebuild_failures"`
	// OutboxBacklogRebuildRows supplies the maximum-ID minus watermark threshold
	// used by scheduler.SnapshotService. Zero disables this trigger; gaps in event
	// IDs can make this ID distance exceed actual pending rows.
	OutboxBacklogRebuildRows int `mapstructure:"outbox_backlog_rebuild_rows"`

	// FullRebuildIntervalSeconds supplies periodic complete snapshot refresh
	// timing in seconds. Zero disables that loop, while outbox-triggered rebuild
	// decisions retain their own configured thresholds.
	FullRebuildIntervalSeconds int `mapstructure:"full_rebuild_interval_seconds"`
}

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

// OpsConfig supplies deployment availability, cleanup, aggregation, and
// collector cache defaults.
type OpsConfig struct {
	// Enabled controls whether ops features should run.
	//
	// The database setting ops_monitoring_enabled controls runtime availability
	// when this deployment-level switch is enabled.
	Enabled bool `mapstructure:"enabled"`

	// Cleanup controls periodic deletion of old ops data to prevent unbounded growth.
	Cleanup OpsCleanupConfig `mapstructure:"cleanup"`

	// accepted collector-cache process inputs
	MetricsCollectorCache OpsMetricsCollectorCacheConfig `mapstructure:"metrics_collector_cache"`

	// operational metrics aggregation settings
	Aggregation OpsAggregationConfig `mapstructure:"aggregation"`
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

// OpsMetricsCollectorCacheConfig holds accepted availability and lifetime
// inputs copied into Ops runtime options.
type OpsMetricsCollectorCacheConfig struct {
	// Enabled and TTL are decoded and copied into ops.Options, with negative TTL
	// rejected by startup validation. Current collection uses direct
	// MetricsQueries SQL reads, so these accepted inputs have no metrics-query
	// cache consumer.
	Enabled bool          `mapstructure:"enabled"` // accepted cache availability flag
	TTL     time.Duration `mapstructure:"ttl"`     // accepted cache duration input
}

// JWTConfig holds signing input and access/refresh token lifetime values for
// identity construction.
type JWTConfig struct {
	// Secret is signing input validated during ordinary Load. LoadForBootstrap can
	// return it empty until database bootstrap selects the persisted key, then
	// performs full validation before identity construction.
	Secret     string `mapstructure:"secret"`
	ExpireHour int    `mapstructure:"expire_hour"` // legacy access-token expiry hours
	// AccessTokenExpireMinutes overrides ExpireHour when positive for access-token
	// expiry selection. Zero retains hour-based behavior, and startup validation
	// rejects a negative override before identity consumes these settings.
	AccessTokenExpireMinutes int `mapstructure:"access_token_expire_minutes"`
	// RefreshTokenExpireDays supplies the refresh-token lifetime in days. Default
	// loading selects thirty days, and validation requires a positive lifetime
	// while warning when the supplied value exceeds ninety days.
	RefreshTokenExpireDays int `mapstructure:"refresh_token_expire_days"`
	// RefreshWindowMinutes permits a refresh request this many minutes before
	// access-token expiry. Startup validation accepts zero and rejects negative
	// window lengths before identity uses these settings.
	RefreshWindowMinutes int `mapstructure:"refresh_window_minutes"`
}

// TotpConfig supplies the secret encryption key and whether deployment input
// configured it.
type TotpConfig struct {
	// EncryptionKey is the hex-encoded 32-byte AES-256 key used to encrypt TOTP secrets.
	// Empty generates a random development key at startup.
	EncryptionKey string `mapstructure:"encryption_key"`
	// EncryptionKeyConfigured reports whether the key was configured rather than
	// generated.
	// Admin settings permit enabling TOTP only with a configured key.
	EncryptionKeyConfigured bool `mapstructure:"-"`
}

// TurnstileConfig supplies the deployment default for mandatory Turnstile
// verification.
type TurnstileConfig struct {
	Required bool `mapstructure:"required"` // default mandatory captcha verification flag
}

// DefaultConfig supplies initial administrator input and default user, key, and
// billing values.
type DefaultConfig struct {
	// AdminEmail and AdminPassword remain accepted startup bootstrap inputs with
	// empty loader defaults. The ordinary installation flow obtains initial
	// credentials separately, and identity bootstrap leaves existing user data
	// unchanged.
	// bootstrap administrator email text
	AdminEmail string `mapstructure:"admin_email"`
	// bootstrap administrator password text
	AdminPassword string `mapstructure:"admin_password"`
	// new-user concurrent request limit
	UserConcurrency int `mapstructure:"user_concurrency"`
	// new-user initial balance amount
	UserBalance float64 `mapstructure:"user_balance"`
	// generated API-key text prefix
	APIKeyPrefix string `mapstructure:"api_key_prefix"`
	// new-user billing rate multiplier
	RateMultiplier float64 `mapstructure:"rate_multiplier"`
}

// RateLimitConfig supplies provider cooldown durations after overload and OAuth
// authorization failures.
type RateLimitConfig struct {
	OverloadCooldownMinutes int `mapstructure:"overload_cooldown_minutes"`  // HTTP 529 overload cooldown, in minutes.
	OAuth401CooldownMinutes int `mapstructure:"oauth_401_cooldown_minutes"` // OAuth HTTP 401 scheduling cooldown, in minutes.
}

// APIKeyAuthCacheConfig supplies authentication cache lifetimes, lookup
// admission, and invalid-key abuse limits.
type APIKeyAuthCacheConfig struct {
	// process authentication entry capacity
	L1Size int `mapstructure:"l1_size"`
	// process authentication retention seconds
	L1TTLSeconds int `mapstructure:"l1_ttl_seconds"`
	// Redis authentication retention seconds
	L2TTLSeconds int `mapstructure:"l2_ttl_seconds"`
	// missing-key observation retention seconds
	NegativeTTLSeconds int `mapstructure:"negative_ttl_seconds"`
	// entry lifetime jitter percent
	JitterPercent int `mapstructure:"jitter_percent"`
	// same-key authentication load coalescing switch
	Singleflight bool `mapstructure:"singleflight"`
	// authentication lookup admission limit
	LookupConcurrency int `mapstructure:"lookup_concurrency"`
	// invalid-key abuse observation settings
	InvalidAbuse InvalidAuthAbuseConfig `mapstructure:"invalid_abuse"`
}

// InvalidAuthAbuseConfig supplies process-local repeated invalid authentication
// observation and blocking limits.
type InvalidAuthAbuseConfig struct {
	// invalid-auth observation availability switch
	Enabled   bool `mapstructure:"enabled"`
	Threshold int  `mapstructure:"threshold"` // invalid-auth count triggering block
	// invalid-auth observation window seconds
	WindowSeconds int `mapstructure:"window_seconds"`
	// invalid-auth blocking duration seconds
	BlockSeconds int `mapstructure:"block_seconds"`
	Capacity     int `mapstructure:"capacity"` // observed invalid-auth source capacity
}

// SubscriptionCacheConfig supplies the subscription authentication L1 cache
// capacity and entry lifetime.
type SubscriptionCacheConfig struct {
	// subscription authentication entry capacity
	L1Size int `mapstructure:"l1_size"`
	// subscription authentication retention seconds
	L1TTLSeconds  int `mapstructure:"l1_ttl_seconds"`
	JitterPercent int `mapstructure:"jitter_percent"` // entry lifetime jitter percent
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

// DashboardAggregationConfig supplies aggregation cadence, historical work, and
// table retention defaults.
type DashboardAggregationConfig struct {
	// Enabled enables preaggregation jobs.
	Enabled bool `mapstructure:"enabled"`
	// aggregate refresh interval seconds
	IntervalSeconds int `mapstructure:"interval_seconds"`
	LookbackSeconds int `mapstructure:"lookback_seconds"` // recent data lookback seconds
	// BackfillEnabled permits full backfills.
	BackfillEnabled bool `mapstructure:"backfill_enabled"`
	// BackfillMaxDays limits the time span of a backfill.
	BackfillMaxDays int `mapstructure:"backfill_max_days"`
	// Retention sets each table's retention window in days.
	Retention DashboardAggregationRetentionConfig `mapstructure:"retention"`
	// RecomputeDays is the number of recent days recomputed at startup.
	RecomputeDays int `mapstructure:"recompute_days"`
}

// DashboardAggregationRetentionConfig supplies day-based retention for raw
// usage, deduplication, and aggregates.
type DashboardAggregationRetentionConfig struct {
	// UsageLogsDays and UsageBillingDedupDays retain raw usage and billing
	// deduplication records in days. Startup validation keeps deduplication
	// retention at least as long as raw-record retention when both are positive,
	// including disabled aggregation settings.
	UsageLogsDays int `mapstructure:"usage_logs_days"` // raw usage record retention days
	// billing deduplication retention days
	UsageBillingDedupDays int `mapstructure:"usage_billing_dedup_days"`
	// hourly aggregate retention days
	HourlyDays int `mapstructure:"hourly_days"`
	// daily aggregate retention days
	DailyDays int `mapstructure:"daily_days"`
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

// firstNonEmptyString returns the first trimmed nonblank value in supplied
// order. It returns empty text when every value is blank, allowing credential
// compatibility code to apply its ordered source choices.
func firstNonEmptyString(values ...string) string {
	for _, value := range values {
		if trimmed := strings.TrimSpace(value); trimmed != "" {
			return trimmed
		}
	}
	return ""
}

// shouldApplyLegacyWeChatEnv reports whether a retired variable may supply this
// current field. YAML presence blocks compatibility, and any current
// environment presence also blocks it, including an explicitly empty variable.
func shouldApplyLegacyWeChatEnv(configKey, envKey string) bool {
	if viper.InConfig(configKey) {
		return false
	}
	_, hasNewEnv := os.LookupEnv(envKey)
	return !hasNewEnv
}

// hasExplicitConfigOrEnv reports whether the current field appears in YAML or
// its named environment variable exists. Empty, zero, and false values still
// count as explicit inputs for OIDC presence flags.
func hasExplicitConfigOrEnv(configKey, envKey string) bool {
	if viper.InConfig(configKey) {
		return true
	}
	_, ok := os.LookupEnv(envKey)
	return ok
}

// applyLegacyWeChatConnectEnvCompatibility fills eligible WeChat fields from
// retired environment input, accepting a nil cfg as a no-op. Current YAML or
// environment presence blocks each corresponding compatibility assignment.
//
// Open and MP credential pairs additionally respect the generic AppID and
// AppSecret current inputs. A complete eligible retired pair can enable the
// overall and specific mode flags, and selection favors Open when both pairs
// are complete. Mode, scopes, and frontend redirect are updated only when their
// own current inputs are absent. The resulting fields still pass through normal
// WeChat normalization and validation.
func applyLegacyWeChatConnectEnvCompatibility(cfg *WeChatConnectConfig) {
	if cfg == nil {
		return
	}

	// Retired Open and MP credentials are eligible only when neither their current
	// mode field nor the generic credential field has YAML/environment presence.
	// Track eligible complete pairs before enabling modes.
	legacyOpenAppID := ""
	if shouldApplyLegacyWeChatEnv("wechat_connect.open_app_id", "WECHAT_CONNECT_OPEN_APP_ID") &&
		shouldApplyLegacyWeChatEnv("wechat_connect.app_id", "WECHAT_CONNECT_APP_ID") {
		legacyOpenAppID = strings.TrimSpace(os.Getenv("WECHAT_OAUTH_OPEN_APP_ID"))
		if legacyOpenAppID != "" {
			cfg.OpenAppID = legacyOpenAppID
		}
	}

	legacyOpenAppSecret := ""
	if shouldApplyLegacyWeChatEnv("wechat_connect.open_app_secret", "WECHAT_CONNECT_OPEN_APP_SECRET") &&
		shouldApplyLegacyWeChatEnv("wechat_connect.app_secret", "WECHAT_CONNECT_APP_SECRET") {
		legacyOpenAppSecret = strings.TrimSpace(os.Getenv("WECHAT_OAUTH_OPEN_APP_SECRET"))
		if legacyOpenAppSecret != "" {
			cfg.OpenAppSecret = legacyOpenAppSecret
		}
	}

	legacyMPAppID := ""
	if shouldApplyLegacyWeChatEnv("wechat_connect.mp_app_id", "WECHAT_CONNECT_MP_APP_ID") &&
		shouldApplyLegacyWeChatEnv("wechat_connect.app_id", "WECHAT_CONNECT_APP_ID") {
		legacyMPAppID = strings.TrimSpace(os.Getenv("WECHAT_OAUTH_MP_APP_ID"))
		if legacyMPAppID != "" {
			cfg.MPAppID = legacyMPAppID
		}
	}

	legacyMPAppSecret := ""
	if shouldApplyLegacyWeChatEnv("wechat_connect.mp_app_secret", "WECHAT_CONNECT_MP_APP_SECRET") &&
		shouldApplyLegacyWeChatEnv("wechat_connect.app_secret", "WECHAT_CONNECT_APP_SECRET") {
		legacyMPAppSecret = strings.TrimSpace(os.Getenv("WECHAT_OAUTH_MP_APP_SECRET"))
		if legacyMPAppSecret != "" {
			cfg.MPAppSecret = legacyMPAppSecret
		}
	}

	if shouldApplyLegacyWeChatEnv("wechat_connect.frontend_redirect_url", "WECHAT_CONNECT_FRONTEND_REDIRECT_URL") {
		if legacyFrontend := strings.TrimSpace(os.Getenv("WECHAT_OAUTH_FRONTEND_REDIRECT_URL")); legacyFrontend != "" {
			cfg.FrontendRedirectURL = legacyFrontend
		}
	}

	// Enable compatibility modes from complete eligible credential pairs so a lone
	// retired ID or secret cannot activate a login mode. Open wins display
	// selection when both pairs are complete.
	hasLegacyOpen := legacyOpenAppID != "" && legacyOpenAppSecret != ""
	hasLegacyMP := legacyMPAppID != "" && legacyMPAppSecret != ""

	if shouldApplyLegacyWeChatEnv("wechat_connect.enabled", "WECHAT_CONNECT_ENABLED") && (hasLegacyOpen || hasLegacyMP) {
		cfg.Enabled = true
	}
	if shouldApplyLegacyWeChatEnv("wechat_connect.open_enabled", "WECHAT_CONNECT_OPEN_ENABLED") && hasLegacyOpen {
		cfg.OpenEnabled = true
	}
	if shouldApplyLegacyWeChatEnv("wechat_connect.mp_enabled", "WECHAT_CONNECT_MP_ENABLED") && hasLegacyMP {
		cfg.MPEnabled = true
	}
	if shouldApplyLegacyWeChatEnv("wechat_connect.mode", "WECHAT_CONNECT_MODE") {
		switch {
		case hasLegacyMP && !hasLegacyOpen:
			cfg.Mode = "mp"
		case hasLegacyOpen:
			cfg.Mode = "open"
		}
	}
	if shouldApplyLegacyWeChatEnv("wechat_connect.scopes", "WECHAT_CONNECT_SCOPES") {
		switch {
		case hasLegacyMP && !hasLegacyOpen:
			cfg.Scopes = authconfig.DefaultWeChatConnectScopesForMode("mp")
		case hasLegacyOpen:
			cfg.Scopes = authconfig.DefaultWeChatConnectScopesForMode("open")
		}
	}
}

// normalizeWeChatConnectConfig trims WeChat deployment text and supplies
// related credential and mode defaults in place. A nil pointer performs no
// work, and callers invoke this before using the fields for validation or login
// settings.
//
// Generic credentials prefer their supplied values, then Open, MP, and Mobile
// values. Each mode-specific credential separately uses the resulting generic
// value when empty, so callers supply matched credential pairs when mixing
// sources. An enabled legacy configuration with no individual mode flags
// enables its selected mode; stored-mode selection then follows available
// flags, scopes follow that mode, and an empty frontend redirect receives its
// default.
func normalizeWeChatConnectConfig(cfg *WeChatConnectConfig) {
	if cfg == nil {
		return
	}

	cfg.AppID = strings.TrimSpace(cfg.AppID)
	cfg.AppSecret = strings.TrimSpace(cfg.AppSecret)
	cfg.OpenAppID = strings.TrimSpace(cfg.OpenAppID)
	cfg.OpenAppSecret = strings.TrimSpace(cfg.OpenAppSecret)
	cfg.MPAppID = strings.TrimSpace(cfg.MPAppID)
	cfg.MPAppSecret = strings.TrimSpace(cfg.MPAppSecret)
	cfg.MobileAppID = strings.TrimSpace(cfg.MobileAppID)
	cfg.MobileAppSecret = strings.TrimSpace(cfg.MobileAppSecret)
	cfg.Mode = authconfig.NormalizeWeChatConnectMode(cfg.Mode)
	cfg.RedirectURL = strings.TrimSpace(cfg.RedirectURL)
	cfg.FrontendRedirectURL = strings.TrimSpace(cfg.FrontendRedirectURL)

	// Resolve generic credentials before filling mode-specific omissions. Each
	// credential member uses its own ordered nonblank choice, so supplied pairs
	// must remain consistent across these sources.
	cfg.AppID = firstNonEmptyString(cfg.AppID, cfg.OpenAppID, cfg.MPAppID, cfg.MobileAppID)
	cfg.AppSecret = firstNonEmptyString(cfg.AppSecret, cfg.OpenAppSecret, cfg.MPAppSecret, cfg.MobileAppSecret)
	cfg.OpenAppID = firstNonEmptyString(cfg.OpenAppID, cfg.AppID)
	cfg.OpenAppSecret = firstNonEmptyString(cfg.OpenAppSecret, cfg.AppSecret)
	cfg.MPAppID = firstNonEmptyString(cfg.MPAppID, cfg.AppID)
	cfg.MPAppSecret = firstNonEmptyString(cfg.MPAppSecret, cfg.AppSecret)
	cfg.MobileAppID = firstNonEmptyString(cfg.MobileAppID, cfg.AppID)
	cfg.MobileAppSecret = firstNonEmptyString(cfg.MobileAppSecret, cfg.AppSecret)

	// An enabled legacy configuration can express its choice through Mode alone.
	// Promote that selected mode before stored-mode normalization so its login
	// path remains available to consumers.
	if !cfg.OpenEnabled && !cfg.MPEnabled && !cfg.MobileEnabled && cfg.Enabled {
		switch cfg.Mode {
		case "mp":
			cfg.MPEnabled = true
		case "mobile":
			cfg.MobileEnabled = true
		default:
			cfg.OpenEnabled = true
		}
	}
	cfg.Mode = authconfig.NormalizeWeChatConnectStoredMode(cfg.OpenEnabled, cfg.MPEnabled, cfg.MobileEnabled, cfg.Mode)
	cfg.Scopes = authconfig.NormalizeWeChatConnectScopes(cfg.Scopes, cfg.Mode)
	if cfg.FrontendRedirectURL == "" {
		cfg.FrontendRedirectURL = defaultWeChatConnectFrontendRedirect
	}
}

// NormalizeForwardedClientIPHeaders delegates ordered HTTP field-name
// normalization to the client-IP policy owner. It trims and canonicalizes
// names, removes case aliases, rejects invalid or excess unique names, and
// returns an allocated empty slice for empty input.
func NormalizeForwardedClientIPHeaders(headers []string) ([]string, error) {
	return ippolicy.NormalizeForwardedClientIPHeaders(headers)
}

// cloneForwardedClientIPHeaders returns an independently allocated list of the
// supplied header names. Empty and nil input both return a nonnil empty slice,
// supporting policy JSON arrays and independent snapshot editing.
func cloneForwardedClientIPHeaders(headers []string) []string {
	if len(headers) == 0 {
		return []string{}
	}
	return append([]string(nil), headers...)
}

// ForwardedClientIPSettings returns one trust/header policy with copied header
// storage. Nil configuration returns an empty policy with an allocated empty
// list, and a populated live pointer supplies the policy ahead of startup
// fields.
//
// The trust flag and header list are read from one published immutable object
// so request consumers see a complete policy. Before live pointer
// initialization, the method copies the static Security fields instead.
// Initialize that pointer before concurrent access and keep startup fields
// stable while readers can use this path; the getter does not normalize
// arbitrary supplied names.
func (c *Config) ForwardedClientIPSettings() ForwardedClientIPSettings {
	if c == nil {
		return ForwardedClientIPSettings{Headers: []string{}}
	}
	live := c.Security.forwardedClientIPSettingsLive
	if live != nil {
		if snapshot := live.Load(); snapshot != nil {
			return ForwardedClientIPSettings{
				TrustForwardedIP: snapshot.TrustForwardedIP,
				Headers:          cloneForwardedClientIPHeaders(snapshot.Headers),
			}
		}
	}
	return ForwardedClientIPSettings{
		TrustForwardedIP: c.Security.TrustForwardedIPForAPIKeyACL,
		Headers:          cloneForwardedClientIPHeaders(c.Security.ForwardedClientIPHeaders),
	}
}

// TrustForwardedIPForAPIKeyACL returns the forwarding trust flag from the
// current copied policy snapshot. Nil configuration uses false, while an
// initialized live snapshot takes precedence over the original startup flag.
func (c *Config) TrustForwardedIPForAPIKeyACL() bool {
	return c.ForwardedClientIPSettings().TrustForwardedIP
}

// SetForwardedClientIPSettings copies headers and publishes one immutable
// trust/header pair, accepting nil configuration as a no-op. Callers validate
// header names before publication; this setter does not invoke the normalizer.
//
// The atomic cell is allocated lazily and its pointer assignment is
// unsynchronized, so initialize it before concurrent readers or publishers
// start. Later stores replace the whole pair and leave the static startup
// fields unchanged. Copying the list keeps subsequent caller slice edits
// outside the published policy used by request consumers.
func (c *Config) SetForwardedClientIPSettings(enabled bool, headers []string) {
	if c == nil {
		return
	}
	headers = cloneForwardedClientIPHeaders(headers)
	// Allocate the live publication cell before sharing this configuration
	// concurrently. The pointer assignment itself is ordinary storage, while
	// subsequent atomic stores publish copied whole-policy objects.
	if c.Security.forwardedClientIPSettingsLive == nil {
		c.Security.forwardedClientIPSettingsLive = &atomic.Pointer[ForwardedClientIPSettings]{}
	}
	// Publish trust and header names as one copied object so a request cannot
	// combine values from separate policy updates. The startup fields stay
	// available for validation and prepublication lookup.
	c.Security.forwardedClientIPSettingsLive.Store(&ForwardedClientIPSettings{
		TrustForwardedIP: enabled,
		Headers:          headers,
	})
}

// SetTrustForwardedIPForAPIKeyACL republishes enabled with the headers read
// from the current policy, accepting nil configuration as a no-op. It delegates
// copied whole-policy storage to SetForwardedClientIPSettings.
//
// Reading the headers and storing the replacement are separate operations. A
// publisher can install a new header list between them, after which this method
// can replace that list with its earlier observation. Callers serialize coupled
// policy edits when retaining the latest headers matters and initialize live
// storage before concurrent use.
func (c *Config) SetTrustForwardedIPForAPIKeyACL(enabled bool) {
	if c == nil {
		return
	}
	// Header retention uses a separate read before publication. Serialize coupled
	// edits when another publisher could otherwise replace headers between this
	// read and the store.
	c.SetForwardedClientIPSettings(enabled, c.ForwardedClientIPSettings().Headers)
}

// normalizeProxyProbeURLs returns trimmed HTTP(S) endpoints and normalized
// recognized parser names in input order. Empty input returns nil; the first
// invalid entry returns an indexed error, and URL host/scheme validation leaves
// additional URL details for the probe consumer.
func normalizeProxyProbeURLs(targets []ProbeURLConfig) ([]ProbeURLConfig, error) {
	if len(targets) == 0 {
		return nil, nil
	}

	normalized := make([]ProbeURLConfig, 0, len(targets))
	for i, target := range targets {
		rawURL := strings.TrimSpace(target.URL)
		parser := strings.ToLower(strings.TrimSpace(target.Parser))
		if rawURL == "" {
			return nil, fmt.Errorf("entry %d: url is required", i)
		}
		if parser == "" {
			return nil, fmt.Errorf("entry %d: parser is required", i)
		}
		switch parser {
		case "ip-api", "ipify", "chatgpt-trace":
		default:
			return nil, fmt.Errorf("entry %d: unsupported parser %q", i, target.Parser)
		}
		parsed, err := url.Parse(rawURL)
		if err != nil || parsed.Host == "" {
			return nil, fmt.Errorf("entry %d: invalid url %q", i, target.URL)
		}
		if parsed.Scheme != "http" && parsed.Scheme != "https" {
			return nil, fmt.Errorf("entry %d: url scheme must be http or https", i)
		}
		normalized = append(normalized, ProbeURLConfig{URL: rawURL, Parser: parser})
	}
	return normalized, nil
}

// WaitTimeout returns the configured positive millisecond value as a duration,
// otherwise thirty seconds. The receiver must be nonnil, and callers keep
// values within time.Duration conversion limits when bypassing normal
// deployment input.
func (c *UserMessageQueueConfig) WaitTimeout() time.Duration {
	if c.WaitTimeoutMs <= 0 {
		return 30 * time.Second
	}
	// Convert configured milliseconds to the duration consumed by queue
	// acquisition contexts.
	return time.Duration(c.WaitTimeoutMs) * time.Millisecond
}

// GetEffectiveMode selects a recognized serialize or throttle mode before
// consulting the legacy Enabled flag. Its receiver must be nonnil; a recognized
// Mode wins independently of that flag.
//
// When the supplied mode is empty or unsupported, true Enabled selects
// serialize and remaining cases return disabled text. load clears an
// unsupported Mode but retains Enabled, so that loading path can still produce
// serialization through this method. Callers use this effective choice rather
// than assuming an empty Mode disables the queue.
func (c *UserMessageQueueConfig) GetEffectiveMode() string {
	if c.Mode == schedulerpolicy.MessageQueueSerialize || c.Mode == schedulerpolicy.MessageQueueThrottle {
		return c.Mode
	}
	// An absent or unsupported Mode leaves the retired Enabled switch in control,
	// so compatibility loading can still select serialized message-lock
	// acquisition.
	if c.Enabled {
		return schedulerpolicy.MessageQueueSerialize // Legacy Enabled flag.
	}
	return ""
}

// Address formats the nonnil server settings as host text followed by a colon
// and port. It performs no validation or IPv6 bracketing, so callers supply
// host spelling accepted by their listener.
func (s *ServerConfig) Address() string {
	return fmt.Sprintf("%s:%d", s.Host, s.Port)
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

// Load decodes normalized process configuration and requires a nonempty,
// sufficiently long JWT secret during validation. It uses process Viper state,
// so callers serialize loader use and handle its configuration-source,
// decoding, template-read, or validation errors.
func Load() (*Config, error) {
	return load(false)
}

// LoadForBootstrap decodes normalized process configuration while allowing a
// missing JWT secret in the returned value. Database bootstrap must supply the
// persisted secret and run full validation again before identity consumers use
// that configuration.
func LoadForBootstrap() (*Config, error) {
	return load(true)
}

// load builds startup configuration from process Viper state, selected YAML,
// and environment input, returning only a validated normalized object. Callers
// serialize the shared Viper configuration; this operation mutates its
// bindings, defaults, and in-memory compatibility mappings.
//
// Only a missing searched configuration file permits continued
// default/environment loading; explicit file access and parsing errors fail. TZ
// sets the overriding timezone value, while trusted-proxy and forwarding-header
// environment presence is decoded separately to retain explicit empty lists.
// Other fields undergo normalization, compatibility choices, and any configured
// template file read before Validate.
//
// An absent TOTP key is generated for this loaded object and marked
// unconfigured for deployment-dependent consumers. Bootstrap loading
// temporarily substitutes valid JWT text during Validate, then restores its
// absent secret in the returned configuration. The bootstrap caller later
// supplies persistence-backed signing input; this loader neither writes the
// mounted YAML nor persists generated TOTP data.
// @project-doc docs/interfaces/configuration.md#configuration_sources
func load(allowMissingJWTSecret bool) (*Config, error) {
	viper.SetConfigName("config")
	viper.SetConfigType("yaml")
	configureConfigSource(viper.SetConfigFile, viper.AddConfigPath)

	// Register environment lookup before defaults and decoding so registered
	// dotted keys use their uppercase underscore names. Explicit bindings below
	// can select a different environment spelling for a current field.
	viper.AutomaticEnv()
	viper.SetEnvKeyReplacer(strings.NewReplacer(".", "_"))
	if tz, ok := os.LookupEnv("TZ"); ok && strings.TrimSpace(tz) != "" {
		// TZ is the standard container timezone input. Put its nonblank trimmed value
		// in Viper's override layer so local-day application consumers use it ahead of
		// legacy TIMEZONE and YAML.
		viper.Set("timezone", strings.TrimSpace(tz))
	}
	if err := viper.BindEnv("server.enable_server_timing", "ENABLE_SERVER_TIMING"); err != nil {
		return nil, fmt.Errorf("bind ENABLE_SERVER_TIMING: %w", err)
	}

	// Register defaults before decoding configuration.
	setDefaults()

	if err := viper.ReadInConfig(); err != nil {
		if _, ok := err.(viper.ConfigFileNotFoundError); !ok {
			return nil, fmt.Errorf("read config error: %w", err)
		}
		// A missing searched file still permits environment-only deployments. Continue
		// with registered defaults and variables; other read errors above retain their
		// failure result for startup.
	}
	if err := applyLegacyConfigCompatibility(); err != nil {
		return nil, err
	}
	// Record list presence separately before Unmarshal so an explicitly empty
	// environment value can replace a supplied list. Viper's usual empty-value
	// lookup alone cannot express that distinction.
	trustedProxiesEnv, trustedProxiesEnvConfigured := os.LookupEnv("SERVER_TRUSTED_PROXIES")
	forwardedClientIPHeadersEnv, forwardedClientIPHeadersEnvConfigured := os.LookupEnv("SECURITY_FORWARDED_CLIENT_IP_HEADERS")
	trustedProxiesConfigured := viper.InConfig("server.trusted_proxies") ||
		viper.IsSet("server.trusted_proxies") || trustedProxiesEnvConfigured
	// The registered default makes viper.IsSet always true for this key, so
	// presence is read from the file and the environment directly.
	_, trustForwardedIPEnvConfigured := os.LookupEnv("SECURITY_TRUST_FORWARDED_IP_FOR_API_KEY_ACL")
	trustForwardedIPConfigured := viper.InConfig("security.trust_forwarded_ip_for_api_key_acl") || trustForwardedIPEnvConfigured

	if strings.TrimSpace(viper.GetString("pricing.override_file")) != "" {
		slog.Warn("pricing.override_file is retired and ignored; configure user prices in Pricing Management before upgrading")
	}
	var cfg Config
	warnLegacyResponsesWSSettings()
	if err := viper.Unmarshal(&cfg); err != nil {
		return nil, fmt.Errorf("unmarshal config error: %w", err)
	}
	if trustedProxiesEnvConfigured {
		cfg.Server.TrustedProxies = normalizeStringSlice(strings.Split(trustedProxiesEnv, ","))
	}
	if forwardedClientIPHeadersEnvConfigured {
		cfg.Security.ForwardedClientIPHeaders = normalizeStringSlice(strings.Split(forwardedClientIPHeadersEnv, ","))
	}
	cfg.Server.TrustedProxiesConfigured = trustedProxiesConfigured
	cfg.Security.TrustForwardedIPConfigured = trustForwardedIPConfigured
	cfg.Gateway.ConnectionPoolIsolation = normalizeLegacyConnectionPoolIsolation(cfg.Gateway.ConnectionPoolIsolation)
	// Normal default registration makes the sticky-escape key present even when
	// unsupplied. This compatibility guard supplies true only when that
	// registration is absent, retaining configured false values otherwise.
	if !cfg.Gateway.AdvancedScheduler.StickyEscapeEnabled && !viper.IsSet("gateway.advanced_scheduler.sticky_escape_enabled") {
		cfg.Gateway.AdvancedScheduler.StickyEscapeEnabled = true
	}

	cfg.Server.Mode = strings.ToLower(strings.TrimSpace(cfg.Server.Mode))
	if cfg.Server.Mode == "" {
		cfg.Server.Mode = "debug"
	}
	cfg.Server.FrontendURL = strings.TrimSpace(cfg.Server.FrontendURL)
	cfg.Plugins.normalize(cfg.Pricing.DataDir)
	cfg.JWT.Secret = strings.TrimSpace(cfg.JWT.Secret)
	cfg.LinuxDo.ClientID = strings.TrimSpace(cfg.LinuxDo.ClientID)
	cfg.LinuxDo.ClientSecret = strings.TrimSpace(cfg.LinuxDo.ClientSecret)
	cfg.LinuxDo.AuthorizeURL = strings.TrimSpace(cfg.LinuxDo.AuthorizeURL)
	cfg.LinuxDo.TokenURL = strings.TrimSpace(cfg.LinuxDo.TokenURL)
	cfg.LinuxDo.UserInfoURL = strings.TrimSpace(cfg.LinuxDo.UserInfoURL)
	cfg.LinuxDo.Scopes = strings.TrimSpace(cfg.LinuxDo.Scopes)
	cfg.LinuxDo.RedirectURL = strings.TrimSpace(cfg.LinuxDo.RedirectURL)
	cfg.LinuxDo.FrontendRedirectURL = strings.TrimSpace(cfg.LinuxDo.FrontendRedirectURL)
	cfg.LinuxDo.TokenAuthMethod = strings.ToLower(strings.TrimSpace(cfg.LinuxDo.TokenAuthMethod))
	cfg.LinuxDo.UserInfoEmailPath = strings.TrimSpace(cfg.LinuxDo.UserInfoEmailPath)
	cfg.LinuxDo.UserInfoIDPath = strings.TrimSpace(cfg.LinuxDo.UserInfoIDPath)
	cfg.LinuxDo.UserInfoUsernamePath = strings.TrimSpace(cfg.LinuxDo.UserInfoUsernamePath)
	applyLegacyWeChatConnectEnvCompatibility(&cfg.WeChat)
	normalizeWeChatConnectConfig(&cfg.WeChat)
	cfg.OIDC.ProviderName = strings.TrimSpace(cfg.OIDC.ProviderName)
	cfg.OIDC.ClientID = strings.TrimSpace(cfg.OIDC.ClientID)
	cfg.OIDC.ClientSecret = strings.TrimSpace(cfg.OIDC.ClientSecret)
	cfg.OIDC.IssuerURL = strings.TrimSpace(cfg.OIDC.IssuerURL)
	cfg.OIDC.DiscoveryURL = strings.TrimSpace(cfg.OIDC.DiscoveryURL)
	cfg.OIDC.AuthorizeURL = strings.TrimSpace(cfg.OIDC.AuthorizeURL)
	cfg.OIDC.TokenURL = strings.TrimSpace(cfg.OIDC.TokenURL)
	cfg.OIDC.UserInfoURL = strings.TrimSpace(cfg.OIDC.UserInfoURL)
	cfg.OIDC.JWKSURL = strings.TrimSpace(cfg.OIDC.JWKSURL)
	cfg.OIDC.Scopes = strings.TrimSpace(cfg.OIDC.Scopes)
	cfg.OIDC.RedirectURL = strings.TrimSpace(cfg.OIDC.RedirectURL)
	cfg.OIDC.FrontendRedirectURL = strings.TrimSpace(cfg.OIDC.FrontendRedirectURL)
	cfg.OIDC.TokenAuthMethod = strings.ToLower(strings.TrimSpace(cfg.OIDC.TokenAuthMethod))
	cfg.OIDC.AllowedSigningAlgs = strings.TrimSpace(cfg.OIDC.AllowedSigningAlgs)
	cfg.OIDC.UserInfoEmailPath = strings.TrimSpace(cfg.OIDC.UserInfoEmailPath)
	cfg.OIDC.UserInfoIDPath = strings.TrimSpace(cfg.OIDC.UserInfoIDPath)
	cfg.OIDC.UserInfoUsernamePath = strings.TrimSpace(cfg.OIDC.UserInfoUsernamePath)
	cfg.OIDC.UsePKCEExplicit = hasExplicitConfigOrEnv("oidc_connect.use_pkce", "OIDC_CONNECT_USE_PKCE")
	cfg.OIDC.ValidateIDTokenExplicit = hasExplicitConfigOrEnv("oidc_connect.validate_id_token", "OIDC_CONNECT_VALIDATE_ID_TOKEN")
	cfg.Dashboard.KeyPrefix = strings.TrimSpace(cfg.Dashboard.KeyPrefix)
	cfg.CORS.AllowedOrigins = normalizeStringSlice(cfg.CORS.AllowedOrigins)
	cfg.Security.ResponseHeaders.AdditionalAllowed = normalizeStringSlice(cfg.Security.ResponseHeaders.AdditionalAllowed)
	cfg.Security.ResponseHeaders.ForceRemove = normalizeStringSlice(cfg.Security.ResponseHeaders.ForceRemove)
	cfg.Security.CSP.Policy = strings.TrimSpace(cfg.Security.CSP.Policy)
	forwardedClientIPHeaders, err := NormalizeForwardedClientIPHeaders(cfg.Security.ForwardedClientIPHeaders)
	if err != nil {
		return nil, fmt.Errorf("security.forwarded_client_ip_headers: %w", err)
	}
	cfg.Security.ForwardedClientIPHeaders = forwardedClientIPHeaders
	cfg.SetForwardedClientIPSettings(cfg.Security.TrustForwardedIPForAPIKeyACL, forwardedClientIPHeaders)
	cfg.Log.Level = strings.ToLower(strings.TrimSpace(cfg.Log.Level))
	cfg.Log.Format = strings.ToLower(strings.TrimSpace(cfg.Log.Format))
	cfg.Log.ServiceName = strings.TrimSpace(cfg.Log.ServiceName)
	cfg.Log.Environment = strings.TrimSpace(cfg.Log.Environment)
	cfg.Log.StacktraceLevel = strings.ToLower(strings.TrimSpace(cfg.Log.StacktraceLevel))
	cfg.Log.Output.FilePath = strings.TrimSpace(cfg.Log.Output.FilePath)
	cfg.Gateway.ForcedCodexInstructionsTemplateFile = strings.TrimSpace(cfg.Gateway.ForcedCodexInstructionsTemplateFile)
	if cfg.Gateway.ForcedCodexInstructionsTemplateFile != "" {
		content, err := os.ReadFile(cfg.Gateway.ForcedCodexInstructionsTemplateFile)
		if err != nil {
			return nil, fmt.Errorf("read forced codex instructions template %q: %w", cfg.Gateway.ForcedCodexInstructionsTemplateFile, err)
		}
		cfg.Gateway.ForcedCodexInstructionsTemplate = string(content)
	}

	// A positive current response-ID lifetime wins. Fill a nonpositive current
	// value from the positive retired lifetime so the WS affinity consumer
	// receives one effective duration during loading.
	if cfg.Gateway.OpenAIWS.StickyResponseIDTTLSeconds <= 0 && cfg.Gateway.OpenAIWS.StickyPreviousResponseTTLSeconds > 0 {
		cfg.Gateway.OpenAIWS.StickyResponseIDTTLSeconds = cfg.Gateway.OpenAIWS.StickyPreviousResponseTTLSeconds
	}

	// Clear an unsupported queue mode before the settings reach message-queue
	// consumers. The legacy Enabled flag is retained, so GetEffectiveMode can
	// still select serialization from that flag.
	if m := cfg.Gateway.UserMessageQueue.Mode; m != "" && m != schedulerpolicy.MessageQueueSerialize && m != schedulerpolicy.MessageQueueThrottle {
		slog.Warn("invalid user_message_queue mode, disabling",
			"mode", m,
			"valid_modes", []string{schedulerpolicy.MessageQueueSerialize, schedulerpolicy.MessageQueueThrottle})
		cfg.Gateway.UserMessageQueue.Mode = ""
	}

	// Generate thirty-two random bytes as hexadecimal text when the trimmed TOTP
	// key is absent. Mark that key unconfigured so deployment-dependent features
	// can distinguish ephemeral generation from supplied encryption input.
	cfg.Totp.EncryptionKey = strings.TrimSpace(cfg.Totp.EncryptionKey)
	if cfg.Totp.EncryptionKey == "" {
		key, err := generateJWTSecret(32)
		if err != nil {
			return nil, fmt.Errorf("generate totp encryption key error: %w", err)
		}
		cfg.Totp.EncryptionKey = key
		cfg.Totp.EncryptionKeyConfigured = false
		slog.Warn("TOTP encryption key auto-generated. Consider setting a fixed key for production.")
	} else {
		cfg.Totp.EncryptionKeyConfigured = true
	}

	originalJWTSecret := cfg.JWT.Secret
	if allowMissingJWTSecret && originalJWTSecret == "" {
		// Supply temporary length-valid text for the missing JWT input so other
		// startup fields can undergo full Validate. Restore absence after success,
		// leaving persistent secret selection to database bootstrap.
		cfg.JWT.Secret = strings.Repeat("0", 32)
	}

	if err := cfg.Validate(); err != nil {
		return nil, fmt.Errorf("validate config error: %w", err)
	}

	if allowMissingJWTSecret && originalJWTSecret == "" {
		cfg.JWT.Secret = ""
	}

	if !cfg.Security.URLAllowlist.Enabled {
		slog.Warn("security.url_allowlist.enabled=false; allowlist/SSRF checks disabled (minimal format validation only).")
	}
	if !cfg.Security.ResponseHeaders.Enabled {
		slog.Warn("security.response_headers.enabled=false; configurable header filtering disabled (default allowlist only).")
	}

	if cfg.JWT.Secret != "" && isWeakJWTSecret(cfg.JWT.Secret) {
		slog.Warn("JWT secret appears weak; use a 32+ character random secret in production.")
	}
	if len(cfg.Security.ResponseHeaders.AdditionalAllowed) > 0 || len(cfg.Security.ResponseHeaders.ForceRemove) > 0 {
		slog.Info("response header policy configured",
			"additional_allowed", cfg.Security.ResponseHeaders.AdditionalAllowed,
			"force_remove", cfg.Security.ResponseHeaders.ForceRemove,
		)
	}

	return &cfg, nil
}

// configureConfigSource registers the trimmed CONFIG_FILE path when supplied,
// otherwise ordered config.yaml search directories. The selected callback must
// be callable; DATA_DIR precedes container and local paths, and current system
// paths precede the legacy path.
func configureConfigSource(setConfigFile, addConfigPath func(string)) {
	if configFile := strings.TrimSpace(os.Getenv("CONFIG_FILE")); configFile != "" {
		setConfigFile(configFile)
		return
	}

	// Register the configured data directory before container, working-directory,
	// and system paths so file discovery uses that deployment location ahead of
	// conventional config directories.
	if dataDir := strings.TrimSpace(os.Getenv("DATA_DIR")); dataDir != "" {
		addConfigPath(dataDir)
	}
	addConfigPath("/app/data")
	addConfigPath(".")
	addConfigPath("./config")
	addConfigPath("/etc/tokenrouter")
	// Keep the retired system directory last so its mounted configuration remains
	// discoverable when current locations have no file. A current system file
	// takes precedence in discovery order.
	addConfigPath("/etc/sub2api")
}

// setDefaults registers process configuration defaults and
// environment-reachable keys on the Viper singleton. Run it before Unmarshal
// with global Viper use serialized, because AutomaticEnv resolves values only
// for keys included in decoding. It supplies process defaults separately from
// installer-written values and invokes setEnvReachableDefaults for additional
// fixed keys.
func setDefaults() {
	setPluginDefaults()
	// Server
	viper.SetDefault("server.host", "0.0.0.0")
	viper.SetDefault("server.port", 8080)
	viper.SetDefault("server.mode", "release")
	viper.SetDefault("server.enable_server_timing", false)
	viper.SetDefault("server.frontend_url", "")
	viper.SetDefault("server.read_header_timeout", 10) // 10-second header read timeout.
	viper.SetDefault("server.max_header_bytes", 64*1024)
	viper.SetDefault("server.idle_timeout", 120) // 120-second idle timeout.
	viper.SetDefault("server.max_request_body_size", int64(256*1024*1024))
	// H2C defaults.
	viper.SetDefault("server.h2c.enabled", false)
	viper.SetDefault("server.h2c.max_concurrent_streams", uint32(50))      // 50 concurrent streams.
	viper.SetDefault("server.h2c.idle_timeout", 75)                        // 75 seconds.
	viper.SetDefault("server.h2c.max_read_frame_size", 1<<20)              // 1 MiB.
	viper.SetDefault("server.h2c.max_upload_buffer_per_connection", 2<<20) // 2 MiB
	viper.SetDefault("server.h2c.max_upload_buffer_per_stream", 512<<10)   // 512 KiB
	// The collector starts from the UI; these values supply its listen settings.
	viper.SetDefault("server.tls_fingerprint_collector.host", "0.0.0.0")
	viper.SetDefault("server.tls_fingerprint_collector.port", 8443)
	viper.SetDefault("server.tls_fingerprint_collector.public_base_url", "")
	viper.SetDefault("server.tls_fingerprint_collector.cert_file", "")
	viper.SetDefault("server.tls_fingerprint_collector.key_file", "")
	viper.SetDefault("server.tls_fingerprint_collector.session_ttl_seconds", 1800)
	viper.SetDefault("server.tls_fingerprint_collector.max_records_per_session", 20)

	// Log
	viper.SetDefault("log.level", "info")
	viper.SetDefault("log.format", "console")
	viper.SetDefault("log.service_name", "tokenrouter")
	viper.SetDefault("log.env", "production")
	viper.SetDefault("log.caller", true)
	viper.SetDefault("log.stacktrace_level", "error")
	viper.SetDefault("log.output.to_stdout", true)
	viper.SetDefault("log.output.to_file", true)
	viper.SetDefault("log.output.file_path", "")
	viper.SetDefault("log.rotation.max_size_mb", 100)
	viper.SetDefault("log.rotation.max_backups", 10)
	viper.SetDefault("log.rotation.max_age_days", 7)
	viper.SetDefault("log.rotation.compress", true)
	viper.SetDefault("log.rotation.local_time", true)
	viper.SetDefault("log.sampling.enabled", false)
	viper.SetDefault("log.sampling.initial", 100)
	viper.SetDefault("log.sampling.thereafter", 100)

	// CORS
	viper.SetDefault("cors.allowed_origins", []string{})
	viper.SetDefault("cors.allow_credentials", true)

	// Passkeys are disabled by default; enabling requires a relying-party domain
	// and trusted browser origins.
	viper.SetDefault("webauthn.enabled", false)
	viper.SetDefault("webauthn.rp_display_name", "TokenRouter")
	viper.SetDefault("webauthn.rp_id", "")
	viper.SetDefault("webauthn.rp_origins", []string{})

	// Security
	viper.SetDefault("security.url_allowlist.enabled", false)
	viper.SetDefault("security.url_allowlist.upstream_hosts", []string{
		"api.openai.com",
		"api.anthropic.com",
		"api.kimi.com",
		"api.moonshot.ai",
		"api.moonshot.cn",
		"open.bigmodel.cn",
		"api.z.ai",
		"api.deepseek.com",
		"api.typesafe.ai",
		"api.minimaxi.com",
		"generativelanguage.googleapis.com",
		"cloudcode-pa.googleapis.com",
		"*.openai.azure.com",
	})
	viper.SetDefault("security.url_allowlist.pricing_hosts", []string{
		"models.dev",
		"raw.githubusercontent.com",
	})
	viper.SetDefault("security.url_allowlist.crs_hosts", []string{})
	viper.SetDefault("security.url_allowlist.allow_private_hosts", true)
	viper.SetDefault("security.url_allowlist.allow_insecure_http", true)
	viper.SetDefault("security.response_headers.enabled", true)
	viper.SetDefault("security.response_headers.additional_allowed", []string{})
	viper.SetDefault("security.response_headers.force_remove", []string{})
	viper.SetDefault("security.csp.enabled", true)
	viper.SetDefault("security.csp.policy", DefaultCSPPolicy)
	viper.SetDefault("security.proxy_probe.insecure_skip_verify", false)
	viper.SetDefault("security.trust_forwarded_ip_for_api_key_acl", false)

	// Security - disable direct fallback on proxy error
	viper.SetDefault("security.proxy_fallback.allow_direct_on_error", false)

	// Billing
	viper.SetDefault("billing.circuit_breaker.enabled", true)
	viper.SetDefault("billing.circuit_breaker.failure_threshold", 5)
	viper.SetDefault("billing.circuit_breaker.reset_timeout_seconds", 30)
	viper.SetDefault("billing.circuit_breaker.half_open_requests", 3)
	viper.SetDefault("billing.minimum_balance_reserve", 0.000001)

	// Turnstile
	viper.SetDefault("turnstile.required", false)

	// LinuxDo Connect OAuth login.
	viper.SetDefault("linuxdo_connect.enabled", false)
	viper.SetDefault("linuxdo_connect.client_id", "")
	viper.SetDefault("linuxdo_connect.client_secret", "")
	viper.SetDefault("linuxdo_connect.authorize_url", "https://connect.linux.do/oauth2/authorize")
	viper.SetDefault("linuxdo_connect.token_url", "https://connect.linux.do/oauth2/token")
	viper.SetDefault("linuxdo_connect.userinfo_url", "https://connect.linux.do/api/user")
	viper.SetDefault("linuxdo_connect.scopes", "user")
	viper.SetDefault("linuxdo_connect.redirect_url", "")
	viper.SetDefault("linuxdo_connect.frontend_redirect_url", "/auth/linuxdo/callback")
	viper.SetDefault("linuxdo_connect.token_auth_method", "client_secret_post")
	viper.SetDefault("linuxdo_connect.use_pkce", false)
	viper.SetDefault("linuxdo_connect.userinfo_email_path", "")
	viper.SetDefault("linuxdo_connect.userinfo_id_path", "")
	viper.SetDefault("linuxdo_connect.userinfo_username_path", "")

	// WeChat Connect OAuth login.
	viper.SetDefault("wechat_connect.enabled", false)
	viper.SetDefault("wechat_connect.app_id", "")
	viper.SetDefault("wechat_connect.app_secret", "")
	viper.SetDefault("wechat_connect.open_app_id", "")
	viper.SetDefault("wechat_connect.open_app_secret", "")
	viper.SetDefault("wechat_connect.mp_app_id", "")
	viper.SetDefault("wechat_connect.mp_app_secret", "")
	viper.SetDefault("wechat_connect.mobile_app_id", "")
	viper.SetDefault("wechat_connect.mobile_app_secret", "")
	viper.SetDefault("wechat_connect.open_enabled", false)
	viper.SetDefault("wechat_connect.mp_enabled", false)
	viper.SetDefault("wechat_connect.mobile_enabled", false)
	viper.SetDefault("wechat_connect.mode", defaultWeChatConnectMode)
	viper.SetDefault("wechat_connect.scopes", defaultWeChatConnectScopes)
	viper.SetDefault("wechat_connect.redirect_url", "")
	viper.SetDefault("wechat_connect.frontend_redirect_url", defaultWeChatConnectFrontendRedirect)

	// Generic OIDC login.
	viper.SetDefault("oidc_connect.enabled", false)
	viper.SetDefault("oidc_connect.provider_name", "OIDC")
	viper.SetDefault("oidc_connect.client_id", "")
	viper.SetDefault("oidc_connect.client_secret", "")
	viper.SetDefault("oidc_connect.issuer_url", "")
	viper.SetDefault("oidc_connect.discovery_url", "")
	viper.SetDefault("oidc_connect.authorize_url", "")
	viper.SetDefault("oidc_connect.token_url", "")
	viper.SetDefault("oidc_connect.userinfo_url", "")
	viper.SetDefault("oidc_connect.jwks_url", "")
	viper.SetDefault("oidc_connect.scopes", "openid email profile")
	viper.SetDefault("oidc_connect.redirect_url", "")
	viper.SetDefault("oidc_connect.frontend_redirect_url", "/auth/oidc/callback")
	viper.SetDefault("oidc_connect.token_auth_method", "client_secret_post")
	viper.SetDefault("oidc_connect.use_pkce", true)
	viper.SetDefault("oidc_connect.validate_id_token", true)
	viper.SetDefault("oidc_connect.allowed_signing_algs", "RS256,ES256,PS256")
	viper.SetDefault("oidc_connect.clock_skew_seconds", 120)
	viper.SetDefault("oidc_connect.require_email_verified", false)
	viper.SetDefault("oidc_connect.userinfo_email_path", "")
	viper.SetDefault("oidc_connect.userinfo_id_path", "")
	viper.SetDefault("oidc_connect.userinfo_username_path", "")

	// DingTalk Connect OAuth login.
	viper.SetDefault("dingtalk_connect.enabled", false)
	viper.SetDefault("dingtalk_connect.authorize_url", "https://login.dingtalk.com/oauth2/auth")
	viper.SetDefault("dingtalk_connect.token_url", "https://api.dingtalk.com/v1.0/oauth2/userAccessToken")
	viper.SetDefault("dingtalk_connect.userinfo_url", "https://api.dingtalk.com/v1.0/contact/users/me")
	viper.SetDefault("dingtalk_connect.scopes", "openid")
	viper.SetDefault("dingtalk_connect.frontend_redirect_url", "/auth/dingtalk/callback")
	viper.SetDefault("dingtalk_connect.dingtalk_app_kind", "internal_app")
	viper.SetDefault("dingtalk_connect.app_type", "public")
	viper.SetDefault("dingtalk_connect.corp_restriction_policy", "none")
	viper.SetDefault("dingtalk_connect.require_email", true)
	viper.SetDefault("dingtalk_connect.username_overwrite_policy", "if_empty")

	// Database
	viper.SetDefault("database.host", "localhost")
	viper.SetDefault("database.port", 5432)
	viper.SetDefault("database.user", "postgres")
	viper.SetDefault("database.password", "postgres")
	viper.SetDefault("database.dbname", "tokenrouter")
	viper.SetDefault("database.sslmode", "prefer")
	viper.SetDefault("database.max_open_conns", 256)
	viper.SetDefault("database.max_idle_conns", 128)
	viper.SetDefault("database.conn_max_lifetime_minutes", 30)
	viper.SetDefault("database.conn_max_idle_time_minutes", 5)

	// Redis
	viper.SetDefault("redis.host", "localhost")
	viper.SetDefault("redis.port", 6379)
	viper.SetDefault("redis.username", "")
	viper.SetDefault("redis.password", "")
	viper.SetDefault("redis.db", 0)
	viper.SetDefault("redis.dial_timeout_seconds", 5)
	viper.SetDefault("redis.read_timeout_seconds", 3)
	viper.SetDefault("redis.write_timeout_seconds", 3)
	viper.SetDefault("redis.pool_size", 1024)
	viper.SetDefault("redis.min_idle_conns", 128)
	viper.SetDefault("redis.enable_tls", false)

	// Batch Image queue
	viper.SetDefault("batch_image.max_items_per_job_default", 200)
	viper.SetDefault("batch_image.max_output_images_per_job", 200)
	viper.SetDefault("batch_image.max_output_images_per_item", 4)
	viper.SetDefault("batch_image.max_prompt_chars_per_item", 8000)
	viper.SetDefault("batch_image.max_reference_images_per_job", 1000)
	viper.SetDefault("batch_image.max_reference_inline_bytes_per_job", 134217728)
	viper.SetDefault("batch_image.default_response_mime_type", "image/png")
	viper.SetDefault("batch_image.default_image_size", "1K")
	viper.SetDefault("batch_image.max_download_items_zip", 200)
	viper.SetDefault("batch_image.max_download_bytes_per_request", 536870912)
	viper.SetDefault("batch_image.max_download_duration_seconds", 600)
	viper.SetDefault("batch_image.max_download_concurrency_per_user", 1)
	viper.SetDefault("batch_image.input_retention_after_terminal_hours", 24)
	viper.SetDefault("batch_image.output_retention_after_terminal_hours", 72)
	viper.SetDefault("batch_image.cleanup_interval_minutes", 30)
	viper.SetDefault("batch_image.cleanup_batch_size", 100)
	viper.SetDefault("batch_image.queue_ready_key", "batch_image:queue:ready")
	viper.SetDefault("batch_image.queue_delayed_key", "batch_image:queue:delayed")
	viper.SetDefault("batch_image.queue_active_key", "batch_image:queue:active")
	viper.SetDefault("batch_image.inflight_key_prefix", "batch_image:queue:inflight:")
	viper.SetDefault("batch_image.lock_key_prefix", "batch_image:queue:lock:")
	viper.SetDefault("batch_image.inflight_ttl_seconds", 604800)
	viper.SetDefault("batch_image.job_lock_ttl_seconds", 300)
	viper.SetDefault("batch_image.default_requeue_delay_seconds", 30)
	viper.SetDefault("batch_image.error_retry_delay_seconds", 60)
	viper.SetDefault("batch_image.lock_conflict_delay_seconds", 5)
	viper.SetDefault("batch_image.stale_active_after_seconds", 600)
	viper.SetDefault("batch_image.delayed_mover_interval_seconds", 5)
	viper.SetDefault("batch_image.recovery_interval_seconds", 300)
	viper.SetDefault("batch_image.delayed_move_limit", 100)
	viper.SetDefault("batch_image.recover_limit", 100)
	viper.SetDefault("batch_image.vertex_project_id", "")
	viper.SetDefault("batch_image.vertex_location", "global")
	viper.SetDefault("batch_image.vertex_managed_gcs_bucket", "")
	viper.SetDefault("batch_image.vertex_managed_gcs_prefix", "batch-image/{env}/{batch_id}")
	viper.SetDefault("batch_image.vertex_batch_prediction_base_url", "")
	viper.SetDefault("batch_image.vertex_gcs_base_url", "")

	// Creative Studio.
	viper.SetDefault("creative.enabled", true)
	viper.SetDefault("creative.queue_enabled", true)
	viper.SetDefault("creative.transient_ttl_seconds", 1800)
	viper.SetDefault("creative.max_asset_bytes", 33554432)
	viper.SetDefault("creative.max_total_input_bytes", 67108864)
	viper.SetDefault("creative.max_prompt_chars", 8000)
	viper.SetDefault("creative.default_response_mime_type", "image/png")
	viper.SetDefault("creative.default_image_size", "1K")
	viper.SetDefault("creative.queue_ready_key", "creative:queue:ready")
	viper.SetDefault("creative.queue_delayed_key", "creative:queue:delayed")
	viper.SetDefault("creative.queue_active_key", "creative:queue:active")
	viper.SetDefault("creative.inflight_key_prefix", "creative:queue:inflight:")
	viper.SetDefault("creative.lock_key_prefix", "creative:queue:lock:")
	viper.SetDefault("creative.inflight_ttl_seconds", 604800)
	viper.SetDefault("creative.job_lock_ttl_seconds", 300)
	viper.SetDefault("creative.default_requeue_delay_seconds", 30)
	viper.SetDefault("creative.error_retry_delay_seconds", 60)
	viper.SetDefault("creative.lock_conflict_delay_seconds", 5)
	viper.SetDefault("creative.stale_active_after_seconds", 600)
	viper.SetDefault("creative.delayed_mover_interval_seconds", 5)
	viper.SetDefault("creative.recovery_interval_seconds", 300)
	viper.SetDefault("creative.delayed_move_limit", 100)
	viper.SetDefault("creative.recover_limit", 100)
	viper.SetDefault("creative.execute_timeout_seconds", 300)
	viper.SetDefault("creative.max_execute_attempts", 3)

	// Ops
	viper.SetDefault("ops.enabled", true)
	viper.SetDefault("ops.cleanup.enabled", true)
	viper.SetDefault("ops.cleanup.schedule", "0 3 * * *")
	viper.SetDefault("ops.cleanup.batch_size", 1000)
	viper.SetDefault("ops.cleanup.batch_pause_ms", 200)
	// Retain 30 days across Ops datasets by default.
	viper.SetDefault("request_log.retention_days", 30)
	viper.SetDefault("ops.cleanup.error_log_retention_days", 30)
	viper.SetDefault("ops.cleanup.system_log_retention_days", 30)
	viper.SetDefault("ops.cleanup.minute_metrics_retention_days", 30)
	viper.SetDefault("ops.cleanup.hourly_metrics_retention_days", 30)
	viper.SetDefault("ops.aggregation.enabled", true)
	viper.SetDefault("ops.metrics_collector_cache.enabled", true)
	// Register the accepted collector-cache lifetime copied into ops.Options. The
	// current MetricsQueries reader does not consume it; sixty-five seconds
	// exceeds the collector's default/minimum one-minute interval, while runtime
	// cadence can be longer.
	viper.SetDefault("ops.metrics_collector_cache.ttl", 65*time.Second)

	// JWT
	viper.SetDefault("jwt.secret", "")
	viper.SetDefault("jwt.expire_hour", 24)
	viper.SetDefault("jwt.access_token_expire_minutes", 0) // Zero uses expire_hour.
	viper.SetDefault("jwt.refresh_token_expire_days", 30)  // 30-day refresh-token lifetime.
	viper.SetDefault("jwt.refresh_window_minutes", 2)      // Permit refresh two minutes before expiry.

	// TOTP
	viper.SetDefault("totp.encryption_key", "")

	// Default
	// Admin credentials are created via the setup flow (web wizard / CLI / AUTO_SETUP).
	// Do not ship fixed defaults here to avoid insecure "known credentials" in
	// production.
	viper.SetDefault("default.admin_email", "")
	viper.SetDefault("default.admin_password", "")
	viper.SetDefault("default.user_concurrency", 5)
	viper.SetDefault("default.user_balance", 0)
	viper.SetDefault("default.api_key_prefix", "sk-")
	viper.SetDefault("default.rate_multiplier", 1.0)

	// RateLimit
	viper.SetDefault("rate_limit.overload_cooldown_minutes", 10)
	viper.SetDefault("rate_limit.oauth_401_cooldown_minutes", 10)

	// Pricing uses the models.dev catalog and local supplements for additional
	// billing dimensions.
	viper.SetDefault("pricing.remote_url", "https://models.dev/catalog.json?type=all")
	viper.SetDefault("pricing.data_dir", "./data")
	viper.SetDefault("pricing.fallback_file", "")
	viper.SetDefault("pricing.check_interval_minutes", 10)

	// Default time zone for local-day statistics.
	viper.SetDefault("timezone", "Asia/Shanghai")
	viper.SetDefault("team.enabled", true)
	viper.SetDefault("team.self_service_enabled", true)
	viper.SetDefault("team.default_member_limit", 10)

	// API Key auth cache
	viper.SetDefault("api_key_auth_cache.l1_size", 65535)
	viper.SetDefault("api_key_auth_cache.l1_ttl_seconds", 15)
	viper.SetDefault("api_key_auth_cache.l2_ttl_seconds", 300)
	viper.SetDefault("api_key_auth_cache.negative_ttl_seconds", 30)
	viper.SetDefault("api_key_auth_cache.jitter_percent", 10)
	viper.SetDefault("api_key_auth_cache.singleflight", true)
	viper.SetDefault("api_key_auth_cache.lookup_concurrency", 64)
	viper.SetDefault("api_key_auth_cache.invalid_abuse.enabled", true)
	viper.SetDefault("api_key_auth_cache.invalid_abuse.threshold", 120)
	viper.SetDefault("api_key_auth_cache.invalid_abuse.window_seconds", 60)
	viper.SetDefault("api_key_auth_cache.invalid_abuse.block_seconds", 60)
	viper.SetDefault("api_key_auth_cache.invalid_abuse.capacity", 16384)

	// Subscription auth L1 cache
	viper.SetDefault("subscription_cache.l1_size", 16384)
	viper.SetDefault("subscription_cache.l1_ttl_seconds", 10)
	viper.SetDefault("subscription_cache.jitter_percent", 10)

	// Dashboard cache
	viper.SetDefault("dashboard_cache.enabled", true)
	viper.SetDefault("dashboard_cache.key_prefix", "tokenrouter:")
	viper.SetDefault("dashboard_cache.stats_fresh_ttl_seconds", 15)
	viper.SetDefault("dashboard_cache.stats_ttl_seconds", 30)
	viper.SetDefault("dashboard_cache.stats_refresh_timeout_seconds", 30)

	// Dashboard aggregation
	viper.SetDefault("dashboard_aggregation.enabled", true)
	viper.SetDefault("dashboard_aggregation.interval_seconds", 60)
	viper.SetDefault("dashboard_aggregation.lookback_seconds", 120)
	viper.SetDefault("dashboard_aggregation.backfill_enabled", false)
	viper.SetDefault("dashboard_aggregation.backfill_max_days", 31)
	viper.SetDefault("dashboard_aggregation.retention.usage_logs_days", 90)
	viper.SetDefault("dashboard_aggregation.retention.usage_billing_dedup_days", 365)
	viper.SetDefault("dashboard_aggregation.retention.hourly_days", 180)
	viper.SetDefault("dashboard_aggregation.retention.daily_days", 730)
	viper.SetDefault("dashboard_aggregation.recompute_days", 2)

	// Usage cleanup task
	viper.SetDefault("usage_cleanup.enabled", true)
	viper.SetDefault("usage_cleanup.max_range_days", 31)
	viper.SetDefault("usage_cleanup.batch_size", 5000)
	viper.SetDefault("usage_cleanup.worker_interval_seconds", 10)
	viper.SetDefault("usage_cleanup.task_timeout_seconds", 1800)

	// Idempotency
	viper.SetDefault("idempotency.observe_only", true)
	viper.SetDefault("idempotency.default_ttl_seconds", 86400)
	viper.SetDefault("idempotency.system_operation_ttl_seconds", 3600)
	viper.SetDefault("idempotency.processing_timeout_seconds", 30)
	viper.SetDefault("idempotency.failed_retry_backoff_seconds", 5)
	viper.SetDefault("idempotency.max_stored_response_len", 64*1024)
	viper.SetDefault("idempotency.cleanup_interval_seconds", 60)
	viper.SetDefault("idempotency.cleanup_batch_size", 500)

	// Gateway
	viper.SetDefault("gateway.response_header_timeout", 600) // Allow ten minutes for upstream LLM queueing before response headers.
	viper.SetDefault("gateway.openai_response_header_timeout", 0)
	viper.SetDefault("gateway.openai_compact_model", "")
	viper.SetDefault("gateway.grok_response_header_timeout", 120)
	viper.SetDefault("gateway.openai_first_output_timeout_seconds", 0)
	viper.SetDefault("gateway.openai_high_effort_first_output_timeout_seconds", 0)
	viper.SetDefault("gateway.log_upstream_error_body", true)
	viper.SetDefault("gateway.log_upstream_error_body_max_bytes", 2048)
	viper.SetDefault("gateway.inject_beta_for_apikey", false)
	viper.SetDefault("gateway.failover_on_400", false)
	viper.SetDefault("gateway.max_provider_switches", 10)
	viper.SetDefault("gateway.max_provider_switches_gemini", 3)
	viper.SetDefault("gateway.force_codex_cli", false)
	viper.SetDefault("gateway.codex_image_generation_bridge_enabled", false)
	viper.SetDefault("gateway.openai_passthrough_allow_timeout_headers", false)
	viper.SetDefault("gateway.live.max_session_duration_seconds", 3600)
	// OpenAI Responses WebSocket defaults.
	viper.SetDefault("gateway.openai_ws.client_first_message_timeout_seconds", DefaultOpenAIWSClientFirstMessageTimeoutSeconds)
	viper.SetDefault("gateway.openai_ws.ingress_inter_turn_idle_timeout_seconds", 300)
	viper.SetDefault("gateway.openai_ws.max_ingress_connections_per_api_key", 64)
	viper.SetDefault("gateway.openai_ws.ingress_previous_response_recovery_enabled", true)
	viper.SetDefault("gateway.openai_ws.client_read_limit_bytes", 64*1024*1024)
	viper.SetDefault("gateway.openai_ws.http_bridge_threshold_bytes", 15*1024*1024)
	viper.SetDefault("gateway.openai_ws.max_conns_per_provider", 128)
	viper.SetDefault("gateway.openai_ws.min_idle_per_provider", 4)
	viper.SetDefault("gateway.openai_ws.max_idle_per_provider", 12)
	viper.SetDefault("gateway.openai_ws.dynamic_max_conns_by_provider_concurrency_enabled", true)
	viper.SetDefault("gateway.openai_ws.oauth_max_conns_factor", 1.0)
	viper.SetDefault("gateway.openai_ws.apikey_max_conns_factor", 1.0)
	viper.SetDefault("gateway.openai_ws.dial_timeout_seconds", 10)
	viper.SetDefault("gateway.openai_ws.read_timeout_seconds", 900)
	viper.SetDefault("gateway.openai_ws.write_timeout_seconds", 120)
	viper.SetDefault("gateway.openai_ws.pool_target_utilization", 0.7)
	viper.SetDefault("gateway.openai_ws.queue_limit_per_conn", 64)
	viper.SetDefault("gateway.openai_ws.prewarm_cooldown_ms", 300)
	viper.SetDefault("gateway.advanced_scheduler.lb_top_k", 7)
	viper.SetDefault("gateway.advanced_scheduler.ewma_error_rate_alpha", 0.2)
	viper.SetDefault("gateway.advanced_scheduler.ewma_ttft_alpha", 0.2)
	viper.SetDefault("gateway.openai_ws.sticky_session_ttl_seconds", 3600)
	viper.SetDefault("gateway.openai_ws.session_hash_read_old_fallback", true)
	viper.SetDefault("gateway.openai_ws.session_hash_dual_write_old", true)
	viper.SetDefault("gateway.openai_ws.metadata_bridge_enabled", true)
	viper.SetDefault("gateway.openai_ws.sticky_response_id_ttl_seconds", 3600)
	viper.SetDefault("gateway.openai_ws.sticky_previous_response_ttl_seconds", 3600)
	viper.SetDefault("gateway.advanced_scheduler.score_weights.priority", 1.0)
	viper.SetDefault("gateway.advanced_scheduler.score_weights.load", 1.0)
	viper.SetDefault("gateway.advanced_scheduler.score_weights.queue", 0.7)
	viper.SetDefault("gateway.advanced_scheduler.score_weights.error_rate", 0.8)
	viper.SetDefault("gateway.advanced_scheduler.score_weights.ttft", 0.5)
	viper.SetDefault("gateway.advanced_scheduler.score_weights.reset", 0.0)
	viper.SetDefault("gateway.advanced_scheduler.score_weights.quota_headroom", 0.0)
	viper.SetDefault("gateway.advanced_scheduler.score_weights.previous_response", 5.0)
	viper.SetDefault("gateway.advanced_scheduler.score_weights.session_sticky", 3.0)
	// OpenAI upstream HTTP version policy.
	viper.SetDefault("gateway.openai_http2.enabled", true)
	viper.SetDefault("gateway.openai_http2.allow_proxy_fallback_to_http1", true)
	viper.SetDefault("gateway.openai_http2.fallback_error_threshold", 2)
	viper.SetDefault("gateway.openai_http2.fallback_window_seconds", 60)
	viper.SetDefault("gateway.openai_http2.fallback_ttl_seconds", 600)
	viper.SetDefault("gateway.openai_proxy_stream_circuit.disabled", false)
	viper.SetDefault("gateway.openai_proxy_stream_circuit.failure_threshold", 2)
	viper.SetDefault("gateway.openai_proxy_stream_circuit.window_seconds", 60)
	viper.SetDefault("gateway.openai_proxy_stream_circuit.ttl_seconds", 600)
	// The Grok quota gate applies to scheduling; admin QueryQuota bypasses it.
	// Only providers marked free are gated, so unknown and paid tiers are unaffected.
	viper.SetDefault("gateway.grok.free_quota_soft_gate_enabled", true)
	viper.SetDefault("gateway.grok.password_auth_enabled", false)
	// Default nominal allowance: 500,000 tokens in a rolling 24-hour window.
	viper.SetDefault("gateway.grok.free_quota_token_limit", int64(500_000))
	viper.SetDefault("gateway.grok.free_quota_soft_gate_percent", 95)
	viper.SetDefault("gateway.grok.free_quota_window_hours", 24)
	viper.SetDefault("gateway.grok.free_quota_stats_cache_seconds", 60)
	// Supplier monitoring is disabled by default; manual admin queries remain available.
	viper.SetDefault("gateway.cn_providers.monitor_enabled", false)
	viper.SetDefault("gateway.cn_providers.balance_threshold", 0.5)
	viper.SetDefault("gateway.cn_providers.interval_minutes", 10)
	viper.SetDefault("gateway.cn_providers.concurrency", 4)
	viper.SetDefault("gateway.cn_providers.probe_timeout_seconds", 20)
	viper.SetDefault("gateway.cn_providers.round_timeout_seconds", 300)
	viper.SetDefault("gateway.image_concurrency.enabled", false)
	viper.SetDefault("gateway.image_concurrency.max_concurrent_requests", 0)
	viper.SetDefault("gateway.image_concurrency.overflow_mode", ImageConcurrencyOverflowModeReject)
	viper.SetDefault("gateway.image_concurrency.wait_timeout_seconds", 30)
	viper.SetDefault("gateway.image_concurrency.max_waiting_requests", 100)
	viper.SetDefault("gateway.antigravity_fallback_cooldown_minutes", 1)
	viper.SetDefault("gateway.antigravity_extra_retries", 10)
	viper.SetDefault("gateway.max_body_size", int64(256*1024*1024))
	viper.SetDefault("gateway.text_max_body_size", int64(32*1024*1024))
	viper.SetDefault("gateway.upstream_response_read_max_bytes", DefaultUpstreamResponseReadMaxBytes)
	viper.SetDefault("gateway.models_list_read_max_bytes", DefaultModelsListReadMaxBytes)
	viper.SetDefault("gateway.proxy_probe_response_read_max_bytes", int64(1024*1024))
	viper.SetDefault("gateway.gemini_debug_response_headers", false)
	viper.SetDefault("gateway.connection_pool_isolation", ConnectionPoolIsolationProviderProxy)
	// Upstream HTTP connection-pool defaults.
	viper.SetDefault("gateway.max_idle_conns", 2560)          // Total idle connection limit.
	viper.SetDefault("gateway.max_idle_conns_per_host", 120)  // Idle connection limit per host.
	viper.SetDefault("gateway.max_conns_per_host", 1024)      // Active and idle connection limit per host.
	viper.SetDefault("gateway.idle_conn_timeout_seconds", 90) // Idle timeout, in seconds.
	viper.SetDefault("gateway.max_upstream_clients", 5000)
	viper.SetDefault("gateway.client_idle_ttl_seconds", 900)
	viper.SetDefault("gateway.concurrency_slot_ttl_minutes", 30) // Slot lifetime accommodates long requests.
	viper.SetDefault("gateway.stream_data_interval_timeout", 180)
	viper.SetDefault("gateway.stream_keepalive_interval", 10)
	viper.SetDefault("gateway.image_stream_data_interval_timeout", 900)
	viper.SetDefault("gateway.image_stream_keepalive_interval", 10)
	viper.SetDefault("gateway.image_nonstream_keepalive_interval", 0)
	viper.SetDefault("gateway.max_line_size", 500*1024*1024)
	viper.SetDefault("gateway.scheduling.sticky_session_max_waiting", 3)
	viper.SetDefault("gateway.scheduling.sticky_session_wait_timeout", 120*time.Second)
	viper.SetDefault("gateway.scheduling.fallback_wait_timeout", 30*time.Second)
	viper.SetDefault("gateway.scheduling.fallback_max_waiting", 100)
	viper.SetDefault("gateway.scheduling.fallback_selection_mode", "last_used")
	viper.SetDefault("gateway.scheduling.prefer_soonest_reset", false)
	viper.SetDefault("gateway.scheduling.load_batch_enabled", true)
	viper.SetDefault("gateway.scheduling.load_batch_cache_ttl_ms", 200)
	viper.SetDefault("gateway.scheduling.snapshot_mget_chunk_size", 128)
	viper.SetDefault("gateway.scheduling.snapshot_write_chunk_size", 256)
	viper.SetDefault("gateway.scheduling.slot_cleanup_interval", 30*time.Second)
	viper.SetDefault("gateway.scheduling.db_fallback_enabled", true)
	viper.SetDefault("gateway.scheduling.db_fallback_timeout_seconds", 0)
	viper.SetDefault("gateway.scheduling.db_fallback_max_qps", 0)
	viper.SetDefault("gateway.scheduling.outbox_poll_interval_seconds", 1)
	viper.SetDefault("gateway.scheduling.outbox_lag_warn_seconds", 5)
	viper.SetDefault("gateway.scheduling.outbox_lag_rebuild_seconds", 10)
	viper.SetDefault("gateway.scheduling.outbox_lag_rebuild_failures", 3)
	viper.SetDefault("gateway.scheduling.outbox_backlog_rebuild_rows", 10000)
	viper.SetDefault("gateway.scheduling.full_rebuild_interval_seconds", 300)
	viper.SetDefault("gateway.usage_record.worker_count", 128)
	viper.SetDefault("gateway.usage_record.queue_size", 16384)
	viper.SetDefault("gateway.usage_record.task_timeout_seconds", 5)
	// The default sync policy runs overflow tasks inline after the response is written.
	// sample and drop can discard billing tasks, creating charge and usage_logs
	// reconciliation gaps (issue #3656).
	// Use those policies only when the deployment accepts that loss.
	viper.SetDefault("gateway.usage_record.overflow_policy", UsageRecordOverflowPolicySync)
	viper.SetDefault("gateway.usage_record.overflow_sample_percent", 10)
	viper.SetDefault("gateway.usage_record.auto_scale_enabled", true)
	viper.SetDefault("gateway.usage_record.auto_scale_min_workers", 128)
	viper.SetDefault("gateway.usage_record.auto_scale_max_workers", 512)
	viper.SetDefault("gateway.usage_record.auto_scale_up_queue_percent", 70)
	viper.SetDefault("gateway.usage_record.auto_scale_down_queue_percent", 15)
	viper.SetDefault("gateway.usage_record.auto_scale_up_step", 32)
	viper.SetDefault("gateway.usage_record.auto_scale_down_step", 16)
	viper.SetDefault("gateway.usage_record.auto_scale_check_interval_seconds", 3)
	viper.SetDefault("gateway.usage_record.auto_scale_cooldown_seconds", 10)
	viper.SetDefault("gateway.user_group_rate_cache_ttl_seconds", 30)
	viper.SetDefault("gateway.models_list_cache_ttl_seconds", 15)
	// User-message queue defaults.
	viper.SetDefault("gateway.user_message_queue.enabled", false)
	viper.SetDefault("gateway.user_message_queue.lock_ttl_ms", 120000)
	viper.SetDefault("gateway.user_message_queue.wait_timeout_ms", 30000)
	viper.SetDefault("gateway.user_message_queue.min_delay_ms", 200)
	viper.SetDefault("gateway.user_message_queue.max_delay_ms", 2000)
	viper.SetDefault("gateway.user_message_queue.cleanup_interval_seconds", 60)

	// Enable TLS fingerprint support by default.
	viper.SetDefault("gateway.tls_fingerprint.enabled", true)
	viper.SetDefault("concurrency.ping_interval", 10)

	// TokenRefresh
	viper.SetDefault("token_refresh.enabled", true)
	viper.SetDefault("token_refresh.check_interval_minutes", 5)        // Check every five minutes.
	viper.SetDefault("token_refresh.refresh_before_expiry_hours", 0.5) // Half-hour proactive refresh window.
	viper.SetDefault("token_refresh.max_retries", 3)                   // Three total exchange attempts, including the first.
	viper.SetDefault("token_refresh.retry_backoff_seconds", 2)         // Two-second base backoff.
	viper.SetDefault("token_refresh.candidate_page_size", 200)
	viper.SetDefault("token_refresh.provider_concurrency", 4)
	viper.SetDefault("token_refresh.provider_qps", 2)
	viper.SetDefault("token_refresh.provider_failure_threshold", 3)
	viper.SetDefault("token_refresh.attempt_timeout_seconds", 15)
	viper.SetDefault("token_refresh.cycle_timeout_seconds", 240)

	// Gemini OAuth credential and scope inputs remain empty by default.
	// Application construction passes them to the OAuth consumer, which selects
	// its effective client configuration.
	viper.SetDefault("gemini.oauth.client_id", "")
	viper.SetDefault("gemini.oauth.client_secret", "")
	viper.SetDefault("gemini.oauth.scopes", "")
	viper.SetDefault("gemini.quota.policy", "")

	setEnvReachableDefaults()
}

// setEnvReachableDefaults registers remaining finite configuration keys so
// environment-only input participates in Viper decoding. Call it before
// Unmarshal as part of setDefaults, using serialized access to process Viper
// state.
//
// Trusted-proxy and forwarded-header lists use BindEnv instead of a default,
// because load needs to distinguish absent policy from an explicitly supplied
// empty list. Their values are then decoded with LookupEnv. Sticky-escape
// defaults allow explicit zero error rate to remain configured while a zero
// TTFT still reaches validation. Other unconfigured scalar fields use
// registered defaults or downstream interpretation after decoding.
func setEnvReachableDefaults() {
	viper.SetDefault("gateway.forced_codex_instructions_template_file", "")
	viper.SetDefault("gateway.session_idle_timeout_minutes", 0)
	viper.SetDefault("gateway.user_message_queue.mode", "")
	viper.SetDefault("update.proxy_url", "")

	// Register sticky-escape defaults as decode keys so supplied zero error rate
	// stays meaningful and zero TTFT reaches validation. YAML and usable
	// environment values can replace these defaults.
	viper.SetDefault("gateway.advanced_scheduler.sticky_escape_enabled", true)
	viper.SetDefault("gateway.advanced_scheduler.sticky_escape_error_rate", 0.5)
	viper.SetDefault("gateway.advanced_scheduler.sticky_escape_ttft_ms", 15000)

	// server.trusted_proxies and security.forwarded_client_ip_headers use BindEnv:
	// load must distinguish configured values from absent values (#4600), while IsSet
	// treats SetDefault entries as configured even when the operator supplies nothing.
	// load parses these two environment variables through os.LookupEnv.
	// BindEnv adds the keys to AllKeys for the environment-reachability check,
	// while leaving IsSet false for absent variables. These complete BindEnv
	// arguments cannot fail.
	_ = viper.BindEnv("server.trusted_proxies", "SERVER_TRUSTED_PROXIES")
	_ = viper.BindEnv("security.forwarded_client_ip_headers", "SECURITY_FORWARDED_CLIENT_IP_HEADERS")

	// Register OAuth client secrets and other login settings so deployments
	// can supply them through environment variables.
	for _, provider := range []string{"github_oauth", "google_oauth"} {
		viper.SetDefault(provider+".enabled", false)
		viper.SetDefault(provider+".client_id", "")
		viper.SetDefault(provider+".client_secret", "")
		viper.SetDefault(provider+".authorize_url", "")
		viper.SetDefault(provider+".token_url", "")
		viper.SetDefault(provider+".userinfo_url", "")
		viper.SetDefault(provider+".emails_url", "")
		viper.SetDefault(provider+".scopes", "")
		viper.SetDefault(provider+".redirect_url", "")
		viper.SetDefault(provider+".frontend_redirect_url", "")
	}

	viper.SetDefault("dingtalk_connect.client_id", "")
	viper.SetDefault("dingtalk_connect.client_secret", "")
	viper.SetDefault("dingtalk_connect.internal_corp_id", "")
	viper.SetDefault("dingtalk_connect.redirect_url", "")
	viper.SetDefault("dingtalk_connect.bypass_registration", false)
	viper.SetDefault("dingtalk_connect.username_attribute_key", "")
	viper.SetDefault("dingtalk_connect.enable_attribute_matching", false)
	viper.SetDefault("dingtalk_connect.enable_attribute_sync", false)
	viper.SetDefault("dingtalk_connect.attribute_sync_fields", []string{})
	viper.SetDefault("dingtalk_connect.attribute_sync_overwrite_policy", "")
	viper.SetDefault("dingtalk_connect.sync_display_name", false)
	viper.SetDefault("dingtalk_connect.sync_display_name_attr_key", "")
	viper.SetDefault("dingtalk_connect.sync_display_name_attr_name", "")
	viper.SetDefault("dingtalk_connect.sync_dept", false)
	viper.SetDefault("dingtalk_connect.sync_dept_attr_key", "")
	viper.SetDefault("dingtalk_connect.sync_dept_attr_name", "")
	viper.SetDefault("dingtalk_connect.sync_corp_email", false)
	viper.SetDefault("dingtalk_connect.sync_corp_email_attr_key", "")
	viper.SetDefault("dingtalk_connect.sync_corp_email_attr_name", "")
}

// Validate checks deployment constraints and returns the first encountered
// configuration error. The receiver must be nonnil, and callers serialize this
// mutating operation before handing configuration to concurrently running
// consumers.
//
// Validation also normalizes known pricing sources, header names, probe
// targets, and enabled WebAuthn origin text. It republishes the static
// forwarding trust/header fields, fills an absent Live duration and compatible
// old response-ID TTL, and can retain those mutations when a later check fails.
// A runtime policy published earlier can therefore be replaced by the startup
// policy during another Validate call.
//
// Enabled feature flags select additional requirements, while several inactive
// controls still reject negative values. Coupled checks retain cache freshness
// inside total lifetime, deduplication retention through raw-record retention,
// pool minima below capacity, finite scheduler scoring sums, and separated
// scaling thresholds. This is the startup rule set; downstream runtime settings
// and subsystem defaults remain their owners' responsibility.
func (c *Config) Validate() error {
	if err := c.Plugins.validate(); err != nil {
		return err
	}
	if c.RequestLog.RetentionDays < 0 {
		return fmt.Errorf("request_log.retention_days must be non-negative")
	}
	c.normalizePricingCatalogSource()

	forwardedClientIPHeaders, err := NormalizeForwardedClientIPHeaders(c.Security.ForwardedClientIPHeaders)
	if err != nil {
		return fmt.Errorf("security.forwarded_client_ip_headers: %w", err)
	}
	// Successful header normalization reaches publication before proxy-probe
	// checking. Proxy-probe or later validation failures leave the startup
	// trust/header policy installed in the live cell.
	c.Security.ForwardedClientIPHeaders = forwardedClientIPHeaders
	c.SetForwardedClientIPSettings(c.Security.TrustForwardedIPForAPIKeyACL, forwardedClientIPHeaders)
	proxyProbeURLs, err := normalizeProxyProbeURLs(c.Security.ProxyProbe.URLs)
	if err != nil {
		return fmt.Errorf("security.proxy_probe.urls: %w", err)
	}
	c.Security.ProxyProbe.URLs = proxyProbeURLs
	// This budget covers ingress header reading before body handling or upstream
	// stream timing begins.
	if c.Server.ReadHeaderTimeout < 1 || c.Server.ReadHeaderTimeout > 60 {
		return fmt.Errorf("server.read_header_timeout must be between 1 and 60 seconds")
	}
	// Server construction also uses this ingress limit for the HTTP/2 header-list
	// size.
	if c.Server.MaxHeaderBytes < 8*1024 || c.Server.MaxHeaderBytes > 1024*1024 {
		return fmt.Errorf("server.max_header_bytes must be between 8192 and 1048576 bytes")
	}
	if c.Server.IdleTimeout <= 0 {
		return fmt.Errorf("server.idle_timeout must be positive")
	}
	if c.Server.MaxRequestBodySize < 0 {
		return fmt.Errorf("server.max_request_body_size must be non-negative")
	}
	// HTTP/2 frame payload lengths use a 24-bit value, and connection receive
	// windows start at 65535 bytes. Check enabled cleartext settings against those
	// protocol limits before server construction.
	if c.Server.H2C.Enabled {
		if c.Server.H2C.MaxConcurrentStreams == 0 {
			return fmt.Errorf("server.h2c.max_concurrent_streams must be positive")
		}
		if c.Server.H2C.IdleTimeout <= 0 {
			return fmt.Errorf("server.h2c.idle_timeout must be positive")
		}
		if c.Server.H2C.MaxReadFrameSize < 16*1024 || c.Server.H2C.MaxReadFrameSize > 16*1024*1024-1 {
			return fmt.Errorf("server.h2c.max_read_frame_size must be between 16384 and 16777215 bytes")
		}
		if c.Server.H2C.MaxUploadBufferPerConnection < 65535 {
			return fmt.Errorf("server.h2c.max_upload_buffer_per_connection must be at least 65535 bytes")
		}
		if c.Server.H2C.MaxUploadBufferPerStream <= 0 {
			return fmt.Errorf("server.h2c.max_upload_buffer_per_stream must be positive")
		}
	}
	// Apply the configured abuse observation limits when tracking is enabled. Its
	// threshold, observation window, block interval, and capacity determine the
	// process-local invalid-auth counter behavior.
	if c.APIKeyAuth.InvalidAbuse.Enabled {
		// The counter threshold governs repeated invalid credentials from one
		// observed client address.
		if c.APIKeyAuth.InvalidAbuse.Threshold < 10 {
			return fmt.Errorf("api_key_auth_cache.invalid_abuse.threshold must be at least 10")
		}
		// Bound the seconds during which invalid-auth observations contribute to that
		// client counter.
		if c.APIKeyAuth.InvalidAbuse.WindowSeconds < 1 || c.APIKeyAuth.InvalidAbuse.WindowSeconds > 3600 {
			return fmt.Errorf("api_key_auth_cache.invalid_abuse.window_seconds must be between 1 and 3600")
		}
		// The blocking interval starts after the observation threshold has been
		// reached.
		if c.APIKeyAuth.InvalidAbuse.BlockSeconds < 1 || c.APIKeyAuth.InvalidAbuse.BlockSeconds > 3600 {
			return fmt.Errorf("api_key_auth_cache.invalid_abuse.block_seconds must be between 1 and 3600")
		}
		// This entry capacity bounds client-address tracking in the process-local
		// abuse cache.
		if c.APIKeyAuth.InvalidAbuse.Capacity < 256 || c.APIKeyAuth.InvalidAbuse.Capacity > 1_000_000 {
			return fmt.Errorf("api_key_auth_cache.invalid_abuse.capacity must be between 256 and 1000000")
		}
	}
	jwtSecret := strings.TrimSpace(c.JWT.Secret)
	if jwtSecret == "" {
		return fmt.Errorf("jwt.secret is required")
	}
	// The signing input minimum is measured as encoded bytes rather than rune
	// count. Check the trimmed secret before identity construction so multibyte
	// text contributes its UTF-8 byte length.
	if len([]byte(jwtSecret)) < 32 {
		return fmt.Errorf("jwt.secret must be at least 32 bytes")
	}
	if err := c.validateLog(); err != nil {
		return err
	}

	// Gemini client selection uses a credential pair. Reject a partial trimmed
	// pair before consumers select configured credentials or their default client
	// choice, keeping the two source members together.
	geminiClientID := strings.TrimSpace(c.Gemini.OAuth.ClientID)
	geminiClientSecret := strings.TrimSpace(c.Gemini.OAuth.ClientSecret)
	if (geminiClientID == "") != (geminiClientSecret == "") {
		return fmt.Errorf("gemini.oauth.client_id and gemini.oauth.client_secret must be both set or both empty")
	}

	if strings.TrimSpace(c.Server.FrontendURL) != "" {
		if err := ValidateAbsoluteHTTPURL(c.Server.FrontendURL); err != nil {
			return fmt.Errorf("server.frontend_url invalid: %w", err)
		}
		u, err := url.Parse(strings.TrimSpace(c.Server.FrontendURL))
		if err != nil {
			return fmt.Errorf("server.frontend_url invalid: %w", err)
		}
		if u.RawQuery != "" || u.ForceQuery {
			return fmt.Errorf("server.frontend_url invalid: must not include query")
		}
		if u.User != nil {
			return fmt.Errorf("server.frontend_url invalid: must not include userinfo")
		}
		warnIfInsecureURL("server.frontend_url", c.Server.FrontendURL)
	}
	if err := c.validateWebAuthn(); err != nil {
		return err
	}
	if c.JWT.ExpireHour <= 0 {
		return fmt.Errorf("jwt.expire_hour must be positive")
	}
	// Validate the legacy hour value even when a positive minute override will
	// select access-token expiry.
	if c.JWT.ExpireHour > 168 {
		return fmt.Errorf("jwt.expire_hour must be <= 168 (7 days)")
	}
	// This shorter warning threshold retains the accepted configured lifetime
	// unchanged.
	if c.JWT.ExpireHour > 24 {
		slog.Warn("jwt.expire_hour is high; consider shorter expiration for security", "expire_hour", c.JWT.ExpireHour)
	}
	if c.JWT.AccessTokenExpireMinutes < 0 {
		return fmt.Errorf("jwt.access_token_expire_minutes must be non-negative")
	}
	// A positive minute override selects expiry; long overrides produce a warning
	// and remain accepted.
	if c.JWT.AccessTokenExpireMinutes > 720 {
		slog.Warn("jwt.access_token_expire_minutes is high; consider shorter expiration for security", "access_token_expire_minutes", c.JWT.AccessTokenExpireMinutes)
	}
	if c.JWT.RefreshTokenExpireDays <= 0 {
		return fmt.Errorf("jwt.refresh_token_expire_days must be positive")
	}
	// Long refresh-token lifetimes remain accepted after this warning is emitted.
	if c.JWT.RefreshTokenExpireDays > 90 {
		slog.Warn("jwt.refresh_token_expire_days is high; consider shorter expiration for security", "refresh_token_expire_days", c.JWT.RefreshTokenExpireDays)
	}
	if c.JWT.RefreshWindowMinutes < 0 {
		return fmt.Errorf("jwt.refresh_window_minutes must be non-negative")
	}
	if c.Security.CSP.Enabled && strings.TrimSpace(c.Security.CSP.Policy) == "" {
		return fmt.Errorf("security.csp.policy is required when CSP is enabled")
	}
	if c.LinuxDo.Enabled {
		if strings.TrimSpace(c.LinuxDo.ClientID) == "" {
			return fmt.Errorf("linuxdo_connect.client_id is required when linuxdo_connect.enabled=true")
		}
		if strings.TrimSpace(c.LinuxDo.AuthorizeURL) == "" {
			return fmt.Errorf("linuxdo_connect.authorize_url is required when linuxdo_connect.enabled=true")
		}
		if strings.TrimSpace(c.LinuxDo.TokenURL) == "" {
			return fmt.Errorf("linuxdo_connect.token_url is required when linuxdo_connect.enabled=true")
		}
		if strings.TrimSpace(c.LinuxDo.UserInfoURL) == "" {
			return fmt.Errorf("linuxdo_connect.userinfo_url is required when linuxdo_connect.enabled=true")
		}
		if strings.TrimSpace(c.LinuxDo.RedirectURL) == "" {
			return fmt.Errorf("linuxdo_connect.redirect_url is required when linuxdo_connect.enabled=true")
		}
		method := strings.ToLower(strings.TrimSpace(c.LinuxDo.TokenAuthMethod))
		switch method {
		case "", "client_secret_post", "client_secret_basic", "none":
		default:
			return fmt.Errorf("linuxdo_connect.token_auth_method must be one of: client_secret_post/client_secret_basic/none")
		}
		if (method == "" || method == "client_secret_post" || method == "client_secret_basic") &&
			strings.TrimSpace(c.LinuxDo.ClientSecret) == "" {
			return fmt.Errorf("linuxdo_connect.client_secret is required when linuxdo_connect.enabled=true and token_auth_method is client_secret_post/client_secret_basic")
		}
		if strings.TrimSpace(c.LinuxDo.FrontendRedirectURL) == "" {
			return fmt.Errorf("linuxdo_connect.frontend_redirect_url is required when linuxdo_connect.enabled=true")
		}

		if err := ValidateAbsoluteHTTPURL(c.LinuxDo.AuthorizeURL); err != nil {
			return fmt.Errorf("linuxdo_connect.authorize_url invalid: %w", err)
		}
		if err := ValidateAbsoluteHTTPURL(c.LinuxDo.TokenURL); err != nil {
			return fmt.Errorf("linuxdo_connect.token_url invalid: %w", err)
		}
		if err := ValidateAbsoluteHTTPURL(c.LinuxDo.UserInfoURL); err != nil {
			return fmt.Errorf("linuxdo_connect.userinfo_url invalid: %w", err)
		}
		if err := ValidateAbsoluteHTTPURL(c.LinuxDo.RedirectURL); err != nil {
			return fmt.Errorf("linuxdo_connect.redirect_url invalid: %w", err)
		}
		if err := ValidateFrontendRedirectURL(c.LinuxDo.FrontendRedirectURL); err != nil {
			return fmt.Errorf("linuxdo_connect.frontend_redirect_url invalid: %w", err)
		}

		warnIfInsecureURL("linuxdo_connect.authorize_url", c.LinuxDo.AuthorizeURL)
		warnIfInsecureURL("linuxdo_connect.token_url", c.LinuxDo.TokenURL)
		warnIfInsecureURL("linuxdo_connect.userinfo_url", c.LinuxDo.UserInfoURL)
		warnIfInsecureURL("linuxdo_connect.redirect_url", c.LinuxDo.RedirectURL)
		warnIfInsecureURL("linuxdo_connect.frontend_redirect_url", c.LinuxDo.FrontendRedirectURL)
	}
	if c.WeChat.Enabled {
		weChat := c.WeChat
		normalizeWeChatConnectConfig(&weChat)

		if weChat.OpenEnabled {
			if strings.TrimSpace(weChat.OpenAppID) == "" {
				return fmt.Errorf("wechat_connect.open_app_id is required when wechat_connect.open_enabled=true")
			}
			if strings.TrimSpace(weChat.OpenAppSecret) == "" {
				return fmt.Errorf("wechat_connect.open_app_secret is required when wechat_connect.open_enabled=true")
			}
		}
		if weChat.MPEnabled {
			if strings.TrimSpace(weChat.MPAppID) == "" {
				return fmt.Errorf("wechat_connect.mp_app_id is required when wechat_connect.mp_enabled=true")
			}
			if strings.TrimSpace(weChat.MPAppSecret) == "" {
				return fmt.Errorf("wechat_connect.mp_app_secret is required when wechat_connect.mp_enabled=true")
			}
		}
		if weChat.MobileEnabled {
			if strings.TrimSpace(weChat.MobileAppID) == "" {
				return fmt.Errorf("wechat_connect.mobile_app_id is required when wechat_connect.mobile_enabled=true")
			}
			if strings.TrimSpace(weChat.MobileAppSecret) == "" {
				return fmt.Errorf("wechat_connect.mobile_app_secret is required when wechat_connect.mobile_enabled=true")
			}
		}
		if v := strings.TrimSpace(weChat.RedirectURL); v != "" {
			if err := ValidateAbsoluteHTTPURL(v); err != nil {
				return fmt.Errorf("wechat_connect.redirect_url invalid: %w", err)
			}
			warnIfInsecureURL("wechat_connect.redirect_url", v)
		}
		if err := ValidateFrontendRedirectURL(weChat.FrontendRedirectURL); err != nil {
			return fmt.Errorf("wechat_connect.frontend_redirect_url invalid: %w", err)
		}
		warnIfInsecureURL("wechat_connect.frontend_redirect_url", weChat.FrontendRedirectURL)
	}
	if err := c.validateOIDC(); err != nil {
		return err
	}
	if c.Billing.CircuitBreaker.Enabled {
		if c.Billing.CircuitBreaker.FailureThreshold <= 0 {
			return fmt.Errorf("billing.circuit_breaker.failure_threshold must be positive")
		}
		if c.Billing.CircuitBreaker.ResetTimeoutSeconds <= 0 {
			return fmt.Errorf("billing.circuit_breaker.reset_timeout_seconds must be positive")
		}
		if c.Billing.CircuitBreaker.HalfOpenRequests <= 0 {
			return fmt.Errorf("billing.circuit_breaker.half_open_requests must be positive")
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
	if strings.TrimSpace(c.BatchImage.QueueReadyKey) == "" {
		return fmt.Errorf("batch_image.queue_ready_key must not be empty")
	}
	if strings.TrimSpace(c.BatchImage.QueueDelayedKey) == "" {
		return fmt.Errorf("batch_image.queue_delayed_key must not be empty")
	}
	if strings.TrimSpace(c.BatchImage.QueueActiveKey) == "" {
		return fmt.Errorf("batch_image.queue_active_key must not be empty")
	}
	if strings.TrimSpace(c.BatchImage.InflightKeyPrefix) == "" {
		return fmt.Errorf("batch_image.inflight_key_prefix must not be empty")
	}
	if strings.TrimSpace(c.BatchImage.LockKeyPrefix) == "" {
		return fmt.Errorf("batch_image.lock_key_prefix must not be empty")
	}
	if c.BatchImage.InflightTTLSeconds <= 0 {
		return fmt.Errorf("batch_image.inflight_ttl_seconds must be positive")
	}
	if c.BatchImage.JobLockTTLSeconds <= 0 {
		return fmt.Errorf("batch_image.job_lock_ttl_seconds must be positive")
	}
	if c.BatchImage.StaleActiveAfterSeconds <= 0 {
		return fmt.Errorf("batch_image.stale_active_after_seconds must be positive")
	}
	if c.BatchImage.DelayedMoveLimit <= 0 {
		return fmt.Errorf("batch_image.delayed_move_limit must be positive")
	}
	if c.BatchImage.RecoverLimit <= 0 {
		return fmt.Errorf("batch_image.recover_limit must be positive")
	}

	// Managed Vertex objects require a bucket name and a batch-specific prefix.
	// Validate the per-batch identity placeholder before these settings are passed
	// to the managed batch adapter.
	if strings.TrimSpace(c.BatchImage.VertexManagedGCSBucket) != "" {
		if strings.Contains(c.BatchImage.VertexManagedGCSBucket, "://") {
			return fmt.Errorf("batch_image.vertex_managed_gcs_bucket must be a bucket name, not a URI")
		}
		if strings.TrimSpace(c.BatchImage.VertexLocation) == "" {
			return fmt.Errorf("batch_image.vertex_location must not be empty when a Vertex bucket is configured")
		}
		if strings.TrimSpace(c.BatchImage.VertexManagedGCSPrefix) == "" {
			return fmt.Errorf("batch_image.vertex_managed_gcs_prefix must not be empty when a Vertex bucket is configured")
		}
		if !strings.Contains(c.BatchImage.VertexManagedGCSPrefix, "{batch_id}") {
			return fmt.Errorf("batch_image.vertex_managed_gcs_prefix must contain {batch_id}")
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
	if c.Creative.QueueEnabled {
		if strings.TrimSpace(c.Creative.QueueReadyKey) == "" {
			return fmt.Errorf("creative.queue_ready_key must not be empty")
		}
		if strings.TrimSpace(c.Creative.QueueDelayedKey) == "" {
			return fmt.Errorf("creative.queue_delayed_key must not be empty")
		}
		if strings.TrimSpace(c.Creative.QueueActiveKey) == "" {
			return fmt.Errorf("creative.queue_active_key must not be empty")
		}
		if strings.TrimSpace(c.Creative.InflightKeyPrefix) == "" {
			return fmt.Errorf("creative.inflight_key_prefix must not be empty")
		}
		if strings.TrimSpace(c.Creative.LockKeyPrefix) == "" {
			return fmt.Errorf("creative.lock_key_prefix must not be empty")
		}
		if c.Creative.InflightTTLSeconds <= 0 {
			return fmt.Errorf("creative.inflight_ttl_seconds must be positive")
		}
		if c.Creative.JobLockTTLSeconds <= 0 {
			return fmt.Errorf("creative.job_lock_ttl_seconds must be positive")
		}
		if c.Creative.StaleActiveAfterSeconds <= 0 {
			return fmt.Errorf("creative.stale_active_after_seconds must be positive")
		}
		if c.Creative.DelayedMoveLimit <= 0 {
			return fmt.Errorf("creative.delayed_move_limit must be positive")
		}
		if c.Creative.RecoverLimit <= 0 {
			return fmt.Errorf("creative.recover_limit must be positive")
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
	if c.Dashboard.Enabled {
		if c.Dashboard.StatsFreshTTLSeconds <= 0 {
			return fmt.Errorf("dashboard_cache.stats_fresh_ttl_seconds must be positive")
		}
		if c.Dashboard.StatsTTLSeconds <= 0 {
			return fmt.Errorf("dashboard_cache.stats_ttl_seconds must be positive")
		}
		if c.Dashboard.StatsRefreshTimeoutSeconds <= 0 {
			return fmt.Errorf("dashboard_cache.stats_refresh_timeout_seconds must be positive")
		}
		if c.Dashboard.StatsFreshTTLSeconds > c.Dashboard.StatsTTLSeconds {
			return fmt.Errorf("dashboard_cache.stats_fresh_ttl_seconds must be <= dashboard_cache.stats_ttl_seconds")
		}
	} else {
		if c.Dashboard.StatsFreshTTLSeconds < 0 {
			return fmt.Errorf("dashboard_cache.stats_fresh_ttl_seconds must be non-negative")
		}
		if c.Dashboard.StatsTTLSeconds < 0 {
			return fmt.Errorf("dashboard_cache.stats_ttl_seconds must be non-negative")
		}
		if c.Dashboard.StatsRefreshTimeoutSeconds < 0 {
			return fmt.Errorf("dashboard_cache.stats_refresh_timeout_seconds must be non-negative")
		}
	}
	// Enabled aggregation needs positive retained datasets, and billing
	// deduplication must survive at least as long as raw usage records. Inactive
	// settings retain nonnegative and paired-retention checks below.
	if c.DashboardAgg.Enabled {
		if c.DashboardAgg.IntervalSeconds <= 0 {
			return fmt.Errorf("dashboard_aggregation.interval_seconds must be positive")
		}
		if c.DashboardAgg.LookbackSeconds < 0 {
			return fmt.Errorf("dashboard_aggregation.lookback_seconds must be non-negative")
		}
		if c.DashboardAgg.BackfillMaxDays < 0 {
			return fmt.Errorf("dashboard_aggregation.backfill_max_days must be non-negative")
		}
		if c.DashboardAgg.BackfillEnabled && c.DashboardAgg.BackfillMaxDays == 0 {
			return fmt.Errorf("dashboard_aggregation.backfill_max_days must be positive")
		}
		if c.DashboardAgg.Retention.UsageLogsDays <= 0 {
			return fmt.Errorf("dashboard_aggregation.retention.usage_logs_days must be positive")
		}
		if c.DashboardAgg.Retention.UsageBillingDedupDays <= 0 {
			return fmt.Errorf("dashboard_aggregation.retention.usage_billing_dedup_days must be positive")
		}
		if c.DashboardAgg.Retention.UsageBillingDedupDays < c.DashboardAgg.Retention.UsageLogsDays {
			return fmt.Errorf("dashboard_aggregation.retention.usage_billing_dedup_days must be greater than or equal to usage_logs_days")
		}
		if c.DashboardAgg.Retention.HourlyDays <= 0 {
			return fmt.Errorf("dashboard_aggregation.retention.hourly_days must be positive")
		}
		if c.DashboardAgg.Retention.DailyDays <= 0 {
			return fmt.Errorf("dashboard_aggregation.retention.daily_days must be positive")
		}
		if c.DashboardAgg.RecomputeDays < 0 {
			return fmt.Errorf("dashboard_aggregation.recompute_days must be non-negative")
		}
	} else {
		if c.DashboardAgg.IntervalSeconds < 0 {
			return fmt.Errorf("dashboard_aggregation.interval_seconds must be non-negative")
		}
		if c.DashboardAgg.LookbackSeconds < 0 {
			return fmt.Errorf("dashboard_aggregation.lookback_seconds must be non-negative")
		}
		if c.DashboardAgg.BackfillMaxDays < 0 {
			return fmt.Errorf("dashboard_aggregation.backfill_max_days must be non-negative")
		}
		if c.DashboardAgg.Retention.UsageLogsDays < 0 {
			return fmt.Errorf("dashboard_aggregation.retention.usage_logs_days must be non-negative")
		}
		if c.DashboardAgg.Retention.UsageBillingDedupDays < 0 {
			return fmt.Errorf("dashboard_aggregation.retention.usage_billing_dedup_days must be non-negative")
		}
		if c.DashboardAgg.Retention.UsageBillingDedupDays > 0 &&
			c.DashboardAgg.Retention.UsageLogsDays > 0 &&
			c.DashboardAgg.Retention.UsageBillingDedupDays < c.DashboardAgg.Retention.UsageLogsDays {
			return fmt.Errorf("dashboard_aggregation.retention.usage_billing_dedup_days must be greater than or equal to usage_logs_days")
		}
		if c.DashboardAgg.Retention.HourlyDays < 0 {
			return fmt.Errorf("dashboard_aggregation.retention.hourly_days must be non-negative")
		}
		if c.DashboardAgg.Retention.DailyDays < 0 {
			return fmt.Errorf("dashboard_aggregation.retention.daily_days must be non-negative")
		}
		if c.DashboardAgg.RecomputeDays < 0 {
			return fmt.Errorf("dashboard_aggregation.recompute_days must be non-negative")
		}
	}
	if c.UsageCleanup.Enabled {
		if c.UsageCleanup.MaxRangeDays <= 0 {
			return fmt.Errorf("usage_cleanup.max_range_days must be positive")
		}
		if c.UsageCleanup.BatchSize <= 0 {
			return fmt.Errorf("usage_cleanup.batch_size must be positive")
		}
		if c.UsageCleanup.WorkerIntervalSeconds <= 0 {
			return fmt.Errorf("usage_cleanup.worker_interval_seconds must be positive")
		}
		if c.UsageCleanup.TaskTimeoutSeconds <= 0 {
			return fmt.Errorf("usage_cleanup.task_timeout_seconds must be positive")
		}
	} else {
		if c.UsageCleanup.MaxRangeDays < 0 {
			return fmt.Errorf("usage_cleanup.max_range_days must be non-negative")
		}
		if c.UsageCleanup.BatchSize < 0 {
			return fmt.Errorf("usage_cleanup.batch_size must be non-negative")
		}
		if c.UsageCleanup.WorkerIntervalSeconds < 0 {
			return fmt.Errorf("usage_cleanup.worker_interval_seconds must be non-negative")
		}
		if c.UsageCleanup.TaskTimeoutSeconds < 0 {
			return fmt.Errorf("usage_cleanup.task_timeout_seconds must be non-negative")
		}
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
	if c.Idempotency.MaxStoredResponseLen <= 0 {
		return fmt.Errorf("idempotency.max_stored_response_len must be positive")
	}
	if c.Idempotency.CleanupIntervalSeconds <= 0 {
		return fmt.Errorf("idempotency.cleanup_interval_seconds must be positive")
	}
	if c.Idempotency.CleanupBatchSize <= 0 {
		return fmt.Errorf("idempotency.cleanup_batch_size must be positive")
	}
	if err := c.validateGateway(); err != nil {
		return err
	}
	if c.Ops.MetricsCollectorCache.TTL < 0 {
		return fmt.Errorf("ops.metrics_collector_cache.ttl must be non-negative")
	}
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
	// Concurrency waiting sends SSE keepalives at this cadence before a request
	// obtains its slot.
	if c.Concurrency.PingInterval < 5 || c.Concurrency.PingInterval > 30 {
		return fmt.Errorf("concurrency.ping_interval must be between 5-30 seconds")
	}
	if c.Gateway.Grok.FreeQuotaSoftGateEnabled {
		if c.Gateway.Grok.FreeQuotaTokenLimit <= 0 {
			return fmt.Errorf("gateway.grok.free_quota_token_limit must be positive")
		}
		// The soft gate compares rolling free-tier token usage with this fraction of
		// the configured token limit.
		if c.Gateway.Grok.FreeQuotaSoftGatePercent < 1 || c.Gateway.Grok.FreeQuotaSoftGatePercent > 100 {
			return fmt.Errorf("gateway.grok.free_quota_soft_gate_percent must be between 1 and 100")
		}
		if c.Gateway.Grok.FreeQuotaWindowHours <= 0 {
			return fmt.Errorf("gateway.grok.free_quota_window_hours must be positive")
		}
	}
	if c.Gateway.Grok.FreeQuotaStatsCacheSeconds < 0 {
		return fmt.Errorf("gateway.grok.free_quota_stats_cache_seconds must be non-negative")
	}
	if err := ValidateDingTalkConfig(c.DingTalk); err != nil {
		return fmt.Errorf("dingtalk_connect: %w", err)
	}
	return nil
}

// validateGateway checks gateway transport, scheduler, and completion limits,
// returning the first invalid setting. Validate calls it after idempotency and
// before Ops; the later Grok quota checks remain after concurrency validation.
// Callers serialize access because Live duration defaulting and retired
// response-affinity TTL fallback mutate the receiver as their checks are reached.
//
// A later error retains those mutations and any earlier idle-lifetime warning.
// Coupled pool bounds, finite scoring sums, and enabled autoscaling constraints
// are checked together so their consumers receive consistent limits. Disabled
// autoscaling skips only its scaling controls; ordinary completion limits still
// apply.
func (c *Config) validateGateway() error {
	if c.Gateway.MaxBodySize <= 0 {
		return fmt.Errorf("gateway.max_body_size must be positive")
	}
	if c.Gateway.TextMaxBodySize <= 0 || c.Gateway.TextMaxBodySize > c.Gateway.MaxBodySize {
		return fmt.Errorf("gateway.text_max_body_size must be positive and no greater than gateway.max_body_size")
	}
	if c.Gateway.UpstreamResponseReadMaxBytes <= 0 {
		return fmt.Errorf("gateway.upstream_response_read_max_bytes must be positive")
	}
	if c.Gateway.ModelsListReadMaxBytes <= 0 {
		return fmt.Errorf("gateway.models_list_read_max_bytes must be positive")
	}
	if c.Gateway.ProxyProbeResponseReadMaxBytes <= 0 {
		return fmt.Errorf("gateway.proxy_probe_response_read_max_bytes must be positive")
	}
	if c.Gateway.ResponseHeaderTimeout < 0 {
		return fmt.Errorf("gateway.response_header_timeout must be non-negative")
	}
	if c.Gateway.OpenAIResponseHeaderTimeout < 0 {
		return fmt.Errorf("gateway.openai_response_header_timeout must be non-negative")
	}
	// Grok header timing has its own cap before streaming data-gap handling takes
	// over.
	if c.Gateway.GrokResponseHeaderTimeout < 0 || c.Gateway.GrokResponseHeaderTimeout > 1800 {
		return fmt.Errorf("gateway.grok_response_header_timeout must be between 0-1800 seconds")
	}
	// The ordinary OpenAI first-output budget is validated independently of the
	// high-effort budget below; zero disables its configured first-output
	// deadline.
	if c.Gateway.OpenAIFirstOutputTimeoutSeconds < 0 || c.Gateway.OpenAIFirstOutputTimeoutSeconds > 600 ||
		(c.Gateway.OpenAIFirstOutputTimeoutSeconds > 0 && c.Gateway.OpenAIFirstOutputTimeoutSeconds < 30) {
		return fmt.Errorf("gateway.openai_first_output_timeout_seconds must be 0 or between 30-600 seconds")
	}
	// The high-effort override can exceed the ordinary budget. The consumer
	// considers it after a positive ordinary budget has enabled first-output
	// timing.
	if c.Gateway.OpenAIHighEffortFirstOutputTimeoutSeconds < 0 || c.Gateway.OpenAIHighEffortFirstOutputTimeoutSeconds > 1800 ||
		(c.Gateway.OpenAIHighEffortFirstOutputTimeoutSeconds > 0 && c.Gateway.OpenAIHighEffortFirstOutputTimeoutSeconds < 30) {
		return fmt.Errorf("gateway.openai_high_effort_first_output_timeout_seconds must be 0 or between 30-1800 seconds")
	}
	// A missing or nonpositive Live session duration has no usable cap. Supply the
	// one-hour process default before downstream Live session construction reads
	// this field.
	if c.Gateway.Live.MaxSessionDurationSeconds <= 0 {
		c.Gateway.Live.MaxSessionDurationSeconds = 3600
	}
	if strings.TrimSpace(c.Gateway.ConnectionPoolIsolation) != "" {
		switch c.Gateway.ConnectionPoolIsolation {
		case ConnectionPoolIsolationProxy, ConnectionPoolIsolationProvider, ConnectionPoolIsolationProviderProxy:
		default:
			return fmt.Errorf("gateway.connection_pool_isolation must be one of: %s/%s/%s",
				ConnectionPoolIsolationProxy, ConnectionPoolIsolationProvider, ConnectionPoolIsolationProviderProxy)
		}
	}
	if c.Gateway.ImageConcurrency.MaxConcurrentRequests < 0 {
		return fmt.Errorf("gateway.image_concurrency.max_concurrent_requests must be non-negative")
	}
	switch strings.TrimSpace(c.Gateway.ImageConcurrency.OverflowMode) {
	case "", ImageConcurrencyOverflowModeReject, ImageConcurrencyOverflowModeWait:
	default:
		return fmt.Errorf("gateway.image_concurrency.overflow_mode must be one of: %s/%s",
			ImageConcurrencyOverflowModeReject, ImageConcurrencyOverflowModeWait)
	}
	if c.Gateway.ImageConcurrency.WaitTimeoutSeconds < 0 {
		return fmt.Errorf("gateway.image_concurrency.wait_timeout_seconds must be non-negative")
	}
	if c.Gateway.ImageConcurrency.MaxWaitingRequests < 0 {
		return fmt.Errorf("gateway.image_concurrency.max_waiting_requests must be non-negative")
	}
	if c.Gateway.MaxIdleConns <= 0 {
		return fmt.Errorf("gateway.max_idle_conns must be positive")
	}
	if c.Gateway.MaxIdleConnsPerHost <= 0 {
		return fmt.Errorf("gateway.max_idle_conns_per_host must be positive")
	}
	if c.Gateway.MaxConnsPerHost < 0 {
		return fmt.Errorf("gateway.max_conns_per_host must be non-negative")
	}
	if c.Gateway.IdleConnTimeoutSeconds <= 0 {
		return fmt.Errorf("gateway.idle_conn_timeout_seconds must be positive")
	}
	// This transport-idle threshold warns while retaining the supplied connection
	// lifetime.
	if c.Gateway.IdleConnTimeoutSeconds > 180 {
		slog.Warn("gateway.idle_conn_timeout_seconds is high; consider 60-120 seconds for better connection reuse", "idle_conn_timeout_seconds", c.Gateway.IdleConnTimeoutSeconds)
	}
	if c.Gateway.MaxUpstreamClients <= 0 {
		return fmt.Errorf("gateway.max_upstream_clients must be positive")
	}
	if c.Gateway.ClientIdleTTLSeconds <= 0 {
		return fmt.Errorf("gateway.client_idle_ttl_seconds must be positive")
	}
	if c.Gateway.ConcurrencySlotTTLMinutes <= 0 {
		return fmt.Errorf("gateway.concurrency_slot_ttl_minutes must be positive")
	}
	if c.Gateway.StreamDataIntervalTimeout < 0 {
		return fmt.Errorf("gateway.stream_data_interval_timeout must be non-negative")
	}
	// Bound ordinary stream inactivity independently of the keepalive interval;
	// zero disables this deadline.
	if c.Gateway.StreamDataIntervalTimeout != 0 &&
		(c.Gateway.StreamDataIntervalTimeout < 30 || c.Gateway.StreamDataIntervalTimeout > 300) {
		return fmt.Errorf("gateway.stream_data_interval_timeout must be 0 or between 30-300 seconds")
	}
	if c.Gateway.StreamKeepaliveInterval < 0 {
		return fmt.Errorf("gateway.stream_keepalive_interval must be non-negative")
	}
	// These downstream keepalives have their own interval, independent of upstream
	// data arrival.
	if c.Gateway.StreamKeepaliveInterval != 0 &&
		(c.Gateway.StreamKeepaliveInterval < 5 || c.Gateway.StreamKeepaliveInterval > 30) {
		return fmt.Errorf("gateway.stream_keepalive_interval must be 0 or between 5-30 seconds")
	}
	if c.Gateway.ImageStreamDataIntervalTimeout < 0 {
		return fmt.Errorf("gateway.image_stream_data_interval_timeout must be non-negative")
	}
	// Image data gaps allow a wider budget than ordinary text streams.
	if c.Gateway.ImageStreamDataIntervalTimeout != 0 &&
		(c.Gateway.ImageStreamDataIntervalTimeout < 60 || c.Gateway.ImageStreamDataIntervalTimeout > 1800) {
		return fmt.Errorf("gateway.image_stream_data_interval_timeout must be 0 or between 60-1800 seconds")
	}
	if c.Gateway.ImageStreamKeepaliveInterval < 0 {
		return fmt.Errorf("gateway.image_stream_keepalive_interval must be non-negative")
	}
	// Image streaming keepalives use the image-specific interval while awaiting
	// generation output.
	if c.Gateway.ImageStreamKeepaliveInterval != 0 &&
		(c.Gateway.ImageStreamKeepaliveInterval < 5 || c.Gateway.ImageStreamKeepaliveInterval > 60) {
		return fmt.Errorf("gateway.image_stream_keepalive_interval must be 0 or between 5-60 seconds")
	}
	if c.Gateway.ImageNonstreamKeepaliveInterval < 0 {
		return fmt.Errorf("gateway.image_nonstream_keepalive_interval must be non-negative")
	}
	// Nonstream image JSON responses have their own keepalive cadence, separate
	// from image SSE.
	if c.Gateway.ImageNonstreamKeepaliveInterval != 0 &&
		(c.Gateway.ImageNonstreamKeepaliveInterval < 5 || c.Gateway.ImageNonstreamKeepaliveInterval > 60) {
		return fmt.Errorf("gateway.image_nonstream_keepalive_interval must be 0 or between 5-60 seconds")
	}
	// Resolve a positive retired response affinity lifetime when the current
	// lifetime is nonpositive. Validate the effective value afterward so
	// compatibility still respects the active WS lifetime requirement.
	if c.Gateway.OpenAIWS.StickyResponseIDTTLSeconds <= 0 && c.Gateway.OpenAIWS.StickyPreviousResponseTTLSeconds > 0 {
		c.Gateway.OpenAIWS.StickyResponseIDTTLSeconds = c.Gateway.OpenAIWS.StickyPreviousResponseTTLSeconds
	}
	if c.Gateway.OpenAIWS.MaxConnsPerProvider <= 0 {
		return fmt.Errorf("gateway.openai_ws.max_conns_per_provider must be positive")
	}
	if c.Gateway.OpenAIWS.ClientFirstMessageTimeoutSeconds <= 0 {
		return fmt.Errorf("gateway.openai_ws.client_first_message_timeout_seconds must be positive")
	}
	if c.Gateway.OpenAIWS.IngressInterTurnIdleTimeoutSeconds < 0 {
		return fmt.Errorf("gateway.openai_ws.ingress_inter_turn_idle_timeout_seconds must be non-negative")
	}
	if c.Gateway.OpenAIWS.MaxIngressConnectionsPerAPIKey < 0 {
		return fmt.Errorf("gateway.openai_ws.max_ingress_connections_per_api_key must be non-negative")
	}
	if c.Gateway.OpenAIWS.MinIdlePerProvider < 0 {
		return fmt.Errorf("gateway.openai_ws.min_idle_per_provider must be non-negative")
	}
	if c.Gateway.OpenAIWS.MaxIdlePerProvider < 0 {
		return fmt.Errorf("gateway.openai_ws.max_idle_per_provider must be non-negative")
	}
	// Provider pool minima and retained-idle maxima share the total connection
	// capacity. Reject inverted or oversized idle limits before constructing pools
	// that maintain and reclaim those connections.
	if c.Gateway.OpenAIWS.MinIdlePerProvider > c.Gateway.OpenAIWS.MaxIdlePerProvider {
		return fmt.Errorf("gateway.openai_ws.min_idle_per_provider must be <= max_idle_per_provider")
	}
	if c.Gateway.OpenAIWS.MaxIdlePerProvider > c.Gateway.OpenAIWS.MaxConnsPerProvider {
		return fmt.Errorf("gateway.openai_ws.max_idle_per_provider must be <= max_conns_per_provider")
	}
	if c.Gateway.OpenAIWS.OAuthMaxConnsFactor <= 0 {
		return fmt.Errorf("gateway.openai_ws.oauth_max_conns_factor must be positive")
	}
	if c.Gateway.OpenAIWS.APIKeyMaxConnsFactor <= 0 {
		return fmt.Errorf("gateway.openai_ws.apikey_max_conns_factor must be positive")
	}
	if c.Gateway.OpenAIWS.DialTimeoutSeconds <= 0 {
		return fmt.Errorf("gateway.openai_ws.dial_timeout_seconds must be positive")
	}
	if c.Gateway.OpenAIWS.ReadTimeoutSeconds <= 0 {
		return fmt.Errorf("gateway.openai_ws.read_timeout_seconds must be positive")
	}
	if c.Gateway.OpenAIWS.WriteTimeoutSeconds <= 0 {
		return fmt.Errorf("gateway.openai_ws.write_timeout_seconds must be positive")
	}
	// Pool sizing consumes this value as a target fraction of connection
	// utilization.
	if c.Gateway.OpenAIWS.PoolTargetUtilization <= 0 || c.Gateway.OpenAIWS.PoolTargetUtilization > 1 {
		return fmt.Errorf("gateway.openai_ws.pool_target_utilization must be within (0,1]")
	}
	if c.Gateway.OpenAIWS.QueueLimitPerConn <= 0 {
		return fmt.Errorf("gateway.openai_ws.queue_limit_per_conn must be positive")
	}
	if c.Gateway.OpenAIWS.PrewarmCooldownMS < 0 {
		return fmt.Errorf("gateway.openai_ws.prewarm_cooldown_ms must be non-negative")
	}
	if c.Gateway.OpenAIWS.ClientReadLimitBytes <= 0 {
		return fmt.Errorf("gateway.openai_ws.client_read_limit_bytes must be positive")
	}
	if c.Gateway.OpenAIWS.HTTPBridgeThresholdBytes <= 0 {
		return fmt.Errorf("gateway.openai_ws.http_bridge_threshold_bytes must be positive")
	}
	if c.Gateway.AdvancedScheduler.LBTopK <= 0 {
		return fmt.Errorf("gateway.advanced_scheduler.lb_top_k must be positive")
	}
	// Both EWMA coefficients weight the latest observation against retained
	// error-rate or TTFT history. Reject nonfinite coefficients before those
	// scheduler updates can consume them.
	if c.Gateway.AdvancedScheduler.EWMAErrorRateAlpha <= 0 || c.Gateway.AdvancedScheduler.EWMAErrorRateAlpha > 1 ||
		math.IsNaN(c.Gateway.AdvancedScheduler.EWMAErrorRateAlpha) || math.IsInf(c.Gateway.AdvancedScheduler.EWMAErrorRateAlpha, 0) {
		return fmt.Errorf("gateway.advanced_scheduler.ewma_error_rate_alpha must be between 0 and 1")
	}
	if c.Gateway.AdvancedScheduler.EWMATTFTAlpha <= 0 || c.Gateway.AdvancedScheduler.EWMATTFTAlpha > 1 ||
		math.IsNaN(c.Gateway.AdvancedScheduler.EWMATTFTAlpha) || math.IsInf(c.Gateway.AdvancedScheduler.EWMATTFTAlpha, 0) {
		return fmt.Errorf("gateway.advanced_scheduler.ewma_ttft_alpha must be between 0 and 1")
	}
	if c.Gateway.OpenAIWS.StickySessionTTLSeconds <= 0 {
		return fmt.Errorf("gateway.openai_ws.sticky_session_ttl_seconds must be positive")
	}
	if c.Gateway.OpenAIWS.StickyResponseIDTTLSeconds <= 0 {
		return fmt.Errorf("gateway.openai_ws.sticky_response_id_ttl_seconds must be positive")
	}
	if c.Gateway.OpenAIWS.StickyPreviousResponseTTLSeconds < 0 {
		return fmt.Errorf("gateway.openai_ws.sticky_previous_response_ttl_seconds must be non-negative")
	}
	if c.Gateway.OpenAIHTTP2.FallbackErrorThreshold < 0 {
		return fmt.Errorf("gateway.openai_http2.fallback_error_threshold must be non-negative")
	}
	if c.Gateway.OpenAIHTTP2.FallbackWindowSeconds < 0 {
		return fmt.Errorf("gateway.openai_http2.fallback_window_seconds must be non-negative")
	}
	if c.Gateway.OpenAIHTTP2.FallbackTTLSeconds < 0 {
		return fmt.Errorf("gateway.openai_http2.fallback_ttl_seconds must be non-negative")
	}
	if c.Gateway.OpenAIProxyStreamCircuit.FailureThreshold < 0 {
		return fmt.Errorf("gateway.openai_proxy_stream_circuit.failure_threshold must be non-negative")
	}
	if c.Gateway.OpenAIProxyStreamCircuit.WindowSeconds < 0 {
		return fmt.Errorf("gateway.openai_proxy_stream_circuit.window_seconds must be non-negative")
	}
	if c.Gateway.OpenAIProxyStreamCircuit.TTLSeconds < 0 {
		return fmt.Errorf("gateway.openai_proxy_stream_circuit.ttl_seconds must be non-negative")
	}
	// Scoring consumes each finite nonnegative weight and their combined totals.
	// Check individual values before summing so an invalid component cannot enter
	// candidate score calculations.
	weights := c.Gateway.AdvancedScheduler.ScoreWeights
	for _, weight := range []float64{
		weights.Priority, weights.Load, weights.Queue, weights.ErrorRate, weights.TTFT,
		weights.Reset, weights.QuotaHeadroom,
		weights.PreviousResponse, weights.SessionSticky,
	} {
		if weight < 0 || math.IsNaN(weight) || math.IsInf(weight, 0) {
			return fmt.Errorf("gateway.advanced_scheduler.score_weights.* must be non-negative and finite")
		}
	}
	// BaseWeightSum omits affinity bonus weights, so a positive bonus cannot make
	// an otherwise zero base score valid. Check sums for overflow as well as
	// component validity before scheduler construction.
	weightSum := weights.BaseWeightSum()
	if weightSum <= 0 {
		return fmt.Errorf("gateway.advanced_scheduler.score_weights must not all be zero")
	}
	if math.IsNaN(weightSum) || math.IsInf(weightSum, 0) {
		return fmt.Errorf("gateway.advanced_scheduler.score_weights base-weight sum must be finite")
	}
	if totalWeightSum := weights.TotalWeightSum(); math.IsNaN(totalWeightSum) || math.IsInf(totalWeightSum, 0) {
		return fmt.Errorf("gateway.advanced_scheduler.score_weights total-weight sum must be finite")
	}
	if c.Gateway.AdvancedScheduler.StickyEscapeTTFTMs <= 0 {
		return fmt.Errorf("gateway.advanced_scheduler.sticky_escape_ttft_ms must be positive")
	}
	// This fraction sets the error-rate condition for leaving sticky provider
	// affinity.
	if c.Gateway.AdvancedScheduler.StickyEscapeErrorRate < 0 || c.Gateway.AdvancedScheduler.StickyEscapeErrorRate > 1 {
		return fmt.Errorf("gateway.advanced_scheduler.sticky_escape_error_rate must be between 0 and 1")
	}
	if c.Gateway.MaxLineSize < 0 {
		return fmt.Errorf("gateway.max_line_size must be non-negative")
	}
	// The nonzero limit applies to one scanned SSE line; zero selects downstream
	// scanner defaults.
	if c.Gateway.MaxLineSize != 0 && c.Gateway.MaxLineSize < 1024*1024 {
		return fmt.Errorf("gateway.max_line_size must be at least 1MB")
	}
	if c.Gateway.UsageRecord.WorkerCount <= 0 {
		return fmt.Errorf("gateway.usage_record.worker_count must be positive")
	}
	if c.Gateway.UsageRecord.QueueSize <= 0 {
		return fmt.Errorf("gateway.usage_record.queue_size must be positive")
	}
	if c.Gateway.UsageRecord.TaskTimeoutSeconds <= 0 {
		return fmt.Errorf("gateway.usage_record.task_timeout_seconds must be positive")
	}
	switch strings.ToLower(strings.TrimSpace(c.Gateway.UsageRecord.OverflowPolicy)) {
	case UsageRecordOverflowPolicyDrop, UsageRecordOverflowPolicySample, UsageRecordOverflowPolicySync:
	default:
		return fmt.Errorf("gateway.usage_record.overflow_policy must be one of: %s/%s/%s",
			UsageRecordOverflowPolicyDrop, UsageRecordOverflowPolicySample, UsageRecordOverflowPolicySync)
	}
	// Validate the percentage for every overflow policy. Sample mode additionally
	// requires positive admission, while the other policies can retain a zero
	// percentage.
	if c.Gateway.UsageRecord.OverflowSamplePercent < 0 || c.Gateway.UsageRecord.OverflowSamplePercent > 100 {
		return fmt.Errorf("gateway.usage_record.overflow_sample_percent must be between 0-100")
	}
	if strings.EqualFold(strings.TrimSpace(c.Gateway.UsageRecord.OverflowPolicy), UsageRecordOverflowPolicySample) &&
		c.Gateway.UsageRecord.OverflowSamplePercent <= 0 {
		return fmt.Errorf("gateway.usage_record.overflow_sample_percent must be positive when overflow_policy=sample")
	}
	// Worker growth and shrinkage use separated queue thresholds inside a
	// configured worker range. Validate the initial count and both adjustment
	// steps with that range before the completion pool can scale.
	if c.Gateway.UsageRecord.AutoScaleEnabled {
		if c.Gateway.UsageRecord.AutoScaleMinWorkers <= 0 {
			return fmt.Errorf("gateway.usage_record.auto_scale_min_workers must be positive")
		}
		if c.Gateway.UsageRecord.AutoScaleMaxWorkers <= 0 {
			return fmt.Errorf("gateway.usage_record.auto_scale_max_workers must be positive")
		}
		if c.Gateway.UsageRecord.AutoScaleMaxWorkers < c.Gateway.UsageRecord.AutoScaleMinWorkers {
			return fmt.Errorf("gateway.usage_record.auto_scale_max_workers must be >= auto_scale_min_workers")
		}
		if c.Gateway.UsageRecord.WorkerCount < c.Gateway.UsageRecord.AutoScaleMinWorkers ||
			c.Gateway.UsageRecord.WorkerCount > c.Gateway.UsageRecord.AutoScaleMaxWorkers {
			return fmt.Errorf("gateway.usage_record.worker_count must be between auto_scale_min_workers and auto_scale_max_workers")
		}
		if c.Gateway.UsageRecord.AutoScaleUpQueuePercent <= 0 || c.Gateway.UsageRecord.AutoScaleUpQueuePercent > 100 {
			return fmt.Errorf("gateway.usage_record.auto_scale_up_queue_percent must be between 1-100")
		}
		if c.Gateway.UsageRecord.AutoScaleDownQueuePercent < 0 || c.Gateway.UsageRecord.AutoScaleDownQueuePercent >= 100 {
			return fmt.Errorf("gateway.usage_record.auto_scale_down_queue_percent must be between 0-99")
		}
		if c.Gateway.UsageRecord.AutoScaleDownQueuePercent >= c.Gateway.UsageRecord.AutoScaleUpQueuePercent {
			return fmt.Errorf("gateway.usage_record.auto_scale_down_queue_percent must be less than auto_scale_up_queue_percent")
		}
		if c.Gateway.UsageRecord.AutoScaleUpStep <= 0 {
			return fmt.Errorf("gateway.usage_record.auto_scale_up_step must be positive")
		}
		if c.Gateway.UsageRecord.AutoScaleDownStep <= 0 {
			return fmt.Errorf("gateway.usage_record.auto_scale_down_step must be positive")
		}
		if c.Gateway.UsageRecord.AutoScaleCheckIntervalSeconds <= 0 {
			return fmt.Errorf("gateway.usage_record.auto_scale_check_interval_seconds must be positive")
		}
		if c.Gateway.UsageRecord.AutoScaleCooldownSeconds < 0 {
			return fmt.Errorf("gateway.usage_record.auto_scale_cooldown_seconds must be non-negative")
		}
	}
	if c.Gateway.UserGroupRateCacheTTLSeconds <= 0 {
		return fmt.Errorf("gateway.user_group_rate_cache_ttl_seconds must be positive")
	}
	// The retired catalog-cache key remains range-validated even though current
	// model lookup does not read it.
	if c.Gateway.ModelsListCacheTTLSeconds < 10 || c.Gateway.ModelsListCacheTTLSeconds > 30 {
		return fmt.Errorf("gateway.models_list_cache_ttl_seconds must be between 10-30")
	}
	if c.Gateway.Scheduling.StickySessionMaxWaiting <= 0 {
		return fmt.Errorf("gateway.scheduling.sticky_session_max_waiting must be positive")
	}
	if c.Gateway.Scheduling.StickySessionWaitTimeout <= 0 {
		return fmt.Errorf("gateway.scheduling.sticky_session_wait_timeout must be positive")
	}
	if c.Gateway.Scheduling.FallbackWaitTimeout <= 0 {
		return fmt.Errorf("gateway.scheduling.fallback_wait_timeout must be positive")
	}
	if c.Gateway.Scheduling.FallbackMaxWaiting <= 0 {
		return fmt.Errorf("gateway.scheduling.fallback_max_waiting must be positive")
	}
	if c.Gateway.Scheduling.LoadBatchCacheTTLMS < 0 {
		return fmt.Errorf("gateway.scheduling.load_batch_cache_ttl_ms must be non-negative")
	}
	if c.Gateway.Scheduling.SnapshotMGetChunkSize <= 0 {
		return fmt.Errorf("gateway.scheduling.snapshot_mget_chunk_size must be positive")
	}
	if c.Gateway.Scheduling.SnapshotWriteChunkSize <= 0 {
		return fmt.Errorf("gateway.scheduling.snapshot_write_chunk_size must be positive")
	}
	if c.Gateway.Scheduling.SlotCleanupInterval < 0 {
		return fmt.Errorf("gateway.scheduling.slot_cleanup_interval must be non-negative")
	}
	if c.Gateway.Scheduling.DbFallbackTimeoutSeconds < 0 {
		return fmt.Errorf("gateway.scheduling.db_fallback_timeout_seconds must be non-negative")
	}
	if c.Gateway.Scheduling.DbFallbackMaxQPS < 0 {
		return fmt.Errorf("gateway.scheduling.db_fallback_max_qps must be non-negative")
	}
	if c.Gateway.Scheduling.OutboxPollIntervalSeconds <= 0 {
		return fmt.Errorf("gateway.scheduling.outbox_poll_interval_seconds must be positive")
	}
	if c.Gateway.Scheduling.OutboxLagWarnSeconds < 0 {
		return fmt.Errorf("gateway.scheduling.outbox_lag_warn_seconds must be non-negative")
	}
	if c.Gateway.Scheduling.OutboxLagRebuildSeconds < 0 {
		return fmt.Errorf("gateway.scheduling.outbox_lag_rebuild_seconds must be non-negative")
	}
	if c.Gateway.Scheduling.OutboxLagRebuildFailures <= 0 {
		return fmt.Errorf("gateway.scheduling.outbox_lag_rebuild_failures must be positive")
	}
	if c.Gateway.Scheduling.OutboxBacklogRebuildRows < 0 {
		return fmt.Errorf("gateway.scheduling.outbox_backlog_rebuild_rows must be non-negative")
	}
	if c.Gateway.Scheduling.FullRebuildIntervalSeconds < 0 {
		return fmt.Errorf("gateway.scheduling.full_rebuild_interval_seconds must be non-negative")
	}
	// Positive outbox thresholds should warn before forced rebuild. Check their
	// relative order when both mechanisms are configured, allowing zero to retain
	// each consumer's disabled-threshold behavior.
	if c.Gateway.Scheduling.OutboxLagWarnSeconds > 0 &&
		c.Gateway.Scheduling.OutboxLagRebuildSeconds > 0 &&
		c.Gateway.Scheduling.OutboxLagRebuildSeconds < c.Gateway.Scheduling.OutboxLagWarnSeconds {
		return fmt.Errorf("gateway.scheduling.outbox_lag_rebuild_seconds must be >= outbox_lag_warn_seconds")
	}
	return nil
}

// validateLog checks logger formats, destinations, rotation, and sampling
// controls in their startup order. It returns the first invalid field and leaves
// configured values unchanged, including sampling values while sampling is
// disabled.
func (c *Config) validateLog() error {
	switch c.Log.Level {
	case "debug", "info", "warn", "error":
	case "":
		return fmt.Errorf("log.level is required")
	default:
		return fmt.Errorf("log.level must be one of: debug/info/warn/error")
	}
	switch c.Log.Format {
	case "json", "console":
	case "":
		return fmt.Errorf("log.format is required")
	default:
		return fmt.Errorf("log.format must be one of: json/console")
	}
	switch c.Log.StacktraceLevel {
	case "none", "error", "fatal":
	case "":
		return fmt.Errorf("log.stacktrace_level is required")
	default:
		return fmt.Errorf("log.stacktrace_level must be one of: none/error/fatal")
	}
	if !c.Log.Output.ToStdout && !c.Log.Output.ToFile {
		return fmt.Errorf("log.output.to_stdout and log.output.to_file cannot both be false")
	}
	if c.Log.Rotation.MaxSizeMB <= 0 {
		return fmt.Errorf("log.rotation.max_size_mb must be positive")
	}
	if c.Log.Rotation.MaxBackups < 0 {
		return fmt.Errorf("log.rotation.max_backups must be non-negative")
	}
	if c.Log.Rotation.MaxAgeDays < 0 {
		return fmt.Errorf("log.rotation.max_age_days must be non-negative")
	}
	if c.Log.Sampling.Enabled {
		if c.Log.Sampling.Initial <= 0 {
			return fmt.Errorf("log.sampling.initial must be positive when sampling is enabled")
		}
		if c.Log.Sampling.Thereafter <= 0 {
			return fmt.Errorf("log.sampling.thereafter must be positive when sampling is enabled")
		}
	} else {
		if c.Log.Sampling.Initial < 0 {
			return fmt.Errorf("log.sampling.initial must be non-negative")
		}
		if c.Log.Sampling.Thereafter < 0 {
			return fmt.Errorf("log.sampling.thereafter must be non-negative")
		}
	}
	return nil
}

// validateWebAuthn checks the enabled relying-party identity and browser origins.
// Disabled passkey settings remain untouched. For enabled settings, it trims the
// display name, lowercases the relying-party ID, and replaces nonempty origin
// input with a trimmed list before checking required fields.
//
// The method then canonicalizes accepted origins in order. A failure leaves
// these mutations visible, including earlier accepted origins, while later
// origins retain their trimmed input spelling. Callers serialize access to the
// receiver during validation.
func (c *Config) validateWebAuthn() error {
	if c.WebAuthn.Enabled {
		c.WebAuthn.RPDisplayName = strings.TrimSpace(c.WebAuthn.RPDisplayName)
		c.WebAuthn.RPID = strings.ToLower(strings.TrimSpace(c.WebAuthn.RPID))
		c.WebAuthn.RPOrigins = normalizeStringSlice(c.WebAuthn.RPOrigins)
		if c.WebAuthn.RPDisplayName == "" {
			return fmt.Errorf("webauthn.rp_display_name is required when passkeys are enabled")
		}
		if c.WebAuthn.RPID == "" {
			return fmt.Errorf("webauthn.rp_id is required when passkeys are enabled")
		}
		if strings.Contains(c.WebAuthn.RPID, "://") || strings.ContainsAny(c.WebAuthn.RPID, "/:") {
			return fmt.Errorf("webauthn.rp_id must be a domain without scheme, port, or path")
		}
		if len(c.WebAuthn.RPOrigins) == 0 {
			return fmt.Errorf("webauthn.rp_origins must contain at least one origin when passkeys are enabled")
		}
		for i, origin := range c.WebAuthn.RPOrigins {
			u, err := url.Parse(origin)
			if err != nil || u.Scheme == "" || u.Host == "" {
				return fmt.Errorf("webauthn.rp_origins contains invalid origin %q", origin)
			}
			if u.User != nil || u.RawQuery != "" || u.Fragment != "" || u.Path != "" {
				return fmt.Errorf("webauthn.rp_origins entry %q must not include userinfo, path, query, or fragment", origin)
			}
			u.Scheme = strings.ToLower(u.Scheme)
			u.Host = strings.ToLower(u.Host)
			host := strings.ToLower(u.Hostname())
			localDevelopment := host == "localhost" || host == "127.0.0.1" || host == "::1"
			// Passkey origins require HTTPS in the deployment rule. Permit HTTP for the
			// exact localhost and loopback host forms so a local development relying
			// party can use a plain HTTP origin.
			if u.Scheme != "https" && (u.Scheme != "http" || !localDevelopment) {
				return fmt.Errorf("webauthn.rp_origins entry %q must use HTTPS (HTTP is allowed only for localhost)", origin)
			}
			if host != c.WebAuthn.RPID && !strings.HasSuffix(host, "."+c.WebAuthn.RPID) {
				return fmt.Errorf("webauthn.rp_origins entry %q is not within relying party ID %q", origin, c.WebAuthn.RPID)
			}
			c.WebAuthn.RPOrigins[i] = u.Scheme + "://" + u.Host
		}
	}
	return nil
}

// validateOIDC checks enabled login credentials, scopes, and endpoints in their
// startup order. Disabled settings pass unchanged. Validation reads trimmed
// views of strings, keeps the configured values, and emits HTTP warnings only
// after all OIDC checks succeed.
func (c *Config) validateOIDC() error {
	if c.OIDC.Enabled {
		if strings.TrimSpace(c.OIDC.ClientID) == "" {
			return fmt.Errorf("oidc_connect.client_id is required when oidc_connect.enabled=true")
		}
		if strings.TrimSpace(c.OIDC.IssuerURL) == "" {
			return fmt.Errorf("oidc_connect.issuer_url is required when oidc_connect.enabled=true")
		}
		if strings.TrimSpace(c.OIDC.RedirectURL) == "" {
			return fmt.Errorf("oidc_connect.redirect_url is required when oidc_connect.enabled=true")
		}
		if strings.TrimSpace(c.OIDC.FrontendRedirectURL) == "" {
			return fmt.Errorf("oidc_connect.frontend_redirect_url is required when oidc_connect.enabled=true")
		}
		if !scopeContainsOpenID(c.OIDC.Scopes) {
			return fmt.Errorf("oidc_connect.scopes must contain openid")
		}

		method := strings.ToLower(strings.TrimSpace(c.OIDC.TokenAuthMethod))
		switch method {
		case "", "client_secret_post", "client_secret_basic", "none":
		default:
			return fmt.Errorf("oidc_connect.token_auth_method must be one of: client_secret_post/client_secret_basic/none")
		}
		if (method == "" || method == "client_secret_post" || method == "client_secret_basic") &&
			strings.TrimSpace(c.OIDC.ClientSecret) == "" {
			return fmt.Errorf("oidc_connect.client_secret is required when oidc_connect.enabled=true and token_auth_method is client_secret_post/client_secret_basic")
		}
		// Enabled OIDC bounds this tolerance even when ID-token validation is
		// disabled.
		if c.OIDC.ClockSkewSeconds < 0 || c.OIDC.ClockSkewSeconds > 600 {
			return fmt.Errorf("oidc_connect.clock_skew_seconds must be between 0 and 600")
		}
		if c.OIDC.ValidateIDToken && strings.TrimSpace(c.OIDC.AllowedSigningAlgs) == "" {
			return fmt.Errorf("oidc_connect.allowed_signing_algs is required when oidc_connect.validate_id_token=true")
		}

		if err := ValidateAbsoluteHTTPURL(c.OIDC.IssuerURL); err != nil {
			return fmt.Errorf("oidc_connect.issuer_url invalid: %w", err)
		}
		if v := strings.TrimSpace(c.OIDC.DiscoveryURL); v != "" {
			if err := ValidateAbsoluteHTTPURL(v); err != nil {
				return fmt.Errorf("oidc_connect.discovery_url invalid: %w", err)
			}
		}
		if v := strings.TrimSpace(c.OIDC.AuthorizeURL); v != "" {
			if err := ValidateAbsoluteHTTPURL(v); err != nil {
				return fmt.Errorf("oidc_connect.authorize_url invalid: %w", err)
			}
		}
		if v := strings.TrimSpace(c.OIDC.TokenURL); v != "" {
			if err := ValidateAbsoluteHTTPURL(v); err != nil {
				return fmt.Errorf("oidc_connect.token_url invalid: %w", err)
			}
		}
		if v := strings.TrimSpace(c.OIDC.UserInfoURL); v != "" {
			if err := ValidateAbsoluteHTTPURL(v); err != nil {
				return fmt.Errorf("oidc_connect.userinfo_url invalid: %w", err)
			}
		}
		if v := strings.TrimSpace(c.OIDC.JWKSURL); v != "" {
			if err := ValidateAbsoluteHTTPURL(v); err != nil {
				return fmt.Errorf("oidc_connect.jwks_url invalid: %w", err)
			}
		}
		if err := ValidateAbsoluteHTTPURL(c.OIDC.RedirectURL); err != nil {
			return fmt.Errorf("oidc_connect.redirect_url invalid: %w", err)
		}
		if err := ValidateFrontendRedirectURL(c.OIDC.FrontendRedirectURL); err != nil {
			return fmt.Errorf("oidc_connect.frontend_redirect_url invalid: %w", err)
		}

		warnIfInsecureURL("oidc_connect.issuer_url", c.OIDC.IssuerURL)
		warnIfInsecureURL("oidc_connect.discovery_url", c.OIDC.DiscoveryURL)
		warnIfInsecureURL("oidc_connect.authorize_url", c.OIDC.AuthorizeURL)
		warnIfInsecureURL("oidc_connect.token_url", c.OIDC.TokenURL)
		warnIfInsecureURL("oidc_connect.userinfo_url", c.OIDC.UserInfoURL)
		warnIfInsecureURL("oidc_connect.jwks_url", c.OIDC.JWKSURL)
		warnIfInsecureURL("oidc_connect.redirect_url", c.OIDC.RedirectURL)
		warnIfInsecureURL("oidc_connect.frontend_redirect_url", c.OIDC.FrontendRedirectURL)
	}
	return nil
}

// normalizeStringSlice returns trimmed nonblank entries in their supplied
// order. Empty input retains its nil or allocated-empty shape; nonempty input
// gets new outer storage and can return an allocated empty result.
func normalizeStringSlice(values []string) []string {
	if len(values) == 0 {
		return values
	}
	normalized := make([]string, 0, len(values))
	for _, v := range values {
		trimmed := strings.TrimSpace(v)
		if trimmed == "" {
			continue
		}
		normalized = append(normalized, trimmed)
	}
	return normalized
}

// isWeakJWTSecret reports blank or known weak-secret spellings after trimming
// and lowercasing. It is a warning-list comparison rather than an entropy or
// cryptographic strength measurement; other text returns false.
func isWeakJWTSecret(secret string) bool {
	lower := strings.ToLower(strings.TrimSpace(secret))
	if lower == "" {
		return true
	}
	weak := map[string]struct{}{
		"change-me-in-production": {},
		"changeme":                {},
		"secret":                  {},
		"password":                {},
		"123456":                  {},
		"12345678":                {},
		"admin":                   {},
		"jwt-secret":              {},
	}
	_, exists := weak[lower]
	return exists
}

// generateJWTSecret returns lowercase hexadecimal text from byteLength random
// bytes, selecting thirty-two bytes for nonpositive input. Encoded text
// contains twice the selected byte count and returns with nil error.
// crypto/rand.Read terminates the process when io.ReadFull reports an error.
func generateJWTSecret(byteLength int) (string, error) {
	if byteLength <= 0 {
		byteLength = 32
	}
	buf := make([]byte, byteLength)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	return hex.EncodeToString(buf), nil
}

// GetServerAddress reads the listener through a separate Viper instance before
// complete startup validation. Environment values precede selected YAML and
// defaults, and configuration-file read errors are discarded here so full
// loading can report them later. Host formatting performs no validation or IPv6
// bracketing.
func GetServerAddress() string {
	v := viper.New()
	v.SetConfigName("config")
	v.SetConfigType("yaml")
	configureConfigSource(v.SetConfigFile, v.AddConfigPath)

	// Environment variables can override the file's listen address.
	v.AutomaticEnv()
	v.SetEnvKeyReplacer(strings.NewReplacer(".", "_"))
	v.SetDefault("server.host", "0.0.0.0")
	v.SetDefault("server.port", 8080)

	// Startup needs a listener address before complete setup is available. Discard
	// file-read errors in this lightweight lookup; the full loader reports invalid
	// explicit files when startup reaches configuration validation.
	_ = v.ReadInConfig()

	host := v.GetString("server.host")
	port := v.GetInt("server.port")
	return fmt.Sprintf("%s:%d", host, port)
}

// ValidateAbsoluteHTTPURL delegates trimmed HTTP(S) endpoint-shape checking to
// identity authconfig. It requires an absolute URL with a host and no fragment;
// callers retain input and separately decide allowed userinfo, ports, paths, or
// reachability.
func ValidateAbsoluteHTTPURL(raw string) error { return authconfig.ValidateAbsoluteHTTPURL(raw) }

// ValidateFrontendRedirectURL delegates callback-shape checking to identity
// authconfig. It accepts a trimmed single-slash path or absolute HTTP(S)
// endpoint, rejects remaining line breaks and protocol-relative prefixes, and
// leaves the caller's input unchanged.
func ValidateFrontendRedirectURL(raw string) error {
	return authconfig.ValidateFrontendRedirectURL(raw)
}

// scopeContainsOpenID reports whether lowercased whitespace-separated scope
// text contains the exact openid token.
func scopeContainsOpenID(scopes string) bool {
	return slices.Contains(strings.Fields(strings.ToLower(strings.TrimSpace(scopes))), "openid")
}

// warnIfInsecureURL logs a field-name warning for parsed HTTP URLs, ignoring
// case in the scheme. Parse failures return silently and non-HTTP schemes
// produce no warning; this helper does not validate or reject the configured
// value.
func warnIfInsecureURL(field, raw string) {
	u, err := url.Parse(strings.TrimSpace(raw))
	// A malformed URL supplies no reliable scheme for this warning helper. Leave
	// rejection to the caller's validator and return without emitting a misleading
	// transport warning.
	if err != nil {
		return
	}
	if strings.EqualFold(u.Scheme, "http") {
		slog.Warn("url uses http scheme; use https in production to avoid token leakage", "field", field)
	}
}

// normalizePricingCatalogSource rewrites recognized catalog URLs and absent
// known packaged supplement paths in the receiver. Custom inputs remain as
// supplied, and the mounted configuration and filesystem contents are not
// rewritten.
//
// The exact unqualified models.dev URL gains type=all. Recognized GitHub legacy
// repository paths select that catalog and add its host once to pricing
// admission settings. Only known packaged supplement paths migrate when Stat
// reports nonexistence; existing paths and other stat errors retain their
// supplied text. The next catalog loader owns missing-file behavior after this
// in-memory selection.
func (c *Config) normalizePricingCatalogSource() {
	// The exact older official catalog URL omits the all-model query selection.
	// Add that selection in memory while keeping URLs with existing query choices
	// under their configured source spelling.
	if strings.TrimSpace(c.Pricing.RemoteURL) == "https://models.dev/catalog.json" {
		c.Pricing.RemoteURL = "https://models.dev/catalog.json?type=all"
	}

	// Recognize known public legacy repositories by host, scheme, absent userinfo,
	// and normalized path. Custom source identities stay available for the
	// configured catalog downloader.
	source, err := url.Parse(c.Pricing.RemoteURL)
	if err == nil && strings.EqualFold(source.Host, "raw.githubusercontent.com") && source.User == nil && (source.Scheme == "https" || source.Scheme == "http") {
		sourcePath := strings.ToLower(path.Clean(source.Path))
		knownRepository := strings.HasPrefix(sourcePath, "/wei-shaw/model-price-repo/") || strings.HasPrefix(sourcePath, "/berriai/litellm/")
		if knownRepository && strings.HasSuffix(sourcePath, "/model_prices_and_context_window.json") {
			c.Pricing.RemoteURL = "https://models.dev/catalog.json?type=all"
			if !slices.Contains(c.Security.URLAllowlist.PricingHosts, "models.dev") {
				c.Security.URLAllowlist.PricingHosts = append(c.Security.URLAllowlist.PricingHosts, "models.dev")
			}
		}
	}
	fallback := filepath.Clean(c.Pricing.FallbackFile)
	// Recognized packaged supplement filenames can move to the current sibling
	// name when the old file is absent. Other paths remain owned by the deployment
	// and keep their configured spelling.
	if fallback != "resources/model-pricing/model_prices_and_context_window.json" && fallback != "/app/resources/model-pricing/model_prices_and_context_window.json" {
		return
	}
	// Use confirmed nonexistence of the old filename as the migration signal.
	// Supplement loading later checks the replacement and can use embedded data
	// when that file is also absent.
	if _, err := os.Stat(fallback); os.IsNotExist(err) {
		c.Pricing.FallbackFile = filepath.Join(filepath.Dir(fallback), "model_pricing_supplements.json")
	}
}

// ValidateDingTalkConfig delegates DingTalk deployment rule checking to
// identity authconfig. Disabled settings are accepted, and recognized mode/type
// failures retain shared sentinel identity for callers using errors.Is across
// this facade.
func ValidateDingTalkConfig(value DingTalkConnectConfig) error {
	return authconfig.ValidateDingTalkConfig(value)
}
