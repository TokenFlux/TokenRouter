package postgres

import (
	"context"
	"encoding/json"
	"errors"

	"github.com/TokenFlux/TokenRouter/internal/account"
)

// ApplyOAuthRefreshFailure 在原单条健康写入中比较交换身份，保留提交后尽力 outbox。
// 区分身份过期与同身份无需延长 cooldown，避免改变后一种情况的原计数语义。
func (r *AccountStore) ApplyOAuthRefreshFailure(ctx context.Context, version account.RefreshFailureVersion, failure account.RefreshFailure) (bool, error) {
	payload, err := json.Marshal(version.Credentials)
	if err != nil {
		return false, err
	}
	if version.Credentials == nil {
		payload = []byte(`{}`)
	}
	args := []any{version.ID, version.Platform, version.Type, version.Status, string(payload), version.ProxyID, version.Schedulable, failure.Message}
	var update string
	switch failure.Kind {
	case account.RefreshFailurePermanent:
		update = `UPDATE accounts SET status='error',error_message=$8,schedulable=false,updated_at=$9 WHERE id IN (SELECT id FROM candidate) RETURNING id`
		args = append(args, r.options.Now())
	case account.RefreshFailureCooldown:
		update = `UPDATE accounts SET temp_unschedulable_until=$9,temp_unschedulable_reason=$8,updated_at=NOW() WHERE id IN (SELECT id FROM candidate) AND (temp_unschedulable_until IS NULL OR temp_unschedulable_until<$9) RETURNING id`
		args = append(args, failure.Until)
	default:
		return false, errors.New("unknown OAuth refresh failure action")
	}
	rows, err := r.sql.QueryContext(ctx, `WITH candidate AS (
 SELECT id FROM accounts WHERE id=$1 AND deleted_at IS NULL AND platform=$2 AND type=$3 AND status=$4
 AND credentials=$5::jsonb AND proxy_id IS NOT DISTINCT FROM $6 AND schedulable=$7 FOR UPDATE
 ), updated AS (`+update+`) SELECT EXISTS(SELECT 1 FROM candidate),EXISTS(SELECT 1 FROM updated)`, args...)
	if err != nil {
		return false, err
	}
	var matched, changed bool
	if rows.Next() {
		err = rows.Scan(&matched, &changed)
	} else {
		err = rows.Err()
		if err == nil {
			err = errors.New("missing OAuth refresh failure outcome")
		}
	}
	if err == nil {
		err = rows.Err()
	}
	closeErr := rows.Close()
	if err != nil {
		return false, err
	}
	if closeErr != nil {
		return false, closeErr
	}
	if changed {
		if err := r.enqueue(ctx, r.sql, AccountChanged, &version.ID, nil, nil); err != nil {
			r.observe("[SchedulerOutbox] enqueue OAuth refresh failure failed: account=%d err=%v", version.ID, err)
		}
		r.afterChange(ctx, version.ID)
	}
	return matched, nil
}

// ClearAntigravityRefreshRequest 在原 Extra/outbox 事务内锁定并复核身份，防止提交后的清理覆盖新授权。
func (r *AccountStore) ClearAntigravityRefreshRequest(ctx context.Context, version account.CredentialVersion) (bool, error) {
	if version.Platform != account.PlatformAntigravity || version.Type != account.AccountTypeOAuth {
		return false, nil
	}
	applied, committed, err := (CredentialRefreshStore{Client: r.client, Persist: func(ctx context.Context, id int64, _ map[string]any) error {
		return r.UpdateExtra(ctx, id, account.ClearedAntigravityRefreshRequest())
	}}).Apply(ctx, version, nil)
	if err == nil && committed {
		r.afterChange(ctx, version.ID)
	}
	return applied, err
}
