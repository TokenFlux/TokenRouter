package testkit

import (
	"context"
	"log/slog"

	dbent "github.com/TokenFlux/TokenRouter/ent"
	"github.com/TokenFlux/TokenRouter/internal/payment"
	paymentpostgres "github.com/TokenFlux/TokenRouter/internal/payment/postgres"
	paymentadapter "github.com/TokenFlux/TokenRouter/internal/payment/provider"
	"github.com/TokenFlux/TokenRouter/internal/promotion"
)

// fulfillment.go assembles payment fulfillment and lifecycle fixtures with
// supplied entitlement services and audit logging.
// Ordinary payment audits are recorded through an errorless callback, so
// failed observation writes are logged while the state operation retains its
// result. Recovery scenarios combine this audit policy with supplied grant
// services and a prepared registry, and process configuration determines
// resume-token signing.

// Fulfillment constructs the shared test helper with Ent order/audit
// persistence and the supplied entitlement services. Optional rebates enable
// their gate; notification callbacks and several provider resolution
// dependencies are omitted, so tests choose paths supported by this setup.
func Fulfillment(client *dbent.Client, redeem payment.FulfillmentRedeemer, subscriptions payment.FulfillmentSubscriptions, rebates *promotion.AffiliateService) *payment.Fulfillment {
	return fulfillment(client, redeem, subscriptions, rebates, nil, false)
}

// Lifecycle binds the fixture fulfillment service, prepared registry, and
// resume service to order recovery. Nil resume uses empty signing
// configuration, and loaded controls lazy registry lookup; these helpers do
// not start expiry work.
func Lifecycle(client *dbent.Client, registry *payment.Registry, redeem payment.FulfillmentRedeemer, subscriptions payment.FulfillmentSubscriptions, resume *payment.PaymentResumeService, loaded bool) *payment.OrderLifecycle {
	if resume == nil {
		resume = Resume("", nil)
	}
	return payment.NewOrderLifecycle(fulfillment(client, redeem, subscriptions, nil, registry, loaded), resume, nil)
}

// fulfillment binds test order storage, audit logging, supplied entitlement
// operations, and optional rebate handling. Audit failures are logged through
// an errorless callback. Its provider bindings omit a configuration balancer
// and RegistryFactory, so database-backed adapter construction and lazy
// registry population require a different setup.
func fulfillment(client *dbent.Client, redeem payment.FulfillmentRedeemer, subscriptions payment.FulfillmentSubscriptions, rebates *promotion.AffiliateService, registry *payment.Registry, loaded bool) *payment.Fulfillment {
	auditStore := paymentpostgres.NewRefundStore(client, nil)
	audit := func(ctx context.Context, id int64, action, operator string, detail map[string]any) {
		// Include orderID and action when audit persistence fails, because callers
		// receive no callback error to correlate with the missing observation.
		if err := auditStore.AppendObservation(ctx, id, action, operator, detail); err != nil {
			slog.Error("audit log failed", "orderID", id, "action", action, "error", err)
		}
	}
	runtime := payment.FulfillmentRuntime{Audit: audit}
	if rebates != nil {
		runtime.RebateEnabled = rebates.IsEnabled
	}
	store := paymentpostgres.NewOrderStore(client, paymentpostgres.OrderStoreRuntime{
		Audit: audit,
		Rebates: func(*dbent.Tx) paymentpostgres.OrderRebates {
			return rebates
		},
	})
	bindings := payment.NewProviderBindings(paymentpostgres.NewInstanceStore(client), registry, nil, payment.BindingRuntime{Factory: paymentadapter.CreateProvider}, loaded)
	return payment.NewFulfillment(store, bindings, registry, redeem, subscriptions, runtime)
}

// Resume constructs a signing service from explicit text and the
// supplied legacy key. A configured key wins signing and can retain distinct
// legacy verification bytes; NewPaymentResumeService copies the chosen keys.
func Resume(raw string, legacyKey []byte) *payment.PaymentResumeService {
	key, fallbacks := payment.ResolvePaymentResumeSigningKeys(raw, legacyKey)
	return payment.NewPaymentResumeService(key, fallbacks...)
}
