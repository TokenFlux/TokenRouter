package provider

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"

	"github.com/TokenFlux/TokenRouter/internal/infra/httpclient"
	"github.com/TokenFlux/TokenRouter/internal/upstream/deepseek"
	"github.com/TokenFlux/TokenRouter/internal/upstream/kimi"
	"github.com/TokenFlux/TokenRouter/internal/upstream/usagecontract"
	"github.com/TokenFlux/TokenRouter/internal/upstream/usageprovider"
	"github.com/TokenFlux/TokenRouter/internal/upstream/zhipu"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// 包装真实本地 TLS 响应，核对查询 Adapter 持有的每个响应体都已释放。
type usageTrackedBody struct {
	io.ReadCloser
	closed *atomic.Int64
}

func (b *usageTrackedBody) Close() error { b.closed.Add(1); return b.ReadCloser.Close() }

// 通过本地 TLS 完成七种原生适配器的实际请求，保留固定端点、认证覆盖顺序和计量口径。
func TestNativeUsageAdaptersLocalTLS(t *testing.T) {
	tests := []struct {
		name                string
		adapter             usagecontract.Adapter
		base                string
		paths, bodies, auth []string
		mode                string
	}{
		{"kimi coding", &kimi.KimiCodingUsageAdapter{}, "/coding/v1", []string{"/coding/v1/usages"}, []string{`{"limits":[{"detail":{"limit":100,"remaining":25}}],"usage":{"limit":1000,"remaining":800}}`}, []string{"Bearer key-secret"}, "limits"},
		{"kimi balance", &kimi.KimiBalanceUsageAdapter{}, "/v1", []string{"/v1/users/me/balance"}, []string{`{"code":0,"data":{"available_balance":"6.25"}}`}, []string{"Bearer key-secret"}, "balance"},
		{"zhipu coding", &zhipu.ZhipuCodingUsageAdapter{}, "/api/coding/paas/v4", []string{"/api/monitor/usage/quota/limit"}, []string{`{"success":true,"data":{"limits":[{"type":"TOKENS_LIMIT","unit":3,"percentage":32}]}}`}, []string{"key-secret"}, "limits"},
		{"deepseek", &deepseek.DeepseekBalanceUsageAdapter{}, "/anthropic", []string{"/user/balance"}, []string{`{"is_available":true,"balance_infos":[{"currency":"CNY","total_balance":"3.5"},{"currency":"USD","total_balance":"1.25"}]}`}, []string{"Bearer key-secret"}, "balance"},
		{"sub2api", &usageprovider.Sub2APIUsageAdapter{}, "/v1", []string{"/v1/usage"}, []string{`{"isValid":true,"mode":"unrestricted","unit":"USD","planName":"payg","remaining":12.5,"balance":12.5}`}, []string{"Bearer key-secret"}, "balance"},
		{"zivv", &usageprovider.ZivvUsageAdapter{}, "/v1", []string{"/v1/user/balance"}, []string{`{"balance":1,"currency":"USD","is_available":true,"key_limit":0,"key_used":0,"total_used":2}`}, []string{"Bearer key-secret"}, "balance"},
		{"newapi", &usageprovider.NewAPIUsageAdapter{}, "/v1", []string{"/api/status", "/api/usage/token/", "/user/balance"}, []string{`{"success":true,"data":{"quota_display_type":"USD","quota_per_unit":500000}}`, `{"code":true,"data":{"object":"token_usage","name":"token","total_granted":632500000,"total_used":360000,"total_available":632140000,"unlimited_quota":false,"expires_at":1893456000}}`, `{"balance_infos":[{"currency":"USD","total_balance":"1264.28"}]}`}, []string{"", "Bearer key-secret", "Bearer key-secret"}, "balance"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var calls, closed atomic.Int64
			server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				i := int(calls.Add(1) - 1)
				if i >= len(tt.paths) {
					t.Errorf("unexpected request %s", r.URL.Path)
					w.WriteHeader(500)
					return
				}
				assert.Equal(t, tt.paths[i], r.URL.Path)
				assert.Equal(t, tt.auth[i], r.Header.Get("Authorization"))
				assert.Equal(t, "query", r.Header.Get("X-Custom"))
				w.Header().Set("Content-Type", "application/json")
				_, _ = io.WriteString(w, tt.bodies[i])
			}))
			defer server.Close()
			input := &usagecontract.Request{BaseURL: server.URL + tt.base, APIKey: "key-secret", Context: func(ctx context.Context) context.Context { return ctx }, Endpoint: httpclient.BuildOpenAIEndpointURL, ApplyHeaders: func(h http.Header) { h.Set("Authorization", "wrong-secret"); h.Set("X-Custom", "query") }, Do: func(r *http.Request) (*http.Response, error) {
				resp, err := server.Client().Do(r)
				if resp != nil {
					resp.Body = &usageTrackedBody{resp.Body, &closed}
				}
				return resp, err
			}}
			result, err := tt.adapter.Query(context.Background(), input)
			require.NoError(t, err)
			require.NotNil(t, result)
			require.Equal(t, tt.mode, result.Mode)
			require.EqualValues(t, len(tt.paths), calls.Load())
			require.Equal(t, calls.Load(), closed.Load())
			payload, err := json.Marshal(result)
			require.NoError(t, err)
			require.NotContains(t, string(payload), "key-secret")
			require.NotContains(t, string(payload), "wrong-secret")
			if tt.name == "kimi coding" {
				require.InDelta(t, 75, *result.Limits[0].Used, 1e-9)
			}
			if tt.name == "deepseek" {
				require.Len(t, result.Balances, 2)
			}
		})
	}
}
