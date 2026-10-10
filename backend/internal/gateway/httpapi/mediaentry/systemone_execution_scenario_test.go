package mediaentry

// 本文件覆盖 systemone.go、httpapi/systemone_forward.go 和 gateway/systemone/execute.go 的池重试。

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"

	"github.com/TokenFlux/TokenRouter/internal/apikey"
	keyhttp "github.com/TokenFlux/TokenRouter/internal/apikey/httpapi"
	"github.com/TokenFlux/TokenRouter/internal/billing"
	"github.com/TokenFlux/TokenRouter/internal/billing/pricing"
	"github.com/TokenFlux/TokenRouter/internal/billing/testkit"
	"github.com/TokenFlux/TokenRouter/internal/gateway/admission"
	gatewayhttp "github.com/TokenFlux/TokenRouter/internal/gateway/httpapi"
	gatewayprovider "github.com/TokenFlux/TokenRouter/internal/gateway/provider"
	"github.com/TokenFlux/TokenRouter/internal/gateway/systemone"
	"github.com/TokenFlux/TokenRouter/internal/infra/httpclient"
	"github.com/TokenFlux/TokenRouter/internal/provider"
	"github.com/TokenFlux/TokenRouter/internal/upstream"
)

type systemOnePoolTransport struct {
	httpclient.UpstreamTransport
	calls int
}

type systemOnePoolRun struct {
	*systemOneRun
	completed, released int
}

// Do 模拟池内首个账号失败、下一次请求成功。
func (p *systemOnePoolTransport) Do(*http.Request, string, int64, int) (*http.Response, error) {
	p.calls++
	status := http.StatusOK
	body := `{"model":"jev-1.13.0","answers":{"available":{"type":"noul","noul":1}},"usage":{"input_tokens":1,"output_tokens":0}}`
	if p.calls == 1 {
		status = http.StatusServiceUnavailable
		body = `{"error":{"message":"busy"}}`
	}
	return &http.Response{StatusCode: status, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(body))}, nil
}

func (p *systemOnePoolRun) Select(context.Context, map[int64]struct{}) (provider.ProviderSnapshot, error) {
	return p.selection.Provider.Record.RoutingSnapshot(), nil
}

func (p *systemOnePoolRun) Acquire(context.Context) (func(), bool) {
	return func() { p.released++ }, true
}

func (p *systemOnePoolRun) Report(context.Context, systemone.Outcome) {}

func (p *systemOnePoolRun) Complete(context.Context, upstream.AttemptResult) { p.completed++ }

// TestSystemOnePoolRetryExecution 覆盖 HTTP 错误分类、适配器预算和编排之间的连接。
func TestSystemOnePoolRetryExecution(t *testing.T) {
	transport := &systemOnePoolTransport{}
	prices := testkit.ResolverWithCards(t, testkit.Calculator(nil, map[string]*pricing.ModelPricing{"jev-latest": {InputPricePerToken: 4.2e-8}}), nil)
	executor := &gatewayhttp.SystemOneExecutor{Transport: transport, Pricing: &admission.ModelPricing{Resolver: prices}}
	runtime := New(Bindings{CheckFunding: func(context.Context, *apikey.APIKey, *billing.UserSubscription, string, bool) error { return nil }, Platform: PlatformPorts{SystemOne: executor.Forward}})
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodPost, gatewayhttp.EndpointSystemOne, nil)
	key := &apikey.APIKey{ID: 1, User: &apikey.User{ID: 1}}
	c.Set(string(keyhttp.ContextKeyAPIKey), key)
	selected := gatewayprovider.NewExecutionProvider(&provider.Record{ID: 1, Platform: provider.PlatformJev, Type: provider.ProviderTypeAPIKey, Credentials: map[string]any{"api_key": "test", "pool_mode": true, "pool_mode_retry_count": 2, "pool_mode_retry_status_codes": []any{503}}})
	run := &systemOnePoolRun{systemOneRun: &systemOneRun{h: runtime, c: c, key: key, input: gatewayhttp.AuxiliaryHTTPInput{Model: "jev-latest", Body: []byte(`{"model":"jev-latest","state":"available","questions":{"available":{"type":"noul","instructions":"Available?"}}}`)}, selection: &gatewayprovider.SelectionResult{Provider: selected}}}
	require.NoError(t, systemone.Run(c.Request.Context(), 0, run))
	require.Equal(t, 2, transport.calls)
	require.Equal(t, 1, run.completed)
	require.Equal(t, 1, run.released)
	require.Equal(t, 200, recorder.Code)
	require.Contains(t, recorder.Body.String(), `"model":"jev-1.13.0"`)
}
