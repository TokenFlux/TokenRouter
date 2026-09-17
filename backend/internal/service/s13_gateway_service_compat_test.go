//go:build unit

// 原私有入口仅为既有测试保留，生产消费者已经迁入所属模块。
package service

import (
	"github.com/TokenFlux/TokenRouter/internal/domain"
)

func cloneBillingAllocations(allocations []domain.BillingAllocation) []domain.BillingAllocation {
	if len(allocations) == 0 {
		return nil
	}
	cloned := make([]domain.BillingAllocation, 0, len(allocations))
	for i := range allocations {
		allocation := allocations[i]
		if allocation.SubscriptionID != nil {
			subscriptionID := *allocation.SubscriptionID
			allocation.SubscriptionID = &subscriptionID
		}
		if allocation.PlanID != nil {
			planID := *allocation.PlanID
			allocation.PlanID = &planID
		}
		cloned = append(cloned, allocation)
	}
	return cloned
}
