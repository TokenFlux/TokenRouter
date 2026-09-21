//go:build integration

package billing_test

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	keypostgres "github.com/TokenFlux/TokenRouter/internal/apikey/postgres"
	billingpostgres "github.com/TokenFlux/TokenRouter/internal/billing/postgres"
	identity "github.com/TokenFlux/TokenRouter/internal/identity"
	routing "github.com/TokenFlux/TokenRouter/internal/routing"
	teampostgres "github.com/TokenFlux/TokenRouter/internal/team/postgres"

	"github.com/TokenFlux/TokenRouter/internal/apikey"
	"github.com/TokenFlux/TokenRouter/internal/batchimage"
	"github.com/TokenFlux/TokenRouter/internal/routing/capability"
	"github.com/TokenFlux/TokenRouter/internal/team"

	"github.com/TokenFlux/TokenRouter/internal/billing"
	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
)

func float64Ptr(v float64) *float64 {
	return &v
}

// insertBatchImageAllowanceTestJob 创建额度预记测试所需的最小任务记录。
func insertBatchImageAllowanceTestJob(t *testing.T, batchID string, actorUserID, billingUserID, apiKeyID int64, teamID *int64, createdAt time.Time) {
	t.Helper()
	_, err := integrationDB.ExecContext(context.Background(), `
		INSERT INTO batch_image_jobs (
			batch_id, user_id, billing_user_id, team_id, api_key_id,
			provider, model, item_count, estimated_cost, hold_amount, created_at, updated_at
		) VALUES ($1, $2, $3, $4, $5, 'gemini', 'image-test', 1, 1, 1, $6, $6)`,
		batchID, actorUserID, billingUserID, teamID, apiKeyID, createdAt)
	require.NoError(t, err)
}

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

func TestUsageBillingRepositoryBatchImageSubscriptionFirstReserveAndCapture(t *testing.T) {
	ctx := context.Background()
	client := testEntClient(t)
	repo := newTaskFundsFixture(integrationDB)
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
	reservedAt := time.Now().UTC()
	windowStart := reservedAt.Add(-time.Hour)
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

	releaseReservedAt := time.Now().UTC()
	releaseBatchID := "imgbatch_" + strings.ReplaceAll(uuid.NewString(), "-", "")
	insertBatchImageAllowanceTestJob(t, releaseBatchID, user.ID, user.ID, apiKey.ID, nil, releaseReservedAt)
	releaseReserveCommand := &billing.TaskFundsCommand{
		RequestID:   batchimage.BatchImageHoldRequestID(releaseBatchID),
		APIKeyID:    apiKey.ID,
		UserID:      user.ID,
		ActorUserID: user.ID,

		HoldAmount: 0.5,
		ReservedAt: releaseReservedAt, Task: batchimage.FundingReference(releaseBatchID),
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
	failedReservedAt := time.Now().UTC()
	failedBatchID := "imgbatch_" + strings.ReplaceAll(uuid.NewString(), "-", "")
	insertBatchImageAllowanceTestJob(t, failedBatchID, user.ID, user.ID, apiKey.ID, nil, failedReservedAt)
	_, err = repo.Reserve(ctx, &billing.TaskFundsCommand{
		RequestID:   batchimage.BatchImageHoldRequestID(failedBatchID),
		APIKeyID:    apiKey.ID,
		UserID:      user.ID,
		ActorUserID: user.ID,

		HoldAmount: 0.5,
		ReservedAt: failedReservedAt, Task: batchimage.FundingReference(failedBatchID),
	})
	require.ErrorIs(t, err, batchimage.ErrBatchImageInsufficientBalance)
	require.NoError(t, integrationDB.QueryRowContext(ctx, `SELECT balance, frozen_balance FROM users WHERE id = $1`, user.ID).Scan(&balance, &frozen))
	require.InDelta(t, 0.05, balance, 0.000001)
	require.Zero(t, frozen)
	require.NoError(t, integrationDB.QueryRowContext(ctx, `SELECT daily_usage_usd FROM user_subscriptions WHERE id = $1`, subscription.ID).Scan(&dailyUsage))
	require.InDelta(t, 0.7, dailyUsage, 0.000001)
}

func TestUsageBillingRepositoryBatchImagePartialSubscriptionUsesBalanceRate(t *testing.T) {
	ctx := context.Background()
	client := testEntClient(t)
	repo := newTaskFundsFixture(integrationDB)
	user := mustCreateUser(t, client, &identity.User{
		Email:   fmt.Sprintf("batch-partial-rate-%d@example.com", time.Now().UnixNano()),
		Balance: 5,
	})
	group := mustCreateGroup(t, client, &routing.Group{
		Name:     "batch-partial-rate-group-" + uuid.NewString(),
		Platform: capability.PlatformGemini,
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
	reservedAt := time.Now().UTC()
	windowStart := reservedAt.Add(-time.Hour)
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
	// 预占时间与窗口使用同一数据库时钟，避免主机/容器微小时差破坏本用例的窗口内前提。
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
		require.InDelta(t, 1.8, usage, 0.000001)
	}

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
	require.InDelta(t, 0.75, dailyUsage, 0.000001)
	require.NoError(t, integrationDB.QueryRowContext(ctx, `SELECT balance, frozen_balance FROM users WHERE id = $1`, owner.ID).Scan(&owner.Balance, &owner.FrozenBalance))
	require.InDelta(t, 9.25, owner.Balance, 0.000001)
	require.InDelta(t, 0.75, owner.FrozenBalance, 0.000001)
}

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
	require.InDelta(t, 9.6, balance, 0.000001)
	require.InDelta(t, 0, frozen, 0.000001)
	require.NoError(t, integrationDB.QueryRowContext(ctx, `SELECT quota_used, usage_5h FROM api_keys WHERE id = $1`, apiKey.ID).Scan(&quotaUsed, &usage5h))
	require.InDelta(t, 0.4, quotaUsed, 0.000001)
	require.InDelta(t, 0.4, usage5h, 0.000001)
}

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
	require.InDelta(t, 10, balance, 0.000001)
	require.InDelta(t, 0, frozen, 0.000001)
	require.NoError(t, integrationDB.QueryRowContext(ctx, `SELECT daily_usage_usd FROM team_memberships WHERE team_id = $1 AND user_id = $2`, teamID, member.ID).Scan(&dailyUsage))
	require.InDelta(t, 0, dailyUsage, 0.000001)
	require.NoError(t, integrationDB.QueryRowContext(ctx, `SELECT allowance_reserved FROM batch_image_jobs WHERE batch_id = $1`, batchID).Scan(&allowanceReserved))
	require.False(t, allowanceReserved)
}

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
	defer func() { require.NoError(t, rows.Close()) }()
	var usages []float64
	for rows.Next() {
		var usage float64
		require.NoError(t, rows.Scan(&usage))
		usages = append(usages, usage)
	}
	require.NoError(t, rows.Err())
	require.Len(t, usages, 2)
	require.InDelta(t, 0, usages[0], 0.000001)
	require.InDelta(t, 0.4, usages[1], 0.000001)
}

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

	_, err = integrationDB.ExecContext(ctx, `UPDATE api_keys SET window_5h_start = $2, usage_5h = 0.5 WHERE id = $1`, apiKey.ID, reservedAt.Add(6*time.Hour))
	require.NoError(t, err)

	releaseCommand := *reserveCommand
	releaseCommand.RequestID = batchimage.BatchImageReleaseRequestID(batchID)
	releaseCommand.AllowanceReserved = true
	releaseCommand.RequestFingerprint = ""
	_, err = repo.Release(ctx, &releaseCommand)
	require.NoError(t, err)

	var quotaUsed, usage5h float64
	require.NoError(t, integrationDB.QueryRowContext(ctx, `SELECT quota_used, usage_5h FROM api_keys WHERE id = $1`, apiKey.ID).Scan(&quotaUsed, &usage5h))
	require.InDelta(t, 0, quotaUsed, 0.000001)
	require.InDelta(t, 0.5, usage5h, 0.000001)
}
