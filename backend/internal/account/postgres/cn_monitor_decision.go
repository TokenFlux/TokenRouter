package postgres

import (
	"context"
	"time"
)

// SetCNUsageDecisionCAS 只修改产生观测的账号版本，避免旧余额结论暂停新凭据或清除其它停调。
// 延续原单语句健康写入及其提交后尽力 outbox，不扩大为新的闭合事务。
func (r *AccountStore) SetCNUsageDecisionCAS(ctx context.Context, id int64, expected time.Time, until time.Time, reason string, clear bool) (bool, error) {
	query := `UPDATE accounts SET temp_unschedulable_until=$1,temp_unschedulable_reason=$2,updated_at=NOW()
 WHERE id=$3 AND deleted_at IS NULL AND updated_at=$4
 AND (temp_unschedulable_until IS NULL OR temp_unschedulable_until<$1)`
	args := []any{until, reason, id, expected}
	if clear {
		query = `UPDATE accounts SET temp_unschedulable_until=NULL,temp_unschedulable_reason=NULL,updated_at=NOW()
 WHERE id=$1 AND deleted_at IS NULL AND updated_at=$2 AND temp_unschedulable_reason=$3`
		args = []any{id, expected, reason}
	}
	result, err := r.sql.ExecContext(ctx, query, args...)
	if err != nil {
		return false, err
	}
	count, err := result.RowsAffected()
	if err != nil {
		return false, err
	}
	if count == 0 {
		return false, nil
	}
	if err := r.enqueue(ctx, r.sql, AccountChanged, &id, nil, nil); err != nil {
		r.observe("[SchedulerOutbox] enqueue CN monitor health failed: account=%d err=%v", id, err)
	}
	r.afterChange(ctx, id)
	return true, nil
}
