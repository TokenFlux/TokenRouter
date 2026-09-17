// 退款报文与恢复计划保留原比例、币种及商户身份规则。
package payment

import (
	"context"
	"fmt"
	"strings"
)

func PaymentOrderSubscriptionValidityDays(order *Order) int {
	if order == nil {
		return 0
	}
	if order.PlanSnapshot.ValidityDays > 0 {
		return order.PlanSnapshot.ValidityDays
	}
	return 0
}
func (s *RefundWorkflow) GwRefund(ctx context.Context, p *RefundPlan) (*RefundResponse, error) {
	if p.Order.PaymentTradeNo == "" {
		s.runtime.Audit(ctx, p.Order.ID, "REFUND_NO_TRADE_NO", "admin", map[string]any{"detail": "skipped"})
		return &RefundResponse{Status: ProviderStatusSuccess}, nil
	}

	// Use the exact provider instance that created this order, not a random one
	// from the registry. Each instance has its own merchant credentials.
	prov, err := s.runtime.Provider(ctx, p.Order)
	if err != nil {
		return nil, fmt.Errorf("get refund provider: %w", err)
	}
	if err := ValidateProviderSnapshotMetadata(p.Order, prov.ProviderKey(), ProviderMerchantIdentityMetadata(prov)); err != nil {
		s.runtime.Audit(ctx, p.Order.ID, "REFUND_PROVIDER_METADATA_MISMATCH", "admin", map[string]any{
			"detail": err.Error(),
		})
		return nil, err
	}
	finishProviderCall := s.runtime.Observe(ctx)
	resp, err := prov.Refund(ctx, RefundRequest{
		TradeNo: p.Order.PaymentTradeNo,
		OrderID: p.Order.OutTradeNo,
		Amount:  FormatGatewayRefundAmount(p.GatewayAmount, p.Order),
		Reason:  p.Reason,
	})
	finishProviderCall()
	if err != nil {
		if resp != nil && strings.TrimSpace(resp.Status) == ProviderStatusPending {
			return resp, nil
		}
		return nil, err
	}
	if err := ValidateRefundProviderResponse(resp); err != nil {
		return nil, err
	}
	return resp, nil
}
func FormatGatewayRefundAmount(amount float64, order *Order) string {
	return FormatAmountForCurrency(amount, PaymentOrderCurrency(order))
}
func ValidateRefundProviderResponse(resp *RefundResponse) error {
	if resp == nil {
		return fmt.Errorf("payment refund response missing")
	}
	status := strings.TrimSpace(resp.Status)
	switch status {
	case ProviderStatusSuccess, ProviderStatusRefunded, ProviderStatusPending:
		return nil
	case ProviderStatusFailed:
		return fmt.Errorf("payment refund failed: status %s", status)
	default:
		return fmt.Errorf("payment refund returned unknown status: %s", status)
	}
}
func (s *RefundWorkflow) RefundFinalizePlan(o *Order, detail RefundPendingDetail) *RefundPlan {
	refundAmount := o.RefundAmount
	reason := strings.TrimSpace(refundStringValue(o.RefundReason))
	if reason == "" {
		reason = fmt.Sprintf("refund order:%d", o.ID)
	}
	p := &RefundPlan{
		OrderID:     o.ID,
		OperationID: detail.OperationID, ChannelRefundID: detail.RefundID,
		Order:         o,
		RefundAmount:  refundAmount,
		GatewayAmount: CalculateGatewayRefundAmount(o.Amount, o.PayAmount, refundAmount, PaymentOrderCurrency(o)),
		Reason:        reason,
		Force:         o.ForceRefund,
		DeductBalance: detail.DeductBalance,
		DeductionType: DeductionTypeNone,
	}
	if detail.DeductionRollbackOK && detail.DeductBalance {
		switch o.OrderType {
		case OrderTypeBalance:
			p.DeductionType = DeductionTypeBalance
			p.BalanceToDeduct = detail.BalanceDeducted
		case OrderTypeSubscription:
			p.DeductionType = DeductionTypeSubscription
			p.SubDaysToDeduct = detail.SubDaysDeducted
			p.SubscriptionID = detail.SubscriptionID
		}
	}
	return p
}
func RefundResponseID(resp *RefundResponse) string {
	if resp == nil {
		return ""
	}
	return strings.TrimSpace(resp.RefundID)
}
func refundStringValue(v *string) string {
	if v == nil {
		return ""
	}
	return *v
}
