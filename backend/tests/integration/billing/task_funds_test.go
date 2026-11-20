//go:build integration

package billing_test

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/TokenFlux/TokenRouter/internal/apikey"
	keypostgres "github.com/TokenFlux/TokenRouter/internal/apikey/postgres"
	"github.com/TokenFlux/TokenRouter/internal/batchimage"
	"github.com/TokenFlux/TokenRouter/internal/billing"
	billingpostgres "github.com/TokenFlux/TokenRouter/internal/billing/postgres"
	"github.com/TokenFlux/TokenRouter/internal/identity"
	"github.com/TokenFlux/TokenRouter/internal/pkg/timezone"
	"github.com/TokenFlux/TokenRouter/internal/routing"
	"github.com/TokenFlux/TokenRouter/internal/team"
	teampostgres "github.com/TokenFlux/TokenRouter/internal/team/postgres"
)

// Batch-image funding reserves payer funds and allowances before capture or
// release reconciles the recorded hold. PostgreSQL cases compare wallet and
// frozen amounts with subscription windows, key counters, member usage, and
// each task's allowance flag. The command retains actor and payer identities
// separately, allowing team tasks to charge the owner while counting a member's
// consumption.
//
// Concurrent reservation uses a shared start signal, buffered results, and a
// WaitGroup so both attempts finish before their limit outcomes are counted.
// Release cases retain the original reservation time while simulating a deleted
// key, departed or rejoined membership, or a newer spending window. Their
// stored-value assertions identify which earlier allowance can be refunded
// without erasing later membership or window usage.

// insertBatchImageAllowanceTestJob inserts one committed Gemini image task
// using the supplied actor, payer, key, optional team, and timestamps. Creation
// failure fails the test before its allowance operation begins.
func insertBatchImageAllowanceTestJob(t *testing.T, batchID string, actorUserID, billingUserID, apiKeyID int64, teamID *int64, createdAt time.Time) {
	t.Helper()
	_, err := integrationDB.ExecContext(context.Background(), `
		INSERT INTO batch_image_jobs (
			batch_id, user_id, billing_user_id, team_id, api_key_id,
			platform, model, item_count, estimated_cost, hold_amount, created_at, updated_at
		) VALUES ($1, $2, $3, $4, $5, 'gemini', 'image-test', 1, 1, 1, $6, $6)`,
		batchID, actorUserID, billingUserID, teamID, apiKeyID, createdAt)
	require.NoError(t, err)
}

// TestUsageBillingRepositoryBatchImageAllowanceReserveCaptureAndRelease
// reserves key and wallet allowances, rejects overflow, captures reduced cost,
// and releases another hold. Persisted counters reconcile to captured cost, and
// repeated capture is unapplied.
func TestUsageBillingRepositoryBatchImageAllowanceReserveCaptureAndRelease(t *testing.T) {
	ctx := context.Background()
	client := testEntClient(t)
	repo := newTaskFundsFixture(integrationDB)
	user := mustCreateUser(t, client, &identity.User{
		Email:   fmt.Sprintf("batch-allowance-%d@example.com", time.Now().UnixNano()),
		Balance: 10,
	})
	apiKey := mustCreateApiKey(t, client, &apikey.APIKey{
		UserID:      user.ID,
		Key:         "sk-batch-allowance-" + uuid.NewString(),
		Name:        "batch-allowance",
		Quota:       1,
		RateLimit5h: 1,
		RateLimit1d: 1,
		RateLimit7d: 1,
	})

	firstBatchID := "imgbatch_" + strings.ReplaceAll(uuid.NewString(), "-", "")
	reservedAt := time.Now().UTC()
	insertBatchImageAllowanceTestJob(t, firstBatchID, user.ID, user.ID, apiKey.ID, nil, reservedAt)
	reserveCommand := &billing.TaskFundsCommand{
		RequestID:   batchimage.BatchImageHoldRequestID(firstBatchID),
		APIKeyID:    apiKey.ID,
		UserID:      user.ID,
		ActorUserID: user.ID,

		HoldAmount: 0.8,
		ReservedAt: reservedAt, Task: batchimage.FundingReference(firstBatchID),
	}
	reserveResult, err := repo.Reserve(ctx, reserveCommand)
	require.NoError(t, err)
	require.True(t, reserveResult.Applied)

	var balance, frozen, quotaUsed, usage5h, usage1d, usage7d float64
	var allowanceReserved bool
	require.NoError(t, integrationDB.QueryRowContext(ctx, `SELECT balance, frozen_balance FROM users WHERE id = $1`, user.ID).Scan(&balance, &frozen))
	// Use micro-dollar comparisons across the 0.8 hold, its frozen funds,
	// and the recorded key costs.
	require.InDelta(t, 9.2, balance, 0.000001)
	require.InDelta(t, 0.8, frozen, 0.000001)
	require.NoError(t, integrationDB.QueryRowContext(ctx, `SELECT quota_used, usage_5h, usage_1d, usage_7d FROM api_keys WHERE id = $1`, apiKey.ID).Scan(&quotaUsed, &usage5h, &usage1d, &usage7d))
	require.InDelta(t, 0.8, quotaUsed, 0.000001)
	require.InDelta(t, 0.8, usage5h, 0.000001)
	require.InDelta(t, 0.8, usage1d, 0.000001)
	require.InDelta(t, 0.8, usage7d, 0.000001)
	require.NoError(t, integrationDB.QueryRowContext(ctx, `SELECT allowance_reserved FROM batch_image_jobs WHERE batch_id = $1`, firstBatchID).Scan(&allowanceReserved))
	require.True(t, allowanceReserved)

	overflowBatchID := "imgbatch_" + strings.ReplaceAll(uuid.NewString(), "-", "")
	overflowReservedAt := time.Now().UTC()
	insertBatchImageAllowanceTestJob(t, overflowBatchID, user.ID, user.ID, apiKey.ID, nil, overflowReservedAt)
	_, err = repo.Reserve(ctx, &billing.TaskFundsCommand{
		RequestID:   batchimage.BatchImageHoldRequestID(overflowBatchID),
		APIKeyID:    apiKey.ID,
		UserID:      user.ID,
		ActorUserID: user.ID,

		HoldAmount: 0.3,
		ReservedAt: overflowReservedAt, Task: batchimage.FundingReference(overflowBatchID),
	})
	require.ErrorIs(t, err, apikey.ErrAPIKeyQuotaExhausted)
	require.NoError(t, integrationDB.QueryRowContext(ctx, `SELECT balance, frozen_balance FROM users WHERE id = $1`, user.ID).Scan(&balance, &frozen))
	require.InDelta(t, 9.2, balance, 0.000001)
	require.InDelta(t, 0.8, frozen, 0.000001)

	// Copy the reservation identity for capture, then select its capture
	// request ID and reconcile recorded hold amounts so this phase has its
	// own durable claim.
	captureCommand := *reserveCommand
	captureCommand.RequestID = batchimage.BatchImageCaptureRequestID(firstBatchID)
	captureCommand.ActualAmount = 0.3

	captureCommand.BalanceHoldAmount = reserveResult.BalanceAmountUSD
	captureCommand.AllowanceReserved = true
	captureCommand.RequestFingerprint = ""
	captureResult, err := repo.Capture(ctx, &captureCommand)
	require.NoError(t, err)
	require.True(t, captureResult.Applied)
	require.NoError(t, integrationDB.QueryRowContext(ctx, `SELECT balance, frozen_balance FROM users WHERE id = $1`, user.ID).Scan(&balance, &frozen))
	require.InDelta(t, 9.7, balance, 0.000001)
	require.InDelta(t, 0, frozen, 0.000001)
	require.NoError(t, integrationDB.QueryRowContext(ctx, `SELECT quota_used, usage_5h FROM api_keys WHERE id = $1`, apiKey.ID).Scan(&quotaUsed, &usage5h))
	require.InDelta(t, 0.3, quotaUsed, 0.000001)
	require.InDelta(t, 0.3, usage5h, 0.000001)

	retryCapture := captureCommand
	retryCapture.AllowanceReserved = false
	retryCapture.RequestFingerprint = ""
	retryResult, err := repo.Capture(ctx, &retryCapture)
	require.NoError(t, err)
	require.False(t, retryResult.Applied)

	secondBatchID := "imgbatch_" + strings.ReplaceAll(uuid.NewString(), "-", "")
	secondReservedAt := time.Now().UTC()
	insertBatchImageAllowanceTestJob(t, secondBatchID, user.ID, user.ID, apiKey.ID, nil, secondReservedAt)
	secondReserve := &billing.TaskFundsCommand{
		RequestID:   batchimage.BatchImageHoldRequestID(secondBatchID),
		APIKeyID:    apiKey.ID,
		UserID:      user.ID,
		ActorUserID: user.ID,

		HoldAmount: 0.7,
		ReservedAt: secondReservedAt, Task: batchimage.FundingReference(secondBatchID),
	}
	_, err = repo.Reserve(ctx, secondReserve)
	require.NoError(t, err)

	// Copy this second reservation into a release operation with its
	// distinct request ID, letting the recorded hold return to its original
	// funding source.
	releaseCommand := *secondReserve
	releaseCommand.RequestID = batchimage.BatchImageReleaseRequestID(secondBatchID)
	releaseCommand.AllowanceReserved = true
	releaseCommand.RequestFingerprint = ""
	releaseResult, err := repo.Release(ctx, &releaseCommand)
	require.NoError(t, err)
	require.True(t, releaseResult.Applied)
	require.NoError(t, integrationDB.QueryRowContext(ctx, `SELECT quota_used, usage_5h, status FROM api_keys WHERE id = $1`, apiKey.ID).Scan(&quotaUsed, &usage5h, &apiKey.Status))
	require.InDelta(t, 0.3, quotaUsed, 0.000001)
	require.InDelta(t, 0.3, usage5h, 0.000001)
	require.Equal(t, apikey.StatusAPIKeyActive, apiKey.Status)
}

// TestUsageBillingRepositoryBatchImageSubscriptionFirstReserveAndCapture
// reserves subscription capacity before wallet funds and captures a smaller
// cost. Release refunds remaining holds, while insufficient-wallet reservation
// retains the earlier stored balances and subscription usage.
func TestUsageBillingRepositoryBatchImageSubscriptionFirstReserveAndCapture(t *testing.T) {
	ctx := context.Background()
	client := testEntClient(t)
	now := time.Now()
	calendar := timezone.NewCalendarWithClock(time.Local, func() time.Time { return now })
	repo := billing.NewFunds(newSettlementFixtureWithCalendar(integrationDB, calendar))
	user := mustCreateUser(t, client, &identity.User{
		Email:   fmt.Sprintf("batch-subscription-%d@example.com", time.Now().UnixNano()),
		Balance: 1,
	})
	plan := mustCreatePlan(t, client, &billing.SubscriptionPlan{
		Name:            "batch-subscription-plan-" + uuid.NewString(),
		Description:     "batch image subscription billing test plan",
		Price:           10,
		ValidityDays:    30,
		ValidityUnit:    "day",
		ForSale:         true,
		DailyLimitUSD:   float64Ptr(1),
		WeeklyLimitUSD:  float64Ptr(1),
		MonthlyLimitUSD: float64Ptr(1),
	})
	reservedAt := now.UTC()
	windowStart := calendar.StartOfDay(reservedAt)
	subscription := mustCreateSubscription(t, client, &billing.UserSubscription{
		UserID:             user.ID,
		PlanID:             plan.ID,
		DailyWindowStart:   &windowStart,
		WeeklyWindowStart:  &windowStart,
		MonthlyWindowStart: &windowStart,
		DailyLimitUSD:      float64Ptr(1),
		WeeklyLimitUSD:     float64Ptr(1),
		MonthlyLimitUSD:    float64Ptr(1),
		DailyUsageUSD:      0.4,
		WeeklyUsageUSD:     0.4,
		MonthlyUsageUSD:    0.4,
	})
	apiKey := mustCreateApiKey(t, client, &apikey.APIKey{
		UserID: user.ID,
		Key:    "sk-batch-subscription-" + uuid.NewString(),
		Name:   "batch-subscription",
	})
	batchID := "imgbatch_" + strings.ReplaceAll(uuid.NewString(), "-", "")
	insertBatchImageAllowanceTestJob(t, batchID, user.ID, user.ID, apiKey.ID, nil, reservedAt)
	reserveCommand := &billing.TaskFundsCommand{
		RequestID:   batchimage.BatchImageHoldRequestID(batchID),
		APIKeyID:    apiKey.ID,
		UserID:      user.ID,
		ActorUserID: user.ID,

		HoldAmount: 1,
		ReservedAt: reservedAt, Task: batchimage.FundingReference(batchID),
	}

	reserved, err := repo.Reserve(ctx, reserveCommand)
	require.NoError(t, err)
	require.True(t, reserved.Applied)
	// A micro-dollar tolerance compares the 0.6 subscription reservation
	// and its 0.4 wallet share.
	require.InDelta(t, 0.6, reserved.SubscriptionAmountUSD, 0.000001)
	require.InDelta(t, 0.4, reserved.BalanceAmountUSD, 0.000001)

	var balance, frozen, dailyUsage float64
	require.NoError(t, integrationDB.QueryRowContext(ctx, `SELECT balance, frozen_balance FROM users WHERE id = $1`, user.ID).Scan(&balance, &frozen))
	require.InDelta(t, 0.6, balance, 0.000001)
	require.InDelta(t, 0.4, frozen, 0.000001)
	require.NoError(t, integrationDB.QueryRowContext(ctx, `SELECT daily_usage_usd FROM user_subscriptions WHERE id = $1`, subscription.ID).Scan(&dailyUsage))
	require.InDelta(t, 1, dailyUsage, 0.000001)

	subscriptionHolds := make([]billing.BillingAllocation, 0, 1)
	for _, allocation := range reserved.BillingAllocations {
		if allocation.Type == billing.BillingAllocationTypeSubscription {
			subscriptionHolds = append(subscriptionHolds, allocation)
		}
	}
	// Copy the reservation identity for capture, then select its capture
	// request ID and reconcile recorded hold amounts so this phase has its
	// own durable claim.
	captureCommand := *reserveCommand
	captureCommand.RequestID = batchimage.BatchImageCaptureRequestID(batchID)
	captureCommand.RequestFingerprint = ""
	captureCommand.ActualAmount = 0.3
	captureCommand.BalanceHoldAmount = reserved.BalanceAmountUSD
	captureCommand.SubscriptionHoldAllocations = subscriptionHolds
	captureCommand.AllowanceReserved = true

	captured, err := repo.Capture(ctx, &captureCommand)
	require.NoError(t, err)
	require.True(t, captured.Applied)
	require.InDelta(t, 0.3, captured.SubscriptionAmountUSD, 0.000001)
	require.Zero(t, captured.BalanceAmountUSD)
	require.NoError(t, integrationDB.QueryRowContext(ctx, `SELECT balance, frozen_balance FROM users WHERE id = $1`, user.ID).Scan(&balance, &frozen))
	require.InDelta(t, 1, balance, 0.000001)
	require.Zero(t, frozen)
	require.NoError(t, integrationDB.QueryRowContext(ctx, `SELECT daily_usage_usd FROM user_subscriptions WHERE id = $1`, subscription.ID).Scan(&dailyUsage))
	require.InDelta(t, 0.7, dailyUsage, 0.000001)

	releaseBatchID := "imgbatch_" + strings.ReplaceAll(uuid.NewString(), "-", "")
	insertBatchImageAllowanceTestJob(t, releaseBatchID, user.ID, user.ID, apiKey.ID, nil, reservedAt)
	releaseReserveCommand := &billing.TaskFundsCommand{
		RequestID:   batchimage.BatchImageHoldRequestID(releaseBatchID),
		APIKeyID:    apiKey.ID,
		UserID:      user.ID,
		ActorUserID: user.ID,

		HoldAmount: 0.5,
		ReservedAt: reservedAt, Task: batchimage.FundingReference(releaseBatchID),
	}
	releaseReserved, err := repo.Reserve(ctx, releaseReserveCommand)
	require.NoError(t, err)
	require.InDelta(t, 0.3, releaseReserved.SubscriptionAmountUSD, 0.000001)
	require.InDelta(t, 0.2, releaseReserved.BalanceAmountUSD, 0.000001)

	releaseSubscriptionHolds := make([]billing.BillingAllocation, 0, 1)
	for _, allocation := range releaseReserved.BillingAllocations {
		if allocation.Type == billing.BillingAllocationTypeSubscription {
			releaseSubscriptionHolds = append(releaseSubscriptionHolds, allocation)
		}
	}
	// Retain this reservation's source allocations and time in the release
	// command so the unused hold can be reconciled against its recorded
	// subscription and wallet.
	releaseCommand := *releaseReserveCommand
	releaseCommand.RequestID = batchimage.BatchImageReleaseRequestID(releaseBatchID)
	releaseCommand.RequestFingerprint = ""
	releaseCommand.BalanceHoldAmount = releaseReserved.BalanceAmountUSD
	releaseCommand.SubscriptionHoldAllocations = releaseSubscriptionHolds
	releaseCommand.AllowanceReserved = true

	released, err := repo.Release(ctx, &releaseCommand)
	require.NoError(t, err)
	require.True(t, released.Applied)
	require.NoError(t, integrationDB.QueryRowContext(ctx, `SELECT balance, frozen_balance FROM users WHERE id = $1`, user.ID).Scan(&balance, &frozen))
	require.InDelta(t, 1, balance, 0.000001)
	require.Zero(t, frozen)
	require.NoError(t, integrationDB.QueryRowContext(ctx, `SELECT daily_usage_usd FROM user_subscriptions WHERE id = $1`, subscription.ID).Scan(&dailyUsage))
	require.InDelta(t, 0.7, dailyUsage, 0.000001)

	_, err = integrationDB.ExecContext(ctx, `UPDATE users SET balance = 0.05 WHERE id = $1`, user.ID)
	require.NoError(t, err)
	failedBatchID := "imgbatch_" + strings.ReplaceAll(uuid.NewString(), "-", "")
	insertBatchImageAllowanceTestJob(t, failedBatchID, user.ID, user.ID, apiKey.ID, nil, reservedAt)
	_, err = repo.Reserve(ctx, &billing.TaskFundsCommand{
		RequestID:   batchimage.BatchImageHoldRequestID(failedBatchID),
		APIKeyID:    apiKey.ID,
		UserID:      user.ID,
		ActorUserID: user.ID,

		HoldAmount: 0.5,
		ReservedAt: reservedAt, Task: batchimage.FundingReference(failedBatchID),
	})
	require.ErrorIs(t, err, batchimage.ErrBatchImageInsufficientBalance)
	require.NoError(t, integrationDB.QueryRowContext(ctx, `SELECT balance, frozen_balance FROM users WHERE id = $1`, user.ID).Scan(&balance, &frozen))
	require.InDelta(t, 0.05, balance, 0.000001)
	require.Zero(t, frozen)
	require.NoError(t, integrationDB.QueryRowContext(ctx, `SELECT daily_usage_usd FROM user_subscriptions WHERE id = $1`, subscription.ID).Scan(&dailyUsage))
	require.InDelta(t, 0.7, dailyUsage, 0.000001)
}

// TestUsageBillingRepositoryBatchImagePartialSubscriptionUsesBalanceRate
// reserves one base dollar using a discounted plan and wallet rate. Capture
// reconciles the stored split and settlement scale to the asserted actual cost
// and balances.
func TestUsageBillingRepositoryBatchImagePartialSubscriptionUsesBalanceRate(t *testing.T) {
	ctx := context.Background()
	client := testEntClient(t)
	now := time.Now()
	calendar := timezone.NewCalendarWithClock(time.Local, func() time.Time { return now })
	repo := billing.NewFunds(newSettlementFixtureWithCalendar(integrationDB, calendar))
	user := mustCreateUser(t, client, &identity.User{
		Email:   fmt.Sprintf("batch-partial-rate-%d@example.com", time.Now().UnixNano()),
		Balance: 5,
	})
	group := mustCreateGroup(t, client, &routing.Group{
		Name: "batch-partial-rate-group-" + uuid.NewString(),
	})
	plan := mustCreatePlan(t, client, &billing.SubscriptionPlan{
		Name:                 "batch-partial-rate-plan-" + uuid.NewString(),
		Description:          "batch image partial subscription rate test plan",
		Price:                10,
		ValidityDays:         30,
		ValidityUnit:         "day",
		GroupIDs:             []int64{group.ID},
		GroupRateMultipliers: map[int64]float64{group.ID: 0.5},
		ForSale:              true,
		DailyLimitUSD:        float64Ptr(1),
		WeeklyLimitUSD:       float64Ptr(1),
		MonthlyLimitUSD:      float64Ptr(1),
	})
	reservedAt := now.UTC()
	windowStart := calendar.StartOfDay(reservedAt)
	subscription := mustCreateSubscription(t, client, &billing.UserSubscription{
		UserID:             user.ID,
		PlanID:             plan.ID,
		DailyWindowStart:   &windowStart,
		WeeklyWindowStart:  &windowStart,
		MonthlyWindowStart: &windowStart,
		DailyLimitUSD:      float64Ptr(1),
		WeeklyLimitUSD:     float64Ptr(1),
		MonthlyLimitUSD:    float64Ptr(1),
		DailyUsageUSD:      0.8,
		WeeklyUsageUSD:     0.8,
		MonthlyUsageUSD:    0.8,
	})
	apiKey := mustCreateApiKey(t, client, &apikey.APIKey{
		UserID:  user.ID,
		GroupID: &group.ID,
		Key:     "sk-batch-partial-rate-" + uuid.NewString(),
		Name:    "batch-partial-rate",
	})
	batchID := "imgbatch_" + strings.ReplaceAll(uuid.NewString(), "-", "")
	insertBatchImageAllowanceTestJob(t, batchID, user.ID, user.ID, apiKey.ID, nil, reservedAt)
	reserveCommand := &billing.TaskFundsCommand{
		RequestID:   batchimage.BatchImageHoldRequestID(batchID),
		APIKeyID:    apiKey.ID,
		UserID:      user.ID,
		ActorUserID: user.ID,
		GroupID:     &group.ID,

		HoldAmount:                      0.5,
		PricingSnapshotVersion:          2,
		BaseAmountUSD:                   1,
		SubscriptionRateMultiplier:      1,
		SubscriptionRateMultiplierScale: 1,
		BalanceRateMultiplier:           2,
		SettlementRateScale:             0.5,
		ReservedAt:                      reservedAt, Task: batchimage.FundingReference(batchID),
	}

	reserved, err := repo.Reserve(ctx, reserveCommand)
	require.NoError(t, err)
	// Compare allocation amounts and plan rate ratios with the same small
	// numeric tolerance.
	require.InDelta(t, 0.2, reserved.SubscriptionAmountUSD, 0.000001)
	require.InDelta(t, 1.2, reserved.BalanceAmountUSD, 0.000001)
	require.InDelta(t, 1.4, reserved.HoldAmountUSD, 0.000001)
	require.InDelta(t, 0.4, reserved.EstimatedAmountUSD, 0.000001)
	require.Len(t, reserved.BillingAllocations, 2)
	require.InDelta(t, 0.4, reserved.BillingAllocations[0].BaseAmountUSD, 0.000001)
	require.InDelta(t, 0.5, reserved.BillingAllocations[0].RateMultiplier, 0.000001)

	subscriptionHolds := make([]billing.BillingAllocation, 0, 1)
	for _, allocation := range reserved.BillingAllocations {
		if allocation.Type == billing.BillingAllocationTypeSubscription {
			subscriptionHolds = append(subscriptionHolds, allocation)
		}
	}
	// Copy the reservation identity for capture, then select its capture
	// request ID and reconcile recorded hold amounts so this phase has its
	// own durable claim.
	captureCommand := *reserveCommand
	captureCommand.RequestID = batchimage.BatchImageCaptureRequestID(batchID)
	captureCommand.RequestFingerprint = ""
	captureCommand.HoldAmount = reserved.HoldAmountUSD
	captureCommand.ActualBaseAmountUSD = 1
	captureCommand.BalanceHoldAmount = reserved.BalanceAmountUSD
	captureCommand.SubscriptionHoldAllocations = subscriptionHolds
	captureCommand.AllowanceReserved = true

	captured, err := repo.Capture(ctx, &captureCommand)
	require.NoError(t, err)
	require.InDelta(t, 0.2, captured.SubscriptionAmountUSD, 0.000001)
	require.InDelta(t, 0.2, captured.BalanceAmountUSD, 0.000001)
	require.InDelta(t, 0.4, captured.ActualAmountUSD, 0.000001)

	var balance, frozen, dailyUsage float64
	require.NoError(t, integrationDB.QueryRowContext(ctx, `SELECT balance, frozen_balance FROM users WHERE id = $1`, user.ID).Scan(&balance, &frozen))
	require.InDelta(t, 4.8, balance, 0.000001)
	require.Zero(t, frozen)
	require.NoError(t, integrationDB.QueryRowContext(ctx, `SELECT daily_usage_usd FROM user_subscriptions WHERE id = $1`, subscription.ID).Scan(&dailyUsage))
	require.InDelta(t, 1, dailyUsage, 0.000001)
}

// TestUsageBillingRepositoryBatchImageUnlimitedKeyReleaseKeepsExistingUsage
// starts with spending on an unlimited key before reserving another task.
// Releasing that hold restores the preexisting quota and rolling counters.
func TestUsageBillingRepositoryBatchImageUnlimitedKeyReleaseKeepsExistingUsage(t *testing.T) {
	ctx := context.Background()
	client := testEntClient(t)
	repo := newTaskFundsFixture(integrationDB)
	user := mustCreateUser(t, client, &identity.User{
		Email:   fmt.Sprintf("batch-unlimited-%d@example.com", time.Now().UnixNano()),
		Balance: 10,
	})
	apiKey := mustCreateApiKey(t, client, &apikey.APIKey{
		UserID: user.ID,
		Key:    "sk-batch-unlimited-" + uuid.NewString(),
		Name:   "batch-unlimited",
	})
	// Capture the saved database window start as the reservation time so
	// the later release compares against the same clock and window that
	// held existing usage.
	var reservedAt time.Time
	err := integrationDB.QueryRowContext(ctx, `
		UPDATE api_keys SET quota_used = 1, usage_5h = 1, usage_1d = 1, usage_7d = 1,
		       window_5h_start = NOW(), window_1d_start = date_trunc('day', NOW()), window_7d_start = date_trunc('day', NOW())
		WHERE id = $1 RETURNING window_5h_start`, apiKey.ID).Scan(&reservedAt)
	require.NoError(t, err)

	batchID := "imgbatch_" + strings.ReplaceAll(uuid.NewString(), "-", "")
	insertBatchImageAllowanceTestJob(t, batchID, user.ID, user.ID, apiKey.ID, nil, reservedAt)
	reserveCommand := &billing.TaskFundsCommand{
		RequestID:   batchimage.BatchImageHoldRequestID(batchID),
		APIKeyID:    apiKey.ID,
		UserID:      user.ID,
		ActorUserID: user.ID,

		HoldAmount: 0.8,
		ReservedAt: reservedAt, Task: batchimage.FundingReference(batchID),
	}
	_, err = repo.Reserve(ctx, reserveCommand)
	require.NoError(t, err)

	var quotaUsed, usage5h, usage1d, usage7d float64
	require.NoError(t, integrationDB.QueryRowContext(ctx, `SELECT quota_used, usage_5h, usage_1d, usage_7d FROM api_keys WHERE id = $1`, apiKey.ID).
		Scan(&quotaUsed, &usage5h, &usage1d, &usage7d))
	for _, usage := range []float64{quotaUsed, usage5h, usage1d, usage7d} {
		// Allow micro-dollar differences when checking preexisting
		// spend before and after refunding this later hold.
		require.InDelta(t, 1.8, usage, 0.000001)
	}

	// Retain the original task and reservation time while selecting the
	// release request identity, so allowance refunds target the membership
	// and window that held it.
	releaseCommand := *reserveCommand
	releaseCommand.RequestID = batchimage.BatchImageReleaseRequestID(batchID)
	releaseCommand.AllowanceReserved = true
	releaseCommand.RequestFingerprint = ""
	_, err = repo.Release(ctx, &releaseCommand)
	require.NoError(t, err)
	require.NoError(t, integrationDB.QueryRowContext(ctx, `SELECT quota_used, usage_5h, usage_1d, usage_7d FROM api_keys WHERE id = $1`, apiKey.ID).
		Scan(&quotaUsed, &usage5h, &usage1d, &usage7d))
	for _, usage := range []float64{quotaUsed, usage5h, usage1d, usage7d} {
		require.InDelta(t, 1, usage, 0.000001)
	}
}

// TestUsageBillingRepositoryBatchImageMemberAllowanceSerializesConcurrentReserve
// starts two holds that together exceed a team member's daily allowance.
// Exactly one succeeds, the other is limited, and payer funds match one
// reservation.
func TestUsageBillingRepositoryBatchImageMemberAllowanceSerializesConcurrentReserve(t *testing.T) {
	ctx := context.Background()
	client := testEntClient(t)
	teamRepo := teampostgres.NewTeamRepository(integrationDB, keypostgres.NewTeamKeys(integrationDB), billingpostgres.NewMemberUsageStore(integrationDB, nil))
	repo := newTaskFundsFixture(integrationDB)
	owner := mustCreateUser(t, client, &identity.User{Email: uniqueTeamTestEmail("batch-owner"), Balance: 10})
	member := mustCreateUser(t, client, &identity.User{Email: uniqueTeamTestEmail("batch-member")})
	teamCtx, err := teamRepo.Create(ctx, "批任务并发额度团队", owner.ID, 5)
	require.NoError(t, err)
	token := uuid.NewString()
	_, err = teamRepo.CreateInvitation(ctx, teamCtx.Team.ID, owner.ID, member.Email, token, time.Now().Add(time.Hour))
	require.NoError(t, err)
	_, err = teamRepo.ResolveInvitation(ctx, token, member.ID, member.Email, "accepted", time.Now())
	require.NoError(t, err)
	require.NoError(t, teamRepo.UpdateMemberLimits(ctx, teamCtx.Team.ID, member.ID, 1, 10, 30))
	teamID := teamCtx.Team.ID
	apiKey := mustCreateApiKey(t, client, &apikey.APIKey{
		UserID: member.ID,
		TeamID: &teamID,
		Key:    "sk-batch-member-" + uuid.NewString(),
		Name:   "batch-member",
	})

	commands := make([]*billing.TaskFundsCommand, 2)
	for index := range commands {
		batchID := "imgbatch_" + strings.ReplaceAll(uuid.NewString(), "-", "")
		reservedAt := time.Now().UTC()
		insertBatchImageAllowanceTestJob(t, batchID, member.ID, owner.ID, apiKey.ID, &teamID, reservedAt)
		commands[index] = &billing.TaskFundsCommand{
			RequestID:   batchimage.BatchImageHoldRequestID(batchID),
			APIKeyID:    apiKey.ID,
			UserID:      owner.ID,
			ActorUserID: member.ID,
			TeamID:      &teamID,

			HoldAmount: 0.75,
			ReservedAt: reservedAt, Task: batchimage.FundingReference(batchID),
		}
	}

	// Gate both reservations at one start signal and buffer every result so
	// workers can finish before the test counts the accepted and limited
	// outcomes.
	start := make(chan struct{})
	results := make(chan error, len(commands))
	var waitGroup sync.WaitGroup
	for _, command := range commands {
		waitGroup.Add(1)
		go func(command *billing.TaskFundsCommand) {
			defer waitGroup.Done()
			<-start
			_, reserveErr := repo.Reserve(ctx, command)
			results <- reserveErr
		}(command)
	}
	// Release both reservation attempts and join all sends before closing
	// results; the receiver then counts completed PostgreSQL outcomes
	// without racing another producer.
	close(start)
	waitGroup.Wait()
	close(results)

	var accepted, limited int
	for result := range results {
		switch {
		case result == nil:
			accepted++
		case errors.Is(result, team.ErrTeamMemberDailyExceeded):
			limited++
		default:
			require.NoError(t, result)
		}
	}
	require.Equal(t, 1, accepted)
	require.Equal(t, 1, limited)

	var dailyUsage float64
	require.NoError(t, integrationDB.QueryRowContext(ctx, `SELECT daily_usage_usd FROM team_memberships WHERE team_id = $1 AND user_id = $2 AND left_at IS NULL`, teamID, member.ID).Scan(&dailyUsage))
	// Compare the accepted 0.75 hold with stored member spending and payer
	// funding within a micro-dollar difference.
	require.InDelta(t, 0.75, dailyUsage, 0.000001)
	require.NoError(t, integrationDB.QueryRowContext(ctx, `SELECT balance, frozen_balance FROM users WHERE id = $1`, owner.ID).Scan(&owner.Balance, &owner.FrozenBalance))
	require.InDelta(t, 9.25, owner.Balance, 0.000001)
	require.InDelta(t, 0.75, owner.FrozenBalance, 0.000001)
}

// TestUsageBillingRepositoryBatchImageLegacyJobChargesActualAmount seeds wallet
// freezing for a job whose allowance flag is false. Capture returns unused hold
// funds and records actual cost in wallet and key counters.
func TestUsageBillingRepositoryBatchImageLegacyJobChargesActualAmount(t *testing.T) {
	ctx := context.Background()
	client := testEntClient(t)
	repo := newTaskFundsFixture(integrationDB)
	user := mustCreateUser(t, client, &identity.User{
		Email:   fmt.Sprintf("batch-legacy-%d@example.com", time.Now().UnixNano()),
		Balance: 10,
	})
	apiKey := mustCreateApiKey(t, client, &apikey.APIKey{
		UserID: user.ID,
		Key:    "sk-batch-legacy-" + uuid.NewString(),
		Name:   "batch-legacy",
	})
	batchID := "imgbatch_" + strings.ReplaceAll(uuid.NewString(), "-", "")
	createdAt := time.Now().UTC()
	insertBatchImageAllowanceTestJob(t, batchID, user.ID, user.ID, apiKey.ID, nil, createdAt)

	// Seed the older job's wallet hold directly because its allowance flag
	// remains false; capture must reconcile these frozen funds while
	// charging actual key usage.
	_, err := integrationDB.ExecContext(ctx, `UPDATE users SET balance = balance - 1, frozen_balance = frozen_balance + 1 WHERE id = $1`, user.ID)
	require.NoError(t, err)

	result, err := repo.Capture(ctx, &billing.TaskFundsCommand{
		RequestID:   batchimage.BatchImageCaptureRequestID(batchID),
		APIKeyID:    apiKey.ID,
		UserID:      user.ID,
		ActorUserID: user.ID,

		HoldAmount:         1,
		ActualAmount:       0.4,
		AllowanceReserved:  false,
		ReservedAt:         createdAt,
		RequestPayloadHash: "legacy-job", Task: batchimage.FundingReference(batchID),
	})
	require.NoError(t, err)
	require.True(t, result.Applied)

	var balance, frozen, quotaUsed, usage5h float64
	require.NoError(t, integrationDB.QueryRowContext(ctx, `SELECT balance, frozen_balance FROM users WHERE id = $1`, user.ID).Scan(&balance, &frozen))
	// A micro-dollar tolerance compares the returned hold remainder with
	// wallet and actual key charges.
	require.InDelta(t, 9.6, balance, 0.000001)
	require.InDelta(t, 0, frozen, 0.000001)
	require.NoError(t, integrationDB.QueryRowContext(ctx, `SELECT quota_used, usage_5h FROM api_keys WHERE id = $1`, apiKey.ID).Scan(&quotaUsed, &usage5h))
	require.InDelta(t, 0.4, quotaUsed, 0.000001)
	require.InDelta(t, 0.4, usage5h, 0.000001)
}

// TestUsageBillingRepositoryBatchImageReleaseAfterKeyDeletedAndMemberLeft
// reserves a team hold before removing its member and deleting the key. Release
// refunds payer funds, clears historical member usage, and resets the task
// allowance flag.
func TestUsageBillingRepositoryBatchImageReleaseAfterKeyDeletedAndMemberLeft(t *testing.T) {
	ctx := context.Background()
	client := testEntClient(t)
	teamRepo := teampostgres.NewTeamRepository(integrationDB, keypostgres.NewTeamKeys(integrationDB), billingpostgres.NewMemberUsageStore(integrationDB, nil))
	repo := newTaskFundsFixture(integrationDB)
	owner := mustCreateUser(t, client, &identity.User{Email: uniqueTeamTestEmail("release-owner"), Balance: 10})
	member := mustCreateUser(t, client, &identity.User{Email: uniqueTeamTestEmail("release-member")})
	teamCtx, err := teamRepo.Create(ctx, "离队退款团队", owner.ID, 5)
	require.NoError(t, err)
	token := uuid.NewString()
	_, err = teamRepo.CreateInvitation(ctx, teamCtx.Team.ID, owner.ID, member.Email, token, time.Now().Add(time.Hour))
	require.NoError(t, err)
	_, err = teamRepo.ResolveInvitation(ctx, token, member.ID, member.Email, "accepted", time.Now())
	require.NoError(t, err)
	require.NoError(t, teamRepo.UpdateMemberLimits(ctx, teamCtx.Team.ID, member.ID, 2, 5, 10))
	teamID := teamCtx.Team.ID
	apiKey := mustCreateApiKey(t, client, &apikey.APIKey{
		UserID: member.ID,
		TeamID: &teamID,
		Key:    "sk-batch-release-" + uuid.NewString(),
		Name:   "batch-release",
		Quota:  2,
	})
	batchID := "imgbatch_" + strings.ReplaceAll(uuid.NewString(), "-", "")
	reservedAt := time.Now().UTC()
	insertBatchImageAllowanceTestJob(t, batchID, member.ID, owner.ID, apiKey.ID, &teamID, reservedAt)
	reserveCommand := &billing.TaskFundsCommand{
		RequestID:   batchimage.BatchImageHoldRequestID(batchID),
		APIKeyID:    apiKey.ID,
		UserID:      owner.ID,
		ActorUserID: member.ID,
		TeamID:      &teamID,

		HoldAmount: 0.5,
		ReservedAt: reservedAt, Task: batchimage.FundingReference(batchID),
	}
	_, err = repo.Reserve(ctx, reserveCommand)
	require.NoError(t, err)
	require.NoError(t, teamRepo.RemoveMember(ctx, teamID, member.ID, time.Now()))
	_, err = integrationDB.ExecContext(ctx, `UPDATE api_keys SET deleted_at = NOW(), status = 'disabled' WHERE id = $1`, apiKey.ID)
	require.NoError(t, err)

	// Retain the original task and reservation time while selecting the
	// release request identity, so allowance refunds target the membership
	// and window that held it.
	releaseCommand := *reserveCommand
	releaseCommand.RequestID = batchimage.BatchImageReleaseRequestID(batchID)
	releaseCommand.AllowanceReserved = true
	releaseCommand.RequestFingerprint = ""
	result, err := repo.Release(ctx, &releaseCommand)
	require.NoError(t, err)
	require.True(t, result.Applied)

	var balance, frozen, dailyUsage float64
	var allowanceReserved bool
	require.NoError(t, integrationDB.QueryRowContext(ctx, `SELECT balance, frozen_balance FROM users WHERE id = $1`, owner.ID).Scan(&balance, &frozen))
	// Compare refunded payer funds and the cleared historical member amount
	// within a micro-dollar difference.
	require.InDelta(t, 10, balance, 0.000001)
	require.InDelta(t, 0, frozen, 0.000001)
	require.NoError(t, integrationDB.QueryRowContext(ctx, `SELECT daily_usage_usd FROM team_memberships WHERE team_id = $1 AND user_id = $2`, teamID, member.ID).Scan(&dailyUsage))
	require.InDelta(t, 0, dailyUsage, 0.000001)
	require.NoError(t, integrationDB.QueryRowContext(ctx, `SELECT allowance_reserved FROM batch_image_jobs WHERE batch_id = $1`, batchID).Scan(&allowanceReserved))
	require.False(t, allowanceReserved)
}

// TestUsageBillingRepositoryBatchImageReleaseOnlyUpdatesReservedMembership
// removes and rejoins a member after an earlier task reservation. Release
// refunds the old membership row while retaining usage recorded on the later
// membership.
func TestUsageBillingRepositoryBatchImageReleaseOnlyUpdatesReservedMembership(t *testing.T) {
	ctx := context.Background()
	client := testEntClient(t)
	teamRepo := teampostgres.NewTeamRepository(integrationDB, keypostgres.NewTeamKeys(integrationDB), billingpostgres.NewMemberUsageStore(integrationDB, nil))
	repo := newTaskFundsFixture(integrationDB)
	owner := mustCreateUser(t, client, &identity.User{Email: uniqueTeamTestEmail("release-cycle-owner"), Balance: 10})
	member := mustCreateUser(t, client, &identity.User{Email: uniqueTeamTestEmail("release-cycle-member")})
	teamCtx, err := teamRepo.Create(ctx, "退款成员周期团队", owner.ID, 5)
	require.NoError(t, err)
	firstToken := uuid.NewString()
	_, err = teamRepo.CreateInvitation(ctx, teamCtx.Team.ID, owner.ID, member.Email, firstToken, time.Now().Add(time.Hour))
	require.NoError(t, err)
	_, err = teamRepo.ResolveInvitation(ctx, firstToken, member.ID, member.Email, "accepted", time.Now())
	require.NoError(t, err)
	require.NoError(t, teamRepo.UpdateMemberLimits(ctx, teamCtx.Team.ID, member.ID, 2, 5, 10))
	teamID := teamCtx.Team.ID
	apiKey := mustCreateApiKey(t, client, &apikey.APIKey{
		UserID: member.ID,
		TeamID: &teamID,
		Key:    "sk-batch-release-cycle-" + uuid.NewString(),
		Name:   "batch-release-cycle",
		Quota:  2,
	})
	batchID := "imgbatch_" + strings.ReplaceAll(uuid.NewString(), "-", "")
	reservedAt := time.Now().UTC()
	insertBatchImageAllowanceTestJob(t, batchID, member.ID, owner.ID, apiKey.ID, &teamID, reservedAt)
	reserveCommand := &billing.TaskFundsCommand{
		RequestID:   batchimage.BatchImageHoldRequestID(batchID),
		APIKeyID:    apiKey.ID,
		UserID:      owner.ID,
		ActorUserID: member.ID,
		TeamID:      &teamID,

		HoldAmount: 0.5,
		ReservedAt: reservedAt, Task: batchimage.FundingReference(batchID),
	}
	_, err = repo.Reserve(ctx, reserveCommand)
	require.NoError(t, err)

	// Separate leave and rejoin times from reservation so the stored
	// membership history identifies the older hold and the later row whose
	// usage should survive.
	leftAt := reservedAt.Add(time.Millisecond)
	require.NoError(t, teamRepo.RemoveMember(ctx, teamID, member.ID, leftAt))
	secondToken := uuid.NewString()
	_, err = teamRepo.CreateInvitation(ctx, teamID, owner.ID, member.Email, secondToken, leftAt.Add(time.Hour))
	require.NoError(t, err)
	joinedAgainAt := leftAt.Add(time.Millisecond)
	_, err = teamRepo.ResolveInvitation(ctx, secondToken, member.ID, member.Email, "accepted", joinedAgainAt)
	require.NoError(t, err)
	_, err = integrationDB.ExecContext(ctx, `
		UPDATE team_memberships
		SET daily_usage_usd = 0.4, daily_window_start = date_trunc('day', $3::timestamptz)
		WHERE team_id = $1 AND user_id = $2 AND left_at IS NULL`, teamID, member.ID, reservedAt)
	require.NoError(t, err)

	// Retain the original task and reservation time while selecting the
	// release request identity, so allowance refunds target the membership
	// and window that held it.
	releaseCommand := *reserveCommand
	releaseCommand.RequestID = batchimage.BatchImageReleaseRequestID(batchID)
	releaseCommand.AllowanceReserved = true
	releaseCommand.RequestFingerprint = ""
	_, err = repo.Release(ctx, &releaseCommand)
	require.NoError(t, err)

	rows, err := integrationDB.QueryContext(ctx, `
		SELECT daily_usage_usd FROM team_memberships
		WHERE team_id = $1 AND user_id = $2 ORDER BY joined_at`, teamID, member.ID)
	require.NoError(t, err)
	// Keep the membership-history result open through scanning, then assert
	// its close result during return so query cleanup errors remain visible
	// in this case.
	defer func() { require.NoError(t, rows.Close()) }()
	var usages []float64
	for rows.Next() {
		var usage float64
		require.NoError(t, rows.Scan(&usage))
		usages = append(usages, usage)
	}
	require.NoError(t, rows.Err())
	require.Len(t, usages, 2)
	// The numeric tolerance compares the refunded older membership with
	// retained later-membership spending.
	require.InDelta(t, 0, usages[0], 0.000001)
	require.InDelta(t, 0.4, usages[1], 0.000001)
}

// TestUsageBillingRepositoryBatchImageReleaseKeepsNewWindowConservative
// advances the key's five-hour window after task reservation. Release clears
// the reserved total quota while leaving the newer window's recorded spending
// intact.
func TestUsageBillingRepositoryBatchImageReleaseKeepsNewWindowConservative(t *testing.T) {
	ctx := context.Background()
	client := testEntClient(t)
	repo := newTaskFundsFixture(integrationDB)
	user := mustCreateUser(t, client, &identity.User{
		Email:   fmt.Sprintf("batch-window-%d@example.com", time.Now().UnixNano()),
		Balance: 10,
	})
	apiKey := mustCreateApiKey(t, client, &apikey.APIKey{
		UserID:      user.ID,
		Key:         "sk-batch-window-" + uuid.NewString(),
		Name:        "batch-window",
		Quota:       2,
		RateLimit5h: 2,
	})
	batchID := "imgbatch_" + strings.ReplaceAll(uuid.NewString(), "-", "")
	reservedAt := time.Now().UTC()
	insertBatchImageAllowanceTestJob(t, batchID, user.ID, user.ID, apiKey.ID, nil, reservedAt)
	reserveCommand := &billing.TaskFundsCommand{
		RequestID:   batchimage.BatchImageHoldRequestID(batchID),
		APIKeyID:    apiKey.ID,
		UserID:      user.ID,
		ActorUserID: user.ID,

		HoldAmount: 0.5,
		ReservedAt: reservedAt, Task: batchimage.FundingReference(batchID),
	}
	_, err := repo.Reserve(ctx, reserveCommand)
	require.NoError(t, err)

	// Move the saved start six hours past reservation to model a newer
	// five-hour window; refunding the older hold must leave spending in
	// that newer window intact.
	_, err = integrationDB.ExecContext(ctx, `UPDATE api_keys SET window_5h_start = $2, usage_5h = 0.5 WHERE id = $1`, apiKey.ID, reservedAt.Add(6*time.Hour))
	require.NoError(t, err)

	// Retain the original task and reservation time while selecting the
	// release request identity, so allowance refunds target the membership
	// and window that held it.
	releaseCommand := *reserveCommand
	releaseCommand.RequestID = batchimage.BatchImageReleaseRequestID(batchID)
	releaseCommand.AllowanceReserved = true
	releaseCommand.RequestFingerprint = ""
	_, err = repo.Release(ctx, &releaseCommand)
	require.NoError(t, err)

	var quotaUsed, usage5h float64
	require.NoError(t, integrationDB.QueryRowContext(ctx, `SELECT quota_used, usage_5h FROM api_keys WHERE id = $1`, apiKey.ID).Scan(&quotaUsed, &usage5h))
	// A micro-dollar tolerance distinguishes cleared total quota from the
	// newer window spending retained after release.
	require.InDelta(t, 0, quotaUsed, 0.000001)
	require.InDelta(t, 0.5, usage5h, 0.000001)
}

// newTaskFundsFixture returns the Funds service around shared settlement
// storage for the supplied SQL pool. The caller owns that pool, while
// newSettlementFixture binds task writers and the process-local calendar.
func newTaskFundsFixture(db *sql.DB) *billing.Funds {
	return billing.NewFunds(newSettlementFixture(db))
}
