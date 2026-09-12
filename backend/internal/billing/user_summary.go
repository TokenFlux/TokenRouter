// 本文件由 billing 拥有资金契约与规则；旧入口仅作过渡适配。
package billing

import (
	contact "github.com/TokenFlux/TokenRouter/internal/identity/contact"
	time "time"
)

// UserSummary 是权益查询所需的只读用户展示投影。
type UserSummary struct {
	ID                         int64
	Email                      string
	Username                   string
	Role                       string
	Balance                    float64
	FrozenBalance              float64
	Concurrency                int
	Status                     string
	AllowedGroups              []int64
	DisabledPublicGroups       []int64
	LastActiveAt               *time.Time
	CreatedAt                  time.Time
	UpdatedAt                  time.Time
	BalanceNotifyEnabled       bool
	BalanceNotifyThresholdType string
	BalanceNotifyThreshold     *float64
	BalanceNotifyExtraEmails   []NotifyEmailSummary
	TotalRecharged             float64
	RPMLimit                   int
	APIKeyLimit                int
	DeletedAt                  *time.Time
}

// NotifyEmailSummary 是身份联系邮箱的只读值投影。
type NotifyEmailSummary = contact.Entry
