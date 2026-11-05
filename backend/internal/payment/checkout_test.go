package payment

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/TokenFlux/TokenRouter/internal/billing"
	"github.com/TokenFlux/TokenRouter/internal/pkg/apperror"
)

// checkout_test.go checks checkout display fields separately from gateway
// money and provider initiation details. They compare explicit response result
// types, JSAPI pointer aliases, PostgreSQL text cleanup and currency-specific
// fee results, using pure local fixtures without an external payment request.

// TestShouldUseAlipayMobilePrecreate varies device flag, preference and
// provider identity. It requires the listed official mobile case to enable
// precreate while desktop, disabled and other-provider cases return false.
func TestShouldUseAlipayMobilePrecreate(t *testing.T) {
	t.Parallel()

	enabled := &PaymentConfig{AlipayMobilePrecreateDeepLink: true}
	officialAlipay := &InstanceSelection{ProviderKey: TypeAlipay}

	tests := []struct {
		name string // parallel scenario display name
		// req, cfg and sel jointly supply the precreate decision. The parallel cases
		// read these request values and shared configuration/selection pointers
		// without mutating their fixture objects.
		req  CreateOrderRequest // supplied checkout device flags
		cfg  *PaymentConfig     // shared precreate preference fixture
		sel  *InstanceSelection // shared selected provider fixture
		want bool               // expected precreate predicate result
	}{
		{name: "mobile official alipay with switch", req: CreateOrderRequest{IsMobile: true}, cfg: enabled, sel: officialAlipay, want: true},
		{name: "desktop remains unchanged", req: CreateOrderRequest{IsMobile: false}, cfg: enabled, sel: officialAlipay, want: false},
		{name: "switch disabled keeps wap", req: CreateOrderRequest{IsMobile: true}, cfg: &PaymentConfig{}, sel: officialAlipay, want: false},
		{name: "other provider remains unchanged", req: CreateOrderRequest{IsMobile: true}, cfg: enabled, sel: &InstanceSelection{ProviderKey: TypeEasyPay}, want: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if got := ShouldUseAlipayMobilePrecreate(tt.req, tt.cfg, tt.sel); got != tt.want {
				t.Fatalf("shouldUseAlipayMobilePrecreate() = %v, want %v", got, tt.want)
			}
		})
	}
}

// TestBuildCreateOrderResponseDefaultsToOrderCreated supplies order_created as
// the result type while assembling a QR response. It checks that explicit
// type, merchant number, QR, expiration and absent JSAPI aliases; default
// selection occurs in InvokeProvider separately.
func TestBuildCreateOrderResponseDefaultsToOrderCreated(t *testing.T) {
	t.Parallel()

	expiresAt := time.Date(2026, 4, 16, 12, 0, 0, 0, time.UTC)
	resp := BuildCreateOrderResponse(
		&Order{
			ID:         42,
			Amount:     12.34,
			FeeRate:    0.03,
			ExpiresAt:  expiresAt,
			OutTradeNo: "sub2_42",
		},
		CreateOrderRequest{PaymentType: TypeWxpay},
		12.71,
		&InstanceSelection{PaymentMode: "qrcode"},
		&CreatePaymentResponse{
			TradeNo: "sub2_42",
			QRCode:  "weixin://wxpay/bizpayurl?pr=test",
		},
		CreatePaymentResultOrderCreated,
	)

	if resp.ResultType != CreatePaymentResultOrderCreated {
		t.Fatalf("result type = %q, want %q", resp.ResultType, CreatePaymentResultOrderCreated)
	}
	if resp.OutTradeNo != "sub2_42" {
		t.Fatalf("out_trade_no = %q, want %q", resp.OutTradeNo, "sub2_42")
	}
	if resp.QRCode != "weixin://wxpay/bizpayurl?pr=test" {
		t.Fatalf("qr_code = %q, want %q", resp.QRCode, "weixin://wxpay/bizpayurl?pr=test")
	}
	if resp.JSAPI != nil || resp.JSAPIPayload != nil {
		t.Fatal("order_created response should not include jsapi payload")
	}
	if !resp.ExpiresAt.Equal(expiresAt) {
		t.Fatalf("expires_at = %v, want %v", resp.ExpiresAt, expiresAt)
	}
}

// TestBuildCreateOrderResponseCopiesJSAPIPayload assembles a ready JSAPI
// response from one payload pointer. Both exposed aliases must equal that
// original pointer, protecting shared identity rather than a deep-copy
// guarantee.
func TestBuildCreateOrderResponseCopiesJSAPIPayload(t *testing.T) {
	t.Parallel()

	jsapiPayload := &WechatJSAPIPayload{
		AppID:     "wx123",
		TimeStamp: "1712345678",
		NonceStr:  "nonce-123",
		Package:   "prepay_id=wx123",
		SignType:  "RSA",
		PaySign:   "signed-payload",
	}
	resp := BuildCreateOrderResponse(
		&Order{
			ID:         88,
			Amount:     66.88,
			FeeRate:    0.01,
			ExpiresAt:  time.Date(2026, 4, 16, 13, 0, 0, 0, time.UTC),
			OutTradeNo: "sub2_88",
		},
		CreateOrderRequest{PaymentType: TypeWxpay},
		67.55,
		&InstanceSelection{PaymentMode: "popup"},
		&CreatePaymentResponse{
			TradeNo:    "sub2_88",
			ResultType: CreatePaymentResultJSAPIReady,
			JSAPI:      jsapiPayload,
		},
		CreatePaymentResultJSAPIReady,
	)

	if resp.ResultType != CreatePaymentResultJSAPIReady {
		t.Fatalf("result type = %q, want %q", resp.ResultType, CreatePaymentResultJSAPIReady)
	}
	if resp.JSAPI == nil || resp.JSAPIPayload == nil {
		t.Fatal("expected jsapi payload aliases to be populated")
	}
	if resp.JSAPI != jsapiPayload || resp.JSAPIPayload != jsapiPayload {
		t.Fatal("expected jsapi aliases to preserve the original pointer")
	}
}

// TestBuildCreateOrderResponseCopiesStripeInvoiceFields supplies
// client/customer/invoice values in a provider result. The constructed
// response must expose each tested string, protecting invoice display transfer
// through checkout response assembly.
func TestBuildCreateOrderResponseCopiesStripeInvoiceFields(t *testing.T) {
	t.Parallel()

	resp := BuildCreateOrderResponse(
		&Order{
			ID:         99,
			Amount:     99.99,
			FeeRate:    0,
			ExpiresAt:  time.Date(2026, 4, 16, 14, 0, 0, 0, time.UTC),
			OutTradeNo: "sub2_99",
		},
		CreateOrderRequest{PaymentType: TypeStripe},
		99.99,
		&InstanceSelection{PaymentMode: "stripe"},
		&CreatePaymentResponse{
			TradeNo:       "pi_99",
			ClientSecret:  "pi_99_secret_abc",
			CustomerID:    "cus_99",
			InvoiceID:     "in_99",
			InvoiceURL:    "https://stripe.example/invoice/in_99",
			InvoicePDF:    "https://stripe.example/invoice/in_99.pdf",
			InvoiceStatus: "open",
		},
		CreatePaymentResultOrderCreated,
	)

	if resp.ClientSecret != "pi_99_secret_abc" {
		t.Fatalf("client_secret = %q", resp.ClientSecret)
	}
	if resp.CustomerID != "cus_99" {
		t.Fatalf("customer_id = %q", resp.CustomerID)
	}
	if resp.InvoiceID != "in_99" {
		t.Fatalf("invoice_id = %q", resp.InvoiceID)
	}
	if resp.InvoiceURL != "https://stripe.example/invoice/in_99" {
		t.Fatalf("invoice_url = %q", resp.InvoiceURL)
	}
	if resp.InvoicePDF != "https://stripe.example/invoice/in_99.pdf" {
		t.Fatalf("invoice_pdf = %q", resp.InvoicePDF)
	}
	if resp.InvoiceStatus != "open" {
		t.Fatalf("invoice_status = %q", resp.InvoiceStatus)
	}
}

// TestBuildProviderCreatePaymentRequestCopiesExpiresAt supplies a fixed
// expiration and Stripe subtype text. The adapter request must retain both
// values, protecting local expiry/subtype transfer into provider initiation
// input.
func TestBuildProviderCreatePaymentRequestCopiesExpiresAt(t *testing.T) {
	t.Parallel()

	expiresAt := time.Date(2026, 5, 7, 12, 30, 0, 0, time.UTC)
	req := BuildProviderCreatePaymentRequest(
		CreateOrderRequest{
			PaymentType: TypeStripe,
			ReturnURL:   "https://app.example.com/payment/result",
		},
		&InstanceSelection{SupportedTypes: "stripe"},
		"sub2_123",
		"10.00",
		"TokenRouter Balance",
		expiresAt,
	)

	if !req.ExpiresAt.Equal(expiresAt) {
		t.Fatalf("expires_at = %s, want %s", req.ExpiresAt, expiresAt)
	}
	if req.InstanceSubMethods != "stripe" {
		t.Fatalf("instance sub methods = %q, want stripe", req.InstanceSubMethods)
	}
}

// TestSanitizeCreatePaymentResponseDetailsRemovesNULBytes supplies NULs in
// persisted provider text and in a transient client secret. It requires the
// eight stored-detail values to be cleaned while the client secret remains
// exactly as supplied.
func TestSanitizeCreatePaymentResponseDetailsRemovesNULBytes(t *testing.T) {
	t.Parallel()

	resp := &CreatePaymentResponse{
		TradeNo:       "trade\x00-no",
		PayURL:        "https://pay.example.com/\x00checkout",
		QRCode:        "wxp://payment-token\x00",
		ClientSecret:  "secret\x00unchanged",
		CustomerID:    "cus\x00-1",
		InvoiceID:     "in\x00-1",
		InvoiceURL:    "https://pay.example.com/invoice\x00",
		InvoicePDF:    "https://pay.example.com/invoice\x00.pdf",
		InvoiceStatus: "op\x00en",
	}

	SanitizeCreatePaymentResponseDetails(resp)

	if strings.ContainsRune(resp.TradeNo, 0) {
		t.Fatalf("trade_no still contains NUL: %q", resp.TradeNo)
	}
	if strings.ContainsRune(resp.PayURL, 0) {
		t.Fatalf("pay_url still contains NUL: %q", resp.PayURL)
	}
	if strings.ContainsRune(resp.QRCode, 0) {
		t.Fatalf("qr_code still contains NUL: %q", resp.QRCode)
	}
	if resp.TradeNo != "trade-no" {
		t.Fatalf("trade_no = %q, want trade-no", resp.TradeNo)
	}
	if resp.PayURL != "https://pay.example.com/checkout" {
		t.Fatalf("pay_url = %q, want sanitized URL", resp.PayURL)
	}
	if resp.QRCode != "wxp://payment-token" {
		t.Fatalf("qr_code = %q, want sanitized QR code", resp.QRCode)
	}
	if resp.CustomerID != "cus-1" || resp.InvoiceID != "in-1" {
		t.Fatalf("stripe ids were not sanitized: customer=%q invoice=%q", resp.CustomerID, resp.InvoiceID)
	}
	if resp.InvoiceURL != "https://pay.example.com/invoice" || resp.InvoicePDF != "https://pay.example.com/invoice.pdf" {
		t.Fatalf("stripe invoice urls were not sanitized: url=%q pdf=%q", resp.InvoiceURL, resp.InvoicePDF)
	}
	if resp.InvoiceStatus != "open" {
		t.Fatalf("invoice_status = %q, want open", resp.InvoiceStatus)
	}
	if resp.ClientSecret != "secret\x00unchanged" {
		t.Fatalf("client_secret = %q, should not be touched by payment detail sanitization", resp.ClientSecret)
	}
}

// TestValidateSelectedCreateOrderAmountCurrencyRejectsFractionalZeroDecimal
// supplies fractional JPY text on a Stripe selection. Validation must fail
// with INVALID_AMOUNT, protecting selected-currency precision checking before
// gateway use.
func TestValidateSelectedCreateOrderAmountCurrencyRejectsFractionalZeroDecimal(t *testing.T) {
	t.Parallel()

	err := ValidateSelectedCreateOrderAmountCurrency("100.50", &InstanceSelection{
		ProviderKey: TypeStripe,
		Config:      map[string]string{"currency": "JPY"},
	})
	if err == nil {
		t.Fatal("expected fractional JPY amount to fail")
	}
	if appErr := apperror.FromError(err); appErr.Reason != "INVALID_AMOUNT" {
		t.Fatalf("reason = %q, want INVALID_AMOUNT", appErr.Reason)
	}
}

// TestCalculateCreateOrderPayAmountUsesCurrencyPrecision calculates percentage
// fees for JPY and KWD fixtures. It requires exact expected formatted and
// float totals, protecting whole-unit and three-decimal charge behavior.
func TestCalculateCreateOrderPayAmountUsesCurrencyPrecision(t *testing.T) {
	t.Parallel()

	_, amountStr, amount, err := CalculateCreateOrderPayAmount(100, FeeConfig{FeeRate: 2.5}, "JPY")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if amountStr != "103" || amount != 103 {
		t.Fatalf("JPY pay amount = (%q, %v), want (103, 103)", amountStr, amount)
	}

	_, amountStr, amount, err = CalculateCreateOrderPayAmount(12.345, FeeConfig{FeeRate: 1}, "KWD")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if amountStr != "12.469" || amount != 12.469 {
		t.Fatalf("KWD pay amount = (%q, %v), want (12.469, 12.469)", amountStr, amount)
	}
}

// TestCalculateCreateOrderPayAmountForSubscriptionKeepsDirectPriceWithFixedFee
// calls the base fee helper with a CNY price and fixed fee. It requires their
// formatted sum and numeric result, without invoking subscription conversion
// in this test.
func TestCalculateCreateOrderPayAmountForSubscriptionKeepsDirectPriceWithFixedFee(t *testing.T) {
	t.Parallel()

	_, amountStr, amount, err := CalculateCreateOrderPayAmount(69.90, FeeConfig{FixedFee: 2.70}, "CNY")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if amountStr != "72.60" || amount != 72.60 {
		t.Fatalf("subscription CNY pay amount = (%q, %v), want (72.60, 72.60)", amountStr, amount)
	}
}

// TestCalculateCreateOrderPayAmountForSubscriptionConvertsCNYPriceWhenRateConfigured
// applies a positive USD-to-CNY rate to the subscription fixture. The
// formatted and numeric result must equal the expected currency-rounded
// converted amount.
func TestCalculateCreateOrderPayAmountForSubscriptionConvertsCNYPriceWhenRateConfigured(t *testing.T) {
	t.Parallel()

	_, amountStr, amount, err := CalculateCreateOrderPayAmountForOrderType(9.99, FeeConfig{}, "CNY", OrderTypeSubscription, 7.15)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if amountStr != "71.43" || amount != 71.43 {
		t.Fatalf("subscription CNY pay amount = (%q, %v), want (71.43, 71.43)", amountStr, amount)
	}
}

// TestCalculateCreateOrderPayAmountForSubscriptionAppliesFeeAfterCNYConversion
// combines the conversion fixture with percentage fees. It requires the
// expected converted-then-fee total in string and float forms, protecting
// their calculation order.
func TestCalculateCreateOrderPayAmountForSubscriptionAppliesFeeAfterCNYConversion(t *testing.T) {
	t.Parallel()

	_, amountStr, amount, err := CalculateCreateOrderPayAmountForOrderType(9.99, FeeConfig{FeeRate: 2.5}, "CNY", OrderTypeSubscription, 7.15)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if amountStr != "73.22" || amount != 73.22 {
		t.Fatalf("subscription CNY pay amount with fee = (%q, %v), want (73.22, 73.22)", amountStr, amount)
	}
}

// TestCalculateCreateOrderPayAmountForSubscriptionKeepsNonCNYPrice supplies a
// positive conversion rate for a USD subscription. The result must keep its
// direct price in both forms, protecting conversion's CNY condition.
func TestCalculateCreateOrderPayAmountForSubscriptionKeepsNonCNYPrice(t *testing.T) {
	t.Parallel()

	_, amountStr, amount, err := CalculateCreateOrderPayAmountForOrderType(9.99, FeeConfig{}, "USD", OrderTypeSubscription, 7.15)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if amountStr != "9.99" || amount != 9.99 {
		t.Fatalf("subscription USD pay amount = (%q, %v), want (9.99, 9.99)", amountStr, amount)
	}
}

// TestCalculateCreateOrderPayAmountForSubscriptionKeepsDirectPriceWhenRateDisabled
// supplies zero conversion rate for a CNY subscription. The result must retain
// the direct price, protecting the explicitly disabled conversion case.
func TestCalculateCreateOrderPayAmountForSubscriptionKeepsDirectPriceWhenRateDisabled(t *testing.T) {
	t.Parallel()

	_, amountStr, amount, err := CalculateCreateOrderPayAmountForOrderType(9.99, FeeConfig{}, "CNY", OrderTypeSubscription, 0)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if amountStr != "9.99" || amount != 9.99 {
		t.Fatalf("subscription CNY pay amount without rate = (%q, %v), want (9.99, 9.99)", amountStr, amount)
	}
}

// TestCalculateCreateOrderPayAmountForBalanceIgnoresSubscriptionRate supplies
// balance order type with a positive subscription rate. The gateway result
// must retain the balance principal, protecting the order-type conversion
// condition.
func TestCalculateCreateOrderPayAmountForBalanceIgnoresSubscriptionRate(t *testing.T) {
	t.Parallel()

	_, amountStr, amount, err := CalculateCreateOrderPayAmountForOrderType(50, FeeConfig{}, "CNY", OrderTypeBalance, 7.15)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if amountStr != "50.00" || amount != 50 {
		t.Fatalf("balance CNY pay amount = (%q, %v), want (50.00, 50)", amountStr, amount)
	}
}

// TestCalculateCreateOrderPayAmountKeepsCurrencyPrecisionWithoutRechargeMultiplier
// passes JPY principal and percentage fee directly to the fee helper. It
// requires the whole-unit result; no recharge multiplier is supplied by this
// helper call.
func TestCalculateCreateOrderPayAmountKeepsCurrencyPrecisionWithoutRechargeMultiplier(t *testing.T) {
	t.Parallel()

	_, amountStr, amount, err := CalculateCreateOrderPayAmount(100, FeeConfig{FeeRate: 2.5}, "JPY")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if amountStr != "103" || amount != 103 {
		t.Fatalf("JPY pay amount = (%q, %v), want (103, 103)", amountStr, amount)
	}
}

// TestCalculateCreateOrderPayAmountRejectsFractionalZeroDecimal supplies
// fractional JPY base money before fees. It requires INVALID_AMOUNT,
// protecting precision rejection before the fee helper could round that
// invalid base.
func TestCalculateCreateOrderPayAmountRejectsFractionalZeroDecimal(t *testing.T) {
	t.Parallel()

	_, _, _, err := CalculateCreateOrderPayAmount(100.5, FeeConfig{}, "JPY")
	if err == nil {
		t.Fatal("expected fractional JPY amount to fail")
	}
	if appErr := apperror.FromError(err); appErr.Reason != "INVALID_AMOUNT" {
		t.Fatalf("reason = %q, want INVALID_AMOUNT", appErr.Reason)
	}
}

// TestBuildPaymentSubjectAppliesAffixToSubscriptionPlanProductName supplies a
// custom plan product name and two affixes. The returned subject must wrap
// that name exactly, protecting configured title precedence.
func TestBuildPaymentSubjectAppliesAffixToSubscriptionPlanProductName(t *testing.T) {
	t.Parallel()

	svc := NewCheckout(nil, nil, nil, nil, CheckoutRuntime{})
	cfg := &PaymentConfig{
		ProductNamePrefix: "PRE",
		ProductNameSuffix: "SUF",
	}
	plan := &billing.SubscriptionPlan{
		Name:        "Pro Monthly",
		ProductName: "Claude Pro",
	}

	got := svc.BuildPaymentSubject(plan, 0, cfg, nil)
	if got != "PRE Claude Pro SUF" {
		t.Fatalf("buildPaymentSubject() = %q, want %q", got, "PRE Claude Pro SUF")
	}
}

// TestBuildPaymentSubjectAppliesAffixToSubscriptionPlanDefaultName supplies a
// plan without an explicit product name. The returned subject must combine the
// default subscription label/name with both affixes, protecting that fallback
// display.
func TestBuildPaymentSubjectAppliesAffixToSubscriptionPlanDefaultName(t *testing.T) {
	t.Parallel()

	svc := NewCheckout(nil, nil, nil, nil, CheckoutRuntime{})
	cfg := &PaymentConfig{
		ProductNamePrefix: "PRE",
		ProductNameSuffix: "SUF",
	}
	plan := &billing.SubscriptionPlan{Name: "Team Monthly"}

	got := svc.BuildPaymentSubject(plan, 0, cfg, nil)
	if got != "PRE TokenRouter Subscription Team Monthly SUF" {
		t.Fatalf("buildPaymentSubject() = %q, want %q", got, "PRE TokenRouter Subscription Team Monthly SUF")
	}
}

// TestCreateOrderResponseSeparatesPriceCurrency builds initiation output for a
// converted subscription. The recorded USD face price must remain distinct
// from the CNY channel charge, including its collected fees.
func TestCreateOrderResponseSeparatesPriceCurrency(t *testing.T) {
	order := &Order{OrderType: OrderTypeSubscription, Amount: 10, PayAmount: 72, FeeAmount: 2, ProviderSnapshot: map[string]any{"currency": "CNY"}}
	order.PlanSnapshot.Currency = "USD"
	result := BuildCreateOrderResponse(order, CreateOrderRequest{}, 72, &InstanceSelection{}, &CreatePaymentResponse{Currency: "CNY"}, CreatePaymentResultOrderCreated)
	if result.AmountCurrency != "USD" || result.Currency != "CNY" || result.Amount != 10 || result.PayAmount != 72 {
		t.Fatalf("unexpected price/payment fields: amount=%v %s, paid=%v %s", result.Amount, result.AmountCurrency, result.PayAmount, result.Currency)
	}
}

// TestGetBalanceRechargeRatesFailsOnBuyerLookupError supplies an unavailable
// account reader. Checkout preview must return the read error instead of
// reporting the global rate as this buyer's confirmed recharge terms.
func TestGetBalanceRechargeRatesFailsOnBuyerLookupError(t *testing.T) {
	lookupErr := errors.New("buyer unavailable")
	checkout := NewCheckout(nil, nil, nil, nil, CheckoutRuntime{
		User: func(context.Context, int64) (*Buyer, error) { return nil, lookupErr },
	})
	_, err := checkout.GetBalanceRechargeRates(context.Background(), 42, 10)
	require.ErrorIs(t, err, lookupErr)
}

// TestCheckoutRejectsUnknownOrderTypes submits noncanonical purchase types with
// a valid principal. They must fail before balance terms or subscription plans
// are selected, preventing unknown types from bypassing recharge rates.
func TestCheckoutRejectsUnknownOrderTypes(t *testing.T) {
	checkout := NewCheckout(nil, nil, nil, nil, CheckoutRuntime{})
	for _, orderType := range []string{"wallet", "Balance", "balance ", " "} {
		_, err := checkout.ValidateOrderInput(context.Background(), CreateOrderRequest{OrderType: orderType, Amount: 100}, &PaymentConfig{})
		require.Equal(t, "INVALID_CHECKOUT_ORDER_TYPE", apperror.Reason(err))
	}
}
