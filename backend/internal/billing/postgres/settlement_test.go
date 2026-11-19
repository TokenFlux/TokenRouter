package postgres

import (
	"context"
	"database/sql"
	"errors"
	"regexp"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/lib/pq"
	"github.com/stretchr/testify/require"

	"github.com/TokenFlux/TokenRouter/internal/batchimage"
	batchpg "github.com/TokenFlux/TokenRouter/internal/batchimage/postgres"
	"github.com/TokenFlux/TokenRouter/internal/billing"
	"github.com/TokenFlux/TokenRouter/internal/pkg/timezone"
)

// Task reservations require available funds before submission, while completed
// ordinary usage can create wallet debt after a pinned grant is capped. SQL
// mocks check the selected statements, their transaction completion and
// supplied return values; fixed-time quota cases separately check lifetime and
// outer-protection decisions.

// Wallet SQL patterns cover guarded available/frozen transfers and the
// payer-existence reread used to classify a rejected update. Their arguments
// and supplied result rows drive task helper assertions.
const (
	reserveBatchImageHoldSQL = `(?s)UPDATE users\s+SET balance = balance - \$1,\s+frozen_balance = COALESCE\(frozen_balance, 0\) \+ \$1,\s+updated_at = NOW\(\)\s+WHERE id = \$2 AND deleted_at IS NULL AND balance >= \$1\s+RETURNING balance, frozen_balance`
	captureBatchImageHoldSQL = `(?s)UPDATE users\s+SET balance = balance\s+\+ CASE WHEN \$1 > \$2 THEN \$1 - \$2 ELSE 0 END\s+- CASE WHEN \$2 > \$1 THEN \$2 - \$1 ELSE 0 END,\s+frozen_balance = COALESCE\(frozen_balance, 0\) - \$1,\s+updated_at = NOW\(\)\s+WHERE id = \$3 AND deleted_at IS NULL AND COALESCE\(frozen_balance, 0\) >= \$1\s+RETURNING balance, frozen_balance`
	releaseBatchImageHoldSQL = `(?s)UPDATE users\s+SET balance = balance \+ \$1,\s+frozen_balance = COALESCE\(frozen_balance, 0\) - \$1,\s+updated_at = NOW\(\)\s+WHERE id = \$2 AND deleted_at IS NULL AND COALESCE\(frozen_balance, 0\) >= \$1\s+RETURNING balance, frozen_balance`
	userExistsForBillingSQL  = `(?s)SELECT 1\s+FROM users\s+WHERE id = \$1 AND deleted_at IS NULL`

	// Usage SQL patterns recognize action claiming, archived identities, payer
	// locking, decimal wallet deduction and Key quota addition. Deadlock and
	// rounding tests use them to check the ordered calls.
	usageBillingClaimSQL         = `(?s)INSERT INTO usage_billing_dedup.*ON CONFLICT.*RETURNING id`
	usageBillingArchiveSQL       = `(?s)SELECT request_fingerprint.*FROM usage_billing_dedup_archive`
	usageBillingUserLockSQL      = `(?s)SELECT id\s+FROM users\s+WHERE id = \$1 AND deleted_at IS NULL\s+FOR NO KEY UPDATE`
	usageBillingBalanceDeductSQL = `(?s)WITH locked_user AS.*FOR NO KEY UPDATE.*UPDATE users.*RETURNING users.balance`
	usageBillingAPIKeyQuotaSQL   = `(?s)UPDATE api_keys.*quota_used = quota_used \+ \$1.*RETURNING quota > 0`
)

// TestReserveUsageBillingBatchImageBalance_MovesAvailableToFrozen supplies a
// 2.5-dollar hold and mocked post-update wallet values. Reservation must pass
// that amount to the guarded SQL update, return 7.5 available and 2.5 frozen,
// and complete the fixture transaction.
func TestReserveUsageBillingBatchImageBalance_MovesAvailableToFrozen(t *testing.T) {
	ctx := context.Background()
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	// Close this mock after checking the held wallet values and commit order.
	// Pool shutdown errors are discarded after those operation assertions.
	defer func() { _ = db.Close() }()

	mock.ExpectBegin()
	tx, err := db.BeginTx(ctx, nil)
	require.NoError(t, err)
	mock.ExpectQuery(reserveBatchImageHoldSQL).
		WithArgs(2.5, int64(42)).
		WillReturnRows(sqlmock.NewRows([]string{"balance", "frozen_balance"}).AddRow(7.5, 2.5))
	mock.ExpectCommit()

	result, err := reserveUsageBillingTaskBalance(ctx, tx, &billing.TaskFundsCommand{UserID: 42, HoldAmount: 2.5})
	require.NoError(t, err)
	require.NotNil(t, result.NewBalance)
	require.NotNil(t, result.FrozenBalance)
	// Allow one millionth dollar of absolute error in these available and frozen
	// wallet observations.
	require.InDelta(t, 7.5, *result.NewBalance, 0.000001)
	require.InDelta(t, 2.5, *result.FrozenBalance, 0.000001)
	// Complete the caller-owned mock transaction after inspecting both wallet
	// values. The expectation check also verifies the reservation used the
	// required guarded update and commit sequence.
	require.NoError(t, tx.Commit())
	require.NoError(t, mock.ExpectationsWereMet())
}

// TestReserveUsageBillingBatchImageBalance_InsufficientBalance makes the
// guarded ten-dollar hold match no row while the payer lookup succeeds. The
// helper must classify insufficient balance and leave the caller to roll back
// the fixture transaction.
func TestReserveUsageBillingBatchImageBalance_InsufficientBalance(t *testing.T) {
	ctx := context.Background()
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	// Close this mock after the rejected hold, payer-existence diagnosis and
	// owner rollback are checked. Pool shutdown errors are discarded after the
	// insufficient-balance assertion.
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

	_, err = reserveUsageBillingTaskBalance(ctx, tx, &billing.TaskFundsCommand{UserID: 42, HoldAmount: 10})
	require.ErrorIs(t, err, billing.ErrTaskInsufficientBalance)
	// End the rejected hold attempt through caller rollback before checking SQL
	// expectations. The mock requires the payer-existence diagnosis after the
	// guarded update returns no row.
	require.NoError(t, tx.Rollback())
	require.NoError(t, mock.ExpectationsWereMet())
}

// TestReserveUsageBillingBatchImageBilling_UsesBalanceRateAfterPartialSubscription
// supplies a group override with only 0.2 subscription dollars remaining. The
// versioned hold must report 0.2 subscription plus 1.2 wallet dollars, retain
// its covered base/rate metadata and estimate capture at 0.4. The task metadata
// statement is required with unrestricted value arguments, so this case does
// not assert its exact persisted split values.
func TestReserveUsageBillingBatchImageBilling_UsesBalanceRateAfterPartialSubscription(t *testing.T) {
	ctx := context.Background()
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	// Close this mock after the mixed funding split, participant write and commit
	// are checked. Discard shutdown errors after the hold and estimated-cost
	// assertions.
	defer func() { _ = db.Close() }()

	now := time.Date(2026, 6, 15, 12, 0, 0, 0, time.UTC)
	windowStart := now.Add(-time.Hour)
	mock.ExpectBegin()
	tx, err := db.BeginTx(ctx, nil)
	require.NoError(t, err)
	mock.ExpectQuery(`(?s)SELECT\s+id,\s+plan_id,.*FROM user_subscriptions.*FOR UPDATE`).
		WithArgs(int64(42), billing.SubscriptionStatusActive, billing.SubscriptionStatusPending, nil, int64(7)).
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
	result, err := reserveUsageBillingTaskBilling(ctx, tx, batchpg.NewFundingParticipant(tx, "imgbatch_partial_rate"), &billing.TaskFundsCommand{
		UserID:                          42,
		GroupID:                         &groupID,
		Task:                            batchimage.FundingReference("imgbatch_partial_rate"),
		HoldAmount:                      0.5,
		PricingSnapshotVersion:          2,
		BaseAmountUSD:                   1,
		SubscriptionRateMultiplier:      1,
		SubscriptionRateMultiplierScale: 1,
		BalanceRateMultiplier:           2,
		SettlementRateScale:             0.5,
	}, now)
	require.NoError(t, err)
	// The 1e-6 allowance covers floating arithmetic in source dollars, covered
	// base dollars and the chosen multiplier; allocation count retains its exact
	// assertion.
	require.InDelta(t, 0.2, result.SubscriptionAmountUSD, 0.000001)
	require.InDelta(t, 1.2, result.BalanceAmountUSD, 0.000001)
	require.InDelta(t, 1.4, result.HoldAmountUSD, 0.000001)
	require.InDelta(t, 0.4, result.EstimatedAmountUSD, 0.000001)
	require.Len(t, result.BillingAllocations, 2)
	require.InDelta(t, 0.4, result.BillingAllocations[0].BaseAmountUSD, 0.000001)
	require.InDelta(t, 0.5, result.BillingAllocations[0].RateMultiplier, 0.000001)
	// Commit only after the subscription split, wallet result and stored task
	// metadata have succeeded. The ordered mock expectations require their writes
	// to precede this transaction completion.
	require.NoError(t, tx.Commit())
	require.NoError(t, mock.ExpectationsWereMet())
}

// TestReserveUsageBillingBatchImageBilling_StrictSubscriptionRejectsPartialHold
// pins a 0.5-dollar reservation to a grant with 0.2 remaining. SQL first caps
// its counters, then the helper must reject insufficient preferred funding and
// require caller rollback before wallet or task metadata writes.
func TestReserveUsageBillingBatchImageBilling_StrictSubscriptionRejectsPartialHold(t *testing.T) {
	ctx := context.Background()
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	// Close this mock after preferred-funding rejection and owner rollback are
	// checked. Pool shutdown errors are discarded after the scenario's
	// capped-counter write expectations.
	defer func() { _ = db.Close() }()

	now := time.Date(2026, 6, 15, 12, 0, 0, 0, time.UTC)
	windowStart := now.Add(-time.Hour)
	preferredID := int64(11)
	mock.ExpectBegin()
	tx, err := db.BeginTx(ctx, nil)
	require.NoError(t, err)
	mock.ExpectQuery(`(?s)SELECT\s+id,\s+plan_id,.*FROM user_subscriptions.*AND NOT EXISTS\s*\(\s*SELECT 1\s+FROM subscription_plan_groups.*FOR UPDATE`).
		WithArgs(int64(42), billing.SubscriptionStatusActive, billing.SubscriptionStatusPending, preferredID).
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
	// The planner writes capped subscription counters before the pinned
	// reservation detects its uncovered amount. Expect owner rollback and no
	// wallet hold or task metadata update after rejection.
	mock.ExpectExec(`(?s)UPDATE user_subscriptions\s+SET.*WHERE id = \$7`).
		WithArgs(sqlmock.AnyArg(), sqlmock.AnyArg(), sqlmock.AnyArg(), 1.0, 1.0, 1.0, preferredID).
		WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectRollback()

	_, err = reserveUsageBillingTaskBilling(ctx, tx, batchpg.NewFundingParticipant(tx, "imgbatch_strict_subscription"), &billing.TaskFundsCommand{
		UserID:                  42,
		Task:                    batchimage.FundingReference("imgbatch_strict_subscription"),
		HoldAmount:              0.5,
		APIKeyBillingMode:       billing.APIKeyBillingModeSubscription,
		PreferredSubscriptionID: &preferredID,
	}, now)

	require.ErrorIs(t, err, billing.ErrPreferredSubscriptionInsufficient)
	// Roll back the caller-owned attempt after its capped counter update and
	// preferred-insufficient error. The mock requires this recovery before any
	// wallet or task metadata write.
	require.NoError(t, tx.Rollback())
	require.NoError(t, mock.ExpectationsWereMet())
}

// TestApplyUsageBillingEffects_StrictSubscriptionChargesOverflowToBalance
// supplies completed base usage beyond a pinned grant's remaining quota.
// Settlement must report capped subscription dollars, rate-converted wallet
// debt, both allocation amounts and the combined effective multiplier from the
// mocked wallet result.
func TestApplyUsageBillingEffects_StrictSubscriptionChargesOverflowToBalance(t *testing.T) {
	ctx := context.Background()
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	// Close this mock after capped quota, wallet debt and commit order are
	// checked. Pool shutdown errors are discarded after the allocation and
	// multiplier assertions.
	defer func() { _ = db.Close() }()

	now := time.Date(2026, 6, 15, 12, 0, 0, 0, time.UTC)
	windowStart := now.Add(-time.Hour)
	preferredID := int64(11)
	mock.ExpectBegin()
	tx, err := db.BeginTx(ctx, nil)
	require.NoError(t, err)
	mock.ExpectQuery(`(?s)SELECT\s+id,\s+plan_id,.*FROM user_subscriptions.*AND NOT EXISTS\s*\(\s*SELECT 1\s+FROM subscription_plan_groups.*FOR UPDATE`).
		WithArgs(int64(42), billing.SubscriptionStatusActive, billing.SubscriptionStatusPending, preferredID).
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
	// The completed request uses 0.2 subscription dollars to cover 0.4 base
	// dollars. The remaining 0.6 base at wallet rate two becomes 1.2 dollars of
	// recorded debt.
	mock.ExpectExec(`(?s)UPDATE user_subscriptions\s+SET.*WHERE id = \$7`).
		WithArgs(sqlmock.AnyArg(), sqlmock.AnyArg(), sqlmock.AnyArg(), 1.0, 1.0, 1.0, preferredID).
		WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectQuery(`(?s)WITH locked_user AS \(.*SELECT updated.balance, \$1::numeric AS deducted_amount`).
		WithArgs(1.2, int64(42)).
		WillReturnRows(sqlmock.NewRows([]string{"balance", "deducted_amount"}).AddRow(-1.2, 1.2))
	mock.ExpectCommit()

	result := &billing.UsageBillingApplyResult{}
	err = (&SettlementStore{}).applyUsageBillingEffects(ctx, tx, &billing.UsageBillingCommand{
		UserID:                          42,
		BillableAmountUSD:               0.5,
		BaseAmountUSD:                   1,
		SubscriptionRateMultiplier:      0.5,
		SubscriptionRateMultiplierScale: 1,
		BalanceRateMultiplier:           2,
		APIKeyBillingMode:               billing.APIKeyBillingModeSubscription,
		PreferredSubscriptionID:         &preferredID,
	}, result)

	require.NoError(t, err)
	// Compare floating allocations and effective rate within 1e-6, while the
	// selected subscription ID retains its exact assertion.
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
	// Complete this caller-owned fixture transaction after validating capped
	// subscription funding and wallet debt. The mock enforces their update order
	// before the commit expectation.
	require.NoError(t, tx.Commit())
	require.NoError(t, mock.ExpectationsWereMet())
}

// TestApplyUsageBillingEffects_StrictSubscriptionWithoutGroupFiltersRestrictedPlans
// supplies pinned funding without a final group and an empty selected-row
// result. The SQL must contain the unrestricted-plan predicate, and missing
// eligible rows must produce the preferred-insufficient error before any
// wallet charge.
func TestApplyUsageBillingEffects_StrictSubscriptionWithoutGroupFiltersRestrictedPlans(t *testing.T) {
	ctx := context.Background()
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	// Close this mock after the unrestricted-plan SQL and preferred-funding error
	// are checked. Pool shutdown errors are discarded after owner rollback and
	// the absent-wallet-write expectations.
	defer func() { _ = db.Close() }()

	preferredID := int64(11)
	mock.ExpectBegin()
	tx, err := db.BeginTx(ctx, nil)
	require.NoError(t, err)
	// The regex checks the unrestricted-plan predicate for a pinned request
	// lacking a final group. An empty mocked row set then exercises
	// preferred-funding insufficiency before wallet charging.
	mock.ExpectQuery(`(?s)SELECT\s+id,\s+plan_id,.*FROM user_subscriptions.*AND NOT EXISTS\s*\(\s*SELECT 1\s+FROM subscription_plan_groups.*FOR UPDATE`).
		WithArgs(int64(42), billing.SubscriptionStatusActive, billing.SubscriptionStatusPending, preferredID).
		WillReturnRows(sqlmock.NewRows([]string{
			"id", "plan_id", "starts_at", "expires_at",
			"daily_window_start", "weekly_window_start", "monthly_window_start",
			"daily_limit_usd", "weekly_limit_usd", "monthly_limit_usd",
			"daily_usage_usd", "weekly_usage_usd", "monthly_usage_usd", "group_rates",
		}))
	mock.ExpectRollback()

	err = (&SettlementStore{}).applyUsageBillingEffects(ctx, tx, &billing.UsageBillingCommand{
		UserID:                  42,
		BillableAmountUSD:       1,
		APIKeyBillingMode:       billing.APIKeyBillingModeSubscription,
		PreferredSubscriptionID: &preferredID,
	}, &billing.UsageBillingApplyResult{})

	require.ErrorIs(t, err, billing.ErrPreferredSubscriptionInsufficient)
	// Roll back the caller-owned attempt after the unrestricted-plan selection
	// returns no candidates. The mock permits no later counter or wallet effect
	// for that preferred-insufficient result.
	require.NoError(t, tx.Rollback())
	require.NoError(t, mock.ExpectationsWereMet())
}

// TestCaptureUsageBillingBatchImageBalance_ReleasesRemainder supplies a
// one-dollar hold with 0.25 actual wallet cost. Capture must pass both amounts
// to the guarded update and return the mocked 9.75 available balance with no
// frozen remainder.
func TestCaptureUsageBillingBatchImageBalance_ReleasesRemainder(t *testing.T) {
	ctx := context.Background()
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	// Close this mock after partial-cost capture and commit order are checked.
	// Pool shutdown errors are discarded after available and frozen wallet
	// observations.
	defer func() { _ = db.Close() }()

	mock.ExpectBegin()
	tx, err := db.BeginTx(ctx, nil)
	require.NoError(t, err)
	mock.ExpectQuery(captureBatchImageHoldSQL).
		WithArgs(1.0, 0.25, int64(42)).
		WillReturnRows(sqlmock.NewRows([]string{"balance", "frozen_balance"}).AddRow(9.75, 0.0))
	mock.ExpectCommit()

	result, err := captureUsageBillingTaskBalance(ctx, tx, &billing.TaskFundsCommand{UserID: 42, HoldAmount: 1, ActualAmount: 0.25})
	require.NoError(t, err)
	// Allow one millionth dollar of absolute error when comparing the available
	// and frozen capture results.
	require.InDelta(t, 9.75, *result.NewBalance, 0.000001)
	require.InDelta(t, 0.0, *result.FrozenBalance, 0.000001)
	// Complete the fixture capture transaction after inspecting the returned
	// available and frozen amounts. The mock requires the full recorded hold to
	// reach the guarded capture SQL first.
	require.NoError(t, tx.Commit())
	require.NoError(t, mock.ExpectationsWereMet())
}

// TestCaptureUsageBillingBatchImageBalance_RejectsActualCostOverHold supplies
// a half-dollar hold with one dollar of actual cost. The helper must reject
// excess cost before a wallet SQL update and leave the fixture transaction for
// caller rollback.
func TestCaptureUsageBillingBatchImageBalance_RejectsActualCostOverHold(t *testing.T) {
	ctx := context.Background()
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	// Close this mock after excess-cost rejection and owner rollback are checked.
	// Discard shutdown errors after verifying no wallet update was expected.
	defer func() { _ = db.Close() }()

	mock.ExpectBegin()
	tx, err := db.BeginTx(ctx, nil)
	require.NoError(t, err)
	mock.ExpectRollback()

	_, err = captureUsageBillingTaskBalance(ctx, tx, &billing.TaskFundsCommand{UserID: 42, HoldAmount: 0.5, ActualAmount: 1})
	require.ErrorIs(t, err, billing.ErrTaskSettlementCostExceedsHold)
	// Close the rejected capture attempt by caller rollback. No wallet update is
	// expected because the excess actual amount fails validation before frozen
	// funds can be consumed.
	require.NoError(t, tx.Rollback())
	require.NoError(t, mock.ExpectationsWereMet())
}

// TestReleaseUsageBillingBatchImageBalance_ReturnsFrozenToAvailable supplies a
// visible original reserve-key/API-Key pair and a one-dollar wallet hold.
// Release must query that identity, refund through guarded wallet SQL and
// return the mocked ten-dollar available balance with zero frozen.
func TestReleaseUsageBillingBatchImageBalance_ReturnsFrozenToAvailable(t *testing.T) {
	ctx := context.Background()
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	// Close this mock after the original reserve identity, wallet refund and
	// commit are checked. Pool shutdown errors are discarded after the
	// returned-balance assertions.
	defer func() { _ = db.Close() }()

	mock.ExpectBegin()
	tx, err := db.BeginTx(ctx, nil)
	require.NoError(t, err)
	mock.ExpectQuery(`SELECT 1\s+FROM usage_billing_dedup\s+WHERE request_id = \$1 AND api_key_id = \$2`).
		WithArgs(("batch_image_hold:" + "imgbatch_release"), int64(7)).
		WillReturnRows(sqlmock.NewRows([]string{"?column?"}).AddRow(1))
	mock.ExpectQuery(releaseBatchImageHoldSQL).
		WithArgs(1.0, int64(42)).
		WillReturnRows(sqlmock.NewRows([]string{"balance", "frozen_balance"}).AddRow(10.0, 0.0))
	mock.ExpectCommit()

	result, err := releaseUsageBillingTaskBilling(ctx, tx, &billing.TaskFundsCommand{UserID: 42, APIKeyID: 7, Task: batchimage.FundingReference("imgbatch_release"), HoldAmount: 1})
	require.NoError(t, err)
	// Compare refunded wallet observations within one millionth dollar; the
	// reserve-key/API-Key lookup keeps its exact SQL argument matching.
	require.InDelta(t, 10.0, *result.NewBalance, 0.000001)
	require.InDelta(t, 0.0, *result.FrozenBalance, 0.000001)
	// Complete the fixture release after the original reserve identity and
	// guarded wallet refund have been observed. The ordered expectations require
	// lookup before any returned funds.
	require.NoError(t, tx.Commit())
	require.NoError(t, mock.ExpectationsWereMet())
}

// TestReleaseUsageBillingBatchImageBalance_SkipsWhenHoldNeverReserved makes
// both live and archive reserve-key lookups return no rows. Release must issue
// no wallet refund and return nil balance observations while the caller can
// still complete the empty fixture transaction.
func TestReleaseUsageBillingBatchImageBalance_SkipsWhenHoldNeverReserved(t *testing.T) {
	ctx := context.Background()
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	// Close this mock after both reserve lookups miss and the empty release
	// commits. Discard shutdown errors after nil-wallet observations and the
	// absent-refund expectations.
	defer func() { _ = db.Close() }()

	mock.ExpectBegin()
	tx, err := db.BeginTx(ctx, nil)
	require.NoError(t, err)
	// Both supplied reserve-key lookups miss, so the release helper cannot
	// identify this task's hold. Omit refund SQL to keep aggregate frozen funds
	// from being returned through that missing identity.
	mock.ExpectQuery(`SELECT 1\s+FROM usage_billing_dedup\s+WHERE request_id = \$1 AND api_key_id = \$2`).
		WithArgs(("batch_image_hold:" + "imgbatch_phantom"), int64(7)).
		WillReturnError(sql.ErrNoRows)
	mock.ExpectQuery(`SELECT 1\s+FROM usage_billing_dedup_archive\s+WHERE request_id = \$1 AND api_key_id = \$2`).
		WithArgs(("batch_image_hold:" + "imgbatch_phantom"), int64(7)).
		WillReturnError(sql.ErrNoRows)
	mock.ExpectCommit()

	result, err := releaseUsageBillingTaskBilling(ctx, tx, &billing.TaskFundsCommand{UserID: 42, APIKeyID: 7, Task: batchimage.FundingReference("imgbatch_phantom"), HoldAmount: 1})
	require.NoError(t, err)
	require.Nil(t, result.NewBalance)
	require.Nil(t, result.FrozenBalance)
	// Complete the empty release attempt after both reserve identities miss. The
	// mock has no refund statement, so an attempted wallet return would fail its
	// ordered expectations.
	require.NoError(t, tx.Commit())
	require.NoError(t, mock.ExpectationsWereMet())
}

// TestNormalizeUsageBillingWindow_ExpiryTailKeepsMonthlyUsage evaluates a
// fixed May observation several hours before expiry with an elapsed monthly
// anchor. The unprotected thirty-day replacement cannot fit, so the existing
// anchor and ninety dollars of use must remain.
func TestNormalizeUsageBillingWindow_ExpiryTailKeepsMonthlyUsage(t *testing.T) {
	now := time.Date(2026, 5, 30, 0, 5, 0, 0, time.UTC)
	startsAt := time.Date(2026, 4, 30, 8, 0, 0, 0, time.UTC)
	expiresAt := time.Date(2026, 5, 30, 8, 0, 0, 0, time.UTC)
	windowStart := time.Date(2026, 4, 30, 0, 0, 0, 0, time.UTC)

	start, used := billing.NormalizeSettlementWindow(usageBillingNullableTimePtr(sql.NullTime{Time: windowStart, Valid: true}), usageBillingNullableFloat64Ptr(sql.NullFloat64{Float64: 100, Valid: true}), 90, timezone.NewCalendar(now.Location()).StartOfDay(now), 30*24*time.Hour, now, startsAt, expiresAt, false)

	require.NotNil(t, start)
	require.Equal(t, windowStart, *start)
	require.Equal(t, 90.0, used, "到期尾段不足完整月窗口时不应清零 monthly usage")
}

// TestNormalizeUsageBillingWindow_ExpiryTailMissingMonthlyWindowStaysNil
// supplies the same short monthly expiry tail with an absent anchor.
// Normalization must keep that absence and ninety dollars of recorded use when
// a complete replacement period cannot fit.
func TestNormalizeUsageBillingWindow_ExpiryTailMissingMonthlyWindowStaysNil(t *testing.T) {
	now := time.Date(2026, 5, 30, 0, 5, 0, 0, time.UTC)
	startsAt := time.Date(2026, 4, 30, 8, 0, 0, 0, time.UTC)
	expiresAt := time.Date(2026, 5, 30, 8, 0, 0, 0, time.UTC)

	start, used := billing.NormalizeSettlementWindow(usageBillingNullableTimePtr(sql.NullTime{}), usageBillingNullableFloat64Ptr(sql.NullFloat64{Float64: 100, Valid: true}), 90, timezone.NewCalendar(now.Location()).StartOfDay(now), 30*24*time.Hour, now, startsAt, expiresAt, false)

	require.Nil(t, start, "到期尾段不足完整月窗口时不应补写 monthly window_start")
	require.Equal(t, 90.0, used)
}

// TestNormalizeUsageBillingWindow_DailyCardDoesNotResetAfterMidnight observes
// an exactly one-day grant after the anchor's next midnight. Even with outer
// protection supplied, its established one-time daily anchor and ten dollars
// of use must remain.
func TestNormalizeUsageBillingWindow_DailyCardDoesNotResetAfterMidnight(t *testing.T) {
	now := time.Date(2026, 5, 31, 0, 5, 0, 0, time.UTC)
	startsAt := time.Date(2026, 5, 30, 12, 0, 0, 0, time.UTC)
	expiresAt := startsAt.Add(24 * time.Hour)
	windowStart := time.Date(2026, 5, 30, 0, 0, 0, 0, time.UTC)

	start, used := billing.NormalizeSettlementWindow(usageBillingNullableTimePtr(sql.NullTime{Time: windowStart, Valid: true}), usageBillingNullableFloat64Ptr(sql.NullFloat64{Float64: 10, Valid: true}), 10, timezone.NewCalendar(now.Location()).StartOfDay(now), 24*time.Hour, now, startsAt, expiresAt, true)

	require.NotNil(t, start)
	require.Equal(t, windowStart, *start)
	require.Equal(t, 10.0, used, "1 日卡跨 0 点后不应刷新第二份 daily quota")
}

// TestNormalizeUsageBillingWindow_MultiDayDailyStillResetsWhenFullWindowFits
// gives a multiday grant an elapsed daily anchor and sufficient lifetime for
// another day. Normalization must select the supplied UTC midnight and clear
// the ten-dollar usage counter.
func TestNormalizeUsageBillingWindow_MultiDayDailyStillResetsWhenFullWindowFits(t *testing.T) {
	now := time.Date(2026, 5, 29, 0, 5, 0, 0, time.UTC)
	startsAt := time.Date(2026, 5, 27, 12, 0, 0, 0, time.UTC)
	expiresAt := time.Date(2026, 6, 1, 12, 0, 0, 0, time.UTC)
	windowStart := time.Date(2026, 5, 28, 0, 0, 0, 0, time.UTC)
	resetStart := timezone.NewCalendar(now.Location()).StartOfDay(now)

	start, used := billing.NormalizeSettlementWindow(usageBillingNullableTimePtr(sql.NullTime{Time: windowStart, Valid: true}), usageBillingNullableFloat64Ptr(sql.NullFloat64{Float64: 10, Valid: true}), 10, resetStart, 24*time.Hour, now, startsAt, expiresAt, false)

	require.NotNil(t, start)
	require.Equal(t, resetStart, *start)
	require.Equal(t, 0.0, used, "多日订阅仍应在可覆盖完整日窗口时重置 daily usage")
}

// TestNormalizeUsageBillingWindow_DailyTailResetsWithFiniteOuterLimit supplies
// an elapsed daily anchor near expiry and a true outer-protection flag.
// Normalization must accept the supplied replacement midnight and clear usage
// although a full day cannot fit.
func TestNormalizeUsageBillingWindow_DailyTailResetsWithFiniteOuterLimit(t *testing.T) {
	now := time.Date(2026, 5, 30, 0, 5, 0, 0, time.UTC)
	startsAt := time.Date(2026, 4, 30, 8, 0, 0, 0, time.UTC)
	expiresAt := time.Date(2026, 5, 30, 8, 0, 0, 0, time.UTC)
	windowStart := time.Date(2026, 5, 29, 0, 0, 0, 0, time.UTC)
	resetStart := timezone.NewCalendar(now.Location()).StartOfDay(now)

	start, used := billing.NormalizeSettlementWindow(usageBillingNullableTimePtr(sql.NullTime{Time: windowStart, Valid: true}), usageBillingNullableFloat64Ptr(sql.NullFloat64{Float64: 10, Valid: true}), 10, resetStart, 24*time.Hour, now, startsAt, expiresAt, true)

	require.NotNil(t, start)
	require.Equal(t, resetStart, *start)
	require.Equal(t, 0.0, used, "有限周或月额度存在时，订阅尾段仍应刷新日额度")
}

// TestNormalizeUsageBillingWindow_WeeklyTailResetsWithFiniteMonthlyLimit
// supplies an elapsed weekly anchor near expiry and a true monthly-protection
// flag. The helper must choose the supplied midnight and clear usage despite
// insufficient lifetime for seven more days.
func TestNormalizeUsageBillingWindow_WeeklyTailResetsWithFiniteMonthlyLimit(t *testing.T) {
	now := time.Date(2026, 5, 30, 0, 5, 0, 0, time.UTC)
	startsAt := time.Date(2026, 4, 30, 8, 0, 0, 0, time.UTC)
	expiresAt := time.Date(2026, 5, 30, 8, 0, 0, 0, time.UTC)
	windowStart := time.Date(2026, 5, 23, 0, 0, 0, 0, time.UTC)
	resetStart := timezone.NewCalendar(now.Location()).StartOfDay(now)

	start, used := billing.NormalizeSettlementWindow(usageBillingNullableTimePtr(sql.NullTime{Time: windowStart, Valid: true}), usageBillingNullableFloat64Ptr(sql.NullFloat64{Float64: 50, Valid: true}), 50, resetStart, 7*24*time.Hour, now, startsAt, expiresAt, true)

	require.NotNil(t, start)
	require.Equal(t, resetStart, *start)
	require.Equal(t, 0.0, used, "有限月额度存在时，订阅尾段仍应刷新周额度")
}

// TestHasFiniteUsageBillingLimit_RequiresPositiveConfiguredLimit converts
// absent, zero, negative and positive SQL limits into optional billing values.
// Only the positive configured case must report a finite protecting quota; the
// other three cases report false.
func TestHasFiniteUsageBillingLimit_RequiresPositiveConfiguredLimit(t *testing.T) {
	tests := []struct {
		name  string          // configured limit scenario label
		limit sql.NullFloat64 // nullable SQL quota input
		want  bool            // expected positive-limit protection result
	}{
		{name: "未配置", limit: sql.NullFloat64{}, want: false},
		{name: "零表示无限", limit: sql.NullFloat64{Float64: 0, Valid: true}, want: false},
		{name: "负数表示无限", limit: sql.NullFloat64{Float64: -1, Valid: true}, want: false},
		{name: "正数有限额度", limit: sql.NullFloat64{Float64: 100, Valid: true}, want: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			require.Equal(t, tt.want, billing.PositiveSubscriptionLimit(usageBillingNullableFloat64Ptr(tt.limit)))
		})
	}
}

// TestUsageBillingRepositoryApply_DeadlockRestartsWholeTransaction injects two
// payer-lock deadlocks before a successful zero-cost action. Every attempt
// must open a fresh transaction and claim/check archive again, with rollback
// for the first two and a final Applied-true commit.
func TestUsageBillingRepositoryApply_DeadlockRestartsWholeTransaction(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	// Close this mock after all three attempts and final Applied success are
	// checked. Pool shutdown errors are discarded after the rollback and commit
	// expectations.
	defer func() {
		_ = db.Close()
	}()

	cmd := &billing.UsageBillingCommand{
		RequestID:          "req-deadlock-retry",
		RequestFingerprint: "fingerprint",
		APIKeyID:           7,
		UserID:             42,
	}
	// Two permitted retries require three complete claim/archive/lock sequences.
	// Inject deadlocks before effects on the first two; after its claim checks,
	// the third zero-cost attempt has a successful payer lock and commit.
	for attempt := 1; attempt <= 3; attempt++ {
		mock.ExpectBegin()
		mock.ExpectQuery(usageBillingClaimSQL).
			WithArgs(cmd.RequestID, cmd.APIKeyID, cmd.RequestFingerprint).
			WillReturnRows(sqlmock.NewRows([]string{"id"}).AddRow(int64(attempt)))
		mock.ExpectQuery(usageBillingArchiveSQL).
			WithArgs(cmd.RequestID, cmd.APIKeyID).
			WillReturnError(sql.ErrNoRows)
		userLock := mock.ExpectQuery(usageBillingUserLockSQL).WithArgs(cmd.UserID)
		if attempt < 3 {
			userLock.WillReturnError(&pq.Error{Code: "40P01"})
			mock.ExpectRollback()
			continue
		}
		userLock.WillReturnRows(sqlmock.NewRows([]string{"id"}).AddRow(cmd.UserID))
		mock.ExpectCommit()
	}

	repo := &SettlementStore{db: db}
	result, err := repo.Apply(context.Background(), cmd)

	require.NoError(t, err)
	require.NotNil(t, result)
	require.True(t, result.Applied)
	require.NoError(t, mock.ExpectationsWereMet())
}

// TestLockUsageBillingUser_ReturnsUserNotFound makes a positive payer's
// undeleted-row lock query return no rows. The helper must return the billing
// user-not-found error, and the caller must close that failed fixture attempt
// by rollback.
func TestLockUsageBillingUser_ReturnsUserNotFound(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	// Close this mock after the missing-payer error and owner rollback are
	// checked. Pool shutdown errors are discarded after the single lock-query
	// expectation.
	defer func() {
		_ = db.Close()
	}()

	mock.ExpectBegin()
	tx, err := db.BeginTx(context.Background(), nil)
	require.NoError(t, err)
	mock.ExpectQuery(usageBillingUserLockSQL).
		WithArgs(int64(42)).
		WillReturnError(sql.ErrNoRows)
	mock.ExpectRollback()

	err = lockUsageBillingUser(context.Background(), tx, 42)
	require.ErrorIs(t, err, billing.ErrUserNotFound)
	// Roll back the caller-owned transaction after its payer lock query finds no
	// undeleted row. The mock checks that no funding statement follows this
	// missing-user result.
	require.NoError(t, tx.Rollback())
	require.NoError(t, mock.ExpectationsWereMet())
}

// TestDeductUsageBillingBalance_KeepsForeignKeyCompatibleUserLock matches the
// deduction CTE containing FOR NO KEY UPDATE. A 1.25-dollar charge must return
// the supplied 8.75 balance and 1.25 deduction; these SQL expectations
// establish the lock clause rather than running a concurrent foreign-key
// scenario.
func TestDeductUsageBillingBalance_KeepsForeignKeyCompatibleUserLock(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	// Close this mock after the lock-clause match, deduction result and owner
	// rollback are checked. Pool shutdown errors are discarded after the
	// statement expectations.
	defer func() {
		_ = db.Close()
	}()

	mock.ExpectBegin()
	tx, err := db.BeginTx(context.Background(), nil)
	require.NoError(t, err)
	mock.ExpectQuery(usageBillingBalanceDeductSQL).
		WithArgs(1.25, int64(42)).
		WillReturnRows(sqlmock.NewRows([]string{"balance", "deducted_amount"}).AddRow(8.75, 1.25))
	mock.ExpectRollback()

	newBalance, deductedAmount, err := deductUsageBillingBalance(context.Background(), tx, 42, 1.25)
	require.NoError(t, err)
	// Compare returned numeric wallet observations within one millionth dollar,
	// independently of the deduction SQL's exact supplied parameters.
	require.InDelta(t, 8.75, newBalance, 0.000001)
	require.InDelta(t, 1.25, deductedAmount, 0.000001)
	// Close this caller-owned statement fixture with rollback after reading its
	// mocked numeric result. The SQL regex checks lock compatibility while no
	// concurrent database workload runs here.
	require.NoError(t, tx.Rollback())
	require.NoError(t, mock.ExpectationsWereMet())
}

// TestUsageBillingMonetaryEffectsQuantizeBeforeSQL supplies a raw decimal
// rounding tie to wallet subtraction and Key quota addition. Both statements
// must receive the shared quantized amount, and the deduction result must
// equal it under the fixture's mocked numeric return.
func TestUsageBillingMonetaryEffectsQuantizeBeforeSQL(t *testing.T) {
	// Choose a decimal half tie at the ninth place, where independent NUMERIC
	// addition and subtraction could otherwise round the financial effect
	// differently.
	const rawAmount = 0.000078125
	wantAmount := billing.QuantizeUsageBillingAmount(rawAmount)

	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	// Close this mock after wallet/quota parameters and owner rollback are
	// checked. Pool shutdown errors are discarded after the shared rounded-amount
	// assertion.
	defer func() {
		_ = db.Close()
	}()

	mock.ExpectBegin()
	tx, err := db.BeginTx(context.Background(), nil)
	require.NoError(t, err)
	mock.ExpectQuery(usageBillingBalanceDeductSQL).
		WithArgs(wantAmount, int64(42)).
		WillReturnRows(sqlmock.NewRows([]string{"balance", "deducted_amount"}).AddRow(9999.99992187, wantAmount))
	mock.ExpectQuery(usageBillingAPIKeyQuotaSQL).
		WithArgs(wantAmount, int64(7), "active", "quota_exhausted").
		WillReturnRows(sqlmock.NewRows([]string{"exhausted"}).AddRow(false))
	mock.ExpectRollback()

	_, deductedAmount, err := deductUsageBillingBalance(context.Background(), tx, 42, rawAmount)
	require.NoError(t, err)
	_, err = incrementUsageBillingAPIKeyQuota(context.Background(), tx, 7, rawAmount)
	require.NoError(t, err)
	require.Equal(t, wantAmount, deductedAmount)
	// Close the caller-owned rounding fixture with rollback after both monetary
	// statements receive the same decimal amount. The ordered mock assertions
	// keep wallet and quota parameters comparable.
	require.NoError(t, tx.Rollback())
	require.NoError(t, mock.ExpectationsWereMet())
}

// TestTaskProjectionRequiresRegisteredScope supplies a task scope absent from
// constructor registrations. Reserve must return an error before beginning a
// SQL transaction, leaving the mock with no funding expectations to satisfy.
func TestTaskProjectionRequiresRegisteredScope(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	// Close this mock after the unregistered-scope error is checked. Discard
	// shutdown errors after confirming the scenario required no transaction or
	// funding statements.
	defer func() { _ = db.Close() }()
	store := NewSettlementStore(db, timezone.NewCalendar(time.Local), nil)
	_, err = store.Reserve(context.Background(), &billing.TaskFundsCommand{Task: billing.TaskReference{Scope: "unknown", ID: "opaque", ReserveRequestID: "hold:opaque"}, RequestID: "hold:opaque"})
	require.Error(t, err)
	require.NoError(t, mock.ExpectationsWereMet())
}

// TestBatchImageHoldClaimRequestID supplies nil input and two recorded reserve
// keys with different task prefixes. Nil must return empty, and both nonnil
// keys must return verbatim independently of the opaque task ID.
func TestBatchImageHoldClaimRequestID(t *testing.T) {
	require.Empty(t, taskHoldClaimRequestID(nil))
	for _, id := range []string{"batch_image_hold:imgbatch_x", "creative_hold:crun_x"} {
		require.Equal(t, id, taskHoldClaimRequestID(&billing.TaskFundsCommand{Task: billing.TaskReference{ID: "opaque", ReserveRequestID: id}}))
	}
}

// TestIncrementUsageBillingProviderQuota_ReturnedState exercises the eight
// returned counters and each limit crossing. Missing providers and unavailable
// outbox writes return errors to the caller, while successful scans retain
// the returned field order and use the supplied transaction for events.
func TestIncrementUsageBillingProviderQuota_ReturnedState(t *testing.T) {
	outboxErr := errors.New("quota outbox unavailable")
	for _, tc := range []struct {
		name          string                     // returned quota scenario
		state         billing.ProviderQuotaState // supplied post-update counters
		missing       bool                       // omit provider result
		missingOutbox bool                       // omit crossing callback
		outboxErr     error                      // injected callback failure
		wantErr       error                      // expected sentinel failure
		wantCalls     int                        // expected callback count
	}{
		{name: "distinct scan fields", state: billing.ProviderQuotaState{TotalUsed: 11, TotalLimit: 100, DailyUsed: 12, DailyLimit: 200, WeeklyUsed: 13, WeeklyLimit: 300, MonthlyUsed: 14, MonthlyLimit: 400}},
		{name: "total crossing", state: billing.ProviderQuotaState{TotalUsed: 11, TotalLimit: 10}, wantCalls: 1},
		{name: "daily crossing", state: billing.ProviderQuotaState{DailyUsed: 11, DailyLimit: 10}, wantCalls: 1},
		{name: "weekly crossing", state: billing.ProviderQuotaState{WeeklyUsed: 11, WeeklyLimit: 10}, wantCalls: 1},
		{name: "monthly crossing", state: billing.ProviderQuotaState{MonthlyUsed: 11, MonthlyLimit: 10}, wantCalls: 1},
		{name: "already exhausted", state: billing.ProviderQuotaState{TotalUsed: 12, TotalLimit: 10}},
		{name: "missing provider", missing: true, wantErr: billing.ErrProviderNotFound},
		{name: "missing outbox", state: billing.ProviderQuotaState{TotalUsed: 11, TotalLimit: 10}, missingOutbox: true},
		{name: "outbox failure", state: billing.ProviderQuotaState{TotalUsed: 11, TotalLimit: 10}, outboxErr: outboxErr, wantErr: outboxErr, wantCalls: 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			db, mock, err := sqlmock.New()
			require.NoError(t, err)
			t.Cleanup(func() { require.NoError(t, db.Close()) })

			ctx := context.Background()
			mock.ExpectBegin()
			tx, err := db.BeginTx(ctx, nil)
			require.NoError(t, err)
			rows := sqlmock.NewRows([]string{
				"quota_used", "quota_limit", "quota_daily_used", "quota_daily_limit",
				"quota_weekly_used", "quota_weekly_limit", "quota_monthly_used", "quota_monthly_limit",
			})
			if !tc.missing {
				rows.AddRow(tc.state.TotalUsed, tc.state.TotalLimit, tc.state.DailyUsed, tc.state.DailyLimit,
					tc.state.WeeklyUsed, tc.state.WeeklyLimit, tc.state.MonthlyUsed, tc.state.MonthlyLimit)
			}
			query := `(?s)^UPDATE providers SET extra = .*WHERE id = \$2 AND deleted_at IS NULL RETURNING ` +
				regexp.QuoteMeta(`COALESCE((extra->>'quota_used')::numeric, 0), COALESCE((extra->>'quota_limit')::numeric, 0), `+
					`COALESCE((extra->>'quota_daily_used')::numeric, 0), COALESCE((extra->>'quota_daily_limit')::numeric, 0), `+
					`COALESCE((extra->>'quota_weekly_used')::numeric, 0), COALESCE((extra->>'quota_weekly_limit')::numeric, 0), `+
					`COALESCE((extra->>'quota_monthly_used')::numeric, 0), COALESCE((extra->>'quota_monthly_limit')::numeric, 0)`) + `$`
			mock.ExpectQuery(query).
				WithArgs(2.0, int64(41)).
				WillReturnRows(rows).
				RowsWillBeClosed()
			mock.ExpectRollback()
			mock.ExpectClose()

			calls := 0
			var outbox ProviderQuotaOutbox
			if !tc.missingOutbox {
				outbox = func(callbackCtx context.Context, callbackTx *sql.Tx, providerID int64) error {
					calls++
					require.Equal(t, ctx, callbackCtx)
					require.Same(t, tx, callbackTx)
					require.Equal(t, int64(41), providerID)
					return tc.outboxErr
				}
			}
			state, err := incrementUsageBillingProviderQuota(ctx, tx, 41, 2, outbox)
			switch {
			case tc.missingOutbox:
				require.EqualError(t, err, "provider quota outbox is not configured")
				require.Nil(t, state)
			case tc.wantErr != nil:
				require.ErrorIs(t, err, tc.wantErr)
				require.Nil(t, state)
			default:
				require.NoError(t, err)
				require.Equal(t, &tc.state, state)
			}
			require.Equal(t, tc.wantCalls, calls)
			// End this caller-owned attempt after inspecting the returned state or
			// failure, so the helper cannot silently complete its enclosing transaction.
			require.NoError(t, tx.Rollback())
			require.NoError(t, db.Close())
			require.NoError(t, mock.ExpectationsWereMet())
		})
	}
}

// TestSettlementStore_UsesConfiguredCalendarForDailyRollover sets a UTC clock
// instant that has already crossed midnight in Asia/Shanghai while still on the
// prior UTC date, with an exhausted afternoon daily anchor from the previous
// Shanghai day. Both unlocked group resolution and transactional settlement must
// reset daily usage at Shanghai midnight instead of treating the grant as exhausted.
func TestSettlementStore_UsesConfiguredCalendarForDailyRollover(t *testing.T) {
	shanghai, err := time.LoadLocation("Asia/Shanghai")
	require.NoError(t, err)
	utcClock := time.Date(2026, 5, 10, 16, 30, 0, 0, time.UTC)
	cal := timezone.NewCalendarWithClock(shanghai, func() time.Time { return utcClock })
	expectedNow := utcClock.In(shanghai)
	expectedMidnight := time.Date(2026, 5, 11, 0, 0, 0, 0, shanghai)
	priorAfternoon := time.Date(2026, 5, 10, 15, 0, 0, 0, shanghai)
	startsAt := time.Date(2026, 5, 1, 0, 0, 0, 0, shanghai)
	expiresAt := time.Date(2026, 5, 20, 0, 0, 0, 0, shanghai)

	ctx := context.Background()
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	// Close the mock pool after both unlocked resolution and transactional
	// settlement expectations have been checked.
	defer func() { _ = db.Close() }()

	store := NewSettlementStore(db, cal, nil)
	preferredID := int64(11)

	mock.ExpectQuery(`(?s)SELECT\s+us\.id,\s+us\.plan_id,.*FROM user_subscriptions us`).
		WithArgs(int64(42), billing.SubscriptionStatusActive, billing.SubscriptionStatusPending, int64(7), expectedNow, &preferredID).
		WillReturnRows(sqlmock.NewRows([]string{
			"id", "plan_id", "starts_at", "expires_at",
			"daily_window_start", "weekly_window_start", "monthly_window_start",
			"daily_limit_usd", "weekly_limit_usd", "monthly_limit_usd",
			"daily_usage_usd", "weekly_usage_usd", "monthly_usage_usd",
			"plan_groups", "plan_rates",
		}).AddRow(
			preferredID, int64(22), startsAt, expiresAt,
			priorAfternoon, nil, nil,
			10.0, nil, nil,
			10.0, 0.0, 0.0,
			`[7]`, `{}`,
		))

	usable, err := store.resolveUsableSubscriptionForGroup(ctx, 42, 7, &preferredID)
	require.NoError(t, err)
	require.NotNil(t, usable)
	require.NotNil(t, usable.DailyWindowStart)
	require.True(t, expectedMidnight.Equal(*usable.DailyWindowStart))
	require.InDelta(t, 0.0, usable.DailyUsageUSD, 0.000001)

	mock.ExpectBegin()
	tx, err := db.BeginTx(ctx, nil)
	require.NoError(t, err)
	mock.ExpectQuery(`(?s)SELECT\s+id,\s+plan_id,.*FROM user_subscriptions.*FOR UPDATE`).
		WithArgs(int64(42), billing.SubscriptionStatusActive, billing.SubscriptionStatusPending, preferredID, int64(7)).
		WillReturnRows(sqlmock.NewRows([]string{
			"id", "plan_id", "starts_at", "expires_at",
			"daily_window_start", "weekly_window_start", "monthly_window_start",
			"daily_limit_usd", "weekly_limit_usd", "monthly_limit_usd",
			"daily_usage_usd", "weekly_usage_usd", "monthly_usage_usd", "group_rates",
		}).AddRow(
			preferredID, int64(22), startsAt, expiresAt,
			priorAfternoon, nil, nil,
			10.0, nil, nil,
			10.0, 0.0, 0.0, `{}`,
		))
	mock.ExpectExec(`(?s)UPDATE user_subscriptions\s+SET.*WHERE id = \$7`).
		WithArgs(expectedMidnight, nil, nil, 1.5, 0.0, 0.0, preferredID).
		WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectCommit()

	groupID := int64(7)
	applyResult := &billing.UsageBillingApplyResult{}
	err = store.applyUsageBillingEffects(ctx, tx, &billing.UsageBillingCommand{
		UserID:                  42,
		GroupID:                 &groupID,
		BillableAmountUSD:       1.5,
		APIKeyBillingMode:       billing.APIKeyBillingModeSubscription,
		PreferredSubscriptionID: &preferredID,
	}, applyResult)
	require.NoError(t, err)
	// Confirm the reset subscription absorbed the full 1.5-dollar charge with zero
	// wallet spillover before committing the caller-owned mock transaction.
	require.InDelta(t, 1.5, applyResult.SubscriptionAmountUSD, 0.000001)
	require.InDelta(t, 0.0, applyResult.BalanceAmountUSD, 0.000001)
	require.NoError(t, tx.Commit())
	require.NoError(t, mock.ExpectationsWereMet())
}
