// 本文件维护 postgres 的所属能力；兼容入口复用唯一实现。
package postgres

import (
	"context"
	"database/sql"
	"errors"

	"github.com/TokenFlux/TokenRouter/internal/billing"
	postgresinfra "github.com/TokenFlux/TokenRouter/internal/infra/postgres"
)

func DeductAvailableBalance(ctx context.Context, q postgresinfra.Queryer, userID int64, amount float64) (float64, float64, error) {
	const query = `
		WITH locked_user AS (
			SELECT id, balance
			FROM users
			WHERE id = $2
				AND deleted_at IS NULL
			FOR UPDATE
		), updated AS (
			UPDATE users
			SET balance = CASE
				WHEN locked_user.balance <= 0 THEN locked_user.balance
				WHEN locked_user.balance <= $1 THEN 0
				ELSE locked_user.balance - $1
			END,
				updated_at = NOW()
			FROM locked_user
			WHERE users.id = locked_user.id
			RETURNING users.balance
		)
		SELECT
			updated.balance,
			CASE
				WHEN locked_user.balance <= 0 THEN 0
				WHEN locked_user.balance <= $1 THEN locked_user.balance
				ELSE $1
			END AS deducted_amount
		FROM updated
		CROSS JOIN locked_user
	`

	var (
		newBalance     float64
		deductedAmount float64
	)
	if err := postgresinfra.ScanSingleRow(ctx, q, query, []any{amount, userID}, &newBalance, &deductedAmount); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return 0, 0, billing.ErrUserNotFound
		}
		return 0, 0, err
	}
	return newBalance, deductedAmount, nil
}
