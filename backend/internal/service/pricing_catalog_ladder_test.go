package service

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/TokenFlux/TokenRouter/internal/config"

	"github.com/stretchr/testify/require"
)

func TestParsePricingData_DerivesLongContextFromAboveTierFields(t *testing.T) {
	service := newPricingServiceFixture(pricingServiceFixture{})
	data, err := service.parsePricingData([]byte(`{
		"gpt-above": {"litellm_provider": "openai", "mode": "chat",
			"input_cost_per_token": 5e-06, "output_cost_per_token": 3e-05,
			"input_cost_per_token_above_272k_tokens": 1e-05,
			"output_cost_per_token_above_272k_tokens": 4.5e-05,
			"input_cost_per_token_above_272k_tokens_flex": 5e-06},
		"gemini-above": {"litellm_provider": "vertex_ai-language-models", "mode": "chat",
			"input_cost_per_token": 1.25e-06, "output_cost_per_token": 1e-05,
			"input_cost_per_token_above_200k_tokens": 2.5e-06,
			"output_cost_per_token_above_200k_tokens": 1.5e-05},
		"explicit-wins": {"litellm_provider": "openai", "mode": "chat",
			"input_cost_per_token": 5e-06, "output_cost_per_token": 3e-05,
			"long_context_input_cost_multiplier": 1,
			"input_cost_per_token_above_272k_tokens": 1e-05,
			"output_cost_per_token_above_272k_tokens": 4.5e-05},
		"no-surcharge": {"litellm_provider": "openai", "mode": "chat",
			"input_cost_per_token": 5e-06, "output_cost_per_token": 3e-05,
			"input_cost_per_token_above_272k_tokens": 5e-06,
			"output_cost_per_token_above_272k_tokens": 3e-05},
		"multi-threshold": {"litellm_provider": "openai", "mode": "chat",
			"input_cost_per_token": 1e-06, "output_cost_per_token": 2e-06,
			"input_cost_per_token_above_128k_tokens": 2e-06,
			"input_cost_per_token_above_272k_tokens": 4e-06}
	}`))
	require.NoError(t, err)

	require.Equal(t, 272000, data["gpt-above"].LongContextInputTokenThreshold)
	require.InDelta(t, 2.0, data["gpt-above"].LongContextInputCostMultiplier, 1e-12)
	require.InDelta(t, 1.5, data["gpt-above"].LongContextOutputCostMultiplier, 1e-12)
	require.Equal(t, 200000, data["gemini-above"].LongContextInputTokenThreshold)
	require.Zero(t, data["explicit-wins"].LongContextInputTokenThreshold)
	require.Zero(t, data["no-surcharge"].LongContextInputTokenThreshold)
	require.Equal(t, 128000, data["multi-threshold"].LongContextInputTokenThreshold)
}

func TestGetModelPricing_XAIThresholdInclusive(t *testing.T) {
	service := NewBillingService(&config.Config{}, newStubPricingServiceFromJSON(t, `{
		"grok-4.5": {"litellm_provider": "xai", "mode": "chat",
			"input_cost_per_token": 2e-06, "output_cost_per_token": 6e-06,
			"input_cost_per_token_above_200k_tokens": 4e-06,
			"output_cost_per_token_above_200k_tokens": 1.2e-05}
	}`))
	pricing, err := service.GetModelPricing("grok-4.5")
	require.NoError(t, err)
	require.Equal(t, 200000, pricing.LongContextInputThreshold)
	require.True(t, pricing.LongContextThresholdInclusive)
}

func TestParsePricingData_ExplicitZeroThresholdDisablesLadder(t *testing.T) {
	service := newPricingServiceFixture(pricingServiceFixture{})
	data, err := service.parsePricingData([]byte(`{
		"gpt-5.5": {"litellm_provider": "openai", "mode": "chat",
			"input_cost_per_token": 5e-06, "output_cost_per_token": 3e-05,
			"long_context_input_token_threshold": 0,
			"input_cost_per_token_above_272k_tokens": 1e-05,
			"output_cost_per_token_above_272k_tokens": 4.5e-05}
	}`))
	require.NoError(t, err)
	require.Zero(t, data["gpt-5.5"].LongContextInputTokenThreshold)
	require.Zero(t, data["gpt-5.5"].LongContextInputCostMultiplier)
}

func TestParsePricingData_WarnsOrphanCacheTierFields(t *testing.T) {
	logSink, restore := captureStructuredLog(t)
	defer restore()

	service := newPricingServiceFixture(pricingServiceFixture{})
	data, err := service.parsePricingData([]byte(`{
		"gemini-orphan": {"litellm_provider": "vertex_ai-language-models", "mode": "chat",
			"input_cost_per_token": 1.25e-06, "output_cost_per_token": 1e-05,
			"input_cost_per_token_above_200k_tokens": 2.5e-06,
			"output_cost_per_token_above_200k_tokens": 1.5e-05,
			"cache_creation_input_token_cost_above_200k_tokens": 2.5e-07},
		"gemini-complete": {"litellm_provider": "vertex_ai-language-models", "mode": "chat",
			"input_cost_per_token": 1.25e-06, "output_cost_per_token": 1e-05,
			"cache_creation_input_token_cost": 1.25e-06,
			"input_cost_per_token_above_200k_tokens": 2.5e-06,
			"output_cost_per_token_above_200k_tokens": 1.5e-05,
			"cache_creation_input_token_cost_above_200k_tokens": 2.5e-06},
		"priority-orphan": {"litellm_provider": "openai", "mode": "chat",
			"input_cost_per_token": 5e-06, "output_cost_per_token": 3e-05,
			"cache_creation_input_token_cost_above_272k_tokens_priority": 2.5e-05}
	}`))
	require.NoError(t, err)
	require.Equal(t, 200000, data["gemini-orphan"].LongContextInputTokenThreshold)
	require.True(t, logSink.ContainsMessageAtLevel("gemini-orphan(cache_creation_input_token_cost_above_200k_tokens)", "warn"))
	require.True(t, logSink.ContainsMessage("priority-orphan(cache_creation_input_token_cost_above_272k_tokens_priority)"))
	require.False(t, logSink.ContainsMessage("gemini-complete"))
}

func TestParsePricingData_WarnsLopsidedLongContextLadder(t *testing.T) {
	logSink, restore := captureStructuredLog(t)
	defer restore()

	service := newPricingServiceFixture(pricingServiceFixture{})
	data, err := service.parsePricingData([]byte(`{
		"mixed-versions": {"litellm_provider": "openai", "mode": "chat",
			"input_cost_per_token": 5e-06, "output_cost_per_token": 3e-05,
			"input_cost_per_token_above_272k_tokens": 8e-06,
			"output_cost_per_token_above_272k_tokens": 3e-05},
		"consistent": {"litellm_provider": "openai", "mode": "chat",
			"input_cost_per_token": 4e-06, "output_cost_per_token": 2e-05,
			"input_cost_per_token_above_272k_tokens": 8e-06,
			"output_cost_per_token_above_272k_tokens": 3e-05}
	}`))
	require.NoError(t, err)
	require.Equal(t, 272000, data["mixed-versions"].LongContextInputTokenThreshold)
	require.True(t, logSink.ContainsMessageAtLevel("mixed-versions(input x1.60, output x1.00)", "warn"))
	require.False(t, logSink.ContainsMessage("consistent"))
}

func TestDefaultCatalogSnapshot_CacheTierContract(t *testing.T) {
	logSink, restore := captureStructuredLog(t)
	defer restore()

	body, err := os.ReadFile(filepath.Join("..", "..", "resources", "model-pricing", "model_prices_and_context_window.json"))
	require.NoError(t, err)
	var rawEntries map[string]json.RawMessage
	require.NoError(t, json.Unmarshal(body, &rawEntries))
	for name, raw := range rawEntries {
		require.Empty(t, orphanCacheTierFields(raw), "快照条目 %s 带孤儿 cache above 字段", name)
	}

	service := newPricingServiceFixture(pricingServiceFixture{})
	data, err := service.parsePricingData(body)
	require.NoError(t, err)
	require.False(t, logSink.ContainsMessage("carry cache above-tier prices"))
	require.False(t, logSink.ContainsMessage("one-sided long-context ladder"))
	for _, model := range []string{
		"gemini-2.5-pro", "gemini-3-pro-preview", "gemini-3.1-pro-preview",
		"gemini-3.1-pro-high", "gemini-3.1-pro-low", "gemini-3.1-pro-preview-customtools",
	} {
		pricing := data[model]
		require.NotNil(t, pricing, model)
		require.InDelta(t, pricing.InputCostPerToken, pricing.CacheCreationInputTokenCost, 1e-15, model)
		require.Equal(t, 200000, pricing.LongContextInputTokenThreshold, model)
		if pricing.InputCostPerTokenPriority > 0 {
			require.InDelta(t, pricing.InputCostPerTokenPriority, pricing.CacheCreationInputTokenCostPriority, 1e-15, model)
		}
	}
}

func TestCalculateCost_PartialLongContextMultiplierDefaultsToOne(t *testing.T) {
	tokens := UsageTokens{InputTokens: 300000, OutputTokens: 1000, CacheReadTokens: 10000}

	t.Run("only input multiplier", func(t *testing.T) {
		service := NewBillingService(&config.Config{}, newStubPricingServiceFromJSON(t, `{
			"partial-in": {"litellm_provider": "openai", "mode": "chat",
				"input_cost_per_token": 2e-06, "output_cost_per_token": 1e-05,
				"cache_read_input_token_cost": 2e-07,
				"long_context_input_token_threshold": 272000,
				"long_context_input_cost_multiplier": 2.0}
		}`))
		cost, err := service.CalculateCost("partial-in", tokens, 1)
		require.NoError(t, err)
		require.InDelta(t, 300000*2e-6*2, cost.InputCost, 1e-10)
		require.InDelta(t, 1000*1e-5, cost.OutputCost, 1e-10)
		require.InDelta(t, 10000*2e-7*2, cost.CacheReadCost, 1e-10)
	})

	t.Run("only output multiplier", func(t *testing.T) {
		service := NewBillingService(&config.Config{}, newStubPricingServiceFromJSON(t, `{
			"partial-out": {"litellm_provider": "openai", "mode": "chat",
				"input_cost_per_token": 2e-06, "output_cost_per_token": 1e-05,
				"cache_read_input_token_cost": 2e-07,
				"long_context_input_token_threshold": 272000,
				"long_context_output_cost_multiplier": 1.5}
		}`))
		cost, err := service.CalculateCost("partial-out", tokens, 1)
		require.NoError(t, err)
		require.InDelta(t, 300000*2e-6, cost.InputCost, 1e-10)
		require.InDelta(t, 1000*1e-5*1.5, cost.OutputCost, 1e-10)
		require.InDelta(t, 10000*2e-7, cost.CacheReadCost, 1e-10)
	})
}

// 模型广场展示必须与结算路径使用相同的缺省倍率，避免部分覆盖把一侧显示成免费。
func TestDisplayPricing_PartialLongContextMultiplierDefaultsToOne(t *testing.T) {
	service := NewBillingService(&config.Config{}, newStubPricingServiceFromJSON(t, `{
		"partial-display": {"litellm_provider": "openai", "mode": "chat",
			"input_cost_per_token": 2e-06, "output_cost_per_token": 1e-05,
			"long_context_input_token_threshold": 272000,
			"long_context_input_cost_multiplier": 2}
	}`))
	display := service.GetDisplayPricing("partial-display", 1)
	require.Len(t, display.ContextIntervals, 2)
	require.InDelta(t, 4e-6, display.ContextIntervals[1].InputPricePerToken, 1e-12)
	require.InDelta(t, 1e-5, display.ContextIntervals[1].OutputPricePerToken, 1e-12)
}

func TestCalculateCost_ClaudeSonnetCatalogLadderIsDataDriven(t *testing.T) {
	service := NewBillingService(&config.Config{}, newStubPricingServiceFromJSON(t, `{
		"claude-sonnet-4-5": {"litellm_provider": "anthropic", "mode": "chat",
			"input_cost_per_token": 3e-06, "output_cost_per_token": 1.5e-05,
			"cache_read_input_token_cost": 3e-07,
			"input_cost_per_token_above_200k_tokens": 6e-06,
			"output_cost_per_token_above_200k_tokens": 2.25e-05}
	}`))

	pricing, err := service.GetModelPricing("claude-sonnet-4-5")
	require.NoError(t, err)
	require.Equal(t, 200000, pricing.LongContextInputThreshold)
	require.False(t, pricing.LongContextThresholdInclusive)
	cost, err := service.CalculateCost("claude-sonnet-4-5", UsageTokens{InputTokens: 250000, OutputTokens: 1000}, 1)
	require.NoError(t, err)
	require.True(t, cost.LongContextBillingApplied)
	require.InDelta(t, 250000*3e-6*2, cost.InputCost, 1e-10)
	require.InDelta(t, 1000*1.5e-5*1.5, cost.OutputCost, 1e-10)
}
