package app

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/TokenFlux/TokenRouter/internal/config"
	"github.com/TokenFlux/TokenRouter/internal/identity"
	"github.com/TokenFlux/TokenRouter/internal/payment"
	"github.com/TokenFlux/TokenRouter/internal/pkg/apperror"
	"github.com/TokenFlux/TokenRouter/internal/pkg/timezone"
)

// WeChat in-app checkout returns an authorization requirement when MP
// credentials and resume signing are available. These cases check
// payment-specific redirect data, missing configuration errors, the configured
// legacy signing key, and EasyPay selection that bypasses this WeChat
// authorization path.

// paymentOAuthSettingsFixture supplies selected OAuth setting values to payment
// assembly.
type paymentOAuthSettingsFixture map[string]string

// TestMaybeBuildWeChatOAuthRequiredResponse configures MP OAuth and a dedicated
// resume key for in-app payment. It checks the required result, app and scope,
// payment callback, and normalized authorization URL with the original purchase
// context.
func TestMaybeBuildWeChatOAuthRequiredResponse(t *testing.T) {
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
		SrcURL:          "https://merchant.example/payment?from=wechat",
		OrderType:       payment.OrderTypeBalance,
	}, 12.5, payment.FeeBreakdown{PayAmount: 12.88, FeeRate: 0.03}, nil)
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

// TestMaybeBuildWeChatOAuthRequiredResponseRequiresMPConfigInWeChat omits MP
// configuration for a WeChat browser order. It checks no response is returned
// and the payment-specific MP-unconfigured reason identifies the failed
// prerequisite.
func TestMaybeBuildWeChatOAuthRequiredResponseRequiresMPConfigInWeChat(t *testing.T) {
	t.Parallel()

	svc := newWeChatPaymentOAuthTestService(nil)

	resp, err := svc.MaybeBuildWeChatOAuthRequiredResponseForSelection(context.Background(), payment.CreateOrderRequest{
		Amount:          12.5,
		PaymentType:     payment.TypeWxpay,
		IsWeChatBrowser: true,
		SrcURL:          "https://merchant.example/payment?from=wechat",
		OrderType:       payment.OrderTypeBalance,
	}, 12.5, payment.FeeBreakdown{PayAmount: 12.88, FeeRate: 0.03}, nil)
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

// TestMaybeBuildWeChatOAuthRequiredResponseRequiresResumeSigningKey provides MP
// credentials but no signing key. It checks no OAuth response is returned and
// PAYMENT_RESUME_NOT_CONFIGURED reports the unavailable resume prerequisite.
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
		// Omit the payment resume signing key for this case.
		nil)

	resp, err := svc.MaybeBuildWeChatOAuthRequiredResponseForSelection(context.Background(), payment.CreateOrderRequest{
		Amount:          12.5,
		PaymentType:     payment.TypeWxpay,
		IsWeChatBrowser: true,
		SrcURL:          "https://merchant.example/payment?from=wechat",
		OrderType:       payment.OrderTypeBalance,
	}, 12.5, payment.FeeBreakdown{PayAmount: 12.88, FeeRate: 0.03}, nil)
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

// TestMaybeBuildWeChatOAuthRequiredResponseFallsBackToConfiguredLegacySigningKey
// supplies MP credentials with a stable legacy key. It checks checkout returns
// an OAuth-required result and a nonblank authorization payload.
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
		// Use the stable legacy key when no dedicated signing key is configured.
		[]byte("0123456789abcdef0123456789abcdef"))

	resp, err := svc.MaybeBuildWeChatOAuthRequiredResponseForSelection(context.Background(), payment.CreateOrderRequest{
		Amount:          12.5,
		PaymentType:     payment.TypeWxpay,
		IsWeChatBrowser: true,
		SrcURL:          "https://merchant.example/payment?from=wechat",
		OrderType:       payment.OrderTypeBalance,
	}, 12.5, payment.FeeBreakdown{PayAmount: 12.88, FeeRate: 0.03}, nil)
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

// TestMaybeBuildWeChatOAuthRequiredResponseForSelectionSkipsEasyPayProvider
// supplies a WeChat browser request with an EasyPay instance selection. It
// checks successful preparation returns no WeChat OAuth-required response for
// that selected channel.
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

// newWeChatPaymentOAuthTestService builds checkout with supplied OAuth settings
// and the fixed fixture signing key. The helper delegates production runtime
// assembly to newWeChatPaymentCheckout, leaving unused operations unconfigured.
func newWeChatPaymentOAuthTestService(values map[string]string) *payment.Checkout {
	return newWeChatPaymentCheckout(values,
		[]byte("0123456789abcdef0123456789abcdef"))
}

// GetMultiple returns only the requested keys present in this settings map. The
// returned map is newly allocated, and missing settings are omitted so OAuth
// defaults can apply.
func (s paymentOAuthSettingsFixture) GetMultiple(_ context.Context, keys []string) (map[string]string, error) {
	values := make(map[string]string, len(keys))
	for _, key := range keys {
		if value, ok := s[key]; ok {
			values[key] = value
		}
	}
	return values, nil
}

// newWeChatPaymentCheckout constructs payment checkout through production
// assembly with UTC dates. Only OAuth preparation dependencies are configured;
// callers exercise that path while order, refund, and fulfillment resources
// remain nil.
func newWeChatPaymentCheckout(values map[string]string, key []byte) *payment.Checkout {
	settings := identity.NewOAuthSettings(paymentOAuthSettingsFixture(values), nil, nil)
	runtime := providePaymentRuntime(nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, payment.EncryptionKey(key), settings, nil, timezone.NewCalendar(time.UTC), &config.Config{})
	return runtime.Checkout
}
