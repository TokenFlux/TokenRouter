package pricing

import (
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// 纯倍率继承不得修改共享基础价，也不得把缺价变为免费。
func TestPriceCardsPreserveInputsAndPricePresence(t *testing.T) {
	base := &ModelPricing{InputPricePerToken: 0.000001, SupportsServiceTier: true}
	pricingConfigMultiplier, groupMultiplier := 2.0, 1.5
	configPricing := &ModelPricingEntry{BillingMode: BillingModeToken, FastMultiplier: &pricingConfigMultiplier}
	group := &ModelPricingEntry{BillingMode: BillingModeToken, FastMultiplier: &groupMultiplier}
	resolved := ResolvePriceCards(group, configPricing, base, PricingSourceLiteLLM, true)
	cost, err := CalculateCost(resolved, CostInput{Model: "custom", Tokens: UsageTokens{InputTokens: 100}, RateMultiplier: 1, ServiceTier: "priority"})
	require.NoError(t, err)
	require.InDelta(t, 0.00015, cost.ActualCost, 1e-12)
	require.Nil(t, base.FastMultiplier)
	require.Equal(t, 2.0, *configPricing.FastMultiplier)
	require.Equal(t, 1.5, *group.FastMultiplier)
	require.Equal(t, PricingSourceLiteLLM, resolved.Source)

	missing := ResolvePriceCards(group, nil, nil, PricingSourceUnpriced, true)
	_, err = CalculateCost(missing, CostInput{Model: "missing", Tokens: UsageTokens{InputTokens: 100}, RateMultiplier: 1})
	require.True(t, errors.Is(err, ErrModelPricingUnavailable))
	zero := 0.0
	free := ResolvePriceCards(&ModelPricingEntry{InputPrice: &zero}, nil, nil, PricingSourceUnpriced, true)
	cost, err = CalculateCost(free, CostInput{Model: "free", Tokens: UsageTokens{InputTokens: 100}, RateMultiplier: 1})
	require.NoError(t, err)
	require.Zero(t, cost.ActualCost)
	require.Equal(t, PricingSourceGroup, free.Source)
}

// 模型峰谷可以使用单独的缺省时刻，不能让零 PricingAt 意外启用价卡分时。
func TestZeroPricingTimeDoesNotEnablePricingTimeMultiplier(t *testing.T) {
	at := time.Date(2026, 6, 29, 2, 0, 0, 0, time.UTC)
	card := &ModelPricingEntry{TimePricing: &TimePricingConfig{Timezone: "UTC", Periods: []TimePricingPeriod{{StartTime: "01:00", EndTime: "04:00", Multiplier: 2}}}}
	resolved := ResolvePriceCards(card, nil, &ModelPricing{InputPricePerToken: 0.000001}, PricingSourceLiteLLM, true)
	input := CostInput{Model: "custom", Tokens: UsageTokens{InputTokens: 100}, RateMultiplier: 1, ModelPricingAt: at, TimePricingLocation: time.UTC}
	cost, err := CalculateCost(resolved, input)
	require.NoError(t, err)
	require.InDelta(t, 0.0001, cost.ActualCost, 1e-12)
	input.PricingAt = at
	cost, err = CalculateCost(resolved, input)
	require.NoError(t, err)
	require.InDelta(t, 0.0002, cost.ActualCost, 1e-12)
}

// 秋季重复的一点钟都按本地窗口计价，窗口结束仍是右开边界。
func TestExplicitTimeLocationPreservesDSTRepeatedHour(t *testing.T) {
	location, err := time.LoadLocation("America/New_York")
	require.NoError(t, err)
	config := &TimePricingConfig{Timezone: "America/New_York", Periods: []TimePricingPeriod{{StartTime: "01:00", EndTime: "02:00", Multiplier: 2}}}
	for _, hour := range []int{5, 6} {
		require.Equal(t, 2.0, config.MultiplierAt(time.Date(2026, 11, 1, hour, 30, 0, 0, time.UTC), location))
	}
	require.Equal(t, 1.0, config.MultiplierAt(time.Date(2026, 11, 1, 7, 0, 0, 0, time.UTC), location))
}
