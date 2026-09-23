//go:build unit

package service

import (
	"context"
	"testing"
	time "time"

	"github.com/TokenFlux/TokenRouter/internal/apikey"
	"github.com/TokenFlux/TokenRouter/internal/batchimage"
	completion "github.com/TokenFlux/TokenRouter/internal/gateway/completion"
	gatewaycapture "github.com/TokenFlux/TokenRouter/internal/gateway/provider"

	billingtestkit "github.com/TokenFlux/TokenRouter/internal/billing/testkit"
	"github.com/TokenFlux/TokenRouter/internal/creative"

	billingcore "github.com/TokenFlux/TokenRouter/internal/billing"
	"github.com/TokenFlux/TokenRouter/internal/billing/pricing"

	forwardcore "github.com/TokenFlux/TokenRouter/internal/gateway/forward"
	"github.com/TokenFlux/TokenRouter/internal/routing"
	"github.com/TokenFlux/TokenRouter/internal/routing/capability"
	"github.com/stretchr/testify/require"
)

// 相同价卡放在分组或渠道，图片按张、视频按秒和按次模式必须得到相同结果。
func TestMediaPricingCardsHaveSameGroupAndChannelSemantics(t *testing.T) {
	for _, media := range []string{"image", "video"} {
		for _, perRequest := range []bool{false, true} {
			for _, zero := range []bool{false, true} {
				model, platform, mode, tier := "gpt-image-2", capability.PlatformOpenAI, routing.BillingModeImage, "4K"
				result := &forwardcore.OpenAIResult{Model: model, ImageCount: 3, ImageSize: tier}
				units := 3.0
				if media == "video" {
					model, platform, mode, tier = "grok-imagine-video", capability.PlatformGrok, routing.BillingModeVideo, "720p"
					result = &forwardcore.OpenAIResult{Model: model, VideoCount: 2, VideoDurationSeconds: 7, VideoResolution: tier}
					units = 14
				}
				if perRequest {
					mode = routing.BillingModePerRequest
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
						card := routing.ChannelModelPricing{Platform: platform, Models: []string{model}, BillingMode: mode, PerRequestPrice: testPtrFloat64(9), Intervals: []routing.PricingInterval{{TierLabel: tier, PerRequestPrice: &price}}}
						group := &routing.Group{ID: 100, Platform: platform, RateMultiplier: 1.5}
						cards := []routing.ChannelModelPricing{card}
						if scope == "group" {
							group.ModelPricing = cards
							cards = nil
						}
						billing := NewBillingService(nil, nil)
						resolver := billingtestkit.ResolverWithCards(t, billing, cards)
						svc := completion.NewRecorder(completion.Dependencies{Calculator: billing, Prices: resolver}, completion.RecorderOptions{DefaultMultiplier: 1})

						key := &apikey.APIKey{GroupID: &group.ID, Group: group}
						cost, err := svc.CalculateOpenAIRecordUsageCostAt(context.Background(), gatewaycapture.ProjectOpenAICompletionResult(result, nil), gatewaycapture.ProjectCompletionKey(key), []string{model}, 6, 1.5, 1.5, 1.5, pricing.UsageTokens{}, "priority", time.Time{})
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
	billing := NewBillingService(nil, newPricingServiceFixture(pricingServiceFixture{pricingData: map[string]*pricing.LiteLLMModelPricing{model: {OutputCostPerToken: 0.000001, OutputCostPerImageToken: 0.000002, OutputCostPerImage: 0.2}}}))
	channel := routing.ChannelModelPricing{Platform: capability.PlatformGemini, Models: []string{model}, BillingMode: routing.BillingModeImage, PerRequestPrice: testPtrFloat64(0.4), Intervals: []routing.PricingInterval{{TierLabel: "512", PerRequestPrice: testPtrFloat64(0)}}}
	resolver := billingtestkit.ResolverWithCards(t, billing, []routing.ChannelModelPricing{channel})
	group := &routing.Group{ID: 100, Platform: capability.PlatformGemini}
	batch := &batchimage.Pricing{Resolver: resolver}
	creativeService := &creative.Public{ImageUnitPrice: creativePriceFixture(billing, resolver)}
	for _, tc := range []struct {
		size string
		want float64
	}{{"512", 0}, {"1K", 0.4}, {"4K", 0.4}} {
		price, err := batch.BatchImageUnitPrice(ctx, batchimage.BatchImagePriceInput{Model: model, GroupID: &group.ID, Group: &batchimage.GroupView{Price: *gatewaycapture.ProjectCompletionPriceGroup(group)}, ImageSize: tc.size})
		require.NoError(t, err)
		require.InDelta(t, tc.want, price, 1e-12)
		unit, ok := creativeService.ImageUnitPrice(ctx, creativeGroupProjection(group), model, tc.size)
		require.True(t, ok)
		require.Equal(t, price, unit)
	}
	group.ModelPricing = []routing.ChannelModelPricing{{Models: []string{model}, BillingMode: routing.BillingModeImage, PerRequestPrice: testPtrFloat64(0.6)}}
	price, err := resolver.ResolveImageUnitPrice(ctx, billingcore.PricingInput{Model: model, GroupID: &group.ID, Group: gatewaycapture.ProjectCompletionPriceGroup(group)}, "1K")
	require.NoError(t, err)
	require.InDelta(t, 0.6, price, 1e-12)
	group.ModelPricing = []routing.ChannelModelPricing{{Models: []string{model}, BillingMode: routing.BillingModeImage, Intervals: []routing.PricingInterval{{TierLabel: "512", PerRequestPrice: testPtrFloat64(0)}}}}
	price, err = resolver.ResolveImageUnitPrice(ctx, billingcore.PricingInput{Model: model, GroupID: &group.ID, Group: gatewaycapture.ProjectCompletionPriceGroup(group)}, "2K")
	require.NoError(t, err)
	require.InDelta(t, 0.3, price, 1e-12)
	price, err = resolver.ResolveImageUnitPrice(ctx, billingcore.PricingInput{Model: model, GroupID: &group.ID, Group: gatewaycapture.ProjectCompletionPriceGroup(group)}, "512")
	require.NoError(t, err)
	require.Zero(t, price)

	group.ModelPricing = []routing.ChannelModelPricing{{Models: []string{model}, BillingMode: routing.BillingModeToken, ImageOutputPrice: testPtrFloat64(0.000009)}}
	price, err = resolver.ResolveImageUnitPrice(ctx, billingcore.PricingInput{Model: model, GroupID: &group.ID, Group: gatewaycapture.ProjectCompletionPriceGroup(group)}, "2K")
	require.NoError(t, err)
	require.InDelta(t, 0.3, price, 1e-12)
}
