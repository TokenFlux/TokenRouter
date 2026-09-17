// 退款用例只协调短事务与渠道调用；失败结果不会覆盖较新的退款事实。
package payment

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/TokenFlux/TokenRouter/internal/billing"
	"github.com/TokenFlux/TokenRouter/internal/pkg/apperror"
)

func (s *RefundWorkflow) ExecuteRefund(ctx context.Context, p *RefundPlan) (*RefundResult, error) {
	receipt, err := s.store.PrepareRefund(ctx, p, ApplyRefundDeduction)
	if err != nil {
		return nil, err
	}
	resp, err := s.GwRefund(ctx, p)
	if err != nil {
		return s.compensateFailure(ctx, p, receipt, err)
	}
	return s.FinishRefund(ctx, p, receipt, resp)
}
func (s *RefundWorkflow) FinishRefund(ctx context.Context, p *RefundPlan, r *RefundReceipt, resp *RefundResponse) (*RefundResult, error) {
	if err := ValidateRefundProviderResponse(resp); err != nil {
		return s.compensateFailure(ctx, p, r, err)
	}
	if strings.TrimSpace(resp.Status) == ProviderStatusPending {
		if err := s.store.CompensateRefund(ctx, p, r, RefundResponseID(resp), "", CompensateRefundDeduction); err != nil {
			return nil, err
		}
		p.BalanceToDeduct = 0
		p.SubDaysToDeduct = 0
		return &RefundResult{Warning: "gateway refund is pending confirmation"}, nil
	}
	return s.store.CompleteRefund(ctx, p, r, nil)
}
func (s *RefundWorkflow) compensateFailure(ctx context.Context, p *RefundPlan, r *RefundReceipt, cause error) (*RefundResult, error) {
	if err := s.store.CompensateRefund(ctx, p, r, "", cause.Error(), CompensateRefundDeduction); err != nil {
		return nil, fmt.Errorf("gateway refund failed (%v); local recovery remains pending: %w", cause, err)
	}
	return &RefundResult{Warning: "gateway failed: " + cause.Error() + ", rolled back"}, nil
}

// QueryAndFinalizeRefund 只查询已发生的渠道操作，不重发退款。
func (s *RefundWorkflow) QueryAndFinalizeRefund(ctx context.Context, id int64) (*RefundResult, error) {
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
	if receipt != nil {
		detail = receipt.RefundPendingDetail
	}
	provider, err := s.runtime.Provider(ctx, order)
	if err != nil {
		return nil, fmt.Errorf("get refund provider: %w", err)
	}
	query, ok := provider.(RefundQueryProvider)
	if !ok {
		return nil, apperror.BadRequest("REFUND_QUERY_UNSUPPORTED", "this payment provider does not support refund status query; please verify manually")
	}
	finish := s.runtime.Observe(ctx)
	resp, err := query.QueryRefund(ctx, RefundQueryRequest{
		TradeNo:  order.PaymentTradeNo,
		OrderID:  order.OutTradeNo,
		RefundID: detail.RefundID,
		Amount:   FormatGatewayRefundAmount(order.RefundAmount, order),
	})
	finish()
	if err != nil {
		return nil, fmt.Errorf("query refund: %w", err)
	}
	if err = ValidateRefundProviderResponse(resp); err != nil {
		// 不明响应不猜测成功/失败，保留原准备事实供人工核实。
		if resp == nil || strings.TrimSpace(resp.Status) != ProviderStatusFailed {
			return nil, RefundRecoveryRequired(err.Error())
		}
		if receipt != nil {
			return s.compensateFailure(ctx, receipt.Plan(order), receipt, err)
		}
		current, saveErr := s.store.FailPendingRefund(ctx, order, detail, err.Error())
		if saveErr != nil {
			return nil, saveErr
		}
		if current.Status == OrderStatusRefunded || current.Status == OrderStatusPartiallyRefunded {
			return &RefundResult{Success: true}, nil
		}
		if current.Status != OrderStatusRefundFailed {
			return nil, apperror.Conflict("CONFLICT", "order status changed")
		}
		return &RefundResult{Warning: "gateway refund failed: " + err.Error()}, nil
	}
	if receipt != nil {
		return s.FinishRefund(ctx, receipt.Plan(order), receipt, resp)
	}
	if strings.TrimSpace(resp.Status) == ProviderStatusPending {
		s.runtime.Audit(ctx, id, "REFUND_QUERY_PENDING", "admin", map[string]any{"refundID": resp.RefundID})
		return &RefundResult{Warning: "gateway refund is still pending confirmation"}, nil
	}
	return s.FinalizePendingRefundSuccess(ctx, s.RefundFinalizePlan(order, detail))
}
func (s *RefundWorkflow) FinalizePendingRefundSuccess(ctx context.Context, p *RefundPlan) (*RefundResult, error) {
	return s.store.CompleteRefund(ctx, p, nil, ApplyRefundDeduction)
}

// ApplyRefundDeduction 只使用当前事务的权益参与能力，保留非负实际扣减及到期撤销。
func ApplyRefundDeduction(ctx context.Context, rights RefundRights, p *RefundPlan) error {
	if p.DeductionType == DeductionTypeBalance && p.BalanceToDeduct > 0 {
		actual, err := rights.DeductBalance(ctx, p.Order.UserID, p.BalanceToDeduct)
		if err != nil {
			return fmt.Errorf("deduction: %w", err)
		}
		p.BalanceToDeduct = actual
	}
	if p.DeductionType == DeductionTypeSubscription && p.SubDaysToDeduct > 0 && p.SubscriptionID > 0 {
		err := rights.AdjustSubscription(ctx, p.SubscriptionID, -p.SubDaysToDeduct)
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
func CompensateRefundDeduction(ctx context.Context, rights RefundRights, p *RefundPlan) error {
	if p.DeductionType == DeductionTypeBalance && p.BalanceToDeduct > 0 {
		if err := rights.CompensateBalance(ctx, p.Order.UserID, p.BalanceToDeduct); err != nil {
			return err
		}
	}
	if p.DeductionType == DeductionTypeSubscription && p.SubDaysToDeduct > 0 && p.SubscriptionID > 0 {
		return rights.AdjustSubscription(ctx, p.SubscriptionID, p.SubDaysToDeduct)
	}
	return nil
}
func (r *RefundReceipt) Plan(order *Order) *RefundPlan {
	return &RefundPlan{
		OrderID:         r.OrderID,
		Order:           order,
		RefundAmount:    r.RefundAmount,
		GatewayAmount:   r.GatewayAmount,
		Reason:          r.Reason,
		Force:           r.Force,
		DeductBalance:   r.DeductBalance,
		DeductionType:   r.DeductionType,
		BalanceToDeduct: r.BalanceDeducted,
		SubDaysToDeduct: r.SubDaysDeducted,
		SubscriptionID:  r.SubscriptionID,
		OperationID:     r.OperationID,
	}
}
