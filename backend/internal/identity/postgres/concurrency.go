// 本文件维护 postgres 的所属能力；兼容入口复用唯一实现。
package postgres

import (
	context "context"

	dbent "github.com/TokenFlux/TokenRouter/ent"
	dbuser "github.com/TokenFlux/TokenRouter/ent/user"
	identitycore "github.com/TokenFlux/TokenRouter/internal/identity"
)

func (r *ConcurrencyStore) UpdateConcurrency(ctx context.Context, id int64, amount int) error {
	client := clientFromContext(ctx, r.client)
	n, err := client.User.Update().Where(dbuser.IDEQ(id)).AddConcurrency(amount).Save(ctx)
	if err != nil {
		return translatePersistenceError(err, identitycore.ErrUserNotFound, nil)
	}
	if n == 0 {
		return identitycore.ErrUserNotFound
	}
	return nil
}

// ApplyRedeemConcurrencyAdjustment 原子应用兑换码并发增量，并确保并发数不低于 0。
func (r *ConcurrencyStore) ApplyRedeemConcurrencyAdjustment(ctx context.Context, id int64, delta int) error {
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
		return identitycore.ErrUserNotFound
	}
	return nil
}

// ConcurrencyStore 只拥有身份并发额度字段；兑换事务仍由 billing 提交。
type ConcurrencyStore struct{ client *dbent.Client }

func NewConcurrencyStore(client *dbent.Client) *ConcurrencyStore {
	return &ConcurrencyStore{client: client}
}

// ConcurrencyInTx 明确绑定外层 Ent 事务，不创建或提交事务。
func ConcurrencyInTx(tx *dbent.Tx) *ConcurrencyStore { return NewConcurrencyStore(tx.Client()) }
