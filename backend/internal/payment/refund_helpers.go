package payment

import (
	"context"
	"fmt"
	"strings"
)

// refund_helpers.go submits gateway refunds and reconstructs entitlement
// recovery plans from saved pending details.
// A gateway refund uses the saved merchant and transaction references while
// local entitlement recovery uses its own quantities. Historical instance
// resolution and snapshot metadata checks precede submission, and a provider
// may return pending progress alongside an error when its settlement is still
// unfinished.
//
// Pending recovery reconstructs intent from the order and audit detail. The
// recorded deduction choice and compensation flag determine whether rights are
// recovered again, with the order type selecting wallet or subscription data.
// Gateway money is formatted in recorded currency, and nil/unknown provider
// responses remain errors for the orchestration layer to handle. An empty
// stored trade number takes audited local success handling, so it represents a
// different path from an externally submitted refund.

// GwRefund submits the prepared plan's gateway amount through the historical
// provider. A subscription requires a trade reference. Balance orders retain
// the audited local-only behavior for orders without a stored trade number.
//
// A resolved provider's reported merchant identity is compared with recorded
// snapshot metadata under the validator's legacy rules before submission. The
// request carries the stored merchant and upstream numbers plus
// currency-formatted money. A provider error accompanied by pending state is
// retained for query recovery. Other responses keep their reference with any
// error so the workflow can record it before changing local rights.
func (s *RefundWorkflow) GwRefund(ctx context.Context, p *RefundPlan) (*RefundResponse, error) {
	// Orders with no stored upstream transaction follow local-only success
	// handling. Record the skipped gateway operation so later audit inspection
	// can distinguish it from provider submission.
	if strings.TrimSpace(p.Order.PaymentTradeNo) == "" {
		if p.Order.OrderType == OrderTypeSubscription {
			return nil, fmt.Errorf("subscription order is missing its payment reference")
		}
		s.runtime.Audit(ctx, p.Order.ID, "REFUND_NO_TRADE_NO", "admin", map[string]any{"detail": "skipped"})
		return &RefundResponse{Status: ProviderStatusSuccess}, nil
	}

	// Resolve the saved order binding before checking merchant metadata. Current
	// configuration belongs to that instance, and a mismatched reported identity
	// returns before a refund request is submitted.
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
	// Cancellation observed here proves that the provider call has not
	// started. Return a definitive local failure so preparation is released.
	if err := ctx.Err(); err != nil {
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
		// Some adapters return pending progress with an error while settlement is
		// unfinished. Keep that progress so the workflow compensates locally and
		// later queries the recorded provider refund.
		if resp != nil && strings.TrimSpace(resp.Status) == ProviderStatusPending {
			return resp, nil
		}
		return resp, err
	}
	if err := ValidateRefundProviderResponse(resp); err != nil {
		return resp, err
	}
	return resp, nil
}

// FormatGatewayRefundAmount formats the quantity using the order currency's
// fraction digits. Currency lookup uses CNY when the order or recorded
// currency is absent or invalid, including a nil order input.
func FormatGatewayRefundAmount(amount float64, order *Order) string {
	return FormatAmountForCurrency(amount, PaymentOrderCurrency(order))
}

// ValidateRefundProviderResponse accepts trimmed success, refunded, and
// pending status text. Explicit failure returns a compensatable error, while
// missing or unrecognized responses carry the unknown-outcome sentinel.
func ValidateRefundProviderResponse(resp *RefundResponse) error {
	if resp == nil {
		return fmt.Errorf("%w: payment refund response missing", ErrRefundOutcomeUnknown)
	}
	status := strings.TrimSpace(resp.Status)
	switch status {
	case ProviderStatusSuccess, ProviderStatusRefunded, ProviderStatusPending:
		return nil
	case ProviderStatusFailed:
		return fmt.Errorf("payment refund failed: status %s", status)
	default:
		return fmt.Errorf("%w: unrecognized provider status %q", ErrRefundOutcomeUnknown, status)
	}
}

// RefundFinalizePlan rebuilds local pending-refund intent from a nonnil order
// and persisted detail. It copies the provider/local operation identities and
// calculates gateway money proportionally from the order's stored amounts and
// currency.
//
// The recorded deduction choice and compensation flag jointly determine
// whether rights need recovery again. When both are true, the order type
// chooses the wallet quantity or subscription day/grant data; other types
// leave deduction kind none. The detail's deduction type is not used for that
// selection. Missing reason text uses an order-specific description, and the
// returned plan retains the supplied order pointer.
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
	if detail.SubscriptionHold {
		p.SubscriptionHold = true
		p.SubscriptionID = detail.SubscriptionID
		p.DeductionType = DeductionTypeSubscription
		return p
	}
	if o.OrderType == OrderTypeSubscription && detail.DeductBalance && !detail.DeductionRollbackOK {
		p.VerifyLegacyRevocation = true
		p.SubscriptionID = detail.SubscriptionID
		return p
	}

	// Pending compensation restored the earlier deduction only when recorded as
	// successful. Recover rights again on final success when that flag and the
	// stored deduction intent both apply.
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

// RefundResponseID returns the trimmed provider refund identity copied into
// pending recovery records. Nil input yields empty text, and a whitespace-only
// identity also becomes empty for later query selection.
func RefundResponseID(resp *RefundResponse) string {
	if resp == nil {
		return ""
	}
	return strings.TrimSpace(resp.RefundID)
}

// refundStringValue returns the pointed-to text as supplied, or an empty
// string for nil. It retains whitespace and empty values, leaving
// normalization to the order, provider, and refund callers.
func refundStringValue(v *string) string {
	if v == nil {
		return ""
	}
	return *v
}
