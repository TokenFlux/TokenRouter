package app

import (
	"context"
	"log/slog"
	"strings"
	"time"

	dbent "github.com/TokenFlux/TokenRouter/ent"
	"github.com/TokenFlux/TokenRouter/internal/app/lifecycle"
	"github.com/TokenFlux/TokenRouter/internal/billing"
	billingpostgres "github.com/TokenFlux/TokenRouter/internal/billing/postgres"
	"github.com/TokenFlux/TokenRouter/internal/config"
	"github.com/TokenFlux/TokenRouter/internal/identity"
	identitypostgres "github.com/TokenFlux/TokenRouter/internal/identity/postgres"
	"github.com/TokenFlux/TokenRouter/internal/infra/telemetry/timing"
	notificationcore "github.com/TokenFlux/TokenRouter/internal/notification"
	"github.com/TokenFlux/TokenRouter/internal/payment"
	paymentpostgres "github.com/TokenFlux/TokenRouter/internal/payment/postgres"
	paymentadapter "github.com/TokenFlux/TokenRouter/internal/payment/provider"
	"github.com/TokenFlux/TokenRouter/internal/pkg/apperror"
	"github.com/TokenFlux/TokenRouter/internal/pkg/timezone"
	"github.com/TokenFlux/TokenRouter/internal/promotion"
	routingpostgres "github.com/TokenFlux/TokenRouter/internal/routing/postgres"
)

// payment_refund.go assembles payment checkout, fulfillment, queries, and
// refunds with shared provider bindings and order storage. Refund rights are
// constructed from the transaction supplied by RefundStore, connecting balance
// and subscription changes to that transaction while external provider calls
// remain owned by RefundWorkflow.

// paymentRefundRights adapts transaction-bound billing changes for payment
// refund persistence.
type paymentRefundRights struct {
	// balances and subscriptions are constructed from RefundStore's selected Ent
	// transaction. RefundStore owns its completion, so both rights operations participate in that same transaction lifetime.
	balances      *billingpostgres.BalanceStore          // refund balance participant
	subscriptions *billing.SubscriptionService           // refund subscription participant
	mutations     *billingpostgres.SubscriptionMutations // refund subscription locks
}

// providePaymentRuntime assembles checkout, queries, refunds, fulfillment, and
// payment resume using shared dependencies. Refund participants receive the
// selected Ent transaction and application calendar; notifications and
// fulfillment background effects use the supplied services and task tracker.
// The configured signing key and configured encryption key are resolved into
// payment resume keys.
func providePaymentRuntime(client *dbent.Client, registry *payment.Registry, balancer payment.LoadBalancer, redeem *billing.RedeemService, subscriptions *billing.SubscriptionService, affiliate *promotion.AffiliateService, notification *notificationcore.NotificationEmailService, instances *paymentpostgres.InstanceStore, routingGroups *routingpostgres.GroupStore, identityUsers *identitypostgres.UserStore, coreConfig *payment.ConfigService, key payment.EncryptionKey, settings *identity.OAuthSettings, tasks *lifecycle.Tasks, calendar timezone.Calendar, cfg *config.Config) *payment.Runtime {
	bindings := payment.NewProviderBindings(instances, registry, balancer, payment.BindingRuntime{Factory: paymentadapter.CreateProvider, RegistryFactory: paymentadapter.CreateProvider, Warn: slog.Warn}, false)
	store := paymentpostgres.NewRefundStore(client, func(tx *dbent.Tx) payment.RefundRights {
		return newPaymentRefundRights(tx, billingGroups{Repository: routingGroups}, billing.DateRuntime{Now: time.Now, Calendar: &calendar})
	})
	audit := func(ctx context.Context, id int64, action, operator string, detail map[string]any) {
		// This audit callback reports write failure through logging and returns
		// no error; the payment operation therefore retains its own result independently of diagnostic persistence.
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
	}, Warn: slog.Warn, Provider: bindings.GetRefundProvider, Observe: func(ctx context.Context) func() { return timing.ObserveDependency(ctx, "payment") }, Audit: func(ctx context.Context, id int64, action, operator string, detail map[string]any) {
		// This audit callback reports write failure through logging and returns
		// no error; the payment operation therefore retains its own result independently of diagnostic persistence.
		if err := store.AppendObservation(ctx, id, action, operator, detail); err != nil {
			slog.Error("audit log failed", "orderID", id, "action", action, "error", err)
		}
	}})
	signing, fallbacks := payment.ResolvePaymentResumeSigningKeys(cfg.Payment.ResumeSigningKey, []byte(key))
	resume := payment.NewPaymentResumeService(signing, fallbacks...)
	checkout := payment.NewCheckout(orders, coreConfig, payment.NewVisibleMethodLoadBalancer(balancer, coreConfig), resume, payment.CheckoutRuntime{
		User: func(ctx context.Context, id int64) (*payment.Buyer, error) {
			u, e := identityUsers.GetByID(ctx, id)
			if u == nil {
				return nil, e
			}
			return &payment.Buyer{
				ID:                        u.ID,
				Email:                     u.Email,
				Username:                  u.Username,
				Notes:                     u.Notes,
				Status:                    u.Status,
				BalanceRechargeMultiplier: u.BalanceRechargeMultiplier,
			}, e
		},
		WeChatCredential: func(ctx context.Context) (string, string, error) {
			cfg, e := settings.GetWeChatConnectOAuthConfig(ctx)
			appID, secret := strings.TrimSpace(cfg.AppIDForMode("mp")), strings.TrimSpace(cfg.AppSecretForMode("mp"))
			// In-app WeChat checkout needs complete MP credentials. Convert settings
			// failure or an unusable mode into the payment-specific unavailable reason before authorization proceeds.
			if e != nil || !cfg.SupportsMode("mp") || appID == "" || secret == "" {
				return "", "", apperror.ServiceUnavailable("WECHAT_PAYMENT_MP_NOT_CONFIGURED", "wechat in-app payment requires a complete WeChat MP OAuth credential")
			}
			return appID, secret, nil
		},
		CreateProvider: paymentadapter.CreateProvider, RememberLocale: notification.RememberRecipientLocale,
		Observe: func(ctx context.Context) func() { return timing.ObserveDependency(ctx, "payment") }, Error: slog.Error,
		Audit: func(ctx context.Context, id int64, action, operator string, detail map[string]any) {
			// Checkout audit is observational: record a failure in logs while the
			// checkout callback returns no audit error to replace the order operation's result.
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
			return notification.Send(ctx, notificationcore.NotificationEmailSendInput{Event: n.Event, Locale: n.Locale, RecipientEmail: n.RecipientEmail, RecipientName: n.RecipientName, UserID: n.UserID, SourceType: n.SourceType, SourceID: n.SourceID, Variables: n.Variables})
		},
	})
	orderLifecycle := payment.NewOrderLifecycle(fulfillment, resume, func(ctx context.Context) func() { return timing.ObserveDependency(ctx, "payment") })
	return &payment.Runtime{Checkout: checkout, OrderQueries: queries, RefundWorkflow: workflow, OrderLifecycle: orderLifecycle, ProviderBindings: bindings}
}

// newPaymentRefundRights binds the refund's wallet and subscription operations
// to the store-owned transaction so the rights and refund state commit together.
func newPaymentRefundRights(tx *dbent.Tx, groups billing.SubscriptionGroupReader, clock billing.DateRuntime) paymentRefundRights {
	return paymentRefundRights{
		balances:      billingpostgres.BalanceInTx(tx),
		subscriptions: billingpostgres.SubscriptionsInTx(tx, groups, clock),
		mutations:     billingpostgres.SubscriptionMutationsInTx(tx),
	}
}

// DeductBalance delegates refund deduction to the transaction-bound billing
// balance writer.
func (p paymentRefundRights) DeductBalance(ctx context.Context, id int64, amount float64) (float64, error) {
	return p.balances.DeductRefundBalance(ctx, id, amount)
}

// CompensateBalance delegates refund compensation through the bound billing
// transaction participant.
func (p paymentRefundRights) CompensateBalance(ctx context.Context, id int64, amount float64) error {
	return p.balances.CompensateRefundBalance(ctx, id, amount)
}

// AdjustSubscription delegates a day adjustment to the transaction-bound
// subscription service. The returned subscription value is discarded, and the
// error remains available to RefundStore for its transaction result.
func (p paymentRefundRights) AdjustSubscription(ctx context.Context, id int64, days int) error {
	_, err := p.subscriptions.ExtendSubscription(ctx, id, days)
	return err
}

// RevokeSubscription delegates revocation through the bound billing
// subscription transaction participant.
func (p paymentRefundRights) RevokeSubscription(ctx context.Context, id int64) error {
	return p.subscriptions.RevokeSubscription(ctx, id)
}

// HoldSubscription applies the order-owned suspension through the bound
// billing transaction and returns the exact grant identity.
func (p paymentRefundRights) HoldSubscription(ctx context.Context, userID, orderID int64, operationID string) (int64, error) {
	return p.mutations.HoldRefundSubscription(ctx, userID, orderID, operationID)
}

// ResolveSubscriptionHold clears the marker under the refund's transaction
// locks. Successful refunds then revoke through billing before commit so a
// chain update failure rolls back the marker change too.
func (p paymentRefundRights) ResolveSubscriptionHold(ctx context.Context, id int64, operationID string, revoke bool) error {
	// Release and optional revocation share one transaction, so clearing the
	// marker cannot expose a grant whose successful refund still needs revocation.
	if err := p.mutations.ClearRefundSubscriptionHold(ctx, id, operationID); err != nil {
		return err
	}
	if revoke {
		return p.subscriptions.RevokeSubscription(ctx, id)
	}
	return nil
}

// VerifyRevokedSubscription checks the legacy grant against its original order
// under the bound transaction before recording a previously completed revocation.
func (p paymentRefundRights) VerifyRevokedSubscription(ctx context.Context, userID, orderID, id int64) error {
	return p.mutations.VerifyRevokedRefundSubscription(ctx, userID, orderID, id)
}
