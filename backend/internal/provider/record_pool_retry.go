package provider

import (
	"encoding/json"
	"slices"
	"sort"
	"strconv"
	"strings"
)

// record_pool_retry.go evaluates pool-mode retry budgets, retryable status
// overrides, and API-key custom error-code filters.
// Same-provider pool retries and custom status handling read dynamic JSON
// credential fields with intentionally different empty-list semantics. A
// configured empty []any retry-status override disables all retryable status
// matches, whereas an absent override inherits DefaultPoolModeRetryableStatusCodes.
//
// Custom error-code filtering applies only to API-key providers and treats an
// empty parsed selection as unrestricted so incomplete forms retain standard
// health handling. Retry counts clamp between zero and MaxPoolModeRetryCount.

const (
	// Pool-mode counts supply the ordinary budget for additional attempts against
	// one provider. Gateway failover evaluates error-specific caps and independent
	// deadline rules before deciding whether another attempt is allowed.
	DefaultPoolModeRetryCount = 3  // missing same-provider retry count
	MaxPoolModeRetryCount     = 10 // maximum same-provider retry count
)

// DefaultPoolModeRetryableStatusCodes supplies the shared membership list for
// pool-mode retry-status decisions. A nil GetPoolModeRetryStatusCodes result
// delegates membership to this list; every configured []any value uses its own
// normalized result.
// Runtime readers share the exported slice, so callers synchronize any changes
// to these defaults with request handling.
var DefaultPoolModeRetryableStatusCodes = []int{401, 403, 429}

// IsCustomErrorCodesEnabled reads the API Key custom-error handling switch.
// Other types, absent credentials, missing keys, and nonboolean values return
// false, leaving ShouldHandleErrorCode on its unrestricted handling path.
func (r *Record) IsCustomErrorCodesEnabled() bool {
	if r.Type != ProviderTypeAPIKey || r.Credentials == nil {
		return false
	}
	if v, ok := r.Credentials["custom_error_codes_enabled"]; ok {
		if enabled, ok := v.(bool); ok {
			return enabled
		}
	}
	return false
}

// IsPoolMode reads the saved boolean pool_mode switch for API Key or Bedrock
// types. Missing credentials or nonboolean values return false; request
// handlers separately apply pool retry and error-policy decisions.
func (r *Record) IsPoolMode() bool {
	if !r.IsAPIKeyOrBedrock() || r.Credentials == nil {
		return false
	}
	if v, ok := r.Credentials["pool_mode"]; ok {
		if enabled, ok := v.(bool); ok {
			return enabled
		}
	}
	return false
}

// GetPoolModeRetryCount returns the ordinary same-provider retry budget when
// pool mode applies. Missing, disabled, or unparseable settings select
// DefaultPoolModeRetryCount; parsed values below zero or above
// MaxPoolModeRetryCount are clamped to those limits.
//
// Gateway failover can narrow this count with an error-specific maximum. An
// uncapped retry deadline is evaluated independently and can admit retries after
// the ordinary count is spent, so this accessor alone does not impose a limit
// on every retry path.
func (r *Record) GetPoolModeRetryCount() int {
	if r == nil || !r.IsPoolMode() || r.Credentials == nil {
		return DefaultPoolModeRetryCount
	}
	raw, ok := r.Credentials["pool_mode_retry_count"]
	if !ok || raw == nil {
		return DefaultPoolModeRetryCount
	}
	count := ParsePoolModeRetryCount(raw)
	// A negative saved count selects the ordinary zero retry budget. Failover
	// separately evaluates deadline rules before deciding whether to keep this
	// provider or proceed with another selection.
	if count < 0 {
		return 0
	}
	// Share the configured ceiling across entry points so each handler receives
	// the same bounded ordinary count before applying its failure-specific rules.
	if count > MaxPoolModeRetryCount {
		return MaxPoolModeRetryCount
	}
	return count
}

// ParsePoolModeRetryCount reads supported integer, float64, json.Number, or
// trimmed string representations. Failed parsing or unsupported input returns
// DefaultPoolModeRetryCount, while range clamping belongs to
// GetPoolModeRetryCount after this conversion.
func ParsePoolModeRetryCount(value any) int {
	switch v := value.(type) {
	case int:
		return v
	case int64:
		return int(v)
	case float64:
		// A retry count uses whole attempts after fractional JSON input is truncated.
		return int(v)
	case json.Number:
		if i, err := v.Int64(); err == nil {
			return int(i)
		}
	case string:
		if i, err := strconv.Atoi(strings.TrimSpace(v)); err == nil {
			return i
		}
	}
	// A failed number or string parse has no usable configured count. Return
	// the shared default before the accessor applies its bounds to successful values.
	return DefaultPoolModeRetryCount
}

// IsPoolModeRetryableStatus reports membership in the shared default pool retry
// status list.
func IsPoolModeRetryableStatus(statusCode int) bool {
	return slices.Contains(DefaultPoolModeRetryableStatusCodes, statusCode)
}

// GetPoolModeRetryStatusCodes normalizes a provider's optional retry-status
// override. Nil records, absent values, and values outside the []any
// representation return nil, instructing the caller to inherit the shared
// defaults.
//
// A supplied []any remains configured even if empty or every element is
// rejected; its empty nonnil result yields no IsPoolModeRetryableStatus matches.
// Accepted numeric and string elements are deduplicated, restricted to HTTP
// statuses 100 through 599, and sorted. Keeping container presence separate from
// valid entries prevents an empty configured selection from restoring default
// status matches.
func (r *Record) GetPoolModeRetryStatusCodes() []int {
	if r == nil || r.Credentials == nil {
		return nil
	}
	raw, ok := r.Credentials["pool_mode_retry_status_codes"]
	if !ok || raw == nil {
		return nil
	}
	arr, ok := raw.([]any)
	if !ok {
		return nil
	}
	seen := make(map[int]struct{}, len(arr))
	codes := make([]int, 0, len(arr))
	for _, v := range arr {
		var code int
		switch n := v.(type) {
		case float64:
			// Convert decoded JSON numbers to whole HTTP status values before filtering.
			code = int(n)
		case int:
			code = n
		case int64:
			code = int(n)
		case json.Number:
			// Exact-number text must encode an integer HTTP status. Reject a failed
			// parse locally so one unusable element cannot discard the other
			// configured statuses or select an invented integral value.
			i, err := n.Int64()
			if err != nil {
				continue
			}
			code = int(i)
		case string:
			// Legacy text entries accept surrounding whitespace. Skip unreadable
			// status text while keeping the supplied array configured, so the
			// surviving entries still define this provider's override.
			i, err := strconv.Atoi(strings.TrimSpace(n))
			if err != nil {
				continue
			}
			code = i
		default:
			continue
		}
		// Keep the supplied array configured while discarding unusable HTTP codes.
		// If none survive, the allocated empty result still reaches membership
		// testing and matches no status. An unavailable override instead returns
		// nil, which selects the shared default list.
		if code < 100 || code > 599 {
			continue
		}
		if _, exists := seen[code]; exists {
			continue
		}
		seen[code] = struct{}{}
		codes = append(codes, code)
	}
	sort.Ints(codes)
	return codes
}

// IsPoolModeRetryableStatus checks the provider override when one is
// configured. A nil normalized override uses the shared defaults, while an
// empty configured slice disables all status matches independently of whether
// pool mode is enabled.
func (r *Record) IsPoolModeRetryableStatus(statusCode int) bool {
	codes := r.GetPoolModeRetryStatusCodes()
	// Both absent overrides and configured empty results have zero length.
	// Test nil here so an empty []any cannot silently restore default status
	// matches when its normalized result contains no codes.
	if codes == nil {
		return IsPoolModeRetryableStatus(statusCode)
	}
	return slices.Contains(codes, statusCode)
}

// GetCustomErrorCodes reads the custom handling statuses from a JSON []any
// array. It accepts float64 elements and converts them to integers; other
// elements are skipped, and missing or differently typed containers return nil.
func (r *Record) GetCustomErrorCodes() []int {
	if r.Credentials == nil {
		return nil
	}
	raw, ok := r.Credentials["custom_error_codes"]
	if !ok || raw == nil {
		return nil
	}
	if arr, ok := raw.([]any); ok {
		result := make([]int, 0, len(arr))
		// This stored compatibility list accepts ordinary JSON numeric elements.
		// Convert those values to integer statuses and leave other representations
		// out of the resulting custom handling selection.
		for _, v := range arr {
			if f, ok := v.(float64); ok {
				result = append(result, int(f))
			}
		}
		return result
	}
	return nil
}

// ShouldHandleErrorCode applies the API Key custom status selection to an
// upstream error. Disabled configuration or an empty parsed list handles every
// status; a nonempty list handles only its members.
func (r *Record) ShouldHandleErrorCode(statusCode int) bool {
	if !r.IsCustomErrorCodesEnabled() {
		return true
	}
	codes := r.GetCustomErrorCodes()
	// This compatibility policy treats an empty parsed selection as unrestricted.
	// Keep health handling available for enabled-but-empty forms, whose lack of
	// status entries differs from a configured empty pool retry override.
	if len(codes) == 0 {
		return true
	}
	return slices.Contains(codes, statusCode)
}
