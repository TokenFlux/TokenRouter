package completion

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/TokenFlux/TokenRouter/internal/billing"
	"github.com/TokenFlux/TokenRouter/internal/billing/pricing"
)

// pricing_test.go checks how completion selects image and token prices from
// observed output. Fixtures separate returned files, animation frames, and
// token counts so only the configured billing mode determines which quantity
// supplies the charge.

// imageTokenCatalog supplies token prices for the specified image model.
type imageTokenCatalog struct{}

// GetModelPricing supplies fixed ordinary-input and image-output token prices
// for gpt-image-2. Other models return nil; the fixture has no per-image price,
// so positive token evidence selects exact token billing in the tested paths.
func (imageTokenCatalog) GetModelPricing(model string) *pricing.CatalogModelPricing {
	if model != "gpt-image-2" {
		return nil
	}
	return &pricing.CatalogModelPricing{InputCostPerToken: 1e-6, OutputCostPerToken: 2e-6, OutputCostPerImageToken: 3e-6}
}

// ForceUpdate satisfies the catalogue interface while retaining the fixed
// image-token fixture prices.
func (imageTokenCatalog) ForceUpdate() error { return nil }

// TestImageTokenUsageUsesExactTokenPricing supplies token evidence for an image
// result whose catalogue lacks a per-image rate. It checks both recording paths'
// token mode, image-output pricing, and missing-price rejection after that token
// evidence is removed.
func TestImageTokenUsageUsesExactTokenPricing(t *testing.T) {
	calculator := billing.NewCalculator(imageTokenCatalog{}, billing.CalculatorOptions{})
	recorder := NewRecorder(Dependencies{Calculator: calculator}, RecorderOptions{})
	result := &Result{Model: "gpt-image-2", ImageCount: 1, Usage: TokenUsage{InputTokens: 100, OutputTokens: 20, ImageOutputTokens: 20}}
	tokens := UsageTokens{InputTokens: 100, OutputTokens: 20, ImageOutputTokens: 20}
	// Image output occupies the reported output bucket, so use its image-token
	// price once rather than also adding the ordinary output-token rate.
	want := 100*1e-6 + 20*3e-6
	openAI, err := recorder.CalculateOpenAIRecordUsageCostAt(context.Background(), result, &KeySnapshot{}, []string{result.Model}, 1, 1, 1, 1, tokens, "", time.Now())
	require.NoError(t, err)
	require.Equal(t, string(BillingModeToken), openAI.BillingMode)
	// Permit arithmetic rounding when comparing the two token-priced dollar totals.
	require.InDelta(t, want, openAI.ActualCost, 1e-12)
	messages := recorder.CalculateRecordUsageCost(context.Background(), result, &KeySnapshot{}, &ProviderSnapshot{}, result.Model, result.Model, "", "", 1, 1, nil)
	require.Equal(t, string(BillingModeToken), messages.BillingMode)
	require.InDelta(t, want, messages.ActualCost, 1e-12)
	result.Usage = TokenUsage{}
	_, err = recorder.CalculateOpenAIRecordUsageCostAt(context.Background(), result, &KeySnapshot{}, []string{result.Model}, 1, 1, 1, 1, UsageTokens{}, "", time.Now())
	require.ErrorIs(t, err, pricing.ErrModelPricingUnavailable)
}

// TestImageBillingUnits preserves file counts for request and token cards.
// Only image mode consumes separately observed frame units, including mixed outputs
// whose static files each contribute one unit.
func TestImageBillingUnits(t *testing.T) {
	result := &Result{ImageCount: 2, ImageFrameCount: 18}
	require.Equal(t, 18, imageBillingUnits(result, nil))
	require.Equal(t, 18, imageBillingUnits(result, &ResolvedPricing{Mode: BillingModeImage}))
	require.Equal(t, 2, imageBillingUnits(result, &ResolvedPricing{Mode: BillingModePerRequest}))
	require.Equal(t, 2, imageBillingUnits(result, &ResolvedPricing{Mode: BillingModeToken}))
	result.ImageFrameCount = 0
	require.Equal(t, 2, imageBillingUnits(result, nil))
}
