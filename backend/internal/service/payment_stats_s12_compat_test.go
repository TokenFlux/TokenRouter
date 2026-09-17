//go:build unit

// 旧单测签名转接，新生产实现仅在所属模块；S15/S16 随测试迁移删除。
package service

import (
	"time"

	dbent "github.com/TokenFlux/TokenRouter/ent"
	"github.com/TokenFlux/TokenRouter/internal/payment"
)

func computeBasicStats(st *DashboardStats, orders []*dbent.PaymentOrder, todayStart time.Time) {
	payment.StatsComputeBasicStats(st, paymentOrderValues(orders), todayStart)
}

func buildDailySeries(orders []*dbent.PaymentOrder, start, end time.Time) []DailyStats {
	return payment.StatsBuildDailySeries(paymentOrderValues(orders), start, end)
}

func buildMethodDistribution(orders []*dbent.PaymentOrder) []PaymentMethodStat {
	return payment.StatsBuildMethodDistribution(paymentOrderValues(orders))
}

func buildTopUsers(orders []*dbent.PaymentOrder) TopUsersByCurrency {
	return payment.StatsBuildTopUsers(paymentOrderValues(orders))
}
