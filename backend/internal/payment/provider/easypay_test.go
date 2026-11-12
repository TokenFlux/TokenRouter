package provider

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync/atomic"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/stretchr/testify/require"

	"github.com/TokenFlux/TokenRouter/internal/payment"
)

// easypay_test.go signs callback fixtures locally and distinguishes accepted
// notification keys from checkout parameters, duplicate values and
// delimiter-bearing structured fields. Query fixtures exercise status
// precedence across JSON layouts and reject paid observations lacking actual
// upstream transaction references.
//
// Loopback refund fixtures capture alternative identifier forms in order,
// returning missing-order text before a successful trade-reference attempt.
// URL fixtures separate slash-prefixed resolution from absolute links or
// opaque QR text, including protocol-relative references that change host.
// Signing tests cover parameter order and excluded fields independently of
// callback structure, while bounded diagnostic fixtures assert valid UTF-8 at
// the truncated prefix.

// easyPayQueryFailingTransport returns a nested credential-bearing request
// error for query diagnostic tests.
type easyPayQueryFailingTransport struct {
	cause error // nested request failure identity
}

// TestEasyPayCreateMerchantNumberCompatibility exercises API checkout through
// its normal adapter entry. Only an opted-in exact merchant-number echo leaves
// the provider transaction unbound for later confirmation.
func TestEasyPayCreateMerchantNumberCompatibility(t *testing.T) {
	for _, value := range []string{"", "false", "true"} {
		for _, tradeNo := range []string{"ORDER123", "gateway-trade", " ORDER123"} {
			t.Run(value+"/"+tradeNo, func(t *testing.T) {
				server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					require.Equal(t, "/mapi.php", r.URL.Path)
					_, _ = fmt.Fprintf(w, `{"code":1,"trade_no":%q,"qrcode":"qr-payload"}`, tradeNo)
				}))
				t.Cleanup(server.Close)
				adapter := newTestEasyPay(t, server.URL)
				adapter.config[payment.ConfigEasyPayAllowMerchantOrderNoAsTradeNo] = value
				result, err := adapter.CreatePayment(context.Background(), payment.CreatePaymentRequest{OrderID: "ORDER123", PaymentType: "alipay", Amount: "10.20"})
				require.NoError(t, err)
				want := tradeNo
				if value == "true" && tradeNo == "ORDER123" {
					want = ""
				}
				require.Equal(t, want, result.TradeNo)
				require.Equal(t, "qr-payload", result.QRCode)
			})
		}
	}
}

// TestEasyPayCompatibilityPaidIdentity requires explicit authenticated identity
// before the adapter grants echo-replacement permission. Missing or conflicting
// identities, unpaid responses, and invalid money cannot carry that permission.
func TestEasyPayCompatibilityPaidIdentity(t *testing.T) {
	for _, tc := range []struct {
		name      string // named upstream response case
		body      string // complete query response JSON
		wantError bool   // expected lookup rejection flag
		confirmed bool   // expected replacement permission flag
	}{
		{"paid", `{"code":1,"status":1,"money":"10.20","trade_no":"UPSTREAM123","pid":"1000","out_trade_no":"ORDER123"}`, false, true},
		{"nested", `{"code":1,"data":{"status":1,"money":"10.20","trade_no":"UPSTREAM123","pid":1000,"out_trade_no":"ORDER123"}}`, false, true},
		{"missing merchant", `{"code":1,"status":1,"money":"10.20","trade_no":"UPSTREAM123","out_trade_no":"ORDER123"}`, true, false},
		{"missing order", `{"code":1,"status":1,"money":"10.20","trade_no":"UPSTREAM123","pid":1000}`, true, false},
		{"wrong merchant", `{"code":1,"status":1,"money":"10.20","trade_no":"UPSTREAM123","pid":9999,"out_trade_no":"ORDER123"}`, true, false},
		{"wrong order", `{"code":1,"status":1,"money":"10.20","trade_no":"UPSTREAM123","pid":1000,"out_trade_no":"OTHER"}`, true, false},
		{"invalid amount", `{"code":1,"status":1,"money":"0","trade_no":"UPSTREAM123","pid":1000,"out_trade_no":"ORDER123"}`, true, false},
		{"missing trade", `{"code":1,"status":1,"money":"10.20","pid":1000,"out_trade_no":"ORDER123"}`, true, false},
		{"pending", `{"code":1,"status":0}`, false, false},
		{"failure", `{"code":0,"msg":"query failed"}`, true, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				_, _ = w.Write([]byte(tc.body))
			}))
			t.Cleanup(server.Close)
			adapter := newTestEasyPay(t, server.URL)
			adapter.config["pid"] = "1000"
			adapter.config[payment.ConfigEasyPayAllowMerchantOrderNoAsTradeNo] = "true"
			result, err := adapter.QueryOrder(context.Background(), "ORDER123")
			if tc.wantError {
				require.Error(t, err)
				return
			}
			require.NoError(t, err)
			require.Equal(t, tc.confirmed, result.Metadata[payment.EasyPayConfirmedMerchantOrderNo] == "ORDER123")
		})
	}
}

// TestEasyPayCompatibilityCallbackAuthenticatesProof checks the callback and
// its independent merchant query together. Signed mismatches and attempts to
// inject the internal permission marker remain rejected with compatibility enabled.
func TestEasyPayCompatibilityCallbackAuthenticatesProof(t *testing.T) {
	for _, tc := range []struct {
		name             string            // named callback validation case
		change           map[string]string // signed callback field replacements
		invalidSignature bool              // deliberately corrupt signature flag
		wantError        bool              // expected confirmation rejection flag
	}{
		{name: "valid"},
		{name: "wrong signature", invalidSignature: true, wantError: true},
		{name: "wrong amount", change: map[string]string{"money": "11.00"}, wantError: true},
		{name: "wrong trade", change: map[string]string{"trade_no": "OTHER"}, wantError: true},
		{name: "injected permission", change: map[string]string{payment.EasyPayConfirmedMerchantOrderNo: "ORDER123"}, wantError: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				_, _ = w.Write([]byte(`{"code":1,"status":1,"money":"10.20","trade_no":"UPSTREAM123","pid":1000,"out_trade_no":"ORDER123"}`))
			}))
			t.Cleanup(server.Close)
			adapter := newTestEasyPay(t, server.URL)
			adapter.config["pid"] = "1000"
			adapter.config[payment.ConfigEasyPayAllowMerchantOrderNoAsTradeNo] = "true"
			values := easyPayTestNotificationValues()
			values.Set("money", "10.20")
			params := make(map[string]string)
			for key := range values {
				params[key] = values.Get(key)
			}
			for key, value := range tc.change {
				params[key] = value
				values.Set(key, value)
			}
			values.Set("sign", easyPaySign(params, adapter.config["pkey"]))
			if tc.invalidSignature {
				values.Set("sign", "invalid")
			}
			result, err := adapter.VerifyNotification(context.Background(), values.Encode(), nil)
			if tc.wantError {
				require.Error(t, err)
				return
			}
			require.NoError(t, err)
			require.Equal(t, "ORDER123", result.Metadata[payment.EasyPayConfirmedMerchantOrderNo])
		})
	}
}

// RoundTrip includes the request URL in a wrapped failure and returns no
// response. The synthetic transport makes secret-bearing inner errors visible
// to the query adapter without contacting a remote merchant.
func (transport easyPayQueryFailingTransport) RoundTrip(request *http.Request) (*http.Response, error) {
	return nil, fmt.Errorf("request %s failed: %w", request.URL.String(), transport.cause)
}

// TestEasyPayNotificationRejectsCheckoutParameters generates a signed
// hosted-checkout query and submits it as callback form text. It expects
// rejection because checkout parameter names do not constitute an accepted
// notification payload.
func TestEasyPayNotificationRejectsCheckoutParameters(t *testing.T) {
	t.Parallel()

	e := &EasyPay{config: map[string]string{
		"pid": "1000", "pkey": "test-merchant-secret",
		"apiBase": "https://pay.example.com", "paymentMode": "popup",
		"notifyUrl": "https://site.example.com/api/v1/payment/webhook/easypay",
	}}
	created, err := e.CreatePayment(context.Background(), payment.CreatePaymentRequest{
		OrderID: "ORDER123", PaymentType: payment.TypeAlipay,
		Subject: "balance recharge", Amount: "650.00",
		ReturnURL: "https://site.example.com/payment/result",
	})
	if err != nil {
		t.Fatal(err)
	}
	payURL, err := url.Parse(created.PayURL)
	if err != nil {
		t.Fatal(err)
	}
	notification, err := e.VerifyNotification(context.Background(), payURL.RawQuery, nil)
	if err == nil || notification != nil {
		t.Fatalf("checkout parameters accepted: notification=%+v, err=%v", notification, err)
	}
}

// TestEasyPayNotificationRejectsCheckoutSignatureReplay rebuilds the historical
// return-url delimiter replay entirely in memory. The callback has the checkout
// digest but is rejected before upstream access because checkout fields remain forbidden.
func TestEasyPayNotificationRejectsCheckoutSignatureReplay(t *testing.T) {
	t.Parallel()
	e := &EasyPay{config: map[string]string{"pid": "1000", "pkey": "test-secret", "apiBase": "https://pay.example.com", "paymentMode": "popup", "notifyUrl": "https://site.example.com/notify"}}
	prefix := "https://site.example.com/payment/result?status=success"
	created, err := e.CreatePayment(context.Background(), payment.CreatePaymentRequest{
		OrderID: "ORDER123", PaymentType: "alipay", Subject: "test", Amount: "650.00",
		ReturnURL: prefix + "&trade_status=TRADE_SUCCESS",
	})
	if err != nil {
		t.Fatal(err)
	}
	parsed, err := url.Parse(created.PayURL)
	if err != nil {
		t.Fatal(err)
	}
	values := parsed.Query()
	values.Set("return_url", prefix)
	values.Set("trade_status", tradeStatusSuccess)
	params := make(map[string]string, len(values))
	for key := range values {
		params[key] = values.Get(key)
	}
	if !easyPayVerifySign(params, e.config["pkey"], values.Get("sign")) {
		t.Fatal("fixture did not preserve the checkout signature")
	}
	notification, err := e.VerifyNotification(context.Background(), values.Encode(), nil)
	if err == nil || notification != nil || !strings.Contains(err.Error(), "unexpected notify param") {
		t.Fatalf("replayed checkout signature accepted: %+v, %v", notification, err)
	}
}

// TestEasyPayNotificationRequiresUpstreamConfirmation supplies valid signatures
// with inconsistent or unavailable upstream orders. Every such notification
// fails before it can provide an accepted success observation to fulfillment.
func TestEasyPayNotificationRequiresUpstreamConfirmation(t *testing.T) {
	t.Parallel()
	for _, body := range []string{
		`{"code":1,"status":0,"money":"650.00","trade_no":"UPSTREAM123"}`,
		`{"code":0,"status":1,"money":"650.00","trade_no":"UPSTREAM123"}`,
		`{"code":1,"status":1,"money":"649.99","trade_no":"UPSTREAM123"}`,
		`{"code":1,"status":1,"money":"650.00","trade_no":"another-trade"}`,
		`{"code":1,"status":1,"money":"650.00"}`,
		`{"code":1,"status":1,"money":"NaN","trade_no":"UPSTREAM123"}`,
		`{"code":1,"status":1,"money":"650.001","trade_no":"UPSTREAM123"}`,
		`{"code":1,"status":1,"money":"650.00","trade_no":"UPSTREAM123","pid":2000}`,
		`{"code":1,"status":1,"money":"650.00","trade_no":"UPSTREAM123","out_trade_no":"another-order"}`,
		`{"code":1,"status":1,"money":"650.00","trade_no":"UPSTREAM123","data":{"pid":"2000"}}`,
		`{"code":1.5,"status":1,"money":"650.00","trade_no":"UPSTREAM123"}`,
		`<html>error</html>`,
	} {
		t.Run(body, func(t *testing.T) {
			t.Parallel()
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				_, _ = w.Write([]byte(body))
			}))
			// Keep the fixture response available until verification returns, then
			// close its listener so later parallel cases own independent merchant endpoints.
			t.Cleanup(server.Close)
			e := &EasyPay{config: map[string]string{"pid": "1000", "pkey": "secret", "apiBase": server.URL}}
			notification, err := e.VerifyNotification(context.Background(), signedEasyPayTestNotification(e), nil)
			if err == nil || notification != nil {
				t.Fatalf("unconfirmed payment accepted: %+v, %v", notification, err)
			}
		})
	}
}

// TestEasyPayQueryCancellationRedactsCredentials expires a lookup context
// while the local merchant holds its response. Cancellation remains detectable
// and the returned diagnostic contains neither the key nor the request query.
func TestEasyPayQueryCancellationRedactsCredentials(t *testing.T) {
	t.Parallel()
	server := httptest.NewServer(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		// Consume the submitted form before waiting so connection cancellation can
		// terminate this handler while the client exercises its bounded request lifetime.
		_ = r.ParseForm()
		<-r.Context().Done()
	}))
	t.Cleanup(server.Close)
	e := newTestEasyPay(t, server.URL)
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	_, err := e.QueryOrder(ctx, "order-123")
	if !errors.Is(err, context.DeadlineExceeded) || strings.Contains(err.Error(), "pkey-1") || strings.Contains(err.Error(), "?act=") {
		t.Fatalf("unexpected query error: %v", err)
	}
}

// TestEasyPayQueryRedirectErrorRedactsCredentials receives an invalid redirect
// path echoing the authenticated query. Neither returned diagnostics nor their
// unwrap chain may retain the raw or encoded merchant key.
func TestEasyPayQueryRedirectErrorRedactsCredentials(t *testing.T) {
	t.Parallel()
	for _, secret := range []string{"review-merchant-secret", "review +&/=?%密钥"} {
		t.Run(secret, func(t *testing.T) {
			t.Parallel()
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Location", "/%zz?"+r.URL.RawQuery)
				w.WriteHeader(http.StatusFound)
			}))
			// Retain this loopback endpoint through redirect parsing and diagnostic
			// assertions, then release its listener independently of other parallel cases.
			t.Cleanup(server.Close)
			adapter := newTestEasyPay(t, server.URL)
			adapter.config["pkey"] = secret
			_, err := adapter.QueryOrder(context.Background(), "review-order")
			assertEasyPayQueryErrorRedacted(t, err, secret)
		})
	}
}

// TestEasyPayQueryNestedErrorsRetainCancellation supplies wrapped request
// failures containing credentials. Query callers can still recognize cancellation
// and deadlines while all exposed diagnostic strings omit the merchant secret.
func TestEasyPayQueryNestedErrorsRetainCancellation(t *testing.T) {
	t.Parallel()
	for _, cause := range []error{context.Canceled, context.DeadlineExceeded, errors.New("connection failed")} {
		t.Run(cause.Error(), func(t *testing.T) {
			t.Parallel()
			adapter := newTestEasyPay(t, "https://merchant.invalid")
			adapter.httpClient.Transport = easyPayQueryFailingTransport{cause: cause}
			_, err := adapter.QueryOrder(context.Background(), "review-order")
			assertEasyPayQueryErrorRedacted(t, err, adapter.config["pkey"])
			if (cause == context.Canceled || cause == context.DeadlineExceeded) && !errors.Is(err, cause) {
				t.Fatalf("query lost cancellation identity: %v", err)
			}
		})
	}
}

// assertEasyPayQueryErrorRedacted requires a failure and inspects every exposed
// wrapped error for credential text. Raw and form-encoded keys, query labels,
// and redirect diagnostics all indicate a leaked request representation.
func assertEasyPayQueryErrorRedacted(t *testing.T, err error, secret string) {
	t.Helper()
	if err == nil {
		t.Fatal("expected query failure")
	}
	for current := err; current != nil; current = errors.Unwrap(current) {
		for _, unsafe := range []string{secret, url.QueryEscape(secret), "key=", "Location header"} {
			if strings.Contains(current.Error(), unsafe) {
				t.Fatalf("query error exposed credential-bearing diagnostics: %v", current)
			}
		}
	}
}

// signedEasyPayTestNotification signs the standard fixture with the supplied
// merchant secret. It retains every decoded field in the signed form so tests
// can independently choose upstream confirmation responses for the same callback.
func signedEasyPayTestNotification(e *EasyPay) string {
	values := easyPayTestNotificationValues()
	params := make(map[string]string, len(values))
	for key := range values {
		params[key] = values.Get(key)
	}
	values.Set("sign", easyPaySign(params, e.config["pkey"]))
	values.Set("sign_type", signTypeMD5)
	return values.Encode()
}

// TestEasyPayRefundOutcomeClassification sends real HTTP refund requests to
// local success, rejection and ambiguous response fixtures. Unknown responses
// retain their sentinel and never cause an alternative-identifier submission.
func TestEasyPayRefundOutcomeClassification(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name    string // refund outcome scenario
		body    string // upstream response body
		status  int    // upstream HTTP status
		outcome string // expected result classification
	}{
		{name: "JSON one", body: `{"code":1,"msg":"ok"}`, outcome: "success"},
		{name: "string one", body: `{"code":"1","msg":"ok"}`, outcome: "success"},
		{name: "JSON zero", body: `{"code":0,"msg":"退款成功"}`, outcome: "success"},
		{name: "string zero", body: `{"code":"0","msg":"退款成功！退款金额¥10.20"}`, outcome: "success"},
		{name: "plain amount", body: "退款成功！退款金额¥10.20", outcome: "success"},
		{name: "plain success", body: " 退款成功！ ", outcome: "success"},
		{name: "explicit rejection", body: `{"code":0,"msg":"退款失败，余额不足"}`, outcome: "failed"},
		{name: "merchant balance rejection", body: `{"code":-1,"msg":"商户余额不足"}`, outcome: "failed"},
		{name: "parameter rejection", body: `{"code":0,"msg":"参数错误"}`, outcome: "failed"},
		{name: "permission rejection", body: `{"code":0,"msg":"权限不足"}`, outcome: "failed"},
		{name: "Alipay system error", body: `{"code":-1,"msg":"ACQ.SYSTEM_ERROR"}`, outcome: "unknown"},
		{name: "WeChat system error", body: `{"code":0,"msg":"SYSTEMERROR"}`, outcome: "unknown"},
		{name: "Chinese system error", body: `{"code":0,"msg":"系统错误"}`, outcome: "unknown"},
		{name: "generic failure", body: `{"code":0,"msg":"退款失败"}`, outcome: "unknown"},
		{name: "unrecognized error", body: `{"code":-1,"msg":"unexpected settlement error"}`, outcome: "unknown"},
		{name: "mixed system and business failure", body: `{"code":0,"msg":"系统错误，余额不足"}`, outcome: "unknown"},
		{name: "contradictory system failure", body: `{"code":1,"msg":"系统异常"}`, outcome: "unknown"},
		{name: "contradictory service failure", body: `{"code":1,"msg":"服务不可用"}`, outcome: "unknown"},
		{name: "wrong amount", body: "退款成功！退款金额¥10.21", outcome: "unknown"},
		{name: "JSON wrong amount", body: `{"code":1,"msg":"退款成功！退款金额¥1.20"}`, outcome: "unknown"},
		{name: "negative success", body: "未退款成功", outcome: "unknown"},
		{name: "application", body: "退款申请成功", outcome: "unknown"},
		{name: "contradiction", body: `{"code":1,"msg":"退款失败"}`, outcome: "unknown"},
		{name: "pending code one", body: `{"code":1,"msg":"退款申请成功"}`, outcome: "unknown"},
		{name: "query failure after submission", body: `{"code":0,"msg":"退款请求已发送，查询失败"}`, outcome: "unknown"},
		{name: "refund timeout", body: `{"code":0,"msg":"退款失败：请求超时"}`, outcome: "unknown"},
		{name: "ambiguous zero", body: `{"code":0,"msg":"ok"}`, outcome: "unknown"},
		{name: "rounded fractional", body: `{"code":1.000000000000000000001,"msg":"ok"}`, outcome: "unknown"},
		{name: "ordinary operation success", body: `{"code":1,"msg":"操作成功"}`, outcome: "success"},
		{name: "fractional", body: `{"code":1.5,"msg":"ok"}`, outcome: "unknown"},
		{name: "missing code", body: `{"msg":"退款成功"}`, outcome: "unknown"},
		{name: "HTML", body: "<html>退款成功</html>", outcome: "unknown"},
		{name: "plain missing order", body: "订单不存在", outcome: "unknown"},
		{name: "HTTP failure", body: `{"code":1}`, status: http.StatusBadGateway, outcome: "unknown"},
		{name: "oversized success", body: "退款成功" + strings.Repeat(" ", maxEasypayResponseSize), outcome: "unknown"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			var calls atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				calls.Add(1)
				if tc.status != 0 {
					w.WriteHeader(tc.status)
				}
				_, _ = w.Write([]byte(tc.body))
			}))
			// Each case has a separate endpoint and submission counter; release its
			// HTTP resources after checking that ambiguous outcomes did not retry refunds.
			t.Cleanup(server.Close)
			e := newTestEasyPay(t, server.URL)
			resp, err := e.Refund(context.Background(), payment.RefundRequest{OrderID: "order", TradeNo: "trade", Amount: "10.20"})
			switch tc.outcome {
			case "success":
				if err != nil || resp == nil || resp.Status != payment.ProviderStatusSuccess {
					t.Fatalf("expected success: %+v, %v", resp, err)
				}
			case "failed":
				if err == nil || errors.Is(err, payment.ErrRefundOutcomeUnknown) {
					t.Fatalf("expected rejection: %v", err)
				}
			case "unknown":
				if !errors.Is(err, payment.ErrRefundOutcomeUnknown) {
					t.Fatalf("expected unknown outcome: %v", err)
				}
			}
			if calls.Load() != 1 {
				t.Fatalf("refund submissions = %d, want 1", calls.Load())
			}
		})
	}
}

// TestEasyPayRefundInterruptedResponse holds a refund response past the client
// deadline. The resulting unknown outcome prevents a second request using the
// available upstream trade number after the first submission might have succeeded.
func TestEasyPayRefundInterruptedResponse(t *testing.T) {
	t.Parallel()
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		_ = r.ParseForm()
		// A withheld response models an accepted external request whose completion
		// is not observable before client cancellation closes the local test connection.
		<-r.Context().Done()
	}))
	t.Cleanup(server.Close)
	e := newTestEasyPay(t, server.URL)
	e.httpClient.Timeout = time.Second
	_, err := e.Refund(context.Background(), payment.RefundRequest{OrderID: "order", TradeNo: "trade", Amount: "10.20"})
	if !errors.Is(err, payment.ErrRefundOutcomeUnknown) || calls.Load() != 1 {
		t.Fatalf("interrupted refund: %v, submissions %d", err, calls.Load())
	}
}

// TestEasyPayNotificationParameters assembles standard, custom-type and
// free-text forms with optional extras before signing, then adds any duplicate
// values. It checks accepted observations, rejects extra or duplicated keys
// and rejects amount changes after signing.
func TestEasyPayNotificationParameters(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string // named callback structure case
		// payType replaces the base Alipay form value when nonempty; empty uses the
		// standard fixture type before signing.
		payType string // optional visible method override
		// extraKey and extra add one form pair before signing when extraKey is
		// nonempty. An empty extra value still produces that accepted-or-rejected
		// parameter name.
		extraKey string // optional added parameter name
		extra    string // added parameter value text
		// duplicate adds a second value after signing when nonempty, testing
		// structural rejection before signature comparison can accept the callback.
		duplicate string // post-signing duplicate field name
		wantError bool   // expected callback rejection flag
	}{
		{name: "standard notification"},
		{name: "wechat notification", payType: "wxpay"},
		{name: "custom payment type", payType: "usdt_trc20"},
		{name: "optional param", extraKey: "param", extra: "merchant-中文"},
		{name: "unsafe param", extraKey: "param", extra: "merchant=value&extra=中文", wantError: true},
		{name: "empty optional param", extraKey: "param"},
		{name: "api trade number", extraKey: "api_trade_no", extra: "API123"},
		{name: "creation time", extraKey: "addtime", extra: "2026-10-24 12:00:00"},
		{name: "payment time", extraKey: "endtime", extra: "2026-10-24 12:00:01"},
		{name: "buyer", extraKey: "buyer", extra: "buyer@example.com"},
		{name: "timestamp", extraKey: "timestamp", extra: "1792800000"},
		{name: "unsafe buyer", extraKey: "buyer", extra: "buyer&trade_status=x", wantError: true},
		{name: "control timestamp", extraKey: "timestamp", extra: "123\t456", wantError: true},
		{name: "duplicate extension", extraKey: "buyer", extra: "demo", duplicate: "buyer", wantError: true},
		{name: "client IP", extraKey: "clientip", extra: "127.0.0.1", wantError: true},
		{name: "channel", extraKey: "cid", extra: "1", wantError: true},
		{name: "return URL", extraKey: "return_url", extra: "https://site.example.com/payment/result", wantError: true},
		{name: "notify URL", extraKey: "notify_url", extra: "https://site.example.com/notify", wantError: true},
		{name: "device", extraKey: "device", extra: "mobile", wantError: true},
		{name: "unknown field", extraKey: "unknown", extra: "value", wantError: true},
		{name: "empty unknown field", extraKey: "unknown", wantError: true},
		{name: "empty return URL", extraKey: "return_url", wantError: true},
		{name: "duplicate status", duplicate: "trade_status", wantError: true},
		{name: "duplicate order", duplicate: "out_trade_no", wantError: true},
		{name: "duplicate signature", duplicate: "sign", wantError: true},
		{name: "duplicate amount", duplicate: "money", wantError: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			var calls atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls.Add(1)
				if r.URL.Query().Get("pid") != "1000" || r.URL.Query().Get("out_trade_no") != "ORDER123" {
					t.Error("confirmation used a different merchant or order")
				}
				_, _ = w.Write([]byte(`{"code":1,"status":1,"money":"650.00","trade_no":"UPSTREAM123"}`))
			}))
			// Keep each loopback merchant alive through its parallel case and release
			// the listener after callback verification and request-count assertions complete.
			t.Cleanup(server.Close)
			e := &EasyPay{config: map[string]string{"pid": "1000", "pkey": "test-merchant-secret", "apiBase": server.URL}}
			params := map[string]string{
				"pid": "1000", "trade_no": "UPSTREAM123", "out_trade_no": "ORDER123",
				"type": "alipay", "name": "充值 & 套餐=10%", "money": "650.00",
				"trade_status": tradeStatusSuccess,
			}
			if tt.payType != "" {
				params["type"] = tt.payType
			}
			if tt.extraKey != "" {
				params[tt.extraKey] = tt.extra
			}
			// Fix the digest before adding wire signature labels and duplicate values,
			// separating accepted signed pairs from later callback structure mutations.
			params["sign"] = easyPaySign(params, e.config["pkey"])
			params["sign_type"] = signTypeMD5
			values := url.Values{}
			for key, value := range params {
				values.Set(key, value)
			}
			if tt.duplicate != "" {
				// Retain both values in encoded form through url.Values.Add, exposing
				// duplicate callback content before the verifier can choose a signed
				// value.
				values.Add(tt.duplicate, "different-value")
			}
			raw := values.Encode()
			notification, err := e.VerifyNotification(context.Background(), raw, nil)
			if tt.wantError {
				if err == nil || notification != nil {
					t.Fatalf("invalid parameters accepted: notification=%+v, err=%v", notification, err)
				}
				if calls.Load() != 0 {
					t.Fatal("rejected fields triggered upstream confirmation")
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if notification.Status != payment.ProviderStatusSuccess || notification.Amount != 650 ||
				notification.OrderID != "ORDER123" || notification.TradeNo != "UPSTREAM123" ||
				notification.Metadata["pid"] != "1000" || notification.RawData != raw {
				t.Fatalf("unexpected notification: %+v", notification)
			}
			// Change money after its signature has been fixed, so the otherwise valid
			// callback structure reaches the signature-mismatch assertion.
			values.Set("money", "651.00")
			if _, err := e.VerifyNotification(context.Background(), values.Encode(), nil); err == nil || !strings.Contains(err.Error(), "invalid signature") {
				t.Fatalf("tampered notification: %v", err)
			}
			if calls.Load() != 1 {
				t.Fatalf("confirmation count = %d, want 1", calls.Load())
			}
		})
	}
}

// TestEasyPayNotificationRejectsInvalidFields mutates unsigned structured
// fields with missing, delimiter, control-byte or whitespace values and
// invalid merchant/type/money observations. It expects exact field-validation
// errors before signature presence can determine the result.
func TestEasyPayNotificationRejectsInvalidFields(t *testing.T) {
	t.Parallel()

	e := &EasyPay{config: map[string]string{"pid": "1000", "pkey": "test-merchant-secret"}}
	for _, key := range []string{"pid", "trade_no", "out_trade_no", "type", "money", "trade_status"} {
		for _, value := range []string{"", "demo&label", "demo=label", "demo\x00label", "demo\rlabel", "demo\nlabel", " demo", "demo "} {
			t.Run(key+"/"+value, func(t *testing.T) {
				t.Parallel()
				values := easyPayTestNotificationValues()
				values.Set(key, value)
				want := "invalid notify param: " + key
				if value == "" {
					want = "missing notify param: " + key
				}
				notification, err := e.VerifyNotification(context.Background(), values.Encode(), nil)
				if err == nil || err.Error() != want || notification != nil {
					t.Fatalf("field validation: notification=%+v, err=%v, want %q", notification, err, want)
				}
			})
		}
	}
	for _, tt := range []struct {
		key   string // mutated structured field name
		value string // supplied invalid field text
		want  string // expected validation diagnostic text
	}{
		{key: "pid", value: "2000", want: "easypay notify pid mismatch"},
		{key: "type", value: "alipay/demo", want: "invalid notify param: type"},
		{key: "type", value: "alipay@demo", want: "invalid notify param: type"},
		{key: "type", value: "alipay%26demo", want: "invalid notify param: type"},
		{key: "money", value: "NaN", want: "invalid notify param: money"},
		{key: "money", value: "+Inf", want: "invalid notify param: money"},
		{key: "money", value: "0", want: "invalid notify param: money"},
		{key: "money", value: "-1", want: "invalid notify param: money"},
		{key: "money", value: "invalid", want: "invalid notify param: money"},
	} {
		t.Run(tt.key+"/"+tt.value, func(t *testing.T) {
			t.Parallel()
			values := easyPayTestNotificationValues()
			values.Set(tt.key, tt.value)
			notification, err := e.VerifyNotification(context.Background(), values.Encode(), nil)
			if err == nil || err.Error() != tt.want || notification != nil {
				t.Fatalf("field validation: notification=%+v, err=%v, want %q", notification, err, tt.want)
			}
		})
	}
}

// TestEasyPayQueryOrderStatusMapping serves top-level, nested, numeric and
// absent query fields from loopback endpoints. It checks textual-state
// precedence, required paid references, money and merchant query form
// values for each fixture.
func TestEasyPayQueryOrderStatusMapping(t *testing.T) {
	t.Parallel()

	const orderID = "order-123"
	tests := []struct {
		name        string  // named query layout case
		body        string  // served channel JSON response
		wantStatus  string  // expected core query state
		wantTradeNo string  // expected selected trade reference
		wantAmount  float64 // expected reported numeric money
		wantError   bool    // expected query rejection flag
	}{
		{
			name:        "top level trade success is paid",
			body:        `{"code":1,"trade_status":"TRADE_SUCCESS","status":0,"money":"12.34","trade_no":"gateway-123"}`,
			wantStatus:  payment.ProviderStatusPaid,
			wantTradeNo: "gateway-123",
			wantAmount:  12.34,
		},
		{
			name:        "waiting trade status with paid numeric status stays pending",
			body:        `{"code":1,"trade_status":"WAITING","status":1,"money":"12.34","trade_no":"gateway-123"}`,
			wantStatus:  payment.ProviderStatusPending,
			wantTradeNo: "gateway-123",
			wantAmount:  12.34,
		},
		{
			name:        "empty trade status with paid numeric status stays pending",
			body:        `{"code":1,"trade_status":"","status":1,"money":"12.34"}`,
			wantStatus:  payment.ProviderStatusPending,
			wantTradeNo: "",
			wantAmount:  12.34,
		},
		{
			name:        "nested data trade success is paid",
			body:        `{"code":1,"data":{"trade_status":"TRADE_SUCCESS","status":0,"money":"9.99","trade_no":"data-456"}}`,
			wantStatus:  payment.ProviderStatusPaid,
			wantTradeNo: "data-456",
			wantAmount:  9.99,
		},
		{
			name:        "legacy numeric paid status remains compatible",
			body:        `{"code":"1","status":1,"money":"3.21","trade_no":"real-trade"}`,
			wantStatus:  payment.ProviderStatusPaid,
			wantTradeNo: "real-trade",
			wantAmount:  3.21,
		},
		{
			name:        "legacy numeric non paid status is pending",
			body:        `{"code":1,"status":0,"money":"3.21"}`,
			wantStatus:  payment.ProviderStatusPending,
			wantTradeNo: "",
			wantAmount:  3.21,
		},
		{
			name:        "query business failure is rejected",
			wantError:   true,
			body:        `{"code":0,"msg":"订单不存在"}`,
			wantStatus:  payment.ProviderStatusPending,
			wantTradeNo: "",
		},
		{
			name:        "missing fields are rejected",
			wantError:   true,
			body:        `{}`,
			wantStatus:  payment.ProviderStatusPending,
			wantTradeNo: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			var gotForm url.Values
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				// Serve the fixed response for client parser assertions; handler Write
				// errors are discarded while callers check channel results or diagnostics.
				for _, key := range []string{"act", "pid", "key", "out_trade_no"} {
					if r.URL.Query().Get(key) == "" {
						t.Errorf("missing URL query parameter: %s", key)
					}
				}
				if r.Method != http.MethodPost {
					t.Errorf("method = %q, want %q", r.Method, http.MethodPost)
				}
				if r.URL.Path != "/api.php" {
					t.Errorf("path = %q, want /api.php", r.URL.Path)
				}
				// Report handler form parsing failure without returning, so the client
				// still receives a fixture body and recorded form assertions can expose
				// missing data.
				if err := r.ParseForm(); err != nil {
					t.Errorf("ParseForm: %v", err)
				}
				// Copy parsed form slices before retaining the request observation, so
				// later assertions inspect captured values independently of the handler
				// form map.
				gotForm = make(url.Values, len(r.PostForm))
				for key, values := range r.PostForm {
					gotForm[key] = append([]string(nil), values...)
					if r.URL.Query().Get(key) != r.PostForm.Get(key) {
						t.Errorf("query/form mismatch for %s", key)
					}
				}
				w.Header().Set("Content-Type", "application/json")
				_, _ = w.Write([]byte(tt.body))
			}))
			// Close the loopback listener when the fixture returns, releasing its HTTP
			// resources after the request and response assertions finish.
			defer server.Close()

			provider := newTestEasyPay(t, server.URL)
			resp, err := provider.QueryOrder(context.Background(), orderID)
			if tt.wantError {
				if err == nil || resp != nil {
					t.Fatalf("invalid query accepted: %+v, %v", resp, err)
				}
				return
			}
			if err != nil {
				t.Fatalf("QueryOrder returned error: %v", err)
			}
			if resp.Status != tt.wantStatus {
				t.Fatalf("status = %q, want %q (response=%+v)", resp.Status, tt.wantStatus, resp)
			}
			if resp.TradeNo != tt.wantTradeNo {
				t.Fatalf("trade_no = %q, want %q", resp.TradeNo, tt.wantTradeNo)
			}
			if resp.Amount != tt.wantAmount {
				t.Fatalf("amount = %v, want %v", resp.Amount, tt.wantAmount)
			}
			for key, want := range map[string]string{
				"act":          "order",
				"pid":          "pid-1",
				"key":          "pkey-1",
				"out_trade_no": orderID,
			} {
				if got := gotForm.Get(key); got != want {
					t.Fatalf("form[%s] = %q, want %q (form=%v)", key, got, want, gotForm)
				}
			}
		})
	}
}

// TestEasyPayQueryOrderRejectsUnsafeResponses serves HTTP failure, HTML, plain
// text and empty query replies. It expects operation-specific parser
// diagnostics and checks that the supplied HTML secret text stays out of the
// error.
func TestEasyPayQueryOrderRejectsUnsafeResponses(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string // named query rejection case
		statusCode int    // served HTTP status code
		body       string // served response body text
		wantErr    string // expected parser diagnostic fragment
	}{
		{
			name:       "non success status",
			statusCode: http.StatusBadGateway,
			body:       `{"code":0,"msg":"gateway error"}`,
			wantErr:    "easypay query HTTP 502",
		},
		{
			name:       "html response",
			statusCode: http.StatusOK,
			body:       "<html>secret-response</html>",
			wantErr:    "easypay query non-JSON response (HTTP 200)",
		},
		{
			name:       "plain response",
			statusCode: http.StatusOK,
			body:       "gateway unavailable",
			wantErr:    "easypay query non-JSON response (HTTP 200)",
		},
		{
			name:       "empty response",
			statusCode: http.StatusOK,
			body:       "",
			wantErr:    "easypay query empty response (HTTP 200)",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				// Serve the fixed response for client parser assertions; handler Write
				// errors are discarded while callers check channel results or diagnostics.
				w.WriteHeader(tt.statusCode)
				_, _ = w.Write([]byte(tt.body))
			}))
			// Close the loopback listener when the fixture returns, releasing its HTTP
			// resources after the request and response assertions finish.
			defer server.Close()

			provider := newTestEasyPay(t, server.URL)
			_, err := provider.QueryOrder(context.Background(), "order-unsafe")
			if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
				t.Fatalf("QueryOrder error = %v, want containing %q", err, tt.wantErr)
			}
			if strings.Contains(err.Error(), "secret-response") {
				t.Fatalf("QueryOrder error leaked response body: %v", err)
			}
		})
	}
}

// TestNormalizeEasyPayAPIBase supplies trailing slashes and recognized
// operation suffixes, including an operation query. It expects the same
// normalized origin text for the listed complete URL fixtures.
func TestNormalizeEasyPayAPIBase(t *testing.T) {
	t.Parallel()

	tests := []struct {
		input string // supplied endpoint URL text
		want  string // expected normalized service base
	}{
		{input: "https://zpayz.cn", want: "https://zpayz.cn"},
		{input: "https://zpayz.cn/", want: "https://zpayz.cn"},
		{input: "https://zpayz.cn/mapi.php", want: "https://zpayz.cn"},
		{input: "https://zpayz.cn/submit.php", want: "https://zpayz.cn"},
		{input: "https://zpayz.cn/api.php", want: "https://zpayz.cn"},
		{input: "https://zpayz.cn/api.php?act=refund", want: "https://zpayz.cn"},
	}

	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			t.Parallel()
			if got := normalizeEasyPayAPIBase(tt.input); got != tt.want {
				t.Fatalf("normalizeEasyPayAPIBase(%q) = %q, want %q", tt.input, got, tt.want)
			}
		})
	}
}

// TestEasyPayRefundNormalizesAPIBaseAndSendsOutTradeNoOnly supplies merchant
// and trade labels to a successful loopback refund. It expects a normalized
// endpoint and merchant-only first form with credentials and amount.
func TestEasyPayRefundNormalizesAPIBaseAndSendsOutTradeNoOnly(t *testing.T) {
	t.Parallel()

	var gotPath string
	var gotQuery url.Values
	var gotForm url.Values
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Serve the fixed response for client parser assertions; handler Write
		// errors are discarded while callers check channel results or diagnostics.
		gotPath = r.URL.Path
		gotQuery = r.URL.Query()
		// Report handler form parsing failure without returning, so the client still
		// receives a fixture body and recorded form assertions can expose missing
		// data.
		if err := r.ParseForm(); err != nil {
			t.Errorf("ParseForm: %v", err)
		}
		gotForm = r.PostForm
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"code":1,"msg":"ok"}`))
	}))
	// Close the loopback listener when the fixture returns, releasing its HTTP
	// resources after the request and response assertions finish.
	defer server.Close()

	provider := newTestEasyPay(t, server.URL+"/mapi.php")
	resp, err := provider.Refund(context.Background(), payment.RefundRequest{
		TradeNo: "trade-123",
		OrderID: "out-456",
		Amount:  "1.50",
	})
	if err != nil {
		t.Fatalf("Refund returned error: %v", err)
	}
	if resp == nil || resp.Status != payment.ProviderStatusSuccess {
		t.Fatalf("Refund response = %+v, want success", resp)
	}
	if gotPath != "/api.php" {
		t.Fatalf("refund path = %q, want /api.php", gotPath)
	}
	if gotQuery.Get("act") != "refund" {
		t.Fatalf("refund act query = %q, want refund", gotQuery.Get("act"))
	}
	for key, want := range map[string]string{
		"pid":          "pid-1",
		"key":          "pkey-1",
		"out_trade_no": "out-456",
		"money":        "1.50",
	} {
		if got := gotForm.Get(key); got != want {
			t.Fatalf("form[%s] = %q, want %q (form=%v)", key, got, want, gotForm)
		}
	}
	if got := gotForm.Get("trade_no"); got != "" {
		t.Fatalf("form[trade_no] = %q, want empty (form=%v)", got, gotForm)
	}
}

// TestEasyPayRefundRetriesWithTradeNoWhenOutTradeNoNotFound serves a
// missing-order diagnostic for the first merchant form and success for the
// next trade form. It checks two separate requests and the successful trade
// label returned as RefundID.
func TestEasyPayRefundRetriesWithTradeNoWhenOutTradeNoNotFound(t *testing.T) {
	t.Parallel()

	var gotForms []url.Values
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Serve the fixed response for client parser assertions; handler Write
		// errors are discarded while callers check channel results or diagnostics.
		if r.URL.Path != "/api.php" {
			t.Errorf("refund path = %q, want /api.php", r.URL.Path)
		}
		if r.URL.Query().Get("act") != "refund" {
			t.Errorf("refund act query = %q, want refund", r.URL.Query().Get("act"))
		}
		// Report handler form parsing failure without returning, so the client still
		// receives a fixture body and recorded form assertions can expose missing
		// data.
		if err := r.ParseForm(); err != nil {
			t.Errorf("ParseForm: %v", err)
		}
		gotForms = append(gotForms, r.PostForm)
		w.Header().Set("Content-Type", "application/json")
		// Reject the first merchant-label form with the recognized missing-order
		// text, permitting Refund to submit its separate trade-label alternative
		// next.
		if len(gotForms) == 1 {
			_, _ = w.Write([]byte(`{"code":0,"msg":"订单编号不存在！"}`))
			return
		}
		_, _ = w.Write([]byte(`{"code":1,"msg":"ok"}`))
	}))
	// Close the loopback listener when the fixture returns, releasing its HTTP
	// resources after the request and response assertions finish.
	defer server.Close()

	provider := newTestEasyPay(t, server.URL+"/mapi.php")
	resp, err := provider.Refund(context.Background(), payment.RefundRequest{
		TradeNo: "trade-123",
		OrderID: "out-456",
		Amount:  "1.50",
	})
	if err != nil {
		t.Fatalf("Refund returned error: %v", err)
	}
	if resp == nil || resp.Status != payment.ProviderStatusSuccess || resp.RefundID != "trade-123" {
		t.Fatalf("Refund response = %+v, want success with trade refund id", resp)
	}
	if len(gotForms) != 2 {
		t.Fatalf("refund attempts = %d, want 2", len(gotForms))
	}
	if got := gotForms[0].Get("out_trade_no"); got != "out-456" {
		t.Fatalf("first form[out_trade_no] = %q, want out-456 (form=%v)", got, gotForms[0])
	}
	if got := gotForms[0].Get("trade_no"); got != "" {
		t.Fatalf("first form[trade_no] = %q, want empty (form=%v)", got, gotForms[0])
	}
	if got := gotForms[1].Get("trade_no"); got != "trade-123" {
		t.Fatalf("second form[trade_no] = %q, want trade-123 (form=%v)", got, gotForms[1])
	}
	if got := gotForms[1].Get("out_trade_no"); got != "" {
		t.Fatalf("second form[out_trade_no] = %q, want empty (form=%v)", got, gotForms[1])
	}
}

// TestEasyPayRefundResponseErrors serves HTML, invalid JSON, HTTP failure and
// empty refund responses. It expects diagnostics containing the corresponding
// status and bounded response summary for those short supplied bodies.
func TestEasyPayRefundResponseErrors(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string // named refund rejection case
		statusCode int    // served HTTP status code
		body       string // served response body text
		want       string // expected refund diagnostic fragment
	}{
		{name: "html response", statusCode: http.StatusOK, body: "<html>bad config</html>", want: "unconfirmed (HTTP 200): <html>bad config</html>"},
		{name: "non json response", statusCode: http.StatusOK, body: "not json", want: "unconfirmed (HTTP 200): not json"},
		{name: "non 2xx response", statusCode: http.StatusBadGateway, body: "bad gateway", want: "HTTP 502): bad gateway"},
		{name: "empty response", statusCode: http.StatusOK, body: "", want: "unconfirmed (HTTP 200): <empty>"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				// Serve the fixed response for client parser assertions; handler Write
				// errors are discarded while callers check channel results or diagnostics.
				w.WriteHeader(tt.statusCode)
				_, _ = w.Write([]byte(tt.body))
			}))
			// Close the loopback listener when the fixture returns, releasing its HTTP
			// resources after the request and response assertions finish.
			defer server.Close()

			provider := newTestEasyPay(t, server.URL)
			_, err := provider.Refund(context.Background(), payment.RefundRequest{
				OrderID: "out-456",
				Amount:  "1.50",
			})
			if err == nil {
				t.Fatal("Refund returned nil error")
			}
			if !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("Refund error = %q, want substring %q", err.Error(), tt.want)
			}
		})
	}
}

// TestSummarizeEasyPayResponsePreservesUTF8 supplies 171 three-byte Chinese
// characters to cross the summary byte limit. It expects valid UTF-8 output
// and the ellipsis marking the truncated fixture.
func TestSummarizeEasyPayResponsePreservesUTF8(t *testing.T) {
	t.Parallel()

	// Use 513 UTF-8 bytes to cross the 512-byte summary threshold.
	summary := summarizeEasyPayResponse([]byte(strings.Repeat("错", 171)))
	if !utf8.ValidString(summary) {
		t.Fatalf("summarizeEasyPayResponse returned invalid UTF-8: %q", summary)
	}
	if !strings.HasSuffix(summary, "...") {
		t.Fatalf("summarizeEasyPayResponse() = %q, want truncated suffix", summary)
	}
}

// TestEasyPayCustomMethodsUseConfiguredUpstreamType configures two custom
// mappings and creates a hosted checkout for the USDT visible label. It
// expects the mapped upstream type in the signed browser query.
func TestEasyPayCustomMethodsUseConfiguredUpstreamType(t *testing.T) {
	t.Parallel()

	provider, err := NewEasyPay("test-instance", map[string]string{
		"pid":           "pid-1",
		"pkey":          "pkey-1",
		"apiBase":       "https://pay.example.com",
		"notifyUrl":     "https://example.com/notify",
		"returnUrl":     "https://example.com/return",
		"paymentMode":   paymentModePopup,
		"customMethods": `[{"type":"ldc","upstreamType":"epay","displayName":"LDC"},{"type":"usdt_trc20","upstreamType":"usdt","displayName":"USDT-TRC20"}]`,
	})
	if err != nil {
		t.Fatalf("NewEasyPay: %v", err)
	}

	resp, err := provider.CreatePayment(context.Background(), payment.CreatePaymentRequest{
		OrderID:     "sub2-custom-1",
		Amount:      "1.00",
		PaymentType: "usdt_trc20",
		Subject:     "Custom EasyPay",
	})
	if err != nil {
		t.Fatalf("CreatePayment: %v", err)
	}
	payURL, err := url.Parse(resp.PayURL)
	if err != nil {
		t.Fatalf("parse pay url: %v", err)
	}
	if got := payURL.Query().Get("type"); got != "usdt" {
		t.Fatalf("pay url type = %q, want usdt (%s)", got, resp.PayURL)
	}
}

// TestEasyPayCustomMethodsResolveCIDFromConfiguredUpstreamType maps a custom
// local label to upstream Alipay while configuring both channel overrides. It
// expects upstream Alipay text and the Alipay-specific CID in the hosted
// query.
func TestEasyPayCustomMethodsResolveCIDFromConfiguredUpstreamType(t *testing.T) {
	t.Parallel()

	provider, err := NewEasyPay("test-instance", map[string]string{
		"pid":           "pid-1",
		"pkey":          "pkey-1",
		"apiBase":       "https://pay.example.com",
		"notifyUrl":     "https://example.com/notify",
		"returnUrl":     "https://example.com/return",
		"paymentMode":   paymentModePopup,
		"cidAlipay":     "cid-alipay",
		"cidWxpay":      "cid-wxpay",
		"customMethods": `[{"type":"ldc","upstreamType":"alipay","displayName":"LDC"}]`,
	})
	if err != nil {
		t.Fatalf("NewEasyPay: %v", err)
	}

	resp, err := provider.CreatePayment(context.Background(), payment.CreatePaymentRequest{
		OrderID:     "sub2-custom-cid",
		Amount:      "1.00",
		PaymentType: "ldc",
		Subject:     "Custom EasyPay CID",
	})
	if err != nil {
		t.Fatalf("CreatePayment: %v", err)
	}
	payURL, err := url.Parse(resp.PayURL)
	if err != nil {
		t.Fatalf("parse pay url: %v", err)
	}
	if got := payURL.Query().Get("type"); got != "alipay" {
		t.Fatalf("pay url type = %q, want alipay (%s)", got, resp.PayURL)
	}
	if got := payURL.Query().Get("cid"); got != "cid-alipay" {
		t.Fatalf("pay url cid = %q, want cid-alipay (%s)", got, resp.PayURL)
	}
}

// TestEasyPaySupportedTypesIncludeCustomMethods configures two complete custom
// mappings alongside built-in support. It checks that the advertised method
// list contains Alipay, WeChat and both supplied custom visible codes.
func TestEasyPaySupportedTypesIncludeCustomMethods(t *testing.T) {
	t.Parallel()

	provider, err := NewEasyPay("test-instance", map[string]string{
		"pid":           "pid-1",
		"pkey":          "pkey-1",
		"apiBase":       "https://pay.example.com",
		"notifyUrl":     "https://example.com/notify",
		"returnUrl":     "https://example.com/return",
		"customMethods": `[{"type":"ldc","upstreamType":"epay","displayName":"LDC"},{"type":"usdt_trc20","upstreamType":"usdt","displayName":"USDT-TRC20"}]`,
	})
	if err != nil {
		t.Fatalf("NewEasyPay: %v", err)
	}

	got := strings.Join(provider.SupportedTypes(), ",")
	for _, want := range []string{"alipay", "wxpay", "ldc", "usdt_trc20"} {
		if !strings.Contains(got, want) {
			t.Fatalf("SupportedTypes() = %q, want it to include %q", got, want)
		}
	}
}

// TestEasyPaySignConsistentOutput signs the same nonempty parameter map twice
// with one secret. It expects equal signature text and the
// thirty-two-character length of the emitted MD5 hexadecimal representation.
func TestEasyPaySignConsistentOutput(t *testing.T) {
	t.Parallel()

	params := map[string]string{
		"pid":          "1001",
		"type":         "alipay",
		"out_trade_no": "ORDER123",
		"name":         "Test Product",
		"money":        "10.00",
	}
	pkey := "test_secret_key"

	sign1 := easyPaySign(params, pkey)
	sign2 := easyPaySign(params, pkey)
	if sign1 != sign2 {
		t.Fatalf("easyPaySign should be deterministic: %q != %q", sign1, sign2)
	}
	if len(sign1) != 32 {
		t.Fatalf("MD5 hex should be 32 chars, got %d", len(sign1))
	}
}

// TestEasyPaySignExcludesSignAndSignType compares a base form with one
// containing signature and algorithm-label fields. It expects equal computed
// signatures because those two fields are excluded from signing input.
func TestEasyPaySignExcludesSignAndSignType(t *testing.T) {
	t.Parallel()

	pkey := "my_key"
	base := map[string]string{
		"pid":  "1001",
		"type": "alipay",
	}
	withSign := map[string]string{
		"pid":       "1001",
		"type":      "alipay",
		"sign":      "should_be_ignored",
		"sign_type": "MD5",
	}

	signBase := easyPaySign(base, pkey)
	signWithExtra := easyPaySign(withSign, pkey)

	if signBase != signWithExtra {
		t.Fatalf("sign and sign_type should be excluded: base=%q, withExtra=%q", signBase, signWithExtra)
	}
}

// TestEasyPaySignExcludesEmptyValues compares a base form with empty device
// and clientip values added. It expects identical signatures because
// empty-valued parameters contribute no pair text to the digest.
func TestEasyPaySignExcludesEmptyValues(t *testing.T) {
	t.Parallel()

	pkey := "key123"
	base := map[string]string{
		"pid":  "1001",
		"type": "alipay",
	}
	withEmpty := map[string]string{
		"pid":      "1001",
		"type":     "alipay",
		"device":   "",
		"clientip": "",
	}

	signBase := easyPaySign(base, pkey)
	signWithEmpty := easyPaySign(withEmpty, pkey)

	if signBase != signWithEmpty {
		t.Fatalf("empty values should be excluded: base=%q, withEmpty=%q", signBase, signWithEmpty)
	}
}

// TestEasyPayVerifySignValid computes a signature and then adds signature and
// algorithm labels to the parameter map. It expects verification to succeed
// with that same secret and digest text.
func TestEasyPayVerifySignValid(t *testing.T) {
	t.Parallel()

	params := map[string]string{
		"pid":          "1001",
		"type":         "alipay",
		"out_trade_no": "ORDER456",
		"money":        "25.00",
	}
	pkey := "secret"

	sign := easyPaySign(params, pkey)

	// Add wire signature fields after computing the digest; the verification
	// helper excludes both fields when rebuilding its signed pair text.
	params["sign"] = sign
	params["sign_type"] = "MD5"

	if !easyPayVerifySign(params, pkey, sign) {
		t.Fatal("easyPayVerifySign should return true for a valid signature")
	}
}

// TestEasyPayVerifySignTampered signs original money and then replaces the
// amount in the form. It expects verification failure because the unchanged
// signature describes the earlier parameter value.
func TestEasyPayVerifySignTampered(t *testing.T) {
	t.Parallel()

	params := map[string]string{
		"pid":          "1001",
		"type":         "alipay",
		"out_trade_no": "ORDER789",
		"money":        "50.00",
	}
	pkey := "secret"

	sign := easyPaySign(params, pkey)

	// Keep the earlier digest while changing money, reproducing a mismatch
	// between authenticated parameter text and the current verification input.
	params["money"] = "99.99"

	if easyPayVerifySign(params, pkey, sign) {
		t.Fatal("easyPayVerifySign should return false for tampered params")
	}
}

// TestEasyPayVerifySignWrongKey signs a WeChat form with one secret and
// verifies using another. It expects failure for this mismatched-key fixture
// even though parameter values remain unchanged.
func TestEasyPayVerifySignWrongKey(t *testing.T) {
	t.Parallel()

	params := map[string]string{
		"pid":  "1001",
		"type": "wxpay",
	}

	sign := easyPaySign(params, "correct_key")

	if easyPayVerifySign(params, "wrong_key", sign) {
		t.Fatal("easyPayVerifySign should return false with wrong key")
	}
}

// TestEasyPaySignEmptyParams signs an empty parameter map with a nonempty
// secret. It expects a nonempty thirty-two-character digest, since the secret
// still supplies the signing input for that case.
func TestEasyPaySignEmptyParams(t *testing.T) {
	t.Parallel()

	sign := easyPaySign(map[string]string{}, "key123")
	if sign == "" {
		t.Fatal("easyPaySign with empty params should still produce a hash")
	}
	if len(sign) != 32 {
		t.Fatalf("MD5 hex should be 32 chars, got %d", len(sign))
	}
}

// TestEasyPaySignSortOrder supplies maps with identical pairs written in
// different literal orders. It expects equal signatures, protecting sorted
// pair construction against map iteration order in these fixtures.
func TestEasyPaySignSortOrder(t *testing.T) {
	t.Parallel()

	pkey := "test_key"
	params1 := map[string]string{
		"a": "1",
		"b": "2",
		"c": "3",
	}
	params2 := map[string]string{
		"c": "3",
		"a": "1",
		"b": "2",
	}

	sign1 := easyPaySign(params1, pkey)
	sign2 := easyPaySign(params2, pkey)

	if sign1 != sign2 {
		t.Fatalf("easyPaySign should be order-independent: %q != %q", sign1, sign2)
	}
}

// TestEasyPayVerifySignWrongSignValue supplies the accepted-length all-zero
// signature with a nonempty form and secret. It expects verification to reject
// that incorrect digest for the fixture values.
func TestEasyPayVerifySignWrongSignValue(t *testing.T) {
	t.Parallel()

	params := map[string]string{
		"pid":  "1001",
		"type": "alipay",
	}
	pkey := "key"

	if easyPayVerifySign(params, pkey, "00000000000000000000000000000000") {
		t.Fatal("easyPayVerifySign should return false for an incorrect sign value")
	}
}

// TestEasyPayMerchantIdentityMetadata supplies one configured merchant pid on
// a direct adapter fixture. It expects that label under pid in the map used by
// core merchant comparison.
func TestEasyPayMerchantIdentityMetadata(t *testing.T) {
	t.Parallel()

	provider := &EasyPay{
		config: map[string]string{
			"pid": "1001",
		},
	}

	metadata := provider.MerchantIdentityMetadata()
	if metadata["pid"] != "1001" {
		t.Fatalf("pid = %q, want %q", metadata["pid"], "1001")
	}
}

// TestResolveEasyPayReturnedRef compares root and protocol-relative paths,
// whitespace, absolute URLs, application links and opaque payload text (issue
// #6292). It checks resolved destinations or retained text, including unusable
// bases leaving slash references unchanged.
func TestResolveEasyPayReturnedRef(t *testing.T) {
	t.Parallel()

	const apiBase = "https://pay.example.com/xpay/epay"

	tests := []struct {
		name    string // named reference resolution case
		apiBase string // supplied service URL prefix
		ref     string // returned payment reference text
		want    string // expected launch or QR text
	}{
		{
			// Resolve root-relative paths with the apiBase scheme and host.
			name:    "root relative path resolves against origin",
			apiBase: apiBase,
			ref:     "/api/pay/toapp/ORDER_ID",
			want:    "https://pay.example.com/api/pay/toapp/ORDER_ID",
		},
		{
			name:    "root relative path keeps query string",
			apiBase: apiBase,
			ref:     "/api/pay/toapp?oid=ORDER_ID&t=1",
			want:    "https://pay.example.com/api/pay/toapp?oid=ORDER_ID&t=1",
		},
		{
			name:    "protocol relative reference inherits base scheme",
			apiBase: apiBase,
			ref:     "//cashier.example.com/pay/ORDER_ID",
			want:    "https://cashier.example.com/pay/ORDER_ID",
		},
		{
			name:    "surrounding whitespace does not defeat resolution",
			apiBase: apiBase,
			ref:     "  /api/pay/toapp/ORDER_ID  ",
			want:    "https://pay.example.com/api/pay/toapp/ORDER_ID",
		},
		{
			name:    "http base keeps its scheme",
			apiBase: "http://pay.example.com",
			ref:     "/api/pay/toapp/ORDER_ID",
			want:    "http://pay.example.com/api/pay/toapp/ORDER_ID",
		},
		// These values are returned unchanged.
		{
			name:    "empty stays empty",
			apiBase: apiBase,
			ref:     "",
			want:    "",
		},
		{
			name:    "absolute https url is untouched",
			apiBase: apiBase,
			ref:     "https://cashier.other.com/pay/ORDER_ID",
			want:    "https://cashier.other.com/pay/ORDER_ID",
		},
		{
			name:    "wechat deep link is untouched",
			apiBase: apiBase,
			ref:     "weixin://wxpay/bizpayurl?pr=ABCdef",
			want:    "weixin://wxpay/bizpayurl?pr=ABCdef",
		},
		{
			name:    "alipay deep link is untouched",
			apiBase: apiBase,
			ref:     "alipays://platformapi/startapp?saId=10000007",
			want:    "alipays://platformapi/startapp?saId=10000007",
		},
		{
			name:    "face-to-face wxp payload is untouched",
			apiBase: apiBase,
			ref:     "wxp://f2f0Abc_dEfGhIjKlMnOpQrStU",
			want:    "wxp://f2f0Abc_dEfGhIjKlMnOpQrStU",
		},
		{
			// A token without a leading slash may be an opaque QR payload.
			name:    "opaque token without leading slash is untouched",
			apiBase: apiBase,
			ref:     "OrderToken123",
			want:    "OrderToken123",
		},
		{
			name:    "path-like reference without leading slash is untouched",
			apiBase: apiBase,
			ref:     "api/pay/toapp/ORDER_ID",
			want:    "api/pay/toapp/ORDER_ID",
		},
		{
			name:    "empty api base leaves the reference alone",
			apiBase: "",
			ref:     "/api/pay/toapp/ORDER_ID",
			want:    "/api/pay/toapp/ORDER_ID",
		},
		{
			name:    "api base without scheme leaves the reference alone",
			apiBase: "pay.example.com",
			ref:     "/api/pay/toapp/ORDER_ID",
			want:    "/api/pay/toapp/ORDER_ID",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if got := resolveEasyPayReturnedRef(tt.apiBase, tt.ref); got != tt.want {
				t.Fatalf("resolveEasyPayReturnedRef(%q, %q) = %q, want %q", tt.apiBase, tt.ref, got, tt.want)
			}
		})
	}
}

// TestEasyPayCreatePaymentResolvesRelativeQRCode serves a root-relative QR
// reference with no launch URL from the local mapi stub. It expects an
// origin-qualified QR value, empty PayURL and the reported trade label.
func TestEasyPayCreatePaymentResolvesRelativeQRCode(t *testing.T) {
	t.Parallel()

	provider, origin := newEasyPayMapiStub(t, "/xpay/epay",
		`{"code":1,"trade_no":"TRADE_NO","payurl":"","qrcode":"/api/pay/toapp/ORDER_ID"}`)

	resp, err := provider.CreatePayment(context.Background(), payment.CreatePaymentRequest{
		OrderID:     "sub2-relative-qr",
		Amount:      "1.00",
		PaymentType: payment.TypeWxpay,
		Subject:     "Relative QR",
	})
	if err != nil {
		t.Fatalf("CreatePayment: %v", err)
	}
	if want := origin + "/api/pay/toapp/ORDER_ID"; resp.QRCode != want {
		t.Fatalf("QRCode = %q, want %q (a bare path would render as text in WeChat)", resp.QRCode, want)
	}
	if resp.PayURL != "" {
		t.Fatalf("PayURL = %q, want empty (upstream sent none)", resp.PayURL)
	}
	if resp.TradeNo != "TRADE_NO" {
		t.Fatalf("TradeNo = %q, want TRADE_NO", resp.TradeNo)
	}
}

// TestEasyPayCreatePaymentResolvesRelativeMobilePayURL2 serves desktop and
// mobile root-relative launch references to a mobile request. It expects the
// mobile alternative resolved against the loopback origin.
func TestEasyPayCreatePaymentResolvesRelativeMobilePayURL2(t *testing.T) {
	t.Parallel()

	provider, origin := newEasyPayMapiStub(t, "",
		`{"code":1,"trade_no":"TRADE_NO","payurl":"/pc/pay/ORDER_ID","payurl2":"/h5/pay/ORDER_ID"}`)

	resp, err := provider.CreatePayment(context.Background(), payment.CreatePaymentRequest{
		OrderID:     "sub2-relative-h5",
		Amount:      "1.00",
		PaymentType: payment.TypeWxpay,
		Subject:     "Relative H5",
		IsMobile:    true,
	})
	if err != nil {
		t.Fatalf("CreatePayment: %v", err)
	}
	// The supplied mobile alternative wins before relative resolution; the
	// asserted destination therefore uses the H5 path and loopback origin.
	if want := origin + "/h5/pay/ORDER_ID"; resp.PayURL != want {
		t.Fatalf("PayURL = %q, want %q", resp.PayURL, want)
	}
}

// TestEasyPayCreatePaymentKeepsUpstreamAbsoluteAndOpaqueValues serves an
// absolute browser URL and a WeChat QR application link. It expects both
// upstream strings retained exactly despite a nested instance base path.
func TestEasyPayCreatePaymentKeepsUpstreamAbsoluteAndOpaqueValues(t *testing.T) {
	t.Parallel()

	const (
		absoluteURL = "https://cashier.example.com/pay/ORDER_ID"
		deepLink    = "weixin://wxpay/bizpayurl?pr=ABCdef"
	)
	provider, _ := newEasyPayMapiStub(t, "/xpay/epay",
		`{"code":1,"trade_no":"TRADE_NO","payurl":"`+absoluteURL+`","qrcode":"`+deepLink+`"}`)

	resp, err := provider.CreatePayment(context.Background(), payment.CreatePaymentRequest{
		OrderID:     "sub2-absolute",
		Amount:      "1.00",
		PaymentType: payment.TypeWxpay,
		Subject:     "Absolute",
	})
	if err != nil {
		t.Fatalf("CreatePayment: %v", err)
	}
	if resp.PayURL != absoluteURL {
		t.Fatalf("PayURL = %q, want %q unchanged", resp.PayURL, absoluteURL)
	}
	if resp.QRCode != deepLink {
		t.Fatalf("QRCode = %q, want %q unchanged", resp.QRCode, deepLink)
	}
}

// easyPayTestNotificationValues returns fresh unsigned standard callback
// values with free-text name and structured identifiers. Validation cases
// mutate their own map before submitting it, so earlier field failures can be
// asserted before signature checking.
func easyPayTestNotificationValues() url.Values {
	return url.Values{
		"pid": {"1000"}, "trade_no": {"UPSTREAM123"}, "out_trade_no": {"ORDER123"},
		"type": {"alipay"}, "name": {"充值 & 套餐=10%"}, "money": {"650.00"},
		"trade_status": {tradeStatusSuccess},
	}
}

// newTestEasyPay constructs an adapter with fixed merchant credentials and
// caller-supplied base text, failing the test on configuration error. Endpoint
// normalization remains active, and HTTP callers supply a loopback URL for
// their fixture.
func newTestEasyPay(t *testing.T, apiBase string) *EasyPay {
	t.Helper()

	provider, err := NewEasyPay("test-instance", map[string]string{
		"pid":       "pid-1",
		"pkey":      "pkey-1",
		"apiBase":   apiBase,
		"notifyUrl": "https://example.com/notify",
		"returnUrl": "https://example.com/return",
	})
	if err != nil {
		t.Fatalf("NewEasyPay: %v", err)
	}
	return provider
}

// newEasyPayMapiStub returns a provider using a fixed local JSON response and
// the listener origin. It registers listener cleanup with the test and
// combines basePath with that origin to exercise installation paths.
//
// The returned provider uses normal base normalization and default API
// checkout, while creation-response fields come from the supplied body string.
// Callers can compare root-relative resolution with the origin independently
// of the nested base, and malformed provider setup fails the test after
// cleanup is already registered.
func newEasyPayMapiStub(t *testing.T, basePath, body string) (*EasyPay, string) {
	t.Helper()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		// Serve the fixed response for client parser assertions; handler Write
		// errors are discarded while callers check channel results or diagnostics.
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(body))
	}))
	// Register listener cleanup before provider construction can fail, keeping
	// the helper-owned loopback resource tied to the test lifetime.
	t.Cleanup(server.Close)

	apiBase := server.URL + basePath
	provider, err := NewEasyPay("test-instance", map[string]string{
		"pid":       "pid-1",
		"pkey":      "pkey-1",
		"apiBase":   apiBase,
		"notifyUrl": "https://example.com/notify",
		"returnUrl": "https://example.com/return",
	})
	if err != nil {
		t.Fatalf("NewEasyPay: %v", err)
	}
	return provider, server.URL
}
