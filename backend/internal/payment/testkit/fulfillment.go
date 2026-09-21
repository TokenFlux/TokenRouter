package testkit

import (
	"context"
	"log/slog"

	dbent "github.com/TokenFlux/TokenRouter/ent"
	"github.com/TokenFlux/TokenRouter/internal/payment"
	paymentpostgres "github.com/TokenFlux/TokenRouter/internal/payment/postgres"
	"github.com/TokenFlux/TokenRouter/internal/payment/provider"
	"github.com/TokenFlux/TokenRouter/internal/promotion"
)

// Fulfillment 只装配原生履约与审计存储，规则仍由各模块唯一实现承担。
func Fulfillment(client *dbent.Client, redeem payment.FulfillmentRedeemer, subscriptions payment.FulfillmentSubscriptions, rebates *promotion.AffiliateService) *payment.Fulfillment {
	return fulfillment(client, redeem, subscriptions, rebates, nil, false)
}

// Lifecycle 让原查单契约共享一份履约和渠道绑定，不创建旧聚合服务。
func Lifecycle(client *dbent.Client, registry *payment.Registry, redeem payment.FulfillmentRedeemer, subscriptions payment.FulfillmentSubscriptions, resume *payment.PaymentResumeService, loaded bool) *payment.OrderLifecycle {
	if resume == nil {
		resume = Resume(nil)
	}
	return payment.NewOrderLifecycle(fulfillment(client, redeem, subscriptions, nil, registry, loaded), resume, nil)
}

func fulfillment(client *dbent.Client, redeem payment.FulfillmentRedeemer, subscriptions payment.FulfillmentSubscriptions, rebates *promotion.AffiliateService, registry *payment.Registry, loaded bool) *payment.Fulfillment {
	auditStore := paymentpostgres.NewRefundStore(client, nil)
	audit := func(ctx context.Context, id int64, action, operator string, detail map[string]any) {
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
	bindings := payment.NewProviderBindings(paymentpostgres.NewInstanceStore(client), registry, nil, payment.BindingRuntime{Factory: provider.CreateProvider}, loaded)
	return payment.NewFulfillment(store, bindings, registry, redeem, subscriptions, runtime)
}
