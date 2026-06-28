package service

import (
	"context"
	"strings"
	"testing"
	"time"

	dbent "github.com/TokenFlux/TokenRouter/ent"
	"github.com/TokenFlux/TokenRouter/internal/payment"
	infraerrors "github.com/TokenFlux/TokenRouter/internal/pkg/errors"
)

func TestBuildCreateOrderResponseDefaultsToOrderCreated(t *testing.T) {
	t.Parallel()

	expiresAt := time.Date(2026, 4, 16, 12, 0, 0, 0, time.UTC)
	resp := buildCreateOrderResponse(
		&dbent.PaymentOrder{
			ID:         42,
			Amount:     12.34,
			FeeRate:    0.03,
			ExpiresAt:  expiresAt,
			OutTradeNo: "sub2_42",
		},
		CreateOrderRequest{PaymentType: payment.TypeWxpay},
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
	resp := buildCreateOrderResponse(
		&dbent.PaymentOrder{
			ID:         88,
			Amount:     66.88,
			FeeRate:    0.01,
			ExpiresAt:  time.Date(2026, 4, 16, 13, 0, 0, 0, time.UTC),
			OutTradeNo: "sub2_88",
		},
		CreateOrderRequest{PaymentType: payment.TypeWxpay},
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

	resp := buildCreateOrderResponse(
		&dbent.PaymentOrder{
			ID:         99,
			Amount:     99.99,
			FeeRate:    0,
			ExpiresAt:  time.Date(2026, 4, 16, 14, 0, 0, 0, time.UTC),
			OutTradeNo: "sub2_99",
		},
		CreateOrderRequest{PaymentType: payment.TypeStripe},
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
	req := buildProviderCreatePaymentRequest(
		CreateOrderRequest{
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

func TestValidateSelectedCreateOrderAmountCurrencyRejectsFractionalZeroDecimal(t *testing.T) {
	t.Parallel()

	err := validateSelectedCreateOrderAmountCurrency("100.50", &payment.InstanceSelection{
		ProviderKey: payment.TypeStripe,
		Config:      map[string]string{"currency": "JPY"},
	})
	if err == nil {
		t.Fatal("expected fractional JPY amount to fail")
	}
	if appErr := infraerrors.FromError(err); appErr.Reason != "INVALID_AMOUNT" {
		t.Fatalf("reason = %q, want INVALID_AMOUNT", appErr.Reason)
	}
}

func TestCalculateCreateOrderPayAmountUsesCurrencyPrecision(t *testing.T) {
	t.Parallel()

	_, amountStr, amount, err := calculateCreateOrderPayAmount(100, payment.FeeConfig{FeeRate: 2.5}, "JPY")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if amountStr != "103" || amount != 103 {
		t.Fatalf("JPY pay amount = (%q, %v), want (103, 103)", amountStr, amount)
	}

	_, amountStr, amount, err = calculateCreateOrderPayAmount(12.345, payment.FeeConfig{FeeRate: 1}, "KWD")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if amountStr != "12.469" || amount != 12.469 {
		t.Fatalf("KWD pay amount = (%q, %v), want (12.469, 12.469)", amountStr, amount)
	}
}

func TestCalculateCreateOrderPayAmountForSubscriptionKeepsDirectPriceWithFixedFee(t *testing.T) {
	t.Parallel()

	_, amountStr, amount, err := calculateCreateOrderPayAmount(69.90, payment.FeeConfig{FixedFee: 2.70}, "CNY")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if amountStr != "72.60" || amount != 72.60 {
		t.Fatalf("subscription CNY pay amount = (%q, %v), want (72.60, 72.60)", amountStr, amount)
	}
}

func TestCalculateCreateOrderPayAmountForSubscriptionAppliesFeeToDirectPrice(t *testing.T) {
	t.Parallel()

	_, amountStr, amount, err := calculateCreateOrderPayAmount(69.90, payment.FeeConfig{FeeRate: 2.5}, "CNY")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if amountStr != "71.65" || amount != 71.65 {
		t.Fatalf("subscription CNY pay amount with fee = (%q, %v), want (71.65, 71.65)", amountStr, amount)
	}
}

func TestCalculateCreditedBalanceStillUsesRechargeMultiplier(t *testing.T) {
	t.Parallel()

	got := calculateCreditedBalance(10, 0.14)
	if got != 1.4 {
		t.Fatalf("credited balance = %v, want 1.4", got)
	}

	got = calculateCreditedBalance(69.90, 10)
	if got != 699 {
		t.Fatalf("credited balance = %v, want 699", got)
	}
}

func TestCalculateCreateOrderPayAmountKeepsCurrencyPrecisionWithoutRechargeMultiplier(t *testing.T) {
	t.Parallel()

	_, amountStr, amount, err := calculateCreateOrderPayAmount(100, payment.FeeConfig{FeeRate: 2.5}, "JPY")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if amountStr != "103" || amount != 103 {
		t.Fatalf("JPY pay amount = (%q, %v), want (103, 103)", amountStr, amount)
	}
}

func TestCalculateCreateOrderPayAmountRejectsFractionalZeroDecimal(t *testing.T) {
	t.Parallel()

	_, _, _, err := calculateCreateOrderPayAmount(100.5, payment.FeeConfig{}, "JPY")
	if err == nil {
		t.Fatal("expected fractional JPY amount to fail")
	}
	if appErr := infraerrors.FromError(err); appErr.Reason != "INVALID_AMOUNT" {
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

			if got := psComputeValidityDays(tt.days, tt.unit); got != tt.want {
				t.Fatalf("psComputeValidityDays(%d, %q) = %d, want %d", tt.days, tt.unit, got, tt.want)
			}
		})
	}
}

func TestBuildPaymentSubjectAppliesAffixToSubscriptionPlanProductName(t *testing.T) {
	t.Parallel()

	svc := &PaymentService{}
	cfg := &PaymentConfig{
		ProductNamePrefix: "PRE",
		ProductNameSuffix: "SUF",
	}
	plan := &dbent.SubscriptionPlan{
		Name:        "Pro Monthly",
		ProductName: "Claude Pro",
	}

	got := svc.buildPaymentSubject(plan, 0, cfg, nil)
	if got != "PRE Claude Pro SUF" {
		t.Fatalf("buildPaymentSubject() = %q, want %q", got, "PRE Claude Pro SUF")
	}
}

func TestBuildPaymentSubjectAppliesAffixToSubscriptionPlanDefaultName(t *testing.T) {
	t.Parallel()

	svc := &PaymentService{}
	cfg := &PaymentConfig{
		ProductNamePrefix: "PRE",
		ProductNameSuffix: "SUF",
	}
	plan := &dbent.SubscriptionPlan{Name: "Team Monthly"}

	got := svc.buildPaymentSubject(plan, 0, cfg, nil)
	if got != "PRE Sub2API Subscription Team Monthly SUF" {
		t.Fatalf("buildPaymentSubject() = %q, want %q", got, "PRE Sub2API Subscription Team Monthly SUF")
	}
}

func TestMaybeBuildWeChatOAuthRequiredResponse(t *testing.T) {
	t.Setenv("PAYMENT_RESUME_SIGNING_KEY", "0123456789abcdef0123456789abcdef")

	svc := newWeChatPaymentOAuthTestService(map[string]string{
		SettingKeyWeChatConnectEnabled:             "true",
		SettingKeyWeChatConnectAppID:               "wx123456",
		SettingKeyWeChatConnectAppSecret:           "wechat-secret",
		SettingKeyWeChatConnectMode:                "mp",
		SettingKeyWeChatConnectScopes:              "snsapi_base",
		SettingKeyWeChatConnectRedirectURL:         "https://api.example.com/api/v1/auth/oauth/wechat/callback",
		SettingKeyWeChatConnectFrontendRedirectURL: "/auth/wechat/callback",
	})

	resp, err := svc.maybeBuildWeChatOAuthRequiredResponse(context.Background(), CreateOrderRequest{
		Amount:          12.5,
		PaymentType:     payment.TypeWxpay,
		IsWeChatBrowser: true,
		SrcURL:          "https://merchant.example/payment?from=wechat",
		OrderType:       payment.OrderTypeBalance,
	}, 12.5, payment.FeeBreakdown{PayAmount: 12.88, FeeRate: 0.03})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resp == nil {
		t.Fatal("expected oauth_required response, got nil")
	}
	if resp.ResultType != payment.CreatePaymentResultOAuthRequired {
		t.Fatalf("result type = %q, want %q", resp.ResultType, payment.CreatePaymentResultOAuthRequired)
	}
	if resp.OAuth == nil {
		t.Fatal("expected oauth payload, got nil")
	}
	if resp.OAuth.AppID != "wx123456" {
		t.Fatalf("appid = %q, want %q", resp.OAuth.AppID, "wx123456")
	}
	if resp.OAuth.Scope != "snsapi_base" {
		t.Fatalf("scope = %q, want %q", resp.OAuth.Scope, "snsapi_base")
	}
	if resp.OAuth.RedirectURL != "/auth/wechat/payment/callback" {
		t.Fatalf("redirect_url = %q, want %q", resp.OAuth.RedirectURL, "/auth/wechat/payment/callback")
	}
	if resp.OAuth.AuthorizeURL != "/api/v1/auth/oauth/wechat/payment/start?amount=12.5&order_type=balance&payment_type=wxpay&redirect=%2Fpurchase%3Ffrom%3Dwechat&scope=snsapi_base" {
		t.Fatalf("authorize_url = %q", resp.OAuth.AuthorizeURL)
	}
}

func TestMaybeBuildWeChatOAuthRequiredResponseRequiresMPConfigInWeChat(t *testing.T) {
	t.Parallel()

	svc := newWeChatPaymentOAuthTestService(nil)

	resp, err := svc.maybeBuildWeChatOAuthRequiredResponse(context.Background(), CreateOrderRequest{
		Amount:          12.5,
		PaymentType:     payment.TypeWxpay,
		IsWeChatBrowser: true,
		SrcURL:          "https://merchant.example/payment?from=wechat",
		OrderType:       payment.OrderTypeBalance,
	}, 12.5, payment.FeeBreakdown{PayAmount: 12.88, FeeRate: 0.03})
	if resp != nil {
		t.Fatalf("expected nil response, got %+v", resp)
	}
	if err == nil {
		t.Fatal("expected error, got nil")
	}

	appErr := infraerrors.FromError(err)
	if appErr.Reason != "WECHAT_PAYMENT_MP_NOT_CONFIGURED" {
		t.Fatalf("reason = %q, want %q", appErr.Reason, "WECHAT_PAYMENT_MP_NOT_CONFIGURED")
	}
}

func TestMaybeBuildWeChatOAuthRequiredResponseRequiresResumeSigningKey(t *testing.T) {
	t.Parallel()

	svc := &PaymentService{
		configService: &PaymentConfigService{
			settingRepo: &paymentConfigSettingRepoStub{values: map[string]string{
				SettingKeyWeChatConnectEnabled:             "true",
				SettingKeyWeChatConnectAppID:               "wx123456",
				SettingKeyWeChatConnectAppSecret:           "wechat-secret",
				SettingKeyWeChatConnectMode:                "mp",
				SettingKeyWeChatConnectScopes:              "snsapi_base",
				SettingKeyWeChatConnectRedirectURL:         "https://api.example.com/api/v1/auth/oauth/wechat/callback",
				SettingKeyWeChatConnectFrontendRedirectURL: "/auth/wechat/callback",
			}},
			// Intentionally missing payment resume signing key.
			encryptionKey: nil,
		},
	}

	resp, err := svc.maybeBuildWeChatOAuthRequiredResponse(context.Background(), CreateOrderRequest{
		Amount:          12.5,
		PaymentType:     payment.TypeWxpay,
		IsWeChatBrowser: true,
		SrcURL:          "https://merchant.example/payment?from=wechat",
		OrderType:       payment.OrderTypeBalance,
	}, 12.5, payment.FeeBreakdown{PayAmount: 12.88, FeeRate: 0.03})
	if resp != nil {
		t.Fatalf("expected nil response, got %+v", resp)
	}
	if err == nil {
		t.Fatal("expected error, got nil")
	}

	appErr := infraerrors.FromError(err)
	if appErr.Reason != "PAYMENT_RESUME_NOT_CONFIGURED" {
		t.Fatalf("reason = %q, want %q", appErr.Reason, "PAYMENT_RESUME_NOT_CONFIGURED")
	}
}

func TestMaybeBuildWeChatOAuthRequiredResponseFallsBackToConfiguredLegacySigningKey(t *testing.T) {
	svc := &PaymentService{
		configService: &PaymentConfigService{
			settingRepo: &paymentConfigSettingRepoStub{values: map[string]string{
				SettingKeyWeChatConnectEnabled:             "true",
				SettingKeyWeChatConnectAppID:               "wx123456",
				SettingKeyWeChatConnectAppSecret:           "wechat-secret",
				SettingKeyWeChatConnectMode:                "mp",
				SettingKeyWeChatConnectScopes:              "snsapi_base",
				SettingKeyWeChatConnectRedirectURL:         "https://api.example.com/api/v1/auth/oauth/wechat/callback",
				SettingKeyWeChatConnectFrontendRedirectURL: "/auth/wechat/callback",
			}},
			// Legacy stable signing key remains available for no-config upgrade compatibility.
			encryptionKey: []byte("0123456789abcdef0123456789abcdef"),
		},
	}

	resp, err := svc.maybeBuildWeChatOAuthRequiredResponse(context.Background(), CreateOrderRequest{
		Amount:          12.5,
		PaymentType:     payment.TypeWxpay,
		IsWeChatBrowser: true,
		SrcURL:          "https://merchant.example/payment?from=wechat",
		OrderType:       payment.OrderTypeBalance,
	}, 12.5, payment.FeeBreakdown{PayAmount: 12.88, FeeRate: 0.03})
	if err != nil {
		t.Fatalf("expected nil error, got %v", err)
	}
	if resp == nil {
		t.Fatal("expected oauth-required response, got nil")
	}
	if resp.ResultType != payment.CreatePaymentResultOAuthRequired {
		t.Fatalf("result type = %q, want %q", resp.ResultType, payment.CreatePaymentResultOAuthRequired)
	}
	if resp.OAuth == nil || strings.TrimSpace(resp.OAuth.AuthorizeURL) == "" {
		t.Fatalf("expected oauth redirect payload, got %+v", resp.OAuth)
	}
}

func TestMaybeBuildWeChatOAuthRequiredResponseForSelectionSkipsEasyPayProvider(t *testing.T) {
	svc := newWeChatPaymentOAuthTestService(map[string]string{
		SettingKeyWeChatConnectEnabled:             "true",
		SettingKeyWeChatConnectAppID:               "wx123456",
		SettingKeyWeChatConnectAppSecret:           "wechat-secret",
		SettingKeyWeChatConnectMode:                "mp",
		SettingKeyWeChatConnectScopes:              "snsapi_base",
		SettingKeyWeChatConnectRedirectURL:         "https://api.example.com/api/v1/auth/oauth/wechat/callback",
		SettingKeyWeChatConnectFrontendRedirectURL: "/auth/wechat/callback",
	})

	resp, err := svc.maybeBuildWeChatOAuthRequiredResponseForSelection(context.Background(), CreateOrderRequest{
		Amount:          12.5,
		PaymentType:     payment.TypeWxpay,
		IsWeChatBrowser: true,
		OrderType:       payment.OrderTypeBalance,
	}, 12.5, payment.FeeBreakdown{PayAmount: 12.88, FeeRate: 0.03}, &payment.InstanceSelection{
		ProviderKey: payment.TypeEasyPay,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resp != nil {
		t.Fatalf("expected nil response, got %+v", resp)
	}
}

func newWeChatPaymentOAuthTestService(values map[string]string) *PaymentService {
	return &PaymentService{
		configService: &PaymentConfigService{
			settingRepo:   &paymentConfigSettingRepoStub{values: values},
			encryptionKey: []byte("0123456789abcdef0123456789abcdef"),
		},
	}
}
