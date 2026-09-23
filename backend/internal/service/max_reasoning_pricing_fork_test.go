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
	time "time"

	accountcore "github.com/TokenFlux/TokenRouter/internal/account"
	billingtestkit "github.com/TokenFlux/TokenRouter/internal/billing/testkit"

	"github.com/TokenFlux/TokenRouter/internal/apikey"
	"github.com/TokenFlux/TokenRouter/internal/billing"
	"github.com/TokenFlux/TokenRouter/internal/billing/pricing"
	"github.com/TokenFlux/TokenRouter/internal/config"
	completion "github.com/TokenFlux/TokenRouter/internal/gateway/completion"
	forwardcore "github.com/TokenFlux/TokenRouter/internal/gateway/forward"
	gatewaycapture "github.com/TokenFlux/TokenRouter/internal/gateway/provider"
	"github.com/TokenFlux/TokenRouter/internal/routing"
	"github.com/TokenFlux/TokenRouter/internal/routing/capability"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

// 核对区间、缓存桶和分组倍率的组合，并确保按次费用不受推理倍率影响。
func TestMaxReasoningPricing_IntervalsAndBillingModes(t *testing.T) {
	bs := newTestBillingService()
	resolver := billingtestkit.PriceResolver(nil, bs)
	for _, factor := range []float64{1, 1.5, 3} {
		resolved := &pricing.ResolvedPricing{
			Mode:        routing.BillingModeToken,
			BasePricing: &pricing.ModelPricing{MaxReasoningEffortMultiplier: &factor},
			Intervals: []routing.PricingInterval{{
				MinTokens: 0, InputPrice: testPtrFloat64(0.01), OutputPrice: testPtrFloat64(0.02),
				CacheWritePrice: testPtrFloat64(0.03), CacheReadPrice: testPtrFloat64(0.001),
			}},
		}
		input := billing.CostInput{Ctx: context.Background(), Model: "claude-fable-5-1", RateMultiplier: 2,
			Tokens:   pricing.UsageTokens{InputTokens: 100, OutputTokens: 20, CacheCreationTokens: 10, CacheReadTokens: 50},
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
	for _, mode := range []routing.BillingMode{routing.BillingModePerRequest, routing.BillingModeImage, routing.BillingModeVideo} {
		cost, err := bs.CalculateCostUnified(billing.CostInput{Model: "claude-fable-5-1", ReasoningEffort: "max",
			RequestCount: 2, RateMultiplier: 2, Resolver: resolver,
			Resolved: &pricing.ResolvedPricing{Mode: mode, DefaultPerRequestPrice: 0.1}})
		require.NoError(t, err)
		require.InDelta(t, 0.2, cost.TotalCost, 1e-12)
	}
}

// 账号自定义价和复用的用户费用均为最终成本，只有模型价兜底要按实际档位计价。
func TestMaxReasoningPricing_AccountStatsPriority(t *testing.T) {
	bs := newTestBillingService()
	channel := &routing.Channel{ID: 1, Status: billing.StatusActive, AccountStatsPricingRules: []routing.AccountStatsPricingRule{{
		GroupIDs: []int64{10}, Pricing: []routing.ChannelModelPricing{{Models: []string{"claude-fable-5-1"}, InputPrice: testPtrFloat64(0.01)}},
	}}}
	cs := newTestChannelServiceForStats(t, channel, 10, capability.PlatformAnthropic)
	tokens := pricing.UsageTokens{InputTokens: 100}
	cost := resolveAccountStatsCost(context.Background(), cs, bs, 1, 10, "claude-fable-5-1", "", tokens, 1, 9, "priority", "max")
	require.NotNil(t, cost)
	require.InDelta(t, 1, *cost, 1e-12)
	channel.AccountStatsPricingRules = nil
	channel.ApplyPricingToAccountStats = true
	// 管理变更后通过读取入口建立新快照，不直接改已发布的缓存。
	cs = newTestChannelServiceForStats(t, channel, 10, capability.PlatformAnthropic)
	cost = resolveAccountStatsCost(context.Background(), cs, bs, 1, 10, "claude-fable-5-1", "", tokens, 1, 9, "priority", "max")
	require.InDelta(t, 9, *cost, 1e-12)
	channel.ApplyPricingToAccountStats = false
	cs = newTestChannelServiceForStats(t, channel, 10, capability.PlatformAnthropic)
	standard := resolveAccountStatsCost(context.Background(), cs, bs, 1, 10, "claude-fable-5-1", "", tokens, 1, 9, "", "xhigh")
	cost = resolveAccountStatsCost(context.Background(), cs, bs, 1, 10, "claude-fable-5-1", "", tokens, 1, 9, "", "max")
	require.NotNil(t, standard)
	require.NotNil(t, cost)
	require.InDelta(t, *standard*3, *cost, 1e-12)
}

// OpenAI 兼容转发的账单按结果档位计算，策略前的 max 仅用于审计。
func TestMaxReasoningPricing_OpenAIUsageUsesFinalEffort(t *testing.T) {
	bs := newTestBillingService()
	for _, resolver := range []*billing.PriceResolver{nil, billingtestkit.PriceResolver(nil, bs)} {
		svc := completion.NewRecorder(completion.Dependencies{Calculator: bs, Prices: resolver}, completion.RecorderOptions{DefaultMultiplier: 1})

		requested, final := "max", "xhigh"
		result := &forwardcore.OpenAIResult{ReasoningEffort: &final, RequestedReasoningEffort: &requested}
		key := &apikey.APIKey{Group: &routing.Group{ID: 1, Platform: capability.PlatformOpenAI}}
		tokens := pricing.UsageTokens{InputTokens: 1000}
		standard, err := svc.CalculateOpenAIRecordUsageCostAt(context.Background(), gatewaycapture.ProjectOpenAICompletionResult(result, nil), gatewaycapture.ProjectCompletionKey(key), []string{"claude-fable-5-1"}, 2, 1, 1, 1, tokens, "", time.Time{})
		require.NoError(t, err)
		final = "max"
		cost, err := svc.CalculateOpenAIRecordUsageCostAt(context.Background(), gatewaycapture.ProjectOpenAICompletionResult(result, nil), gatewaycapture.ProjectCompletionKey(key), []string{"claude-fable-5-1"}, 2, 1, 1, 1, tokens, "", time.Time{})
		require.NoError(t, err)
		require.InDelta(t, standard.TotalCost*3, cost.TotalCost, 1e-12)
		require.InDelta(t, standard.ActualCost*3, cost.ActualCost, 1e-12)
	}
}

func TestMaxReasoningPricing_RejectsInvalidMultipliers(t *testing.T) {
	for _, factor := range []float64{0, -1, math.NaN(), math.Inf(1)} {
		require.Error(t, checkBillingModeRequirements(routing.ChannelModelPricing{BillingMode: routing.BillingModeToken, MaxReasoningEffortMultiplier: &factor}))
	}
}

// 从兼容入口实际转发，核对账单使用的结果档位与上游收到的档位一致。
func TestMaxReasoningPricing_AnthropicForwardReportsOutboundEffort(t *testing.T) {
	for _, protocol := range []string{"responses", "chat"} {
		for _, effort := range []string{"xhigh", "max"} {
			t.Run(protocol+"/"+effort, func(t *testing.T) {
				stream := "event: message_start\ndata: {\"type\":\"message_start\",\"message\":{\"id\":\"msg_1\",\"type\":\"message\",\"role\":\"assistant\",\"content\":[],\"model\":\"claude-fable-5-1\",\"usage\":{\"input_tokens\":100}}}\n\nevent: message_delta\ndata: {\"type\":\"message_delta\",\"delta\":{\"stop_reason\":\"end_turn\"},\"usage\":{\"output_tokens\":1}}\n\nevent: message_stop\ndata: {\"type\":\"message_stop\"}\n\n"
				upstream := &httpUpstreamRecorder{responses: []*http.Response{{StatusCode: 200, Header: http.Header{"Content-Type": {"text/event-stream"}}, Body: io.NopCloser(strings.NewReader(stream))}}}
				svc := withSchedulerParametersForTest(&GatewayService{cfg: &config.Config{}, httpUpstream: upstream})
				account := &gatewaycapture.ExecutionAccount{Record: accountcore.Record{LoadLocation: time.LoadLocation, ID: 1, Platform: capability.PlatformAnthropic, Type: capability.AccountTypeAPIKey, Credentials: map[string]any{"api_key": "test-key", "base_url": "https://api.anthropic.com"}}}
				c, _ := gin.CreateTestContext(httptest.NewRecorder())
				c.Request = httptest.NewRequest(http.MethodPost, "/v1/"+protocol, nil)
				var result *forwardcore.MessagesResult
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
