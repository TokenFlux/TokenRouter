//go:build unit

package service

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
)

// 相同价卡放在分组或渠道，图片按张、视频按秒和按次模式必须得到相同结果。
func TestMediaPricingCardsHaveSameGroupAndChannelSemantics(t *testing.T) {
	for _, media := range []string{"image", "video"} {
		for _, perRequest := range []bool{false, true} {
			for _, zero := range []bool{false, true} {
				model, platform, mode, tier := "gpt-image-2", PlatformOpenAI, BillingModeImage, "4K"
				result := &OpenAIForwardResult{Model: model, ImageCount: 3, ImageSize: tier}
				units := 3.0
				if media == "video" {
					model, platform, mode, tier = "grok-imagine-video", PlatformGrok, BillingModeVideo, "720p"
					result = &OpenAIForwardResult{Model: model, VideoCount: 2, VideoDurationSeconds: 7, VideoResolution: tier}
					units = 14
				}
				if perRequest {
					mode = BillingModePerRequest
					if media == "video" {
						units = 2
					}
				}
				price := 0.2
				if zero {
					price = 0
				}
				for _, scope := range []string{"group", "channel"} {
					t.Run(media+"/"+string(mode)+"/"+scope+"/"+map[bool]string{true: "free", false: "paid"}[zero], func(t *testing.T) {
						card := ChannelModelPricing{Platform: platform, Models: []string{model}, BillingMode: mode, PerRequestPrice: testPtrFloat64(9), Intervals: []PricingInterval{{TierLabel: tier, PerRequestPrice: &price}}}
						group := &Group{ID: 100, Platform: platform, RateMultiplier: 1.5}
						cards := []ChannelModelPricing{card}
						if scope == "group" {
							group.ModelPricing = cards
							cards = nil
						}
						billing := NewBillingService(nil, nil)
						resolver := newResolverWithBillingService(t, billing, cards)
						svc := &OpenAIGatewayService{billingService: billing, resolver: resolver}
						key := &APIKey{GroupID: &group.ID, Group: group}
						cost, err := svc.calculateOpenAIRecordUsageCost(context.Background(), result, key, []string{model}, 6, 1.5, 1.5, 1.5, UsageTokens{}, "priority")
						require.NoError(t, err)
						require.InDelta(t, price*units, cost.TotalCost, 1e-12)
						require.InDelta(t, price*units*1.5, cost.ActualCost, 1e-12)
					})
				}
			}
		}
	}
}

// 新异步任务读取模型价卡和尺寸；token 单价不可冒充每张费用。
func TestAsyncImageUnitPricingUsesCardsAndPerImageFallback(t *testing.T) {
	ctx := context.Background()
	model := "gemini-3.1-flash-image"
	billing := NewBillingService(nil, newPricingServiceFixture(pricingServiceFixture{pricingData: map[string]*LiteLLMModelPricing{model: {OutputCostPerToken: 0.000001, OutputCostPerImageToken: 0.000002, OutputCostPerImage: 0.2}}}))
	channel := ChannelModelPricing{Platform: PlatformGemini, Models: []string{model}, BillingMode: BillingModeImage, PerRequestPrice: testPtrFloat64(0.4), Intervals: []PricingInterval{{TierLabel: "512", PerRequestPrice: testPtrFloat64(0)}}}
	resolver := newResolverWithBillingService(t, billing, []ChannelModelPricing{channel})
	group := &Group{ID: 100, Platform: PlatformGemini}
	batch := &BatchImageModelPricingResolver{Resolver: resolver}
	creative := &CreativePublicService{Pricing: billing, PricingResolver: resolver}
	for _, tc := range []struct {
		size string
		want float64
	}{{"512", 0}, {"1K", 0.4}, {"4K", 0.4}} {
		price, err := batch.BatchImageUnitPrice(ctx, BatchImagePriceInput{Model: model, GroupID: &group.ID, Group: group, ImageSize: tc.size})
		require.NoError(t, err)
		require.InDelta(t, tc.want, price, 1e-12)
		unit, ok := creative.creativeResolvedImageUnitPrice(ctx, group, model, tc.size)
		require.True(t, ok)
		require.Equal(t, price, unit)
	}
	group.ModelPricing = []ChannelModelPricing{{Models: []string{model}, BillingMode: BillingModeImage, PerRequestPrice: testPtrFloat64(0.6)}}
	price, err := resolver.ResolveImageUnitPrice(ctx, PricingInput{Model: model, GroupID: &group.ID, Group: group}, "1K")
	require.NoError(t, err)
	require.InDelta(t, 0.6, price, 1e-12)
	group.ModelPricing = []ChannelModelPricing{{Models: []string{model}, BillingMode: BillingModeImage, Intervals: []PricingInterval{{TierLabel: "512", PerRequestPrice: testPtrFloat64(0)}}}}
	price, err = resolver.ResolveImageUnitPrice(ctx, PricingInput{Model: model, GroupID: &group.ID, Group: group}, "2K")
	require.NoError(t, err)
	require.InDelta(t, 0.3, price, 1e-12)
	price, err = resolver.ResolveImageUnitPrice(ctx, PricingInput{Model: model, GroupID: &group.ID, Group: group}, "512")
	require.NoError(t, err)
	require.Zero(t, price)

	group.ModelPricing = []ChannelModelPricing{{Models: []string{model}, BillingMode: BillingModeToken, ImageOutputPrice: testPtrFloat64(0.000009)}}
	price, err = resolver.ResolveImageUnitPrice(ctx, PricingInput{Model: model, GroupID: &group.ID, Group: group}, "2K")
	require.NoError(t, err)
	require.InDelta(t, 0.3, price, 1e-12)
}
