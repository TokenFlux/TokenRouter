package provider

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"

	"github.com/TokenFlux/TokenRouter/internal/egress"
)

// usage_identity.go computes usage-query identities from provider credentials
// and connection settings.
// Normal JSON digest input includes query choices, proxy credentials, and TLS
// settings so changes to those inputs select another shared query. usage monitor
// snapshots reuse the digest to reject observations from an earlier identity.
// When JSON encoding fails, the reduced input contains provider ID, platform,
// adapter, and formatted credentials; connection settings are omitted on that
// path.
//
// Ollama browser usage groups use a narrower identity: the fixed host and API
// key. Providers with that same key can share the managed browser observation
// despite different provider IDs. These digest inputs contain sensitive
// authentication data, so callers use the resulting hashes as identifiers and
// keep source records stable during construction.

// UpstreamUsageContextFingerprint hashes the provider, query configuration and
// selected query root used to identify shared work. Nil input returns the
// literal nil marker. Keep provider credentials, proxy and transport settings
// stable while the input is encoded.
//
// The JSON input includes proxy credentials and selected TLS fields. If JSON
// encoding fails, a reduced formatted input containing ID, platform, adapter and
// credentials is hashed instead; that exceptional result does not retain every
// connection field used by normal encoding.
func UpstreamUsageContextFingerprint(provider *Record, config UpstreamUsageQueryConfig, baseURL string) string {
	if provider == nil {
		return "nil"
	}
	payload := struct {
		ID       int64  // observed provider database ID
		Platform string // observed provider platform name
		Type     string // observed provider authentication type
		// Credentials borrows the provider credential map during JSON encoding.
		// Callers keep its entries and nested values stable until the digest input
		// has been encoded.
		Credentials map[string]any           // observed provider credential object
		Config      UpstreamUsageQueryConfig // normalized usage query choices
		BaseURL     string                   // resolved usage query root
		// ProxyID borrows the provider's selected proxy pointer. Keep its pointed
		// value stable until JSON has captured that connection choice.
		ProxyID     *int64 // selected provider proxy ID
		Proxy       any    // hydrated selected proxy metadata
		Concurrency int    // configured provider concurrency limit
		// Transport collects three TLS Extra values in a new map. Mutable values
		// returned by usageExtraValue remain borrowed from the provider until JSON
		// encoding finishes.
		Transport map[string]any // selected TLS configuration values
	}{
		ID:          provider.ID,
		Platform:    provider.Platform,
		Type:        provider.Type,
		Credentials: provider.Credentials,
		Config:      config,
		BaseURL:     baseURL,
		ProxyID:     provider.ProxyID,
		Concurrency: provider.Concurrency,
		Transport: map[string]any{
			"enable_tls_fingerprint":     usageExtraValue(provider.Extra, "enable_tls_fingerprint"),
			"tls_fingerprint_profile_id": usageExtraValue(provider.Extra, "tls_fingerprint_profile_id"),
			"tls_fingerprint_router_id":  usageExtraValue(provider.Extra, "tls_fingerprint_router_id"),
		},
	}
	if provider.Proxy != nil {
		// Include hydrated proxy credentials in the normal digest input so queries
		// using replacement proxy authentication cannot reuse an earlier shared
		// result.
		payload.Proxy = struct {
			ID       int64  // selected proxy database ID
			Protocol string // proxy connection protocol name
			Host     string // proxy connection host name
			Port     int    // proxy connection port number
			Username string // proxy authentication username value
			Password string // proxy authentication password value
			Status   string // proxy connection status value
		}{provider.Proxy.ID, provider.Proxy.Protocol, provider.Proxy.Host, provider.Proxy.Port, provider.Proxy.Username, provider.Proxy.Password, provider.Proxy.Status}
	}
	// If the complete input cannot be JSON encoded, use a reduced formatted
	// identity. That path omits proxy and other connection fields included by the
	// normal digest.
	data, err := json.Marshal(payload)
	if err != nil {
		data = []byte(fmt.Sprintf("%d:%s:%s:%v", provider.ID, provider.Platform, config.Adapter, provider.Credentials))
	}
	hash := sha256.Sum256(data)
	return hex.EncodeToString(hash[:])
}

// UsageMonitorIdentityFingerprint hashes the query identity for a
// monitoring-enabled API-key record. Lightweight scheduler metadata records
// return their precomputed UsageMonitorIdentityHint; invalid query
// configuration or an unavailable adapter returns empty, leaving snapshot
// consumers without a trusted identity for that configuration.
func UsageMonitorIdentityFingerprint(provider *Record) string {
	if provider == nil || !provider.SupportsUsageMonitoring() || provider.Type != ProviderTypeAPIKey {
		return ""
	}
	if provider.UsageMonitorIdentityHint != "" {
		return provider.UsageMonitorIdentityHint
	}
	queryConfig, err := EffectiveUpstreamUsageConfig(provider)
	// Configuration merging compares this identity to decide whether a Kimi,
	// Zhipu, or DeepSeek snapshot still belongs to the record. Query-validation
	// failure therefore supplies the empty identity marker.
	if err != nil {
		return ""
	}
	if queryConfig.Adapter == "" {
		return ""
	}
	return UpstreamUsageContextFingerprint(provider, queryConfig, provider.OpenAIBaseURL(provider.ConfiguredAPIProtocol() == APIProtocolAdaptive))
}

// IsOllamaCloudUsageProvider reports whether an OpenAI or Anthropic API Key
// record targets an Ollama Cloud base URL. Nil, other provider types and other
// platforms return false.
func IsOllamaCloudUsageProvider(provider *Record) bool {
	if provider == nil || provider.Type != ProviderTypeAPIKey || (provider.Platform != PlatformOpenAI && provider.Platform != PlatformAnthropic) {
		return false
	}
	baseURL, _ := provider.Credentials["base_url"].(string)
	return egress.IsOllamaCloudBaseURL(baseURL)
}

// OllamaCloudUsageIdentity returns a new host/API-key map for a supported Ollama
// Cloud provider. Missing or empty string API keys return nil; the map contains
// sensitive credentials for internal identity comparison.
func OllamaCloudUsageIdentity(provider *Record) map[string]any {
	if !IsOllamaCloudUsageProvider(provider) {
		return nil
	}
	apiKey, ok := provider.Credentials["api_key"].(string)
	if !ok || apiKey == "" {
		return nil
	}
	return map[string]any{"host": "ollama.com", "api_key": apiKey}
}

// OllamaCloudUsageGroupFingerprint hashes the fixed Ollama host and exact API
// key for browser-usage grouping. Unsupported identities return empty with
// false, allowing providers with the same usable key to share one group.
func OllamaCloudUsageGroupFingerprint(provider *Record) (string, bool) {
	identity := OllamaCloudUsageIdentity(provider)
	if identity == nil {
		return "", false
	}
	apiKey, _ := identity["api_key"].(string)
	sum := sha256.Sum256([]byte("ollama.com\x00" + apiKey))
	return hex.EncodeToString(sum[:]), true
}

// usageExtraValue reads a named setting from Extra, returning nil when its
// map or key is absent. Mutable results remain borrowed; their owners keep them
// stable until fingerprint encoding finishes.
func usageExtraValue(extra map[string]any, key string) any { return extra[key] }
