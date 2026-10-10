package provider

// 本场景检查 service.go、catalog.go 提供的目录价格与 billing 计算器、pricing 解析和价格换算的协作。

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/TokenFlux/TokenRouter/internal/billing"
	billingpricing "github.com/TokenFlux/TokenRouter/internal/billing/pricing"
	"github.com/TokenFlux/TokenRouter/internal/modelcatalog"
)

// gpt56LadderCatalogJSON 为 GPT-5.6 型号配置上下文阶梯价格。
const gpt56LadderCatalogJSON = `{
	"gpt-5.6-sol": {"provider": "openai", "mode": "chat", "cache_write_multiplier": 1.25, "flex_multiplier": 0.5,
		"input_cost_per_token": 5e-06, "input_cost_per_token_priority": 1e-05,
		"output_cost_per_token": 3e-05, "output_cost_per_token_priority": 6e-05,
		"cache_read_input_token_cost": 5e-07, "cache_read_input_token_cost_priority": 1e-06,
		"input_cost_per_token_above_272k_tokens": 1e-05,
		"output_cost_per_token_above_272k_tokens": 4.5e-05,
		"cache_read_input_token_cost_above_272k_tokens": 1e-06},
	"gpt-5.6-terra": {"provider": "openai", "mode": "chat", "cache_write_multiplier": 1.25, "flex_multiplier": 0.5,
		"input_cost_per_token": 2e-06, "input_cost_per_token_priority": 4e-06,
		"output_cost_per_token": 1.2e-05, "output_cost_per_token_priority": 2.4e-05,
		"cache_read_input_token_cost": 2e-07, "cache_read_input_token_cost_priority": 4e-07,
		"input_cost_per_token_above_272k_tokens": 4e-06,
		"output_cost_per_token_above_272k_tokens": 1.8e-05,
		"cache_read_input_token_cost_above_272k_tokens": 4e-07},
	"gpt-5.6-luna": {"provider": "openai", "mode": "chat", "cache_write_multiplier": 1.25, "flex_multiplier": 0.5,
		"input_cost_per_token": 2e-07, "input_cost_per_token_priority": 4e-07,
		"output_cost_per_token": 1.2e-06, "output_cost_per_token_priority": 2.4e-06,
		"cache_read_input_token_cost": 2e-08, "cache_read_input_token_cost_priority": 4e-08,
		"input_cost_per_token_above_272k_tokens": 4e-07,
		"output_cost_per_token_above_272k_tokens": 1.8e-06,
		"cache_read_input_token_cost_above_272k_tokens": 4e-08}
}`

func TestParsePricingData_DerivesLongContextFromAboveTierFields(t *testing.T) {
	data, err := parsePricingFixture([]byte(`{
		"gpt-above": {"provider": "openai", "mode": "chat",
			"input_cost_per_token": 5e-06, "output_cost_per_token": 3e-05,
			"input_cost_per_token_above_272k_tokens": 1e-05,
			"output_cost_per_token_above_272k_tokens": 4.5e-05,
			"input_cost_per_token_above_272k_tokens_flex": 5e-06},
		"gemini-above": {"provider": "vertex_ai-language-models", "mode": "chat",
			"input_cost_per_token": 1.25e-06, "output_cost_per_token": 1e-05,
			"input_cost_per_token_above_200k_tokens": 2.5e-06,
			"output_cost_per_token_above_200k_tokens": 1.5e-05},
		"explicit-wins": {"provider": "openai", "mode": "chat",
			"input_cost_per_token": 5e-06, "output_cost_per_token": 3e-05,
			"long_context_input_cost_multiplier": 1,
			"input_cost_per_token_above_272k_tokens": 1e-05,
			"output_cost_per_token_above_272k_tokens": 4.5e-05},
		"no-surcharge": {"provider": "openai", "mode": "chat",
			"input_cost_per_token": 5e-06, "output_cost_per_token": 3e-05,
			"input_cost_per_token_above_272k_tokens": 5e-06,
			"output_cost_per_token_above_272k_tokens": 3e-05},
		"multi-threshold": {"provider": "openai", "mode": "chat",
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
	service := newBillingFixture(newStubCatalogFromJSON(t, `{
		"grok-4.5": {"provider": "xai", "mode": "chat",
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
	data, err := parsePricingFixture([]byte(`{
		"gpt-5.5": {"provider": "openai", "mode": "chat",
			"input_cost_per_token": 5e-06, "output_cost_per_token": 3e-05,
			"long_context_input_token_threshold": 0,
			"input_cost_per_token_above_272k_tokens": 1e-05,
			"output_cost_per_token_above_272k_tokens": 4.5e-05}
	}`))
	require.NoError(t, err)
	require.Zero(t, data["gpt-5.5"].LongContextInputTokenThreshold)
	require.Zero(t, data["gpt-5.5"].LongContextInputCostMultiplier)
}

func TestCalculateCost_PartialLongContextMultiplierDefaultsToOne(t *testing.T) {
	tokens := billingpricing.UsageTokens{InputTokens: 300000, OutputTokens: 1000, CacheReadTokens: 10000}

	t.Run("only input multiplier", func(t *testing.T) {
		service := newBillingFixture(newStubCatalogFromJSON(t, `{
			"partial-in": {"provider": "openai", "mode": "chat",
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
		service := newBillingFixture(newStubCatalogFromJSON(t, `{
			"partial-out": {"provider": "openai", "mode": "chat",
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

// TestDisplayPricing_PartialLongContextMultiplierDefaultsToOne 检查展示和结算使用相同的缺省倍率，未配置的一侧使用倍率 1。
func TestDisplayPricing_PartialLongContextMultiplierDefaultsToOne(t *testing.T) {
	service := newBillingFixture(newStubCatalogFromJSON(t, `{
		"partial-display": {"provider": "openai", "mode": "chat",
			"input_cost_per_token": 2e-06, "output_cost_per_token": 1e-05,
			"long_context_input_token_threshold": 272000,
			"long_context_input_cost_multiplier": 2}
	}`))
	display := service.DisplayPricing("partial-display", 1)
	require.Len(t, display.ContextIntervals, 2)
	require.InDelta(t, 4e-6, display.ContextIntervals[1].InputPricePerToken, 1e-12)
	require.InDelta(t, 1e-5, display.ContextIntervals[1].OutputPricePerToken, 1e-12)
}

func TestCalculateCost_ClaudeSonnetCatalogLadderIsDataDriven(t *testing.T) {
	service := newBillingFixture(newStubCatalogFromJSON(t, `{
		"claude-sonnet-4-5": {"provider": "anthropic", "mode": "chat",
			"input_cost_per_token": 3e-06, "output_cost_per_token": 1.5e-05,
			"cache_read_input_token_cost": 3e-07,
			"input_cost_per_token_above_200k_tokens": 6e-06,
			"output_cost_per_token_above_200k_tokens": 2.25e-05}
	}`))

	pricing, err := service.GetModelPricing("claude-sonnet-4-5")
	require.NoError(t, err)
	require.Equal(t, 200000, pricing.LongContextInputThreshold)
	require.False(t, pricing.LongContextThresholdInclusive)
	cost, err := service.CalculateCost("claude-sonnet-4-5", billingpricing.UsageTokens{InputTokens: 250000, OutputTokens: 1000}, 1)
	require.NoError(t, err)
	require.True(t, cost.LongContextBillingApplied)
	require.InDelta(t, 250000*3e-6*2, cost.InputCost, 1e-10)
	require.InDelta(t, 1000*1.5e-5*1.5, cost.OutputCost, 1e-10)
}

func TestParsePricingData_ParsesPriorityAndServiceTierFields(t *testing.T) {
	body := []byte(`{
		"gpt-5.4": {
			"input_cost_per_token": 0.0000025,
			"input_cost_per_token_priority": 0.000005,
			"output_cost_per_token": 0.000015,
			"output_cost_per_token_priority": 0.00003,
			"cache_creation_input_token_cost": 0.0000025,
			"cache_creation_input_token_cost_priority": 0.000005,
			"cache_read_input_token_cost": 0.00000025,
			"cache_read_input_token_cost_priority": 0.0000005,
			"long_context_input_token_threshold": 272000,
			"long_context_input_cost_multiplier": 2,
			"long_context_output_cost_multiplier": 1.5,
			"supports_service_tier": true,
			"supports_prompt_caching": true,
			"provider": "openai",
			"mode": "chat"
		}
	}`)

	data, err := parsePricingFixture(body)
	require.NoError(t, err)
	pricing := data["gpt-5.4"]
	require.NotNil(t, pricing)
	require.InDelta(t, 5e-6, pricing.InputCostPerTokenPriority, 1e-12)
	require.InDelta(t, 3e-5, pricing.OutputCostPerTokenPriority, 1e-12)
	require.InDelta(t, 5e-6, pricing.CacheCreationInputTokenCostPriority, 1e-12)
	require.InDelta(t, 5e-7, pricing.CacheReadInputTokenCostPriority, 1e-12)
	require.Equal(t, 272000, pricing.LongContextInputTokenThreshold)
	require.InDelta(t, 2.0, pricing.LongContextInputCostMultiplier, 1e-12)
	require.InDelta(t, 1.5, pricing.LongContextOutputCostMultiplier, 1e-12)
	require.True(t, pricing.SupportsServiceTier)
}

func TestParsePricingData_ParsesImageInputTokenPrice(t *testing.T) {
	pricingSvc := newModelCatalogFixture(modelCatalogFixture{})
	data, err := parsePricingFixture([]byte(`{
		"gpt-image-2": {
			"input_cost_per_token": 0.000005,
			"input_cost_per_image_token": 0.000008,
			"output_cost_per_token": 0.00001,
			"output_cost_per_image_token": 0.00003,
			"provider": "openai",
			"mode": "image_generation"
		}
	}`))
	require.NoError(t, err)
	parsed := data["gpt-image-2"]
	require.NotNil(t, parsed)
	require.InDelta(t, 8e-6, parsed.InputCostPerImageToken, 1e-12)

	setPricingFixtureData(pricingSvc, data)
	billingSvc := newBillingFixture(pricingSvc)
	pricing, err := billingSvc.GetModelPricing("gpt-image-2")
	require.NoError(t, err)
	require.InDelta(t, 8e-6, pricing.ImageInputPricePerToken, 1e-12)
}

func TestBillingService_GPT56UsesLongContextPricingAcrossModelsAndTiers(t *testing.T) {
	models := []struct {
		name               string
		input, cached      float64
		cacheWrite, output float64
	}{
		{name: "gpt-5.6-sol", input: 5e-6, cached: 0.5e-6, cacheWrite: 6.25e-6, output: 30e-6},
		{name: "gpt-5.6-terra", input: 2e-6, cached: 0.2e-6, cacheWrite: 2.5e-6, output: 12e-6},
		{name: "gpt-5.6-luna", input: 0.2e-6, cached: 0.02e-6, cacheWrite: 0.25e-6, output: 1.2e-6},
	}
	tiers := []struct {
		name       string
		priceScale float64
	}{
		{name: "standard", priceScale: 1},
		{name: "priority", priceScale: 2},
		{name: "flex", priceScale: 0.5},
	}
	tokens := billingpricing.UsageTokens{
		InputTokens:         100000,
		CacheCreationTokens: 100000,
		CacheReadTokens:     73000,
		OutputTokens:        10,
	}

	for _, model := range models {
		for _, tier := range tiers {
			t.Run(model.name+"/"+tier.name, func(t *testing.T) {
				svc := newBillingFixture(newStubCatalogFromJSON(t, gpt56LadderCatalogJSON))
				serviceTier := ""
				if tier.name != "standard" {
					serviceTier = tier.name
				}
				cost, err := svc.CalculateCostWithServiceTier(model.name, tokens, 1, serviceTier)
				require.NoError(t, err)
				require.InDelta(t, float64(tokens.InputTokens)*model.input*tier.priceScale*2, cost.InputCost, 1e-12)
				require.InDelta(t, float64(tokens.CacheCreationTokens)*model.cacheWrite*tier.priceScale*2, cost.CacheCreationCost, 1e-12)
				require.InDelta(t, float64(tokens.CacheReadTokens)*model.cached*tier.priceScale*2, cost.CacheReadCost, 1e-12)
				require.InDelta(t, float64(tokens.OutputTokens)*model.output*tier.priceScale*1.5, cost.OutputCost, 1e-12)
			})
		}
	}
}

func TestBillingService_GPT56LongContextBoundaryIsExclusive(t *testing.T) {
	svc := newBillingFixture(newStubCatalogFromJSON(t, gpt56LadderCatalogJSON))
	tokens := billingpricing.UsageTokens{InputTokens: 100000, CacheCreationTokens: 100000, CacheReadTokens: 72000, OutputTokens: 10}

	cost, err := svc.CalculateCost("gpt-5.6-sol", tokens, 1)
	require.NoError(t, err)
	require.InDelta(t, 100000*5e-6, cost.InputCost, 1e-12)
	require.InDelta(t, 100000*6.25e-6, cost.CacheCreationCost, 1e-12)
	require.InDelta(t, 72000*0.5e-6, cost.CacheReadCost, 1e-12)
	require.InDelta(t, 10*30e-6, cost.OutputCost, 1e-12)
}

func TestDefaultPricingIncludesModelsDevGPT56Rates(t *testing.T) {
	pricingSvc := newOfflinePricingFixture(t)
	billingSvc := newBillingFixture(pricingSvc)

	tests := []struct {
		model                                                             string
		input, cached, cacheWrite, output                                 float64
		inputPriority, cachedPriority, cacheWritePriority, outputPriority float64
	}{
		{model: "gpt-5.6-sol", input: 4e-6, cached: 0.4e-6, cacheWrite: 5e-6, output: 20e-6, inputPriority: 8e-6, cachedPriority: 0.8e-6, cacheWritePriority: 10e-6, outputPriority: 40e-6},
		{model: "gpt-5.6-terra", input: 2e-6, cached: 0.2e-6, cacheWrite: 2.5e-6, output: 12e-6, inputPriority: 4e-6, cachedPriority: 0.4e-6, cacheWritePriority: 5e-6, outputPriority: 24e-6},
		{model: "gpt-5.6-luna", input: 0.2e-6, cached: 0.02e-6, cacheWrite: 0.25e-6, output: 1.2e-6, inputPriority: 0.4e-6, cachedPriority: 0.04e-6, cacheWritePriority: 0.5e-6, outputPriority: 2.4e-6},
	}
	for _, tt := range tests {
		t.Run(tt.model, func(t *testing.T) {
			pricing, err := billingSvc.GetModelPricing(tt.model)
			require.NoError(t, err)
			require.InDelta(t, tt.input, pricing.InputPricePerToken, 1e-12)
			require.InDelta(t, tt.cached, pricing.CacheReadPricePerToken, 1e-12)
			require.InDelta(t, tt.cacheWrite, pricing.CacheCreationPricePerToken, 1e-12)
			require.InDelta(t, tt.output, pricing.OutputPricePerToken, 1e-12)
			require.InDelta(t, tt.inputPriority, pricing.InputPricePerTokenPriority, 1e-12)
			require.InDelta(t, tt.cachedPriority, pricing.CacheReadPricePerTokenPriority, 1e-12)
			require.InDelta(t, tt.cacheWritePriority, pricing.CacheCreationPricePerTokenPriority, 1e-12)
			require.InDelta(t, tt.outputPriority, pricing.OutputPricePerTokenPriority, 1e-12)
			require.Len(t, pricing.ContextPrices, 1)
			require.Equal(t, 272000, pricing.ContextPrices[0].Threshold)
			require.InDelta(t, tt.input*2, pricing.ContextPrices[0].Pricing.InputPricePerToken, 1e-12)
			require.InDelta(t, tt.output*1.5, pricing.ContextPrices[0].Pricing.OutputPricePerToken, 1e-12)
		})
	}
}

func TestDefaultPricingIncludesModelsDevGPT6AstraRates(t *testing.T) {
	pricingSvc := newOfflinePricingFixture(t)
	billingSvc := newBillingFixture(pricingSvc)

	pricing, err := billingSvc.GetModelPricing("gpt-6-astra")
	require.NoError(t, err)
	require.InDelta(t, 10e-6, pricing.InputPricePerToken, 1e-12)
	require.InDelta(t, 1e-6, pricing.CacheReadPricePerToken, 1e-12)
	require.InDelta(t, 12.5e-6, pricing.CacheCreationPricePerToken, 1e-12)
	require.InDelta(t, 50e-6, pricing.OutputPricePerToken, 1e-12)
	require.Len(t, pricing.ContextPrices, 1)
	require.Equal(t, 272000, pricing.ContextPrices[0].Threshold)
	require.InDelta(t, 20e-6, pricing.ContextPrices[0].Pricing.InputPricePerToken, 1e-12)
	require.InDelta(t, 75e-6, pricing.ContextPrices[0].Pricing.OutputPricePerToken, 1e-12)
	inputModalities, outputModalities := pricingSvc.GetModelModalities("gpt-6-astra")
	require.Equal(t, []string{"text", "image"}, inputModalities)
	require.Equal(t, []string{"text"}, outputModalities)
}

func TestParsePricingData_KeepsImageOnlyPricing(t *testing.T) {
	body := []byte(`{
		"image-only-model": {
			"output_cost_per_image": 0.034,
			"provider": "vertex_ai-language-models",
			"mode": "image_generation"
		}
	}`)

	data, err := parsePricingFixture(body)
	require.NoError(t, err)
	pricing := data["image-only-model"]
	require.NotNil(t, pricing)
	require.InDelta(t, 0.034, pricing.OutputCostPerImage, 1e-12)
	require.Equal(t, "image_generation", pricing.Mode)
	// 仅有图片价的条目标记为 token 价缺失，token 计费据此拒绝计价。
	require.True(t, pricing.TokenPricingAbsent)
}

func TestBillingService_GetModelPricing_FailsClosedForImageOnlyEntries(t *testing.T) {
	pricingSvc := newModelCatalogFixture(modelCatalogFixture{})
	data, err := parsePricingFixture([]byte(`{
		"imagen-9.0-generate": {
			"output_cost_per_image": 0.04,
			"provider": "vertex_ai-image-models",
			"mode": "image_generation"
		},
		"gemini-image-with-token-price": {
			"input_cost_per_token": 0.0,
			"output_cost_per_token": 0.0,
			"output_cost_per_image": 0.034,
			"provider": "vertex_ai-language-models",
			"mode": "image_generation"
		}
	}`))
	require.NoError(t, err)
	setPricingFixtureData(pricingSvc, data)
	billingSvc := newBillingFixture(pricingSvc)

	// image-only 条目的 token 计费返回 ErrModelPricingUnavailable。
	_, err = billingSvc.GetModelPricing("imagen-9.0-generate")
	require.ErrorIs(t, err, billingpricing.ErrModelPricingUnavailable)

	// token 价配置为 0 的免费条目正常返回。
	pricing, err := billingSvc.GetModelPricing("gemini-image-with-token-price")
	require.NoError(t, err)
	require.Zero(t, pricing.InputPricePerToken)

	// 图片计费读取 image-only 条目的图片单价。
	raw := pricingSvc.GetModelPricing("imagen-9.0-generate")
	require.NotNil(t, raw)
	require.InDelta(t, 0.04, raw.OutputCostPerImage, 1e-12)
}

// TestBillingService_GetDisplayPricing_ChatImageMetadataKeepsTokenMode 验证聊天模型携带按图元数据时仍展示 token 价格。
func TestBillingService_GetDisplayPricing_ChatImageMetadataKeepsTokenMode(t *testing.T) {
	pricingSvc := newModelCatalogFixture(modelCatalogFixture{pricingData: map[string]*billingpricing.CatalogModelPricing{
		"gemini-3.1-pro-high": {
			InputCostPerToken:  2e-6,
			OutputCostPerToken: 12e-6,
			OutputCostPerImage: 0.00012,
			Mode:               "chat",
		},
		"gemini-3.1-flash-image": {
			OutputCostPerImage: 0.0672,
			Mode:               "image_generation",
		},
	}})
	billingSvc := newBillingFixture(pricingSvc)

	// 聊天模型优先展示 token 价格。
	chatPricing := billingSvc.DisplayPricing("gemini-3.1-pro-high", 8)
	require.Equal(t, "token", chatPricing.PricingMode)
	require.Equal(t, "priced", chatPricing.PriceStatus)
	require.InDelta(t, 16e-6, chatPricing.InputPricePerToken, 1e-12)
	require.InDelta(t, 96e-6, chatPricing.OutputPricePerToken, 1e-12)

	// 图片生成模型按图片单价展示。
	imagePricing := billingSvc.DisplayPricing("gemini-3.1-flash-image", 2)
	require.Equal(t, "image", imagePricing.PricingMode)
	require.Equal(t, "priced", imagePricing.PriceStatus)
	require.InDelta(t, 0.1344, imagePricing.ImagePrice1K, 1e-12)
}

// TestBillingService_Gemini35FlashTiersRequireOwnPricing 检查未配置完整档位价格时返回缺价。
func TestBillingService_Gemini35FlashTiersRequireOwnPricing(t *testing.T) {
	svc := newBillingFixture(nil)
	for _, model := range []string{"gemini-3.5-flash-high", "gemini-3.5-flash-low", "gemini-3.5-flash-medium", "gemini-3.5-flash-tiered"} {
		price, err := svc.GetModelPricing(model)
		require.ErrorIs(t, err, billingpricing.ErrModelPricingUnavailable)
		require.Nil(t, price)
	}
}

func TestBillingService_Gemini36FlashTiersRequireOwnPricing(t *testing.T) {
	svc := newBillingFixture(nil)
	for _, model := range []string{"gemini-3.6-flash-high", "gemini-3.6-flash-low", "gemini-3.6-flash-medium", "gemini-3.6-flash-tiered"} {
		price, err := svc.GetModelPricing(model)
		require.ErrorIs(t, err, billingpricing.ErrModelPricingUnavailable)
		require.Nil(t, price)
	}
}

func TestParsePricingData_PreservesPriorityAndServiceTierFields(t *testing.T) {
	raw := map[string]any{
		"gpt-5.4": map[string]any{
			"input_cost_per_token":                 2.5e-6,
			"input_cost_per_token_priority":        5e-6,
			"output_cost_per_token":                15e-6,
			"output_cost_per_token_priority":       30e-6,
			"cache_read_input_token_cost":          0.25e-6,
			"cache_read_input_token_cost_priority": 0.5e-6,
			"supports_service_tier":                true,
			"supports_prompt_caching":              true,
			"provider":                             "openai",
			"mode":                                 "chat",
		},
	}
	body, err := json.Marshal(raw)
	require.NoError(t, err)

	pricingMap, err := parsePricingFixture(body)
	require.NoError(t, err)

	pricing := pricingMap["gpt-5.4"]
	require.NotNil(t, pricing)
	require.InDelta(t, 2.5e-6, pricing.InputCostPerToken, 1e-12)
	require.InDelta(t, 5e-6, pricing.InputCostPerTokenPriority, 1e-12)
	require.InDelta(t, 15e-6, pricing.OutputCostPerToken, 1e-12)
	require.InDelta(t, 30e-6, pricing.OutputCostPerTokenPriority, 1e-12)
	require.InDelta(t, 0.25e-6, pricing.CacheReadInputTokenCost, 1e-12)
	require.InDelta(t, 0.5e-6, pricing.CacheReadInputTokenCostPriority, 1e-12)
	require.True(t, pricing.SupportsServiceTier)
}

func TestParsePricingData_PreservesServiceTierPriorityFields(t *testing.T) {
	pricingData, err := parsePricingFixture([]byte(`{
		"gpt-5.4": {
			"input_cost_per_token": 0.0000025,
			"input_cost_per_token_priority": 0.000005,
			"output_cost_per_token": 0.000015,
			"output_cost_per_token_priority": 0.00003,
			"cache_read_input_token_cost": 0.00000025,
			"cache_read_input_token_cost_priority": 0.0000005,
			"supports_service_tier": true,
			"provider": "openai",
			"mode": "chat"
		}
	}`))
	require.NoError(t, err)

	pricing := pricingData["gpt-5.4"]
	require.NotNil(t, pricing)
	require.InDelta(t, 0.0000025, pricing.InputCostPerToken, 1e-12)
	require.InDelta(t, 0.000005, pricing.InputCostPerTokenPriority, 1e-12)
	require.InDelta(t, 0.000015, pricing.OutputCostPerToken, 1e-12)
	require.InDelta(t, 0.00003, pricing.OutputCostPerTokenPriority, 1e-12)
	require.InDelta(t, 0.00000025, pricing.CacheReadInputTokenCost, 1e-12)
	require.InDelta(t, 0.0000005, pricing.CacheReadInputTokenCostPriority, 1e-12)
	require.True(t, pricing.SupportsServiceTier)
}

func TestParsePricingData_ParsesModalityFields(t *testing.T) {
	data, err := parsePricingFixture([]byte(`{
		"gemini-2.5-flash": {
			"input_cost_per_token": 0.0000003,
			"output_cost_per_token": 0.0000025,
			"provider": "google",
			"mode": "chat",
			"supported_modalities": ["text", "image", "audio", "video"],
			"supported_output_modalities": ["text", "image"],
			"supports_vision": true,
			"supports_audio_input": true
		}
	}`))
	require.NoError(t, err)
	parsed := data["gemini-2.5-flash"]
	require.NotNil(t, parsed)
	require.Equal(t, []string{"text", "image", "audio", "video"}, parsed.SupportedModalities)
	require.Equal(t, []string{"text", "image"}, parsed.SupportedOutputModalities)
	require.True(t, parsed.SupportsVision)
	require.True(t, parsed.SupportsAudioInput)
	require.False(t, parsed.SupportsAudioOutput)
}

// TestEmbeddedSupplementsWithoutResources 验证空工作目录、离线启动及缺失外部文件都保留官方价格。
func TestEmbeddedSupplementsWithoutResources(t *testing.T) {
	t.Chdir(t.TempDir())
	for _, path := range []string{"", "./resources/model-pricing/model_pricing_supplements.json"} {
		service := NewService(Options{DataDir: t.TempDir(), FallbackFile: path}, nil)
		require.NoError(t, service.Initialize())
		calculator := billing.NewCalculator(service, billing.CalculatorOptions{})
		price, err := calculator.DefaultImagePrice("gemini-3-pro-image", "1K")
		require.NoError(t, err)
		require.Equal(t, 0.134, price)
		require.Equal(t, 0.02, calculator.CalculateWebSearchCost(2, nil, 1).ActualCost)
		require.Equal(t, 15.0, *service.BillingDefaults().AudioTTSPricePerMillionChars)
		require.NoError(t, service.ForceUpdate())
		require.Equal(t, 0.01, *service.BillingDefaults().WebSearchPricePerCall)
	}
	_, err := os.Stat("resources")
	require.True(t, os.IsNotExist(err), "加载默认价格不应创建外部资源")
}

func TestModelsCatalogEmbeddingDefaultPricing(t *testing.T) {
	service := NewService(Options{DataDir: t.TempDir()}, nil)
	require.NoError(t, service.Initialize())
	for _, tc := range []struct {
		model string
		price float64
	}{
		{"text-embedding-3-small", 0.02},
		{"text-embedding-3-large", 0.13},
		{"text-embedding-ada-002", 0.1},
	} {
		t.Run(tc.model, func(t *testing.T) {
			value := catalogPriceForTest(t, service, tc.model)
			cost := billingpricing.ComputeTokenBreakdown(value, billingpricing.UsageTokens{InputTokens: 1000000}, 1, "", true)
			require.InDelta(t, tc.price, cost.TotalCost, 1e-12)
		})
	}
}

func TestModelsCatalogMediaSupplementAliases(t *testing.T) {
	dir := t.TempDir()
	supplement := filepath.Join(dir, "supplement.json")
	require.NoError(t, os.WriteFile(supplement, []byte(`{"gpt-image-2":{"input_cost_per_image_token":0.000008,"output_cost_per_image_token":0.00003}}`), 0o600))
	service := NewService(Options{DataDir: dir, RemoteURL: "https://models.dev/catalog.json", FallbackFile: supplement}, &catalogRemoteFixture{body: []byte(mediaAliasFixture)})
	require.NoError(t, service.ForceUpdate())
	for _, model := range []string{"gpt-image-2", "openai/gpt-image-2"} {
		value := catalogPriceForTest(t, service, model)
		cost := billingpricing.ComputeTokenBreakdown(value, billingpricing.UsageTokens{InputTokens: 1000, ImageInputTokens: 1000}, 1, "", true)
		require.InDelta(t, 0.008, cost.TotalCost, 1e-12, model)
		require.Equal(t, "local_supplement", service.GetModelPricing(model).PriceSources["image_input"])
	}
	for _, model := range []string{"openrouter/gpt-image-2", "gpt-image-2-snapshot", "openai/gpt-image-2-snapshot"} {
		require.Zero(t, service.GetModelPricing(model).InputCostPerImageToken, model)
	}
}

// TestModelsCatalogGeminiImageTextPricing 同时验证内嵌目录、媒体补充和实际用量拆分。
func TestModelsCatalogGeminiImageTextPricing(t *testing.T) {
	service := NewService(Options{
		DataDir: t.TempDir(),
	}, nil)
	require.NoError(t, service.Initialize())
	for _, tc := range []struct {
		model                 string
		textPrice, imagePrice float64
	}{
		{"gemini-3-pro-image", 12e-6, 120e-6},
		{"gemini-2.5-flash-image", 2.5e-6, 30e-6},
		{"gemini-3.1-flash-image", 3e-6, 60e-6},
		{"gemini-3.1-flash-lite-image", 1.5e-6, 30e-6},
	} {
		for _, model := range []string{tc.model, "google/" + tc.model} {
			t.Run(model, func(t *testing.T) {
				value := catalogPriceForTest(t, service, model)
				cost := billingpricing.ComputeTokenBreakdown(value, billingpricing.UsageTokens{OutputTokens: 2000, ImageOutputTokens: 1000}, 1, "", true)
				require.InDelta(t, 1000*tc.textPrice, cost.OutputCost, 1e-12)
				require.InDelta(t, 1000*tc.imagePrice, cost.ImageOutputCost, 1e-12)
				display := billingpricing.BuildTokenDisplayPricing(value, 1)
				require.InDelta(t, tc.textPrice, display.OutputPricePerToken, 1e-12)
				require.InDelta(t, tc.imagePrice, display.ImageOutputPricePerToken, 1e-12)
				require.Equal(t, "local_supplement", service.GetModelPricing(model).PriceSources["output"])
			})
		}
	}
}

func TestModelsCatalogGeminiImageSupplementPrecedence(t *testing.T) {
	dir := t.TempDir()
	supplement := filepath.Join(dir, "supplement.json")
	const fixture = `{"providers":{
		"google":{"models":{"gemini-image-test":{"cost":{"input":2,"output":150},"modalities":{"output":["text","image"]}}}},
		"openrouter":{"models":{"gemini-image-test":{"cost":{"input":1,"output":9},"modalities":{"output":["text","image"]}}}}
	}}`
	service := NewService(Options{
		DataDir:      dir,
		RemoteURL:    "https://models.dev/catalog.json",
		FallbackFile: supplement,
	}, &catalogRemoteFixture{body: []byte(fixture)})
	require.NoError(t, service.ForceUpdate())
	// 缺少文本费率时，文本报价为未定价。
	_, err := billingpricing.ResolveModelPricing("gemini-image-test", service.GetModelPricing("gemini-image-test"))
	require.ErrorIs(t, err, billingpricing.ErrModelPricingUnavailable)
	require.InDelta(t, 150e-6, service.GetModelPricing("gemini-image-test").OutputCostPerImageToken, 1e-12)
	require.NoError(t, os.WriteFile(supplement, []byte(`{"gemini-image-test":{"output_cost_per_token":0,"output_cost_per_image_token":0.00012}}`), 0o600))
	require.NoError(t, service.ForceUpdate())
	for _, model := range []string{"gemini-image-test", "google/gemini-image-test"} {
		value := catalogPriceForTest(t, service, model)
		require.Zero(t, value.OutputPricePerToken)
		require.InDelta(t, 150e-6, value.ImageOutputPricePerToken, 1e-12)
	}
	// 文本补充按原厂模型名匹配。
	require.InDelta(t, 9e-6, catalogPriceForTest(t, service, "openrouter/gemini-image-test").OutputPricePerToken, 1e-12)
}

func TestModelsCatalogImagePriceOverrideAcrossContextTiers(t *testing.T) {
	service := NewService(Options{DataDir: t.TempDir()}, nil)
	require.NoError(t, service.Initialize())
	base := catalogPriceForTest(t, service, "gpt-5.4")
	require.NotEmpty(t, base.ContextPrices)
	imagePrice := 1e-6
	resolved := billingpricing.ResolvePriceCards(&billingpricing.ModelPricingEntry{ImageInputPrice: &imagePrice}, base, billingpricing.PricingSourceCatalog, true)
	for _, input := range []int{10000, 272000, 272001, 300000} {
		cost, err := billingpricing.CalculateTokenCost(resolved, billingpricing.CostInput{
			Model:          "gpt-5.4",
			Tokens:         billingpricing.UsageTokens{InputTokens: input, ImageInputTokens: 1000},
			RateMultiplier: 1,
		})
		require.NoError(t, err)
		require.InDelta(t, 0.001, cost.ImageInputCost, 1e-12, "input=%d", input)
	}
}

// TestModelsCatalogGrokInclusiveContextBoundary 覆盖实际目录的阈值前、阈值处及缓存混合输入。
func TestModelsCatalogGrokInclusiveContextBoundary(t *testing.T) {
	service := NewService(Options{DataDir: t.TempDir()}, nil)
	require.NoError(t, service.Initialize())
	for _, model := range []string{"grok-4.6", "xai/grok-4.6"} {
		t.Run(model, func(t *testing.T) {
			base := catalogPriceForTest(t, service, model)
			for _, tc := range []struct {
				tokens billingpricing.UsageTokens
				want   float64
				long   bool
			}{
				{billingpricing.UsageTokens{InputTokens: 199999, OutputTokens: 1000}, 0.405998, false},
				{billingpricing.UsageTokens{InputTokens: 200000, OutputTokens: 1000}, 0.812, true},
				{billingpricing.UsageTokens{InputTokens: 200001, OutputTokens: 1000}, 0.812004, true},
				{billingpricing.UsageTokens{InputTokens: 100000, CacheReadTokens: 100000, OutputTokens: 1000}, 0.512, true},
			} {
				cost := billingpricing.ComputeTokenBreakdown(base, tc.tokens, 1, "", true)
				require.InDelta(t, tc.want, cost.TotalCost, 1e-12)
				require.Equal(t, tc.long, cost.LongContextBillingApplied)
			}
			intervals := billingpricing.LongContextDisplayPricingIntervals(base, 1)
			require.Len(t, intervals, 2)
			require.Equal(t, 199999, *intervals[0].MaxTokens)
			require.Equal(t, 199999, intervals[1].MinTokens)
			cost := billingpricing.ComputeTokenBreakdown(base, billingpricing.UsageTokens{InputTokens: 200000, OutputTokens: 1000}, 1, "", false)
			require.InDelta(t, 0.406, cost.TotalCost, 1e-12)
		})
	}
}

func TestModelsCatalogExplicitZeroImageOutput(t *testing.T) {
	dir := t.TempDir()
	supplement := filepath.Join(dir, "supplement.json")
	require.NoError(t, os.WriteFile(supplement, []byte(`{"gemini-image-test":{"output_cost_per_token":0.000012}}`), 0o600))
	remote := &catalogRemoteFixture{body: []byte(`{"providers":{"google":{"models":{"gemini-image-test":{"cost":{"input":2,"output":0},"modalities":{"output":["text","image"]}}}}}}`)}
	service := NewService(Options{
		DataDir:      dir,
		RemoteURL:    "https://models.dev/catalog.json",
		FallbackFile: supplement,
	}, remote)
	require.NoError(t, service.ForceUpdate())
	base := catalogPriceForTest(t, service, "gemini-image-test")
	cost := billingpricing.ComputeTokenBreakdown(base, billingpricing.UsageTokens{OutputTokens: 2000, ImageOutputTokens: 1000}, 1, "", true)
	require.InDelta(t, 0.012, cost.OutputCost, 1e-12)
	require.Zero(t, cost.ImageOutputCost)
	require.True(t, base.ImageOutputPriceExplicit)
}

// TestSupplementRulesPublishAtomically 覆盖目录优先、独立单位、操作默认价及失败保留。
func TestSupplementRulesPublishAtomically(t *testing.T) {
	dir := t.TempDir()
	supplement := filepath.Join(dir, "supplement.json")
	body := `{"claude-test":{"input_cost_per_token":99,"cache_write_1h_multiplier":2,"fast_multiplier":3,"flex_multiplier":0.25,"max_reasoning_effort_multiplier":4,"image_prices":{"1K":0,"2K":0.7},"video_prices":{"720p":0.2},"time_pricing":{"timezone":"UTC","periods":[{"start_time":"01:00","end_time":"03:00","multiplier":2}]}},"_billing_defaults":{"web_search_price_per_call":0.25,"audio_tts_price_per_million_chars":10}}`
	require.NoError(t, os.WriteFile(supplement, []byte(body), 0o600))
	remote := &catalogRemoteFixture{body: []byte(modelsCatalogFixture), etag: "v1"}
	service := NewService(Options{DataDir: dir, RemoteURL: "https://models.dev/catalog.json", FallbackFile: supplement}, remote)
	require.NoError(t, service.ForceUpdate())
	raw := service.GetModelPricing("claude-test")
	require.InDelta(t, 3e-6, raw.InputCostPerToken, 1e-12)
	require.InDelta(t, 6e-6, raw.CacheCreationInputTokenCostAbove1hr, 1e-12)
	require.InDelta(t, 12e-6, raw.ContextPrices[0].Pricing.CacheCreationInputTokenCostAbove1hr, 1e-12)
	require.Equal(t, "rule_supplement", raw.PriceSources["cache_write_1h"])
	require.Equal(t, "local_supplement", raw.PriceSources["image_prices"])
	require.Nil(t, service.GetModelPricing(billingpricing.BillingDefaultsKey))
	require.NotContains(t, service.Snapshot().Data, billingpricing.BillingDefaultsKey)
	require.NotContains(t, service.ListModelNamesByProvider(""), billingpricing.BillingDefaultsKey)
	require.Equal(t, service.GetModelPricing("anthropic/claude-test").ImagePrices, raw.ImagePrices)
	calc := billing.NewCalculator(service, billing.CalculatorOptions{Now: func() time.Time { return time.Date(2026, 10, 1, 2, 0, 0, 0, time.UTC) }})
	unit, err := calc.DefaultImagePrice("claude-test", "1K")
	require.NoError(t, err)
	require.Zero(t, unit)
	_, err = calc.DefaultImagePrice("claude-test", "4K")
	require.ErrorIs(t, err, billingpricing.ErrModelPricingUnavailable)
	unit, err = calc.DefaultVideoPrice("claude-test", "720p")
	require.NoError(t, err)
	require.Equal(t, 0.2, unit)
	quote := calc.DefaultModelPrice("claude-test", "anthropic", "video")
	require.Equal(t, "priced", quote.PriceStatus)
	require.Len(t, quote.Prices, 1)
	require.Equal(t, "720p", quote.Prices[0].Key)
	require.Equal(t, 0.5, calc.CalculateWebSearchCost(2, nil, 1).ActualCost)
	zero := 0.0
	require.Zero(t, calc.CalculateWebSearchCost(2, &zero, 1).ActualCost)
	// 无长上下文和自定义价卡时，时段与推理倍率取自同一目录数据。
	resolver := billing.NewPriceResolver(nil, calc, nil, nil)
	cost, err := calc.CalculateCostUnified(billing.CostInput{Ctx: context.Background(), Model: "claude-test", Tokens: billingpricing.UsageTokens{InputTokens: 10}, RateMultiplier: 1, ServiceTier: "priority", ReasoningEffort: "max", Resolver: resolver})
	require.NoError(t, err)
	require.InDelta(t, 10*3e-6*3*4*2, cost.ActualCost, 1e-12)
	before := service.Snapshot()
	require.NoError(t, os.WriteFile(supplement, []byte(`{"claude-test":{"image_prices":{"1K":-1}},"_billing_defaults":{"web_search_price_per_call":2}}`), 0o600))
	require.Error(t, service.ForceUpdate())
	require.Equal(t, before.Data, service.Snapshot().Data)
	require.Equal(t, before.BillingDefaults, service.BillingDefaults())
	require.Equal(t, before.LastUpdated, service.Snapshot().LastUpdated)
	// 304 时仍重读补充文件，配置为零的价格和已删除字段同时生效。
	require.NoError(t, os.WriteFile(supplement, []byte(`{"claude-test":{"cache_creation_input_token_cost_above_1hr":0,"image_prices":{"1K":0}},"_billing_defaults":{"audio_tts_price_per_million_chars":0}}`), 0o600))
	remote.unchanged = true
	require.NoError(t, service.syncWithRemote())
	require.Equal(t, 0.01, *service.BillingDefaults().WebSearchPricePerCall)
	require.Zero(t, *service.BillingDefaults().AudioTTSPricePerMillionChars)
	require.Zero(t, service.GetModelPricing("claude-test").CacheCreationInputTokenCostAbove1hr)
	require.NoError(t, os.Remove(supplement))
	require.NoError(t, service.syncWithRemote())
	require.Empty(t, service.GetModelPricing("claude-test").ImagePrices)
	require.Equal(t, 0.01, *service.BillingDefaults().WebSearchPricePerCall)
	// 发布后的快照可独立修改。
	snap := before
	snap.BillingDefaults.WebSearchPricePerCall = new(float64)
	snap.Data["claude-test"].ImagePrices["1K"] = 99
	require.Empty(t, service.GetModelPricing("claude-test").ImagePrices)
}

// TestShippedSupplementsAreMinimalAndDocumented 验证分发数据没有混入旧展示字段。
func TestShippedSupplementsAreMinimalAndDocumented(t *testing.T) {
	body := modelcatalog.Supplements()
	var records map[string]map[string]json.RawMessage
	require.NoError(t, json.Unmarshal(body, &records))
	for model, fields := range records {
		if model == billingpricing.BillingDefaultsKey {
			require.Contains(t, fields, "sources")
			continue
		}
		require.Contains(t, fields, "source_url", model)
		require.Contains(t, fields, "verified_at", model)
		for _, field := range []string{"max_input_tokens", "max_output_tokens", "max_tokens", "supports_vision", "supported_endpoints", "supported_modalities"} {
			require.NotContains(t, fields, field, model)
		}
	}
}

// TestModelRulesReturnIndependentValues 防止调用方修改可空倍率或分时数组污染已发布目录。
func TestModelRulesReturnIndependentValues(t *testing.T) {
	entries, diagnostics, err := billingpricing.ParsePricingEntries(map[string]json.RawMessage{"model": json.RawMessage(`{"input_cost_per_token":0.000001,"output_cost_per_token":0.000002,"cache_write_multiplier":1.25,"cache_write_1h_multiplier":2,"fast_multiplier":3,"flex_multiplier":0.5,"max_reasoning_effort_multiplier":4,"time_pricing":{"timezone":"UTC","periods":[{"start_time":"01:00","end_time":"03:00","multiplier":2}]}}`)})
	require.NoError(t, err)
	require.NoError(t, diagnostics.ValidationError())
	service := NewServiceFromSnapshot(Options{}, nil, Snapshot{Data: entries})
	calc := billing.NewCalculator(service, billing.CalculatorOptions{})
	first, err := calc.GetModelPricing("model")
	require.NoError(t, err)
	*first.FastMultiplier = 99
	first.TimePricing.Periods[0].Multiplier = 99
	second, err := calc.GetModelPricing("model")
	require.NoError(t, err)
	require.Equal(t, 3.0, *second.FastMultiplier)
	require.Equal(t, 2.0, second.TimePricing.Periods[0].Multiplier)
	require.InDelta(t, 1.25e-6, second.CacheCreation5mPrice, 1e-12)
	require.InDelta(t, 2e-6, second.CacheCreation1hPrice, 1e-12)
}

// TestJevOfflineCatalogPricesKeepProviderIdentity 检查 TypeSafe 直连与渠道免费型号各自查价。
func TestJevOfflineCatalogPricesKeepProviderIdentity(t *testing.T) {
	service := newOfflinePricingFixture(t)
	for _, model := range []string{"jev-latest", "jev-preview", "jev-1.13.0"} {
		p := service.GetModelPricing(model)
		require.NotNil(t, p, model)
		require.InDelta(t, 4.2e-8, p.InputCostPerToken, 1e-15)
		require.Zero(t, p.OutputCostPerToken)
		require.Zero(t, p.CacheReadInputTokenCost)
		require.Equal(t, "local_supplement", p.PriceSources["input"])
		attributes := service.ModelAttributes(model)
		require.NotNil(t, attributes.InputModalities)
		require.NotNil(t, attributes.OutputModalities)
		require.Equal(t, []string{"text"}, *attributes.InputModalities)
		require.Equal(t, []string{"text"}, *attributes.OutputModalities)
	}
	free := service.GetModelPricing("opencode/jev-1.13-free")
	require.NotNil(t, free)
	require.Zero(t, free.InputCostPerToken)
	require.Zero(t, free.OutputCostPerToken)
	attributes := service.ModelAttributes("jev-latest")
	require.NotNil(t, attributes.DisplayName)
	require.Equal(t, "Jev", *attributes.DisplayName)
	require.Equal(t, 64000, *attributes.Context)
	require.Nil(t, service.GetModelPricing("jev-unknown-version"))
}

// TestJevCatalogPricesOverrideSupplement 检查原厂目录报价和零价优先，渠道报价各自生效。
func TestJevCatalogPricesOverrideSupplement(t *testing.T) {
	remote := &catalogRemoteFixture{body: []byte(`{"providers":{"typesafe":{"models":{"jev-latest":{"cost":{"input":0.084,"output":0}},"jev-preview":{"cost":{"input":0,"output":0}}}},"relay":{"models":{"jev-preview":{"cost":{"input":5,"output":2}}}}}}`)}
	service := NewService(Options{DataDir: t.TempDir(), RemoteURL: "https://models.dev/catalog.json?type=all"}, remote)
	require.NoError(t, service.ForceUpdate())
	require.InDelta(t, 8.4e-8, service.GetModelPricing("jev-latest").InputCostPerToken, 1e-15)
	require.Equal(t, "models.dev", service.GetModelPricing("jev-latest").Source)
	require.NotContains(t, service.GetModelPricing("jev-latest").PriceSources, "input")
	require.Zero(t, service.GetModelPricing("jev-preview").InputCostPerToken)
	require.InDelta(t, 5e-6, service.GetModelPricing("relay/jev-preview").InputCostPerToken, 1e-15)
}

// TestUnifiedModelSupplementPublishesPriceAndAttributes 检查同一补充文件的属性和价格一起发布，坏属性拒绝整次更新。
func TestUnifiedModelSupplementPublishesPriceAndAttributes(t *testing.T) {
	path := filepath.Join(t.TempDir(), "models.json")
	require.NoError(t, os.WriteFile(path, []byte(`{"jev-preview":{"provider":"typesafe","input_cost_per_token":0.0000001,"output_cost_per_token":0,"attributes":{"display_name":"Custom preview","context":128000,"input_modalities":["text"]}}}`), 0o600))
	service := NewService(Options{DataDir: t.TempDir(), FallbackFile: path}, nil)
	require.NoError(t, service.Initialize())
	defer service.Stop()
	require.InDelta(t, 1e-7, service.GetModelPricing("jev-preview").InputCostPerToken, 1e-15)
	attrs := service.ModelAttributes("jev-preview")
	require.Equal(t, "Custom preview", *attrs.DisplayName)
	require.Equal(t, 128000, *attrs.Context)
	require.Equal(t, []string{"text"}, *attrs.OutputModalities)
	require.NoError(t, os.WriteFile(path, []byte(`{"jev-preview":{"input_cost_per_token":2,"attributes":{"input_modalities":["invalid"]}}}`), 0o600))
	require.Error(t, service.ForceUpdate())
	require.InDelta(t, 1e-7, service.GetModelPricing("jev-preview").InputCostPerToken, 1e-15)
	require.Equal(t, "Custom preview", *service.ModelAttributes("jev-preview").DisplayName)
}
