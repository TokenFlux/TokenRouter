// 本文件维护 dto 的所属能力；兼容入口复用唯一实现。
package dto

import (
	"strconv"

	"github.com/TokenFlux/TokenRouter/internal/billing"
)

func SubscriptionPlanFromServiceShallow(plan *billing.SubscriptionPlan) *SubscriptionPlan {
	if plan == nil {
		return nil
	}
	out := &SubscriptionPlan{
		ID:                   plan.ID,
		Name:                 plan.Name,
		Description:          plan.Description,
		Price:                plan.Price,
		OriginalPrice:        plan.OriginalPrice,
		Currency:             plan.Currency,
		ValidityDays:         plan.ValidityDays,
		ValidityUnit:         plan.ValidityUnit,
		GroupIDs:             append([]int64(nil), plan.GroupIDs...),
		GroupRateMultipliers: CloneInt64Float64Map(plan.GroupRateMultipliers),
		GroupsRestricted:     plan.GroupsRestricted || len(plan.GroupIDs) > 0,
		DailyLimitUSD:        plan.DailyLimitUSD,
		WeeklyLimitUSD:       plan.WeeklyLimitUSD,
		MonthlyLimitUSD:      plan.MonthlyLimitUSD,
		Features:             plan.Features,
		ProductName:          plan.ProductName,
		ForSale:              plan.ForSale,
		SortOrder:            plan.SortOrder,
		CreatedAt:            plan.CreatedAt,
		UpdatedAt:            plan.UpdatedAt,
	}
	out.ApplicableGroups = make([]SubscriptionPlanGroup, 0, len(plan.ApplicableGroups))
	for _, group := range plan.ApplicableGroups {
		out.ApplicableGroups = append(out.ApplicableGroups, SubscriptionPlanGroup{ID: group.ID, Name: group.Name})
	}
	if out.ApplicableGroups == nil {
		out.ApplicableGroups = []SubscriptionPlanGroup{}
	}
	return out
}

func UserSubscriptionFromService(sub *billing.UserSubscription) *UserSubscription {
	if sub == nil {
		return nil
	}
	out := UserSubscriptionFromServiceBase(sub)
	return &out
}

// UserSubscriptionFromServiceAdmin 将 billing.UserSubscription 转换为管理员 DTO。
// 管理员接口会额外返回分配人、分配时间和备注。
func UserSubscriptionFromServiceAdmin(sub *billing.UserSubscription) *AdminUserSubscription {
	if sub == nil {
		return nil
	}
	return &AdminUserSubscription{
		UserSubscription: UserSubscriptionFromServiceBase(sub),
		AssignedBy:       sub.AssignedBy,
		AssignedAt:       sub.AssignedAt,
		Notes:            sub.Notes,
		AssignedByUser:   UserSummaryFromBilling(sub.AssignedByUser),
	}
}

func UserSubscriptionFromServiceBase(sub *billing.UserSubscription) UserSubscription {
	return UserSubscription{
		ID:                 sub.ID,
		UserID:             sub.UserID,
		PlanID:             sub.PlanID,
		StartsAt:           sub.StartsAt,
		ExpiresAt:          sub.ExpiresAt,
		Status:             sub.Status,
		DailyWindowStart:   sub.DailyWindowStart,
		WeeklyWindowStart:  sub.WeeklyWindowStart,
		MonthlyWindowStart: sub.MonthlyWindowStart,
		DailyLimitUSD:      sub.DailyLimitUSD,
		WeeklyLimitUSD:     sub.WeeklyLimitUSD,
		MonthlyLimitUSD:    sub.MonthlyLimitUSD,
		DailyUsageUSD:      sub.DailyUsageUSD,
		WeeklyUsageUSD:     sub.WeeklyUsageUSD,
		MonthlyUsageUSD:    sub.MonthlyUsageUSD,
		CreatedAt:          sub.CreatedAt,
		UpdatedAt:          sub.UpdatedAt,
		RevokedAt:          sub.DeletedAt,
		User:               UserSummaryFromBilling(sub.User),
		Plan:               SubscriptionPlanFromServiceShallow(sub.Plan),
	}
}

func BulkAssignResultFromService(r *billing.BulkAssignResult) *BulkAssignResult {
	if r == nil {
		return nil
	}
	subs := make([]AdminUserSubscription, 0, len(r.Subscriptions))
	for i := range r.Subscriptions {
		subs = append(subs, *UserSubscriptionFromServiceAdmin(&r.Subscriptions[i]))
	}
	statuses := make(map[string]string, len(r.Statuses))
	for userID, status := range r.Statuses {
		statuses[strconv.FormatInt(userID, 10)] = status
	}
	return &BulkAssignResult{
		SuccessCount:  r.SuccessCount,
		CreatedCount:  r.CreatedCount,
		ReusedCount:   r.ReusedCount,
		FailedCount:   r.FailedCount,
		Subscriptions: subs,
		Errors:        r.Errors,
		Statuses:      statuses,
	}
}

func CloneInt64Float64Map(in map[int64]float64) map[int64]float64 {
	if len(in) == 0 {
		return map[int64]float64{}
	}
	out := make(map[int64]float64, len(in))
	for k, v := range in {
		out[k] = v
	}
	return out
}

func UserSummaryFromBilling(u *billing.UserSummary) *UserSummaryResponse {
	if u == nil {
		return nil
	}
	return &UserSummaryResponse{
		ID:                         u.ID,
		Email:                      u.Email,
		Username:                   u.Username,
		Role:                       u.Role,
		Balance:                    u.Balance,
		FrozenBalance:              u.FrozenBalance,
		Concurrency:                u.Concurrency,
		Status:                     u.Status,
		AllowedGroups:              u.AllowedGroups,
		DisabledPublicGroups:       u.DisabledPublicGroups,
		LastActiveAt:               u.LastActiveAt,
		CreatedAt:                  u.CreatedAt,
		UpdatedAt:                  u.UpdatedAt,
		BalanceNotifyEnabled:       u.BalanceNotifyEnabled,
		BalanceNotifyThresholdType: u.BalanceNotifyThresholdType,
		BalanceNotifyThreshold:     u.BalanceNotifyThreshold,
		BalanceNotifyExtraEmails:   CloneNotifyEmailEntries(u.BalanceNotifyExtraEmails),
		TotalRecharged:             u.TotalRecharged,
		RPMLimit:                   u.RPMLimit,
		APIKeyLimit:                u.APIKeyLimit,
		DeletedAt:                  u.DeletedAt,
	}
}

// CloneNotifyEmailEntries 保留 nil 与显式空数组的 HTTP 形状。
func CloneNotifyEmailEntries(v []billing.NotifyEmailSummary) []billing.NotifyEmailSummary {
	if v == nil {
		return nil
	}
	out := make([]billing.NotifyEmailSummary, len(v))
	copy(out, v)
	return out
}
