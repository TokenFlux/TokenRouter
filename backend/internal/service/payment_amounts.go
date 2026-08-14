package service

import (
	"math"

	dbent "github.com/BrandonVee/TokenRouter/ent"
	"github.com/BrandonVee/TokenRouter/internal/payment"
	"github.com/shopspring/decimal"
)

const defaultBalanceRechargeMultiplier = 1.0

func normalizeBalanceRechargeMultiplier(multiplier float64) float64 {
	if math.IsNaN(multiplier) || math.IsInf(multiplier, 0) || multiplier <= 0 {
		return defaultBalanceRechargeMultiplier
	}
	return multiplier
}

// normalizeSubscriptionUSDToCNYRate 将非法值归一为 0（换算关闭）。
// 与余额倍率不同，0 是合法状态：表示订阅保持 price 直付的存量行为。
func normalizeSubscriptionUSDToCNYRate(rate float64) float64 {
	if math.IsNaN(rate) || math.IsInf(rate, 0) || rate < 0 {
		return 0
	}
	return rate
}

func calculateCreditedBalance(paymentAmount, multiplier float64) float64 {
	return decimal.NewFromFloat(paymentAmount).
		Mul(decimal.NewFromFloat(normalizeBalanceRechargeMultiplier(multiplier))).
		Round(2).
		InexactFloat64()
}

// paymentOrderPurchasedReasoningPoints 返回订单用于邀请返利的推理积分基数。
// 余额订单使用到账积分；30 天订阅套餐按月、周、日的优先级取第一个有效额度。
func paymentOrderPurchasedReasoningPoints(order *dbent.PaymentOrder) (float64, bool) {
	if order == nil {
		return 0, false
	}

	var points float64
	switch order.OrderType {
	case payment.OrderTypeBalance:
		points = order.Amount
	case payment.OrderTypeSubscription:
		limits := []*float64{
			order.PlanSnapshot.MonthlyLimitUSD,
			order.PlanSnapshot.WeeklyLimitUSD,
			order.PlanSnapshot.DailyLimitUSD,
		}
		for _, limit := range limits {
			if limit == nil || *limit <= 0 || math.IsNaN(*limit) || math.IsInf(*limit, 0) {
				continue
			}
			return *limit, true
		}
		return 0, false
	default:
		return 0, false
	}

	if points <= 0 || math.IsNaN(points) || math.IsInf(points, 0) {
		return 0, false
	}
	return points, true
}

func calculateGatewayRefundAmount(orderAmount, payAmount, refundAmount float64, currency string) float64 {
	if orderAmount <= 0 || payAmount <= 0 || refundAmount <= 0 {
		return 0
	}
	fractionDigits := int32(payment.CurrencyMaxFractionDigits(currency))
	if math.Abs(refundAmount-orderAmount) <= paymentAmountToleranceForCurrency(currency) {
		return decimal.NewFromFloat(payAmount).Round(fractionDigits).InexactFloat64()
	}
	return decimal.NewFromFloat(payAmount).
		Mul(decimal.NewFromFloat(refundAmount)).
		Div(decimal.NewFromFloat(orderAmount)).
		Round(fractionDigits).
		InexactFloat64()
}
