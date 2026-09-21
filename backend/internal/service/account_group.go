package service

import (
	"time"

	routing "github.com/TokenFlux/TokenRouter/internal/routing"
)

type AccountGroup struct {
	AccountID int64
	GroupID   int64
	CreatedAt time.Time

	Account *Account
	Group   *routing.Group
}
