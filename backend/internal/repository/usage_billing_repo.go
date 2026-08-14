package repository

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"math"
	"strconv"
	"strings"
	"time"

	dbent "github.com/BrandonVee/TokenRouter/ent"
	"github.com/BrandonVee/TokenRouter/internal/domain"
	"github.com/BrandonVee/TokenRouter/internal/pkg/logger"
	"github.com/BrandonVee/TokenRouter/internal/pkg/timezone"
	"github.com/BrandonVee/TokenRouter/internal/service"
)

type usageBillingRepository struct {
	db *sql.DB
}

func NewUsageBillingRepository(_ *dbent.Client, sqlDB *sql.DB) service.UsageBillingRepository {
	return &usageBillingRepository{db: sqlDB}
}

func (r *usageBillingRepository) Apply(ctx context.Context, cmd *service.UsageBillingCommand) (_ *service.UsageBillingApplyResult, err error) {
	if cmd == nil {
		return &service.UsageBillingApplyResult{}, nil
	}
	if r == nil || r.db == nil {
		return nil, errors.New("usage billing repository db is nil")
	}

	cmd.Normalize()
	if cmd.RequestID == "" {
		return nil, service.ErrUsageBillingRequestIDRequired
	}

	return retryPostgresDeadlock(ctx, "usage_billing_apply", 0, func() (*service.UsageBillingApplyResult, error) {
		return r.applyOnce(ctx, cmd)
	})
}

// applyOnce 执行一次完整计费事务；死锁后必须由外层重开事务，不能复用已中止的事务。
func (r *usageBillingRepository) applyOnce(ctx context.Context, cmd *service.UsageBillingCommand) (_ *service.UsageBillingApplyResult, err error) {
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer func() {
		if tx != nil {
			_ = tx.Rollback()
		}
	}()

	applied, err := r.claimUsageBillingKey(ctx, tx, cmd)
	if err != nil {
		return nil, err
	}
	if !applied {
		return &service.UsageBillingApplyResult{Applied: false}, nil
	}
	if err := lockUsageBillingUser(ctx, tx, cmd.UserID); err != nil {
		return nil, err
	}

	result := &service.UsageBillingApplyResult{Applied: true}
	if err := r.applyUsageBillingEffects(ctx, tx, cmd, result); err != nil {
		return nil, err
	}

	if err := tx.Commit(); err != nil {
		return nil, err
	}
	tx = nil
	return result, nil
}

// lockUsageBillingUser 先串行化付款用户写入，再访问订阅；NO KEY UPDATE 与日志外键的 KEY SHARE 兼容。
func lockUsageBillingUser(ctx context.Context, tx *sql.Tx, userID int64) error {
	// 账户额度维护测试和内部任务允许只携带 AccountID；没有付款用户时不存在本次锁环。
	if userID <= 0 {
		return nil
	}
	var lockedUserID int64
	err := tx.QueryRowContext(ctx, `
		SELECT id
		FROM users
		WHERE id = $1 AND deleted_at IS NULL
		FOR NO KEY UPDATE
	`, userID).Scan(&lockedUserID)
	if errors.Is(err, sql.ErrNoRows) {
		return service.ErrUserNotFound
	}
	return err
}

func (r *usageBillingRepository) ResolveUsableSubscriptionForGroup(ctx context.Context, userID, groupID int64) (*service.UserSubscription, error) {
	return r.resolveUsableSubscriptionForGroup(ctx, userID, groupID, nil)
}

// ResolvePreferredSubscriptionForGroup 为批量任务定价读取指定订阅，避免把 API Key 锁定的套餐重新扩展为自动选择。
func (r *usageBillingRepository) ResolvePreferredSubscriptionForGroup(ctx context.Context, userID, subscriptionID, groupID int64) (*service.UserSubscription, error) {
	if subscriptionID <= 0 {
		return nil, service.ErrSubscriptionNotFound
	}
	return r.resolveUsableSubscriptionForGroup(ctx, userID, groupID, &subscriptionID)
}

func (r *usageBillingRepository) resolveUsableSubscriptionForGroup(ctx context.Context, userID, groupID int64, preferredSubscriptionID *int64) (*service.UserSubscription, error) {
	if r == nil || r.db == nil {
		return nil, errors.New("usage billing repository db is nil")
	}
	if userID <= 0 || groupID <= 0 {
		return nil, service.ErrSubscriptionNotFound
	}

	now := time.Now()
	rows, err := r.db.QueryContext(ctx, `
		SELECT
			us.id,
			us.plan_id,
			us.starts_at,
			us.expires_at,
			us.daily_window_start,
			us.weekly_window_start,
			us.monthly_window_start,
			us.daily_limit_usd,
			us.weekly_limit_usd,
			us.monthly_limit_usd,
			us.daily_usage_usd,
			us.weekly_usage_usd,
			us.monthly_usage_usd,
			COALESCE((
				SELECT jsonb_agg(spg.group_id ORDER BY spg.group_id)
				FROM subscription_plan_groups spg
				WHERE spg.plan_id = sp.id
			), '[]'::jsonb),
			COALESCE((
				SELECT jsonb_object_agg(spg.group_id, spg.rate_multiplier)
				FROM subscription_plan_groups spg
				WHERE spg.plan_id = sp.id
					AND spg.rate_multiplier IS NOT NULL
			), '{}'::jsonb)
		FROM user_subscriptions us
		JOIN subscription_plans sp ON sp.id = us.plan_id
		WHERE us.user_id = $1
			AND us.deleted_at IS NULL
			AND us.starts_at <= $5
			AND us.expires_at > $5
			AND us.status IN ($2, $3)
			AND ($6::bigint IS NULL OR us.id = $6)
			AND (
				NOT EXISTS (
					SELECT 1
					FROM subscription_plan_groups spg
					WHERE spg.plan_id = us.plan_id
				)
				OR EXISTS (
					SELECT 1
					FROM subscription_plan_groups spg
					WHERE spg.plan_id = us.plan_id
						AND spg.group_id = $4
				)
			)
		ORDER BY us.expires_at ASC, us.starts_at ASC, us.id ASC
	`,
		userID,
		service.SubscriptionStatusActive,
		service.SubscriptionStatusPending,
		groupID,
		now,
		preferredSubscriptionID,
	)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()

	for rows.Next() {
		var row usageBillingSubscriptionRow
		if err := rows.Scan(
			&row.ID,
			&row.PlanID,
			&row.StartsAt,
			&row.ExpiresAt,
			&row.DailyWindowStart,
			&row.WeeklyWindowStart,
			&row.MonthlyWindowStart,
			&row.DailyLimitUSD,
			&row.WeeklyLimitUSD,
			&row.MonthlyLimitUSD,
			&row.DailyUsageUSD,
			&row.WeeklyUsageUSD,
			&row.MonthlyUsageUSD,
			&row.PlanGroupIDsRaw,
			&row.PlanGroupRateMultipliersRaw,
		); err != nil {
			return nil, err
		}

		// 订阅倍率解析必须与最终扣费使用相同的窗口状态，不能按数据库中的旧累计值提前过滤。
		row = normalizeUsageBillingSubscriptionRow(row, now)
		available := usageBillingSubscriptionAvailable(
			1,
			windowRemaining(row.DailyLimitUSD, row.DailyUsageUSD),
			windowRemaining(row.WeeklyLimitUSD, row.WeeklyUsageUSD),
			windowRemaining(row.MonthlyLimitUSD, row.MonthlyUsageUSD),
		)
		if available > 0 {
			return usageBillingSubscriptionRowToService(userID, row), nil
		}
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return nil, service.ErrSubscriptionNotFound
}

func (r *usageBillingRepository) claimUsageBillingKey(ctx context.Context, tx *sql.Tx, cmd *service.UsageBillingCommand) (bool, error) {
	return r.claimUsageBillingRequest(ctx, tx, cmd.RequestID, cmd.APIKeyID, cmd.RequestFingerprint)
}

func (r *usageBillingRepository) claimUsageBillingRequest(ctx context.Context, tx *sql.Tx, requestID string, apiKeyID int64, requestFingerprint string) (bool, error) {
	var id int64
	err := tx.QueryRowContext(ctx, `
		INSERT INTO usage_billing_dedup (request_id, api_key_id, request_fingerprint)
		VALUES ($1, $2, $3)
		ON CONFLICT (request_id, api_key_id) DO NOTHING
		RETURNING id
	`, requestID, apiKeyID, requestFingerprint).Scan(&id)
	if errors.Is(err, sql.ErrNoRows) {
		var existingFingerprint string
		if err := tx.QueryRowContext(ctx, `
			SELECT request_fingerprint
			FROM usage_billing_dedup
			WHERE request_id = $1 AND api_key_id = $2
		`, requestID, apiKeyID).Scan(&existingFingerprint); err != nil {
			return false, err
		}
		if strings.TrimSpace(existingFingerprint) != strings.TrimSpace(requestFingerprint) {
			return false, service.ErrUsageBillingRequestConflict
		}
		return false, nil
	}
	if err != nil {
		return false, err
	}
	var archivedFingerprint string
	err = tx.QueryRowContext(ctx, `
		SELECT request_fingerprint
		FROM usage_billing_dedup_archive
		WHERE request_id = $1 AND api_key_id = $2
	`, requestID, apiKeyID).Scan(&archivedFingerprint)
	if err == nil {
		if strings.TrimSpace(archivedFingerprint) != strings.TrimSpace(requestFingerprint) {
			return false, service.ErrUsageBillingRequestConflict
		}
		return false, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return false, err
	}
	return true, nil
}

func (r *usageBillingRepository) ReserveBatchImageBalance(ctx context.Context, cmd *service.BatchImageBalanceHoldCommand) (*service.BatchImageBalanceHoldResult, error) {
	return r.applyBatchImageBalanceHold(ctx, cmd, batchImageAllowanceReserve, reserveUsageBillingBatchImageBilling)
}

func (r *usageBillingRepository) CaptureBatchImageBalance(ctx context.Context, cmd *service.BatchImageBalanceHoldCommand) (*service.BatchImageBalanceHoldResult, error) {
	return r.applyBatchImageBalanceHold(ctx, cmd, batchImageAllowanceCapture, captureUsageBillingBatchImageBilling)
}

func (r *usageBillingRepository) ReleaseBatchImageBalance(ctx context.Context, cmd *service.BatchImageBalanceHoldCommand) (*service.BatchImageBalanceHoldResult, error) {
	return r.applyBatchImageBalanceHold(ctx, cmd, batchImageAllowanceRelease, releaseUsageBillingBatchImageBilling)
}

type batchImageAllowanceOperation int

const (
	batchImageAllowanceReserve batchImageAllowanceOperation = iota
	batchImageAllowanceCapture
	batchImageAllowanceRelease
)

func (r *usageBillingRepository) applyBatchImageBalanceHold(
	ctx context.Context,
	cmd *service.BatchImageBalanceHoldCommand,
	operation batchImageAllowanceOperation,
	apply func(context.Context, *sql.Tx, *service.BatchImageBalanceHoldCommand) (*service.BatchImageBalanceHoldResult, error),
) (_ *service.BatchImageBalanceHoldResult, err error) {
	if cmd == nil {
		return &service.BatchImageBalanceHoldResult{}, nil
	}
	if r == nil || r.db == nil {
		return nil, errors.New("usage billing repository db is nil")
	}
	cmd.Normalize()
	if cmd.RequestID == "" {
		return nil, service.ErrUsageBillingRequestIDRequired
	}
	original := cloneBatchImageBalanceHoldCommand(cmd)

	result, err := retryPostgresDeadlock(ctx, batchImageAllowanceOperationName(operation), 0, func() (*service.BatchImageBalanceHoldResult, error) {
		attemptCmd := cloneBatchImageBalanceHoldCommand(&original)
		attemptResult, attemptErr := r.applyBatchImageBalanceHoldOnce(ctx, &attemptCmd, operation, apply)
		if attemptErr == nil {
			*cmd = attemptCmd
		}
		return attemptResult, attemptErr
	})
	return result, err
}

// applyBatchImageBalanceHoldOnce 执行一次完整批量图片计费事务。
func (r *usageBillingRepository) applyBatchImageBalanceHoldOnce(
	ctx context.Context,
	cmd *service.BatchImageBalanceHoldCommand,
	operation batchImageAllowanceOperation,
	apply func(context.Context, *sql.Tx, *service.BatchImageBalanceHoldCommand) (*service.BatchImageBalanceHoldResult, error),
) (_ *service.BatchImageBalanceHoldResult, err error) {
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer func() {
		if tx != nil {
			_ = tx.Rollback()
		}
	}()

	applied, err := r.claimUsageBillingRequest(ctx, tx, cmd.RequestID, cmd.APIKeyID, cmd.RequestFingerprint)
	if err != nil {
		return nil, err
	}
	if !applied {
		return batchImageBillingResultForCommand(cmd, operation), nil
	}
	if err := lockUsageBillingUser(ctx, tx, cmd.UserID); err != nil {
		return nil, err
	}

	result, err := apply(ctx, tx, cmd)
	if err != nil {
		return nil, err
	}
	if result == nil {
		result = &service.BatchImageBalanceHoldResult{}
	}
	result.Applied = true
	if err := applyBatchImageAllowance(ctx, tx, cmd, operation); err != nil {
		return nil, err
	}

	if err := tx.Commit(); err != nil {
		return nil, err
	}
	tx = nil
	return result, nil
}

// cloneBatchImageBalanceHoldCommand 隔离失败尝试中的命令变更，保证重试从相同输入开始。
func cloneBatchImageBalanceHoldCommand(cmd *service.BatchImageBalanceHoldCommand) service.BatchImageBalanceHoldCommand {
	if cmd == nil {
		return service.BatchImageBalanceHoldCommand{}
	}
	cloned := *cmd
	cloned.SubscriptionHoldAllocations = append([]domain.BillingAllocation(nil), cmd.SubscriptionHoldAllocations...)
	return cloned
}

// batchImageAllowanceOperationName 返回稳定的重试日志操作名。
func batchImageAllowanceOperationName(operation batchImageAllowanceOperation) string {
	switch operation {
	case batchImageAllowanceReserve:
		return "usage_billing_batch_image_reserve"
	case batchImageAllowanceCapture:
		return "usage_billing_batch_image_capture"
	case batchImageAllowanceRelease:
		return "usage_billing_batch_image_release"
	default:
		return "usage_billing_batch_image_unknown"
	}
}

func applyBatchImageAllowance(ctx context.Context, tx *sql.Tx, cmd *service.BatchImageBalanceHoldCommand, operation batchImageAllowanceOperation) error {
	if cmd == nil || (cmd.HoldAmount <= 0 && cmd.BaseAmountUSD <= 0) {
		return nil
	}
	switch operation {
	case batchImageAllowanceReserve:
		reservedAt := cmd.ReservedAt
		if reservedAt.IsZero() {
			reservedAt = time.Now()
		}
		if err := reserveBatchImageAPIKeyAllowance(ctx, tx, cmd.APIKeyID, cmd.HoldAmount, reservedAt); err != nil {
			return err
		}
		if err := reserveBatchImageMemberAllowance(ctx, tx, cmd, cmd.HoldAmount); err != nil {
			return err
		}
		return setBatchImageAllowanceReserved(ctx, tx, cmd.BatchID, true)
	case batchImageAllowanceCapture:
		if cmd.AllowanceReserved {
			adjustment := cmd.HoldAmount - cmd.ActualAmount
			if adjustment > 0 {
				if err := rollbackBatchImageAllowanceBestEffort(ctx, tx, cmd.BatchID, func() error {
					if err := releaseBatchImageAPIKeyAllowance(ctx, tx, cmd.APIKeyID, adjustment, cmd.ReservedAt); err != nil {
						return err
					}
					return releaseBatchImageMemberAllowance(ctx, tx, cmd, adjustment)
				}); err != nil {
					return err
				}
			}
		} else if cmd.ActualAmount > 0 {
			// 滚动升级期间的旧任务没有预记标记，结算时按实际金额补记。
			if err := chargeLegacyBatchImageAPIKey(ctx, tx, cmd.APIKeyID, cmd.ActualAmount); err != nil {
				return err
			}
			if cmd.TeamID != nil && cmd.ActorUserID > 0 && cmd.ActorUserID != cmd.UserID {
				if err := incrementUsageBillingTeamMember(ctx, tx, *cmd.TeamID, cmd.ActorUserID, cmd.ActualAmount, time.Now()); err != nil {
					return err
				}
			}
		}
		return setBatchImageAllowanceReserved(ctx, tx, cmd.BatchID, false)
	case batchImageAllowanceRelease:
		if cmd.AllowanceReserved {
			if err := rollbackBatchImageAllowanceBestEffort(ctx, tx, cmd.BatchID, func() error {
				if err := releaseBatchImageAPIKeyAllowance(ctx, tx, cmd.APIKeyID, cmd.HoldAmount, cmd.ReservedAt); err != nil {
					return err
				}
				return releaseBatchImageMemberAllowance(ctx, tx, cmd, cmd.HoldAmount)
			}); err != nil {
				return err
			}
		}
		return setBatchImageAllowanceReserved(ctx, tx, cmd.BatchID, false)
	default:
		return nil
	}
}

// rollbackBatchImageAllowanceBestEffort 用保存点隔离额度回退故障。
// 回退失败时保留偏保守计数，但不能连带撤销已经完成的余额结算。
func rollbackBatchImageAllowanceBestEffort(ctx context.Context, tx *sql.Tx, batchID string, rollback func() error) error {
	const savepoint = "batch_image_allowance_rollback"
	if _, err := tx.ExecContext(ctx, "SAVEPOINT "+savepoint); err != nil {
		return err
	}
	if err := rollback(); err != nil {
		if _, rollbackErr := tx.ExecContext(ctx, "ROLLBACK TO SAVEPOINT "+savepoint); rollbackErr != nil {
			return rollbackErr
		}
		if _, releaseErr := tx.ExecContext(ctx, "RELEASE SAVEPOINT "+savepoint); releaseErr != nil {
			return releaseErr
		}
		logger.LegacyPrintf("repository.usage_billing", "[BatchImage] allowance rollback skipped: batch=%s error=%v", batchID, err)
		return nil
	}
	_, err := tx.ExecContext(ctx, "RELEASE SAVEPOINT "+savepoint)
	return err
}

func setBatchImageAllowanceReserved(ctx context.Context, tx *sql.Tx, batchID string, reserved bool) error {
	result, err := tx.ExecContext(ctx, `UPDATE batch_image_jobs SET allowance_reserved = $2, updated_at = NOW() WHERE batch_id = $1`, batchID, reserved)
	if err != nil {
		return err
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if affected == 0 {
		return service.ErrBatchImageJobNotFound
	}
	return nil
}

func reserveBatchImageAPIKeyAllowance(ctx context.Context, tx *sql.Tx, apiKeyID int64, amount float64, reservedAt time.Time) error {
	var id int64
	// 预记和回退必须使用同一时间点，否则数据库 NOW() 晚于任务创建时间时会误判为跨窗口。
	err := tx.QueryRowContext(ctx, `
		UPDATE api_keys SET
			quota_used = quota_used + $1,
			usage_5h = CASE WHEN window_5h_start IS NULL OR window_5h_start + INTERVAL '5 hours' <= $5 THEN $1 ELSE usage_5h + $1 END,
			usage_1d = CASE WHEN window_1d_start IS NULL OR window_1d_start + INTERVAL '24 hours' <= $5 THEN $1 ELSE usage_1d + $1 END,
			usage_7d = CASE WHEN window_7d_start IS NULL OR window_7d_start + INTERVAL '7 days' <= $5 THEN $1 ELSE usage_7d + $1 END,
			window_5h_start = CASE WHEN window_5h_start IS NULL OR window_5h_start + INTERVAL '5 hours' <= $5 THEN $5 ELSE window_5h_start END,
			window_1d_start = CASE WHEN window_1d_start IS NULL OR window_1d_start + INTERVAL '24 hours' <= $5 THEN date_trunc('day', $5::timestamptz) ELSE window_1d_start END,
			window_7d_start = CASE WHEN window_7d_start IS NULL OR window_7d_start + INTERVAL '7 days' <= $5 THEN date_trunc('day', $5::timestamptz) ELSE window_7d_start END,
			status = CASE WHEN quota > 0 AND quota_used + $1 >= quota THEN $3 ELSE status END,
			updated_at = NOW()
		WHERE id = $2 AND deleted_at IS NULL AND status = $4 AND team_owner_disabled = FALSE
		  AND (quota <= 0 OR quota_used + $1 <= quota)
		  AND (rate_limit_5h <= 0 OR (CASE WHEN window_5h_start IS NULL OR window_5h_start + INTERVAL '5 hours' <= $5 THEN 0 ELSE usage_5h END) + $1 <= rate_limit_5h)
		  AND (rate_limit_1d <= 0 OR (CASE WHEN window_1d_start IS NULL OR window_1d_start + INTERVAL '24 hours' <= $5 THEN 0 ELSE usage_1d END) + $1 <= rate_limit_1d)
		  AND (rate_limit_7d <= 0 OR (CASE WHEN window_7d_start IS NULL OR window_7d_start + INTERVAL '7 days' <= $5 THEN 0 ELSE usage_7d END) + $1 <= rate_limit_7d)
		RETURNING id`, amount, apiKeyID, service.StatusAPIKeyQuotaExhausted, service.StatusAPIKeyActive, reservedAt).Scan(&id)
	if err == nil {
		return nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return err
	}
	return batchImageAPIKeyAllowanceError(ctx, tx, apiKeyID, amount)
}

func batchImageAPIKeyAllowanceError(ctx context.Context, tx *sql.Tx, apiKeyID int64, amount float64) error {
	var status string
	var ownerDisabled bool
	var quota, quotaUsed, limit5h, limit1d, limit7d, usage5h, usage1d, usage7d float64
	var start5h, start1d, start7d sql.NullTime
	err := tx.QueryRowContext(ctx, `
		SELECT status, team_owner_disabled, quota, quota_used, rate_limit_5h, rate_limit_1d, rate_limit_7d,
		       usage_5h, usage_1d, usage_7d, window_5h_start, window_1d_start, window_7d_start
		FROM api_keys WHERE id = $1 AND deleted_at IS NULL`, apiKeyID).
		Scan(&status, &ownerDisabled, &quota, &quotaUsed, &limit5h, &limit1d, &limit7d, &usage5h, &usage1d, &usage7d, &start5h, &start1d, &start7d)
	if errors.Is(err, sql.ErrNoRows) {
		return service.ErrAPIKeyNotFound
	}
	if err != nil {
		return err
	}
	if ownerDisabled || status != service.StatusAPIKeyActive {
		if status == service.StatusAPIKeyQuotaExhausted {
			return service.ErrAPIKeyQuotaExhausted
		}
		return service.ErrAPIKeyNotFound
	}
	if quota > 0 && quotaUsed+amount > quota {
		return service.ErrAPIKeyQuotaExhausted
	}
	now := time.Now()
	if limit5h > 0 && effectiveSQLWindowUsage(usage5h, start5h, service.RateLimitWindow5h, now)+amount > limit5h {
		return service.ErrAPIKeyRateLimit5hExceeded
	}
	if limit1d > 0 && effectiveSQLWindowUsage(usage1d, start1d, service.RateLimitWindow1d, now)+amount > limit1d {
		return service.ErrAPIKeyRateLimit1dExceeded
	}
	if limit7d > 0 && effectiveSQLWindowUsage(usage7d, start7d, service.RateLimitWindow7d, now)+amount > limit7d {
		return service.ErrAPIKeyRateLimit7dExceeded
	}
	return service.ErrAPIKeyNotFound
}

func effectiveSQLWindowUsage(usage float64, start sql.NullTime, duration time.Duration, now time.Time) float64 {
	if !start.Valid || !start.Time.Add(duration).After(now) {
		return 0
	}
	return usage
}

func reserveBatchImageMemberAllowance(ctx context.Context, tx *sql.Tx, cmd *service.BatchImageBalanceHoldCommand, amount float64) error {
	if cmd.TeamID == nil || cmd.ActorUserID <= 0 || cmd.ActorUserID == cmd.UserID {
		return nil
	}
	now := time.Now()
	dailyStart := timezone.StartOfDay(now)
	weeklyStart := timezone.StartOfWeek(now)
	monthlyStart := timezone.StartOfMonth(now)
	var id int64
	err := tx.QueryRowContext(ctx, `
		UPDATE team_memberships SET
			daily_usage_usd = CASE WHEN daily_window_start IS NULL OR daily_window_start < $4 THEN $3 ELSE daily_usage_usd + $3 END,
			weekly_usage_usd = CASE WHEN weekly_window_start IS NULL OR weekly_window_start < $5 THEN $3 ELSE weekly_usage_usd + $3 END,
			monthly_usage_usd = CASE WHEN monthly_window_start IS NULL OR monthly_window_start < $6 THEN $3 ELSE monthly_usage_usd + $3 END,
			daily_window_start = CASE WHEN daily_window_start IS NULL OR daily_window_start < $4 THEN $4 ELSE daily_window_start END,
			weekly_window_start = CASE WHEN weekly_window_start IS NULL OR weekly_window_start < $5 THEN $5 ELSE weekly_window_start END,
			monthly_window_start = CASE WHEN monthly_window_start IS NULL OR monthly_window_start < $6 THEN $6 ELSE monthly_window_start END,
			updated_at = $7
		WHERE team_id = $1 AND user_id = $2 AND left_at IS NULL AND role = 'member'
		  AND (daily_limit_usd <= 0 OR (CASE WHEN daily_window_start IS NULL OR daily_window_start < $4 THEN 0 ELSE daily_usage_usd END) + $3 <= daily_limit_usd)
		  AND (weekly_limit_usd <= 0 OR (CASE WHEN weekly_window_start IS NULL OR weekly_window_start < $5 THEN 0 ELSE weekly_usage_usd END) + $3 <= weekly_limit_usd)
		  AND (monthly_limit_usd <= 0 OR (CASE WHEN monthly_window_start IS NULL OR monthly_window_start < $6 THEN 0 ELSE monthly_usage_usd END) + $3 <= monthly_limit_usd)
		RETURNING id`, *cmd.TeamID, cmd.ActorUserID, amount, dailyStart, weeklyStart, monthlyStart, now).Scan(&id)
	if err == nil {
		return nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return err
	}
	return batchImageMemberAllowanceError(ctx, tx, *cmd.TeamID, cmd.ActorUserID, amount, dailyStart, weeklyStart, monthlyStart)
}

func batchImageMemberAllowanceError(ctx context.Context, tx *sql.Tx, teamID, userID int64, amount float64, dailyStart, weeklyStart, monthlyStart time.Time) error {
	var role string
	var dailyLimit, weeklyLimit, monthlyLimit, dailyUsage, weeklyUsage, monthlyUsage float64
	var dailyWindow, weeklyWindow, monthlyWindow sql.NullTime
	err := tx.QueryRowContext(ctx, `
		SELECT role, daily_limit_usd, weekly_limit_usd, monthly_limit_usd,
		       daily_usage_usd, weekly_usage_usd, monthly_usage_usd,
		       daily_window_start, weekly_window_start, monthly_window_start
		FROM team_memberships WHERE team_id = $1 AND user_id = $2 AND left_at IS NULL`, teamID, userID).
		Scan(&role, &dailyLimit, &weeklyLimit, &monthlyLimit, &dailyUsage, &weeklyUsage, &monthlyUsage, &dailyWindow, &weeklyWindow, &monthlyWindow)
	if errors.Is(err, sql.ErrNoRows) {
		return service.ErrTeamMembershipRequired
	}
	if err != nil {
		return err
	}
	if role == service.TeamRoleOwner {
		return nil
	}
	if dailyLimit > 0 && effectiveNaturalWindowUsage(dailyUsage, dailyWindow, dailyStart)+amount > dailyLimit {
		return service.ErrTeamMemberDailyExceeded
	}
	if weeklyLimit > 0 && effectiveNaturalWindowUsage(weeklyUsage, weeklyWindow, weeklyStart)+amount > weeklyLimit {
		return service.ErrTeamMemberWeeklyExceeded
	}
	if monthlyLimit > 0 && effectiveNaturalWindowUsage(monthlyUsage, monthlyWindow, monthlyStart)+amount > monthlyLimit {
		return service.ErrTeamMemberMonthlyExceeded
	}
	return service.ErrTeamMembershipRequired
}

func effectiveNaturalWindowUsage(usage float64, window sql.NullTime, expectedStart time.Time) float64 {
	if !window.Valid || window.Time.Before(expectedStart) {
		return 0
	}
	return usage
}

func releaseBatchImageAPIKeyAllowance(ctx context.Context, tx *sql.Tx, apiKeyID int64, amount float64, reservedAt time.Time) error {
	_, err := tx.ExecContext(ctx, `
		UPDATE api_keys SET
			quota_used = GREATEST(0, quota_used - $1),
			usage_5h = CASE WHEN window_5h_start <= $3 AND $3 < window_5h_start + INTERVAL '5 hours' THEN GREATEST(0, usage_5h - $1) ELSE usage_5h END,
			usage_1d = CASE WHEN window_1d_start <= $3 AND $3 < window_1d_start + INTERVAL '24 hours' THEN GREATEST(0, usage_1d - $1) ELSE usage_1d END,
			usage_7d = CASE WHEN window_7d_start <= $3 AND $3 < window_7d_start + INTERVAL '7 days' THEN GREATEST(0, usage_7d - $1) ELSE usage_7d END,
			status = CASE WHEN status = $4 AND team_owner_disabled = FALSE AND (quota <= 0 OR GREATEST(0, quota_used - $1) < quota) THEN $5 ELSE status END,
			updated_at = NOW()
		WHERE id = $2`, amount, apiKeyID, reservedAt, service.StatusAPIKeyQuotaExhausted, service.StatusAPIKeyActive)
	return err
}

func releaseBatchImageMemberAllowance(ctx context.Context, tx *sql.Tx, cmd *service.BatchImageBalanceHoldCommand, amount float64) error {
	if cmd.TeamID == nil || cmd.ActorUserID <= 0 || cmd.ActorUserID == cmd.UserID {
		return nil
	}
	_, err := tx.ExecContext(ctx, `
		UPDATE team_memberships SET
			daily_usage_usd = CASE WHEN daily_window_start <= $4 AND $4 < daily_window_start + INTERVAL '1 day' THEN GREATEST(0, daily_usage_usd - $3) ELSE daily_usage_usd END,
			weekly_usage_usd = CASE WHEN weekly_window_start <= $4 AND $4 < weekly_window_start + INTERVAL '7 days' THEN GREATEST(0, weekly_usage_usd - $3) ELSE weekly_usage_usd END,
			monthly_usage_usd = CASE WHEN monthly_window_start <= $4 AND $4 < monthly_window_start + INTERVAL '1 month' THEN GREATEST(0, monthly_usage_usd - $3) ELSE monthly_usage_usd END,
			updated_at = NOW()
		WHERE team_id = $1 AND user_id = $2
		  AND joined_at <= $4 AND (left_at IS NULL OR left_at > $4)`, *cmd.TeamID, cmd.ActorUserID, amount, cmd.ReservedAt)
	return err
}

func chargeLegacyBatchImageAPIKey(ctx context.Context, tx *sql.Tx, apiKeyID int64, amount float64) error {
	_, err := tx.ExecContext(ctx, `
		UPDATE api_keys SET
			quota_used = quota_used + $1,
			usage_5h = CASE WHEN window_5h_start IS NULL OR window_5h_start + INTERVAL '5 hours' <= NOW() THEN $1 ELSE usage_5h + $1 END,
			usage_1d = CASE WHEN window_1d_start IS NULL OR window_1d_start + INTERVAL '24 hours' <= NOW() THEN $1 ELSE usage_1d + $1 END,
			usage_7d = CASE WHEN window_7d_start IS NULL OR window_7d_start + INTERVAL '7 days' <= NOW() THEN $1 ELSE usage_7d + $1 END,
			window_5h_start = CASE WHEN window_5h_start IS NULL OR window_5h_start + INTERVAL '5 hours' <= NOW() THEN NOW() ELSE window_5h_start END,
			window_1d_start = CASE WHEN window_1d_start IS NULL OR window_1d_start + INTERVAL '24 hours' <= NOW() THEN date_trunc('day', NOW()) ELSE window_1d_start END,
			window_7d_start = CASE WHEN window_7d_start IS NULL OR window_7d_start + INTERVAL '7 days' <= NOW() THEN date_trunc('day', NOW()) ELSE window_7d_start END,
			status = CASE WHEN quota > 0 AND quota_used + $1 >= quota AND team_owner_disabled = FALSE THEN $3 ELSE status END,
			updated_at = NOW()
		WHERE id = $2`, amount, apiKeyID, service.StatusAPIKeyQuotaExhausted)
	return err
}

func (r *usageBillingRepository) applyUsageBillingEffects(ctx context.Context, tx *sql.Tx, cmd *service.UsageBillingCommand, result *service.UsageBillingApplyResult) error {
	remainingAmount, subscriptionAmount, allocations, err := allocateUsageBillingSubscriptions(ctx, tx, cmd)
	if err != nil {
		return err
	}
	result.BillingAllocations = allocations
	result.SubscriptionAmountUSD = subscriptionAmount

	// 普通请求到这里已经完成上游调用；指定订阅只扣到额度上限，已放行请求的溢出部分记为余额欠费。
	balanceAmount := remainingAmount
	if usageBillingUsesBaseAmount(cmd) {
		balanceAmount = remainingAmount * usageBillingNonNegativeRate(cmd.BalanceRateMultiplier)
	}
	if balanceAmount > 0 {
		newBalance, deductedAmount, err := deductUsageBillingBalance(ctx, tx, cmd.UserID, balanceAmount)
		if err != nil {
			return err
		}
		result.BalanceAmountUSD = deductedAmount
		if deductedAmount > 0 {
			result.NewBalance = &newBalance
			result.BillingAllocations = append(result.BillingAllocations, domain.BillingAllocation{
				Type:      domain.BillingAllocationTypeBalance,
				AmountUSD: deductedAmount,
			})
		}
	}

	billableAmount := result.SubscriptionAmountUSD + result.BalanceAmountUSD
	if cmd.TeamID != nil && cmd.ActorUserID > 0 && cmd.ActorUserID != cmd.UserID {
		if err := incrementUsageBillingTeamMember(ctx, tx, *cmd.TeamID, cmd.ActorUserID, billableAmount, time.Now()); err != nil {
			return err
		}
	}
	if usageBillingUsesBaseAmount(cmd) && cmd.BaseAmountUSD > 0 {
		effectiveRate := billableAmount / cmd.BaseAmountUSD
		result.EffectiveRateMultiplier = &effectiveRate
	}
	if cmd.APIKeyQuotaCost > 0 {
		quotaCost := cmd.APIKeyQuotaCost
		if usageBillingUsesBaseAmount(cmd) {
			quotaCost = billableAmount
		}
		exhausted, err := incrementUsageBillingAPIKeyQuota(ctx, tx, cmd.APIKeyID, quotaCost)
		if err != nil {
			return err
		}
		result.APIKeyQuotaExhausted = exhausted
	}

	if cmd.APIKeyRateLimitCost > 0 {
		rateLimitCost := cmd.APIKeyRateLimitCost
		if usageBillingUsesBaseAmount(cmd) {
			rateLimitCost = billableAmount
		}
		if err := incrementUsageBillingAPIKeyRateLimit(ctx, tx, cmd.APIKeyID, rateLimitCost); err != nil {
			return err
		}
	}

	if cmd.AccountQuotaCost > 0 && (strings.EqualFold(cmd.AccountType, service.AccountTypeAPIKey) || strings.EqualFold(cmd.AccountType, service.AccountTypeBedrock)) {
		quotaState, err := incrementUsageBillingAccountQuota(ctx, tx, cmd.AccountID, cmd.AccountQuotaCost)
		if err != nil {
			return err
		}
		result.QuotaState = quotaState
	}

	return nil
}

// incrementUsageBillingTeamMember 在同一扣费事务中累计成员自然周期用量。
func incrementUsageBillingTeamMember(ctx context.Context, tx *sql.Tx, teamID, actorUserID int64, amount float64, now time.Time) error {
	if amount <= 0 {
		return nil
	}
	dailyStart := timezone.StartOfDay(now)
	weeklyStart := timezone.StartOfWeek(now)
	monthlyStart := timezone.StartOfMonth(now)
	_, err := tx.ExecContext(ctx, `
		UPDATE team_memberships SET
			daily_usage_usd = CASE WHEN daily_window_start IS NULL OR daily_window_start < $4 THEN $3 ELSE daily_usage_usd + $3 END,
			weekly_usage_usd = CASE WHEN weekly_window_start IS NULL OR weekly_window_start < $5 THEN $3 ELSE weekly_usage_usd + $3 END,
			monthly_usage_usd = CASE WHEN monthly_window_start IS NULL OR monthly_window_start < $6 THEN $3 ELSE monthly_usage_usd + $3 END,
			daily_window_start = CASE WHEN daily_window_start IS NULL OR daily_window_start < $4 THEN $4 ELSE daily_window_start END,
			weekly_window_start = CASE WHEN weekly_window_start IS NULL OR weekly_window_start < $5 THEN $5 ELSE weekly_window_start END,
			monthly_window_start = CASE WHEN monthly_window_start IS NULL OR monthly_window_start < $6 THEN $6 ELSE monthly_window_start END,
			updated_at = $7
		WHERE team_id = $1 AND user_id = $2 AND left_at IS NULL AND role = 'member'`,
		teamID, actorUserID, amount, dailyStart, weeklyStart, monthlyStart, now)
	if err != nil {
		return err
	}
	// 请求在途期间成员可能退出或成为 Owner；付款快照仍需完成，只跳过已失效的成员限额计数。
	return nil
}

type usageBillingSubscriptionRow struct {
	ID                          int64
	PlanID                      int64
	StartsAt                    time.Time
	ExpiresAt                   time.Time
	DailyWindowStart            sql.NullTime
	WeeklyWindowStart           sql.NullTime
	MonthlyWindowStart          sql.NullTime
	DailyLimitUSD               sql.NullFloat64
	WeeklyLimitUSD              sql.NullFloat64
	MonthlyLimitUSD             sql.NullFloat64
	DailyUsageUSD               float64
	WeeklyUsageUSD              float64
	MonthlyUsageUSD             float64
	PlanGroupIDsRaw             []byte
	PlanGroupRateMultipliersRaw []byte
}

func usageBillingSubscriptionRowToService(userID int64, row usageBillingSubscriptionRow) *service.UserSubscription {
	return &service.UserSubscription{
		ID:                 row.ID,
		UserID:             userID,
		PlanID:             row.PlanID,
		StartsAt:           row.StartsAt,
		ExpiresAt:          row.ExpiresAt,
		Status:             service.SubscriptionStatusActive,
		DailyWindowStart:   usageBillingNullableTimePtr(row.DailyWindowStart),
		WeeklyWindowStart:  usageBillingNullableTimePtr(row.WeeklyWindowStart),
		MonthlyWindowStart: usageBillingNullableTimePtr(row.MonthlyWindowStart),
		DailyLimitUSD:      usageBillingNullableFloat64Ptr(row.DailyLimitUSD),
		WeeklyLimitUSD:     usageBillingNullableFloat64Ptr(row.WeeklyLimitUSD),
		MonthlyLimitUSD:    usageBillingNullableFloat64Ptr(row.MonthlyLimitUSD),
		DailyUsageUSD:      row.DailyUsageUSD,
		WeeklyUsageUSD:     row.WeeklyUsageUSD,
		MonthlyUsageUSD:    row.MonthlyUsageUSD,
		Plan: &service.SubscriptionPlan{
			ID:                   row.PlanID,
			GroupIDs:             parseInt64JSONSlice(row.PlanGroupIDsRaw),
			GroupRateMultipliers: parseInt64Float64JSONMap(row.PlanGroupRateMultipliersRaw),
		},
	}
}

func parseInt64JSONSlice(data []byte) []int64 {
	if len(data) == 0 {
		return nil
	}
	var out []int64
	if err := json.Unmarshal(data, &out); err != nil {
		return nil
	}
	return out
}

func parseInt64Float64JSONMap(data []byte) map[int64]float64 {
	if len(data) == 0 {
		return map[int64]float64{}
	}
	var raw map[string]float64
	if err := json.Unmarshal(data, &raw); err != nil {
		return map[int64]float64{}
	}
	out := make(map[int64]float64, len(raw))
	for key, value := range raw {
		id, err := strconv.ParseInt(key, 10, 64)
		if err != nil || id <= 0 {
			continue
		}
		out[id] = value
	}
	return out
}

func usageBillingNullableTimePtr(v sql.NullTime) *time.Time {
	if !v.Valid {
		return nil
	}
	return &v.Time
}

func usageBillingNullableFloat64Ptr(v sql.NullFloat64) *float64 {
	if !v.Valid {
		return nil
	}
	return &v.Float64
}

func allocateUsageBillingSubscriptions(ctx context.Context, tx *sql.Tx, cmd *service.UsageBillingCommand) (float64, float64, []domain.BillingAllocation, error) {
	if cmd == nil {
		return 0, 0, nil, nil
	}
	amountUSD := cmd.BillableAmountUSD
	if usageBillingUsesBaseAmount(cmd) {
		amountUSD = cmd.BaseAmountUSD
	}
	if amountUSD <= 0 {
		return 0, 0, nil, nil
	}
	if cmd.APIKeyBillingMode == service.APIKeyBillingModeBalance {
		return amountUSD, 0, nil, nil
	}
	if cmd.APIKeyBillingMode == service.APIKeyBillingModeSubscription && (cmd.PreferredSubscriptionID == nil || *cmd.PreferredSubscriptionID <= 0) {
		return 0, 0, nil, service.ErrPreferredSubscriptionInvalid
	}

	query := `
		SELECT
			id,
			plan_id,
			starts_at,
			expires_at,
			daily_window_start,
			weekly_window_start,
			monthly_window_start,
			daily_limit_usd,
			weekly_limit_usd,
			monthly_limit_usd,
			daily_usage_usd,
			weekly_usage_usd,
			monthly_usage_usd,
			COALESCE((
				SELECT jsonb_object_agg(spg.group_id, spg.rate_multiplier)
				FROM subscription_plan_groups spg
				WHERE spg.plan_id = user_subscriptions.plan_id
					AND spg.rate_multiplier IS NOT NULL
			), '{}'::jsonb)
		FROM user_subscriptions
		WHERE user_id = $1
			AND deleted_at IS NULL
			AND starts_at <= NOW()
			AND expires_at > NOW()
			AND status IN ($2, $3)
			AND ($4::bigint IS NULL OR id = $4)
`
	var preferredSubscriptionID any
	if cmd.PreferredSubscriptionID != nil {
		preferredSubscriptionID = *cmd.PreferredSubscriptionID
	}
	args := []any{cmd.UserID, service.SubscriptionStatusActive, service.SubscriptionStatusPending, preferredSubscriptionID}
	if cmd.GroupID != nil && *cmd.GroupID > 0 {
		query += `
			AND EXISTS (
				SELECT 1
				FROM subscription_plans sp
				WHERE sp.id = user_subscriptions.plan_id
					AND (
						NOT EXISTS (
							SELECT 1
							FROM subscription_plan_groups spg
							WHERE spg.plan_id = user_subscriptions.plan_id
						)
						OR EXISTS (
							SELECT 1
							FROM subscription_plan_groups spg
							WHERE spg.plan_id = user_subscriptions.plan_id
								AND spg.group_id = $5
						)
					)
				)
`
		args = append(args, *cmd.GroupID)
	} else if cmd.APIKeyBillingMode == service.APIKeyBillingModeSubscription {
		// 指定订阅没有最终分组时，只允许套餐本身不限制分组；受限套餐不能由无分组路径扣费。
		query += `
			AND NOT EXISTS (
				SELECT 1
				FROM subscription_plan_groups spg
				WHERE spg.plan_id = user_subscriptions.plan_id
			)
`
	}
	query += `
		ORDER BY expires_at ASC, starts_at ASC, id ASC
		FOR UPDATE
	`
	rows, err := tx.QueryContext(ctx, query, args...)
	if err != nil {
		return 0, 0, nil, err
	}
	subscriptions := make([]usageBillingSubscriptionRow, 0)
	for rows.Next() {
		var row usageBillingSubscriptionRow
		if err := rows.Scan(
			&row.ID,
			&row.PlanID,
			&row.StartsAt,
			&row.ExpiresAt,
			&row.DailyWindowStart,
			&row.WeeklyWindowStart,
			&row.MonthlyWindowStart,
			&row.DailyLimitUSD,
			&row.WeeklyLimitUSD,
			&row.MonthlyLimitUSD,
			&row.DailyUsageUSD,
			&row.WeeklyUsageUSD,
			&row.MonthlyUsageUSD,
			&row.PlanGroupRateMultipliersRaw,
		); err != nil {
			_ = rows.Close()
			return 0, 0, nil, err
		}
		subscriptions = append(subscriptions, row)
	}
	if err := rows.Err(); err != nil {
		_ = rows.Close()
		return 0, 0, nil, err
	}
	if err := rows.Close(); err != nil {
		return 0, 0, nil, err
	}
	// 指定订阅不存在、失效或不覆盖最终分组时不能把整笔请求伪装成余额回退。
	if cmd.APIKeyBillingMode == service.APIKeyBillingModeSubscription && len(subscriptions) == 0 {
		return 0, 0, nil, service.ErrPreferredSubscriptionInsufficient
	}

	now := time.Now()
	remaining := amountUSD
	subscriptionAmount := 0.0
	allocations := make([]domain.BillingAllocation, 0, len(subscriptions))

	for _, row := range subscriptions {
		if remaining <= 0 {
			break
		}

		row = normalizeUsageBillingSubscriptionRow(row, now)

		rateMultiplier := 1.0
		if usageBillingUsesBaseAmount(cmd) {
			if cmd.DisablePlanGroupRateMultiplier {
				rateMultiplier = usageBillingNonNegativeRate(cmd.SubscriptionRateMultiplier)
			} else {
				rateMultiplier = usageBillingSubscriptionRateMultiplier(row, cmd.GroupID, cmd.SubscriptionRateMultiplier, cmd.SubscriptionRateMultiplierScale)
			}
			if rateMultiplier <= 0 {
				remaining = 0
				break
			}
		}
		remainingBillable := remaining
		if usageBillingUsesBaseAmount(cmd) {
			remainingBillable = remaining * rateMultiplier
		}

		available := usageBillingSubscriptionAvailable(
			remainingBillable,
			windowRemaining(row.DailyLimitUSD, row.DailyUsageUSD),
			windowRemaining(row.WeeklyLimitUSD, row.WeeklyUsageUSD),
			windowRemaining(row.MonthlyLimitUSD, row.MonthlyUsageUSD),
		)
		if available <= 0 {
			continue
		}

		allocated := math.Min(remainingBillable, available)
		if allocated <= 0 {
			continue
		}
		coveredBaseAmount := allocated
		if usageBillingUsesBaseAmount(cmd) {
			coveredBaseAmount = allocated / rateMultiplier
		}

		if row.DailyLimitUSD.Valid && row.DailyLimitUSD.Float64 > 0 {
			row.DailyUsageUSD += allocated
		}
		if row.WeeklyLimitUSD.Valid && row.WeeklyLimitUSD.Float64 > 0 {
			row.WeeklyUsageUSD += allocated
		}
		if row.MonthlyLimitUSD.Valid && row.MonthlyLimitUSD.Float64 > 0 {
			row.MonthlyUsageUSD += allocated
		}

		if err := updateUsageBillingSubscription(
			ctx,
			tx,
			row.ID,
			usageBillingNullableTimePtr(row.DailyWindowStart),
			usageBillingNullableTimePtr(row.WeeklyWindowStart),
			usageBillingNullableTimePtr(row.MonthlyWindowStart),
			row.DailyUsageUSD,
			row.WeeklyUsageUSD,
			row.MonthlyUsageUSD,
		); err != nil {
			return 0, 0, nil, err
		}

		subscriptionID := row.ID
		planID := row.PlanID
		allocation := domain.BillingAllocation{
			Type:           domain.BillingAllocationTypeSubscription,
			AmountUSD:      allocated,
			SubscriptionID: &subscriptionID,
			PlanID:         &planID,
		}
		if cmd.IncludeAllocationPricing {
			allocation.BaseAmountUSD = coveredBaseAmount
			allocation.RateMultiplier = rateMultiplier
		}
		allocations = append(allocations, allocation)
		subscriptionAmount += allocated
		remaining -= coveredBaseAmount
	}

	return remaining, subscriptionAmount, allocations, nil
}

func usageBillingUsesBaseAmount(cmd *service.UsageBillingCommand) bool {
	return cmd != nil && cmd.BaseAmountUSD > 0
}

func usageBillingNonNegativeRate(rate float64) float64 {
	if rate < 0 {
		return 0
	}
	return rate
}

func usageBillingSubscriptionRateMultiplier(row usageBillingSubscriptionRow, groupID *int64, defaultRate, scale float64) float64 {
	rate := defaultRate
	if groupID != nil && *groupID > 0 {
		if value, ok := parseInt64Float64JSONMap(row.PlanGroupRateMultipliersRaw)[*groupID]; ok && value > 0 {
			if scale <= 0 {
				scale = 1
			}
			rate = value * scale
		}
	}
	return usageBillingNonNegativeRate(rate)
}

// @project-doc docs/domains/payments_and_entitlements.md#subscription_quota_windows
// normalizeUsageBillingSubscriptionRow 统一解析倍率与事务扣费看到的额度窗口状态。
func normalizeUsageBillingSubscriptionRow(row usageBillingSubscriptionRow, now time.Time) usageBillingSubscriptionRow {
	windowStart := startOfDay(now)
	dailyHasFiniteOuterLimit := hasFiniteUsageBillingLimit(row.WeeklyLimitUSD) || hasFiniteUsageBillingLimit(row.MonthlyLimitUSD)
	weeklyHasFiniteOuterLimit := hasFiniteUsageBillingLimit(row.MonthlyLimitUSD)

	dailyStart, dailyUsage := normalizeUsageBillingWindow(
		row.DailyWindowStart, row.DailyLimitUSD, row.DailyUsageUSD,
		windowStart, 24*time.Hour, now, row.StartsAt, row.ExpiresAt, dailyHasFiniteOuterLimit,
	)
	weeklyStart, weeklyUsage := normalizeUsageBillingWindow(
		row.WeeklyWindowStart, row.WeeklyLimitUSD, row.WeeklyUsageUSD,
		windowStart, 7*24*time.Hour, now, row.StartsAt, row.ExpiresAt, weeklyHasFiniteOuterLimit,
	)
	monthlyStart, monthlyUsage := normalizeUsageBillingWindow(
		row.MonthlyWindowStart, row.MonthlyLimitUSD, row.MonthlyUsageUSD,
		windowStart, 30*24*time.Hour, now, row.StartsAt, row.ExpiresAt, false,
	)

	row.DailyWindowStart = nullTimePtr(dailyStart)
	row.WeeklyWindowStart = nullTimePtr(weeklyStart)
	row.MonthlyWindowStart = nullTimePtr(monthlyStart)
	row.DailyUsageUSD = dailyUsage
	row.WeeklyUsageUSD = weeklyUsage
	row.MonthlyUsageUSD = monthlyUsage
	return row
}

func normalizeUsageBillingWindow(
	windowStart sql.NullTime,
	limit sql.NullFloat64,
	used float64,
	resetStart time.Time,
	duration time.Duration,
	now, startsAt, expiresAt time.Time,
	hasFiniteOuterLimit bool,
) (*time.Time, float64) {
	if !limit.Valid || limit.Float64 <= 0 {
		if !windowStart.Valid {
			return nil, used
		}
		start := windowStart.Time
		return &start, used
	}

	// 1 日卡是一次性日额度：首次扣费要记录窗口，但跨过 24 小时边界后不能清零。
	if duration == 24*time.Hour && !expiresAt.After(startsAt.AddDate(0, 0, 1)) {
		if !windowStart.Valid || windowStart.Time.IsZero() {
			start := resetStart
			return &start, 0
		}
		start := windowStart.Time
		return &start, used
	}

	// 没有有限外层额度保护时，尾段仍须容纳完整窗口，避免最高层额度重复发放。
	if !canStartUsageBillingWindow(resetStart, duration, expiresAt, hasFiniteOuterLimit) {
		if !windowStart.Valid || windowStart.Time.IsZero() {
			return nil, used
		}
		start := windowStart.Time
		return &start, used
	}

	if !windowStart.Valid || windowStart.Time.IsZero() || !windowStart.Time.Add(duration).After(now) {
		start := resetStart
		return &start, 0
	}
	start := windowStart.Time
	return &start, used
}

func canStartUsageBillingWindow(windowStart time.Time, duration time.Duration, expiresAt time.Time, hasFiniteOuterLimit bool) bool {
	if expiresAt.IsZero() || duration <= 0 || !windowStart.Before(expiresAt) {
		return false
	}
	return hasFiniteOuterLimit || !windowStart.Add(duration).After(expiresAt)
}

func hasFiniteUsageBillingLimit(limit sql.NullFloat64) bool {
	return limit.Valid && limit.Float64 > 0
}

func windowRemaining(limit sql.NullFloat64, used float64) *float64 {
	if !limit.Valid || limit.Float64 <= 0 {
		return nil
	}
	remaining := limit.Float64 - used
	if remaining < 0 {
		remaining = 0
	}
	return &remaining
}

func usageBillingSubscriptionAvailable(unlimitedAmount float64, values ...*float64) float64 {
	var (
		min   float64
		found bool
	)
	for _, value := range values {
		if value == nil {
			continue
		}
		if !found || *value < min {
			min = *value
			found = true
		}
	}
	if !found {
		// nil 表示该窗口无限额；所有窗口都无限时，本次剩余费用都由订阅覆盖。
		return unlimitedAmount
	}
	return min
}

func updateUsageBillingSubscription(
	ctx context.Context,
	tx *sql.Tx,
	subscriptionID int64,
	dailyWindowStart *time.Time,
	weeklyWindowStart *time.Time,
	monthlyWindowStart *time.Time,
	dailyUsageUSD float64,
	weeklyUsageUSD float64,
	monthlyUsageUSD float64,
) error {
	res, err := tx.ExecContext(ctx, `
		UPDATE user_subscriptions
		SET
			daily_window_start = $1,
			weekly_window_start = $2,
			monthly_window_start = $3,
			daily_usage_usd = $4,
			weekly_usage_usd = $5,
			monthly_usage_usd = $6,
			updated_at = NOW()
		WHERE id = $7
			AND deleted_at IS NULL
	`, nullTimePtr(dailyWindowStart), nullTimePtr(weeklyWindowStart), nullTimePtr(monthlyWindowStart), dailyUsageUSD, weeklyUsageUSD, monthlyUsageUSD, subscriptionID)
	if err != nil {
		return err
	}
	affected, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if affected == 0 {
		return service.ErrSubscriptionNotFound
	}
	return nil
}

func nullTimePtr(value *time.Time) sql.NullTime {
	if value == nil {
		return sql.NullTime{}
	}
	return sql.NullTime{Time: *value, Valid: true}
}

func startOfDay(t time.Time) time.Time {
	return time.Date(t.Year(), t.Month(), t.Day(), 0, 0, 0, 0, t.Location())
}

// usage billing 必须完整记录本次请求成本，余额不足时扣成负数作为欠费。
// 此处不能升级为 FOR UPDATE，否则取得订阅锁后会再次与 usage_logs 的用户外键锁形成锁环。
func deductUsageBillingBalance(ctx context.Context, tx *sql.Tx, userID int64, amount float64) (float64, float64, error) {
	// 余额列是 NUMERIC(20,8)，必须在执行减法前固定刻度，避免与配额加法向相反方向舍入。
	amount = service.QuantizeUsageBillingAmount(amount)
	const query = `
		WITH locked_user AS (
			SELECT id, balance
			FROM users
			WHERE id = $2
				AND deleted_at IS NULL
				FOR NO KEY UPDATE
		), updated AS (
			UPDATE users
			SET balance = locked_user.balance - $1,
				updated_at = NOW()
			FROM locked_user
			WHERE users.id = locked_user.id
			RETURNING users.balance
		)
		SELECT updated.balance, $1::numeric AS deducted_amount
		FROM updated
	`

	var (
		newBalance     float64
		deductedAmount float64
	)
	if err := scanSingleRow(ctx, tx, query, []any{amount, userID}, &newBalance, &deductedAmount); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return 0, 0, service.ErrUserNotFound
		}
		return 0, 0, err
	}
	return newBalance, deductedAmount, nil
}

// reserveUsageBillingBatchImageBilling 先预占订阅额度，再冻结未覆盖的按量余额。
func reserveUsageBillingBatchImageBilling(ctx context.Context, tx *sql.Tx, cmd *service.BatchImageBalanceHoldCommand) (*service.BatchImageBalanceHoldResult, error) {
	result := &service.BatchImageBalanceHoldResult{}
	if cmd == nil || (cmd.HoldAmount <= 0 && cmd.BaseAmountUSD <= 0) {
		return result, nil
	}
	allocationCommand := &service.UsageBillingCommand{
		APIKeyBillingMode:               cmd.APIKeyBillingMode,
		PreferredSubscriptionID:         cmd.PreferredSubscriptionID,
		UserID:                          cmd.UserID,
		GroupID:                         cmd.GroupID,
		BillableAmountUSD:               cmd.HoldAmount,
		BaseAmountUSD:                   cmd.BaseAmountUSD,
		SubscriptionRateMultiplier:      cmd.SubscriptionRateMultiplier,
		SubscriptionRateMultiplierScale: cmd.SubscriptionRateMultiplierScale,
		BalanceRateMultiplier:           cmd.BalanceRateMultiplier,
		DisablePlanGroupRateMultiplier:  cmd.DisablePlanGroupRateMultiplier,
		IncludeAllocationPricing:        cmd.PricingSnapshotVersion >= 2,
	}
	allocationCommand.Normalize()
	remainingBase, subscriptionAmount, allocations, err := allocateUsageBillingSubscriptions(ctx, tx, allocationCommand)
	if err != nil {
		return nil, err
	}
	if allocationCommand.APIKeyBillingMode == service.APIKeyBillingModeSubscription && remainingBase > 1e-10 {
		return nil, service.ErrPreferredSubscriptionInsufficient
	}
	balanceAmount := remainingBase
	if usageBillingUsesBaseAmount(allocationCommand) {
		balanceAmount = remainingBase * usageBillingNonNegativeRate(cmd.BalanceRateMultiplier)
	}
	balanceAmount = normalizeBatchImageBalanceAmount(balanceAmount)

	balanceCommand := *cmd
	balanceCommand.HoldAmount = balanceAmount
	balanceResult, err := reserveUsageBillingBatchImageBalance(ctx, tx, &balanceCommand)
	if err != nil {
		return nil, err
	}
	if balanceResult != nil {
		result.NewBalance = balanceResult.NewBalance
		result.FrozenBalance = balanceResult.FrozenBalance
	}
	result.SubscriptionAmountUSD = subscriptionAmount
	result.BalanceAmountUSD = balanceAmount
	result.BillingAllocations = append(result.BillingAllocations, allocations...)
	if balanceAmount > 0 {
		result.BillingAllocations = append(result.BillingAllocations, domain.BillingAllocation{
			Type:           domain.BillingAllocationTypeBalance,
			AmountUSD:      balanceAmount,
			BaseAmountUSD:  remainingBase,
			RateMultiplier: usageBillingNonNegativeRate(cmd.BalanceRateMultiplier),
		})
	}
	result.HoldAmountUSD = subscriptionAmount + balanceAmount
	result.EstimatedAmountUSD = result.HoldAmountUSD
	if cmd.PricingSnapshotVersion >= 2 && cmd.BaseAmountUSD > 0 {
		captureCommand := *cmd
		captureCommand.BalanceHoldAmount = balanceAmount
		captureCommand.SubscriptionHoldAllocations = allocations
		captureCommand.ActualBaseAmountUSD = cmd.BaseAmountUSD
		plan, planErr := service.PlanBatchImageBillingCapture(&captureCommand)
		if planErr != nil {
			return nil, planErr
		}
		result.EstimatedAmountUSD = plan.ActualAmountUSD
	}
	// 第二版指纹不依赖最终预占金额，可以在 claim 后把额度口径改为真实混合预占金额。
	cmd.HoldAmount = result.HoldAmountUSD
	if err := persistBatchImageBillingHold(ctx, tx, cmd.BatchID, balanceAmount, allocations, result.HoldAmountUSD, result.EstimatedAmountUSD); err != nil {
		return nil, err
	}
	return result, nil
}

// captureUsageBillingBatchImageBilling 保留实际费用，并释放未使用的订阅与余额预占。
func captureUsageBillingBatchImageBilling(ctx context.Context, tx *sql.Tx, cmd *service.BatchImageBalanceHoldCommand) (*service.BatchImageBalanceHoldResult, error) {
	// 新任务必须证明预占已提交；升级前已冻结但没有 dedup 记录的旧任务仍允许安全消费冻结额。
	if cmd != nil && cmd.HoldAmount > 0 && (cmd.AllowanceReserved || cmd.BalanceHoldAmount > 0 || len(cmd.SubscriptionHoldAllocations) > 0) {
		held, err := batchImageHoldClaimExists(ctx, tx, service.BatchImageHoldRequestID(cmd.BatchID), cmd.APIKeyID)
		if err != nil {
			return nil, err
		}
		if !held {
			return nil, errors.New("batch image billing hold is missing")
		}
	}
	plan, err := service.PlanBatchImageBillingCapture(cmd)
	if err != nil {
		return nil, err
	}
	cmd.ActualAmount = plan.ActualAmountUSD
	if err := releaseBatchImageSubscriptionAllocations(ctx, tx, cmd, plan.SubscriptionReleases); err != nil {
		return nil, err
	}

	balanceCommand := *cmd
	balanceCommand.HoldAmount = plan.BalanceHoldAmount
	balanceCommand.ActualAmount = plan.BalanceAmountUSD
	balanceResult, err := captureUsageBillingBatchImageBalance(ctx, tx, &balanceCommand)
	if err != nil {
		return nil, err
	}
	if balanceResult == nil {
		balanceResult = &service.BatchImageBalanceHoldResult{}
	}
	balanceResult.SubscriptionAmountUSD = plan.SubscriptionAmountUSD
	balanceResult.BalanceAmountUSD = plan.BalanceAmountUSD
	balanceResult.HoldAmountUSD = service.TotalBatchImageHoldAmount(cmd)
	balanceResult.EstimatedAmountUSD = plan.ActualAmountUSD
	balanceResult.ActualAmountUSD = plan.ActualAmountUSD
	balanceResult.BillingAllocations = plan.BillingAllocations
	return balanceResult, nil
}

// releaseUsageBillingBatchImageBilling 释放失败或取消任务的全部订阅与余额预占。
func releaseUsageBillingBatchImageBilling(ctx context.Context, tx *sql.Tx, cmd *service.BatchImageBalanceHoldCommand) (*service.BatchImageBalanceHoldResult, error) {
	if cmd == nil {
		return &service.BatchImageBalanceHoldResult{}, nil
	}
	held, err := batchImageHoldClaimExists(ctx, tx, service.BatchImageHoldRequestID(cmd.BatchID), cmd.APIKeyID)
	if err != nil {
		return nil, err
	}
	if !held {
		logger.LegacyPrintf("repository.usage_billing", "[BatchImage] release skipped, hold was never reserved: batch=%s", cmd.BatchID)
		return &service.BatchImageBalanceHoldResult{}, nil
	}
	if err := releaseBatchImageSubscriptionAllocations(ctx, tx, cmd, cmd.SubscriptionHoldAllocations); err != nil {
		return nil, err
	}
	balanceAmount := service.EffectiveBatchImageBalanceHoldAmount(cmd)
	return releaseUsageBillingBatchImageFrozenBalance(ctx, tx, cmd.UserID, balanceAmount)
}

func persistBatchImageBillingHold(ctx context.Context, tx *sql.Tx, batchID string, balanceAmount float64, allocations []domain.BillingAllocation, holdAmount, estimatedAmount float64) error {
	encoded, err := json.Marshal(allocations)
	if err != nil {
		return err
	}
	result, err := tx.ExecContext(ctx, `
		UPDATE batch_image_jobs
		SET balance_hold_amount = $2,
			subscription_hold_allocations = $3::jsonb,
			hold_amount = $4,
			estimated_cost = $5,
			updated_at = NOW()
		WHERE batch_id = $1
	`, batchID, balanceAmount, string(encoded), holdAmount, estimatedAmount)
	if err != nil {
		return err
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if affected == 0 {
		return service.ErrBatchImageJobNotFound
	}
	return nil
}

// releaseBatchImageSubscriptionAllocations 仅回退仍处于原预占窗口内的订阅用量。
func releaseBatchImageSubscriptionAllocations(ctx context.Context, tx *sql.Tx, cmd *service.BatchImageBalanceHoldCommand, allocations []domain.BillingAllocation) error {
	if cmd == nil {
		return nil
	}
	for _, allocation := range allocations {
		if allocation.Type != domain.BillingAllocationTypeSubscription || allocation.SubscriptionID == nil || allocation.AmountUSD <= 0 {
			continue
		}
		result, err := tx.ExecContext(ctx, `
			UPDATE user_subscriptions
			SET daily_usage_usd = CASE
					WHEN daily_limit_usd IS NOT NULL AND daily_limit_usd > 0
						AND daily_window_start <= $4 AND $4 < daily_window_start + INTERVAL '24 hours'
					THEN GREATEST(0, daily_usage_usd - $1) ELSE daily_usage_usd END,
				weekly_usage_usd = CASE
					WHEN weekly_limit_usd IS NOT NULL AND weekly_limit_usd > 0
						AND weekly_window_start <= $4 AND $4 < weekly_window_start + INTERVAL '7 days'
					THEN GREATEST(0, weekly_usage_usd - $1) ELSE weekly_usage_usd END,
				monthly_usage_usd = CASE
					WHEN monthly_limit_usd IS NOT NULL AND monthly_limit_usd > 0
						AND monthly_window_start <= $4 AND $4 < monthly_window_start + INTERVAL '30 days'
					THEN GREATEST(0, monthly_usage_usd - $1) ELSE monthly_usage_usd END,
				updated_at = NOW()
			WHERE id = $2 AND user_id = $3
		`, allocation.AmountUSD, *allocation.SubscriptionID, cmd.UserID, cmd.ReservedAt)
		if err != nil {
			return err
		}
		affected, err := result.RowsAffected()
		if err != nil {
			return err
		}
		if affected == 0 {
			return service.ErrSubscriptionNotFound
		}
	}
	return nil
}

func batchImageBillingResultForCommand(cmd *service.BatchImageBalanceHoldCommand, operation batchImageAllowanceOperation) *service.BatchImageBalanceHoldResult {
	result := &service.BatchImageBalanceHoldResult{Applied: false}
	if cmd == nil {
		return result
	}
	if operation == batchImageAllowanceCapture {
		plan, err := service.PlanBatchImageBillingCapture(cmd)
		if err != nil {
			return result
		}
		result.SubscriptionAmountUSD = plan.SubscriptionAmountUSD
		result.BalanceAmountUSD = plan.BalanceAmountUSD
		result.HoldAmountUSD = service.TotalBatchImageHoldAmount(cmd)
		result.EstimatedAmountUSD = plan.ActualAmountUSD
		result.ActualAmountUSD = plan.ActualAmountUSD
		result.BillingAllocations = plan.BillingAllocations
		return result
	}
	if operation != batchImageAllowanceReserve {
		return result
	}
	for _, allocation := range cmd.SubscriptionHoldAllocations {
		if allocation.Type == domain.BillingAllocationTypeSubscription && allocation.AmountUSD > 0 {
			result.SubscriptionAmountUSD += allocation.AmountUSD
			result.BillingAllocations = append(result.BillingAllocations, allocation)
		}
	}
	result.BalanceAmountUSD = service.EffectiveBatchImageBalanceHoldAmount(cmd)
	if result.BalanceAmountUSD > 0 {
		result.BillingAllocations = append(result.BillingAllocations, domain.BillingAllocation{
			Type:      domain.BillingAllocationTypeBalance,
			AmountUSD: result.BalanceAmountUSD,
		})
	}
	result.HoldAmountUSD = result.SubscriptionAmountUSD + result.BalanceAmountUSD
	result.EstimatedAmountUSD = result.HoldAmountUSD
	if cmd.PricingSnapshotVersion >= 2 && cmd.BaseAmountUSD > 0 {
		captureCommand := *cmd
		captureCommand.ActualBaseAmountUSD = cmd.BaseAmountUSD
		if plan, err := service.PlanBatchImageBillingCapture(&captureCommand); err == nil {
			result.EstimatedAmountUSD = plan.ActualAmountUSD
		}
	}
	return result
}

func reserveUsageBillingBatchImageBalance(ctx context.Context, tx *sql.Tx, cmd *service.BatchImageBalanceHoldCommand) (*service.BatchImageBalanceHoldResult, error) {
	if cmd.HoldAmount <= 0 {
		return &service.BatchImageBalanceHoldResult{}, nil
	}
	var balance, frozen float64
	err := tx.QueryRowContext(ctx, `
		UPDATE users
		SET balance = balance - $1,
			frozen_balance = COALESCE(frozen_balance, 0) + $1,
			updated_at = NOW()
		WHERE id = $2 AND deleted_at IS NULL AND balance >= $1
		RETURNING balance, frozen_balance
	`, cmd.HoldAmount, cmd.UserID).Scan(&balance, &frozen)
	if err == nil {
		return &service.BatchImageBalanceHoldResult{NewBalance: &balance, FrozenBalance: &frozen}, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return nil, err
	}
	if exists, existsErr := userExistsForBilling(ctx, tx, cmd.UserID); existsErr != nil {
		return nil, existsErr
	} else if !exists {
		return nil, service.ErrUserNotFound
	}
	return nil, service.ErrBatchImageInsufficientBalance
}

func captureUsageBillingBatchImageBalance(ctx context.Context, tx *sql.Tx, cmd *service.BatchImageBalanceHoldCommand) (*service.BatchImageBalanceHoldResult, error) {
	holdAmount := normalizeBatchImageBalanceAmount(cmd.HoldAmount)
	actualAmount := normalizeBatchImageBalanceAmount(cmd.ActualAmount)
	if holdAmount <= 0 && actualAmount <= 0 {
		return &service.BatchImageBalanceHoldResult{}, nil
	}
	if actualAmount-holdAmount > 0.00000001 {
		return nil, service.ErrBatchImageSettlementCostExceedsHold
	}
	var balance, frozen float64
	err := tx.QueryRowContext(ctx, `
		UPDATE users
		SET balance = balance
				+ CASE WHEN $1 > $2 THEN $1 - $2 ELSE 0 END
				- CASE WHEN $2 > $1 THEN $2 - $1 ELSE 0 END,
			frozen_balance = COALESCE(frozen_balance, 0) - $1,
			updated_at = NOW()
		WHERE id = $3 AND deleted_at IS NULL AND COALESCE(frozen_balance, 0) >= $1
		RETURNING balance, frozen_balance
	`, holdAmount, actualAmount, cmd.UserID).Scan(&balance, &frozen)
	if err == nil {
		return &service.BatchImageBalanceHoldResult{NewBalance: &balance, FrozenBalance: &frozen}, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return nil, err
	}
	if exists, existsErr := userExistsForBilling(ctx, tx, cmd.UserID); existsErr != nil {
		return nil, existsErr
	} else if !exists {
		return nil, service.ErrUserNotFound
	}
	return nil, errors.New("batch image frozen balance is insufficient")
}

func releaseUsageBillingBatchImageFrozenBalance(ctx context.Context, tx *sql.Tx, userID int64, amount float64) (*service.BatchImageBalanceHoldResult, error) {
	amount = normalizeBatchImageBalanceAmount(amount)
	if amount <= 0 {
		return &service.BatchImageBalanceHoldResult{}, nil
	}
	var balance, frozen float64
	err := tx.QueryRowContext(ctx, `
		UPDATE users
		SET balance = balance + $1,
			frozen_balance = COALESCE(frozen_balance, 0) - $1,
			updated_at = NOW()
		WHERE id = $2 AND deleted_at IS NULL AND COALESCE(frozen_balance, 0) >= $1
		RETURNING balance, frozen_balance
	`, amount, userID).Scan(&balance, &frozen)
	if err == nil {
		return &service.BatchImageBalanceHoldResult{NewBalance: &balance, FrozenBalance: &frozen}, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return nil, err
	}
	if exists, existsErr := userExistsForBilling(ctx, tx, userID); existsErr != nil {
		return nil, existsErr
	} else if !exists {
		return nil, service.ErrUserNotFound
	}
	return nil, errors.New("batch image frozen balance is insufficient")
}

// normalizeBatchImageBalanceAmount 对齐 users.balance 的 DECIMAL(20,8) 精度，避免浮点尾差导致冻结额比较失败。
func normalizeBatchImageBalanceAmount(amount float64) float64 {
	return math.Round(amount*1e8) / 1e8
}

// batchImageHoldClaimExists 检查 hold request id 是否已在 dedup（或归档）表中被 claim，
// 即该 batch 的冻结操作确实成功提交过。
func batchImageHoldClaimExists(ctx context.Context, tx *sql.Tx, holdRequestID string, apiKeyID int64) (bool, error) {
	var exists int
	err := tx.QueryRowContext(ctx, `
		SELECT 1
		FROM usage_billing_dedup
		WHERE request_id = $1 AND api_key_id = $2
	`, holdRequestID, apiKeyID).Scan(&exists)
	if err == nil {
		return true, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return false, err
	}
	err = tx.QueryRowContext(ctx, `
		SELECT 1
		FROM usage_billing_dedup_archive
		WHERE request_id = $1 AND api_key_id = $2
	`, holdRequestID, apiKeyID).Scan(&exists)
	if err == nil {
		return true, nil
	}
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	return false, err
}

func userExistsForBilling(ctx context.Context, tx *sql.Tx, userID int64) (bool, error) {
	var exists int
	err := tx.QueryRowContext(ctx, `
		SELECT 1
		FROM users
		WHERE id = $1 AND deleted_at IS NULL
	`, userID).Scan(&exists)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return true, nil
}

func incrementUsageBillingAPIKeyQuota(ctx context.Context, tx *sql.Tx, apiKeyID int64, amount float64) (bool, error) {
	// 配额列与余额列共享 8 位金额刻度，派生金额也必须在 SQL 前量化。
	amount = service.QuantizeUsageBillingAmount(amount)
	var exhausted bool
	err := tx.QueryRowContext(ctx, `
		UPDATE api_keys
		SET quota_used = quota_used + $1,
			status = CASE
				WHEN quota > 0
					AND status = $3
					AND quota_used < quota
					AND quota_used + $1 >= quota
				THEN $4
				ELSE status
			END,
			updated_at = NOW()
		WHERE id = $2 AND deleted_at IS NULL
		RETURNING quota > 0 AND quota_used >= quota AND quota_used - $1 < quota
	`, amount, apiKeyID, service.StatusAPIKeyActive, service.StatusAPIKeyQuotaExhausted).Scan(&exhausted)
	if errors.Is(err, sql.ErrNoRows) {
		return false, service.ErrAPIKeyNotFound
	}
	if err != nil {
		return false, err
	}
	return exhausted, nil
}

func incrementUsageBillingAPIKeyRateLimit(ctx context.Context, tx *sql.Tx, apiKeyID int64, cost float64) error {
	cost = service.QuantizeUsageBillingAmount(cost)
	res, err := tx.ExecContext(ctx, `
		UPDATE api_keys SET
			usage_5h = CASE WHEN window_5h_start IS NOT NULL AND window_5h_start + INTERVAL '5 hours' <= NOW() THEN $1 ELSE usage_5h + $1 END,
			usage_1d = CASE WHEN window_1d_start IS NOT NULL AND window_1d_start + INTERVAL '24 hours' <= NOW() THEN $1 ELSE usage_1d + $1 END,
			usage_7d = CASE WHEN window_7d_start IS NOT NULL AND window_7d_start + INTERVAL '7 days' <= NOW() THEN $1 ELSE usage_7d + $1 END,
			window_5h_start = CASE WHEN window_5h_start IS NULL OR window_5h_start + INTERVAL '5 hours' <= NOW() THEN NOW() ELSE window_5h_start END,
			window_1d_start = CASE WHEN window_1d_start IS NULL OR window_1d_start + INTERVAL '24 hours' <= NOW() THEN date_trunc('day', NOW()) ELSE window_1d_start END,
			window_7d_start = CASE WHEN window_7d_start IS NULL OR window_7d_start + INTERVAL '7 days' <= NOW() THEN date_trunc('day', NOW()) ELSE window_7d_start END,
			updated_at = NOW()
		WHERE id = $2 AND deleted_at IS NULL
	`, cost, apiKeyID)
	if err != nil {
		return err
	}
	affected, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if affected == 0 {
		return service.ErrAPIKeyNotFound
	}
	return nil
}

func incrementUsageBillingAccountQuota(ctx context.Context, tx *sql.Tx, accountID int64, amount float64) (*service.AccountQuotaState, error) {
	rows, err := tx.QueryContext(ctx,
		`UPDATE accounts SET extra = (
			COALESCE(extra, '{}'::jsonb)
			|| jsonb_build_object('quota_used', COALESCE((extra->>'quota_used')::numeric, 0) + $1)
			|| CASE WHEN COALESCE((extra->>'quota_daily_limit')::numeric, 0) > 0 THEN
				jsonb_build_object(
					'quota_daily_used',
					CASE WHEN `+dailyExpiredExpr+`
					THEN $1
					ELSE COALESCE((extra->>'quota_daily_used')::numeric, 0) + $1 END,
					'quota_daily_start',
					CASE WHEN `+dailyExpiredExpr+`
					THEN `+nowUTC+`
					ELSE COALESCE(extra->>'quota_daily_start', `+nowUTC+`) END
				)
				|| CASE WHEN `+dailyExpiredExpr+` AND `+nextDailyResetAtExpr+` IS NOT NULL
				   THEN jsonb_build_object('quota_daily_reset_at', `+nextDailyResetAtExpr+`)
				   ELSE '{}'::jsonb END
			ELSE '{}'::jsonb END
			|| CASE WHEN COALESCE((extra->>'quota_weekly_limit')::numeric, 0) > 0 THEN
				jsonb_build_object(
					'quota_weekly_used',
					CASE WHEN `+weeklyExpiredExpr+`
					THEN $1
					ELSE COALESCE((extra->>'quota_weekly_used')::numeric, 0) + $1 END,
					'quota_weekly_start',
					CASE WHEN `+weeklyExpiredExpr+`
					THEN `+nowUTC+`
					ELSE COALESCE(extra->>'quota_weekly_start', `+nowUTC+`) END
				)
				|| CASE WHEN `+weeklyExpiredExpr+` AND `+nextWeeklyResetAtExpr+` IS NOT NULL
				   THEN jsonb_build_object('quota_weekly_reset_at', `+nextWeeklyResetAtExpr+`)
				   ELSE '{}'::jsonb END
			ELSE '{}'::jsonb END
		), updated_at = NOW()
		WHERE id = $2 AND deleted_at IS NULL
		RETURNING
			COALESCE((extra->>'quota_used')::numeric, 0),
			COALESCE((extra->>'quota_limit')::numeric, 0),
			COALESCE((extra->>'quota_daily_used')::numeric, 0),
			COALESCE((extra->>'quota_daily_limit')::numeric, 0),
			COALESCE((extra->>'quota_weekly_used')::numeric, 0),
			COALESCE((extra->>'quota_weekly_limit')::numeric, 0)`,
		amount, accountID)
	if err != nil {
		return nil, err
	}

	var state service.AccountQuotaState
	if rows.Next() {
		if err := rows.Scan(
			&state.TotalUsed, &state.TotalLimit,
			&state.DailyUsed, &state.DailyLimit,
			&state.WeeklyUsed, &state.WeeklyLimit,
		); err != nil {
			_ = rows.Close()
			return nil, err
		}
	} else {
		if err := rows.Err(); err != nil {
			_ = rows.Close()
			return nil, err
		}
		_ = rows.Close()
		return nil, service.ErrAccountNotFound
	}
	if err := rows.Err(); err != nil {
		_ = rows.Close()
		return nil, err
	}
	// 必须在执行下一条 SQL 前显式关闭 rows：pq 驱动在同一连接上
	// 不允许前一条查询的结果集未耗尽时启动新查询，否则会返回
	// "unexpected Parse response" 错误。
	if err := rows.Close(); err != nil {
		return nil, err
	}
	// 任意维度额度在本次递增中从"未超"跨越到"已超"时，必须刷新调度快照，
	// 否则 Redis 中缓存的 Account 仍显示旧的 used 值，后续请求会继续选中本账号，
	// 最终观察到 daily_used / weekly_used 大幅超过配置的 limit。
	// 对于日/周额度，即使本次触发了周期重置（pre=0、post=amount），
	// 判定式 (post-amount) < limit 同样成立，逻辑与总额度保持一致。
	crossedTotal := state.TotalLimit > 0 && state.TotalUsed >= state.TotalLimit && (state.TotalUsed-amount) < state.TotalLimit
	crossedDaily := state.DailyLimit > 0 && state.DailyUsed >= state.DailyLimit && (state.DailyUsed-amount) < state.DailyLimit
	crossedWeekly := state.WeeklyLimit > 0 && state.WeeklyUsed >= state.WeeklyLimit && (state.WeeklyUsed-amount) < state.WeeklyLimit
	if crossedTotal || crossedDaily || crossedWeekly {
		if err := enqueueSchedulerOutbox(ctx, tx, service.SchedulerOutboxEventAccountChanged, &accountID, nil, nil); err != nil {
			logger.LegacyPrintf("repository.usage_billing", "[SchedulerOutbox] enqueue quota exceeded failed: account=%d err=%v", accountID, err)
			return nil, err
		}
	}
	return &state, nil
}
