//go:build integration

package payment_test

import (
	"context"
	"encoding/json"
	"strconv"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/TokenFlux/TokenRouter/internal/billing"
	billingpostgres "github.com/TokenFlux/TokenRouter/internal/billing/postgres"
	"github.com/TokenFlux/TokenRouter/internal/payment"
	paymentpostgres "github.com/TokenFlux/TokenRouter/internal/payment/postgres"
	paymentadapter "github.com/TokenFlux/TokenRouter/internal/payment/provider"
	paymenttestkit "github.com/TokenFlux/TokenRouter/internal/payment/testkit"
	"github.com/TokenFlux/TokenRouter/internal/pkg/apperror"
)

// epusdt_scenario_integration_test.go applies verified EPay payment observations
// through production fulfillment to real PostgreSQL orders and billing redemption records.
// Concurrent callbacks and reconciliation observations share the production
// fulfillment lease, while expired orders exercise recovery of late payment.
// Signature and cashier checks are exercised by the provider TLS unit fixtures.
// These database assertions protect the resulting fiat entitlement separately.
// Merchant and currency snapshots are persisted before either observation runs.
//
// Checkout uncertainty uses one counted provider submission after a real local
// draft commit. Its returned recovery identity locates the pending row, whose
// state must remain available for future callbacks. Separate completed-order
// fixtures supply the buyer and instance, and cleanup removes the additional
// pending order before releasing those owning fixture records.

// epusdtUnconfirmedProvider simulates one submitted checkout with an uncertain response.
type epusdtUnconfirmedProvider struct {
	payment.Provider     // unused provider operation delegates
	calls            int // submitted checkout call count
}

// CreatePayment records a single submission and reports an uncertain gateway outcome.
// Callers inspect the submission count and retain their local order when this
// classified error is returned for recovery.
func (p *epusdtUnconfirmedProvider) CreatePayment(context.Context, payment.CreatePaymentRequest) (*payment.CreatePaymentResponse, error) {
	p.calls++
	return nil, apperror.ServiceUnavailable(payment.CodePaymentCreateUnconfirmed, "unconfirmed fixture checkout")
}

// TestEPUSDTUnconfirmedCreationRetainsPendingOrder simulates a lost checkout response after PostgreSQL draft creation.
// The caller receives its recovery identity and the provider is invoked once while the persisted order remains pending.
func TestEPUSDTUnconfirmedCreationRetainsPendingOrder(t *testing.T) {
	ctx := context.Background()
	client, _, existing := refundOrderFixture(t, payment.OrderStatusCompleted)
	instanceID, err := strconv.ParseInt(*existing.ProviderInstanceID, 10, 64)
	require.NoError(t, err)
	_, err = client.PaymentProviderInstance.UpdateOneID(instanceID).
		SetProviderKey(payment.TypeEPUSDT).SetSupportedTypes(payment.TypeEPUSDT).
		SetPaymentMode("popup").SetRefundEnabled(false).
		SetConfig(`{"pid":"1001","secretKey":"fixture-secret","apiBase":"https://pay.example","currency":"CNY","notifyUrl":"https://site.example/notify"}`).Save(ctx)
	require.NoError(t, err)
	config := paymenttestkit.Configuration(client, &paymentFulfillmentSettingRepoStub{values: map[string]string{
		payment.SettingPaymentEnabled: "true", payment.SettingEnabledPaymentTypes: payment.TypeEPUSDT,
	}}, nil)
	provider := &epusdtUnconfirmedProvider{}
	checkout := payment.NewCheckout(paymentpostgres.NewOrderStore(client), config,
		payment.NewDefaultLoadBalancer(paymentpostgres.NewInstanceStore(client), nil), nil,
		payment.CheckoutRuntime{
			User: func(context.Context, int64) (*payment.Buyer, error) {
				return &payment.Buyer{ID: existing.UserID, Status: "active", Email: existing.UserEmail}, nil
			},
			CreateProvider: func(string, string, map[string]string) (payment.Provider, error) {
				return provider, nil
			},
		})
	_, err = checkout.CreateOrder(ctx, payment.CreateOrderRequest{
		UserID: existing.UserID, Amount: 20, PaymentType: payment.TypeEPUSDT,
		ReturnURL: "https://site.example/payment/result", SrcHost: "site.example",
	})
	var appErr *apperror.ApplicationError
	require.ErrorAs(t, err, &appErr)
	require.Equal(t, payment.CodePaymentCreateUnconfirmed, appErr.Reason)
	orderID, err := strconv.ParseInt(appErr.Metadata["order_id"], 10, 64)
	require.NoError(t, err)
	t.Cleanup(func() {
		require.NoError(t, client.PaymentOrder.DeleteOneID(orderID).Exec(context.Background()))
	})
	current, err := client.PaymentOrder.Get(ctx, orderID)
	require.NoError(t, err)
	require.Equal(t, payment.OrderStatusPending, current.Status)
	require.Empty(t, current.PaymentTradeNo)
	require.Equal(t, current.OutTradeNo, appErr.Metadata["out_trade_no"])
	require.Equal(t, 1, provider.calls)
	_, err = config.UpdateProviderInstance(ctx, instanceID, payment.UpdateProviderInstanceRequest{
		Config: map[string]string{"secretKey": "rotated-secret"},
	})
	require.Equal(t, "PENDING_ORDERS", apperror.Reason(err))
	_, err = config.UpdateProviderInstance(ctx, instanceID, payment.UpdateProviderInstanceRequest{
		SupportedTypes: []string{},
	})
	require.Equal(t, "VALIDATION_ERROR", apperror.Reason(err))
	refundEnabled := true
	_, err = config.UpdateProviderInstance(ctx, instanceID, payment.UpdateProviderInstanceRequest{
		RefundEnabled: &refundEnabled,
	})
	require.Equal(t, "REFUND_UNSUPPORTED", apperror.Reason(err))
}

// TestEPUSDTConcurrentAndLatePaymentCreditsOnce runs verified EPUSDT EPay observations against PostgreSQL.
// Replays and competing payment observations credit fiat entitlement once, including after local expiry.
func TestEPUSDTConcurrentAndLatePaymentCreditsOnce(t *testing.T) {
	for _, initialStatus := range []string{payment.OrderStatusPending, payment.OrderStatusExpired} {
		t.Run(initialStatus, func(t *testing.T) {
			ctx := context.Background()
			client, _, order := refundOrderFixture(t, initialStatus)
			instanceID, err := strconv.ParseInt(*order.ProviderInstanceID, 10, 64)
			require.NoError(t, err)
			config := map[string]string{"apiBase": "https://pay.example", "pid": "1001", "secretKey": "fixture-secret", "currency": "CNY", "notifyUrl": "https://site.example/notify"}
			encoded, err := json.Marshal(config)
			require.NoError(t, err)
			_, err = client.PaymentProviderInstance.UpdateOneID(instanceID).
				SetProviderKey(payment.TypeEPUSDT).SetSupportedTypes(payment.TypeEPUSDT).
				SetConfig(string(encoded)).SetRefundEnabled(false).Save(ctx)
			require.NoError(t, err)
			snapshot := payment.BuildPaymentOrderProviderSnapshot(&payment.InstanceSelection{
				ProviderKey: payment.TypeEPUSDT, InstanceID: *order.ProviderInstanceID,
				PaymentMode: "popup", Config: config,
			}, payment.CreateOrderRequest{})
			order, err = client.PaymentOrder.UpdateOneID(order.ID).
				SetProviderKey(payment.TypeEPUSDT).SetPaymentType(payment.TypeEPUSDT).
				SetPaymentTradeNo("gateway_trade").SetProviderSnapshot(snapshot).ClearPaidAt().Save(ctx)
			require.NoError(t, err)
			adapter, err := paymentadapter.NewEPUSDT(*order.ProviderInstanceID, config)
			require.NoError(t, err)
			notification := &payment.PaymentNotification{
				OrderID: order.OutTradeNo, TradeNo: "gateway_trade", Amount: 50,
				Status: payment.ProviderStatusSuccess, Metadata: adapter.MerchantIdentityMetadata(),
			}
			repo := billingpostgres.NewRedeemCodeRepository(client)
			reader := &fulfillmentBalance{getByIDUser: &billing.UserSummary{ID: order.UserID}}
			mutations := billingpostgres.NewRedeemMutations(client, billingpostgres.RedeemWriters{Balances: billingpostgres.NewBalanceStore(client)})
			redeemer := billing.NewRedeemService(repo, reader, nil, nil, nil, mutations, nil, nil, billing.RedeemRuntime{})
			core := paymenttestkit.Fulfillment(client, redeemer, nil, nil)
			lifecycle := payment.NewOrderLifecycle(core, nil, nil)
			candidates, err := paymentpostgres.NewOrderStore(client).PendingReconciliation(ctx, time.Now(), 20)
			require.NoError(t, err)
			if initialStatus == payment.OrderStatusPending {
				require.Len(t, candidates, 1)
				require.Equal(t, order.ID, candidates[0].ID)
			}
			start := make(chan struct{})
			results := make(chan error, 2)
			// Both verified observations race on the same database order and fulfillment
			// lease; collect their results before asserting balances or cleaning fixture rows.
			go func() {
				<-start
				results <- core.HandlePaymentNotification(ctx, notification, payment.TypeEPUSDT)
			}()
			go func() {
				<-start
				_, err := lifecycle.ApplyQueriedPaymentStatus(ctx, paymentpostgres.OrderFromEntity(order), adapter, "gateway_trade", &payment.QueryOrderResponse{
					TradeNo: "gateway_trade", Status: payment.ProviderStatusSuccess, Amount: 50,
					Metadata: adapter.MerchantIdentityMetadata(),
				})
				results <- err
			}()
			close(start)
			for range 2 {
				if err := <-results; err != nil {
					require.Equal(t, "CONFLICT", apperror.Reason(err))
				}
			}
			require.NoError(t, core.HandlePaymentNotification(ctx, notification, payment.TypeEPUSDT))
			currentUser, err := client.User.Get(ctx, order.UserID)
			require.NoError(t, err)
			require.Equal(t, 150.0, currentUser.Balance)
			current, err := client.PaymentOrder.Get(ctx, order.ID)
			require.NoError(t, err)
			require.Equal(t, payment.OrderStatusCompleted, current.Status)
			notification.TradeNo = "conflicting_trade"
			require.ErrorContains(t, core.HandlePaymentNotification(ctx, notification, payment.TypeEPUSDT), "conflicts")
		})
	}
}
