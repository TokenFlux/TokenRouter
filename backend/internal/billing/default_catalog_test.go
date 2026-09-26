package billing

import (
	"testing"

	"github.com/TokenFlux/TokenRouter/internal/billing/pricing"
	"github.com/stretchr/testify/require"
)

type defaultCatalogStub struct {
	entries map[string]*pricing.LiteLLMModelPricing
}

func (s defaultCatalogStub) GetModelPricing(model string) *pricing.LiteLLMModelPricing {
	return s.entries[model]
}
func (s defaultCatalogStub) GetStatus() map[string]any { return nil }
func (s defaultCatalogStub) ForceUpdate() error {
	panic("default price queries must not update the catalog")
}

func TestDefaultPriceUsesCatalogAndPreservesZero(t *testing.T) {
	catalog := defaultCatalogStub{entries: map[string]*pricing.LiteLLMModelPricing{
		"custom": {InputCostPerToken: 0, OutputCostPerToken: 0.000004, SupportsServiceTier: true, InputCostPerTokenPriority: 0, OutputCostPerTokenPriority: 0.000008, LongContextInputTokenThreshold: 100000, LongContextInputCostMultiplier: 2, LongContextOutputCostMultiplier: 1.5},
	}}
	calculator := NewCalculator(catalog, CalculatorOptions{})
	row := calculator.DefaultModelPrice("custom", "openai", "token")
	require.Equal(t, "priced", row.PriceStatus)
	require.Equal(t, 100000, row.LongContextThreshold)
	values := make(map[string]float64)
	for _, price := range row.Prices {
		if price.Value != nil {
			values[price.Key] = *price.Value
		}
	}
	require.Contains(t, values, "input")
	require.Zero(t, values["input"])
	require.Equal(t, 4.0, values["output"])
	require.Equal(t, 8.0, values["fast_output"])
	require.Equal(t, 2.0, values["flex_output"])
	require.Equal(t, "unpriced", calculator.DefaultModelPrice("unknown-model", "openai", "token").PriceStatus)
	require.Equal(t, "priced", calculator.DefaultModelPrice("claude-sonnet-4", "anthropic", "token").PriceStatus)
}
