// 旧履约形状只作投影；实例和通知队列由 app 持有，测试手工构造沿原端口转接。
package service

import (
	"context"
	"log/slog"

	dbent "github.com/TokenFlux/TokenRouter/ent"
	"github.com/TokenFlux/TokenRouter/internal/payment"
	paymentpostgres "github.com/TokenFlux/TokenRouter/internal/payment/postgres"
)

func (s *PaymentService) paymentFulfillment() *payment.Fulfillment {
	if s.fulfillment != nil {
		return s.fulfillment
	}
	runtime := payment.FulfillmentRuntime{Audit: s.writeAuditLog, Log: func(level, message string, args ...any) {
		switch level {
		case "error":
			slog.Error(message, args...)
		case "warn":
			slog.Warn(message, args...)
		default:
			slog.Info(message, args...)
		}
	}, Background: func(name string, fn func()) { RunBackgroundTask(name, BackgroundCall0(fn)) }}
	if s.userRepo != nil {
		runtime.User = func(ctx context.Context, id int64) (*payment.RefundUser, error) {
			u, e := s.userRepo.GetByID(ctx, id)
			if u == nil {
				return nil, e
			}
			return &payment.RefundUser{Balance: u.Balance}, e
		}
	}
	if s.notificationEmailService != nil {
		runtime.Notify = func(ctx context.Context, n payment.PaymentNotice) error {
			return s.notificationEmailService.Send(ctx, NotificationEmailSendInput{Event: n.Event, RecipientEmail: n.RecipientEmail, RecipientName: n.RecipientName, UserID: n.UserID, SourceType: n.SourceType, SourceID: n.SourceID, Variables: n.Variables})
		}
	}
	if s.affiliateService != nil {
		runtime.RebateEnabled = s.affiliateService.IsEnabled
	}
	store := paymentpostgres.NewOrderStore(s.entClient, paymentpostgres.OrderStoreRuntime{Rebates: func(*dbent.Tx) paymentpostgres.OrderRebates { return s.affiliateService }, Audit: s.writeAuditLog})
	var subscriptions payment.FulfillmentSubscriptions
	if s.subscriptionSvc != nil {
		subscriptions = s.subscriptionSvc
	}
	return payment.NewFulfillment(store, s.paymentBindings(), s.registry, s.redeemService, subscriptions, runtime)
}
func (s *PaymentService) BindFulfillment(core *payment.Fulfillment) { s.fulfillment = core }
