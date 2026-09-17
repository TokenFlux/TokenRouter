// 推广事务由本 Adapter 拥有；跨模块能力仅接收原事务，不新增 context key。
package postgres

import (
	"context"
	"fmt"

	dbent "github.com/TokenFlux/TokenRouter/ent"
	"github.com/TokenFlux/TokenRouter/internal/identity"
)

type TransferBalance interface {
	CreditAffiliateTransfer(context.Context, int64, float64) error
}
type TransferBalanceFactory func(*dbent.Tx) TransferBalance

func clientFromContext(ctx context.Context, fallback *dbent.Client) *dbent.Client {
	if tx := dbent.TxFromContext(ctx); tx != nil {
		return tx.Client()
	}
	return fallback
}
func (r *affiliateRepository) WithLockedInviter(ctx context.Context, id int64, fn func(context.Context) error) error {
	return r.withTx(ctx, func(txCtx context.Context, c *dbent.Client) error {
		rows, err := c.QueryContext(txCtx, "SELECT user_id FROM user_affiliates WHERE user_id=$1 FOR UPDATE", id)
		if err != nil {
			return fmt.Errorf("lock affiliate profile: %w", err)
		}
		exists := rows.Next()
		readErr := rows.Err()
		closeErr := rows.Close()
		if readErr != nil {
			return readErr
		}
		if closeErr != nil {
			return closeErr
		}
		if !exists {
			return identity.ErrUserNotFound
		}
		return fn(txCtx)
	})
}
