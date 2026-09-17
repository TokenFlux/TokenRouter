// 只投影优惠码查询原有的浅层用户展示，不包含密码、TOTP 或递归实体。
package postgres

import (
	dbent "github.com/TokenFlux/TokenRouter/ent"
	"github.com/TokenFlux/TokenRouter/internal/identity/contact"
	"github.com/TokenFlux/TokenRouter/internal/promotion"
)

func promoUserView(u *dbent.User) *promotion.UserView {
	if u == nil {
		return nil
	}
	return &promotion.UserView{
		ID:                         u.ID,
		Email:                      u.Email,
		Username:                   u.Username,
		Role:                       u.Role,
		Balance:                    u.Balance,
		FrozenBalance:              u.FrozenBalance,
		Concurrency:                u.Concurrency,
		Status:                     u.Status,
		LastActiveAt:               u.LastActiveAt,
		CreatedAt:                  u.CreatedAt,
		UpdatedAt:                  u.UpdatedAt,
		DeletedAt:                  u.DeletedAt,
		BalanceNotifyEnabled:       u.BalanceNotifyEnabled,
		BalanceNotifyThresholdType: u.BalanceNotifyThresholdType,
		BalanceNotifyThreshold:     u.BalanceNotifyThreshold,
		BalanceNotifyExtraEmails:   contact.ParseNotifyEmails(u.BalanceNotifyExtraEmails),
		TotalRecharged:             u.TotalRecharged,
		RPMLimit:                   u.RpmLimit,
		APIKeyLimit:                u.APIKeyLimit,
	}
}
