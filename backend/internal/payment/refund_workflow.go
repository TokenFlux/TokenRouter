package payment

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/TokenFlux/TokenRouter/internal/billing"
	"github.com/TokenFlux/TokenRouter/internal/pkg/apperror"
)

// refund_workflow.go coordinates refund preparation, provider submission,
// compensation, and recovery finalization.
// An external refund can complete while its local final state still needs
// persistence, so refund progress is recorded before provider submission.
// Preparation, compensation, and finalization use separate store transactions,
// with the provider call between them and receipt identity carrying the
// previously committed local recovery.
// An unknown channel outcome retains that preparation for manual verification.
//
// Pending observations restore prepared wallet funds and retain the provider
// identity for later query. Subscription holds remain through pending and
// unknown results. A terminal result either revokes the held grant or clears
// its marker, alongside the corresponding order transition. Conditional store
// claims prevent a stale observation from changing a completed refund or the
// grant owned by a different operation.

// refundLocalRecoveryTimeout bounds critical local writes that must finish
// even after the administrative request disconnects.
const refundLocalRecoveryTimeout = 5 * time.Second

// ExecuteRefund persists preparation and its deduction receipt before making
// the gateway request. Preparation failure returns before external submission,
// and the store publishes committed mutation results into the plan.
//
// The gateway call runs between short store transactions. An unknown outcome
// retains preparation for verification; other errors choose compensation,
// while a response goes through FinishRefund for pending or successful local
// settlement. A later store failure can leave the already
// committed preparation and externally attempted refund for recovery; this
// method does not resubmit the gateway after that local failure.
func (s *RefundWorkflow) ExecuteRefund(ctx context.Context, p *RefundPlan) (*RefundResult, error) {
	// Commit local preparation before external submission so failed preparation
	// cannot initiate a provider refund and later local recovery has a persisted
	// operation receipt.
	receipt, err := s.store.PrepareRefund(ctx, p, PrepareRefundRights)
	if errors.Is(err, billing.ErrSubscriptionNotFound) {
		return &RefundResult{
			Status:       RefundResultRequiresForce,
			RequireForce: true,
			Warning:      "No active or queued subscription was found for this order. Confirm a refund without revoking it.",
		}, nil
	}
	if err != nil {
		return nil, err
	}
	// The preparation transaction has returned before this provider call.
	// External progress can therefore survive a later completion or compensation
	// error in its separate transaction.
	resp, err := s.GwRefund(ctx, p)
	// Persist a known reference before changing rights again. If that write
	// fails, preserve the preparation for recovery without a second submission.
	if saveErr := s.recordProviderRefund(ctx, receipt, resp); saveErr != nil {
		return nil, saveErr
	}
	if err != nil {
		// The channel may have transferred money before its response was lost.
		// Keep the prepared deduction and claim so another submission cannot double-refund.
		if errors.Is(err, ErrRefundOutcomeUnknown) {
			s.runtime.Warn("refund: provider outcome unknown", "orderID", p.OrderID, "error", err)
			return nil, RefundRecoveryRequired("provider refund outcome is unknown")
		}
		return s.compensateFailure(ctx, p, receipt, err)
	}
	return s.FinishRefund(ctx, p, receipt, resp)
}

// recordProviderRefund persists a nonblank channel reference on the current
// preparation. Persistence failure retains the existing recovery state and
// tells callers to verify the prior submission.
func (s *RefundWorkflow) recordProviderRefund(ctx context.Context, receipt *RefundReceipt, response *RefundResponse) error {
	if id := RefundResponseID(response); id != "" {
		// A disconnected browser can cancel the request after the provider
		// returned its refund ID. Give that ID five seconds to reach storage
		// so a later query can recover the exact operation.
		recordCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), refundLocalRecoveryTimeout)
		defer cancel()
		if err := s.store.RecordRefundProviderID(recordCtx, receipt, id); err != nil {
			s.runtime.Warn("refund: provider reference persistence failed", "orderID", receipt.OrderID, "error", err)
			return RefundRecoveryRequired("could not record the provider refund reference")
		}
	}
	return nil
}

// FinishRefund applies a provider observation to an existing preparation.
// A pending refund keeps a subscription hold and restores prepared wallet
// funds before publishing the pending state. A confirmed success revokes the
// held grant in the same transaction as finalization. Legacy preparation
// which already deducted rights completes without applying another deduction.
//
// The caller records any channel reference before invoking this method.
// After an unknown result or local write failure, the original preparation
// stays available for the next query.
func (s *RefundWorkflow) FinishRefund(ctx context.Context, p *RefundPlan, r *RefundReceipt, resp *RefundResponse) (*RefundResult, error) {
	if err := ValidateRefundProviderResponse(resp); err != nil {
		if errors.Is(err, ErrRefundOutcomeUnknown) {
			return nil, RefundRecoveryRequired(err.Error())
		}
		return s.compensateFailure(ctx, p, r, err)
	}
	if strings.TrimSpace(resp.Status) == ProviderStatusPending {
		compensate := CompensateRefundDeduction
		if p.SubscriptionHold {
			// Keep the grant suspended until Stripe or another provider reports
			// a terminal result. Only the order changes in this transaction.
			compensate = func(context.Context, RefundRights, *RefundPlan) error { return nil }
		}
		if err := s.store.CompensateRefund(ctx, p, r, RefundResponseID(resp), "", compensate); err != nil {
			return nil, RefundRecoveryRequired("could not record pending refund")
		}
		p.BalanceToDeduct = 0
		p.SubDaysToDeduct = 0
		return &RefundResult{Status: RefundResultPending, Warning: "gateway refund is pending confirmation"}, nil
	}
	var finalize RefundMutation
	if p.SubscriptionHold {
		// Revoke the held grant before committing success. A local failure leaves
		// both its marker and the channel reference available for another query.
		finalize = ApplyRefundDeduction
	}
	result, err := s.store.CompleteRefund(ctx, p, r, finalize)
	if err != nil {
		s.runtime.Warn("refund: completion failed", "orderID", p.OrderID, "error", err)
		return nil, RefundRecoveryRequired("could not record the confirmed refund")
	}
	return result, nil
}

// compensateFailure asks the store to restore prepared rights and record
// gateway failure together using the matching receipt. A successful
// compensation returns a warning result, while store failure returns both
// gateway cause and local recovery error.
//
// The earlier preparation remains the persisted progress used for later
// recovery until the compensation transaction succeeds. Definitive failure
// compensation has a separate bounded context so disconnecting the request
// cannot strand the hold after the provider was rejected or never called.
func (s *RefundWorkflow) compensateFailure(ctx context.Context, p *RefundPlan, r *RefundReceipt, cause error) (*RefundResult, error) {
	// Use the receipt to compensate exactly this local preparation and record its
	// gateway failure. A failed recovery transaction leaves the earlier prepared
	// state for later verification.
	// Once failure is known, the request can disconnect while the application
	// still owes the customer a release of the committed entitlement hold.
	compensationCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), refundLocalRecoveryTimeout)
	defer cancel()
	if err := s.store.CompensateRefund(compensationCtx, p, r, "", cause.Error(), CompensateRefundDeduction); err != nil {
		s.runtime.Warn("refund: compensation failed", "orderID", p.OrderID, "providerError", cause, "error", err)
		return nil, RefundRecoveryRequired("could not restore the prepared entitlement")
	}
	return &RefundResult{Status: RefundResultFailed, Warning: "gateway failed: " + cause.Error() + ", rolled back"}, nil
}

// QueryAndFinalizeRefund reconciles an order using the refund evidence already
// saved locally. It returns the same recovery guidance as the administrative
// lookup when the provider cannot verify the existing attempt.
func (s *RefundWorkflow) QueryAndFinalizeRefund(ctx context.Context, id int64) (*RefundResult, error) {
	return s.QueryAndFinalizeRefundWithReference(ctx, id, "")
}

// QueryAndFinalizeRefundWithReference queries an existing pending or recoverable refunding
// operation and completes local recovery from that observation. It requires
// valid pending detail or prepared receipt before resolving the order's
// refund-query provider; unsupported query capability returns a manual-check
// error.
//
// Query failures and inconclusive responses return while local progress
// remains. An explicit failed prepared result compensates that receipt; a
// failed pending result conditionally records failure and accepts an already
// newer refund success. A valid prepared response reuses FinishRefund, while
// pending success rebuilds deduction intent and invokes one transaction for
// rights and completion. An administrator may supply a missing Stripe refund
// ID for a prepared order. Its association is checked by the provider before
// persistence, and an existing reference cannot be replaced by operator input.
func (s *RefundWorkflow) QueryAndFinalizeRefundWithReference(ctx context.Context, id int64, suppliedRefundID string) (*RefundResult, error) {
	order, err := s.store.Order(ctx, id)
	if err != nil {
		return nil, apperror.NotFound("NOT_FOUND", "order not found")
	}
	if order.Status != OrderStatusRefundPending && order.Status != OrderStatusRefunding {
		return nil, apperror.BadRequest("INVALID_STATUS", "only refund pending or recoverable refunding orders can be finalized")
	}
	var receipt *RefundReceipt
	var detail RefundPendingDetail
	if order.Status == OrderStatusRefunding {
		receipt, err = s.store.RefundRecovery(ctx, order)
	} else {
		detail, err = s.store.PendingDetail(ctx, id)
	}
	if err != nil {
		return nil, err
	}
	currency := PaymentOrderCurrency(order)
	amount := CalculateGatewayRefundAmount(order.Amount, order.PayAmount, order.RefundAmount, currency)
	if receipt != nil {
		detail = receipt.RefundPendingDetail
		detail.ObservedAt = receipt.OperationVersion
		amount = receipt.GatewayAmount
	}
	if detail.SubscriptionHold && order.OrderType != OrderTypeSubscription {
		return nil, RefundRecoveryRequired("subscription hold does not match the order type")
	}
	provider, err := s.runtime.Provider(ctx, order)
	if err != nil {
		return nil, fmt.Errorf("get refund provider: %w", err)
	}
	query, ok := provider.(RefundQueryProvider)
	if !ok {
		return nil, apperror.BadRequest("REFUND_QUERY_UNSUPPORTED", "this payment provider does not support refund status query; please verify manually")
	}
	suppliedRefundID = strings.TrimSpace(suppliedRefundID)
	refundID := detail.RefundID
	earliest := detail.ObservedAt
	if refundID != "" {
		earliest = time.Time{}
	}
	stripePreparation := receipt != nil && provider.ProviderKey() == TypeStripe
	canSupplyReference := stripePreparation && refundID == ""
	if suppliedRefundID != "" {
		if !stripePreparation || (refundID != "" && refundID != suppliedRefundID) ||
			!strings.HasPrefix(suppliedRefundID, "re_") || len(suppliedRefundID) == len("re_") {
			return nil, apperror.BadRequest("REFUND_REFERENCE_UNAVAILABLE", "this Stripe refund ID cannot be associated with the prepared refund")
		}
		// Keep the preparation timestamp for an unbound reference. Stripe
		// checks it together with the payment, amount and currency before
		// this workflow can record the reference or release any rights.
		refundID = suppliedRefundID
	}
	finish := s.runtime.Observe(ctx)
	resp, err := query.QueryRefund(ctx, RefundQueryRequest{
		TradeNo:           order.PaymentTradeNo,
		OrderID:           order.OutTradeNo,
		RefundID:          refundID,
		Amount:            FormatAmountForCurrency(amount, currency),
		Currency:          currency,
		EarliestCreatedAt: earliest,
	})
	finish()
	if err != nil {
		s.runtime.Warn("refund: provider query inconclusive", "orderID", id, "error", err)
		if canSupplyReference {
			if suppliedRefundID != "" {
				return nil, apperror.BadRequest("REFUND_REFERENCE_UNVERIFIED", "Stripe did not verify this refund ID for the prepared refund")
			}
			return nil, apperror.Conflict("REFUND_REFERENCE_REQUIRED", "provide the Stripe refund ID to verify this refund")
		}
		return nil, RefundRecoveryRequired("provider refund could not be verified")
	}
	if suppliedRefundID != "" && RefundResponseID(resp) != suppliedRefundID {
		return nil, apperror.BadRequest("REFUND_REFERENCE_UNVERIFIED", "Stripe did not verify this refund ID for the prepared refund")
	}
	if receipt != nil {
		if err = s.recordProviderRefund(ctx, receipt, resp); err != nil {
			return nil, err
		}
	}
	if err = ValidateRefundProviderResponse(resp); err != nil {
		// An inconclusive query cannot establish whether external refund occurred.
		// Retain persisted progress and require verification before local finalization.
		if resp == nil || strings.TrimSpace(resp.Status) != ProviderStatusFailed {
			return nil, RefundRecoveryRequired(err.Error())
		}
		if receipt != nil {
			return s.compensateFailure(ctx, receipt.Plan(order), receipt, err)
		}
		current, saveErr := s.store.FailPendingRefund(ctx, order, detail, err.Error())
		if saveErr != nil {
			if apperror.Reason(saveErr) == "REFUND_RECOVERY_REQUIRED" {
				return nil, saveErr
			}
			s.runtime.Warn("refund: failed to record provider failure", "orderID", id, "error", saveErr)
			return nil, RefundRecoveryRequired("could not record the provider refund failure")
		}
		// A later successful finalization can win while this failed provider
		// observation is being stored. Return that newer successful state instead of
		// treating it as refund failure.
		if current.Status == OrderStatusRefunded || current.Status == OrderStatusPartiallyRefunded {
			return &RefundResult{Status: RefundResultSucceeded, Success: true}, nil
		}
		if current.Status != OrderStatusRefundFailed {
			return nil, apperror.Conflict("CONFLICT", "order status changed")
		}
		return &RefundResult{Status: RefundResultFailed, Warning: "gateway refund failed: " + err.Error()}, nil
	}
	if receipt != nil {
		return s.FinishRefund(ctx, receipt.Plan(order), receipt, resp)
	}
	if strings.TrimSpace(resp.Status) == ProviderStatusPending {
		s.runtime.Audit(ctx, id, "REFUND_QUERY_PENDING", "admin", map[string]any{"refundID": resp.RefundID})
		return &RefundResult{Status: RefundResultPending, Warning: "gateway refund is still pending confirmation"}, nil
	}
	result, err := s.FinalizePendingRefundSuccess(ctx, s.RefundFinalizePlan(order, detail))
	if err != nil {
		switch apperror.Reason(err) {
		case "CONFLICT", "REFUND_RECOVERY_REQUIRED":
			return nil, err
		}
		s.runtime.Warn("refund: pending finalization failed", "orderID", id, "error", err)
		return nil, RefundRecoveryRequired("could not record the confirmed refund")
	}
	return result, nil
}

// FinalizePendingRefundSuccess delegates a reconstructed pending plan to
// transactional completion with ApplyRefundDeduction. The store validates
// current status, version, and operation identity before recovering
// compensated rights and writing success.
func (s *RefundWorkflow) FinalizePendingRefundSuccess(ctx context.Context, p *RefundPlan) (*RefundResult, error) {
	return s.store.CompleteRefund(ctx, p, nil, ApplyRefundDeduction)
}

// PrepareRefundRights pauses the purchased subscription or applies the planned
// wallet deduction in the transaction that records refund preparation. Force
// can bypass a missing eligible grant, but storage and conflicting hold
// errors abort preparation before the provider is called.
func PrepareRefundRights(ctx context.Context, rights RefundRights, p *RefundPlan) error {
	if !p.SubscriptionHold {
		return ApplyRefundDeduction(ctx, rights, p)
	}
	id, err := rights.HoldSubscription(ctx, p.Order.UserID, p.OrderID, p.OperationID)
	// Allow a missing grant only after explicit force consent. A storage error
	// cannot prove that the order has no entitlement left to recover.
	if errors.Is(err, billing.ErrSubscriptionNotFound) && p.Force {
		return nil
	}
	if err != nil {
		return err
	}
	p.SubscriptionID = id
	return nil
}

// ApplyRefundDeduction recovers wallet funds or finalizes the recorded
// subscription rights inside the refund store transaction. A held grant is
// revoked only when its marker belongs to this refund. For older pending
// records, a preexisting revocation is checked before recording success.
//
// A legacy deduction that was successfully compensated is reapplied using its
// saved days and grant identity. The store commits the entitlement change and
// publishes the resulting audit together with order finalization.
func ApplyRefundDeduction(ctx context.Context, rights RefundRights, p *RefundPlan) error {
	if p.SubscriptionHold && p.SubscriptionID > 0 {
		return rights.ResolveSubscriptionHold(ctx, p.SubscriptionID, p.OperationID, true)
	}
	if p.VerifyLegacyRevocation {
		// The older pending record says compensation failed. Verify the retained
		// revoked grant before marking the order successful without another revoke.
		if err := rights.VerifyRevokedSubscription(ctx, p.Order.UserID, p.OrderID, p.SubscriptionID); err != nil {
			return RefundRecoveryRequired("legacy subscription revocation could not be verified")
		}
		return nil
	}
	if p.DeductionType == DeductionTypeBalance && p.BalanceToDeduct > 0 {
		actual, err := rights.DeductBalance(ctx, p.Order.UserID, p.BalanceToDeduct)
		if err != nil {
			return fmt.Errorf("deduction: %w", err)
		}
		p.BalanceToDeduct = actual
	}
	if p.DeductionType == DeductionTypeSubscription && p.SubDaysToDeduct > 0 && p.SubscriptionID > 0 {
		err := rights.AdjustSubscription(ctx, p.SubscriptionID, -p.SubDaysToDeduct)
		// The requested negative adjustment would end the grant before the allowed
		// time. Revoke that identified subscription through the same transaction,
		// retaining the recorded day quantity for recovery.
		if errors.Is(err, billing.ErrAdjustWouldExpire) {
			if err = rights.RevokeSubscription(ctx, p.SubscriptionID); err != nil {
				return fmt.Errorf("revoke subscription: %w", err)
			}
		} else if err != nil {
			return fmt.Errorf("deduct subscription days: %w", err)
		}
	}
	return nil
}

// CompensateRefundDeduction restores wallet funds or releases the owning refund
// hold in the store's transaction. Legacy subscription day counts cannot
// recover a revoked or shifted chain, so those records require manual
// verification before any rights are changed.
func CompensateRefundDeduction(ctx context.Context, rights RefundRights, p *RefundPlan) error {
	if p.DeductionType == DeductionTypeBalance && p.BalanceToDeduct > 0 {
		if err := rights.CompensateBalance(ctx, p.Order.UserID, p.BalanceToDeduct); err != nil {
			return err
		}
	}
	if p.SubscriptionHold && p.SubscriptionID > 0 {
		return rights.ResolveSubscriptionHold(ctx, p.SubscriptionID, p.OperationID, false)
	}
	if p.DeductionType == DeductionTypeSubscription && p.SubDaysToDeduct > 0 && p.SubscriptionID > 0 {
		return RefundRecoveryRequired("legacy subscription deduction needs manual compensation")
	}
	return nil
}

// Plan recreates refund intent and recorded deduction quantities from the
// prepared receipt while retaining the supplied order pointer. It copies the
// local operation identity for store claims, with provider refund identity
// handled by the pending path.
func (r *RefundReceipt) Plan(order *Order) *RefundPlan {
	return &RefundPlan{
		OrderID:          r.OrderID,
		Order:            order,
		RefundAmount:     r.RefundAmount,
		GatewayAmount:    r.GatewayAmount,
		Reason:           r.Reason,
		Force:            r.Force,
		DeductBalance:    r.DeductBalance,
		DeductionType:    r.DeductionType,
		BalanceToDeduct:  r.BalanceDeducted,
		SubDaysToDeduct:  r.SubDaysDeducted,
		SubscriptionID:   r.SubscriptionID,
		SubscriptionHold: r.SubscriptionHold,
		OperationID:      r.OperationID,
	}
}
