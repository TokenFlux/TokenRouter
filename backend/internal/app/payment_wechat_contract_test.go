package app

import (
	"context"
	"strings"
	"testing"

	"github.com/TokenFlux/TokenRouter/internal/identity"
	"github.com/TokenFlux/TokenRouter/internal/pkg/apperror"

	"github.com/TokenFlux/TokenRouter/internal/payment"
)

func TestMaybeBuildWeChatOAuthRequiredResponse(t *testing.T) {
	t.Setenv("PAYMENT_RESUME_SIGNING_KEY", "0123456789abcdef0123456789abcdef")

	svc := newWeChatPaymentOAuthTestService(map[string]string{
		identity.SettingKeyWeChatConnectEnabled:             "true",
		identity.SettingKeyWeChatConnectAppID:               "wx123456",
		identity.SettingKeyWeChatConnectAppSecret:           "wechat-secret",
		identity.SettingKeyWeChatConnectMode:                "mp",
		identity.SettingKeyWeChatConnectScopes:              "snsapi_base",
		identity.SettingKeyWeChatConnectRedirectURL:         "https://api.example.com/api/v1/auth/oauth/wechat/callback",
		identity.SettingKeyWeChatConnectFrontendRedirectURL: "/auth/wechat/callback",
	})

	resp, err := svc.MaybeBuildWeChatOAuthRequiredResponse(context.Background(), payment.CreateOrderRequest{
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
		return
	}
	if resp.ResultType != payment.CreatePaymentResultOAuthRequired {
		t.Fatalf("result type = %q, want %q", resp.ResultType, payment.CreatePaymentResultOAuthRequired)
	}
	if resp.OAuth == nil {
		t.Fatal("expected oauth payload, got nil")
		return
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

	resp, err := svc.MaybeBuildWeChatOAuthRequiredResponse(context.Background(), payment.CreateOrderRequest{
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

	appErr := apperror.FromError(err)
	if appErr.Reason != "WECHAT_PAYMENT_MP_NOT_CONFIGURED" {
		t.Fatalf("reason = %q, want %q", appErr.Reason, "WECHAT_PAYMENT_MP_NOT_CONFIGURED")
	}
}

func TestMaybeBuildWeChatOAuthRequiredResponseRequiresResumeSigningKey(t *testing.T) {
	t.Parallel()

	svc := newWeChatPaymentCheckout(map[string]string{
		identity.SettingKeyWeChatConnectEnabled:             "true",
		identity.SettingKeyWeChatConnectAppID:               "wx123456",
		identity.SettingKeyWeChatConnectAppSecret:           "wechat-secret",
		identity.SettingKeyWeChatConnectMode:                "mp",
		identity.SettingKeyWeChatConnectScopes:              "snsapi_base",
		identity.SettingKeyWeChatConnectRedirectURL:         "https://api.example.com/api/v1/auth/oauth/wechat/callback",
		identity.SettingKeyWeChatConnectFrontendRedirectURL: "/auth/wechat/callback",
	},
		// Intentionally missing payment resume signing key.
		nil)

	resp, err := svc.MaybeBuildWeChatOAuthRequiredResponse(context.Background(), payment.CreateOrderRequest{
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

	appErr := apperror.FromError(err)
	if appErr.Reason != "PAYMENT_RESUME_NOT_CONFIGURED" {
		t.Fatalf("reason = %q, want %q", appErr.Reason, "PAYMENT_RESUME_NOT_CONFIGURED")
	}
}

func TestMaybeBuildWeChatOAuthRequiredResponseFallsBackToConfiguredLegacySigningKey(t *testing.T) {
	svc := newWeChatPaymentCheckout(map[string]string{
		identity.SettingKeyWeChatConnectEnabled:             "true",
		identity.SettingKeyWeChatConnectAppID:               "wx123456",
		identity.SettingKeyWeChatConnectAppSecret:           "wechat-secret",
		identity.SettingKeyWeChatConnectMode:                "mp",
		identity.SettingKeyWeChatConnectScopes:              "snsapi_base",
		identity.SettingKeyWeChatConnectRedirectURL:         "https://api.example.com/api/v1/auth/oauth/wechat/callback",
		identity.SettingKeyWeChatConnectFrontendRedirectURL: "/auth/wechat/callback",
	},
		// Legacy stable signing key remains available for no-config upgrade compatibility.
		[]byte("0123456789abcdef0123456789abcdef"))

	resp, err := svc.MaybeBuildWeChatOAuthRequiredResponse(context.Background(), payment.CreateOrderRequest{
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
		return
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
		identity.SettingKeyWeChatConnectEnabled:             "true",
		identity.SettingKeyWeChatConnectAppID:               "wx123456",
		identity.SettingKeyWeChatConnectAppSecret:           "wechat-secret",
		identity.SettingKeyWeChatConnectMode:                "mp",
		identity.SettingKeyWeChatConnectScopes:              "snsapi_base",
		identity.SettingKeyWeChatConnectRedirectURL:         "https://api.example.com/api/v1/auth/oauth/wechat/callback",
		identity.SettingKeyWeChatConnectFrontendRedirectURL: "/auth/wechat/callback",
	})

	resp, err := svc.MaybeBuildWeChatOAuthRequiredResponseForSelection(context.Background(), payment.CreateOrderRequest{
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

func newWeChatPaymentOAuthTestService(values map[string]string) *payment.Checkout {
	return newWeChatPaymentCheckout(values,
		[]byte("0123456789abcdef0123456789abcdef"))

}
