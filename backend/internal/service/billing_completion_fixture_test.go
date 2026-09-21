// 本文件由 billing 拥有资金契约与规则；旧入口仅作过渡适配。
package service

import (
	"github.com/TokenFlux/TokenRouter/internal/billing"
)

// newBillingEligibilityForCompletionTest 只为完成路径提供原缓存端口，不保留旧服务包装。
func newBillingEligibilityForCompletionTest(cache billing.BillingCache) *billing.Eligibility {
	return billing.NewEligibility(cache, nil, nil, nil, func() billing.EligibilityOptions { return billing.EligibilityOptions{} }, nil, billing.NewQuotaCoordinator(), func(name string, fn func()) { RunBackgroundTask(name, BackgroundCall0(fn)) })
}
