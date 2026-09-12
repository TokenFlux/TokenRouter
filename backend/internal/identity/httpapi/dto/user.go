// 本文件维护 dto 的所属能力；兼容入口复用唯一实现。
package dto

import (
	billinghttpapi "github.com/TokenFlux/TokenRouter/internal/billing/httpapi/dto"
	identity "github.com/TokenFlux/TokenRouter/internal/identity"
	time "time"
)

type User[K any] struct {
	ID            int64   `json:"id"`
	Email         string  `json:"email"`
	Username      string  `json:"username"`
	Role          string  `json:"role"`
	Balance       float64 `json:"balance"`
	FrozenBalance float64 `json:"frozen_balance"`
	Concurrency   int     `json:"concurrency"`
	Status        string  `json:"status"`
	AllowedGroups []int64 `json:"allowed_groups"`
	// DisabledPublicGroups 为管理员显式禁止该用户使用的公开分组 ID。
	DisabledPublicGroups []int64    `json:"disabled_public_groups"`
	LastActiveAt         *time.Time `json:"last_active_at,omitempty"`
	CreatedAt            time.Time  `json:"created_at"`
	UpdatedAt            time.Time  `json:"updated_at"`
	DeletedAt            *time.Time `json:"deleted_at,omitempty"`

	// 余额不足通知
	BalanceNotifyEnabled       bool               `json:"balance_notify_enabled"`
	BalanceNotifyThresholdType string             `json:"balance_notify_threshold_type"`
	BalanceNotifyThreshold     *float64           `json:"balance_notify_threshold"`
	BalanceNotifyExtraEmails   []NotifyEmailEntry `json:"balance_notify_extra_emails"`
	TotalRecharged             float64            `json:"total_recharged"`

	// RPMLimit 用户级每分钟请求数上限（0 = 不限制），仅在所用分组未设置 rpm_limit 时作为兜底生效。
	RPMLimit int `json:"rpm_limit"`
	// APIKeyLimit 用户可创建的 API Key 数量上限，0 表示不限制。
	APIKeyLimit int `json:"api_key_limit"`

	APIKeys       []K                               `json:"api_keys,omitempty"`
	Subscriptions []billinghttpapi.UserSubscription `json:"subscriptions,omitempty"`
}

// AdminUser 是管理员接口使用的 user DTO（包含敏感/内部字段）。
// 注意：普通用户接口不得返回 notes 等管理员备注信息。
type AdminUser[K any] struct {
	User[K]

	Notes      string     `json:"notes"`
	LastUsedAt *time.Time `json:"last_used_at"`
	// GroupRates 用户专属分组倍率配置
	// map[groupID]rateMultiplier
	GroupRates map[int64]float64 `json:"group_rates,omitempty"`
}

func UserFromIdentityShallow[K any](u *identity.User) *User[K] {
	if u == nil {
		return nil
	}
	return &User[K]{
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
		BalanceNotifyExtraEmails:   NotifyEmailEntriesFromIdentity(u.BalanceNotifyExtraEmails),
		TotalRecharged:             u.TotalRecharged,
		RPMLimit:                   u.RPMLimit,
		APIKeyLimit:                u.APIKeyLimit,
		DeletedAt:                  u.DeletedAt,
	}
}

// UserFromIdentity 投影当前用户与已有订阅；Key 关联由消费者提供独立展示值。
func UserFromIdentity[K any](u *identity.User, keys []K) *User[K] {
	out := UserFromIdentityShallow[K](u)
	if out == nil {
		return nil
	}
	out.APIKeys = keys
	if len(u.Subscriptions) > 0 {
		out.Subscriptions = make([]billinghttpapi.UserSubscription, 0, len(u.Subscriptions))
		for i := range u.Subscriptions {
			out.Subscriptions = append(out.Subscriptions, *billinghttpapi.UserSubscriptionFromService(&u.Subscriptions[i]))
		}
	}
	return out
}
func AdminUserFromIdentity[K any](u *identity.User, keys []K) *AdminUser[K] {
	base := UserFromIdentity(u, keys)
	if base == nil {
		return nil
	}
	return &AdminUser[K]{User: *base, Notes: u.Notes, LastUsedAt: u.LastUsedAt, GroupRates: u.GroupRates}
}

// AuthResponse 保留认证响应字段和省略条件。
type AuthResponse[K any] struct {
	AccessToken  string   `json:"access_token"`
	RefreshToken string   `json:"refresh_token,omitempty"`
	ExpiresIn    int      `json:"expires_in,omitempty"`
	TokenType    string   `json:"token_type"`
	User         *User[K] `json:"user"`
}
