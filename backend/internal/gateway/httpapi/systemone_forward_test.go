package httpapi

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"

	"github.com/TokenFlux/TokenRouter/internal/apikey"
	"github.com/TokenFlux/TokenRouter/internal/billing/testkit"
	"github.com/TokenFlux/TokenRouter/internal/gateway/admission"
	"github.com/TokenFlux/TokenRouter/internal/gateway/forward"
	gatewayprovider "github.com/TokenFlux/TokenRouter/internal/gateway/provider"
	"github.com/TokenFlux/TokenRouter/internal/gateway/requeststate"
	"github.com/TokenFlux/TokenRouter/internal/protocol"
	"github.com/TokenFlux/TokenRouter/internal/protocol/systemone"
	"github.com/TokenFlux/TokenRouter/internal/provider"
	"github.com/TokenFlux/TokenRouter/internal/routing"
)

// TestSystemOneForwardPricingAndMapping 检查三种计费来源、零价放行和原始模型观测。
func TestSystemOneForwardPricingAndMapping(t *testing.T) {
	for _, source := range []string{routing.BillingModelSourceRequested, routing.BillingModelSourceGroupMapped, routing.BillingModelSourceUpstream} {
		for _, priced := range []bool{false, true} {
			t.Run(source+map[bool]string{true: "/priced", false: "/missing"}[priced], func(t *testing.T) {
				requested, mapped, actual := "customer-name", "group-name", "jev-latest"
				model := requested
				if source == routing.BillingModelSourceGroupMapped {
					model = mapped
				}
				if source == routing.BillingModelSourceUpstream {
					model = actual
				}
				var cards []routing.ModelPricingEntry
				zero := 0.0
				if priced {
					cards = []routing.ModelPricingEntry{{Models: []string{model}, InputPrice: &zero, OutputPrice: &zero}}
				}
				transport := &auxiliaryHTTPRecorder{resp: &http.Response{StatusCode: 200, Header: http.Header{}, Body: io.NopCloser(strings.NewReader(`{"model":"jev-1.13.0","answers":{"available":{"type":"noul","noul":0.8}},"usage":{"input_tokens":12,"output_tokens":4}}`))}}
				executor := &SystemOneExecutor{Transport: transport, Pricing: textPricingFixture(t, cards...)}
				value := gatewayprovider.NewExecutionProvider(&provider.Record{ID: 1, Platform: provider.PlatformJev, Type: provider.ProviderTypeAPIKey, Credentials: map[string]any{"api_key": "test-key", "model_mapping": map[string]any{mapped: actual}}})
				recorder := httptest.NewRecorder()
				c, _ := gin.CreateTestContext(recorder)
				c.Request = httptest.NewRequest(http.MethodPost, EndpointSystemOne, nil)
				key := &apikey.APIKey{GroupID: testkit.GroupID()}
				c.Set("gateway_effective_key", key)
				mapping := routing.GroupMappingResult{Mapped: true, MappedModel: mapped, BillingModelSource: source}
				plan := routing.Plan(routing.PlanInput{GroupID: key.GroupID, ClientProtocol: protocol.ProtocolSystemOne, RequestedModel: requested, GroupMapping: mapping})
				ctx := requeststate.WithRoutePlan(context.Background(), plan)
				c.Request = c.Request.WithContext(ctx)
				result, err := executor.Forward(ctx, c, value, systemone.ProbeBody(mapped, ""))
				if !priced {
					require.ErrorIs(t, err, admission.ErrModelPricingRejected)
					require.Nil(t, transport.lastReq)
					require.Equal(t, 400, recorder.Code)
					return
				}
				require.NoError(t, err)
				require.True(t, result.HasUsage)
				require.Equal(t, "https://api.typesafe.ai/v1/systemone", transport.lastReq.URL.String())
				require.Equal(t, "Bearer test-key", transport.lastReq.Header.Get("Authorization"))
				require.Contains(t, string(transport.lastBody), `"model":"jev-latest"`)
				require.Contains(t, recorder.Body.String(), `"model":"customer-name"`)
				require.Equal(t, "jev-1.13.0", result.UpstreamResponseModel)
			})
		}
	}
}

func TestSystemOneForwardFailurePolicy(t *testing.T) {
	for _, status := range []int{401, 422, 429, 529, 503} {
		transport := &auxiliaryHTTPRecorder{resp: &http.Response{StatusCode: status, Header: http.Header{"Retry-After": []string{"17"}}, Body: io.NopCloser(strings.NewReader(`{"error":"upstream failure"}`))}}
		zero := 0.0
		executor := &SystemOneExecutor{Transport: transport, Pricing: textPricingFixture(t, routing.ModelPricingEntry{Models: []string{"jev-latest"}, InputPrice: &zero, OutputPrice: &zero})}
		target := gatewayprovider.NewExecutionProvider(&provider.Record{ID: 1, Platform: provider.PlatformJev, Type: provider.ProviderTypeAPIKey, Credentials: map[string]any{"api_key": "test"}})
		c, recorder := gin.CreateTestContext(httptest.NewRecorder())
		c.Request = httptest.NewRequest(http.MethodPost, EndpointSystemOne, bytes.NewReader(nil))
		c.Set("gateway_effective_key", &apikey.APIKey{GroupID: testkit.GroupID()})
		result, err := executor.Forward(context.Background(), c, target, systemone.ProbeBody("jev-latest", ""))
		require.Error(t, err)
		require.False(t, result.Served)
		if status == 422 {
			require.Equal(t, 422, c.Writer.Status())
			require.True(t, c.Writer.Written())
		} else {
			var failure *forward.UpstreamFailoverError
			require.ErrorAs(t, err, &failure)
			require.Equal(t, "17", http.Header(failure.ResponseHeaders).Get("Retry-After"))
			require.False(t, c.Writer.Written())
		}
		_ = recorder
	}
}

// TestSystemOneRequiresPricing 检查装配缺价组件时在发送请求前失败。
func TestSystemOneRequiresPricing(t *testing.T) {
	transport := &auxiliaryHTTPRecorder{}
	for _, pricing := range []*admission.ModelPricing{nil, {}} {
		executor := &SystemOneExecutor{Transport: transport, Pricing: pricing}
		c, _ := gin.CreateTestContext(httptest.NewRecorder())
		c.Request = httptest.NewRequest(http.MethodPost, EndpointSystemOne, nil)
		_, err := executor.Forward(context.Background(), c, nil, nil)
		require.Error(t, err)
		require.Equal(t, http.StatusServiceUnavailable, c.Writer.Status())
		require.Nil(t, transport.lastReq)
	}
}
