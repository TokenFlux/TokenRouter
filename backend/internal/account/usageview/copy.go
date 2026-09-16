// 账号展示兼容入口委托中立用量值，S15/S16 清理。
package usageview

import native "github.com/TokenFlux/TokenRouter/internal/upstream/usageview"

func CloneQuotaWindow(value *QuotaWindow) *QuotaWindow { return native.CloneQuotaWindow(value) }
func CloneBillingSummary(value *BillingSummary) *BillingSummary {
	return native.CloneBillingSummary(value)
}
