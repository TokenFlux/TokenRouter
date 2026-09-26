package provider

import (
	"testing"

	"github.com/TokenFlux/TokenRouter/internal/apikey"
	"github.com/TokenFlux/TokenRouter/internal/routing"
	"github.com/stretchr/testify/require"
)

// 完成快照只能复制输入，不能因只读策略判断替换请求仍在使用的分组。
func TestCompletionKeySnapshotPreservesSourceGroupAndExplicitZeroPrice(t *testing.T) {
	multiplier := 2.0
	group := &routing.Group{ID: 100, ModelPricing: []routing.ModelPricingEntry{{Models: []string{"claude-sonnet-4"}, FastMultiplier: &multiplier}}}
	key := &apikey.APIKey{Group: group, BillingMode: apikey.APIKeyBillingModeBalance, RateLimit5h: 1}

	first := ProjectCompletionKey(key)
	require.Same(t, group, key.Group)
	require.Equal(t, apikey.APIKeyBillingModeBalance, first.BillingMode)
	require.True(t, first.HasRateLimits)

	zero := 0.0
	group.ModelPricing = []routing.ModelPricingEntry{{Models: []string{"claude-sonnet-4"}, InputPrice: &zero}}
	second := ProjectCompletionKey(key)
	require.Same(t, group, key.Group)
	require.NotNil(t, second.Group.Price.ModelPricing[0].InputPrice)
	require.Zero(t, *second.Group.Price.ModelPricing[0].InputPrice)
	// 已拍快照保留自己的价卡，后续快照读取更新后的原输入。
	require.Nil(t, first.Group.Price.ModelPricing[0].InputPrice)
	require.Equal(t, 2.0, *first.Group.Price.ModelPricing[0].FastMultiplier)
	zero = 3
	require.Zero(t, *second.Group.Price.ModelPricing[0].InputPrice)
}
