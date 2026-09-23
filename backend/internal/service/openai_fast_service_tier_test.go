package service

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	time "time"

	accountcore "github.com/TokenFlux/TokenRouter/internal/account"
	billingcore "github.com/TokenFlux/TokenRouter/internal/billing"
	billingpricing "github.com/TokenFlux/TokenRouter/internal/billing/pricing"
	"github.com/TokenFlux/TokenRouter/internal/config"
	"github.com/TokenFlux/TokenRouter/internal/gateway"
	gatewayprovider "github.com/TokenFlux/TokenRouter/internal/gateway/provider"
	"github.com/TokenFlux/TokenRouter/internal/gateway/tierpolicy"
	"github.com/TokenFlux/TokenRouter/internal/routing/capability"
	"github.com/TokenFlux/TokenRouter/internal/upstream/anthropic"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

// ---------------------------------------------------------------------------
// 请求侧：service_tier 校验（fast/priority 等价、非法值拒绝、省略保持现状）
// ---------------------------------------------------------------------------

// ---------------------------------------------------------------------------
// 计费：gpt-5.6 系列 / gpt-5.4 按标准价 2x，gpt-5.5 按标准价 2.5x
// ---------------------------------------------------------------------------

func TestApplyModelSpecificPricingPolicy_EnforcesOpenAIFastRatios(t *testing.T) {
	t.Parallel()

	svc := newBillingServiceWithPrices(nil, nil, map[string]*billingpricing.ModelPricing{})

	t.Run("gpt-5.5 catalog 2x priority is corrected to 2.5x", func(t *testing.T) {
		// 模拟本地 LiteLLM 目录仍携带官方旧口径（gpt-5.5 priority = 2x）。
		catalog := &billingpricing.ModelPricing{
			InputPricePerToken:             5e-6,
			InputPricePerTokenPriority:     10e-6,
			OutputPricePerToken:            30e-6,
			OutputPricePerTokenPriority:    60e-6,
			CacheReadPricePerToken:         0.5e-6,
			CacheReadPricePerTokenPriority: 1e-6,
		}
		got := svc.ApplyModelSpecificPricingPolicy("gpt-5.5", catalog)
		require.InDelta(t, 12.5e-6, got.InputPricePerTokenPriority, 1e-12)
		require.InDelta(t, 75e-6, got.OutputPricePerTokenPriority, 1e-12)
		require.InDelta(t, 1.25e-6, got.CacheReadPricePerTokenPriority, 1e-12)
		// 标准价不被改动。
		require.InDelta(t, 5e-6, got.InputPricePerToken, 1e-12)
		// 原始指针不被污染。
		require.InDelta(t, 10e-6, catalog.InputPricePerTokenPriority, 1e-12)
	})

	t.Run("gpt-5.4 keeps 2x", func(t *testing.T) {
		got := svc.ApplyModelSpecificPricingPolicy("gpt-5.4", &billingpricing.ModelPricing{
			InputPricePerToken:          2.5e-6,
			InputPricePerTokenPriority:  5e-6,
			OutputPricePerToken:         15e-6,
			OutputPricePerTokenPriority: 30e-6,
		})
		require.InDelta(t, 5e-6, got.InputPricePerTokenPriority, 1e-12)
		require.InDelta(t, 30e-6, got.OutputPricePerTokenPriority, 1e-12)
	})

	t.Run("gpt-5.6 family keeps 2x", func(t *testing.T) {
		for _, model := range []string{"gpt-5.6", "gpt-5.6-sol", "gpt-5.6-terra", "gpt-5.6-luna", "gpt-5.6-max", "gpt-5.6-sol-preview"} {
			got := svc.ApplyModelSpecificPricingPolicy(model, &billingpricing.ModelPricing{
				InputPricePerToken:             5e-6,
				InputPricePerTokenPriority:     10e-6,
				OutputPricePerToken:            30e-6,
				OutputPricePerTokenPriority:    60e-6,
				CacheReadPricePerToken:         0.5e-6,
				CacheReadPricePerTokenPriority: 1e-6,
			})
			require.InDelta(t, 10e-6, got.InputPricePerTokenPriority, 1e-12, "model %s", model)
			require.InDelta(t, 60e-6, got.OutputPricePerTokenPriority, 1e-12, "model %s", model)
		}
	})

	t.Run("missing priority prices are backfilled from standard", func(t *testing.T) {
		got := svc.ApplyModelSpecificPricingPolicy("gpt-5.5", &billingpricing.ModelPricing{
			InputPricePerToken:         5e-6,
			OutputPricePerToken:        30e-6,
			CacheReadPricePerToken:     0.5e-6,
			CacheCreationPricePerToken: 5e-6,
		})
		require.InDelta(t, 12.5e-6, got.InputPricePerTokenPriority, 1e-12)
		require.InDelta(t, 75e-6, got.OutputPricePerTokenPriority, 1e-12)
		require.InDelta(t, 1.25e-6, got.CacheReadPricePerTokenPriority, 1e-12)
		require.InDelta(t, 12.5e-6, got.CacheCreationPricePerTokenPriority, 1e-12)
	})

	t.Run("gpt-5.5-pro has no mandated fast tier", func(t *testing.T) {
		got := svc.ApplyModelSpecificPricingPolicy("gpt-5.5-pro", &billingpricing.ModelPricing{
			InputPricePerToken:         30e-6,
			InputPricePerTokenPriority: 60e-6,
			OutputPricePerToken:        180e-6,
		})
		require.InDelta(t, 60e-6, got.InputPricePerTokenPriority, 1e-12)
	})

	t.Run("unrelated models untouched", func(t *testing.T) {
		got := svc.ApplyModelSpecificPricingPolicy("claude-opus-5", &billingpricing.ModelPricing{InputPricePerToken: 1, OutputPricePerToken: 2})
		require.InDelta(t, 1, got.InputPricePerToken, 1e-12)
		require.Zero(t, got.InputPricePerTokenPriority)
	})
}

func TestOpenAIFastBillingMultiplier_2xAnd25x(t *testing.T) {
	t.Parallel()

	// 目录数据携带官方旧口径（gpt-5.5 priority=2x）；修正后 fast 必须按 2.5x 计费。
	catalog := map[string]*billingpricing.LiteLLMModelPricing{
		"gpt-5.4": {
			InputCostPerToken:               2.5e-6,
			InputCostPerTokenPriority:       5e-6,
			OutputCostPerToken:              15e-6,
			OutputCostPerTokenPriority:      30e-6,
			CacheReadInputTokenCost:         0.25e-6,
			CacheReadInputTokenCostPriority: 0.5e-6,
		},
		"gpt-5.5": {
			InputCostPerToken:               5e-6,
			InputCostPerTokenPriority:       10e-6,
			OutputCostPerToken:              30e-6,
			OutputCostPerTokenPriority:      60e-6,
			CacheReadInputTokenCost:         0.5e-6,
			CacheReadInputTokenCostPriority: 1e-6,
		},
		"gpt-5.6-sol": {
			InputCostPerToken:               5e-6,
			InputCostPerTokenPriority:       10e-6,
			OutputCostPerToken:              30e-6,
			OutputCostPerTokenPriority:      60e-6,
			CacheReadInputTokenCost:         0.5e-6,
			CacheReadInputTokenCostPriority: 1e-6,
		},
	}
	billing := NewBillingService(&config.Config{}, newPricingServiceFixture(pricingServiceFixture{pricingData: catalog}))
	tokens := billingpricing.UsageTokens{InputTokens: 1_000_000, OutputTokens: 1_000_000}

	standard := func(model string) *billingpricing.CostBreakdown {
		cost, err := billing.CalculateCost(model, tokens, 1)
		require.NoError(t, err)
		return cost
	}
	fast := func(model, tier string) *billingpricing.CostBreakdown {
		cost, err := billing.CalculateCostWithServiceTier(model, tokens, 1, tier)
		require.NoError(t, err)
		return cost
	}

	tests := []struct {
		model string
		ratio float64
	}{
		{model: "gpt-5.4", ratio: 2.0},
		{model: "gpt-5.5", ratio: 2.5},
		{model: "gpt-5.6-sol", ratio: 2.0},
		{model: "gpt-5.6-terra", ratio: 2.0},
		{model: "gpt-5.6-luna", ratio: 2.0},
	}
	for _, tt := range tests {
		t.Run(tt.model+"/fast", func(t *testing.T) {
			base := standard(tt.model)
			fastCost := fast(tt.model, "fast")
			require.InDelta(t, base.TotalCost*tt.ratio, fastCost.TotalCost, 1e-9,
				"fast total must be %.1fx standard", tt.ratio)
		})
		t.Run(tt.model+"/priority_alias", func(t *testing.T) {
			fastCost := fast(tt.model, "fast")
			priorityCost := fast(tt.model, "priority")
			require.InDelta(t, fastCost.TotalCost, priorityCost.TotalCost, 1e-12,
				"client alias fast must bill identically to priority")
			require.InDelta(t, standard(tt.model).TotalCost*tt.ratio, priorityCost.TotalCost, 1e-9)
		})
		t.Run(tt.model+"/no_tier_unchanged", func(t *testing.T) {
			base := standard(tt.model)
			noTier, err := billing.CalculateCostWithServiceTier(tt.model, tokens, 1, "")
			require.NoError(t, err)
			require.InDelta(t, base.TotalCost, noTier.TotalCost, 1e-12)
		})
		t.Run(tt.model+"/default_equals_standard", func(t *testing.T) {
			base := standard(tt.model)
			defaultCost, err := billing.CalculateCostWithServiceTier(tt.model, tokens, 1, "default")
			require.NoError(t, err)
			require.InDelta(t, base.TotalCost, defaultCost.TotalCost, 1e-12)
			require.InDelta(t, base.InputCost, defaultCost.InputCost, 1e-12)
			require.InDelta(t, base.OutputCost, defaultCost.OutputCost, 1e-12)
			require.InDelta(t, base.CacheReadCost, defaultCost.CacheReadCost, 1e-12)
		})
	}
}

func TestOpenAIFastBilling_FastMultiplierOverridesEnforcedRatio(t *testing.T) {
	t.Parallel()

	svc := newBillingServiceWithPrices(nil, nil, map[string]*billingpricing.ModelPricing{})
	catalog := &billingpricing.ModelPricing{
		InputPricePerToken:             5e-6,
		InputPricePerTokenPriority:     10e-6,
		OutputPricePerToken:            30e-6,
		OutputPricePerTokenPriority:    60e-6,
		CacheReadPricePerToken:         0.5e-6,
		CacheReadPricePerTokenPriority: 1e-6,
	}
	pricing := svc.ApplyModelSpecificPricingPolicy("gpt-5.5", catalog)
	require.InDelta(t, 12.5e-6, pricing.InputPricePerTokenPriority, 1e-12, "enforce must still write 2.5x priority prices")
	require.InDelta(t, 75e-6, pricing.OutputPricePerTokenPriority, 1e-12)

	multiplier := 1.7
	pricing.FastMultiplier = &multiplier

	tokens := billingpricing.UsageTokens{InputTokens: 1_000_000, OutputTokens: 1_000_000, CacheReadTokens: 1_000_000}
	standard := svc.ComputeTokenBreakdown(pricing, tokens, 1, "", false)
	fast := svc.ComputeTokenBreakdown(pricing, tokens, 1, "fast", false)
	priority := svc.ComputeTokenBreakdown(pricing, tokens, 1, "priority", false)

	require.InDelta(t, standard.TotalCost*1.7, fast.TotalCost, 1e-9)
	require.InDelta(t, fast.TotalCost, priority.TotalCost, 1e-12)

	withoutOverride := *pricing
	withoutOverride.FastMultiplier = nil
	enforced := svc.ComputeTokenBreakdown(&withoutOverride, tokens, 1, "fast", false)
	require.InDelta(t, standard.TotalCost*2.5, enforced.TotalCost, 1e-9,
		"without FastMultiplier the same enforced prices still bill 2.5x")
}

// ---------------------------------------------------------------------------
// 上游 payload：fast 归一化为 priority 并确实到达上游
// ---------------------------------------------------------------------------

func TestForwardAsChatCompletions_ServiceTierFastNormalizedToPriorityUpstream(t *testing.T) {

	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	body := []byte(`{"model":"gpt-5.5","messages":[{"role":"user","content":"hello"}],"service_tier":"fast","stream":false}`)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/chat/completions", bytes.NewReader(body))
	c.Request.Header.Set("Content-Type", "application/json")

	upstream := &httpUpstreamRecorder{resp: &http.Response{
		StatusCode: http.StatusBadRequest,
		Header:     http.Header{"Content-Type": []string{"application/json"}, "x-request-id": []string{"rid-chat-st"}},
		Body:       io.NopCloser(strings.NewReader(`{"error":{"type":"invalid_request_error","message":"stop"}}`)),
	}}

	svc := withSchedulerParametersForTest(&OpenAIGatewayService{
		cfg:            &config.Config{},
		httpUpstream:   upstream,
		settingService: newExecutionReadersFixture(&openAIFastPolicyRepoStub{values: map[string]string{}}, &config.Config{}),
	})
	account := &gatewayprovider.ExecutionAccount{Record: accountcore.Record{LoadLocation: time.LoadLocation, ID: 21,
		Name:        "openai-compatible",
		Platform:    capability.PlatformOpenAI,
		Type:        capability.AccountTypeAPIKey,
		Concurrency: 1,
		Credentials: map[string]any{"api_key": "sk-compatible"},
		Extra:       map[string]any{}},
	}

	_, err := svc.ForwardAsChatCompletions(context.Background(), c, account, body, "", "gpt-5.5")
	require.Error(t, err) // upstream 400 → 错误返回，但请求体已被 recorder 捕获
	require.NotNil(t, upstream.lastBody)
	require.Equal(t, "priority", gjson.GetBytes(upstream.lastBody, "service_tier").String(),
		"client alias fast must reach upstream as priority")
}

func TestForwardAsChatCompletions_ServiceTierPriorityPreservedUpstream(t *testing.T) {

	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	body := []byte(`{"model":"gpt-5.5","messages":[{"role":"user","content":"hello"}],"service_tier":"priority","stream":false}`)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/chat/completions", bytes.NewReader(body))
	c.Request.Header.Set("Content-Type", "application/json")

	upstream := &httpUpstreamRecorder{resp: &http.Response{
		StatusCode: http.StatusBadRequest,
		Header:     http.Header{"Content-Type": []string{"application/json"}, "x-request-id": []string{"rid-chat-st2"}},
		Body:       io.NopCloser(strings.NewReader(`{"error":{"type":"invalid_request_error","message":"stop"}}`)),
	}}

	svc := withSchedulerParametersForTest(&OpenAIGatewayService{
		cfg:            &config.Config{},
		httpUpstream:   upstream,
		settingService: newExecutionReadersFixture(&openAIFastPolicyRepoStub{values: map[string]string{}}, &config.Config{}),
	})
	account := &gatewayprovider.ExecutionAccount{Record: accountcore.Record{LoadLocation: time.LoadLocation, ID: 2,
		Name:        "openai-compatible",
		Platform:    capability.PlatformOpenAI,
		Type:        capability.AccountTypeAPIKey,
		Concurrency: 1,
		Credentials: map[string]any{"api_key": "sk-compatible"},
		Extra:       map[string]any{}},
	}

	_, err := svc.ForwardAsChatCompletions(context.Background(), c, account, body, "", "gpt-5.5")
	require.Error(t, err)
	require.NotNil(t, upstream.lastBody)
	require.Equal(t, "priority", gjson.GetBytes(upstream.lastBody, "service_tier").String())
}

func TestForward_ResponsesServiceTierFastNormalizedToPriorityUpstream(t *testing.T) {

	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	body := []byte(`{"model":"gpt-5.5","service_tier":"fast","input":"hello","stream":false}`)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", bytes.NewReader(body))
	c.Request.Header.Set("Content-Type", "application/json")

	upstream := &httpUpstreamRecorder{resp: &http.Response{
		StatusCode: http.StatusOK,
		Header:     http.Header{"Content-Type": []string{"application/json"}, "x-request-id": []string{"rid-resp-st"}},
		Body: io.NopCloser(strings.NewReader(
			`{"id":"resp_1","object":"response","status":"completed","model":"gpt-5.5","output":[],"usage":{"input_tokens":1,"output_tokens":1}}`,
		)),
	}}

	svc := withSchedulerParametersForTest(&OpenAIGatewayService{
		cfg:            &config.Config{Security: config.SecurityConfig{URLAllowlist: config.URLAllowlistConfig{Enabled: false}}},
		httpUpstream:   upstream,
		settingService: newExecutionReadersFixture(&openAIFastPolicyRepoStub{values: map[string]string{}}, &config.Config{}),
	})
	account := &gatewayprovider.ExecutionAccount{Record: accountcore.Record{LoadLocation: time.LoadLocation, ID: 7,
		Name:        "openai-apikey",
		Platform:    capability.PlatformOpenAI,
		Type:        capability.AccountTypeAPIKey,
		Concurrency: 1,
		Credentials: map[string]any{"api_key": "sk-test"},
		Extra:       map[string]any{},
		Status:      billingcore.StatusActive,
		Schedulable: true},
	}

	result, err := svc.Forward(context.Background(), c, account, body)
	require.NoError(t, err)
	require.NotNil(t, result)
	require.NotNil(t, upstream.lastBody)
	require.Equal(t, "priority", gjson.GetBytes(upstream.lastBody, "service_tier").String(),
		"client alias fast must reach the upstream as priority")
	// 计费上下文：result 携带归一化后的 tier。
	require.NotNil(t, result.ServiceTier)
	require.Equal(t, "priority", *result.ServiceTier)
}

func TestForward_ResponsesServiceTierOmittedStaysOmitted(t *testing.T) {

	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	body := []byte(`{"model":"gpt-5.5","input":"hello","stream":false}`)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", bytes.NewReader(body))
	c.Request.Header.Set("Content-Type", "application/json")

	upstream := &httpUpstreamRecorder{resp: &http.Response{
		StatusCode: http.StatusOK,
		Header:     http.Header{"Content-Type": []string{"application/json"}, "x-request-id": []string{"rid-resp-st2"}},
		Body: io.NopCloser(strings.NewReader(
			`{"id":"resp_2","object":"response","status":"completed","model":"gpt-5.5","output":[],"usage":{"input_tokens":1,"output_tokens":1}}`,
		)),
	}}

	svc := withSchedulerParametersForTest(&OpenAIGatewayService{
		cfg:            &config.Config{Security: config.SecurityConfig{URLAllowlist: config.URLAllowlistConfig{Enabled: false}}},
		httpUpstream:   upstream,
		settingService: newExecutionReadersFixture(&openAIFastPolicyRepoStub{values: map[string]string{}}, &config.Config{}),
	})
	account := &gatewayprovider.ExecutionAccount{Record: accountcore.Record{LoadLocation: time.LoadLocation, ID: 7,
		Name:        "openai-apikey",
		Platform:    capability.PlatformOpenAI,
		Type:        capability.AccountTypeAPIKey,
		Concurrency: 1,
		Credentials: map[string]any{"api_key": "sk-test"},
		Extra:       map[string]any{},
		Status:      billingcore.StatusActive,
		Schedulable: true},
	}

	result, err := svc.Forward(context.Background(), c, account, body)
	require.NoError(t, err)
	require.NotNil(t, result)
	require.NotNil(t, upstream.lastBody)
	require.False(t, gjson.GetBytes(upstream.lastBody, "service_tier").Exists(),
		"omitted service_tier must stay omitted")
	require.Nil(t, result.ServiceTier)
}

// ---------------------------------------------------------------------------
// 流式计费上下文：service_tier 需要从请求体传到 usage 计费
// ---------------------------------------------------------------------------

func TestForwardStreaming_ServiceTierPropagatedToResult(t *testing.T) {

	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	body := []byte(`{"model":"gpt-5.5","service_tier":"fast","input":"hello","stream":true}`)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", bytes.NewReader(body))
	c.Request.Header.Set("Content-Type", "application/json")

	streamPayload := "data: {\"type\":\"response.created\",\"response\":{\"id\":\"resp_s1\",\"object\":\"response\",\"model\":\"gpt-5.5\",\"status\":\"in_progress\"}}\n\n" +
		"data: {\"type\":\"response.output_text.delta\",\"item_id\":\"it_1\",\"output_index\":0,\"delta\":\"hi\"}\n\n" +
		"data: {\"type\":\"response.completed\",\"response\":{\"id\":\"resp_s1\",\"object\":\"response\",\"model\":\"gpt-5.5\",\"status\":\"completed\",\"output\":[],\"usage\":{\"input_tokens\":1,\"output_tokens\":1}}}\n\n" +
		"data: [DONE]\n\n"

	upstream := &httpUpstreamRecorder{resp: &http.Response{
		StatusCode: http.StatusOK,
		Header:     http.Header{"Content-Type": []string{"text/event-stream"}, "x-request-id": []string{"rid-resp-stream-st"}},
		Body:       io.NopCloser(strings.NewReader(streamPayload)),
	}}

	svc := withSchedulerParametersForTest(&OpenAIGatewayService{
		cfg:            &config.Config{Security: config.SecurityConfig{URLAllowlist: config.URLAllowlistConfig{Enabled: false}}},
		httpUpstream:   upstream,
		settingService: newExecutionReadersFixture(&openAIFastPolicyRepoStub{values: map[string]string{}}, &config.Config{}),
	})
	account := &gatewayprovider.ExecutionAccount{Record: accountcore.Record{LoadLocation: time.LoadLocation, ID: 7,
		Name:        "openai-apikey",
		Platform:    capability.PlatformOpenAI,
		Type:        capability.AccountTypeAPIKey,
		Concurrency: 1,
		Credentials: map[string]any{"api_key": "sk-test"},
		Extra:       map[string]any{},
		Status:      billingcore.StatusActive,
		Schedulable: true},
	}

	result, err := svc.Forward(context.Background(), c, account, body)
	require.NoError(t, err)
	require.NotNil(t, result)
	require.NotNil(t, result.ServiceTier)
	require.Equal(t, "priority", *result.ServiceTier, "streaming billing context must carry the normalized tier")
	// /v1/responses 流是上游 SSE 原样透传：上游没回 service_tier 就不该出现；
	// 网关只在计费结果里携带请求侧 tier，不往下游流里注入。
	require.Contains(t, rec.Body.String(), `"delta":"hi"`, "streamed content must reach the client")
	require.NotContains(t, rec.Body.String(), `"service_tier"`, "upstream did not return service_tier, client stream must stay untouched")
}

// ---------------------------------------------------------------------------
// 转发阶段分别保留最终出站 tier 与上游回显 tier
// ---------------------------------------------------------------------------

func TestForward_ResponsesKeepsOutboundAndObservedServiceTiersSeparate(t *testing.T) {

	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	body := []byte(`{"model":"gpt-5.5","service_tier":"fast","input":"hello","stream":false}`)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", bytes.NewReader(body))
	c.Request.Header.Set("Content-Type", "application/json")

	// 上游回显 service_tier=default（例如请求实际被降级）。
	upstream := &httpUpstreamRecorder{resp: &http.Response{
		StatusCode: http.StatusOK,
		Header:     http.Header{"Content-Type": []string{"application/json"}, "x-request-id": []string{"rid-resp-echo"}},
		Body: io.NopCloser(strings.NewReader(
			`{"id":"resp_1","object":"response","status":"completed","model":"gpt-5.5","service_tier":"default","output":[],"usage":{"input_tokens":1,"output_tokens":1}}`,
		)),
	}}

	svc := withSchedulerParametersForTest(&OpenAIGatewayService{
		cfg:            &config.Config{Security: config.SecurityConfig{URLAllowlist: config.URLAllowlistConfig{Enabled: false}}},
		httpUpstream:   upstream,
		settingService: newExecutionReadersFixture(&openAIFastPolicyRepoStub{values: map[string]string{}}, &config.Config{}),
	})
	account := &gatewayprovider.ExecutionAccount{Record: accountcore.Record{LoadLocation: time.LoadLocation, ID: 7,
		Name:        "openai-apikey",
		Platform:    capability.PlatformOpenAI,
		Type:        capability.AccountTypeAPIKey,
		Concurrency: 1,
		Credentials: map[string]any{"api_key": "sk-test"},
		Extra:       map[string]any{},
		Status:      billingcore.StatusActive,
		Schedulable: true},
	}

	result, err := svc.Forward(context.Background(), c, account, body)
	require.NoError(t, err)
	require.NotNil(t, result)
	require.NotNil(t, result.ServiceTier)
	require.Equal(t, "priority", *result.ServiceTier)
	require.Equal(t, "default", result.UpstreamResponseServiceTier)
	// 非流式响应原样透传：客户端同样看到 default。
	require.Contains(t, rec.Body.String(), `"service_tier":"default"`)
	require.NotContains(t, rec.Body.String(), `"service_tier":"priority"`)
}

func TestForwardStreaming_KeepsOutboundAndObservedServiceTiersSeparate(t *testing.T) {

	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	body := []byte(`{"model":"gpt-5.5","service_tier":"fast","input":"hello","stream":true}`)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", bytes.NewReader(body))
	c.Request.Header.Set("Content-Type", "application/json")

	streamPayload := "data: {\"type\":\"response.created\",\"response\":{\"id\":\"resp_s1\",\"object\":\"response\",\"model\":\"gpt-5.5\",\"status\":\"in_progress\"}}\n\n" +
		"data: {\"type\":\"response.completed\",\"response\":{\"id\":\"resp_s1\",\"object\":\"response\",\"model\":\"gpt-5.5\",\"status\":\"completed\",\"service_tier\":\"default\",\"output\":[],\"usage\":{\"input_tokens\":1,\"output_tokens\":1}}}\n\n" +
		"data: [DONE]\n\n"

	upstream := &httpUpstreamRecorder{resp: &http.Response{
		StatusCode: http.StatusOK,
		Header:     http.Header{"Content-Type": []string{"text/event-stream"}, "x-request-id": []string{"rid-resp-echo-stream"}},
		Body:       io.NopCloser(strings.NewReader(streamPayload)),
	}}

	svc := withSchedulerParametersForTest(&OpenAIGatewayService{
		cfg:            &config.Config{Security: config.SecurityConfig{URLAllowlist: config.URLAllowlistConfig{Enabled: false}}},
		httpUpstream:   upstream,
		settingService: newExecutionReadersFixture(&openAIFastPolicyRepoStub{values: map[string]string{}}, &config.Config{}),
	})
	account := &gatewayprovider.ExecutionAccount{Record: accountcore.Record{LoadLocation: time.LoadLocation, ID: 7,
		Name:        "openai-apikey",
		Platform:    capability.PlatformOpenAI,
		Type:        capability.AccountTypeAPIKey,
		Concurrency: 1,
		Credentials: map[string]any{"api_key": "sk-test"},
		Extra:       map[string]any{},
		Status:      billingcore.StatusActive,
		Schedulable: true},
	}

	result, err := svc.Forward(context.Background(), c, account, body)
	require.NoError(t, err)
	require.NotNil(t, result)
	require.NotNil(t, result.ServiceTier)
	require.Equal(t, "priority", *result.ServiceTier)
	require.Equal(t, "default", result.UpstreamResponseServiceTier)
	// 流式原样透传：客户端在终止事件里看到 default。
	require.Contains(t, rec.Body.String(), `"service_tier":"default"`)
}

func TestForwardAsChatCompletions_KeepsOutboundAndObservedServiceTiersSeparate(t *testing.T) {

	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	body := []byte(`{"model":"gpt-5.5","messages":[{"role":"user","content":"hello"}],"service_tier":"fast","stream":false}`)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/chat/completions", bytes.NewReader(body))
	c.Request.Header.Set("Content-Type", "application/json")

	streamPayload := "data: {\"type\":\"response.created\",\"response\":{\"id\":\"resp_c1\",\"object\":\"response\",\"model\":\"gpt-5.5\",\"status\":\"in_progress\"}}\n\n" +
		"data: {\"type\":\"response.output_text.delta\",\"item_id\":\"it_1\",\"output_index\":0,\"delta\":\"hi\"}\n\n" +
		"data: {\"type\":\"response.completed\",\"response\":{\"id\":\"resp_c1\",\"object\":\"response\",\"model\":\"gpt-5.5\",\"status\":\"completed\",\"service_tier\":\"default\",\"output\":[],\"usage\":{\"input_tokens\":1,\"output_tokens\":1}}}\n\n" +
		"data: [DONE]\n\n"

	upstream := &httpUpstreamRecorder{resp: &http.Response{
		StatusCode: http.StatusOK,
		Header:     http.Header{"Content-Type": []string{"text/event-stream"}, "x-request-id": []string{"rid-chat-echo"}},
		Body:       io.NopCloser(strings.NewReader(streamPayload)),
	}}

	svc := withSchedulerParametersForTest(&OpenAIGatewayService{
		cfg:            &config.Config{Security: config.SecurityConfig{URLAllowlist: config.URLAllowlistConfig{Enabled: false}}},
		httpUpstream:   upstream,
		settingService: newExecutionReadersFixture(&openAIFastPolicyRepoStub{values: map[string]string{}}, &config.Config{}),
	})
	account := &gatewayprovider.ExecutionAccount{Record: accountcore.Record{LoadLocation: time.LoadLocation, ID: 21,
		Name:        "openai-compatible",
		Platform:    capability.PlatformOpenAI,
		Type:        capability.AccountTypeAPIKey,
		Concurrency: 1,
		Credentials: map[string]any{"api_key": "sk-compatible"},
		Extra:       map[string]any{},
		Status:      billingcore.StatusActive,
		Schedulable: true},
	}

	result, err := svc.ForwardAsChatCompletions(context.Background(), c, account, body, "", "gpt-5.5")
	require.NoError(t, err)
	require.NotNil(t, result)
	require.NotNil(t, result.ServiceTier)
	require.Equal(t, "priority", *result.ServiceTier)
	require.Equal(t, "default", result.UpstreamResponseServiceTier)
	// 缓冲转回 Chat Completions：客户端响应里如实回显 default。
	require.Contains(t, rec.Body.String(), `"service_tier":"default"`)
	require.NotContains(t, rec.Body.String(), `"service_tier":"priority"`)
}

// ---------------------------------------------------------------------------
// policy filter：删除 service_tier 后不得再按原请求 Fast 计费
// ---------------------------------------------------------------------------

func TestForward_ServiceTierFilteredByPolicyBillsStandard(t *testing.T) {

	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	body := []byte(`{"model":"gpt-5.5","service_tier":"priority","input":"hello","stream":false}`)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", bytes.NewReader(body))
	c.Request.Header.Set("Content-Type", "application/json")

	// 管理员配置 priority → filter：字段在出站前被删除。
	settings := &tierpolicy.OpenAIFastPolicySettings{Rules: []tierpolicy.OpenAIFastPolicyRule{{
		ServiceTier: tierpolicy.OpenAIFastTierPriority,
		Action:      anthropic.BetaPolicyActionFilter,
		Scope:       anthropic.BetaPolicyScopeAll,
	}}}
	raw, err := json.Marshal(settings)
	require.NoError(t, err)
	repo := &openAIFastPolicyRepoStub{values: map[string]string{gateway.SettingKeyOpenAIFastPolicySettings: string(raw)}}

	upstream := &httpUpstreamRecorder{resp: &http.Response{
		StatusCode: http.StatusOK,
		Header:     http.Header{"Content-Type": []string{"application/json"}, "x-request-id": []string{"rid-resp-filter"}},
		Body: io.NopCloser(strings.NewReader(
			`{"id":"resp_1","object":"response","status":"completed","model":"gpt-5.5","output":[],"usage":{"input_tokens":1,"output_tokens":1}}`,
		)),
	}}

	svc := withSchedulerParametersForTest(&OpenAIGatewayService{
		cfg:            &config.Config{Security: config.SecurityConfig{URLAllowlist: config.URLAllowlistConfig{Enabled: false}}},
		httpUpstream:   upstream,
		settingService: newExecutionReadersFixture(repo, &config.Config{}),
	})
	account := &gatewayprovider.ExecutionAccount{Record: accountcore.Record{LoadLocation: time.LoadLocation, ID: 7,
		Name:        "openai-apikey",
		Platform:    capability.PlatformOpenAI,
		Type:        capability.AccountTypeAPIKey,
		Concurrency: 1,
		Credentials: map[string]any{"api_key": "sk-test"},
		Extra:       map[string]any{},
		Status:      billingcore.StatusActive,
		Schedulable: true},
	}

	result, err := svc.Forward(context.Background(), c, account, body)
	require.NoError(t, err)
	require.NotNil(t, result)
	// 出站 body 已剥离 service_tier、上游也未回显 → 无 tier → 按标准价计费。
	require.False(t, gjson.GetBytes(upstream.lastBody, "service_tier").Exists(),
		"policy filter must strip service_tier from the outbound body")
	require.Nil(t, result.ServiceTier, "filtered request must not bill as fast")
}

// ---------------------------------------------------------------------------
// 上游回显观察与解析器单测
// ---------------------------------------------------------------------------
