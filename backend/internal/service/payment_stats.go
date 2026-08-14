package service

import (
	"context"
	"encoding/json"
	"log/slog"
	"math"
	"sort"
	"strconv"
	"time"

	dbent "github.com/BrandonVee/TokenRouter/ent"
	"github.com/BrandonVee/TokenRouter/ent/paymentauditlog"
	"github.com/BrandonVee/TokenRouter/ent/paymentorder"
	"github.com/BrandonVee/TokenRouter/ent/subscriptionplan"
	"github.com/BrandonVee/TokenRouter/internal/payment"
)

// --- Dashboard & Analytics ---

func (s *PaymentService) GetDashboardStats(ctx context.Context, days int) (*DashboardStats, error) {
	if days <= 0 {
		days = 30
	}
	now := time.Now()
	todayStart := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, now.Location())
	start := todayStart.AddDate(0, 0, -days+1)
	end := todayStart.AddDate(0, 0, 1)

	return s.GetDashboardStatsWithRange(ctx, start, end)
}

func (s *PaymentService) GetDashboardStatsWithRange(ctx context.Context, start, end time.Time) (*DashboardStats, error) {
	paidStatuses := []string{OrderStatusCompleted, OrderStatusPaid, OrderStatusRecharging}
	todayNow := time.Now().In(start.Location())
	todayStart := time.Date(todayNow.Year(), todayNow.Month(), todayNow.Day(), 0, 0, 0, 0, todayNow.Location())

	orders, err := s.entClient.PaymentOrder.Query().
		Where(
			paymentorder.StatusIn(paidStatuses...),
			paymentorder.PaidAtGTE(start),
			paymentorder.PaidAtLT(end),
		).
		All(ctx)
	if err != nil {
		return nil, err
	}

	st := &DashboardStats{}
	computeBasicStats(st, orders, todayStart)
	if err := s.fillPaymentDashboardTodayStats(ctx, st, todayStart, paidStatuses); err != nil {
		return nil, err
	}

	st.PendingOrders, err = s.entClient.PaymentOrder.Query().
		Where(paymentorder.StatusIn(OrderStatusPending, OrderStatusProcessing)).
		Count(ctx)
	if err != nil {
		return nil, err
	}

	st.DailySeries = buildDailySeries(orders, start, end)
	st.PaymentMethods = buildMethodDistribution(orders)
	computeReasoningPointPurchaseUnitPrice(st, orders)
	planNames, err := s.loadPaymentDashboardPlanNames(ctx, orders)
	if err != nil {
		return nil, err
	}
	st.PurchaseDistribution = buildPurchaseDistribution(orders, planNames)
	st.TopUsers = buildTopUsers(orders)

	return st, nil
}

func (s *PaymentService) fillPaymentDashboardTodayStats(ctx context.Context, st *DashboardStats, todayStart time.Time, paidStatuses []string) error {
	// 今日卡片保持自然日口径，不跟随自定义历史范围变化。
	todayOrders, err := s.entClient.PaymentOrder.Query().
		Where(
			paymentorder.StatusIn(paidStatuses...),
			paymentorder.PaidAtGTE(todayStart),
			paymentorder.PaidAtLT(todayStart.AddDate(0, 0, 1)),
		).
		All(ctx)
	if err != nil {
		return err
	}
	todayAmounts := make(CurrencyAmounts)
	for _, o := range todayOrders {
		todayAmounts[PaymentOrderCurrency(o)] += o.PayAmount
	}
	roundCurrencyAmounts(todayAmounts)
	st.TodayAmount = todayAmounts
	st.TodayCount = len(todayOrders)
	return nil
}

func computeBasicStats(st *DashboardStats, orders []*dbent.PaymentOrder, todayStart time.Time) {
	st.TotalAmount = make(CurrencyAmounts)
	st.TodayAmount = make(CurrencyAmounts)
	st.AvgAmount = make(CurrencyAmounts)
	currencyCounts := make(map[string]int)
	var todayCount int
	for _, o := range orders {
		currency := PaymentOrderCurrency(o)
		st.TotalAmount[currency] += o.PayAmount
		currencyCounts[currency]++
		if o.PaidAt != nil && !o.PaidAt.Before(todayStart) {
			st.TodayAmount[currency] += o.PayAmount
			todayCount++
		}
	}
	st.TotalCount = len(orders)
	st.TodayCount = todayCount
	for currency, totalAmount := range st.TotalAmount {
		st.AvgAmount[currency] = roundAmount(totalAmount / float64(currencyCounts[currency]))
	}
	roundCurrencyAmounts(st.TotalAmount)
	roundCurrencyAmounts(st.TodayAmount)
}

func computeReasoningPointPurchaseUnitPrice(st *DashboardStats, orders []*dbent.PaymentOrder) {
	var totalPrincipal, totalReasoningPoints float64
	var orderCount int
	for _, o := range orders {
		principal, points, ok := paymentDashboardReasoningPointPurchase(o)
		if !ok {
			continue
		}
		totalPrincipal += principal
		totalReasoningPoints += points
		orderCount++
	}
	st.ReasoningPointPurchaseOrderCount = orderCount
	if totalReasoningPoints > 0 {
		st.AvgReasoningPointPurchaseUnitPrice = math.Round(totalPrincipal/totalReasoningPoints*10000) / 10000
	}
}

func paymentDashboardReasoningPointPurchase(o *dbent.PaymentOrder) (principal float64, points float64, ok bool) {
	switch o.OrderType {
	case payment.OrderTypeSubscription:
		// 订阅订单只按月额度快照折算推理积分，避免日/周/无限额套餐混入口径。
		if o.PlanSnapshot.MonthlyLimitUSD == nil || *o.PlanSnapshot.MonthlyLimitUSD <= 0 || o.Amount <= 0 {
			return 0, 0, false
		}
		return o.Amount, *o.PlanSnapshot.MonthlyLimitUSD, true
	default:
		// 余额充值的 amount 是到账推理积分，pay_amount 扣除手续费后得到充值本金。
		principal = o.PayAmount - o.FeeAmount
		if principal <= 0 || o.Amount <= 0 {
			return 0, 0, false
		}
		return principal, o.Amount, true
	}
}

func buildDailySeries(orders []*dbent.PaymentOrder, start, end time.Time) []DailyStats {
	dailyMap := make(map[string]*DailyStats)
	for _, o := range orders {
		if o.PaidAt == nil {
			continue
		}
		date := o.PaidAt.In(start.Location()).Format("2006-01-02")
		ds, ok := dailyMap[date]
		if !ok {
			ds = &DailyStats{Date: date, Amount: make(CurrencyAmounts)}
			dailyMap[date] = ds
		}
		ds.Amount[PaymentOrderCurrency(o)] += o.PayAmount
		ds.Count++
	}
	seriesStart := startOfPaymentStatsDay(start)
	seriesEnd := startOfPaymentStatsDay(end)
	if end.Equal(seriesEnd) {
		seriesEnd = seriesEnd.AddDate(0, 0, -1)
	}
	series := make([]DailyStats, 0)
	for cursor := seriesStart; !cursor.After(seriesEnd); cursor = cursor.AddDate(0, 0, 1) {
		date := cursor.Format("2006-01-02")
		if ds, ok := dailyMap[date]; ok {
			roundCurrencyAmounts(ds.Amount)
			series = append(series, *ds)
		} else {
			series = append(series, DailyStats{Date: date, Amount: make(CurrencyAmounts)})
		}
	}
	return series
}

func startOfPaymentStatsDay(t time.Time) time.Time {
	inLoc := t.In(t.Location())
	return time.Date(inLoc.Year(), inLoc.Month(), inLoc.Day(), 0, 0, 0, 0, inLoc.Location())
}

func buildMethodDistribution(orders []*dbent.PaymentOrder) []PaymentMethodStat {
	methodMap := make(map[string]*PaymentMethodStat)
	for _, o := range orders {
		ms, ok := methodMap[o.PaymentType]
		if !ok {
			ms = &PaymentMethodStat{Type: o.PaymentType, Amount: make(CurrencyAmounts)}
			methodMap[o.PaymentType] = ms
		}
		ms.Amount[PaymentOrderCurrency(o)] += o.PayAmount
		ms.Count++
	}
	methods := make([]PaymentMethodStat, 0, len(methodMap))
	for _, ms := range methodMap {
		roundCurrencyAmounts(ms.Amount)
		methods = append(methods, *ms)
	}
	sort.Slice(methods, func(i, j int) bool {
		return methods[i].Type < methods[j].Type
	})
	return methods
}

func (s *PaymentService) loadPaymentDashboardPlanNames(ctx context.Context, orders []*dbent.PaymentOrder) (map[int64]string, error) {
	planIDSet := make(map[int64]struct{})
	for _, o := range orders {
		if o.OrderType == payment.OrderTypeSubscription && o.PlanID != nil {
			planIDSet[*o.PlanID] = struct{}{}
		}
	}
	if len(planIDSet) == 0 {
		return nil, nil
	}
	planIDs := make([]int64, 0, len(planIDSet))
	for id := range planIDSet {
		planIDs = append(planIDs, id)
	}
	plans, err := s.entClient.SubscriptionPlan.Query().
		Where(subscriptionplan.IDIn(planIDs...)).
		All(ctx)
	if err != nil {
		return nil, err
	}
	planNames := make(map[int64]string, len(plans))
	for _, plan := range plans {
		planNames[plan.ID] = plan.Name
	}
	return planNames, nil
}

func buildPurchaseDistribution(orders []*dbent.PaymentOrder, planNames map[int64]string) []PurchaseDistributionStat {
	distributionMap := make(map[string]*PurchaseDistributionStat)
	for _, o := range orders {
		key, item := purchaseDistributionKey(o, planNames)
		stat, ok := distributionMap[key]
		if !ok {
			stat = item
			distributionMap[key] = stat
		}
		stat.Amount += o.PayAmount
		stat.Count++
	}
	items := make([]PurchaseDistributionStat, 0, len(distributionMap))
	for _, stat := range distributionMap {
		stat.Amount = math.Round(stat.Amount*100) / 100
		items = append(items, *stat)
	}
	sort.Slice(items, func(i, j int) bool {
		if items[i].Amount == items[j].Amount {
			return items[i].Count > items[j].Count
		}
		return items[i].Amount > items[j].Amount
	})
	return items
}

func purchaseDistributionKey(o *dbent.PaymentOrder, planNames map[int64]string) (string, *PurchaseDistributionStat) {
	// 按量充值统一成一个分组，订阅订单按套餐 ID 聚合，名称优先显示当前套餐名。
	if o.OrderType != payment.OrderTypeSubscription {
		return payment.OrderTypeBalance, &PurchaseDistributionStat{Type: payment.OrderTypeBalance, Label: payment.OrderTypeBalance}
	}
	label := o.PlanSnapshot.Name
	if o.PlanID != nil {
		if currentName := planNames[*o.PlanID]; currentName != "" {
			label = currentName
		}
	}
	if label == "" && o.PlanID != nil {
		label = "plan #" + strconv.FormatInt(*o.PlanID, 10)
	}
	if label == "" {
		label = payment.OrderTypeSubscription
	}
	var planID *int64
	key := label
	if o.PlanID != nil {
		id := *o.PlanID
		planID = &id
		key = strconv.FormatInt(id, 10)
	}
	return payment.OrderTypeSubscription + ":" + key, &PurchaseDistributionStat{Type: payment.OrderTypeSubscription, Label: label, PlanID: planID}
}

func buildTopUsers(orders []*dbent.PaymentOrder) TopUsersByCurrency {
	userMap := make(map[string]map[int64]*TopUserStat)
	for _, o := range orders {
		currency := PaymentOrderCurrency(o)
		users, ok := userMap[currency]
		if !ok {
			users = make(map[int64]*TopUserStat)
			userMap[currency] = users
		}
		us, ok := users[o.UserID]
		if !ok {
			us = &TopUserStat{UserID: o.UserID, Email: o.UserEmail}
			users[o.UserID] = us
		}
		us.Amount += o.PayAmount
	}
	result := make(TopUsersByCurrency, len(userMap))
	for currency, users := range userMap {
		userList := make([]*TopUserStat, 0, len(users))
		for _, us := range users {
			us.Amount = roundAmount(us.Amount)
			userList = append(userList, us)
		}
		sort.Slice(userList, func(i, j int) bool {
			return userList[i].Amount > userList[j].Amount
		})
		limit := topUsersLimit
		if len(userList) < limit {
			limit = len(userList)
		}
		result[currency] = make([]TopUserStat, 0, limit)
		for i := 0; i < limit; i++ {
			result[currency] = append(result[currency], *userList[i])
		}
	}
	return result
}

func roundCurrencyAmounts(amounts CurrencyAmounts) {
	for currency, amount := range amounts {
		amounts[currency] = roundAmount(amount)
	}
}

func roundAmount(amount float64) float64 {
	return math.Round(amount*100) / 100
}

// --- Audit Logs ---

func (s *PaymentService) writeAuditLog(ctx context.Context, oid int64, action, op string, detail map[string]any) {
	dj, _ := json.Marshal(detail)
	_, err := s.entClient.PaymentAuditLog.Create().SetOrderID(strconv.FormatInt(oid, 10)).SetAction(action).SetDetail(string(dj)).SetOperator(op).Save(ctx)
	if err != nil {
		slog.Error("audit log failed", "orderID", oid, "action", action, "error", err)
	}
}

func (s *PaymentService) GetOrderAuditLogs(ctx context.Context, oid int64) ([]*dbent.PaymentAuditLog, error) {
	return s.entClient.PaymentAuditLog.Query().Where(paymentauditlog.OrderIDEQ(strconv.FormatInt(oid, 10))).Order(paymentauditlog.ByCreatedAt()).All(ctx)
}
