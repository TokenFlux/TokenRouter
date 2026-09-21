package payment_test

import (
	"strings"
	"testing"
	"time"

	"github.com/TokenFlux/TokenRouter/internal/billing"
	"github.com/TokenFlux/TokenRouter/internal/pkg/apperror"

	"github.com/TokenFlux/TokenRouter/internal/payment"
)

func TestShouldUseAlipayMobilePrecreate(t *testing.T) {
	t.Parallel()

	enabled := &payment.PaymentConfig{AlipayMobilePrecreateDeepLink: true}
	officialAlipay := &payment.InstanceSelection{ProviderKey: payment.TypeAlipay}

	tests := []struct {
		name string
		req  payment.CreateOrderRequest
		cfg  *payment.PaymentConfig
		sel  *payment.InstanceSelection
		want bool
	}{
		{name: "mobile official alipay with switch", req: payment.CreateOrderRequest{IsMobile: true}, cfg: enabled, sel: officialAlipay, want: true},
		{name: "desktop remains unchanged", req: payment.CreateOrderRequest{IsMobile: false}, cfg: enabled, sel: officialAlipay, want: false},
		{name: "switch disabled keeps wap", req: payment.CreateOrderRequest{IsMobile: true}, cfg: &payment.PaymentConfig{}, sel: officialAlipay, want: false},
		{name: "other provider remains unchanged", req: payment.CreateOrderRequest{IsMobile: true}, cfg: enabled, sel: &payment.InstanceSelection{ProviderKey: payment.TypeEasyPay}, want: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if got := payment.ShouldUseAlipayMobilePrecreate(tt.req, tt.cfg, tt.sel); got != tt.want {
				t.Fatalf("shouldUseAlipayMobilePrecreate() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestIsOfficialAlipayProviderInstance(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		instance *payment.ProviderInstance
		want     bool
	}{
		{name: "nil instance", instance: nil, want: false},
		{name: "official alipay", instance: &payment.ProviderInstance{ProviderKey: payment.TypeAlipay}, want: true},
		{name: "normalized official alipay", instance: &payment.ProviderInstance{ProviderKey: " ALIPAY "}, want: true},
		{name: "easypay alipay route", instance: &payment.ProviderInstance{ProviderKey: payment.TypeEasyPay}, want: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if got := payment.ConfigIsOfficialAlipayProviderInstance(tt.instance); got != tt.want {
				t.Fatalf("payment.ConfigIsOfficialAlipayProviderInstance() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestBuildCreateOrderResponseDefaultsToOrderCreated(t *testing.T) {
	t.Parallel()

	expiresAt := time.Date(2026, 4, 16, 12, 0, 0, 0, time.UTC)
	resp := payment.BuildCreateOrderResponse(
		&payment.Order{
			ID:         42,
			Amount:     12.34,
			FeeRate:    0.03,
			ExpiresAt:  expiresAt,
			OutTradeNo: "sub2_42",
		},
		payment.CreateOrderRequest{PaymentType: payment.TypeWxpay},
		12.71,
		&payment.InstanceSelection{PaymentMode: "qrcode"},
		&payment.CreatePaymentResponse{
			TradeNo: "sub2_42",
			QRCode:  "weixin://wxpay/bizpayurl?pr=test",
		},
		payment.CreatePaymentResultOrderCreated,
	)

	if resp.ResultType != payment.CreatePaymentResultOrderCreated {
		t.Fatalf("result type = %q, want %q", resp.ResultType, payment.CreatePaymentResultOrderCreated)
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

func TestBuildCreateOrderResponseCopiesJSAPIPayload(t *testing.T) {
	t.Parallel()

	jsapiPayload := &payment.WechatJSAPIPayload{
		AppID:     "wx123",
		TimeStamp: "1712345678",
		NonceStr:  "nonce-123",
		Package:   "prepay_id=wx123",
		SignType:  "RSA",
		PaySign:   "signed-payload",
	}
	resp := payment.BuildCreateOrderResponse(
		&payment.Order{
			ID:         88,
			Amount:     66.88,
			FeeRate:    0.01,
			ExpiresAt:  time.Date(2026, 4, 16, 13, 0, 0, 0, time.UTC),
			OutTradeNo: "sub2_88",
		},
		payment.CreateOrderRequest{PaymentType: payment.TypeWxpay},
		67.55,
		&payment.InstanceSelection{PaymentMode: "popup"},
		&payment.CreatePaymentResponse{
			TradeNo:    "sub2_88",
			ResultType: payment.CreatePaymentResultJSAPIReady,
			JSAPI:      jsapiPayload,
		},
		payment.CreatePaymentResultJSAPIReady,
	)

	if resp.ResultType != payment.CreatePaymentResultJSAPIReady {
		t.Fatalf("result type = %q, want %q", resp.ResultType, payment.CreatePaymentResultJSAPIReady)
	}
	if resp.JSAPI == nil || resp.JSAPIPayload == nil {
		t.Fatal("expected jsapi payload aliases to be populated")
	}
	if resp.JSAPI != jsapiPayload || resp.JSAPIPayload != jsapiPayload {
		t.Fatal("expected jsapi aliases to preserve the original pointer")
	}
}

func TestBuildCreateOrderResponseCopiesStripeInvoiceFields(t *testing.T) {
	t.Parallel()

	resp := payment.BuildCreateOrderResponse(
		&payment.Order{
			ID:         99,
			Amount:     99.99,
			FeeRate:    0,
			ExpiresAt:  time.Date(2026, 4, 16, 14, 0, 0, 0, time.UTC),
			OutTradeNo: "sub2_99",
		},
		payment.CreateOrderRequest{PaymentType: payment.TypeStripe},
		99.99,
		&payment.InstanceSelection{PaymentMode: "stripe"},
		&payment.CreatePaymentResponse{
			TradeNo:       "pi_99",
			ClientSecret:  "pi_99_secret_abc",
			CustomerID:    "cus_99",
			InvoiceID:     "in_99",
			InvoiceURL:    "https://stripe.example/invoice/in_99",
			InvoicePDF:    "https://stripe.example/invoice/in_99.pdf",
			InvoiceStatus: "open",
		},
		payment.CreatePaymentResultOrderCreated,
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

func TestBuildProviderCreatePaymentRequestCopiesExpiresAt(t *testing.T) {
	t.Parallel()

	expiresAt := time.Date(2026, 5, 7, 12, 30, 0, 0, time.UTC)
	req := payment.BuildProviderCreatePaymentRequest(
		payment.CreateOrderRequest{
			PaymentType: payment.TypeStripe,
			ReturnURL:   "https://app.example.com/payment/result",
		},
		&payment.InstanceSelection{SupportedTypes: "stripe"},
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

func TestSanitizeCreatePaymentResponseDetailsRemovesNULBytes(t *testing.T) {
	t.Parallel()

	resp := &payment.CreatePaymentResponse{
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

	payment.SanitizeCreatePaymentResponseDetails(resp)

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

func TestValidateSelectedCreateOrderAmountCurrencyRejectsFractionalZeroDecimal(t *testing.T) {
	t.Parallel()

	err := payment.ValidateSelectedCreateOrderAmountCurrency("100.50", &payment.InstanceSelection{
		ProviderKey: payment.TypeStripe,
		Config:      map[string]string{"currency": "JPY"},
	})
	if err == nil {
		t.Fatal("expected fractional JPY amount to fail")
	}
	if appErr := apperror.FromError(err); appErr.Reason != "INVALID_AMOUNT" {
		t.Fatalf("reason = %q, want INVALID_AMOUNT", appErr.Reason)
	}
}

func TestCalculateCreateOrderPayAmountUsesCurrencyPrecision(t *testing.T) {
	t.Parallel()

	_, amountStr, amount, err := payment.CalculateCreateOrderPayAmount(100, payment.FeeConfig{FeeRate: 2.5}, "JPY")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if amountStr != "103" || amount != 103 {
		t.Fatalf("JPY pay amount = (%q, %v), want (103, 103)", amountStr, amount)
	}

	_, amountStr, amount, err = payment.CalculateCreateOrderPayAmount(12.345, payment.FeeConfig{FeeRate: 1}, "KWD")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if amountStr != "12.469" || amount != 12.469 {
		t.Fatalf("KWD pay amount = (%q, %v), want (12.469, 12.469)", amountStr, amount)
	}
}

func TestCalculateCreateOrderPayAmountForSubscriptionKeepsDirectPriceWithFixedFee(t *testing.T) {
	t.Parallel()

	_, amountStr, amount, err := payment.CalculateCreateOrderPayAmount(69.90, payment.FeeConfig{FixedFee: 2.70}, "CNY")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if amountStr != "72.60" || amount != 72.60 {
		t.Fatalf("subscription CNY pay amount = (%q, %v), want (72.60, 72.60)", amountStr, amount)
	}
}

func TestCalculateCreateOrderPayAmountForSubscriptionConvertsCNYPriceWhenRateConfigured(t *testing.T) {
	t.Parallel()

	_, amountStr, amount, err := payment.CalculateCreateOrderPayAmountForOrderType(9.99, payment.FeeConfig{}, "CNY", payment.OrderTypeSubscription, 7.15)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if amountStr != "71.43" || amount != 71.43 {
		t.Fatalf("subscription CNY pay amount = (%q, %v), want (71.43, 71.43)", amountStr, amount)
	}
}

func TestCalculateCreateOrderPayAmountForSubscriptionAppliesFeeAfterCNYConversion(t *testing.T) {
	t.Parallel()

	_, amountStr, amount, err := payment.CalculateCreateOrderPayAmountForOrderType(9.99, payment.FeeConfig{FeeRate: 2.5}, "CNY", payment.OrderTypeSubscription, 7.15)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if amountStr != "73.22" || amount != 73.22 {
		t.Fatalf("subscription CNY pay amount with fee = (%q, %v), want (73.22, 73.22)", amountStr, amount)
	}
}

func TestCalculateCreateOrderPayAmountForSubscriptionKeepsNonCNYPrice(t *testing.T) {
	t.Parallel()

	_, amountStr, amount, err := payment.CalculateCreateOrderPayAmountForOrderType(9.99, payment.FeeConfig{}, "USD", payment.OrderTypeSubscription, 7.15)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if amountStr != "9.99" || amount != 9.99 {
		t.Fatalf("subscription USD pay amount = (%q, %v), want (9.99, 9.99)", amountStr, amount)
	}
}

// 换算是 opt-in：未配置汇率（rate=0）时，CNY 订阅保持 price 直付的存量行为。
// 该测试锁住存量部署升级后行为不变的兼容承诺。
func TestCalculateCreateOrderPayAmountForSubscriptionKeepsDirectPriceWhenRateDisabled(t *testing.T) {
	t.Parallel()

	_, amountStr, amount, err := payment.CalculateCreateOrderPayAmountForOrderType(9.99, payment.FeeConfig{}, "CNY", payment.OrderTypeSubscription, 0)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if amountStr != "9.99" || amount != 9.99 {
		t.Fatalf("subscription CNY pay amount without rate = (%q, %v), want (9.99, 9.99)", amountStr, amount)
	}
}

// 汇率只作用于订阅订单，余额充值订单不受影响。
func TestCalculateCreateOrderPayAmountForBalanceIgnoresSubscriptionRate(t *testing.T) {
	t.Parallel()

	_, amountStr, amount, err := payment.CalculateCreateOrderPayAmountForOrderType(50, payment.FeeConfig{}, "CNY", payment.OrderTypeBalance, 7.15)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if amountStr != "50.00" || amount != 50 {
		t.Fatalf("balance CNY pay amount = (%q, %v), want (50.00, 50)", amountStr, amount)
	}
}

func TestCalculateCreditedBalanceStillUsesRechargeMultiplier(t *testing.T) {
	t.Parallel()

	got := payment.CalculateCreditedBalance(10, 0.14)
	if got != 1.4 {
		t.Fatalf("credited balance = %v, want 1.4", got)
	}

	got = payment.CalculateCreditedBalance(69.90, 10)
	if got != 699 {
		t.Fatalf("credited balance = %v, want 699", got)
	}
}

func TestCalculateCreateOrderPayAmountKeepsCurrencyPrecisionWithoutRechargeMultiplier(t *testing.T) {
	t.Parallel()

	_, amountStr, amount, err := payment.CalculateCreateOrderPayAmount(100, payment.FeeConfig{FeeRate: 2.5}, "JPY")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if amountStr != "103" || amount != 103 {
		t.Fatalf("JPY pay amount = (%q, %v), want (103, 103)", amountStr, amount)
	}
}

func TestCalculateCreateOrderPayAmountRejectsFractionalZeroDecimal(t *testing.T) {
	t.Parallel()

	_, _, _, err := payment.CalculateCreateOrderPayAmount(100.5, payment.FeeConfig{}, "JPY")
	if err == nil {
		t.Fatal("expected fractional JPY amount to fail")
	}
	if appErr := apperror.FromError(err); appErr.Reason != "INVALID_AMOUNT" {
		t.Fatalf("reason = %q, want INVALID_AMOUNT", appErr.Reason)
	}
}

func TestComputeValidityDaysSupportsSingularAndPluralUnits(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		days int
		unit string
		want int
	}{
		{name: "days", days: 1, unit: "days", want: 1},
		{name: "week", days: 1, unit: "week", want: 7},
		{name: "weeks", days: 2, unit: "weeks", want: 14},
		{name: "month", days: 1, unit: "month", want: 30},
		{name: "months", days: 1, unit: "months", want: 30},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			if got := billing.ComputeValidityDays(tt.days, tt.unit); got != tt.want {
				t.Fatalf("psComputeValidityDays(%d, %q) = %d, want %d", tt.days, tt.unit, got, tt.want)
			}
		})
	}
}

func TestBuildPaymentSubjectAppliesAffixToSubscriptionPlanProductName(t *testing.T) {
	t.Parallel()

	svc := payment.NewCheckout(nil, nil, nil, nil, payment.CheckoutRuntime{})
	cfg := &payment.PaymentConfig{
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

func TestBuildPaymentSubjectAppliesAffixToSubscriptionPlanDefaultName(t *testing.T) {
	t.Parallel()

	svc := payment.NewCheckout(nil, nil, nil, nil, payment.CheckoutRuntime{})
	cfg := &payment.PaymentConfig{
		ProductNamePrefix: "PRE",
		ProductNameSuffix: "SUF",
	}
	plan := &billing.SubscriptionPlan{Name: "Team Monthly"}

	got := svc.BuildPaymentSubject(plan, 0, cfg, nil)
	if got != "PRE Sub2API Subscription Team Monthly SUF" {
		t.Fatalf("buildPaymentSubject() = %q, want %q", got, "PRE Sub2API Subscription Team Monthly SUF")
	}
}
