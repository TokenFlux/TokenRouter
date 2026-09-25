package billing

import (
	"context"

	"github.com/TokenFlux/TokenRouter/internal/pkg/apperror"
)

// BalanceChange 记录一次余额变更前后的值。
type BalanceChange struct {
	Old float64
	New float64
}

// BalanceAdjuster 提供闭合原子调账，不接受完整用户快照。
type BalanceAdjuster interface {
	SetBalance(context.Context, int64, float64) (BalanceChange, error)
	AdjustBalance(context.Context, int64, float64) (BalanceChange, error)
}

var ErrBalanceNegative = apperror.BadRequest("BALANCE_NEGATIVE", "balance cannot be negative")
