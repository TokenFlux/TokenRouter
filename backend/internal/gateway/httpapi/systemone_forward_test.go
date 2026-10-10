package httpapi

import (
	"bytes"
	"compress/gzip"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"

	"github.com/TokenFlux/TokenRouter/internal/apikey"
	"github.com/TokenFlux/TokenRouter/internal/billing/testkit"
	"github.com/TokenFlux/TokenRouter/internal/gateway/admission"
	"github.com/TokenFlux/TokenRouter/internal/gateway/errorpolicy"
	"github.com/TokenFlux/TokenRouter/internal/gateway/forward"
	gatewayprovider "github.com/TokenFlux/TokenRouter/internal/gateway/provider"
	"github.com/TokenFlux/TokenRouter/internal/gateway/requeststate"
	gatewaytestkit "github.com/TokenFlux/TokenRouter/internal/gateway/testkit"
	"github.com/TokenFlux/TokenRouter/internal/protocol"
	"github.com/TokenFlux/TokenRouter/internal/protocol/systemone"
	"github.com/TokenFlux/TokenRouter/internal/provider"
	"github.com/TokenFlux/TokenRouter/internal/routing"
	"github.com/TokenFlux/TokenRouter/internal/upstream/jev"
)

// TestSystemOneErrorRepresentation 检查改写后的 JSON 头和未改写报文的透传头。
func TestSystemOneErrorRepresentation(t *testing.T) {
	for _, contentType := range []string{"text/plain; charset=utf-8", "text/html; charset=utf-8", "application/problem+json", "application/json"} {
		for _, encoded := range []bool{false, true} {
			for _, matched := range []bool{false, true} {
				t.Run(contentType+map[bool]string{false: "/plain", true: "/gzip"}[encoded]+map[bool]string{false: "/passthrough", true: "/rewrite"}[matched], func(t *testing.T) {
					body := []byte(`{"error":{"message":"invalid schema"}}`)
					headers := http.Header{"Content-Type": []string{contentType}, "X-Request-Id": []string{"upstream-id"}, "Retry-After": []string{"17"}}
					if encoded {
						var compressed bytes.Buffer
						writer := gzip.NewWriter(&compressed)
						_, err := writer.Write(body)
						require.NoError(t, err)
						require.NoError(t, writer.Close())
						body = compressed.Bytes()
						headers.Set("Content-Encoding", "gzip")
					}
					recorder := httptest.NewRecorder()
					c, _ := gin.CreateTestContext(recorder)
					if matched {
						rule := gatewaytestkit.NonFailoverRule(422, "", 409, "Configured Jev error")
						rule.Keywords = nil
						rule.Platforms = []string{"jev"}
						rules := gatewaytestkit.ErrorRules([]*errorpolicy.ErrorPassthroughRule{rule})
						defer rules.Stop()
						BindErrorPassthroughService(c, rules)
					}
					(&SystemOneExecutor{}).writeUpstreamError(c, &jev.HTTPError{Status: 422, Header: headers, Body: body})
					require.Equal(t, "upstream-id", recorder.Header().Get("X-Request-Id"))
					require.Equal(t, "17", recorder.Header().Get("Retry-After"))
					if matched {
						require.Equal(t, 409, recorder.Code)
						require.Equal(t, "application/json; charset=utf-8", recorder.Header().Get("Content-Type"))
						require.Empty(t, recorder.Header().Get("Content-Encoding"))
						require.JSONEq(t, `{"error":{"type":"upstream_error","message":"Configured Jev error"}}`, recorder.Body.String())
					} else {
						require.Equal(t, 422, recorder.Code)
						require.Equal(t, contentType, recorder.Header().Get("Content-Type"))
						require.Equal(t, headers.Get("Content-Encoding"), recorder.Header().Get("Content-Encoding"))
						require.Equal(t, body, recorder.Body.Bytes())
					}
				})
			}
		}
	}
}

// TestSystemOneTerminalErrorRules 检查终止请求的错误使用 Jev 或全平台规则。
func TestSystemOneTerminalErrorRules(t *testing.T) {
	for _, sourceStatus := range []int{400, 422} {
		for _, platform := range []string{"jev", "openai", ""} {
			t.Run(http.StatusText(sourceStatus)+"/"+platform, func(t *testing.T) {
				body := `{"error":{"message":"invalid schema"}}`
				transport := &auxiliaryHTTPRecorder{resp: &http.Response{StatusCode: sourceStatus, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(body))}}
				zero := 0.0
				executor := &SystemOneExecutor{Transport: transport, Pricing: textPricingFixture(t, routing.ModelPricingEntry{Models: []string{"jev-latest"}, InputPrice: &zero, OutputPrice: &zero})}
				recorder := httptest.NewRecorder()
				c, _ := gin.CreateTestContext(recorder)
				c.Request = httptest.NewRequest(http.MethodPost, EndpointSystemOne, nil)
				c.Set("gateway_effective_key", &apikey.APIKey{GroupID: testkit.GroupID()})
				rule := gatewaytestkit.NonFailoverRule(sourceStatus, "invalid schema", 409, "Configured Jev error")
				if platform != "" {
					rule.Platforms = []string{platform}
				}
				rule.SkipMonitoring = true
				rules := gatewaytestkit.ErrorRules([]*errorpolicy.ErrorPassthroughRule{rule})
				defer rules.Stop()
				BindErrorPassthroughService(c, rules)
				target := gatewayprovider.NewExecutionProvider(&provider.Record{ID: 1, Platform: provider.PlatformJev, Type: provider.ProviderTypeAPIKey, Credentials: map[string]any{"api_key": "test"}})
				_, err := executor.Forward(c.Request.Context(), c, target, systemone.ProbeBody("jev-latest", ""))
				require.Error(t, err)
				var retry *forward.UpstreamFailoverError
				require.NotErrorAs(t, err, &retry)
				if platform == "openai" {
					require.Equal(t, sourceStatus, recorder.Code)
					require.JSONEq(t, body, recorder.Body.String())
					require.False(t, c.GetBool(OpsSkipPassthroughKey))
				} else {
					require.Equal(t, 409, recorder.Code)
					require.Contains(t, recorder.Body.String(), "Configured Jev error")
					require.True(t, c.GetBool(OpsSkipPassthroughKey))
				}
			})
		}
	}
}

// TestSystemOnePoolErrorPolicy 检查池重试资格、Retry-After 和自定义错误码优先级。
func TestSystemOnePoolErrorPolicy(t *testing.T) {
	for _, tc := range []struct {
		name            string
		status          int
		pool            bool
		retryCodes      []int
		customCodes     []int
		wantRetry, same bool
		wantStatus      int
	}{
		{name: "pool retry", status: 503, pool: true, retryCodes: []int{503}, wantRetry: true, same: true},
		{name: "pool code excluded", status: 503, pool: true, retryCodes: []int{429}, wantRetry: true},
		{name: "pool additional code", status: 400, pool: true, retryCodes: []int{400}, wantRetry: true, same: true},
		{name: "parameter error", status: 422, pool: true, retryCodes: []int{422}, wantStatus: 422},
		{name: "ordinary provider", status: 503, wantRetry: true},
		{name: "custom matched", status: 503, pool: true, retryCodes: []int{503}, customCodes: []int{503}, wantRetry: true},
		{name: "custom skipped", status: 503, pool: true, retryCodes: []int{503}, customCodes: []int{401}, wantStatus: 500},
	} {
		t.Run(tc.name, func(t *testing.T) {
			transport := &auxiliaryHTTPRecorder{resp: &http.Response{StatusCode: tc.status, Header: http.Header{"Retry-After": []string{"3"}}, Body: io.NopCloser(strings.NewReader(`{"error":{"message":"busy"}}`))}}
			zero := 0.0
			executor := &SystemOneExecutor{Transport: transport, Pricing: textPricingFixture(t, routing.ModelPricingEntry{Models: []string{"jev-latest"}, InputPrice: &zero, OutputPrice: &zero})}
			credentials := map[string]any{"api_key": "test", "pool_mode": tc.pool, "pool_mode_retry_status_codes": tc.retryCodes}
			if tc.customCodes != nil {
				credentials["custom_error_codes_enabled"] = true
				credentials["custom_error_codes"] = tc.customCodes
			}
			encoded, err := json.Marshal(credentials)
			require.NoError(t, err)
			require.NoError(t, json.Unmarshal(encoded, &credentials))
			target := gatewayprovider.NewExecutionProvider(&provider.Record{ID: 1, Platform: provider.PlatformJev, Type: provider.ProviderTypeAPIKey, Credentials: credentials})
			recorder := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(recorder)
			c.Request = httptest.NewRequest(http.MethodPost, EndpointSystemOne, nil)
			c.Set("gateway_effective_key", &apikey.APIKey{GroupID: testkit.GroupID()})
			_, err = executor.Forward(c.Request.Context(), c, target, systemone.ProbeBody("jev-latest", ""))
			require.Error(t, err)
			var retry *forward.UpstreamFailoverError
			if tc.wantRetry {
				require.ErrorAs(t, err, &retry)
				require.Equal(t, tc.same, retry.RetryableOnSameProvider)
				if tc.same {
					require.InDelta(t, float64(3*time.Second), float64(retry.SameProviderRetryDelay), float64(100*time.Millisecond))
				}
				require.False(t, c.Writer.Written())
			} else {
				require.NotErrorAs(t, err, &retry)
				require.Equal(t, tc.wantStatus, recorder.Code)
			}
		})
	}
}

// TestSystemOneForwardResponseModel 检查复合前缀、各级别名和上游版本名分别用于响应与观测。
func TestSystemOneForwardResponseModel(t *testing.T) {
	for _, tc := range []struct {
		name, requested, groupMapped, composite, keyAlias, want string
	}{
		{name: "direct", requested: "jev-latest", want: "jev-1.13.0"},
		{name: "provider mapping", requested: "provider-alias", want: "provider-alias"},
		{name: "group mapping", requested: "group-alias", groupMapped: "jev-latest", want: "group-alias"},
		{name: "key mapping", requested: "jev-latest", keyAlias: "key-alias", want: "key-alias"},
		{name: "composite", requested: "jev-latest", composite: "TS/jev-latest", want: "TS/jev-latest"},
		{name: "composite provider mapping", requested: "provider-alias", composite: "TS/provider-alias", want: "TS/provider-alias"},
		{name: "composite group mapping", requested: "group-alias", groupMapped: "jev-latest", composite: "TS/group-alias", want: "TS/group-alias"},
		{name: "composite key mapping", requested: "jev-latest", keyAlias: "key-alias", composite: "TS/key-alias", want: "TS/key-alias"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			transport := &auxiliaryHTTPRecorder{resp: &http.Response{StatusCode: 200, Header: http.Header{}, Body: io.NopCloser(strings.NewReader(`{"model":"jev-1.13.0","answers":{"available":{"type":"noul","noul":0.8}},"usage":{"input_tokens":12,"output_tokens":4}}`))}}
			zero := 0.0
			executor := &SystemOneExecutor{Transport: transport, Pricing: textPricingFixture(t, routing.ModelPricingEntry{Models: []string{"jev-latest"}, InputPrice: &zero, OutputPrice: &zero})}
			target := gatewayprovider.NewExecutionProvider(&provider.Record{ID: 1, Platform: provider.PlatformJev, Type: provider.ProviderTypeAPIKey, Credentials: map[string]any{"api_key": "test-key", "model_mapping": map[string]any{"provider-alias": "jev-latest"}}})
			recorder := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(recorder)
			c.Request = httptest.NewRequest(http.MethodPost, EndpointSystemOne, nil)
			key := &apikey.APIKey{GroupID: testkit.GroupID()}
			c.Set("gateway_effective_key", key)
			if tc.composite != "" {
				actual := tc.requested
				if tc.keyAlias != "" {
					actual = tc.keyAlias
				}
				SetCompositeModelContext(c, tc.composite, actual)
			}
			if tc.keyAlias != "" {
				SetAPIKeyModelRedirectContext(c, tc.keyAlias, tc.requested)
			}
			mapped := tc.requested
			if tc.groupMapped != "" {
				mapped = tc.groupMapped
			}
			mapping := routing.GroupMappingResult{Mapped: mapped != tc.requested, MappedModel: mapped, BillingModelSource: routing.BillingModelSourceUpstream}
			plan := routing.Plan(routing.PlanInput{GroupID: key.GroupID, ClientProtocol: protocol.ProtocolSystemOne, RequestedModel: tc.requested, GroupMapping: mapping})
			ctx := requeststate.WithRoutePlan(c.Request.Context(), plan)
			c.Request = c.Request.WithContext(ctx)
			result, err := executor.Forward(ctx, c, target, systemone.ProbeBody(mapped, ""))
			require.NoError(t, err)
			require.True(t, result.HasUsage)
			require.Equal(t, "jev-latest", result.UpstreamModel)
			require.Equal(t, "jev-1.13.0", result.UpstreamResponseModel)
			var sent, received struct{ Model string }
			require.NoError(t, json.Unmarshal(transport.lastBody, &sent))
			require.NoError(t, json.Unmarshal(recorder.Body.Bytes(), &received))
			require.Equal(t, "jev-latest", sent.Model)
			require.Equal(t, tc.want, received.Model)
		})
	}
}

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
