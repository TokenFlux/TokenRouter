//go:build unit

package service

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestCalculateCostUnifiedAppliesChannelTimeMultiplierToTokenBuckets(t *testing.T) {
	groupID := int64(71)
	pricing := &ChannelModelPricing{
		BillingMode: BillingModeToken,
		InputPrice:  testPtrFloat64(5e-6),
		TimePricing: &ChannelTimePricing{
			Timezone: "Asia/Shanghai",
			Periods:  []ChannelTimePricingPeriod{{StartTime: "09:00", EndTime: "12:00", Multiplier: 2}},
		},
	}
	resolved := &ResolvedPricing{
		Mode:           BillingModeToken,
		Source:         PricingSourceChannel,
		BasePricing:    &ModelPricing{InputPricePerToken: 5e-6, OutputPricePerToken: 15e-6},
		ChannelPricing: pricing,
	}
	service := newTestBillingService()
	resolver := NewModelPricingResolver(nil, service)

	cost, err := service.CalculateCostUnified(CostInput{
		Ctx:            context.Background(),
		Model:          "claude-sonnet-4",
		GroupID:        &groupID,
		Tokens:         UsageTokens{InputTokens: 100, OutputTokens: 10},
		RateMultiplier: 3,
		PricingAt:      time.Date(2026, 6, 29, 1, 0, 0, 0, time.UTC),
		Resolver:       resolver,
		Resolved:       resolved,
	})
	require.NoError(t, err)
	want := (100*5e-6 + 10*15e-6) * 2
	require.InDelta(t, want, cost.TotalCost, 1e-12)
	require.InDelta(t, want*3, cost.ActualCost, 1e-12)
}

func TestCalculateCostUnifiedDoesNotApplyChannelTimeMultiplierToPerRequest(t *testing.T) {
	groupID := int64(72)
	pricing := &ChannelModelPricing{
		BillingMode:     BillingModePerRequest,
		PerRequestPrice: testPtrFloat64(0.05),
		TimePricing: &ChannelTimePricing{
			Timezone: "Asia/Shanghai",
			Periods:  []ChannelTimePricingPeriod{{StartTime: "09:00", EndTime: "12:00", Multiplier: 2}},
		},
	}
	resolved := &ResolvedPricing{Mode: BillingModePerRequest, Source: PricingSourceChannel, ChannelPricing: pricing, DefaultPerRequestPrice: 0.05}
	service := newTestBillingService()
	resolver := NewModelPricingResolver(nil, service)

	cost, err := service.CalculateCostUnified(CostInput{
		Ctx:            context.Background(),
		Model:          "image-model",
		GroupID:        &groupID,
		RequestCount:   3,
		RateMultiplier: 2,
		PricingAt:      time.Date(2026, 6, 29, 1, 0, 0, 0, time.UTC),
		Resolver:       resolver,
		Resolved:       resolved,
	})
	require.NoError(t, err)
	require.InDelta(t, 0.15, cost.TotalCost, 1e-12)
	require.InDelta(t, 0.30, cost.ActualCost, 1e-12)
}
