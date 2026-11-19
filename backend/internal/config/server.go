package config

import (
	"fmt"
	"log/slog"
	"net/url"
	"strings"
	"sync/atomic"

	"github.com/TokenFlux/TokenRouter/internal/identity/authconfig"
	ippolicy "github.com/TokenFlux/TokenRouter/internal/server/clientip/policy"
	"github.com/TokenFlux/TokenRouter/internal/server/httpconfig"
)

// server.go defines HTTP listener, client-IP forwarding, browser security,
// logging, egress probe, and auxiliary proxy configuration types and checks.
// Upfront normalization canonicalizes valid forwarded-IP header lists and
// proxy-probe URLs while leaving malformed values untouched so read-only
// startup validation still reports configuration errors deterministically.
//
// Live forwarded-IP settings are published through an atomic pointer snapshot
// so concurrent request middleware observes a consistent trust flag and header
// slice when runtime settings update forwarded-IP policy after startup.

const (
	// DefaultCSPPolicy is the default Content-Security-Policy.
	// SecurityHeaders replaces __CSP_NONCE__ with a per-request nonce.
	DefaultCSPPolicy = httpconfig.DefaultCSPPolicy

	// MaxForwardedClientIPHeaders shares the discovery-source limit with the policy
	// normalizer used by both process configuration and persisted runtime settings.
	MaxForwardedClientIPHeaders = ippolicy.MaxForwardedClientIPHeaders
)

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

// ForwardedClientIPSettings pairs one live forwarding trust flag with its
// ordered header names.
type ForwardedClientIPSettings struct {
	// TrustForwardedIP and Headers form one policy observation. Config publishes
	// them together in an immutable object and returns a copied header list so
	// readers can retain the pair independently.
	TrustForwardedIP bool     // raw forwarding-header trust switch
	Headers          []string // ordered client-address header names
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

// CSPConfig aliases the HTTP content-security-policy values used by server
// response middleware.
type CSPConfig = httpconfig.CSPConfig

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

// SiteConfig supplies the public-site URL fallback used by application link builders.
type SiteConfig struct {
	FrontendURL string `mapstructure:"frontend_url"` // Default frontend address for email links and MCP OAuth.
}

// StorageConfig supplies the application data directory used by catalog and plugin fallbacks.
type StorageConfig struct {
	Dir string `mapstructure:"dir"` // Local data directory.
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

// DebugConfig selects optional routing, request-body, and Claude identity diagnostics.
type DebugConfig struct {
	ClaudeMimicLog     bool   `mapstructure:"claude_mimic_log"`      // Write Claude mimic request diagnostics.
	GatewayBodyLogFile string `mapstructure:"gateway_body_log_file"` // Gateway request body diagnostic file path.
	ModelRoutingLog    bool   `mapstructure:"model_routing_log"`     // Write detailed model routing diagnostics.
}

// UpdateConfig supplies GitHub authentication for the application release-check client.
type UpdateConfig struct {
	GitHubToken string `mapstructure:"github_token"` // GitHub release download authentication token.
}

// EgressConfig groups proxy-exit probes and the optional TLS fingerprint collector listener.
type EgressConfig struct {
	ProxyProbe              EgressProxyProbeConfig        `mapstructure:"proxy_probe"`               // egress proxy probe settings
	TLSFingerprintCollector TLSFingerprintCollectorConfig `mapstructure:"tls_fingerprint_collector"` // egress tls fingerprint collector settings
}

// EgressProxyProbeConfig supplies startup configuration for its application consumers and validators.
type EgressProxyProbeConfig struct {
	// Proxy-probe response limit, in bytes.
	ResponseMaxBytes int64 `mapstructure:"response_max_bytes"`
	// URLs lists probe endpoints in priority order; empty uses the built-in list.
	URLs []ProbeURLConfig `mapstructure:"urls"`
}

// ProbeURLConfig describes a proxy-egress probe endpoint and its response parser.
type ProbeURLConfig struct {
	URL    string `mapstructure:"url"`    // proxy probe endpoint URL
	Parser string `mapstructure:"parser"` // ip-api, ipify, or chatgpt-trace parser
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

// Address formats the nonnil server settings as host text followed by a colon
// and port. It performs no validation or IPv6 bracketing, so callers supply
// host spelling accepted by their listener.
func (s *ServerConfig) Address() string {
	return fmt.Sprintf("%s:%d", s.Host, s.Port)
}

// normalizeServer trims listener mode, site URL, CORS origins, CSP policy, log
// settings, and environment labels, and publishes normalized forwarded-IP
// headers or proxy-probe URLs when their inputs are valid.
func (c *Config) normalizeServer() {
	c.Server.Mode = strings.ToLower(strings.TrimSpace(c.Server.Mode))
	c.Site.FrontendURL = strings.TrimSpace(c.Site.FrontendURL)
	c.Security.CORS.AllowedOrigins = normalizeStringSlice(c.Security.CORS.AllowedOrigins)
	c.Security.CSP.Policy = strings.TrimSpace(c.Security.CSP.Policy)
	if forwardedClientIPHeaders, err := NormalizeForwardedClientIPHeaders(c.Server.ClientIP.ForwardedHeaders); err == nil {
		c.Server.ClientIP.ForwardedHeaders = forwardedClientIPHeaders
		c.SetForwardedClientIPSettings(c.Server.ClientIP.TrustForwardedHeaders, forwardedClientIPHeaders)
	}
	if proxyProbeURLs, err := normalizeProxyProbeURLs(c.Egress.ProxyProbe.URLs); err == nil {
		c.Egress.ProxyProbe.URLs = proxyProbeURLs
	}
	c.Log.Level = strings.ToLower(strings.TrimSpace(c.Log.Level))
	c.Log.Format = strings.ToLower(strings.TrimSpace(c.Log.Format))
	c.Log.ServiceName = strings.TrimSpace(c.Log.ServiceName)
	c.Environment = strings.TrimSpace(c.Environment)
	c.Log.StacktraceLevel = strings.ToLower(strings.TrimSpace(c.Log.StacktraceLevel))
	c.Log.Output.FilePath = strings.TrimSpace(c.Log.Output.FilePath)
}

// validateServerMode checks that server.mode is debug or release.
func (c *Config) validateServerMode() error {
	if c.Server.Mode != "debug" && c.Server.Mode != "release" {
		return fmt.Errorf("server.mode must be debug or release")
	}
	return nil
}

// validateServerIngress checks forwarded-IP headers, proxy-probe URLs, HTTP
// listener timeouts and body limits, and enabled H2C protocol bounds.
func (c *Config) validateServerIngress() error {
	if _, err := NormalizeForwardedClientIPHeaders(c.Server.ClientIP.ForwardedHeaders); err != nil {
		return fmt.Errorf("server.client_ip.forwarded_headers: %w", err)
	}
	if _, err := normalizeProxyProbeURLs(c.Egress.ProxyProbe.URLs); err != nil {
		return fmt.Errorf("egress.proxy_probe.urls: %w", err)
	}
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
	return nil
}

// validateFrontendURL checks that an optional configured site.frontend_url is
// an absolute HTTP(S) URL without query parameters or userinfo.
func (c *Config) validateFrontendURL() error {
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
	return nil
}

// validateCSP checks that a non-empty policy exists when CSP is enabled.
func (c *Config) validateCSP() error {
	if c.Security.CSP.Enabled && strings.TrimSpace(c.Security.CSP.Policy) == "" {
		return fmt.Errorf("security.csp.policy is required when CSP is enabled")
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
