package provider

import (
	"maps"
	"slices"

	"github.com/TokenFlux/TokenRouter/internal/provider/usageview"
)

// oauth_usage_copy.go copies mutable usage display fields for cache storage
// and independent caller updates.
// Usage query results contain mutable windows, maps, capability pointers, and
// nested quota pools. Copying those values before cache storage or caller
// editing keeps one display update from changing another observation,
// including scalar pointers inside Grok product billing entries.

// CloneUsageInfo copies every current mutable display field into independent
// storage. Nil remains nil, while nested windows, reset times, quota maps,
// capability pointers, Qoder pools, and Grok billing values can be edited
// independently; callers keep the source stable during copying.
func CloneUsageInfo(value *UsageInfo) *UsageInfo {
	if value == nil {
		return nil
	}
	out := *value
	out.UpdatedAt = clonePointer(value.UpdatedAt)
	out.FiveHour = cloneUsageProgress(value.FiveHour)
	out.SevenDay = cloneUsageProgress(value.SevenDay)
	out.SevenDaySonnet = cloneUsageProgress(value.SevenDaySonnet)
	out.SevenDayFable = cloneUsageProgress(value.SevenDayFable)
	out.GeminiSharedDaily = cloneUsageProgress(value.GeminiSharedDaily)
	out.GeminiProDaily = cloneUsageProgress(value.GeminiProDaily)
	out.GeminiFlashDaily = cloneUsageProgress(value.GeminiFlashDaily)
	out.GeminiSharedMinute = cloneUsageProgress(value.GeminiSharedMinute)
	out.GeminiProMinute = cloneUsageProgress(value.GeminiProMinute)
	out.GeminiFlashMinute = cloneUsageProgress(value.GeminiFlashMinute)
	out.GrokRequestQuota = usageview.CloneQuotaWindow(value.GrokRequestQuota)
	out.GrokTokenQuota = usageview.CloneQuotaWindow(value.GrokTokenQuota)
	out.GrokRetryAfterSeconds = clonePointer(value.GrokRetryAfterSeconds)
	out.GrokLocalUsage = clonePointer(value.GrokLocalUsage)
	out.GrokLocalUsage24h = clonePointer(value.GrokLocalUsage24h)
	out.GrokLocalUsage7d = clonePointer(value.GrokLocalUsage7d)
	out.GrokLocalUsageMonthly = clonePointer(value.GrokLocalUsageMonthly)
	out.ThirtyDay = cloneUsageProgress(value.ThirtyDay)
	out.GrokBilling = usageview.CloneBillingSummary(value.GrokBilling)
	out.AICredits = slices.Clone(value.AICredits)
	out.ModelForwardingRules = maps.Clone(value.ModelForwardingRules)
	if value.AntigravityQuota != nil {
		out.AntigravityQuota = make(map[string]*AntigravityModelQuota, len(value.AntigravityQuota))
		for k, v := range value.AntigravityQuota {
			out.AntigravityQuota[k] = cloneAntigravityModelQuota(v)
		}
	}
	out.AntigravityQuotaSummary = cloneAntigravityQuotaSummary(value.AntigravityQuotaSummary)
	if value.AntigravityQuotaDetails != nil {
		out.AntigravityQuotaDetails = make(map[string]*AntigravityModelDetail, len(value.AntigravityQuotaDetails))
		for k, v := range value.AntigravityQuotaDetails {
			out.AntigravityQuotaDetails[k] = cloneAntigravityModelDetail(v)
		}
	}
	out.QoderQuota = cloneQoderQuota(value.QoderQuota)
	return &out
}

// cloneUsageProgress copies the window, reset timestamp, and local totals. Nil
// remains nil; all current nested values are scalar data, so the returned
// window can be edited independently.
func cloneUsageProgress(value *UsageProgress) *UsageProgress {
	if value == nil {
		return nil
	}
	out := *value
	out.ResetsAt = clonePointer(value.ResetsAt)
	out.WindowStats = clonePointer(value.WindowStats)
	return &out
}

// cloneAntigravityQuotaSummary copies grouped Antigravity weekly and 5-hour
// quota windows so cached observations stay isolated from caller edits.
func cloneAntigravityQuotaSummary(value *AntigravityQuotaSummary) *AntigravityQuotaSummary {
	if value == nil {
		return nil
	}
	return &AntigravityQuotaSummary{
		Gemini:    cloneAntigravityQuotaGroup(value.Gemini),
		ClaudeGPT: cloneAntigravityQuotaGroup(value.ClaudeGPT),
	}
}

// cloneAntigravityModelQuota copies one model or family quota window and its
// optional local WindowStats pointer.
func cloneAntigravityModelQuota(value *AntigravityModelQuota) *AntigravityModelQuota {
	if value == nil {
		return nil
	}
	out := *value
	out.WindowStats = clonePointer(value.WindowStats)
	return &out
}

// cloneAntigravityQuotaGroup copies one family's weekly and 5-hour windows.
func cloneAntigravityQuotaGroup(value *AntigravityQuotaGroup) *AntigravityQuotaGroup {
	if value == nil {
		return nil
	}
	return &AntigravityQuotaGroup{
		Weekly:   cloneAntigravityModelQuota(value.Weekly),
		FiveHour: cloneAntigravityModelQuota(value.FiveHour),
	}
}

// cloneAntigravityModelDetail copies optional capability pointers and MIME
// flags. Nil remains nil, and callers can independently change each copied
// pointer or map while keeping the source stable during copying.
func cloneAntigravityModelDetail(value *AntigravityModelDetail) *AntigravityModelDetail {
	if value == nil {
		return nil
	}
	out := *value
	out.SupportsImages = clonePointer(value.SupportsImages)
	out.SupportsThinking = clonePointer(value.SupportsThinking)
	out.ThinkingBudget = clonePointer(value.ThinkingBudget)
	out.Recommended = clonePointer(value.Recommended)
	out.MaxTokens = clonePointer(value.MaxTokens)
	out.MaxOutputTokens = clonePointer(value.MaxOutputTokens)
	out.SupportedMimeTypes = maps.Clone(value.SupportedMimeTypes)
	return &out
}

// cloneQoderQuota copies account timestamps, general pools, dedicated package
// metadata, and the optional enterprise view. Nil remains nil. Returned labels
// and expiry pointers can be edited without changing cached observations.
func cloneQoderQuota(value *QoderQuotaInfo) *QoderQuotaInfo {
	if value == nil {
		return nil
	}
	out := *value
	out.ExpiresAt = clonePointer(value.ExpiresAt)
	out.LastUpdatedAt = clonePointer(value.LastUpdatedAt)
	out.UserQuota = clonePointer(value.UserQuota)
	out.AddOnQuota = clonePointer(value.AddOnQuota)
	out.OrgResourcePackage = clonePointer(value.OrgResourcePackage)
	out.Enterprise = clonePointer(value.Enterprise)
	out.Dedicated = slices.Clone(value.Dedicated)
	for index := range out.Dedicated {
		out.Dedicated[index].ExpiresAt = clonePointer(value.Dedicated[index].ExpiresAt)
		out.Dedicated[index].Labels = slices.Clone(value.Dedicated[index].Labels)
	}
	return &out
}
