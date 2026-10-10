package app

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"

	"github.com/TokenFlux/TokenRouter/internal/app/lifecycle"
	"github.com/TokenFlux/TokenRouter/internal/billing/pricing"
	"github.com/TokenFlux/TokenRouter/internal/billing/testkit"
	"github.com/TokenFlux/TokenRouter/internal/config"
	"github.com/TokenFlux/TokenRouter/internal/egress"
	gatewayprovider "github.com/TokenFlux/TokenRouter/internal/gateway/provider"
	"github.com/TokenFlux/TokenRouter/internal/infra/httpclient"
	"github.com/TokenFlux/TokenRouter/internal/protocol/systemone"
	"github.com/TokenFlux/TokenRouter/internal/provider"
)

type systemOneTransportStub struct {
	httpclient.UpstreamTransport
	calls   int
	request *http.Request
}

func (s *systemOneTransportStub) Do(request *http.Request, _ string, _ int64, _ int) (*http.Response, error) {
	s.calls++
	s.request = request
	return &http.Response{StatusCode: http.StatusOK, Header: http.Header{"X-Test": []string{"visible"}, "X-Secret": []string{"hidden"}}, Body: io.NopCloser(strings.NewReader(`{"model":"jev-1.13.0","answers":{"available":{"type":"noul","noul":1}},"usage":{"input_tokens":1,"output_tokens":0}}`))}, nil
}

// TestProvideSystemOneExecutor 检查独立装配后的决策执行、响应头过滤、地址限制和关闭屏障。
func TestProvideSystemOneExecutor(t *testing.T) {
	cfg := &config.Config{}
	cfg.Gateway.UpstreamResponseReadMaxBytes = 1024
	cfg.Security.URLAllowlist.Enabled = true
	cfg.Security.URLAllowlist.UpstreamHosts = []string{"api.typesafe.ai"}
	transport := &systemOneTransportStub{}
	prices := testkit.ResolverWithCards(t, testkit.Calculator(nil, map[string]*pricing.ModelPricing{"jev-latest": {InputPricePerToken: 4.2e-8}}), nil)
	headers := egress.CompileHeaderFilter(egress.ResponseHeaderOptions{Enabled: true, AdditionalAllowed: []string{"X-Test"}, ForceRemove: []string{"X-Secret"}})
	activity := &gatewayRequestActivity{Operations: lifecycle.NewOperations("SystemOneTest")}
	executor := provideSystemOneExecutor(cfg, transport, headers, nil, prices, activity)
	cfg.Security.URLAllowlist.UpstreamHosts[0] = "changed.example"
	target := gatewayprovider.NewExecutionProvider(&provider.Record{ID: 9, Platform: provider.PlatformJev, Type: provider.ProviderTypeAPIKey, Credentials: map[string]any{"api_key": "test-key"}})
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/systemone", nil)
	result, err := executor.Forward(context.Background(), c, target, systemone.ProbeBody("jev-latest", ""))
	require.NoError(t, err)
	require.True(t, result.HasUsage)
	require.Equal(t, "https://api.typesafe.ai/v1/systemone", transport.request.URL.String())
	require.Equal(t, "Bearer test-key", transport.request.Header.Get("Authorization"))
	require.Equal(t, "visible", c.Writer.Header().Get("X-Test"))
	require.Empty(t, c.Writer.Header().Get("X-Secret"))
	target.Record.Credentials["base_url"] = "https://blocked.example"
	_, err = executor.Forward(context.Background(), c, target, systemone.ProbeBody("jev-latest", ""))
	require.Error(t, err)
	require.Equal(t, 1, transport.calls)
	target.Record.Credentials["base_url"] = "https://api.typesafe.ai"
	require.NoError(t, activity.StopContext(context.Background()))
	_, err = executor.Forward(context.Background(), c, target, systemone.ProbeBody("jev-latest", ""))
	require.Error(t, err)
	require.Equal(t, 1, transport.calls)
}
