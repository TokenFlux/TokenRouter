package service

import (
	"maps"

	"github.com/TokenFlux/TokenRouter/internal/account/usageview"
)

// cloneGrokQuotaProbeResult 隔离共享探测返回的嵌套展示值；平台报文仍由旧适配层拥有。
func cloneGrokQuotaProbeResult(value *GrokQuotaProbeResult) *GrokQuotaProbeResult {
	if value == nil {
		return nil
	}
	out := *value
	out.Billing = usageview.CloneBillingSummary(value.Billing)
	out.LocalUsage24h = cloneGrokProbePointer(value.LocalUsage24h)
	out.LocalUsage7d = cloneGrokProbePointer(value.LocalUsage7d)
	out.LocalUsageMonthly = cloneGrokProbePointer(value.LocalUsageMonthly)
	if value.Snapshot != nil {
		out.Snapshot = cloneGrokProbePointer(value.Snapshot)
		out.Snapshot.Requests = usageview.CloneQuotaWindow(value.Snapshot.Requests)
		out.Snapshot.Tokens = usageview.CloneQuotaWindow(value.Snapshot.Tokens)
		out.Snapshot.RetryAfterSeconds = cloneGrokProbePointer(value.Snapshot.RetryAfterSeconds)
		out.Snapshot.Headers = maps.Clone(value.Snapshot.Headers)
	}
	return &out
}

// cloneGrokProbePointer 用于仅含标量的窗口值与可选字段。
func cloneGrokProbePointer[T any](value *T) *T {
	if value == nil {
		return nil
	}
	out := *value
	return &out
}
