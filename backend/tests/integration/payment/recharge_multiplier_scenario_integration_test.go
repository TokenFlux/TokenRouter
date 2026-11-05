//go:build integration

package payment_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strconv"
	"sync/atomic"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"

	"github.com/TokenFlux/TokenRouter/ent/paymentauditlog"
	"github.com/TokenFlux/TokenRouter/internal/billing"
	billingpostgres "github.com/TokenFlux/TokenRouter/internal/billing/postgres"
	"github.com/TokenFlux/TokenRouter/internal/identity"
	identityhttp "github.com/TokenFlux/TokenRouter/internal/identity/httpapi"
	"github.com/TokenFlux/TokenRouter/internal/identity/httpapi/authctx"
	identitypostgres "github.com/TokenFlux/TokenRouter/internal/identity/postgres"
	"github.com/TokenFlux/TokenRouter/internal/payment"
	paymenthttp "github.com/TokenFlux/TokenRouter/internal/payment/httpapi"
	paymentpostgres "github.com/TokenFlux/TokenRouter/internal/payment/postgres"
	paymentadapter "github.com/TokenFlux/TokenRouter/internal/payment/provider"
	paymenttestkit "github.com/TokenFlux/TokenRouter/internal/payment/testkit"
	"github.com/TokenFlux/TokenRouter/internal/pkg/apperror"
)

// recharge_multiplier_scenario_integration_test.go exercises checkout.go,
// httpapi/user.go, fulfillment.go and refund_workflow.go against PostgreSQL.
// Account rates are read for preview and order creation, while callbacks and
// refunds consume the persisted credited amount after those rates change.
//
// Hosted EasyPay creation produces only a local signed URL. The refund adapter
// calls a local HTTP merchant which records the requested currency amount;
// fulfillment uses the real billing redemption transaction. These observations
// distinguish a user's credited units from paid money and prove repeated
// notifications do not add the same credit twice. Every provider configuration
// and account belongs to the fixture and is removed during ordered cleanup.

// TestPersonalRechargeRateCheckoutAndSettlement varies inherited and explicit
// rates through checkout and its HTTP preview. Later settings changes and
// repeated callbacks preserve stored credits, and refunds recover those units
// while returning only the order's original payment principal.
func TestPersonalRechargeRateCheckoutAndSettlement(t *testing.T) {
	for _, rate := range []*float64{nil, new(float64(18)), new(float64(0.5)), new(float64(10)), new(float64(1))} {
		name := "inherited"
		if rate != nil {
			name = fmt.Sprintf("personal_%g", *rate)
		}
		t.Run(name, func(t *testing.T) {
			ctx := context.Background()
			client := testEntClient(t)
			buyer, err := client.User.Create().SetEmail("recharge@example.com").SetPasswordHash("hash").SetNillableBalanceRechargeMultiplier(rate).Save(ctx)
			require.NoError(t, err)
			var refundedAmount atomic.Value
			var refundCalls atomic.Int32
			merchant := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if err := r.ParseForm(); err != nil {
					t.Error(err)
					w.WriteHeader(http.StatusBadRequest)
					return
				}
				// Publish observed money before replying so the requesting goroutine can
				// inspect the result without sharing an unsynchronized mutable slice.
				refundedAmount.Store(r.Form.Get("money"))
				refundCalls.Add(1)
				_, _ = w.Write([]byte(`{"code":1,"msg":"退款成功"}`))
			}))
			t.Cleanup(merchant.Close)
			configuration, err := json.Marshal(map[string]string{
				"pid": "1000", "pkey": "local-secret", "apiBase": merchant.URL,
				"notifyUrl": "https://site.example.com/notify", "returnUrl": "https://site.example.com/payment/result",
				"paymentMode": "popup",
			})
			require.NoError(t, err)
			instance, err := client.PaymentProviderInstance.Create().SetName("personal rate fixture").SetProviderKey(payment.TypeEasyPay).
				SetConfig(string(configuration)).SetSupportedTypes(payment.TypeAlipay).SetPaymentMode("popup").SetEnabled(true).SetRefundEnabled(true).Save(ctx)
			require.NoError(t, err)
			t.Cleanup(func() {
				require.NoError(t, client.PaymentProviderInstance.DeleteOneID(instance.ID).Exec(context.Background()))
			})
			settings := &paymentFulfillmentSettingRepoStub{values: map[string]string{
				payment.SettingPaymentEnabled: "true", payment.SettingBalanceRechargeMult: "10",
			}}
			cfg := paymenttestkit.Configuration(client, settings, nil)
			instances := paymentpostgres.NewInstanceStore(client)
			var buyerErr error
			checkout := payment.NewCheckout(paymentpostgres.NewOrderStore(client), cfg,
				payment.NewVisibleMethodLoadBalancer(payment.NewDefaultLoadBalancer(instances, nil), cfg), nil,
				payment.CheckoutRuntime{
					User: func(ctx context.Context, id int64) (*payment.Buyer, error) {
						if buyerErr != nil {
							return nil, buyerErr
						}
						u, err := client.User.Get(ctx, id)
						if err != nil {
							return nil, err
						}
						return &payment.Buyer{ID: u.ID, Email: u.Email, Username: u.Username, Status: u.Status, BalanceRechargeMultiplier: u.BalanceRechargeMultiplier}, nil
					},
					CreateProvider: paymentadapter.CreateProvider,
				})
			plans := billing.NewPlans(billingpostgres.NewPlanStore(client), instances, nil)
			handler := paymenthttp.NewPaymentHandler(&payment.Runtime{Checkout: checkout}, cfg, plans)
			router := gin.New()
			router.GET("/checkout-info", func(c *gin.Context) {
				c.Set(authctx.ContextKeyUser, authctx.AuthSubject{UserID: buyer.ID})
				handler.GetCheckoutInfo(c)
			})
			res := httptest.NewRecorder()
			router.ServeHTTP(res, httptest.NewRequest(http.MethodGet, "/checkout-info", nil))
			require.Equal(t, http.StatusOK, res.Code, res.Body.String())
			var preview struct {
				Data paymenthttp.UserCheckoutInfoResponse `json:"data"`
			} // decoded checkout envelope
			require.NoError(t, json.Unmarshal(res.Body.Bytes(), &preview))
			effective := 10.0
			if rate != nil {
				effective = *rate
			}
			require.Equal(t, effective, preview.Data.BalanceRechargeMultiplier)
			require.Equal(t, 10.0, preview.Data.DefaultBalanceRechargeMultiplier)
			require.Equal(t, rate, preview.Data.UserBalanceRechargeMultiplier)
			request := payment.CreateOrderRequest{UserID: buyer.ID, Amount: 100, PaymentType: payment.TypeAlipay, OrderType: payment.OrderTypeBalance, ReturnURL: "https://site.example.com/payment/result", SrcHost: "site.example.com", SrcURL: "https://site.example.com/purchase"}

			// Reject credits that cannot become a positive redeem code or fit its wallet
			// precision before saving an order, so no paid purchase is left unfulfillable.
			for _, invalidRate := range []float64{1e-8, 1e11, 1e308} {
				_, err := client.User.UpdateOneID(buyer.ID).SetBalanceRechargeMultiplier(invalidRate).Save(ctx)
				require.NoError(t, err)
				_, err = checkout.CreateOrder(ctx, request)
				require.Equal(t, "INVALID_CREDITED_AMOUNT", apperror.Reason(err))
				count, err := client.PaymentOrder.Query().Count(ctx)
				require.NoError(t, err)
				require.Zero(t, count)
			}
			update := client.User.UpdateOneID(buyer.ID)
			if rate == nil {
				update.ClearBalanceRechargeMultiplier()
			} else {
				update.SetBalanceRechargeMultiplier(*rate)
			}
			_, err = update.Save(ctx)
			require.NoError(t, err)
			invalidType := request
			invalidType.OrderType = "wallet"
			_, err = checkout.CreateOrder(ctx, invalidType)
			require.Equal(t, "INVALID_CHECKOUT_ORDER_TYPE", apperror.Reason(err))
			created, err := checkout.CreateOrder(ctx, request)
			require.NoError(t, err)
			order, err := client.PaymentOrder.Get(ctx, created.OrderID)
			require.NoError(t, err)
			// Remove independent audit and order records before their provider and
			// buyer fixtures; the shared test client retains unrelated cases' records.
			t.Cleanup(func() {
				cleanup := context.Background()
				_, err := client.PaymentAuditLog.Delete().Where(paymentauditlog.OrderIDEQ(strconv.FormatInt(order.ID, 10))).Exec(cleanup)
				require.NoError(t, err)
				require.NoError(t, client.PaymentOrder.DeleteOneID(order.ID).Exec(cleanup))
			})
			require.Equal(t, 100*effective, order.Amount)
			require.Equal(t, 100.0, order.PayAmount)
			require.Zero(t, order.FeeAmount)
			_, err = client.User.UpdateOneID(buyer.ID).ClearBalanceRechargeMultiplier().Save(ctx)
			require.NoError(t, err)
			settings.values[payment.SettingBalanceRechargeMult] = "7"
			changed, err := checkout.GetBalanceRechargeRates(ctx, buyer.ID, 7)
			require.NoError(t, err)
			require.Nil(t, changed.User)
			require.Equal(t, 7.0, changed.Effective)
			repo := billingpostgres.NewRedeemCodeRepository(client)
			mutations := billingpostgres.NewRedeemMutations(client, billingpostgres.RedeemWriters{Balances: billingpostgres.NewBalanceStore(client)})
			redeemer := billing.NewRedeemService(repo, &fulfillmentBalance{getByIDUser: &billing.UserSummary{ID: buyer.ID}}, nil, nil, nil, mutations, nil, nil, billing.RedeemRuntime{})
			fulfillment := paymenttestkit.Fulfillment(client, redeemer, nil, nil)
			notification := &payment.PaymentNotification{OrderID: order.OutTradeNo, TradeNo: "personal-trade", Amount: 100, Status: payment.NotificationStatusSuccess}
			require.NoError(t, fulfillment.HandlePaymentNotification(ctx, notification, payment.TypeEasyPay))
			require.NoError(t, fulfillment.HandlePaymentNotification(ctx, notification, payment.TypeEasyPay))
			credited, err := client.User.Get(ctx, buyer.ID)
			require.NoError(t, err)
			require.Equal(t, 100*effective, credited.Balance)
			code, err := repo.GetByCode(ctx, order.RechargeCode)
			require.NoError(t, err)
			require.Equal(t, 1, code.UsedCount)
			workflow := newPostgresRefundWorkflow(client)
			refund, early, err := workflow.PrepareRefund(ctx, order.ID, order.Amount, "fixture refund", false, true)
			require.NoError(t, err)
			require.Nil(t, early)
			result, err := workflow.ExecuteRefund(ctx, refund)
			require.NoError(t, err)
			require.True(t, result.Success)
			require.Equal(t, int32(1), refundCalls.Load())
			require.Equal(t, "100.00", refundedAmount.Load())
			assertRefundPostgresState(t, ctx, client, buyer.ID, order.ID, 0, payment.OrderStatusRefunded, 1)
			buyerErr = errors.New("buyer unavailable")
			res = httptest.NewRecorder()
			router.ServeHTTP(res, httptest.NewRequest(http.MethodGet, "/checkout-info", nil))
			require.Equal(t, http.StatusInternalServerError, res.Code)
			_, err = checkout.CreateOrder(ctx, request)
			require.ErrorIs(t, err, buyerErr)
		})
	}
}

// TestPersonalRechargeRateNotWritableThroughProfile submits an administrator-only
// field through the ordinary profile handler. The legitimate username edit succeeds
// while the persisted personal recharge rate remains unchanged.
func TestPersonalRechargeRateNotWritableThroughProfile(t *testing.T) {
	ctx := context.Background()
	client := testEntClient(t)
	user, err := client.User.Create().SetEmail("profile-rate@example.com").SetPasswordHash("hash").SetBalanceRechargeMultiplier(18).Save(ctx)
	require.NoError(t, err)
	users := identity.NewUserService(identitypostgres.NewUserStore(client, integrationDB), nil, nil, nil, nil)
	handler := identityhttp.NewUserHandler(users, nil, nil, nil)
	router := gin.New()
	router.PUT("/user/profile", func(c *gin.Context) {
		c.Set(authctx.ContextKeyUser, authctx.AuthSubject{UserID: user.ID})
		handler.UpdateProfile(c)
	})
	req := httptest.NewRequest(http.MethodPut, "/user/profile", bytes.NewBufferString(`{"username":"updated","balance_recharge_multiplier":999}`))
	req.Header.Set("Content-Type", "application/json")
	res := httptest.NewRecorder()
	router.ServeHTTP(res, req)
	require.Equal(t, http.StatusOK, res.Code, res.Body.String())
	current, err := client.User.Get(ctx, user.ID)
	require.NoError(t, err)
	require.Equal(t, "updated", current.Username)
	require.Equal(t, 18.0, *current.BalanceRechargeMultiplier)
}
