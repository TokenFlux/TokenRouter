//go:build unit

package postgres_test

import (
	"context"
	"strconv"
	"testing"

	paymentpostgres "github.com/TokenFlux/TokenRouter/internal/payment/postgres"
	sqlitetest "github.com/TokenFlux/TokenRouter/internal/testutil/sqlite"

	billingpostgres "github.com/TokenFlux/TokenRouter/internal/billing/postgres"

	"github.com/TokenFlux/TokenRouter/internal/payment"
	"github.com/stretchr/testify/require"
)

func TestBuildPaymentOrderProviderSnapshot_ExcludesSensitiveConfig(t *testing.T) {
	t.Parallel()

	sel := &payment.InstanceSelection{
		InstanceID:     "12",
		ProviderKey:    payment.TypeWxpay,
		SupportedTypes: "wxpay,wxpay_direct",
		PaymentMode:    "popup",
		Config: map[string]string{
			"privateKey": "secret",
			"apiV3Key":   "secret-v3",
			"appId":      "wx-app-id",
		},
	}

	snapshot := payment.BuildPaymentOrderProviderSnapshot(sel, payment.CreateOrderRequest{})
	require.Equal(t, map[string]any{
		"schema_version":       2,
		"provider_instance_id": "12",
		"provider_key":         payment.TypeWxpay,
		"payment_mode":         "popup",
		"merchant_app_id":      "wx-app-id",
		"currency":             "CNY",
	}, snapshot)
	require.NotContains(t, snapshot, "config")
	require.NotContains(t, snapshot, "privateKey")
	require.NotContains(t, snapshot, "apiV3Key")
	require.NotContains(t, snapshot, "supported_types")
	require.NotContains(t, snapshot, "instance_name")
	require.NotContains(t, snapshot, "merchant_id")
}

func TestCreateOrderInTx_WritesProviderSnapshot(t *testing.T) {
	ctx := context.Background()
	client := sqlitetest.NewClient(t)

	user, err := client.User.Create().
		SetEmail("snapshot@example.com").
		SetPasswordHash("hash").
		SetUsername("snapshot-user").
		Save(ctx)
	require.NoError(t, err)

	instance, err := client.PaymentProviderInstance.Create().
		SetProviderKey(payment.TypeAlipay).
		SetName("Primary Alipay").
		SetConfig(`{"secretKey":"do-not-copy"}`).
		SetSupportedTypes("alipay,alipay_direct").
		SetPaymentMode("redirect").
		SetEnabled(true).
		Save(ctx)
	require.NoError(t, err)

	svc := payment.NewCheckout(paymentpostgres.NewOrderStore(client), nil, nil, nil, payment.CheckoutRuntime{})
	order, err := svc.CreateOrderInTx(
		ctx,
		payment.CreateOrderRequest{
			UserID:      user.ID,
			PaymentType: payment.TypeAlipay,
			OrderType:   payment.OrderTypeBalance,
			ClientIP:    "127.0.0.1",
			SrcHost:     "app.example.com",
		},
		&payment.Buyer{
			ID:       user.ID,
			Email:    user.Email,
			Username: user.Username,
		},
		nil,
		&payment.PaymentConfig{
			MaxPendingOrders: 3,
			OrderTimeoutMin:  30,
		},
		88,
		88,
		payment.FeeBreakdown{BaseAmount: 88, PayAmount: 88},
		&payment.InstanceSelection{
			InstanceID:     strconv.FormatInt(instance.ID, 10),
			ProviderKey:    payment.TypeAlipay,
			SupportedTypes: "alipay,alipay_direct",
			PaymentMode:    "redirect",
			Config: map[string]string{
				"secretKey": "do-not-copy",
			},
		},
	)
	require.NoError(t, err)
	require.Equal(t, strconv.FormatInt(instance.ID, 10), valueOrEmpty(order.ProviderInstanceID))
	require.Equal(t, payment.TypeAlipay, valueOrEmpty(order.ProviderKey))
	require.Equal(t, float64(2), order.ProviderSnapshot["schema_version"])
	require.Equal(t, strconv.FormatInt(instance.ID, 10), order.ProviderSnapshot["provider_instance_id"])
	require.Equal(t, payment.TypeAlipay, order.ProviderSnapshot["provider_key"])
	require.Equal(t, "redirect", order.ProviderSnapshot["payment_mode"])
	require.NotContains(t, order.ProviderSnapshot, "config")
	require.NotContains(t, order.ProviderSnapshot, "secretKey")
	require.NotContains(t, order.ProviderSnapshot, "supported_types")
	require.NotContains(t, order.ProviderSnapshot, "instance_name")
}

// TestCreateOrderInTx_WritesSubscriptionPlanCurrencySnapshot 验证套餐标价币种随订单固化。
func TestCreateOrderInTx_WritesSubscriptionPlanCurrencySnapshot(t *testing.T) {
	ctx := context.Background()
	client := sqlitetest.NewClient(t)

	user, err := client.User.Create().
		SetEmail("plan-currency-snapshot@example.com").
		SetPasswordHash("hash").
		SetUsername("plan-currency-snapshot-user").
		Save(ctx)
	require.NoError(t, err)

	plan, err := client.SubscriptionPlan.Create().
		SetName("USD Plan").
		SetPrice(10).
		SetCurrency("USD").
		Save(ctx)
	require.NoError(t, err)

	svc := payment.NewCheckout(paymentpostgres.NewOrderStore(client), nil, nil, nil, payment.CheckoutRuntime{})
	order, err := svc.CreateOrderInTx(
		ctx,
		payment.CreateOrderRequest{
			UserID:      user.ID,
			PaymentType: payment.TypeAlipay,
			OrderType:   payment.OrderTypeSubscription,
			ClientIP:    "127.0.0.1",
			SrcHost:     "app.example.com",
		},
		&payment.Buyer{ID: user.ID, Email: user.Email, Username: user.Username},
		billingpostgres.PlanFromEntity(plan),
		&payment.PaymentConfig{MaxPendingOrders: 3, OrderTimeoutMin: 30},
		10,
		10,
		payment.FeeBreakdown{BaseAmount: 72, PayAmount: 72},
		&payment.InstanceSelection{ProviderKey: payment.TypeAlipay, PaymentMode: "redirect"},
	)
	require.NoError(t, err)
	require.Equal(t, "USD", order.PlanSnapshot.Currency)
}

func TestCreateOrderInTx_WritesFeeBreakdown(t *testing.T) {
	ctx := context.Background()
	client := sqlitetest.NewClient(t)

	user, err := client.User.Create().
		SetEmail("fee@example.com").
		SetPasswordHash("hash").
		SetUsername("fee-user").
		Save(ctx)
	require.NoError(t, err)

	instance, err := client.PaymentProviderInstance.Create().
		SetProviderKey(payment.TypeStripe).
		SetName("Stripe").
		SetConfig("{}").
		SetSupportedTypes("card,link").
		SetPaymentMode("stripe").
		SetEnabled(true).
		Save(ctx)
	require.NoError(t, err)

	breakdown := payment.CalculatePayAmountWithFee(100, payment.FeeConfig{FixedFee: 2.5, FeeRate: 2.2})
	svc := payment.NewCheckout(paymentpostgres.NewOrderStore(client), nil, nil, nil, payment.CheckoutRuntime{})
	order, err := svc.CreateOrderInTx(
		ctx,
		payment.CreateOrderRequest{UserID: user.ID, PaymentType: payment.TypeStripe, OrderType: payment.OrderTypeBalance},
		&payment.Buyer{ID: user.ID, Email: user.Email, Username: user.Username},
		nil,
		&payment.PaymentConfig{MaxPendingOrders: 3, OrderTimeoutMin: 30},
		100,
		100,
		breakdown,
		&payment.InstanceSelection{InstanceID: strconv.FormatInt(instance.ID, 10), ProviderKey: payment.TypeStripe, SupportedTypes: "card,link", PaymentMode: "stripe"},
	)
	require.NoError(t, err)
	require.Equal(t, 104.7, order.PayAmount)
	require.Equal(t, 2.2, order.FeeRate)
	require.Equal(t, 2.5, order.FeeFixed)
	require.Equal(t, 2.2, order.FeeRateAmount)
	require.Equal(t, 4.7, order.FeeAmount)
}

func TestBuildPaymentOrderProviderSnapshot_UsesWxpayJSAPIAppIDForOpenIDOrders(t *testing.T) {
	t.Parallel()

	snapshot := payment.BuildPaymentOrderProviderSnapshot(&payment.InstanceSelection{
		InstanceID:  "88",
		ProviderKey: payment.TypeWxpay,
		Config: map[string]string{
			"appId":   "wx-open-app",
			"mpAppId": "wx-mp-app",
			"mchId":   "mch-88",
		},
		PaymentMode: "jsapi",
	}, payment.CreateOrderRequest{OpenID: "openid-123"})

	require.Equal(t, "wx-mp-app", snapshot["merchant_app_id"])
	require.Equal(t, "mch-88", snapshot["merchant_id"])
	require.Equal(t, "CNY", snapshot["currency"])
}

func TestBuildPaymentOrderProviderSnapshot_IncludesAlipayMerchantIdentity(t *testing.T) {
	t.Parallel()

	snapshot := payment.BuildPaymentOrderProviderSnapshot(&payment.InstanceSelection{
		InstanceID:  "21",
		ProviderKey: payment.TypeAlipay,
		Config: map[string]string{
			"appId":      "alipay-app-21",
			"privateKey": "secret",
		},
		PaymentMode: "redirect",
	}, payment.CreateOrderRequest{})

	require.Equal(t, "alipay-app-21", snapshot["merchant_app_id"])
	require.NotContains(t, snapshot, "privateKey")
}

func TestBuildPaymentOrderProviderSnapshot_IncludesEasyPayMerchantIdentity(t *testing.T) {
	t.Parallel()

	snapshot := payment.BuildPaymentOrderProviderSnapshot(&payment.InstanceSelection{
		InstanceID:  "66",
		ProviderKey: payment.TypeEasyPay,
		Config: map[string]string{
			"pid":  "easypay-merchant-66",
			"pkey": "secret",
		},
		PaymentMode: "popup",
	}, payment.CreateOrderRequest{PaymentType: payment.TypeAlipay})

	require.Equal(t, "easypay-merchant-66", snapshot["merchant_id"])
	require.NotContains(t, snapshot, "pkey")
}

func TestBuildPaymentOrderProviderSnapshot_IncludesProviderCurrency(t *testing.T) {
	t.Parallel()

	stripeSnapshot := payment.BuildPaymentOrderProviderSnapshot(&payment.InstanceSelection{
		InstanceID:  "77",
		ProviderKey: payment.TypeStripe,
		Config: map[string]string{
			"currency": "hkd",
		},
	}, payment.CreateOrderRequest{})
	require.Equal(t, "HKD", stripeSnapshot["currency"])

	airwallexSnapshot := payment.BuildPaymentOrderProviderSnapshot(&payment.InstanceSelection{
		InstanceID:  "78",
		ProviderKey: payment.TypeAirwallex,
		Config: map[string]string{
			"currency":  "usd",
			"accountId": "acct-78",
		},
	}, payment.CreateOrderRequest{})
	require.Equal(t, "USD", airwallexSnapshot["currency"])
	require.Equal(t, "acct-78", airwallexSnapshot["merchant_id"])
}

func valueOrEmpty(v *string) string {
	if v == nil {
		return ""
	}
	return *v
}
