//go:build unit

package pricingcontract

import (
	"context"
	"testing"
	"time"

	purepricing "github.com/TokenFlux/TokenRouter/internal/billing/pricing"

	"github.com/TokenFlux/TokenRouter/internal/billing"

	"github.com/TokenFlux/TokenRouter/internal/config"
	"github.com/TokenFlux/TokenRouter/internal/routing"
	"github.com/TokenFlux/TokenRouter/internal/routing/capability"
	routingtestkit "github.com/TokenFlux/TokenRouter/internal/routing/testkit"
	"github.com/TokenFlux/TokenRouter/internal/usage"
	"github.com/stretchr/testify/require"
)

// ---------------------------------------------------------------------------
// matchAccountStatsRule
// ---------------------------------------------------------------------------

// ---------------------------------------------------------------------------
// findPricingForModel
// ---------------------------------------------------------------------------

// ---------------------------------------------------------------------------
// calculateStatsCost
// ---------------------------------------------------------------------------

// ---------------------------------------------------------------------------
// tryCustomRules — 多规则顺序测试
// ---------------------------------------------------------------------------

// ---------------------------------------------------------------------------
// tryModelFilePricing
// ---------------------------------------------------------------------------

// newTestBillingServiceWithPrices creates a BillingService with pre-populated
// fallback prices for testing. No config or pricing service is needed.
// The key must match what getFallbackPricing resolves to for a given model name.
// E.g., model "claude-sonnet-4" resolves to key "claude-sonnet-4".
func newTestBillingServiceWithPrices(prices map[string]*purepricing.ModelPricing) *billing.Calculator {
	return newCalculatorWithPrices(nil, nil, prices)
}

func TestTryModelFilePricing_Success(t *testing.T) {
	bs := newTestBillingServiceWithPrices(map[string]*purepricing.ModelPricing{
		"claude-sonnet-4": {
			InputPricePerToken:  0.001,
			OutputPricePerToken: 0.002,
		},
	})
	tokens := purepricing.UsageTokens{InputTokens: 100, OutputTokens: 50}
	result := bs.ModelFileStatsCost("claude-sonnet-4", tokens, "", "")
	require.NotNil(t, result)
	// 100*0.001 + 50*0.002 = 0.1 + 0.1 = 0.2
	require.InDelta(t, 0.2, *result, 1e-12)
}

func TestTryModelFilePricing_AppliesLongContextPricing(t *testing.T) {
	bs := newTestBillingServiceWithPrices(map[string]*purepricing.ModelPricing{
		"gpt-5.6-sol": {
			InputPricePerToken:          0.001,
			OutputPricePerToken:         0.002,
			CacheReadPricePerToken:      0.0001,
			LongContextInputThreshold:   100,
			LongContextInputMultiplier:  2,
			LongContextOutputMultiplier: 1.5,
		},
	})
	tokens := purepricing.UsageTokens{InputTokens: 101, OutputTokens: 10, CacheReadTokens: 5}

	result := bs.ModelFileStatsCost("gpt-5.6-sol", tokens, "", "")

	require.NotNil(t, result)
	// 输入和缓存读取使用 2 倍输入档位，输出使用 1.5 倍档位。
	require.InDelta(t, 0.233, *result, 1e-12)
}

func TestTryModelFilePricing_AppliesServiceTierPricing(t *testing.T) {
	bs := newTestBillingServiceWithPrices(map[string]*purepricing.ModelPricing{
		"gpt-5.6-sol": {
			InputPricePerToken:                 0.001,
			InputPricePerTokenPriority:         0.002,
			OutputPricePerToken:                0.002,
			OutputPricePerTokenPriority:        0.004,
			CacheCreationPricePerToken:         0.003,
			CacheCreationPricePerTokenPriority: 0.006,
			CacheReadPricePerToken:             0.0005,
			CacheReadPricePerTokenPriority:     0.001,
		},
	})
	tokens := purepricing.UsageTokens{
		InputTokens:         100,
		OutputTokens:        50,
		CacheCreationTokens: 20,
		CacheReadTokens:     10,
	}

	tests := []struct {
		name        string
		serviceTier string
		want        float64
	}{
		{name: "standard", serviceTier: "", want: 0.265},
		{name: "priority", serviceTier: "priority", want: 0.53},
		{name: "flex", serviceTier: "flex", want: 0.1325},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := bs.ModelFileStatsCost("gpt-5.6-sol", tokens, tt.serviceTier, "")
			require.NotNil(t, result)
			require.InDelta(t, tt.want, *result, 1e-12)
		})
	}
}

func TestTryModelFilePricing_CombinesPriorityAndLongContextPricing(t *testing.T) {
	bs := newTestBillingServiceWithPrices(map[string]*purepricing.ModelPricing{
		"gpt-5.6-sol": {
			InputPricePerToken:                 0.001,
			InputPricePerTokenPriority:         0.002,
			OutputPricePerToken:                0.002,
			OutputPricePerTokenPriority:        0.004,
			CacheCreationPricePerToken:         0.003,
			CacheCreationPricePerTokenPriority: 0.006,
			CacheReadPricePerToken:             0.0005,
			CacheReadPricePerTokenPriority:     0.001,
			LongContextInputThreshold:          100,
			LongContextInputMultiplier:         2,
			LongContextOutputMultiplier:        1.5,
		},
	})
	tokens := purepricing.UsageTokens{
		InputTokens:         101,
		OutputTokens:        10,
		CacheCreationTokens: 5,
		CacheReadTokens:     5,
	}

	result := bs.ModelFileStatsCost("gpt-5.6-sol", tokens, "priority", "")

	require.NotNil(t, result)
	// priority 单价先应用，再叠加长上下文输入 2 倍、输出 1.5 倍。
	require.InDelta(t, 0.534, *result, 1e-12)
}

func TestTryModelFilePricing_PricingNotFound(t *testing.T) {
	// "nonexistent-model" does not match any fallback pattern
	bs := newTestBillingServiceWithPrices(map[string]*purepricing.ModelPricing{})
	tokens := purepricing.UsageTokens{InputTokens: 100, OutputTokens: 50}
	result := bs.ModelFileStatsCost("nonexistent-model", tokens, "", "")
	require.Nil(t, result)
}

func TestTryModelFilePricing_NilFallback(t *testing.T) {
	// getFallbackPricing returns nil when key maps to nil
	bs := newTestBillingServiceWithPrices(map[string]*purepricing.ModelPricing{
		"claude-sonnet-4": nil,
	})
	tokens := purepricing.UsageTokens{InputTokens: 100}
	result := bs.ModelFileStatsCost("claude-sonnet-4", tokens, "", "")
	require.Nil(t, result)
}

func TestTryModelFilePricing_ZeroCost(t *testing.T) {
	bs := newTestBillingServiceWithPrices(map[string]*purepricing.ModelPricing{
		"claude-sonnet-4": {
			InputPricePerToken:  0.001,
			OutputPricePerToken: 0.002,
		},
	})
	tokens := purepricing.UsageTokens{} // all zero tokens → cost = 0 → nil
	result := bs.ModelFileStatsCost("claude-sonnet-4", tokens, "", "")
	require.Nil(t, result)
}

func TestTryModelFilePricing_WithImageOutput(t *testing.T) {
	bs := newTestBillingServiceWithPrices(map[string]*purepricing.ModelPricing{
		"claude-sonnet-4": {
			InputPricePerToken:       0.001,
			OutputPricePerToken:      0.002,
			ImageOutputPricePerToken: 0.01,
		},
	})
	tokens := purepricing.UsageTokens{
		InputTokens:       100,
		OutputTokens:      50,
		ImageOutputTokens: 10,
	}
	result := bs.ModelFileStatsCost("claude-sonnet-4", tokens, "", "")
	require.NotNil(t, result)
	// ImageOutputTokens 是 OutputTokens 的子集，先扣除再按图片单价计。
	// 100*0.001 + (50-10)*0.002 + 10*0.01 = 0.1 + 0.08 + 0.1 = 0.28
	require.InDelta(t, 0.28, *result, 1e-12)
}

func TestTryModelFilePricing_WithCacheTokens(t *testing.T) {
	bs := newTestBillingServiceWithPrices(map[string]*purepricing.ModelPricing{
		"claude-sonnet-4": {
			InputPricePerToken:         0.001,
			OutputPricePerToken:        0.002,
			CacheCreationPricePerToken: 0.003,
			CacheReadPricePerToken:     0.0005,
		},
	})
	tokens := purepricing.UsageTokens{
		InputTokens:         100,
		OutputTokens:        50,
		CacheCreationTokens: 200,
		CacheReadTokens:     300,
	}
	result := bs.ModelFileStatsCost("claude-sonnet-4", tokens, "", "")
	require.NotNil(t, result)
	// 100*0.001 + 50*0.002 + 200*0.003 + 300*0.0005
	// = 0.1 + 0.1 + 0.6 + 0.15 = 0.95
	require.InDelta(t, 0.95, *result, 1e-12)
}

// ---------------------------------------------------------------------------
// contractAccountStatsCost — integration tests covering the 4-level priority chain
// ---------------------------------------------------------------------------

func TestResolveAccountStatsCost_NilPricingConfigService(t *testing.T) {
	result := contractAccountStatsCost(
		context.Background(),
		nil, // channelService is nil
		newTestBillingServiceWithPrices(map[string]*purepricing.ModelPricing{}),
		1, 1, "claude-sonnet-4", "",
		purepricing.UsageTokens{InputTokens: 100}, 1, 0.5, "",
	)
	require.Nil(t, result)
}

func TestResolveAccountStatsCost_EmptyUpstreamModel(t *testing.T) {
	cs := newTestPricingConfigServiceForStats(t, &routingtestkit.Configuration{
		ID:     1,
		Status: billing.StatusActive,
	}, 1, "")

	result := contractAccountStatsCost(
		context.Background(),
		cs,
		newTestBillingServiceWithPrices(map[string]*purepricing.ModelPricing{}),
		1, 1, "", "", // empty upstream model
		purepricing.UsageTokens{InputTokens: 100}, 1, 0.5, "",
	)
	require.Nil(t, result)
}

func TestResolveAccountStatsCost_GetPricingConfigForGroupReturnsNil(t *testing.T) {
	// Group 99 is NOT in the cache, so GetPricingConfigForGroup returns nil
	cs := newTestPricingConfigServiceForStats(t, &routingtestkit.Configuration{
		ID:     1,
		Status: billing.StatusActive,
	}, 1, "")

	result := contractAccountStatsCost(
		context.Background(),
		cs,
		newTestBillingServiceWithPrices(map[string]*purepricing.ModelPricing{}),
		1, 99, "claude-sonnet-4", "", // groupID 99 has no channel
		purepricing.UsageTokens{InputTokens: 100}, 1, 0.5, "",
	)
	require.Nil(t, result)
}

func TestResolveAccountStatsCost_HitsCustomRule(t *testing.T) {
	pricingConfig := &routingtestkit.Configuration{
		ID:     1,
		Status: billing.StatusActive,
		AccountStatsPricingRules: []routing.AccountStatsPricingRule{
			{
				GroupIDs: []int64{10},
				Pricing: []routing.ModelPricingEntry{
					{
						ID:          100,
						Models:      []string{"claude-sonnet-4"},
						InputPrice:  testPtrFloat64(0.01),
						OutputPrice: testPtrFloat64(0.02),
					},
				},
			},
		},
	}
	cs := newTestPricingConfigServiceForStats(t, pricingConfig, 10, "anthropic")

	tokens := purepricing.UsageTokens{InputTokens: 100, OutputTokens: 50}

	result := contractAccountStatsCost(
		context.Background(),
		cs, nil, // billingService not needed when custom rule hits
		1, 10, "claude-sonnet-4", "",
		tokens, 1, 999.0, "priority", // 自定义账号价格不叠加服务层级倍率
	)
	require.NotNil(t, result)
	// 100*0.01 + 50*0.02 = 1.0 + 1.0 = 2.0
	require.InDelta(t, 2.0, *result, 1e-12)
}

func TestResolveAccountStatsCost_ApplyPricingToAccountStats_UsesTotalCost(t *testing.T) {
	pricingConfig := &routingtestkit.Configuration{
		ID:                         1,
		Status:                     billing.StatusActive,
		ApplyPricingToAccountStats: true,
		// No custom rules
	}
	cs := newTestPricingConfigServiceForStats(t, pricingConfig, 10, "anthropic")

	tokens := purepricing.UsageTokens{InputTokens: 100, OutputTokens: 50}

	result := contractAccountStatsCost(
		context.Background(),
		cs, nil,
		1, 10, "claude-sonnet-4", "",
		tokens, 1, 0.75, "priority", // 已完成用户计费，不再重复应用服务层级倍率
	)
	require.NotNil(t, result)
	require.InDelta(t, 0.75, *result, 1e-12)
}

func TestResolveAccountStatsCost_ApplyPricingToAccountStats_ZeroTotalCost_ReturnsNil(t *testing.T) {
	pricingConfig := &routingtestkit.Configuration{
		ID:                         1,
		Status:                     billing.StatusActive,
		ApplyPricingToAccountStats: true,
	}
	cs := newTestPricingConfigServiceForStats(t, pricingConfig, 10, "anthropic")

	result := contractAccountStatsCost(
		context.Background(),
		cs, nil,
		1, 10, "claude-sonnet-4", "",
		purepricing.UsageTokens{}, 1, 0.0, "", // totalCost = 0
	)
	require.Nil(t, result)
}

func TestResolveAccountStatsCost_FallsBackToLiteLLM(t *testing.T) {
	pricingConfig := &routingtestkit.Configuration{
		ID:                         1,
		Status:                     billing.StatusActive,
		ApplyPricingToAccountStats: false, // not enabled
		// No custom rules
	}
	cs := newTestPricingConfigServiceForStats(t, pricingConfig, 10, "anthropic")

	bs := newTestBillingServiceWithPrices(map[string]*purepricing.ModelPricing{
		"claude-sonnet-4": {
			InputPricePerToken:  0.001,
			OutputPricePerToken: 0.002,
		},
	})

	tokens := purepricing.UsageTokens{InputTokens: 100, OutputTokens: 50}

	result := contractAccountStatsCost(
		context.Background(),
		cs, bs,
		1, 10, "claude-sonnet-4", "",
		tokens, 1, 999.0, "", // totalCost ignored
	)
	require.NotNil(t, result)
	// 100*0.001 + 50*0.002 = 0.1 + 0.1 = 0.2
	require.InDelta(t, 0.2, *result, 1e-12)
}

func TestResolveAccountStatsCost_QoderRouteKeyWithoutManualPricingReturnsNil(t *testing.T) {
	pricingConfig := &routingtestkit.Configuration{
		ID:                         1,
		Status:                     billing.StatusActive,
		ApplyPricingToAccountStats: false,
	}
	cs := newTestPricingConfigServiceForStats(t, pricingConfig, 10, capability.PlatformQoder)

	bs := newTestBillingServiceWithPrices(map[string]*purepricing.ModelPricing{
		"claude-opus-4.8": {
			InputPricePerToken:  0.005,
			OutputPricePerToken: 0.025,
		},
	})

	tokens := purepricing.UsageTokens{InputTokens: 100, OutputTokens: 50}

	result := contractAccountStatsCost(
		context.Background(),
		cs, bs,
		1, 10, "qmodel", "qwen3.7-plus",
		tokens, 1, 999.0, "",
	)
	require.Nil(t, result)
}

func TestResolveAccountStatsCost_QoderAliasUsesStandardUpstreamPricing(t *testing.T) {
	pricingConfig := &routingtestkit.Configuration{
		ID:                         1,
		Status:                     billing.StatusActive,
		ApplyPricingToAccountStats: false,
	}
	cs := newTestPricingConfigServiceForStats(t, pricingConfig, 10, capability.PlatformQoder)
	bs := newTestBillingServiceWithPrices(map[string]*purepricing.ModelPricing{
		"gpt-5.4-mini": {
			InputPricePerToken:  0.001,
			OutputPricePerToken: 0.002,
		},
	})

	result := contractAccountStatsCost(
		context.Background(),
		cs, bs,
		1, 10, "gpt-5.4-mini", "qwen3.7-plus",
		purepricing.UsageTokens{InputTokens: 100, OutputTokens: 50}, 1, 999.0, "",
	)
	require.NotNil(t, result)
	require.InDelta(t, 0.2, *result, 1e-12)
}

func TestResolveAccountStatsCost_QoderCustomRuleCanMatchRequestedAliasAfterRouteKeyMiss(t *testing.T) {
	pricingConfig := &routingtestkit.Configuration{
		ID:     1,
		Status: billing.StatusActive,
		AccountStatsPricingRules: []routing.AccountStatsPricingRule{
			{
				GroupIDs: []int64{10},
				Pricing: []routing.ModelPricingEntry{
					{
						ID:          100,
						Models:      []string{"qwen3.7-plus"},
						InputPrice:  testPtrFloat64(0.01),
						OutputPrice: testPtrFloat64(0.02),
					},
				},
			},
		},
	}
	cs := newTestPricingConfigServiceForStats(t, pricingConfig, 10, capability.PlatformQoder)

	result := contractAccountStatsCost(
		context.Background(),
		cs, nil,
		1, 10, "qmodel", "qwen3.7-plus",
		purepricing.UsageTokens{InputTokens: 100, OutputTokens: 50}, 1, 999.0, "",
	)

	require.NotNil(t, result)
	require.InDelta(t, 2.0, *result, 1e-12)
}

func TestResolveAccountStatsCost_QoderGroupMappedRuleMatchesBeforeDifferentUpstream(t *testing.T) {
	pricingConfig := &routingtestkit.Configuration{
		ID:     1,
		Status: billing.StatusActive,
		AccountStatsPricingRules: []routing.AccountStatsPricingRule{
			{
				GroupIDs: []int64{10},
				Pricing: []routing.ModelPricingEntry{
					{
						ID:          100,
						Models:      []string{"qmodel"},
						InputPrice:  testPtrFloat64(0.01),
						OutputPrice: testPtrFloat64(0.02),
					},
				},
			},
		},
	}
	cs := newTestPricingConfigServiceForStats(t, pricingConfig, 10, capability.PlatformQoder)

	result := contractAccountStatsWithMapping(
		context.Background(),
		cs, nil,
		1, 10, "ultimate", "qwen3.7-plus", "qmodel",
		purepricing.UsageTokens{InputTokens: 100, OutputTokens: 50}, 1, 999.0, "",
	)

	require.NotNil(t, result)
	require.InDelta(t, 2.0, *result, 1e-12)
}

func TestResolveAccountStatsCost_QoderGroupMappedRuleMatchesWhenUpstreamFallsBackToRequested(t *testing.T) {
	pricingConfig := &routingtestkit.Configuration{
		ID:     1,
		Status: billing.StatusActive,
		AccountStatsPricingRules: []routing.AccountStatsPricingRule{
			{
				GroupIDs: []int64{10},
				Pricing: []routing.ModelPricingEntry{
					{
						ID:          100,
						Models:      []string{"qmodel"},
						InputPrice:  testPtrFloat64(0.01),
						OutputPrice: testPtrFloat64(0.02),
					},
				},
			},
		},
	}
	cs := newTestPricingConfigServiceForStats(t, pricingConfig, 10, capability.PlatformQoder)

	result := contractAccountStatsWithMapping(
		context.Background(),
		cs, nil,
		1, 10, "qwen3.7-plus", "qwen3.7-plus", "qmodel",
		purepricing.UsageTokens{InputTokens: 100, OutputTokens: 50}, 1, 999.0, "",
	)

	require.NotNil(t, result)
	require.InDelta(t, 2.0, *result, 1e-12)
}

func TestResolveAccountStatsCost_QoderRequestedAliasRuleOverridesRouteKeyRule(t *testing.T) {
	pricingConfig := &routingtestkit.Configuration{
		ID:     1,
		Status: billing.StatusActive,
		AccountStatsPricingRules: []routing.AccountStatsPricingRule{
			{
				GroupIDs: []int64{10},
				Pricing: []routing.ModelPricingEntry{
					{
						ID:          100,
						Models:      []string{"qmodel"},
						InputPrice:  testPtrFloat64(0.50),
						OutputPrice: testPtrFloat64(0.75),
					},
					{
						ID:          101,
						Models:      []string{"qwen3.7-plus"},
						InputPrice:  testPtrFloat64(0.01),
						OutputPrice: testPtrFloat64(0.02),
					},
				},
			},
		},
	}
	cs := newTestPricingConfigServiceForStats(t, pricingConfig, 10, capability.PlatformQoder)

	result := contractAccountStatsCost(
		context.Background(),
		cs, nil,
		1, 10, "qmodel", "qwen3.7-plus",
		purepricing.UsageTokens{InputTokens: 100, OutputTokens: 50}, 1, 999.0, "",
	)

	require.NotNil(t, result)
	require.InDelta(t, 2.0, *result, 1e-12)
}

func TestResolveAccountStatsCost_QoderBlankRuleDoesNotMaskLaterAliasRule(t *testing.T) {
	pricingConfig := &routingtestkit.Configuration{
		ID:     1,
		Status: billing.StatusActive,
		AccountStatsPricingRules: []routing.AccountStatsPricingRule{
			{
				GroupIDs: []int64{10},
				Pricing: []routing.ModelPricingEntry{
					{
						ID:     100,
						Models: []string{"qwen3.7-plus"},
					},
				},
			},
			{
				GroupIDs: []int64{10},
				Pricing: []routing.ModelPricingEntry{
					{
						ID:          101,
						Models:      []string{"qwen3.7-plus"},
						InputPrice:  testPtrFloat64(0.01),
						OutputPrice: testPtrFloat64(0.02),
					},
				},
			},
		},
	}
	cs := newTestPricingConfigServiceForStats(t, pricingConfig, 10, capability.PlatformQoder)

	result := contractAccountStatsCost(
		context.Background(),
		cs, nil,
		1, 10, "qmodel", "qwen3.7-plus",
		purepricing.UsageTokens{InputTokens: 100, OutputTokens: 50}, 1, 999.0, "",
	)

	require.NotNil(t, result)
	require.InDelta(t, 2.0, *result, 1e-12)
}

func TestResolveAccountStatsCost_CustomRuleExplicitZeroOverridesTotalCost(t *testing.T) {
	pricingConfig := &routingtestkit.Configuration{
		ID:     1,
		Status: billing.StatusActive,
		AccountStatsPricingRules: []routing.AccountStatsPricingRule{
			{
				GroupIDs: []int64{10},
				Pricing: []routing.ModelPricingEntry{
					{
						ID:         100,
						Models:     []string{"qwen3.7-plus"},
						InputPrice: testPtrFloat64(0),
					},
				},
			},
		},
	}
	cs := newTestPricingConfigServiceForStats(t, pricingConfig, 10, capability.PlatformQoder)

	result := contractAccountStatsCost(
		context.Background(),
		cs, nil,
		1, 10, "qmodel", "qwen3.7-plus",
		purepricing.UsageTokens{InputTokens: 100, OutputTokens: 50}, 1, 999.0, "",
	)

	require.NotNil(t, result)
	require.Zero(t, *result)
}

func TestResolveAccountStatsCost_QoderUnknownUpstreamDoesNotUseRequestedPrice(t *testing.T) {
	pricingConfig := &routingtestkit.Configuration{
		ID:                         1,
		Status:                     billing.StatusActive,
		ApplyPricingToAccountStats: false,
	}
	cs := newTestPricingConfigServiceForStats(t, pricingConfig, 10, capability.PlatformQoder)
	bs := newTestBillingServiceWithPrices(map[string]*purepricing.ModelPricing{
		"gpt-5.4": {
			InputPricePerToken:  0.001,
			OutputPricePerToken: 0.002,
		},
		"ultimate": {
			InputPricePerToken:  0.50,
			OutputPricePerToken: 0.75,
		},
	})

	result := contractAccountStatsCost(
		context.Background(),
		cs, bs,
		1, 10, "ultimate", "gpt-5.4",
		purepricing.UsageTokens{InputTokens: 100, OutputTokens: 50}, 1, 999.0, "",
	)

	require.Nil(t, result)
}

func TestResolveAccountStatsCost_Gemini36FlashTierUsesFallbackPricing(t *testing.T) {
	pricingConfig := &routingtestkit.Configuration{
		ID:                         1,
		Status:                     billing.StatusActive,
		ApplyPricingToAccountStats: false,
	}
	cs := newTestPricingConfigServiceForStats(t, pricingConfig, 10, "antigravity")
	bs := newCalculator(&config.Config{}, nil)

	result := contractAccountStatsCost(
		context.Background(),
		cs, bs,
		1, 10, "gemini-3.6-flash-low", "",
		purepricing.UsageTokens{InputTokens: 1_000_000, OutputTokens: 1_000_000, CacheReadTokens: 1_000_000}, 1, 0, "",
	)
	require.NotNil(t, result)
	require.InDelta(t, 9.15, *result, 1e-12)
}

func TestResolveAccountStatsCost_AllMiss_ReturnsNil(t *testing.T) {
	pricingConfig := &routingtestkit.Configuration{
		ID:                         1,
		Status:                     billing.StatusActive,
		ApplyPricingToAccountStats: false,
		// No custom rules
	}
	cs := newTestPricingConfigServiceForStats(t, pricingConfig, 10, "anthropic")

	// BillingService with no pricing for the model
	bs := newTestBillingServiceWithPrices(map[string]*purepricing.ModelPricing{})

	tokens := purepricing.UsageTokens{InputTokens: 100, OutputTokens: 50}

	result := contractAccountStatsCost(
		context.Background(),
		cs, bs,
		1, 10, "totally-unknown-model", "",
		tokens, 1, 0.0, "",
	)
	require.Nil(t, result)
}

func TestResolveAccountStatsCost_NilBillingService_SkipsLiteLLM(t *testing.T) {
	pricingConfig := &routingtestkit.Configuration{
		ID:                         1,
		Status:                     billing.StatusActive,
		ApplyPricingToAccountStats: false,
	}
	cs := newTestPricingConfigServiceForStats(t, pricingConfig, 10, "anthropic")

	result := contractAccountStatsCost(
		context.Background(),
		cs, nil, // billingService is nil
		1, 10, "claude-sonnet-4", "",
		purepricing.UsageTokens{InputTokens: 100}, 1, 0.0, "",
	)
	require.Nil(t, result)
}

func TestResolveAccountStatsCost_CustomRulePriorityOverApplyPricing(t *testing.T) {
	// Both custom rule and ApplyPricingToAccountStats are configured;
	// custom rule should take precedence.
	pricingConfig := &routingtestkit.Configuration{
		ID:                         1,
		Status:                     billing.StatusActive,
		ApplyPricingToAccountStats: true,
		AccountStatsPricingRules: []routing.AccountStatsPricingRule{
			{
				GroupIDs: []int64{10},
				Pricing: []routing.ModelPricingEntry{
					{
						ID:         100,
						Models:     []string{"claude-sonnet-4"},
						InputPrice: testPtrFloat64(0.05),
					},
				},
			},
		},
	}
	cs := newTestPricingConfigServiceForStats(t, pricingConfig, 10, "anthropic")

	tokens := purepricing.UsageTokens{InputTokens: 100}

	result := contractAccountStatsCost(
		context.Background(),
		cs, nil,
		1, 10, "claude-sonnet-4", "",
		tokens, 1, 99.0, "", // totalCost = 99.0 (would be used if ApplyPricing wins)
	)
	require.NotNil(t, result)
	// Custom rule: 100*0.05 = 5.0 (NOT 99.0 from totalCost)
	require.InDelta(t, 5.0, *result, 1e-12)
}

func TestApplyAccountStatsCost_UsesUsageLogServiceTier(t *testing.T) {
	pricingConfig := &routingtestkit.Configuration{
		ID:                         1,
		Status:                     billing.StatusActive,
		ApplyPricingToAccountStats: false,
	}
	cs := newTestPricingConfigServiceForStats(t, pricingConfig, 10, "openai")
	bs := newTestBillingServiceWithPrices(map[string]*purepricing.ModelPricing{
		"gpt-5.6-sol": {
			InputPricePerToken:          0.001,
			InputPricePerTokenPriority:  0.002,
			OutputPricePerToken:         0.002,
			OutputPricePerTokenPriority: 0.004,
		},
	})
	serviceTier := "priority"
	usageLog := &usage.UsageLog{ServiceTier: &serviceTier}

	applyContractAccountStatsCost(
		context.Background(), usageLog, cs, bs,
		1, 10, "gpt-5.6-sol", "gpt-5.6-sol", "",
		purepricing.UsageTokens{InputTokens: 100, OutputTokens: 50}, 999,
	)

	require.NotNil(t, usageLog.AccountStatsCost)
	require.InDelta(t, 0.4, *usageLog.AccountStatsCost, 1e-12)
}

// ---------------------------------------------------------------------------
// helpers for contractAccountStatsCost tests
// ---------------------------------------------------------------------------

// newTestPricingConfigServiceForStats creates a PricingConfigService with a single channel
// mapped to the given groupID, suitable for contractAccountStatsCost tests.
func newTestPricingConfigServiceForStats(t *testing.T, pricingConfig *routingtestkit.Configuration, groupID int64, platform string) *routing.PricingConfigService {
	t.Helper()
	cache := routingtestkit.NewModelConfigData()
	cache.ByGroup[groupID] = pricingConfig
	cache.Platforms[groupID] = platform

	cache.LoadedAt = time.Now()
	cs := routingtestkit.ModelConfigFromData(cache)
	return cs
}
