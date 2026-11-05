package config

import (
	"bytes"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"log/slog"
	"math"
	"net/netip"
	"net/url"
	"os"
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

// config.go decodes, normalizes, and validates versioned process configuration.
// Startup selects one YAML document, upgrades historical input through the
// migration layer, rejects unknown keys, and resolves explicit references before
// decoding with local Viper state. Application construction receives the resulting
// settings through typed module options. File presence remains observable for
// client-IP policy and OAuth flags even when their supplied values are empty or
// false; code defaults do not make those inputs explicit.
//
// Validation publishes copied forwarded-IP policy and normalizes selected values,
// so an error can follow earlier mutations to the loaded object. Bootstrap may
// defer JWT signing-key selection to persistent storage. Missing encryption input
// receives an ephemeral key marked unconfigured, whereas setup writes a stable
// key into its installation file. Historical aliases and implicit environment
// overrides belong exclusively to version 1 migration rather than this decoder.

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
	// Override it with gateway.upstream_response_max_bytes.
	DefaultUpstreamResponseReadMaxBytes int64 = 128 * 1024 * 1024

	// DefaultModelsListReadMaxBytes limits upstream model-list response bodies.
	// Override it with gateway.models_list_response_max_bytes.
	DefaultModelsListReadMaxBytes int64 = 8 * 1024 * 1024

	// These values supply WeChat Open-mode and frontend callback defaults while
	// identity defaults normalize stored mode and scope choices for runtime readers.
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
	// process login settings.
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
	APIKey        APIKeyConfig        `mapstructure:"api_key"`        // api key settings
	Auth          AuthConfig          `mapstructure:"auth"`           // process auth settings
	BatchImage    BatchImageConfig    `mapstructure:"batch_image"`    // batch image settings
	Billing       BillingConfig       `mapstructure:"billing"`        // process billing settings
	ConfigVersion int                 `mapstructure:"config_version"` // Current configuration schema version.
	Creative      CreativeConfig      `mapstructure:"creative"`       // process creative settings
	Database      DatabaseConfig      `mapstructure:"database"`       // process database settings
	Debug         DebugConfig         `mapstructure:"debug"`          // process debug settings
	Egress        EgressConfig        `mapstructure:"egress"`         // process egress settings
	Environment   string              `mapstructure:"environment"`    // record environment label text
	Gateway       GatewayConfig       `mapstructure:"gateway"`        // process gateway settings
	Idempotency   IdempotencyConfig   `mapstructure:"idempotency"`    // process idempotency settings
	Log           LogConfig           `mapstructure:"log"`            // process log settings
	ModelCatalog  ModelCatalogConfig  `mapstructure:"model_catalog"`  // model catalog settings
	Ops           OpsConfig           `mapstructure:"ops"`            // process ops settings
	OutboundProxy OutboundProxyConfig `mapstructure:"outbound_proxy"` // outbound proxy settings
	Payment       PaymentConfig       `mapstructure:"payment"`        // process payment settings
	Plugins       PluginsConfig       `mapstructure:"plugins"`        // process plugins settings
	Provider      ProviderConfig      `mapstructure:"provider"`       // process provider settings
	Redis         RedisConfig         `mapstructure:"redis"`          // process redis settings
	RequestLog    RequestLogConfig    `mapstructure:"request_log"`    // request log settings
	Scheduling    SchedulingConfig    `mapstructure:"scheduling"`     // process scheduling settings
	Security      SecurityConfig      `mapstructure:"security"`       // process security settings
	Server        ServerConfig        `mapstructure:"server"`         // process server settings
	Site          SiteConfig          `mapstructure:"site"`           // process site settings
	Storage       StorageConfig       `mapstructure:"storage"`        // process storage settings
	Team          TeamConfig          `mapstructure:"team"`           // process team settings
	// Timezone selects the calendar zone initialized by app/bootstrap after
	// loading. The file value and bootstrap empty-value fallback use Asia/Shanghai
	// by default; invalid IANA names fail before time.Local is published.
	Timezone     string             `mapstructure:"timezone"`
	Update       UpdateConfig       `mapstructure:"update"`        // process update settings
	Usage        UsageConfig        `mapstructure:"usage"`         // process usage settings
	UserDefaults UserDefaultsConfig `mapstructure:"user_defaults"` // user defaults settings
	// MCPOAuthSigningSecret is loaded from persistent security storage during
	// bootstrap. File decoding never accepts or overwrites this signing material.
	// MCPOAuthSigningSecret is loaded from persistent security storage during
	// bootstrap. File decoding never accepts or overwrites this signing material.
	MCPOAuthSigningSecret string `mapstructure:"-" json:"-" yaml:"-"`
}

// TeamConfig sets the default availability and limits of team features.
type TeamConfig struct {
	// initial team membership limit
	DefaultMaxMembers int  `mapstructure:"default_max_members"`
	Enabled           bool `mapstructure:"enabled"` // deployment team availability flag
	// user team creation availability flag
	SelfServiceEnabled bool `mapstructure:"self_service_enabled"`
}

// LogConfig holds process logging thresholds, labels, sinks, rotation, and
// sampling controls.
type LogConfig struct {
	Caller bool `mapstructure:"caller"` // caller location inclusion flag
	// console or JSON encoding selection
	Format   string            `mapstructure:"format"`
	Level    string            `mapstructure:"level"`    // minimum process log severity
	Output   LogOutputConfig   `mapstructure:"output"`   // log output settings
	Rotation LogRotationConfig `mapstructure:"rotation"` // log rotation settings
	Sampling LogSamplingConfig `mapstructure:"sampling"` // log sampling settings
	// record service label text
	ServiceName string `mapstructure:"service_name"`
	// stacktrace inclusion severity text
	StacktraceLevel string `mapstructure:"stacktrace_level"`
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

// UpdateConfig supplies GitHub authentication for the application release-check client.
type UpdateConfig struct {
	GitHubToken string `mapstructure:"github_token"` // GitHub release download authentication token.
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

// LinuxDoConnectConfig aliases the identity module's LinuxDo OAuth deployment
// settings.
type LinuxDoConnectConfig = authconfig.LinuxDoConnectConfig

// OIDCConnectConfig aliases identity settings for issuer discovery and OIDC
// login validation.
type OIDCConnectConfig = authconfig.OIDCConnectConfig

// EmailOAuthProviderConfig configures email-based OAuth login such as GitHub and Google.
type EmailOAuthProviderConfig = authconfig.EmailOAuthProviderConfig

// TokenRefreshConfig supplies polling, admission, retry, and timeout defaults
// for token refresh workers.
type TokenRefreshConfig struct {
	// AttemptTimeoutSeconds supplies each refresh exchange budget when the caller
	// has no positive override. provider.RefreshTuning uses fifteen seconds for
	// nonpositive input, caps configured positive budgets at five minutes, and can
	// shorten them for a distributed lock lease.
	AttemptTimeoutSeconds int `mapstructure:"attempt_timeout_seconds"`
	// CandidatePageSize, ProviderConcurrency, ProviderQPS, and
	// ProviderFailureThreshold become bounded maintenance limits through
	// provider.RefreshTuning. Its accessors replace nonpositive values with
	// matching defaults and cap positive values at each owning ceiling.
	// database candidate page size
	CandidatePageSize int `mapstructure:"candidate_page_size"`
	// Interval between refresh checks, in minutes.
	CheckIntervalMinutes int `mapstructure:"check_interval_minutes"`
	// CycleTimeoutSeconds bounds a complete candidate scan, including its page
	// processing. provider.RefreshTuning selects four minutes for nonpositive
	// input and caps positive values at one hour.
	CycleTimeoutSeconds int `mapstructure:"cycle_timeout_seconds"`
	// Enabled controls automatic token refresh.
	Enabled bool `mapstructure:"enabled"`
	// MaxAttempts supplies the total attempted refresh exchanges, including the
	// first request. provider.RefreshTuning.Retries selects one attempt for
	// nonpositive input and caps positive counts at ten.
	MaxAttempts int `mapstructure:"max_attempts"`
	// occupied attempts per platform
	PlatformConcurrency int `mapstructure:"platform_concurrency"`
	// consecutive platform failure trip count
	PlatformFailureThreshold int `mapstructure:"platform_failure_threshold"`
	PlatformQPS              int `mapstructure:"platform_qps"` // exchange starts per platform second
	// Hours before expiry at which a token becomes eligible for refresh.
	RefreshBeforeExpiryHours float64 `mapstructure:"refresh_before_expiry_hours"`
	// RetryBackoffSeconds supplies the initial retry delay in seconds.
	// provider.RefreshTuning disables waiting for nonpositive input; positive
	// delays undergo exponential growth, provider/attempt jitter, and a
	// thirty-second final cap.
	RetryBackoffSeconds int `mapstructure:"retry_backoff_seconds"`
}

// ServerConfig holds listener, ingress, trusted-proxy, and optional collector
// settings for server construction.
type ServerConfig struct {
	ClientIP            ServerClientIPConfig `mapstructure:"client_ip"`              // server client ip settings
	EnableServerTiming  bool                 `mapstructure:"enable_server_timing"`   // Return Server-Timing for admin page requests.
	H2C                 H2CConfig            `mapstructure:"h2c"`                    // server h2c settings
	Host                string               `mapstructure:"host"`                   // listener host address text
	IdleTimeoutSeconds  int                  `mapstructure:"idle_timeout_seconds"`   // Idle connection timeout, in seconds.
	MaxHeaderBytes      int                  `mapstructure:"max_header_bytes"`       // Header size limit in bytes; maps to the HTTP/2 header-list limit.
	MaxRequestBodyBytes int64                `mapstructure:"max_request_body_bytes"` // Global request-body size limit, in bytes.
	// Gin debug or release mode
	Mode                     string `mapstructure:"mode"`
	Port                     int    `mapstructure:"port"`                        // listener TCP port number
	ReadHeaderTimeoutSeconds int    `mapstructure:"read_header_timeout_seconds"` // Request-header read timeout, in seconds.
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
	Enabled                           bool   `mapstructure:"enabled"`
	IdleTimeoutSeconds                int    `mapstructure:"idle_timeout_seconds"`                   // Idle timeout, in seconds.
	MaxConcurrentStreams              uint32 `mapstructure:"max_concurrent_streams"`                 // Maximum concurrent streams.
	MaxReadFrameBytes                 int    `mapstructure:"max_read_frame_bytes"`                   // Maximum frame size, in bytes.
	MaxUploadBufferPerConnectionBytes int    `mapstructure:"max_upload_buffer_per_connection_bytes"` // Upload buffer per connection, in bytes.
	MaxUploadBufferPerStreamBytes     int    `mapstructure:"max_upload_buffer_per_stream_bytes"`     // Upload buffer per stream, in bytes.
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

// SecurityConfig supplies browser security, outbound URL admission, and persistent
// credential encryption inputs used during application construction.
type SecurityConfig struct {
	CORS CORSConfig `mapstructure:"cors"` // browser cross-origin response settings
	CSP  CSPConfig  `mapstructure:"csp"`  // security csp settings
	// SecretEncryptionKey is the hex-encoded 32-byte AES-256 key used to encrypt TOTP secrets.
	// Empty generates a random development key at startup.
	SecretEncryptionKey string             `mapstructure:"secret_encryption_key"`
	URLAllowlist        URLAllowlistConfig `mapstructure:"url_allowlist"` // security url allowlist settings
	// SecretEncryptionKeyConfigured distinguishes supplied encryption input from an
	// ephemeral generated key, allowing administration to require stable credential storage.
	SecretEncryptionKeyConfigured bool `mapstructure:"-" json:"-" yaml:"-"`
}

// URLAllowlistConfig supplies host patterns and private or HTTP admission
// choices for URL checks.
type URLAllowlistConfig struct {
	// AllowInsecureHTTP permits HTTP in format-only operator URL checks when the
	// allowlist is disabled. setDefaults enables this flag; enabled
	// OperatorURLPolicy instead applies its HTTPS allowlist check.
	AllowInsecureHTTP bool `mapstructure:"allow_insecure_http"`
	// private target address admission switch
	AllowPrivateHosts bool `mapstructure:"allow_private_hosts"`
	// permitted CRS hostname patterns
	CrsSyncHosts []string `mapstructure:"crs_sync_hosts"`
	Enabled      bool     `mapstructure:"enabled"` // outbound allowlist check switch
	// permitted catalog hostname patterns
	ModelCatalogHosts []string `mapstructure:"model_catalog_hosts"`
	// permitted upstream hostname patterns
	UpstreamHosts []string `mapstructure:"upstream_hosts"`
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

// ProbeURLConfig describes a proxy-egress probe endpoint and its response parser.
type ProbeURLConfig struct {
	URL    string `mapstructure:"url"`    // proxy probe endpoint URL
	Parser string `mapstructure:"parser"` // ip-api, ipify, or chatgpt-trace parser
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
	Anthropic GatewayAnthropicConfig `mapstructure:"anthropic"` // gateway anthropic settings
	Failover  GatewayFailoverConfig  `mapstructure:"failover"`  // gateway failover settings
	Gemini    GatewayGeminiConfig    `mapstructure:"gemini"`    // gateway provider.gemini settings
	Grok      GatewayGrokConfig      `mapstructure:"grok"`      // gateway grok settings
	Images    GatewayImagesConfig    `mapstructure:"images"`    // gateway images settings
	// Maximum gateway request-body size, in bytes.
	MaxRequestBodyBytes int64 `mapstructure:"max_request_body_bytes"`
	// MaxSSELineBytes limits an upstream SSE line in bytes; zero uses the default.
	MaxSSELineBytes int `mapstructure:"max_sse_line_bytes"`
	// Upstream model-list response limit, in bytes.
	ModelsListResponseMaxBytes int64                  `mapstructure:"models_list_response_max_bytes"`
	OpenAI                     GatewayOpenAIConfig    `mapstructure:"openai"`    // gateway openai settings
	Streaming                  GatewayStreamingConfig `mapstructure:"streaming"` // gateway streaming settings
	// TextMaxRequestBodyBytes limits bodies on endpoints that disallow inline image or
	// video payloads.
	TextMaxRequestBodyBytes int64                      `mapstructure:"text_max_request_body_bytes"`
	Transport               GatewayTransportConfig     `mapstructure:"transport"`                 // gateway transport settings
	UpstreamErrorBodyLog    UpstreamErrorBodyLogConfig `mapstructure:"upstream_error_body_log"`   // gateway upstream error body log settings
	ResponseHeaders         ResponseHeaderConfig       `mapstructure:"upstream_response_headers"` // gateway upstream response headers settings
	// Non-streaming upstream response limit in bytes, bounding memory used by body reads.
	UpstreamResponseMaxBytes int64 `mapstructure:"upstream_response_max_bytes"`
}

// GatewayGrokConfig supplies the response-header wait budget for Grok gateway requests.
type GatewayGrokConfig struct {
	// ResponseHeaderTimeoutSeconds bounds xAI/Grok response-header waits in seconds.
	// With configured transport options, zero disables this deadline; only absent
	// options select the transport's 120-second Grok default. The generic gateway
	// header timeout is replaced during Grok profile selection.
	ResponseHeaderTimeoutSeconds int `mapstructure:"response_header_timeout_seconds"`
}

// GatewayUsageMonitorConfig configures periodic balance and quota probes for
// Kimi, Zhipu, and DeepSeek API-key providers. Disabled by default; when
// enabled it probes active API-key providers. Manual queries run independently.
type GatewayUsageMonitorConfig struct {
	// simultaneous supplier probe limit
	Concurrency int `mapstructure:"concurrency"`
	// Enabled selects periodic supplier probes, while the deployment
	// default leaves that monitor disabled. Administrator-initiated usage queries
	// remain separate from this background availability choice.
	Enabled bool `mapstructure:"enabled"`
	// supplier probe round interval minutes
	IntervalMinutes int `mapstructure:"interval_minutes"`
	// supplier low-balance observation threshold
	PauseBelowBalance float64 `mapstructure:"pause_below_balance"`
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
	Enabled bool `mapstructure:"enabled"` // OpenAI proxy stream circuit switch.
	// FailureThreshold counts eligible proxy disconnections within WindowSeconds,
	// and TTLSeconds controls the resulting isolation period.
	// egress.NewProxyStreamCircuit replaces nonpositive counts and durations with
	// its corresponding defaults; app converts Enabled into the consumer
	// option that bypasses circuit recording when false.
	// proxy disconnection trigger count
	FailureThreshold int `mapstructure:"failure_threshold"`
	TTLSeconds       int `mapstructure:"ttl_seconds"` // proxy isolation retention seconds
	// proxy disconnection window seconds
	WindowSeconds int `mapstructure:"window_seconds"`
}

// UserMessageQueueConfig configures serial user-message delivery
// for Anthropic OAuth and SetupToken providers.
type UserMessageQueueConfig struct {
	// CleanupIntervalSeconds supplies the orphaned message-lock cleanup cadence.
	// Zero disables this periodic cleanup choice, while message admission and its
	// lock lifetime retain their own settings.
	CleanupIntervalSeconds int `mapstructure:"cleanup_interval_seconds"`
	// DefaultMode selects serialize, throttle, or disabled message queue behavior.
	// Serialize holds a provider lock and applies RPM delay; throttle applies
	// delay while allowing concurrent requests. Empty text disables the queue.
	DefaultMode string `mapstructure:"default_mode"` // serialize, throttle, or empty choice
	// LockTTLMs is the serial lock lifetime in milliseconds; set it longer than
	// the longest request.
	LockTTLMs int `mapstructure:"lock_ttl_ms"`
	// MinDelayMs and MaxDelayMs bound RPM-based delay interpolation in
	// milliseconds. scheduler.CalculateRPMAwareDelay replaces nonpositive values
	// with its defaults and swaps inverted bounds before calculating the delay and
	// applying jitter.
	// lower RPM-delay interpolation milliseconds
	MinDelayMs int `mapstructure:"min_delay_ms"`
	// upper RPM-delay interpolation milliseconds
	MaxDelayMs int `mapstructure:"max_delay_ms"`
	// WaitTimeoutMs bounds lock acquisition in milliseconds.
	WaitTimeoutMs int `mapstructure:"wait_timeout_ms"`
}

// GatewayOpenAIWSConfig supplies deployment defaults for Responses WS,
// overridable by admin settings.
type GatewayOpenAIWSConfig struct {
	// API-key pool concurrency multiplier
	APIKeyMaxConnsFactor float64 `mapstructure:"api_key_max_conns_factor"`
	// ingress first-message wait seconds
	ClientFirstMessageTimeoutSeconds int `mapstructure:"client_first_message_timeout_seconds"`
	// client message read-limit bytes
	ClientReadLimitBytes int64 `mapstructure:"client_read_limit_bytes"`
	// upstream connection dialing seconds
	DialTimeoutSeconds int `mapstructure:"dial_timeout_seconds"`
	// concurrency-derived pool capacity switch
	DynamicMaxConnsByProviderConcurrencyEnabled bool `mapstructure:"dynamic_max_conns_by_provider_concurrency_enabled"`
	// large message HTTP-bridge threshold bytes
	HTTPBridgeThresholdBytes int64 `mapstructure:"http_bridge_threshold_bytes"`
	// ingress between-turn idle seconds
	IngressInterTurnIdleTimeoutSeconds int `mapstructure:"ingress_inter_turn_idle_timeout_seconds"`
	// ingress previous-response recovery switch
	IngressPreviousResponseRecoveryEnabled bool `mapstructure:"ingress_previous_response_recovery_enabled"`
	// MaxConnsPerProvider, MinIdlePerProvider, and MaxIdlePerProvider describe one
	// provider pool. Startup validation keeps the idle minimum within the idle
	// maximum and keeps that maximum within total capacity before pool
	// construction.
	// provider connection pool capacity
	MaxConnsPerProvider int `mapstructure:"max_conns_per_provider"`
	// provider idle connection maximum
	MaxIdlePerProvider int `mapstructure:"max_idle_per_provider"`
	// per-key ingress connection limit
	MaxIngressConnectionsPerAPIKey int `mapstructure:"max_ingress_connections_per_api_key"`
	// provider idle connection minimum
	MinIdlePerProvider int `mapstructure:"min_idle_per_provider"`
	// OAuth pool concurrency multiplier
	OAuthMaxConnsFactor float64 `mapstructure:"oauth_max_conns_factor"`
	// pool target occupation fraction
	PoolTargetUtilization float64 `mapstructure:"pool_target_utilization"`
	// pool prewarming cooldown milliseconds
	PrewarmCooldownMS int `mapstructure:"prewarm_cooldown_ms"`
	// per-connection queued request limit
	QueueLimitPerConn int `mapstructure:"queue_limit_per_conn"`
	// upstream frame reading seconds
	ReadTimeoutSeconds int `mapstructure:"read_timeout_seconds"`
	// upstream frame writing seconds
	WriteTimeoutSeconds int `mapstructure:"write_timeout_seconds"`
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

// APIKeyAuthCacheConfig supplies authentication cache lifetimes, lookup
// admission, and invalid-key abuse limits.
type APIKeyAuthCacheConfig struct {
	// entry lifetime jitter percent
	JitterPercent int `mapstructure:"jitter_percent"`
	// process authentication entry capacity
	L1Size int `mapstructure:"l1_size"`
	// process authentication retention seconds
	L1TTLSeconds int `mapstructure:"l1_ttl_seconds"`
	// Redis authentication retention seconds
	L2TTLSeconds int `mapstructure:"l2_ttl_seconds"`
	// missing-key observation retention seconds
	NegativeTTLSeconds int `mapstructure:"negative_ttl_seconds"`
	// same-key authentication load coalescing switch
	Singleflight bool `mapstructure:"singleflight"`
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

// DingTalkOAuthConfig supplies active DingTalk login, organization, and profile-synchronization defaults.
type DingTalkOAuthConfig struct {
	AppType      string `mapstructure:"app_type"`      // public or internal application
	AuthorizeURL string `mapstructure:"authorize_url"` // browser authorization request endpoint
	// BypassRegistration permits closed-registration signup for internal_only
	// organizations. OAuthSettings clears the value when another organization policy
	// is selected before AuthService considers the bypass.
	BypassRegistration    bool   `mapstructure:"bypass_registration"`     // internal organization registration bypass
	ClientID              string `mapstructure:"client_id"`               // registered OAuth client identifier
	ClientSecret          string `mapstructure:"client_secret"`           // OAuth token exchange secret
	CorpRestrictionPolicy string `mapstructure:"corp_restriction_policy"` // none or internal_only policy
	// DingTalkAppKind requires internal_app for enabled settings. AppType additionally
	// requires internal when CorpRestrictionPolicy is internal_only; OAuthSettings
	// sets that effective AppType before calling ValidateDingTalkConfig.
	DingTalkAppKind     string `mapstructure:"dingtalk_app_kind"`     // internal_app for enabled login
	Enabled             bool   `mapstructure:"enabled"`               // provider login enablement flag
	FrontendRedirectURL string `mapstructure:"frontend_redirect_url"` // frontend flow completion destination
	// InternalCorpID retains an organization identifier for settings readback.
	// DingTalkHandler checks internal_only membership through the application
	// directory; DingTalkCorpAllowed accepts missing or different corpID values.
	InternalCorpID string `mapstructure:"internal_corp_id"` // configured organization identifier value
	RedirectURL    string `mapstructure:"redirect_url"`     // registered backend callback URL
	// RequireEmail prevents synthetic-email signup for a new DingTalk identity.
	// DingTalkHandler requests email completion when staff email is missing and signup
	// is allowed; a supplied staff email goes into account selection.
	RequireEmail bool   `mapstructure:"require_email"` // new identity email requirement
	Scopes       string `mapstructure:"scopes"`        // requested OAuth scope text
	// SyncCorpEmail, SyncDisplayName, and SyncDept select local attribute updates in
	// DingTalkProfileSync. OAuthSettings clears these flags outside internal_only mode,
	// and the sync service applies the same organization-policy check before writing.
	SyncCorpEmail bool `mapstructure:"sync_corp_email"` // enterprise email attribute synchronization
	// SyncCorpEmailAttrKey, SyncDisplayNameAttrKey, and SyncDeptAttrKey name the
	// existing local attribute definitions updated during profile sync. Their AttrName
	// partners provide labels when settings administration prepares those definitions.
	SyncCorpEmailAttrKey    string `mapstructure:"sync_corp_email_attr_key"`    // enterprise email attribute key
	SyncCorpEmailAttrName   string `mapstructure:"sync_corp_email_attr_name"`   // enterprise email attribute label
	SyncDept                bool   `mapstructure:"sync_dept"`                   // department path attribute synchronization
	SyncDeptAttrKey         string `mapstructure:"sync_dept_attr_key"`          // department path attribute key
	SyncDeptAttrName        string `mapstructure:"sync_dept_attr_name"`         // department path attribute label
	SyncDisplayName         bool   `mapstructure:"sync_display_name"`           // display name attribute synchronization
	SyncDisplayNameAttrKey  string `mapstructure:"sync_display_name_attr_key"`  // display name attribute key
	SyncDisplayNameAttrName string `mapstructure:"sync_display_name_attr_name"` // display name attribute label
	TokenURL                string `mapstructure:"token_url"`                   // authorization code exchange endpoint
	UserInfoURL             string `mapstructure:"userinfo_url"`                // authenticated profile request endpoint
}

// WeChatOAuthConfig supplies explicit Open, MP, and Mobile WeChat login defaults.
type WeChatOAuthConfig struct {
	Enabled             bool   `mapstructure:"enabled"`               // provider login enablement flag
	FrontendRedirectURL string `mapstructure:"frontend_redirect_url"` // frontend flow completion destination
	MobileAppID         string `mapstructure:"mobile_app_id"`         // mobile application identifier value
	MobileAppSecret     string `mapstructure:"mobile_app_secret"`     // mobile application secret value
	MobileEnabled       bool   `mapstructure:"mobile_enabled"`        // mobile login enablement flag
	Mode                string `mapstructure:"mode"`                  // selected WeChat login mode
	MPAppID             string `mapstructure:"mp_app_id"`             // public-account application identifier value
	MPAppSecret         string `mapstructure:"mp_app_secret"`         // public-account application secret value
	MPEnabled           bool   `mapstructure:"mp_enabled"`            // public-account login enablement flag
	OpenAppID           string `mapstructure:"open_app_id"`           // open-platform application identifier value
	OpenAppSecret       string `mapstructure:"open_app_secret"`       // open-platform application secret value
	OpenEnabled         bool   `mapstructure:"open_enabled"`          // open-platform login enablement flag
	RedirectURL         string `mapstructure:"redirect_url"`          // registered backend callback URL
	Scopes              string `mapstructure:"scopes"`                // requested OAuth scope text
}

// CodexConfig selects Codex client emulation, image bridging, and optional instruction-template input.
type CodexConfig struct {
	// ForceCLI treats OpenAI /v1/responses requests as Codex CLI requests.
	// Use when a gateway removes or rewrites User-Agent; disabled by default.
	ForceCLI bool `mapstructure:"force_cli"`
	// ForcedInstructionsTemplateFile is the template that supplies final
	// Codex instructions.
	// The rendered template replaces instructions; include {{ .ExistingInstructions }} to retain converted client system text.
	ForcedInstructionsTemplateFile string `mapstructure:"forced_instructions_template_file"`
	// ImageGenerationBridgeEnabled injects the image_generation tool and
	// bridge instructions into Codex
	// /v1/responses.
	// Disabled by default; requests already containing the tool still follow group
	// capabilities.
	ImageGenerationBridgeEnabled bool `mapstructure:"image_generation_bridge_enabled"`
	// ForcedInstructionsTemplate contains startup-loaded text when a template path
	// is configured. It is derived locally and never decoded from a YAML value.
	ForcedInstructionsTemplate string `mapstructure:"-" json:"-" yaml:"-"`
}

// UpstreamClientCacheConfig bounds the number and idle lifetime of cached upstream HTTP clients.
type UpstreamClientCacheConfig struct {
	// IdleTTLSeconds is the idle threshold for client reclamation.
	// Clients unused beyond this threshold become eligible for reclamation.
	// Choose the threshold based on request frequency.
	IdleTTLSeconds int `mapstructure:"idle_ttl_seconds"`
	// MaxClients limits cached upstream HTTP clients.
	// Pool isolation creates clients for distinct provider/proxy identities.
	// Exceeding the cache limit evicts the least recently used client.
	// Size this limit for the expected active provider/proxy identities.
	MaxClients int `mapstructure:"max_clients"`
}

// ProviderGrokOAuthConfig supplies explicit Grok OAuth endpoint, client, scope, and callback overrides.
type ProviderGrokOAuthConfig struct {
	AuthorizeURL string `mapstructure:"authorize_url"` // Grok OAuth authorize url override.
	ClientID     string `mapstructure:"client_id"`     // Grok OAuth client id override.
	RedirectURI  string `mapstructure:"redirect_uri"`  // Grok OAuth redirect uri override.
	Scope        string `mapstructure:"scope"`         // Grok OAuth scope override.
	TokenURL     string `mapstructure:"token_url"`     // Grok OAuth token url override.
}

// JWTConfig supplies signing input and access-token and refresh-token lifetimes.
type JWTConfig struct {
	AccessTokenTTLMinutes int `mapstructure:"access_token_ttl_minutes"` // Access token lifetime in minutes.
	// RefreshTokenTTLDays supplies the refresh-token lifetime in days. Default
	// loading selects thirty days, and validation requires a positive lifetime
	// while warning when the supplied value exceeds ninety days.
	RefreshTokenTTLDays int `mapstructure:"refresh_token_ttl_days"`
	// Secret is signing input validated during ordinary Load. LoadForBootstrap can
	// return it empty until database bootstrap selects the persisted key, then
	// performs full validation before identity construction.
	Secret string `mapstructure:"secret"`
}

// OAuthProvidersConfig groups per-provider login defaults supplied to database-backed identity settings readers.
type OAuthProvidersConfig struct {
	DingTalk DingTalkOAuthConfig      `mapstructure:"dingtalk"` // auth oauth dingtalk settings
	GitHub   EmailOAuthProviderConfig `mapstructure:"github"`   // auth oauth github settings
	Google   EmailOAuthProviderConfig `mapstructure:"google"`   // auth oauth google settings
	LinuxDo  LinuxDoConnectConfig     `mapstructure:"linuxdo"`  // auth oauth linuxdo settings
	OIDC     OIDCConnectConfig        `mapstructure:"oidc"`     // auth oauth oidc settings
	WeChat   WeChatOAuthConfig        `mapstructure:"wechat"`   // auth oauth wechat settings
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

// EgressProxyProbeConfig supplies startup configuration for its application consumers and validators.
type EgressProxyProbeConfig struct {
	// Proxy-probe response limit, in bytes.
	ResponseMaxBytes int64 `mapstructure:"response_max_bytes"`
	// URLs lists probe endpoints in priority order; empty uses the built-in list.
	URLs []ProbeURLConfig `mapstructure:"urls"`
}

// GatewayAnthropicConfig supplies Anthropic beta-header and user-message-queue process defaults.
type GatewayAnthropicConfig struct {
	// Add required anthropic-beta values for API-key providers when absent from
	// the client; disabled by default.
	InjectBetaForAPIKey bool                   `mapstructure:"inject_beta_for_api_key"`
	UserMessageQueue    UserMessageQueueConfig `mapstructure:"user_message_queue"` // gateway anthropic user message queue settings
}

// GatewayFailoverConfig bounds provider switching and the Anthropic Messages response-status retry policy.
type GatewayFailoverConfig struct {
	// Maximum provider switches after upstream errors.
	MaxProviderSwitches int `mapstructure:"max_provider_switches"`
	// Separate maximum provider switches for Gemini.
	MaxProviderSwitchesGemini int `mapstructure:"max_provider_switches_gemini"`
	// MessagesOn400 permits provider switching for the selected recoverable HTTP
	// 400 cases interpreted by gateway error handling. It defaults false and does
	// not classify every 400 response as retryable.
	MessagesOn400 bool `mapstructure:"messages_on_400"`
}

// GatewayGeminiConfig selects Gemini response-header diagnostics for gateway request processing.
type GatewayGeminiConfig struct {
	// Log Gemini response headers for debugging; disabled by default to limit log volume.
	DebugResponseHeaders bool `mapstructure:"debug_response_headers"`
}

// GatewayImagesConfig sets image-specific streaming intervals and concurrent-request admission limits.
type GatewayImagesConfig struct {
	Concurrency ImageConcurrencyConfig `mapstructure:"concurrency"` // gateway images concurrency settings
	// NonstreamKeepaliveIntervalSeconds supplies keepalive seconds for nonstreaming
	// image JSON responses. Zero disables that behavior, and the setting remains
	// separate from image SSE keepalive timing.
	NonstreamKeepaliveIntervalSeconds int `mapstructure:"nonstream_keepalive_interval_seconds"`
	// StreamDataIdleTimeoutSeconds supplies the image stream data-gap limit in
	// seconds. Zero disables that deadline; image generation uses this budget
	// separately from ordinary text stream timing.
	StreamDataIdleTimeoutSeconds int `mapstructure:"stream_data_idle_timeout_seconds"`
	// StreamKeepaliveIntervalSeconds supplies heartbeat seconds during image
	// response-header waits and SSE reads. Zero disables those pings, while the
	// independent image data-gap timeout remains a separate stream liveness choice.
	StreamKeepaliveIntervalSeconds int `mapstructure:"stream_keepalive_interval_seconds"`
}

// GatewayOpenAIConfig groups OpenAI transport, timeout, Responses WebSocket, Live, and Codex settings.
type GatewayOpenAIConfig struct {
	Codex CodexConfig `mapstructure:"codex"` // gateway openai codex settings
	// CompactModel supplies the global model for explicit compact requests
	// when provider mapping misses.
	CompactModel string `mapstructure:"compact_model"`
	// FirstOutputTimeoutSeconds and
	// OpenAIHighEffortFirstOutputTimeoutSeconds feed
	// OpenAIResponseOutput.FirstOutputTimeout for HTTP and WS Responses. A
	// nonpositive ordinary budget disables the deadline before the override is
	// considered; with a positive ordinary budget, high, xhigh, and max reasoning
	// requests use a positive override or retain the ordinary value.
	// ordinary first-output deadline seconds
	FirstOutputTimeoutSeconds int `mapstructure:"first_output_timeout_seconds"`
	// high-effort first-output override seconds
	HighEffortFirstOutputTimeoutSeconds int                      `mapstructure:"high_effort_first_output_timeout_seconds"`
	HTTP2                               GatewayOpenAIHTTP2Config `mapstructure:"http2"` // gateway openai http2 settings
	Live                                GatewayLiveConfig        `mapstructure:"live"`  // gateway openai live settings
	// PassthroughAllowTimeoutHeaders permits client timeout headers in
	// OpenAI passthrough mode.
	// Disabled by default so headers such as x-stainless-timeout do not
	// prematurely end streams.
	PassthroughAllowTimeoutHeaders bool                                  `mapstructure:"passthrough_allow_timeout_headers"`
	ProxyStreamCircuit             GatewayOpenAIProxyStreamCircuitConfig `mapstructure:"proxy_stream_circuit"` // gateway openai proxy stream circuit settings
	// ResponseHeaderTimeoutSeconds bounds OpenAI/Codex header waits in seconds;
	// zero disables it.
	// The default permits long upstream queue waits independently of the generic timeout.
	ResponseHeaderTimeoutSeconds int                   `mapstructure:"response_header_timeout_seconds"`
	ResponsesWS                  GatewayOpenAIWSConfig `mapstructure:"responses_ws"` // gateway openai responses ws settings
}

// GatewayStreamingConfig sets text-stream inactivity, heartbeat, and provider-slot waiting intervals.
type GatewayStreamingConfig struct {
	// DataIdleTimeoutSeconds is the allowed ordinary stream data gap in
	// seconds. Zero disables the gap deadline; positive values reach the stream
	// consumer separately from its keepalive interval.
	DataIdleTimeoutSeconds int `mapstructure:"data_idle_timeout_seconds"`
	// KeepaliveIntervalSeconds supplies downstream text SSE idle heartbeat intervals in
	// seconds. Zero disables these keepalives, while the separate data-gap timeout
	// can still bound an inactive upstream stream.
	KeepaliveIntervalSeconds int `mapstructure:"keepalive_interval_seconds"`
	// WaitPingIntervalSeconds is the SSE ping interval while waiting for concurrency, in seconds.
	WaitPingIntervalSeconds int `mapstructure:"wait_ping_interval_seconds"`
}

// GatewayTransportConfig bounds upstream connection pools, response-header waits, and cached transport clients.
type GatewayTransportConfig struct {
	ClientCache UpstreamClientCacheConfig `mapstructure:"client_cache"` // gateway transport client cache settings
	// ConnectionPoolIsolation selects proxy, provider, or provider_proxy isolation.
	ConnectionPoolIsolation string `mapstructure:"connection_pool_isolation"`
	// IdleConnTimeoutSeconds is the idle connection timeout.
	IdleConnTimeoutSeconds int `mapstructure:"idle_conn_timeout_seconds"`
	// MaxConnsPerHost bounds active and idle connections to one host when the
	// global limits apply. Zero leaves that host limit unlimited while the idle
	// limits still govern retained connections.
	MaxConnsPerHost int `mapstructure:"max_conns_per_host"`
	// MaxIdleConns, MaxIdleConnsPerHost, and MaxConnsPerHost supply global pool
	// limits. transport.Client.resolvePoolSettings replaces all three with a
	// positive provider concurrency for provider or provider_proxy isolation;
	// other pools retain these configured limits.
	// MaxIdleConns limits idle connections across all hosts.
	MaxIdleConns int `mapstructure:"max_idle_conns"`
	// MaxIdleConnsPerHost limits idle connections retained per host for reuse.
	MaxIdleConnsPerHost int `mapstructure:"max_idle_conns_per_host"`
	// ResponseHeaderTimeoutSeconds is the generic upstream header budget in seconds.
	// Zero disables it; OpenAI and Grok profile selection uses their separate
	// fields. This deadline ends when headers arrive, before streaming duration.
	ResponseHeaderTimeoutSeconds int `mapstructure:"response_header_timeout_seconds"`
}

// UpstreamErrorBodyLogConfig controls whether bounded upstream error bodies enter diagnostic logs.
type UpstreamErrorBodyLogConfig struct {
	// Log summaries of upstream error response bodies.
	Enabled bool `mapstructure:"enabled"`
	// Maximum logged upstream error-body bytes; longer bodies are truncated.
	MaxBytes int `mapstructure:"max_bytes"`
}

// OpsWebSocketConfig bounds administrator WebSocket admission and defines handshake proxy trust.
type OpsWebSocketConfig struct {
	MaxConns       int      `mapstructure:"max_conns"`        // Global websocket connection admission limit.
	MaxConnsPerIP  int      `mapstructure:"max_conns_per_ip"` // Per-address admission limit; zero disables it.
	OriginPolicy   string   `mapstructure:"origin_policy"`    // strict or permissive missing-Origin policy.
	TrustProxy     bool     `mapstructure:"trust_proxy"`      // Trust forwarded addresses from configured peers.
	TrustedProxies []string `mapstructure:"trusted_proxies"`  // Trusted peer addresses and CIDR ranges.
}

// AnthropicProviderConfig selects the Claude CLI identity used by headers and billing templates.
type AnthropicProviderConfig struct {
	ClaudeCLIVersion string `mapstructure:"claude_cli_version"` // Claude CLI identity version override.
}

// AntigravityProviderConfig supplies Antigravity identity overrides, forwarding selection, and fallback cooldown timing.
type AntigravityProviderConfig struct {
	FallbackCooldownSeconds int    `mapstructure:"fallback_cooldown_seconds"` // Fallback scheduling cooldown in seconds.
	ForwardEndpoint         string `mapstructure:"forward_endpoint"`          // auto, prod, or daily endpoint selection.
	OAuthClientSecret       string `mapstructure:"oauth_client_secret"`       // Antigravity OAuth client secret override.
	UserAgentVersion        string `mapstructure:"user_agent_version"`        // Antigravity decimal major.minor.patch identity override.
}

// ProviderCooldownConfig supplies fallback scheduling pauses after overload and OAuth authorization failures.
type ProviderCooldownConfig struct {
	OAuth401Minutes int `mapstructure:"oauth_401_minutes"` // OAuth HTTP 401 scheduling cooldown, in minutes.
	OverloadMinutes int `mapstructure:"overload_minutes"`  // HTTP 529 overload cooldown, in minutes.
}

// GrokProviderConfig supplies Grok login policy, client identity, and explicit endpoint overrides.
type GrokProviderConfig struct {
	AllowUnsafeURLOverrides bool                    `mapstructure:"allow_unsafe_url_overrides"` // Permit explicitly configured unsafe endpoint overrides.
	BaseURL                 string                  `mapstructure:"base_url"`                   // Grok endpoint override subject to URL policy.
	CLIVersion              string                  `mapstructure:"cli_version"`                // Grok CLI identity version override.
	OAuth                   ProviderGrokOAuthConfig `mapstructure:"oauth"`                      // provider grok oauth settings
	// PasswordAuthEnabled enables the optional password-to-SSO OAuth flow;
	// disabled by default.
	// POST /admin/grok/oauth/password performs authorization only when enabled.
	PasswordAuthEnabled            bool   `mapstructure:"password_auth_enabled"`
	UnsafeAllowConcurrencyAboveOne bool   `mapstructure:"unsafe_allow_concurrency_above_one"` // Permit Grok concurrency above the safe default.
	YesCaptchaClientKey            string `mapstructure:"yescaptcha_client_key"`              // YesCaptcha client authentication key.
}

// ProviderWaitConfig bounds the waiting queue and acquisition timeout for a selected provider.
type ProviderWaitConfig struct {
	// alternative-provider waiter count limit
	MaxWaiting int `mapstructure:"max_waiting"`
	// TimeoutSeconds supplies the configured scheduler interval in whole seconds.
	TimeoutSeconds int `mapstructure:"timeout_seconds"`
}

// GrokFreeQuotaConfig bounds the local rolling token budget for Free Grok providers.
type GrokFreeQuotaConfig struct {
	// Enabled gates Grok OAuth providers marked free using local
	// rolling usage.
	Enabled bool `mapstructure:"enabled"`
	// StatsCacheSeconds is the quota-statistics cache TTL; requests do
	// not wait for usage_logs.
	// A cache miss permits scheduling and triggers an asynchronous refresh.
	StatsCacheSeconds int `mapstructure:"stats_cache_seconds"`
	// ThresholdPercent is the allowance percentage that stops new scheduling.
	ThresholdPercent int `mapstructure:"threshold_percent"`
	// TokenLimit is the nominal token allowance for the rolling window.
	TokenLimit int64 `mapstructure:"token_limit"`
	// WindowHours is the local usage window length.
	WindowHours int `mapstructure:"window_hours"`
}

// SchedulingLoadBatchConfig controls batched load observations and their short-lived scheduling cache.
type SchedulingLoadBatchConfig struct {
	// CacheTTLMS supplies provider-load cache lifetime in milliseconds to
	// scheduler.ConcurrencyService. Zero disables and clears that cache; the
	// batched observation selection switch remains independently configured.
	CacheTTLMS int `mapstructure:"cache_ttl_ms"`
	// batched load observation switch
	Enabled bool `mapstructure:"enabled"`
}

// SessionAffinityConfig controls session and response affinity expiry and Redis hash-format compatibility.
type SessionAffinityConfig struct {
	// legacy session affinity write switch
	DualWriteLegacyHash bool `mapstructure:"dual_write_legacy_hash"`
	// legacy session affinity read switch
	ReadLegacyHash bool `mapstructure:"read_legacy_hash"`
	// response-ID affinity retention seconds
	ResponseIDTTLSeconds int `mapstructure:"response_id_ttl_seconds"`
	// session affinity retention seconds
	TTLSeconds int `mapstructure:"ttl_seconds"`
}

// SchedulingSnapshotConfig bounds cached scheduling reads, database fallback, and durable outbox rebuilding.
type SchedulingSnapshotConfig struct {
	// DbFallbackEnabled permits scheduler storage reads when cached scheduling
	// data cannot supply trusted candidates. The timeout and per-process QPS
	// fields then constrain those permitted database reads.
	DbFallbackEnabled bool `mapstructure:"db_fallback_enabled"`
	// DbFallbackMaxQPS supplies the per-process scheduler database-read rate
	// limit. Zero leaves it unlimited, while DbFallbackEnabled still determines
	// whether cache-miss database reads can run.
	DbFallbackMaxQPS int `mapstructure:"db_fallback_max_qps"`
	// DbFallbackTimeoutSeconds adds a timeout to permitted scheduler database
	// reads when positive. Zero leaves the caller context budget in control, and
	// values are supplied in seconds.
	DbFallbackTimeoutSeconds int `mapstructure:"db_fallback_timeout_seconds"`
	// FullRebuildIntervalSeconds supplies periodic complete snapshot refresh
	// timing in seconds. Zero disables that loop, while outbox-triggered rebuild
	// decisions retain their own configured thresholds.
	FullRebuildIntervalSeconds int `mapstructure:"full_rebuild_interval_seconds"`
	// MGET chunk size when reading snapshot buckets.
	MGetChunkSize int `mapstructure:"mget_chunk_size"`
	// OutboxBacklogRebuildRows supplies the maximum-ID minus watermark threshold
	// used by scheduler.SnapshotService. Zero disables this trigger; gaps in event
	// IDs can make this ID distance exceed actual pending rows.
	OutboxBacklogRebuildRows int `mapstructure:"outbox_backlog_rebuild_rows"`
	// eligible excessive-age observation count
	OutboxLagRebuildFailures int `mapstructure:"outbox_lag_rebuild_failures"`
	// outbox age rebuild threshold seconds
	OutboxLagRebuildSeconds int `mapstructure:"outbox_lag_rebuild_seconds"`
	// OutboxLagWarnSeconds enables age warnings when positive. Positive
	// OutboxLagRebuildSeconds enables the separate age trigger, and
	// OutboxLagRebuildFailures counts eligible degraded observations before a
	// rebuild. SnapshotService pauses that count while rebuilds or their lag
	// retries are active; zero age thresholds disable their respective triggers.
	// outbox age warning threshold seconds
	OutboxLagWarnSeconds int `mapstructure:"outbox_lag_warn_seconds"`
	// Outbox polling and rebuild thresholds.
	// Outbox polling interval, in seconds.
	OutboxPollIntervalSeconds int `mapstructure:"outbox_poll_interval_seconds"`
	// Cache write chunk size when rebuilding snapshots.
	WriteChunkSize int `mapstructure:"write_chunk_size"`
}

// ServerClientIPConfig combines startup forwarding policy, explicit proxy presence, and live policy publication.
type ServerClientIPConfig struct {
	// startup client-address header names
	ForwardedHeaders []string `mapstructure:"forwarded_headers" json:"forwarded_headers" yaml:"forwarded_headers"`
	// TrustForwardedHeaders lets raw forwarding headers determine client IPs.
	// When disabled, client IP consumers rely on server.client_ip.trusted_proxies.
	TrustForwardedHeaders bool `mapstructure:"trust_forwarded_headers"`
	// TrustedProxies is the ordered startup list of proxy IPs and CIDRs.
	// TrustedProxiesConfigured distinguishes omission from supplied emptiness,
	// while server/clientip interprets that presence when selecting address
	// handling.
	TrustedProxies []string `mapstructure:"trusted_proxies"`
	// TrustedProxiesConfigured records file presence independently of list length.
	// An explicit empty list therefore remains a configured client-IP policy.
	TrustedProxiesConfigured bool `mapstructure:"-" json:"-" yaml:"-"`
	// forwardedClientIPSettingsLive owns immutable copied policy snapshots. Initialize
	// this cell before concurrent access; subsequent stores publish the entire pair.
	forwardedClientIPSettingsLive *atomic.Pointer[ForwardedClientIPSettings] `mapstructure:"-" json:"-" yaml:"-"`
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

// APIKeyConfig groups key prefixes, lookup admission, authentication caches, and invalid-key abuse limits.
type APIKeyConfig struct {
	AuthCache       APIKeyAuthCacheConfig  `mapstructure:"auth_cache"`        // api key auth cache settings
	InvalidKeyAbuse InvalidAuthAbuseConfig `mapstructure:"invalid_key_abuse"` // api key invalid key abuse settings
	// authentication lookup admission limit
	LookupConcurrency int `mapstructure:"lookup_concurrency"`
	// generated API-key text prefix
	Prefix string `mapstructure:"prefix"`
}

// AuthConfig groups token, passkey, captcha, and OAuth process inputs for identity construction.
type AuthConfig struct {
	JWT            JWTConfig            `mapstructure:"jwt"`             // auth jwt settings
	OAuth          OAuthProvidersConfig `mapstructure:"oauth"`           // auth oauth settings
	RequireCaptcha bool                 `mapstructure:"require_captcha"` // default mandatory captcha verification flag
	WebAuthn       WebAuthnConfig       `mapstructure:"webauthn"`        // auth auth.webauthn settings
}

// DebugConfig selects optional routing, request-body, and Claude identity diagnostics.
type DebugConfig struct {
	ClaudeMimicLog     bool   `mapstructure:"claude_mimic_log"`      // Write Claude mimic request diagnostics.
	GatewayBodyLogFile string `mapstructure:"gateway_body_log_file"` // Gateway request body diagnostic file path.
	ModelRoutingLog    bool   `mapstructure:"model_routing_log"`     // Write detailed model routing diagnostics.
}

// EgressConfig groups proxy-exit probes and the optional TLS fingerprint collector listener.
type EgressConfig struct {
	ProxyProbe              EgressProxyProbeConfig        `mapstructure:"proxy_probe"`               // egress proxy probe settings
	TLSFingerprintCollector TLSFingerprintCollectorConfig `mapstructure:"tls_fingerprint_collector"` // egress tls fingerprint collector settings
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

// OutboundProxyConfig controls auxiliary downloads and their permitted behavior after proxy initialization fails.
type OutboundProxyConfig struct {
	// AllowDirectOnError permits direct connections after auxiliary-service proxy
	// initialization fails.
	// It applies to these auxiliary services:
	//   - GitHub Release update checks
	//   - Pricing data downloads
	// AI gateway connections, including Claude, OpenAI, Gemini, and Antigravity,
	// return proxy errors rather than connecting directly.
	// The default is false to prevent proxy misconfiguration from exposing the server IP.
	AllowDirectOnError bool `mapstructure:"allow_direct_on_error"`
	// URL is the proxy address for GitHub access.
	// Supported schemes are http, https, socks5, and socks5h.
	// Examples: "http://127.0.0.1:7890", "socks5://127.0.0.1:1080".
	URL string `mapstructure:"url"`
}

// PaymentConfig supplies the explicit signing input for payment-resume token construction.
type PaymentConfig struct {
	ResumeSigningKey string `mapstructure:"resume_signing_key"` // Explicit payment resume signing key.
}

// ProviderConfig groups upstream identity, token refresh, cooldown, and usage-monitor settings.
type ProviderConfig struct {
	Anthropic    AnthropicProviderConfig   `mapstructure:"anthropic"`     // provider anthropic settings
	Antigravity  AntigravityProviderConfig `mapstructure:"antigravity"`   // provider antigravity settings
	Cooldown     ProviderCooldownConfig    `mapstructure:"cooldown"`      // provider cooldown settings
	Gemini       GeminiConfig              `mapstructure:"gemini"`        // Gemini OAuth and quota settings
	Grok         GrokProviderConfig        `mapstructure:"grok"`          // provider grok settings
	TokenRefresh TokenRefreshConfig        `mapstructure:"token_refresh"` // provider token refresh settings
	UsageMonitor GatewayUsageMonitorConfig `mapstructure:"usage_monitor"` // provider usage monitor settings
}

// SchedulingConfig groups provider waiting, session affinity, snapshot maintenance, and advanced scoring inputs.
type SchedulingConfig struct {
	Advanced              GatewayAdvancedSchedulerConfig `mapstructure:"advanced"`                // scheduling advanced settings
	AlternateProviderWait ProviderWaitConfig             `mapstructure:"alternate_provider_wait"` // scheduling alternate provider wait settings
	// Selection mode: "last_used" orders by last use by default; "random"
	// randomizes selection.
	AlternateSelectionMode string `mapstructure:"alternate_selection_mode"`
	// ConcurrencySlotTTLMinutes is the concurrency slot lifetime.
	// Set it longer than the longest LLM request so active slots do not expire.
	ConcurrencySlotTTLMinutes int `mapstructure:"concurrency_slot_ttl_minutes"`
	// DefaultSessionIdleTimeoutMinutes is the session idle timeout; the default is five minutes.
	// It applies to session-count limits for Anthropic OAuth and SetupToken providers.
	// Sessions idle past the timeout are released.
	DefaultSessionIdleTimeoutMinutes int                       `mapstructure:"default_session_idle_timeout_minutes"`
	GrokFreeQuota                    GrokFreeQuotaConfig       `mapstructure:"grok_free_quota"` // scheduling grok free quota settings
	LoadBatch                        SchedulingLoadBatchConfig `mapstructure:"load_batch"`      // scheduling load batch settings
	// PreferSoonestReset makes load-aware selection favor providers whose session
	// windows reset soonest.
	// This consumes allowances near reset before those with more time remaining.
	// Disabled by default, using priority, load ratio, and LRU ordering.
	PreferSoonestReset bool                  `mapstructure:"prefer_soonest_reset"`
	SessionAffinity    SessionAffinityConfig `mapstructure:"session_affinity"` // scheduling session affinity settings
	// SlotCleanupIntervalSeconds supplies the configured scheduler interval in whole seconds.
	SlotCleanupIntervalSeconds int                      `mapstructure:"slot_cleanup_interval_seconds"`
	Snapshot                   SchedulingSnapshotConfig `mapstructure:"snapshot"`             // scheduling snapshot settings
	StickyProviderWait         ProviderWaitConfig       `mapstructure:"sticky_provider_wait"` // scheduling sticky provider wait settings
}

// SiteConfig supplies the public-site URL fallback used by application link builders.
type SiteConfig struct {
	FrontendURL string `mapstructure:"frontend_url"` // Default frontend address for email links and MCP OAuth.
}

// StorageConfig supplies the application data directory used by catalog and plugin fallbacks.
type StorageConfig struct {
	Dir string `mapstructure:"dir"` // Local data directory.
}

// UsageConfig groups request-record queues, dashboard caches, aggregation, retention, and cleanup tasks.
type UsageConfig struct {
	Aggregation    UsageAggregationConfig   `mapstructure:"aggregation"`     // usage aggregation settings
	CleanupTasks   UsageCleanupConfig       `mapstructure:"cleanup_tasks"`   // usage cleanup tasks settings
	DashboardCache DashboardCacheConfig     `mapstructure:"dashboard_cache"` // usage dashboard cache settings
	RecordQueue    GatewayUsageRecordConfig `mapstructure:"record_queue"`    // usage record queue settings
	Retention      UsageRetentionConfig     `mapstructure:"retention"`       // usage retention settings
}

// UserDefaultsConfig supplies fallback concurrency and balance for administrator-managed user grants.
type UserDefaultsConfig struct {
	// new-user initial balance amount
	Balance float64 `mapstructure:"balance"`
	// new-user concurrent request limit
	Concurrency int `mapstructure:"concurrency"`
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
	live := c.Server.ClientIP.forwardedClientIPSettingsLive
	if live != nil {
		if snapshot := live.Load(); snapshot != nil {
			return ForwardedClientIPSettings{
				TrustForwardedIP: snapshot.TrustForwardedIP,
				Headers:          cloneForwardedClientIPHeaders(snapshot.Headers),
			}
		}
	}
	return ForwardedClientIPSettings{
		TrustForwardedIP: c.Server.ClientIP.TrustForwardedHeaders,
		Headers:          cloneForwardedClientIPHeaders(c.Server.ClientIP.ForwardedHeaders),
	}
}

// TrustForwardedHeaders returns the forwarding trust flag from the
// current copied policy snapshot. Nil configuration uses false, while an
// initialized live snapshot takes precedence over the original startup flag.
func (c *Config) TrustForwardedHeaders() bool {
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
	if c.Server.ClientIP.forwardedClientIPSettingsLive == nil {
		c.Server.ClientIP.forwardedClientIPSettingsLive = &atomic.Pointer[ForwardedClientIPSettings]{}
	}
	// Publish trust and header names as one copied object so a request cannot
	// combine values from separate policy updates. The startup fields stay
	// available for validation and prepublication lookup.
	c.Server.ClientIP.forwardedClientIPSettingsLive.Store(&ForwardedClientIPSettings{
		TrustForwardedIP: enabled,
		Headers:          headers,
	})
}

// SetTrustForwardedHeaders republishes enabled with the headers read
// from the current policy, accepting nil configuration as a no-op. It delegates
// copied whole-policy storage to SetForwardedClientIPSettings.
//
// Reading the headers and storing the replacement are separate operations. A
// publisher can install a new header list between them, after which this method
// can replace that list with its earlier observation. Callers serialize coupled
// policy edits when retaining the latest headers matters and initialize live
// storage before concurrent use.
func (c *Config) SetTrustForwardedHeaders(enabled bool) {
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

// load resolves the process configuration path and delegates versioned loading.
// Path failures stop startup before installation or application construction can
// consume ambiguous input. Bootstrap callers may defer only the JWT secret.
// @project-doc docs/interfaces/configuration.md#configuration_sources
func load(allowMissingJWTSecret bool) (*Config, error) {
	path, exists, err := ResolveConfigFile()
	if err != nil {
		return nil, err
	}
	return loadFrom(path, exists, processUpgradeEnvironment(), allowMissingJWTSecret)
}

// loadFrom upgrades and validates one explicitly selected configuration file.
// The supplied environment is consulted only for version 1 imports and explicit
// references. Defaults use a local Viper instance, so independent loads do not
// share overrides or bindings. Bootstrap loading can defer JWT secret selection
// until persistent storage is available.
//
// Unknown keys fail before interpolation, and interpolation uses a copied YAML
// graph so disk references remain intact. Presence flags derive from the file
// itself, including explicitly empty lists and false OAuth choices. Validation
// completes before ignored-environment diagnostics describe residual deployment
// variables, and secret values never enter those diagnostics.
func loadFrom(path string, exists bool, env upgradeEnvironment, allowMissingJWTSecret bool) (*Config, error) {
	env = completeUpgradeEnvironment(env)
	doc, err := upgradeConfigFile(path, exists, env)
	if err != nil {
		return nil, err
	}
	if err = checkUnknownKeys(doc); err != nil {
		return nil, err
	}
	decoded, err := interpolateEnvReferences(doc, env.LookupEnv)
	if err != nil {
		return nil, err
	}
	v := viper.New()
	v.SetConfigType("yaml")
	setDefaults(v)
	if decoded != nil {
		raw, err := encodeConfigDocument(decoded)
		if err != nil {
			return nil, err
		}
		if err = v.ReadConfig(bytes.NewReader(raw)); err != nil {
			return nil, fmt.Errorf("read configuration %s", path)
		}
	}
	var cfg Config
	if err = v.Unmarshal(&cfg); err != nil {
		return nil, fmt.Errorf("configuration %s contains an invalid value type", path)
	}
	cfg.Server.ClientIP.TrustedProxiesConfigured = v.InConfig("server.client_ip.trusted_proxies")
	cfg.Server.Mode = strings.ToLower(strings.TrimSpace(cfg.Server.Mode))
	cfg.Site.FrontendURL = strings.TrimSpace(cfg.Site.FrontendURL)
	cfg.Plugins.normalize(cfg.Storage.Dir)
	cfg.Auth.JWT.Secret = strings.TrimSpace(cfg.Auth.JWT.Secret)
	cfg.Auth.OAuth.LinuxDo.ClientID = strings.TrimSpace(cfg.Auth.OAuth.LinuxDo.ClientID)
	cfg.Auth.OAuth.LinuxDo.ClientSecret = strings.TrimSpace(cfg.Auth.OAuth.LinuxDo.ClientSecret)
	cfg.Auth.OAuth.LinuxDo.AuthorizeURL = strings.TrimSpace(cfg.Auth.OAuth.LinuxDo.AuthorizeURL)
	cfg.Auth.OAuth.LinuxDo.TokenURL = strings.TrimSpace(cfg.Auth.OAuth.LinuxDo.TokenURL)
	cfg.Auth.OAuth.LinuxDo.UserInfoURL = strings.TrimSpace(cfg.Auth.OAuth.LinuxDo.UserInfoURL)
	cfg.Auth.OAuth.LinuxDo.Scopes = strings.TrimSpace(cfg.Auth.OAuth.LinuxDo.Scopes)
	cfg.Auth.OAuth.LinuxDo.RedirectURL = strings.TrimSpace(cfg.Auth.OAuth.LinuxDo.RedirectURL)
	cfg.Auth.OAuth.LinuxDo.FrontendRedirectURL = strings.TrimSpace(cfg.Auth.OAuth.LinuxDo.FrontendRedirectURL)
	cfg.Auth.OAuth.LinuxDo.TokenAuthMethod = strings.ToLower(strings.TrimSpace(cfg.Auth.OAuth.LinuxDo.TokenAuthMethod))
	cfg.Auth.OAuth.LinuxDo.UserInfoEmailPath = strings.TrimSpace(cfg.Auth.OAuth.LinuxDo.UserInfoEmailPath)
	cfg.Auth.OAuth.LinuxDo.UserInfoIDPath = strings.TrimSpace(cfg.Auth.OAuth.LinuxDo.UserInfoIDPath)
	cfg.Auth.OAuth.LinuxDo.UserInfoUsernamePath = strings.TrimSpace(cfg.Auth.OAuth.LinuxDo.UserInfoUsernamePath)
	normalizeWeChatOAuthConfig(&cfg.Auth.OAuth.WeChat)
	cfg.Auth.OAuth.OIDC.ProviderName = strings.TrimSpace(cfg.Auth.OAuth.OIDC.ProviderName)
	cfg.Auth.OAuth.OIDC.ClientID = strings.TrimSpace(cfg.Auth.OAuth.OIDC.ClientID)
	cfg.Auth.OAuth.OIDC.ClientSecret = strings.TrimSpace(cfg.Auth.OAuth.OIDC.ClientSecret)
	cfg.Auth.OAuth.OIDC.IssuerURL = strings.TrimSpace(cfg.Auth.OAuth.OIDC.IssuerURL)
	cfg.Auth.OAuth.OIDC.DiscoveryURL = strings.TrimSpace(cfg.Auth.OAuth.OIDC.DiscoveryURL)
	cfg.Auth.OAuth.OIDC.AuthorizeURL = strings.TrimSpace(cfg.Auth.OAuth.OIDC.AuthorizeURL)
	cfg.Auth.OAuth.OIDC.TokenURL = strings.TrimSpace(cfg.Auth.OAuth.OIDC.TokenURL)
	cfg.Auth.OAuth.OIDC.UserInfoURL = strings.TrimSpace(cfg.Auth.OAuth.OIDC.UserInfoURL)
	cfg.Auth.OAuth.OIDC.JWKSURL = strings.TrimSpace(cfg.Auth.OAuth.OIDC.JWKSURL)
	cfg.Auth.OAuth.OIDC.Scopes = strings.TrimSpace(cfg.Auth.OAuth.OIDC.Scopes)
	cfg.Auth.OAuth.OIDC.RedirectURL = strings.TrimSpace(cfg.Auth.OAuth.OIDC.RedirectURL)
	cfg.Auth.OAuth.OIDC.FrontendRedirectURL = strings.TrimSpace(cfg.Auth.OAuth.OIDC.FrontendRedirectURL)
	cfg.Auth.OAuth.OIDC.TokenAuthMethod = strings.ToLower(strings.TrimSpace(cfg.Auth.OAuth.OIDC.TokenAuthMethod))
	cfg.Auth.OAuth.OIDC.AllowedSigningAlgs = strings.TrimSpace(cfg.Auth.OAuth.OIDC.AllowedSigningAlgs)
	cfg.Auth.OAuth.OIDC.UserInfoEmailPath = strings.TrimSpace(cfg.Auth.OAuth.OIDC.UserInfoEmailPath)
	cfg.Auth.OAuth.OIDC.UserInfoIDPath = strings.TrimSpace(cfg.Auth.OAuth.OIDC.UserInfoIDPath)
	cfg.Auth.OAuth.OIDC.UserInfoUsernamePath = strings.TrimSpace(cfg.Auth.OAuth.OIDC.UserInfoUsernamePath)
	cfg.Auth.OAuth.OIDC.UsePKCEExplicit = v.InConfig("auth.oauth.oidc.use_pkce")
	cfg.Auth.OAuth.OIDC.ValidateIDTokenExplicit = v.InConfig("auth.oauth.oidc.validate_id_token")
	cfg.Usage.DashboardCache.KeyPrefix = strings.TrimSpace(cfg.Usage.DashboardCache.KeyPrefix)
	cfg.Security.CORS.AllowedOrigins = normalizeStringSlice(cfg.Security.CORS.AllowedOrigins)
	cfg.Gateway.ResponseHeaders.AdditionalAllowed = normalizeStringSlice(cfg.Gateway.ResponseHeaders.AdditionalAllowed)
	cfg.Gateway.ResponseHeaders.ForceRemove = normalizeStringSlice(cfg.Gateway.ResponseHeaders.ForceRemove)
	cfg.Security.CSP.Policy = strings.TrimSpace(cfg.Security.CSP.Policy)
	forwardedClientIPHeaders, err := NormalizeForwardedClientIPHeaders(cfg.Server.ClientIP.ForwardedHeaders)
	if err != nil {
		return nil, fmt.Errorf("server.client_ip.forwarded_headers: %w", err)
	}
	cfg.Server.ClientIP.ForwardedHeaders = forwardedClientIPHeaders
	cfg.SetForwardedClientIPSettings(cfg.Server.ClientIP.TrustForwardedHeaders, forwardedClientIPHeaders)
	cfg.Log.Level = strings.ToLower(strings.TrimSpace(cfg.Log.Level))
	cfg.Log.Format = strings.ToLower(strings.TrimSpace(cfg.Log.Format))
	cfg.Log.ServiceName = strings.TrimSpace(cfg.Log.ServiceName)
	cfg.Environment = strings.TrimSpace(cfg.Environment)
	cfg.Log.StacktraceLevel = strings.ToLower(strings.TrimSpace(cfg.Log.StacktraceLevel))
	cfg.Log.Output.FilePath = strings.TrimSpace(cfg.Log.Output.FilePath)
	cfg.Gateway.OpenAI.Codex.ForcedInstructionsTemplateFile = strings.TrimSpace(cfg.Gateway.OpenAI.Codex.ForcedInstructionsTemplateFile)
	if cfg.Gateway.OpenAI.Codex.ForcedInstructionsTemplateFile != "" {
		content, err := os.ReadFile(cfg.Gateway.OpenAI.Codex.ForcedInstructionsTemplateFile)
		if err != nil {
			return nil, fmt.Errorf("read forced codex instructions template %q: %w", cfg.Gateway.OpenAI.Codex.ForcedInstructionsTemplateFile, err)
		}
		cfg.Gateway.OpenAI.Codex.ForcedInstructionsTemplate = string(content)
	}

	// Generate thirty-two random bytes as hexadecimal text when the trimmed TOTP
	// key is absent. Mark that key unconfigured so deployment-dependent features
	// can distinguish ephemeral generation from supplied encryption input.
	cfg.Security.SecretEncryptionKey = strings.TrimSpace(cfg.Security.SecretEncryptionKey)
	if cfg.Security.SecretEncryptionKey == "" {
		key, err := generateJWTSecret(32)
		if err != nil {
			return nil, fmt.Errorf("generate secret encryption key: %w", err)
		}
		cfg.Security.SecretEncryptionKey = key
		cfg.Security.SecretEncryptionKeyConfigured = false
		slog.Warn("security.secret_encryption_key is empty; generated an ephemeral key that cannot enable persistent credential storage.")
	} else {
		cfg.Security.SecretEncryptionKeyConfigured = true
	}

	originalJWTSecret := cfg.Auth.JWT.Secret
	if allowMissingJWTSecret && originalJWTSecret == "" {
		// Supply temporary length-valid text for the missing JWT input so other
		// startup fields can undergo full Validate. Restore absence after success,
		// leaving persistent secret selection to database bootstrap.
		cfg.Auth.JWT.Secret = strings.Repeat("0", 32)
	}

	if err := cfg.Validate(); err != nil {
		return nil, fmt.Errorf("validate config error: %w", err)
	}

	if allowMissingJWTSecret && originalJWTSecret == "" {
		cfg.Auth.JWT.Secret = ""
	}

	if !cfg.Security.URLAllowlist.Enabled {
		slog.Warn("security.url_allowlist.enabled=false; allowlist/SSRF checks disabled (minimal format validation only).")
	}
	if !cfg.Gateway.ResponseHeaders.Enabled {
		slog.Warn("gateway.upstream_response_headers.enabled=false; configurable header filtering disabled (default allowlist only).")
	}

	if cfg.Auth.JWT.Secret != "" && isWeakJWTSecret(cfg.Auth.JWT.Secret) {
		slog.Warn("JWT secret appears weak; use a 32+ character random secret in production.")
	}
	if len(cfg.Gateway.ResponseHeaders.AdditionalAllowed) > 0 || len(cfg.Gateway.ResponseHeaders.ForceRemove) > 0 {
		slog.Info("response header policy configured",
			"additional_allowed", cfg.Gateway.ResponseHeaders.AdditionalAllowed,
			"force_remove", cfg.Gateway.ResponseHeaders.ForceRemove,
		)
	}

	warnIgnoredEnvironment(&cfg, doc, path, env)
	return &cfg, nil
}

// setDefaults registers current process defaults on the caller's Viper instance.
// Call before decoding the selected YAML document. Defaults fill omitted values
// without enabling implicit environment lookup or marking file-presence flags.
func setDefaults(v *viper.Viper) {
	v.SetDefault("config_version", CurrentConfigVersion)
	v.SetDefault("auth.jwt.access_token_ttl_minutes", 1440)
	v.SetDefault("gateway.openai.proxy_stream_circuit.enabled", true)
	v.SetDefault("provider.antigravity.fallback_cooldown_seconds", 60)
	v.SetDefault("provider.antigravity.forward_endpoint", "auto")
	v.SetDefault("ops.websocket.trust_proxy", true)
	v.SetDefault("ops.websocket.trusted_proxies", []string{"127.0.0.0/8", "::1/128"})
	v.SetDefault("ops.websocket.origin_policy", "permissive")
	v.SetDefault("ops.websocket.max_conns", 100)
	v.SetDefault("ops.websocket.max_conns_per_ip", 20)

	setPluginDefaults(v)
	// Server
	v.SetDefault("server.host", "0.0.0.0")
	v.SetDefault("server.port", 8080)
	v.SetDefault("server.mode", "release")
	v.SetDefault("server.enable_server_timing", false)
	v.SetDefault("site.frontend_url", "")
	v.SetDefault("server.read_header_timeout_seconds", 10) // 10-second header read timeout.
	v.SetDefault("server.max_header_bytes", 64*1024)
	v.SetDefault("server.idle_timeout_seconds", 120) // 120-second idle timeout.
	v.SetDefault("server.max_request_body_bytes", int64(256*1024*1024))
	// H2C defaults.
	v.SetDefault("server.h2c.enabled", false)
	v.SetDefault("server.h2c.max_concurrent_streams", uint32(50))            // 50 concurrent streams.
	v.SetDefault("server.h2c.idle_timeout_seconds", 75)                      // 75 seconds.
	v.SetDefault("server.h2c.max_read_frame_bytes", 1<<20)                   // 1 MiB.
	v.SetDefault("server.h2c.max_upload_buffer_per_connection_bytes", 2<<20) // 2 MiB
	v.SetDefault("server.h2c.max_upload_buffer_per_stream_bytes", 512<<10)   // 512 KiB
	// The collector starts from the UI; these values supply its listen settings.
	v.SetDefault("egress.tls_fingerprint_collector.host", "0.0.0.0")
	v.SetDefault("egress.tls_fingerprint_collector.port", 8443)
	v.SetDefault("egress.tls_fingerprint_collector.public_base_url", "")
	v.SetDefault("egress.tls_fingerprint_collector.cert_file", "")
	v.SetDefault("egress.tls_fingerprint_collector.key_file", "")
	v.SetDefault("egress.tls_fingerprint_collector.session_ttl_seconds", 1800)
	v.SetDefault("egress.tls_fingerprint_collector.max_records_per_session", 20)

	// Log
	v.SetDefault("log.level", "info")
	v.SetDefault("log.format", "console")
	v.SetDefault("log.service_name", "tokenrouter")
	v.SetDefault("environment", "production")
	v.SetDefault("log.caller", true)
	v.SetDefault("log.stacktrace_level", "error")
	v.SetDefault("log.output.to_stdout", true)
	v.SetDefault("log.output.to_file", true)
	v.SetDefault("log.output.file_path", "")
	v.SetDefault("log.rotation.max_size_mb", 100)
	v.SetDefault("log.rotation.max_backups", 10)
	v.SetDefault("log.rotation.max_age_days", 7)
	v.SetDefault("log.rotation.compress", true)
	v.SetDefault("log.rotation.local_time", true)
	v.SetDefault("log.sampling.enabled", false)
	v.SetDefault("log.sampling.initial", 100)
	v.SetDefault("log.sampling.thereafter", 100)

	// CORS
	v.SetDefault("security.cors.allowed_origins", []string{})
	v.SetDefault("security.cors.allow_credentials", true)

	// Passkeys are disabled by default; enabling requires a relying-party domain
	// and trusted browser origins.
	v.SetDefault("auth.webauthn.enabled", false)
	v.SetDefault("auth.webauthn.rp_display_name", "TokenRouter")
	v.SetDefault("auth.webauthn.rp_id", "")
	v.SetDefault("auth.webauthn.rp_origins", []string{})

	// Security
	v.SetDefault("security.url_allowlist.enabled", false)
	v.SetDefault("security.url_allowlist.upstream_hosts", []string{
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
	v.SetDefault("security.url_allowlist.model_catalog_hosts", []string{
		"models.dev",
		"raw.githubusercontent.com",
	})
	v.SetDefault("security.url_allowlist.crs_sync_hosts", []string{})
	v.SetDefault("security.url_allowlist.allow_private_hosts", true)
	v.SetDefault("security.url_allowlist.allow_insecure_http", true)
	v.SetDefault("gateway.upstream_response_headers.enabled", true)
	v.SetDefault("gateway.upstream_response_headers.additional_allowed", []string{})
	v.SetDefault("gateway.upstream_response_headers.force_remove", []string{})
	v.SetDefault("security.csp.enabled", true)
	v.SetDefault("security.csp.policy", DefaultCSPPolicy)
	v.SetDefault("server.client_ip.trust_forwarded_headers", true)

	// Security - disable direct fallback on proxy error
	v.SetDefault("outbound_proxy.allow_direct_on_error", false)

	// Billing
	v.SetDefault("billing.balance_check_circuit_breaker.enabled", true)
	v.SetDefault("billing.balance_check_circuit_breaker.failure_threshold", 5)
	v.SetDefault("billing.balance_check_circuit_breaker.reset_timeout_seconds", 30)
	v.SetDefault("billing.balance_check_circuit_breaker.half_open_requests", 3)
	v.SetDefault("billing.minimum_balance_reserve", 0.000001)

	// Turnstile
	v.SetDefault("auth.require_captcha", false)

	// LinuxDo Connect OAuth login.
	v.SetDefault("auth.oauth.linuxdo.enabled", false)
	v.SetDefault("auth.oauth.linuxdo.client_id", "")
	v.SetDefault("auth.oauth.linuxdo.client_secret", "")
	v.SetDefault("auth.oauth.linuxdo.authorize_url", "https://connect.linux.do/oauth2/authorize")
	v.SetDefault("auth.oauth.linuxdo.token_url", "https://connect.linux.do/oauth2/token")
	v.SetDefault("auth.oauth.linuxdo.userinfo_url", "https://connect.linux.do/api/user")
	v.SetDefault("auth.oauth.linuxdo.scopes", "user")
	v.SetDefault("auth.oauth.linuxdo.redirect_url", "")
	v.SetDefault("auth.oauth.linuxdo.frontend_redirect_url", "/auth/linuxdo/callback")
	v.SetDefault("auth.oauth.linuxdo.token_auth_method", "client_secret_post")
	v.SetDefault("auth.oauth.linuxdo.use_pkce", false)
	v.SetDefault("auth.oauth.linuxdo.userinfo_email_path", "")
	v.SetDefault("auth.oauth.linuxdo.userinfo_id_path", "")
	v.SetDefault("auth.oauth.linuxdo.userinfo_username_path", "")

	// WeChat Connect OAuth login.
	v.SetDefault("auth.oauth.wechat.enabled", false)
	v.SetDefault("auth.oauth.wechat.open_app_id", "")
	v.SetDefault("auth.oauth.wechat.open_app_secret", "")
	v.SetDefault("auth.oauth.wechat.mp_app_id", "")
	v.SetDefault("auth.oauth.wechat.mp_app_secret", "")
	v.SetDefault("auth.oauth.wechat.mobile_app_id", "")
	v.SetDefault("auth.oauth.wechat.mobile_app_secret", "")
	v.SetDefault("auth.oauth.wechat.open_enabled", false)
	v.SetDefault("auth.oauth.wechat.mp_enabled", false)
	v.SetDefault("auth.oauth.wechat.mobile_enabled", false)
	v.SetDefault("auth.oauth.wechat.mode", defaultWeChatConnectMode)
	v.SetDefault("auth.oauth.wechat.scopes", defaultWeChatConnectScopes)
	v.SetDefault("auth.oauth.wechat.redirect_url", "")
	v.SetDefault("auth.oauth.wechat.frontend_redirect_url", defaultWeChatConnectFrontendRedirect)

	// Generic OIDC login.
	v.SetDefault("auth.oauth.oidc.enabled", false)
	v.SetDefault("auth.oauth.oidc.provider_name", "OIDC")
	v.SetDefault("auth.oauth.oidc.client_id", "")
	v.SetDefault("auth.oauth.oidc.client_secret", "")
	v.SetDefault("auth.oauth.oidc.issuer_url", "")
	v.SetDefault("auth.oauth.oidc.discovery_url", "")
	v.SetDefault("auth.oauth.oidc.authorize_url", "")
	v.SetDefault("auth.oauth.oidc.token_url", "")
	v.SetDefault("auth.oauth.oidc.userinfo_url", "")
	v.SetDefault("auth.oauth.oidc.jwks_url", "")
	v.SetDefault("auth.oauth.oidc.scopes", "openid email profile")
	v.SetDefault("auth.oauth.oidc.redirect_url", "")
	v.SetDefault("auth.oauth.oidc.frontend_redirect_url", "/auth/oidc/callback")
	v.SetDefault("auth.oauth.oidc.token_auth_method", "client_secret_post")
	v.SetDefault("auth.oauth.oidc.use_pkce", true)
	v.SetDefault("auth.oauth.oidc.validate_id_token", true)
	v.SetDefault("auth.oauth.oidc.allowed_signing_algs", "RS256,ES256,PS256")
	v.SetDefault("auth.oauth.oidc.clock_skew_seconds", 120)
	v.SetDefault("auth.oauth.oidc.require_email_verified", false)
	v.SetDefault("auth.oauth.oidc.userinfo_email_path", "")
	v.SetDefault("auth.oauth.oidc.userinfo_id_path", "")
	v.SetDefault("auth.oauth.oidc.userinfo_username_path", "")

	// DingTalk Connect OAuth login.
	v.SetDefault("auth.oauth.dingtalk.enabled", false)
	v.SetDefault("auth.oauth.dingtalk.authorize_url", "https://login.dingtalk.com/oauth2/auth")
	v.SetDefault("auth.oauth.dingtalk.token_url", "https://api.dingtalk.com/v1.0/oauth2/userAccessToken")
	v.SetDefault("auth.oauth.dingtalk.userinfo_url", "https://api.dingtalk.com/v1.0/contact/users/me")
	v.SetDefault("auth.oauth.dingtalk.scopes", "openid")
	v.SetDefault("auth.oauth.dingtalk.frontend_redirect_url", "/auth/dingtalk/callback")
	v.SetDefault("auth.oauth.dingtalk.dingtalk_app_kind", "internal_app")
	v.SetDefault("auth.oauth.dingtalk.app_type", "public")
	v.SetDefault("auth.oauth.dingtalk.corp_restriction_policy", "none")
	v.SetDefault("auth.oauth.dingtalk.require_email", true)

	// Database
	v.SetDefault("database.host", "localhost")
	v.SetDefault("database.port", 5432)
	v.SetDefault("database.user", "postgres")
	v.SetDefault("database.password", "postgres")
	v.SetDefault("database.dbname", "tokenrouter")
	v.SetDefault("database.sslmode", "prefer")
	v.SetDefault("database.max_open_conns", 256)
	v.SetDefault("database.max_idle_conns", 128)
	v.SetDefault("database.conn_max_lifetime_minutes", 30)
	v.SetDefault("database.conn_max_idle_time_minutes", 5)

	// Redis
	v.SetDefault("redis.host", "localhost")
	v.SetDefault("redis.port", 6379)
	v.SetDefault("redis.username", "")
	v.SetDefault("redis.password", "")
	v.SetDefault("redis.db", 0)
	v.SetDefault("redis.dial_timeout_seconds", 5)
	v.SetDefault("redis.read_timeout_seconds", 3)
	v.SetDefault("redis.write_timeout_seconds", 3)
	v.SetDefault("redis.pool_size", 1024)
	v.SetDefault("redis.min_idle_conns", 128)
	v.SetDefault("redis.enable_tls", false)

	// Batch Image queue
	v.SetDefault("batch_image.max_items_per_job", 200)
	v.SetDefault("batch_image.max_output_images_per_job", 200)
	v.SetDefault("batch_image.max_output_images_per_item", 4)
	v.SetDefault("batch_image.max_prompt_bytes_per_item", 8000)
	v.SetDefault("batch_image.max_reference_images_per_job", 1000)
	v.SetDefault("batch_image.max_reference_inline_bytes_per_job", 134217728)
	v.SetDefault("batch_image.default_response_mime_type", "image/png")
	v.SetDefault("batch_image.default_image_size", "1K")
	v.SetDefault("batch_image.max_download_items_zip", 200)
	v.SetDefault("batch_image.max_download_bytes_per_request", 536870912)
	v.SetDefault("batch_image.max_download_duration_seconds", 600)
	v.SetDefault("batch_image.max_download_concurrency_per_user", 1)
	v.SetDefault("batch_image.input_retention_after_terminal_hours", 24)
	v.SetDefault("batch_image.output_retention_after_terminal_hours", 72)
	v.SetDefault("batch_image.cleanup_interval_minutes", 30)
	v.SetDefault("batch_image.cleanup_batch_size", 100)
	v.SetDefault("batch_image.queue.ready_key", "batch_image:queue:ready")
	v.SetDefault("batch_image.queue.delayed_key", "batch_image:queue:delayed")
	v.SetDefault("batch_image.queue.active_key", "batch_image:queue:active")
	v.SetDefault("batch_image.queue.inflight_key_prefix", "batch_image:queue:inflight:")
	v.SetDefault("batch_image.queue.lock_key_prefix", "batch_image:queue:lock:")
	v.SetDefault("batch_image.queue.inflight_ttl_seconds", 604800)
	v.SetDefault("batch_image.queue.job_lock_ttl_seconds", 300)
	v.SetDefault("batch_image.queue.default_requeue_delay_seconds", 30)
	v.SetDefault("batch_image.queue.error_retry_delay_seconds", 60)
	v.SetDefault("batch_image.queue.lock_conflict_delay_seconds", 5)
	v.SetDefault("batch_image.queue.stale_active_after_seconds", 600)
	v.SetDefault("batch_image.queue.delayed_mover_interval_seconds", 5)
	v.SetDefault("batch_image.queue.recovery_interval_seconds", 300)
	v.SetDefault("batch_image.queue.delayed_move_limit", 100)
	v.SetDefault("batch_image.queue.recover_limit", 100)
	v.SetDefault("batch_image.vertex.project_id", "")
	v.SetDefault("batch_image.vertex.location", "global")
	v.SetDefault("batch_image.vertex.managed_gcs_bucket", "")
	v.SetDefault("batch_image.vertex.managed_gcs_prefix", "batch-image/{env}/{batch_id}")
	v.SetDefault("batch_image.vertex.batch_prediction_base_url", "")
	v.SetDefault("batch_image.vertex.gcs_base_url", "")

	// Creative Studio.
	v.SetDefault("creative.enabled", true)
	v.SetDefault("creative.queue_worker_enabled", true)
	v.SetDefault("creative.transient_ttl_seconds", 1800)
	v.SetDefault("creative.max_asset_bytes", 33554432)
	v.SetDefault("creative.max_total_input_bytes", 67108864)
	v.SetDefault("creative.max_prompt_chars", 8000)
	v.SetDefault("creative.default_image_size", "1K")
	v.SetDefault("creative.queue.ready_key", "creative:queue:ready")
	v.SetDefault("creative.queue.delayed_key", "creative:queue:delayed")
	v.SetDefault("creative.queue.active_key", "creative:queue:active")
	v.SetDefault("creative.queue.inflight_key_prefix", "creative:queue:inflight:")
	v.SetDefault("creative.queue.lock_key_prefix", "creative:queue:lock:")
	v.SetDefault("creative.queue.inflight_ttl_seconds", 604800)
	v.SetDefault("creative.queue.job_lock_ttl_seconds", 300)
	v.SetDefault("creative.queue.default_requeue_delay_seconds", 30)
	v.SetDefault("creative.queue.error_retry_delay_seconds", 60)
	v.SetDefault("creative.queue.lock_conflict_delay_seconds", 5)
	v.SetDefault("creative.queue.stale_active_after_seconds", 600)
	v.SetDefault("creative.queue.delayed_mover_interval_seconds", 5)
	v.SetDefault("creative.queue.recovery_interval_seconds", 300)
	v.SetDefault("creative.queue.delayed_move_limit", 100)
	v.SetDefault("creative.queue.recover_limit", 100)
	v.SetDefault("creative.execute_timeout_seconds", 300)
	v.SetDefault("creative.max_execute_attempts", 3)

	// Ops
	v.SetDefault("ops.enabled", true)
	v.SetDefault("ops.cleanup.enabled", true)
	v.SetDefault("ops.cleanup.schedule", "0 3 * * *")
	v.SetDefault("ops.cleanup.batch_size", 1000)
	v.SetDefault("ops.cleanup.batch_pause_ms", 200)
	// Retain 30 days across Ops datasets by default.
	v.SetDefault("request_log.retention_days", 30)
	v.SetDefault("ops.cleanup.error_log_retention_days", 30)
	v.SetDefault("ops.cleanup.system_log_retention_days", 30)
	v.SetDefault("ops.cleanup.minute_metrics_retention_days", 30)
	v.SetDefault("ops.cleanup.hourly_metrics_retention_days", 30)
	v.SetDefault("ops.aggregation.enabled", true)
	// Register the accepted collector-cache lifetime copied into ops.Options. The
	// current MetricsQueries reader does not consume it; sixty-five seconds
	// exceeds the collector's default/minimum one-minute interval, while runtime
	// cadence can be longer.

	// JWT
	v.SetDefault("auth.jwt.secret", "")
	v.SetDefault("auth.jwt.refresh_token_ttl_days", 30) // 30-day refresh-token lifetime.

	// TOTP
	v.SetDefault("security.secret_encryption_key", "")

	// Default
	// Admin credentials are created via the setup flow (web wizard / CLI / AUTO_SETUP).
	// Do not ship fixed defaults here to avoid insecure "known credentials" in
	// production.
	v.SetDefault("user_defaults.concurrency", 5)
	v.SetDefault("user_defaults.balance", 0)
	v.SetDefault("api_key.prefix", "sk-")
	v.SetDefault("billing.fallback_rate_multiplier", 1.0)

	// RateLimit
	v.SetDefault("provider.cooldown.overload_minutes", 10)
	v.SetDefault("provider.cooldown.oauth_401_minutes", 10)

	// Pricing uses the models.dev catalog and local supplements for additional
	// billing dimensions.
	v.SetDefault("model_catalog.catalog_url", "https://models.dev/catalog.json?type=all")
	v.SetDefault("storage.dir", "./data")
	v.SetDefault("model_catalog.supplement_file", "")
	v.SetDefault("model_catalog.check_interval_minutes", 10)

	// Default time zone for local-day statistics.
	v.SetDefault("timezone", "Asia/Shanghai")
	v.SetDefault("team.enabled", true)
	v.SetDefault("team.self_service_enabled", true)
	v.SetDefault("team.default_max_members", 10)

	// API Key auth cache
	v.SetDefault("api_key.auth_cache.l1_size", 65535)
	v.SetDefault("api_key.auth_cache.l1_ttl_seconds", 15)
	v.SetDefault("api_key.auth_cache.l2_ttl_seconds", 300)
	v.SetDefault("api_key.auth_cache.negative_ttl_seconds", 30)
	v.SetDefault("api_key.auth_cache.jitter_percent", 10)
	v.SetDefault("api_key.auth_cache.singleflight", true)
	v.SetDefault("api_key.lookup_concurrency", 64)
	v.SetDefault("api_key.invalid_key_abuse.enabled", true)
	v.SetDefault("api_key.invalid_key_abuse.threshold", 120)
	v.SetDefault("api_key.invalid_key_abuse.window_seconds", 60)
	v.SetDefault("api_key.invalid_key_abuse.block_seconds", 60)
	v.SetDefault("api_key.invalid_key_abuse.capacity", 16384)

	// Subscription auth L1 cache

	// Dashboard cache
	v.SetDefault("usage.dashboard_cache.enabled", true)
	v.SetDefault("usage.dashboard_cache.key_prefix", "tokenrouter:")
	v.SetDefault("usage.dashboard_cache.stats_fresh_ttl_seconds", 15)
	v.SetDefault("usage.dashboard_cache.stats_ttl_seconds", 30)
	v.SetDefault("usage.dashboard_cache.stats_refresh_timeout_seconds", 30)

	// Dashboard aggregation
	v.SetDefault("usage.aggregation.enabled", true)
	v.SetDefault("usage.aggregation.interval_seconds", 60)
	v.SetDefault("usage.aggregation.lookback_seconds", 120)
	v.SetDefault("usage.aggregation.backfill_enabled", false)
	v.SetDefault("usage.aggregation.backfill_max_days", 31)
	v.SetDefault("usage.retention.logs_days", 90)
	v.SetDefault("usage.retention.billing_dedup_days", 365)
	v.SetDefault("usage.aggregation.hourly_retention_days", 180)
	v.SetDefault("usage.aggregation.daily_retention_days", 730)
	v.SetDefault("usage.aggregation.recompute_days", 2)

	// Usage cleanup task
	v.SetDefault("usage.cleanup_tasks.enabled", true)
	v.SetDefault("usage.cleanup_tasks.max_range_days", 31)
	v.SetDefault("usage.cleanup_tasks.batch_size", 5000)
	v.SetDefault("usage.cleanup_tasks.worker_interval_seconds", 10)
	v.SetDefault("usage.cleanup_tasks.task_timeout_seconds", 1800)

	// Idempotency
	v.SetDefault("idempotency.observe_only", true)
	v.SetDefault("idempotency.default_ttl_seconds", 86400)
	v.SetDefault("idempotency.system_operation_ttl_seconds", 3600)
	v.SetDefault("idempotency.processing_timeout_seconds", 30)
	v.SetDefault("idempotency.failed_retry_backoff_seconds", 5)
	v.SetDefault("idempotency.max_stored_response_bytes", 64*1024)
	v.SetDefault("idempotency.cleanup_interval_seconds", 60)
	v.SetDefault("idempotency.cleanup_batch_size", 500)

	// Gateway
	v.SetDefault("gateway.transport.response_header_timeout_seconds", 600) // Allow ten minutes for upstream LLM queueing before response headers.
	v.SetDefault("gateway.openai.response_header_timeout_seconds", 0)
	v.SetDefault("gateway.openai.compact_model", "")
	v.SetDefault("gateway.grok.response_header_timeout_seconds", 120)
	v.SetDefault("gateway.openai.first_output_timeout_seconds", 0)
	v.SetDefault("gateway.openai.high_effort_first_output_timeout_seconds", 0)
	v.SetDefault("gateway.upstream_error_body_log.enabled", true)
	v.SetDefault("gateway.upstream_error_body_log.max_bytes", 2048)
	v.SetDefault("gateway.anthropic.inject_beta_for_api_key", false)
	v.SetDefault("gateway.failover.messages_on_400", false)
	v.SetDefault("gateway.failover.max_provider_switches", 10)
	v.SetDefault("gateway.failover.max_provider_switches_gemini", 3)
	v.SetDefault("gateway.openai.codex.force_cli", false)
	v.SetDefault("gateway.openai.codex.image_generation_bridge_enabled", false)
	v.SetDefault("gateway.openai.passthrough_allow_timeout_headers", false)
	v.SetDefault("gateway.openai.live.max_session_duration_seconds", 3600)
	// OpenAI Responses WebSocket defaults.
	v.SetDefault("gateway.openai.responses_ws.client_first_message_timeout_seconds", DefaultOpenAIWSClientFirstMessageTimeoutSeconds)
	v.SetDefault("gateway.openai.responses_ws.ingress_inter_turn_idle_timeout_seconds", 300)
	v.SetDefault("gateway.openai.responses_ws.max_ingress_connections_per_api_key", 64)
	v.SetDefault("gateway.openai.responses_ws.ingress_previous_response_recovery_enabled", true)
	v.SetDefault("gateway.openai.responses_ws.client_read_limit_bytes", 64*1024*1024)
	v.SetDefault("gateway.openai.responses_ws.http_bridge_threshold_bytes", 15*1024*1024)
	v.SetDefault("gateway.openai.responses_ws.max_conns_per_provider", 128)
	v.SetDefault("gateway.openai.responses_ws.min_idle_per_provider", 4)
	v.SetDefault("gateway.openai.responses_ws.max_idle_per_provider", 12)
	v.SetDefault("gateway.openai.responses_ws.dynamic_max_conns_by_provider_concurrency_enabled", true)
	v.SetDefault("gateway.openai.responses_ws.oauth_max_conns_factor", 1.0)
	v.SetDefault("gateway.openai.responses_ws.api_key_max_conns_factor", 1.0)
	v.SetDefault("gateway.openai.responses_ws.dial_timeout_seconds", 10)
	v.SetDefault("gateway.openai.responses_ws.read_timeout_seconds", 900)
	v.SetDefault("gateway.openai.responses_ws.write_timeout_seconds", 120)
	v.SetDefault("gateway.openai.responses_ws.pool_target_utilization", 0.7)
	v.SetDefault("gateway.openai.responses_ws.queue_limit_per_conn", 64)
	v.SetDefault("gateway.openai.responses_ws.prewarm_cooldown_ms", 300)
	v.SetDefault("scheduling.advanced.lb_top_k", 7)
	v.SetDefault("scheduling.advanced.ewma_error_rate_alpha", 0.2)
	v.SetDefault("scheduling.advanced.ewma_ttft_alpha", 0.2)
	v.SetDefault("scheduling.session_affinity.ttl_seconds", 3600)
	v.SetDefault("scheduling.session_affinity.read_legacy_hash", true)
	v.SetDefault("scheduling.session_affinity.dual_write_legacy_hash", true)
	v.SetDefault("scheduling.session_affinity.response_id_ttl_seconds", 3600)
	v.SetDefault("scheduling.advanced.score_weights.priority", 1.0)
	v.SetDefault("scheduling.advanced.score_weights.load", 1.0)
	v.SetDefault("scheduling.advanced.score_weights.queue", 0.7)
	v.SetDefault("scheduling.advanced.score_weights.error_rate", 0.8)
	v.SetDefault("scheduling.advanced.score_weights.ttft", 0.5)
	v.SetDefault("scheduling.advanced.score_weights.reset", 0.0)
	v.SetDefault("scheduling.advanced.score_weights.quota_headroom", 0.0)
	v.SetDefault("scheduling.advanced.score_weights.previous_response", 5.0)
	v.SetDefault("scheduling.advanced.score_weights.session_sticky", 3.0)
	// OpenAI upstream HTTP version policy.
	v.SetDefault("gateway.openai.http2.enabled", true)
	v.SetDefault("gateway.openai.http2.allow_proxy_fallback_to_http1", true)
	v.SetDefault("gateway.openai.http2.fallback_error_threshold", 2)
	v.SetDefault("gateway.openai.http2.fallback_window_seconds", 60)
	v.SetDefault("gateway.openai.http2.fallback_ttl_seconds", 600)
	v.SetDefault("gateway.openai.proxy_stream_circuit.failure_threshold", 2)
	v.SetDefault("gateway.openai.proxy_stream_circuit.window_seconds", 60)
	v.SetDefault("gateway.openai.proxy_stream_circuit.ttl_seconds", 600)
	// The Grok quota gate applies to scheduling; admin QueryQuota bypasses it.
	// Only providers marked free are gated, so unknown and paid tiers are unaffected.
	v.SetDefault("scheduling.grok_free_quota.enabled", true)
	v.SetDefault("provider.grok.password_auth_enabled", false)
	// Default nominal allowance: 500,000 tokens in a rolling 24-hour window.
	v.SetDefault("scheduling.grok_free_quota.token_limit", int64(500_000))
	v.SetDefault("scheduling.grok_free_quota.threshold_percent", 95)
	v.SetDefault("scheduling.grok_free_quota.window_hours", 24)
	v.SetDefault("scheduling.grok_free_quota.stats_cache_seconds", 60)
	// Supplier monitoring is disabled by default; manual admin queries remain available.
	v.SetDefault("provider.usage_monitor.enabled", false)
	v.SetDefault("provider.usage_monitor.pause_below_balance", 0.5)
	v.SetDefault("provider.usage_monitor.interval_minutes", 10)
	v.SetDefault("provider.usage_monitor.concurrency", 4)
	v.SetDefault("provider.usage_monitor.probe_timeout_seconds", 20)
	v.SetDefault("provider.usage_monitor.round_timeout_seconds", 300)
	v.SetDefault("gateway.images.concurrency.enabled", false)
	v.SetDefault("gateway.images.concurrency.max_concurrent_requests", 0)
	v.SetDefault("gateway.images.concurrency.overflow_mode", ImageConcurrencyOverflowModeReject)
	v.SetDefault("gateway.images.concurrency.wait_timeout_seconds", 30)
	v.SetDefault("gateway.images.concurrency.max_waiting_requests", 100)
	v.SetDefault("gateway.max_request_body_bytes", int64(256*1024*1024))
	v.SetDefault("gateway.text_max_request_body_bytes", int64(32*1024*1024))
	v.SetDefault("gateway.upstream_response_max_bytes", DefaultUpstreamResponseReadMaxBytes)
	v.SetDefault("gateway.models_list_response_max_bytes", DefaultModelsListReadMaxBytes)
	v.SetDefault("egress.proxy_probe.response_max_bytes", int64(1024*1024))
	v.SetDefault("gateway.gemini.debug_response_headers", false)
	v.SetDefault("gateway.transport.connection_pool_isolation", ConnectionPoolIsolationProviderProxy)
	// Upstream HTTP connection-pool defaults.
	v.SetDefault("gateway.transport.max_idle_conns", 2560)          // Total idle connection limit.
	v.SetDefault("gateway.transport.max_idle_conns_per_host", 120)  // Idle connection limit per host.
	v.SetDefault("gateway.transport.max_conns_per_host", 1024)      // Active and idle connection limit per host.
	v.SetDefault("gateway.transport.idle_conn_timeout_seconds", 90) // Idle timeout, in seconds.
	v.SetDefault("gateway.transport.client_cache.max_clients", 5000)
	v.SetDefault("gateway.transport.client_cache.idle_ttl_seconds", 900)
	v.SetDefault("scheduling.concurrency_slot_ttl_minutes", 30) // Slot lifetime accommodates long requests.
	v.SetDefault("gateway.streaming.data_idle_timeout_seconds", 180)
	v.SetDefault("gateway.streaming.keepalive_interval_seconds", 10)
	v.SetDefault("gateway.images.stream_data_idle_timeout_seconds", 900)
	v.SetDefault("gateway.images.stream_keepalive_interval_seconds", 10)
	v.SetDefault("gateway.images.nonstream_keepalive_interval_seconds", 0)
	v.SetDefault("gateway.max_sse_line_bytes", 500*1024*1024)
	v.SetDefault("scheduling.sticky_provider_wait.max_waiting", 3)
	v.SetDefault("scheduling.sticky_provider_wait.timeout_seconds", 120)
	v.SetDefault("scheduling.alternate_provider_wait.timeout_seconds", 30)
	v.SetDefault("scheduling.alternate_provider_wait.max_waiting", 100)
	v.SetDefault("scheduling.alternate_selection_mode", "last_used")
	v.SetDefault("scheduling.prefer_soonest_reset", false)
	v.SetDefault("scheduling.load_batch.enabled", true)
	v.SetDefault("scheduling.load_batch.cache_ttl_ms", 200)
	v.SetDefault("scheduling.snapshot.mget_chunk_size", 128)
	v.SetDefault("scheduling.snapshot.write_chunk_size", 256)
	v.SetDefault("scheduling.slot_cleanup_interval_seconds", 30)
	v.SetDefault("scheduling.snapshot.db_fallback_enabled", true)
	v.SetDefault("scheduling.snapshot.db_fallback_timeout_seconds", 0)
	v.SetDefault("scheduling.snapshot.db_fallback_max_qps", 0)
	v.SetDefault("scheduling.snapshot.outbox_poll_interval_seconds", 1)
	v.SetDefault("scheduling.snapshot.outbox_lag_warn_seconds", 5)
	v.SetDefault("scheduling.snapshot.outbox_lag_rebuild_seconds", 10)
	v.SetDefault("scheduling.snapshot.outbox_lag_rebuild_failures", 3)
	v.SetDefault("scheduling.snapshot.outbox_backlog_rebuild_rows", 10000)
	v.SetDefault("scheduling.snapshot.full_rebuild_interval_seconds", 300)
	v.SetDefault("usage.record_queue.worker_count", 128)
	v.SetDefault("usage.record_queue.queue_size", 16384)
	v.SetDefault("usage.record_queue.task_timeout_seconds", 5)
	// The default sync policy runs overflow tasks inline after the response is written.
	// sample and drop can discard billing tasks, creating charge and usage_logs
	// reconciliation gaps (issue #3656).
	// Use those policies only when the deployment accepts that loss.
	v.SetDefault("usage.record_queue.overflow_policy", UsageRecordOverflowPolicySync)
	v.SetDefault("usage.record_queue.overflow_sample_percent", 10)
	v.SetDefault("usage.record_queue.auto_scale_enabled", true)
	v.SetDefault("usage.record_queue.auto_scale_min_workers", 128)
	v.SetDefault("usage.record_queue.auto_scale_max_workers", 512)
	v.SetDefault("usage.record_queue.auto_scale_up_queue_percent", 70)
	v.SetDefault("usage.record_queue.auto_scale_down_queue_percent", 15)
	v.SetDefault("usage.record_queue.auto_scale_up_step", 32)
	v.SetDefault("usage.record_queue.auto_scale_down_step", 16)
	v.SetDefault("usage.record_queue.auto_scale_check_interval_seconds", 3)
	v.SetDefault("usage.record_queue.auto_scale_cooldown_seconds", 10)
	v.SetDefault("billing.group_rate_cache_ttl_seconds", 30)
	// User-message queue defaults.
	v.SetDefault("gateway.anthropic.user_message_queue.lock_ttl_ms", 120000)
	v.SetDefault("gateway.anthropic.user_message_queue.wait_timeout_ms", 30000)
	v.SetDefault("gateway.anthropic.user_message_queue.min_delay_ms", 200)
	v.SetDefault("gateway.anthropic.user_message_queue.max_delay_ms", 2000)
	v.SetDefault("gateway.anthropic.user_message_queue.cleanup_interval_seconds", 60)

	// Enable TLS fingerprint support by default.
	v.SetDefault("gateway.streaming.wait_ping_interval_seconds", 10)

	// TokenRefresh
	v.SetDefault("provider.token_refresh.enabled", true)
	v.SetDefault("provider.token_refresh.check_interval_minutes", 5)        // Check every five minutes.
	v.SetDefault("provider.token_refresh.refresh_before_expiry_hours", 0.5) // Half-hour proactive refresh window.
	v.SetDefault("provider.token_refresh.max_attempts", 3)                  // Three total exchange attempts, including the first.
	v.SetDefault("provider.token_refresh.retry_backoff_seconds", 2)         // Two-second base backoff.
	v.SetDefault("provider.token_refresh.candidate_page_size", 200)
	v.SetDefault("provider.token_refresh.platform_concurrency", 4)
	v.SetDefault("provider.token_refresh.platform_qps", 2)
	v.SetDefault("provider.token_refresh.platform_failure_threshold", 3)
	v.SetDefault("provider.token_refresh.attempt_timeout_seconds", 15)
	v.SetDefault("provider.token_refresh.cycle_timeout_seconds", 240)

	// Gemini OAuth credential and scope inputs remain empty by default.
	// Application construction passes them to the OAuth consumer, which selects
	// its effective client configuration.
	v.SetDefault("provider.gemini.oauth.client_id", "")
	v.SetDefault("provider.gemini.oauth.client_secret", "")
	v.SetDefault("provider.gemini.oauth.scopes", "")
	v.SetDefault("provider.gemini.quota.policy", "")

	v.SetDefault("gateway.openai.codex.forced_instructions_template_file", "")
	v.SetDefault("scheduling.default_session_idle_timeout_minutes", 0)
	v.SetDefault("gateway.anthropic.user_message_queue.default_mode", "")
	v.SetDefault("outbound_proxy.url", "")

	// Register sticky-escape defaults as decode keys so supplied zero error rate
	// stays meaningful and zero TTFT reaches validation. YAML and usable
	// file values can replace these defaults.
	v.SetDefault("scheduling.advanced.sticky_escape_enabled", true)
	v.SetDefault("scheduling.advanced.sticky_escape_error_rate", 0.5)
	v.SetDefault("scheduling.advanced.sticky_escape_ttft_ms", 15000)

	// Register OAuth client secrets and login defaults for sparse version 2 files.
	// Explicit references resolve before this instance decodes the selected document.
	for _, provider := range []string{"auth.oauth.github", "auth.oauth.google"} {
		v.SetDefault(provider+".enabled", false)
		v.SetDefault(provider+".client_id", "")
		v.SetDefault(provider+".client_secret", "")
		v.SetDefault(provider+".authorize_url", "")
		v.SetDefault(provider+".token_url", "")
		v.SetDefault(provider+".userinfo_url", "")
		v.SetDefault(provider+".emails_url", "")
		v.SetDefault(provider+".scopes", "")
		v.SetDefault(provider+".redirect_url", "")
		v.SetDefault(provider+".frontend_redirect_url", "")
	}

	v.SetDefault("auth.oauth.dingtalk.client_id", "")
	v.SetDefault("auth.oauth.dingtalk.client_secret", "")
	v.SetDefault("auth.oauth.dingtalk.internal_corp_id", "")
	v.SetDefault("auth.oauth.dingtalk.redirect_url", "")
	v.SetDefault("auth.oauth.dingtalk.bypass_registration", false)
	v.SetDefault("auth.oauth.dingtalk.sync_display_name", false)
	v.SetDefault("auth.oauth.dingtalk.sync_display_name_attr_key", "")
	v.SetDefault("auth.oauth.dingtalk.sync_display_name_attr_name", "")
	v.SetDefault("auth.oauth.dingtalk.sync_dept", false)
	v.SetDefault("auth.oauth.dingtalk.sync_dept_attr_key", "")
	v.SetDefault("auth.oauth.dingtalk.sync_dept_attr_name", "")
	v.SetDefault("auth.oauth.dingtalk.sync_corp_email", false)
	v.SetDefault("auth.oauth.dingtalk.sync_corp_email_attr_key", "")
	v.SetDefault("auth.oauth.dingtalk.sync_corp_email_attr_name", "")
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
	if c.Server.Mode != "debug" && c.Server.Mode != "release" {
		return fmt.Errorf("server.mode must be debug or release")
	}
	mode := c.Gateway.Anthropic.UserMessageQueue.DefaultMode
	if mode != "" && mode != "serialize" && mode != "throttle" {
		return fmt.Errorf("gateway.anthropic.user_message_queue.default_mode must be empty, serialize, or throttle")
	}
	if c.Provider.Antigravity.FallbackCooldownSeconds <= 0 {
		return fmt.Errorf("provider.antigravity.fallback_cooldown_seconds must be positive")
	}
	switch c.Provider.Antigravity.ForwardEndpoint {
	case "auto", "prod", "daily":
	default:
		return fmt.Errorf("provider.antigravity.forward_endpoint must be auto, prod, or daily")
	}
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

	if err := c.Plugins.validate(); err != nil {
		return err
	}
	if c.RequestLog.RetentionDays < 0 {
		return fmt.Errorf("request_log.retention_days must be non-negative")
	}

	forwardedClientIPHeaders, err := NormalizeForwardedClientIPHeaders(c.Server.ClientIP.ForwardedHeaders)
	if err != nil {
		return fmt.Errorf("server.client_ip.forwarded_headers: %w", err)
	}
	// Successful header normalization reaches publication before proxy-probe
	// checking. Proxy-probe or later validation failures leave the startup
	// trust/header policy installed in the live cell.
	c.Server.ClientIP.ForwardedHeaders = forwardedClientIPHeaders
	c.SetForwardedClientIPSettings(c.Server.ClientIP.TrustForwardedHeaders, forwardedClientIPHeaders)
	proxyProbeURLs, err := normalizeProxyProbeURLs(c.Egress.ProxyProbe.URLs)
	if err != nil {
		return fmt.Errorf("egress.proxy_probe.urls: %w", err)
	}
	c.Egress.ProxyProbe.URLs = proxyProbeURLs
	// This budget covers ingress header reading before body handling or upstream
	// stream timing begins.
	if c.Server.ReadHeaderTimeoutSeconds < 1 || c.Server.ReadHeaderTimeoutSeconds > 60 {
		return fmt.Errorf("server.read_header_timeout_seconds must be between 1 and 60 seconds")
	}
	// Server construction also uses this ingress limit for the HTTP/2 header-list
	// size.
	if c.Server.MaxHeaderBytes < 8*1024 || c.Server.MaxHeaderBytes > 1024*1024 {
		return fmt.Errorf("server.max_header_bytes must be between 8192 and 1048576 bytes")
	}
	if c.Server.IdleTimeoutSeconds <= 0 {
		return fmt.Errorf("server.idle_timeout_seconds must be positive")
	}
	if c.Server.MaxRequestBodyBytes < 0 {
		return fmt.Errorf("server.max_request_body_bytes must be non-negative")
	}
	// HTTP/2 frame payload lengths use a 24-bit value, and connection receive
	// windows start at 65535 bytes. Check enabled cleartext settings against those
	// protocol limits before server construction.
	if c.Server.H2C.Enabled {
		if c.Server.H2C.MaxConcurrentStreams == 0 {
			return fmt.Errorf("server.h2c.max_concurrent_streams must be positive")
		}
		if c.Server.H2C.IdleTimeoutSeconds <= 0 {
			return fmt.Errorf("server.h2c.idle_timeout_seconds must be positive")
		}
		if c.Server.H2C.MaxReadFrameBytes < 16*1024 || c.Server.H2C.MaxReadFrameBytes > 16*1024*1024-1 {
			return fmt.Errorf("server.h2c.max_read_frame_bytes must be between 16384 and 16777215 bytes")
		}
		if c.Server.H2C.MaxUploadBufferPerConnectionBytes < 65535 {
			return fmt.Errorf("server.h2c.max_upload_buffer_per_connection_bytes must be at least 65535 bytes")
		}
		if c.Server.H2C.MaxUploadBufferPerStreamBytes <= 0 {
			return fmt.Errorf("server.h2c.max_upload_buffer_per_stream_bytes must be positive")
		}
	}
	// Apply the configured abuse observation limits when tracking is enabled. Its
	// threshold, observation window, block interval, and capacity determine the
	// process-local invalid-auth counter behavior.
	if c.APIKey.InvalidKeyAbuse.Enabled {
		// The counter threshold governs repeated invalid credentials from one
		// observed client address.
		if c.APIKey.InvalidKeyAbuse.Threshold < 10 {
			return fmt.Errorf("api_key.invalid_key_abuse.threshold must be at least 10")
		}
		// Bound the seconds during which invalid-auth observations contribute to that
		// client counter.
		if c.APIKey.InvalidKeyAbuse.WindowSeconds < 1 || c.APIKey.InvalidKeyAbuse.WindowSeconds > 3600 {
			return fmt.Errorf("api_key.invalid_key_abuse.window_seconds must be between 1 and 3600")
		}
		// The blocking interval starts after the observation threshold has been
		// reached.
		if c.APIKey.InvalidKeyAbuse.BlockSeconds < 1 || c.APIKey.InvalidKeyAbuse.BlockSeconds > 3600 {
			return fmt.Errorf("api_key.invalid_key_abuse.block_seconds must be between 1 and 3600")
		}
		// This entry capacity bounds client-address tracking in the process-local
		// abuse cache.
		if c.APIKey.InvalidKeyAbuse.Capacity < 256 || c.APIKey.InvalidKeyAbuse.Capacity > 1_000_000 {
			return fmt.Errorf("api_key.invalid_key_abuse.capacity must be between 256 and 1000000")
		}
	}
	jwtSecret := strings.TrimSpace(c.Auth.JWT.Secret)
	if jwtSecret == "" {
		return fmt.Errorf("auth.jwt.secret is required")
	}
	// The signing input minimum is measured as encoded bytes rather than rune
	// count. Check the trimmed secret before identity construction so multibyte
	// text contributes its UTF-8 byte length.
	if len([]byte(jwtSecret)) < 32 {
		return fmt.Errorf("auth.jwt.secret must be at least 32 bytes")
	}
	if err := c.validateLog(); err != nil {
		return err
	}

	// Gemini client selection uses a credential pair. Reject a partial trimmed
	// pair before consumers select configured credentials or their default client
	// choice, keeping the two source members together.
	geminiClientID := strings.TrimSpace(c.Provider.Gemini.OAuth.ClientID)
	geminiClientSecret := strings.TrimSpace(c.Provider.Gemini.OAuth.ClientSecret)
	if (geminiClientID == "") != (geminiClientSecret == "") {
		return fmt.Errorf("provider.gemini.oauth.client_id and provider.gemini.oauth.client_secret must be both set or both empty")
	}

	if strings.TrimSpace(c.Site.FrontendURL) != "" {
		if err := ValidateAbsoluteHTTPURL(c.Site.FrontendURL); err != nil {
			return fmt.Errorf("site.frontend_url invalid: %w", err)
		}
		u, err := url.Parse(strings.TrimSpace(c.Site.FrontendURL))
		if err != nil {
			return fmt.Errorf("site.frontend_url invalid: %w", err)
		}
		if u.RawQuery != "" || u.ForceQuery {
			return fmt.Errorf("site.frontend_url invalid: must not include query")
		}
		if u.User != nil {
			return fmt.Errorf("site.frontend_url invalid: must not include userinfo")
		}
		warnIfInsecureURL("site.frontend_url", c.Site.FrontendURL)
	}
	if err := c.validateWebAuthn(); err != nil {
		return err
	}
	if c.Auth.JWT.AccessTokenTTLMinutes <= 0 {
		return fmt.Errorf("auth.jwt.access_token_ttl_minutes must be positive")
	}
	// Warn above the ordinary one-day default without rejecting an explicit longer lifetime.
	if c.Auth.JWT.AccessTokenTTLMinutes > 1440 {
		slog.Warn("auth.jwt.access_token_ttl_minutes is high; consider shorter expiration for security")
	}
	if c.Auth.JWT.RefreshTokenTTLDays <= 0 {
		return fmt.Errorf("auth.jwt.refresh_token_ttl_days must be positive")
	}
	// Long refresh-token lifetimes remain accepted after this warning is emitted.
	if c.Auth.JWT.RefreshTokenTTLDays > 90 {
		slog.Warn("auth.jwt.refresh_token_ttl_days is high; consider shorter expiration for security", "refresh_token_expire_days", c.Auth.JWT.RefreshTokenTTLDays)
	}
	if c.Security.CSP.Enabled && strings.TrimSpace(c.Security.CSP.Policy) == "" {
		return fmt.Errorf("security.csp.policy is required when CSP is enabled")
	}
	if c.Auth.OAuth.LinuxDo.Enabled {
		if strings.TrimSpace(c.Auth.OAuth.LinuxDo.ClientID) == "" {
			return fmt.Errorf("auth.oauth.linuxdo.client_id is required when auth.oauth.linuxdo.enabled=true")
		}
		if strings.TrimSpace(c.Auth.OAuth.LinuxDo.AuthorizeURL) == "" {
			return fmt.Errorf("auth.oauth.linuxdo.authorize_url is required when auth.oauth.linuxdo.enabled=true")
		}
		if strings.TrimSpace(c.Auth.OAuth.LinuxDo.TokenURL) == "" {
			return fmt.Errorf("auth.oauth.linuxdo.token_url is required when auth.oauth.linuxdo.enabled=true")
		}
		if strings.TrimSpace(c.Auth.OAuth.LinuxDo.UserInfoURL) == "" {
			return fmt.Errorf("auth.oauth.linuxdo.userinfo_url is required when auth.oauth.linuxdo.enabled=true")
		}
		if strings.TrimSpace(c.Auth.OAuth.LinuxDo.RedirectURL) == "" {
			return fmt.Errorf("auth.oauth.linuxdo.redirect_url is required when auth.oauth.linuxdo.enabled=true")
		}
		method := strings.ToLower(strings.TrimSpace(c.Auth.OAuth.LinuxDo.TokenAuthMethod))
		switch method {
		case "", "client_secret_post", "client_secret_basic", "none":
		default:
			return fmt.Errorf("auth.oauth.linuxdo.token_auth_method must be one of: client_secret_post/client_secret_basic/none")
		}
		if (method == "" || method == "client_secret_post" || method == "client_secret_basic") &&
			strings.TrimSpace(c.Auth.OAuth.LinuxDo.ClientSecret) == "" {
			return fmt.Errorf("auth.oauth.linuxdo.client_secret is required when auth.oauth.linuxdo.enabled=true and token_auth_method is client_secret_post/client_secret_basic")
		}
		if strings.TrimSpace(c.Auth.OAuth.LinuxDo.FrontendRedirectURL) == "" {
			return fmt.Errorf("auth.oauth.linuxdo.frontend_redirect_url is required when auth.oauth.linuxdo.enabled=true")
		}

		if err := ValidateAbsoluteHTTPURL(c.Auth.OAuth.LinuxDo.AuthorizeURL); err != nil {
			return fmt.Errorf("auth.oauth.linuxdo.authorize_url invalid: %w", err)
		}
		if err := ValidateAbsoluteHTTPURL(c.Auth.OAuth.LinuxDo.TokenURL); err != nil {
			return fmt.Errorf("auth.oauth.linuxdo.token_url invalid: %w", err)
		}
		if err := ValidateAbsoluteHTTPURL(c.Auth.OAuth.LinuxDo.UserInfoURL); err != nil {
			return fmt.Errorf("auth.oauth.linuxdo.userinfo_url invalid: %w", err)
		}
		if err := ValidateAbsoluteHTTPURL(c.Auth.OAuth.LinuxDo.RedirectURL); err != nil {
			return fmt.Errorf("auth.oauth.linuxdo.redirect_url invalid: %w", err)
		}
		if err := ValidateFrontendRedirectURL(c.Auth.OAuth.LinuxDo.FrontendRedirectURL); err != nil {
			return fmt.Errorf("auth.oauth.linuxdo.frontend_redirect_url invalid: %w", err)
		}

		warnIfInsecureURL("auth.oauth.linuxdo.authorize_url", c.Auth.OAuth.LinuxDo.AuthorizeURL)
		warnIfInsecureURL("auth.oauth.linuxdo.token_url", c.Auth.OAuth.LinuxDo.TokenURL)
		warnIfInsecureURL("auth.oauth.linuxdo.userinfo_url", c.Auth.OAuth.LinuxDo.UserInfoURL)
		warnIfInsecureURL("auth.oauth.linuxdo.redirect_url", c.Auth.OAuth.LinuxDo.RedirectURL)
		warnIfInsecureURL("auth.oauth.linuxdo.frontend_redirect_url", c.Auth.OAuth.LinuxDo.FrontendRedirectURL)
	}
	if c.Auth.OAuth.WeChat.Enabled {
		weChat := c.Auth.OAuth.WeChat

		if weChat.OpenEnabled {
			if strings.TrimSpace(weChat.OpenAppID) == "" {
				return fmt.Errorf("auth.oauth.wechat.open_app_id is required when auth.oauth.wechat.open_enabled=true")
			}
			if strings.TrimSpace(weChat.OpenAppSecret) == "" {
				return fmt.Errorf("auth.oauth.wechat.open_app_secret is required when auth.oauth.wechat.open_enabled=true")
			}
		}
		if weChat.MPEnabled {
			if strings.TrimSpace(weChat.MPAppID) == "" {
				return fmt.Errorf("auth.oauth.wechat.mp_app_id is required when auth.oauth.wechat.mp_enabled=true")
			}
			if strings.TrimSpace(weChat.MPAppSecret) == "" {
				return fmt.Errorf("auth.oauth.wechat.mp_app_secret is required when auth.oauth.wechat.mp_enabled=true")
			}
		}
		if weChat.MobileEnabled {
			if strings.TrimSpace(weChat.MobileAppID) == "" {
				return fmt.Errorf("auth.oauth.wechat.mobile_app_id is required when auth.oauth.wechat.mobile_enabled=true")
			}
			if strings.TrimSpace(weChat.MobileAppSecret) == "" {
				return fmt.Errorf("auth.oauth.wechat.mobile_app_secret is required when auth.oauth.wechat.mobile_enabled=true")
			}
		}
		if v := strings.TrimSpace(weChat.RedirectURL); v != "" {
			if err := ValidateAbsoluteHTTPURL(v); err != nil {
				return fmt.Errorf("auth.oauth.wechat.redirect_url invalid: %w", err)
			}
			warnIfInsecureURL("auth.oauth.wechat.redirect_url", v)
		}
		if err := ValidateFrontendRedirectURL(weChat.FrontendRedirectURL); err != nil {
			return fmt.Errorf("auth.oauth.wechat.frontend_redirect_url invalid: %w", err)
		}
		warnIfInsecureURL("auth.oauth.wechat.frontend_redirect_url", weChat.FrontendRedirectURL)
	}
	if err := c.validateOIDC(); err != nil {
		return err
	}
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
	if err := c.validateGateway(); err != nil {
		return err
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
	if c.Gateway.Streaming.WaitPingIntervalSeconds < 5 || c.Gateway.Streaming.WaitPingIntervalSeconds > 30 {
		return fmt.Errorf("gateway.streaming.wait_ping_interval_seconds must be between 5-30 seconds")
	}
	if c.Scheduling.GrokFreeQuota.Enabled {
		if c.Scheduling.GrokFreeQuota.TokenLimit <= 0 {
			return fmt.Errorf("scheduling.grok_free_quota.token_limit must be positive")
		}
		// The soft gate compares rolling free-tier token usage with this fraction of
		// the configured token limit.
		if c.Scheduling.GrokFreeQuota.ThresholdPercent < 1 || c.Scheduling.GrokFreeQuota.ThresholdPercent > 100 {
			return fmt.Errorf("scheduling.grok_free_quota.threshold_percent must be between 1 and 100")
		}
		if c.Scheduling.GrokFreeQuota.WindowHours <= 0 {
			return fmt.Errorf("scheduling.grok_free_quota.window_hours must be positive")
		}
	}
	if c.Scheduling.GrokFreeQuota.StatsCacheSeconds < 0 {
		return fmt.Errorf("scheduling.grok_free_quota.stats_cache_seconds must be non-negative")
	}
	if err := ValidateDingTalkConfig(c.Auth.OAuth.DingTalk); err != nil {
		return fmt.Errorf("auth.oauth.dingtalk: %w", err)
	}
	return nil
}

// validateGateway checks gateway transport, scheduler, and completion limits,
// returning the first invalid setting. Validate calls it after idempotency and
// before Ops; the later Grok quota checks remain after concurrency validation.
// Callers serialize access because Live duration defaulting mutates the receiver
// as validation reaches that field; response-affinity lifetimes are already final.
//
// A later error retains those mutations and any earlier idle-lifetime warning.
// Coupled pool bounds, finite scoring sums, and enabled autoscaling constraints
// are checked together so their consumers receive consistent limits. Disabled
// autoscaling skips only its scaling controls; ordinary completion limits still
// apply.
func (c *Config) validateGateway() error {
	if c.Gateway.MaxRequestBodyBytes <= 0 {
		return fmt.Errorf("gateway.max_request_body_bytes must be positive")
	}
	if c.Gateway.TextMaxRequestBodyBytes <= 0 || c.Gateway.TextMaxRequestBodyBytes > c.Gateway.MaxRequestBodyBytes {
		return fmt.Errorf("gateway.text_max_request_body_bytes must be positive and no greater than gateway.max_request_body_bytes")
	}
	if c.Gateway.UpstreamResponseMaxBytes <= 0 {
		return fmt.Errorf("gateway.upstream_response_max_bytes must be positive")
	}
	if c.Gateway.ModelsListResponseMaxBytes <= 0 {
		return fmt.Errorf("gateway.models_list_response_max_bytes must be positive")
	}
	if c.Egress.ProxyProbe.ResponseMaxBytes <= 0 {
		return fmt.Errorf("egress.proxy_probe.response_max_bytes must be positive")
	}
	if c.Gateway.Transport.ResponseHeaderTimeoutSeconds < 0 {
		return fmt.Errorf("gateway.transport.response_header_timeout_seconds must be non-negative")
	}
	if c.Gateway.OpenAI.ResponseHeaderTimeoutSeconds < 0 {
		return fmt.Errorf("gateway.openai.response_header_timeout_seconds must be non-negative")
	}
	// Grok header timing has its own cap before streaming data-gap handling takes
	// over.
	if c.Gateway.Grok.ResponseHeaderTimeoutSeconds < 0 || c.Gateway.Grok.ResponseHeaderTimeoutSeconds > 1800 {
		return fmt.Errorf("gateway.grok.response_header_timeout_seconds must be between 0-1800 seconds")
	}
	// The ordinary OpenAI first-output budget is validated independently of the
	// high-effort budget below; zero disables its configured first-output
	// deadline.
	if c.Gateway.OpenAI.FirstOutputTimeoutSeconds < 0 || c.Gateway.OpenAI.FirstOutputTimeoutSeconds > 600 ||
		(c.Gateway.OpenAI.FirstOutputTimeoutSeconds > 0 && c.Gateway.OpenAI.FirstOutputTimeoutSeconds < 30) {
		return fmt.Errorf("gateway.openai.first_output_timeout_seconds must be 0 or between 30-600 seconds")
	}
	// The high-effort override can exceed the ordinary budget. The consumer
	// considers it after a positive ordinary budget has enabled first-output
	// timing.
	if c.Gateway.OpenAI.HighEffortFirstOutputTimeoutSeconds < 0 || c.Gateway.OpenAI.HighEffortFirstOutputTimeoutSeconds > 1800 ||
		(c.Gateway.OpenAI.HighEffortFirstOutputTimeoutSeconds > 0 && c.Gateway.OpenAI.HighEffortFirstOutputTimeoutSeconds < 30) {
		return fmt.Errorf("gateway.openai.high_effort_first_output_timeout_seconds must be 0 or between 30-1800 seconds")
	}
	// A missing or nonpositive Live session duration has no usable cap. Supply the
	// one-hour process default before downstream Live session construction reads
	// this field.
	if c.Gateway.OpenAI.Live.MaxSessionDurationSeconds <= 0 {
		c.Gateway.OpenAI.Live.MaxSessionDurationSeconds = 3600
	}
	if strings.TrimSpace(c.Gateway.Transport.ConnectionPoolIsolation) != "" {
		switch c.Gateway.Transport.ConnectionPoolIsolation {
		case ConnectionPoolIsolationProxy, ConnectionPoolIsolationProvider, ConnectionPoolIsolationProviderProxy:
		default:
			return fmt.Errorf("gateway.transport.connection_pool_isolation must be one of: %s/%s/%s",
				ConnectionPoolIsolationProxy, ConnectionPoolIsolationProvider, ConnectionPoolIsolationProviderProxy)
		}
	}
	if c.Gateway.Images.Concurrency.MaxConcurrentRequests < 0 {
		return fmt.Errorf("gateway.images.concurrency.max_concurrent_requests must be non-negative")
	}
	switch strings.TrimSpace(c.Gateway.Images.Concurrency.OverflowMode) {
	case "", ImageConcurrencyOverflowModeReject, ImageConcurrencyOverflowModeWait:
	default:
		return fmt.Errorf("gateway.images.concurrency.overflow_mode must be one of: %s/%s",
			ImageConcurrencyOverflowModeReject, ImageConcurrencyOverflowModeWait)
	}
	if c.Gateway.Images.Concurrency.WaitTimeoutSeconds < 0 {
		return fmt.Errorf("gateway.images.concurrency.wait_timeout_seconds must be non-negative")
	}
	if c.Gateway.Images.Concurrency.MaxWaitingRequests < 0 {
		return fmt.Errorf("gateway.images.concurrency.max_waiting_requests must be non-negative")
	}
	if c.Gateway.Transport.MaxIdleConns <= 0 {
		return fmt.Errorf("gateway.transport.max_idle_conns must be positive")
	}
	if c.Gateway.Transport.MaxIdleConnsPerHost <= 0 {
		return fmt.Errorf("gateway.transport.max_idle_conns_per_host must be positive")
	}
	if c.Gateway.Transport.MaxConnsPerHost < 0 {
		return fmt.Errorf("gateway.transport.max_conns_per_host must be non-negative")
	}
	if c.Gateway.Transport.IdleConnTimeoutSeconds <= 0 {
		return fmt.Errorf("gateway.transport.idle_conn_timeout_seconds must be positive")
	}
	// This transport-idle threshold warns while retaining the supplied connection
	// lifetime.
	if c.Gateway.Transport.IdleConnTimeoutSeconds > 180 {
		slog.Warn("gateway.transport.idle_conn_timeout_seconds is high; consider 60-120 seconds for better connection reuse", "idle_conn_timeout_seconds", c.Gateway.Transport.IdleConnTimeoutSeconds)
	}
	if c.Gateway.Transport.ClientCache.MaxClients <= 0 {
		return fmt.Errorf("gateway.transport.client_cache.max_clients must be positive")
	}
	if c.Gateway.Transport.ClientCache.IdleTTLSeconds <= 0 {
		return fmt.Errorf("gateway.transport.client_cache.idle_ttl_seconds must be positive")
	}
	if c.Scheduling.ConcurrencySlotTTLMinutes <= 0 {
		return fmt.Errorf("scheduling.concurrency_slot_ttl_minutes must be positive")
	}
	if c.Gateway.Streaming.DataIdleTimeoutSeconds < 0 {
		return fmt.Errorf("gateway.streaming.data_idle_timeout_seconds must be non-negative")
	}
	// Bound ordinary stream inactivity independently of the keepalive interval;
	// zero disables this deadline.
	if c.Gateway.Streaming.DataIdleTimeoutSeconds != 0 &&
		(c.Gateway.Streaming.DataIdleTimeoutSeconds < 30 || c.Gateway.Streaming.DataIdleTimeoutSeconds > 300) {
		return fmt.Errorf("gateway.streaming.data_idle_timeout_seconds must be 0 or between 30-300 seconds")
	}
	if c.Gateway.Streaming.KeepaliveIntervalSeconds < 0 {
		return fmt.Errorf("gateway.streaming.keepalive_interval_seconds must be non-negative")
	}
	// These downstream keepalives have their own interval, independent of upstream
	// data arrival.
	if c.Gateway.Streaming.KeepaliveIntervalSeconds != 0 &&
		(c.Gateway.Streaming.KeepaliveIntervalSeconds < 5 || c.Gateway.Streaming.KeepaliveIntervalSeconds > 30) {
		return fmt.Errorf("gateway.streaming.keepalive_interval_seconds must be 0 or between 5-30 seconds")
	}
	if c.Gateway.Images.StreamDataIdleTimeoutSeconds < 0 {
		return fmt.Errorf("gateway.images.stream_data_idle_timeout_seconds must be non-negative")
	}
	// Image data gaps allow a wider budget than ordinary text streams.
	if c.Gateway.Images.StreamDataIdleTimeoutSeconds != 0 &&
		(c.Gateway.Images.StreamDataIdleTimeoutSeconds < 60 || c.Gateway.Images.StreamDataIdleTimeoutSeconds > 1800) {
		return fmt.Errorf("gateway.images.stream_data_idle_timeout_seconds must be 0 or between 60-1800 seconds")
	}
	if c.Gateway.Images.StreamKeepaliveIntervalSeconds < 0 {
		return fmt.Errorf("gateway.images.stream_keepalive_interval_seconds must be non-negative")
	}
	// Image streaming keepalives use the image-specific interval while awaiting
	// generation output.
	if c.Gateway.Images.StreamKeepaliveIntervalSeconds != 0 &&
		(c.Gateway.Images.StreamKeepaliveIntervalSeconds < 5 || c.Gateway.Images.StreamKeepaliveIntervalSeconds > 60) {
		return fmt.Errorf("gateway.images.stream_keepalive_interval_seconds must be 0 or between 5-60 seconds")
	}
	if c.Gateway.Images.NonstreamKeepaliveIntervalSeconds < 0 {
		return fmt.Errorf("gateway.images.nonstream_keepalive_interval_seconds must be non-negative")
	}
	// Nonstream image JSON responses have their own keepalive cadence, separate
	// from image SSE.
	if c.Gateway.Images.NonstreamKeepaliveIntervalSeconds != 0 &&
		(c.Gateway.Images.NonstreamKeepaliveIntervalSeconds < 5 || c.Gateway.Images.NonstreamKeepaliveIntervalSeconds > 60) {
		return fmt.Errorf("gateway.images.nonstream_keepalive_interval_seconds must be 0 or between 5-60 seconds")
	}
	if c.Gateway.OpenAI.ResponsesWS.MaxConnsPerProvider <= 0 {
		return fmt.Errorf("gateway.openai.responses_ws.max_conns_per_provider must be positive")
	}
	if c.Gateway.OpenAI.ResponsesWS.ClientFirstMessageTimeoutSeconds <= 0 {
		return fmt.Errorf("gateway.openai.responses_ws.client_first_message_timeout_seconds must be positive")
	}
	if c.Gateway.OpenAI.ResponsesWS.IngressInterTurnIdleTimeoutSeconds < 0 {
		return fmt.Errorf("gateway.openai.responses_ws.ingress_inter_turn_idle_timeout_seconds must be non-negative")
	}
	if c.Gateway.OpenAI.ResponsesWS.MaxIngressConnectionsPerAPIKey < 0 {
		return fmt.Errorf("gateway.openai.responses_ws.max_ingress_connections_per_api_key must be non-negative")
	}
	if c.Gateway.OpenAI.ResponsesWS.MinIdlePerProvider < 0 {
		return fmt.Errorf("gateway.openai.responses_ws.min_idle_per_provider must be non-negative")
	}
	if c.Gateway.OpenAI.ResponsesWS.MaxIdlePerProvider < 0 {
		return fmt.Errorf("gateway.openai.responses_ws.max_idle_per_provider must be non-negative")
	}
	// Provider pool minima and retained-idle maxima share the total connection
	// capacity. Reject inverted or oversized idle limits before constructing pools
	// that maintain and reclaim those connections.
	if c.Gateway.OpenAI.ResponsesWS.MinIdlePerProvider > c.Gateway.OpenAI.ResponsesWS.MaxIdlePerProvider {
		return fmt.Errorf("gateway.openai.responses_ws.min_idle_per_provider must be <= max_idle_per_provider")
	}
	if c.Gateway.OpenAI.ResponsesWS.MaxIdlePerProvider > c.Gateway.OpenAI.ResponsesWS.MaxConnsPerProvider {
		return fmt.Errorf("gateway.openai.responses_ws.max_idle_per_provider must be <= max_conns_per_provider")
	}
	if c.Gateway.OpenAI.ResponsesWS.OAuthMaxConnsFactor <= 0 {
		return fmt.Errorf("gateway.openai.responses_ws.oauth_max_conns_factor must be positive")
	}
	if c.Gateway.OpenAI.ResponsesWS.APIKeyMaxConnsFactor <= 0 {
		return fmt.Errorf("gateway.openai.responses_ws.api_key_max_conns_factor must be positive")
	}
	if c.Gateway.OpenAI.ResponsesWS.DialTimeoutSeconds <= 0 {
		return fmt.Errorf("gateway.openai.responses_ws.dial_timeout_seconds must be positive")
	}
	if c.Gateway.OpenAI.ResponsesWS.ReadTimeoutSeconds <= 0 {
		return fmt.Errorf("gateway.openai.responses_ws.read_timeout_seconds must be positive")
	}
	if c.Gateway.OpenAI.ResponsesWS.WriteTimeoutSeconds <= 0 {
		return fmt.Errorf("gateway.openai.responses_ws.write_timeout_seconds must be positive")
	}
	// Pool sizing consumes this value as a target fraction of connection
	// utilization.
	if c.Gateway.OpenAI.ResponsesWS.PoolTargetUtilization <= 0 || c.Gateway.OpenAI.ResponsesWS.PoolTargetUtilization > 1 {
		return fmt.Errorf("gateway.openai.responses_ws.pool_target_utilization must be within (0,1]")
	}
	if c.Gateway.OpenAI.ResponsesWS.QueueLimitPerConn <= 0 {
		return fmt.Errorf("gateway.openai.responses_ws.queue_limit_per_conn must be positive")
	}
	if c.Gateway.OpenAI.ResponsesWS.PrewarmCooldownMS < 0 {
		return fmt.Errorf("gateway.openai.responses_ws.prewarm_cooldown_ms must be non-negative")
	}
	if c.Gateway.OpenAI.ResponsesWS.ClientReadLimitBytes <= 0 {
		return fmt.Errorf("gateway.openai.responses_ws.client_read_limit_bytes must be positive")
	}
	if c.Gateway.OpenAI.ResponsesWS.HTTPBridgeThresholdBytes <= 0 {
		return fmt.Errorf("gateway.openai.responses_ws.http_bridge_threshold_bytes must be positive")
	}
	if c.Scheduling.Advanced.LBTopK <= 0 {
		return fmt.Errorf("scheduling.advanced.lb_top_k must be positive")
	}
	// Both EWMA coefficients weight the latest observation against retained
	// error-rate or TTFT history. Reject nonfinite coefficients before those
	// scheduler updates can consume them.
	if c.Scheduling.Advanced.EWMAErrorRateAlpha <= 0 || c.Scheduling.Advanced.EWMAErrorRateAlpha > 1 ||
		math.IsNaN(c.Scheduling.Advanced.EWMAErrorRateAlpha) || math.IsInf(c.Scheduling.Advanced.EWMAErrorRateAlpha, 0) {
		return fmt.Errorf("scheduling.advanced.ewma_error_rate_alpha must be between 0 and 1")
	}
	if c.Scheduling.Advanced.EWMATTFTAlpha <= 0 || c.Scheduling.Advanced.EWMATTFTAlpha > 1 ||
		math.IsNaN(c.Scheduling.Advanced.EWMATTFTAlpha) || math.IsInf(c.Scheduling.Advanced.EWMATTFTAlpha, 0) {
		return fmt.Errorf("scheduling.advanced.ewma_ttft_alpha must be between 0 and 1")
	}
	if c.Scheduling.SessionAffinity.TTLSeconds <= 0 {
		return fmt.Errorf("scheduling.session_affinity.ttl_seconds must be positive")
	}
	if c.Scheduling.SessionAffinity.ResponseIDTTLSeconds <= 0 {
		return fmt.Errorf("scheduling.session_affinity.response_id_ttl_seconds must be positive")
	}
	if c.Gateway.OpenAI.HTTP2.FallbackErrorThreshold < 0 {
		return fmt.Errorf("gateway.openai.http2.fallback_error_threshold must be non-negative")
	}
	if c.Gateway.OpenAI.HTTP2.FallbackWindowSeconds < 0 {
		return fmt.Errorf("gateway.openai.http2.fallback_window_seconds must be non-negative")
	}
	if c.Gateway.OpenAI.HTTP2.FallbackTTLSeconds < 0 {
		return fmt.Errorf("gateway.openai.http2.fallback_ttl_seconds must be non-negative")
	}
	if c.Gateway.OpenAI.ProxyStreamCircuit.FailureThreshold < 0 {
		return fmt.Errorf("gateway.openai.proxy_stream_circuit.failure_threshold must be non-negative")
	}
	if c.Gateway.OpenAI.ProxyStreamCircuit.WindowSeconds < 0 {
		return fmt.Errorf("gateway.openai.proxy_stream_circuit.window_seconds must be non-negative")
	}
	if c.Gateway.OpenAI.ProxyStreamCircuit.TTLSeconds < 0 {
		return fmt.Errorf("gateway.openai.proxy_stream_circuit.ttl_seconds must be non-negative")
	}
	// Scoring consumes each finite nonnegative weight and their combined totals.
	// Check individual values before summing so an invalid component cannot enter
	// candidate score calculations.
	weights := c.Scheduling.Advanced.ScoreWeights
	for _, weight := range []float64{
		weights.Priority, weights.Load, weights.Queue, weights.ErrorRate, weights.TTFT,
		weights.Reset, weights.QuotaHeadroom,
		weights.PreviousResponse, weights.SessionSticky,
	} {
		if weight < 0 || math.IsNaN(weight) || math.IsInf(weight, 0) {
			return fmt.Errorf("scheduling.advanced.score_weights.* must be non-negative and finite")
		}
	}
	// BaseWeightSum omits affinity bonus weights, so a positive bonus cannot make
	// an otherwise zero base score valid. Check sums for overflow as well as
	// component validity before scheduler construction.
	weightSum := weights.BaseWeightSum()
	if weightSum <= 0 {
		return fmt.Errorf("scheduling.advanced.score_weights must not all be zero")
	}
	if math.IsNaN(weightSum) || math.IsInf(weightSum, 0) {
		return fmt.Errorf("scheduling.advanced.score_weights base-weight sum must be finite")
	}
	if totalWeightSum := weights.TotalWeightSum(); math.IsNaN(totalWeightSum) || math.IsInf(totalWeightSum, 0) {
		return fmt.Errorf("scheduling.advanced.score_weights total-weight sum must be finite")
	}
	if c.Scheduling.Advanced.StickyEscapeTTFTMs <= 0 {
		return fmt.Errorf("scheduling.advanced.sticky_escape_ttft_ms must be positive")
	}
	// This fraction sets the error-rate condition for leaving sticky provider
	// affinity.
	if c.Scheduling.Advanced.StickyEscapeErrorRate < 0 || c.Scheduling.Advanced.StickyEscapeErrorRate > 1 {
		return fmt.Errorf("scheduling.advanced.sticky_escape_error_rate must be between 0 and 1")
	}
	if c.Gateway.MaxSSELineBytes < 0 {
		return fmt.Errorf("gateway.max_sse_line_bytes must be non-negative")
	}
	// The nonzero limit applies to one scanned SSE line; zero selects downstream
	// scanner defaults.
	if c.Gateway.MaxSSELineBytes != 0 && c.Gateway.MaxSSELineBytes < 1024*1024 {
		return fmt.Errorf("gateway.max_sse_line_bytes must be at least 1MB")
	}
	if c.Usage.RecordQueue.WorkerCount <= 0 {
		return fmt.Errorf("usage.record_queue.worker_count must be positive")
	}
	if c.Usage.RecordQueue.QueueSize <= 0 {
		return fmt.Errorf("usage.record_queue.queue_size must be positive")
	}
	if c.Usage.RecordQueue.TaskTimeoutSeconds <= 0 {
		return fmt.Errorf("usage.record_queue.task_timeout_seconds must be positive")
	}
	switch strings.ToLower(strings.TrimSpace(c.Usage.RecordQueue.OverflowPolicy)) {
	case UsageRecordOverflowPolicyDrop, UsageRecordOverflowPolicySample, UsageRecordOverflowPolicySync:
	default:
		return fmt.Errorf("usage.record_queue.overflow_policy must be one of: %s/%s/%s",
			UsageRecordOverflowPolicyDrop, UsageRecordOverflowPolicySample, UsageRecordOverflowPolicySync)
	}
	// Validate the percentage for every overflow policy. Sample mode additionally
	// requires positive admission, while the other policies can retain a zero
	// percentage.
	if c.Usage.RecordQueue.OverflowSamplePercent < 0 || c.Usage.RecordQueue.OverflowSamplePercent > 100 {
		return fmt.Errorf("usage.record_queue.overflow_sample_percent must be between 0-100")
	}
	if strings.EqualFold(strings.TrimSpace(c.Usage.RecordQueue.OverflowPolicy), UsageRecordOverflowPolicySample) &&
		c.Usage.RecordQueue.OverflowSamplePercent <= 0 {
		return fmt.Errorf("usage.record_queue.overflow_sample_percent must be positive when overflow_policy=sample")
	}
	// Worker growth and shrinkage use separated queue thresholds inside a
	// configured worker range. Validate the initial count and both adjustment
	// steps with that range before the completion pool can scale.
	if c.Usage.RecordQueue.AutoScaleEnabled {
		if c.Usage.RecordQueue.AutoScaleMinWorkers <= 0 {
			return fmt.Errorf("usage.record_queue.auto_scale_min_workers must be positive")
		}
		if c.Usage.RecordQueue.AutoScaleMaxWorkers <= 0 {
			return fmt.Errorf("usage.record_queue.auto_scale_max_workers must be positive")
		}
		if c.Usage.RecordQueue.AutoScaleMaxWorkers < c.Usage.RecordQueue.AutoScaleMinWorkers {
			return fmt.Errorf("usage.record_queue.auto_scale_max_workers must be >= auto_scale_min_workers")
		}
		if c.Usage.RecordQueue.WorkerCount < c.Usage.RecordQueue.AutoScaleMinWorkers ||
			c.Usage.RecordQueue.WorkerCount > c.Usage.RecordQueue.AutoScaleMaxWorkers {
			return fmt.Errorf("usage.record_queue.worker_count must be between auto_scale_min_workers and auto_scale_max_workers")
		}
		if c.Usage.RecordQueue.AutoScaleUpQueuePercent <= 0 || c.Usage.RecordQueue.AutoScaleUpQueuePercent > 100 {
			return fmt.Errorf("usage.record_queue.auto_scale_up_queue_percent must be between 1-100")
		}
		if c.Usage.RecordQueue.AutoScaleDownQueuePercent < 0 || c.Usage.RecordQueue.AutoScaleDownQueuePercent >= 100 {
			return fmt.Errorf("usage.record_queue.auto_scale_down_queue_percent must be between 0-99")
		}
		if c.Usage.RecordQueue.AutoScaleDownQueuePercent >= c.Usage.RecordQueue.AutoScaleUpQueuePercent {
			return fmt.Errorf("usage.record_queue.auto_scale_down_queue_percent must be less than auto_scale_up_queue_percent")
		}
		if c.Usage.RecordQueue.AutoScaleUpStep <= 0 {
			return fmt.Errorf("usage.record_queue.auto_scale_up_step must be positive")
		}
		if c.Usage.RecordQueue.AutoScaleDownStep <= 0 {
			return fmt.Errorf("usage.record_queue.auto_scale_down_step must be positive")
		}
		if c.Usage.RecordQueue.AutoScaleCheckIntervalSeconds <= 0 {
			return fmt.Errorf("usage.record_queue.auto_scale_check_interval_seconds must be positive")
		}
		if c.Usage.RecordQueue.AutoScaleCooldownSeconds < 0 {
			return fmt.Errorf("usage.record_queue.auto_scale_cooldown_seconds must be non-negative")
		}
	}
	if c.Billing.GroupRateCacheTTLSeconds <= 0 {
		return fmt.Errorf("billing.group_rate_cache_ttl_seconds must be positive")
	}
	if c.Scheduling.StickyProviderWait.MaxWaiting <= 0 {
		return fmt.Errorf("scheduling.sticky_provider_wait.max_waiting must be positive")
	}
	if c.Scheduling.StickyProviderWait.TimeoutSeconds <= 0 {
		return fmt.Errorf("scheduling.sticky_provider_wait.timeout_seconds must be positive")
	}
	if c.Scheduling.AlternateProviderWait.TimeoutSeconds <= 0 {
		return fmt.Errorf("scheduling.alternate_provider_wait.timeout_seconds must be positive")
	}
	if c.Scheduling.AlternateProviderWait.MaxWaiting <= 0 {
		return fmt.Errorf("scheduling.alternate_provider_wait.max_waiting must be positive")
	}
	if c.Scheduling.LoadBatch.CacheTTLMS < 0 {
		return fmt.Errorf("scheduling.load_batch.cache_ttl_ms must be non-negative")
	}
	if c.Scheduling.Snapshot.MGetChunkSize <= 0 {
		return fmt.Errorf("scheduling.snapshot.mget_chunk_size must be positive")
	}
	if c.Scheduling.Snapshot.WriteChunkSize <= 0 {
		return fmt.Errorf("scheduling.snapshot.write_chunk_size must be positive")
	}
	if c.Scheduling.SlotCleanupIntervalSeconds < 0 {
		return fmt.Errorf("scheduling.slot_cleanup_interval_seconds must be non-negative")
	}
	if c.Scheduling.Snapshot.DbFallbackTimeoutSeconds < 0 {
		return fmt.Errorf("scheduling.snapshot.db_fallback_timeout_seconds must be non-negative")
	}
	if c.Scheduling.Snapshot.DbFallbackMaxQPS < 0 {
		return fmt.Errorf("scheduling.snapshot.db_fallback_max_qps must be non-negative")
	}
	if c.Scheduling.Snapshot.OutboxPollIntervalSeconds <= 0 {
		return fmt.Errorf("scheduling.snapshot.outbox_poll_interval_seconds must be positive")
	}
	if c.Scheduling.Snapshot.OutboxLagWarnSeconds < 0 {
		return fmt.Errorf("scheduling.snapshot.outbox_lag_warn_seconds must be non-negative")
	}
	if c.Scheduling.Snapshot.OutboxLagRebuildSeconds < 0 {
		return fmt.Errorf("scheduling.snapshot.outbox_lag_rebuild_seconds must be non-negative")
	}
	if c.Scheduling.Snapshot.OutboxLagRebuildFailures <= 0 {
		return fmt.Errorf("scheduling.snapshot.outbox_lag_rebuild_failures must be positive")
	}
	if c.Scheduling.Snapshot.OutboxBacklogRebuildRows < 0 {
		return fmt.Errorf("scheduling.snapshot.outbox_backlog_rebuild_rows must be non-negative")
	}
	if c.Scheduling.Snapshot.FullRebuildIntervalSeconds < 0 {
		return fmt.Errorf("scheduling.snapshot.full_rebuild_interval_seconds must be non-negative")
	}
	// Positive outbox thresholds should warn before forced rebuild. Check their
	// relative order when both mechanisms are configured, allowing zero to retain
	// each consumer's disabled-threshold behavior.
	if c.Scheduling.Snapshot.OutboxLagWarnSeconds > 0 &&
		c.Scheduling.Snapshot.OutboxLagRebuildSeconds > 0 &&
		c.Scheduling.Snapshot.OutboxLagRebuildSeconds < c.Scheduling.Snapshot.OutboxLagWarnSeconds {
		return fmt.Errorf("scheduling.snapshot.outbox_lag_rebuild_seconds must be >= outbox_lag_warn_seconds")
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
	if c.Auth.WebAuthn.Enabled {
		c.Auth.WebAuthn.RPDisplayName = strings.TrimSpace(c.Auth.WebAuthn.RPDisplayName)
		c.Auth.WebAuthn.RPID = strings.ToLower(strings.TrimSpace(c.Auth.WebAuthn.RPID))
		c.Auth.WebAuthn.RPOrigins = normalizeStringSlice(c.Auth.WebAuthn.RPOrigins)
		if c.Auth.WebAuthn.RPDisplayName == "" {
			return fmt.Errorf("auth.webauthn.rp_display_name is required when passkeys are enabled")
		}
		if c.Auth.WebAuthn.RPID == "" {
			return fmt.Errorf("auth.webauthn.rp_id is required when passkeys are enabled")
		}
		if strings.Contains(c.Auth.WebAuthn.RPID, "://") || strings.ContainsAny(c.Auth.WebAuthn.RPID, "/:") {
			return fmt.Errorf("auth.webauthn.rp_id must be a domain without scheme, port, or path")
		}
		if len(c.Auth.WebAuthn.RPOrigins) == 0 {
			return fmt.Errorf("auth.webauthn.rp_origins must contain at least one origin when passkeys are enabled")
		}
		for i, origin := range c.Auth.WebAuthn.RPOrigins {
			u, err := url.Parse(origin)
			if err != nil || u.Scheme == "" || u.Host == "" {
				return fmt.Errorf("auth.webauthn.rp_origins contains invalid origin %q", origin)
			}
			if u.User != nil || u.RawQuery != "" || u.Fragment != "" || u.Path != "" {
				return fmt.Errorf("auth.webauthn.rp_origins entry %q must not include userinfo, path, query, or fragment", origin)
			}
			u.Scheme = strings.ToLower(u.Scheme)
			u.Host = strings.ToLower(u.Host)
			host := strings.ToLower(u.Hostname())
			localDevelopment := host == "localhost" || host == "127.0.0.1" || host == "::1"
			// Passkey origins require HTTPS in the deployment rule. Permit HTTP for the
			// exact localhost and loopback host forms so a local development relying
			// party can use a plain HTTP origin.
			if u.Scheme != "https" && (u.Scheme != "http" || !localDevelopment) {
				return fmt.Errorf("auth.webauthn.rp_origins entry %q must use HTTPS (HTTP is allowed only for localhost)", origin)
			}
			if host != c.Auth.WebAuthn.RPID && !strings.HasSuffix(host, "."+c.Auth.WebAuthn.RPID) {
				return fmt.Errorf("auth.webauthn.rp_origins entry %q is not within relying party ID %q", origin, c.Auth.WebAuthn.RPID)
			}
			c.Auth.WebAuthn.RPOrigins[i] = u.Scheme + "://" + u.Host
		}
	}
	return nil
}

// validateOIDC checks enabled login credentials, scopes, and endpoints in their
// startup order. Disabled settings pass unchanged. Validation reads trimmed
// views of strings, keeps the configured values, and emits HTTP warnings only
// after all OIDC checks succeed.
func (c *Config) validateOIDC() error {
	if c.Auth.OAuth.OIDC.Enabled {
		if strings.TrimSpace(c.Auth.OAuth.OIDC.ClientID) == "" {
			return fmt.Errorf("auth.oauth.oidc.client_id is required when auth.oauth.oidc.enabled=true")
		}
		if strings.TrimSpace(c.Auth.OAuth.OIDC.IssuerURL) == "" {
			return fmt.Errorf("auth.oauth.oidc.issuer_url is required when auth.oauth.oidc.enabled=true")
		}
		if strings.TrimSpace(c.Auth.OAuth.OIDC.RedirectURL) == "" {
			return fmt.Errorf("auth.oauth.oidc.redirect_url is required when auth.oauth.oidc.enabled=true")
		}
		if strings.TrimSpace(c.Auth.OAuth.OIDC.FrontendRedirectURL) == "" {
			return fmt.Errorf("auth.oauth.oidc.frontend_redirect_url is required when auth.oauth.oidc.enabled=true")
		}
		if !scopeContainsOpenID(c.Auth.OAuth.OIDC.Scopes) {
			return fmt.Errorf("auth.oauth.oidc.scopes must contain openid")
		}

		method := strings.ToLower(strings.TrimSpace(c.Auth.OAuth.OIDC.TokenAuthMethod))
		switch method {
		case "", "client_secret_post", "client_secret_basic", "none":
		default:
			return fmt.Errorf("auth.oauth.oidc.token_auth_method must be one of: client_secret_post/client_secret_basic/none")
		}
		if (method == "" || method == "client_secret_post" || method == "client_secret_basic") &&
			strings.TrimSpace(c.Auth.OAuth.OIDC.ClientSecret) == "" {
			return fmt.Errorf("auth.oauth.oidc.client_secret is required when auth.oauth.oidc.enabled=true and token_auth_method is client_secret_post/client_secret_basic")
		}
		// Enabled OIDC bounds this tolerance even when ID-token validation is
		// disabled.
		if c.Auth.OAuth.OIDC.ClockSkewSeconds < 0 || c.Auth.OAuth.OIDC.ClockSkewSeconds > 600 {
			return fmt.Errorf("auth.oauth.oidc.clock_skew_seconds must be between 0 and 600")
		}
		if c.Auth.OAuth.OIDC.ValidateIDToken && strings.TrimSpace(c.Auth.OAuth.OIDC.AllowedSigningAlgs) == "" {
			return fmt.Errorf("auth.oauth.oidc.allowed_signing_algs is required when auth.oauth.oidc.validate_id_token=true")
		}

		if err := ValidateAbsoluteHTTPURL(c.Auth.OAuth.OIDC.IssuerURL); err != nil {
			return fmt.Errorf("auth.oauth.oidc.issuer_url invalid: %w", err)
		}
		if v := strings.TrimSpace(c.Auth.OAuth.OIDC.DiscoveryURL); v != "" {
			if err := ValidateAbsoluteHTTPURL(v); err != nil {
				return fmt.Errorf("auth.oauth.oidc.discovery_url invalid: %w", err)
			}
		}
		if v := strings.TrimSpace(c.Auth.OAuth.OIDC.AuthorizeURL); v != "" {
			if err := ValidateAbsoluteHTTPURL(v); err != nil {
				return fmt.Errorf("auth.oauth.oidc.authorize_url invalid: %w", err)
			}
		}
		if v := strings.TrimSpace(c.Auth.OAuth.OIDC.TokenURL); v != "" {
			if err := ValidateAbsoluteHTTPURL(v); err != nil {
				return fmt.Errorf("auth.oauth.oidc.token_url invalid: %w", err)
			}
		}
		if v := strings.TrimSpace(c.Auth.OAuth.OIDC.UserInfoURL); v != "" {
			if err := ValidateAbsoluteHTTPURL(v); err != nil {
				return fmt.Errorf("auth.oauth.oidc.userinfo_url invalid: %w", err)
			}
		}
		if v := strings.TrimSpace(c.Auth.OAuth.OIDC.JWKSURL); v != "" {
			if err := ValidateAbsoluteHTTPURL(v); err != nil {
				return fmt.Errorf("auth.oauth.oidc.jwks_url invalid: %w", err)
			}
		}
		if err := ValidateAbsoluteHTTPURL(c.Auth.OAuth.OIDC.RedirectURL); err != nil {
			return fmt.Errorf("auth.oauth.oidc.redirect_url invalid: %w", err)
		}
		if err := ValidateFrontendRedirectURL(c.Auth.OAuth.OIDC.FrontendRedirectURL); err != nil {
			return fmt.Errorf("auth.oauth.oidc.frontend_redirect_url invalid: %w", err)
		}

		warnIfInsecureURL("auth.oauth.oidc.issuer_url", c.Auth.OAuth.OIDC.IssuerURL)
		warnIfInsecureURL("auth.oauth.oidc.discovery_url", c.Auth.OAuth.OIDC.DiscoveryURL)
		warnIfInsecureURL("auth.oauth.oidc.authorize_url", c.Auth.OAuth.OIDC.AuthorizeURL)
		warnIfInsecureURL("auth.oauth.oidc.token_url", c.Auth.OAuth.OIDC.TokenURL)
		warnIfInsecureURL("auth.oauth.oidc.userinfo_url", c.Auth.OAuth.OIDC.UserInfoURL)
		warnIfInsecureURL("auth.oauth.oidc.jwks_url", c.Auth.OAuth.OIDC.JWKSURL)
		warnIfInsecureURL("auth.oauth.oidc.redirect_url", c.Auth.OAuth.OIDC.RedirectURL)
		warnIfInsecureURL("auth.oauth.oidc.frontend_redirect_url", c.Auth.OAuth.OIDC.FrontendRedirectURL)
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

// ValidateDingTalkConfig delegates DingTalk deployment rule checking to
// identity authconfig. Disabled settings are accepted, and recognized mode/type
// failures retain shared sentinel identity for callers using errors.Is across
// this facade.
func ValidateDingTalkConfig(value DingTalkOAuthConfig) error {
	return authconfig.ValidateDingTalkConfig(value.IdentityDefaults())
}
