// 本文件维护 httpapi 的所属能力；兼容入口复用唯一实现。
package httpapi

import (
	"github.com/TokenFlux/TokenRouter/internal/billing"
	billingdto "github.com/TokenFlux/TokenRouter/internal/billing/httpapi/dto"
)

// SubscriptionPlanFromServiceShallow 委托所属模块的唯一实现。
func SubscriptionPlanFromServiceShallow(plan *billing.SubscriptionPlan) *SubscriptionPlan {
	return billingdto.SubscriptionPlanFromServiceShallow(plan)
}

// UserSubscriptionFromService 委托所属模块的唯一实现。
func UserSubscriptionFromService(sub *billing.UserSubscription) *UserSubscription {
	return billingdto.UserSubscriptionFromService(sub)
}

// UserSubscriptionFromServiceAdmin 委托所属模块的唯一实现。
func UserSubscriptionFromServiceAdmin(sub *billing.UserSubscription) *AdminUserSubscription {
	return billingdto.UserSubscriptionFromServiceAdmin(sub)
}

// BulkAssignResultFromService 委托所属模块的唯一实现。
func BulkAssignResultFromService(r *billing.BulkAssignResult) *BulkAssignResult {
	return billingdto.BulkAssignResultFromService(r)
}
