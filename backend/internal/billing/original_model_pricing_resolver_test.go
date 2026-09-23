//go:build unit

package billing_test

import (
	"context"
	"errors"

	billingtestkit "github.com/TokenFlux/TokenRouter/internal/billing/testkit"

	slog "log/slog"
	"testing"
	"time"

	"github.com/TokenFlux/TokenRouter/internal/billing"

	billingpricing "github.com/TokenFlux/TokenRouter/internal/billing/pricing"

	pricingprovider "github.com/TokenFlux/TokenRouter/internal/billing/provider"
	"github.com/TokenFlux/TokenRouter/internal/routing"
	"github.com/TokenFlux/TokenRouter/internal/routing/capability"

	routingtestkit "github.com/TokenFlux/TokenRouter/internal/routing/testkit"
	"github.com/stretchr/testify/require"
)

func TestResolve_NoGroupID(t *testing.T) {
	bs := billingtestkit.ResolverCalculator()
	r := billingtestkit.PriceResolver(routing.NewChannelService(nil, nil, routing.ChannelOptions{Warn: slog.
		Warn,
		Now: time.
			Now, LoadLocation: pricingprovider.
			LoadPricingLocation,
	}),

		bs)

	resolved := r.Resolve(context.Background(), billing.PricingInput{
		Model:   "claude-sonnet-4",
		GroupID: nil,
	})

	require.NotNil(t, resolved)
	require.Equal(t, routing.BillingModeToken, resolved.Mode)
	require.NotNil(t, resolved.BasePricing)
	require.InDelta(t, 3e-6, resolved.BasePricing.InputPricePerToken, 1e-12)
	// BillingService.GetModelPricing uses fallback internally, but resolveBasePricing
	// reports "litellm" when GetModelPricing succeeds (regardless of internal source)
	require.Equal(t, "litellm", resolved.Source)
}

func TestResolve_UnknownModel(t *testing.T) {
	bs := billingtestkit.ResolverCalculator()
	r := billingtestkit.PriceResolver(routing.NewChannelService(nil, nil, routing.ChannelOptions{Warn: slog.
		Warn,
		Now: time.
			Now, LoadLocation: pricingprovider.
			LoadPricingLocation,
	}),

		bs)

	resolved := r.Resolve(context.Background(), billing.PricingInput{
		Model:   "unknown-model-xyz",
		GroupID: nil,
	})

	require.NotNil(t, resolved)
	require.Nil(t, resolved.BasePricing)
	// Unknown model: GetModelPricing returns error, source is "fallback"
	require.Equal(t, "fallback", resolved.Source)
}

func TestGetIntervalPricing_NoIntervals(t *testing.T) {
	bs := billingtestkit.ResolverCalculator()
	r := billingtestkit.PriceResolver(routing.NewChannelService(nil, nil, routing.ChannelOptions{Warn: slog.
		Warn,
		Now: time.
			Now, LoadLocation: pricingprovider.
			LoadPricingLocation,
	}),

		bs)

	basePricing := &billingpricing.ModelPricing{InputPricePerToken: 5e-6}
	resolved := &billingpricing.ResolvedPricing{
		Mode:        routing.BillingModeToken,
		BasePricing: basePricing,
		Intervals:   nil,
	}

	result := r.GetIntervalPricing(resolved, 50000)
	require.Equal(t, basePricing, result)
}

func TestGetIntervalPricing_MatchesInterval(t *testing.T) {
	bs := billingtestkit.ResolverCalculator()
	r := billingtestkit.PriceResolver(routing.NewChannelService(nil, nil, routing.ChannelOptions{Warn: slog.
		Warn,
		Now: time.
			Now, LoadLocation: pricingprovider.
			LoadPricingLocation,
	}),

		bs)

	resolved := &billingpricing.ResolvedPricing{
		Mode:                   routing.BillingModeToken,
		BasePricing:            &billingpricing.ModelPricing{InputPricePerToken: 5e-6},
		SupportsCacheBreakdown: true,
		Intervals: []routing.PricingInterval{
			{MinTokens: 0, MaxTokens: testPtrInt(128000), InputPrice: testPtrFloat64(1e-6), OutputPrice: testPtrFloat64(2e-6)},
			{MinTokens: 128000, MaxTokens: nil, InputPrice: testPtrFloat64(3e-6), OutputPrice: testPtrFloat64(6e-6)},
		},
	}

	result := r.GetIntervalPricing(resolved, 50000)
	require.NotNil(t, result)
	require.InDelta(t, 1e-6, result.InputPricePerToken, 1e-12)
	require.InDelta(t, 2e-6, result.OutputPricePerToken, 1e-12)
	require.True(t, result.SupportsCacheBreakdown)

	result2 := r.GetIntervalPricing(resolved, 200000)
	require.NotNil(t, result2)
	require.InDelta(t, 3e-6, result2.InputPricePerToken, 1e-12)
}

func TestGetIntervalPricing_NoMatch_FallsBackToBase(t *testing.T) {
	bs := billingtestkit.ResolverCalculator()
	r := billingtestkit.PriceResolver(routing.NewChannelService(nil, nil, routing.ChannelOptions{Warn: slog.
		Warn,
		Now: time.
			Now, LoadLocation: pricingprovider.
			LoadPricingLocation,
	}),

		bs)

	basePricing := &billingpricing.ModelPricing{InputPricePerToken: 99e-6}
	resolved := &billingpricing.ResolvedPricing{
		Mode:        routing.BillingModeToken,
		BasePricing: basePricing,
		Intervals: []routing.PricingInterval{
			{MinTokens: 10000, MaxTokens: testPtrInt(50000), InputPrice: testPtrFloat64(1e-6)},
		},
	}

	result := r.GetIntervalPricing(resolved, 5000)
	require.Equal(t, basePricing, result)
}

func TestGPT56ExplicitZeroCacheWritePriceIsPreserved(t *testing.T) {
	bs := newCalculatorWithPrices(nil, nil, map[string]*billingpricing.ModelPricing{})
	resolver := billingtestkit.PriceResolver(nil, bs)
	zero := 0.0

	t.Run("flat channel price", func(t *testing.T) {
		resolved := &billingpricing.ResolvedPricing{
			Mode: routing.BillingModeToken,
			BasePricing: &billingpricing.ModelPricing{
				InputPricePerToken:  5e-6,
				OutputPricePerToken: 30e-6,
			},
		}
		billingpricing.ApplyTokenOverrides(&routing.ChannelModelPricing{CacheWritePrice: &zero}, resolved)

		require.True(t, resolved.BasePricing.CacheCreationPriceExplicit)
		cost, err := bs.CalculateCostUnified(billing.CostInput{
			Model:          "gpt-5.6-sol",
			Tokens:         billingpricing.UsageTokens{CacheCreationTokens: 100},
			RateMultiplier: 1,
			Resolver:       resolver,
			Resolved:       resolved,
		})
		require.NoError(t, err)
		require.Zero(t, cost.CacheCreationCost)
	})

	t.Run("interval price", func(t *testing.T) {
		pricing := billingpricing.IntervalToModelPricing(&routing.PricingInterval{CacheWritePrice: &zero}, false, nil)
		require.True(t, pricing.CacheCreationPriceExplicit)

		cost, err := bs.CalculateCostUnified(billing.CostInput{
			Model:          "gpt-5.6-sol",
			Tokens:         billingpricing.UsageTokens{CacheCreationTokens: 100},
			RateMultiplier: 1,
			Resolver:       resolver,
			Resolved: &billingpricing.ResolvedPricing{
				Mode:        routing.BillingModeToken,
				BasePricing: pricing,
			},
		})
		require.NoError(t, err)
		require.Zero(t, cost.CacheCreationCost)
	})
}

func TestGetRequestTierPrice(t *testing.T) {
	bs := billingtestkit.ResolverCalculator()
	r := billingtestkit.PriceResolver(routing.NewChannelService(nil, nil, routing.ChannelOptions{Warn: slog.
		Warn,
		Now: time.
			Now, LoadLocation: pricingprovider.
			LoadPricingLocation,
	}),

		bs)

	resolved := &billingpricing.ResolvedPricing{
		Mode: routing.BillingModePerRequest,
		RequestTiers: []routing.PricingInterval{
			{TierLabel: "1K", PerRequestPrice: testPtrFloat64(0.04)},
			{TierLabel: "2K", PerRequestPrice: testPtrFloat64(0.08)},
		},
	}

	require.InDelta(t, 0.04, r.GetRequestTierPrice(resolved, "1K"), 1e-12)
	require.InDelta(t, 0.04, r.GetRequestTierPrice(resolved, "1k"), 1e-12)
	require.InDelta(t, 0.08, r.GetRequestTierPrice(resolved, "2K"), 1e-12)
	require.InDelta(t, 0.0, r.GetRequestTierPrice(resolved, "4K"), 1e-12)

	price, ok := r.GetRequestTierPriceValue(resolved, "4K")
	require.False(t, ok)
	require.Zero(t, price)
}

func TestGetRequestTierPriceByContext(t *testing.T) {
	bs := billingtestkit.ResolverCalculator()
	r := billingtestkit.PriceResolver(routing.NewChannelService(nil, nil, routing.ChannelOptions{Warn: slog.
		Warn,
		Now: time.
			Now, LoadLocation: pricingprovider.
			LoadPricingLocation,
	}),

		bs)

	resolved := &billingpricing.ResolvedPricing{
		Mode: routing.BillingModePerRequest,
		RequestTiers: []routing.PricingInterval{
			{MinTokens: 0, MaxTokens: testPtrInt(128000), PerRequestPrice: testPtrFloat64(0.05)},
			{MinTokens: 128000, MaxTokens: nil, PerRequestPrice: testPtrFloat64(0.10)},
		},
	}

	require.InDelta(t, 0.05, r.GetRequestTierPriceByContext(resolved, 50000), 1e-12)
	require.InDelta(t, 0.10, r.GetRequestTierPriceByContext(resolved, 200000), 1e-12)

	price, ok := r.GetRequestTierPriceByContextValue(resolved, 0)
	require.False(t, ok)
	require.Zero(t, price)
}

func TestGetRequestTierPrice_NilPerRequestPrice(t *testing.T) {
	bs := billingtestkit.ResolverCalculator()
	r := billingtestkit.PriceResolver(routing.NewChannelService(nil, nil, routing.ChannelOptions{Warn: slog.
		Warn,
		Now: time.
			Now, LoadLocation: pricingprovider.
			LoadPricingLocation,
	}),

		bs)

	resolved := &billingpricing.ResolvedPricing{
		Mode: routing.BillingModePerRequest,
		RequestTiers: []routing.PricingInterval{
			{TierLabel: "1K", PerRequestPrice: nil},
		},
	}

	require.InDelta(t, 0.0, r.GetRequestTierPrice(resolved, "1K"), 1e-12)
}

// ===========================================================================
// Channel override tests — exercises applyChannelOverrides via Resolve
// ===========================================================================

// newResolverWithChannel 创建带指定渠道定价的解析器，分组平台跟随首条定价配置。
func newResolverWithChannel(t *testing.T, pricing []routing.ChannelModelPricing) *billing.PriceResolver {
	t.Helper()
	return billingtestkit.ResolverWithCards(t, billingtestkit.ResolverCalculator(), pricing)
}

// ---------------------------------------------------------------------------
// 1. Token mode overrides
// ---------------------------------------------------------------------------

func TestResolve_WithChannelOverride_TokenFlat(t *testing.T) {
	r := newResolverWithChannel(t, []routing.ChannelModelPricing{{
		Platform:    "anthropic",
		Models:      []string{"claude-sonnet-4"},
		BillingMode: routing.BillingModeToken,
		InputPrice:  testPtrFloat64(10e-6),
		OutputPrice: testPtrFloat64(50e-6),
	}})

	resolved := r.Resolve(context.Background(), billing.PricingInput{
		Model:   "claude-sonnet-4",
		GroupID: billingtestkit.GroupID(),
	})

	require.NotNil(t, resolved)
	require.Equal(t, routing.BillingModeToken, resolved.Mode)
	require.Equal(t, "channel", resolved.Source)
	require.NotNil(t, resolved.BasePricing)
	require.InDelta(t, 10e-6, resolved.BasePricing.InputPricePerToken, 1e-12)
	// claude-sonnet-4 没有目录 priority 价，渠道覆盖不能凭空制造 tier 价格。
	require.Zero(t, resolved.BasePricing.InputPricePerTokenPriority)
	require.InDelta(t, 50e-6, resolved.BasePricing.OutputPricePerToken, 1e-12)
	require.Zero(t, resolved.BasePricing.OutputPricePerTokenPriority)
}

func TestResolve_WithChannelOverride_TokenFlatPreservesNativeTierRatio(t *testing.T) {
	prices := billingtestkit.ResolverFallbackPrices()
	prices["gpt-5.4"] = &billingpricing.ModelPricing{
		InputPricePerToken:             2e-6,
		InputPricePerTokenPriority:     4e-6,
		OutputPricePerToken:            10e-6,
		OutputPricePerTokenPriority:    20e-6,
		CacheReadPricePerToken:         0.5e-6,
		CacheReadPricePerTokenPriority: 1e-6,
	}
	bs := newCalculatorWithPrices(nil, nil, prices)
	r := billingtestkit.ResolverWithCards(t, bs, []routing.ChannelModelPricing{{
		Platform:       "openai",
		Models:         []string{"gpt-5.4"},
		BillingMode:    routing.BillingModeToken,
		InputPrice:     testPtrFloat64(7e-6),
		OutputPrice:    testPtrFloat64(30e-6),
		CacheReadPrice: testPtrFloat64(2e-6),
	}})

	resolved := r.Resolve(context.Background(), billing.PricingInput{Model: "gpt-5.4", GroupID: billingtestkit.GroupID()})
	require.NotNil(t, resolved)
	require.InDelta(t, 14e-6, resolved.BasePricing.InputPricePerTokenPriority, 1e-12)
	require.InDelta(t, 60e-6, resolved.BasePricing.OutputPricePerTokenPriority, 1e-12)
	require.InDelta(t, 4e-6, resolved.BasePricing.CacheReadPricePerTokenPriority, 1e-12)
}

func TestResolve_WithChannelOverride_TokenPartialOverride(t *testing.T) {
	// Channel only sets InputPrice; OutputPrice should remain from the base (LiteLLM/fallback).
	r := newResolverWithChannel(t, []routing.ChannelModelPricing{{
		Platform:    "anthropic",
		Models:      []string{"claude-sonnet-4"},
		BillingMode: routing.BillingModeToken,
		InputPrice:  testPtrFloat64(20e-6),
		// OutputPrice intentionally nil
	}})

	resolved := r.Resolve(context.Background(), billing.PricingInput{
		Model:   "claude-sonnet-4",
		GroupID: billingtestkit.GroupID(),
	})

	require.NotNil(t, resolved)
	require.Equal(t, "channel", resolved.Source)
	require.NotNil(t, resolved.BasePricing)
	// InputPrice overridden by channel
	require.InDelta(t, 20e-6, resolved.BasePricing.InputPricePerToken, 1e-12)
	// OutputPrice kept from base (fallback: 15e-6)
	require.InDelta(t, 15e-6, resolved.BasePricing.OutputPricePerToken, 1e-12)
}

func TestResolve_WithChannelOverride_PriceMultiplierOnlyIsIgnored(t *testing.T) {
	r := newResolverWithChannel(t, []routing.ChannelModelPricing{{
		Platform:        "anthropic",
		Models:          []string{"claude-sonnet-4"},
		BillingMode:     routing.BillingModeToken,
		PriceMultiplier: testPtrFloat64(2),
	}})

	resolved := r.Resolve(context.Background(), billing.PricingInput{
		Model:   "claude-sonnet-4",
		GroupID: billingtestkit.GroupID(),
	})

	// 非法的仅倍率存量数据不能改变默认模型价格。
	require.NotNil(t, resolved)
	require.Equal(t, billingpricing.PricingSourceLiteLLM, resolved.Source)
	require.False(t, resolved.HasEffectiveChannelPricing())
	require.NotNil(t, resolved.BasePricing)
	require.InDelta(t, 3e-6, resolved.BasePricing.InputPricePerToken, 1e-12)
	require.InDelta(t, 15e-6, resolved.BasePricing.OutputPricePerToken, 1e-12)
}

func TestResolve_BlankChannelPricingIsIgnoredForNonQoder(t *testing.T) {
	r := newResolverWithChannel(t, []routing.ChannelModelPricing{{
		Platform:    "anthropic",
		Models:      []string{"claude-sonnet-4"},
		BillingMode: routing.BillingModeToken,
	}})

	resolved := r.Resolve(context.Background(), billing.PricingInput{
		Model:   "claude-sonnet-4",
		GroupID: billingtestkit.GroupID(),
	})

	require.NotNil(t, resolved)
	require.Equal(t, billingpricing.PricingSourceLiteLLM, resolved.Source)
	require.False(t, resolved.HasEffectiveChannelPricing())
	require.NotNil(t, resolved.BasePricing)
	require.InDelta(t, 3e-6, resolved.BasePricing.InputPricePerToken, 1e-12)
	require.InDelta(t, 15e-6, resolved.BasePricing.OutputPricePerToken, 1e-12)
	require.False(t, resolved.BasePricing.ImageOutputPriceExplicit)
}

func TestResolve_QoderCustomAliasMappedToRouteKeyZerosMissingPartialChannelPricing(t *testing.T) {
	groupID := int64(100)
	inputPrice := 20e-6
	cache := routingtestkit.NewChannelData()
	cache.ByGroup[groupID] = &routing.Channel{ID: 1, Status: billing.StatusActive}
	cache.Platforms[groupID] = capability.PlatformQoder
	cache.Prices[routingtestkit.ModelKey{GroupID: groupID, Platform: capability.PlatformQoder, Model: "custom-qoder"}] = &routing.ChannelModelPricing{
		Platform:    capability.PlatformQoder,
		Models:      []string{"custom-qoder"},
		BillingMode: routing.BillingModeToken,
		InputPrice:  &inputPrice,
	}
	cache.Models[routingtestkit.ModelKey{GroupID: groupID, Platform: capability.PlatformQoder, Model: "custom-qoder"}] = "qmodel"
	cache.LoadedAt = time.Now()

	channelService := routingtestkit.ChannelFromData(cache)
	billingService := newCalculator(nil, nil)
	r := billingtestkit.PriceResolver(channelService, billingService)

	resolved := r.Resolve(context.Background(), billing.PricingInput{
		Model:   "custom-qoder",
		GroupID: &groupID,
	})

	require.NotNil(t, resolved)
	require.Equal(t, billingpricing.PricingSourceChannel, resolved.Source)
	require.NotNil(t, resolved.BasePricing)
	require.InDelta(t, inputPrice, resolved.BasePricing.InputPricePerToken, 1e-12)
	require.Zero(t, resolved.BasePricing.OutputPricePerToken)
}

func TestResolve_QoderStandardModelMappedToRouteKeyKeepsBaseForPartialChannelPricing(t *testing.T) {
	groupID := int64(100)
	inputPrice := 20e-6
	cache := routingtestkit.NewChannelData()
	cache.ByGroup[groupID] = &routing.Channel{ID: 1, Status: billing.StatusActive}
	cache.Platforms[groupID] = capability.PlatformQoder
	cache.Prices[routingtestkit.ModelKey{GroupID: groupID, Platform: capability.PlatformQoder, Model: "gpt-5.4"}] = &routing.ChannelModelPricing{
		Platform:    capability.PlatformQoder,
		Models:      []string{"gpt-5.4"},
		BillingMode: routing.BillingModeToken,
		InputPrice:  &inputPrice,
	}
	cache.Models[routingtestkit.ModelKey{GroupID: groupID, Platform: capability.PlatformQoder, Model: "gpt-5.4"}] = "qmodel"
	cache.LoadedAt = time.Now()

	channelService := routingtestkit.ChannelFromData(cache)
	billingService := newCalculator(nil, nil)
	r := billingtestkit.PriceResolver(channelService, billingService)

	basePricing, err := billingService.GetModelPricing("gpt-5.4")
	require.NoError(t, err)
	require.NotZero(t, basePricing.OutputPricePerToken)

	resolved := r.Resolve(context.Background(), billing.PricingInput{
		Model:   "gpt-5.4",
		GroupID: &groupID,
	})

	require.NotNil(t, resolved)
	require.Equal(t, billingpricing.PricingSourceChannel, resolved.Source)
	require.NotNil(t, resolved.BasePricing)
	require.InDelta(t, inputPrice, resolved.BasePricing.InputPricePerToken, 1e-12)
	require.InDelta(t, basePricing.OutputPricePerToken, resolved.BasePricing.OutputPricePerToken, 1e-12)
}

func TestResolve_QoderStandardModelMappedToRouteKeyKeepsBaseForPartialIntervalPricing(t *testing.T) {
	groupID := int64(100)
	inputPrice := 20e-6
	maxTokens := 1000
	cache := routingtestkit.NewChannelData()
	cache.ByGroup[groupID] = &routing.Channel{ID: 1, Status: billing.StatusActive}
	cache.Platforms[groupID] = capability.PlatformQoder
	cache.Prices[routingtestkit.ModelKey{GroupID: groupID, Platform: capability.PlatformQoder, Model: "gpt-5.4"}] = &routing.ChannelModelPricing{
		Platform:    capability.PlatformQoder,
		Models:      []string{"gpt-5.4"},
		BillingMode: routing.BillingModeToken,
		Intervals: []routing.PricingInterval{
			{MinTokens: 0, MaxTokens: &maxTokens, InputPrice: &inputPrice},
		},
	}
	cache.Models[routingtestkit.ModelKey{GroupID: groupID, Platform: capability.PlatformQoder, Model: "gpt-5.4"}] = "qmodel"
	cache.LoadedAt = time.Now()

	channelService := routingtestkit.ChannelFromData(cache)
	billingService := newCalculator(nil, nil)
	r := billingtestkit.PriceResolver(channelService, billingService)

	basePricing, err := billingService.GetModelPricing("gpt-5.4")
	require.NoError(t, err)
	require.NotZero(t, basePricing.OutputPricePerToken)

	resolved := r.Resolve(context.Background(), billing.PricingInput{
		Model:   "gpt-5.4",
		GroupID: &groupID,
	})
	require.NotNil(t, resolved)
	require.Equal(t, billingpricing.PricingSourceChannel, resolved.Source)

	intervalPricing := r.GetIntervalPricing(resolved, 100)
	require.NotNil(t, intervalPricing)
	require.InDelta(t, inputPrice, intervalPricing.InputPricePerToken, 1e-12)
	require.InDelta(t, basePricing.OutputPricePerToken, intervalPricing.OutputPricePerToken, 1e-12)
}

func TestResolve_QoderCustomAliasUnknownBaseZerosMissingPartialChannelPricing(t *testing.T) {
	groupID := int64(100)
	inputPrice := 20e-6
	cache := routingtestkit.NewChannelData()
	cache.ByGroup[groupID] = &routing.Channel{ID: 1, Status: billing.StatusActive}
	cache.Platforms[groupID] = capability.PlatformQoder
	cache.Prices[routingtestkit.ModelKey{GroupID: groupID, Platform: capability.PlatformQoder, Model: "custom-qoder"}] = &routing.ChannelModelPricing{
		Platform:    capability.PlatformQoder,
		Models:      []string{"custom-qoder"},
		BillingMode: routing.BillingModeToken,
		InputPrice:  &inputPrice,
	}
	cache.LoadedAt = time.Now()

	channelService := routingtestkit.ChannelFromData(cache)
	billingService := newCalculator(nil, nil)
	r := billingtestkit.PriceResolver(channelService, billingService)

	resolved := r.Resolve(context.Background(), billing.PricingInput{
		Model:   "custom-qoder",
		GroupID: &groupID,
	})

	require.NotNil(t, resolved)
	require.Equal(t, billingpricing.PricingSourceChannel, resolved.Source)
	require.NotNil(t, resolved.BasePricing)
	require.InDelta(t, inputPrice, resolved.BasePricing.InputPricePerToken, 1e-12)
	require.Zero(t, resolved.BasePricing.OutputPricePerToken)
}

func TestResolve_QoderCustomAliasUnknownBaseZerosMissingPartialIntervalPricing(t *testing.T) {
	groupID := int64(100)
	inputPrice := 20e-6
	maxTokens := 1000
	cache := routingtestkit.NewChannelData()
	cache.ByGroup[groupID] = &routing.Channel{ID: 1, Status: billing.StatusActive}
	cache.Platforms[groupID] = capability.PlatformQoder
	cache.Prices[routingtestkit.ModelKey{GroupID: groupID, Platform: capability.PlatformQoder, Model: "custom-qoder"}] = &routing.ChannelModelPricing{
		Platform:    capability.PlatformQoder,
		Models:      []string{"custom-qoder"},
		BillingMode: routing.BillingModeToken,
		Intervals: []routing.PricingInterval{
			{MinTokens: 0, MaxTokens: &maxTokens, InputPrice: &inputPrice},
		},
	}
	cache.LoadedAt = time.Now()

	channelService := routingtestkit.ChannelFromData(cache)
	billingService := newCalculator(nil, nil)
	r := billingtestkit.PriceResolver(channelService, billingService)

	resolved := r.Resolve(context.Background(), billing.PricingInput{
		Model:   "custom-qoder",
		GroupID: &groupID,
	})
	require.NotNil(t, resolved)
	require.Equal(t, billingpricing.PricingSourceChannel, resolved.Source)

	intervalPricing := r.GetIntervalPricing(resolved, 100)
	require.NotNil(t, intervalPricing)
	require.InDelta(t, inputPrice, intervalPricing.InputPricePerToken, 1e-12)
	require.Zero(t, intervalPricing.OutputPricePerToken)
}

func TestResolve_QoderBlankRouteKeyPricingIsUnpricedButAliasManualPricingWorks(t *testing.T) {
	groupID := int64(100)
	inputPrice := 20e-6
	cache := routingtestkit.NewChannelData()
	cache.ByGroup[groupID] = &routing.Channel{ID: 1, Status: billing.StatusActive}
	cache.Platforms[groupID] = capability.PlatformQoder
	cache.Prices[routingtestkit.ModelKey{GroupID: groupID, Platform: capability.PlatformQoder, Model: "qmodel"}] = &routing.ChannelModelPricing{
		Platform:    capability.PlatformQoder,
		Models:      []string{"qmodel"},
		BillingMode: routing.BillingModeToken,
	}
	cache.Prices[routingtestkit.ModelKey{GroupID: groupID, Platform: capability.PlatformQoder, Model: "qwen3.7-plus"}] = &routing.ChannelModelPricing{
		Platform:    capability.PlatformQoder,
		Models:      []string{"qwen3.7-plus"},
		BillingMode: routing.BillingModeToken,
		InputPrice:  &inputPrice,
	}
	cache.LoadedAt = time.Now()

	channelService := routingtestkit.ChannelFromData(cache)
	billingService := newCalculator(nil, nil)
	r := billingtestkit.PriceResolver(channelService, billingService)

	routeResolved := r.Resolve(context.Background(), billing.PricingInput{
		Model:   "qmodel",
		GroupID: &groupID,
	})
	require.NotNil(t, routeResolved)
	require.Equal(t, billingpricing.PricingSourceFallback, routeResolved.Source)
	require.Nil(t, routeResolved.BasePricing)
	require.False(t, routeResolved.HasEffectiveChannelPricing())

	aliasResolved := r.Resolve(context.Background(), billing.PricingInput{
		Model:   "qwen3.7-plus",
		GroupID: &groupID,
	})
	require.NotNil(t, aliasResolved)
	require.Equal(t, billingpricing.PricingSourceChannel, aliasResolved.Source)
	require.True(t, aliasResolved.HasEffectiveChannelPricing())
	require.InDelta(t, inputPrice, aliasResolved.BasePricing.InputPricePerToken, 1e-12)
	require.Zero(t, aliasResolved.BasePricing.OutputPricePerToken)
}

func TestResolve_BlankWildcardPricingDoesNotMaskLaterEffectiveWildcard(t *testing.T) {
	groupID := int64(100)
	inputPrice := 20e-6
	cache := routingtestkit.NewChannelData()
	cache.ByGroup[groupID] = &routing.Channel{ID: 1, Status: billing.StatusActive}
	cache.Platforms[groupID] = capability.PlatformQoder
	cache.PricePatterns[routingtestkit.GroupPlatform{GroupID: groupID, Platform: capability.PlatformQoder}] = []*routingtestkit.PricePattern{
		{
			Prefix: "qwen3.",
			Pricing: &routing.ChannelModelPricing{
				Platform:    capability.PlatformQoder,
				Models:      []string{"qwen3.*"},
				BillingMode: routing.BillingModeToken,
			},
		},
		{
			Prefix: "qwen3.7-",
			Pricing: &routing.ChannelModelPricing{
				Platform:    capability.PlatformQoder,
				Models:      []string{"qwen3.7-*"},
				BillingMode: routing.BillingModeToken,
				InputPrice:  &inputPrice,
			},
		},
	}
	cache.LoadedAt = time.Now()

	channelService := routingtestkit.ChannelFromData(cache)
	billingService := newCalculator(nil, nil)
	r := billingtestkit.PriceResolver(channelService, billingService)

	resolved := r.Resolve(context.Background(), billing.PricingInput{
		Model:   "qwen3.7-plus",
		GroupID: &groupID,
	})

	require.NotNil(t, resolved)
	require.Equal(t, billingpricing.PricingSourceChannel, resolved.Source)
	require.True(t, resolved.HasEffectiveChannelPricing())
	require.InDelta(t, inputPrice, resolved.BasePricing.InputPricePerToken, 1e-12)
	require.Zero(t, resolved.BasePricing.OutputPricePerToken)
}

func TestResolve_QoderPerRequestRouteKeyTokenOnlyIntervalIsUnpriced(t *testing.T) {
	groupID := int64(100)
	inputPrice := 20e-6
	staleTokenPrice := 99e-6
	cache := routingtestkit.NewChannelData()
	cache.ByGroup[groupID] = &routing.Channel{ID: 1, Status: billing.StatusActive}
	cache.Platforms[groupID] = capability.PlatformQoder
	cache.Prices[routingtestkit.ModelKey{GroupID: groupID, Platform: capability.PlatformQoder, Model: "qmodel"}] = &routing.ChannelModelPricing{
		Platform:    capability.PlatformQoder,
		Models:      []string{"qmodel"},
		BillingMode: routing.BillingModePerRequest,
		Intervals: []routing.PricingInterval{
			{MinTokens: 0, InputPrice: &staleTokenPrice},
		},
	}
	cache.Prices[routingtestkit.ModelKey{GroupID: groupID, Platform: capability.PlatformQoder, Model: "qwen3.7-plus"}] = &routing.ChannelModelPricing{
		Platform:    capability.PlatformQoder,
		Models:      []string{"qwen3.7-plus"},
		BillingMode: routing.BillingModeToken,
		InputPrice:  &inputPrice,
	}
	cache.LoadedAt = time.Now()

	channelService := routingtestkit.ChannelFromData(cache)
	billingService := newCalculator(nil, nil)
	r := billingtestkit.PriceResolver(channelService, billingService)

	routeResolved := r.Resolve(context.Background(), billing.PricingInput{
		Model:   "qmodel",
		GroupID: &groupID,
	})
	require.NotNil(t, routeResolved)
	require.Equal(t, billingpricing.PricingSourceFallback, routeResolved.Source)
	require.Nil(t, routeResolved.BasePricing)
	require.False(t, routeResolved.HasEffectiveChannelPricing())

	aliasResolved := r.Resolve(context.Background(), billing.PricingInput{
		Model:   "qwen3.7-plus",
		GroupID: &groupID,
	})
	require.NotNil(t, aliasResolved)
	require.Equal(t, billingpricing.PricingSourceChannel, aliasResolved.Source)
	require.True(t, aliasResolved.HasEffectiveChannelPricing())
	require.InDelta(t, inputPrice, aliasResolved.BasePricing.InputPricePerToken, 1e-12)
}

func TestResolve_WithChannelOverride_TokenWithIntervals(t *testing.T) {
	r := newResolverWithChannel(t, []routing.ChannelModelPricing{{
		Platform:    "anthropic",
		Models:      []string{"claude-sonnet-4"},
		BillingMode: routing.BillingModeToken,
		Intervals: []routing.PricingInterval{
			{MinTokens: 0, MaxTokens: testPtrInt(128000), InputPrice: testPtrFloat64(2e-6), OutputPrice: testPtrFloat64(8e-6)},
			{MinTokens: 128000, MaxTokens: nil, InputPrice: testPtrFloat64(4e-6), OutputPrice: testPtrFloat64(16e-6)},
		},
	}})

	resolved := r.Resolve(context.Background(), billing.PricingInput{
		Model:   "claude-sonnet-4",
		GroupID: billingtestkit.GroupID(),
	})

	require.NotNil(t, resolved)
	require.Equal(t, "channel", resolved.Source)
	require.Len(t, resolved.Intervals, 2)

	// GetIntervalPricing should use channel intervals
	iv := r.GetIntervalPricing(resolved, 50000)
	require.NotNil(t, iv)
	require.InDelta(t, 2e-6, iv.InputPricePerToken, 1e-12)
	require.InDelta(t, 8e-6, iv.OutputPricePerToken, 1e-12)

	iv2 := r.GetIntervalPricing(resolved, 200000)
	require.NotNil(t, iv2)
	require.InDelta(t, 4e-6, iv2.InputPricePerToken, 1e-12)
	require.InDelta(t, 16e-6, iv2.OutputPricePerToken, 1e-12)
}

func TestResolve_WithChannelOverride_PriceMultiplierScalesIntervalsAndFallbackFields(t *testing.T) {
	r := newResolverWithChannel(t, []routing.ChannelModelPricing{{
		Platform:        "anthropic",
		Models:          []string{"claude-sonnet-4"},
		BillingMode:     routing.BillingModeToken,
		PriceMultiplier: testPtrFloat64(2),
		Intervals: []routing.PricingInterval{
			{MinTokens: 0, MaxTokens: testPtrInt(128000), InputPrice: testPtrFloat64(2e-6)},
		},
	}})

	resolved := r.Resolve(context.Background(), billing.PricingInput{
		Model:   "claude-sonnet-4",
		GroupID: billingtestkit.GroupID(),
	})

	pricing := r.GetIntervalPricing(resolved, 50000)
	require.NotNil(t, pricing)
	require.InDelta(t, 4e-6, pricing.InputPricePerToken, 1e-12)
	// 区间未配置输出价时继承模型默认价，再统一乘以倍率。
	require.InDelta(t, 30e-6, pricing.OutputPricePerToken, 1e-12)
}

func TestResolve_WithChannelOverride_FastModeMultiplierAppliesToIntervals(t *testing.T) {
	calculator := billingtestkit.ResolverCalculator()
	r := billingtestkit.ResolverWithCards(t, calculator, []routing.ChannelModelPricing{{
		Platform:           capability.PlatformOpenAI,
		Models:             []string{"claude-sonnet-4"},
		BillingMode:        routing.BillingModeToken,
		PriceMultiplier:    testPtrFloat64(1.25),
		FastModeMultiplier: testPtrFloat64(2),
		Intervals: []routing.PricingInterval{{
			MinTokens:   0,
			MaxTokens:   testPtrInt(128000),
			InputPrice:  testPtrFloat64(2e-6),
			OutputPrice: testPtrFloat64(8e-6),
		}},
	}})

	resolved := r.Resolve(context.Background(), billing.PricingInput{
		Model:   "claude-sonnet-4",
		GroupID: billingtestkit.GroupID(),
	})
	pricing := r.GetIntervalPricing(resolved, 50000)
	require.NotNil(t, pricing)
	require.Equal(t, testPtrFloat64(2), pricing.FastModeMultiplier)

	tokens := billingpricing.UsageTokens{InputTokens: 100, OutputTokens: 10}
	standard, err := calculator.CalculateCostUnified(billing.CostInput{
		Ctx:         context.Background(),
		Model:       "claude-sonnet-4",
		GroupID:     billingtestkit.GroupID(),
		Tokens:      tokens,
		ServiceTier: "",
		Resolver:    r,
		Resolved:    resolved,
	})
	require.NoError(t, err)
	fast, err := calculator.CalculateCostUnified(billing.CostInput{
		Ctx:         context.Background(),
		Model:       "claude-sonnet-4",
		GroupID:     billingtestkit.GroupID(),
		Tokens:      tokens,
		ServiceTier: "priority",
		Resolver:    r,
		Resolved:    resolved,
	})
	require.NoError(t, err)
	// 区间价先应用普通定价倍率，再以最终普通价格为基准应用 Fast 倍率。
	require.InDelta(t, standard.TotalCost*2, fast.TotalCost, 1e-12)
}

func TestResolve_WithChannelOverride_TokenNilBasePricing(t *testing.T) {
	// Base pricing is nil (unknown model), channel has flat prices → creates new BasePricing.
	r := newResolverWithChannel(t, []routing.ChannelModelPricing{{
		Platform:    "anthropic",
		Models:      []string{"unknown-model-xyz"},
		BillingMode: routing.BillingModeToken,
		InputPrice:  testPtrFloat64(7e-6),
		OutputPrice: testPtrFloat64(21e-6),
	}})

	resolved := r.Resolve(context.Background(), billing.PricingInput{
		Model:   "unknown-model-xyz",
		GroupID: billingtestkit.GroupID(),
	})

	require.NotNil(t, resolved)
	require.Equal(t, "channel", resolved.Source)
	// BasePricing was nil from resolveBasePricing but applyTokenOverrides creates a new one
	require.NotNil(t, resolved.BasePricing)
	require.InDelta(t, 7e-6, resolved.BasePricing.InputPricePerToken, 1e-12)
	require.InDelta(t, 21e-6, resolved.BasePricing.OutputPricePerToken, 1e-12)
}

// ---------------------------------------------------------------------------
// 2. Per-request mode overrides
// ---------------------------------------------------------------------------

func TestResolve_WithChannelOverride_PerRequest(t *testing.T) {
	r := newResolverWithChannel(t, []routing.ChannelModelPricing{{
		Platform:        "anthropic",
		Models:          []string{"claude-sonnet-4"},
		BillingMode:     routing.BillingModePerRequest,
		PerRequestPrice: testPtrFloat64(0.05),
		Intervals: []routing.PricingInterval{
			{MinTokens: 0, MaxTokens: testPtrInt(128000), PerRequestPrice: testPtrFloat64(0.03)},
			{MinTokens: 128000, MaxTokens: nil, PerRequestPrice: testPtrFloat64(0.10)},
		},
	}})

	resolved := r.Resolve(context.Background(), billing.PricingInput{
		Model:   "claude-sonnet-4",
		GroupID: billingtestkit.GroupID(),
	})

	require.NotNil(t, resolved)
	require.Equal(t, routing.BillingModePerRequest, resolved.Mode)
	require.Equal(t, "channel", resolved.Source)
	require.InDelta(t, 0.05, resolved.DefaultPerRequestPrice, 1e-12)
	require.Len(t, resolved.RequestTiers, 2)

	// Verify tier lookups
	require.InDelta(t, 0.03, r.GetRequestTierPriceByContext(resolved, 50000), 1e-12)
	require.InDelta(t, 0.10, r.GetRequestTierPriceByContext(resolved, 200000), 1e-12)
}

func TestResolve_WithChannelOverride_PriceMultiplierScalesPerRequestPrices(t *testing.T) {
	r := newResolverWithChannel(t, []routing.ChannelModelPricing{{
		Platform:        "anthropic",
		Models:          []string{"claude-sonnet-4"},
		BillingMode:     routing.BillingModePerRequest,
		PriceMultiplier: testPtrFloat64(2),
		PerRequestPrice: testPtrFloat64(0.05),
		Intervals: []routing.PricingInterval{
			{MinTokens: 0, MaxTokens: testPtrInt(128000), PerRequestPrice: testPtrFloat64(0.03)},
		},
	}})

	resolved := r.Resolve(context.Background(), billing.PricingInput{
		Model:   "claude-sonnet-4",
		GroupID: billingtestkit.GroupID(),
	})

	require.InDelta(t, 0.10, resolved.DefaultPerRequestPrice, 1e-12)
	require.InDelta(t, 0.06, r.GetRequestTierPriceByContext(resolved, 50000), 1e-12)
}

func TestResolve_WithChannelOverride_PerRequestNilPrice(t *testing.T) {
	// PerRequestPrice nil → DefaultPerRequestPrice stays 0.
	r := newResolverWithChannel(t, []routing.ChannelModelPricing{{
		Platform:    "anthropic",
		Models:      []string{"claude-sonnet-4"},
		BillingMode: routing.BillingModePerRequest,
		// PerRequestPrice intentionally nil
		Intervals: []routing.PricingInterval{
			{MinTokens: 0, MaxTokens: testPtrInt(128000), PerRequestPrice: testPtrFloat64(0.02)},
		},
	}})

	resolved := r.Resolve(context.Background(), billing.PricingInput{
		Model:   "claude-sonnet-4",
		GroupID: billingtestkit.GroupID(),
	})

	require.NotNil(t, resolved)
	require.Equal(t, routing.BillingModePerRequest, resolved.Mode)
	require.InDelta(t, 0.0, resolved.DefaultPerRequestPrice, 1e-12)
	require.Len(t, resolved.RequestTiers, 1)
}

// ---------------------------------------------------------------------------
// 3. Image mode overrides
// ---------------------------------------------------------------------------

func TestResolve_WithChannelOverride_Image(t *testing.T) {
	r := newResolverWithChannel(t, []routing.ChannelModelPricing{{
		Platform:        "anthropic",
		Models:          []string{"claude-sonnet-4"},
		BillingMode:     routing.BillingModeImage,
		PerRequestPrice: testPtrFloat64(0.08),
		Intervals: []routing.PricingInterval{
			{TierLabel: "1K", PerRequestPrice: testPtrFloat64(0.04)},
			{TierLabel: "2K", PerRequestPrice: testPtrFloat64(0.08)},
			{TierLabel: "4K", PerRequestPrice: testPtrFloat64(0.16)},
		},
	}})

	resolved := r.Resolve(context.Background(), billing.PricingInput{
		Model:   "claude-sonnet-4",
		GroupID: billingtestkit.GroupID(),
	})

	require.NotNil(t, resolved)
	require.Equal(t, routing.BillingModeImage, resolved.Mode)
	require.Equal(t, "channel", resolved.Source)
	require.InDelta(t, 0.08, resolved.DefaultPerRequestPrice, 1e-12)
	require.Len(t, resolved.RequestTiers, 3)
}

func TestResolve_WithChannelOverride_ImageTierLabels(t *testing.T) {
	r := newResolverWithChannel(t, []routing.ChannelModelPricing{{
		Platform:    "anthropic",
		Models:      []string{"claude-sonnet-4"},
		BillingMode: routing.BillingModeImage,
		Intervals: []routing.PricingInterval{
			{TierLabel: "1K", PerRequestPrice: testPtrFloat64(0.04)},
			{TierLabel: "2K", PerRequestPrice: testPtrFloat64(0.08)},
			{TierLabel: "4K", PerRequestPrice: testPtrFloat64(0.16)},
		},
	}})

	resolved := r.Resolve(context.Background(), billing.PricingInput{
		Model:   "claude-sonnet-4",
		GroupID: billingtestkit.GroupID(),
	})

	require.InDelta(t, 0.04, r.GetRequestTierPrice(resolved, "1K"), 1e-12)
	require.InDelta(t, 0.08, r.GetRequestTierPrice(resolved, "2K"), 1e-12)
	require.InDelta(t, 0.16, r.GetRequestTierPrice(resolved, "4K"), 1e-12)
	require.InDelta(t, 0.0, r.GetRequestTierPrice(resolved, "8K"), 1e-12) // not found
}

// ---------------------------------------------------------------------------
// 4. Source tracking & default mode
// ---------------------------------------------------------------------------

func TestResolve_WithChannelOverride_SourceIsChannel(t *testing.T) {
	r := newResolverWithChannel(t, []routing.ChannelModelPricing{{
		Platform:    "anthropic",
		Models:      []string{"claude-sonnet-4"},
		BillingMode: routing.BillingModeToken,
		InputPrice:  testPtrFloat64(1e-6),
	}})

	resolved := r.Resolve(context.Background(), billing.PricingInput{
		Model:   "claude-sonnet-4",
		GroupID: billingtestkit.GroupID(),
	})

	require.Equal(t, "channel", resolved.Source)
}

func TestResolve_WithChannelOverride_DefaultMode(t *testing.T) {
	// Channel pricing with empty BillingMode → defaults to BillingModeToken.
	r := newResolverWithChannel(t, []routing.ChannelModelPricing{{
		Platform:    "anthropic",
		Models:      []string{"claude-sonnet-4"},
		BillingMode: "", // intentionally empty
		InputPrice:  testPtrFloat64(5e-6),
	}})

	resolved := r.Resolve(context.Background(), billing.PricingInput{
		Model:   "claude-sonnet-4",
		GroupID: billingtestkit.GroupID(),
	})

	require.Equal(t, "channel", resolved.Source)
	require.Equal(t, routing.BillingModeToken, resolved.Mode)
	require.NotNil(t, resolved.BasePricing)
	require.InDelta(t, 5e-6, resolved.BasePricing.InputPricePerToken, 1e-12)
}

// ---------------------------------------------------------------------------
// 5. GetIntervalPricing integration after channel override
// ---------------------------------------------------------------------------

func TestGetIntervalPricing_WithChannelIntervals(t *testing.T) {
	// Channel provides intervals that override the base pricing path.
	r := newResolverWithChannel(t, []routing.ChannelModelPricing{{
		Platform:    "anthropic",
		Models:      []string{"claude-sonnet-4"},
		BillingMode: routing.BillingModeToken,
		Intervals: []routing.PricingInterval{
			{MinTokens: 0, MaxTokens: testPtrInt(100000), InputPrice: testPtrFloat64(1e-6), OutputPrice: testPtrFloat64(5e-6)},
			{MinTokens: 100000, MaxTokens: nil, InputPrice: testPtrFloat64(2e-6), OutputPrice: testPtrFloat64(10e-6)},
		},
	}})

	resolved := r.Resolve(context.Background(), billing.PricingInput{
		Model:   "claude-sonnet-4",
		GroupID: billingtestkit.GroupID(),
	})

	// Token count 50000 matches first interval
	pricing := r.GetIntervalPricing(resolved, 50000)
	require.NotNil(t, pricing)
	require.InDelta(t, 1e-6, pricing.InputPricePerToken, 1e-12)
	require.InDelta(t, 5e-6, pricing.OutputPricePerToken, 1e-12)

	// Token count 150000 matches second interval
	pricing2 := r.GetIntervalPricing(resolved, 150000)
	require.NotNil(t, pricing2)
	require.InDelta(t, 2e-6, pricing2.InputPricePerToken, 1e-12)
	require.InDelta(t, 10e-6, pricing2.OutputPricePerToken, 1e-12)
}

func TestGetIntervalPricing_ChannelIntervalsNoMatch(t *testing.T) {
	// Channel intervals don't match token count → falls back to BasePricing.
	r := newResolverWithChannel(t, []routing.ChannelModelPricing{{
		Platform:    "anthropic",
		Models:      []string{"claude-sonnet-4"},
		BillingMode: routing.BillingModeToken,
		Intervals: []routing.PricingInterval{
			// Only covers tokens > 50000
			{MinTokens: 50000, MaxTokens: testPtrInt(200000), InputPrice: testPtrFloat64(9e-6)},
		},
	}})

	resolved := r.Resolve(context.Background(), billing.PricingInput{
		Model:   "claude-sonnet-4",
		GroupID: billingtestkit.GroupID(),
	})

	// Token count 1000 doesn't match any interval (1000 <= 50000 minTokens)
	pricing := r.GetIntervalPricing(resolved, 1000)
	// Should fall back to BasePricing (from the billing service fallback)
	require.NotNil(t, pricing)
	require.Equal(t, resolved.BasePricing, pricing)
	require.InDelta(t, 3e-6, pricing.InputPricePerToken, 1e-12) // original base price
}

// ===========================================================================
// 6. Error path tests
// ===========================================================================

func TestResolve_WithChannelOverride_CacheError(t *testing.T) {
	// When ListAll returns an error, the ChannelService cache build fails.
	// Resolve should gracefully fall back to base pricing without panicking.
	repo := &routingtestkit.ChannelRepositoryStub{
		ListAllFn: func(_ context.Context) ([]routing.Channel, error) {
			return nil, errors.New("database unavailable")
		},
	}
	cs := routing.NewChannelService(repo, nil, routing.ChannelOptions{Warn: slog.
		Warn,
		Now: time.
			Now, LoadLocation: pricingprovider.
			LoadPricingLocation,
	},
	)
	bs := billingtestkit.ResolverCalculator()
	r := billingtestkit.PriceResolver(cs, bs)

	gid := int64(100)
	resolved := r.Resolve(context.Background(), billing.PricingInput{
		Model:   "claude-sonnet-4",
		GroupID: &gid,
	})

	require.NotNil(t, resolved)
	// Should NOT panic, should NOT have source "channel"
	require.NotEqual(t, "channel", resolved.Source)
	// Base pricing should still be present (from BillingService fallback)
	require.NotNil(t, resolved.BasePricing)
	require.InDelta(t, 3e-6, resolved.BasePricing.InputPricePerToken, 1e-12)
}

// ===========================================================================
// 7. GetRequestTierPriceByContext boundary tests
// ===========================================================================

func TestGetRequestTierPriceByContext_EmptyTiers(t *testing.T) {
	bs := billingtestkit.ResolverCalculator()
	r := billingtestkit.PriceResolver(routing.NewChannelService(nil, nil, routing.ChannelOptions{Warn: slog.
		Warn,
		Now: time.
			Now, LoadLocation: pricingprovider.
			LoadPricingLocation,
	}),

		bs)

	resolved := &billingpricing.ResolvedPricing{
		Mode:         routing.BillingModePerRequest,
		RequestTiers: nil, // empty
	}

	price := r.GetRequestTierPriceByContext(resolved, 50000)
	require.InDelta(t, 0.0, price, 1e-12)

	// Also test with explicit empty slice
	resolved2 := &billingpricing.ResolvedPricing{
		Mode:         routing.BillingModePerRequest,
		RequestTiers: []routing.PricingInterval{},
	}

	price2 := r.GetRequestTierPriceByContext(resolved2, 50000)
	require.InDelta(t, 0.0, price2, 1e-12)
}

func TestGetRequestTierPriceByContext_ExactBoundary(t *testing.T) {
	bs := billingtestkit.ResolverCalculator()
	r := billingtestkit.PriceResolver(routing.NewChannelService(nil, nil, routing.ChannelOptions{Warn: slog.
		Warn,
		Now: time.
			Now, LoadLocation: pricingprovider.
			LoadPricingLocation,
	}),

		bs)

	resolved := &billingpricing.ResolvedPricing{
		Mode: routing.BillingModePerRequest,
		RequestTiers: []routing.PricingInterval{
			{MinTokens: 0, MaxTokens: testPtrInt(128000), PerRequestPrice: testPtrFloat64(0.05)},
			{MinTokens: 128000, MaxTokens: nil, PerRequestPrice: testPtrFloat64(0.10)},
		},
	}

	// totalContextTokens = 128000 exactly:
	// FindMatchingInterval checks: totalTokens > MinTokens && totalTokens <= MaxTokens
	// For first interval: 128000 > 0 (true) && 128000 <= 128000 (true) → matches first interval
	price := r.GetRequestTierPriceByContext(resolved, 128000)
	require.InDelta(t, 0.05, price, 1e-12)

	// totalContextTokens = 128001 should match second interval
	// For first interval: 128001 > 0 (true) && 128001 <= 128000 (false) → no match
	// For second interval: 128001 > 128000 (true) && MaxTokens == nil → matches
	price2 := r.GetRequestTierPriceByContext(resolved, 128001)
	require.InDelta(t, 0.10, price2, 1e-12)
}

// ===========================================================================
// 8. interval filtering
// ===========================================================================

func TestFilterValidTokenIntervals(t *testing.T) {
	tests := []struct {
		name      string
		intervals []routing.PricingInterval
		wantLen   int
	}{
		{
			name:      "empty list",
			intervals: nil,
			wantLen:   0,
		},
		{
			name: "all-nil interval filtered out",
			intervals: []routing.PricingInterval{
				{MinTokens: 0, MaxTokens: testPtrInt(128000)},
			},
			wantLen: 0,
		},
		{
			name: "interval with only InputPrice kept",
			intervals: []routing.PricingInterval{
				{MinTokens: 0, MaxTokens: testPtrInt(128000), InputPrice: testPtrFloat64(1e-6)},
			},
			wantLen: 1,
		},
		{
			name: "interval with only OutputPrice kept",
			intervals: []routing.PricingInterval{
				{MinTokens: 0, MaxTokens: testPtrInt(128000), OutputPrice: testPtrFloat64(2e-6)},
			},
			wantLen: 1,
		},
		{
			name: "interval with only CacheWritePrice kept",
			intervals: []routing.PricingInterval{
				{MinTokens: 0, CacheWritePrice: testPtrFloat64(3e-6)},
			},
			wantLen: 1,
		},
		{
			name: "interval with only CacheReadPrice kept",
			intervals: []routing.PricingInterval{
				{MinTokens: 0, CacheReadPrice: testPtrFloat64(0.5e-6)},
			},
			wantLen: 1,
		},
		{
			name: "interval with only InputMultiplier kept",
			intervals: []routing.PricingInterval{
				{MinTokens: 0, InputMultiplier: testPtrFloat64(1.2)},
			},
			wantLen: 1,
		},
		{
			name: "interval with only PerRequestPrice filtered out for token mode",
			intervals: []routing.PricingInterval{
				{TierLabel: "1K", PerRequestPrice: testPtrFloat64(0.04)},
			},
			wantLen: 0,
		},
		{
			name: "mixed valid and invalid",
			intervals: []routing.PricingInterval{
				{MinTokens: 0, MaxTokens: testPtrInt(128000), InputPrice: testPtrFloat64(1e-6)},
				{MinTokens: 128000, MaxTokens: nil}, // all-nil → filtered out
				{MinTokens: 256000, OutputPrice: testPtrFloat64(5e-6)},
			},
			wantLen: 2,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := billingpricing.FilterValidTokenIntervals(tt.intervals)
			require.Len(t, result, tt.wantLen)
		})
	}
}

func TestFilterValidRequestIntervals(t *testing.T) {
	tests := []struct {
		name      string
		intervals []routing.PricingInterval
		wantLen   int
	}{
		{
			name: "all-nil interval filtered out",
			intervals: []routing.PricingInterval{
				{TierLabel: "1K"},
			},
			wantLen: 0,
		},
		{
			name: "token-only interval filtered out for request mode",
			intervals: []routing.PricingInterval{
				{MinTokens: 0, MaxTokens: testPtrInt(128000), InputPrice: testPtrFloat64(1e-6)},
			},
			wantLen: 0,
		},
		{
			name: "per-request interval kept",
			intervals: []routing.PricingInterval{
				{TierLabel: "1K", PerRequestPrice: testPtrFloat64(0.04)},
			},
			wantLen: 1,
		},
		{
			name: "explicit zero per-request interval kept",
			intervals: []routing.PricingInterval{
				{TierLabel: "1K", PerRequestPrice: testPtrFloat64(0)},
			},
			wantLen: 1,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := billingpricing.FilterValidRequestIntervals(tt.intervals)
			require.Len(t, result, tt.wantLen)
		})
	}
}

func TestChannelModelPricingHasEffectivePricingIsModeAware(t *testing.T) {
	require.False(t, (&routing.ChannelModelPricing{
		BillingMode: routing.BillingModeToken,
		Intervals: []routing.PricingInterval{
			{TierLabel: "1K", PerRequestPrice: testPtrFloat64(0.04)},
		},
	}).HasEffectivePricing())

	require.False(t, (&routing.ChannelModelPricing{
		BillingMode: routing.BillingModePerRequest,
		Intervals: []routing.PricingInterval{
			{MinTokens: 0, InputPrice: testPtrFloat64(1e-6)},
		},
	}).HasEffectivePricing())

	require.True(t, (&routing.ChannelModelPricing{
		BillingMode: routing.BillingModePerRequest,
		Intervals: []routing.PricingInterval{
			{TierLabel: "1K", PerRequestPrice: testPtrFloat64(0)},
		},
	}).HasEffectivePricing())

	require.True(t, (&routing.ChannelModelPricing{
		BillingMode:     routing.BillingModeVideo,
		PerRequestPrice: testPtrFloat64(0),
	}).HasEffectivePricing())

	require.True(t, (&routing.ChannelModelPricing{
		BillingMode: routing.BillingModeToken,
		InputPrice:  testPtrFloat64(0),
	}).HasEffectivePricing())

	require.True(t, (&routing.ChannelModelPricing{
		BillingMode:    routing.BillingModeToken,
		FastMultiplier: testPtrFloat64(1.5),
	}).HasEffectivePricing())
}

// ===========================================================================
// 9. 图片输出价格显式标记测试
// ===========================================================================

func TestApplyTokenOverrides_FlatSetsImageOutputPriceExplicit(t *testing.T) {
	r := newResolverWithChannel(t, []routing.ChannelModelPricing{{
		Platform:    "anthropic",
		Models:      []string{"claude-sonnet-4"},
		BillingMode: routing.BillingModeToken,
		InputPrice:  testPtrFloat64(3e-6),
		OutputPrice: testPtrFloat64(15e-6),
		// 图片输出价格有意保持为空
	}})
	resolved := r.Resolve(context.Background(), billing.PricingInput{
		Model:   "claude-sonnet-4",
		GroupID: billingtestkit.GroupID(),
	})

	require.Equal(t, billingpricing.PricingSourceChannel, resolved.Source)
	require.True(t, resolved.BasePricing.ImageOutputPriceExplicit)
	require.Equal(t, 0.0, resolved.BasePricing.ImageOutputPricePerToken)
}

func TestApplyTokenOverrides_FlatWithImageOutputPriceSetsExplicit(t *testing.T) {
	r := newResolverWithChannel(t, []routing.ChannelModelPricing{{
		Platform:         "anthropic",
		Models:           []string{"claude-sonnet-4"},
		BillingMode:      routing.BillingModeToken,
		InputPrice:       testPtrFloat64(3e-6),
		OutputPrice:      testPtrFloat64(15e-6),
		ImageOutputPrice: testPtrFloat64(50e-6),
	}})
	resolved := r.Resolve(context.Background(), billing.PricingInput{
		Model:   "claude-sonnet-4",
		GroupID: billingtestkit.GroupID(),
	})

	require.True(t, resolved.BasePricing.ImageOutputPriceExplicit)
	require.InDelta(t, 50e-6, resolved.BasePricing.ImageOutputPricePerToken, 1e-12)
}

func TestApplyTokenOverrides_FlatUsesChannelImageInputPrice(t *testing.T) {
	r := newResolverWithChannel(t, []routing.ChannelModelPricing{{
		Platform:        "anthropic",
		Models:          []string{"claude-sonnet-4"},
		BillingMode:     routing.BillingModeToken,
		InputPrice:      testPtrFloat64(3e-6),
		ImageInputPrice: testPtrFloat64(8e-6),
	}})
	resolved := r.Resolve(context.Background(), billing.PricingInput{
		Model:   "claude-sonnet-4",
		GroupID: billingtestkit.GroupID(),
	})

	require.Equal(t, billingpricing.PricingSourceChannel, resolved.Source)
	require.InDelta(t, 8e-6, resolved.BasePricing.ImageInputPricePerToken, 1e-12)
}

func TestIntervalToModelPricingWithBaseClearsUnsetChannelImageInputPrice(t *testing.T) {
	base := &billingpricing.ModelPricing{ImageInputPricePerToken: 99e-6}
	pricing := billingpricing.IntervalToModelPricingWithBase(
		&routing.PricingInterval{MinTokens: 0, InputPrice: testPtrFloat64(3e-6)},
		false,
		&routing.ChannelModelPricing{BillingMode: routing.BillingModeToken},
		base,
	)

	require.Zero(t, pricing.ImageInputPricePerToken)
}

func TestIntervalToModelPricingWithBaseAppliesMultipliersAndPreservesTierRatio(t *testing.T) {
	base := &billingpricing.ModelPricing{
		InputPricePerToken:                 2e-6,
		InputPricePerTokenPriority:         4e-6,
		OutputPricePerToken:                10e-6,
		OutputPricePerTokenPriority:        20e-6,
		CacheCreationPricePerToken:         3e-6,
		CacheCreationPricePerTokenPriority: 6e-6,
		CacheReadPricePerToken:             0.5e-6,
		CacheReadPricePerTokenPriority:     1e-6,
	}
	pricing := billingpricing.IntervalToModelPricingWithBase(
		&routing.PricingInterval{
			InputMultiplier:      testPtrFloat64(1.5),
			OutputMultiplier:     testPtrFloat64(0.5),
			CacheWriteMultiplier: testPtrFloat64(2),
			CacheReadMultiplier:  testPtrFloat64(0.25),
		},
		false,
		&routing.ChannelModelPricing{BillingMode: routing.BillingModeToken, FastMultiplier: testPtrFloat64(2), FlexMultiplier: testPtrFloat64(0.25)},
		base,
	)

	require.InDelta(t, 3e-6, pricing.InputPricePerToken, 1e-12)
	require.InDelta(t, 6e-6, pricing.InputPricePerTokenPriority, 1e-12)
	require.InDelta(t, 5e-6, pricing.OutputPricePerToken, 1e-12)
	require.InDelta(t, 10e-6, pricing.OutputPricePerTokenPriority, 1e-12)
	require.InDelta(t, 6e-6, pricing.CacheCreationPricePerToken, 1e-12)
	require.InDelta(t, 12e-6, pricing.CacheCreationPricePerTokenPriority, 1e-12)
	require.InDelta(t, 0.125e-6, pricing.CacheReadPricePerToken, 1e-12)
	require.InDelta(t, 0.25e-6, pricing.CacheReadPricePerTokenPriority, 1e-12)
	require.NotNil(t, pricing.FastMultiplier)
	require.NotNil(t, pricing.FlexMultiplier)
	require.InDelta(t, 2, *pricing.FastMultiplier, 1e-12)
	require.InDelta(t, 0.25, *pricing.FlexMultiplier, 1e-12)
}

func TestApplyTokenOverrides_IntervalSetsImageOutputPriceExplicit(t *testing.T) {
	r := newResolverWithChannel(t, []routing.ChannelModelPricing{{
		Platform:    "anthropic",
		Models:      []string{"claude-sonnet-4"},
		BillingMode: routing.BillingModeToken,
		// 不配置图片输出价格
		Intervals: []routing.PricingInterval{
			{MinTokens: 0, MaxTokens: testPtrInt(100000), InputPrice: testPtrFloat64(3e-6), OutputPrice: testPtrFloat64(15e-6)},
		},
	}})
	resolved := r.Resolve(context.Background(), billing.PricingInput{
		Model:   "claude-sonnet-4",
		GroupID: billingtestkit.GroupID(),
	})

	// 基础定价应带显式标记，供区间未命中时回退使用
	require.True(t, resolved.BasePricing.ImageOutputPriceExplicit)
	require.Equal(t, 0.0, resolved.BasePricing.ImageOutputPricePerToken)

	// 区间定价也应带显式标记
	pricing := r.GetIntervalPricing(resolved, 50000)
	require.True(t, pricing.ImageOutputPriceExplicit)
	require.Equal(t, 0.0, pricing.ImageOutputPricePerToken)
}

func TestIntervalToModelPricingWithBaseClearsImageOutputWhenChannelUnset(t *testing.T) {
	base := &billingpricing.ModelPricing{
		ImageOutputPricePerToken: 99e-6,
	}
	pricing := billingpricing.IntervalToModelPricingWithBase(
		&routing.PricingInterval{
			MinTokens:   0,
			InputPrice:  testPtrFloat64(3e-6),
			OutputPrice: testPtrFloat64(15e-6),
		},
		false,
		&routing.ChannelModelPricing{BillingMode: routing.BillingModeToken},
		base,
	)

	require.True(t, pricing.ImageOutputPriceExplicit)
	require.Equal(t, 0.0, pricing.ImageOutputPricePerToken)
}

// TestApplyTokenOverrides_FlatDoesNotPolluteFallbackPrices 验证平铺覆盖路径
// 会先克隆 BasePricing，避免写穿 fallbackPrices 中的共享条目。
func TestApplyTokenOverrides_FlatDoesNotPolluteFallbackPrices(t *testing.T) {
	prices := billingtestkit.ResolverFallbackPrices()
	calculator := newCalculatorWithPrices(nil, nil, prices)
	r := billingtestkit.ResolverWithCards(t, calculator, []routing.ChannelModelPricing{{
		Platform:    "anthropic",
		Models:      []string{"claude-sonnet-4"},
		BillingMode: routing.BillingModeToken,
		InputPrice:  testPtrFloat64(10e-6), // 基础价格为 3e-6
		OutputPrice: testPtrFloat64(50e-6), // 基础价格为 15e-6
	}})

	resolved := r.Resolve(context.Background(), billing.PricingInput{
		Model:   "claude-sonnet-4",
		GroupID: billingtestkit.GroupID(),
	})

	// 解析结果应采用渠道覆盖价格。
	require.NotNil(t, resolved)
	require.InDelta(t, 10e-6, resolved.BasePricing.InputPricePerToken, 1e-12)
	require.InDelta(t, 50e-6, resolved.BasePricing.OutputPricePerToken, 1e-12)

	// 全局 fallbackPrices 不得被污染。
	fp := prices["claude-sonnet-4"]
	require.InDelta(t, 3e-6, fp.InputPricePerToken, 1e-12, "fallback InputPricePerToken polluted")
	require.InDelta(t, 15e-6, fp.OutputPricePerToken, 1e-12, "fallback OutputPricePerToken polluted")
	require.False(t, fp.ImageOutputPriceExplicit, "fallback ImageOutputPriceExplicit polluted")
}

// TestApplyTokenOverrides_IntervalDoesNotPolluteFallbackPrices 验证区间覆盖路径
// 同样会在修改前克隆基础定价。
func TestApplyTokenOverrides_IntervalDoesNotPolluteFallbackPrices(t *testing.T) {
	prices := billingtestkit.ResolverFallbackPrices()
	calculator := newCalculatorWithPrices(nil, nil, prices)
	r := billingtestkit.ResolverWithCards(t, calculator, []routing.ChannelModelPricing{{
		Platform:    "anthropic",
		Models:      []string{"claude-sonnet-4"},
		BillingMode: routing.BillingModeToken,
		Intervals: []routing.PricingInterval{
			{MinTokens: 0, MaxTokens: testPtrInt(100000), InputPrice: testPtrFloat64(2e-6), OutputPrice: testPtrFloat64(8e-6)},
		},
	}})

	resolved := r.Resolve(context.Background(), billing.PricingInput{
		Model:   "claude-sonnet-4",
		GroupID: billingtestkit.GroupID(),
	})

	require.NotNil(t, resolved)
	require.True(t, resolved.BasePricing.ImageOutputPriceExplicit)

	// 全局 fallbackPrices 不得被污染。
	fp := prices["claude-sonnet-4"]
	require.InDelta(t, 3e-6, fp.InputPricePerToken, 1e-12, "fallback InputPricePerToken polluted")
	require.InDelta(t, 15e-6, fp.OutputPricePerToken, 1e-12, "fallback OutputPricePerToken polluted")
	require.False(t, fp.ImageOutputPriceExplicit, "fallback ImageOutputPriceExplicit polluted")
}

func TestResolve_GroupPricingOverridesChannel(t *testing.T) {
	r := newResolverWithChannel(t, []routing.ChannelModelPricing{{
		Platform: "anthropic", Models: []string{"claude-sonnet-4"}, BillingMode: routing.BillingModeToken,
		InputPrice: testPtrFloat64(10e-6), OutputPrice: testPtrFloat64(20e-6),
	}})
	group := &routing.Group{ID: 100, ModelPricing: []routing.ChannelModelPricing{{
		Models: []string{"claude-sonnet-*"}, BillingMode: routing.BillingModeToken,
		InputPrice: testPtrFloat64(1e-6), OutputPrice: testPtrFloat64(2e-6),
	}}}
	resolved := r.Resolve(context.Background(), billing.PricingInput{Model: "claude-sonnet-4", GroupID: billingtestkit.GroupID(), Group: projectPriceGroup(group)})

	require.Equal(t, billingpricing.PricingSourceGroup, resolved.Source)
	require.InDelta(t, 1e-6, resolved.BasePricing.InputPricePerToken, 1e-12)
	require.InDelta(t, 2e-6, resolved.BasePricing.OutputPricePerToken, 1e-12)
}

func TestResolve_GroupContextIntervalsOverridePresetRegardlessOfToggle(t *testing.T) {
	prices := billingtestkit.ResolverFallbackPrices()
	prices["claude-sonnet-4"].LongContextInputThreshold = 200000
	prices["claude-sonnet-4"].LongContextThresholdInclusive = true
	prices["claude-sonnet-4"].LongContextInputMultiplier = 2
	prices["claude-sonnet-4"].LongContextOutputMultiplier = 2
	bs := newCalculatorWithPrices(nil, nil, prices)
	r := billingtestkit.PriceResolver(nil, bs)
	group := &routing.Group{ID: 100, ModelPricing: []routing.ChannelModelPricing{{
		Models: []string{"claude-sonnet-4"}, BillingMode: routing.BillingModeToken,
		InputPrice: testPtrFloat64(1e-6), OutputPrice: testPtrFloat64(2e-6),
		Intervals: []routing.PricingInterval{
			{MinTokens: 0, MaxTokens: testPtrInt(200000), InputPrice: testPtrFloat64(9e-6)},
			{MinTokens: 200000, InputPrice: testPtrFloat64(18e-6)},
		},
	}}}

	resolved := r.Resolve(context.Background(), billing.PricingInput{Model: "claude-sonnet-4", Group: projectPriceGroup(group)})
	require.False(t, resolved.LongContextPricingEnabled)
	require.Len(t, resolved.Intervals, 2)
	require.InDelta(t, 18e-6, r.GetIntervalPricing(resolved, 300000).InputPricePerToken, 1e-12)
	require.Equal(t, 200000, resolved.BasePricing.LongContextInputThreshold)

	group.LongContextPricingEnabled = true
	resolved = r.Resolve(context.Background(), billing.PricingInput{Model: "claude-sonnet-4", Group: projectPriceGroup(group)})
	require.True(t, resolved.LongContextPricingEnabled)
	require.Len(t, resolved.Intervals, 2)
	require.InDelta(t, 18e-6, r.GetIntervalPricing(resolved, 300000).InputPricePerToken, 1e-12)
	require.Equal(t, 200000, resolved.BasePricing.LongContextInputThreshold)
	require.InDelta(t, 2.0, resolved.BasePricing.LongContextInputMultiplier, 1e-12)
}

func TestCalculateCostUnified_UsesContinuousMediaUnits(t *testing.T) {
	bs := billingtestkit.ResolverCalculator()
	r := billingtestkit.PriceResolver(nil, bs)
	price := 0.08
	group := &routing.Group{ModelPricing: []routing.ChannelModelPricing{{
		Models: []string{"grok-voice-think-fast-2.0"}, BillingMode: routing.BillingModePerRequest,
		PerRequestPrice: &price,
	}}}
	cost, err := bs.CalculateCostUnified(billing.CostInput{
		Ctx: context.Background(), Model: "grok-voice-think-fast-2.0", Group: projectPriceGroup(group),
		UsageUnits: 1.5, RateMultiplier: 1, Resolver: r,
	})
	require.NoError(t, err)
	require.InDelta(t, 0.12, cost.TotalCost, 1e-12)
}
