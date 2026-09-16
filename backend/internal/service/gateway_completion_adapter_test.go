package service

import (
	"testing"

	"github.com/stretchr/testify/require"
)

// 完成快照只能复制输入，不能因只读策略判断替换请求仍在使用的分组。
func TestCompletionKeySnapshotPreservesSourceGroupAndExplicitZeroPrice(t *testing.T) {
	multiplier := 2.0
	group := &Group{ID: 100, ModelPricing: []ChannelModelPricing{{Models: []string{"claude-sonnet-4"}, FastMultiplier: &multiplier}}}
	key := &APIKey{Group: group, BillingMode: APIKeyBillingModeBalance, RateLimit5h: 1}

	first := completionKey(key)
	require.Same(t, group, key.Group)
	require.Equal(t, APIKeyBillingModeBalance, first.BillingMode)
	require.True(t, first.HasRateLimits)

	zero := 0.0
	group.ModelPricing = []ChannelModelPricing{{Models: []string{"claude-sonnet-4"}, InputPrice: &zero}}
	second := completionKey(key)
	require.Same(t, group, key.Group)
	require.NotNil(t, second.Group.Price.ModelPricing[0].InputPrice)
	require.Zero(t, *second.Group.Price.ModelPricing[0].InputPrice)
	// 已拍快照保留自己的价卡，后续快照读取更新后的原输入。
	require.Nil(t, first.Group.Price.ModelPricing[0].InputPrice)
	require.Equal(t, 2.0, *first.Group.Price.ModelPricing[0].FastMultiplier)
	zero = 3
	require.Zero(t, *second.Group.Price.ModelPricing[0].InputPrice)
}
