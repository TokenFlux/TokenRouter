// 本文件维护 account 的所属能力；兼容入口复用唯一实现。
package account

import (
	json "encoding/json"
	fmt "fmt"
	usageview "github.com/TokenFlux/TokenRouter/internal/account/usageview"
	strings "strings"
)

func ParseGrokBillingSnapshot(extra map[string]any) (*usageview.BillingSummary, error) {
	if extra == nil {
		return nil, nil
	}
	raw, ok := extra[GrokUsageBillingExtraKey]
	if !ok || raw == nil {
		return nil, nil
	}
	switch snapshot := raw.(type) {
	case *usageview.BillingSummary:
		return usageview.CloneBillingSummary(snapshot), nil
	case usageview.BillingSummary:
		return usageview.CloneBillingSummary(&snapshot), nil
	case map[string]any:
		data, err := json.Marshal(snapshot)
		if err != nil {
			return nil, err
		}
		var out usageview.BillingSummary
		if err := json.Unmarshal(data, &out); err != nil {
			return nil, err
		}
		return &out, nil
	default:
		data, err := json.Marshal(raw)
		if err != nil {
			return nil, fmt.Errorf("marshal grok billing snapshot: %w", err)
		}
		var out usageview.BillingSummary
		if err := json.Unmarshal(data, &out); err != nil {
			return nil, err
		}
		return &out, nil
	}
}

// MergeUsageExtra 隔离请求快照的顶层与 JSON 容器，保留原补丁覆盖顺序。
func MergeUsageExtra(extra, updates map[string]any) map[string]any {
	if len(updates) == 0 {
		return extra
	}
	out := CloneValues(extra)
	if out == nil {
		out = make(map[string]any, len(updates))
	}
	for k, v := range CloneValues(updates) {
		out[k] = v
	}
	return out
}

func GrokBillingHasAuthoritativeQuota(billing *usageview.BillingSummary) bool {
	if billing == nil {
		return false
	}
	return billing.UsagePercent != nil ||
		billing.UsedPercent != nil ||
		(billing.MonthlyLimitCents != nil && *billing.MonthlyLimitCents > 0) ||
		strings.TrimSpace(billing.Plan) != ""
}
