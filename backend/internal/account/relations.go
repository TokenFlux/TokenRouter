package account

import (
	"time"

	"github.com/TokenFlux/TokenRouter/internal/routing/accessview"
)

// GroupMembership 只表达账号与分组的原关联，不引入分组业务或调度优先级。
type GroupMembership struct {
	AccountID int64
	GroupID   int64
	CreatedAt time.Time
	Account   *Record
	Group     *accessview.GroupConfig
}
