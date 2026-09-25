package pricingcontract

import (
	"testing"

	billingpricing "github.com/TokenFlux/TokenRouter/internal/billing/pricing"
	billingtestkit "github.com/TokenFlux/TokenRouter/internal/billing/testkit"

	"github.com/stretchr/testify/require"
)

func TestApplyModelSpecificPricingPolicy_EnforcesOpenAIFastRatios(t *testing.T) {
	t.Parallel()

	svc := billingtestkit.Calculator(0, nil, map[string]*billingpricing.ModelPricing{})

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
	billing := billingtestkit.Calculator(0, newCatalogFixture(catalogFixture{pricingData: catalog}), nil)
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

	svc := billingtestkit.Calculator(0, nil, map[string]*billingpricing.ModelPricing{})
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
