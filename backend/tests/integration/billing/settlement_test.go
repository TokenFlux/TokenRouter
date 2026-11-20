//go:build integration

package billing_test

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/lib/pq"
	"github.com/stretchr/testify/require"

	dbent "github.com/TokenFlux/TokenRouter/ent"
	dbprovider "github.com/TokenFlux/TokenRouter/ent/provider"
	"github.com/TokenFlux/TokenRouter/internal/apikey"
	keypostgres "github.com/TokenFlux/TokenRouter/internal/apikey/postgres"
	"github.com/TokenFlux/TokenRouter/internal/billing"
	"github.com/TokenFlux/TokenRouter/internal/billing/postgres"
	"github.com/TokenFlux/TokenRouter/internal/identity"
	infra "github.com/TokenFlux/TokenRouter/internal/infra/postgres"
	"github.com/TokenFlux/TokenRouter/internal/pkg/timezone"
	providercore "github.com/TokenFlux/TokenRouter/internal/provider"
	"github.com/TokenFlux/TokenRouter/internal/routing"
	"github.com/TokenFlux/TokenRouter/internal/routing/capability"
	"github.com/TokenFlux/TokenRouter/internal/scheduler"
	teampostgres "github.com/TokenFlux/TokenRouter/internal/team/postgres"
	usageerrors "github.com/TokenFlux/TokenRouter/internal/usage"
	usagepg "github.com/TokenFlux/TokenRouter/internal/usage/postgres"
)

// Usage settlement allocates completed costs across subscriptions and the
// paying user's balance, then records key and provider counters under a durable
// request identity. PostgreSQL cases inspect overflow debt, plan coverage and
// rates, expiring quota windows, scheduler outbox transitions, and replay after
// deduplication archival. Returned allocation amounts are compared with raw
// stored values to expose partially applied or repeated effects.
//
// Lock-order fixtures hold a subscription row, wait until settlement has locked
// the payer, and start a synchronous usage-log insert that also references that
// subscription. A key-share probe checks compatibility with the payer lock
// before the blocker is released. Buffered completion channels retain both
// results for bounded joins, after which tests inspect funding totals, optional
// member usage, and exactly one usage row.

// usageBillingApplyOutcome carries the settlement report and failure returned
// by an overlapping worker.
type usageBillingApplyOutcome struct {
	// completed settlement effect report
	result *billing.UsageBillingApplyResult
	err    error // settlement worker operation error
}

// directUsageExecutor hides the concrete SQL pool type so usage Create follows
// its synchronous single-row path.
type directUsageExecutor struct {
	// Executor retains the borrowed pool's SQL methods while the wrapper's
	// concrete type prevents the usage repository from initializing its
	// SQL-pool batching path.
	infra.Executor
}

// TestUsageBillingRepositoryApply_DeduplicatesBalanceBilling settles a cost
// exceeding the payer's balance and key quota, then repeats the request.
// Balance debt and key counters reflect one charge with one deduplication row.
func TestUsageBillingRepositoryApply_DeduplicatesBalanceBilling(t *testing.T) {
	ctx := context.Background()
	client := testEntClient(t)
	repo := newSettlementFixture(integrationDB)

	user := mustCreateUser(t, client, &identity.User{
		Email:        fmt.Sprintf("usage-billing-user-%d@example.com", time.Now().UnixNano()),
		PasswordHash: "hash",
		Balance:      1,
	})
	apiKey := mustCreateApiKey(t, client, &apikey.APIKey{
		UserID: user.ID,
		Key:    "sk-usage-billing-" + uuid.NewString(),
		Name:   "billing",
		Quota:  1,
	})
	provider := mustCreateProvider(t, client, &providercore.Record{
		Name: "usage-billing-provider-" + uuid.NewString(),
		Type: capability.ProviderTypeAPIKey,
	})

	requestID := uuid.NewString()
	cmd := &billing.UsageBillingCommand{
		RequestID:           requestID,
		APIKeyID:            apiKey.ID,
		UserID:              user.ID,
		ProviderID:          provider.ID,
		ProviderType:        capability.ProviderTypeAPIKey,
		BillableAmountUSD:   1.25,
		APIKeyQuotaCost:     1.25,
		APIKeyRateLimitCost: 1.25,
	}

	result1, err := repo.Apply(ctx, cmd)
	require.NoError(t, err)
	require.NotNil(t, result1)
	require.True(t, result1.Applied)
	require.True(t, result1.APIKeyQuotaExhausted)
	// Amounts permit a micro-dollar comparison gap when checking one charge
	// and its resulting debt.
	require.InDelta(t, 1.25, result1.BalanceAmountUSD, 0.000001)
	require.NotNil(t, result1.NewBalance)
	require.InDelta(t, -0.25, *result1.NewBalance, 0.000001)

	result2, err := repo.Apply(ctx, cmd)
	require.NoError(t, err)
	require.NotNil(t, result2)
	require.False(t, result2.Applied)

	var balance float64
	require.NoError(t, integrationDB.QueryRowContext(ctx, "SELECT balance FROM users WHERE id = $1", user.ID).Scan(&balance))
	require.InDelta(t, -0.25, balance, 0.000001)

	var quotaUsed float64
	require.NoError(t, integrationDB.QueryRowContext(ctx, "SELECT quota_used FROM api_keys WHERE id = $1", apiKey.ID).Scan(&quotaUsed))
	require.InDelta(t, 1.25, quotaUsed, 0.000001)

	var usage5h float64
	require.NoError(t, integrationDB.QueryRowContext(ctx, "SELECT usage_5h FROM api_keys WHERE id = $1", apiKey.ID).Scan(&usage5h))
	require.InDelta(t, 1.25, usage5h, 0.000001)

	var status string
	require.NoError(t, integrationDB.QueryRowContext(ctx, "SELECT status FROM api_keys WHERE id = $1", apiKey.ID).Scan(&status))
	require.Equal(t, apikey.StatusAPIKeyQuotaExhausted, status)

	var dedupCount int
	require.NoError(t, integrationDB.QueryRowContext(ctx, "SELECT COUNT(*) FROM usage_billing_dedup WHERE request_id = $1 AND api_key_id = $2", requestID, apiKey.ID).Scan(&dedupCount))
	require.Equal(t, 1, dedupCount)
}

// TestUsageBillingRepositoryApply_DeduplicatesSubscriptionBilling applies the
// same automatically funded request twice against an available subscription.
// The first applies, replay does not, and stored daily usage equals one charge.
func TestUsageBillingRepositoryApply_DeduplicatesSubscriptionBilling(t *testing.T) {
	ctx := context.Background()
	client := testEntClient(t)
	repo := newSettlementFixture(integrationDB)

	user := mustCreateUser(t, client, &identity.User{
		Email:        fmt.Sprintf("usage-billing-sub-user-%d@example.com", time.Now().UnixNano()),
		PasswordHash: "hash",
	})
	group := mustCreateGroup(t, client, &routing.Group{
		Name: "usage-billing-group-" + uuid.NewString(),
	})
	plan := mustCreatePlan(t, client, &billing.SubscriptionPlan{
		Name:            "usage-billing-plan-" + uuid.NewString(),
		Description:     "usage billing test plan",
		Price:           19.9,
		ValidityDays:    30,
		ValidityUnit:    "day",
		ForSale:         true,
		DailyLimitUSD:   float64Ptr(100),
		WeeklyLimitUSD:  float64Ptr(200),
		MonthlyLimitUSD: float64Ptr(300),
	})
	apiKey := mustCreateApiKey(t, client, &apikey.APIKey{
		UserID:  user.ID,
		GroupID: &group.ID,
		Key:     "sk-usage-billing-sub-" + uuid.NewString(),
		Name:    "billing-sub",
	})
	subscription := mustCreateSubscription(t, client, &billing.UserSubscription{
		UserID:          user.ID,
		PlanID:          plan.ID,
		DailyLimitUSD:   float64Ptr(100),
		WeeklyLimitUSD:  float64Ptr(200),
		MonthlyLimitUSD: float64Ptr(300),
	})

	requestID := uuid.NewString()
	cmd := &billing.UsageBillingCommand{
		RequestID:         requestID,
		APIKeyID:          apiKey.ID,
		UserID:            user.ID,
		ProviderID:        0,
		BillableAmountUSD: 2.5,
	}

	result1, err := repo.Apply(ctx, cmd)
	require.NoError(t, err)
	require.True(t, result1.Applied)

	result2, err := repo.Apply(ctx, cmd)
	require.NoError(t, err)
	require.False(t, result2.Applied)

	var dailyUsage float64
	require.NoError(t, integrationDB.QueryRowContext(ctx, "SELECT daily_usage_usd FROM user_subscriptions WHERE id = $1", subscription.ID).Scan(&dailyUsage))
	// The stored daily amount allows a micro-dollar difference from the
	// single expected charge.
	require.InDelta(t, 2.5, dailyUsage, 0.000001)
}

// TestUsageBillingRepositoryResolveUsableSubscriptionForGroup creates a
// currently valid subscription whose plan includes the selected group.
// Resolution returns that subscription and its expected plan ID in the
// PostgreSQL fixture.
func TestUsageBillingRepositoryResolveUsableSubscriptionForGroup(t *testing.T) {
	ctx := context.Background()
	client := testEntClient(t)
	repo := newSettlementFixture(integrationDB)
	require.NotNil(t, repo, "原生结算存储已构造")

	user := mustCreateUser(t, client, &identity.User{
		Email:        fmt.Sprintf("usage-billing-resolve-sub-user-%d@example.com", time.Now().UnixNano()),
		PasswordHash: "hash",
	})
	group := mustCreateGroup(t, client, &routing.Group{
		Name: "usage-billing-resolve-group-" + uuid.NewString(),
	})
	plan := mustCreatePlan(t, client, &billing.SubscriptionPlan{
		Name:            "usage-billing-resolve-plan-" + uuid.NewString(),
		Description:     "usage billing resolve test plan",
		Price:           19.9,
		ValidityDays:    30,
		ValidityUnit:    "day",
		GroupIDs:        []int64{group.ID},
		ForSale:         true,
		DailyLimitUSD:   float64Ptr(100),
		MonthlyLimitUSD: float64Ptr(1000),
	})
	subscription := mustCreateSubscription(t, client, &billing.UserSubscription{
		UserID:          user.ID,
		PlanID:          plan.ID,
		DailyLimitUSD:   float64Ptr(100),
		MonthlyLimitUSD: float64Ptr(1000),
	})

	got, err := repo.ResolveUsableSubscriptionForGroup(ctx, user.ID, group.ID)

	require.NoError(t, err)
	require.NotNil(t, got)
	require.Equal(t, subscription.ID, got.ID)
	require.Equal(t, plan.ID, got.PlanID)
}

// TestUsageBillingRepositoryResolveUsableSubscriptionForGroup_NormalizesTailWindows
// supplies exhausted and refreshable tail-window candidates. Resolution selects
// the refreshable grant, clears its daily usage, and retains monthly usage plus
// its plan group rate.
func TestUsageBillingRepositoryResolveUsableSubscriptionForGroup_NormalizesTailWindows(t *testing.T) {
	ctx := context.Background()
	client := testEntClient(t)
	now := time.Now()
	calendar := timezone.NewCalendarWithClock(time.Local, func() time.Time { return now })
	repo := newSettlementFixtureWithCalendar(integrationDB, calendar)
	require.NotNil(t, repo, "原生结算存储已构造")

	user := mustCreateUser(t, client, &identity.User{
		Email:        fmt.Sprintf("usage-billing-resolve-tail-user-%d@example.com", time.Now().UnixNano()),
		PasswordHash: "hash",
	})
	group := mustCreateGroup(t, client, &routing.Group{
		Name: "usage-billing-resolve-tail-group-" + uuid.NewString(),
	})
	blockedPlan := mustCreatePlan(t, client, &billing.SubscriptionPlan{
		Name:            "usage-billing-resolve-blocked-plan-" + uuid.NewString(),
		Description:     "usage billing blocked resolver candidate",
		Price:           9.9,
		ValidityDays:    30,
		ValidityUnit:    "day",
		GroupIDs:        []int64{group.ID},
		ForSale:         true,
		DailyLimitUSD:   float64Ptr(200),
		MonthlyLimitUSD: float64Ptr(1000),
	})
	targetPlan := mustCreatePlan(t, client, &billing.SubscriptionPlan{
		Name:                 "usage-billing-resolve-tail-plan-" + uuid.NewString(),
		Description:          "usage billing tail resolver candidate",
		Price:                19.9,
		ValidityDays:         30,
		ValidityUnit:         "day",
		GroupIDs:             []int64{group.ID},
		GroupRateMultipliers: map[int64]float64{group.ID: 0.5},
		ForSale:              true,
		DailyLimitUSD:        float64Ptr(200),
		MonthlyLimitUSD:      float64Ptr(1000),
	})

	blockedDailyWindowStart := calendar.StartOfDay(now)
	blockedMonthlyWindowStart := now.AddDate(0, 0, -1)
	mustCreateSubscription(t, client, &billing.UserSubscription{
		UserID:             user.ID,
		PlanID:             blockedPlan.ID,
		StartsAt:           now.AddDate(0, 0, -1),
		ExpiresAt:          now.Add(time.Hour),
		DailyWindowStart:   &blockedDailyWindowStart,
		MonthlyWindowStart: &blockedMonthlyWindowStart,
		DailyLimitUSD:      float64Ptr(200),
		MonthlyLimitUSD:    float64Ptr(1000),
		DailyUsageUSD:      200,
		MonthlyUsageUSD:    200,
	})
	targetDailyWindowStart := calendar.StartOfDay(now.AddDate(0, 0, -1))
	targetMonthlyWindowStart := now.AddDate(0, 0, -29)
	target := mustCreateSubscription(t, client, &billing.UserSubscription{
		UserID:             user.ID,
		PlanID:             targetPlan.ID,
		StartsAt:           now.AddDate(0, 0, -30),
		ExpiresAt:          now.Add(2 * time.Hour),
		DailyWindowStart:   &targetDailyWindowStart,
		MonthlyWindowStart: &targetMonthlyWindowStart,
		DailyLimitUSD:      float64Ptr(200),
		MonthlyLimitUSD:    float64Ptr(1000),
		DailyUsageUSD:      200,
		MonthlyUsageUSD:    860,
	})

	got, err := repo.ResolveUsableSubscriptionForGroup(ctx, user.ID, group.ID)

	require.NoError(t, err)
	require.NotNil(t, got)
	require.Equal(t, target.ID, got.ID, "应跳过当前窗口已耗尽的候选并选择可刷新尾段窗口的订阅")
	require.Zero(t, got.DailyUsageUSD, "倍率解析应看到与最终扣费一致的已刷新日窗口")
	require.NotNil(t, got.DailyWindowStart)
	// A small numeric tolerance checks retained monthly usage and the
	// selected plan rate.
	require.InDelta(t, 860, got.MonthlyUsageUSD, 0.000001)
	require.InDelta(t, 0.5, got.Plan.GroupRateMultipliers[group.ID], 0.000001)
}

// TestUsageBillingRepositoryApply_ResetsDailyTailWithinFiniteMonthlyLimit
// charges two hundred during a subscription's final two hours. The reset daily
// window takes remaining monthly capacity and the balance funds the uncovered
// sixty.
func TestUsageBillingRepositoryApply_ResetsDailyTailWithinFiniteMonthlyLimit(t *testing.T) {
	ctx := context.Background()
	client := testEntClient(t)
	repo := newSettlementFixture(integrationDB)
	now := time.Now()

	user := mustCreateUser(t, client, &identity.User{
		Email:        fmt.Sprintf("usage-billing-tail-window-user-%d@example.com", time.Now().UnixNano()),
		PasswordHash: "hash",
		Balance:      100,
	})
	plan := mustCreatePlan(t, client, &billing.SubscriptionPlan{
		Name:            "usage-billing-tail-window-plan-" + uuid.NewString(),
		Description:     "usage billing tail window test plan",
		Price:           19.9,
		ValidityDays:    30,
		ValidityUnit:    "day",
		ForSale:         true,
		DailyLimitUSD:   float64Ptr(200),
		MonthlyLimitUSD: float64Ptr(1000),
	})
	apiKey := mustCreateApiKey(t, client, &apikey.APIKey{
		UserID: user.ID,
		Key:    "sk-usage-billing-tail-window-" + uuid.NewString(),
		Name:   "billing-tail-window",
	})
	dailyWindowStart := now.Add(-25 * time.Hour)
	monthlyWindowStart := now.AddDate(0, 0, -29)
	subscription := mustCreateSubscription(t, client, &billing.UserSubscription{
		UserID:             user.ID,
		PlanID:             plan.ID,
		StartsAt:           now.AddDate(0, 0, -30),
		ExpiresAt:          now.Add(2 * time.Hour),
		DailyWindowStart:   &dailyWindowStart,
		MonthlyWindowStart: &monthlyWindowStart,
		DailyLimitUSD:      float64Ptr(200),
		MonthlyLimitUSD:    float64Ptr(1000),
		DailyUsageUSD:      200,
		MonthlyUsageUSD:    860,
	})

	// A large charge in the final two hours checks that the daily reset
	// still respects the remaining monthly quota.
	result, err := repo.Apply(ctx, &billing.UsageBillingCommand{
		RequestID:         uuid.NewString(),
		APIKeyID:          apiKey.ID,
		UserID:            user.ID,
		BillableAmountUSD: 200,
	})

	require.NoError(t, err)
	require.True(t, result.Applied)
	// Numeric comparisons allow a micro-dollar difference while checking
	// the capped subscription and wallet split.
	require.InDelta(t, 140, result.SubscriptionAmountUSD, 0.000001, "日窗口刷新后仍受剩余月额度约束")
	require.InDelta(t, 60, result.BalanceAmountUSD, 0.000001, "超过月额度的部分应继续走余额")

	var dailyUsage, monthlyUsage float64
	require.NoError(t, integrationDB.QueryRowContext(ctx, `
		SELECT daily_usage_usd, monthly_usage_usd
		FROM user_subscriptions
		WHERE id = $1
	`, subscription.ID).Scan(&dailyUsage, &monthlyUsage))
	require.InDelta(t, 140, dailyUsage, 0.000001)
	require.InDelta(t, 1000, monthlyUsage, 0.000001, "有限月额度必须保持订阅尾段的硬上限")
}

// TestUsageBillingRepositoryApply_DeductsBalanceDeficitAfterPartialSubscription
// settles against nearly exhausted subscription windows and a zero wallet.
// Usage reaches the subscription cap, and uncovered cost becomes persisted
// negative balance.
func TestUsageBillingRepositoryApply_DeductsBalanceDeficitAfterPartialSubscription(t *testing.T) {
	ctx := context.Background()
	client := testEntClient(t)
	now := time.Now()
	calendar := timezone.NewCalendarWithClock(time.Local, func() time.Time { return now })
	repo := newSettlementFixtureWithCalendar(integrationDB, calendar)

	user := mustCreateUser(t, client, &identity.User{
		Email:        fmt.Sprintf("usage-billing-sub-deficit-user-%d@example.com", time.Now().UnixNano()),
		PasswordHash: "hash",
		Balance:      0,
	})
	plan := mustCreatePlan(t, client, &billing.SubscriptionPlan{
		Name:            "usage-billing-deficit-plan-" + uuid.NewString(),
		Description:     "usage billing deficit test plan",
		Price:           19.9,
		ValidityDays:    30,
		ValidityUnit:    "day",
		ForSale:         true,
		DailyLimitUSD:   float64Ptr(10),
		WeeklyLimitUSD:  float64Ptr(10),
		MonthlyLimitUSD: float64Ptr(10),
	})
	apiKey := mustCreateApiKey(t, client, &apikey.APIKey{
		UserID: user.ID,
		Key:    "sk-usage-billing-sub-deficit-" + uuid.NewString(),
		Name:   "billing-sub-deficit",
	})
	windowStart := calendar.StartOfDay(now)
	subscription := mustCreateSubscription(t, client, &billing.UserSubscription{
		UserID:             user.ID,
		PlanID:             plan.ID,
		DailyWindowStart:   &windowStart,
		WeeklyWindowStart:  &windowStart,
		MonthlyWindowStart: &windowStart,
		DailyLimitUSD:      float64Ptr(10),
		WeeklyLimitUSD:     float64Ptr(10),
		MonthlyLimitUSD:    float64Ptr(10),
		DailyUsageUSD:      9.99,
		WeeklyUsageUSD:     9.99,
		MonthlyUsageUSD:    9.99,
	})

	result, err := repo.Apply(ctx, &billing.UsageBillingCommand{
		RequestID:         uuid.NewString(),
		APIKeyID:          apiKey.ID,
		UserID:            user.ID,
		BillableAmountUSD: 1.25,
	})

	require.NoError(t, err)
	require.True(t, result.Applied)
	// Allow a micro-dollar difference when comparing the capped
	// subscription portion and resulting wallet debt.
	require.InDelta(t, 0.01, result.SubscriptionAmountUSD, 0.000001)
	require.InDelta(t, 1.24, result.BalanceAmountUSD, 0.000001)
	require.NotNil(t, result.NewBalance)
	require.InDelta(t, -1.24, *result.NewBalance, 0.000001)

	var balance float64
	require.NoError(t, integrationDB.QueryRowContext(ctx, "SELECT balance FROM users WHERE id = $1", user.ID).Scan(&balance))
	require.InDelta(t, -1.24, balance, 0.000001)

	var dailyUsage float64
	require.NoError(t, integrationDB.QueryRowContext(ctx, "SELECT daily_usage_usd FROM user_subscriptions WHERE id = $1", subscription.ID).Scan(&dailyUsage))
	require.InDelta(t, 10.0, dailyUsage, 0.000001)
}

// TestUsageBillingRepositoryApply_PreferredSubscriptionChargesOverflowToBalance
// uses a selected subscription with distinct subscription and wallet rates.
// Stored windows cap at ten, overflow creates wallet debt, and reported
// allocations match the combined rate.
func TestUsageBillingRepositoryApply_PreferredSubscriptionChargesOverflowToBalance(t *testing.T) {
	ctx := context.Background()
	client := testEntClient(t)
	now := time.Now()
	calendar := timezone.NewCalendarWithClock(time.Local, func() time.Time { return now })
	repo := newSettlementFixtureWithCalendar(integrationDB, calendar)

	user := mustCreateUser(t, client, &identity.User{
		Email:        fmt.Sprintf("usage-billing-sub-overage-user-%d@example.com", time.Now().UnixNano()),
		PasswordHash: "hash",
		Balance:      0,
	})
	plan := mustCreatePlan(t, client, &billing.SubscriptionPlan{
		Name:            "usage-billing-overage-plan-" + uuid.NewString(),
		Description:     "usage billing preferred subscription overage test plan",
		Price:           19.9,
		ValidityDays:    30,
		ValidityUnit:    "day",
		ForSale:         true,
		DailyLimitUSD:   float64Ptr(10),
		WeeklyLimitUSD:  float64Ptr(10),
		MonthlyLimitUSD: float64Ptr(10),
	})
	apiKey := mustCreateApiKey(t, client, &apikey.APIKey{
		UserID: user.ID,
		Key:    "sk-usage-billing-sub-overage-" + uuid.NewString(),
		Name:   "billing-sub-overage",
	})
	windowStart := calendar.StartOfDay(now)
	subscription := mustCreateSubscription(t, client, &billing.UserSubscription{
		UserID:             user.ID,
		PlanID:             plan.ID,
		DailyWindowStart:   &windowStart,
		WeeklyWindowStart:  &windowStart,
		MonthlyWindowStart: &windowStart,
		DailyLimitUSD:      float64Ptr(10),
		WeeklyLimitUSD:     float64Ptr(10),
		MonthlyLimitUSD:    float64Ptr(10),
		DailyUsageUSD:      9.8,
		WeeklyUsageUSD:     9.8,
		MonthlyUsageUSD:    9.8,
	})

	preferredID := subscription.ID
	result, err := repo.Apply(ctx, &billing.UsageBillingCommand{
		RequestID:                       uuid.NewString(),
		APIKeyID:                        apiKey.ID,
		UserID:                          user.ID,
		BillableAmountUSD:               0.5,
		BaseAmountUSD:                   1,
		SubscriptionRateMultiplier:      0.5,
		SubscriptionRateMultiplierScale: 1,
		BalanceRateMultiplier:           2,
		APIKeyBillingMode:               apikey.APIKeyBillingModeSubscription,
		PreferredSubscriptionID:         &preferredID,
	})

	require.NoError(t, err)
	require.True(t, result.Applied)
	// The small comparison tolerance covers allocation amounts and the
	// resulting combined rate ratio.
	require.InDelta(t, 0.2, result.SubscriptionAmountUSD, 0.000001)
	require.InDelta(t, 1.2, result.BalanceAmountUSD, 0.000001)
	require.NotNil(t, result.NewBalance)
	require.InDelta(t, -1.2, *result.NewBalance, 0.000001)
	require.Len(t, result.BillingAllocations, 2)
	require.Equal(t, billing.BillingAllocationTypeSubscription, result.BillingAllocations[0].Type)
	require.InDelta(t, 0.2, result.BillingAllocations[0].AmountUSD, 0.000001)
	require.Equal(t, billing.BillingAllocationTypeBalance, result.BillingAllocations[1].Type)
	require.InDelta(t, 1.2, result.BillingAllocations[1].AmountUSD, 0.000001)
	require.NotNil(t, result.EffectiveRateMultiplier)
	require.InDelta(t, 1.4, *result.EffectiveRateMultiplier, 0.000001)

	var balance float64
	require.NoError(t, integrationDB.QueryRowContext(ctx, "SELECT balance FROM users WHERE id = $1", user.ID).Scan(&balance))
	require.InDelta(t, -1.2, balance, 0.000001, "超出指定订阅额度的部分必须形成余额欠费")

	var dailyUsage, weeklyUsage, monthlyUsage float64
	require.NoError(t, integrationDB.QueryRowContext(ctx, `
		SELECT daily_usage_usd, weekly_usage_usd, monthly_usage_usd
		FROM user_subscriptions
		WHERE id = $1
	`, subscription.ID).Scan(&dailyUsage, &weeklyUsage, &monthlyUsage))
	// Compare all saved windows after overflow settlement so each finite
	// period remains capped at its limit when later requests check
	// subscription admission.
	require.InDelta(t, 10, dailyUsage, 0.000001)
	require.InDelta(t, 10, weeklyUsage, 0.000001)
	require.InDelta(t, 10, monthlyUsage, 0.000001)
}

// TestUsageBillingRepositoryApply_DeadlockLockOrderAllowsConcurrentPersonalUsageLogInsert
// holds a subscription during overlapping settlement and personal usage
// insertion. Both complete after release with the expected funding split and
// one stored usage row.
func TestUsageBillingRepositoryApply_DeadlockLockOrderAllowsConcurrentPersonalUsageLogInsert(t *testing.T) {
	runUsageBillingConcurrentUsageLogInsert(t, false)
}

// TestUsageBillingRepositoryApply_DeadlockLockOrderAllowsConcurrentTeamUsageLogInsert
// holds a subscription during overlapping team settlement and usage insertion.
// Both complete after release with owner funding, member usage, and one stored
// usage row.
func TestUsageBillingRepositoryApply_DeadlockLockOrderAllowsConcurrentTeamUsageLogInsert(t *testing.T) {
	runUsageBillingConcurrentUsageLogInsert(t, true)
}

// runUsageBillingConcurrentUsageLogInsert coordinates overlapping PostgreSQL
// settlement and synchronous usage insertion for a personal or team request. It
// owns a ten-second context and retains a blocker transaction until both
// operations have been launched.
//
// The blocker first locks the subscription so settlement can hold the payer
// while waiting. A NOWAIT probe observes payer-lock contention, and a separate
// KEY SHARE probe checks the lock needed by usage foreign keys. The usage
// insert is then observed incomplete for one hundred milliseconds before
// blocker commit releases both operations.
//
// Buffered result channels allow each worker to report independently of receive
// order. The helper joins both results within its context budget before
// checking subscription, wallet, optional member spending, and persisted usage
// count. Deferred rollback releases the blocker on an early exit.
func runUsageBillingConcurrentUsageLogInsert(t *testing.T, teamRequest bool) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	client := testEntClient(t)
	now := time.Now()
	calendar := timezone.NewCalendarWithClock(time.Local, func() time.Time { return now })
	billingRepo := newSettlementFixtureWithCalendar(integrationDB, calendar)

	owner := mustCreateUser(t, client, &identity.User{
		Email:        fmt.Sprintf("usage-billing-lock-order-%d@example.com", time.Now().UnixNano()),
		PasswordHash: "hash",
		Balance:      10,
	})
	actor := owner
	var teamID *int64
	if teamRequest {
		teamRepo := teampostgres.NewTeamRepository(integrationDB, keypostgres.NewTeamKeys(integrationDB), postgres.NewMemberUsageStore(integrationDB, nil))
		member := mustCreateUser(t, client, &identity.User{Email: uniqueTeamTestEmail("usage-billing-lock-member")})
		teamCtx, err := teamRepo.Create(ctx, "计费锁序团队", owner.ID, 5)
		require.NoError(t, err)
		token := uuid.NewString()
		_, err = teamRepo.CreateInvitation(ctx, teamCtx.Team.ID, owner.ID, member.Email, token, time.Now().Add(time.Hour))
		require.NoError(t, err)
		_, err = teamRepo.ResolveInvitation(ctx, token, member.ID, member.Email, "accepted", time.Now())
		require.NoError(t, err)
		actor = member
		teamIDValue := teamCtx.Team.ID
		teamID = &teamIDValue
	}
	plan := mustCreatePlan(t, client, &billing.SubscriptionPlan{
		Name:            "usage-billing-lock-order-plan-" + uuid.NewString(),
		Description:     "usage billing lock order test plan",
		Price:           10,
		ValidityDays:    30,
		ValidityUnit:    "day",
		ForSale:         true,
		DailyLimitUSD:   float64Ptr(1),
		WeeklyLimitUSD:  float64Ptr(1),
		MonthlyLimitUSD: float64Ptr(1),
	})
	apiKey := mustCreateApiKey(t, client, &apikey.APIKey{
		UserID: actor.ID,
		TeamID: teamID,
		Key:    "sk-usage-billing-lock-order-" + uuid.NewString(),
		Name:   "billing-lock-order",
	})
	provider := mustCreateProvider(t, client, &providercore.Record{
		Name: "usage-billing-lock-order-provider-" + uuid.NewString(),
		Type: capability.ProviderTypeAPIKey,
	})
	windowStart := calendar.StartOfDay(now)
	subscription := mustCreateSubscription(t, client, &billing.UserSubscription{
		UserID:             owner.ID,
		PlanID:             plan.ID,
		DailyWindowStart:   &windowStart,
		WeeklyWindowStart:  &windowStart,
		MonthlyWindowStart: &windowStart,
		DailyLimitUSD:      float64Ptr(1),
		WeeklyLimitUSD:     float64Ptr(1),
		MonthlyLimitUSD:    float64Ptr(1),
		DailyUsageUSD:      0.9,
		WeeklyUsageUSD:     0.9,
		MonthlyUsageUSD:    0.9,
	})

	// Hold the subscription row before settlement starts so the later
	// payer-lock observation occurs while settlement waits on this
	// deliberately blocked subscription.
	blockerTx, err := integrationDB.BeginTx(ctx, nil)
	require.NoError(t, err)
	// Retain rollback until the asserted commit succeeds so an early
	// failure releases the subscription row and lets context-bound
	// competing operations finish.
	blockerReleased := false
	defer func() {
		if !blockerReleased {
			_ = blockerTx.Rollback()
		}
	}()
	var blockedSubscriptionID int64
	require.NoError(t, blockerTx.QueryRowContext(ctx,
		`SELECT id FROM user_subscriptions WHERE id = $1 FOR UPDATE`, subscription.ID,
	).Scan(&blockedSubscriptionID))

	// Buffer settlement completion independently of the test's receive
	// order; the lock probe can report premature completion before the
	// normal result join.
	applyDone := make(chan usageBillingApplyOutcome, 1)
	go func() {
		result, applyErr := billingRepo.Apply(ctx, &billing.UsageBillingCommand{
			RequestID:         uuid.NewString(),
			APIKeyID:          apiKey.ID,
			UserID:            owner.ID,
			ActorUserID:       actor.ID,
			TeamID:            teamID,
			BillableAmountUSD: 1,
		})
		applyDone <- usageBillingApplyOutcome{result: result, err: applyErr}
	}()

	require.NoError(t, waitForUsageBillingUserLock(ctx, owner.ID, applyDone))
	assertUsageBillingUserLockAllowsKeyShare(t, ctx, owner.ID)

	usageRequestID := uuid.NewString()
	usageLog := &usageerrors.UsageLog{
		UserID:         actor.ID,
		BillingUserID:  owner.ID,
		TeamID:         teamID,
		APIKeyID:       apiKey.ID,
		ProviderID:     provider.ID,
		RequestID:      usageRequestID,
		Model:          "gpt-5",
		SubscriptionID: &subscription.ID,
		InputTokens:    10,
		OutputTokens:   5,
		TotalCost:      1,
		ActualCost:     1,
		CreatedAt:      time.Now().UTC(),
	}
	// The wrapper disables usage batching by hiding the concrete SQL pool
	// type, so this worker's completion reflects its synchronous insert and
	// foreign-key waits.
	insertDone := make(chan error, 1)
	go func() {
		_, insertErr := usagepg.NewUsageLogRepositoryWithSQL(client, directUsageExecutor{integrationDB}, timezone.NewCalendar(time.Local)).Create(ctx, usageLog)
		insertDone <- insertErr
	}()

	// Keep the subscription blocker while sampling insert completion for
	// one hundred milliseconds; the subsequent commit should release its
	// foreign-key lock wait.
	select {
	case insertErr := <-insertDone:
		require.FailNow(t, "用量日志应在订阅行被占用时等待", "unexpected error: %v", insertErr)
	case <-time.After(100 * time.Millisecond):
	}

	// Release the subscription blocker before receiving the two operation
	// results, allowing both settlement mutation and usage foreign-key
	// checks to complete their work.
	require.NoError(t, blockerTx.Commit())
	blockerReleased = true

	var outcome usageBillingApplyOutcome
	select {
	case outcome = <-applyDone:
	case <-ctx.Done():
		require.FailNow(t, "等待计费事务完成超时", ctx.Err().Error())
	}
	require.NoError(t, outcome.err)
	require.NotNil(t, outcome.result)
	require.True(t, outcome.result.Applied)
	// A micro-dollar tolerance compares the reported split with saved
	// wallet, subscription, and member amounts.
	require.InDelta(t, 0.1, outcome.result.SubscriptionAmountUSD, 0.000001)
	require.InDelta(t, 0.9, outcome.result.BalanceAmountUSD, 0.000001)

	select {
	case insertErr := <-insertDone:
		require.NoError(t, insertErr)
	case <-ctx.Done():
		require.FailNow(t, "等待用量日志写入完成超时", ctx.Err().Error())
	}

	var balance, dailyUsage float64
	require.NoError(t, integrationDB.QueryRowContext(ctx, `SELECT balance FROM users WHERE id = $1`, owner.ID).Scan(&balance))
	require.NoError(t, integrationDB.QueryRowContext(ctx, `SELECT daily_usage_usd FROM user_subscriptions WHERE id = $1`, subscription.ID).Scan(&dailyUsage))
	require.InDelta(t, 9.1, balance, 0.000001)
	require.InDelta(t, 1.0, dailyUsage, 0.000001)
	if teamID != nil {
		var memberDailyUsage float64
		require.NoError(t, integrationDB.QueryRowContext(ctx, `
			SELECT daily_usage_usd
			FROM team_memberships
			WHERE team_id = $1 AND user_id = $2 AND left_at IS NULL`, *teamID, actor.ID).Scan(&memberDailyUsage))
		require.InDelta(t, 1.0, memberDailyUsage, 0.000001)
	}

	var usageCount int
	require.NoError(t, integrationDB.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM usage_logs WHERE request_id = $1 AND api_key_id = $2`, usageRequestID, apiKey.ID,
	).Scan(&usageCount))
	require.Equal(t, 1, usageCount)
}

// assertUsageBillingUserLockAllowsKeyShare probes the payer row with FOR KEY
// SHARE NOWAIT and fails the test if it cannot acquire that lock. Its probe
// transaction is rolled back before returning.
func assertUsageBillingUserLockAllowsKeyShare(t *testing.T, ctx context.Context, userID int64) {
	t.Helper()
	probeTx, err := integrationDB.BeginTx(ctx, nil)
	require.NoError(t, err)
	// Release the successful key-share probe before the helper returns; an
	// assertion failure also rolls it back, keeping this fixture's extra
	// lock short lived.
	defer func() { _ = probeTx.Rollback() }()
	var lockedUserID int64
	require.NoError(t, probeTx.QueryRowContext(ctx,
		`SELECT id FROM users WHERE id = $1 FOR KEY SHARE NOWAIT`, userID,
	).Scan(&lockedUserID))
}

// waitForUsageBillingUserLock polls the payer row with FOR UPDATE NOWAIT until
// PostgreSQL reports lock contention. Other probe failures return their errors,
// while a completed settlement result or canceled context ends the wait
// unsuccessfully.
//
// Every probe transaction is rolled back before interpreting its result,
// releasing a successfully acquired lock before the next attempt. SQLSTATE
// 55P03 identifies contention and wins immediately when observed. Between
// successful probes, a ten-millisecond ticker avoids spinning and the helper
// can consume applyDone to report premature settlement completion; callers
// account for that consumed result.
func waitForUsageBillingUserLock(ctx context.Context, userID int64, applyDone <-chan usageBillingApplyOutcome) error {
	ticker := time.NewTicker(10 * time.Millisecond)
	// Stop the polling ticker on every return so this helper's retry
	// cadence ends as soon as contention, another failure, or context
	// cancellation is observed.
	defer ticker.Stop()
	for {
		probeTx, err := integrationDB.BeginTx(ctx, nil)
		if err != nil {
			return err
		}
		var lockedUserID int64
		err = probeTx.QueryRowContext(ctx, `SELECT id FROM users WHERE id = $1 FOR UPDATE NOWAIT`, userID).Scan(&lockedUserID)
		// Release a successful probe lock before retrying so the probe
		// cannot become the contention source it is trying to observe
		// on the paying user row.
		_ = probeTx.Rollback()
		if err == nil {
			// This probe acquired the row, so settlement has not
			// yet created observed contention. Wait for its
			// completion signal, context cancellation, or the next
			// tick.
		} else {
			var pgErr *pq.Error
			if errors.As(err, &pgErr) && pgErr != nil && pgErr.Code == "55P03" {
				return nil
			}
			return err
		}

		select {
		case outcome := <-applyDone:
			return fmt.Errorf("计费事务在取得用户锁前结束: %v", outcome.err)
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
		}
	}
}

// TestUsageBillingRepositoryApply_PricesByActualSubscriptionAllocations
// combines a small discounted plan and a larger expensive plan for one group.
// Actual plan allocations determine three dollars of subscription cost and
// leave wallet balance unchanged.
func TestUsageBillingRepositoryApply_PricesByActualSubscriptionAllocations(t *testing.T) {
	ctx := context.Background()
	client := testEntClient(t)
	repo := newSettlementFixture(integrationDB)

	user := mustCreateUser(t, client, &identity.User{
		Email:        fmt.Sprintf("usage-billing-allocation-rate-user-%d@example.com", time.Now().UnixNano()),
		PasswordHash: "hash",
		Balance:      10,
	})
	group := mustCreateGroup(t, client, &routing.Group{
		Name: "usage-billing-allocation-rate-group-" + uuid.NewString(),
	})
	planA := mustCreatePlan(t, client, &billing.SubscriptionPlan{
		Name:                 "usage-billing-allocation-rate-plan-a-" + uuid.NewString(),
		Description:          "discounted plan with little remaining quota",
		Price:                19.9,
		ValidityDays:         30,
		ValidityUnit:         "day",
		GroupIDs:             []int64{group.ID},
		GroupRateMultipliers: map[int64]float64{group.ID: 0.5},
		ForSale:              true,
		DailyLimitUSD:        float64Ptr(1),
		WeeklyLimitUSD:       float64Ptr(1),
		MonthlyLimitUSD:      float64Ptr(1),
	})
	planB := mustCreatePlan(t, client, &billing.SubscriptionPlan{
		Name:                 "usage-billing-allocation-rate-plan-b-" + uuid.NewString(),
		Description:          "higher-rate plan",
		Price:                19.9,
		ValidityDays:         30,
		ValidityUnit:         "day",
		GroupIDs:             []int64{group.ID},
		GroupRateMultipliers: map[int64]float64{group.ID: 2},
		ForSale:              true,
		DailyLimitUSD:        float64Ptr(100),
		WeeklyLimitUSD:       float64Ptr(100),
		MonthlyLimitUSD:      float64Ptr(100),
	})
	apiKey := mustCreateApiKey(t, client, &apikey.APIKey{
		UserID:  user.ID,
		GroupID: &group.ID,
		Key:     "sk-usage-billing-allocation-rate-" + uuid.NewString(),
		Name:    "billing-allocation-rate",
	})
	subA := mustCreateSubscription(t, client, &billing.UserSubscription{
		UserID:          user.ID,
		PlanID:          planA.ID,
		DailyLimitUSD:   float64Ptr(1),
		WeeklyLimitUSD:  float64Ptr(1),
		MonthlyLimitUSD: float64Ptr(1),
	})
	subB := mustCreateSubscription(t, client, &billing.UserSubscription{
		UserID:          user.ID,
		PlanID:          planB.ID,
		DailyLimitUSD:   float64Ptr(100),
		WeeklyLimitUSD:  float64Ptr(100),
		MonthlyLimitUSD: float64Ptr(100),
	})

	result, err := repo.Apply(ctx, &billing.UsageBillingCommand{
		RequestID:                       uuid.NewString(),
		APIKeyID:                        apiKey.ID,
		UserID:                          user.ID,
		GroupID:                         &group.ID,
		BillableAmountUSD:               1.5,
		BaseAmountUSD:                   3,
		SubscriptionRateMultiplier:      1,
		SubscriptionRateMultiplierScale: 1,
		BalanceRateMultiplier:           1,
	})

	require.NoError(t, err)
	require.True(t, result.Applied)
	// Allow a micro-dollar comparison difference across the summed plan
	// cost and each saved source amount.
	require.InDelta(t, 3.0, result.SubscriptionAmountUSD, 0.000001)
	require.InDelta(t, 0.0, result.BalanceAmountUSD, 0.000001)

	var usageA float64
	require.NoError(t, integrationDB.QueryRowContext(ctx, "SELECT daily_usage_usd FROM user_subscriptions WHERE id = $1", subA.ID).Scan(&usageA))
	require.InDelta(t, 1.0, usageA, 0.000001)

	var usageB float64
	require.NoError(t, integrationDB.QueryRowContext(ctx, "SELECT daily_usage_usd FROM user_subscriptions WHERE id = $1", subB.ID).Scan(&usageB))
	require.InDelta(t, 2.0, usageB, 0.000001)

	var balance float64
	require.NoError(t, integrationDB.QueryRowContext(ctx, "SELECT balance FROM users WHERE id = $1", user.ID).Scan(&balance))
	require.InDelta(t, 10.0, balance, 0.000001)
}

// TestUsageBillingRepositoryApply_UsesBalanceRateAfterPartialSubscription
// exhausts a discounted plan partway through three base dollars. The remaining
// base dollar uses the wallet rate, and stored amounts match the reported
// effective multiplier.
func TestUsageBillingRepositoryApply_UsesBalanceRateAfterPartialSubscription(t *testing.T) {
	ctx := context.Background()
	client := testEntClient(t)
	repo := newSettlementFixture(integrationDB)

	user := mustCreateUser(t, client, &identity.User{
		Email:        fmt.Sprintf("usage-billing-partial-rate-user-%d@example.com", time.Now().UnixNano()),
		PasswordHash: "hash",
		Balance:      10,
	})
	group := mustCreateGroup(t, client, &routing.Group{
		Name: "usage-billing-partial-rate-group-" + uuid.NewString(),
	})
	plan := mustCreatePlan(t, client, &billing.SubscriptionPlan{
		Name:                 "usage-billing-partial-rate-plan-" + uuid.NewString(),
		Description:          "subscription covers only part of the base amount",
		Price:                19.9,
		ValidityDays:         30,
		ValidityUnit:         "day",
		GroupIDs:             []int64{group.ID},
		GroupRateMultipliers: map[int64]float64{group.ID: 0.5},
		ForSale:              true,
		DailyLimitUSD:        float64Ptr(1),
		WeeklyLimitUSD:       float64Ptr(1),
		MonthlyLimitUSD:      float64Ptr(1),
	})
	apiKey := mustCreateApiKey(t, client, &apikey.APIKey{
		UserID:  user.ID,
		GroupID: &group.ID,
		Key:     "sk-usage-billing-partial-rate-" + uuid.NewString(),
		Name:    "billing-partial-rate",
	})
	subscription := mustCreateSubscription(t, client, &billing.UserSubscription{
		UserID:          user.ID,
		PlanID:          plan.ID,
		DailyLimitUSD:   float64Ptr(1),
		WeeklyLimitUSD:  float64Ptr(1),
		MonthlyLimitUSD: float64Ptr(1),
	})

	// The plan covers $2 of base cost at 0.5x; the remaining $1 uses the
	// 0.25x balance rate.
	result, err := repo.Apply(ctx, &billing.UsageBillingCommand{
		RequestID:                       uuid.NewString(),
		APIKeyID:                        apiKey.ID,
		UserID:                          user.ID,
		GroupID:                         &group.ID,
		BillableAmountUSD:               0.75,
		BaseAmountUSD:                   3,
		SubscriptionRateMultiplier:      0.25,
		SubscriptionRateMultiplierScale: 1,
		BalanceRateMultiplier:           0.25,
	})

	require.NoError(t, err)
	require.True(t, result.Applied)
	// Compare the source amounts and effective rate with the same small
	// numeric tolerance.
	require.InDelta(t, 1.0, result.SubscriptionAmountUSD, 0.000001)
	require.InDelta(t, 0.25, result.BalanceAmountUSD, 0.000001)
	require.NotNil(t, result.EffectiveRateMultiplier)
	require.InDelta(t, 1.25/3.0, *result.EffectiveRateMultiplier, 0.000001)

	var usage float64
	require.NoError(t, integrationDB.QueryRowContext(ctx, "SELECT daily_usage_usd FROM user_subscriptions WHERE id = $1", subscription.ID).Scan(&usage))
	require.InDelta(t, 1.0, usage, 0.000001)

	var balance float64
	require.NoError(t, integrationDB.QueryRowContext(ctx, "SELECT balance FROM users WHERE id = $1", user.ID).Scan(&balance))
	require.InDelta(t, 9.75, balance, 0.000001)
}

// TestUsageBillingRepositoryApply_UsesOnlySubscriptionPlansContainingRequestGroup
// creates group A and B grants then settles a group B command. Usage remains
// zero on A and increases on B.
func TestUsageBillingRepositoryApply_UsesOnlySubscriptionPlansContainingRequestGroup(t *testing.T) {
	ctx := context.Background()
	client := testEntClient(t)
	repo := newSettlementFixture(integrationDB)

	user := mustCreateUser(t, client, &identity.User{
		Email:        fmt.Sprintf("usage-billing-plan-group-user-%d@example.com", time.Now().UnixNano()),
		PasswordHash: "hash",
		Balance:      10,
	})
	groupA := mustCreateGroup(t, client, &routing.Group{
		Name: "usage-billing-plan-group-a-" + uuid.NewString(),
	})
	groupB := mustCreateGroup(t, client, &routing.Group{
		Name: "usage-billing-plan-group-b-" + uuid.NewString(),
	})
	planA := mustCreatePlan(t, client, &billing.SubscriptionPlan{
		Name:            "usage-billing-plan-a-" + uuid.NewString(),
		Description:     "plan for group A",
		Price:           19.9,
		ValidityDays:    30,
		ValidityUnit:    "day",
		ForSale:         true,
		DailyLimitUSD:   float64Ptr(100),
		WeeklyLimitUSD:  float64Ptr(100),
		MonthlyLimitUSD: float64Ptr(100),
	})
	planB := mustCreatePlan(t, client, &billing.SubscriptionPlan{
		Name:            "usage-billing-plan-b-" + uuid.NewString(),
		Description:     "plan for group B",
		Price:           19.9,
		ValidityDays:    30,
		ValidityUnit:    "day",
		ForSale:         true,
		DailyLimitUSD:   float64Ptr(100),
		WeeklyLimitUSD:  float64Ptr(100),
		MonthlyLimitUSD: float64Ptr(100),
	})
	_, err := integrationDB.ExecContext(ctx, `
		INSERT INTO subscription_plan_groups (plan_id, group_id)
		VALUES ($1, $2), ($3, $4)
	`, planA.ID, groupA.ID, planB.ID, groupB.ID)
	require.NoError(t, err)

	apiKey := mustCreateApiKey(t, client, &apikey.APIKey{
		UserID:  user.ID,
		GroupID: &groupB.ID,
		Key:     "sk-usage-billing-plan-group-" + uuid.NewString(),
		Name:    "billing-plan-group",
	})
	subA := mustCreateSubscription(t, client, &billing.UserSubscription{
		UserID:          user.ID,
		PlanID:          planA.ID,
		DailyLimitUSD:   float64Ptr(100),
		WeeklyLimitUSD:  float64Ptr(100),
		MonthlyLimitUSD: float64Ptr(100),
	})
	subB := mustCreateSubscription(t, client, &billing.UserSubscription{
		UserID:          user.ID,
		PlanID:          planB.ID,
		DailyLimitUSD:   float64Ptr(100),
		WeeklyLimitUSD:  float64Ptr(100),
		MonthlyLimitUSD: float64Ptr(100),
	})

	result, err := repo.Apply(ctx, &billing.UsageBillingCommand{
		RequestID:         uuid.NewString(),
		APIKeyID:          apiKey.ID,
		UserID:            user.ID,
		GroupID:           &groupB.ID,
		BillableAmountUSD: 2.5,
	})

	require.NoError(t, err)
	require.True(t, result.Applied)
	// The micro-dollar tolerance checks the selected grant charge and the
	// untouched other-group amount.
	require.InDelta(t, 2.5, result.SubscriptionAmountUSD, 0.000001)

	var usageA float64
	require.NoError(t, integrationDB.QueryRowContext(ctx, "SELECT daily_usage_usd FROM user_subscriptions WHERE id = $1", subA.ID).Scan(&usageA))
	require.InDelta(t, 0.0, usageA, 0.000001)

	var usageB float64
	require.NoError(t, integrationDB.QueryRowContext(ctx, "SELECT daily_usage_usd FROM user_subscriptions WHERE id = $1", subB.ID).Scan(&usageB))
	require.InDelta(t, 2.5, usageB, 0.000001)
}

// TestUsageBillingRepositoryApply_GlobalPlanAppliesToNewRequestGroup creates an
// unrestricted plan before creating a request group. A later command for that
// group is fully charged to the existing subscription.
func TestUsageBillingRepositoryApply_GlobalPlanAppliesToNewRequestGroup(t *testing.T) {
	ctx := context.Background()
	client := testEntClient(t)
	repo := newSettlementFixture(integrationDB)

	user := mustCreateUser(t, client, &identity.User{
		Email:        fmt.Sprintf("usage-billing-global-plan-user-%d@example.com", time.Now().UnixNano()),
		PasswordHash: "hash",
		Balance:      10,
	})
	plan := mustCreatePlan(t, client, &billing.SubscriptionPlan{
		Name:            "usage-billing-global-plan-" + uuid.NewString(),
		Description:     "global plan applies to future groups",
		Price:           19.9,
		ValidityDays:    30,
		ValidityUnit:    "day",
		ForSale:         true,
		DailyLimitUSD:   float64Ptr(100),
		WeeklyLimitUSD:  float64Ptr(100),
		MonthlyLimitUSD: float64Ptr(100),
	})
	group := mustCreateGroup(t, client, &routing.Group{
		Name: "usage-billing-new-group-" + uuid.NewString(),
	})
	apiKey := mustCreateApiKey(t, client, &apikey.APIKey{
		UserID:  user.ID,
		GroupID: &group.ID,
		Key:     "sk-usage-billing-global-plan-" + uuid.NewString(),
		Name:    "billing-global-plan",
	})
	subscription := mustCreateSubscription(t, client, &billing.UserSubscription{
		UserID:          user.ID,
		PlanID:          plan.ID,
		DailyLimitUSD:   float64Ptr(100),
		WeeklyLimitUSD:  float64Ptr(100),
		MonthlyLimitUSD: float64Ptr(100),
	})

	result, err := repo.Apply(ctx, &billing.UsageBillingCommand{
		RequestID:         uuid.NewString(),
		APIKeyID:          apiKey.ID,
		UserID:            user.ID,
		GroupID:           &group.ID,
		BillableAmountUSD: 2.5,
	})

	require.NoError(t, err)
	require.True(t, result.Applied)
	// A micro-dollar tolerance compares the reported subscription cost with
	// its saved usage.
	require.InDelta(t, 2.5, result.SubscriptionAmountUSD, 0.000001)

	var usage float64
	require.NoError(t, integrationDB.QueryRowContext(ctx, "SELECT daily_usage_usd FROM user_subscriptions WHERE id = $1", subscription.ID).Scan(&usage))
	require.InDelta(t, 2.5, usage, 0.000001)
}

// TestUsageBillingRepositoryApply_UnlimitedSubscriptionDoesNotDeductBalance
// settles a request with an unlimited subscription and zero wallet. The
// subscription funds the cost while the persisted wallet balance and daily
// usage counter remain zero.
func TestUsageBillingRepositoryApply_UnlimitedSubscriptionDoesNotDeductBalance(t *testing.T) {
	ctx := context.Background()
	client := testEntClient(t)
	repo := newSettlementFixture(integrationDB)

	user := mustCreateUser(t, client, &identity.User{
		Email:        fmt.Sprintf("usage-billing-unlimited-sub-user-%d@example.com", time.Now().UnixNano()),
		PasswordHash: "hash",
		Balance:      0,
	})
	plan := mustCreatePlan(t, client, &billing.SubscriptionPlan{
		Name:         "usage-billing-unlimited-plan-" + uuid.NewString(),
		Description:  "usage billing unlimited subscription test plan",
		Price:        19.9,
		ValidityDays: 30,
		ValidityUnit: "day",
		ForSale:      true,
	})
	apiKey := mustCreateApiKey(t, client, &apikey.APIKey{
		UserID: user.ID,
		Key:    "sk-usage-billing-unlimited-sub-" + uuid.NewString(),
		Name:   "billing-unlimited-sub",
	})
	subscription := mustCreateSubscription(t, client, &billing.UserSubscription{
		UserID: user.ID,
		PlanID: plan.ID,
	})

	result, err := repo.Apply(ctx, &billing.UsageBillingCommand{
		RequestID:         uuid.NewString(),
		APIKeyID:          apiKey.ID,
		UserID:            user.ID,
		BillableAmountUSD: 1.25,
	})

	require.NoError(t, err)
	require.True(t, result.Applied)
	// A micro-dollar tolerance checks funded cost alongside zero wallet and
	// finite-window amounts.
	require.InDelta(t, 1.25, result.SubscriptionAmountUSD, 0.000001)
	require.InDelta(t, 0.0, result.BalanceAmountUSD, 0.000001)
	require.Nil(t, result.NewBalance)

	var balance float64
	require.NoError(t, integrationDB.QueryRowContext(ctx, "SELECT balance FROM users WHERE id = $1", user.ID).Scan(&balance))
	require.InDelta(t, 0.0, balance, 0.000001)

	var dailyUsage float64
	require.NoError(t, integrationDB.QueryRowContext(ctx, "SELECT daily_usage_usd FROM user_subscriptions WHERE id = $1", subscription.ID).Scan(&dailyUsage))
	require.InDelta(t, 0.0, dailyUsage, 0.000001)
}

// TestUsageBillingRepositoryApply_RequestFingerprintConflict repeats one
// request and key identity with a different charge amount. The second command
// returns UsageBillingRequestConflict after the first has successfully applied.
func TestUsageBillingRepositoryApply_RequestFingerprintConflict(t *testing.T) {
	ctx := context.Background()
	client := testEntClient(t)
	repo := newSettlementFixture(integrationDB)

	user := mustCreateUser(t, client, &identity.User{
		Email:        fmt.Sprintf("usage-billing-conflict-user-%d@example.com", time.Now().UnixNano()),
		PasswordHash: "hash",
		Balance:      100,
	})
	apiKey := mustCreateApiKey(t, client, &apikey.APIKey{
		UserID: user.ID,
		Key:    "sk-usage-billing-conflict-" + uuid.NewString(),
		Name:   "billing-conflict",
	})

	requestID := uuid.NewString()
	_, err := repo.Apply(ctx, &billing.UsageBillingCommand{
		RequestID:         requestID,
		APIKeyID:          apiKey.ID,
		UserID:            user.ID,
		BillableAmountUSD: 1.25,
	})
	require.NoError(t, err)

	_, err = repo.Apply(ctx, &billing.UsageBillingCommand{
		RequestID:         requestID,
		APIKeyID:          apiKey.ID,
		UserID:            user.ID,
		BillableAmountUSD: 2.50,
	})
	require.ErrorIs(t, err, billing.ErrUsageBillingRequestConflict)
}

// TestUsageBillingRepositoryApply_UpdatesProviderQuota supplies provider quota
// cost with no payer charge in its command. The persisted provider extra data
// records the expected quota usage of 3.5.
func TestUsageBillingRepositoryApply_UpdatesProviderQuota(t *testing.T) {
	ctx := context.Background()
	client := testEntClient(t)
	repo := newSettlementFixture(integrationDB)

	user := mustCreateUser(t, client, &identity.User{
		Email:        fmt.Sprintf("usage-billing-provider-user-%d@example.com", time.Now().UnixNano()),
		PasswordHash: "hash",
	})
	apiKey := mustCreateApiKey(t, client, &apikey.APIKey{
		UserID: user.ID,
		Key:    "sk-usage-billing-provider-" + uuid.NewString(),
		Name:   "billing-provider",
	})
	provider := mustCreateProvider(t, client, &providercore.Record{
		Name: "usage-billing-provider-quota-" + uuid.NewString(),
		Type: capability.ProviderTypeAPIKey,
		Extra: map[string]any{
			"quota_limit": 100.0,
		},
	})

	_, err := repo.Apply(ctx, &billing.UsageBillingCommand{
		RequestID:         uuid.NewString(),
		APIKeyID:          apiKey.ID,
		UserID:            user.ID,
		ProviderID:        provider.ID,
		ProviderType:      capability.ProviderTypeAPIKey,
		ProviderQuotaCost: 3.5,
	})
	require.NoError(t, err)

	var quotaUsed float64
	require.NoError(t, integrationDB.QueryRowContext(ctx, "SELECT COALESCE((extra->>'quota_used')::numeric, 0) FROM providers WHERE id = $1", provider.ID).Scan(&quotaUsed))
	// Compare the numeric value extracted from provider JSON within a
	// micro-dollar difference.
	require.InDelta(t, 3.5, quotaUsed, 0.000001)
}

// TestUsageBillingRepositoryApply_EnqueuesSchedulerOutboxOnQuotaCrossing
// crosses daily and weekly provider quotas in PostgreSQL. Below-limit and later
// above-limit increments retain the expected event count, while each first
// crossing enqueues once.
func TestUsageBillingRepositoryApply_EnqueuesSchedulerOutboxOnQuotaCrossing(t *testing.T) {
	ctx := context.Background()
	client := testEntClient(t)
	repo := newSettlementFixture(integrationDB)

	newFixture := func(t *testing.T, extra map[string]any) (int64, int64) {
		t.Helper()
		user := mustCreateUser(t, client, &identity.User{
			Email:        fmt.Sprintf("usage-billing-outbox-user-%d-%s@example.com", time.Now().UnixNano(), uuid.NewString()),
			PasswordHash: "hash",
		})
		apiKey := mustCreateApiKey(t, client, &apikey.APIKey{
			UserID: user.ID,
			Key:    "sk-usage-billing-outbox-" + uuid.NewString(),
			Name:   "billing-outbox",
		})
		provider := mustCreateProvider(t, client, &providercore.Record{
			Name:  "usage-billing-outbox-" + uuid.NewString(),
			Type:  capability.ProviderTypeAPIKey,
			Extra: extra,
		})
		return apiKey.ID, provider.ID
	}

	outboxCountFor := func(t *testing.T, providerID int64) int {
		t.Helper()
		var count int
		require.NoError(t, integrationDB.QueryRowContext(ctx,
			"SELECT COUNT(*) FROM scheduler_outbox WHERE event_type = $1 AND provider_id = $2",
			scheduler.SchedulerOutboxEventProviderChanged, providerID,
		).Scan(&count))
		return count
	}

	t.Run("daily_first_crossing_enqueues", func(t *testing.T) {
		apiKeyID, providerID := newFixture(t, map[string]any{
			"quota_daily_limit": 10.0,
		})
		// Begin below the configured daily quota so later event counts
		// distinguish the first crossing from ordinary increments in
		// the same provider window.
		_, err := repo.Apply(ctx, &billing.UsageBillingCommand{
			RequestID:         uuid.NewString(),
			APIKeyID:          apiKeyID,
			ProviderID:        providerID,
			ProviderType:      capability.ProviderTypeAPIKey,
			ProviderQuotaCost: 4,
		})
		require.NoError(t, err)
		require.Equal(t, 0, outboxCountFor(t, providerID), "below limit should not enqueue")

		// Add enough cost to cross the saved daily quota; this
		// transition should produce the scheduling event counted for
		// this provider before later increments.
		_, err = repo.Apply(ctx, &billing.UsageBillingCommand{
			RequestID:         uuid.NewString(),
			APIKeyID:          apiKeyID,
			ProviderID:        providerID,
			ProviderType:      capability.ProviderTypeAPIKey,
			ProviderQuotaCost: 8,
		})
		require.NoError(t, err)
		require.Equal(t, 1, outboxCountFor(t, providerID), "crossing daily limit should enqueue once")

		// Add cost after exhaustion is already recorded so the event
		// count can expose duplicate crossing notifications within the
		// same exhausted provider window.
		_, err = repo.Apply(ctx, &billing.UsageBillingCommand{
			RequestID:         uuid.NewString(),
			APIKeyID:          apiKeyID,
			ProviderID:        providerID,
			ProviderType:      capability.ProviderTypeAPIKey,
			ProviderQuotaCost: 2,
		})
		require.NoError(t, err)
		require.Equal(t, 1, outboxCountFor(t, providerID), "subsequent increments beyond limit should not re-enqueue")
	})

	t.Run("weekly_first_crossing_enqueues", func(t *testing.T) {
		apiKeyID, providerID := newFixture(t, map[string]any{
			"quota_weekly_limit": 10.0,
		})
		_, err := repo.Apply(ctx, &billing.UsageBillingCommand{
			RequestID:         uuid.NewString(),
			APIKeyID:          apiKeyID,
			ProviderID:        providerID,
			ProviderType:      capability.ProviderTypeAPIKey,
			ProviderQuotaCost: 15, // Crosses the weekly limit in one charge.
		})
		require.NoError(t, err)
		require.Equal(t, 1, outboxCountFor(t, providerID), "single-shot crossing weekly limit should enqueue once")
	})

	// Monthly exhaustion publishes one scheduling event, and request replay
	// retains the committed counter instead of applying the provider cost twice.
	t.Run("monthly_first_crossing_enqueues", func(t *testing.T) {
		apiKeyID, providerID := newFixture(t, map[string]any{"quota_monthly_limit": 10.0})
		cmd := &billing.UsageBillingCommand{
			RequestID: uuid.NewString(), APIKeyID: apiKeyID, ProviderID: providerID,
			ProviderType: capability.ProviderTypeAPIKey, ProviderQuotaCost: 15,
		}
		result, err := repo.Apply(ctx, cmd)
		require.NoError(t, err)
		require.Equal(t, 15.0, result.QuotaState.MonthlyUsed)
		require.Equal(t, 10.0, result.QuotaState.MonthlyLimit)
		replayed, err := repo.Apply(ctx, cmd)
		require.NoError(t, err)
		require.False(t, replayed.Applied)
		require.Equal(t, 1, outboxCountFor(t, providerID))
	})
}

// TestDashboardAggregationRepositoryCleanupUsageBillingDedup_BatchDeletesOldRows
// supplies old and recent request claims around a cleanup cutoff. Cleanup
// removes the old active row, archives it, and retains the recent claim.
func TestDashboardAggregationRepositoryCleanupUsageBillingDedup_BatchDeletesOldRows(t *testing.T) {
	ctx := context.Background()
	repo := newAggregationFixture(integrationDB)

	oldRequestID := "dedup-old-" + uuid.NewString()
	newRequestID := "dedup-new-" + uuid.NewString()
	// Four hundred elapsed days put the old fixture beyond the one-year
	// cutoff.
	oldCreatedAt := time.Now().UTC().AddDate(0, 0, -400)
	newCreatedAt := time.Now().UTC().Add(-time.Hour)

	_, err := integrationDB.ExecContext(ctx, `
		INSERT INTO usage_billing_dedup (request_id, api_key_id, request_fingerprint, created_at)
		VALUES ($1, 1, $2, $3), ($4, 1, $5, $6)
	`,
		oldRequestID, strings.Repeat("a", 64), oldCreatedAt,
		newRequestID, strings.Repeat("b", 64), newCreatedAt,
	)
	require.NoError(t, err)

	require.NoError(t, repo.CleanupUsageBillingDedup(ctx, time.Now().UTC().AddDate(0, 0, -365)))

	var oldCount int
	require.NoError(t, integrationDB.QueryRowContext(ctx, "SELECT COUNT(*) FROM usage_billing_dedup WHERE request_id = $1", oldRequestID).Scan(&oldCount))
	require.Equal(t, 0, oldCount)

	var newCount int
	require.NoError(t, integrationDB.QueryRowContext(ctx, "SELECT COUNT(*) FROM usage_billing_dedup WHERE request_id = $1", newRequestID).Scan(&newCount))
	require.Equal(t, 1, newCount)

	var archivedCount int
	require.NoError(t, integrationDB.QueryRowContext(ctx, "SELECT COUNT(*) FROM usage_billing_dedup_archive WHERE request_id = $1", oldRequestID).Scan(&archivedCount))
	require.Equal(t, 1, archivedCount)
}

// TestUsageBillingRepositoryApply_DeduplicatesAgainstArchivedKey archives the
// claim for a successfully billed request before retry. Replay remains
// unapplied and the payer balance still reflects exactly one charge.
func TestUsageBillingRepositoryApply_DeduplicatesAgainstArchivedKey(t *testing.T) {
	ctx := context.Background()
	client := testEntClient(t)
	repo := newSettlementFixture(integrationDB)
	aggRepo := newAggregationFixture(integrationDB)

	user := mustCreateUser(t, client, &identity.User{
		Email:        fmt.Sprintf("usage-billing-archive-user-%d@example.com", time.Now().UnixNano()),
		PasswordHash: "hash",
		Balance:      100,
	})
	apiKey := mustCreateApiKey(t, client, &apikey.APIKey{
		UserID: user.ID,
		Key:    "sk-usage-billing-archive-" + uuid.NewString(),
		Name:   "billing-archive",
	})

	requestID := uuid.NewString()
	cmd := &billing.UsageBillingCommand{
		RequestID:         requestID,
		APIKeyID:          apiKey.ID,
		UserID:            user.ID,
		BillableAmountUSD: 1.25,
	}

	result1, err := repo.Apply(ctx, cmd)
	require.NoError(t, err)
	require.True(t, result1.Applied)

	// Age the applied claim before archival so retry reads its durable
	// archived identity after cleanup removes the active claim from the hot
	// deduplication table.
	_, err = integrationDB.ExecContext(ctx, `
		UPDATE usage_billing_dedup
		SET created_at = $1
		WHERE request_id = $2 AND api_key_id = $3
	`, time.Now().UTC().AddDate(0, 0, -400), requestID, apiKey.ID)
	require.NoError(t, err)
	require.NoError(t, aggRepo.CleanupUsageBillingDedup(ctx, time.Now().UTC().AddDate(0, 0, -365)))

	result2, err := repo.Apply(ctx, cmd)
	require.NoError(t, err)
	require.False(t, result2.Applied)

	var balance float64
	require.NoError(t, integrationDB.QueryRowContext(ctx, "SELECT balance FROM users WHERE id = $1", user.ID).Scan(&balance))
	// A micro-dollar tolerance checks the unchanged payer amount after
	// replay of the archived identity.
	require.InDelta(t, 98.75, balance, 0.000001)
}

// mustCreateProvider saves selected provider fields and fills the supplied
// record's stored ID and timestamps. Empty platform, type, and status select
// Anthropic, OAuth, and active; zero concurrency and priority select three and
// fifty, false schedulability becomes true, and nil credential or extra maps
// become empty.
func mustCreateProvider(t *testing.T, client *dbent.Client, a *providercore.Record) *providercore.Record {
	t.Helper()
	ctx := context.Background()

	if a.Platform == "" {
		a.Platform = capability.PlatformAnthropic
	}
	if a.Type == "" {
		a.Type = capability.ProviderTypeOAuth
	}
	if a.Status == "" {
		a.Status = providercore.StatusActive
	}
	if a.Concurrency == 0 {
		a.Concurrency = 3
	}
	if a.Priority == 0 {
		a.Priority = 50
	}
	if !a.Schedulable {
		a.Schedulable = true
	}
	if a.Credentials == nil {
		a.Credentials = map[string]any{}
	}
	if a.Extra == nil {
		a.Extra = map[string]any{}
	}

	create := client.Provider.Create().
		SetName(a.Name).
		SetPlatform(a.Platform).
		SetType(a.Type).
		SetCredentials(a.Credentials).
		SetExtra(a.Extra).
		SetConcurrency(a.Concurrency).
		SetPriority(a.Priority).
		SetStatus(a.Status).
		SetSchedulable(a.Schedulable).
		SetErrorMessage(a.ErrorMessage)

	if a.ProxyID != nil {
		create.SetProxyID(*a.ProxyID)
	}
	if a.LastUsedAt != nil {
		create.SetLastUsedAt(*a.LastUsedAt)
	}
	if a.RateLimitedAt != nil {
		create.SetRateLimitedAt(*a.RateLimitedAt)
	}
	if a.RateLimitResetAt != nil {
		create.SetRateLimitResetAt(*a.RateLimitResetAt)
	}
	if a.OverloadUntil != nil {
		create.SetOverloadUntil(*a.OverloadUntil)
	}
	if a.SessionWindowStart != nil {
		create.SetSessionWindowStart(*a.SessionWindowStart)
	}
	if a.SessionWindowEnd != nil {
		create.SetSessionWindowEnd(*a.SessionWindowEnd)
	}
	if a.SessionWindowStatus != "" {
		create.SetSessionWindowStatus(a.SessionWindowStatus)
	}
	if !a.CreatedAt.IsZero() {
		create.SetCreatedAt(a.CreatedAt)
	}
	if !a.UpdatedAt.IsZero() {
		create.SetUpdatedAt(a.UpdatedAt)
	}
	if a.ParentProviderID != nil {
		create.SetParentProviderID(*a.ParentProviderID)
	}
	if a.QuotaDimension != "" {
		create.SetQuotaDimension(dbprovider.QuotaDimension(a.QuotaDimension))
	}

	created, err := create.Save(ctx)
	require.NoError(t, err, "create provider")

	a.ID = created.ID
	a.CreatedAt = created.CreatedAt
	a.UpdatedAt = created.UpdatedAt
	return a
}

// newAggregationFixture retains q for aggregation and billing-claim archival
// using the process-local calendar. The caller owns its executor lifetime, and
// the bound archival callback uses that same executor for cleanup work.
func newAggregationFixture(q infra.Executor) *usagepg.AggregationStore {
	return usagepg.NewAggregationStoreWithSQL(q, timezone.NewCalendar(time.Local), func(ctx context.Context, t time.Time) error {
		return postgres.ArchiveUsageDedup(ctx, q, t)
	})
}
