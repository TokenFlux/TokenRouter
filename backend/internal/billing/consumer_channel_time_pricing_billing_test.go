//go:build unit

package billing_test

import (
	"context"
	"testing"
	"time"

	"github.com/TokenFlux/TokenRouter/internal/billing"
	billingpricing "github.com/TokenFlux/TokenRouter/internal/billing/pricing"
	billingtestkit "github.com/TokenFlux/TokenRouter/internal/billing/testkit"
	"github.com/TokenFlux/TokenRouter/internal/gateway/provider/modelidentity"
	"github.com/TokenFlux/TokenRouter/internal/routing"
	"github.com/stretchr/testify/require"
)

func TestCalculateCostUnifiedAppliesChannelTimeMultiplierToTokenBuckets(t *testing.T) {
	groupID := int64(71)
	pricing := &routing.ChannelModelPricing{
		BillingMode: routing.BillingModeToken,
		InputPrice:  floatPtr(5e-6),
		TimePricing: &routing.ChannelTimePricing{
			Timezone: "Asia/Shanghai",
			Periods:  []routing.ChannelTimePricingPeriod{{StartTime: "09:00", EndTime: "12:00", Multiplier: 2}},
		},
	}
	resolved := &billingpricing.ResolvedPricing{
		Mode:           routing.BillingModeToken,
		Source:         billingpricing.PricingSourceChannel,
		BasePricing:    &billingpricing.ModelPricing{InputPricePerToken: 5e-6, OutputPricePerToken: 15e-6},
		ChannelPricing: pricing,
	}
	service := billingtestkit.Calculator(0, nil, nil)
	resolver := billing.NewPriceResolver(nil, service, modelidentity.Identity, nil)

	cost, err := service.CalculateCostUnified(billing.CostInput{
		Ctx:            context.Background(),
		Model:          "claude-sonnet-4",
		GroupID:        &groupID,
		Tokens:         billingpricing.UsageTokens{InputTokens: 100, OutputTokens: 10},
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
	pricing := &routing.ChannelModelPricing{
		BillingMode:     routing.BillingModePerRequest,
		PerRequestPrice: floatPtr(0.05),
		TimePricing: &routing.ChannelTimePricing{
			Timezone: "Asia/Shanghai",
			Periods:  []routing.ChannelTimePricingPeriod{{StartTime: "09:00", EndTime: "12:00", Multiplier: 2}},
		},
	}
	resolved := &billingpricing.ResolvedPricing{Mode: routing.BillingModePerRequest, Source: billingpricing.PricingSourceChannel, ChannelPricing: pricing, DefaultPerRequestPrice: 0.05}
	service := billingtestkit.Calculator(0, nil, nil)
	resolver := billing.NewPriceResolver(nil, service, modelidentity.Identity, nil)

	cost, err := service.CalculateCostUnified(billing.CostInput{
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
