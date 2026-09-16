// RiskStatusParticipant 在调用方已有连接内取得用户锁并写状态。
package postgres

import (
	"context"
	"database/sql"
	"errors"
)

type RiskStatusParticipant struct{ tx *sql.Tx }

func NewRiskStatusParticipant(tx *sql.Tx) *RiskStatusParticipant {
	return &RiskStatusParticipant{tx: tx}
}
func (p *RiskStatusParticipant) LockUser(ctx context.Context, id int64) (string, bool, error) {
	var status string
	err := p.tx.QueryRowContext(ctx, `SELECT status FROM users WHERE id = $1 FOR UPDATE`, id).Scan(&status)
	if errors.Is(err, sql.ErrNoRows) {
		return "", false, nil
	}
	return status, err == nil, err
}
func (p *RiskStatusParticipant) SetDisabled(ctx context.Context, id int64) (bool, error) {
	result, err := p.tx.ExecContext(ctx, `UPDATE users SET status = $2, updated_at = NOW() WHERE id = $1 AND status <> $2`, id, "disabled")
	if err != nil {
		return false, err
	}
	n, err := result.RowsAffected()
	return err == nil && n > 0, nil
}
