package postgres

import (
	"context"
	"encoding/json"
	"fmt"
	"github.com/TokenFlux/TokenRouter/internal/account"
)

// ApplyManagedRecoveryStep 每条 SQL 独立提交；只在原身份、错误和该步骤旧状态匹配时恢复。
func (r *AccountStore) ApplyManagedRecoveryStep(ctx context.Context, step account.ManagedRecoveryStep, v account.ManagedRecoveryVersion) (bool, error) {
	args := []any{v.ID, v.ErrorMessage}
	where, values, err := usageObservationPredicate(v.UsageObservationVersion, 3, step == account.ManagedRecoveryRateLimit)
	if err != nil {
		return false, err
	}
	args = append(args, values...)
	set := ""
	switch step {
	case account.ManagedRecoveryError:
		if v.Status != account.StatusError {
			return false, nil
		}
		args = append(args, account.StatusActive, r.options.Now())
		set = fmt.Sprintf("status=$%d,error_message='',updated_at=$%d", len(args)-1, len(args))
	case account.ManagedRecoveryRateLimit:
		set = "rate_limited_at=NULL,rate_limit_reset_at=NULL,overload_until=NULL,updated_at=NOW()"
	case account.ManagedRecoveryQuotaScopes, account.ManagedRecoveryModelLimits:
		key := "antigravity_quota_scopes"
		if step == account.ManagedRecoveryModelLimits {
			key = "model_rate_limits"
		}
		var payload any
		if value, present := v.Extra[key]; present {
			encoded, encodeErr := json.Marshal(value)
			if encodeErr != nil {
				return false, encodeErr
			}
			payload = string(encoded)
		}
		args = append(args, payload)
		where += fmt.Sprintf(" AND extra -> '%s' IS NOT DISTINCT FROM $%d::jsonb", key, len(args))
		set = fmt.Sprintf("extra=COALESCE(extra,'{}'::jsonb)-'%s',updated_at=NOW()", key)
	case account.ManagedRecoveryTemporary:
		args = append(args, v.Until, v.Reason)
		where += fmt.Sprintf(" AND temp_unschedulable_until IS NOT DISTINCT FROM $%d AND COALESCE(temp_unschedulable_reason,'')=$%d", len(args)-1, len(args))
		set = "temp_unschedulable_until=NULL,temp_unschedulable_reason=NULL,updated_at=NOW()"
	default:
		return false, fmt.Errorf("unknown managed recovery step: %d", step)
	}
	result, err := r.sql.ExecContext(ctx, "UPDATE accounts SET "+set+" WHERE id=$1 AND deleted_at IS NULL AND COALESCE(error_message,'')=$2 AND "+where, args...)
	if err != nil {
		return false, err
	}
	rows, err := result.RowsAffected()
	if err != nil || rows == 0 {
		return false, err
	}
	// 与旧清理保持同样的尽力 outbox；事件失败不回滚已完成的独立写入。
	if err := r.enqueue(ctx, r.sql, AccountChanged, &v.ID, nil, nil); err != nil {
		r.observe("[SchedulerOutbox] enqueue managed recovery failed: account=%d step=%d err=%v", v.ID, step, err)
	}
	if step != account.ManagedRecoveryQuotaScopes {
		r.afterChange(ctx, v.ID)
	}
	return true, nil
}
