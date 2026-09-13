//go:build unit

package service

import (
	"context"
	"io"
	"math"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/TokenFlux/TokenRouter/internal/config"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

// 核对区间、缓存桶和分组倍率的组合，并确保按次费用不受推理倍率影响。
func TestMaxReasoningPricing_IntervalsAndBillingModes(t *testing.T) {
	bs := newTestBillingService()
	resolver := NewModelPricingResolver(nil, bs)
	for _, factor := range []float64{1, 1.5, 3} {
		resolved := &ResolvedPricing{
			Mode:        BillingModeToken,
			BasePricing: &ModelPricing{MaxReasoningEffortMultiplier: &factor},
			Intervals: []PricingInterval{{
				MinTokens: 0, InputPrice: testPtrFloat64(0.01), OutputPrice: testPtrFloat64(0.02),
				CacheWritePrice: testPtrFloat64(0.03), CacheReadPrice: testPtrFloat64(0.001),
			}},
		}
		input := CostInput{Ctx: context.Background(), Model: "claude-fable-5-1", RateMultiplier: 2,
			Tokens:   UsageTokens{InputTokens: 100, OutputTokens: 20, CacheCreationTokens: 10, CacheReadTokens: 50},
			Resolver: resolver, Resolved: resolved, ReasoningEffort: "max"}
		maxCost, err := bs.CalculateCostUnified(input)
		require.NoError(t, err)
		input.ReasoningEffort = "xhigh"
		standard, err := bs.CalculateCostUnified(input)
		require.NoError(t, err)
		require.InDelta(t, 1.75, standard.TotalCost, 1e-12)
		require.InDelta(t, 1.75*factor, maxCost.TotalCost, 1e-12)
		require.InDelta(t, 3.5*factor, maxCost.ActualCost, 1e-12)
		require.InDelta(t, 0.3*factor, maxCost.CacheCreationCost, 1e-12)
		require.InDelta(t, 0.05*factor, maxCost.CacheReadCost, 1e-12)
	}
	for _, mode := range []BillingMode{BillingModePerRequest, BillingModeImage, BillingModeVideo} {
		cost, err := bs.CalculateCostUnified(CostInput{Model: "claude-fable-5-1", ReasoningEffort: "max",
			RequestCount: 2, RateMultiplier: 2, Resolver: resolver,
			Resolved: &ResolvedPricing{Mode: mode, DefaultPerRequestPrice: 0.1}})
		require.NoError(t, err)
		require.InDelta(t, 0.2, cost.TotalCost, 1e-12)
	}
}

// 账号自定义价和复用的用户费用均为最终成本，只有模型价兜底要按实际档位计价。
func TestMaxReasoningPricing_AccountStatsPriority(t *testing.T) {
	bs := newTestBillingService()
	channel := &Channel{ID: 1, Status: StatusActive, AccountStatsPricingRules: []AccountStatsPricingRule{{
		GroupIDs: []int64{10}, Pricing: []ChannelModelPricing{{Models: []string{"claude-fable-5-1"}, InputPrice: testPtrFloat64(0.01)}},
	}}}
	cs := newTestChannelServiceForStats(t, channel, 10, PlatformAnthropic)
	tokens := UsageTokens{InputTokens: 100}
	cost := resolveAccountStatsCost(context.Background(), cs, bs, 1, 10, "claude-fable-5-1", "", tokens, 1, 9, "priority", "max")
	require.NotNil(t, cost)
	require.InDelta(t, 1, *cost, 1e-12)
	channel.AccountStatsPricingRules = nil
	channel.ApplyPricingToAccountStats = true
	// 管理变更后通过读取入口建立新快照，不直接改已发布的缓存。
	cs = newTestChannelServiceForStats(t, channel, 10, PlatformAnthropic)
	cost = resolveAccountStatsCost(context.Background(), cs, bs, 1, 10, "claude-fable-5-1", "", tokens, 1, 9, "priority", "max")
	require.InDelta(t, 9, *cost, 1e-12)
	channel.ApplyPricingToAccountStats = false
	cs = newTestChannelServiceForStats(t, channel, 10, PlatformAnthropic)
	standard := resolveAccountStatsCost(context.Background(), cs, bs, 1, 10, "claude-fable-5-1", "", tokens, 1, 9, "", "xhigh")
	cost = resolveAccountStatsCost(context.Background(), cs, bs, 1, 10, "claude-fable-5-1", "", tokens, 1, 9, "", "max")
	require.NotNil(t, standard)
	require.NotNil(t, cost)
	require.InDelta(t, *standard*3, *cost, 1e-12)
}

// OpenAI 兼容转发的账单按结果档位计算，策略前的 max 仅用于审计。
func TestMaxReasoningPricing_OpenAIUsageUsesFinalEffort(t *testing.T) {
	bs := newTestBillingService()
	for _, resolver := range []*ModelPricingResolver{nil, NewModelPricingResolver(nil, bs)} {
		svc := &OpenAIGatewayService{billingService: bs, resolver: resolver}
		requested, final := "max", "xhigh"
		result := &OpenAIForwardResult{ReasoningEffort: &final, RequestedReasoningEffort: &requested}
		key := &APIKey{Group: &Group{ID: 1, Platform: PlatformOpenAI}}
		tokens := UsageTokens{InputTokens: 1000}
		standard, err := svc.calculateOpenAIRecordUsageCost(context.Background(), result, key, []string{"claude-fable-5-1"}, 2, 1, 1, 1, tokens, "")
		require.NoError(t, err)
		final = "max"
		cost, err := svc.calculateOpenAIRecordUsageCost(context.Background(), result, key, []string{"claude-fable-5-1"}, 2, 1, 1, 1, tokens, "")
		require.NoError(t, err)
		require.InDelta(t, standard.TotalCost*3, cost.TotalCost, 1e-12)
		require.InDelta(t, standard.ActualCost*3, cost.ActualCost, 1e-12)
	}
}

func TestMaxReasoningPricing_RejectsInvalidMultipliers(t *testing.T) {
	for _, factor := range []float64{0, -1, math.NaN(), math.Inf(1)} {
		require.Error(t, checkBillingModeRequirements(ChannelModelPricing{BillingMode: BillingModeToken, MaxReasoningEffortMultiplier: &factor}))
	}
}

// 从兼容入口实际转发，核对账单使用的结果档位与上游收到的档位一致。
func TestMaxReasoningPricing_AnthropicForwardReportsOutboundEffort(t *testing.T) {
	for _, protocol := range []string{"responses", "chat"} {
		for _, effort := range []string{"xhigh", "max"} {
			t.Run(protocol+"/"+effort, func(t *testing.T) {
				stream := "event: message_start\ndata: {\"type\":\"message_start\",\"message\":{\"id\":\"msg_1\",\"type\":\"message\",\"role\":\"assistant\",\"content\":[],\"model\":\"claude-fable-5-1\",\"usage\":{\"input_tokens\":100}}}\n\nevent: message_delta\ndata: {\"type\":\"message_delta\",\"delta\":{\"stop_reason\":\"end_turn\"},\"usage\":{\"output_tokens\":1}}\n\nevent: message_stop\ndata: {\"type\":\"message_stop\"}\n\n"
				upstream := &httpUpstreamRecorder{responses: []*http.Response{{StatusCode: 200, Header: http.Header{"Content-Type": {"text/event-stream"}}, Body: io.NopCloser(strings.NewReader(stream))}}}
				svc := &GatewayService{cfg: &config.Config{}, httpUpstream: upstream}
				account := &Account{ID: 1, Platform: PlatformAnthropic, Type: AccountTypeAPIKey, Credentials: map[string]any{"api_key": "test-key", "base_url": "https://api.anthropic.com"}}
				c, _ := gin.CreateTestContext(httptest.NewRecorder())
				c.Request = httptest.NewRequest(http.MethodPost, "/v1/"+protocol, nil)
				var result *ForwardResult
				var err error
				if protocol == "responses" {
					result, err = svc.ForwardAsResponses(c.Request.Context(), c, account, []byte(`{"model":"claude-fable-5-1","input":"hi","reasoning":{"effort":"`+effort+`"}}`), nil)
				} else {
					result, err = svc.ForwardAsChatCompletions(c.Request.Context(), c, account, []byte(`{"model":"claude-fable-5-1","messages":[{"role":"user","content":"hi"}],"reasoning_effort":"`+effort+`"}`), nil)
				}
				require.NoError(t, err)
				require.NotNil(t, result.ReasoningEffort)
				require.Equal(t, effort, *result.ReasoningEffort)
				requestBody, err := io.ReadAll(upstream.lastReq.Body)
				require.NoError(t, err)
				require.Equal(t, effort, gjson.GetBytes(requestBody, "output_config.effort").String())
			})
		}
	}
}
