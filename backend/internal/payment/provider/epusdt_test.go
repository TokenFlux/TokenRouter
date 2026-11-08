package provider

import (
	"context"
	"crypto/md5"
	"encoding/hex"
	"fmt"
	"maps"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/TokenFlux/TokenRouter/internal/payment"
	"github.com/TokenFlux/TokenRouter/internal/pkg/apperror"
)

// epusdt_test.go exercises EPay redirect checkout and payment observation
// against local TLS gateway fixtures. Callback cases require signed fiat money
// and a matching paid cashier, while malformed forms and redirect destinations
// remain unable to reach successful local payment confirmation. Creation cases
// distinguish explicit rejection from uncertain acceptance and retain callback
// verification for normalized product descriptions.

// TestEPUSDTCreatePayment sends one EPay form and inspects the cashier redirect.
// It checks a fixed MD5 signing vector and ensures browser navigation is not followed.
func TestEPUSDTCreatePayment(t *testing.T) {
	calls := 0
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		require.Equal(t, http.MethodPost, r.Method)
		require.Equal(t, "/payments/epay/v1/order/create-transaction/submit.php", r.URL.Path)
		require.NoError(t, r.ParseForm())
		require.Equal(t, "100.00", r.Form.Get("money"))
		require.Equal(t, "1001", r.Form.Get("pid"))
		require.Equal(t, "tr_fixture_order", r.Form.Get("out_trade_no"))
		require.Equal(t, "alipay", r.Form.Get("type"))
		require.Equal(t, "MD5", r.Form.Get("sign_type"))
		require.NotContains(t, r.Form, "signature")
		canonical := "currency=cny&money=100.00&name=Balance recharge&notify_url=https://site.example/api/v1/payment/webhook/epusdt&out_trade_no=tr_fixture_order&pid=1001&return_url=https://site.example/payment/result&type=alipaymerchant-secret"
		digest := md5.Sum([]byte(canonical))
		require.Equal(t, hex.EncodeToString(digest[:]), r.Form.Get("sign"))
		w.Header().Set("Location", "/pay/checkout-counter/gateway_trade")
		w.WriteHeader(http.StatusFound)
	}))
	defer server.Close()
	adapter := newEPUSDTFixture(t, server)
	result, err := adapter.CreatePayment(context.Background(), payment.CreatePaymentRequest{
		OrderID: "tr_fixture_order", Amount: "100.00", Subject: "Balance recharge",
		ReturnURL: "https://site.example/payment/result",
	})
	require.NoError(t, err)
	require.Equal(t, "gateway_trade", result.TradeNo)
	require.Equal(t, server.URL+"/pay/checkout-counter/gateway_trade", result.PayURL)
	require.Equal(t, "CNY", result.Currency)
	require.True(t, result.ExpiresAt.IsZero())
	require.Equal(t, 1, calls)
}

// TestEPUSDTCreateRejectsUnconfirmedResponses submits checkout to malformed redirect fixtures.
// Uncertain gateway acceptance retains the local order, while external destinations cannot become trusted cashier references.
func TestEPUSDTCreateRejectsUnconfirmedResponses(t *testing.T) {
	for _, location := range []string{"", "https://evil.example/pay/checkout-counter/gateway_trade", "/wrong/gateway_trade", "/pay/checkout-counter/../other", "/pay/checkout-counter/gateway_trade?extra=1"} {
		t.Run(location, func(t *testing.T) {
			server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				if location != "" {
					w.Header().Set("Location", location)
					w.WriteHeader(http.StatusFound)
				}
			}))
			defer server.Close()
			adapter := newEPUSDTFixture(t, server)
			result, err := adapter.CreatePayment(context.Background(), payment.CreatePaymentRequest{OrderID: "order", Amount: "100.00"})
			require.Nil(t, result)
			var appErr *apperror.ApplicationError
			require.ErrorAs(t, err, &appErr)
			require.Equal(t, payment.CodePaymentCreateUnconfirmed, appErr.Reason)
			require.NotContains(t, err.Error(), "merchant-secret")
		})
	}
}

// TestEPUSDTCreatePaymentClassifiesRejections exercises complete and ambiguous gateway errors.
// Only documented pre-creation rejections release checkout; duplicate orders, inconsistent
// envelopes, and unavailable responses must keep the original order recoverable.
func TestEPUSDTCreatePaymentClassifiesRejections(t *testing.T) {
	cases := []struct {
		name     string // gateway response scenario
		status   int    // HTTP response status
		body     string // gateway JSON error envelope
		rejected bool   // definite pre-creation rejection expected
	}{
		{"signature", 401, `{"status_code":401,"message":"signature verification failed","data":null}`, true},
		{"wallet", 400, `{"status_code":10003,"data":null}`, true},
		{"amount", 400, `{"status_code":10004,"data":null}`, true},
		{"amount channel", 400, `{"status_code":10005,"data":null}`, true},
		{"rate", 400, `{"status_code":10006,"data":null}`, true},
		{"parameters", 400, `{"status_code":10009}`, true},
		{"chain", 400, `{"status_code":10014,"data":null}`, true},
		{"asset", 400, `{"status_code":10016,"data":null}`, true},
		{"callback URL", 400, `{"status_code":10041,"data":null}`, true},
		{"duplicate order", 400, `{"status_code":10002,"data":null}`, false},
		{"system error", 400, `{"status_code":400,"data":null}`, false},
		{"unknown code", 400, `{"status_code":19999,"data":null}`, false},
		{"server error", 500, `{"status_code":401,"data":null}`, false},
		{"request timeout", 408, `{"status_code":401,"data":null}`, false},
		{"unexpected success status", 200, `{"status_code":401,"data":null}`, false},
		{"conflicting code", 401, `{"status_code":10004,"data":null}`, false},
		{"order data", 401, `{"status_code":401,"data":{"trade_id":"gateway_trade"}}`, false},
		{"invalid JSON", 401, `merchant-secret`, false},
		{"truncated JSON", 401, `{"status_code":401`, false},
		{"oversized body", 401, `{"status_code":401}` + strings.Repeat(" ", 1<<20), false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(tc.status)
				_, _ = w.Write([]byte(tc.body))
			}))
			defer server.Close()
			adapter := newEPUSDTFixture(t, server)
			result, err := adapter.CreatePayment(t.Context(), payment.CreatePaymentRequest{
				OrderID: "tr_rejected", Amount: "100.00", Subject: "Balance recharge",
			})
			require.Nil(t, result)
			require.Error(t, err)
			require.NotContains(t, err.Error(), "merchant-secret")
			if tc.rejected {
				require.NotEqual(t, payment.CodePaymentCreateUnconfirmed, apperror.Reason(err))
			} else {
				require.Equal(t, payment.CodePaymentCreateUnconfirmed, apperror.Reason(err))
			}
		})
	}
}

// TestEPUSDTProductNameRoundTrip sends ordinary and punctuated subscription names to checkout.
// Gateway callbacks reuse the signed normalized name, while the original plan and
// local display subject retain their punctuation and remain eligible for purchase.
func TestEPUSDTProductNameRoundTrip(t *testing.T) {
	cases := []struct {
		original string // original subscription product name
		gateway  string // expected gateway product description
	}{
		{"Team Pro", "Team Pro"},
		{"Pro & Team = Plus", "Pro ＆ Team ＝ Plus"},
		{"  团队\nPro & Plus\t", "团队 Pro ＆ Plus"},
	}
	for _, tc := range cases {
		t.Run(tc.original, func(t *testing.T) {
			server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				switch r.URL.Path {
				case "/payments/epay/v1/order/create-transaction/submit.php":
					require.NoError(t, r.ParseForm())
					require.Equal(t, tc.gateway, r.Form.Get("name"))
					w.Header().Set("Location", "/pay/checkout-counter/gateway_trade")
					w.WriteHeader(http.StatusFound)
				case "/pay/checkout-counter-resp/gateway_trade":
					_, _ = w.Write([]byte(`{"status_code":200,"data":{"trade_id":"gateway_trade","amount":100,"currency":"CNY"}}`))
				case "/pay/check-status/gateway_trade":
					_, _ = w.Write([]byte(`{"status_code":200,"data":{"trade_id":"gateway_trade","status":2}}`))
				default:
					t.Errorf("unexpected path: %s", r.URL.Path)
					w.WriteHeader(http.StatusNotFound)
				}
			}))
			defer server.Close()
			adapter := newEPUSDTFixture(t, server)
			plan := &payment.SubscriptionPlan{ProductName: tc.original}
			subject := payment.PaymentSubject(plan, 100, nil, nil, "en")
			result, err := adapter.CreatePayment(t.Context(), payment.CreatePaymentRequest{
				OrderID: "tr_named", Amount: "100.00", Subject: subject,
			})
			require.NoError(t, err)
			require.Equal(t, "gateway_trade", result.TradeNo)
			require.Equal(t, tc.original, plan.ProductName)
			require.Equal(t, tc.original, subject)

			params := map[string]string{
				"pid": "1001", "out_trade_no": "tr_named", "trade_no": result.TradeNo,
				"money": "100.0000", "type": "alipay", "name": tc.gateway,
				"trade_status": "TRADE_SUCCESS", "sign_type": "MD5",
			}
			form := url.Values{}
			for key, value := range params {
				form.Set(key, value)
			}
			form.Set("sign", easyPaySign(params, "merchant-secret"))
			notification, err := adapter.VerifyNotification(t.Context(), form.Encode(), nil)
			require.NoError(t, err)
			require.Equal(t, "tr_named", notification.OrderID)
			require.Equal(t, 100.0, notification.Amount)
		})
	}
}

// TestEPUSDTQueryUsesCurrentStatus reads cashier money and the dedicated status endpoint.
// Token quantities cannot replace the fiat amount used for the local payment confirmation.
func TestEPUSDTQueryUsesCurrentStatus(t *testing.T) {
	for _, state := range []int{epusdtWaiting, epusdtPaid, epusdtExpired, epusdtSelecting, 99} {
		t.Run(fmt.Sprint(state), func(t *testing.T) {
			server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if strings.HasPrefix(r.URL.Path, "/pay/checkout-counter-resp/") {
					_, _ = w.Write([]byte(`{"status_code":200,"data":{"trade_id":"gateway_trade","amount":100,"actual_amount":14.29,"currency":"CNY","status":1}}`))
					return
				}
				require.Equal(t, "/pay/check-status/gateway_trade", r.URL.Path)
				_, _ = fmt.Fprintf(w, `{"status_code":200,"data":{"trade_id":"gateway_trade","status":%d}}`, state)
			}))
			defer server.Close()
			adapter := newEPUSDTFixture(t, server)
			result, err := adapter.QueryOrder(context.Background(), "gateway_trade")
			if state == 99 {
				require.Error(t, err)
				return
			}
			require.NoError(t, err)
			require.Equal(t, 100.0, result.Amount)
			want := map[int]string{
				1: payment.ProviderStatusPending, 2: payment.ProviderStatusSuccess,
				3: payment.ProviderStatusFailed, 4: payment.ProviderStatusPending,
			}
			require.Equal(t, want[state], result.Status)
			require.Equal(t, map[string]string{"pid": "1001", "currency": "CNY"}, result.Metadata)
		})
	}
}

// TestEPUSDTNotificationAuthenticatesFiatMoney checks an MD5-signed EPay callback against a TLS cashier.
// The verifier accepts the fiat amount only after observing that exact gateway trade as paid.
func TestEPUSDTNotificationAuthenticatesFiatMoney(t *testing.T) {
	server := epusdtPaidCashier(t)
	defer server.Close()
	adapter := newEPUSDTFixture(t, server)
	params := map[string]string{"pid": "1001", "out_trade_no": "tr_order", "trade_no": "gateway_trade", "money": "100.0000", "type": "alipay", "trade_status": "TRADE_SUCCESS", "sign_type": "MD5"}
	params["sign"] = easyPaySign(params, "merchant-secret")
	form := url.Values{}
	for key, value := range params {
		form.Set(key, value)
	}
	notification, err := adapter.VerifyNotification(context.Background(), form.Encode(), nil)
	require.NoError(t, err)
	require.Equal(t, 100.0, notification.Amount)
	require.Equal(t, "tr_order", notification.OrderID)
	require.Equal(t, "1001", notification.Metadata["pid"])
}

// TestEPUSDTNotificationRejectsAmbiguity submits invalid and contradictory signed EPay forms.
// Duplicate fields, merchant changes, delimiter tricks, and money differing from the paid cashier cannot authorize payment.
func TestEPUSDTNotificationRejectsAmbiguity(t *testing.T) {
	server := epusdtPaidCashier(t)
	defer server.Close()
	adapter := newEPUSDTFixture(t, server)
	for _, raw := range []string{"money=100&money=100", "unknown=", "money=%zz", `{"pid":"1001"}`} {
		_, err := adapter.VerifyNotification(context.Background(), raw, nil)
		require.Error(t, err, raw)
	}
	for _, change := range []map[string]string{
		{"pid": "another_merchant"},
		{"trade_status": "WAIT_BUYER_PAY"},
		{"money": "-100"},
		{"money": "101.00"},
		{"trade_no": "../other"},
		{"type": "wxpay"},
		{"name": "item&money=1000"},
		{"sign_type": "HMAC-SHA256"},
	} {
		params := map[string]string{"pid": "1001", "out_trade_no": "tr_order", "trade_no": "gateway_trade", "money": "100", "type": "alipay", "trade_status": "TRADE_SUCCESS", "sign_type": "MD5"}
		maps.Copy(params, change)
		params["sign"] = easyPaySign(params, "merchant-secret")
		form := url.Values{}
		for key, value := range params {
			form.Set(key, value)
		}
		_, err := adapter.VerifyNotification(context.Background(), form.Encode(), nil)
		require.Error(t, err)
	}
	params := url.Values{
		"pid": {"1001"}, "out_trade_no": {"tr_order"}, "trade_no": {"gateway_trade"},
		"money": {"100"}, "type": {"alipay"}, "trade_status": {"TRADE_SUCCESS"},
		"sign": {"00000000000000000000000000000000"},
	}
	_, err := adapter.VerifyNotification(context.Background(), params.Encode(), nil)
	require.ErrorContains(t, err, "signature")
}

// TestEPUSDTNotificationRejectsUnpaidCashier submits a correctly signed success callback for a waiting trade.
// An unconfirmed cashier state must prevent the signed message from authorizing local payment fulfillment.
func TestEPUSDTNotificationRejectsUnpaidCashier(t *testing.T) {
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Path, "/pay/checkout-counter-resp/") {
			_, _ = w.Write([]byte(`{"status_code":200,"data":{"trade_id":"gateway_trade","amount":100,"currency":"CNY"}}`))
			return
		}
		_, _ = w.Write([]byte(`{"status_code":200,"data":{"trade_id":"gateway_trade","status":1}}`))
	}))
	defer server.Close()
	adapter := newEPUSDTFixture(t, server)
	params := map[string]string{
		"pid": "1001", "out_trade_no": "tr_order", "trade_no": "gateway_trade",
		"money": "100", "type": "alipay", "trade_status": "TRADE_SUCCESS",
	}
	form := url.Values{}
	for key, value := range params {
		form.Set(key, value)
	}
	form.Set("sign", easyPaySign(params, "merchant-secret"))
	_, err := adapter.VerifyNotification(context.Background(), form.Encode(), nil)
	require.ErrorContains(t, err, "does not match a paid cashier order")
}

// epusdtPaidCashier serves the two public JSON observations used by paid EPay verification.
// The caller closes the returned TLS server after its adapter operations finish;
// production redirect and timeout policies remain enforced by newEPUSDTFixture.
func epusdtPaidCashier(t *testing.T) *httptest.Server {
	t.Helper()
	return httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/pay/checkout-counter-resp/gateway_trade":
			_, _ = w.Write([]byte(`{"status_code":200,"data":{"trade_id":"gateway_trade","amount":100,"actual_amount":14.29,"currency":"CNY"}}`))
		case "/pay/check-status/gateway_trade":
			_, _ = w.Write([]byte(`{"status_code":200,"data":{"trade_id":"gateway_trade","status":2}}`))
		default:
			t.Errorf("unexpected cashier path: %s", r.URL.Path)
			w.WriteHeader(http.StatusNotFound)
		}
	}))
}

// TestEPUSDTRejectsUnsafeConfiguration checks merchant secrets and endpoint transport requirements.
// The constructor refuses cleartext URLs and URL-carried credentials before any provider request can occur.
func TestEPUSDTRejectsUnsafeConfiguration(t *testing.T) {
	for _, base := range []string{
		"http://pay.example", "https://user:secret@pay.example",
		"https://pay.example?secret=value", "https://pay.example#fragment",
	} {
		_, err := NewEPUSDT("fixture", map[string]string{
			"apiBase": base, "pid": "1001", "secretKey": "merchant-secret",
			"notifyUrl": "https://site.example/notify",
		})
		require.Error(t, err)
	}
}

// newEPUSDTFixture constructs an adapter trusting only the supplied local TLS server.
// The production timeout and redirect policy remain attached to the fixture client.
func newEPUSDTFixture(t *testing.T, server *httptest.Server) *EPUSDT {
	t.Helper()
	adapter, err := NewEPUSDT("fixture", map[string]string{
		"apiBase": server.URL, "pid": "1001", "secretKey": "merchant-secret", "currency": "CNY",
		"notifyUrl": "https://site.example/api/v1/payment/webhook/epusdt",
	})
	require.NoError(t, err)
	adapter.httpClient.Transport = server.Client().Transport
	return adapter
}
