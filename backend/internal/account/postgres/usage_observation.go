package postgres

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/TokenFlux/TokenRouter/internal/account"
	"time"
)

// usageObservationPredicate 使用当前字段作原子比较，不新增版本列或事务 context。
func usageObservationPredicate(v account.UsageObservationVersion, offset int, health bool) (string, []any, error) {
	credentials := v.Credentials
	if credentials == nil {
		credentials = map[string]any{}
	}
	payload, err := json.Marshal(credentials)
	if err != nil {
		return "", nil, err
	}
	values := []any{v.Platform, v.Type, v.Status, string(payload), v.ProxyID, v.ParentAccountID, v.QuotaDimension}
	condition := fmt.Sprintf("platform=$%d AND type=$%d AND status=$%d AND credentials=$%d::jsonb AND proxy_id IS NOT DISTINCT FROM $%d AND parent_account_id IS NOT DISTINCT FROM $%d AND quota_dimension=$%d", offset, offset+1, offset+2, offset+3, offset+4, offset+5, offset+6)
	if health {
		condition += fmt.Sprintf(" AND rate_limited_at IS NOT DISTINCT FROM $%d AND rate_limit_reset_at IS NOT DISTINCT FROM $%d AND overload_until IS NOT DISTINCT FROM $%d", offset+7, offset+8, offset+9)
		values = append(values, v.RateLimitedAt, v.RateLimitResetAt, v.OverloadUntil)
	}
	return condition, values, nil
}

func (r *AccountStore) UpdateUsageExtraIfUnchanged(ctx context.Context, v account.UsageObservationVersion, updates map[string]any) (bool, error) {
	err := r.updateExtra(ctx, v.ID, account.CloneValues(updates), &v)
	if errors.Is(err, account.ErrUsageObservationChanged) {
		return false, nil
	}
	return err == nil, err
}
func (r *AccountStore) SetUsageRateLimitIfUnchanged(ctx context.Context, v account.UsageObservationVersion, reset time.Time) (bool, error) {
	return r.updateUsageRateLimit(ctx, v, &reset)
}
func (r *AccountStore) ClearUsageRateLimitIfUnchanged(ctx context.Context, v account.UsageObservationVersion) (bool, error) {
	return r.updateUsageRateLimit(ctx, v, nil)
}

// updateUsageRateLimit 保留原独立健康写入及提交后尽力事件；清除还需匹配原窗口，避免清掉新停调。
func (r *AccountStore) updateUsageRateLimit(ctx context.Context, v account.UsageObservationVersion, reset *time.Time) (bool, error) {
	now := r.options.Now()
	set := "rate_limited_at=NULL,rate_limit_reset_at=NULL,overload_until=NULL,updated_at=$2"
	args := []any{v.ID, now}
	if reset != nil {
		set = "rate_limited_at=$2,rate_limit_reset_at=$3,updated_at=$2"
		args = append(args, *reset)
	}
	where, values, err := usageObservationPredicate(v, len(args)+1, reset == nil)
	if err != nil {
		return false, err
	}
	args = append(args, values...)
	result, err := r.sql.ExecContext(ctx, "UPDATE accounts SET "+set+" WHERE id=$1 AND deleted_at IS NULL AND "+where, args...)
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
		r.observe("[SchedulerOutbox] enqueue usage rate limit failed: account=%d err=%v", v.ID, err)
	}
	r.afterChange(ctx, v.ID)
	return true, nil
}
