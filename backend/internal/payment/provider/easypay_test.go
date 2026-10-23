package provider

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/TokenFlux/TokenRouter/internal/payment"
)

// EasyPay callback fixtures sign form values locally and distinguish required
// notification fields from signed V1 extensions, duplicate values and unknown
// parameter names. Query fixtures exercise status precedence across JSON
// layouts and keep requested merchant labels when upstream references are
// absent.
//
// Loopback refund fixtures capture alternative identifier forms in order and
// exercise JSON or text success responses used by V1-compatible platforms.
// URL fixtures separate slash-prefixed resolution from absolute links or
// opaque QR text, including protocol-relative references that change host.
// Signing tests cover parameter order and excluded fields independently of
// callback structure, while bounded diagnostic fixtures assert valid UTF-8 at
// the truncated prefix.

// TestEasyPayNotificationRejectsCheckoutParameters generates a signed
// hosted-checkout query and submits it as callback form text. It expects
// rejection because the checkout form lacks required callback identity and
// status fields.
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

// TestEasyPayNotificationRejectsForgedCheckoutSignature reproduces the
// checkout-signature replay from the upstream security report. A return URL
// contains a success field that is promoted into the callback, but checkout
// fields remain outside the accepted notification set.
func TestEasyPayNotificationRejectsForgedCheckoutSignature(t *testing.T) {
	t.Parallel()

	e := &EasyPay{config: map[string]string{
		"pid": "1000", "pkey": "test-merchant-secret",
		"notifyUrl": "https://site.example.com/api/v1/payment/webhook/easypay",
	}}
	returnURL := "https://site.example.com/payment/result?order_id=99&out_trade_no=ORDER123&status=success&trade_status=TRADE_SUCCESS"
	createParams := map[string]string{
		"pid": "1000", "type": "alipay", "out_trade_no": "ORDER123",
		"notify_url": e.config["notifyUrl"], "return_url": returnURL,
		"name": "balance recharge", "money": "650.00",
	}
	sign := easyPaySign(createParams, e.config["pkey"])

	callback := url.Values{
		"pid": {"1000"}, "type": {"alipay"}, "out_trade_no": {"ORDER123"},
		"notify_url": {e.config["notifyUrl"]}, "name": {"balance recharge"},
		"money":      {"650.00"},
		"return_url": {"https://site.example.com/payment/result?order_id=99&out_trade_no=ORDER123&status=success"},
	}
	raw := callback.Encode() + "&trade_status=TRADE_SUCCESS&sign=" + sign + "&sign_type=MD5"
	if notification, err := e.VerifyNotification(context.Background(), raw, nil); err == nil || notification != nil {
		t.Fatalf("forged checkout signature accepted: notification=%+v, err=%v", notification, err)
	}
}

// TestEasyPayNotificationParameters assembles standard, custom-type and
// free-text forms with V1 extension fields before signing, then adds any
// duplicate values. It checks accepted observations, rejects duplicates and
// rejects amount changes after signing.
func TestEasyPayNotificationParameters(t *testing.T) {
	t.Parallel()

	e := &EasyPay{config: map[string]string{"pid": "1000", "pkey": "test-merchant-secret"}}
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
		{name: "ambiguous optional param", extraKey: "param", extra: "merchant=value&extra=中文", wantError: true},
		{name: "empty optional param", extraKey: "param"},
		{name: "API trade number", extraKey: "api_trade_no", extra: "API123"},
		{name: "payment creation time", extraKey: "addtime", extra: "2026-10-23 17:59:00"},
		{name: "payment completion time", extraKey: "endtime", extra: "2026-10-23 18:00:00"},
		{name: "buyer identifier", extraKey: "buyer", extra: "buyer@example.com"},
		{name: "timestamp", extraKey: "timestamp", extra: "1792759200"},
		{name: "return URL", extraKey: "return_url", extra: "https://site.example.com/payment/result", wantError: true},
		{name: "notify URL", extraKey: "notify_url", extra: "https://site.example.com/notify", wantError: true},
		{name: "device", extraKey: "device", extra: "mobile", wantError: true},
		{name: "unknown signed field", extraKey: "provider_extension", extra: "value", wantError: true},
		{name: "empty unknown field", extraKey: "provider_extension", wantError: true},
		{name: "duplicate status", duplicate: "trade_status", wantError: true},
		{name: "duplicate order", duplicate: "out_trade_no", wantError: true},
		{name: "duplicate signature", duplicate: "sign", wantError: true},
		{name: "duplicate amount", duplicate: "money", wantError: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
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
		})
	}
}

// TestEasyPayNotificationRejectsUnknownParameterNames supplies callback forms
// with names outside the bounded V1 notification set. The verifier must reject
// each name before signature acceptance can interpret the callback.
func TestEasyPayNotificationRejectsUnknownParameterNames(t *testing.T) {
	t.Parallel()

	e := &EasyPay{config: map[string]string{"pid": "1000", "pkey": "test-merchant-secret"}}
	for _, tt := range []struct {
		name string // unsafe parameter-name case
		key  string // submitted extension key
	}{
		{name: "space", key: "bad key"},
		{name: "ampersand", key: "bad&key"},
		{name: "equals", key: "bad=key"},
		{name: "NUL", key: "bad\x00key"},
		{name: "empty", key: ""},
	} {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			values := easyPayTestNotificationValues()
			values[tt.key] = []string{"value"}
			notification, err := e.VerifyNotification(context.Background(), values.Encode(), nil)
			if err == nil || notification != nil || !strings.Contains(err.Error(), "unexpected notify param") {
				t.Fatalf("unsafe parameter name accepted: notification=%+v, err=%v", notification, err)
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
// precedence, requested-reference retention, money and merchant query form
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
			wantTradeNo: orderID,
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
			body:        `{"code":1,"status":1,"money":"3.21"}`,
			wantStatus:  payment.ProviderStatusPaid,
			wantTradeNo: orderID,
			wantAmount:  3.21,
		},
		{
			name:        "legacy numeric non paid status is pending",
			body:        `{"code":1,"status":0,"money":"3.21"}`,
			wantStatus:  payment.ProviderStatusPending,
			wantTradeNo: orderID,
			wantAmount:  3.21,
		},
		{
			name:        "query failure code remains pending",
			body:        `{"code":0,"msg":"订单不存在","status":1,"money":"3.21"}`,
			wantStatus:  payment.ProviderStatusPending,
			wantTradeNo: orderID,
		},
		{
			name:        "string scalar response is compatible",
			body:        `{"code":"1","out_trade_no":"order-123","trade_no":"gateway-123","status":"1","money":"3.21"}`,
			wantStatus:  payment.ProviderStatusPaid,
			wantTradeNo: "gateway-123",
			wantAmount:  3.21,
		},
		{
			name:        "missing fields are pending",
			body:        `{}`,
			wantStatus:  payment.ProviderStatusPending,
			wantTradeNo: orderID,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			var gotForm url.Values
			var gotQuery url.Values
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				// Serve the fixed response for client parser assertions; handler Write
				// errors are discarded while callers check channel results or diagnostics.
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
				}
				gotQuery = r.URL.Query()
				w.Header().Set("Content-Type", "application/json")
				_, _ = w.Write([]byte(tt.body))
			}))
			// Close the loopback listener when the fixture returns, releasing its HTTP
			// resources after the request and response assertions finish.
			defer server.Close()

			provider := newTestEasyPay(t, server.URL)
			resp, err := provider.QueryOrder(context.Background(), orderID)
			if tt.wantError {
				if err == nil {
					t.Fatalf("QueryOrder response = %+v, want error", resp)
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
				if got := gotQuery.Get(key); got != want {
					t.Fatalf("query[%s] = %q, want %q (query=%v)", key, got, want, gotQuery)
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
		{
			name:       "foreign order echo",
			statusCode: http.StatusOK,
			body:       `{"code":1,"out_trade_no":"different-order","status":1,"money":"10.00"}`,
			wantErr:    "easypay query out_trade_no mismatch",
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

// TestEasyPayConfirmNotification checks that a signed success observation is
// still rejected unless the upstream order query reports the same paid amount
// and trade reference. This is the final guard against reusable or spliced
// checkout signatures.
func TestEasyPayConfirmNotification(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string // named confirmation case
		body      string // served order-query response
		wantError string // expected confirmation error fragment
	}{
		{
			name: "matching paid order",
			body: `{"code":1,"out_trade_no":"ORDER123","trade_no":"UPSTREAM123","money":"10.20","status":1}`,
		},
		{
			name:      "upstream order pending",
			body:      `{"code":1,"out_trade_no":"ORDER123","trade_no":"UPSTREAM123","money":"10.20","status":0}`,
			wantError: "not paid",
		},
		{
			name:      "amount mismatch",
			body:      `{"code":1,"out_trade_no":"ORDER123","trade_no":"UPSTREAM123","money":"0.01","status":1}`,
			wantError: "amount mismatch",
		},
		{
			name:      "trade number mismatch",
			body:      `{"code":1,"out_trade_no":"ORDER123","trade_no":"OTHER","money":"10.20","status":1}`,
			wantError: "trade_no mismatch",
		},
		{
			name:      "failure response with paid status",
			body:      `{"code":0,"msg":"订单不存在","money":"10.20","status":1}`,
			wantError: "订单不存在",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				_, _ = w.Write([]byte(tt.body))
			}))
			defer server.Close()

			provider := newTestEasyPay(t, server.URL)
			notification := &payment.PaymentNotification{
				TradeNo: "UPSTREAM123", OrderID: "ORDER123", Amount: 10.20,
				Status: payment.NotificationStatusSuccess,
			}
			err := provider.ConfirmNotification(context.Background(), notification)
			if tt.wantError != "" {
				if err == nil || !strings.Contains(err.Error(), tt.wantError) {
					t.Fatalf("ConfirmNotification error = %v, want containing %q", err, tt.wantError)
				}
				return
			}
			if err != nil {
				t.Fatalf("ConfirmNotification returned error: %v", err)
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

// TestEasyPayRefundResponseCompatibility serves explicit JSON and exact
// plain-text success variants. Reported Chinese money must match the amount
// submitted for the current refund attempt.
func TestEasyPayRefundResponseCompatibility(t *testing.T) {
	t.Parallel()

	for _, body := range []string{
		`{"code":1,"msg":"ok"}`,
		`{"code":"1","msg":"success"}`,
		`{"code":1,"msg":"退款成功！退款金额¥10.20"}`,
		"退款成功！退款金额¥10.20",
		"\ufeff 退款成功！退款金额¥10.20 ",
		"success",
		"ok",
		"1",
	} {
		t.Run(body, func(t *testing.T) {
			t.Parallel()
			status, err := parseEasyPayRefundResponse(http.StatusOK, []byte(body), "10.20")
			if err != nil {
				t.Fatalf("parseEasyPayRefundResponse(%q): %v", body, err)
			}
			if status != payment.ProviderStatusSuccess {
				t.Fatalf("parseEasyPayRefundResponse(%q) status = %q, want success", body, status)
			}
		})
	}
}

// TestEasyPayRefundIndeterminateResponsesRemainPending serves successful HTTP
// responses that do not prove this refund attempt completed. Refund must stop
// without trying another identifier and expose pending manual verification.
func TestEasyPayRefundIndeterminateResponsesRemainPending(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string // named indeterminate response case
		statusCode int    // served HTTP status; zero selects HTTP 200
		body       string // served response body
	}{
		{name: "html response", body: "<html>bad config</html>"},
		{name: "non json response", body: "not json"},
		{name: "ambiguous success text", body: "原订单未支付成功"},
		{name: "previous refund diagnostic", body: "已退款，请勿重复退款"},
		{name: "fractional JSON code", body: `{"code":1.9,"msg":"invalid code"}`},
		{name: "empty response", body: ""},
		{name: "plain amount mismatch", body: "退款成功！退款金额¥10.20"},
		{name: "JSON amount mismatch", body: `{"code":1,"msg":"退款成功！退款金额¥10.20"}`},
		{name: "upstream bad gateway", statusCode: http.StatusBadGateway, body: "bad gateway"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			var attempts int
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				attempts++
				if tt.statusCode != 0 {
					w.WriteHeader(tt.statusCode)
				}
				_, _ = w.Write([]byte(tt.body))
			}))
			defer server.Close()

			provider := newTestEasyPay(t, server.URL)
			resp, err := provider.Refund(context.Background(), payment.RefundRequest{
				TradeNo: "trade-123",
				OrderID: "out-456",
				Amount:  "1.50",
			})
			if err != nil {
				t.Fatalf("Refund returned error: %v", err)
			}
			if resp == nil || resp.Status != payment.ProviderStatusPending || resp.RefundID != "out-456" {
				t.Fatalf("Refund response = %+v, want pending with merchant refund id", resp)
			}
			if attempts != 1 {
				t.Fatalf("refund attempts = %d, want 1 for indeterminate response", attempts)
			}
		})
	}
}

// TestEasyPayRefundTransportErrorPreservesPendingProgress closes a loopback
// endpoint before submission. The adapter must retain the attempted merchant
// identity with pending state so the workflow can compensate and reconcile it.
func TestEasyPayRefundTransportErrorPreservesPendingProgress(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	serverURL := server.URL
	server.Close()

	provider := newTestEasyPay(t, serverURL)
	resp, err := provider.Refund(context.Background(), payment.RefundRequest{
		TradeNo: "trade-123",
		OrderID: "out-456",
		Amount:  "1.50",
	})
	if err == nil {
		t.Fatal("Refund returned nil error")
	}
	if resp == nil || resp.Status != payment.ProviderStatusPending || resp.RefundID != "out-456" {
		t.Fatalf("Refund response = %+v, want pending with attempted merchant refund id", resp)
	}
}

// TestEasyPayRefundResponseErrors serves explicit JSON and HTTP failures. The
// adapter must return their diagnostics instead of converting them to success
// or pending progress.
func TestEasyPayRefundResponseErrors(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string // named explicit failure case
		statusCode int    // served HTTP status code
		body       string // served response body text
		want       string // expected refund diagnostic fragment
	}{
		{name: "zero failure code", statusCode: http.StatusOK, body: `{"code":0,"msg":"退款失败"}`, want: "refund failed (HTTP 200): 退款失败"},
		{name: "success-looking failure diagnostic", statusCode: http.StatusOK, body: `{"code":0,"msg":"已退款金额超过订单金额，本次退款失败"}`, want: "refund failed (HTTP 200): 已退款金额超过订单金额，本次退款失败"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.WriteHeader(tt.statusCode)
				_, _ = w.Write([]byte(tt.body))
			}))
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
