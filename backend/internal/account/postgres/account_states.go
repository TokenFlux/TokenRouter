// 本文件维护 postgres 的所属能力；兼容入口复用唯一实现。
package postgres

import (
	context "context"
	json "encoding/json"
	strconv "strconv"
	strings "strings"
	time "time"

	dbaccount "github.com/TokenFlux/TokenRouter/ent/account"
	acctcore "github.com/TokenFlux/TokenRouter/internal/account"
	pq "github.com/lib/pq"
)

func (r *AccountStore) UpdateLastUsed(ctx context.Context, id int64) error {
	now := r.options.Now()
	_, err := r.client.Account.Update().
		Where(dbaccount.IDEQ(id)).
		SetLastUsedAt(now).
		Save(ctx)
	if err != nil {
		return err
	}
	payload := map[string]any{
		"last_used": map[string]int64{
			strconv.FormatInt(id, 10): now.Unix(),
		},
	}
	if err := r.enqueue(ctx, r.sql, AccountLastUsed, &id, nil, payload); err != nil {
		r.observe("[SchedulerOutbox] enqueue last used failed: account=%d err=%v", id, err)
	}
	return nil
}

func (r *AccountStore) BatchUpdateLastUsed(ctx context.Context, updates map[int64]time.Time) error {
	if len(updates) == 0 {
		return nil
	}

	ids := make([]int64, 0, len(updates))
	args := make([]any, 0, len(updates)*2+1)
	caseSQL := "UPDATE accounts SET last_used_at = CASE id"

	idx := 1
	for id, ts := range updates {
		caseSQL += " WHEN $" + strconv.Itoa(idx) + " THEN $" + strconv.Itoa(idx+1) + "::timestamptz"
		args = append(args, id, ts)
		ids = append(ids, id)
		idx += 2
	}

	caseSQL += " END, updated_at = NOW() WHERE id = ANY($" + strconv.Itoa(idx) + ") AND deleted_at IS NULL"
	args = append(args, pq.Array(ids))

	_, err := r.sql.ExecContext(ctx, caseSQL, args...)
	if err != nil {
		return err
	}
	lastUsedPayload := make(map[string]int64, len(updates))
	for id, ts := range updates {
		lastUsedPayload[strconv.FormatInt(id, 10)] = ts.Unix()
	}
	payload := map[string]any{"last_used": lastUsedPayload}
	if err := r.enqueue(ctx, r.sql, AccountLastUsed, nil, nil, payload); err != nil {
		r.observe("[SchedulerOutbox] enqueue batch last used failed: err=%v", err)
	}
	return nil
}

func (r *AccountStore) SetError(ctx context.Context, id int64, errorMsg string) error {
	// 标记错误时同步关闭调度开关，确保快照刷新后调度器不再选中该账号。
	_, err := r.client.Account.Update().
		Where(dbaccount.IDEQ(id)).
		SetStatus(acctcore.StatusError).
		SetErrorMessage(errorMsg).
		SetSchedulable(false).
		Save(ctx)
	if err != nil {
		return err
	}
	if err := r.enqueue(ctx, r.sql, AccountChanged, &id, nil, nil); err != nil {
		r.observe("[SchedulerOutbox] enqueue set error failed: account=%d err=%v", id, err)
	}
	r.afterChange(ctx, id)
	return nil
}

func (r *AccountStore) ClearError(ctx context.Context, id int64) error {
	_, err := r.client.Account.Update().
		Where(dbaccount.IDEQ(id)).
		SetStatus(acctcore.StatusActive).
		SetErrorMessage("").
		Save(ctx)
	if err != nil {
		return err
	}
	if err := r.enqueue(ctx, r.sql, AccountChanged, &id, nil, nil); err != nil {
		r.observe("[SchedulerOutbox] enqueue clear error failed: account=%d err=%v", id, err)
	}
	r.afterChange(ctx, id)
	return nil
}

func (r *AccountStore) SetRateLimited(ctx context.Context, id int64, resetAt time.Time) error {
	now := r.options.Now()
	_, err := r.client.Account.Update().
		Where(dbaccount.IDEQ(id)).
		SetRateLimitedAt(now).
		SetRateLimitResetAt(resetAt).
		Save(ctx)
	if err != nil {
		return err
	}
	if err := r.enqueue(ctx, r.sql, AccountChanged, &id, nil, nil); err != nil {
		r.observe("[SchedulerOutbox] enqueue rate limit failed: account=%d err=%v", id, err)
	}
	r.afterChange(ctx, id)
	return nil
}

// SetRateLimitedIfLater 以原子方式延长账号级限流。Grok 请求可能并发结束，较旧响应
// 不得覆盖其它请求或实例已观测到的更晚重置边界。
func (r *AccountStore) SetRateLimitedIfLater(ctx context.Context, id int64, resetAt time.Time) error {
	now := r.options.Now()
	updated, err := r.client.Account.Update().
		Where(
			dbaccount.IDEQ(id),
			dbaccount.Or(
				dbaccount.RateLimitResetAtIsNil(),
				dbaccount.RateLimitResetAtLT(resetAt),
			),
		).
		SetRateLimitedAt(now).
		SetRateLimitResetAt(resetAt).
		Save(ctx)
	if err != nil {
		return err
	}
	if updated == 0 {
		// 当前实例可能尚未观测到其它实例写入的更晚值；即使无需发送 outbox 事件，
		// 仍需刷新本地调度快照。
		r.afterChange(ctx, id)
		return nil
	}
	if err := r.enqueue(ctx, r.sql, AccountChanged, &id, nil, nil); err != nil {
		r.observe("[SchedulerOutbox] enqueue extended rate limit failed: account=%d err=%v", id, err)
	}
	r.afterChange(ctx, id)
	return nil
}

// ClearRateLimitIfObserved 只清除成功请求观察到的 Grok 限流代次。
// 同时匹配两个时间戳，避免过期成功请求清除后来重新设置且重置时间相同或更短的新代次。
func (r *AccountStore) ClearRateLimitIfObserved(ctx context.Context, id int64, observedLimitedAt, observedResetAt time.Time) (bool, error) {
	updated, err := r.client.Account.Update().
		Where(
			dbaccount.IDEQ(id),
			dbaccount.PlatformEQ(acctcore.PlatformGrok),
			dbaccount.TypeEQ(acctcore.AccountTypeOAuth),
			dbaccount.RateLimitedAtEQ(observedLimitedAt),
			dbaccount.RateLimitResetAtEQ(observedResetAt),
		).
		ClearRateLimitedAt().
		ClearRateLimitResetAt().
		Save(ctx)
	if err != nil {
		return false, err
	}
	if updated == 0 {
		r.afterChange(ctx, id)
		return false, nil
	}
	if err := r.enqueue(ctx, r.sql, AccountChanged, &id, nil, nil); err != nil {
		r.observe("[SchedulerOutbox] enqueue observed rate-limit clear failed: account=%d err=%v", id, err)
	}
	r.afterChange(ctx, id)
	return true, nil
}

func (r *AccountStore) SetModelRateLimit(ctx context.Context, id int64, scope string, resetAt time.Time, reason ...string) error {
	if scope == "" {
		return nil
	}
	now := r.options.Now().UTC()
	payload := map[string]string{
		"rate_limited_at":     now.Format(time.RFC3339),
		"rate_limit_reset_at": resetAt.UTC().Format(time.RFC3339),
	}
	if len(reason) > 0 {
		if value := strings.TrimSpace(reason[0]); value != "" {
			payload["reason"] = value
		}
	}
	raw, err := json.Marshal(payload)
	if err != nil {
		return err
	}

	client := clientFromContext(ctx, r.client)
	result, err := client.ExecContext(
		ctx,
		`UPDATE accounts SET
			extra = jsonb_set(
				jsonb_set(COALESCE(extra, '{}'::jsonb), '{model_rate_limits}'::text[], COALESCE(extra->'model_rate_limits', '{}'::jsonb), true),
				ARRAY['model_rate_limits', $1]::text[],
				$2::jsonb,
				true
			),
			updated_at = NOW()
		WHERE id = $3 AND deleted_at IS NULL`,
		scope,
		raw,
		id,
	)
	if err != nil {
		return err
	}

	affected, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if affected == 0 {
		return acctcore.ErrAccountNotFound
	}
	if err := r.enqueue(ctx, r.sql, AccountChanged, &id, nil, nil); err != nil {
		r.observe("[SchedulerOutbox] enqueue model rate limit failed: account=%d err=%v", id, err)
	}
	r.afterChange(ctx, id)
	return nil
}

func (r *AccountStore) SetOverloaded(ctx context.Context, id int64, until time.Time) error {
	_, err := r.client.Account.Update().
		Where(dbaccount.IDEQ(id)).
		SetOverloadUntil(until).
		Save(ctx)
	if err != nil {
		return err
	}
	if err := r.enqueue(ctx, r.sql, AccountChanged, &id, nil, nil); err != nil {
		r.observe("[SchedulerOutbox] enqueue overload failed: account=%d err=%v", id, err)
	}
	r.afterChange(ctx, id)
	return nil
}

func (r *AccountStore) SetTempUnschedulable(ctx context.Context, id int64, until time.Time, reason string) error {
	result, err := r.sql.ExecContext(ctx, `
		UPDATE accounts
		SET temp_unschedulable_until = $1,
			temp_unschedulable_reason = $2,
			updated_at = NOW()
		WHERE id = $3
			AND deleted_at IS NULL
			AND (temp_unschedulable_until IS NULL OR temp_unschedulable_until < $1)
	`, until, reason, id)
	if err != nil {
		return err
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if affected <= 0 {
		return nil
	}
	if err := r.enqueue(ctx, r.sql, AccountChanged, &id, nil, nil); err != nil {
		r.observe("[SchedulerOutbox] enqueue temp unschedulable failed: account=%d err=%v", id, err)
	}
	r.afterChange(ctx, id)
	return nil
}

func (r *AccountStore) ClearTempUnschedulable(ctx context.Context, id int64) error {
	_, err := r.sql.ExecContext(ctx, `
		UPDATE accounts
		SET temp_unschedulable_until = NULL,
			temp_unschedulable_reason = NULL,
			updated_at = NOW()
		WHERE id = $1
			AND deleted_at IS NULL
	`, id)
	if err != nil {
		return err
	}
	if err := r.enqueue(ctx, r.sql, AccountChanged, &id, nil, nil); err != nil {
		r.observe("[SchedulerOutbox] enqueue clear temp unschedulable failed: account=%d err=%v", id, err)
	}
	r.afterChange(ctx, id)
	return nil
}

func (r *AccountStore) ClearRateLimit(ctx context.Context, id int64) error {
	_, err := r.client.Account.Update().
		Where(dbaccount.IDEQ(id)).
		ClearRateLimitedAt().
		ClearRateLimitResetAt().
		ClearOverloadUntil().
		Save(ctx)
	if err != nil {
		return err
	}
	if err := r.enqueue(ctx, r.sql, AccountChanged, &id, nil, nil); err != nil {
		r.observe("[SchedulerOutbox] enqueue clear rate limit failed: account=%d err=%v", id, err)
	}
	r.afterChange(ctx, id)
	return nil
}

func (r *AccountStore) ClearAntigravityQuotaScopes(ctx context.Context, id int64) error {
	client := clientFromContext(ctx, r.client)
	result, err := client.ExecContext(
		ctx,
		"UPDATE accounts SET extra = COALESCE(extra, '{}'::jsonb) - 'antigravity_quota_scopes', updated_at = NOW() WHERE id = $1 AND deleted_at IS NULL",
		id,
	)
	if err != nil {
		return err
	}

	affected, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if affected == 0 {
		return acctcore.ErrAccountNotFound
	}
	if err := r.enqueue(ctx, r.sql, AccountChanged, &id, nil, nil); err != nil {
		r.observe("[SchedulerOutbox] enqueue clear quota scopes failed: account=%d err=%v", id, err)
	}
	return nil
}

func (r *AccountStore) ClearModelRateLimits(ctx context.Context, id int64) error {
	client := clientFromContext(ctx, r.client)
	result, err := client.ExecContext(
		ctx,
		"UPDATE accounts SET extra = COALESCE(extra, '{}'::jsonb) - 'model_rate_limits', updated_at = NOW() WHERE id = $1 AND deleted_at IS NULL",
		id,
	)
	if err != nil {
		return err
	}

	affected, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if affected == 0 {
		return acctcore.ErrAccountNotFound
	}
	if err := r.enqueue(ctx, r.sql, AccountChanged, &id, nil, nil); err != nil {
		r.observe("[SchedulerOutbox] enqueue clear model rate limit failed: account=%d err=%v", id, err)
	}
	r.afterChange(ctx, id)
	return nil
}

func (r *AccountStore) UpdateSessionWindow(ctx context.Context, id int64, start, end *time.Time, status string) error {
	builder := r.client.Account.Update().
		Where(dbaccount.IDEQ(id)).
		SetSessionWindowStatus(status)
	if start != nil {
		builder.SetSessionWindowStart(*start)
	}
	if end != nil {
		builder.SetSessionWindowEnd(*end)
	}
	_, err := builder.Save(ctx)
	if err != nil {
		return err
	}
	// 触发调度器缓存更新（仅当窗口时间有变化时）
	if start != nil || end != nil {
		if err := r.enqueue(ctx, r.sql, AccountChanged, &id, nil, nil); err != nil {
			r.observe("[SchedulerOutbox] enqueue session window update failed: account=%d err=%v", id, err)
		}
	}
	return nil
}

func (r *AccountStore) UpdateSessionWindowEnd(ctx context.Context, id int64, end time.Time) error {
	_, err := r.client.Account.Update().
		Where(dbaccount.IDEQ(id)).
		SetSessionWindowEnd(end).
		Save(ctx)
	if err != nil {
		return err
	}
	if err := r.enqueue(ctx, r.sql, AccountChanged, &id, nil, nil); err != nil {
		r.observe("[SchedulerOutbox] enqueue session window end update failed: account=%d err=%v", id, err)
	}
	return nil
}

func (r *AccountStore) SetSchedulable(ctx context.Context, id int64, schedulable bool) error {
	_, err := r.client.Account.Update().
		Where(dbaccount.IDEQ(id)).
		SetSchedulable(schedulable).
		Save(ctx)
	if err != nil {
		return err
	}
	if err := r.enqueue(ctx, r.sql, AccountChanged, &id, nil, nil); err != nil {
		r.observe("[SchedulerOutbox] enqueue schedulable change failed: account=%d err=%v", id, err)
	}
	if !schedulable {
		r.afterChange(ctx, id)
	}
	return nil
}

func (r *AccountStore) AutoPauseExpiredAccounts(ctx context.Context, now time.Time) (int64, error) {
	rows, err := r.sql.QueryContext(ctx, `
		UPDATE accounts
		SET schedulable = FALSE,
			updated_at = NOW()
		WHERE deleted_at IS NULL
			AND schedulable = TRUE
			AND auto_pause_on_expired = TRUE
			AND expires_at IS NOT NULL
			AND expires_at <= $1
		RETURNING id
	`, now)
	if err != nil {
		return 0, err
	}
	defer func() {
		_ = rows.Close()
	}()

	accountIDs := make([]int64, 0)
	for rows.Next() {
		var accountID int64
		if err := rows.Scan(&accountID); err != nil {
			return 0, err
		}
		accountIDs = append(accountIDs, accountID)
	}
	if err := rows.Err(); err != nil {
		return 0, err
	}

	if len(accountIDs) > 0 {
		// 只刷新本次暂停的账号及其所属分组，避免少量账号到期触发所有调度桶重建。
		payload := map[string]any{"account_ids": accountIDs}
		if err := r.enqueue(ctx, r.sql, AccountBulkChanged, nil, nil, payload); err != nil {
			r.observe("[SchedulerOutbox] enqueue auto pause account changes failed: err=%v", err)
		}
	}
	return int64(len(accountIDs)), nil
}

// RevertProxyFallback 将账号的 proxy_id 切回 proxy_fallback_origin_id，并清空 origin 字段。
// 仅当 proxy_fallback_origin_id IS NOT NULL 时执行更新；
// 若影响行数为 0，则返回 ErrAccountNotInFallback（账号存在但不在 fallback 状态）。
func (r *AccountStore) RevertProxyFallback(ctx context.Context, accountID int64) error {
	res, err := r.sql.ExecContext(ctx, `
		UPDATE accounts SET proxy_id=proxy_fallback_origin_id, proxy_fallback_origin_id=NULL, updated_at=NOW()
		WHERE id=$1 AND proxy_fallback_origin_id IS NOT NULL AND deleted_at IS NULL`, accountID)
	if err != nil {
		return err
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return acctcore.ErrAccountNotInFallback
	}
	if err := r.enqueue(ctx, r.sql, AccountChanged, &accountID, nil, nil); err != nil {
		r.observe("[SchedulerOutbox] revert fallback enqueue failed: account=%d err=%v", accountID, err)
	}
	return nil
}
