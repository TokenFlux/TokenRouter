// 本文件维护 postgres 的所属能力；兼容入口复用唯一实现。
package postgres

import (
	context "context"
	fmt "fmt"

	dbent "github.com/TokenFlux/TokenRouter/ent"
)

// runGroupMutationTx 在可用时为分组变更开启事务，保证“切换默认组”过程原子化。
func (s *GroupStore) Mutate(ctx context.Context, fn func(context.Context) error) error {
	if dbent.TxFromContext(ctx) != nil || s.client == nil {
		return fn(ctx)
	}

	tx, err := s.client.Tx(ctx)
	if err != nil {
		return fmt.Errorf("begin group mutation transaction: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	txCtx := dbent.NewTxContext(ctx, tx)
	if err := fn(txCtx); err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit group mutation transaction: %w", err)
	}
	return nil
}
