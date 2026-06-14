//go:build unit

package service

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestQoderBillingModelMapsPublicAliasesToRouteKeys(t *testing.T) {
	require.Equal(t, "qoder:ultimate", qoderBillingModel("claude-opus-4-6", ""))
	require.Equal(t, "qoder:kmodel", qoderBillingModel("kimi-k2.6", ""))
	require.Equal(t, "qoder:kmodel", qoderBillingModel("kimi-k2.7-code", ""))
	require.Equal(t, "qoder:dmodel", qoderBillingModel("", "dmodel"))
	require.Empty(t, qoderBillingModel("unknown-model", ""))
}

func TestQoderPricingUsesCreditMultipliers(t *testing.T) {
	svc := newTestBillingService()

	tests := []struct {
		model      string
		multiplier float64
	}{
		{"qoder:auto", 1.0},
		{"qoder:ultimate", 1.6},
		{"qoder:performance", 1.1},
		{"qoder:efficient", 0.3},
		{"qoder:lite", 0},
		{"qoder:qmodel_latest", 0.5},
		{"qoder:qmodel", 0.1},
		{"qoder:q35model", 0.1},
		{"qoder:dmodel", 0.5},
		{"qoder:dfmodel", 0.1},
		{"qoder:gmodel", 0.6},
		{"qoder:gm51model", 0.6},
		{"qoder:kmodel", 0.3},
		{"qoder:mmodel", 0.4},
	}

	for _, tt := range tests {
		t.Run(tt.model, func(t *testing.T) {
			pricing, err := svc.GetModelPricing(tt.model)
			require.NoError(t, err)
			require.NotNil(t, pricing)
			require.InDelta(t, tt.multiplier*1e-6, pricing.InputPricePerToken, 1e-12)
			require.InDelta(t, tt.multiplier*1e-6, pricing.OutputPricePerToken, 1e-12)
			require.InDelta(t, tt.multiplier*1e-6, pricing.CacheCreationPricePerToken, 1e-12)
			require.InDelta(t, tt.multiplier*1e-6, pricing.CacheReadPricePerToken, 1e-12)
		})
	}
}

func TestQoderPricingRequiresInternalBillingPrefix(t *testing.T) {
	svc := newTestBillingService()

	pricing, err := svc.GetModelPricing("auto")

	require.Error(t, err)
	require.Nil(t, pricing)
}

func TestQoderCalculateCostUsesRouteMultiplier(t *testing.T) {
	svc := newTestBillingService()

	cost, err := svc.CalculateCost("qoder:ultimate", UsageTokens{
		InputTokens:         1000,
		OutputTokens:        500,
		CacheCreationTokens: 100,
		CacheReadTokens:     200,
	}, 2.0)

	require.NoError(t, err)
	expectedTotal := (1000 + 500 + 100 + 200) * 1.6e-6
	require.InDelta(t, expectedTotal, cost.TotalCost, 1e-12)
	require.InDelta(t, expectedTotal*2.0, cost.ActualCost, 1e-12)
}

func TestQoderLiteDisplayPricingIsPricedAtZero(t *testing.T) {
	svc := newTestBillingService()

	pricing := svc.GetDisplayPricing("qoder:lite", 1.0, nil)

	require.Equal(t, "token", pricing.PricingMode)
	require.Equal(t, "priced", pricing.PriceStatus)
	require.Zero(t, pricing.InputPricePerToken)
	require.Zero(t, pricing.OutputPricePerToken)
	require.Zero(t, pricing.CacheWritePricePerToken)
	require.Zero(t, pricing.CacheReadPricePerToken)
}
