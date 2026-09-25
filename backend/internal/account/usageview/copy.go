// 账号用量展示复用共享用量值和复制函数。
package usageview

import "github.com/TokenFlux/TokenRouter/internal/upstream/usageview"

func CloneQuotaWindow(value *QuotaWindow) *QuotaWindow { return usageview.CloneQuotaWindow(value) }
func CloneBillingSummary(value *BillingSummary) *BillingSummary {
	return usageview.CloneBillingSummary(value)
}
