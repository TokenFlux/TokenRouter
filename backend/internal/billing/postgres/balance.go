// 本文件由 billing 拥有资金契约与规则；旧入口仅作过渡适配。
package postgres

import (
	context "context"
	dbent "github.com/TokenFlux/TokenRouter/ent"
	dbuser "github.com/TokenFlux/TokenRouter/ent/user"
	billing "github.com/TokenFlux/TokenRouter/internal/billing"
)

// BalanceStore 保存原子用户权益操作；身份并发数参与端口在 S05 继续解耦。
type BalanceStore struct{ client *dbent.Client }

func NewBalanceStore(client *dbent.Client) *BalanceStore { return &BalanceStore{client: client} }
func (r *BalanceStore) UpdateBalance(ctx context.Context, id int64, amount float64) error {
	client := clientFromContext(ctx, r.client)
	update := client.User.Update().Where(dbuser.IDEQ(id)).AddBalance(amount)
	// Track cumulative recharge amount for percentage-based notifications
	if amount > 0 {
		update = update.AddTotalRecharged(amount)
	}
	n, err := update.Save(ctx)
	if err != nil {
		return translatePersistenceError(err, billing.ErrUserNotFound, nil)
	}
	if n == 0 {
		return billing.ErrUserNotFound
	}
	return nil
}

func (r *BalanceStore) AddBalance(ctx context.Context, id int64, amount float64) error {
	if amount == 0 {
		return nil
	}
	client := clientFromContext(ctx, r.client)
	n, err := client.User.Update().
		Where(dbuser.IDEQ(id)).
		AddBalance(amount).
		Save(ctx)
	if err != nil {
		return translatePersistenceError(err, billing.ErrUserNotFound, nil)
	}
	if n == 0 {
		return billing.ErrUserNotFound
	}
	return nil
}

// ApplyRedeemBalanceAdjustment 原子应用兑换码余额增量，并确保余额不低于 0。
func (r *BalanceStore) ApplyRedeemBalanceAdjustment(ctx context.Context, id int64, delta float64) error {
	const updateSQL = `
		UPDATE users
		SET balance = GREATEST(balance + $1, 0), updated_at = NOW()
		WHERE id = $2 AND deleted_at IS NULL
	`
	client := clientFromContext(ctx, r.client)
	result, err := client.ExecContext(ctx, updateSQL, delta, id)
	if err != nil {
		return err
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if affected == 0 {
		return billing.ErrUserNotFound
	}
	return nil
}

// AdjustBalance 原子地把 delta 累加到余额上，结果为负时整条语句不生效。
// 相比"读余额 → 算新值 → 整行写回"，这里把读与写压进同一条 UPDATE，
// 并发的计费扣款不会被旧快照覆盖。
func (r *BalanceStore) AdjustBalance(ctx context.Context, id int64, delta float64) (billing.BalanceChange, error) {
	const updateSQL = `
		UPDATE users
		SET balance = balance + $1, updated_at = NOW()
		WHERE id = $2 AND deleted_at IS NULL AND balance + $1 >= 0
		RETURNING balance - $1, balance
	`
	change, ok, err := scanBalanceChange(ctx, clientFromContext(ctx, r.client), updateSQL, delta, id)
	if err != nil {
		return billing.BalanceChange{}, err
	}
	if ok {
		return change, nil
	}

	// 0 行既可能是用户不存在，也可能是余额不足以承受这次扣减，需要区分。
	current, err := r.currentBalance(ctx, id)
	if err != nil {
		return billing.BalanceChange{}, err
	}
	return billing.BalanceChange{Old: current, New: current + delta}, billing.ErrBalanceNegative
}

// SetBalance 原子地把余额置为 value，并返回变更前后的值。
func (r *BalanceStore) SetBalance(ctx context.Context, id int64, value float64) (billing.BalanceChange, error) {
	if value < 0 {
		// 连同当前余额一起返回，便于上层给出可读的错误信息。
		current, err := r.currentBalance(ctx, id)
		if err != nil {
			return billing.BalanceChange{}, err
		}
		return billing.BalanceChange{Old: current, New: value}, billing.ErrBalanceNegative
	}
	const updateSQL = `
		WITH prev AS MATERIALIZED (
            SELECT id, balance FROM users
            WHERE id = $2 AND deleted_at IS NULL
            FOR NO KEY UPDATE
        )
        UPDATE users AS u
        SET balance = $1, updated_at = NOW()
        FROM prev
        WHERE u.id = prev.id AND u.deleted_at IS NULL
        RETURNING prev.balance, u.balance
	`
	change, ok, err := scanBalanceChange(ctx, clientFromContext(ctx, r.client), updateSQL, value, id)
	if err != nil {
		return billing.BalanceChange{}, err
	}
	if !ok {
		return billing.BalanceChange{}, billing.ErrUserNotFound
	}
	return change, nil
}

// currentBalance 读取用户当前余额，用户不存在时返回 ErrUserNotFound。
func (r *BalanceStore) currentBalance(ctx context.Context, id int64) (balance float64, err error) {
	rows, err := clientFromContext(ctx, r.client).QueryContext(ctx,
		`SELECT balance FROM users WHERE id = $1 AND deleted_at IS NULL`, id)
	if err != nil {
		return 0, err
	}
	defer func() {
		if closeErr := rows.Close(); closeErr != nil && err == nil {
			err = closeErr
		}
	}()
	if !rows.Next() {
		if rowsErr := rows.Err(); rowsErr != nil {
			return 0, rowsErr
		}
		return 0, billing.ErrUserNotFound
	}
	if err := rows.Scan(&balance); err != nil {
		return 0, err
	}
	return balance, rows.Err()
}

// scanBalanceChange 执行一条 RETURNING 旧余额、新余额的语句。ok 为 false 表示语句未命中任何行。
func scanBalanceChange(ctx context.Context, client *dbent.Client, query string, args ...any) (change billing.BalanceChange, ok bool, err error) {
	rows, err := client.QueryContext(ctx, query, args...)
	if err != nil {
		return billing.BalanceChange{}, false, err
	}
	defer func() {
		if closeErr := rows.Close(); closeErr != nil && err == nil {
			err = closeErr
		}
	}()
	if !rows.Next() {
		if rowsErr := rows.Err(); rowsErr != nil {
			return billing.BalanceChange{}, false, rowsErr
		}
		return billing.BalanceChange{}, false, nil
	}
	if err := rows.Scan(&change.Old, &change.New); err != nil {
		return billing.BalanceChange{}, false, err
	}
	return change, true, rows.Err()
}

func (r *BalanceStore) UpdateConcurrency(ctx context.Context, id int64, amount int) error {
	client := clientFromContext(ctx, r.client)
	n, err := client.User.Update().Where(dbuser.IDEQ(id)).AddConcurrency(amount).Save(ctx)
	if err != nil {
		return translatePersistenceError(err, billing.ErrUserNotFound, nil)
	}
	if n == 0 {
		return billing.ErrUserNotFound
	}
	return nil
}

// ApplyRedeemConcurrencyAdjustment 原子应用兑换码并发增量，并确保并发数不低于 0。
func (r *BalanceStore) ApplyRedeemConcurrencyAdjustment(ctx context.Context, id int64, delta int) error {
	const updateSQL = `
		UPDATE users
		SET concurrency = GREATEST(concurrency + $1, 0), updated_at = NOW()
		WHERE id = $2 AND deleted_at IS NULL
	`
	client := clientFromContext(ctx, r.client)
	result, err := client.ExecContext(ctx, updateSQL, delta, id)
	if err != nil {
		return err
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if affected == 0 {
		return billing.ErrUserNotFound
	}
	return nil
}
