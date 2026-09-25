// 本文件维护 account 的所属能力；兼容入口复用唯一实现。
package account

import (
	"maps"
	"slices"

	"github.com/TokenFlux/TokenRouter/internal/account/usageview"
)

// CloneUsageInfo 隔离缓存/flight 与每个请求；纯展示值的复制不改变金额或字段省略规则。
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
			out.AntigravityQuota[k] = clonePointer(v)
		}
	}
	if value.AntigravityQuotaDetails != nil {
		out.AntigravityQuotaDetails = make(map[string]*AntigravityModelDetail, len(value.AntigravityQuotaDetails))
		for k, v := range value.AntigravityQuotaDetails {
			out.AntigravityQuotaDetails[k] = cloneAntigravityModelDetail(v)
		}
	}
	out.QoderQuota = cloneQoderQuota(value.QoderQuota)
	return &out
}
func cloneUsageProgress(value *UsageProgress) *UsageProgress {
	if value == nil {
		return nil
	}
	out := *value
	out.ResetsAt = clonePointer(value.ResetsAt)
	out.WindowStats = clonePointer(value.WindowStats)
	return &out
}
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
	return &out
}
