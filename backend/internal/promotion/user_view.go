// UserView 仅保存优惠码记录的原公开展示字段，不包含凭据或递归关联。
package promotion

import (
	"time"

	"github.com/TokenFlux/TokenRouter/internal/identity/contact"
)

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
