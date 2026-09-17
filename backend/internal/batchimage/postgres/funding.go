// 资金投影只参与调用方 SQL 事务，不提交、不回滚、不发布失效。
package postgres

import (
	"context"
	"database/sql"
	"encoding/json"

	"github.com/TokenFlux/TokenRouter/internal/billing"
)

type FundingParticipant struct {
	tx *sql.Tx
	id string
}

func NewFundingParticipant(tx *sql.Tx, id string) *FundingParticipant {
	return &FundingParticipant{tx: tx, id: id}
}
func (p *FundingParticipant) SaveReservation(ctx context.Context, balance float64, allocations []billing.BillingAllocation, hold, estimated float64) error {
	encoded, err := json.Marshal(allocations)
	if err != nil {
		return err
	}
	result, err := p.tx.ExecContext(ctx, `UPDATE batch_image_jobs
 SET balance_hold_amount = $2, subscription_hold_allocations = $3::jsonb,
 hold_amount = $4, estimated_cost = $5, updated_at = NOW()
 WHERE batch_id = $1`, p.id, balance, string(encoded), hold, estimated)
	return fundingAffected(result, err)
}
func (p *FundingParticipant) SetAllowanceReserved(ctx context.Context, reserved bool) error {
	result, err := p.tx.ExecContext(ctx, `UPDATE batch_image_jobs SET allowance_reserved = $2, updated_at = NOW() WHERE batch_id = $1`, p.id, reserved)
	return fundingAffected(result, err)
}
func fundingAffected(result sql.Result, err error) error {
	if err != nil {
		return err
	}
	n, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if n == 0 {
		return billing.ErrTaskNotFound
	}
	return nil
}
