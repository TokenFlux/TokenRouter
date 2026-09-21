// 本文件维护 dto 的所属能力；兼容入口复用唯一实现。
package dto

import (
	time "time"

	billing "github.com/TokenFlux/TokenRouter/internal/billing"
)

type SubscriptionPlan struct {
	ID                   int64                   `json:"id"`
	Name                 string                  `json:"name"`
	Description          string                  `json:"description"`
	Price                float64                 `json:"price"`
	OriginalPrice        *float64                `json:"original_price,omitempty"`
	Currency             string                  `json:"currency,omitempty"`
	ValidityDays         int                     `json:"validity_days"`
	ValidityUnit         string                  `json:"validity_unit"`
	GroupIDs             []int64                 `json:"group_ids"`
	GroupRateMultipliers map[int64]float64       `json:"group_rate_multipliers"`
	GroupsRestricted     bool                    `json:"groups_restricted"`
	ApplicableGroups     []SubscriptionPlanGroup `json:"applicable_groups"`
	DailyLimitUSD        *float64                `json:"daily_limit_usd"`
	WeeklyLimitUSD       *float64                `json:"weekly_limit_usd"`
	MonthlyLimitUSD      *float64                `json:"monthly_limit_usd"`
	Features             string                  `json:"features"`
	ProductName          string                  `json:"product_name"`
	ForSale              bool                    `json:"for_sale"`
	SortOrder            int                     `json:"sort_order"`
	CreatedAt            time.Time               `json:"created_at"`
	UpdatedAt            time.Time               `json:"updated_at"`
}

type SubscriptionPlanGroup struct {
	ID   int64  `json:"id"`
	Name string `json:"name"`
}

type UserSubscription struct {
	ID     int64 `json:"id"`
	UserID int64 `json:"user_id"`
	PlanID int64 `json:"plan_id"`

	StartsAt  time.Time `json:"starts_at"`
	ExpiresAt time.Time `json:"expires_at"`
	Status    string    `json:"status"`

	DailyWindowStart   *time.Time `json:"daily_window_start"`
	WeeklyWindowStart  *time.Time `json:"weekly_window_start"`
	MonthlyWindowStart *time.Time `json:"monthly_window_start"`

	DailyLimitUSD   *float64 `json:"daily_limit_usd"`
	WeeklyLimitUSD  *float64 `json:"weekly_limit_usd"`
	MonthlyLimitUSD *float64 `json:"monthly_limit_usd"`

	DailyUsageUSD   float64 `json:"daily_usage_usd"`
	WeeklyUsageUSD  float64 `json:"weekly_usage_usd"`
	MonthlyUsageUSD float64 `json:"monthly_usage_usd"`

	CreatedAt time.Time  `json:"created_at"`
	UpdatedAt time.Time  `json:"updated_at"`
	RevokedAt *time.Time `json:"revoked_at,omitempty"`

	User *UserSummaryResponse `json:"user,omitempty"`
	Plan *SubscriptionPlan    `json:"plan,omitempty"`
}

// AdminUserSubscription 是管理员接口使用的订阅 DTO（包含分配信息/备注等字段）。
// 注意：普通用户接口不得返回 assigned_by/assigned_at/notes/assigned_by_user 等管理员字段。
type AdminUserSubscription struct {
	UserSubscription

	AssignedBy *int64    `json:"assigned_by"`
	AssignedAt time.Time `json:"assigned_at"`
	Notes      string    `json:"notes"`

	AssignedByUser *UserSummaryResponse `json:"assigned_by_user,omitempty"`
}

type BulkAssignResult struct {
	SuccessCount  int                     `json:"success_count"`
	CreatedCount  int                     `json:"created_count"`
	ReusedCount   int                     `json:"reused_count"`
	FailedCount   int                     `json:"failed_count"`
	Subscriptions []AdminUserSubscription `json:"subscriptions"`
	Errors        []string                `json:"errors"`
	Statuses      map[string]string       `json:"statuses,omitempty"`
}

type UserSummaryResponse struct {
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
	BalanceNotifyEnabled       bool                         `json:"balance_notify_enabled"`
	BalanceNotifyThresholdType string                       `json:"balance_notify_threshold_type"`
	BalanceNotifyThreshold     *float64                     `json:"balance_notify_threshold"`
	BalanceNotifyExtraEmails   []billing.NotifyEmailSummary `json:"balance_notify_extra_emails"`
	TotalRecharged             float64                      `json:"total_recharged"`

	// RPMLimit 用户级每分钟请求数上限（0 = 不限制），仅在所用分组未设置 rpm_limit 时作为兜底生效。
	RPMLimit int `json:"rpm_limit"`
	// APIKeyLimit 用户可创建的 API Key 数量上限，0 表示不限制。
	APIKeyLimit int `json:"api_key_limit"`
}
