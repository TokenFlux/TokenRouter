//go:build unit

package service

import (
	"context"
	"testing"
	"time"

	"github.com/BrandonVee/TokenRouter/internal/payment"
	"github.com/stretchr/testify/require"
)

func TestPersistCreatePaymentResponseSyncsProviderExpiry(t *testing.T) {
	ctx := context.Background()
	client := newPaymentOrderLifecycleTestClient(t)
	localExpiry := time.Now().Add(30 * time.Minute).UTC().Truncate(time.Second)
	providerExpiry := time.Now().Add(31 * time.Minute).UTC().Truncate(time.Second)
	order := createPaymentOrderLifecycleOrder(t, ctx, client, OrderStatusPending, localExpiry)
	svc := &PaymentService{entClient: client}

	updated, err := svc.persistCreatePaymentResponse(ctx, order.ID, &payment.InstanceSelection{
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
	order := createPaymentOrderLifecycleOrder(t, ctx, client, OrderStatusPending, localExpiry)
	svc := &PaymentService{entClient: client}

	updated, err := svc.persistCreatePaymentResponse(ctx, order.ID, &payment.InstanceSelection{
		InstanceID:  "alipay-primary",
		ProviderKey: payment.TypeAlipay,
	}, &payment.CreatePaymentResponse{TradeNo: order.OutTradeNo})
	require.NoError(t, err)
	require.True(t, updated.ExpiresAt.Equal(localExpiry))
}
