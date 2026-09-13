package service

import (
	"context"
	"testing"

	"github.com/TokenFlux/TokenRouter/internal/config"
	"github.com/TokenFlux/TokenRouter/internal/pkg/xai"

	"github.com/stretchr/testify/require"
)

// 新目录别名在有无内置价时均先采用渠道价，不能以目录回退跳过运营者的显式配置。
func TestResolveCatalogAliasesPreserveChannelPricing(t *testing.T) {
	previous := xai.RuntimeModelMappingOptions()
	t.Cleanup(func() { xai.SetRuntimeModelMappingOptions(previous) })
	xai.SetRuntimeModelMappingOptions(xai.ModelMappingOptions{DefaultText: "X-AI/GROK-4.6"})
	for _, tc := range []struct{ platform, base, alias string }{
		{PlatformGemini, "gemini-3.8-flash", "gemini-3.8-flash-tiered"},
		{PlatformGemini, "gemini-3.7-flash", "models/gemini-3.7-flash-medium"},
		{PlatformGrok, "grok-4.6", "grok-4.6-latest"},
		{PlatformGrok, "grok-4.6", "grok-latest"},
		{PlatformOpenAI, "gpt-5.6-luna", "gpt-5.6-luna-high"},
		{PlatformAnthropic, "claude-opus-4-6", "claude-opus-4.6"},
	} {
		t.Run(tc.alias, func(t *testing.T) {
			for _, hasCatalog := range []bool{true, false} {
				groupID := int64(998)
				channelPrice := 9e-6
				channel := Channel{ID: 998, Status: StatusActive, GroupIDs: []int64{groupID}, ModelPricing: []ChannelModelPricing{{
					Platform: tc.platform, Models: []string{tc.base}, BillingMode: BillingModeToken, InputPrice: &channelPrice,
				}}}
				channels := &ChannelService{}
				seedLegacyChannelFixture(channels, populateChannelCache([]Channel{channel}, map[int64]string{groupID: tc.platform}))
				var catalog *PricingService
				if hasCatalog {
					catalog = newPricingServiceFixture(pricingServiceFixture{pricingData: map[string]*LiteLLMModelPricing{
						tc.base: {Mode: "chat", InputCostPerToken: 1e-6, OutputCostPerToken: 2e-6},
					}})
				}
				resolver := NewModelPricingResolver(channels, NewBillingService(&config.Config{}, catalog))
				input := PricingInput{Model: tc.alias, GroupID: &groupID}
				resolved := resolver.Resolve(context.Background(), input)
				require.Equal(t, PricingSourceChannel, resolved.Source, "catalog=%v", hasCatalog)
				require.InDelta(t, channelPrice, resolved.BasePricing.InputPricePerToken, 1e-12)
				// Claude 分隔符写法在渠道层属于同一个定价键，不能重复配置独立价卡。
				if normalizeChannelPricingModelName(tc.base) == normalizeChannelPricingModelName(tc.alias) {
					continue
				}

				// 完整请求名的独立价卡仍然优先，显式零价也不能被基础名价格覆盖。
				zero := 0.0
				channel.ModelPricing = append(channel.ModelPricing, ChannelModelPricing{
					Platform: tc.platform, Models: []string{tc.alias}, BillingMode: BillingModeToken, InputPrice: &zero,
				})
				seedLegacyChannelFixture(channels, populateChannelCache([]Channel{channel}, map[int64]string{groupID: tc.platform}))
				resolved = resolver.Resolve(context.Background(), input)
				require.True(t, resolved.HasEffectiveChannelPricing())
				require.Zero(t, resolved.BasePricing.InputPricePerToken)
				base := resolver.Resolve(context.Background(), PricingInput{Model: tc.base, GroupID: &groupID})
				require.InDelta(t, channelPrice, base.BasePricing.InputPricePerToken, 1e-12)
			}
		})
	}
}

// 别名只在当前分组平台内查价，不能误命中其它平台或不相关模型。
func TestResolveCatalogAliasesKeepChannelPlatformBoundary(t *testing.T) {
	groupID := int64(999)
	price := 9e-6
	channels := &ChannelService{}
	seedLegacyChannelFixture(channels, populateChannelCache([]Channel{{
		ID: 999, Status: StatusActive, GroupIDs: []int64{groupID}, ModelPricing: []ChannelModelPricing{
			{Platform: PlatformOpenAI, Models: []string{"gemini-3.8-flash"}, BillingMode: BillingModeToken, InputPrice: &price},
			{Platform: PlatformGemini, Models: []string{"gemini-3.7-flash"}, BillingMode: BillingModeToken, InputPrice: &price},
		},
	}}, map[int64]string{groupID: PlatformGemini}))
	catalog := newPricingServiceFixture(pricingServiceFixture{pricingData: map[string]*LiteLLMModelPricing{
		"gemini-3.8-flash": {Mode: "chat", InputCostPerToken: 1e-6},
	}})
	resolver := NewModelPricingResolver(channels, NewBillingService(&config.Config{}, catalog))
	resolved := resolver.Resolve(context.Background(), PricingInput{Model: "gemini-3.8-flash-tiered", GroupID: &groupID})
	require.False(t, resolved.HasEffectiveChannelPricing())
	require.InDelta(t, 1e-6, resolved.BasePricing.InputPricePerToken, 1e-12)
}

// 两类价卡共用身份候选，空完整名条目不遮蔽基础价，完整名零价和通配价仍优先。
func TestGroupAndChannelCatalogAliasPrecedence(t *testing.T) {
	for _, tc := range []struct{ platform, base, alias string }{
		{PlatformOpenAI, "gpt-5.6-luna", "gpt-5.6-luna-high"},
		{PlatformGemini, "gemini-3.8-flash", "models/gemini-3.8-flash-tiered"},
		{PlatformGrok, "grok-4.6", "grok-4.6-latest"},
		{PlatformAnthropic, "claude-opus-4-6", "claude-opus-4.6"},
	} {
		t.Run(tc.alias, func(t *testing.T) {
			price, zero := 9e-6, 0.0
			card := ChannelModelPricing{Platform: tc.platform, Models: []string{tc.base}, InputPrice: &price}
			for _, scope := range []string{PricingSourceGroup, PricingSourceChannel} {
				group := &Group{ID: 990, Platform: tc.platform, LongContextPricingEnabled: true}
				channels := &ChannelService{}
				resolver := NewModelPricingResolver(channels, NewBillingService(&config.Config{}, nil))
				resolve := func(cards []ChannelModelPricing) *ResolvedPricing {
					group.ModelPricing = nil
					channel := Channel{ID: 990, Status: StatusActive, GroupIDs: []int64{group.ID}}
					if scope == PricingSourceGroup {
						group.ModelPricing = cards
					} else {
						channel.ModelPricing = cards
					}
					seedLegacyChannelFixture(channels, populateChannelCache([]Channel{channel}, map[int64]string{group.ID: tc.platform}))
					return resolver.Resolve(context.Background(), PricingInput{Model: tc.alias, GroupID: &group.ID, Group: group})
				}
				base := resolve([]ChannelModelPricing{card})
				require.Equal(t, scope, base.Source)
				require.Equal(t, price, base.BasePricing.InputPricePerToken)
				if normalizeChannelPricingModelName(tc.base) == normalizeChannelPricingModelName(tc.alias) {
					continue
				}
				exact := ChannelModelPricing{Platform: tc.platform, Models: []string{tc.alias}}
				require.Equal(t, price, resolve([]ChannelModelPricing{exact, card}).BasePricing.InputPricePerToken)
				exact.InputPrice = &zero
				require.Zero(t, resolve([]ChannelModelPricing{card, exact}).BasePricing.InputPricePerToken)
				wildcard := ChannelModelPricing{Platform: tc.platform, Models: []string{tc.alias + "*"}, InputPrice: &zero}
				require.Zero(t, resolve([]ChannelModelPricing{card, wildcard}).BasePricing.InputPricePerToken)
			}
		})
	}
}
