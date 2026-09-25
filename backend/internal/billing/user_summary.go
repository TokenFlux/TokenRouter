package billing

import (
	time "time"

	contact "github.com/TokenFlux/TokenRouter/internal/identity/contact"
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
