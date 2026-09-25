// 本文件维护 usageview 的所属能力；兼容入口复用唯一实现。
package usageview

import (
	"slices"
)

// CloneQuotaWindow 只复制展示值，保留 nil 与空集合。
func CloneQuotaWindow(value *QuotaWindow) *QuotaWindow {
	if value == nil {
		return nil
	}
	out := *value
	out.Limit = clonePointer(value.Limit)
	out.Remaining = clonePointer(value.Remaining)
	out.ResetUnix = clonePointer(value.ResetUnix)
	return &out
}

// CloneBillingSummary 只复制展示值，保留 nil 与空集合。
func CloneBillingSummary(value *BillingSummary) *BillingSummary {
	if value == nil {
		return nil
	}
	out := *value
	out.UsagePercent = clonePointer(value.UsagePercent)
	out.ProductUsage = slices.Clone(value.ProductUsage)
	out.MonthlyLimitCents = clonePointer(value.MonthlyLimitCents)
	out.UsedCents = clonePointer(value.UsedCents)
	out.IncludedUsedCents = clonePointer(value.IncludedUsedCents)
	out.UsedPercent = clonePointer(value.UsedPercent)
	out.FailedWindows = slices.Clone(value.FailedWindows)
	out.PrepaidBalance = clonePointer(value.PrepaidBalance)
	out.OnDemandCap = clonePointer(value.OnDemandCap)
	out.OnDemandUsed = clonePointer(value.OnDemandUsed)
	out.MonthlyLimit = clonePointer(value.MonthlyLimit)
	out.MonthlyUsed = clonePointer(value.MonthlyUsed)
	for i := range out.ProductUsage {
		out.ProductUsage[i].UsagePercent = clonePointer(value.ProductUsage[i].UsagePercent)
	}
	return &out
}
func clonePointer[T any](value *T) *T {
	if value == nil {
		return nil
	}
	out := *value
	return &out
}
