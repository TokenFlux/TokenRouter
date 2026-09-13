package postgres

import (
	"context"
	"github.com/TokenFlux/TokenRouter/internal/account"
	"time"
)

// UpdateUsageSessionWindowEndIfUnchanged 保留原窗口更新的独立提交与尽力事件，不覆盖新身份或较新窗口。
func (r *AccountStore) UpdateUsageSessionWindowEndIfUnchanged(ctx context.Context, v account.UsageObservationVersion, observed *time.Time, end time.Time) (bool, error) {
	where, values, err := usageObservationPredicate(v, 5, false)
	if err != nil {
		return false, err
	}
	args := []any{v.ID, end, observed, r.options.Now()}
	args = append(args, values...)
	result, err := r.sql.ExecContext(ctx, "UPDATE accounts SET session_window_end=$2,updated_at=$4 WHERE id=$1 AND deleted_at IS NULL AND session_window_end IS NOT DISTINCT FROM $3 AND "+where, args...)
	if err != nil {
		return false, err
	}
	rows, err := result.RowsAffected()
	if err != nil || rows == 0 {
		return false, err
	}
	if err := r.enqueue(ctx, r.sql, AccountChanged, &v.ID, nil, nil); err != nil {
		r.observe("[SchedulerOutbox] enqueue session window end update failed: account=%d err=%v", v.ID, err)
	}
	return true, nil
}
