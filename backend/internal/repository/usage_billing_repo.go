package repository

import (
	"context"
	"database/sql"
	"errors"
	"math"
	"strings"
	"time"

	dbent "github.com/TokenFlux/TokenRouter/ent"
	"github.com/TokenFlux/TokenRouter/internal/domain"
	"github.com/TokenFlux/TokenRouter/internal/pkg/logger"
	"github.com/TokenFlux/TokenRouter/internal/service"
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

func (r *usageBillingRepository) claimUsageBillingKey(ctx context.Context, tx *sql.Tx, cmd *service.UsageBillingCommand) (bool, error) {
	var id int64
	err := tx.QueryRowContext(ctx, `
		INSERT INTO usage_billing_dedup (request_id, api_key_id, request_fingerprint)
		VALUES ($1, $2, $3)
		ON CONFLICT (request_id, api_key_id) DO NOTHING
		RETURNING id
	`, cmd.RequestID, cmd.APIKeyID, cmd.RequestFingerprint).Scan(&id)
	if errors.Is(err, sql.ErrNoRows) {
		var existingFingerprint string
		if err := tx.QueryRowContext(ctx, `
			SELECT request_fingerprint
			FROM usage_billing_dedup
			WHERE request_id = $1 AND api_key_id = $2
		`, cmd.RequestID, cmd.APIKeyID).Scan(&existingFingerprint); err != nil {
			return false, err
		}
		if strings.TrimSpace(existingFingerprint) != strings.TrimSpace(cmd.RequestFingerprint) {
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
	`, cmd.RequestID, cmd.APIKeyID).Scan(&archivedFingerprint)
	if err == nil {
		if strings.TrimSpace(archivedFingerprint) != strings.TrimSpace(cmd.RequestFingerprint) {
			return false, service.ErrUsageBillingRequestConflict
		}
		return false, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return false, err
	}
	return true, nil
}

func (r *usageBillingRepository) applyUsageBillingEffects(ctx context.Context, tx *sql.Tx, cmd *service.UsageBillingCommand, result *service.UsageBillingApplyResult) error {
	remainingAmount, allocations, err := allocateUsageBillingSubscriptions(ctx, tx, cmd.UserID, cmd.BillableAmountUSD)
	if err != nil {
		return err
	}
	result.BillingAllocations = allocations
	result.SubscriptionAmountUSD = cmd.BillableAmountUSD - remainingAmount

	if remainingAmount > 0 {
		newBalance, err := deductUsageBillingBalance(ctx, tx, cmd.UserID, remainingAmount)
		if err != nil {
			return err
		}
		result.NewBalance = &newBalance
		result.BalanceAmountUSD = remainingAmount
		result.BillingAllocations = append(result.BillingAllocations, domain.BillingAllocation{
			Type:      domain.BillingAllocationTypeBalance,
			AmountUSD: remainingAmount,
		})
	}

	if cmd.APIKeyQuotaCost > 0 {
		exhausted, err := incrementUsageBillingAPIKeyQuota(ctx, tx, cmd.APIKeyID, cmd.APIKeyQuotaCost)
		if err != nil {
			return err
		}
		result.APIKeyQuotaExhausted = exhausted
	}

	if cmd.APIKeyRateLimitCost > 0 {
		if err := incrementUsageBillingAPIKeyRateLimit(ctx, tx, cmd.APIKeyID, cmd.APIKeyRateLimitCost); err != nil {
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

type usageBillingSubscriptionRow struct {
	ID                 int64
	PlanID             int64
	DailyWindowStart   sql.NullTime
	WeeklyWindowStart  sql.NullTime
	MonthlyWindowStart sql.NullTime
	DailyLimitUSD      sql.NullFloat64
	WeeklyLimitUSD     sql.NullFloat64
	MonthlyLimitUSD    sql.NullFloat64
	DailyUsageUSD      float64
	WeeklyUsageUSD     float64
	MonthlyUsageUSD    float64
}

func allocateUsageBillingSubscriptions(ctx context.Context, tx *sql.Tx, userID int64, amountUSD float64) (float64, []domain.BillingAllocation, error) {
	if amountUSD <= 0 {
		return 0, nil, nil
	}

	rows, err := tx.QueryContext(ctx, `
		SELECT
			id,
			plan_id,
			daily_window_start,
			weekly_window_start,
			monthly_window_start,
			daily_limit_usd,
			weekly_limit_usd,
			monthly_limit_usd,
			daily_usage_usd,
			weekly_usage_usd,
			monthly_usage_usd
		FROM user_subscriptions
		WHERE user_id = $1
			AND deleted_at IS NULL
			AND starts_at <= NOW()
			AND expires_at > NOW()
			AND status IN ($2, $3)
		ORDER BY expires_at ASC, starts_at ASC, id ASC
		FOR UPDATE
	`, userID, service.SubscriptionStatusActive, service.SubscriptionStatusPending)
	if err != nil {
		return 0, nil, err
	}
	defer func() { _ = rows.Close() }()

	now := time.Now()
	windowStart := startOfDay(now)
	remaining := amountUSD
	allocations := make([]domain.BillingAllocation, 0)

	for rows.Next() {
		var row usageBillingSubscriptionRow
		if err := rows.Scan(
			&row.ID,
			&row.PlanID,
			&row.DailyWindowStart,
			&row.WeeklyWindowStart,
			&row.MonthlyWindowStart,
			&row.DailyLimitUSD,
			&row.WeeklyLimitUSD,
			&row.MonthlyLimitUSD,
			&row.DailyUsageUSD,
			&row.WeeklyUsageUSD,
			&row.MonthlyUsageUSD,
		); err != nil {
			return 0, nil, err
		}
		if remaining <= 0 {
			continue
		}

		dailyStart, dailyUsage := normalizeUsageBillingWindow(row.DailyWindowStart, row.DailyLimitUSD, row.DailyUsageUSD, windowStart, 24*time.Hour, now)
		weeklyStart, weeklyUsage := normalizeUsageBillingWindow(row.WeeklyWindowStart, row.WeeklyLimitUSD, row.WeeklyUsageUSD, windowStart, 7*24*time.Hour, now)
		monthlyStart, monthlyUsage := normalizeUsageBillingWindow(row.MonthlyWindowStart, row.MonthlyLimitUSD, row.MonthlyUsageUSD, windowStart, 30*24*time.Hour, now)

		available := minUsageBillingRemaining(
			windowRemaining(row.DailyLimitUSD, dailyUsage),
			windowRemaining(row.WeeklyLimitUSD, weeklyUsage),
			windowRemaining(row.MonthlyLimitUSD, monthlyUsage),
		)
		if available <= 0 {
			continue
		}

		allocated := math.Min(remaining, available)
		if allocated <= 0 {
			continue
		}

		if row.DailyLimitUSD.Valid && row.DailyLimitUSD.Float64 > 0 {
			dailyUsage += allocated
		}
		if row.WeeklyLimitUSD.Valid && row.WeeklyLimitUSD.Float64 > 0 {
			weeklyUsage += allocated
		}
		if row.MonthlyLimitUSD.Valid && row.MonthlyLimitUSD.Float64 > 0 {
			monthlyUsage += allocated
		}

		if err := updateUsageBillingSubscription(ctx, tx, row.ID, dailyStart, weeklyStart, monthlyStart, dailyUsage, weeklyUsage, monthlyUsage); err != nil {
			return 0, nil, err
		}

		subscriptionID := row.ID
		planID := row.PlanID
		allocations = append(allocations, domain.BillingAllocation{
			Type:           domain.BillingAllocationTypeSubscription,
			AmountUSD:      allocated,
			SubscriptionID: &subscriptionID,
			PlanID:         &planID,
		})
		remaining -= allocated
	}
	if err := rows.Err(); err != nil {
		return 0, nil, err
	}

	return remaining, allocations, nil
}

func normalizeUsageBillingWindow(windowStart sql.NullTime, limit sql.NullFloat64, used float64, resetStart time.Time, duration time.Duration, now time.Time) (*time.Time, float64) {
	if !limit.Valid || limit.Float64 <= 0 {
		if !windowStart.Valid {
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

func minUsageBillingRemaining(values ...*float64) float64 {
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
		return 0
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

func deductUsageBillingBalance(ctx context.Context, tx *sql.Tx, userID int64, amount float64) (float64, error) {
	var newBalance float64
	err := tx.QueryRowContext(ctx, `
		UPDATE users
		SET balance = balance - $1,
			updated_at = NOW()
		WHERE id = $2 AND deleted_at IS NULL
		RETURNING balance
	`, amount, userID).Scan(&newBalance)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, service.ErrUserNotFound
	}
	if err != nil {
		return 0, err
	}
	return newBalance, nil
}

func incrementUsageBillingAPIKeyQuota(ctx context.Context, tx *sql.Tx, apiKeyID int64, amount float64) (bool, error) {
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
	defer func() { _ = rows.Close() }()

	var state service.AccountQuotaState
	if rows.Next() {
		if err := rows.Scan(
			&state.TotalUsed, &state.TotalLimit,
			&state.DailyUsed, &state.DailyLimit,
			&state.WeeklyUsed, &state.WeeklyLimit,
		); err != nil {
			return nil, err
		}
	} else {
		if err := rows.Err(); err != nil {
			return nil, err
		}
		return nil, service.ErrAccountNotFound
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if state.TotalLimit > 0 && state.TotalUsed >= state.TotalLimit && (state.TotalUsed-amount) < state.TotalLimit {
		if err := enqueueSchedulerOutbox(ctx, tx, service.SchedulerOutboxEventAccountChanged, &accountID, nil, nil); err != nil {
			logger.LegacyPrintf("repository.usage_billing", "[SchedulerOutbox] enqueue quota exceeded failed: account=%d err=%v", accountID, err)
			return nil, err
		}
	}
	return &state, nil
}
