package provider

import (
	"encoding/json"
	"strings"

	"github.com/TokenFlux/TokenRouter/internal/egress"
)

// usage_config.go resolves and validates provider usage-query settings and
// platform-selected adapters.
// Administrator usage settings describe a fixed adapter and optional query root,
// with credentials stored separately on the provider. Absent configuration
// inherits platform defaults, while malformed objects or unsupported choices
// return structured errors so a configured but unusable query does not silently
// adopt another format.
//
// PlatformUsageAdapter is the single rule for platforms whose platform and
// provider mode select the query format regardless of a supplied generic
// adapter name. A fixed selection without a format leaves the effective
// adapter empty and disables the query. Adapters whose management host differs
// from the forwarding host receive their default query root during resolution.
// Write normalization accepts the same small field set and creates a
// replacement object after validation. Query execution still performs its own
// provider eligibility and URL policy checks; format validation here
// establishes the shape of the saved query configuration.

// These Extra keys name query configuration and platform-maintained usage
// snapshots shared by provider administration, monitoring and usage display.
const (
	// UsageMonitorSnapshotExtraKey retains the persisted name read by existing
	// provider records and administrative clients when displaying upstream
	// usage.
	UsageMonitorSnapshotExtraKey     = "cn_usage_monitor_snapshot"
	UpstreamUsageQueryExtraKey       = "upstream_usage_query"
	OllamaCloudUsageSnapshotExtraKey = "ollama_cloud_usage_snapshot"
)

// EffectiveUpstreamUsageConfig resolves stored query choices over defaults for
// the supplied provider. Nil input uses enabled Sub2API defaults, and Jev
// defaults to disabled. A missing or nil configuration object inherits those
// defaults. Derouter and OpenCode Go use their official management roots when
// the query address is empty.
//
// Present fields require their expected types, and unknown fields are rejected.
// Providers with a platform-selected adapter keep it even when a generic
// adapter name is supplied. When their platform and mode have no query format,
// the result has an empty adapter and stays disabled even when a query-enabled
// flag is supplied. Unsupported adapters and invalid query-root formats return
// structured errors instead of replacing configured choices.
func EffectiveUpstreamUsageConfig(provider *Record) (UpstreamUsageQueryConfig, error) {
	config := UpstreamUsageQueryConfig{Enabled: provider == nil || provider.Platform != PlatformJev, Adapter: UpstreamUsageDefaultAdapter}
	platformAdapter, fixed := PlatformUsageAdapter(provider)
	if fixed {
		config.Adapter = platformAdapter
	}
	if provider != nil && provider.Extra[UpstreamUsageQueryExtraKey] != nil {
		var err error
		config, err = mergeStoredUpstreamUsageConfig(config, provider.Extra[UpstreamUsageQueryExtraKey], fixed)
		if err != nil {
			return UpstreamUsageQueryConfig{}, err
		}
	}
	// A platform mode without a query format has nothing to enable. Clearing the
	// stored flag keeps the empty adapter out of monitors and reminders, while
	// query execution reports the mode as unsupported.
	if fixed && config.Adapter == "" {
		config.Enabled = false
	} else if !IsKnownUpstreamUsageAdapter(config.Adapter) {
		return UpstreamUsageQueryConfig{}, ErrUpstreamUsageUnsupported
	}
	// Adapters whose management host differs from model forwarding declare their
	// default root in usageAdapterCatalog; resolve it here so execution and query
	// identity checks share one destination.
	if config.BaseURL == "" {
		config.BaseURL = DefaultUsageAdapterBaseURL(config.Adapter)
	}
	if config.BaseURL != "" {
		if err := egress.ValidateUsageBaseURLFormat(config.BaseURL); err != nil {
			return UpstreamUsageQueryConfig{}, ErrUpstreamUsageConfigInvalid.WithCause(err)
		}
	}
	return config, nil
}

// mergeStoredUpstreamUsageConfig applies a saved query object over config and
// returns the merged copy. A supplied adapter name replaces config.Adapter only
// when fixed is false, but its type is checked either way. A non-object value,
// a field with the wrong type, an empty adapter name or an unknown field
// returns ErrUpstreamUsageConfigInvalid; adapter registration and root format
// are left to the caller.
func mergeStoredUpstreamUsageConfig(config UpstreamUsageQueryConfig, raw any, fixed bool) (UpstreamUsageQueryConfig, error) {
	object, ok := UpstreamUsageConfigMap(raw)
	if !ok {
		return UpstreamUsageQueryConfig{}, ErrUpstreamUsageConfigInvalid
	}
	if value, exists := object["enabled"]; exists {
		parsed, ok := value.(bool)
		if !ok {
			return UpstreamUsageQueryConfig{}, ErrUpstreamUsageConfigInvalid
		}
		config.Enabled = parsed
	}
	if value, exists := object["adapter"]; exists {
		parsed, ok := value.(string)
		if !ok || strings.TrimSpace(parsed) == "" {
			return UpstreamUsageQueryConfig{}, ErrUpstreamUsageConfigInvalid
		}
		// Platform-selected formats own adapter selection; a supplied generic name
		// cannot change them, though its input type is still checked.
		if !fixed {
			config.Adapter = strings.TrimSpace(parsed)
		}
	}
	if value, exists := object["base_url"]; exists {
		parsed, ok := value.(string)
		if !ok {
			return UpstreamUsageQueryConfig{}, ErrUpstreamUsageConfigInvalid
		}
		config.BaseURL = strings.TrimSpace(parsed)
	}
	for key := range object {
		if key != "enabled" && key != "adapter" && key != "base_url" {
			return UpstreamUsageQueryConfig{}, ErrUpstreamUsageConfigInvalid
		}
	}
	return config, nil
}

// NormalizeUpstreamUsageExtra validates a supplied query object and replaces its
// Extra entry with normalized choices. A nil map or missing/nil entry succeeds
// without replacement. Present objects default enabled and adapter fields, trim
// strings and discard an empty root.
//
// Unknown fields, invalid types, unsupported adapter names and unsafe root
// formats return before publishing the replacement object. This keeps
// credentials and arbitrary request templates out of saved query configuration.
// Callers own and synchronize the Extra map being updated.
func NormalizeUpstreamUsageExtra(extra map[string]any) error {
	if extra == nil {
		return nil
	}
	raw, exists := extra[UpstreamUsageQueryExtraKey]
	if !exists || raw == nil {
		return nil
	}
	object, ok := UpstreamUsageConfigMap(raw)
	if !ok {
		return ErrUpstreamUsageConfigInvalid
	}
	normalized := map[string]any{
		"enabled": true,
		"adapter": UpstreamUsageDefaultAdapter,
	}
	if value, exists := object["enabled"]; exists {
		parsed, ok := value.(bool)
		if !ok {
			return ErrUpstreamUsageConfigInvalid
		}
		normalized["enabled"] = parsed
	}
	if value, exists := object["adapter"]; exists {
		parsed, ok := value.(string)
		if !ok || !IsKnownUpstreamUsageAdapter(strings.TrimSpace(parsed)) {
			return ErrUpstreamUsageConfigInvalid
		}
		normalized["adapter"] = strings.TrimSpace(parsed)
	}
	if value, exists := object["base_url"]; exists {
		parsed, ok := value.(string)
		if !ok {
			return ErrUpstreamUsageConfigInvalid
		}
		parsed = strings.TrimSpace(parsed)
		if parsed != "" {
			if err := egress.ValidateUsageBaseURLFormat(parsed); err != nil {
				return ErrUpstreamUsageConfigInvalid.WithCause(err)
			}
			normalized["base_url"] = parsed
		}
	}
	// Reject unrecognized fields before publishing normalized choices so saved
	// configuration cannot carry credentials, arbitrary headers or request templates.
	for key := range object {
		if key != "enabled" && key != "adapter" && key != "base_url" {
			return ErrUpstreamUsageConfigInvalid
		}
	}
	extra[UpstreamUsageQueryExtraKey] = normalized
	return nil
}

// NormalizedUpstreamUsageConfigValue normalizes supplied configuration into a
// replacement object. Nil interface input or invalid values return nil with
// false; a typed nil map is accepted as an empty object by UpstreamUsageConfigMap.
// Successful output has fresh top-level storage for its scalar choices.
func NormalizedUpstreamUsageConfigValue(value any) (any, bool) {
	if value == nil {
		return nil, false
	}
	extra := map[string]any{UpstreamUsageQueryExtraKey: value}
	// Admin.UpdateProvider uses this result when carrying saved query settings
	// into the replacement map it validates. An invalid saved value supplies no
	// normalized object for that map.
	if err := NormalizeUpstreamUsageExtra(extra); err != nil {
		return nil, false
	}
	normalized, ok := extra[UpstreamUsageQueryExtraKey]
	return normalized, ok
}

// UpstreamUsageConfigMap returns a supplied map[string]any directly, including
// a typed nil map, with true. Other inputs use JSON conversion; encoding failure,
// non-object JSON or null returns false. Treat a returned direct map as borrowed
// while reading it.
func UpstreamUsageConfigMap(raw any) (map[string]any, bool) {
	if object, ok := raw.(map[string]any); ok {
		return object, true
	}
	// Inputs outside map[string]any use storage's JSON representation. Encoding
	// failures or non-object JSON provide no usable configuration object to
	// validate.
	payload, err := json.Marshal(raw)
	if err != nil {
		return nil, false
	}
	var object map[string]any
	if err := json.Unmarshal(payload, &object); err != nil {
		return nil, false
	}
	return object, object != nil
}

// IsKnownUpstreamUsageAdapter reports whether name exactly matches a registered
// query format.
func IsKnownUpstreamUsageAdapter(name string) bool {
	for _, registration := range usageAdapterCatalog {
		if registration.Name == name {
			return true
		}
	}
	return false
}

// PlatformUsageAdapter reports the query format selected by a provider's
// platform and mode. fixed is true for Kimi, Zhipu, and DeepSeek and OpenCode
// providers, whose saved generic adapter choice is ignored; other platforms and
// nil return "", false and use the administrator's choice.
//
// A fixed result with an empty name means the current mode has no registered
// query format, such as Zhipu or OpenCode pay-as-you-go.
// EffectiveUpstreamUsageConfig resolves that case as disabled, and query
// execution reports it as unsupported before sending a request.
func PlatformUsageAdapter(provider *Record) (name string, fixed bool) {
	rules := provider.rules().usage
	if !rules.fixedAdapter {
		return "", false
	}
	if rules.modeAdapter != nil {
		return rules.modeAdapter(provider), true
	}
	return provider.textMode().usageAdapter, true
}
