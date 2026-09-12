// 本文件维护 httpapi 的所属能力；兼容入口复用唯一实现。
package httpapi

import (
	billingdto "github.com/TokenFlux/TokenRouter/internal/billing/httpapi/dto"
)

type SubscriptionPlan = billingdto.SubscriptionPlan

type SubscriptionPlanGroup = billingdto.SubscriptionPlanGroup

type UserSubscription = billingdto.UserSubscription

type AdminUserSubscription = billingdto.AdminUserSubscription

type BulkAssignResult = billingdto.BulkAssignResult

type UserSummaryResponse = billingdto.UserSummaryResponse
