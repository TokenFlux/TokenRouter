package config

import (
	"fmt"
	"log/slog"
	"math"
	"strings"
	"time"

	schedulerpolicy "github.com/TokenFlux/TokenRouter/internal/scheduler/policy"
)

// gateway.go defines AI gateway forwarding, transport pooling, OpenAI Responses
// WebSocket/Live, provider refresh/cooldown, scheduler scoring, and completion
// record-queue configuration types, normalizers, and read-only validators.
// Upfront normalization trims response-header lists and Codex template paths
// and defaults nonpositive Live session caps to one hour before validation
// checks coupled pool, timeout, EWMA, and autoscaling bounds.

const (
	// These policy strings select how completion work handles failed queue admission.
	// Inline execution, percentage selection, or dropped work have different
	// recorded outcomes.
	UsageRecordOverflowPolicyDrop   = "drop"   // discarded overflow work mode
	UsageRecordOverflowPolicySample = "sample" // percentage inline overflow mode
	UsageRecordOverflowPolicySync   = "sync"   // inline overflow execution mode

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

	// These modes choose immediate rejection or queue waiting when separate image
	// admission reaches its process request capacity in the image limiter.
	ImageConcurrencyOverflowModeReject = "reject" // immediate image-slot rejection mode
	ImageConcurrencyOverflowModeWait   = "wait"   // queued image-slot admission mode

	// DefaultOpenAIWSClientFirstMessageTimeoutSeconds supplies the deployment ingress
	// budget for the first client message before a Responses WebSocket turn begins.
	DefaultOpenAIWSClientFirstMessageTimeoutSeconds = 30
)

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

// GatewayAnthropicConfig supplies Anthropic beta-header and user-message-queue process defaults.
type GatewayAnthropicConfig struct {
	// Add required anthropic-beta values for API-key providers when absent from
	// the client; disabled by default.
	InjectBetaForAPIKey bool                   `mapstructure:"inject_beta_for_api_key"`
	UserMessageQueue    UserMessageQueueConfig `mapstructure:"user_message_queue"` // gateway anthropic user message queue settings
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

// GatewayGrokConfig supplies the response-header wait budget for Grok gateway requests.
type GatewayGrokConfig struct {
	// ResponseHeaderTimeoutSeconds bounds xAI/Grok response-header waits in seconds.
	// With configured transport options, zero disables this deadline; only absent
	// options select the transport's 120-second Grok default. The generic gateway
	// header timeout is replaced during Grok profile selection.
	ResponseHeaderTimeoutSeconds int `mapstructure:"response_header_timeout_seconds"`
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

// GatewayLiveConfig supplies the hard session duration used by Frameless Live
// forwarding.
type GatewayLiveConfig struct {
	// MaxSessionDurationSeconds supplies the Live session cap in seconds. Validate
	// replaces a nonpositive value with one hour before application construction
	// passes it to Live forwarding.
	MaxSessionDurationSeconds int `mapstructure:"max_session_duration_seconds"`
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

// UpstreamErrorBodyLogConfig controls whether bounded upstream error bodies enter diagnostic logs.
type UpstreamErrorBodyLogConfig struct {
	// Log summaries of upstream error response bodies.
	Enabled bool `mapstructure:"enabled"`
	// Maximum logged upstream error-body bytes; longer bodies are truncated.
	MaxBytes int `mapstructure:"max_bytes"`
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

// ProviderGrokOAuthConfig supplies explicit Grok OAuth endpoint, client, scope, and callback overrides.
type ProviderGrokOAuthConfig struct {
	AuthorizeURL string `mapstructure:"authorize_url"` // Grok OAuth authorize url override.
	ClientID     string `mapstructure:"client_id"`     // Grok OAuth client id override.
	RedirectURI  string `mapstructure:"redirect_uri"`  // Grok OAuth redirect uri override.
	Scope        string `mapstructure:"scope"`         // Grok OAuth scope override.
	TokenURL     string `mapstructure:"token_url"`     // Grok OAuth token url override.
}

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

// normalizeGateway trims response-header lists and Codex template path text and
// supplies the one-hour default when Live session duration is nonpositive.
func (c *Config) normalizeGateway() {
	c.Gateway.ResponseHeaders.AdditionalAllowed = normalizeStringSlice(c.Gateway.ResponseHeaders.AdditionalAllowed)
	c.Gateway.ResponseHeaders.ForceRemove = normalizeStringSlice(c.Gateway.ResponseHeaders.ForceRemove)
	c.Gateway.OpenAI.Codex.ForcedInstructionsTemplateFile = strings.TrimSpace(c.Gateway.OpenAI.Codex.ForcedInstructionsTemplateFile)
	if c.Gateway.OpenAI.Live.MaxSessionDurationSeconds <= 0 {
		c.Gateway.OpenAI.Live.MaxSessionDurationSeconds = 3600
	}
}

// validateGatewayEarlyEnums checks Anthropic user-message-queue mode and
// Antigravity cooldown and endpoint enumerations at the start of validation.
func (c *Config) validateGatewayEarlyEnums() error {
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
	return nil
}

// validateGeminiOAuthPair checks that Gemini OAuth client ID and client secret
// are either both set or both empty.
func (c *Config) validateGeminiOAuthPair() error {
	// Gemini client selection uses a credential pair. Reject a partial trimmed
	// pair before consumers select configured credentials or their default client
	// choice, keeping the two source members together.
	geminiClientID := strings.TrimSpace(c.Provider.Gemini.OAuth.ClientID)
	geminiClientSecret := strings.TrimSpace(c.Provider.Gemini.OAuth.ClientSecret)
	if (geminiClientID == "") != (geminiClientSecret == "") {
		return fmt.Errorf("provider.gemini.oauth.client_id and provider.gemini.oauth.client_secret must be both set or both empty")
	}
	return nil
}

// validateGateway checks gateway transport, scheduler, and completion limits
// without mutating receiver fields. Coupled pool bounds, finite scoring sums,
// and enabled autoscaling constraints are checked together so their consumers
// receive consistent limits.
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

// validateStreamingWaitPing checks that gateway.streaming.wait_ping_interval_seconds
// lies within 5..30 seconds.
func (c *Config) validateStreamingWaitPing() error {
	// Concurrency waiting sends SSE keepalives at this cadence before a request
	// obtains its slot.
	if c.Gateway.Streaming.WaitPingIntervalSeconds < 5 || c.Gateway.Streaming.WaitPingIntervalSeconds > 30 {
		return fmt.Errorf("gateway.streaming.wait_ping_interval_seconds must be between 5-30 seconds")
	}
	return nil
}

// validateGrokFreeQuota checks rolling Grok free-tier quota settings.
func (c *Config) validateGrokFreeQuota() error {
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
	return nil
}
