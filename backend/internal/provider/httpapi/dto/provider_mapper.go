package dto

import (
	"encoding/json"
	"net/url"
	"strings"
	"time"

	proxydto "github.com/TokenFlux/TokenRouter/internal/egress/httpapi/dto"
	"github.com/TokenFlux/TokenRouter/internal/provider"
	"github.com/TokenFlux/TokenRouter/internal/routing"
	routingdto "github.com/TokenFlux/TokenRouter/internal/routing/httpapi/dto"
)

// provider_mapper.go maps provider records to redacted administration
// responses with platform-specific display fields.
// A management DTO combines redacted provider configuration with
// platform-specific display controls. Mapping first clones the record's
// supported containers and pointers, then removes classified credential secrets
// and managed Extra before exposing effective cost, session, RPM, TLS and quota
// values. Extra usage-query configuration has its own allowlist because
// historical objects can contain credentials.
//
// Shallow mapping stops before proxy and group relationships, and membership
// mapping uses that form to bound recursion. Optional usage totals follow
// positive-limit rules; expired daily, weekly, or monthly quota usage is
// displayed as
// zero without updating storage. CloneValues handles its listed JSON
// containers, while unsupported mutable values can remain shared and need
// immutable use during serialization.

// ProviderFromRecordShallow builds a redacted provider view with
// platform-qualified display controls. Nil input returns nil. The record is
// cloned before redaction, copying the supported credential and Extra
// containers plus record pointers; unsupported mutable values inside those maps
// can remain shared.
//
// Effective getters choose optional controls, including reserve and
// idle-timeout defaults. Expired daily, weekly, and monthly quota usage is
// shown as zero,
// while stored accounting remains unchanged. Proxy and group relationships are
// left for the full mapper, and parent account display fields are filled by
// RuntimePresenter.
func ProviderFromRecordShallow(a *provider.Record) *Provider {
	if a == nil {
		return nil
	}
	a = provider.CloneRecord(a)
	runtime := provider.RuntimeConfig{Extra: a.Extra, Concurrency: a.Concurrency, SessionWindowStart: a.SessionWindowStart, SessionWindowEnd: a.SessionWindowEnd}
	redactedCreds, credsStatus := RedactCredentials(a.Credentials)
	extra := redactProviderManagedExtra(a.Extra)
	var ollamaCloudUsage *provider.OllamaCloudUsageState
	if state := provider.OllamaCloudUsageStateFromProvider(a); state.Eligible {
		ollamaCloudUsage = state
	}
	out := &Provider{
		ID:                      a.ID,
		Name:                    a.Name,
		Notes:                   a.Notes,
		Platform:                a.Platform,
		Type:                    a.Type,
		Credentials:             redactedCreds,
		CredentialsStatus:       credsStatus,
		Extra:                   extra,
		OllamaCloudUsage:        ollamaCloudUsage,
		ProxyID:                 a.ProxyID,
		ProxyFallbackOriginID:   a.ProxyFallbackOriginID,
		ProxyFallbackOriginName: a.ProxyFallbackOriginName,
		Concurrency:             a.Concurrency,
		LoadFactor:              a.LoadFactor,
		Priority:                a.Priority,
		RateMultiplier:          a.BillingRateMultiplier(),
		Status:                  a.Status,
		ErrorMessage:            a.ErrorMessage,
		LastUsedAt:              a.LastUsedAt,
		ExpiresAt:               timeToUnixSeconds(a.ExpiresAt),
		AutoPauseOnExpired:      a.AutoPauseOnExpired,
		CreatedAt:               a.CreatedAt,
		UpdatedAt:               a.UpdatedAt,
		Schedulable:             a.Schedulable,
		RateLimitedAt:           a.RateLimitedAt,
		RateLimitResetAt:        a.RateLimitResetAt,
		OverloadUntil:           a.OverloadUntil,
		TempUnschedulableUntil:  a.TempUnschedulableUntil,
		TempUnschedulableReason: a.TempUnschedulableReason,
		QuotaAutoPaused:         a.QuotaAutoPaused,
		SessionWindowStart:      a.SessionWindowStart,
		SessionWindowEnd:        a.SessionWindowEnd,
		SessionWindowStatus:     a.SessionWindowStatus,
		GroupIDs:                a.GroupIDs,
		ParentProviderID:        a.ParentProviderID,
		QuotaDimension:          a.QuotaDimension,
	}

	// Anthropic getters supply effective defaults as well as configured values.
	// Include positive controls independently so the browser can display the same rules.
	if a.IsAnthropicOAuthOrSetupToken() {
		if limit := runtime.GetWindowCostLimit(); limit > 0 {
			out.WindowCostLimit = &limit
		}
		if reserve := runtime.GetWindowCostStickyReserve(); reserve > 0 {
			out.WindowCostStickyReserve = &reserve
		}
		if maxSessions := runtime.GetMaxSessions(); maxSessions > 0 {
			out.MaxSessions = &maxSessions
		}
		if idleTimeout := runtime.GetSessionIdleTimeoutMinutes(); idleTimeout > 0 {
			out.SessionIdleTimeoutMin = &idleTimeout
		}
		if rpm := runtime.GetBaseRPM(); rpm > 0 {
			out.BaseRPM = &rpm
			strategy := runtime.GetRPMStrategy()
			out.RPMStrategy = &strategy
			buffer := runtime.GetRPMStickyBuffer()
			out.RPMStickyBuffer = &buffer
		}

		if mode := runtime.GetUserMsgQueueMode(); mode != "" {
			out.UserMsgQueueMode = &mode
		}

		if a.IsSessionIDMaskingEnabled() {
			enabled := true
			out.EnableSessionIDMasking = &enabled
		}

		if a.IsCacheTTLOverrideEnabled() {
			enabled := true
			out.CacheTTLOverrideEnabled = &enabled
			target := a.GetCacheTTLOverrideTarget()
			out.CacheTTLOverrideTarget = &target
		}

		if a.IsCustomBaseURLEnabled() {
			enabled := true
			out.CustomBaseURLEnabled = &enabled
			if customURL := a.GetCustomBaseURL(); customURL != "" {
				out.CustomBaseURL = &customURL
			}
		}
	}

	// Provider support gates the TLS display group. Nonzero selectors remain
	// visible independently of enablement so editing can retain a configured profile or router.
	if a.SupportsTLSFingerprint() {
		if a.IsTLSFingerprintEnabled() {
			enabled := true
			out.EnableTLSFingerprint = &enabled
		}
		if profileID := a.GetTLSFingerprintProfileID(); profileID != 0 {
			out.TLSFingerprintProfileID = &profileID
		}
		if routerID := a.GetTLSFingerprintRouterID(); routerID != 0 {
			out.TLSFingerprintRouterID = &routerID
		}
	}

	if a.IsOpenAIOAuth() {
		policy := a.GetOpenAIOAuthClientPolicy()
		out.OpenAIOAuthClientPolicy = &policy
	}

	// Quota values belong to API Key and Bedrock accounting. Read effective
	// limits before exposing their consumption, and treat expired windows as unused for display.
	if a.IsAPIKeyOrBedrock() {
		if limit := a.GetQuotaLimit(); limit > 0 {
			out.QuotaLimit = &limit
			used := a.GetQuotaUsed()
			out.QuotaUsed = &used
		}
		if limit := a.GetQuotaDailyLimit(); limit > 0 {
			out.QuotaDailyLimit = &limit
			used := a.GetQuotaDailyUsed()
			// An expired daily period represents a fresh display allowance. Zero this
			// response value while leaving stored accounting to the quota-maintenance path.
			if a.IsDailyQuotaPeriodExpired() {
				used = 0
			}
			out.QuotaDailyUsed = &used
		}
		if limit := a.GetQuotaWeeklyLimit(); limit > 0 {
			out.QuotaWeeklyLimit = &limit
			used := a.GetQuotaWeeklyUsed()
			// An expired weekly period represents a fresh display allowance. Zero this
			// response value while leaving stored accounting to the quota-maintenance path.
			if a.IsWeeklyQuotaPeriodExpired() {
				used = 0
			}
			out.QuotaWeeklyUsed = &used
		}
		if limit := a.GetQuotaMonthlyLimit(); limit > 0 {
			out.QuotaMonthlyLimit = &limit
			used := a.GetQuotaMonthlyUsed()
			// An expired monthly period represents a fresh display allowance. Zero this
			// response value while leaving stored accounting to the quota-maintenance path.
			if a.IsMonthlyQuotaPeriodExpired() {
				used = 0
			}
			out.QuotaMonthlyUsed = &used
		}

		if mode := a.GetQuotaDailyResetMode(); mode == "fixed" {
			out.QuotaDailyResetMode = &mode
			hour := a.GetQuotaDailyResetHour()
			out.QuotaDailyResetHour = &hour
		}
		if mode := a.GetQuotaWeeklyResetMode(); mode == "fixed" {
			out.QuotaWeeklyResetMode = &mode
			day := a.GetQuotaWeeklyResetDay()
			out.QuotaWeeklyResetDay = &day
			hour := a.GetQuotaWeeklyResetHour()
			out.QuotaWeeklyResetHour = &hour
		}
		if mode := a.GetQuotaMonthlyResetMode(); mode == "fixed" {
			out.QuotaMonthlyResetMode = &mode
			day := a.GetQuotaMonthlyResetDay()
			out.QuotaMonthlyResetDay = &day
			hour := a.GetQuotaMonthlyResetHour()
			out.QuotaMonthlyResetHour = &hour
		}
		if a.GetQuotaDailyResetMode() == "fixed" || a.GetQuotaWeeklyResetMode() == "fixed" || a.GetQuotaMonthlyResetMode() == "fixed" {
			tz := a.GetQuotaResetTimezone()
			out.QuotaResetTimezone = &tz
		}
		if a.Extra != nil {
			if v, ok := a.Extra["quota_daily_reset_at"].(string); ok && v != "" {
				out.QuotaDailyResetAt = &v
			}
			if v, ok := a.Extra["quota_weekly_reset_at"].(string); ok && v != "" {
				out.QuotaWeeklyResetAt = &v
			}
			if v, ok := a.Extra["quota_monthly_reset_at"].(string); ok && v != "" {
				out.QuotaMonthlyResetAt = &v
			}
		}

		if enabled := a.GetQuotaNotifyDailyEnabled(); enabled {
			out.QuotaNotifyDailyEnabled = &enabled
		}
		if threshold := a.GetQuotaNotifyDailyThreshold(); threshold > 0 {
			out.QuotaNotifyDailyThreshold = &threshold
		}
		if enabled := a.GetQuotaNotifyWeeklyEnabled(); enabled {
			out.QuotaNotifyWeeklyEnabled = &enabled
		}
		if threshold := a.GetQuotaNotifyWeeklyThreshold(); threshold > 0 {
			out.QuotaNotifyWeeklyThreshold = &threshold
		}
		if enabled := a.GetQuotaNotifyMonthlyEnabled(); enabled {
			out.QuotaNotifyMonthlyEnabled = &enabled
		}
		if threshold := a.GetQuotaNotifyMonthlyThreshold(); threshold > 0 {
			out.QuotaNotifyMonthlyThreshold = &threshold
		}
		if enabled := a.GetQuotaNotifyTotalEnabled(); enabled {
			out.QuotaNotifyTotalEnabled = &enabled
		}
		if threshold := a.GetQuotaNotifyTotalThreshold(); threshold > 0 {
			out.QuotaNotifyTotalThreshold = &threshold
		}
	}

	if a.IsQoderCosy() {
		out.QoderModelCatalogStatus = a.QoderCatalogStatus(time.Now())
	}
	return out
}

// redactProviderManagedExtra copies permitted Extra values while excluding the
// internal Qoder account observations and Ollama maintenance data. Nil stays
// nil. Upstream usage-query configuration follows a separate display allowlist.
// Other values use the copying rules of CloneValues.
func redactProviderManagedExtra(extra map[string]any) map[string]any {
	if extra == nil {
		return nil
	}
	redacted := make(map[string]any, len(extra))
	for key, value := range extra {
		switch key {
		case provider.QoderCatalogExtraKey, provider.QoderUsageQuotaSnapshotExtraKey, provider.QoderUsageQuotaUpdatedAtExtraKey:
			// The catalog status and usage endpoints validate account ownership before
			// displaying observations. Raw snapshots also contain the account digest.
			continue
		case provider.OllamaCloudUsageSessionExtraKey,
			provider.OllamaCloudUsageAutoRefreshExtraKey,
			provider.OllamaCloudUsageSnapshotExtraKey:
			// Session encryption, managed refresh policy and saved usage data have
			// their own redacted Ollama view, so raw Extra omits these storage values.
			continue
		case provider.UpstreamUsageQueryExtraKey:
			// Historical Extra values may contain credentials inside the usage query configuration.
			// Return only the three allowed non-sensitive configuration fields to the browser.
			redacted[key] = redactUpstreamUsageQuery(value)
		default:
			redacted[key] = provider.CloneValues(map[string]any{key: value})[key]
		}
	}
	return redacted
}

// redactUpstreamUsageQuery returns permitted display fields from a usage-query
// object. Values other than map[string]any are first converted through JSON;
// failed conversion returns an empty map. Only boolean enabled, registered
// string adapters and HTTP(S) URLs with a host are retained.
//
// URL user information, query strings and fragments reject the base URL field
// because historical configuration may place secrets there. This is
// serialization redaction rather than upstream destination validation: allowed
// path text is retained, and the result owns a new top-level map containing
// accepted scalar values.
func redactUpstreamUsageQuery(value any) map[string]any {
	object, ok := value.(map[string]any)
	if !ok {
		payload, err := json.Marshal(value)
		if err != nil {
			// An unsupported representation cannot be safely inspected for secrets.
			// Return an empty display object instead of forwarding historical raw values.
			return map[string]any{}
		}
		// JSON decoding admits equivalent object representations while rejecting
		// scalar and null values that cannot supply the permitted configuration fields.
		if err := json.Unmarshal(payload, &object); err != nil || object == nil {
			return map[string]any{}
		}
	}
	result := make(map[string]any, 3)
	if enabled, ok := object["enabled"].(bool); ok {
		result["enabled"] = enabled
	}
	if adapter, ok := object["adapter"].(string); ok {
		// Arbitrary historical adapter strings may contain secret text. Expose a
		// recognized adapter name so old credential-shaped values cannot reach the browser.
		if adapter == provider.UpstreamUsageAdapterSub2API || adapter == provider.UpstreamUsageAdapterNewAPI ||
			adapter == provider.UpstreamUsageAdapterZivv || adapter == provider.UpstreamUsageAdapterDerouter {
			result["adapter"] = adapter
		}
	}
	// Historical URLs can embed secrets in user information, queries or fragments.
	// Keep a scalar base URL only when those positions are absent and its scheme is HTTP(S).
	if baseURL, ok := object["base_url"].(string); ok {
		if parsed, err := url.Parse(strings.TrimSpace(baseURL)); err == nil && parsed.Scheme != "" && parsed.Host != "" &&
			parsed.User == nil && parsed.RawQuery == "" && parsed.Fragment == "" &&
			(strings.EqualFold(parsed.Scheme, "http") || strings.EqualFold(parsed.Scheme, "https")) {
			result["base_url"] = strings.TrimRight(strings.TrimSpace(baseURL), "/")
		}
	}
	return result
}

// ProviderFromRecord adds loaded proxy and group relationships to the shallow
// redacted view. Nil records return nil; nested membership provider views
// remain shallow to stop recursive provider-to-group association expansion.
func ProviderFromRecord(a *provider.Record) *Provider {
	if a == nil {
		return nil
	}
	out := ProviderFromRecordShallow(a)
	out.Proxy = proxydto.ProxyFromEgress(a.Proxy)
	if len(a.ProviderGroups) > 0 {
		out.ProviderGroups = make([]ProviderGroup, 0, len(a.ProviderGroups))
		for i := range a.ProviderGroups {
			ag := a.ProviderGroups[i]
			out.ProviderGroups = append(out.ProviderGroups, *ProviderGroupFromRecord(&ag))
		}
	}
	if len(a.Groups) > 0 {
		out.Groups = make([]*Group, 0, len(a.Groups))
		for _, g := range a.Groups {
			out.Groups = append(out.Groups, routingdto.GroupFromRouting((*routing.Group)(g)))
		}
	}
	return out
}

// ProviderGroupFromRecord maps a loaded membership into provider and routing
// group display objects. Nil membership returns nil, and its provider side uses
// the shallow mapper so loaded associations do not recurse indefinitely.
func ProviderGroupFromRecord(ag *provider.GroupMembership) *ProviderGroup {
	if ag == nil {
		return nil
	}
	return &ProviderGroup{
		ProviderID: ag.ProviderID,
		GroupID:    ag.GroupID,
		CreatedAt:  ag.CreatedAt,
		Provider:   ProviderFromRecordShallow(ag.Provider),
		Group:      routingdto.GroupFromRouting((*routing.Group)(ag.Group)),
	}
}

// timeToUnixSeconds returns an independently allocated Unix-second value for a
// supplied timestamp. Nil input stays nil, retaining absent expiry in JSON;
// subsecond precision is discarded by time.Time.Unix.
func timeToUnixSeconds(value *time.Time) *int64 {
	if value == nil {
		return nil
	}
	// HTTP expiry uses whole Unix seconds, dropping source subsecond precision.
	ts := value.Unix()
	return &ts
}
