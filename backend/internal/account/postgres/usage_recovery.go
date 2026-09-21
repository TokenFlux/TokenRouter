package postgres

import (
	"context"
	"encoding/json"

	"github.com/TokenFlux/TokenRouter/internal/account"
)

// ClearUsageErrorIfUnchanged 保留单条恢复 SQL 与提交后尽力 outbox，原子比较本轮观察身份和错误。
func (r *AccountStore) ClearUsageErrorIfUnchanged(ctx context.Context, v account.UsageRecoveryVersion) (bool, error) {
	if v.Status != account.StatusError {
		return false, nil
	}
	credentials := v.Credentials
	if credentials == nil {
		credentials = map[string]any{}
	}
	payload, err := json.Marshal(credentials)
	if err != nil {
		return false, err
	}
	result, err := r.sql.ExecContext(ctx, `UPDATE accounts SET status=$8,error_message='',updated_at=$9
 WHERE id=$1 AND deleted_at IS NULL AND platform=$2 AND type=$3 AND status=$4
 AND credentials=$5::jsonb AND proxy_id IS NOT DISTINCT FROM $6 AND error_message=$7`, v.ID, v.Platform, v.Type, v.Status, string(payload), v.ProxyID, v.ErrorMessage, account.StatusActive, r.options.Now())
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
		r.observe("[SchedulerOutbox] enqueue clear error failed: account=%d err=%v", v.ID, err)
	}
	r.afterChange(ctx, v.ID)
	return true, nil
}
