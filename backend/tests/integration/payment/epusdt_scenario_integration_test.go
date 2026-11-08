//go:build integration

package payment_test

import (
	"context"
	"encoding/json"
	"errors"
	"strconv"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/TokenFlux/TokenRouter/ent/paymentorder"
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
// Checkout failures use one counted provider submission after a real local
// draft commit. Uncertain results retain the pending row for future callbacks,
// while explicit rejection marks it failed and releases configuration protection.
// Separate completed-order fixtures supply the buyer and instance, and cleanup
// removes the additional order before releasing those owning fixture records.

// epusdtCreateErrorProvider simulates a submitted checkout returning a classified failure.
type epusdtCreateErrorProvider struct {
	payment.Provider       // unused provider operation delegates
	calls            int   // submitted checkout call count
	failure          error // configured provider creation failure
}

// CreatePayment records one submission and returns the configured failure.
// Callers inspect whether checkout retains a pending order or marks it failed;
// the fixture does not contact an external gateway.
func (p *epusdtCreateErrorProvider) CreatePayment(context.Context, payment.CreatePaymentRequest) (*payment.CreatePaymentResponse, error) {
	p.calls++
	return nil, p.failure
}

// TestEPUSDTCreationFailureLifecycle applies rejected and uncertain checkout results after PostgreSQL draft creation.
// Rejection releases pending capacity and merchant configuration protection, while uncertainty preserves the order for signed callback recovery.
func TestEPUSDTCreationFailureLifecycle(t *testing.T) {
	cases := []struct {
		name    string // creation failure scenario
		failure error  // classified provider creation failure
		status  string // expected persisted order status
	}{
		{
			name:    "unconfirmed",
			failure: apperror.ServiceUnavailable(payment.CodePaymentCreateUnconfirmed, "unconfirmed fixture checkout"),
			status:  payment.OrderStatusPending,
		},
		{
			name:    "rejected",
			failure: errors.New("epusdt EPay checkout was rejected (HTTP 401)"),
			status:  payment.OrderStatusFailed,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			ctx := t.Context()
			client, _, existing := refundOrderFixture(t, payment.OrderStatusCompleted)
			instanceID, err := strconv.ParseInt(*existing.ProviderInstanceID, 10, 64)
			require.NoError(t, err)
			_, err = client.PaymentProviderInstance.UpdateOneID(instanceID).
				SetProviderKey(payment.TypeEPUSDT).
				SetSupportedTypes(payment.TypeEPUSDT).
				SetPaymentMode("popup").
				SetRefundEnabled(false).
				SetConfig(`{"pid":"1001","secretKey":"fixture-secret","apiBase":"https://pay.example","currency":"CNY","notifyUrl":"https://site.example/notify"}`).
				Save(ctx)
			require.NoError(t, err)
			config := paymenttestkit.Configuration(client, &paymentFulfillmentSettingRepoStub{values: map[string]string{
				payment.SettingPaymentEnabled: "true", payment.SettingEnabledPaymentTypes: payment.TypeEPUSDT,
			}}, nil)
			provider := &epusdtCreateErrorProvider{failure: tc.failure}
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
			_, createErr := checkout.CreateOrder(ctx, payment.CreateOrderRequest{
				UserID: existing.UserID, Amount: 20, PaymentType: payment.TypeEPUSDT,
				ReturnURL: "https://site.example/payment/result", SrcHost: "site.example",
			})
			var appErr *apperror.ApplicationError
			require.ErrorAs(t, createErr, &appErr)
			current, err := client.PaymentOrder.Query().Where(
				paymentorder.UserIDEQ(existing.UserID),
				paymentorder.IDNEQ(existing.ID),
			).Only(ctx)
			require.NoError(t, err)
			// Remove the new order before its owning user and provider fixtures are
			// released; test context cancellation has already happened when cleanup runs.
			t.Cleanup(func() {
				cleanupCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
				defer cancel()
				require.NoError(t, client.PaymentOrder.DeleteOneID(current.ID).Exec(cleanupCtx))
			})
			require.Equal(t, tc.status, current.Status)
			require.Empty(t, current.PaymentTradeNo)
			require.Equal(t, 1, provider.calls)
			pendingCount, err := config.ConfigCountPendingOrders(ctx, instanceID)
			require.NoError(t, err)
			_, updateErr := config.UpdateProviderInstance(ctx, instanceID, payment.UpdateProviderInstanceRequest{
				Config: map[string]string{"secretKey": "rotated-secret"},
			})
			if tc.status == payment.OrderStatusPending {
				require.Equal(t, payment.CodePaymentCreateUnconfirmed, appErr.Reason)
				require.Equal(t, strconv.FormatInt(current.ID, 10), appErr.Metadata["order_id"])
				require.Equal(t, current.OutTradeNo, appErr.Metadata["out_trade_no"])
				require.Equal(t, 1, pendingCount)
				require.Equal(t, "PENDING_ORDERS", apperror.Reason(updateErr))
			} else {
				require.Equal(t, "PAYMENT_GATEWAY_ERROR", appErr.Reason)
				require.Zero(t, pendingCount)
				require.NoError(t, updateErr)
			}
			_, err = config.UpdateProviderInstance(ctx, instanceID, payment.UpdateProviderInstanceRequest{
				SupportedTypes: []string{},
			})
			require.Equal(t, "VALIDATION_ERROR", apperror.Reason(err))
			refundEnabled := true
			_, err = config.UpdateProviderInstance(ctx, instanceID, payment.UpdateProviderInstanceRequest{
				RefundEnabled: &refundEnabled,
			})
			require.Equal(t, "REFUND_UNSUPPORTED", apperror.Reason(err))
		})
	}
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
