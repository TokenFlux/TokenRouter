package postgres

import (
	"context"

	"github.com/TokenFlux/TokenRouter/internal/account"
)

// ClearRefreshCooldownIfUnchanged 沿用原单条清理和尽力 outbox，附加身份及原窗口比较。
func (r *AccountStore) ClearRefreshCooldownIfUnchanged(ctx context.Context, v account.RefreshCooldownVersion) (bool, error) {
	where, args, err := usageObservationPredicate(account.UsageObservationVersion{CredentialVersion: v.CredentialVersion, ParentAccountID: v.ParentAccountID, QuotaDimension: v.QuotaDimension}, 4, false)
	if err != nil {
		return false, err
	}
	values := []any{v.ID, v.Until, v.Reason}
	values = append(values, args...)
	result, err := r.sql.ExecContext(ctx, `UPDATE accounts SET temp_unschedulable_until=NULL,temp_unschedulable_reason=NULL,updated_at=NOW()
 WHERE id=$1 AND deleted_at IS NULL AND temp_unschedulable_until IS NOT DISTINCT FROM $2 AND COALESCE(temp_unschedulable_reason,'')=$3 AND `+where, values...)
	if err != nil {
		return false, err
	}
	rows, err := result.RowsAffected()
	if err != nil {
		return false, err
	}
	if rows == 0 {
		return false, nil
	}
	if err := r.enqueue(ctx, r.sql, AccountChanged, &v.ID, nil, nil); err != nil {
		r.observe("[SchedulerOutbox] enqueue clear temp unschedulable failed: account=%d err=%v", v.ID, err)
	}
	r.afterChange(ctx, v.ID)
	return true, nil
}
