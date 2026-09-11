//go:build unit

package service

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// 图片和视频的 token 价卡必须保留全部倍率，不能因为继承内置来源而改走按次计费。
func TestOpenAIMediaPricingUsesModifierOnlyCards(t *testing.T) {
	for _, media := range []string{"image", "video"} {
		model, platform := "gpt-image-1", PlatformOpenAI
		if media == "video" {
			model, platform = "grok-imagine-video", PlatformGrok
		}
		for _, scope := range []string{"group", "channel"} {
			for _, kind := range []string{"fast", "flex", "max", "time", "combined"} {
				t.Run(media+"/"+scope+"/"+kind, func(t *testing.T) {
					card := ChannelModelPricing{Platform: platform, Models: []string{model}, BillingMode: BillingModeToken}
					factor, tier, effort := 1.0, "", ""
					if kind == "fast" || kind == "combined" {
						card.FastMultiplier, tier = testPtrFloat64(2), "priority"
						factor *= 2
					}
					if kind == "flex" {
						card.FlexMultiplier, tier = testPtrFloat64(0.4), "flex"
						factor *= 0.4
					}
					if kind == "max" || kind == "combined" {
						card.MaxReasoningEffortMultiplier, effort = testPtrFloat64(3), "max"
						factor *= 3
					}
					if kind == "time" || kind == "combined" {
						card.TimePricing = &ChannelTimePricing{Timezone: "UTC", Periods: []ChannelTimePricingPeriod{{StartTime: "00:00", EndTime: "12:00", Multiplier: 2}}}
						factor *= 2
					}
					require.NoError(t, validatePricingEntries([]ChannelModelPricing{card}))
					billing := NewBillingService(nil, newPricingServiceFixture(pricingServiceFixture{pricingData: map[string]*LiteLLMModelPricing{
						model: {Mode: media, InputCostPerToken: 0.001, OutputCostPerToken: 0.002, OutputCostPerImageToken: 0.004},
					}}))
					group := &Group{ID: 100, Platform: platform}
					var channelCards []ChannelModelPricing
					if scope == "group" {
						group.ModelPricing = []ChannelModelPricing{card}
					} else {
						channelCards = []ChannelModelPricing{card}
					}
					resolver := newResolverWithBillingService(t, billing, channelCards)
					svc := &OpenAIGatewayService{billingService: billing, resolver: resolver}
					key := &APIKey{GroupID: &group.ID, Group: group}
					resolved := svc.resolveOpenAIChannelPricing(context.Background(), model, key)
					require.NotNil(t, resolved)
					require.Equal(t, PricingSourceLiteLLM, resolved.Source)
					result := &OpenAIForwardResult{Model: model, ReasoningEffort: &effort, ImageCount: 1}
					if media == "video" {
						result.ImageCount, result.VideoCount = 0, 1
						result.VideoDurationSeconds = 8
					}
					cost, err := svc.calculateOpenAIRecordUsageCostAt(context.Background(), result, key, []string{model}, 1.5, 0.7, 0.8, 1,
						UsageTokens{InputTokens: 100, OutputTokens: 50, ImageOutputTokens: 50}, tier, time.Date(2026, 9, 9, 9, 0, 0, 0, time.UTC))
					require.NoError(t, err)
					require.Equal(t, string(BillingModeToken), cost.BillingMode)
					require.InDelta(t, 0.3*factor, cost.TotalCost, 1e-12)
					require.InDelta(t, 0.3*factor*1.5, cost.ActualCost, 1e-12)
				})
			}
		}
	}
}

// 分组纯倍率继承到按图或按秒价卡时保持原模式，不叠加 token 专属倍率。
func TestOpenAIMediaModifiersPreserveInheritedRequestBilling(t *testing.T) {
	for _, mode := range []BillingMode{BillingModeImage, BillingModeVideo} {
		t.Run(string(mode), func(t *testing.T) {
			model, platform := "gpt-image-1", PlatformOpenAI
			result := &OpenAIForwardResult{Model: model, ImageCount: 2}
			wantTotal, rate := 0.5, 0.7
			if mode == BillingModeVideo {
				model, platform = "grok-imagine-video", PlatformGrok
				result = &OpenAIForwardResult{Model: model, VideoCount: 2, VideoDurationSeconds: 8}
				wantTotal, rate = 4, 0.8
			}
			billing := NewBillingService(nil, nil)
			resolver := newResolverWithBillingService(t, billing, []ChannelModelPricing{{Platform: platform, Models: []string{model}, BillingMode: mode, PerRequestPrice: testPtrFloat64(0.25)}})
			group := &Group{ID: 100, Platform: platform, ModelPricing: []ChannelModelPricing{{Models: []string{model}, FastMultiplier: testPtrFloat64(3),
				TimePricing: &ChannelTimePricing{Timezone: "UTC", Periods: []ChannelTimePricingPeriod{{StartTime: "00:00", EndTime: "12:00", Multiplier: 2}}}}}}
			svc := &OpenAIGatewayService{billingService: billing, resolver: resolver}
			cost, err := svc.calculateOpenAIRecordUsageCostAt(context.Background(), result, &APIKey{Group: group}, []string{model}, 1.5, 0.7, 0.8, 1,
				UsageTokens{InputTokens: 100}, "priority", time.Date(2026, 9, 9, 9, 0, 0, 0, time.UTC))
			require.NoError(t, err)
			require.Equal(t, string(mode), cost.BillingMode)
			require.InDelta(t, wantTotal, cost.TotalCost, 1e-12)
			require.InDelta(t, wantTotal*rate, cost.ActualCost, 1e-12)
		})
	}
}

// 放宽媒体价卡识别不能让国产供应商通过倍率条目启用 Claude 内置回退价。
func TestCNProviderPricingModifiersDoNotCountAsExplicitPrices(t *testing.T) {
	for _, platform := range []string{PlatformKimi, PlatformZhipu, PlatformDeepseek} {
		for _, scope := range []string{"group", "channel"} {
			t.Run(platform+"/"+scope, func(t *testing.T) {
				model := "claude-sonnet-4"
				card := ChannelModelPricing{Platform: platform, Models: []string{model}, FastMultiplier: testPtrFloat64(2)}
				group := &Group{ID: 100, Platform: platform}
				var channelCards []ChannelModelPricing
				if scope == "group" {
					group.ModelPricing = []ChannelModelPricing{card}
				} else {
					channelCards = []ChannelModelPricing{card}
				}
				resolver := newResolverWithBillingService(t, NewBillingService(nil, nil), channelCards)
				svc := &OpenAIGatewayService{resolver: resolver}
				key := &APIKey{Group: group}
				require.NotNil(t, svc.resolveOpenAIChannelPricing(context.Background(), model, key))
				require.Empty(t, svc.filterCNProviderBillingModelCandidates(context.Background(), &Account{Platform: platform}, key, []string{model}))
				// 显式零价仍是管理员的定价合同，应允许候选进入结算。
				group.ModelPricing = []ChannelModelPricing{{Models: []string{model}, InputPrice: testPtrFloat64(0)}}
				require.Equal(t, []string{model}, svc.filterCNProviderBillingModelCandidates(context.Background(), &Account{Platform: platform}, key, []string{model}))
			})
		}
	}
}
