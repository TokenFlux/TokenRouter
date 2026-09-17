// 优惠码锁定、资金参与、usage 与次数在同一个原 Ent 事务内提交。
package postgres

import (
	"context"
	"fmt"
	"time"

	dbent "github.com/TokenFlux/TokenRouter/ent"
	"github.com/TokenFlux/TokenRouter/internal/promotion"
)

type PromoBalance interface {
	CreditRegistrationPromo(context.Context, int64, float64) error
}
type PromoBalanceFactory func(*dbent.Tx) PromoBalance
type PromoMutations struct {
	client   *dbent.Client
	balances PromoBalanceFactory
}

func NewPromoMutations(client *dbent.Client, balances PromoBalanceFactory) *PromoMutations {
	return &PromoMutations{client: client, balances: balances}
}
func (m *PromoMutations) ApplyCode(ctx context.Context, userID int64, code string, validate func(*promotion.PromoCode) error, now func() time.Time) error {
	tx, err := m.client.Tx(ctx)
	if err != nil {
		return fmt.Errorf("begin transaction: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	txCtx := dbent.NewTxContext(ctx, tx)
	repo := NewPromoCodeRepository(tx.Client())
	value, err := repo.GetByCodeForUpdate(txCtx, code)
	if err != nil {
		return err
	}
	if err = validate(value); err != nil {
		return err
	}
	existing, err := repo.GetUsageByPromoCodeAndUser(txCtx, value.ID, userID)
	if err != nil {
		return fmt.Errorf("check existing usage: %w", err)
	}
	if existing != nil {
		return promotion.ErrPromoCodeAlreadyUsed
	}
	if err = m.balances(tx).CreditRegistrationPromo(txCtx, userID, value.BonusAmount); err != nil {
		return fmt.Errorf("update user balance: %w", err)
	}
	usage := &promotion.PromoCodeUsage{PromoCodeID: value.ID, UserID: userID, BonusAmount: value.BonusAmount, UsedAt: now()}
	if err = repo.CreateUsage(txCtx, usage); err != nil {
		return fmt.Errorf("create usage record: %w", err)
	}
	if err = repo.IncrementUsedCount(txCtx, value.ID); err != nil {
		return fmt.Errorf("increment used count: %w", err)
	}
	if err = tx.Commit(); err != nil {
		return fmt.Errorf("commit transaction: %w", err)
	}
	return nil
}
