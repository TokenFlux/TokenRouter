//go:build unit

package server_test

import (
	"context"

	"github.com/TokenFlux/TokenRouter/internal/billing"
	"github.com/TokenFlux/TokenRouter/internal/identity"
	"github.com/TokenFlux/TokenRouter/internal/routing"
)

// contractSubscriptionGroups 保留原一次分组读取及名称投影。
type contractSubscriptionGroups struct{ source routing.GroupRepository }

func (p contractSubscriptionGroups) GetByIDLite(ctx context.Context, id int64) (*billing.SubscriptionPlanGroup, error) {
	if p.source == nil {
		return nil, nil
	}
	v, err := p.source.GetByIDLite(ctx, id)
	if v == nil || err != nil {
		return nil, err
	}
	return &billing.SubscriptionPlanGroup{ID: v.ID, Name: v.Name}, nil
}

// contractRedeemUsers 保留 HTTP 契约中的身份展示与余额投影。
type contractRedeemUsers struct{ source identity.UserRepository }

func (p contractRedeemUsers) GetByID(ctx context.Context, id int64) (*billing.UserSummary, error) {
	v, err := p.source.GetByID(ctx, id)
	return contractBillingUserSummary(v), err
}

// contractBillingUserSummary 保持原夹具的字段及通知邮箱副本边界。
func contractBillingUserSummary(u *identity.User) *billing.UserSummary {
	if u == nil {
		return nil
	}
	out := &billing.UserSummary{
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
		TotalRecharged:             u.TotalRecharged,
		RPMLimit:                   u.RPMLimit,
		APIKeyLimit:                u.APIKeyLimit,
		DeletedAt:                  u.DeletedAt,
	}
	if u.BalanceNotifyExtraEmails != nil {
		out.BalanceNotifyExtraEmails = make([]billing.NotifyEmailSummary, len(u.BalanceNotifyExtraEmails))
		copy(out.BalanceNotifyExtraEmails, u.BalanceNotifyExtraEmails)
	}
	return out
}
