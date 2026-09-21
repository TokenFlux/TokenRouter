//go:build unit

package postgres_test

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"net/url"
	"strconv"
	"testing"
	"time"

	paymenttestkit "github.com/TokenFlux/TokenRouter/internal/payment/testkit"
	sqlitetest "github.com/TokenFlux/TokenRouter/internal/testutil/sqlite"

	"github.com/TokenFlux/TokenRouter/internal/payment"
	"github.com/TokenFlux/TokenRouter/internal/pkg/apperror"
)

func TestNormalizeVisibleMethods(t *testing.T) {
	t.Parallel()

	got := payment.NormalizeVisibleMethods([]string{
		"alipay_direct",
		"alipay",
		" wxpay_direct ",
		"wxpay",
		"stripe",
		"ldc",
	})

	want := []string{"alipay", "wxpay", "stripe", "ldc"}
	if len(got) != len(want) {
		t.Fatalf("NormalizeVisibleMethods len = %d, want %d (%v)", len(got), len(want), got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("NormalizeVisibleMethods[%d] = %q, want %q (full=%v)", i, got[i], want[i], got)
		}
	}
}

func TestEnabledVisibleMethodsForEasyPayIncludesCustomSupportedTypes(t *testing.T) {
	t.Parallel()

	got := payment.ConfigEnabledVisibleMethodsForProvider(payment.TypeEasyPay, "alipay,ldc,usdt_trc20")
	want := []string{"alipay", "ldc", "usdt_trc20"}
	if len(got) != len(want) {
		t.Fatalf("enabledVisibleMethodsForProvider len = %d, want %d (%v)", len(got), len(want), got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("enabledVisibleMethodsForProvider[%d] = %q, want %q (full=%v)", i, got[i], want[i], got)
		}
	}
}

func TestNormalizePaymentSource(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		input  string
		expect string
	}{
		{name: "empty uses default", input: "", expect: payment.PaymentSourceHostedRedirect},
		{name: "wechat alias normalized", input: "wechat_in_app", expect: payment.PaymentSourceWechatInAppResume},
		{name: "canonical value preserved", input: payment.PaymentSourceWechatInAppResume, expect: payment.PaymentSourceWechatInAppResume},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if got := payment.NormalizePaymentSource(tt.input); got != tt.expect {
				t.Fatalf("NormalizePaymentSource(%q) = %q, want %q", tt.input, got, tt.expect)
			}
		})
	}
}

func TestCanonicalizeReturnURL(t *testing.T) {
	t.Parallel()

	got, err := payment.CanonicalizeReturnURL("https://example.com/payment/result?b=2#a", "example.com", "")
	if err != nil {
		t.Fatalf("CanonicalizeReturnURL returned error: %v", err)
	}
	if got != "https://example.com/payment/result?b=2" {
		t.Fatalf("CanonicalizeReturnURL = %q, want %q", got, "https://example.com/payment/result?b=2")
	}
}

func TestCanonicalizeReturnURLRejectsRelativeURL(t *testing.T) {
	t.Parallel()

	if _, err := payment.CanonicalizeReturnURL("/payment/result", "example.com", ""); err == nil {
		t.Fatal("CanonicalizeReturnURL should reject relative URLs")
	}
}

func TestCanonicalizeReturnURLRejectsExternalHost(t *testing.T) {
	t.Parallel()

	if _, err := payment.CanonicalizeReturnURL("https://evil.example/payment/result", "app.example.com", ""); err == nil {
		t.Fatal("CanonicalizeReturnURL should reject external hosts")
	}
}

func TestCanonicalizeReturnURLAllowsConfiguredFrontendHost(t *testing.T) {
	t.Parallel()

	got, err := payment.CanonicalizeReturnURL(
		"https://app.example.com/payment/result?from=checkout",
		"api.example.com",
		"https://app.example.com/purchase",
	)
	if err != nil {
		t.Fatalf("CanonicalizeReturnURL returned error: %v", err)
	}
	if got != "https://app.example.com/payment/result?from=checkout" {
		t.Fatalf("CanonicalizeReturnURL = %q, want %q", got, "https://app.example.com/payment/result?from=checkout")
	}
}

func TestCanonicalizeReturnURLRejectsNonCanonicalPath(t *testing.T) {
	t.Parallel()

	if _, err := payment.CanonicalizeReturnURL("https://app.example.com/orders/42", "app.example.com", ""); err == nil {
		t.Fatal("CanonicalizeReturnURL should reject non-canonical result paths")
	}
}

func TestBuildPaymentReturnURL(t *testing.T) {
	t.Parallel()

	got, err := payment.ResumeBuildPaymentReturnURL("https://example.com/payment/result?from=checkout#fragment", 42, "sub2_42", "resume-token")
	if err != nil {
		t.Fatalf("buildPaymentReturnURL returned error: %v", err)
	}

	parsed, err := url.Parse(got)
	if err != nil {
		t.Fatalf("url.Parse returned error: %v", err)
	}
	if parsed.Fragment != "" {
		t.Fatalf("buildPaymentReturnURL should strip fragments, got %q", parsed.Fragment)
	}
	query := parsed.Query()
	if query.Get("from") != "checkout" {
		t.Fatalf("expected original query to be preserved, got %q", query.Get("from"))
	}
	if query.Get("order_id") != strconv.FormatInt(42, 10) {
		t.Fatalf("order_id = %q", query.Get("order_id"))
	}
	if query.Get("out_trade_no") != "sub2_42" {
		t.Fatalf("out_trade_no = %q", query.Get("out_trade_no"))
	}
	if query.Get("resume_token") != "resume-token" {
		t.Fatalf("resume_token = %q", query.Get("resume_token"))
	}
	if query.Get("status") != "success" {
		t.Fatalf("status = %q", query.Get("status"))
	}
}

func TestBuildPaymentReturnURLWithoutResumeTokenStillIncludesOutTradeNo(t *testing.T) {
	t.Parallel()

	got, err := payment.ResumeBuildPaymentReturnURL("https://example.com/payment/result", 42, "sub2_42", "")
	if err != nil {
		t.Fatalf("buildPaymentReturnURL returned error: %v", err)
	}

	parsed, err := url.Parse(got)
	if err != nil {
		t.Fatalf("url.Parse returned error: %v", err)
	}
	query := parsed.Query()
	if query.Get("order_id") != "42" {
		t.Fatalf("order_id = %q", query.Get("order_id"))
	}
	if query.Get("out_trade_no") != "sub2_42" {
		t.Fatalf("out_trade_no = %q", query.Get("out_trade_no"))
	}
	if query.Get("resume_token") != "" {
		t.Fatalf("resume_token = %q, want empty", query.Get("resume_token"))
	}
}

func TestBuildPaymentReturnURLEmptyBase(t *testing.T) {
	t.Parallel()

	got, err := payment.ResumeBuildPaymentReturnURL("", 42, "sub2_42", "resume-token")
	if err != nil {
		t.Fatalf("buildPaymentReturnURL returned error: %v", err)
	}
	if got != "" {
		t.Fatalf("buildPaymentReturnURL = %q, want empty string", got)
	}
}

func TestPaymentResumeTokenRoundTrip(t *testing.T) {
	t.Parallel()

	svc := payment.NewPaymentResumeService([]byte("0123456789abcdef0123456789abcdef"))
	token, err := svc.CreateToken(payment.ResumeTokenClaims{
		OrderID:            42,
		UserID:             7,
		ProviderInstanceID: "19",
		ProviderKey:        "easypay",
		PaymentType:        "wxpay",
		CanonicalReturnURL: "https://example.com/payment/result",
		IssuedAt:           1234567890,
	})
	if err != nil {
		t.Fatalf("CreateToken returned error: %v", err)
	}

	claims, err := svc.ParseToken(token)
	if err != nil {
		t.Fatalf("ParseToken returned error: %v", err)
	}
	if claims.OrderID != 42 || claims.UserID != 7 {
		t.Fatalf("claims mismatch: %+v", claims)
	}
	if claims.ProviderInstanceID != "19" || claims.ProviderKey != "easypay" || claims.PaymentType != "wxpay" {
		t.Fatalf("claims provider snapshot mismatch: %+v", claims)
	}
	if claims.CanonicalReturnURL != "https://example.com/payment/result" {
		t.Fatalf("claims return URL = %q", claims.CanonicalReturnURL)
	}
}

func TestCreateTokenRejectsMissingSigningKey(t *testing.T) {
	t.Parallel()

	svc := payment.NewPaymentResumeService(nil)
	_, err := svc.CreateToken(payment.ResumeTokenClaims{OrderID: 42})
	if err == nil {
		t.Fatal("CreateToken should reject missing signing key")
	}
}

func TestParseTokenRejectsFallbackSignedTokenWhenSigningKeyMissing(t *testing.T) {
	t.Parallel()

	token := mustCreateFallbackSignedToken(t, payment.ResumeTokenClaims{OrderID: 42, UserID: 7})
	svc := payment.NewPaymentResumeService(nil)
	_, err := svc.ParseToken(token)
	if err == nil {
		t.Fatal("ParseToken should reject tokens when signing key is missing")
	}
}

func TestParseTokenRejectsExpiredToken(t *testing.T) {
	t.Parallel()

	svc := payment.NewPaymentResumeService([]byte("0123456789abcdef0123456789abcdef"))
	token, err := svc.CreateToken(payment.ResumeTokenClaims{
		OrderID:   42,
		UserID:    7,
		IssuedAt:  time.Now().Add(-25 * time.Hour).Unix(),
		ExpiresAt: time.Now().Add(-1 * time.Hour).Unix(),
	})
	if err != nil {
		t.Fatalf("CreateToken returned error: %v", err)
	}

	_, err = svc.ParseToken(token)
	if err == nil {
		t.Fatal("ParseToken should reject expired tokens")
	}
}

func TestWeChatPaymentResumeTokenRoundTrip(t *testing.T) {
	t.Parallel()

	svc := payment.NewPaymentResumeService([]byte("0123456789abcdef0123456789abcdef"))
	token, err := svc.CreateWeChatPaymentResumeToken(payment.WeChatPaymentResumeClaims{
		OpenID:      "openid-123",
		PaymentType: payment.TypeWxpay,
		Amount:      "12.50",
		OrderType:   payment.OrderTypeSubscription,
		PlanID:      7,
		RedirectTo:  "/purchase?from=wechat",
		Scope:       "snsapi_base",
		IssuedAt:    1234567890,
	})
	if err != nil {
		t.Fatalf("CreateWeChatPaymentResumeToken returned error: %v", err)
	}

	claims, err := svc.ParseWeChatPaymentResumeToken(token)
	if err != nil {
		t.Fatalf("ParseWeChatPaymentResumeToken returned error: %v", err)
	}
	if claims.OpenID != "openid-123" || claims.PaymentType != payment.TypeWxpay {
		t.Fatalf("claims mismatch: %+v", claims)
	}
	if claims.Amount != "12.50" || claims.OrderType != payment.OrderTypeSubscription || claims.PlanID != 7 {
		t.Fatalf("claims payment context mismatch: %+v", claims)
	}
	if claims.RedirectTo != "/purchase?from=wechat" || claims.Scope != "snsapi_base" {
		t.Fatalf("claims redirect/scope mismatch: %+v", claims)
	}
}

func TestCreateWeChatPaymentResumeTokenRejectsMissingSigningKey(t *testing.T) {
	t.Parallel()

	svc := payment.NewPaymentResumeService(nil)
	_, err := svc.CreateWeChatPaymentResumeToken(payment.WeChatPaymentResumeClaims{OpenID: "openid-123"})
	if err == nil {
		t.Fatal("CreateWeChatPaymentResumeToken should reject missing signing key")
	}
}

func TestParseWeChatPaymentResumeTokenRejectsFallbackSignedTokenWhenSigningKeyMissing(t *testing.T) {
	t.Parallel()

	token := mustCreateFallbackSignedToken(t, payment.WeChatPaymentResumeClaims{
		TokenType:   payment.ResumeWechatPaymentResumeTokenType,
		OpenID:      "openid-123",
		PaymentType: payment.TypeWxpay,
	})
	svc := payment.NewPaymentResumeService(nil)
	_, err := svc.ParseWeChatPaymentResumeToken(token)
	if err == nil {
		t.Fatal("ParseWeChatPaymentResumeToken should reject tokens when signing key is missing")
	}
}

func TestParseWeChatPaymentResumeTokenRejectsExpiredToken(t *testing.T) {
	t.Parallel()

	svc := payment.NewPaymentResumeService([]byte("0123456789abcdef0123456789abcdef"))
	token, err := svc.CreateWeChatPaymentResumeToken(payment.WeChatPaymentResumeClaims{
		OpenID:      "openid-123",
		PaymentType: payment.TypeWxpay,
		IssuedAt:    time.Now().Add(-30 * time.Minute).Unix(),
		ExpiresAt:   time.Now().Add(-1 * time.Minute).Unix(),
	})
	if err != nil {
		t.Fatalf("CreateWeChatPaymentResumeToken returned error: %v", err)
	}

	_, err = svc.ParseWeChatPaymentResumeToken(token)
	if err == nil {
		t.Fatal("ParseWeChatPaymentResumeToken should reject expired tokens")
	}
}

func TestPaymentServiceParseWeChatPaymentResumeTokenUsesExplicitSigningKey(t *testing.T) {
	t.Setenv("PAYMENT_RESUME_SIGNING_KEY", "explicit-payment-resume-signing-key")

	token, err := payment.NewPaymentResumeService([]byte("explicit-payment-resume-signing-key")).CreateWeChatPaymentResumeToken(payment.WeChatPaymentResumeClaims{
		OpenID:      "openid-explicit-key",
		PaymentType: payment.TypeWxpay,
	})
	if err != nil {
		t.Fatalf("CreateWeChatPaymentResumeToken returned error: %v", err)
	}

	svc := paymenttestkit.Resume([]byte("0123456789abcdef0123456789abcdef"))

	claims, err := svc.ParseWeChatPaymentResumeToken(token)
	if err != nil {
		t.Fatalf("ParseWeChatPaymentResumeToken returned error: %v", err)
	}
	if claims.OpenID != "openid-explicit-key" {
		t.Fatalf("openid = %q, want %q", claims.OpenID, "openid-explicit-key")
	}
}

func TestPaymentServiceParseWeChatPaymentResumeTokenAcceptsLegacyEncryptionKeyDuringMigration(t *testing.T) {
	t.Setenv("PAYMENT_RESUME_SIGNING_KEY", "explicit-payment-resume-signing-key")

	legacyKey := []byte("0123456789abcdef0123456789abcdef")
	token, err := payment.NewPaymentResumeService(legacyKey).CreateWeChatPaymentResumeToken(payment.WeChatPaymentResumeClaims{
		OpenID:      "openid-legacy-key",
		PaymentType: payment.TypeWxpay,
	})
	if err != nil {
		t.Fatalf("CreateWeChatPaymentResumeToken returned error: %v", err)
	}

	svc := paymenttestkit.Resume(legacyKey)

	claims, err := svc.ParseWeChatPaymentResumeToken(token)
	if err != nil {
		t.Fatalf("ParseWeChatPaymentResumeToken returned error: %v", err)
	}
	if claims.OpenID != "openid-legacy-key" {
		t.Fatalf("openid = %q, want %q", claims.OpenID, "openid-legacy-key")
	}
}

func TestNewConfiguredPaymentResumeServicePrefersExplicitSigningKeyAndKeepsLegacyVerificationFallback(t *testing.T) {
	t.Setenv("PAYMENT_RESUME_SIGNING_KEY", "explicit-payment-resume-signing-key")

	legacyKey := []byte("0123456789abcdef0123456789abcdef")
	svc := paymenttestkit.Resume(legacyKey)

	explicitToken, err := svc.CreateWeChatPaymentResumeToken(payment.WeChatPaymentResumeClaims{
		OpenID:      "openid-explicit-key",
		PaymentType: payment.TypeWxpay,
	})
	if err != nil {
		t.Fatalf("CreateWeChatPaymentResumeToken returned error: %v", err)
	}

	explicitClaims, err := payment.NewPaymentResumeService([]byte("explicit-payment-resume-signing-key")).ParseWeChatPaymentResumeToken(explicitToken)
	if err != nil {
		t.Fatalf("ParseWeChatPaymentResumeToken returned error: %v", err)
	}
	if explicitClaims.OpenID != "openid-explicit-key" {
		t.Fatalf("openid = %q, want %q", explicitClaims.OpenID, "openid-explicit-key")
	}

	legacyToken, err := payment.NewPaymentResumeService(legacyKey).CreateWeChatPaymentResumeToken(payment.WeChatPaymentResumeClaims{
		OpenID:      "openid-legacy-key",
		PaymentType: payment.TypeWxpay,
	})
	if err != nil {
		t.Fatalf("CreateWeChatPaymentResumeToken returned error: %v", err)
	}

	legacyClaims, err := svc.ParseWeChatPaymentResumeToken(legacyToken)
	if err != nil {
		t.Fatalf("ParseWeChatPaymentResumeToken returned error: %v", err)
	}
	if legacyClaims.OpenID != "openid-legacy-key" {
		t.Fatalf("openid = %q, want %q", legacyClaims.OpenID, "openid-legacy-key")
	}
}

func TestNormalizeVisibleMethodSource(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		method string
		input  string
		want   string
	}{
		{name: "alipay official alias", method: payment.TypeAlipay, input: "alipay", want: payment.VisibleMethodSourceOfficialAlipay},
		{name: "alipay easypay alias", method: payment.TypeAlipay, input: "easypay", want: payment.VisibleMethodSourceEasyPayAlipay},
		{name: "wxpay official alias", method: payment.TypeWxpay, input: "wxpay", want: payment.VisibleMethodSourceOfficialWechat},
		{name: "wxpay easypay alias", method: payment.TypeWxpay, input: "easypay", want: payment.VisibleMethodSourceEasyPayWechat},
		{name: "unsupported source", method: payment.TypeWxpay, input: "stripe", want: ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if got := payment.NormalizeVisibleMethodSource(tt.method, tt.input); got != tt.want {
				t.Fatalf("NormalizeVisibleMethodSource(%q, %q) = %q, want %q", tt.method, tt.input, got, tt.want)
			}
		})
	}
}

func TestVisibleMethodProviderKeyForSource(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		method string
		source string
		want   string
		ok     bool
	}{
		{name: "official alipay", method: payment.TypeAlipay, source: payment.VisibleMethodSourceOfficialAlipay, want: payment.TypeAlipay, ok: true},
		{name: "easypay alipay", method: payment.TypeAlipay, source: payment.VisibleMethodSourceEasyPayAlipay, want: payment.TypeEasyPay, ok: true},
		{name: "official wechat", method: payment.TypeWxpay, source: payment.VisibleMethodSourceOfficialWechat, want: payment.TypeWxpay, ok: true},
		{name: "easypay wechat", method: payment.TypeWxpay, source: payment.VisibleMethodSourceEasyPayWechat, want: payment.TypeEasyPay, ok: true},
		{name: "mismatched method and source", method: payment.TypeAlipay, source: payment.VisibleMethodSourceOfficialWechat, want: "", ok: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got, ok := payment.VisibleMethodProviderKeyForSource(tt.method, tt.source)
			if got != tt.want || ok != tt.ok {
				t.Fatalf("VisibleMethodProviderKeyForSource(%q, %q) = (%q, %v), want (%q, %v)", tt.method, tt.source, got, ok, tt.want, tt.ok)
			}
		})
	}
}

func TestVisibleMethodLoadBalancerUsesEnabledProviderInstance(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	client := sqlitetest.NewClient(t)
	_, err := client.PaymentProviderInstance.Create().
		SetProviderKey(payment.TypeAlipay).
		SetName("Official Alipay").
		SetConfig("{}").
		SetSupportedTypes("alipay").
		SetEnabled(true).
		SetSortOrder(1).
		Save(ctx)
	if err != nil {
		t.Fatalf("create alipay provider: %v", err)
	}

	inner := &paymenttestkit.CaptureLoadBalancer{}
	configService := paymenttestkit.Configuration(client, nil, nil)
	lb := payment.NewVisibleMethodLoadBalancer(inner, configService)

	_, err = lb.SelectInstance(ctx, "", payment.TypeAlipay, payment.StrategyRoundRobin, 12.5)
	if err != nil {
		t.Fatalf("SelectInstance returned error: %v", err)
	}
	if inner.LastProviderKey != payment.TypeAlipay {
		t.Fatalf("lastProviderKey = %q, want %q", inner.LastProviderKey, payment.TypeAlipay)
	}
}

func TestVisibleMethodLoadBalancerUsesConfiguredSourceWhenMultipleProvidersEnabled(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name          string
		method        payment.PaymentType
		officialName  string
		officialTypes string
		easyPayName   string
		easyPayTypes  string
		sourceSetting string
		wantProvider  string
	}{
		{
			name:          "alipay uses official source",
			method:        payment.TypeAlipay,
			officialName:  "Official Alipay",
			officialTypes: "alipay",
			easyPayName:   "EasyPay Alipay",
			easyPayTypes:  "alipay",
			sourceSetting: payment.VisibleMethodSourceOfficialAlipay,
			wantProvider:  payment.TypeAlipay,
		},
		{
			name:          "alipay uses easypay source",
			method:        payment.TypeAlipay,
			officialName:  "Official Alipay",
			officialTypes: "alipay",
			easyPayName:   "EasyPay Alipay",
			easyPayTypes:  "alipay",
			sourceSetting: payment.VisibleMethodSourceEasyPayAlipay,
			wantProvider:  payment.TypeEasyPay,
		},
		{
			name:          "wxpay uses official source",
			method:        payment.TypeWxpay,
			officialName:  "Official WeChat",
			officialTypes: "wxpay",
			easyPayName:   "EasyPay WeChat",
			easyPayTypes:  "wxpay",
			sourceSetting: payment.VisibleMethodSourceOfficialWechat,
			wantProvider:  payment.TypeWxpay,
		},
		{
			name:          "wxpay uses easypay source",
			method:        payment.TypeWxpay,
			officialName:  "Official WeChat",
			officialTypes: "wxpay",
			easyPayName:   "EasyPay WeChat",
			easyPayTypes:  "wxpay",
			sourceSetting: payment.VisibleMethodSourceEasyPayWechat,
			wantProvider:  payment.TypeEasyPay,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			ctx := context.Background()
			client := sqlitetest.NewClient(t)

			officialProviderKey := payment.TypeAlipay
			if tt.method == payment.TypeWxpay {
				officialProviderKey = payment.TypeWxpay
			}

			_, err := client.PaymentProviderInstance.Create().
				SetProviderKey(officialProviderKey).
				SetName(tt.officialName).
				SetConfig("{}").
				SetSupportedTypes(tt.officialTypes).
				SetEnabled(true).
				SetSortOrder(1).
				Save(ctx)
			if err != nil {
				t.Fatalf("create official provider: %v", err)
			}

			_, err = client.PaymentProviderInstance.Create().
				SetProviderKey(payment.TypeEasyPay).
				SetName(tt.easyPayName).
				SetConfig("{}").
				SetSupportedTypes(tt.easyPayTypes).
				SetEnabled(true).
				SetSortOrder(2).
				Save(ctx)
			if err != nil {
				t.Fatalf("create easypay provider: %v", err)
			}

			inner := &paymenttestkit.CaptureLoadBalancer{}
			configService := paymenttestkit.Configuration(client, &paymentConfigSettingRepoStub{
				values: map[string]string{
					payment.ResumeVisibleMethodSourceSettingKey(tt.method): tt.sourceSetting,
				},
			}, nil)
			lb := payment.NewVisibleMethodLoadBalancer(inner, configService)

			_, err = lb.SelectInstance(ctx, "", tt.method, payment.StrategyRoundRobin, 12.5)
			if err != nil {
				t.Fatalf("SelectInstance returned error: %v", err)
			}
			if inner.LastProviderKey != tt.wantProvider {
				t.Fatalf("lastProviderKey = %q, want %q", inner.LastProviderKey, tt.wantProvider)
			}
		})
	}
}

func TestVisibleMethodLoadBalancerPreservesLegacyCrossProviderRoutingWhenSourceMissing(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	client := sqlitetest.NewClient(t)

	_, err := client.PaymentProviderInstance.Create().
		SetProviderKey(payment.TypeAlipay).
		SetName("Official Alipay").
		SetConfig("{}").
		SetSupportedTypes("alipay").
		SetEnabled(true).
		SetSortOrder(1).
		Save(ctx)
	if err != nil {
		t.Fatalf("create official provider: %v", err)
	}

	_, err = client.PaymentProviderInstance.Create().
		SetProviderKey(payment.TypeEasyPay).
		SetName("EasyPay Alipay").
		SetConfig("{}").
		SetSupportedTypes("alipay").
		SetEnabled(true).
		SetSortOrder(2).
		Save(ctx)
	if err != nil {
		t.Fatalf("create easypay provider: %v", err)
	}

	inner := &paymenttestkit.CaptureLoadBalancer{}
	configService := paymenttestkit.Configuration(client, &paymentConfigSettingRepoStub{
		values: map[string]string{
			payment.ResumeVisibleMethodSourceSettingKey(payment.TypeAlipay): "",
		},
	}, nil)
	lb := payment.NewVisibleMethodLoadBalancer(inner, configService)

	_, err = lb.SelectInstance(ctx, "", payment.TypeAlipay, payment.StrategyRoundRobin, 9.9)
	if err != nil {
		t.Fatalf("SelectInstance returned error: %v", err)
	}
	if inner.LastProviderKey != "" {
		t.Fatalf("lastProviderKey = %q, want legacy cross-provider empty key", inner.LastProviderKey)
	}
	if inner.LastPaymentType != payment.TypeAlipay {
		t.Fatalf("lastPaymentType = %q, want %q", inner.LastPaymentType, payment.TypeAlipay)
	}
}

func TestVisibleMethodLoadBalancerRejectsInvalidSourceWhenMultipleProvidersEnabled(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name        string
		method      payment.PaymentType
		sourceValue string
		wantMessage string
	}{
		{
			name:        "invalid wxpay source",
			method:      payment.TypeWxpay,
			sourceValue: "stripe",
			wantMessage: "wxpay source must be one of the supported payment providers",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			ctx := context.Background()
			client := sqlitetest.NewClient(t)

			officialProviderKey := payment.TypeAlipay
			officialSupportedTypes := "alipay"
			officialName := "Official Alipay"
			easyPaySupportedTypes := "alipay"
			easyPayName := "EasyPay Alipay"
			if tt.method == payment.TypeWxpay {
				officialProviderKey = payment.TypeWxpay
				officialSupportedTypes = "wxpay"
				officialName = "Official WeChat"
				easyPaySupportedTypes = "wxpay"
				easyPayName = "EasyPay WeChat"
			}

			_, err := client.PaymentProviderInstance.Create().
				SetProviderKey(officialProviderKey).
				SetName(officialName).
				SetConfig("{}").
				SetSupportedTypes(officialSupportedTypes).
				SetEnabled(true).
				SetSortOrder(1).
				Save(ctx)
			if err != nil {
				t.Fatalf("create official provider: %v", err)
			}

			_, err = client.PaymentProviderInstance.Create().
				SetProviderKey(payment.TypeEasyPay).
				SetName(easyPayName).
				SetConfig("{}").
				SetSupportedTypes(easyPaySupportedTypes).
				SetEnabled(true).
				SetSortOrder(2).
				Save(ctx)
			if err != nil {
				t.Fatalf("create easypay provider: %v", err)
			}

			inner := &paymenttestkit.CaptureLoadBalancer{}
			configService := paymenttestkit.Configuration(client, &paymentConfigSettingRepoStub{
				values: map[string]string{
					payment.ResumeVisibleMethodSourceSettingKey(tt.method): tt.sourceValue,
				},
			}, nil)
			lb := payment.NewVisibleMethodLoadBalancer(inner, configService)

			_, err = lb.SelectInstance(ctx, "", tt.method, payment.StrategyRoundRobin, 9.9)
			if err == nil {
				t.Fatal("SelectInstance should reject invalid visible method source configuration")
			}
			if apperror.Reason(err) != "INVALID_PAYMENT_VISIBLE_METHOD_SOURCE" {
				t.Fatalf("Reason(err) = %q, want %q", apperror.Reason(err), "INVALID_PAYMENT_VISIBLE_METHOD_SOURCE")
			}
			if apperror.Message(err) != tt.wantMessage {
				t.Fatalf("Message(err) = %q, want %q", apperror.Message(err), tt.wantMessage)
			}
		})
	}
}

func TestVisibleMethodLoadBalancerRejectsMissingEnabledVisibleMethodProvider(t *testing.T) {
	t.Parallel()

	inner := &paymenttestkit.CaptureLoadBalancer{}
	configService := paymenttestkit.Configuration(sqlitetest.NewClient(t), nil, nil)
	lb := payment.NewVisibleMethodLoadBalancer(inner, configService)

	if _, err := lb.SelectInstance(context.Background(), "", payment.TypeWxpay, payment.StrategyRoundRobin, 9.9); err == nil {
		t.Fatal("SelectInstance should reject when no enabled provider instance exists")
	}
}

func mustCreateFallbackSignedToken(t *testing.T, claims any) string {
	t.Helper()

	payload, err := json.Marshal(claims)
	if err != nil {
		t.Fatalf("marshal claims: %v", err)
	}
	encodedPayload := base64.RawURLEncoding.EncodeToString(payload)
	mac := hmac.New(sha256.New, []byte("sub2api-payment-resume"))
	_, _ = mac.Write([]byte(encodedPayload))
	signature := base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
	return encodedPayload + "." + signature
}
