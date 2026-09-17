// 旧退款入口仅投影，存储事务与恢复规则分别由 payment/postgres 和 payment 拥有。
package service

import (
	"context"
	"log/slog"

	dbent "github.com/TokenFlux/TokenRouter/ent"
	"github.com/TokenFlux/TokenRouter/internal/payment"
	paymentpostgres "github.com/TokenFlux/TokenRouter/internal/payment/postgres"
	"github.com/TokenFlux/TokenRouter/internal/pkg/servertiming"
)

func paymentRefundPlan(p *RefundPlan) *payment.RefundPlan {
	if p == nil {
		return nil
	}
	return &payment.RefundPlan{OrderID: p.OrderID, Order: paymentOrderValue(p.Order), RefundAmount: p.RefundAmount, GatewayAmount: p.GatewayAmount, Reason: p.Reason, Force: p.Force, DeductBalance: p.DeductBalance, DeductionType: p.DeductionType, BalanceToDeduct: p.BalanceToDeduct, SubDaysToDeduct: p.SubDaysToDeduct, SubscriptionID: p.SubscriptionID, OperationID: p.OperationID, ChannelRefundID: p.ChannelRefundID}
}
func paymentRefundLegacyPlan(p *payment.RefundPlan) *RefundPlan {
	if p == nil {
		return nil
	}
	return &RefundPlan{OrderID: p.OrderID, Order: paymentOrderEntity(p.Order), RefundAmount: p.RefundAmount, GatewayAmount: p.GatewayAmount, Reason: p.Reason, Force: p.Force, DeductBalance: p.DeductBalance, DeductionType: p.DeductionType, BalanceToDeduct: p.BalanceToDeduct, SubDaysToDeduct: p.SubDaysToDeduct, SubscriptionID: p.SubscriptionID, OperationID: p.OperationID, ChannelRefundID: p.ChannelRefundID}
}
func copyPaymentRefundPlan(to *RefundPlan, from *payment.RefundPlan) {
	to.BalanceToDeduct = from.BalanceToDeduct
	to.SubDaysToDeduct = from.SubDaysToDeduct
	to.OperationID = from.OperationID
	to.ChannelRefundID = from.ChannelRefundID
}
func (s *PaymentService) paymentRefundStore() *paymentpostgres.RefundStore {
	if s.refundStore != nil {
		return s.refundStore
	}
	return paymentpostgres.NewRefundStore(s.entClient, func(*dbent.Tx) payment.RefundRights { return legacyRefundRights{s.userRepo, s.subscriptionSvc} })
}
func (s *PaymentService) paymentRefunds() *payment.RefundWorkflow {
	if s.refunds != nil {
		return s.refunds
	}
	return payment.NewRefundWorkflow(s.paymentRefundStore(), payment.RefundRuntime{Instance: s.paymentBindings().GetRefundOrderProviderInstance, User: func(ctx context.Context, id int64) (*payment.RefundUser, error) {
		u, e := s.userRepo.GetByID(ctx, id)
		if u == nil {
			return nil, e
		}
		return &payment.RefundUser{Balance: u.Balance}, e
	}, Subscriptions: func(ctx context.Context, id int64) ([]payment.RefundSubscription, error) {
		rows, e := s.subscriptionSvc.ListSubscriptionsBySourceOrderID(ctx, id)
		out := make([]payment.RefundSubscription, len(rows))
		for i, v := range rows {
			out[i] = payment.RefundSubscription{ID: v.ID, Status: v.Status}
		}
		return out, e
	}, Warn: slog.Warn, Provider: func(ctx context.Context, o *payment.Order) (payment.Provider, error) {
		return s.getRefundProvider(ctx, paymentOrderEntity(o))
	}, Observe: func(ctx context.Context) func() { return servertiming.ObserveDependency(ctx, "payment") }, Audit: s.writeAuditLog})
}

// 旧手工构造测试和迁移中的入口继续传递原 Ent context；生产装配注入 billing 同连接参与工厂。
type legacyRefundRights struct {
	users         UserRepository
	subscriptions *SubscriptionService
}

func (p legacyRefundRights) DeductBalance(ctx context.Context, id int64, amount float64) (float64, error) {
	return p.users.DeductBalance(ctx, id, amount)
}
func (p legacyRefundRights) CompensateBalance(ctx context.Context, id int64, amount float64) error {
	return p.users.UpdateBalance(ctx, id, amount)
}
func (p legacyRefundRights) AdjustSubscription(ctx context.Context, id int64, days int) error {
	_, err := p.subscriptions.ExtendSubscription(ctx, id, days)
	return err
}
func (p legacyRefundRights) RevokeSubscription(ctx context.Context, id int64) error {
	return p.subscriptions.RevokeSubscription(ctx, id)
}

// BindRefundWorkflow 在生产图构造期间绑定唯一退款实例，不在请求中改绑。
func (s *PaymentService) BindRefundWorkflow(store *paymentpostgres.RefundStore, workflow *payment.RefundWorkflow) {
	s.refundStore = store
	s.refunds = workflow
}
