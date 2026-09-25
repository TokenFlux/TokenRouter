package postgres

import (
	context "context"
	fmt "fmt"

	dbent "github.com/TokenFlux/TokenRouter/ent"
	billing "github.com/TokenFlux/TokenRouter/internal/billing"
)

// RedeemUserWriter 只允许兑换所需的原子权益增量，不能整体更新用户。
type RedeemUserWriter interface {
	UpdateBalance(context.Context, int64, float64) error
	UpdateConcurrency(context.Context, int64, int) error
}
type redeemFloorWriter interface {
	ApplyRedeemBalanceAdjustment(context.Context, int64, float64) error
	ApplyRedeemConcurrencyAdjustment(context.Context, int64, int) error
}
type RedeemMutations struct {
	client *dbent.Client
	users  RedeemUserWriter
}

func NewRedeemMutations(client *dbent.Client, users RedeemUserWriter) *RedeemMutations {
	return &RedeemMutations{client: client, users: users}
}
func (m *RedeemMutations) Within(ctx context.Context, fn func(context.Context) error) error {
	tx, err := m.client.Tx(ctx)
	if err != nil {
		return fmt.Errorf("begin transaction: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	if err := fn(dbent.NewTxContext(ctx, tx)); err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit transaction: %w", err)
	}
	return nil
}
func (m *RedeemMutations) ApplyBalance(ctx context.Context, id int64, amount float64) error {
	if amount < 0 {
		writer, ok := m.users.(redeemFloorWriter)
		if !ok {
			return billing.ErrRedeemBalanceUnsupported
		}
		return writer.ApplyRedeemBalanceAdjustment(ctx, id, amount)
	}
	return m.users.UpdateBalance(ctx, id, amount)
}
func (m *RedeemMutations) ApplyConcurrency(ctx context.Context, id int64, delta int) error {
	if delta < 0 {
		writer, ok := m.users.(redeemFloorWriter)
		if !ok {
			return billing.ErrRedeemConcurrencyUnsupported
		}
		return writer.ApplyRedeemConcurrencyAdjustment(ctx, id, delta)
	}
	return m.users.UpdateConcurrency(ctx, id, delta)
}
