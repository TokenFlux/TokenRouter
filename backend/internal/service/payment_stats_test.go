//go:build unit

package service

import (
	"context"
	"testing"
	"time"

	dbent "github.com/BrandonVee/TokenRouter/ent"
	"github.com/BrandonVee/TokenRouter/internal/domain"
	"github.com/BrandonVee/TokenRouter/internal/payment"
	"github.com/stretchr/testify/require"
)

func TestPaymentDashboardStatsWithRangeBuildsPurchaseDistribution(t *testing.T) {
	ctx := context.Background()
	client := newPaymentConfigServiceTestClient(t)

	user, err := client.User.Create().
		SetEmail("buyer@example.com").
		SetPasswordHash("hash").
		SetUsername("buyer").
		Save(ctx)
	require.NoError(t, err)

	paidAt := time.Date(2026, 5, 2, 10, 0, 0, 0, time.UTC)
	plan, err := client.SubscriptionPlan.Create().
		SetName("Pro 版").
		SetPrice(30).
		Save(ctx)
	require.NoError(t, err)
	planID := plan.ID
	createPaidPaymentStatsOrder(t, ctx, client, paymentStatsOrderSeed{
		userID:      user.ID,
		userEmail:   user.Email,
		userName:    user.Username,
		paymentType: payment.TypeAlipay,
		orderType:   payment.OrderTypeBalance,
		status:      OrderStatusCompleted,
		amount:      50,
		paidAt:      paidAt,
		tradeNo:     "balance-in-range",
	})
	createPaidPaymentStatsOrder(t, ctx, client, paymentStatsOrderSeed{
		userID:      user.ID,
		userEmail:   user.Email,
		userName:    user.Username,
		paymentType: payment.TypeStripe,
		orderType:   payment.OrderTypeSubscription,
		status:      OrderStatusPaid,
		amount:      30,
		paidAt:      paidAt.Add(time.Hour),
		tradeNo:     "plan-one",
		planID:      &planID,
		planName:    "专业版",
	})
	createPaidPaymentStatsOrder(t, ctx, client, paymentStatsOrderSeed{
		userID:      user.ID,
		userEmail:   user.Email,
		userName:    user.Username,
		paymentType: payment.TypeStripe,
		orderType:   payment.OrderTypeSubscription,
		status:      OrderStatusRecharging,
		amount:      20,
		paidAt:      paidAt.Add(2 * time.Hour),
		tradeNo:     "plan-two",
		planID:      &planID,
		planName:    "专业版",
	})
	createPaidPaymentStatsOrder(t, ctx, client, paymentStatsOrderSeed{
		userID:      user.ID,
		userEmail:   user.Email,
		userName:    user.Username,
		paymentType: payment.TypeWxpay,
		orderType:   payment.OrderTypeBalance,
		status:      OrderStatusPending,
		amount:      999,
		paidAt:      paidAt,
		tradeNo:     "pending-ignored",
	})
	createPaidPaymentStatsOrder(t, ctx, client, paymentStatsOrderSeed{
		userID:      user.ID,
		userEmail:   user.Email,
		userName:    user.Username,
		paymentType: payment.TypeWxpay,
		orderType:   payment.OrderTypeBalance,
		status:      OrderStatusCompleted,
		amount:      999,
		paidAt:      paidAt.AddDate(0, 0, 5),
		tradeNo:     "outside-range",
	})

	svc := &PaymentService{entClient: client}
	stats, err := svc.GetDashboardStatsWithRange(
		ctx,
		time.Date(2026, 5, 1, 0, 0, 0, 0, time.UTC),
		time.Date(2026, 5, 4, 0, 0, 0, 0, time.UTC),
	)
	require.NoError(t, err)

	require.Equal(t, CurrencyAmounts{"CNY": 100}, stats.TotalAmount)
	require.Equal(t, 3, stats.TotalCount)
	require.Len(t, stats.DailySeries, 3)

	require.Len(t, stats.PurchaseDistribution, 2)
	balanceStat := findPurchaseDistributionStat(t, stats.PurchaseDistribution, payment.OrderTypeBalance)
	require.Equal(t, payment.OrderTypeBalance, balanceStat.Label)
	require.Equal(t, 50.0, balanceStat.Amount)
	require.Equal(t, 1, balanceStat.Count)
	subscriptionStat := findPurchaseDistributionStat(t, stats.PurchaseDistribution, payment.OrderTypeSubscription)
	require.Equal(t, "Pro 版", subscriptionStat.Label)
	require.Equal(t, planID, *subscriptionStat.PlanID)
	require.Equal(t, 50.0, subscriptionStat.Amount)
	require.Equal(t, 2, subscriptionStat.Count)
}

func TestPaymentDashboardStatsWithRangeFallsBackToPlanSnapshotWhenPlanDeleted(t *testing.T) {
	ctx := context.Background()
	client := newPaymentConfigServiceTestClient(t)

	user, err := client.User.Create().
		SetEmail("deleted-plan@example.com").
		SetPasswordHash("hash").
		SetUsername("deleted-plan-user").
		Save(ctx)
	require.NoError(t, err)

	planID := int64(22)
	paidAt := time.Date(2026, 5, 2, 10, 0, 0, 0, time.UTC)
	createPaidPaymentStatsOrder(t, ctx, client, paymentStatsOrderSeed{
		userID:      user.ID,
		userEmail:   user.Email,
		userName:    user.Username,
		paymentType: payment.TypeStripe,
		orderType:   payment.OrderTypeSubscription,
		status:      OrderStatusCompleted,
		amount:      66,
		paidAt:      paidAt,
		tradeNo:     "deleted-plan-order",
		planID:      &planID,
		planName:    "历史套餐",
	})

	svc := &PaymentService{entClient: client}
	stats, err := svc.GetDashboardStatsWithRange(
		ctx,
		time.Date(2026, 5, 1, 0, 0, 0, 0, time.UTC),
		time.Date(2026, 5, 4, 0, 0, 0, 0, time.UTC),
	)
	require.NoError(t, err)

	require.Len(t, stats.PurchaseDistribution, 1)
	require.Equal(t, "历史套餐", stats.PurchaseDistribution[0].Label)
	require.Equal(t, planID, *stats.PurchaseDistribution[0].PlanID)
	require.Equal(t, 66.0, stats.PurchaseDistribution[0].Amount)
	require.Equal(t, 1, stats.PurchaseDistribution[0].Count)
}

func TestPaymentDashboardStatsWithRangeComputesReasoningPointPurchaseUnitPrice(t *testing.T) {
	ctx := context.Background()
	client := newPaymentConfigServiceTestClient(t)

	user, err := client.User.Create().
		SetEmail("unit-price@example.com").
		SetPasswordHash("hash").
		SetUsername("unit-price-user").
		Save(ctx)
	require.NoError(t, err)

	paidAt := time.Date(2026, 5, 2, 10, 0, 0, 0, time.UTC)
	monthlyLimit := 120.0
	createPaidPaymentStatsOrder(t, ctx, client, paymentStatsOrderSeed{
		userID:      user.ID,
		userEmail:   user.Email,
		userName:    user.Username,
		paymentType: payment.TypeAlipay,
		orderType:   payment.OrderTypeBalance,
		status:      OrderStatusCompleted,
		amount:      200,
		payAmount:   103,
		feeAmount:   3,
		paidAt:      paidAt,
		tradeNo:     "balance-with-fee",
	})
	createPaidPaymentStatsOrder(t, ctx, client, paymentStatsOrderSeed{
		userID:       user.ID,
		userEmail:    user.Email,
		userName:     user.Username,
		paymentType:  payment.TypeStripe,
		orderType:    payment.OrderTypeSubscription,
		status:       OrderStatusPaid,
		amount:       60,
		paidAt:       paidAt.Add(time.Hour),
		tradeNo:      "subscription-monthly-limit",
		monthlyLimit: &monthlyLimit,
	})
	createPaidPaymentStatsOrder(t, ctx, client, paymentStatsOrderSeed{
		userID:      user.ID,
		userEmail:   user.Email,
		userName:    user.Username,
		paymentType: payment.TypeStripe,
		orderType:   payment.OrderTypeSubscription,
		status:      OrderStatusCompleted,
		amount:      88,
		paidAt:      paidAt.Add(2 * time.Hour),
		tradeNo:     "subscription-no-monthly-limit",
	})
	createPaidPaymentStatsOrder(t, ctx, client, paymentStatsOrderSeed{
		userID:      user.ID,
		userEmail:   user.Email,
		userName:    user.Username,
		paymentType: payment.TypeWxpay,
		orderType:   payment.OrderTypeBalance,
		status:      OrderStatusPending,
		amount:      999,
		payAmount:   999,
		paidAt:      paidAt,
		tradeNo:     "pending-ignored-by-status",
	})
	createPaidPaymentStatsOrder(t, ctx, client, paymentStatsOrderSeed{
		userID:      user.ID,
		userEmail:   user.Email,
		userName:    user.Username,
		paymentType: payment.TypeWxpay,
		orderType:   payment.OrderTypeBalance,
		status:      OrderStatusCompleted,
		amount:      999,
		payAmount:   999,
		paidAt:      paidAt.AddDate(0, 0, 4),
		tradeNo:     "outside-range-unit-price",
	})

	svc := &PaymentService{entClient: client}
	stats, err := svc.GetDashboardStatsWithRange(
		ctx,
		time.Date(2026, 5, 1, 0, 0, 0, 0, time.UTC),
		time.Date(2026, 5, 4, 0, 0, 0, 0, time.UTC),
	)
	require.NoError(t, err)

	require.Equal(t, 2, stats.ReasoningPointPurchaseOrderCount)
	require.Equal(t, 0.5, stats.AvgReasoningPointPurchaseUnitPrice)
}

func TestComputeBasicStatsGroupsAmountsByCurrency(t *testing.T) {
	t.Parallel()

	todayStart := time.Date(2026, time.July, 25, 0, 0, 0, 0, time.UTC)
	yesterday := todayStart.Add(-time.Hour)
	today := todayStart.Add(time.Hour)
	orders := []*dbent.PaymentOrder{
		paymentStatsTestOrder(1, "alice@example.com", "CNY", 10, &today),
		paymentStatsTestOrder(2, "bob@example.com", "USD", 10, &today),
		paymentStatsTestOrder(1, "alice@example.com", "CNY", 5, &yesterday),
	}

	stats := &DashboardStats{}
	computeBasicStats(stats, orders, todayStart)

	require.Equal(t, CurrencyAmounts{"CNY": 15, "USD": 10}, stats.TotalAmount)
	require.Equal(t, CurrencyAmounts{"CNY": 10, "USD": 10}, stats.TodayAmount)
	require.Equal(t, CurrencyAmounts{"CNY": 7.5, "USD": 10}, stats.AvgAmount)
	require.Equal(t, 3, stats.TotalCount)
	require.Equal(t, 2, stats.TodayCount)
}

func TestPaymentDashboardBreakdownsGroupAmountsAndRankingsByCurrency(t *testing.T) {
	t.Parallel()

	firstDay := time.Date(2026, time.July, 24, 12, 0, 0, 0, time.UTC)
	secondDay := firstDay.AddDate(0, 0, 1)
	orders := []*dbent.PaymentOrder{
		paymentStatsTestOrder(1, "alice@example.com", "CNY", 5.555, &firstDay),
		paymentStatsTestOrder(2, "bob@example.com", "CNY", 10, &firstDay),
		paymentStatsTestOrder(1, "alice@example.com", "USD", 20, &secondDay),
		paymentStatsTestOrder(2, "bob@example.com", "USD", 10, &secondDay),
	}
	orders[0].PaymentType = "stripe"
	orders[1].PaymentType = "stripe"
	orders[2].PaymentType = "stripe"
	orders[3].PaymentType = "alipay"

	dailyEnd := time.Date(2026, time.July, 26, 0, 0, 0, 0, time.UTC)
	daily := buildDailySeries(orders, firstDay, dailyEnd)
	require.Equal(t, []DailyStats{
		{Date: "2026-07-24", Amount: CurrencyAmounts{"CNY": 15.56}, Count: 2},
		{Date: "2026-07-25", Amount: CurrencyAmounts{"USD": 30}, Count: 2},
	}, daily)

	methods := buildMethodDistribution(orders)
	require.Equal(t, []PaymentMethodStat{
		{Type: "alipay", Amount: CurrencyAmounts{"USD": 10}, Count: 1},
		{Type: "stripe", Amount: CurrencyAmounts{"CNY": 15.56, "USD": 20}, Count: 3},
	}, methods)

	users := buildTopUsers(orders)
	require.Equal(t, TopUsersByCurrency{
		"CNY": {
			{UserID: 2, Email: "bob@example.com", Amount: 10},
			{UserID: 1, Email: "alice@example.com", Amount: 5.56},
		},
		"USD": {
			{UserID: 1, Email: "alice@example.com", Amount: 20},
			{UserID: 2, Email: "bob@example.com", Amount: 10},
		},
	}, users)
}

func paymentStatsTestOrder(userID int64, email, currency string, amount float64, paidAt *time.Time) *dbent.PaymentOrder {
	return &dbent.PaymentOrder{
		UserID:           userID,
		UserEmail:        email,
		PayAmount:        amount,
		PaidAt:           paidAt,
		PaymentType:      "stripe",
		ProviderSnapshot: map[string]interface{}{"currency": currency},
	}
}

func findPurchaseDistributionStat(t *testing.T, items []PurchaseDistributionStat, itemType string) PurchaseDistributionStat {
	t.Helper()
	for _, item := range items {
		if item.Type == itemType {
			return item
		}
	}
	t.Fatalf("purchase distribution item %q not found", itemType)
	return PurchaseDistributionStat{}
}

type paymentStatsOrderSeed struct {
	userID       int64
	userEmail    string
	userName     string
	paymentType  string
	orderType    string
	status       string
	amount       float64
	payAmount    float64
	feeAmount    float64
	paidAt       time.Time
	tradeNo      string
	planID       *int64
	planName     string
	monthlyLimit *float64
}

func createPaidPaymentStatsOrder(t *testing.T, ctx context.Context, client *dbent.Client, seed paymentStatsOrderSeed) {
	t.Helper()
	payAmount := seed.payAmount
	if payAmount == 0 {
		payAmount = seed.amount
	}
	create := client.PaymentOrder.Create().
		SetUserID(seed.userID).
		SetUserEmail(seed.userEmail).
		SetUserName(seed.userName).
		SetAmount(seed.amount).
		SetPayAmount(payAmount).
		SetFeeRate(0).
		SetFeeAmount(seed.feeAmount).
		SetRechargeCode("PAY-" + seed.tradeNo).
		SetOutTradeNo(seed.tradeNo).
		SetPaymentType(seed.paymentType).
		SetPaymentTradeNo("trade-" + seed.tradeNo).
		SetOrderType(seed.orderType).
		SetStatus(seed.status).
		SetExpiresAt(seed.paidAt.Add(time.Hour)).
		SetPaidAt(seed.paidAt).
		SetClientIP("127.0.0.1").
		SetSrcHost("api.example.com")
	if seed.planID != nil {
		create.SetPlanID(*seed.planID)
	}
	if seed.orderType == payment.OrderTypeSubscription || seed.planID != nil || seed.planName != "" || seed.monthlyLimit != nil {
		create.SetPlanSnapshot(domain.SubscriptionPlanSnapshot{
			Name:            seed.planName,
			Price:           seed.amount,
			ValidityDays:    30,
			MonthlyLimitUSD: seed.monthlyLimit,
		})
	}
	_, err := create.Save(ctx)
	require.NoError(t, err)
}
