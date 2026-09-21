//go:build unit

package payment_test

import (
	"context"
	"testing"
	"time"

	paymentpostgres "github.com/TokenFlux/TokenRouter/internal/payment/postgres"

	"github.com/TokenFlux/TokenRouter/internal/payment"
	"github.com/stretchr/testify/require"
)

func TestPersistCreatePaymentResponseSyncsProviderExpiry(t *testing.T) {
	ctx := context.Background()
	client := newPaymentOrderLifecycleTestClient(t)
	localExpiry := time.Now().Add(30 * time.Minute).UTC().Truncate(time.Second)
	providerExpiry := time.Now().Add(31 * time.Minute).UTC().Truncate(time.Second)
	order := createPaymentOrderLifecycleOrder(t, ctx, client, payment.OrderStatusPending, localExpiry)
	svc := payment.NewCheckout(paymentpostgres.NewOrderStore(client), nil, nil, nil, payment.CheckoutRuntime{})

	updated, err := svc.PersistCreatePaymentResponse(ctx, order.ID, &payment.InstanceSelection{
		InstanceID:  "stripe-primary",
		ProviderKey: payment.TypeStripe,
	}, &payment.CreatePaymentResponse{
		TradeNo:   "cs_test_expiry_sync",
		PayURL:    "https://checkout.stripe.example/session",
		ExpiresAt: providerExpiry,
	})
	require.NoError(t, err)
	require.True(t, updated.ExpiresAt.Equal(providerExpiry))

	reloaded, err := client.PaymentOrder.Get(ctx, order.ID)
	require.NoError(t, err)
	require.True(t, reloaded.ExpiresAt.Equal(providerExpiry))
}

func TestPersistCreatePaymentResponseKeepsLocalExpiryWhenProviderOmitsIt(t *testing.T) {
	ctx := context.Background()
	client := newPaymentOrderLifecycleTestClient(t)
	localExpiry := time.Now().Add(20 * time.Minute).UTC().Truncate(time.Second)
	order := createPaymentOrderLifecycleOrder(t, ctx, client, payment.OrderStatusPending, localExpiry)
	svc := payment.NewCheckout(paymentpostgres.NewOrderStore(client), nil, nil, nil, payment.CheckoutRuntime{})

	updated, err := svc.PersistCreatePaymentResponse(ctx, order.ID, &payment.InstanceSelection{
		InstanceID:  "alipay-primary",
		ProviderKey: payment.TypeAlipay,
	}, &payment.CreatePaymentResponse{TradeNo: order.OutTradeNo})
	require.NoError(t, err)
	require.True(t, updated.ExpiresAt.Equal(localExpiry))
}
