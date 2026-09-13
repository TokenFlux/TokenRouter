// 关联投影只保存当前用量 HTTP 所需字段，避免引入完整身份递归图。
package usage

import (
	"time"

	"github.com/TokenFlux/TokenRouter/internal/identity/contact"
	"github.com/TokenFlux/TokenRouter/internal/routing/accessview"
)

type GroupView = accessview.GroupConfig
type AccountView struct {
	ID   int64
	Name string
}
type KeyCompositeGroupView struct {
	ID, APIKeyID, GroupID    int64
	Prefix, NormalizedPrefix string
	SortOrder                int
	UserGroupRPMOverride     *int
	Group                    *GroupView
}
type UserView struct {
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
	DeletedAt                  *time.Time
	BalanceNotifyEnabled       bool
	BalanceNotifyThresholdType string
	BalanceNotifyThreshold     *float64
	BalanceNotifyExtraEmails   []contact.Entry
	TotalRecharged             float64
	RPMLimit                   int
	APIKeyLimit                int
}
type KeyView struct {
	ID                                    int64
	UserID                                int64
	TeamID                                *int64
	TeamOwnerDisabled                     bool
	Key                                   string
	Name                                  string
	GroupID                               *int64
	IsComposite                           bool
	CompositeGroups                       []KeyCompositeGroupView
	Status                                string
	FastModePolicy                        string
	BillingMode                           string
	PreferredSubscriptionID               *int64
	ModelMapping                          map[string]string
	IPWhitelist                           []string
	IPBlacklist                           []string
	LastUsedAt                            *time.Time
	LastUsedIP                            *string
	CreatedAt                             time.Time
	UpdatedAt                             time.Time
	Group                                 *GroupView
	FallbackToDefaultGroupWhenUnavailable bool
	CurrentConcurrency                    int
	ManagedBy                             *string
	Quota                                 float64
	QuotaUsed                             float64
	ExpiresAt                             *time.Time
	RateLimit5h                           float64
	RateLimit1d                           float64
	RateLimit7d                           float64
	Usage5h                               float64
	Usage1d                               float64
	Usage7d                               float64
	Window5hStart                         *time.Time
	Window1dStart                         *time.Time
	Window7dStart                         *time.Time
}
