package provider

import (
	"encoding/json"
	"net/url"
	"strings"
)

// record_endpoints.go resolves provider endpoint roots, passthrough flags,
// client access policies, TLS fingerprint settings, and session overrides.
// Forwarding paths combine credential base URLs with Extra transport options
// whose eligibility depends on platform and authentication type. Current keys
// take precedence over compatibility aliases so explicit false or unrestricted
// policy values cannot be overridden by stale legacy flags.
//
// TLS emulation, custom relay roots, session masking, and cache TTL overrides
// each check provider type eligibility before reading stored booleans or IDs.
// URL helpers normalize suffixes without performing outbound policy validation,
// leaving egress checks to request senders.

const (
	// DefaultJevBaseURL supplies the SystemOne service root when a provider has
	// no configured endpoint override; operation builders append their request path.
	DefaultJevBaseURL = "https://api.typesafe.ai"

	// OpenAI OAuth policy values select unrestricted clients, Codex identities,
	// or a TLS-router match after administrator and legacy settings are resolved.
	// OpenAIOAuthClientPolicyAny allows any client to use the OpenAI OAuth provider.
	OpenAIOAuthClientPolicyAny = "any"
	// OpenAIOAuthClientPolicyCodexOnly restricts access to official Codex clients.
	OpenAIOAuthClientPolicyCodexOnly = "codex_only"
	// OpenAIOAuthClientPolicyTLSRouterMatchedOnly requires a UA matched by the TLS
	// router.
	OpenAIOAuthClientPolicyTLSRouterMatchedOnly = "tls_router_matched_only"
)

// GetBaseURL returns the saved base_url for an API Key record. An empty value
// selects the Anthropic default endpoint, and other provider types return
// empty; the accessor does not validate or trim a supplied URL.
func (r *Record) GetBaseURL() string {
	if r.Type != ProviderTypeAPIKey {
		return ""
	}
	baseURL := r.GetCredential("base_url")
	if baseURL == "" {
		return "https://api.anthropic.com"
	}
	return baseURL
}

// GetGeminiBaseURL returns the trimmed saved endpoint or the supplied default
// URL. The supplied default is used when the stored base is missing or
// whitespace only; protocol-specific service endpoints are selected by
// ProtocolTarget.BaseURL before constructing a request.
func (r *Record) GetGeminiBaseURL(defaultBaseURL string) string {
	baseURL := strings.TrimSpace(r.GetCredential("base_url"))
	if baseURL == "" {
		return defaultBaseURL
	}
	return baseURL
}

// StripAnthropicPathSuffix trims a relay address and removes a final decoded
// anthropic path segment when present. Parse errors return the trimmed input;
// callers still apply the request path's transport validation to the result.
//
// Removing the suffix also clears RawPath, and URL.String escapes the resulting
// decoded path while carrying the parsed query and fragment. Trailing slashes
// are trimmed from the entire input and output strings, including one at the
// end of query or fragment text. A caller needing byte-for-byte retention of
// those suffixes therefore keeps its original URL instead of using this
// compatibility rewrite.
func StripAnthropicPathSuffix(baseURL string) string {
	trimmed := strings.TrimRight(strings.TrimSpace(baseURL), "/")
	parsed, err := url.Parse(trimmed)
	// Malformed compatibility input retains its trimmed text for the egress
	// validator. A parse failure here cannot establish a safe scheme or host for
	// the caller's eventual request.
	if err != nil {
		return trimmed
	}
	// Edit the decoded path and discard its encoded hint together. URL.String
	// then derives an escaped path from that modified value; the final whole-URL
	// trim still applies to any trailing query or fragment slash.
	path := strings.TrimRight(parsed.Path, "/")
	if path == "/anthropic" {
		parsed.Path = ""
		parsed.RawPath = ""
	} else if before, ok := strings.CutSuffix(path, "/anthropic"); ok {
		parsed.Path = before
		parsed.RawPath = ""
	}
	return strings.TrimRight(parsed.String(), "/")
}

// GetClaudeUserID selects the first nonblank trimmed Claude identity hint.
// Extra claude_user_id and anthropic_user_id take priority, followed by the
// corresponding credential keys; all missing or blank candidates produce an
// empty string.
func (r *Record) GetClaudeUserID() string {
	if v := strings.TrimSpace(r.GetExtraString("claude_user_id")); v != "" {
		return v
	}
	if v := strings.TrimSpace(r.GetExtraString("anthropic_user_id")); v != "" {
		return v
	}
	if v := strings.TrimSpace(r.GetCredential("claude_user_id")); v != "" {
		return v
	}
	if v := strings.TrimSpace(r.GetCredential("anthropic_user_id")); v != "" {
		return v
	}
	return ""
}

// IsInterceptWarmupEnabled reads the saved warmup-interception boolean from
// Credentials. Missing maps, absent values, and nonboolean representations
// return false; this accessor leaves provider eligibility and warmup
// classification with its consumers.
func (r *Record) IsInterceptWarmupEnabled() bool {
	if r.Credentials == nil {
		return false
	}
	if v, ok := r.Credentials["intercept_warmup_requests"]; ok {
		if enabled, ok := v.(bool); ok {
			return enabled
		}
	}
	return false
}

// IsOpenAIPassthroughEnabled reads the OpenAI passthrough selection used by
// forwarding. A typed openai_passthrough value takes priority, including false;
// otherwise the legacy openai_oauth_passthrough boolean is used, with absent or
// unusable input selecting false.
func (r *Record) IsOpenAIPassthroughEnabled() bool {
	if r == nil || !r.IsOpenAI() || r.Extra == nil {
		return false
	}
	if enabled, ok := r.Extra["openai_passthrough"].(bool); ok {
		return enabled
	}
	if enabled, ok := r.Extra["openai_oauth_passthrough"].(bool); ok {
		return enabled
	}
	return false
}

// IsOpenAIResponsesFlattenNamespacesEnabled reads the namespace-flattening
// opt-in for OpenAI OAuth. Nil records, other types, missing configuration, and
// nonboolean values return false, leaving namespace encoding with the standard
// forwarding path.
func (r *Record) IsOpenAIResponsesFlattenNamespacesEnabled() bool {
	if r == nil || !r.IsOpenAIOAuth() || r.Extra == nil {
		return false
	}
	enabled, ok := r.Extra["openai_responses_flatten_namespaces"].(bool)
	return ok && enabled
}

// IsOpenAIWSAllowStoreRecoveryEnabled reads the OpenAI WebSocket store-recovery
// opt-in from Extra. Nil records, other platforms, missing configuration, and
// nonboolean values return false before the consumer applies its recovery
// handling.
func (r *Record) IsOpenAIWSAllowStoreRecoveryEnabled() bool {
	if r == nil || !r.IsOpenAI() || r.Extra == nil {
		return false
	}
	enabled, ok := r.Extra["openai_ws_allow_store_recovery"].(bool)
	return ok && enabled
}

// IsOpenAIOAuthPassthroughEnabled restricts the shared passthrough setting to
// OpenAI OAuth records. Nil records and other provider kinds return false;
// eligible records use the current setting before its legacy alias.
func (r *Record) IsOpenAIOAuthPassthroughEnabled() bool {
	return r != nil && r.IsOpenAIOAuth() && r.IsOpenAIPassthroughEnabled()
}

// IsAnthropicAPIKeyPassthroughEnabled reads passthrough for Anthropic API Key
// forwarding. Nil records, other platform/type combinations, missing
// configuration, and nonboolean values return false, so the option needs both
// eligibility and a true boolean.
func (r *Record) IsAnthropicAPIKeyPassthroughEnabled() bool {
	if r == nil || r.Platform != PlatformAnthropic || r.Type != ProviderTypeAPIKey || r.Extra == nil {
		return false
	}
	enabled, ok := r.Extra["anthropic_passthrough"].(bool)
	return ok && enabled
}

// IsCodexCLIOnlyEnabled reports whether the resolved OpenAI OAuth client policy
// selects Codex.
func (r *Record) IsCodexCLIOnlyEnabled() bool {
	return r.GetOpenAIOAuthClientPolicy() == OpenAIOAuthClientPolicyCodexOnly
}

// GetOpenAIOAuthClientPolicy selects the recognized OpenAI OAuth client access
// policy. A trimmed supported string takes priority over the legacy
// codex_cli_only flag, including the unrestricted any value.
//
// If the string is absent, invalid, or differently cased, a true legacy boolean
// selects codex_only. Other cases use any, including nil records and providers
// outside OpenAI OAuth. Callers separately enforce preset client identities or
// a TLS-router match according to this returned policy.
func (r *Record) GetOpenAIOAuthClientPolicy() string {
	if r == nil || !r.IsOpenAIOAuth() || r.Extra == nil {
		return OpenAIOAuthClientPolicyAny
	}
	if policy, ok := r.Extra["openai_oauth_client_policy"].(string); ok {
		switch strings.TrimSpace(policy) {
		case OpenAIOAuthClientPolicyCodexOnly:
			return OpenAIOAuthClientPolicyCodexOnly
		case OpenAIOAuthClientPolicyTLSRouterMatchedOnly:
			return OpenAIOAuthClientPolicyTLSRouterMatchedOnly
		case OpenAIOAuthClientPolicyAny:
			return OpenAIOAuthClientPolicyAny
		}
	}
	enabled, ok := r.Extra["codex_cli_only"].(bool)
	if ok && enabled {
		return OpenAIOAuthClientPolicyCodexOnly
	}
	return OpenAIOAuthClientPolicyAny
}

// IsOpenAIOAuthTLSRouterMatchedOnly reports whether the resolved policy
// requires a TLS-router match.
func (r *Record) IsOpenAIOAuthTLSRouterMatchedOnly() bool {
	return r.GetOpenAIOAuthClientPolicy() == OpenAIOAuthClientPolicyTLSRouterMatchedOnly
}

// GetCodexCLIOnlyAllowedClients returns a new slice of preset client IDs for
// OpenAI OAuth. Supported arrays skip nonstrings and whitespace-only values
// while retaining each accepted string's original bytes; unsupported providers
// or containers return nil.
func (r *Record) GetCodexCLIOnlyAllowedClients() []string {
	if r == nil || !r.IsOpenAIOAuth() || r.Extra == nil {
		return nil
	}
	raw, ok := r.Extra["codex_cli_only_allowed_clients"]
	if !ok || raw == nil {
		return nil
	}
	switch v := raw.(type) {
	case []string:
		result := make([]string, 0, len(v))
		for _, s := range v {
			if strings.TrimSpace(s) != "" {
				result = append(result, s)
			}
		}
		return result
	case []any:
		result := make([]string, 0, len(v))
		for _, item := range v {
			if s, ok := item.(string); ok && strings.TrimSpace(s) != "" {
				result = append(result, s)
			}
		}
		return result
	}
	return nil
}

// IsAnthropicOAuthOrSetupToken identifies Anthropic OAuth and Setup Token
// records used by forwarding. Nil records return false; the type classification
// leaves credential availability and refresh eligibility to the consuming path.
func (r *Record) IsAnthropicOAuthOrSetupToken() bool {
	if r == nil {
		return false
	}
	return r.Platform == PlatformAnthropic && (r.Type == ProviderTypeOAuth || r.Type == ProviderTypeSetupToken)
}

// SupportsTLSFingerprint classifies provider types whose outbound transport can
// emulate a TLS profile. Anthropic OAuth/Setup Token, OpenAI OAuth, Kimi,
// Zhipu, and DeepSeek API Key, and Qoder COSY records qualify; nil and other
// combinations return false.
func (r *Record) SupportsTLSFingerprint() bool {
	if r == nil {
		return false
	}
	if r.IsAnthropicOAuthOrSetupToken() {
		return true
	}
	return (r.Platform == PlatformOpenAI && r.Type == ProviderTypeOAuth) ||
		(r.rules().apiKeyTLSFingerprint && r.Type == ProviderTypeAPIKey) ||
		r.IsQoderCosy()
}

// IsTLSFingerprintEnabled requires both a supported provider type and a true
// Extra boolean. Missing maps, missing keys, or nonboolean values leave
// emulation disabled even when the provider type supports it.
func (r *Record) IsTLSFingerprintEnabled() bool {
	if !r.SupportsTLSFingerprint() {
		return false
	}
	if r.Extra == nil {
		return false
	}
	if v, ok := r.Extra["enable_tls_fingerprint"]; ok {
		if enabled, ok := v.(bool); ok {
			return enabled
		}
	}
	return false
}

// GetTLSFingerprintProfileID reads a supported numeric profile ID from Extra.
// Missing or unsupported values and unparseable json.Number text return zero;
// the transport consumer treats zero as its built-in default profile.
func (r *Record) GetTLSFingerprintProfileID() int64 {
	if r.Extra == nil {
		return 0
	}
	v, ok := r.Extra["tls_fingerprint_profile_id"]
	if !ok {
		return 0
	}
	switch id := v.(type) {
	case float64:
		// JSON numeric identifiers lose fractions when converted to integer record IDs.
		return int64(id)
	case int64:
		return id
	case int:
		return int64(id)
	case json.Number:
		if i, err := id.Int64(); err == nil {
			return i
		}
	}
	// Unsupported or unreadable profile metadata uses the zero selection that
	// the transport consumer already interprets for an absent configuration value.
	return 0
}

// GetTLSFingerprintRouterID reads a supported numeric TLS-router ID from Extra.
// Missing or unsupported values and unparseable json.Number text return zero,
// indicating that no router was selected by this setting.
func (r *Record) GetTLSFingerprintRouterID() int64 {
	if r.Extra == nil {
		return 0
	}
	v, ok := r.Extra["tls_fingerprint_router_id"]
	if !ok {
		return 0
	}
	switch id := v.(type) {
	case float64:
		// JSON numeric identifiers lose fractions when converted to integer record IDs.
		return int64(id)
	case int64:
		return id
	case int:
		return int64(id)
	case json.Number:
		if i, err := id.Int64(); err == nil {
			return i
		}
	}
	// Unsupported or unreadable profile metadata uses the zero selection that
	// the transport consumer already interprets for an absent configuration value.
	return 0
}

// IsSessionIDMaskingEnabled reads the saved masking opt-in for Anthropic OAuth
// or Setup Token. Other provider kinds, missing Extra, and nonboolean values
// return false; session lifetime and renewal belong to the masking consumer.
func (r *Record) IsSessionIDMaskingEnabled() bool {
	if !r.IsAnthropicOAuthOrSetupToken() {
		return false
	}
	if r.Extra == nil {
		return false
	}
	if v, ok := r.Extra["session_id_masking_enabled"]; ok {
		if enabled, ok := v.(bool); ok {
			return enabled
		}
	}
	return false
}

// IsCustomBaseURLEnabled reads the relay endpoint opt-in for Anthropic OAuth or
// Setup Token. Other provider kinds, absent Extra, and nonboolean values return
// false before the caller considers GetCustomBaseURL.
func (r *Record) IsCustomBaseURLEnabled() bool {
	if !r.IsAnthropicOAuthOrSetupToken() {
		return false
	}
	if r.Extra == nil {
		return false
	}
	if v, ok := r.Extra["custom_base_url_enabled"]; ok {
		if enabled, ok := v.(bool); ok {
			return enabled
		}
	}
	return false
}

// GetCustomBaseURL reads the configured relay address string from provider
// Extra.
func (r *Record) GetCustomBaseURL() string {
	return r.GetExtraString("custom_base_url")
}

// IsCacheTTLOverrideEnabled reads the cache-creation TTL opt-in for Anthropic
// OAuth or Setup Token. Other provider kinds, missing configuration, and
// nonboolean values return false; the selected category comes from
// GetCacheTTLOverrideTarget.
func (r *Record) IsCacheTTLOverrideEnabled() bool {
	if !r.IsAnthropicOAuthOrSetupToken() {
		return false
	}
	if r.Extra == nil {
		return false
	}
	if v, ok := r.Extra["cache_ttl_override_enabled"]; ok {
		if enabled, ok := v.(bool); ok {
			return enabled
		}
	}
	return false
}

// GetCacheTTLOverrideTarget returns the configured cache TTL category when it
// is exactly 5m or 1h. Missing or unsupported values return 5m; enablement is
// checked separately by the caller using the category.
func (r *Record) GetCacheTTLOverrideTarget() string {
	if r.Extra == nil {
		return "5m"
	}
	if v, ok := r.Extra["cache_ttl_override_target"]; ok {
		if target, ok := v.(string); ok && (target == "5m" || target == "1h") {
			return target
		}
	}
	return "5m"
}
