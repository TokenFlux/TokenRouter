// 支付装配持有唯一退款用例，权益参与工厂显式绑定到订单的短事务。
package app

import (
	"context"
	"log/slog"
	"os"
	"strings"
	"time"

	"github.com/TokenFlux/TokenRouter/internal/app/lifecycle"
	"github.com/TokenFlux/TokenRouter/internal/identity"
	notificationcore "github.com/TokenFlux/TokenRouter/internal/notification"
	"github.com/TokenFlux/TokenRouter/internal/pkg/apperror"
	"github.com/TokenFlux/TokenRouter/internal/promotion"

	identitypostgres "github.com/TokenFlux/TokenRouter/internal/identity/postgres"

	dbent "github.com/TokenFlux/TokenRouter/ent"
	"github.com/TokenFlux/TokenRouter/internal/billing"

	billingpostgres "github.com/TokenFlux/TokenRouter/internal/billing/postgres"
	"github.com/TokenFlux/TokenRouter/internal/infra/telemetry/timing"
	"github.com/TokenFlux/TokenRouter/internal/payment"

	paymentpostgres "github.com/TokenFlux/TokenRouter/internal/payment/postgres"
	"github.com/TokenFlux/TokenRouter/internal/payment/provider"
	"github.com/TokenFlux/TokenRouter/internal/pkg/timezone"

	routingpostgres "github.com/TokenFlux/TokenRouter/internal/routing/postgres"
)

func providePaymentRuntime(client *dbent.Client, registry *payment.Registry, balancer payment.LoadBalancer, redeem *billing.RedeemService, subscriptions *billing.SubscriptionService, affiliate *promotion.AffiliateService, notification *notificationcore.NotificationEmailService, instances *paymentpostgres.InstanceStore, routingGroups *routingpostgres.GroupStore, identityUsers *identitypostgres.UserStore, coreConfig *payment.ConfigService, key payment.EncryptionKey, settings *identity.OAuthSettings, tasks *lifecycle.Tasks, calendar timezone.Calendar) *payment.Runtime {
	bindings := payment.NewProviderBindings(instances, registry, balancer, payment.BindingRuntime{Factory: provider.CreateProvider, RegistryFactory: provider.CreateProvider, Warn: slog.Warn}, false)
	store := paymentpostgres.NewRefundStore(client, func(tx *dbent.Tx) payment.RefundRights {
		return paymentRefundRights{balances: billingpostgres.BalanceInTx(tx), subscriptions: billingpostgres.SubscriptionsInTx(tx, billingGroups{Repository: routingGroups}, billing.DateRuntime{Now: time.Now, Calendar: &calendar})}
	})
	audit := func(ctx context.Context, id int64, action, operator string, detail map[string]any) {
		if err := store.AppendObservation(ctx, id, action, operator, detail); err != nil {
			slog.Error("audit log failed", "orderID", id, "action", action, "error", err)
		}
	}
	orders := paymentpostgres.NewOrderStore(client, paymentpostgres.OrderStoreRuntime{Rebates: func(*dbent.Tx) paymentpostgres.OrderRebates { return affiliate }, Audit: audit})
	queries := payment.NewOrderQueries(orders, time.Now, bindings.GetOrderProvider)

	workflow := payment.NewRefundWorkflow(store, payment.RefundRuntime{Instance: bindings.GetRefundOrderProviderInstance, User: func(ctx context.Context, id int64) (*payment.RefundUser, error) {
		u, e := identityUsers.GetByID(ctx, id)
		if u == nil {
			return nil, e
		}
		return &payment.RefundUser{Balance: u.Balance}, e
	}, Subscriptions: func(ctx context.Context, id int64) ([]payment.RefundSubscription, error) {
		rows, e := subscriptions.ListSubscriptionsBySourceOrderID(ctx, id)
		out := make([]payment.RefundSubscription, len(rows))
		for i, v := range rows {
			out[i] = payment.RefundSubscription{ID: v.ID, Status: v.Status}
		}
		return out, e
	}, Warn: slog.Warn, Provider: bindings.GetRefundProvider, Observe: func(ctx context.Context) func() { return timing.ObserveDependency(ctx, "payment") }, Audit: func(ctx context.Context, id int64, action, operator string, detail map[string]any) {
		if err := store.AppendObservation(ctx, id, action, operator, detail); err != nil {
			slog.Error("audit log failed", "orderID", id, "action", action, "error", err)
		}
	}})
	signing, fallbacks := payment.ResolvePaymentResumeSigningKeys(os.Getenv("PAYMENT_RESUME_SIGNING_KEY"), []byte(key))
	resume := payment.NewPaymentResumeService(signing, fallbacks...)
	checkout := payment.NewCheckout(orders, coreConfig, payment.NewVisibleMethodLoadBalancer(balancer, coreConfig), resume, payment.CheckoutRuntime{
		User: func(ctx context.Context, id int64) (*payment.Buyer, error) {
			u, e := identityUsers.GetByID(ctx, id)
			if u == nil {
				return nil, e
			}
			return &payment.Buyer{ID: u.ID, Email: u.Email, Username: u.Username, Notes: u.Notes, Status: u.Status}, e
		},
		WeChatCredential: func(ctx context.Context) (string, string, error) {
			cfg, e := settings.GetWeChatConnectOAuthConfig(ctx)
			appID, secret := strings.TrimSpace(cfg.AppIDForMode("mp")), strings.TrimSpace(cfg.AppSecretForMode("mp"))
			if e != nil || !cfg.SupportsMode("mp") || appID == "" || secret == "" {
				return "", "", apperror.ServiceUnavailable("WECHAT_PAYMENT_MP_NOT_CONFIGURED", "wechat in-app payment requires a complete WeChat MP OAuth credential")
			}
			return appID, secret, nil
		},
		CreateProvider: provider.CreateProvider, RememberLocale: notification.RememberRecipientLocale,
		Observe: func(ctx context.Context) func() { return timing.ObserveDependency(ctx, "payment") }, Error: slog.Error,
		Audit: func(ctx context.Context, id int64, action, operator string, detail map[string]any) {
			if e := store.AppendObservation(ctx, id, action, operator, detail); e != nil {
				slog.Error("audit log failed", "orderID", id, "action", action, "error", e)
			}
		},
	})

	fulfillment := payment.NewFulfillment(orders, bindings, registry, redeem, subscriptions, payment.FulfillmentRuntime{
		Audit: audit, Now: time.Now, RebateEnabled: affiliate.IsEnabled,
		Log: func(level, message string, args ...any) {
			switch level {
			case "warn":
				slog.Warn(message, args...)
			case "error":
				slog.Error(message, args...)
			default:
				slog.Info(message, args...)
			}
		},
		Background: func(name string, fn func()) { tasks.Go(name, fn) },
		User: func(ctx context.Context, id int64) (*payment.RefundUser, error) {
			u, e := identityUsers.GetByID(ctx, id)
			if u == nil {
				return nil, e
			}
			return &payment.RefundUser{Balance: u.Balance}, e
		},
		Notify: func(ctx context.Context, n payment.PaymentNotice) error {
			return notification.Send(ctx, notificationcore.NotificationEmailSendInput{Event: n.Event, RecipientEmail: n.RecipientEmail, RecipientName: n.RecipientName, UserID: n.UserID, SourceType: n.SourceType, SourceID: n.SourceID, Variables: n.Variables})
		},
	})
	orderLifecycle := payment.NewOrderLifecycle(fulfillment, resume, func(ctx context.Context) func() { return timing.ObserveDependency(ctx, "payment") })
	return &payment.Runtime{Checkout: checkout, OrderQueries: queries, RefundWorkflow: workflow, OrderLifecycle: orderLifecycle, ProviderBindings: bindings}
}

type paymentRefundRights struct {
	balances      *billingpostgres.BalanceStore
	subscriptions *billing.SubscriptionService
}

func (p paymentRefundRights) DeductBalance(ctx context.Context, id int64, amount float64) (float64, error) {
	return p.balances.DeductRefundBalance(ctx, id, amount)
}
func (p paymentRefundRights) CompensateBalance(ctx context.Context, id int64, amount float64) error {
	return p.balances.CompensateRefundBalance(ctx, id, amount)
}
func (p paymentRefundRights) AdjustSubscription(ctx context.Context, id int64, days int) error {
	_, err := p.subscriptions.ExtendSubscription(ctx, id, days)
	return err
}
func (p paymentRefundRights) RevokeSubscription(ctx context.Context, id int64) error {
	return p.subscriptions.RevokeSubscription(ctx, id)
}
