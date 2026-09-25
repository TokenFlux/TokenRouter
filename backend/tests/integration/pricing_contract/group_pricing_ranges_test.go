//go:build unit

package pricingcontract

import (
	"context"
	"fmt"
	"testing"

	pricingprovider "github.com/TokenFlux/TokenRouter/internal/billing/provider"

	"github.com/TokenFlux/TokenRouter/internal/billing"
	"github.com/TokenFlux/TokenRouter/internal/billing/pricing"
	gatewaycapture "github.com/TokenFlux/TokenRouter/internal/gateway/provider"

	billingtestkit "github.com/TokenFlux/TokenRouter/internal/billing/testkit"
	"github.com/TokenFlux/TokenRouter/internal/routing"
	"github.com/TokenFlux/TokenRouter/internal/routing/capability"
	"github.com/stretchr/testify/require"
)

// 展示必须补齐首段、中间空档和尾段，并与同一上下文的实际单价一致。
func TestPricingDisplayPreservesDefaultRanges(t *testing.T) {
	for _, source := range []string{"group", "channel"} {
		for _, freeFast := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/freeFast=%v", source, freeFast), func(t *testing.T) {
				card := routing.ChannelModelPricing{Platform: capability.PlatformOpenAI, Models: []string{"custom-ranges"}, BillingMode: routing.BillingModeToken,
					InputPrice: testPtrFloat64(0.001), FastMultiplier: testPtrFloat64(3),
					// 有意按倒序保存，展示排序不能改动管理员配置。
					Intervals: []routing.PricingInterval{{MinTokens: 200, MaxTokens: testPtrInt(300), InputPrice: testPtrFloat64(0.003)},
						{MinTokens: 100, MaxTokens: testPtrInt(150), InputPrice: testPtrFloat64(0.002)}},
				}
				group := &routing.Group{ID: 100, Platform: capability.PlatformOpenAI, RateMultiplier: 1.5, FreeOpenAIFast: freeFast, ModelPricing: []routing.ChannelModelPricing{card}}
				rCalculator := billingtestkit.ResolverCalculator()
				r := billingtestkit.ResolverWithCards(t, rCalculator, nil)
				if source == "channel" {
					rCalculator = billingtestkit.ResolverCalculator()
					r = billingtestkit.ResolverWithCards(t, rCalculator, []routing.ChannelModelPricing{card})
					group.ModelPricing = nil
				}
				market := newPricingMarketplaceFixture(nil, nil, r, rCalculator, nil, nil, nil)
				display := market.PublicModelPricing(context.Background(), group, "custom-ranges")
				require.Len(t, display.ContextIntervals, 5)
				require.Equal(t, 200, card.Intervals[0].MinTokens)
				fastRatio := 3.0
				if freeFast {
					fastRatio = 1
				}
				for i, min := range []int{0, 100, 150, 200, 300} {
					require.Equal(t, min, display.ContextIntervals[i].MinTokens)
					if i < 4 {
						require.Equal(t, []int{100, 150, 200, 300}[i], *display.ContextIntervals[i].MaxTokens)
					} else {
						require.Nil(t, display.ContextIntervals[i].MaxTokens)
					}
					require.InDelta(t, display.ContextIntervals[i].InputPricePerToken*fastRatio, display.ContextIntervals[i].FastInputPricePerToken, 1e-12)
				}
				for _, count := range []int{50, 100, 101, 150, 151, 200, 201, 300, 301} {
					var displayed float64
					for _, interval := range display.ContextIntervals {
						if count > interval.MinTokens && (interval.MaxTokens == nil || count <= *interval.MaxTokens) {
							displayed = interval.InputPricePerToken
							break
						}
					}
					cost, err := rCalculator.CalculateCostUnified(billing.CostInput{Ctx: context.Background(), Model: "custom-ranges", Group: gatewaycapture.ProjectCompletionPriceGroup(group), GroupID: &group.ID,
						Tokens: pricing.UsageTokens{InputTokens: count}, RateMultiplier: group.RateMultiplier, Resolver: r})
					require.NoError(t, err)
					require.InDelta(t, displayed*float64(count), cost.ActualCost, 1e-12, "context=%d", count)
				}
			})
		}
	}
}

// 默认价与全部区间价格相同才允许压平；缺少基础价的范围不能用其它区间价代替。
func TestPricingDisplayOnlyFlattensCompleteUniformRanges(t *testing.T) {
	rCalculator := billingtestkit.ResolverCalculator()
	r := billingtestkit.PriceResolver(nil, rCalculator)
	for _, pricedBase := range []bool{true, false} {
		card := routing.ChannelModelPricing{Models: []string{"custom-uniform"}, BillingMode: routing.BillingModeToken,
			Intervals: []routing.PricingInterval{{MinTokens: 100, MaxTokens: testPtrInt(200), InputPrice: testPtrFloat64(0.001)}},
		}
		if pricedBase {
			card.InputPrice = testPtrFloat64(0.001)
		}
		group := &routing.Group{ID: 1, Platform: capability.PlatformOpenAI, RateMultiplier: 1, ModelPricing: []routing.ChannelModelPricing{card}}
		market := newPricingMarketplaceFixture(nil, nil, r, rCalculator, nil, nil, nil)
		display := market.PublicModelPricing(context.Background(), group, "custom-uniform")
		if pricedBase {
			require.Empty(t, display.ContextIntervals)
			require.Equal(t, 0.001, display.InputPricePerToken)
		} else {
			require.Len(t, display.ContextIntervals, 1)
			require.Equal(t, 100, display.ContextIntervals[0].MinTokens)
			require.Equal(t, 200, *display.ContextIntervals[0].MaxTokens)
		}
	}
}

// 倍率只调整存在的基础价；显式默认零价和显式区间零价仍是有效免费价格。
func TestPricingIntervalsDistinguishMissingBaseFromExplicitZero(t *testing.T) {
	for _, source := range []string{"group", "channel"} {
		for _, kind := range []string{"missing", "builtin", "zero_base", "zero_interval"} {
			t.Run(source+"/"+kind, func(t *testing.T) {
				model := "custom-no-base"
				if kind == "builtin" {
					model = "claude-sonnet-4"
				}
				card := routing.ChannelModelPricing{Platform: capability.PlatformOpenAI, Models: []string{model}, BillingMode: routing.BillingModeToken,
					Intervals: []routing.PricingInterval{{MinTokens: 0, InputMultiplier: testPtrFloat64(2)}},
				}
				if kind == "zero_base" {
					card.InputPrice = testPtrFloat64(0)
				}
				if kind == "zero_interval" {
					card.Intervals[0].InputPrice = testPtrFloat64(0)
				}
				_, err := (routing.ChannelValidation{LoadLocation: pricingprovider.LoadPricingLocation}).NormalizeGroupPricing(capability.PlatformOpenAI, []routing.ChannelModelPricing{card})
				require.NoError(t, err)
				group := &routing.Group{ID: 100, Platform: capability.PlatformOpenAI, RateMultiplier: 1, ModelPricing: []routing.ChannelModelPricing{card}}
				rCalculator := billingtestkit.ResolverCalculator()
				r := billingtestkit.ResolverWithCards(t, rCalculator, nil)
				if source == "channel" {
					rCalculator = billingtestkit.ResolverCalculator()
					r = billingtestkit.ResolverWithCards(t, rCalculator, []routing.ChannelModelPricing{card})
					group.ModelPricing = nil
				}
				cost, err := rCalculator.CalculateCostUnified(billing.CostInput{Ctx: context.Background(), Model: model, Group: gatewaycapture.ProjectCompletionPriceGroup(group), GroupID: &group.ID,
					Tokens: pricing.UsageTokens{InputTokens: 50}, RateMultiplier: 1, Resolver: r})
				market := newPricingMarketplaceFixture(nil, nil, r, rCalculator, nil, nil, nil)
				display := market.PublicModelPricing(context.Background(), group, model)
				if kind == "missing" {
					require.ErrorIs(t, err, pricing.ErrModelPricingUnavailable)
					require.Nil(t, cost)
					require.Equal(t, "unpriced", display.PriceStatus)
				} else {
					require.NoError(t, err)
					require.Equal(t, "priced", display.PriceStatus)
					expected := 0.0
					if kind == "builtin" {
						expected = 50 * 3e-6 * 2
					}
					require.InDelta(t, expected, cost.ActualCost, 1e-12)
				}
			})
		}
	}
}

// 部分范围有显式价格不代表其它范围也有价；缺价的倍率区间必须继续报缺价。
func TestPricingMissingMultiplierRangeDoesNotBorrowOtherIntervalPrice(t *testing.T) {
	rCalculator := billingtestkit.ResolverCalculator()
	r := billingtestkit.PriceResolver(nil, rCalculator)
	group := &routing.Group{ID: 1, Platform: capability.PlatformOpenAI, RateMultiplier: 1, ModelPricing: []routing.ChannelModelPricing{{Models: []string{"custom-partial"}, BillingMode: routing.BillingModeToken,
		Intervals: []routing.PricingInterval{{MinTokens: 0, MaxTokens: testPtrInt(100), InputMultiplier: testPtrFloat64(2)}, {MinTokens: 100, InputPrice: testPtrFloat64(0.003)}}}},
	}
	for _, count := range []int{50, 150} {
		cost, err := rCalculator.CalculateCostUnified(billing.CostInput{Ctx: context.Background(), Model: "custom-partial", Group: gatewaycapture.ProjectCompletionPriceGroup(group),
			Tokens: pricing.UsageTokens{InputTokens: count}, RateMultiplier: 1, Resolver: r})
		if count <= 100 {
			require.ErrorIs(t, err, pricing.ErrModelPricingUnavailable)
		} else {
			require.NoError(t, err)
			require.Equal(t, 0.45, cost.ActualCost)
		}
	}
	market := newPricingMarketplaceFixture(nil, nil, r, rCalculator, nil, nil, nil)
	display := market.PublicModelPricing(context.Background(), group, "custom-partial")
	require.Len(t, display.ContextIntervals, 1)
	require.Equal(t, 100, display.ContextIntervals[0].MinTokens)
}
