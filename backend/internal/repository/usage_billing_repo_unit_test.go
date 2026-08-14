//go:build unit

package repository

import (
	"context"
	"database/sql"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/stretchr/testify/require"

	"github.com/BrandonVee/TokenRouter/internal/service"
)

const (
	reserveBatchImageHoldSQL = `(?s)UPDATE users\s+SET balance = balance - \$1,\s+frozen_balance = COALESCE\(frozen_balance, 0\) \+ \$1,\s+updated_at = NOW\(\)\s+WHERE id = \$2 AND deleted_at IS NULL AND balance >= \$1\s+RETURNING balance, frozen_balance`
	captureBatchImageHoldSQL = `(?s)UPDATE users\s+SET balance = balance\s+\+ CASE WHEN \$1 > \$2 THEN \$1 - \$2 ELSE 0 END\s+- CASE WHEN \$2 > \$1 THEN \$2 - \$1 ELSE 0 END,\s+frozen_balance = COALESCE\(frozen_balance, 0\) - \$1,\s+updated_at = NOW\(\)\s+WHERE id = \$3 AND deleted_at IS NULL AND COALESCE\(frozen_balance, 0\) >= \$1\s+RETURNING balance, frozen_balance`
	releaseBatchImageHoldSQL = `(?s)UPDATE users\s+SET balance = balance \+ \$1,\s+frozen_balance = COALESCE\(frozen_balance, 0\) - \$1,\s+updated_at = NOW\(\)\s+WHERE id = \$2 AND deleted_at IS NULL AND COALESCE\(frozen_balance, 0\) >= \$1\s+RETURNING balance, frozen_balance`
	userExistsForBillingSQL  = `(?s)SELECT 1\s+FROM users\s+WHERE id = \$1 AND deleted_at IS NULL`
)

func TestReserveUsageBillingBatchImageBalance_MovesAvailableToFrozen(t *testing.T) {
	ctx := context.Background()
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer func() { _ = db.Close() }()

	mock.ExpectBegin()
	tx, err := db.BeginTx(ctx, nil)
	require.NoError(t, err)
	mock.ExpectQuery(reserveBatchImageHoldSQL).
		WithArgs(2.5, int64(42)).
		WillReturnRows(sqlmock.NewRows([]string{"balance", "frozen_balance"}).AddRow(7.5, 2.5))
	mock.ExpectCommit()

	result, err := reserveUsageBillingBatchImageBalance(ctx, tx, &service.BatchImageBalanceHoldCommand{UserID: 42, HoldAmount: 2.5})
	require.NoError(t, err)
	require.NotNil(t, result.NewBalance)
	require.NotNil(t, result.FrozenBalance)
	require.InDelta(t, 7.5, *result.NewBalance, 0.000001)
	require.InDelta(t, 2.5, *result.FrozenBalance, 0.000001)
	require.NoError(t, tx.Commit())
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestReserveUsageBillingBatchImageBalance_InsufficientBalance(t *testing.T) {
	ctx := context.Background()
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer func() { _ = db.Close() }()

	mock.ExpectBegin()
	tx, err := db.BeginTx(ctx, nil)
	require.NoError(t, err)
	mock.ExpectQuery(reserveBatchImageHoldSQL).
		WithArgs(10.0, int64(42)).
		WillReturnError(sql.ErrNoRows)
	mock.ExpectQuery(userExistsForBillingSQL).
		WithArgs(int64(42)).
		WillReturnRows(sqlmock.NewRows([]string{"?column?"}).AddRow(1))
	mock.ExpectRollback()

	_, err = reserveUsageBillingBatchImageBalance(ctx, tx, &service.BatchImageBalanceHoldCommand{UserID: 42, HoldAmount: 10})
	require.ErrorIs(t, err, service.ErrBatchImageInsufficientBalance)
	require.NoError(t, tx.Rollback())
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestReserveUsageBillingBatchImageBilling_UsesBalanceRateAfterPartialSubscription(t *testing.T) {
	ctx := context.Background()
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer func() { _ = db.Close() }()

	now := time.Now().UTC()
	windowStart := now.Add(-time.Hour)
	mock.ExpectBegin()
	tx, err := db.BeginTx(ctx, nil)
	require.NoError(t, err)
	mock.ExpectQuery(`(?s)SELECT\s+id,\s+plan_id,.*FROM user_subscriptions.*FOR UPDATE`).
		WithArgs(int64(42), service.SubscriptionStatusActive, service.SubscriptionStatusPending, nil, int64(7)).
		WillReturnRows(sqlmock.NewRows([]string{
			"id", "plan_id", "starts_at", "expires_at",
			"daily_window_start", "weekly_window_start", "monthly_window_start",
			"daily_limit_usd", "weekly_limit_usd", "monthly_limit_usd",
			"daily_usage_usd", "weekly_usage_usd", "monthly_usage_usd", "group_rates",
		}).AddRow(
			int64(11), int64(22), now.Add(-24*time.Hour), now.Add(30*24*time.Hour),
			windowStart, windowStart, windowStart,
			1.0, 1.0, 1.0,
			0.8, 0.8, 0.8, `{"7":0.5}`,
		))
	mock.ExpectExec(`(?s)UPDATE user_subscriptions\s+SET.*WHERE id = \$7`).
		WithArgs(sqlmock.AnyArg(), sqlmock.AnyArg(), sqlmock.AnyArg(), 1.0, 1.0, 1.0, int64(11)).
		WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectQuery(reserveBatchImageHoldSQL).
		WithArgs(sqlmock.AnyArg(), int64(42)).
		WillReturnRows(sqlmock.NewRows([]string{"balance", "frozen_balance"}).AddRow(3.8, 1.2))
	mock.ExpectExec(`(?s)UPDATE batch_image_jobs\s+SET balance_hold_amount = \$2,.*estimated_cost = \$5`).
		WithArgs("imgbatch_partial_rate", sqlmock.AnyArg(), sqlmock.AnyArg(), sqlmock.AnyArg(), sqlmock.AnyArg()).
		WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectCommit()

	groupID := int64(7)
	result, err := reserveUsageBillingBatchImageBilling(ctx, tx, &service.BatchImageBalanceHoldCommand{
		UserID:                          42,
		GroupID:                         &groupID,
		BatchID:                         "imgbatch_partial_rate",
		HoldAmount:                      0.5,
		PricingSnapshotVersion:          2,
		BaseAmountUSD:                   1,
		SubscriptionRateMultiplier:      1,
		SubscriptionRateMultiplierScale: 1,
		BalanceRateMultiplier:           2,
		SettlementRateScale:             0.5,
	})
	require.NoError(t, err)
	require.InDelta(t, 0.2, result.SubscriptionAmountUSD, 0.000001)
	require.InDelta(t, 1.2, result.BalanceAmountUSD, 0.000001)
	require.InDelta(t, 1.4, result.HoldAmountUSD, 0.000001)
	require.InDelta(t, 0.4, result.EstimatedAmountUSD, 0.000001)
	require.Len(t, result.BillingAllocations, 2)
	require.InDelta(t, 0.4, result.BillingAllocations[0].BaseAmountUSD, 0.000001)
	require.InDelta(t, 0.5, result.BillingAllocations[0].RateMultiplier, 0.000001)
	require.NoError(t, tx.Commit())
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestReserveUsageBillingBatchImageBilling_StrictSubscriptionRejectsPartialHold(t *testing.T) {
	ctx := context.Background()
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer func() { _ = db.Close() }()

	now := time.Now().UTC()
	windowStart := now.Add(-time.Hour)
	preferredID := int64(11)
	mock.ExpectBegin()
	tx, err := db.BeginTx(ctx, nil)
	require.NoError(t, err)
	mock.ExpectQuery(`(?s)SELECT\s+id,\s+plan_id,.*FROM user_subscriptions.*AND NOT EXISTS\s*\(\s*SELECT 1\s+FROM subscription_plan_groups.*FOR UPDATE`).
		WithArgs(int64(42), service.SubscriptionStatusActive, service.SubscriptionStatusPending, preferredID).
		WillReturnRows(sqlmock.NewRows([]string{
			"id", "plan_id", "starts_at", "expires_at",
			"daily_window_start", "weekly_window_start", "monthly_window_start",
			"daily_limit_usd", "weekly_limit_usd", "monthly_limit_usd",
			"daily_usage_usd", "weekly_usage_usd", "monthly_usage_usd", "group_rates",
		}).AddRow(
			preferredID, int64(22), now.Add(-24*time.Hour), now.Add(30*24*time.Hour),
			windowStart, windowStart, windowStart,
			1.0, 1.0, 1.0,
			0.8, 0.8, 0.8, `{}`,
		))
	// 批量任务尚未提交上游，只能预占剩余额度，不能沿用普通请求的溢出欠费结算语义。
	mock.ExpectExec(`(?s)UPDATE user_subscriptions\s+SET.*WHERE id = \$7`).
		WithArgs(sqlmock.AnyArg(), sqlmock.AnyArg(), sqlmock.AnyArg(), 1.0, 1.0, 1.0, preferredID).
		WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectRollback()

	_, err = reserveUsageBillingBatchImageBilling(ctx, tx, &service.BatchImageBalanceHoldCommand{
		UserID:                  42,
		BatchID:                 "imgbatch_strict_subscription",
		HoldAmount:              0.5,
		APIKeyBillingMode:       service.APIKeyBillingModeSubscription,
		PreferredSubscriptionID: &preferredID,
	})

	require.ErrorIs(t, err, service.ErrPreferredSubscriptionInsufficient)
	require.NoError(t, tx.Rollback())
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestApplyUsageBillingEffects_StrictSubscriptionChargesOverflowToBalance(t *testing.T) {
	ctx := context.Background()
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer func() { _ = db.Close() }()

	now := time.Now().UTC()
	windowStart := now.Add(-time.Hour)
	preferredID := int64(11)
	mock.ExpectBegin()
	tx, err := db.BeginTx(ctx, nil)
	require.NoError(t, err)
	mock.ExpectQuery(`(?s)SELECT\s+id,\s+plan_id,.*FROM user_subscriptions.*AND NOT EXISTS\s*\(\s*SELECT 1\s+FROM subscription_plan_groups.*FOR UPDATE`).
		WithArgs(int64(42), service.SubscriptionStatusActive, service.SubscriptionStatusPending, preferredID).
		WillReturnRows(sqlmock.NewRows([]string{
			"id", "plan_id", "starts_at", "expires_at",
			"daily_window_start", "weekly_window_start", "monthly_window_start",
			"daily_limit_usd", "weekly_limit_usd", "monthly_limit_usd",
			"daily_usage_usd", "weekly_usage_usd", "monthly_usage_usd", "group_rates",
		}).AddRow(
			preferredID, int64(22), now.Add(-24*time.Hour), now.Add(30*24*time.Hour),
			windowStart, windowStart, windowStart,
			1.0, 1.0, 1.0,
			0.8, 0.8, 0.8, `{}`,
		))
	// 指定订阅只扣到额度上限，剩余基础用量按余额倍率形成欠费。
	mock.ExpectExec(`(?s)UPDATE user_subscriptions\s+SET.*WHERE id = \$7`).
		WithArgs(sqlmock.AnyArg(), sqlmock.AnyArg(), sqlmock.AnyArg(), 1.0, 1.0, 1.0, preferredID).
		WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectQuery(`(?s)WITH locked_user AS \(.*SELECT updated.balance, \$1::numeric AS deducted_amount`).
		WithArgs(1.2, int64(42)).
		WillReturnRows(sqlmock.NewRows([]string{"balance", "deducted_amount"}).AddRow(-1.2, 1.2))
	mock.ExpectCommit()

	result := &service.UsageBillingApplyResult{}
	err = (&usageBillingRepository{}).applyUsageBillingEffects(ctx, tx, &service.UsageBillingCommand{
		UserID:                          42,
		BillableAmountUSD:               0.5,
		BaseAmountUSD:                   1,
		SubscriptionRateMultiplier:      0.5,
		SubscriptionRateMultiplierScale: 1,
		BalanceRateMultiplier:           2,
		APIKeyBillingMode:               service.APIKeyBillingModeSubscription,
		PreferredSubscriptionID:         &preferredID,
	}, result)

	require.NoError(t, err)
	require.InDelta(t, 0.2, result.SubscriptionAmountUSD, 0.000001)
	require.InDelta(t, 1.2, result.BalanceAmountUSD, 0.000001)
	require.NotNil(t, result.NewBalance)
	require.InDelta(t, -1.2, *result.NewBalance, 0.000001)
	require.Len(t, result.BillingAllocations, 2)
	require.InDelta(t, 0.2, result.BillingAllocations[0].AmountUSD, 0.000001)
	require.InDelta(t, 1.2, result.BillingAllocations[1].AmountUSD, 0.000001)
	require.NotNil(t, result.EffectiveRateMultiplier)
	require.InDelta(t, 1.4, *result.EffectiveRateMultiplier, 0.000001)
	require.NotNil(t, result.BillingAllocations[0].SubscriptionID)
	require.Equal(t, preferredID, *result.BillingAllocations[0].SubscriptionID)
	require.NoError(t, tx.Commit())
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestApplyUsageBillingEffects_StrictSubscriptionWithoutGroupFiltersRestrictedPlans(t *testing.T) {
	ctx := context.Background()
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer func() { _ = db.Close() }()

	preferredID := int64(11)
	mock.ExpectBegin()
	tx, err := db.BeginTx(ctx, nil)
	require.NoError(t, err)
	// 无最终分组的指定订阅只能选择没有套餐分组限制的计划；查询不能把受限套餐当作全局套餐。
	mock.ExpectQuery(`(?s)SELECT\s+id,\s+plan_id,.*FROM user_subscriptions.*AND NOT EXISTS\s*\(\s*SELECT 1\s+FROM subscription_plan_groups.*FOR UPDATE`).
		WithArgs(int64(42), service.SubscriptionStatusActive, service.SubscriptionStatusPending, preferredID).
		WillReturnRows(sqlmock.NewRows([]string{
			"id", "plan_id", "starts_at", "expires_at",
			"daily_window_start", "weekly_window_start", "monthly_window_start",
			"daily_limit_usd", "weekly_limit_usd", "monthly_limit_usd",
			"daily_usage_usd", "weekly_usage_usd", "monthly_usage_usd", "group_rates",
		}))
	mock.ExpectRollback()

	err = (&usageBillingRepository{}).applyUsageBillingEffects(ctx, tx, &service.UsageBillingCommand{
		UserID:                  42,
		BillableAmountUSD:       1,
		APIKeyBillingMode:       service.APIKeyBillingModeSubscription,
		PreferredSubscriptionID: &preferredID,
	}, &service.UsageBillingApplyResult{})

	require.ErrorIs(t, err, service.ErrPreferredSubscriptionInsufficient)
	require.NoError(t, tx.Rollback())
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestCaptureUsageBillingBatchImageBalance_ReleasesRemainder(t *testing.T) {
	ctx := context.Background()
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer func() { _ = db.Close() }()

	mock.ExpectBegin()
	tx, err := db.BeginTx(ctx, nil)
	require.NoError(t, err)
	mock.ExpectQuery(captureBatchImageHoldSQL).
		WithArgs(1.0, 0.25, int64(42)).
		WillReturnRows(sqlmock.NewRows([]string{"balance", "frozen_balance"}).AddRow(9.75, 0.0))
	mock.ExpectCommit()

	result, err := captureUsageBillingBatchImageBalance(ctx, tx, &service.BatchImageBalanceHoldCommand{UserID: 42, HoldAmount: 1, ActualAmount: 0.25})
	require.NoError(t, err)
	require.InDelta(t, 9.75, *result.NewBalance, 0.000001)
	require.InDelta(t, 0.0, *result.FrozenBalance, 0.000001)
	require.NoError(t, tx.Commit())
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestCaptureUsageBillingBatchImageBalance_RejectsActualCostOverHold(t *testing.T) {
	ctx := context.Background()
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer func() { _ = db.Close() }()

	mock.ExpectBegin()
	tx, err := db.BeginTx(ctx, nil)
	require.NoError(t, err)
	mock.ExpectRollback()

	_, err = captureUsageBillingBatchImageBalance(ctx, tx, &service.BatchImageBalanceHoldCommand{UserID: 42, HoldAmount: 0.5, ActualAmount: 1})
	require.ErrorIs(t, err, service.ErrBatchImageSettlementCostExceedsHold)
	require.NoError(t, tx.Rollback())
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestReleaseUsageBillingBatchImageBalance_ReturnsFrozenToAvailable(t *testing.T) {
	ctx := context.Background()
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer func() { _ = db.Close() }()

	mock.ExpectBegin()
	tx, err := db.BeginTx(ctx, nil)
	require.NoError(t, err)
	mock.ExpectQuery(`SELECT 1\s+FROM usage_billing_dedup\s+WHERE request_id = \$1 AND api_key_id = \$2`).
		WithArgs(service.BatchImageHoldRequestID("imgbatch_release"), int64(7)).
		WillReturnRows(sqlmock.NewRows([]string{"?column?"}).AddRow(1))
	mock.ExpectQuery(releaseBatchImageHoldSQL).
		WithArgs(1.0, int64(42)).
		WillReturnRows(sqlmock.NewRows([]string{"balance", "frozen_balance"}).AddRow(10.0, 0.0))
	mock.ExpectCommit()

	result, err := releaseUsageBillingBatchImageBilling(ctx, tx, &service.BatchImageBalanceHoldCommand{UserID: 42, APIKeyID: 7, BatchID: "imgbatch_release", HoldAmount: 1})
	require.NoError(t, err)
	require.InDelta(t, 10.0, *result.NewBalance, 0.000001)
	require.InDelta(t, 0.0, *result.FrozenBalance, 0.000001)
	require.NoError(t, tx.Commit())
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestReleaseUsageBillingBatchImageBalance_SkipsWhenHoldNeverReserved(t *testing.T) {
	ctx := context.Background()
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer func() { _ = db.Close() }()

	mock.ExpectBegin()
	tx, err := db.BeginTx(ctx, nil)
	require.NoError(t, err)
	// dedup 与归档表均无 hold claim：说明该 job 从未成功冻结，
	// 释放必须跳过，不得从他人冻结资金池中凭空生成余额。
	mock.ExpectQuery(`SELECT 1\s+FROM usage_billing_dedup\s+WHERE request_id = \$1 AND api_key_id = \$2`).
		WithArgs(service.BatchImageHoldRequestID("imgbatch_phantom"), int64(7)).
		WillReturnError(sql.ErrNoRows)
	mock.ExpectQuery(`SELECT 1\s+FROM usage_billing_dedup_archive\s+WHERE request_id = \$1 AND api_key_id = \$2`).
		WithArgs(service.BatchImageHoldRequestID("imgbatch_phantom"), int64(7)).
		WillReturnError(sql.ErrNoRows)
	mock.ExpectCommit()

	result, err := releaseUsageBillingBatchImageBilling(ctx, tx, &service.BatchImageBalanceHoldCommand{UserID: 42, APIKeyID: 7, BatchID: "imgbatch_phantom", HoldAmount: 1})
	require.NoError(t, err)
	require.Nil(t, result.NewBalance)
	require.Nil(t, result.FrozenBalance)
	require.NoError(t, tx.Commit())
	require.NoError(t, mock.ExpectationsWereMet())
}
