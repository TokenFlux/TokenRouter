// 下单快照与限额比较保持原金额算法及读取后的校验顺序。
package payment

import (
	"fmt"
	"math"
	"strconv"
	"strings"

	"github.com/TokenFlux/TokenRouter/internal/billing"
	"github.com/TokenFlux/TokenRouter/internal/pkg/apperror"
)

func BuildCheckoutDraft(req CreateOrderRequest, user *Buyer, plan *SubscriptionPlan, cfg *PaymentConfig, amount, limitAmount float64, fee FeeBreakdown, sel *InstanceSelection) CheckoutDraft {
	order := Order{
		UserID:           req.UserID,
		UserEmail:        user.Email,
		UserName:         user.Username,
		UserNotes:        NilIfEmpty(user.Notes),
		Amount:           amount,
		PayAmount:        fee.PayAmount,
		FeeRate:          fee.FeeRate,
		FeeFixed:         fee.FixedFee,
		FeeRateAmount:    fee.FeeRateAmount,
		FeeAmount:        fee.FeeAmount,
		PaymentType:      req.PaymentType,
		OrderType:        req.OrderType,
		ClientIP:         req.ClientIP,
		SrcHost:          req.SrcHost,
		SrcURL:           NilIfEmpty(req.SrcURL),
		ProviderSnapshot: BuildPaymentOrderProviderSnapshot(sel, req),
		BillingSnapshot:  BillingInfoSnapshot(req.BillingInfo),
	}
	if sel != nil {
		order.ProviderInstanceID = NilIfEmpty(strings.TrimSpace(sel.InstanceID))
		order.ProviderKey = NilIfEmpty(strings.TrimSpace(sel.ProviderKey))
	}
	if plan != nil {
		id := plan.ID
		order.PlanID = &id
		order.PlanSnapshot = billing.SubscriptionPlanSnapshot{
			Name:            plan.Name,
			Price:           plan.Price,
			Currency:        plan.Currency,
			ValidityDays:    billing.ComputeValidityDays(plan.ValidityDays, plan.ValidityUnit),
			DailyLimitUSD:   plan.DailyLimitUSD,
			WeeklyLimitUSD:  plan.WeeklyLimitUSD,
			MonthlyLimitUSD: plan.MonthlyLimitUSD,
		}
	}
	timeout := cfg.OrderTimeoutMin
	if timeout <= 0 {
		timeout = ConfigDefaultOrderTimeoutMin
	}
	return CheckoutDraft{
		Order:          *order.Clone(),
		MaxPending:     cfg.MaxPendingOrders,
		DailyLimit:     cfg.DailyLimit,
		LimitAmount:    limitAmount,
		TimeoutMinutes: timeout,
	}
}
func ValidateCheckoutPending(count, max int) error {
	if max <= 0 {
		max = ConfigDefaultMaxPendingOrders
	}
	if count >= max {
		return apperror.TooManyRequests("TOO_MANY_PENDING", "too_many_pending").WithMetadata(map[string]string{"max": strconv.Itoa(max)})
	}
	return nil
}
func ValidateCheckoutDaily(orders []*Order, amount, limit float64) error {
	var used float64
	for _, o := range orders {
		if o.OrderType == OrderTypeBalance {
			used += o.PayAmount
			continue
		}
		used += o.Amount
	}
	if used+amount > limit {
		return apperror.TooManyRequests("DAILY_LIMIT_EXCEEDED", "daily_limit_exceeded").WithMetadata(map[string]string{"remaining": fmt.Sprintf("%.2f", math.Max(0, limit-used))})
	}
	return nil
}

// RefundStringValue 供存储投影共享可空字段读取，不复制订单规则。
func RefundStringValue(v *string) string { return refundStringValue(v) }
