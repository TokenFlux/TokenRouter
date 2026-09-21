// 本文件由 billing 拥有资金契约与规则；旧入口仅作过渡适配。
package postgres

import (
	context "context"
	fmt "fmt"

	dbent "github.com/TokenFlux/TokenRouter/ent"
)

type RedeemAdministrationMutations struct{ client *dbent.Client }

func NewRedeemAdministrationMutations(client *dbent.Client) *RedeemAdministrationMutations {
	return &RedeemAdministrationMutations{client: client}
}
func (m *RedeemAdministrationMutations) Mutation(ctx context.Context, fn func(context.Context) error) error {
	if dbent.TxFromContext(ctx) != nil || m.client == nil {
		return fn(ctx)
	}
	tx, err := m.client.Tx(ctx)
	if err != nil {
		return fmt.Errorf("begin redeem code mutation transaction: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	if err := fn(dbent.NewTxContext(ctx, tx)); err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit redeem code mutation transaction: %w", err)
	}
	return nil
}
func (m *RedeemAdministrationMutations) Adjustment(ctx context.Context, fn func(context.Context) error) error {
	if m.client == nil {
		return fn(ctx)
	}
	tx, err := m.client.Tx(ctx)
	if err != nil {
		return fmt.Errorf("begin adjustment redeem transaction: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	if err := fn(dbent.NewTxContext(ctx, tx)); err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit adjustment redeem transaction: %w", err)
	}
	return nil
}
func (m *RedeemAdministrationMutations) PlanExists(ctx context.Context, id int64) error {
	_, err := clientFromContext(ctx, m.client).SubscriptionPlan.Get(ctx, id)
	return err
}
