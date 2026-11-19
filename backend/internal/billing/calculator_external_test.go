package billing_test

import (
	"context"
	"math"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/TokenFlux/TokenRouter/internal/billing"
	billingpricing "github.com/TokenFlux/TokenRouter/internal/billing/pricing"
	"github.com/TokenFlux/TokenRouter/internal/billing/testkit"
	"github.com/TokenFlux/TokenRouter/internal/gateway/provider/modelidentity"
	"github.com/TokenFlux/TokenRouter/internal/modelcatalog/provider"
	"github.com/TokenFlux/TokenRouter/internal/routing"
	"github.com/TokenFlux/TokenRouter/internal/routing/capability"
	routingtestkit "github.com/TokenFlux/TokenRouter/internal/routing/testkit"
)

// Direct model lookup remains unavailable when a calculator has no catalog
// data, while configured cards can still supply complete prices. These fixture
// scenarios isolate full-name identity, explicit zero buckets, aggregate cache
// limits and price modifiers, then compare selected unit amounts with token,
// media and operation charges.

const (
	// gpt56LadderCatalogJSON supplies standard, priority and above-threshold
	// fixture amounts for comparing grouped marketplace display with token
	// calculation, including cache writes and reads.
	gpt56LadderCatalogJSON = `{
	"gpt-5.6-sol": {"provider": "openai", "mode": "chat", "cache_write_multiplier": 1.25, "flex_multiplier": 0.5,
		"input_cost_per_token": 5e-06, "input_cost_per_token_priority": 1e-05,
		"output_cost_per_token": 3e-05, "output_cost_per_token_priority": 6e-05,
		"cache_read_input_token_cost": 5e-07, "cache_read_input_token_cost_priority": 1e-06,
		"input_cost_per_token_above_272k_tokens": 1e-05,
		"output_cost_per_token_above_272k_tokens": 4.5e-05,
		"cache_read_input_token_cost_above_272k_tokens": 1e-06},
	"gpt-5.6-terra": {"provider": "openai", "mode": "chat", "cache_write_multiplier": 1.25, "flex_multiplier": 0.5,
		"input_cost_per_token": 2e-06, "input_cost_per_token_priority": 4e-06,
		"output_cost_per_token": 1.2e-05, "output_cost_per_token_priority": 2.4e-05,
		"cache_read_input_token_cost": 2e-07, "cache_read_input_token_cost_priority": 4e-07,
		"input_cost_per_token_above_272k_tokens": 4e-06,
		"output_cost_per_token_above_272k_tokens": 1.8e-05,
		"cache_read_input_token_cost_above_272k_tokens": 4e-07},
	"gpt-5.6-luna": {"provider": "openai", "mode": "chat", "cache_write_multiplier": 1.25, "flex_multiplier": 0.5,
		"input_cost_per_token": 2e-07, "input_cost_per_token_priority": 4e-07,
		"output_cost_per_token": 1.2e-06, "output_cost_per_token_priority": 2.4e-06,
		"cache_read_input_token_cost": 2e-08, "cache_read_input_token_cost_priority": 4e-08,
		"input_cost_per_token_above_272k_tokens": 4e-07,
		"output_cost_per_token_above_272k_tokens": 1.8e-06,
		"cache_read_input_token_cost_above_272k_tokens": 4e-08}
}`

	// openAILadderCatalogJSON supplies absolute above-272k fixture amounts for
	// decoder-driven threshold conversion. Its two explicit model entries use
	// separate input and output surcharge ratios.
	openAILadderCatalogJSON = `{
	"gpt-5.4": {"provider": "openai", "mode": "chat", "fast_multiplier": 2, "flex_multiplier": 0.5,
		"input_cost_per_token": 2.5e-06, "output_cost_per_token": 1.5e-05,
		"cache_read_input_token_cost": 2.5e-07, "cache_creation_input_token_cost": 2.5e-06,
		"input_cost_per_token_above_272k_tokens": 5e-06,
		"output_cost_per_token_above_272k_tokens": 2.25e-05,
		"cache_read_input_token_cost_above_272k_tokens": 5e-07},
	"gpt-5.5-pro": {"provider": "openai", "mode": "chat",
		"input_cost_per_token": 3e-05, "output_cost_per_token": 1.8e-04,
		"input_cost_per_token_above_272k_tokens": 6e-05,
		"output_cost_per_token_above_272k_tokens": 2.7e-04}
}`
)

// TestCalculateCost_RateMultiplier_NegativeClampedToZero supplies negative,
// zero and positive user factors to fixture token pricing. Base cost remains
// positive while actual cost must match each expected clamped or positive
// factor.
func TestCalculateCost_RateMultiplier_NegativeClampedToZero(t *testing.T) {
	svc := newTestCalculator()
	tokens := billingpricing.UsageTokens{InputTokens: 1000, OutputTokens: 500}

	tests := []struct {
		name       string  // user factor case label
		multiplier float64 // supplied user charge factor
		wantRatio  float64 // expected actual-to-base cost factor
	}{
		{"negative clamped to 0", -1.5, 0},
		{"zero passes through as 0 (defense in depth)", 0, 0},
		{"positive 2x applied", 2.0, 2.0},
		{"positive 0.5x applied", 0.5, 0.5},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cost, err := svc.CalculateCost("claude-sonnet-4", tokens, tt.multiplier)
			require.NoError(t, err)
			require.Greater(t, cost.TotalCost, 0.0, "TotalCost should be non-zero")
			// Allow float multiplication residue in these scaled component comparisons.
			require.InDelta(t, tt.wantRatio*cost.TotalCost, cost.ActualCost, 1e-9)
		})
	}
}

// TestCalculateImageCost_RateMultiplier_NegativeClampedToZero prices two
// fixture images with negative, zero and positive factors. Their base total
// remains positive, and actual image charges must reflect the selected
// nonnegative factor.
func TestCalculateImageCost_RateMultiplier_NegativeClampedToZero(t *testing.T) {
	svc := newTestCalculator()

	tests := []struct {
		name       string  // image factor case label
		multiplier float64 // supplied image charge factor
		wantRatio  float64 // expected actual-to-base cost factor
	}{
		{"negative clamped to 0", -0.5, 0},
		{"zero passes through", 0, 0},
		{"positive 3x applied", 3.0, 3.0},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cost, mediaErr := svc.CalculateImageCost("grok-imagine-image-quality", "1K", 2, tt.multiplier)
			if mediaErr != nil {
				t.Fatal(mediaErr)
			}
			require.NotNil(t, cost)
			require.Greater(t, cost.TotalCost, 0.0)
			// Allow float multiplication residue in these scaled component comparisons.
			require.InDelta(t, tt.wantRatio*cost.TotalCost, cost.ActualCost, 1e-9)
		})
	}
}

// TestCalculateCost_BasicComputation supplies one thousand input and five
// hundred output tokens at known fixture rates. Component costs, base total
// and unit-factor actual cost must equal the expected token arithmetic.
func TestCalculateCost_BasicComputation(t *testing.T) {
	svc := newTestCalculator()

	// The fixture prices claude-sonnet-4 at $3/MTok input and $15/MTok output.
	tokens := billingpricing.UsageTokens{
		InputTokens:  1000,
		OutputTokens: 500,
	}
	cost, err := svc.CalculateCost("claude-sonnet-4", tokens, 1.0)
	require.NoError(t, err)

	// 1000 * 3e-6 = 0.003, 500 * 15e-6 = 0.0075
	expectedInput := 1000 * 3e-6
	expectedOutput := 500 * 15e-6
	// Allow float arithmetic residue in these calculated component amounts.
	require.InDelta(t, expectedInput, cost.InputCost, 1e-10)
	require.InDelta(t, expectedOutput, cost.OutputCost, 1e-10)
	require.InDelta(t, expectedInput+expectedOutput, cost.TotalCost, 1e-10)
	require.InDelta(t, expectedInput+expectedOutput, cost.ActualCost, 1e-10)
}

// TestCalculateCost_WithCacheTokens adds cache creation and reading to
// ordinary text usage. Each cache bucket must use its fixture rate, and total
// cost must include the text components and both cache amounts.
func TestCalculateCost_WithCacheTokens(t *testing.T) {
	svc := newTestCalculator()

	tokens := billingpricing.UsageTokens{
		InputTokens:         1000,
		OutputTokens:        500,
		CacheCreationTokens: 2000,
		CacheReadTokens:     3000,
	}
	cost, err := svc.CalculateCost("claude-sonnet-4", tokens, 1.0)
	require.NoError(t, err)

	expectedCacheCreation := 2000 * 3.75e-6
	expectedCacheRead := 3000 * 0.3e-6
	// Allow float arithmetic residue in these calculated component amounts.
	require.InDelta(t, expectedCacheCreation, cost.CacheCreationCost, 1e-10)
	require.InDelta(t, expectedCacheRead, cost.CacheReadCost, 1e-10)

	expectedTotal := cost.InputCost + cost.OutputCost + expectedCacheCreation + expectedCacheRead
	require.InDelta(t, expectedTotal, cost.TotalCost, 1e-10)
}

// TestCalculateCost_RateMultiplier calculates identical token usage with unit
// and doubled user factors. Base total must remain equal while the actual user
// charge doubles with the second factor.
func TestCalculateCost_RateMultiplier(t *testing.T) {
	svc := newTestCalculator()

	tokens := billingpricing.UsageTokens{InputTokens: 1000, OutputTokens: 500}

	cost1x, err := svc.CalculateCost("claude-sonnet-4", tokens, 1.0)
	require.NoError(t, err)

	cost2x, err := svc.CalculateCost("claude-sonnet-4", tokens, 2.0)
	require.NoError(t, err)

	// Allow float multiplication residue in these scaled component comparisons.
	require.InDelta(t, cost1x.TotalCost, cost2x.TotalCost, 1e-10)
	require.InDelta(t, cost1x.ActualCost*2, cost2x.ActualCost, 1e-10)
}

// TestGetModelPricing_FallbackMatchesByFamily queries supplied Claude version
// spellings against the test catalog. Only the registered Opus 4.8 full name
// returns its fixture amount; every other listed spelling must remain
// unavailable.
func TestGetModelPricing_FallbackMatchesByFamily(t *testing.T) {
	svc := newTestCalculator()

	tests := []struct {
		model string // requested full model spelling
		// expectedInput is compared only for the registered Opus row. Other model
		// rows assert unavailable pricing and never consume their retained reference
		// amount.
		expectedInput float64 // registered fixture input rate
	}{
		{"claude-opus-4.5-20250101", 5e-6},
		{"claude-opus-4-8", 5e-6},
		{"claude-3-opus-20240229", 15e-6},
		{"claude-sonnet-4-20250514", 3e-6},
		{"claude-3-5-sonnet-20241022", 3e-6},
		{"claude-3-5-haiku-20241022", 1e-6},
		{"claude-3-haiku-20240307", 0.25e-6},
	}

	for _, tt := range tests {
		pricing, err := svc.GetModelPricing(tt.model)
		if tt.model != "claude-opus-4-8" {
			require.ErrorIs(t, err, billingpricing.ErrModelPricingUnavailable)
			require.Nil(t, pricing)
			continue
		}
		require.NoError(t, err, "模型 %s", tt.model)
		// Allow binary representation residue in these fixture rate comparisons.
		require.InDelta(t, tt.expectedInput, pricing.InputPricePerToken, 1e-12, "模型 %s 输入价格", tt.model)
	}
}

// TestGetModelPricing_CaseInsensitive queries mixed-case and lowercase
// spellings of the same registered model. Both lookups must succeed and return
// equal input rates, protecting case normalization without testing cross-model
// aliases.
func TestGetModelPricing_CaseInsensitive(t *testing.T) {
	svc := newTestCalculator()

	p1, err := svc.GetModelPricing("Claude-Sonnet-4")
	require.NoError(t, err)

	p2, err := svc.GetModelPricing("claude-sonnet-4")
	require.NoError(t, err)

	require.Equal(t, p1.InputPricePerToken, p2.InputPricePerToken)
}

// TestGetModelPricing_GLM52UsesOwnPrice queries the registered GLM-5.2 fixture
// entry. Input, output and cached-input rates must match that full-name
// record, protecting its distinct lookup from shorter family-name selection.
func TestGetModelPricing_GLM52UsesOwnPrice(t *testing.T) {
	svc := newTestCalculator()

	pricing, err := svc.GetModelPricing("glm-5.2")
	require.NoError(t, err)
	require.NotNil(t, pricing)
	// GLM-5.2 uses its own fixture rates, matching GLM-5.1; a glm-5 substring
	// must not select another price.
	// Allow binary representation residue in these fixture rate comparisons.
	require.InDelta(t, 1.4e-6, pricing.InputPricePerToken, 1e-12)
	require.InDelta(t, 4.4e-6, pricing.OutputPricePerToken, 1e-12)
	require.InDelta(t, 0.26e-6, pricing.CacheReadPricePerToken, 1e-12)
}

// TestGetModelPricing_UnknownClaudeModelIsUnpriced requests a Claude spelling
// absent from the fixture catalog. The result must be nil with
// ErrModelPricingUnavailable, keeping a familiar family prefix insufficient
// for pricing.
func TestGetModelPricing_UnknownClaudeModelIsUnpriced(t *testing.T) {
	value, err := newTestCalculator().GetModelPricing("claude-unknown-model")
	require.ErrorIs(t, err, billingpricing.ErrModelPricingUnavailable)
	require.Nil(t, value)
}

// TestGetModelPricing_UnknownOpenAIModelReturnsError requests an absent GPT
// spelling from the fixture catalog. Lookup must return nil pricing and an
// error containing the missing-price diagnostic, exposing unavailability to
// callers.
func TestGetModelPricing_UnknownOpenAIModelReturnsError(t *testing.T) {
	svc := newTestCalculator()

	pricing, err := svc.GetModelPricing("gpt-unknown-model")
	require.Error(t, err)
	require.Nil(t, pricing)
	require.Contains(t, err.Error(), "pricing not found")
}

// TestGetModelPricing_OpenAIGPT54Fallback reads the registered GPT-5.4
// fixture's text and cache rates. Its omitted above-threshold metadata must
// leave the built-in long-context threshold and both multipliers zero.
func TestGetModelPricing_OpenAIGPT54Fallback(t *testing.T) {
	svc := newTestCalculator()

	pricing, err := svc.GetModelPricing("gpt-5.4")
	require.NoError(t, err)
	require.NotNil(t, pricing)
	// Allow binary representation residue in these fixture rate comparisons.
	require.InDelta(t, 2.5e-6, pricing.InputPricePerToken, 1e-12)
	require.InDelta(t, 15e-6, pricing.OutputPricePerToken, 1e-12)
	require.InDelta(t, 0.25e-6, pricing.CacheReadPricePerToken, 1e-12)
	// This fixture has neither explicit long-context rules nor above-threshold
	// amounts. The full model name alone therefore supplies no surcharge metadata
	// to this lookup.
	require.Zero(t, pricing.LongContextInputThreshold)
	require.Zero(t, pricing.LongContextInputMultiplier)
	require.Zero(t, pricing.LongContextOutputMultiplier)
}

// TestGetModelPricing_CatalogAboveTierFieldsDriveLongContext parses above-272k
// catalog amounts into calculation metadata. Lookup must retain the exclusive
// threshold and derive the supplied input and output surcharge factors.
func TestGetModelPricing_CatalogAboveTierFieldsDriveLongContext(t *testing.T) {
	svc := newLadderCalculator(t)

	pricing, err := svc.GetModelPricing("gpt-5.4")
	require.NoError(t, err)
	require.Equal(t, 272000, pricing.LongContextInputThreshold)
	// Allow binary representation residue in these fixture rate comparisons.
	require.InDelta(t, 2.0, pricing.LongContextInputMultiplier, 1e-12)
	require.InDelta(t, 1.5, pricing.LongContextOutputMultiplier, 1e-12)
	require.False(t, pricing.LongContextThresholdInclusive)
}

// TestGetModelPricing_OpenAIImplicitAliasesAreUnpriced requests the listed
// compact, prefixed and effort-suffixed spellings absent from the fixture
// catalog. Every lookup must return nil with pricing unavailable rather than
// borrowing another full model entry.
func TestGetModelPricing_OpenAIImplicitAliasesAreUnpriced(t *testing.T) {
	svc := newTestCalculator()
	for _, model := range []string{"gpt5.5", "gpt-5.5-pro-high", "openai/gpt5.5pro", "openai/gpt5.4", "gpt5.4-mini", "gpt5.3codexspark"} {
		value, err := svc.GetModelPricing(model)
		require.ErrorIs(t, err, billingpricing.ErrModelPricingUnavailable, model)
		require.Nil(t, value)
	}
}

// TestGetModelPricing_OpenAIGPT54MiniFallback reads the explicit GPT-5.4-mini
// fixture. Its three text/cache rates must match that entry, and its absent
// long-context metadata must leave the threshold zero.
func TestGetModelPricing_OpenAIGPT54MiniFallback(t *testing.T) {
	svc := newTestCalculator()

	pricing, err := svc.GetModelPricing("gpt-5.4-mini")
	require.NoError(t, err)
	require.NotNil(t, pricing)
	// Allow binary representation residue in these fixture rate comparisons.
	require.InDelta(t, 7.5e-7, pricing.InputPricePerToken, 1e-12)
	require.InDelta(t, 4.5e-6, pricing.OutputPricePerToken, 1e-12)
	require.InDelta(t, 7.5e-8, pricing.CacheReadPricePerToken, 1e-12)
	require.Zero(t, pricing.LongContextInputThreshold)
}

// TestCalculateCost_OpenAIGPT54LongContextAppliesWholeSessionMultipliers
// supplies input above the parsed fixture threshold. Whole input and output
// components must receive their respective factors, and unit-rate totals must
// retain the actual-charge marker.
func TestCalculateCost_OpenAIGPT54LongContextAppliesWholeSessionMultipliers(t *testing.T) {
	svc := newLadderCalculator(t)

	tokens := billingpricing.UsageTokens{
		InputTokens:  300000,
		OutputTokens: 4000,
	}

	cost, err := svc.CalculateCost("gpt-5.4", tokens, 1.0)
	require.NoError(t, err)

	expectedInput := float64(tokens.InputTokens) * 2.5e-6 * 2.0
	expectedOutput := float64(tokens.OutputTokens) * 15e-6 * 1.5
	// Allow float multiplication residue in these scaled component comparisons.
	require.InDelta(t, expectedInput, cost.InputCost, 1e-10)
	require.InDelta(t, expectedOutput, cost.OutputCost, 1e-10)
	require.InDelta(t, expectedInput+expectedOutput, cost.TotalCost, 1e-10)
	require.InDelta(t, expectedInput+expectedOutput, cost.ActualCost, 1e-10)
	require.True(t, cost.LongContextBillingApplied)
}

// TestCalculateCost_OpenAIGPT54LongContextMarkerRequiresActualCostIncrease
// prices above-threshold input with a zero user factor. Actual cost must
// remain zero, and the long-context marker must be false because this user
// charge does not increase.
func TestCalculateCost_OpenAIGPT54LongContextMarkerRequiresActualCostIncrease(t *testing.T) {
	svc := newLadderCalculator(t)

	cost, err := svc.CalculateCostWithServiceTier(
		"gpt-5.4",
		billingpricing.UsageTokens{InputTokens: 300000},
		0,
		"",
	)

	require.NoError(t, err)
	require.Zero(t, cost.ActualCost)
	require.False(t, cost.LongContextBillingApplied)
}

// TestCalculateCost_OpenAILongContextBoundaryIncludesCacheTokens fixes input
// and cache writes while varying cached reads by one token. Exact aggregate
// threshold must leave the surcharge marker false, while one token above must
// set it.
func TestCalculateCost_OpenAILongContextBoundaryIncludesCacheTokens(t *testing.T) {
	svc := newLadderCalculator(t)
	tests := []struct {
		name        string // context threshold case label
		cacheRead   int    // variable cached-input token count
		wantApplied bool   // expected actual-charge surcharge marker
	}{
		{name: "exact threshold uses base price", cacheRead: 72000, wantApplied: false},
		{name: "one token above threshold uses long price", cacheRead: 72001, wantApplied: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cost, err := svc.CalculateCost("gpt-5.4", billingpricing.UsageTokens{
				InputTokens:         100000,
				CacheCreationTokens: 100000,
				CacheReadTokens:     tt.cacheRead,
				OutputTokens:        1000,
			}, 1)

			require.NoError(t, err)
			require.Equal(t, tt.wantApplied, cost.LongContextBillingApplied)
		})
	}
}

// TestCalculateCost_GPT56SolMarketplaceIntervalsMatchSettlement builds grouped
// display intervals from the parsed GPT-5.6 fixture. Standard and priority
// calculations above its threshold must match the displayed bucket amounts and
// final grouped charge.
func TestCalculateCost_GPT56SolMarketplaceIntervalsMatchSettlement(t *testing.T) {
	svc := newCalculator(newStubCatalogFromJSON(t, gpt56LadderCatalogJSON))
	// Use one fixed user factor for display and actual-cost comparison, keeping
	// catalog unit prices distinct from the grouped amounts exposed to
	// marketplace readers.
	const groupRate = 3.0

	display := svc.DisplayPricing("gpt-5.6-sol", groupRate)
	require.Equal(t, "token", display.PricingMode)
	require.Len(t, display.ContextIntervals, 2)
	baseInterval := display.ContextIntervals[0]
	longInterval := display.ContextIntervals[1]
	require.NotNil(t, baseInterval.MaxTokens)
	require.Equal(t, 272000, *baseInterval.MaxTokens)
	require.Equal(t, 272000, longInterval.MinTokens)
	require.Nil(t, longInterval.MaxTokens)
	// Convert displayed per-token amounts to USD per million for these fixture
	// assertions. Apply the same conversion to ordinary, priority and cache rates
	// on both intervals.
	require.InDelta(t, 15.0, baseInterval.InputPricePerToken*1_000_000, 1e-10)
	require.InDelta(t, 90.0, baseInterval.OutputPricePerToken*1_000_000, 1e-10)
	require.InDelta(t, 18.75, baseInterval.CacheWritePricePerToken*1_000_000, 1e-10)
	require.InDelta(t, 1.5, baseInterval.CacheReadPricePerToken*1_000_000, 1e-10)
	require.InDelta(t, 30.0, baseInterval.FastInputPricePerToken*1_000_000, 1e-10)
	require.InDelta(t, 180.0, baseInterval.FastOutputPricePerToken*1_000_000, 1e-10)
	require.InDelta(t, 37.5, baseInterval.FastCacheWritePricePerToken*1_000_000, 1e-10)
	require.InDelta(t, 3.0, baseInterval.FastCacheReadPricePerToken*1_000_000, 1e-10)
	require.InDelta(t, 30.0, longInterval.InputPricePerToken*1_000_000, 1e-10)
	require.InDelta(t, 135.0, longInterval.OutputPricePerToken*1_000_000, 1e-10)
	require.InDelta(t, 37.5, longInterval.CacheWritePricePerToken*1_000_000, 1e-10)
	require.InDelta(t, 3.0, longInterval.CacheReadPricePerToken*1_000_000, 1e-10)
	require.InDelta(t, 60.0, longInterval.FastInputPricePerToken*1_000_000, 1e-10)
	require.InDelta(t, 270.0, longInterval.FastOutputPricePerToken*1_000_000, 1e-10)
	require.InDelta(t, 75.0, longInterval.FastCacheWritePricePerToken*1_000_000, 1e-10)
	require.InDelta(t, 6.0, longInterval.FastCacheReadPricePerToken*1_000_000, 1e-10)

	tokens := billingpricing.UsageTokens{
		InputTokens:         100000,
		CacheCreationTokens: 100000,
		CacheReadTokens:     72001,
		OutputTokens:        1000,
	}
	tiers := []struct {
		// name labels this display comparison and serviceTier selects its
		// calculation tier. inputPrice, outputPrice, cacheWritePrice and
		// cacheReadPrice are the grouped display amounts used to reconstruct that
		// tier's expected charge.
		name, // service-tier comparison case label
		serviceTier string // calculation billing tier text
		inputPrice, // displayed input USD per token
		outputPrice float64 // displayed output USD per token
		cacheWritePrice, // displayed cache-write USD per token
		cacheReadPrice float64 // displayed cache-read USD per token
	}{
		{
			name:            "standard",
			inputPrice:      longInterval.InputPricePerToken,
			outputPrice:     longInterval.OutputPricePerToken,
			cacheWritePrice: longInterval.CacheWritePricePerToken,
			cacheReadPrice:  longInterval.CacheReadPricePerToken,
		},
		{
			name:            "priority",
			serviceTier:     "priority",
			inputPrice:      longInterval.FastInputPricePerToken,
			outputPrice:     longInterval.FastOutputPricePerToken,
			cacheWritePrice: longInterval.FastCacheWritePricePerToken,
			cacheReadPrice:  longInterval.FastCacheReadPricePerToken,
		},
	}
	for _, tier := range tiers {
		t.Run(tier.name, func(t *testing.T) {
			cost, err := svc.CalculateCostWithServiceTier("gpt-5.6-sol", tokens, groupRate, tier.serviceTier)
			require.NoError(t, err)
			require.True(t, cost.LongContextBillingApplied)
			require.InDelta(t, float64(tokens.InputTokens)*tier.inputPrice, cost.InputCost*groupRate, 1e-10)
			require.InDelta(t, float64(tokens.OutputTokens)*tier.outputPrice, cost.OutputCost*groupRate, 1e-10)
			require.InDelta(t, float64(tokens.CacheCreationTokens)*tier.cacheWritePrice, cost.CacheCreationCost*groupRate, 1e-10)
			require.InDelta(t, float64(tokens.CacheReadTokens)*tier.cacheReadPrice, cost.CacheReadCost*groupRate, 1e-10)
			wantActualCost := float64(tokens.InputTokens)*tier.inputPrice +
				float64(tokens.OutputTokens)*tier.outputPrice +
				float64(tokens.CacheCreationTokens)*tier.cacheWritePrice +
				float64(tokens.CacheReadTokens)*tier.cacheReadPrice
			require.InDelta(t, wantActualCost, cost.ActualCost, 1e-10)
		})
	}
}

// TestApplyLongContextDisplayMultipliersScalesAllCachePrices supplies distinct
// ordinary, priority and cache-TTL rates with separate input/output factors.
// The result must be a different object with expected scaled buckets while the
// checked source input rate remains unchanged.
func TestApplyLongContextDisplayMultipliersScalesAllCachePrices(t *testing.T) {
	pricing := &billingpricing.ModelPricing{
		InputPricePerToken:                 1,
		InputPricePerTokenPriority:         2,
		OutputPricePerToken:                3,
		OutputPricePerTokenPriority:        4,
		CacheCreationPricePerToken:         5,
		CacheCreationPricePerTokenPriority: 6,
		CacheCreation5mPrice:               7,
		CacheCreation1hPrice:               8,
		CacheReadPricePerToken:             9,
		CacheReadPricePerTokenPriority:     10,
		LongContextInputMultiplier:         2,
		LongContextOutputMultiplier:        1.5,
	}

	adjusted := billingpricing.ApplyLongContextDisplayMultipliers(pricing)

	require.NotSame(t, pricing, adjusted)
	require.Equal(t, float64(1), pricing.InputPricePerToken)
	require.Equal(t, float64(2), adjusted.InputPricePerToken)
	require.Equal(t, float64(4), adjusted.InputPricePerTokenPriority)
	require.Equal(t, 4.5, adjusted.OutputPricePerToken)
	require.Equal(t, float64(6), adjusted.OutputPricePerTokenPriority)
	require.Equal(t, float64(10), adjusted.CacheCreationPricePerToken)
	require.Equal(t, float64(12), adjusted.CacheCreationPricePerTokenPriority)
	require.Equal(t, float64(14), adjusted.CacheCreation5mPrice)
	require.Equal(t, float64(16), adjusted.CacheCreation1hPrice)
	require.Equal(t, float64(18), adjusted.CacheReadPricePerToken)
	require.Equal(t, float64(20), adjusted.CacheReadPricePerTokenPriority)
}

// TestCalculateCostUnified_ExplicitIntervalsDoNotReapplyLongContextMultiplier
// uses manual short/long rates with the default fixture base, which has no
// built-in ladder. Calculation checks the chosen manual buckets and false
// surcharge marker; display checks their grouped long amounts.
func TestCalculateCostUnified_ExplicitIntervalsDoNotReapplyLongContextMultiplier(t *testing.T) {
	svc := newTestCalculator()
	resolver := testkit.PriceResolver(nil, svc)
	basePricing, err := svc.GetModelPricing("gpt-5.6-sol")
	require.NoError(t, err)

	// Place the explicit interval boundary at the fixture's chosen context scale.
	// The supplied aggregate of 272001 then exercises manual long-range selection
	// rather than catalog tier metadata.
	shortMax := 272000
	shortInput := 7e-6
	shortOutput := 31e-6
	shortCacheWrite := 3e-6
	shortCacheRead := 0.3e-6
	longInput := 11e-6
	longOutput := 41e-6
	longCacheWrite := 4e-6
	longCacheRead := 0.4e-6
	resolved := &billingpricing.ResolvedPricing{
		Mode:        routing.BillingModeToken,
		BasePricing: basePricing,
		Source:      billingpricing.PricingSourceConfig,
		Intervals: []routing.PricingInterval{
			{
				MinTokens: 0, MaxTokens: &shortMax,
				InputPrice: &shortInput, OutputPrice: &shortOutput,
				CacheWritePrice: &shortCacheWrite, CacheReadPrice: &shortCacheRead,
			},
			{
				MinTokens:  shortMax,
				InputPrice: &longInput, OutputPrice: &longOutput,
				CacheWritePrice: &longCacheWrite, CacheReadPrice: &longCacheRead,
			},
		},
	}
	tokens := billingpricing.UsageTokens{
		InputTokens:         100000,
		CacheCreationTokens: 100000,
		CacheReadTokens:     72001,
		OutputTokens:        1000,
	}
	// Use the same user factor in calculation and display, so their manual
	// interval amounts are compared after a common final scaling step.
	const groupRate = 2.0

	cost, err := svc.CalculateCostUnified(billing.CostInput{
		Ctx:            context.Background(),
		Model:          "gpt-5.6-sol",
		Tokens:         tokens,
		RateMultiplier: groupRate,
		Resolver:       resolver,
		Resolved:       resolved,
	})

	require.NoError(t, err)
	require.False(t, cost.LongContextBillingApplied)
	// Allow float multiplication residue in these scaled component comparisons.
	require.InDelta(t, float64(tokens.InputTokens)*longInput, cost.InputCost, 1e-10)
	require.InDelta(t, float64(tokens.OutputTokens)*longOutput, cost.OutputCost, 1e-10)
	require.InDelta(t, float64(tokens.CacheCreationTokens)*longCacheWrite, cost.CacheCreationCost, 1e-10)
	require.InDelta(t, float64(tokens.CacheReadTokens)*longCacheRead, cost.CacheReadCost, 1e-10)
	wantActualCost := (float64(tokens.InputTokens)*longInput +
		float64(tokens.OutputTokens)*longOutput +
		float64(tokens.CacheCreationTokens)*longCacheWrite +
		float64(tokens.CacheReadTokens)*longCacheRead) * groupRate
	require.InDelta(t, wantActualCost, cost.ActualCost, 1e-10)

	display, ok := billingpricing.DisplayPricingFromResolved("gpt-5.6-sol", groupRate, resolved)
	require.True(t, ok)
	require.Len(t, display.ContextIntervals, 2)
	displayLong := display.ContextIntervals[1]
	require.InDelta(t, longInput*groupRate, displayLong.InputPricePerToken, 1e-12)
	require.InDelta(t, longOutput*groupRate, displayLong.OutputPricePerToken, 1e-12)
	require.InDelta(t, longCacheWrite*groupRate, displayLong.CacheWritePricePerToken, 1e-12)
	require.InDelta(t, longCacheRead*groupRate, displayLong.CacheReadPricePerToken, 1e-12)
}

// TestCalculateCost_OpenAIGPT55ProLongContextAppliesWholeSessionMultipliers
// supplies above-threshold usage to parsed GPT-5.5-pro fixture pricing. Input,
// output and totals must reflect the complete-session factors for their
// respective buckets.
func TestCalculateCost_OpenAIGPT55ProLongContextAppliesWholeSessionMultipliers(t *testing.T) {
	svc := newLadderCalculator(t)

	tokens := billingpricing.UsageTokens{
		InputTokens:  300000,
		OutputTokens: 4000,
	}

	cost, err := svc.CalculateCost("gpt-5.5-pro", tokens, 1.0)
	require.NoError(t, err)

	expectedInput := float64(tokens.InputTokens) * 30e-6 * 2.0
	expectedOutput := float64(tokens.OutputTokens) * 180e-6 * 1.5
	// Allow float multiplication residue in these scaled component comparisons.
	require.InDelta(t, expectedInput, cost.InputCost, 1e-10)
	require.InDelta(t, expectedOutput, cost.OutputCost, 1e-10)
	require.InDelta(t, expectedInput+expectedOutput, cost.TotalCost, 1e-10)
	require.InDelta(t, expectedInput+expectedOutput, cost.ActualCost, 1e-10)
}

// TestCalculateCost_OpenAIGPT54LongContextAppliesMultiplierToCacheRead reaches
// the long tier primarily through cached reads. Text input and cached input
// must receive the input factor, output its separate factor, and totals must
// include all three components.
func TestCalculateCost_OpenAIGPT54LongContextAppliesMultiplierToCacheRead(t *testing.T) {
	svc := newLadderCalculator(t)

	// Text input and cached reads contribute 301000 context tokens, so this
	// fixture reaches the long tier through cached input rather than ordinary
	// prompt volume alone.
	tokens := billingpricing.UsageTokens{
		InputTokens:     1000,
		CacheReadTokens: 300000,
		OutputTokens:    1000,
	}

	cost, err := svc.CalculateCost("gpt-5.4", tokens, 1.0)
	require.NoError(t, err)

	expectedInput := float64(tokens.InputTokens) * 2.5e-6 * 2.0
	expectedOutput := float64(tokens.OutputTokens) * 15e-6 * 1.5
	expectedCacheRead := float64(tokens.CacheReadTokens) * 0.25e-6 * 2.0

	// Allow float multiplication residue in these scaled component comparisons.
	require.InDelta(t, expectedInput, cost.InputCost, 1e-10)
	require.InDelta(t, expectedOutput, cost.OutputCost, 1e-10)
	require.InDelta(t, expectedCacheRead, cost.CacheReadCost, 1e-10,
		"cache_read_cost should be scaled by LongContextInputMultiplier when long-context pricing applies (issue #2293)")

	expectedTotal := expectedInput + expectedOutput + expectedCacheRead
	require.InDelta(t, expectedTotal, cost.TotalCost, 1e-10)
	require.InDelta(t, expectedTotal, cost.ActualCost, 1e-10)
}

// TestCalculateCost_OpenAIGPT54NoLongContextKeepsCacheReadAtBasePrice supplies
// aggregate input below the parsed long-context threshold. Its cache-read
// component must retain the ordinary fixture rate, isolating that bucket from
// above-threshold scaling.
func TestCalculateCost_OpenAIGPT54NoLongContextKeepsCacheReadAtBasePrice(t *testing.T) {
	svc := newLadderCalculator(t)

	// Text input and cached reads total 101000 context tokens. Their combined
	// value stays below the fixture threshold, leaving the read bucket at its
	// ordinary amount.
	tokens := billingpricing.UsageTokens{
		InputTokens:     1000,
		CacheReadTokens: 100000,
		OutputTokens:    1000,
	}

	cost, err := svc.CalculateCost("gpt-5.4", tokens, 1.0)
	require.NoError(t, err)

	expectedCacheRead := float64(tokens.CacheReadTokens) * 0.25e-6
	// Allow float arithmetic residue in these calculated component amounts.
	require.InDelta(t, expectedCacheRead, cost.CacheReadCost, 1e-10,
		"cache_read_cost should remain at base price when below long-context threshold")
}

// TestCalculateCost_OpenAIGPT54LongContextAppliesMultiplierToCacheCreation
// supplies input, cached reads and cache writes above the parsed threshold.
// The cache-creation component must double its separately supplied fixture
// write rate.
func TestCalculateCost_OpenAIGPT54LongContextAppliesMultiplierToCacheCreation(t *testing.T) {
	svc := newLadderCalculator(t)

	// Text input, cached reads and aggregate writes contribute 311000 context
	// tokens. The write bucket is therefore charged on the long tier selected by
	// their combined context.
	tokens := billingpricing.UsageTokens{
		InputTokens:         1000,
		CacheReadTokens:     300000,
		CacheCreationTokens: 10000,
		OutputTokens:        1000,
	}

	cost, err := svc.CalculateCost("gpt-5.4", tokens, 1.0)
	require.NoError(t, err)

	// The fixture sets CacheCreationPricePerToken to 2.5e-6 and
	// LongContextInputMultiplier to 2.0.
	expectedCacheCreation := float64(tokens.CacheCreationTokens) * 2.5e-6 * 2.0
	// Allow float multiplication residue in these scaled component comparisons.
	require.InDelta(t, expectedCacheCreation, cost.CacheCreationCost, 1e-10,
		"cache_creation_cost should be scaled by LongContextInputMultiplier when long-context pricing applies")
}

// TestCalculateCost_OpenAIGPT54NoLongContextKeepsCacheCreationAtBasePrice
// supplies text/cache usage whose aggregate remains below threshold. Cache
// creation must retain its ordinary fixture bucket amount rather than
// receiving a long-context surcharge.
func TestCalculateCost_OpenAIGPT54NoLongContextKeepsCacheCreationAtBasePrice(t *testing.T) {
	svc := newLadderCalculator(t)

	// Including the aggregate cache writes makes this supplied context 111000
	// tokens. It still stays below threshold, so the separately checked write
	// bucket keeps its base rate.
	tokens := billingpricing.UsageTokens{
		InputTokens:         1000,
		CacheReadTokens:     100000,
		CacheCreationTokens: 10000,
		OutputTokens:        1000,
	}

	cost, err := svc.CalculateCost("gpt-5.4", tokens, 1.0)
	require.NoError(t, err)

	expectedCacheCreation := float64(tokens.CacheCreationTokens) * 2.5e-6
	// Allow float arithmetic residue in these calculated component amounts.
	require.InDelta(t, expectedCacheCreation, cost.CacheCreationCost, 1e-10,
		"cache_creation_cost should remain at base price when below long-context threshold")
}

// TestCalculateCost_LongContextAppliesMultiplierToCacheCreation5mAnd1h
// supplies separate five-minute and hourly cache-write details with
// above-threshold input. Their combined component must scale both fixture TTL
// prices by the input surcharge factor.
func TestCalculateCost_LongContextAppliesMultiplierToCacheCreation5mAnd1h(t *testing.T) {
	svc := newCalculatorWithPrices(nil, map[string]*billingpricing.ModelPricing{
		"claude-sonnet-4": {
			InputPricePerToken:          3e-6,
			OutputPricePerToken:         15e-6,
			CacheReadPricePerToken:      0.3e-6,
			SupportsCacheBreakdown:      true,
			CacheCreation5mPrice:        4e-6,
			CacheCreation1hPrice:        5e-6,
			LongContextInputThreshold:   272000,
			LongContextInputMultiplier:  2.0,
			LongContextOutputMultiplier: 1.5,
		},
	})

	// Ordinary input plus cached reads already contribute 301000 context tokens.
	// This exceeds threshold independently of the detailed TTL counts whose
	// scaled write charge is asserted below.
	tokens := billingpricing.UsageTokens{
		InputTokens:           1000,
		CacheReadTokens:       300000,
		CacheCreation5mTokens: 8000,
		CacheCreation1hTokens: 4000,
		OutputTokens:          1000,
	}

	cost, err := svc.CalculateCost("claude-sonnet-4", tokens, 1.0)
	require.NoError(t, err)

	expected5m := float64(tokens.CacheCreation5mTokens) * 4e-6 * 2.0
	expected1h := float64(tokens.CacheCreation1hTokens) * 5e-6 * 2.0
	// Allow float multiplication residue in these scaled component comparisons.
	require.InDelta(t, expected5m+expected1h, cost.CacheCreationCost, 1e-10,
		"both 5m and 1h cache_creation prices should be scaled by LongContextInputMultiplier")
}

// TestGetModelPricing_DoubaoEmbeddingVisionImageInputRate checks bare and
// case-folded registered names plus one absent dated spelling. Valid entries
// must expose the fixture text/image input rates and zero output, while the
// absent full name stays unavailable.
func TestGetModelPricing_DoubaoEmbeddingVisionImageInputRate(t *testing.T) {
	svc := newTestCalculator()

	for _, model := range []string{
		"doubao-embedding-vision",
		"doubao-embedding-vision-251215",
		"Doubao-Embedding-Vision",
	} {
		pricing, err := svc.GetModelPricing(model)
		if model == "grok-4.5-latest" || model == "grok-4.6-latest" || model == "grok-4.20-reasoning" || model == "grok-4.20-non-reasoning" || model == "grok-build" || model == "grok-composer" || model == "composer-2.5" || model == "doubao-embedding-vision-251215" {
			require.ErrorIs(t, err, billingpricing.ErrModelPricingUnavailable)
			require.Nil(t, pricing)
			continue
		}
		require.NoError(t, err)
		// Allow binary representation residue in these fixture rate comparisons.
		require.InDelta(t, 0.098e-6, pricing.InputPricePerToken, 1e-12)
		require.InDelta(t, 0.252e-6, pricing.ImageInputPricePerToken, 1e-12)
		require.Zero(t, pricing.OutputPricePerToken)
	}
}

// TestCalculateCost_DoubaoEmbeddingVisionDifferentialInput separates image
// tokens from a shared prompt count. Mixed and text-only charges must match
// their respective rates, and oversized image counts must cap at total input
// with zero text remainder.
func TestCalculateCost_DoubaoEmbeddingVisionDifferentialInput(t *testing.T) {
	svc := newTestCalculator()

	// Mixed input has 1340 prompt tokens: 28 image and 1312 text tokens.
	cost, err := svc.CalculateCost("doubao-embedding-vision", billingpricing.UsageTokens{
		InputTokens:      1340,
		ImageInputTokens: 28,
	}, 1.0)
	require.NoError(t, err)
	wantText := float64(1312) * 0.098e-6
	wantImage := float64(28) * 0.252e-6
	// Allow float arithmetic residue in these calculated component amounts.
	require.InDelta(t, wantText, cost.InputCost, 1e-15, "InputCost 仅计文本输入")
	require.InDelta(t, wantImage, cost.ImageInputCost, 1e-15, "ImageInputCost 单独计图片输入")
	require.InDelta(t, wantText+wantImage, cost.TotalCost, 1e-15, "TotalCost 口径不变")
	require.Zero(t, cost.OutputCost)

	// Text-only input uses the text rate throughout.
	textOnly := billingpricing.UsageTokens{InputTokens: 1340}
	costText, err := svc.CalculateCost("doubao-embedding-vision", textOnly, 1.0)
	require.NoError(t, err)
	require.InDelta(t, float64(1340)*0.098e-6, costText.InputCost, 1e-15)
	require.Zero(t, costText.ImageInputCost)

	// Cap upstream image token counts at total input tokens.
	costWeird, err := svc.CalculateCost("doubao-embedding-vision", billingpricing.UsageTokens{
		InputTokens:      10,
		ImageInputTokens: 50,
	}, 1.0)
	require.NoError(t, err)
	require.Zero(t, costWeird.InputCost, "全为图片输入时文本费用为 0")
	require.InDelta(t, float64(10)*0.252e-6, costWeird.ImageInputCost, 1e-15)
	require.InDelta(t, float64(10)*0.252e-6, costWeird.TotalCost, 1e-15)
}

// TestComputeTokenBreakdown_GptImage2ImageEditIssue4386 supplies 352 image
// plus nineteen text input tokens and all-image output. Separate fixture rates
// must produce the asserted component charges and 0.016081 USD total, with
// zero text output cost.
func TestComputeTokenBreakdown_GptImage2ImageEditIssue4386(t *testing.T) {
	svc := newTestCalculator()

	pricing := &billingpricing.ModelPricing{
		InputPricePerToken:       5e-6,
		ImageInputPricePerToken:  8e-6,
		OutputPricePerToken:      10e-6,
		ImageOutputPricePerToken: 30e-6,
		ImageOutputPriceExplicit: true,
	}
	tokens := billingpricing.UsageTokens{
		InputTokens:       371,
		ImageInputTokens:  352,
		OutputTokens:      439,
		ImageOutputTokens: 439,
	}

	cost := svc.ComputeTokenBreakdown(pricing, tokens, 1.0, "", false)

	wantTextInput := float64(19) * 5e-6     // 0.000095
	wantImageInput := float64(352) * 8e-6   // 0.002816
	wantImageOutput := float64(439) * 30e-6 // 0.013170
	// Allow float residue in the selected media-unit charge comparisons.
	require.InDelta(t, wantTextInput, cost.InputCost, 1e-15, "InputCost 仅含文本输入")
	require.InDelta(t, wantImageInput, cost.ImageInputCost, 1e-15, "图片输入按 $8/1M 独立计费")
	require.Zero(t, cost.OutputCost, "输出全部为图片，文本输出费用为 0")
	require.InDelta(t, wantImageOutput, cost.ImageOutputCost, 1e-15)
	require.InDelta(t, 0.016081, cost.TotalCost, 1e-9, "总额应为 $0.016081（修复前为 $0.015025）")
}

// TestCalculateImageCost prices three images using the registered
// quality-model size fixture. Base and unit-factor actual totals must both
// equal three times its supplied 0.05 USD per-image amount.
func TestCalculateImageCost(t *testing.T) {
	svc := newTestCalculator()

	cost, mediaErr := svc.CalculateImageCost("grok-imagine-image-quality", "1K", 3, 1.0)
	if mediaErr != nil {
		t.Fatal(mediaErr)
	}

	// Allow float residue in the selected media-unit charge comparisons.
	require.InDelta(t, 0.05*3, cost.TotalCost, 1e-10)
	require.InDelta(t, 0.05*3, cost.ActualCost, 1e-10)
}

// TestCalculateVideoCostBillsPerSecond compares one-second and fifteen-second
// fixture videos, then nonpositive and long durations. Totals must use
// the per-second rate with the selected eight-second default while retaining
// the complete duration for a long video.
func TestCalculateVideoCostBillsPerSecond(t *testing.T) {
	svc := newTestCalculator()

	oneSecond, mediaErr := svc.CalculateVideoCost("grok-imagine-video", "720p", 1, 1, 1.0)
	if mediaErr != nil {
		t.Fatal(mediaErr)
	}
	fifteenSeconds, mediaErr := svc.CalculateVideoCost("grok-imagine-video", "720p", 1, 15, 1.0)
	if mediaErr != nil {
		t.Fatal(mediaErr)
	}
	// Missing duration uses eight seconds; long observed outputs retain their
	// full duration so settlement includes every generated second.
	defaultDuration, mediaErr := svc.CalculateVideoCost("grok-imagine-video", "720p", 1, 0, 1.0)
	if mediaErr != nil {
		t.Fatal(mediaErr)
	}
	longDuration, mediaErr := svc.CalculateVideoCost("grok-imagine-video", "720p", 1, 999, 1.0)
	if mediaErr != nil {
		t.Fatal(mediaErr)
	}

	// Allow float residue in the selected media-unit charge comparisons.
	require.InDelta(t, 0.07, oneSecond.TotalCost, 1e-10)
	require.InDelta(t, 0.07*15, fifteenSeconds.TotalCost, 1e-10)
	require.InDelta(t, 0.07*8, defaultDuration.TotalCost, 1e-10)
	require.InDelta(t, 0.07*999, longDuration.TotalCost, 1e-10)
}

// TestCalculateGrokImagineImageCostUsesDefaultRateCard queries two sizes for
// ordinary and quality image fixtures. The supplied four per-image amounts
// must determine totals, including the ordinary model's equal rates across
// those sizes.
func TestCalculateGrokImagineImageCostUsesDefaultRateCard(t *testing.T) {
	svc := newTestCalculator()

	standard1K, mediaErr := svc.CalculateImageCost("grok-imagine-image", "1K", 1, 1.0)
	if mediaErr != nil {
		t.Fatal(mediaErr)
	}
	standard2K, mediaErr := svc.CalculateImageCost("grok-imagine-image", "2K", 1, 1.0)
	if mediaErr != nil {
		t.Fatal(mediaErr)
	}
	quality1K, mediaErr := svc.CalculateImageCost("grok-imagine-image-quality", "1K", 1, 1.0)
	if mediaErr != nil {
		t.Fatal(mediaErr)
	}
	quality2K, mediaErr := svc.CalculateImageCost("grok-imagine-image-quality", "2K", 1, 1.0)
	if mediaErr != nil {
		t.Fatal(mediaErr)
	}

	// Allow float residue in the selected media-unit charge comparisons.
	require.InDelta(t, 0.02, standard1K.TotalCost, 1e-10)
	require.InDelta(t, 0.02, standard2K.TotalCost, 1e-10)
	require.InDelta(t, 0.05, quality1K.TotalCost, 1e-10)
	require.InDelta(t, 0.07, quality2K.TotalCost, 1e-10)
}

// TestCalculateGrokImagineVideoCostUsesDefaultRateCard queries one-second
// outputs for two registered video fixtures across supported resolutions. Each
// total must match its specific fixture per-second amount, including the newer
// model's highest resolution.
func TestCalculateGrokImagineVideoCostUsesDefaultRateCard(t *testing.T) {
	svc := newTestCalculator()

	// Use one second to check the fixture's per-second rates.
	standard480P, mediaErr := svc.CalculateVideoCost("grok-imagine-video", "480p", 1, 1, 1.0)
	if mediaErr != nil {
		t.Fatal(mediaErr)
	}
	standard720P, mediaErr := svc.CalculateVideoCost("grok-imagine-video", "720p", 1, 1, 1.0)
	if mediaErr != nil {
		t.Fatal(mediaErr)
	}
	video15_480P, mediaErr := svc.CalculateVideoCost("grok-imagine-video-1.5", "480p", 1, 1, 1.0)
	if mediaErr != nil {
		t.Fatal(mediaErr)
	}
	video15_720P, mediaErr := svc.CalculateVideoCost("grok-imagine-video-1.5", "720p", 1, 1, 1.0)
	if mediaErr != nil {
		t.Fatal(mediaErr)
	}
	video15_1080P, mediaErr := svc.CalculateVideoCost("grok-imagine-video-1.5", "1080p", 1, 1, 1.0)
	if mediaErr != nil {
		t.Fatal(mediaErr)
	}

	// Allow float residue in the selected media-unit charge comparisons.
	require.InDelta(t, 0.05, standard480P.TotalCost, 1e-10)
	require.InDelta(t, 0.07, standard720P.TotalCost, 1e-10)
	require.InDelta(t, 0.08, video15_480P.TotalCost, 1e-10)
	require.InDelta(t, 0.14, video15_720P.TotalCost, 1e-10)
	require.InDelta(t, 0.25, video15_1080P.TotalCost, 1e-10)
}

// TestCalculateCost_ZeroTokens supplies an empty usage record for a catalogued
// fixture model. Calculation must succeed with zero base total and actual
// cost, retaining known pricing independently of usage quantity.
func TestCalculateCost_ZeroTokens(t *testing.T) {
	svc := newTestCalculator()

	cost, err := svc.CalculateCost("claude-sonnet-4", billingpricing.UsageTokens{}, 1.0)
	require.NoError(t, err)
	require.Equal(t, 0.0, cost.TotalCost)
	require.Equal(t, 0.0, cost.ActualCost)
}

// TestForceUpdatePricing_NilService constructs a calculator with no catalog
// reader and requests refresh. It must return an initialization error,
// demonstrating refresh availability separately from configured-card price
// calculation.
func TestForceUpdatePricing_NilService(t *testing.T) {
	svc := billing.NewCalculator(nil, billing.CalculatorOptions{})

	err := svc.ForceUpdatePricing()
	require.Error(t, err)
	require.Contains(t, err.Error(), "not initialized")
}

// TestGetModelPricing_Grok45OfficialFallback queries registered Grok-4.5 and
// an absent latest suffix. The registered name must expose its fixture
// text/cache amounts and unsupported cache breakdown, while the suffix stays
// unavailable.
func TestGetModelPricing_Grok45OfficialFallback(t *testing.T) {
	svc := newTestCalculator()

	for _, model := range []string{"grok-4.5", "grok-4.5-latest"} {
		t.Run(model, func(t *testing.T) {
			pricing, err := svc.GetModelPricing(model)
			if model == "grok-4.5-latest" || model == "grok-4.6-latest" || model == "grok-4.20-reasoning" || model == "grok-4.20-non-reasoning" || model == "grok-build" || model == "grok-composer" || model == "composer-2.5" || model == "doubao-embedding-vision-251215" {
				require.ErrorIs(t, err, billingpricing.ErrModelPricingUnavailable)
				require.Nil(t, pricing)
				return
			}
			require.NoError(t, err)
			// Allow binary representation residue in these fixture rate comparisons.
			require.InDelta(t, 2e-6, pricing.InputPricePerToken, 1e-12)
			require.InDelta(t, 6e-6, pricing.OutputPricePerToken, 1e-12)
			require.InDelta(t, 0.3e-6, pricing.CacheReadPricePerToken, 1e-12)
			require.False(t, pricing.SupportsCacheBreakdown)
		})
	}
}

// TestGetModelPricing_GrokBareAliasesUseGrok46 requests bare Grok and
// latest-alias text from the default fixture catalog. Both must return nil
// with pricing unavailable for these unregistered full spellings.
func TestGetModelPricing_GrokBareAliasesUseGrok46(t *testing.T) {
	svc := newTestCalculator()
	for _, model := range []string{"grok", "grok-latest"} {
		pricing, err := svc.GetModelPricing(model)
		require.ErrorIs(t, err, billingpricing.ErrModelPricingUnavailable)
		require.Nil(t, pricing)
	}
}

// TestGetModelPricing_Grok46OfficialFallback queries registered Grok-4.6 and
// an absent latest suffix. The registered fixture returns its text/cache
// amounts and 200000-token factors, while the suffixed name stays unavailable.
func TestGetModelPricing_Grok46OfficialFallback(t *testing.T) {
	svc := newTestCalculator()

	for _, model := range []string{"grok-4.6", "grok-4.6-latest"} {
		t.Run(model, func(t *testing.T) {
			pricing, err := svc.GetModelPricing(model)
			if model == "grok-4.5-latest" || model == "grok-4.6-latest" || model == "grok-4.20-reasoning" || model == "grok-4.20-non-reasoning" || model == "grok-build" || model == "grok-composer" || model == "composer-2.5" || model == "doubao-embedding-vision-251215" {
				require.ErrorIs(t, err, billingpricing.ErrModelPricingUnavailable)
				require.Nil(t, pricing)
				return
			}
			require.NoError(t, err)
			// Allow binary representation residue in these fixture rate comparisons.
			require.InDelta(t, 2e-6, pricing.InputPricePerToken, 1e-12)
			require.InDelta(t, 6e-6, pricing.OutputPricePerToken, 1e-12)
			require.InDelta(t, 0.5e-6, pricing.CacheReadPricePerToken, 1e-12)
			require.Equal(t, 200000, pricing.LongContextInputThreshold)
			require.InDelta(t, 2.0, pricing.LongContextInputMultiplier, 1e-12)
			require.InDelta(t, 2.0, pricing.LongContextOutputMultiplier, 1e-12)
			require.False(t, pricing.SupportsCacheBreakdown)
		})
	}
}

// TestGetModelPricing_GrokOfficialFamilyCards reads three explicitly
// registered fixture model IDs. Each must return its own supplied text/cache
// buckets and threshold, without relying on substring or family fallback
// selection.
func TestGetModelPricing_GrokOfficialFamilyCards(t *testing.T) {
	svc := newTestCalculator()
	for _, tc := range []struct {
		model  string // requested registered fixture model
		input, // fixture input USD per token
		cached, // fixture cached-input USD per token
		output float64 // fixture output USD per token
	}{
		{"grok-4.3", 1.25e-6, 0.2e-6, 2.5e-6},
		{"grok-4.20-0309-reasoning", 1.25e-6, 0.2e-6, 2.5e-6},
		{"grok-build-0.1", 1e-6, 0.2e-6, 2e-6},
	} {
		p, err := svc.GetModelPricing(tc.model)
		require.NoError(t, err, tc.model)
		// Allow binary representation residue in these fixture rate comparisons.
		require.InDelta(t, tc.input, p.InputPricePerToken, 1e-12)
		require.InDelta(t, tc.cached, p.CacheReadPricePerToken, 1e-12)
		require.InDelta(t, tc.output, p.OutputPricePerToken, 1e-12)
		require.Equal(t, 200000, p.LongContextInputThreshold)
	}
}

// TestCalculateCostUnified_ConfigLongContextToggleUsesPresetLadder prices
// fixed above-threshold Grok fixture usage before and after enabling built-in
// tiers. Only enabled calculation must mark surcharge and double the checked
// text components.
func TestCalculateCostUnified_ConfigLongContextToggleUsesPresetLadder(t *testing.T) {
	svc := newTestCalculator()
	settings := billingpricing.DefaultBillingSettings()
	settings.LongContextPricingEnabled = false
	resolver, source := settingsResolver(svc, settings, nil)
	tokens := billingpricing.UsageTokens{InputTokens: 250000, OutputTokens: 1000}

	disabled, err := svc.CalculateCostUnified(billing.CostInput{
		Model: "grok-4.5", GroupID: testkit.GroupID(), Tokens: tokens, RateMultiplier: 1, Resolver: resolver,
	})
	require.NoError(t, err)

	// Flip only the supplied built-in tier setting while retaining usage and
	// catalog amounts. The repeated calculation therefore isolates policy
	// enablement from model-rate changes.
	source.settings.LongContextPricingEnabled = true
	enabled, err := svc.CalculateCostUnified(billing.CostInput{
		Model: "grok-4.5", GroupID: testkit.GroupID(), Tokens: tokens, RateMultiplier: 1, Resolver: resolver,
	})
	require.NoError(t, err)

	require.False(t, disabled.LongContextBillingApplied)
	require.True(t, enabled.LongContextBillingApplied)
	// Allow float arithmetic residue in these calculated component amounts.
	require.InDelta(t, disabled.InputCost*2, enabled.InputCost, 1e-12)
	require.InDelta(t, disabled.OutputCost*2, enabled.OutputCost, 1e-12)
}

// TestGetModelPricing_UnknownGrokTextFallsBackToGrok46 establishes one
// registered model, then queries absent text and media spellings. Those
// requests must stay unavailable, while the separately registered build model
// retains its own input amount.
func TestGetModelPricing_UnknownGrokTextFallsBackToGrok46(t *testing.T) {
	svc := newTestCalculator()
	_, err := svc.GetModelPricing("grok-4.6")
	require.NoError(t, err)

	for _, model := range []string{"grok-5", "grok-5-latest", "x-ai/grok-7", "grok-4.7-beta", "grok-2-vision-1212"} {
		pricing, err := svc.GetModelPricing(model)
		require.ErrorIs(t, err, billingpricing.ErrModelPricingUnavailable, model)
		require.Nil(t, pricing)
	}

	for _, model := range []string{
		"grok-2-image-1212",
		"grok-2-audio",
		"grok-5-video",
		"x-ai/grok-6-image",
		"grok-imagine-image-3.0",
		"grok-imagine-video-2",
		"grok-voice-latest",
		"grok-web-search",
		"grok-x-search",
		"grok-speech-1",
	} {
		_, err := svc.GetModelPricing(model)
		require.Error(t, err, "non-text grok family %s must not inherit grok-4.5 token rates", model)
		require.ErrorIs(t, err, billingpricing.ErrModelPricingUnavailable)
	}

	// Read the separately registered build entry after missing-name results. Its
	// input amount comes from that full-name fixture, not a family price selected
	// for the preceding requests.
	build, err := svc.GetModelPricing("grok-build-0.1")
	require.NoError(t, err)
	// Allow binary representation residue in these fixture rate comparisons.
	require.InDelta(t, 1e-6, build.InputPricePerToken, 1e-12)
}

// TestGetModelPricing_GrokCatalogFallbacks iterates explicit fixture records
// and listed absent aliases in two groups. Registered names must return each
// group's supplied bucket rates, while selected unregistered full spellings
// must return nil and pricing unavailable.
func TestGetModelPricing_GrokCatalogFallbacks(t *testing.T) {
	svc := newTestCalculator()

	tests := []struct {
		name      string   // fixture model group label
		models    []string // requested full model spellings
		input     float64  // fixture input USD per token
		cacheRead float64  // fixture cached-input USD per token
		output    float64  // fixture output USD per token
	}{
		{
			name: "Grok 4.3 family",
			models: []string{
				"grok-4.3",
				"grok-4.20-0309-reasoning",
				"grok-4.20-0309-non-reasoning",
				"grok-4.20-multi-agent-0309",
				"grok-4.20-reasoning",
				"grok-4.20-non-reasoning",
			},
			input:     1.25e-6,
			cacheRead: 0.2e-6,
			output:    2.5e-6,
		},
		{
			name: "Grok coding and Composer family",
			models: []string{
				"grok-build",
				"grok-build-0.1",
				"grok-composer",
				"grok-composer-2.5-fast",
				"composer-2.5",
			},
			input:     1e-6,
			cacheRead: 0.2e-6,
			output:    2e-6,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			for _, model := range tt.models {
				pricing, err := svc.GetModelPricing(model)
				if model == "grok-4.5-latest" || model == "grok-4.6-latest" || model == "grok-4.20-reasoning" || model == "grok-4.20-non-reasoning" || model == "grok-build" || model == "grok-composer" || model == "composer-2.5" || model == "doubao-embedding-vision-251215" {
					require.ErrorIs(t, err, billingpricing.ErrModelPricingUnavailable)
					require.Nil(t, pricing)
					continue
				}
				require.NoError(t, err, "model %s", model)
				// Allow binary representation residue in these fixture rate comparisons.
				require.InDelta(t, tt.input, pricing.InputPricePerToken, 1e-12, "model %s input", model)
				require.InDelta(t, tt.cacheRead, pricing.CacheReadPricePerToken, 1e-12, "model %s cached input", model)
				require.InDelta(t, tt.output, pricing.OutputPricePerToken, 1e-12, "model %s output", model)
			}
		})
	}
}

// TestCalculateCost_SupportsCacheBreakdown supplies supported five-minute and
// hourly cache-write counts with distinct prices. The combined cache-creation
// component must equal both detail charges, keeping their TTL rates separately
// billable.
func TestCalculateCost_SupportsCacheBreakdown(t *testing.T) {
	svc := newCalculatorWithPrices(nil, map[string]*billingpricing.ModelPricing{
		"claude-sonnet-4": {
			InputPricePerToken:     3e-6,
			OutputPricePerToken:    15e-6,
			SupportsCacheBreakdown: true,
			CacheCreation5mPrice:   4e-6,
			CacheCreation1hPrice:   5e-6,
		},
	})

	tokens := billingpricing.UsageTokens{
		InputTokens:           1000,
		OutputTokens:          500,
		CacheCreation5mTokens: 100000,
		CacheCreation1hTokens: 50000,
	}
	cost, err := svc.CalculateCost("claude-sonnet-4", tokens, 1.0)
	require.NoError(t, err)

	expected5m := float64(tokens.CacheCreation5mTokens) * 4e-6
	expected1h := float64(tokens.CacheCreation1hTokens) * 5e-6
	// Allow float arithmetic residue in these calculated component amounts.
	require.InDelta(t, expected5m+expected1h, cost.CacheCreationCost, 1e-10)
}

// TestComputeCacheCreationCost_CapsContradictoryBreakdownAtAggregate supplies
// two full-size cache details against one positive aggregate. Equal unit TTL
// rates must produce a billed count equivalent no larger than the asserted
// aggregate.
func TestComputeCacheCreationCost_CapsContradictoryBreakdownAtAggregate(t *testing.T) {
	pricing := &billingpricing.ModelPricing{
		SupportsCacheBreakdown: true,
		CacheCreation5mPrice:   1,
		CacheCreation1hPrice:   1,
	}

	tokens := billingpricing.UsageTokens{
		CacheCreationTokens:   463184,
		CacheCreation5mTokens: 463184,
		CacheCreation1hTokens: 463184,
	}

	cost := billingpricing.ComputeCacheCreationCost(pricing, tokens, 0, 1)
	require.Equal(t, float64(tokens.CacheCreationTokens), cost,
		"billed cache-creation token equivalent must not exceed the positive aggregate")
}

// TestSettleCacheWrite_BillingSafetyInvariant verifies SettleCacheWrite across
// exact, untagged, partial, missing-total, negative, integer-boundary, and
// overreported subdivisions. Every case must conserve the authoritative total,
// assign every billed token to a concrete TTL bucket, and flag anomalies.
func TestSettleCacheWrite_BillingSafetyInvariant(t *testing.T) {
	splitRates := billingpricing.CacheWriteRates{
		Default: 2.5e-6,
		TTL5m:   new(2.5e-6),
		TTL1h:   new(4.0e-6),
	}
	equalRates := billingpricing.CacheWriteRates{
		Default: 2.5e-6,
		TTL5m:   new(2.5e-6),
		TTL1h:   new(2.5e-6),
	}

	tests := []struct {
		name        string                         // cache settlement scenario label
		rates       billingpricing.CacheWriteRates // resolved write rates under test
		tokens      billingpricing.UsageTokens     // supplied aggregate and TTL counts
		want5m      int                            // expected billed five-minute tokens
		want1h      int                            // expected billed hourly tokens
		wantCost    float64                        // expected USD cache-creation cost
		wantAnomaly string                         // expected anomaly kind or empty
	}{
		{
			name:     "untagged writes use default 5m bucket",
			rates:    splitRates,
			tokens:   billingpricing.UsageTokens{CacheCreationTokens: 311},
			want5m:   311,
			want1h:   0,
			wantCost: 311 * 2.5e-6,
		},
		{
			name:     "exact split billed per bucket",
			rates:    splitRates,
			tokens:   billingpricing.UsageTokens{CacheCreationTokens: 311, CacheCreation5mTokens: 211, CacheCreation1hTokens: 100},
			want5m:   211,
			want1h:   100,
			wantCost: 211*2.5e-6 + 100*4.0e-6,
		},
		{
			name:        "partial 5m detail bills remainder at default 5m",
			rates:       splitRates,
			tokens:      billingpricing.UsageTokens{CacheCreationTokens: 311, CacheCreation5mTokens: 211},
			want5m:      311,
			want1h:      0,
			wantCost:    311 * 2.5e-6,
			wantAnomaly: billingpricing.CacheWriteAnomalyPartialBreakdown,
		},
		{
			name:        "partial 1h detail retains 1h and bills remainder at default 5m",
			rates:       splitRates,
			tokens:      billingpricing.UsageTokens{CacheCreationTokens: 311, CacheCreation1hTokens: 211},
			want5m:      100,
			want1h:      211,
			wantCost:    100*2.5e-6 + 211*4.0e-6,
			wantAnomaly: billingpricing.CacheWriteAnomalyPartialBreakdown,
		},
		{
			name:        "overreported details cap at aggregate and prioritize pricier 1h",
			rates:       splitRates,
			tokens:      billingpricing.UsageTokens{CacheCreationTokens: 10, CacheCreation5mTokens: 10, CacheCreation1hTokens: 10},
			want5m:      0,
			want1h:      10,
			wantCost:    10 * 4.0e-6,
			wantAnomaly: billingpricing.CacheWriteAnomalyExceedsTotal,
		},
		{
			name:        "overreported details with equal rates prefer default 5m",
			rates:       equalRates,
			tokens:      billingpricing.UsageTokens{CacheCreationTokens: 10, CacheCreation5mTokens: 10, CacheCreation1hTokens: 10},
			want5m:      10,
			want1h:      0,
			wantCost:    10 * 2.5e-6,
			wantAnomaly: billingpricing.CacheWriteAnomalyExceedsTotal,
		},
		{
			name:        "negative detail clamped and remainder billed at default 5m",
			rates:       splitRates,
			tokens:      billingpricing.UsageTokens{CacheCreationTokens: 100, CacheCreation5mTokens: -50, CacheCreation1hTokens: 60},
			want5m:      40,
			want1h:      60,
			wantCost:    40*2.5e-6 + 60*4.0e-6,
			wantAnomaly: billingpricing.CacheWriteAnomalyExceedsTotal,
		},
		{
			name:        "negative detail cannot hide oversized positive detail",
			rates:       splitRates,
			tokens:      billingpricing.UsageTokens{CacheCreationTokens: 100, CacheCreation5mTokens: -50, CacheCreation1hTokens: 150},
			want5m:      0,
			want1h:      100,
			wantCost:    100 * 4.0e-6,
			wantAnomaly: billingpricing.CacheWriteAnomalyExceedsTotal,
		},
		{
			name:        "integer boundary details capped without overflow",
			rates:       splitRates,
			tokens:      billingpricing.UsageTokens{CacheCreationTokens: 100, CacheCreation5mTokens: int(^uint(0) >> 1), CacheCreation1hTokens: int(^uint(0) >> 1)},
			want5m:      0,
			want1h:      100,
			wantCost:    100 * 4.0e-6,
			wantAnomaly: billingpricing.CacheWriteAnomalyExceedsTotal,
		},
		{
			name:        "integer boundary aggregate avoids overflow",
			rates:       splitRates,
			tokens:      billingpricing.UsageTokens{CacheCreationTokens: int(^uint(0) >> 1), CacheCreation5mTokens: int(^uint(0) >> 1), CacheCreation1hTokens: 1},
			want5m:      int(^uint(0)>>1) - 1,
			want1h:      1,
			wantCost:    float64(int(^uint(0)>>1)-1)*2.5e-6 + 4.0e-6,
			wantAnomaly: billingpricing.CacheWriteAnomalyExceedsTotal,
		},
		{
			name:        "zero aggregate promotes positive details",
			rates:       splitRates,
			tokens:      billingpricing.UsageTokens{CacheCreation5mTokens: 90, CacheCreation1hTokens: 60},
			want5m:      90,
			want1h:      60,
			wantCost:    90*2.5e-6 + 60*4.0e-6,
			wantAnomaly: billingpricing.CacheWriteAnomalyMissingTotal,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := billingpricing.SettleCacheWrite(tt.rates, tt.tokens, billingpricing.CacheWriteFamilyClaude, 1)
			require.Equal(t, tt.want5m, got.Billed5m)
			require.Equal(t, tt.want1h, got.Billed1h)
			require.Equal(t, tt.want5m+tt.want1h, got.BilledTotal)
			require.InDelta(t, tt.wantCost, got.Cost, 1e-12)
			if tt.wantAnomaly == "" {
				require.Nil(t, got.Anomaly)
			} else {
				require.NotNil(t, got.Anomaly)
				require.Equal(t, tt.wantAnomaly, got.Anomaly.Kind)
			}
		})
	}
}

// TestGetModelPricingWithConfig_CacheWrite5mPricePreserves1h verifies that an
// independent five-minute card override updates 5m and fallback rates without
// overwriting the catalog hourly cache-write price.
func TestGetModelPricingWithConfig_CacheWrite5mPricePreserves1h(t *testing.T) {
	svc := newTestCalculator()

	pricing, err := svc.GetModelPricingWithConfig("claude-fable-5-1", &routing.ModelPricingEntry{
		CacheWrite5mPrice: testPtrFloat64(7e-6),
	})
	require.NoError(t, err)
	require.True(t, pricing.SupportsCacheBreakdown)
	require.True(t, pricing.CacheCreation5mPriceExplicit)
	require.InDelta(t, 7e-6, pricing.CacheCreationPricePerToken, 1e-12)
	require.InDelta(t, 7e-6, pricing.CacheCreation5mPrice, 1e-12)
	require.InDelta(t, 20e-6, pricing.CacheCreation1hPrice, 1e-12)
}

// TestComputeCacheCreationCost_PreservesZeroDetailFallback supplies positive
// aggregate cache writes with absent or negative detail counts. Each case must
// charge the aggregate at the fixture's five-minute rate after unusable
// details normalize away.
func TestComputeCacheCreationCost_PreservesZeroDetailFallback(t *testing.T) {
	pricing := &billingpricing.ModelPricing{
		SupportsCacheBreakdown: true,
		CacheCreation5mPrice:   4e-6,
		CacheCreation1hPrice:   5e-6,
	}

	tests := []struct {
		name   string                     // unusable cache detail case
		tokens billingpricing.UsageTokens // supplied aggregate and TTL counts
	}{
		{name: "zero details", tokens: billingpricing.UsageTokens{CacheCreationTokens: 100}},
		{name: "one negative detail", tokens: billingpricing.UsageTokens{CacheCreationTokens: 100, CacheCreation5mTokens: -25}},
		{name: "both negative details", tokens: billingpricing.UsageTokens{CacheCreationTokens: 100, CacheCreation5mTokens: -25, CacheCreation1hTokens: -75}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cost := billingpricing.ComputeCacheCreationCost(pricing, tt.tokens, 0, 1)
			// Allow float arithmetic residue in these calculated component amounts.
			require.InDelta(t, 100*4e-6, cost, 1e-12)
		})
	}
}

// TestCalculateCost_LargeTokenCount supplies one million input and output
// tokens at the fixture's ordinary rates. Components must reach their expected
// dollar amounts and the total must remain finite.
func TestCalculateCost_LargeTokenCount(t *testing.T) {
	svc := newTestCalculator()

	tokens := billingpricing.UsageTokens{
		InputTokens:  1_000_000,
		OutputTokens: 1_000_000,
	}
	cost, err := svc.CalculateCost("claude-sonnet-4", tokens, 1.0)
	require.NoError(t, err)

	// Input: 1M * 3e-6 = $3, Output: 1M * 15e-6 = $15
	// Allow float arithmetic residue in these calculated component amounts.
	require.InDelta(t, 3.0, cost.InputCost, 1e-6)
	require.InDelta(t, 15.0, cost.OutputCost, 1e-6)
	require.False(t, math.IsNaN(cost.TotalCost))
	require.False(t, math.IsInf(cost.TotalCost, 0))
}

// TestCalculateCostWithServiceTier_PricingConfigFlexMultiplier calculates
// identical usage with a configured quarter-price Flex factor. Flex total must
// equal one quarter of the standard total selected after those manual token
// rates.
func TestCalculateCostWithServiceTier_PricingConfigFlexMultiplier(t *testing.T) {
	svc := newTestCalculator()
	configPricing := &routing.ModelPricingEntry{
		FlexMultiplier: testPtrFloat64(0.25),
		InputPrice:     testPtrFloat64(10e-6),
		OutputPrice:    testPtrFloat64(20e-6),
	}
	tokens := billingpricing.UsageTokens{InputTokens: 100, OutputTokens: 50}
	standard, err := svc.CalculateCostInternal("claude-sonnet-4", tokens, 1, "", configPricing)
	require.NoError(t, err)
	flex, err := svc.CalculateCostInternal("claude-sonnet-4", tokens, 1, "flex", configPricing)
	require.NoError(t, err)
	// Allow float multiplication residue in these scaled component comparisons.
	require.InDelta(t, standard.TotalCost*0.25, flex.TotalCost, 1e-12)
}

// TestCalculateCostWithServiceTier_OpenAIPriorityUsesPriorityPricing compares
// standard and priority usage for the registered Codex fixture. Input, output,
// cached input and total must double according to its supplied priority
// buckets.
func TestCalculateCostWithServiceTier_OpenAIPriorityUsesPriorityPricing(t *testing.T) {
	svc := newTestCalculator()
	tokens := billingpricing.UsageTokens{InputTokens: 100, OutputTokens: 50, CacheReadTokens: 20}

	baseCost, err := svc.CalculateCost("gpt-5.3-codex", tokens, 1.0)
	require.NoError(t, err)

	priorityCost, err := svc.CalculateCostWithServiceTier("gpt-5.3-codex", tokens, 1.0, "priority")
	require.NoError(t, err)

	// Allow float multiplication residue in these scaled component comparisons.
	require.InDelta(t, baseCost.InputCost*2, priorityCost.InputCost, 1e-10)
	require.InDelta(t, baseCost.OutputCost*2, priorityCost.OutputCost, 1e-10)
	require.InDelta(t, baseCost.CacheReadCost*2, priorityCost.CacheReadCost, 1e-10)
	require.InDelta(t, baseCost.TotalCost*2, priorityCost.TotalCost, 1e-10)
}

// TestCalculateCostWithServiceTier_FlexAppliesHalfMultiplier compares standard
// and Flex usage for GPT-5.4 fixture pricing. All checked text/cache
// components and total must halve under the fixture's declared factor.
func TestCalculateCostWithServiceTier_FlexAppliesHalfMultiplier(t *testing.T) {
	svc := newTestCalculator()
	tokens := billingpricing.UsageTokens{InputTokens: 100, OutputTokens: 50, CacheCreationTokens: 40, CacheReadTokens: 20}

	baseCost, err := svc.CalculateCost("gpt-5.4", tokens, 1.0)
	require.NoError(t, err)

	flexCost, err := svc.CalculateCostWithServiceTier("gpt-5.4", tokens, 1.0, "flex")
	require.NoError(t, err)

	// Allow float multiplication residue in these scaled component comparisons.
	require.InDelta(t, baseCost.InputCost*0.5, flexCost.InputCost, 1e-10)
	require.InDelta(t, baseCost.OutputCost*0.5, flexCost.OutputCost, 1e-10)
	require.InDelta(t, baseCost.CacheCreationCost*0.5, flexCost.CacheCreationCost, 1e-10)
	require.InDelta(t, baseCost.CacheReadCost*0.5, flexCost.CacheReadCost, 1e-10)
	require.InDelta(t, baseCost.TotalCost*0.5, flexCost.TotalCost, 1e-10)
}

// TestCalculateCostWithServiceTier_Gpt54MiniPriorityFallsBackToTierMultiplier
// supplies ordinary, cache-write and cached-read usage to the mini fixture.
// Priority components and total must double through its configured
// service-tier factor.
func TestCalculateCostWithServiceTier_Gpt54MiniPriorityFallsBackToTierMultiplier(t *testing.T) {
	svc := newTestCalculator()
	tokens := billingpricing.UsageTokens{InputTokens: 120, OutputTokens: 30, CacheCreationTokens: 12, CacheReadTokens: 8}

	baseCost, err := svc.CalculateCost("gpt-5.4-mini", tokens, 1.0)
	require.NoError(t, err)

	priorityCost, err := svc.CalculateCostWithServiceTier("gpt-5.4-mini", tokens, 1.0, "priority")
	require.NoError(t, err)

	// Allow float multiplication residue in these scaled component comparisons.
	require.InDelta(t, baseCost.InputCost*2, priorityCost.InputCost, 1e-10)
	require.InDelta(t, baseCost.OutputCost*2, priorityCost.OutputCost, 1e-10)
	require.InDelta(t, baseCost.CacheCreationCost*2, priorityCost.CacheCreationCost, 1e-10)
	require.InDelta(t, baseCost.CacheReadCost*2, priorityCost.CacheReadCost, 1e-10)
	require.InDelta(t, baseCost.TotalCost*2, priorityCost.TotalCost, 1e-10)
}

// TestCalculateCostWithServiceTier_Gpt54NanoFlexAppliesHalfMultiplier compares
// standard and Flex charges for the nano fixture. Each text/cache component
// and total must receive the supplied half-price factor.
func TestCalculateCostWithServiceTier_Gpt54NanoFlexAppliesHalfMultiplier(t *testing.T) {
	svc := newTestCalculator()
	tokens := billingpricing.UsageTokens{InputTokens: 100, OutputTokens: 50, CacheCreationTokens: 40, CacheReadTokens: 20}

	baseCost, err := svc.CalculateCost("gpt-5.4-nano", tokens, 1.0)
	require.NoError(t, err)

	flexCost, err := svc.CalculateCostWithServiceTier("gpt-5.4-nano", tokens, 1.0, "flex")
	require.NoError(t, err)

	// Allow float multiplication residue in these scaled component comparisons.
	require.InDelta(t, baseCost.InputCost*0.5, flexCost.InputCost, 1e-10)
	require.InDelta(t, baseCost.OutputCost*0.5, flexCost.OutputCost, 1e-10)
	require.InDelta(t, baseCost.CacheCreationCost*0.5, flexCost.CacheCreationCost, 1e-10)
	require.InDelta(t, baseCost.CacheReadCost*0.5, flexCost.CacheReadCost, 1e-10)
	require.InDelta(t, baseCost.TotalCost*0.5, flexCost.TotalCost, 1e-10)
}

// TestCalculateCostWithServiceTier_PriorityFallsBackToTierMultiplierWithoutExplicitPriorityPrice
// compares Claude fixture charges lacking explicit priority bucket amounts.
// Its declared Fast factor must double every checked text/cache component and
// total.
func TestCalculateCostWithServiceTier_PriorityFallsBackToTierMultiplierWithoutExplicitPriorityPrice(t *testing.T) {
	svc := newTestCalculator()
	tokens := billingpricing.UsageTokens{InputTokens: 120, OutputTokens: 30, CacheCreationTokens: 12, CacheReadTokens: 8}

	baseCost, err := svc.CalculateCost("claude-sonnet-4", tokens, 1.0)
	require.NoError(t, err)

	priorityCost, err := svc.CalculateCostWithServiceTier("claude-sonnet-4", tokens, 1.0, "priority")
	require.NoError(t, err)

	// Allow float multiplication residue in these scaled component comparisons.
	require.InDelta(t, baseCost.InputCost*2, priorityCost.InputCost, 1e-10)
	require.InDelta(t, baseCost.OutputCost*2, priorityCost.OutputCost, 1e-10)
	require.InDelta(t, baseCost.CacheCreationCost*2, priorityCost.CacheCreationCost, 1e-10)
	require.InDelta(t, baseCost.CacheReadCost*2, priorityCost.CacheReadCost, 1e-10)
	require.InDelta(t, baseCost.TotalCost*2, priorityCost.TotalCost, 1e-10)
}

// TestCalculateCostWithServiceTier_ClaudeOpus48FastUsesDoublePricing first
// checks the registered Opus fixture's ordinary and TTL rates. Priority
// calculation must double text/cache components and total through the
// fixture's declared service-tier support.
func TestCalculateCostWithServiceTier_ClaudeOpus48FastUsesDoublePricing(t *testing.T) {
	svc := newTestCalculator()
	tokens := billingpricing.UsageTokens{
		InputTokens:           100,
		OutputTokens:          50,
		CacheCreation5mTokens: 40,
		CacheCreation1hTokens: 20,
		CacheReadTokens:       10,
	}

	pricing, err := svc.GetModelPricing("claude-opus-4-8")
	require.NoError(t, err)
	// Allow float multiplication residue in these scaled component comparisons.
	require.InDelta(t, 5e-6, pricing.InputPricePerToken, 1e-12)
	require.InDelta(t, 25e-6, pricing.OutputPricePerToken, 1e-12)
	require.InDelta(t, 6.25e-6, pricing.CacheCreation5mPrice, 1e-12)
	require.InDelta(t, 10e-6, pricing.CacheCreation1hPrice, 1e-12)
	require.InDelta(t, 0.5e-6, pricing.CacheReadPricePerToken, 1e-12)
	require.True(t, pricing.SupportsCacheBreakdown)
	require.True(t, pricing.SupportsServiceTier)

	baseCost, err := svc.CalculateCost("claude-opus-4-8", tokens, 1.0)
	require.NoError(t, err)

	fastCost, err := svc.CalculateCostWithServiceTier("claude-opus-4-8", tokens, 1.0, "priority")
	require.NoError(t, err)

	// The fixture's Claude Opus 4.8 Fast rates are twice its standard rates.
	require.InDelta(t, baseCost.InputCost*2, fastCost.InputCost, 1e-10)
	require.InDelta(t, baseCost.OutputCost*2, fastCost.OutputCost, 1e-10)
	require.InDelta(t, baseCost.CacheCreationCost*2, fastCost.CacheCreationCost, 1e-10)
	require.InDelta(t, baseCost.CacheReadCost*2, fastCost.CacheReadCost, 1e-10)
	require.InDelta(t, baseCost.TotalCost*2, fastCost.TotalCost, 1e-10)
}

// TestBillingServiceGetModelPricing_UsesDynamicPriorityFields supplies one raw
// catalog record with priority, cache and long-context metadata. Lookup must
// transfer the checked ordinary/priority amounts, threshold and factors into
// calculation pricing.
func TestBillingServiceGetModelPricing_UsesDynamicPriorityFields(t *testing.T) {
	pricingSvc := newCatalogFixture(catalogFixture{
		pricingData: map[string]*billingpricing.CatalogModelPricing{
			"gpt-5.4": {
				InputCostPerToken:               2.5e-6,
				InputCostPerTokenPriority:       5e-6,
				OutputCostPerToken:              15e-6,
				OutputCostPerTokenPriority:      30e-6,
				CacheCreationInputTokenCost:     2.5e-6,
				CacheReadInputTokenCost:         0.25e-6,
				CacheReadInputTokenCostPriority: 0.5e-6,
				LongContextInputTokenThreshold:  272000,
				LongContextInputCostMultiplier:  2.0,
				LongContextOutputCostMultiplier: 1.5,
			},
		},
	})
	svc := newCalculator(pricingSvc)

	pricing, err := svc.GetModelPricing("gpt-5.4")
	require.NoError(t, err)
	// Allow binary representation residue in these fixture rate comparisons.
	require.InDelta(t, 2.5e-6, pricing.InputPricePerToken, 1e-12)
	require.InDelta(t, 5e-6, pricing.InputPricePerTokenPriority, 1e-12)
	require.InDelta(t, 15e-6, pricing.OutputPricePerToken, 1e-12)
	require.InDelta(t, 30e-6, pricing.OutputPricePerTokenPriority, 1e-12)
	require.InDelta(t, 0.25e-6, pricing.CacheReadPricePerToken, 1e-12)
	require.InDelta(t, 0.5e-6, pricing.CacheReadPricePerTokenPriority, 1e-12)
	require.Equal(t, 272000, pricing.LongContextInputThreshold)
	require.InDelta(t, 2.0, pricing.LongContextInputMultiplier, 1e-12)
	require.InDelta(t, 1.5, pricing.LongContextOutputMultiplier, 1e-12)
}

// TestBillingServiceGetModelPricing_OpenAIFallbackGpt52Variants reads the
// registered GPT-5.2 fixture and an absent Codex full name. The first must
// expose its ordinary/priority input rates, while the second returns nil and
// pricing unavailable.
func TestBillingServiceGetModelPricing_OpenAIFallbackGpt52Variants(t *testing.T) {
	svc := newTestCalculator()

	gpt52, err := svc.GetModelPricing("gpt-5.2")
	require.NoError(t, err)
	require.NotNil(t, gpt52)
	// Allow binary representation residue in these fixture rate comparisons.
	require.InDelta(t, 1.75e-6, gpt52.InputPricePerToken, 1e-12)
	require.InDelta(t, 3.5e-6, gpt52.InputPricePerTokenPriority, 1e-12)

	value, err := svc.GetModelPricing("gpt-5.2-codex")
	require.ErrorIs(t, err, billingpricing.ErrModelPricingUnavailable)
	require.Nil(t, value)
}

// TestCalculateCostWithServiceTier_MissingPriorityDoesNotInventMultiplier
// supplies a custom catalog model with ordinary buckets and no priority rules.
// Standard and priority calls must return equal checked components and totals.
func TestCalculateCostWithServiceTier_MissingPriorityDoesNotInventMultiplier(t *testing.T) {
	svc := newCalculator(newCatalogFixture(catalogFixture{
		pricingData: map[string]*billingpricing.CatalogModelPricing{
			"custom-no-priority": {
				InputCostPerToken:           1e-6,
				OutputCostPerToken:          2e-6,
				CacheCreationInputTokenCost: 0.5e-6,
				CacheReadInputTokenCost:     0.25e-6,
			},
		},
	}))
	tokens := billingpricing.UsageTokens{InputTokens: 100, OutputTokens: 50, CacheCreationTokens: 40, CacheReadTokens: 20}

	baseCost, err := svc.CalculateCost("custom-no-priority", tokens, 1.0)
	require.NoError(t, err)

	priorityCost, err := svc.CalculateCostWithServiceTier("custom-no-priority", tokens, 1.0, "priority")
	require.NoError(t, err)

	// Allow float multiplication residue in these scaled component comparisons.
	require.InDelta(t, baseCost.InputCost, priorityCost.InputCost, 1e-10)
	require.InDelta(t, baseCost.OutputCost, priorityCost.OutputCost, 1e-10)
	require.InDelta(t, baseCost.CacheCreationCost, priorityCost.CacheCreationCost, 1e-10)
	require.InDelta(t, baseCost.CacheReadCost, priorityCost.CacheReadCost, 1e-10)
	require.InDelta(t, baseCost.TotalCost, priorityCost.TotalCost, 1e-10)
}

// TestGetModelPricing_OpenAIGpt52FallbacksExposePriorityPrices checks
// ordinary/priority text amounts on the registered GPT-5.2 fixture. An absent
// Codex spelling must return nil with pricing unavailable rather than inherit
// those rates.
func TestGetModelPricing_OpenAIGpt52FallbacksExposePriorityPrices(t *testing.T) {
	svc := newTestCalculator()

	gpt52, err := svc.GetModelPricing("gpt-5.2")
	require.NoError(t, err)
	// Allow binary representation residue in these fixture rate comparisons.
	require.InDelta(t, 1.75e-6, gpt52.InputPricePerToken, 1e-12)
	require.InDelta(t, 3.5e-6, gpt52.InputPricePerTokenPriority, 1e-12)
	require.InDelta(t, 14e-6, gpt52.OutputPricePerToken, 1e-12)
	require.InDelta(t, 28e-6, gpt52.OutputPricePerTokenPriority, 1e-12)

	value, err := svc.GetModelPricing("gpt-5.2-codex")
	require.ErrorIs(t, err, billingpricing.ErrModelPricingUnavailable)
	require.Nil(t, value)
}

// TestGetModelPricing_MapsDynamicPriorityFieldsIntoBillingPricing supplies raw
// text, priority, cache-TTL and threshold fields for a custom model. Lookup
// must transfer the checked amounts, cache-breakdown support and long-context
// factors.
func TestGetModelPricing_MapsDynamicPriorityFieldsIntoBillingPricing(t *testing.T) {
	svc := newCalculator(newCatalogFixture(catalogFixture{
		pricingData: map[string]*billingpricing.CatalogModelPricing{
			"dynamic-tier-model": {
				InputCostPerToken:                   1e-6,
				InputCostPerTokenPriority:           2e-6,
				OutputCostPerToken:                  3e-6,
				OutputCostPerTokenPriority:          6e-6,
				CacheCreationInputTokenCost:         4e-6,
				CacheCreationInputTokenCostAbove1hr: 5e-6,
				CacheReadInputTokenCost:             7e-7,
				CacheReadInputTokenCostPriority:     8e-7,
				LongContextInputTokenThreshold:      999,
				LongContextInputCostMultiplier:      1.5,
				LongContextOutputCostMultiplier:     1.25,
			},
		},
	}))

	pricing, err := svc.GetModelPricing("dynamic-tier-model")
	require.NoError(t, err)
	// Allow binary representation residue in these fixture rate comparisons.
	require.InDelta(t, 1e-6, pricing.InputPricePerToken, 1e-12)
	require.InDelta(t, 2e-6, pricing.InputPricePerTokenPriority, 1e-12)
	require.InDelta(t, 3e-6, pricing.OutputPricePerToken, 1e-12)
	require.InDelta(t, 6e-6, pricing.OutputPricePerTokenPriority, 1e-12)
	require.InDelta(t, 4e-6, pricing.CacheCreation5mPrice, 1e-12)
	require.InDelta(t, 5e-6, pricing.CacheCreation1hPrice, 1e-12)
	require.True(t, pricing.SupportsCacheBreakdown)
	require.InDelta(t, 7e-7, pricing.CacheReadPricePerToken, 1e-12)
	require.InDelta(t, 8e-7, pricing.CacheReadPricePerTokenPriority, 1e-12)
	require.Equal(t, 999, pricing.LongContextInputThreshold)
	require.InDelta(t, 1.5, pricing.LongContextInputMultiplier, 1e-12)
	require.InDelta(t, 1.25, pricing.LongContextOutputMultiplier, 1e-12)
}

// TestGetModelPricingWithConfig_NilConfigPricing_ReturnsOriginal compares a
// nil-card lookup with an ordinary fixture lookup. The four checked
// token/cache bucket rates must remain equal, without asserting pointer
// identity or nested ownership.
func TestGetModelPricingWithConfig_NilConfigPricing_ReturnsOriginal(t *testing.T) {
	svc := newTestCalculator()

	pricing, err := svc.GetModelPricingWithConfig("claude-sonnet-4", nil)
	require.NoError(t, err)
	require.NotNil(t, pricing)

	original, err := svc.GetModelPricing("claude-sonnet-4")
	require.NoError(t, err)
	// Allow binary representation residue in these fixture rate comparisons.
	require.InDelta(t, original.InputPricePerToken, pricing.InputPricePerToken, 1e-12)
	require.InDelta(t, original.OutputPricePerToken, pricing.OutputPricePerToken, 1e-12)
	require.InDelta(t, original.CacheCreationPricePerToken, pricing.CacheCreationPricePerToken, 1e-12)
	require.InDelta(t, original.CacheReadPricePerToken, pricing.CacheReadPricePerToken, 1e-12)
}

// TestGetModelPricingWithConfig_OverrideInputPriceOnly supplies a manual input
// amount for a known Claude fixture. Input must change while output keeps its
// catalog value and missing priority input remains zero.
func TestGetModelPricingWithConfig_OverrideInputPriceOnly(t *testing.T) {
	svc := newTestCalculator()

	chPricing := &routing.ModelPricingEntry{
		InputPrice: testPtrFloat64(99e-6),
	}
	pricing, err := svc.GetModelPricingWithConfig("claude-sonnet-4", chPricing)
	require.NoError(t, err)

	// The input override applies to standard rates; this catalog model has no
	// priority price.
	// Allow binary representation residue in these fixture rate comparisons.
	require.InDelta(t, 99e-6, pricing.InputPricePerToken, 1e-12)
	require.Zero(t, pricing.InputPricePerTokenPriority)

	require.InDelta(t, 15e-6, pricing.OutputPricePerToken, 1e-12)
}

// TestGetModelPricingWithConfig_PriceMultiplierAppliesAfterOverrides supplies
// an input override plus a doubled price factor. Returned input and inherited
// output must both reflect scaling after their bucket selection.
func TestGetModelPricingWithConfig_PriceMultiplierAppliesAfterOverrides(t *testing.T) {
	svc := newTestCalculator()

	chPricing := &routing.ModelPricingEntry{
		PriceMultiplier: testPtrFloat64(2),
		InputPrice:      testPtrFloat64(10e-6),
	}
	pricing, err := svc.GetModelPricingWithConfig("claude-sonnet-4", chPricing)
	require.NoError(t, err)

	// The multiplier applies after merging the manual input rate and inherited
	// output rate.
	// Allow binary representation residue in these fixture rate comparisons.
	require.InDelta(t, 20e-6, pricing.InputPricePerToken, 1e-12)
	require.InDelta(t, 30e-6, pricing.OutputPricePerToken, 1e-12)
}

// TestGetModelPricingWithConfig_PreservesNativeTierRatio overrides GPT-5.4
// fixture text and cache amounts. Existing doubled priority ratios must remain
// on supplied buckets, while absent cache-write priority continues to be
// unset.
func TestGetModelPricingWithConfig_PreservesNativeTierRatio(t *testing.T) {
	svc := newTestCalculator()

	pricing, err := svc.GetModelPricingWithConfig("gpt-5.4", &routing.ModelPricingEntry{
		InputPrice:      testPtrFloat64(10e-6),
		OutputPrice:     testPtrFloat64(40e-6),
		CacheWritePrice: testPtrFloat64(8e-6),
		CacheReadPrice:  testPtrFloat64(1e-6),
	})
	require.NoError(t, err)

	// The GPT-5.4 fixture's priority rates are twice standard rates; its absent
	// cache-write priority price stays unset.
	// Allow binary representation residue in these fixture rate comparisons.
	require.InDelta(t, 20e-6, pricing.InputPricePerTokenPriority, 1e-12)
	require.InDelta(t, 80e-6, pricing.OutputPricePerTokenPriority, 1e-12)
	require.Zero(t, pricing.CacheCreationPricePerTokenPriority)
	require.InDelta(t, 2e-6, pricing.CacheReadPricePerTokenPriority, 1e-12)
}

// TestCalculateCostWithPricingConfigFastModeMultiplierUsesFinalStandardPrice
// combines bucket overrides, a price factor and a manual Fast factor. Every
// checked text/image/cache component and total must scale from the selected
// standard calculation by 1.5.
func TestCalculateCostWithPricingConfigFastModeMultiplierUsesFinalStandardPrice(t *testing.T) {
	svc := newTestCalculator()
	configPricing := &routing.ModelPricingEntry{
		PriceMultiplier:    testPtrFloat64(1.25),
		FastModeMultiplier: testPtrFloat64(1.5),
		InputPrice:         testPtrFloat64(10e-6),
		ImageInputPrice:    testPtrFloat64(12e-6),
		OutputPrice:        testPtrFloat64(20e-6),
		CacheWritePrice:    testPtrFloat64(4e-6),
		CacheReadPrice:     testPtrFloat64(2e-6),
		ImageOutputPrice:   testPtrFloat64(30e-6),
	}
	tokens := billingpricing.UsageTokens{
		InputTokens:         100,
		ImageInputTokens:    20,
		OutputTokens:        50,
		ImageOutputTokens:   10,
		CacheCreationTokens: 30,
		CacheReadTokens:     40,
	}

	standard, err := svc.CalculateCostInternal("gpt-5.4", tokens, 1, "", configPricing)
	require.NoError(t, err)
	fast, err := svc.CalculateCostInternal("gpt-5.4", tokens, 1, "priority", configPricing)
	require.NoError(t, err)

	// The configured multiplier overrides catalog priority rates for text,
	// image, and cache charges.
	// Allow float multiplication residue in these scaled component comparisons.
	require.InDelta(t, standard.InputCost*1.5, fast.InputCost, 1e-12)
	require.InDelta(t, standard.ImageInputCost*1.5, fast.ImageInputCost, 1e-12)
	require.InDelta(t, standard.OutputCost*1.5, fast.OutputCost, 1e-12)
	require.InDelta(t, standard.ImageOutputCost*1.5, fast.ImageOutputCost, 1e-12)
	require.InDelta(t, standard.CacheCreationCost*1.5, fast.CacheCreationCost, 1e-12)
	require.InDelta(t, standard.CacheReadCost*1.5, fast.CacheReadCost, 1e-12)
	require.InDelta(t, standard.TotalCost*1.5, fast.TotalCost, 1e-12)
}

// TestGetModelPricingWithConfig_DoesNotMutateFallbackPricing resolves a manual
// input override before querying the fixture model again. The later ordinary
// input/output rates must retain their base values, protecting the observed
// subsequent lookup.
func TestGetModelPricingWithConfig_DoesNotMutateFallbackPricing(t *testing.T) {
	svc := newTestCalculator()

	chPricing := &routing.ModelPricingEntry{
		InputPrice: testPtrFloat64(99e-6),
	}
	_, err := svc.GetModelPricingWithConfig("claude-sonnet-4", chPricing)
	require.NoError(t, err)

	pricing, err := svc.GetModelPricing("claude-sonnet-4")
	require.NoError(t, err)
	// Allow binary representation residue in these fixture rate comparisons.
	require.InDelta(t, 3e-6, pricing.InputPricePerToken, 1e-12)
	require.InDelta(t, 15e-6, pricing.OutputPricePerToken, 1e-12)
}

// TestGetModelPricingWithConfig_OverrideOutputPriceOnly supplies a manual
// output amount for a known Claude fixture. Output must change while input
// retains its catalog value and missing priority output remains zero.
func TestGetModelPricingWithConfig_OverrideOutputPriceOnly(t *testing.T) {
	svc := newTestCalculator()

	chPricing := &routing.ModelPricingEntry{
		OutputPrice: testPtrFloat64(88e-6),
	}
	pricing, err := svc.GetModelPricingWithConfig("claude-sonnet-4", chPricing)
	require.NoError(t, err)

	// The output override applies to standard rates; this catalog model has no
	// priority price.
	// Allow binary representation residue in these fixture rate comparisons.
	require.InDelta(t, 88e-6, pricing.OutputPricePerToken, 1e-12)
	require.Zero(t, pricing.OutputPricePerTokenPriority)

	require.InDelta(t, 3e-6, pricing.InputPricePerToken, 1e-12)
}

// TestGetModelPricingWithConfig_OverrideAllFields supplies manual text, cache
// and image-output buckets for a known model. Returned amounts must match the
// overrides, both TTL write slots share the supplied rate and absent priority
// slots stay zero.
func TestGetModelPricingWithConfig_OverrideAllFields(t *testing.T) {
	svc := newTestCalculator()

	chPricing := &routing.ModelPricingEntry{
		InputPrice:       testPtrFloat64(10e-6),
		OutputPrice:      testPtrFloat64(20e-6),
		CacheWritePrice:  testPtrFloat64(5e-6),
		CacheReadPrice:   testPtrFloat64(1e-6),
		ImageOutputPrice: testPtrFloat64(50e-6),
	}
	pricing, err := svc.GetModelPricingWithConfig("claude-sonnet-4", chPricing)
	require.NoError(t, err)

	// Allow binary representation residue in these fixture rate comparisons.
	require.InDelta(t, 10e-6, pricing.InputPricePerToken, 1e-12)
	require.Zero(t, pricing.InputPricePerTokenPriority)
	require.InDelta(t, 20e-6, pricing.OutputPricePerToken, 1e-12)
	require.Zero(t, pricing.OutputPricePerTokenPriority)
	require.InDelta(t, 5e-6, pricing.CacheCreationPricePerToken, 1e-12)
	require.InDelta(t, 5e-6, pricing.CacheCreation5mPrice, 1e-12)
	require.InDelta(t, 5e-6, pricing.CacheCreation1hPrice, 1e-12)
	require.InDelta(t, 1e-6, pricing.CacheReadPricePerToken, 1e-12)
	require.Zero(t, pricing.CacheReadPricePerTokenPriority)
	require.InDelta(t, 50e-6, pricing.ImageOutputPricePerToken, 1e-12)
}

// TestGetModelPricingWithConfig_CacheWritePriceAffects5mAnd1h supplies one
// manual short cache-write amount without an hourly replacement. Aggregate,
// five-minute and hourly returned write slots must all use that supplied rate.
func TestGetModelPricingWithConfig_CacheWritePriceAffects5mAnd1h(t *testing.T) {
	svc := newTestCalculator()

	chPricing := &routing.ModelPricingEntry{
		CacheWritePrice: testPtrFloat64(7e-6),
	}
	pricing, err := svc.GetModelPricingWithConfig("claude-sonnet-4", chPricing)
	require.NoError(t, err)

	// Allow binary representation residue in these fixture rate comparisons.
	require.InDelta(t, 7e-6, pricing.CacheCreationPricePerToken, 1e-12)
	require.InDelta(t, 7e-6, pricing.CacheCreation5mPrice, 1e-12)
	require.InDelta(t, 7e-6, pricing.CacheCreation1hPrice, 1e-12)
}

// TestGetModelPricingWithConfig_CacheWriteTTLPricesCanDiffer supplies distinct
// short and hourly cache amounts for the Fable fixture. The result must retain
// cache-breakdown support and separate five-minute/hourly rates.
func TestGetModelPricingWithConfig_CacheWriteTTLPricesCanDiffer(t *testing.T) {
	svc := newTestCalculator()

	pricing, err := svc.GetModelPricingWithConfig("claude-fable-5-1", &routing.ModelPricingEntry{
		CacheWritePrice:   testPtrFloat64(13e-6),
		CacheWrite1hPrice: testPtrFloat64(21e-6),
	})
	require.NoError(t, err)
	require.True(t, pricing.SupportsCacheBreakdown)
	// Allow binary representation residue in these fixture rate comparisons.
	require.InDelta(t, 13e-6, pricing.CacheCreationPricePerToken, 1e-12)
	require.InDelta(t, 13e-6, pricing.CacheCreation5mPrice, 1e-12)
	require.InDelta(t, 21e-6, pricing.CacheCreation1hPrice, 1e-12)
}

// TestGetModelPricing_Fable51FallbackPricing reads the registered Fable
// fixture's text/cache buckets. Lookup must expose those supplied rates and
// the present three-times Max reasoning factor from that entry.
func TestGetModelPricing_Fable51FallbackPricing(t *testing.T) {
	svc := newTestCalculator()

	pricing, err := svc.GetModelPricing("claude-fable-5-1")
	require.NoError(t, err)
	// Allow binary representation residue in these fixture rate comparisons.
	require.InDelta(t, 10e-6, pricing.InputPricePerToken, 1e-12)
	require.InDelta(t, 50e-6, pricing.OutputPricePerToken, 1e-12)
	require.InDelta(t, 12.5e-6, pricing.CacheCreation5mPrice, 1e-12)
	require.InDelta(t, 20e-6, pricing.CacheCreation1hPrice, 1e-12)
	require.InDelta(t, 0.25e-6, pricing.CacheReadPricePerToken, 1e-12)
	require.NotNil(t, pricing.MaxReasoningEffortMultiplier)
	require.Equal(t, 3.0, *pricing.MaxReasoningEffortMultiplier)
}

// TestGetModelPricingWithConfig_CacheReadPriceAffectsPriority overrides cached
// input on a fixture without an explicit priority cache bucket. Standard
// cached input must change to the supplied amount while priority remains zero.
func TestGetModelPricingWithConfig_CacheReadPriceAffectsPriority(t *testing.T) {
	svc := newTestCalculator()

	chPricing := &routing.ModelPricingEntry{
		CacheReadPrice: testPtrFloat64(2e-6),
	}
	pricing, err := svc.GetModelPricingWithConfig("claude-sonnet-4", chPricing)
	require.NoError(t, err)

	// CacheReadPrice overrides the standard rate; this model has no configured
	// priority rate.
	// Allow binary representation residue in these fixture rate comparisons.
	require.InDelta(t, 2e-6, pricing.CacheReadPricePerToken, 1e-12)
	require.Zero(t, pricing.CacheReadPricePerTokenPriority)
}

// TestGetModelPricingWithConfig_UnknownModelReturnsError supplies a manual
// input card for an absent full model through the catalog-first helper. Lookup
// must fail before card application, returning nil and the missing-price
// diagnostic.
func TestGetModelPricingWithConfig_UnknownModelReturnsError(t *testing.T) {
	svc := newTestCalculator()

	chPricing := &routing.ModelPricingEntry{
		InputPrice: testPtrFloat64(1e-6),
	}
	pricing, err := svc.GetModelPricingWithConfig("totally-unknown-model", chPricing)
	require.Error(t, err)
	require.Nil(t, pricing)
	require.Contains(t, err.Error(), "pricing not found")
}

// TestGetModelPricingWithConfig_NilImageOutputPriceZerosAndMarksExplicit
// supplies text overrides while omitting the image-output bucket. Returned
// image output must be explicit zero, preventing configuration absence from
// borrowing a text-output amount.
func TestGetModelPricingWithConfig_NilImageOutputPriceZerosAndMarksExplicit(t *testing.T) {
	svc := newTestCalculator()

	chPricing := &routing.ModelPricingEntry{
		InputPrice:  testPtrFloat64(10e-6),
		OutputPrice: testPtrFloat64(20e-6),
		// Leave this configuration image bucket absent so the returned explicit-zero
		// marker can distinguish card control from catalog text-output inheritance.
	}
	pricing, err := svc.GetModelPricingWithConfig("claude-sonnet-4", chPricing)
	require.NoError(t, err)

	require.Equal(t, 0.0, pricing.ImageOutputPricePerToken)
	require.True(t, pricing.ImageOutputPriceExplicit)
}

// TestComputeTokenBreakdown_ExplicitZeroImagePrice_NoFallback splits fifty
// image tokens from two hundred output tokens with explicit zero image
// pricing. Image cost must stay zero while the remaining text output uses its
// ordinary rate.
func TestComputeTokenBreakdown_ExplicitZeroImagePrice_NoFallback(t *testing.T) {
	svc := newTestCalculator()

	pricing := &billingpricing.ModelPricing{
		InputPricePerToken:       3e-6,
		OutputPricePerToken:      15e-6,
		ImageOutputPricePerToken: 0,
		ImageOutputPriceExplicit: true,
	}
	tokens := billingpricing.UsageTokens{
		InputTokens:       100,
		OutputTokens:      200,
		ImageOutputTokens: 50,
	}
	bd := svc.ComputeTokenBreakdown(pricing, tokens, 1.0, "", false)

	// An explicit zero image-output rate prevents use of the text-output rate.
	require.Equal(t, 0.0, bd.ImageOutputCost)
	// Text output tokens = 200 - 50 = 150.
	// Allow float residue in the selected media-unit charge comparisons.
	require.InDelta(t, 150*15e-6, bd.OutputCost, 1e-12)
}

// TestComputeTokenBreakdown_NonExplicitZeroImagePrice_FallsBackToOutput
// supplies the same output split with an absent image-price marker. Both text
// and image portions must use the text-output rate in their separately checked
// components.
func TestComputeTokenBreakdown_NonExplicitZeroImagePrice_FallsBackToOutput(t *testing.T) {
	svc := newTestCalculator()

	pricing := &billingpricing.ModelPricing{
		InputPricePerToken:       3e-6,
		OutputPricePerToken:      15e-6,
		ImageOutputPricePerToken: 0,
		ImageOutputPriceExplicit: false,
	}
	tokens := billingpricing.UsageTokens{
		InputTokens:       100,
		OutputTokens:      200,
		ImageOutputTokens: 50,
	}
	bd := svc.ComputeTokenBreakdown(pricing, tokens, 1.0, "", false)

	// An unset image-output rate uses the text-output rate.
	// Allow float residue in the selected media-unit charge comparisons.
	require.InDelta(t, 50*15e-6, bd.ImageOutputCost, 1e-12)
	// Text output tokens = 200 - 50 = 150.
	require.InDelta(t, 150*15e-6, bd.OutputCost, 1e-12)
}

// TestCalculateCostUnified_LongContextContract supplies ten combinations of
// context buckets, threshold policy, enablement, rates and surcharge factors.
// Base total, actual charge and marker must match each case, including
// zero-charge and unit-factor exclusions.
func TestCalculateCostUnified_LongContextContract(t *testing.T) {
	svc := newTestCalculator()
	resolver := testkit.PriceResolver(nil, svc)
	cases := []struct {
		name string // supplied long-context case label
		// input, cacheRead and cacheWrite contribute to the aggregate context tested
		// against threshold. Their separate buckets also determine which input-side
		// component is charged after the threshold decision.
		input, // ordinary input token count
		cacheRead, // cached-input token count
		cacheWrite int // aggregate cache-write token count
		threshold int // configured context token threshold
		// inclusive, disabled and unitPrices combine threshold equality, enablement
		// and surcharge-factor inputs. These controls determine whether this
		// supplied context receives a price increase in the asserted result.
		// inclusive selects whether aggregate context exactly at threshold enters
		// long pricing. The paired equality cases assert different amounts under
		// that policy.
		inclusive, // equality enters the long tier
		// disabled sets the resolution's enablement false, keeping built-in tier
		// factors inactive even when the supplied context crosses threshold.
		disabled, // built-in tier enablement disabled
		// unitPrices replaces both long-context factors with one before calculation,
		// so eligible context alone cannot create the expected surcharge marker.
		unitPrices bool // unit long-context factors selected
		// rate supplies the final user factor, wantTotal is the expected base charge
		// and wantActual the scaled user charge. These expectations distinguish base
		// surcharge from actual-cost marker eligibility.
		rate, // supplied final user cost factor
		wantTotal, // expected pre-user-factor base charge
		wantActual float64 // expected final user charge
		applied bool // expected actual-cost surcharge marker
	}{
		{name: "低于阈值", input: 99, cacheRead: 100, threshold: 200, rate: 1, wantTotal: 111, wantActual: 111},
		{name: "等于排他阈值", input: 100, cacheRead: 100, threshold: 200, rate: 1, wantTotal: 112, wantActual: 112},
		{name: "超过阈值整段计价", input: 101, cacheRead: 100, threshold: 200, rate: 2, wantTotal: 225, wantActual: 450, applied: true},
		{name: "缓存读取超过阈值", input: 10, cacheRead: 210, threshold: 200, rate: 1, wantTotal: 65, wantActual: 65, applied: true},
		{name: "缓存创建参与阈值和加价", input: 60, cacheRead: 30, cacheWrite: 120, threshold: 200, rate: 1, wantTotal: 249, wantActual: 249, applied: true},
		{name: "零倍率不标记加价", input: 101, cacheRead: 100, threshold: 200, rate: 0, wantTotal: 225, wantActual: 0},
		{name: "关闭长上下文", input: 101, cacheRead: 100, threshold: 200, disabled: true, rate: 1, wantTotal: 113, wantActual: 113},
		{name: "零阈值", input: 101, cacheRead: 100, rate: 1, wantTotal: 113, wantActual: 113},
		{name: "倍率均为一", input: 101, cacheRead: 100, threshold: 200, unitPrices: true, rate: 1, wantTotal: 113, wantActual: 113},
		{name: "等于包含阈值", input: 100, cacheRead: 100, threshold: 200, inclusive: true, rate: 1, wantTotal: 223, wantActual: 223, applied: true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			prices := &billingpricing.ModelPricing{
				InputPricePerToken: 1, OutputPricePerToken: 2,
				CacheReadPricePerToken: 0.1, CacheCreationPricePerToken: 0.5,
				LongContextInputThreshold: tc.threshold, LongContextThresholdInclusive: tc.inclusive,
				LongContextInputMultiplier: 2, LongContextOutputMultiplier: 1.5,
			}
			if tc.unitPrices {
				prices.LongContextInputMultiplier = 1
				prices.LongContextOutputMultiplier = 1
			}
			cost, err := svc.CalculateCostUnified(billing.CostInput{
				Ctx: t.Context(), Model: "contract-long-context", RateMultiplier: tc.rate,
				Tokens:   billingpricing.UsageTokens{InputTokens: tc.input, OutputTokens: 1, CacheReadTokens: tc.cacheRead, CacheCreationTokens: tc.cacheWrite},
				Resolver: resolver,
				Resolved: &billingpricing.ResolvedPricing{Mode: billingpricing.BillingModeToken, BasePricing: prices, Source: billingpricing.PricingSourceConfig, LongContextPricingEnabled: !tc.disabled},
			})
			require.NoError(t, err)
			// Allow float arithmetic residue in these calculated component amounts.
			require.InDelta(t, tc.wantTotal, cost.TotalCost, 1e-10)
			require.InDelta(t, tc.wantActual, cost.ActualCost, 1e-10)
			require.Equal(t, tc.applied, cost.LongContextBillingApplied)
		})
	}
}

// TestCalculateCostUnified_NilResolver_FallsBackToOldPath omits Resolver for
// ordinary fixture token usage. Totals must match direct token calculation,
// and the result's billing mode must remain empty on that delegated path.
func TestCalculateCostUnified_NilResolver_FallsBackToOldPath(t *testing.T) {
	svc := newTestCalculator()

	tokens := billingpricing.UsageTokens{InputTokens: 1000, OutputTokens: 500}
	input := billing.CostInput{
		Model:          "claude-sonnet-4",
		Tokens:         tokens,
		RateMultiplier: 1.0,
		Resolver:       nil, // no resolver
	}
	cost, err := svc.CalculateCostUnified(input)
	require.NoError(t, err)

	expected, err := svc.CalculateCostInternal("claude-sonnet-4", tokens, 1.0, "", nil)
	require.NoError(t, err)
	// Allow float arithmetic residue in these calculated component amounts.
	require.InDelta(t, expected.TotalCost, cost.TotalCost, 1e-10)
	require.InDelta(t, expected.ActualCost, cost.ActualCost, 1e-10)
	require.Empty(t, cost.BillingMode)
}

// TestCalculateCostUnified_TokenMode resolves known fixture token amounts and
// applies a 1.5 user factor. Base and actual totals must match the supplied
// text usage and the result must report token mode.
func TestCalculateCostUnified_TokenMode(t *testing.T) {
	bs := newTestCalculator()
	resolver := testkit.PriceResolver(nil, bs)

	tokens := billingpricing.UsageTokens{InputTokens: 1000, OutputTokens: 500}
	input := billing.CostInput{
		Ctx:            context.Background(),
		Model:          "claude-sonnet-4",
		Tokens:         tokens,
		RateMultiplier: 1.5,
		Resolver:       resolver,
	}
	cost, err := bs.CalculateCostUnified(input)
	require.NoError(t, err)
	require.NotNil(t, cost)

	expectedTotal := 1000*3e-6 + 500*15e-6
	// Allow float arithmetic residue in these calculated component amounts.
	require.InDelta(t, expectedTotal, cost.TotalCost, 1e-10)
	require.InDelta(t, expectedTotal*1.5, cost.ActualCost, 1e-10)
	require.Equal(t, string(routing.BillingModeToken), cost.BillingMode)
}

// TestCalculateCostUnified_Fable51MaxReasoningMultiplier compares xhigh and
// max effort on identical Fable fixture usage. Max base and actual totals must
// triple according to the entry's supplied reasoning factor.
func TestCalculateCostUnified_Fable51MaxReasoningMultiplier(t *testing.T) {
	bs := newTestCalculator()
	resolver := testkit.PriceResolver(nil, bs)
	tokens := billingpricing.UsageTokens{InputTokens: 1000, OutputTokens: 100}

	standard, err := bs.CalculateCostUnified(billing.CostInput{
		Ctx: context.Background(), Model: "claude-fable-5-1", Tokens: tokens,
		RateMultiplier: 1, ReasoningEffort: "xhigh", Resolver: resolver,
	})
	require.NoError(t, err)
	max, err := bs.CalculateCostUnified(billing.CostInput{
		Ctx: context.Background(), Model: "claude-fable-5-1", Tokens: tokens,
		RateMultiplier: 1, ReasoningEffort: "max", Resolver: resolver,
	})
	require.NoError(t, err)
	// Allow float multiplication residue in these scaled component comparisons.
	require.InDelta(t, standard.TotalCost*3, max.TotalCost, 1e-12)
	require.InDelta(t, standard.ActualCost*3, max.ActualCost, 1e-12)
}

// TestCalculateCostUnified_PricingConfigOverridesFable51MaxReasoningMultiplier
// supplies a configured reasoning factor above the Fable base card. Max-effort
// total must use that 1.5 factor on the selected manual input amount.
func TestCalculateCostUnified_PricingConfigOverridesFable51MaxReasoningMultiplier(t *testing.T) {
	configured := 1.5
	groupID := int64(7)
	cs := newTestPricingConfigServiceWithCache(t, &routingtestkit.ModelConfigData{
		Prices: map[routingtestkit.ModelKey]*routing.ModelPricingEntry{
			{GroupID: groupID, Platform: capability.PlatformAnthropic, Model: "claude-fable-5-1"}: {
				BillingMode: routing.BillingModeToken,
				InputPrice:  testPtrFloat64(10e-6), OutputPrice: testPtrFloat64(50e-6),
				MaxReasoningEffortMultiplier: &configured,
			},
		},
		ByGroup:       map[int64]*routingtestkit.Configuration{groupID: {ID: 1, Status: billing.StatusActive}},
		Platforms:     map[int64]string{groupID: capability.PlatformAnthropic},
		PricePatterns: map[routingtestkit.GroupPlatform][]*routingtestkit.PricePattern{},
		Models:        map[routingtestkit.ModelKey]string{},
		ModelPatterns: map[routingtestkit.GroupPlatform][]*routingtestkit.ModelPattern{},
		ByID:          map[int64]*routingtestkit.Configuration{},
	})
	bs := newTestCalculator()
	resolver := testkit.PriceResolver(cs, bs)
	cost, err := bs.CalculateCostUnified(billing.CostInput{
		Ctx: context.Background(), Model: "claude-fable-5-1", GroupID: &groupID,
		Tokens: billingpricing.UsageTokens{InputTokens: 1000}, RateMultiplier: 1, ReasoningEffort: "max", Resolver: resolver,
	})
	require.NoError(t, err)
	// Allow float multiplication residue in these scaled component comparisons.
	require.InDelta(t, 1000*10e-6*configured, cost.TotalCost, 1e-12)
}

// TestCalculateCostUnified_AppliesPricingConfigPriceMultiplierBeforeRateMultiplier
// combines a manual input amount, inherited output, doubled card factor and
// tripled user factor. Base total must reflect card scaling before the final
// actual-cost scaling.
func TestCalculateCostUnified_AppliesPricingConfigPriceMultiplierBeforeRateMultiplier(t *testing.T) {
	cs := newTestPricingConfigServiceWithCache(t, &routingtestkit.ModelConfigData{
		Prices: map[routingtestkit.ModelKey]*routing.ModelPricingEntry{
			{GroupID: 2, Model: "claude-sonnet-4"}: {
				BillingMode:     routing.BillingModeToken,
				PriceMultiplier: testPtrFloat64(2),
				InputPrice:      testPtrFloat64(5e-6),
			},
		},
		ByGroup: map[int64]*routingtestkit.Configuration{
			2: {ID: 2, Status: billing.StatusActive},
		},
		Platforms:     map[int64]string{2: ""},
		PricePatterns: map[routingtestkit.GroupPlatform][]*routingtestkit.PricePattern{},
		Models:        map[routingtestkit.ModelKey]string{},
		ModelPatterns: map[routingtestkit.GroupPlatform][]*routingtestkit.ModelPattern{},
		ByID:          map[int64]*routingtestkit.Configuration{},
	})

	bs := newTestCalculator()
	resolver := testkit.PriceResolver(cs, bs)
	groupID := int64(2)
	cost, err := bs.CalculateCostUnified(billing.CostInput{
		Ctx:            context.Background(),
		Model:          "claude-sonnet-4",
		GroupID:        &groupID,
		Tokens:         billingpricing.UsageTokens{InputTokens: 100, OutputTokens: 10},
		RateMultiplier: 3,
		Resolver:       resolver,
	})
	require.NoError(t, err)

	// Input 5e-6 and inherited output 15e-6 receive the configured 2x
	// multiplier, then the group 3x multiplier.
	expectedTotal := 100*10e-6 + 10*30e-6
	// Allow float multiplication residue in these scaled component comparisons.
	require.InDelta(t, expectedTotal, cost.TotalCost, 1e-12)
	require.InDelta(t, expectedTotal*3, cost.ActualCost, 1e-12)
}

// TestCalculateCostUnified_TokenModeAppliesRateMultiplierToImageTokens
// separates image output from aggregate output on known fixture pricing. Base
// total must include both output portions and actual cost must apply the user
// factor to their combined amount.
func TestCalculateCostUnified_TokenModeAppliesRateMultiplierToImageTokens(t *testing.T) {
	bs := newTestCalculator()
	resolver := testkit.PriceResolver(nil, bs)

	tokens := billingpricing.UsageTokens{InputTokens: 1000, OutputTokens: 600, ImageOutputTokens: 100}
	cost, err := bs.CalculateCostUnified(billing.CostInput{
		Ctx:            context.Background(),
		Model:          "claude-sonnet-4",
		Tokens:         tokens,
		RateMultiplier: 3.0,
		Resolver:       resolver,
	})
	require.NoError(t, err)

	textInput := 1000 * 3e-6
	textOutput := 500 * 15e-6
	imageOutput := 100 * 15e-6
	// Allow float multiplication residue in these scaled component comparisons.
	require.InDelta(t, textInput+textOutput+imageOutput, cost.TotalCost, 1e-10)
	require.InDelta(t, (textInput+textOutput+imageOutput)*3.0, cost.ActualCost, 1e-10)
	require.InDelta(t, imageOutput, cost.ImageOutputCost, 1e-10)
}

// TestCalculateCostUnified_PerRequestMode supplies a fixed per-request card
// for three requests with a doubled user factor. Base and actual totals must
// follow unit-count arithmetic and report per-request mode.
func TestCalculateCostUnified_PerRequestMode(t *testing.T) {
	cs := newTestPricingConfigServiceWithCache(t, &routingtestkit.ModelConfigData{
		Prices: map[routingtestkit.ModelKey]*routing.ModelPricingEntry{
			{GroupID: 1, Model: "claude-sonnet-4"}: {
				BillingMode:     routing.BillingModePerRequest,
				PerRequestPrice: testPtrFloat64(0.05),
			},
		},
		ByGroup: map[int64]*routingtestkit.Configuration{
			1: {ID: 1, Status: billing.StatusActive},
		},
		Platforms:     map[int64]string{1: ""},
		PricePatterns: map[routingtestkit.GroupPlatform][]*routingtestkit.PricePattern{},
		Models:        map[routingtestkit.ModelKey]string{},
		ModelPatterns: map[routingtestkit.GroupPlatform][]*routingtestkit.ModelPattern{},
		ByID:          map[int64]*routingtestkit.Configuration{},
	})

	bs := newTestCalculator()
	resolver := testkit.PriceResolver(cs, bs)
	groupID := int64(1)

	input := billing.CostInput{
		Ctx:            context.Background(),
		Model:          "claude-sonnet-4",
		GroupID:        &groupID,
		Tokens:         billingpricing.UsageTokens{InputTokens: 100, OutputTokens: 50},
		RequestCount:   3,
		RateMultiplier: 2.0,
		Resolver:       resolver,
	}
	cost, err := bs.CalculateCostUnified(input)
	require.NoError(t, err)
	require.NotNil(t, cost)

	// Allow float arithmetic residue in these calculated component amounts.
	require.InDelta(t, 0.15, cost.TotalCost, 1e-10)
	require.InDelta(t, 0.30, cost.ActualCost, 1e-10)
	require.Equal(t, string(routing.BillingModePerRequest), cost.BillingMode)
}

// TestCalculateCostUnified_PerRequestTierExplicitZeroDoesNotFallbackToDefault
// supplies an image-size zero tier above a nonzero default. The chosen size
// must produce zero totals for both images and retain image billing mode.
func TestCalculateCostUnified_PerRequestTierExplicitZeroDoesNotFallbackToDefault(t *testing.T) {
	zero := 0.0
	defaultPrice := 0.10
	cs := newTestPricingConfigServiceWithCache(t, &routingtestkit.ModelConfigData{
		Prices: map[routingtestkit.ModelKey]*routing.ModelPricingEntry{
			{GroupID: 3, Model: "qoder-image"}: {
				BillingMode:     routing.BillingModeImage,
				PerRequestPrice: &defaultPrice,
				Intervals: []routing.PricingInterval{
					{TierLabel: "1K", PerRequestPrice: &zero},
				},
			},
		},
		ByGroup: map[int64]*routingtestkit.Configuration{
			3: {ID: 3, Status: billing.StatusActive},
		},
		Platforms:     map[int64]string{3: ""},
		PricePatterns: map[routingtestkit.GroupPlatform][]*routingtestkit.PricePattern{},
		Models:        map[routingtestkit.ModelKey]string{},
		ModelPatterns: map[routingtestkit.GroupPlatform][]*routingtestkit.ModelPattern{},
		ByID:          map[int64]*routingtestkit.Configuration{},
	})

	bs := newTestCalculator()
	resolver := testkit.PriceResolver(cs, bs)
	groupID := int64(3)

	cost, err := bs.CalculateCostUnified(billing.CostInput{
		Ctx:            context.Background(),
		Model:          "qoder-image",
		GroupID:        &groupID,
		SizeTier:       "1K",
		RequestCount:   2,
		RateMultiplier: 1.0,
		Resolver:       resolver,
	})
	require.NoError(t, err)
	require.NotNil(t, cost)
	// Allow float multiplication residue in these scaled component comparisons.
	require.InDelta(t, 0.0, cost.TotalCost, 1e-10)
	require.InDelta(t, 0.0, cost.ActualCost, 1e-10)
	require.Equal(t, string(routing.BillingModeImage), cost.BillingMode)
}

// TestCalculateCostUnified_PerRequestContextTierExplicitZeroDoesNotFallbackToDefault
// supplies an explicit-zero request context tier above a nonzero default. A
// matching context must produce zero totals for the supplied request count and
// retain request mode.
func TestCalculateCostUnified_PerRequestContextTierExplicitZeroDoesNotFallbackToDefault(t *testing.T) {
	zero := 0.0
	defaultPrice := 0.10
	maxTokens := 1000
	cs := newTestPricingConfigServiceWithCache(t, &routingtestkit.ModelConfigData{
		Prices: map[routingtestkit.ModelKey]*routing.ModelPricingEntry{
			{GroupID: 4, Model: "qoder-request"}: {
				BillingMode:     routing.BillingModePerRequest,
				PerRequestPrice: &defaultPrice,
				Intervals: []routing.PricingInterval{
					{MinTokens: 0, MaxTokens: &maxTokens, PerRequestPrice: &zero},
				},
			},
		},
		ByGroup: map[int64]*routingtestkit.Configuration{
			4: {ID: 4, Status: billing.StatusActive},
		},
		Platforms:     map[int64]string{4: ""},
		PricePatterns: map[routingtestkit.GroupPlatform][]*routingtestkit.PricePattern{},
		Models:        map[routingtestkit.ModelKey]string{},
		ModelPatterns: map[routingtestkit.GroupPlatform][]*routingtestkit.ModelPattern{},
		ByID:          map[int64]*routingtestkit.Configuration{},
	})

	bs := newTestCalculator()
	resolver := testkit.PriceResolver(cs, bs)
	groupID := int64(4)

	cost, err := bs.CalculateCostUnified(billing.CostInput{
		Ctx:            context.Background(),
		Model:          "qoder-request",
		GroupID:        &groupID,
		Tokens:         billingpricing.UsageTokens{InputTokens: 500},
		RequestCount:   3,
		RateMultiplier: 1.0,
		Resolver:       resolver,
	})
	require.NoError(t, err)
	require.NotNil(t, cost)
	// Allow float multiplication residue in these scaled component comparisons.
	require.InDelta(t, 0.0, cost.TotalCost, 1e-10)
	require.InDelta(t, 0.0, cost.ActualCost, 1e-10)
	require.Equal(t, string(routing.BillingModePerRequest), cost.BillingMode)
}

// TestCalculateCostUnified_ImageMode supplies a manual per-image amount for a
// model absent from the calculator catalog. Two image units must produce the
// configured total and report image mode with unit-factor actual cost.
func TestCalculateCostUnified_ImageMode(t *testing.T) {
	cs := newTestPricingConfigServiceWithCache(t, &routingtestkit.ModelConfigData{
		Prices: map[routingtestkit.ModelKey]*routing.ModelPricingEntry{
			{GroupID: 2, Model: "gemini-image"}: {
				BillingMode:     routing.BillingModeImage,
				PerRequestPrice: testPtrFloat64(0.10),
			},
		},
		ByGroup: map[int64]*routingtestkit.Configuration{
			2: {ID: 2, Status: billing.StatusActive},
		},
		Platforms:     map[int64]string{2: ""},
		PricePatterns: map[routingtestkit.GroupPlatform][]*routingtestkit.PricePattern{},
		Models:        map[routingtestkit.ModelKey]string{},
		ModelPatterns: map[routingtestkit.GroupPlatform][]*routingtestkit.ModelPattern{},
		ByID:          map[int64]*routingtestkit.Configuration{},
	})

	bs := newCalculatorWithPrices(nil, map[string]*billingpricing.ModelPricing{})
	resolver := testkit.PriceResolver(cs, bs)
	groupID := int64(2)

	input := billing.CostInput{
		Ctx:            context.Background(),
		Model:          "gemini-image",
		GroupID:        &groupID,
		Tokens:         billingpricing.UsageTokens{},
		RequestCount:   2,
		RateMultiplier: 1.0,
		Resolver:       resolver,
	}
	cost, err := bs.CalculateCostUnified(input)
	require.NoError(t, err)
	require.NotNil(t, cost)

	// Allow float residue in the selected media-unit charge comparisons.
	require.InDelta(t, 0.20, cost.TotalCost, 1e-10)
	require.InDelta(t, 0.20, cost.ActualCost, 1e-10)
	require.Equal(t, string(routing.BillingModeImage), cost.BillingMode)
}

// TestCalculateCostUnified_RateMultiplierZeroProducesZero supplies known token
// pricing with a zero user factor. Base total must remain positive while
// actual cost is zero, protecting the calculator's accepted zero-factor input.
func TestCalculateCostUnified_RateMultiplierZeroProducesZero(t *testing.T) {
	bs := newTestCalculator()
	resolver := testkit.PriceResolver(nil, bs)

	tokens := billingpricing.UsageTokens{InputTokens: 1000, OutputTokens: 500}

	cost, err := bs.CalculateCostUnified(billing.CostInput{
		Ctx:            context.Background(),
		Model:          "claude-sonnet-4",
		Tokens:         tokens,
		RateMultiplier: 0,
		Resolver:       resolver,
	})
	require.NoError(t, err)
	require.Greater(t, cost.TotalCost, 0.0)
	// Allow float multiplication residue in these scaled component comparisons.
	require.InDelta(t, 0.0, cost.ActualCost, 1e-10)
}

// TestCalculateCostUnified_NegativeRateMultiplierClampedToZero supplies known
// token pricing with a negative user factor. Base total must remain positive
// while actual cost is zero under calculation-side factor clamping.
func TestCalculateCostUnified_NegativeRateMultiplierClampedToZero(t *testing.T) {
	bs := newTestCalculator()
	resolver := testkit.PriceResolver(nil, bs)

	tokens := billingpricing.UsageTokens{InputTokens: 1000}

	cost, err := bs.CalculateCostUnified(billing.CostInput{
		Ctx:            context.Background(),
		Model:          "claude-sonnet-4",
		Tokens:         tokens,
		RateMultiplier: -5.0,
		Resolver:       resolver,
	})
	require.NoError(t, err)
	require.Greater(t, cost.TotalCost, 0.0)
	// Allow float multiplication residue in these scaled component comparisons.
	require.InDelta(t, 0.0, cost.ActualCost, 1e-10)
}

// TestCalculateCostUnified_BillingModeFieldFilled supplies a resolver for
// ordinary catalogued fixture token usage. The successful result must report
// token mode, exposing resolved dispatch metadata alongside calculation
// amounts.
func TestCalculateCostUnified_BillingModeFieldFilled(t *testing.T) {
	bs := newTestCalculator()
	resolver := testkit.PriceResolver(nil, bs)

	cost, err := bs.CalculateCostUnified(billing.CostInput{
		Ctx:            context.Background(),
		Model:          "claude-sonnet-4",
		Tokens:         billingpricing.UsageTokens{InputTokens: 100},
		RateMultiplier: 1.0,
		Resolver:       resolver,
	})
	require.NoError(t, err)
	require.Equal(t, "token", cost.BillingMode)
}

// TestCalculateCostUnified_UsesPreResolvedPricing supplies request-mode
// pricing while the ordinary fixture model has token rates. Calculation must
// reuse the supplied selection, charging two request units and reporting
// per-request mode.
func TestCalculateCostUnified_UsesPreResolvedPricing(t *testing.T) {
	bs := newTestCalculator()
	resolver := testkit.PriceResolver(nil, bs)

	// Supply request-mode resolution for a catalog-token model. This fixes the
	// selected funding unit for the call and makes an accidental second
	// resolution observable in its result.
	preResolved := &billingpricing.ResolvedPricing{
		Mode:                   routing.BillingModePerRequest,
		DefaultPerRequestPrice: 0.07,
	}

	cost, err := bs.CalculateCostUnified(billing.CostInput{
		Ctx:            context.Background(),
		Model:          "claude-sonnet-4",
		Tokens:         billingpricing.UsageTokens{InputTokens: 100},
		RequestCount:   2,
		RateMultiplier: 1.0,
		Resolver:       resolver,
		Resolved:       preResolved,
	})
	require.NoError(t, err)
	require.NotNil(t, cost)

	// Allow float arithmetic residue in these calculated component amounts.
	require.InDelta(t, 0.14, cost.TotalCost, 1e-10)
	require.Equal(t, string(routing.BillingModePerRequest), cost.BillingMode)
}

// TestEmptyCatalogNeverRecreatesHistoricalPrices constructs a production
// calculator with no catalog and iterates historical fixture names. Every
// lookup must remain unavailable, and each default display must report
// unpriced instead of reconstructing fixture rates.
func TestEmptyCatalogNeverRecreatesHistoricalPrices(t *testing.T) {
	calculator := billing.NewCalculator(nil, billing.CalculatorOptions{})
	for model := range testkit.HistoricalPrices() {
		price, err := calculator.GetModelPricing(model)
		require.ErrorIs(t, err, billingpricing.ErrModelPricingUnavailable, model)
		require.Nil(t, price, model)
		require.Equal(t, "unpriced", calculator.DefaultModelPrice(model, "", "token").PriceStatus, model)
	}
}

// TestCalculateSearchCost checks nonpositive calls, missing/zero rates and a
// supplied USD-per-thousand amount. Positive usage must produce the asserted
// base and scaled actual totals, while those nonbillable inputs return zero.
func TestCalculateSearchCost(t *testing.T) {
	t.Parallel()
	s := testkit.Calculator(nil, map[string]*billingpricing.ModelPricing{})
	require.Equal(t, 0.0, s.CalculateSearchCost(0, floatPtr(10), 1).ActualCost)
	// Missing configured and catalog rates produce zero cost.
	require.Zero(t, s.CalculateSearchCost(5, nil, 1).ActualCost)
	// An explicit zero price means free usage.
	require.Equal(t, 0.0, s.CalculateSearchCost(5, floatPtr(0), 1).ActualCost)
	// The configured search amount is USD per thousand calls, so one hundred
	// calls consume one tenth before the final user factor applies.
	price := 10.0
	cost := s.CalculateSearchCost(100, &price, 1.5)
	// Allow float residue after converting the thousand-call price unit.
	require.InDelta(t, 1.0, cost.TotalCost, 1e-9)
	require.InDelta(t, 1.5, cost.ActualCost, 1e-9)
}

// TestCalculateAudioCost supplies realtime minutes, TTS million-character
// units and STT hours with configured prices. Each recognized mode must
// produce its asserted cost, while unknown modes and missing/explicit-zero
// rates return zero.
func TestCalculateAudioCost(t *testing.T) {
	t.Parallel()
	s := testkit.Calculator(nil, map[string]*billingpricing.ModelPricing{})
	// Supply realtime rates per minute, TTS per million characters and STT per
	// hour. Each later quantity is already in its matching unit, keeping those
	// operation bases distinct.
	rt, tts, stt := 0.10, 15.0, 0.50
	cfg := &billingpricing.AudioPriceConfig{RealtimePerMin: &rt, TTSPerMChars: &tts, STTPerHour: &stt}
	// Allow float residue in the separately converted operation-unit charges.
	require.InDelta(t, 0.20, s.CalculateAudioCost("realtime", 2, cfg, 1).ActualCost, 1e-9)
	require.InDelta(t, 1.5, s.CalculateAudioCost("tts", 0.1, cfg, 1).ActualCost, 1e-9)
	require.InDelta(t, 0.25, s.CalculateAudioCost("stt", 0.5, cfg, 1).ActualCost, 1e-9)
	require.Equal(t, 0.0, s.CalculateAudioCost("unknown", 1, cfg, 1).ActualCost)
	// Without operation prices, each mode records zero cost.
	require.Zero(t, s.CalculateAudioCost("realtime", 1, nil, 1).ActualCost)
	require.Zero(t, s.CalculateAudioCost("tts", 1, nil, 1).ActualCost)
	require.Zero(t, s.CalculateAudioCost("stt", 1, nil, 1).ActualCost)
	// An explicit zero price means free usage.
	zero := 0.0
	require.Equal(t, 0.0, s.CalculateAudioCost("realtime", 1, &billingpricing.AudioPriceConfig{RealtimePerMin: &zero}, 1).ActualCost)
}

// TestCalculateImageCost_DefaultPricing supplies explicit catalog image-size
// amounts for a full model. One and three 2K images must use the supplied
// 0.201 USD unit amount in the checked totals.
func TestCalculateImageCost_DefaultPricing(t *testing.T) {
	svc := testkit.Calculator(newCatalogFixture(catalogFixture{pricingData: map[string]*billingpricing.CatalogModelPricing{"gemini-3-pro-image": {CatalogRules: billingpricing.CatalogRules{ImagePrices: map[string]float64{"1K": 0.134, "2K": 0.201, "4K": 0.268}}, OutputCostPerImage: 0.134, ImagePricePresent: true, Mode: "image_generation", TokenPricingAbsent: true}}}), map[string]*billingpricing.ModelPricing{}) // Use the full model name's catalog entry.

	// The fixture's 2K rate is $0.201 per image.
	cost, mediaErr := svc.CalculateImageCost("gemini-3-pro-image", "2K", 1, 1.0)
	if mediaErr != nil {
		t.Fatal(mediaErr)
	}
	// Allow float residue in the selected media-unit charge comparisons.
	require.InDelta(t, 0.201, cost.TotalCost, 0.0001)
	require.InDelta(t, 0.201, cost.ActualCost, 0.0001)

	cost, mediaErr = svc.CalculateImageCost("gemini-3-pro-image", "2K", 3, 1.0)
	if mediaErr != nil {
		t.Fatal(mediaErr)
	}
	require.InDelta(t, 0.603, cost.TotalCost, 0.0001)
}

// TestCalculateImageCost_NormalizesInvalidSizeTo2K requests empty, auto and
// unrecognized size labels from a catalog image fixture. Each must select 2K
// and charge its supplied rate for two images.
func TestCalculateImageCost_NormalizesInvalidSizeTo2K(t *testing.T) {
	svc := testkit.Calculator(newCatalogFixture(catalogFixture{pricingData: map[string]*billingpricing.CatalogModelPricing{"gemini-3-pro-image": {CatalogRules: billingpricing.CatalogRules{ImagePrices: map[string]float64{"1K": 0.134, "2K": 0.201, "4K": 0.268}}, OutputCostPerImage: 0.134, ImagePricePresent: true, Mode: "image_generation", TokenPricingAbsent: true}}}), map[string]*billingpricing.ModelPricing{})

	for _, imageSize := range []string{"", "auto", "not-a-size"} {
		t.Run(imageSize, func(t *testing.T) {
			cost, mediaErr := svc.CalculateImageCost("gemini-3-pro-image", imageSize, 2, 1.0)
			if mediaErr != nil {
				t.Fatal(mediaErr)
			}
			// Allow float residue in the selected media-unit charge comparisons.
			require.InDelta(t, 0.402, cost.TotalCost, 0.0001)
			require.InDelta(t, 0.402, cost.ActualCost, 0.0001)
		})
	}
}

// TestCalculateImageCost_Explicit4KPrice queries one 4K image using an
// explicit catalog size table. Total cost must equal that fixture's 0.268 USD
// amount, protecting size-specific unit selection.
func TestCalculateImageCost_Explicit4KPrice(t *testing.T) {
	svc := testkit.Calculator(newCatalogFixture(catalogFixture{pricingData: map[string]*billingpricing.CatalogModelPricing{"gemini-3-pro-image": {CatalogRules: billingpricing.CatalogRules{ImagePrices: map[string]float64{"1K": 0.134, "2K": 0.201, "4K": 0.268}}, OutputCostPerImage: 0.134, ImagePricePresent: true, Mode: "image_generation", TokenPricingAbsent: true}}}), map[string]*billingpricing.ModelPricing{})

	// The fixture's 4K rate is $0.268 per image.
	cost, mediaErr := svc.CalculateImageCost("gemini-3-pro-image", "4K", 1, 1.0)
	if mediaErr != nil {
		t.Fatal(mediaErr)
	}
	// Allow float residue in the selected media-unit charge comparisons.
	require.InDelta(t, 0.268, cost.TotalCost, 0.0001)
}

// TestCalculateImageCost_RateMultiplier combines image count and user factors
// on an explicit catalog size table. Base totals must follow image quantity,
// while actual totals apply each supplied factor afterward.
func TestCalculateImageCost_RateMultiplier(t *testing.T) {
	svc := testkit.Calculator(newCatalogFixture(catalogFixture{pricingData: map[string]*billingpricing.CatalogModelPricing{"gemini-3-pro-image": {CatalogRules: billingpricing.CatalogRules{ImagePrices: map[string]float64{"1K": 0.134, "2K": 0.201, "4K": 0.268}}, OutputCostPerImage: 0.134, ImagePricePresent: true, Mode: "image_generation", TokenPricingAbsent: true}}}), map[string]*billingpricing.ModelPricing{})

	cost, mediaErr := svc.CalculateImageCost("gemini-3-pro-image", "2K", 1, 1.5)
	if mediaErr != nil {
		t.Fatal(mediaErr)
	}
	// Allow float multiplication residue in these scaled component comparisons.
	require.InDelta(t, 0.201, cost.TotalCost, 0.0001)   // TotalCost uses the 2K unit price before the group multiplier.
	require.InDelta(t, 0.3015, cost.ActualCost, 0.0001) // ActualCost = 0.201 * 1.5

	cost, mediaErr = svc.CalculateImageCost("gemini-3-pro-image", "2K", 2, 2.0)
	if mediaErr != nil {
		t.Fatal(mediaErr)
	}
	require.InDelta(t, 0.402, cost.TotalCost, 0.0001)
	require.InDelta(t, 0.804, cost.ActualCost, 0.0001)
}

// TestCalculateImageCost_ZeroCount supplies a priced catalog model but
// requests zero images. Calculation must succeed with zero base and actual
// amounts, keeping quantity handling separate from fixture availability.
func TestCalculateImageCost_ZeroCount(t *testing.T) {
	svc := testkit.Calculator(newCatalogFixture(catalogFixture{pricingData: map[string]*billingpricing.CatalogModelPricing{"gemini-3-pro-image": {CatalogRules: billingpricing.CatalogRules{ImagePrices: map[string]float64{"1K": 0.134, "2K": 0.201, "4K": 0.268}}, OutputCostPerImage: 0.134, ImagePricePresent: true, Mode: "image_generation", TokenPricingAbsent: true}}}), map[string]*billingpricing.ModelPricing{})

	cost, mediaErr := svc.CalculateImageCost("gemini-3-pro-image", "2K", 0, 1.0)
	if mediaErr != nil {
		t.Fatal(mediaErr)
	}
	require.Equal(t, 0.0, cost.TotalCost)
	require.Equal(t, 0.0, cost.ActualCost)
}

// TestCalculateImageCost_NegativeCount supplies a priced catalog model with
// negative image quantity. Calculation must succeed with zero base and actual
// amounts, retaining the calculator's nonpositive-count result.
func TestCalculateImageCost_NegativeCount(t *testing.T) {
	svc := testkit.Calculator(newCatalogFixture(catalogFixture{pricingData: map[string]*billingpricing.CatalogModelPricing{"gemini-3-pro-image": {CatalogRules: billingpricing.CatalogRules{ImagePrices: map[string]float64{"1K": 0.134, "2K": 0.201, "4K": 0.268}}, OutputCostPerImage: 0.134, ImagePricePresent: true, Mode: "image_generation", TokenPricingAbsent: true}}}), map[string]*billingpricing.ModelPricing{})

	cost, mediaErr := svc.CalculateImageCost("gemini-3-pro-image", "2K", -1, 1.0)
	if mediaErr != nil {
		t.Fatal(mediaErr)
	}
	require.Equal(t, 0.0, cost.TotalCost)
	require.Equal(t, 0.0, cost.ActualCost)
}

// TestCalculateImageCost_ZeroRateMultiplier requests a priced image with zero
// user factor. The base amount must retain its catalog size rate while the
// actual user charge remains zero.
func TestCalculateImageCost_ZeroRateMultiplier(t *testing.T) {
	svc := testkit.Calculator(newCatalogFixture(catalogFixture{pricingData: map[string]*billingpricing.CatalogModelPricing{"gemini-3-pro-image": {CatalogRules: billingpricing.CatalogRules{ImagePrices: map[string]float64{"1K": 0.134, "2K": 0.201, "4K": 0.268}}, OutputCostPerImage: 0.134, ImagePricePresent: true, Mode: "image_generation", TokenPricingAbsent: true}}}), map[string]*billingpricing.ModelPricing{})

	cost, mediaErr := svc.CalculateImageCost("gemini-3-pro-image", "2K", 1, 0)
	if mediaErr != nil {
		t.Fatal(mediaErr)
	}
	// Allow float multiplication residue in these scaled component comparisons.
	require.InDelta(t, 0.201, cost.TotalCost, 0.0001)
	require.InDelta(t, 0.0, cost.ActualCost, 1e-10)
}

// TestGetDefaultImagePrice_UnknownModel queries supported sizes for a full
// model absent from the default historical/media fixture. Each positive-count
// calculation must return nil cost with the shared pricing-unavailable error.
func TestGetDefaultImagePrice_UnknownModel(t *testing.T) {
	svc := testkit.Calculator(nil, nil)
	for _, size := range []string{"1K", "2K", "4K"} {
		cost, err := svc.CalculateImageCost("gemini-3-pro-image", size, 1, 1)
		require.ErrorIs(t, err, billingpricing.ErrModelPricingUnavailable)
		require.Nil(t, cost)
	}
}

// TestCalculateCostUnifiedAppliesPricingTimeMultiplierToTokenBuckets supplies
// a UTC instant matching the configured Shanghai morning schedule. Both token
// buckets must receive its doubled factor before the final tripled user
// charge.
func TestCalculateCostUnifiedAppliesPricingTimeMultiplierToTokenBuckets(t *testing.T) {
	groupID := int64(71)
	pricing := &routing.ModelPricingEntry{
		BillingMode: routing.BillingModeToken,
		InputPrice:  floatPtr(5e-6),
		TimePricing: &routing.TimePricingConfig{
			Timezone: "Asia/Shanghai",
			Periods:  []routing.TimePricingPeriod{{StartTime: "09:00", EndTime: "12:00", Multiplier: 2}},
		},
	}
	resolved := &billingpricing.ResolvedPricing{
		Mode:          routing.BillingModeToken,
		Source:        billingpricing.PricingSourceConfig,
		BasePricing:   &billingpricing.ModelPricing{InputPricePerToken: 5e-6, OutputPricePerToken: 15e-6},
		ConfigPricing: pricing,
	}
	service := testkit.Calculator(nil, nil)
	resolver := billing.NewPriceResolver(nil, service, modelidentity.Identity, nil)

	cost, err := service.CalculateCostUnified(billing.CostInput{
		Ctx:            context.Background(),
		Model:          "claude-sonnet-4",
		GroupID:        &groupID,
		Tokens:         billingpricing.UsageTokens{InputTokens: 100, OutputTokens: 10},
		RateMultiplier: 3,
		// The fixture instant is 09:00 in the configured Shanghai zone, placing this
		// call at the schedule's inclusive start for the selected billing mode.
		PricingAt: time.Date(2026, 6, 29, 1, 0, 0, 0, time.UTC),
		Resolver:  resolver,
		Resolved:  resolved,
	})
	require.NoError(t, err)
	want := (100*5e-6 + 10*15e-6) * 2
	// Allow float multiplication residue in these scaled component comparisons.
	require.InDelta(t, want, cost.TotalCost, 1e-12)
	require.InDelta(t, want*3, cost.ActualCost, 1e-12)
}

// TestCalculateCostUnifiedDoesNotApplyConfigTimeMultiplierToPerRequest
// supplies that same schedule instant with request-mode pricing. Three units
// must retain their ordinary request total and doubled user charge despite the
// token-only time rule.
func TestCalculateCostUnifiedDoesNotApplyConfigTimeMultiplierToPerRequest(t *testing.T) {
	groupID := int64(72)
	pricing := &routing.ModelPricingEntry{
		BillingMode:     routing.BillingModePerRequest,
		PerRequestPrice: floatPtr(0.05),
		TimePricing: &routing.TimePricingConfig{
			Timezone: "Asia/Shanghai",
			Periods:  []routing.TimePricingPeriod{{StartTime: "09:00", EndTime: "12:00", Multiplier: 2}},
		},
	}
	resolved := &billingpricing.ResolvedPricing{Mode: routing.BillingModePerRequest, Source: billingpricing.PricingSourceConfig, ConfigPricing: pricing, DefaultPerRequestPrice: 0.05}
	service := testkit.Calculator(nil, nil)
	resolver := billing.NewPriceResolver(nil, service, modelidentity.Identity, nil)

	cost, err := service.CalculateCostUnified(billing.CostInput{
		Ctx:            context.Background(),
		Model:          "image-model",
		GroupID:        &groupID,
		RequestCount:   3,
		RateMultiplier: 2,
		// The fixture instant is 09:00 in the configured Shanghai zone, placing this
		// call at the schedule's inclusive start for the selected billing mode.
		PricingAt: time.Date(2026, 6, 29, 1, 0, 0, 0, time.UTC),
		Resolver:  resolver,
		Resolved:  resolved,
	})
	require.NoError(t, err)
	// Allow float multiplication residue in these scaled component comparisons.
	require.InDelta(t, 0.15, cost.TotalCost, 1e-12)
	require.InDelta(t, 0.30, cost.ActualCost, 1e-12)
}

// TestDisplayPricingCarriesConfigTimePricingAndMaxReasoningMultiplier supplies
// schedule metadata, a reasoning factor and a doubled user rate. Displayed
// base input must reflect the user rate, while schedule/factor metadata remain
// separate and the schedule is a different object.
func TestDisplayPricingCarriesConfigTimePricingAndMaxReasoningMultiplier(t *testing.T) {
	maxMultiplier := 1.5
	pricing := &routing.ModelPricingEntry{
		BillingMode: routing.BillingModeToken,
		InputPrice:  floatPtr(5e-6),
		TimePricing: &routing.TimePricingConfig{
			Timezone:     "Asia/Shanghai",
			WeekdaysOnly: true,
			Periods:      []routing.TimePricingPeriod{{StartTime: "09:00", EndTime: "12:00", Multiplier: 2}},
		},
	}
	resolved := &billingpricing.ResolvedPricing{
		Mode:   routing.BillingModeToken,
		Source: billingpricing.PricingSourceConfig,
		BasePricing: &billingpricing.ModelPricing{
			InputPricePerToken:           5e-6,
			OutputPricePerToken:          15e-6,
			MaxReasoningEffortMultiplier: &maxMultiplier,
		},
		ConfigPricing: pricing,
	}
	service := testkit.Calculator(nil, nil)

	display := service.DisplayPricingWithResolvedMultipliers("claude-sonnet-4", 2, resolved)

	// Apply the supplied user factor to base display amounts while leaving
	// schedule and reasoning factors as metadata. Their later time/effort
	// selections do not inflate this default displayed bucket.
	// Allow float multiplication residue in these scaled component comparisons.
	require.InDelta(t, 10e-6, display.InputPricePerToken, 1e-15)
	require.NotNil(t, display.MaxReasoningEffortMultiplier)
	require.Equal(t, 1.5, *display.MaxReasoningEffortMultiplier)
	require.Equal(t, pricing.TimePricing, display.TimePricing)
	require.NotSame(t, pricing.TimePricing, display.TimePricing)
}

// TestDisplayPricingOmitsNeutralModifiers supplies a unit reasoning factor and
// an overlapping time schedule. Display must omit both modifier fields,
// matching their neutral or invalid contribution to the selected price view.
func TestDisplayPricingOmitsNeutralModifiers(t *testing.T) {
	neutral := 1.0
	resolved := &billingpricing.ResolvedPricing{
		Mode:   routing.BillingModeToken,
		Source: billingpricing.PricingSourceConfig,
		BasePricing: &billingpricing.ModelPricing{
			InputPricePerToken:           5e-6,
			MaxReasoningEffortMultiplier: &neutral,
		},
		ConfigPricing: &routing.ModelPricingEntry{
			BillingMode: routing.BillingModeToken,
			InputPrice:  floatPtr(5e-6),
			// Overlapping periods are omitted from display because settlement treats them as 1x.
			TimePricing: &routing.TimePricingConfig{
				Timezone: "UTC",
				Periods: []routing.TimePricingPeriod{
					{StartTime: "01:00", EndTime: "04:00", Multiplier: 2},
					{StartTime: "03:00", EndTime: "05:00", Multiplier: 3},
				},
			},
		},
	}
	service := testkit.Calculator(nil, nil)

	display := service.DisplayPricingWithResolvedMultipliers("claude-sonnet-4", 1, resolved)

	require.Nil(t, display.MaxReasoningEffortMultiplier)
	require.Nil(t, display.TimePricing)
}

// TestCalculateCostUnified_UsesContinuousMediaUnits supplies one and a half
// usage units to a fixed request-price card. The base total must use the
// fractional quantity and supplied unit rate rather than an integer request
// default.
func TestCalculateCostUnified_UsesContinuousMediaUnits(t *testing.T) {
	bs := testkit.ResolverCalculator()
	price := 0.08
	cards := []routing.ModelPricingEntry{{
		Models: []string{"grok-voice-think-fast-2.0"}, BillingMode: routing.BillingModePerRequest,
		PerRequestPrice: &price,
	}}
	r, _ := settingsResolver(bs, billingpricing.DefaultBillingSettings(), cards)
	cost, err := bs.CalculateCostUnified(billing.CostInput{
		Ctx: context.Background(), Model: "grok-voice-think-fast-2.0", GroupID: testkit.GroupID(),
		UsageUnits: 1.5, RateMultiplier: 1, Resolver: r,
	})
	require.NoError(t, err)
	// Allow float arithmetic residue in these calculated component amounts.
	require.InDelta(t, 0.12, cost.TotalCost, 1e-12)
}

// newTestCalculator builds the baseline historical/media fixture calculator
// through newCalculator(nil). Construction starts no remote refresh work; each
// call obtains fixture data for the calculation scenarios that need registered
// prices.
func newTestCalculator() *billing.Calculator {
	return newCalculator(nil)
}

// newLadderCalculator builds a calculator from the OpenAI above-threshold JSON
// fixture. Decoder and price validation failures terminate the test through
// its helper path; ordinary calculation then uses that parsed catalog.
func newLadderCalculator(t *testing.T) *billing.Calculator {
	t.Helper()
	return newCalculator(newStubCatalogFromJSON(t, openAILadderCatalogJSON))
}

// newTestPricingConfigServiceWithCache writes current LoadedAt to a nonnil
// caller fixture and constructs a configuration service. ModelConfigFromData
// rebuilds rows without reading LoadedAt and can populate GroupPolicies; this
// timestamp does not seed the production cache.
func newTestPricingConfigServiceWithCache(t *testing.T, cache *routingtestkit.ModelConfigData) *routing.PricingConfigService {
	t.Helper()

	// LoadedAt is fixture bookkeeping here: ModelConfigFromData reconstructs
	// configuration rows without reading it. The service's first cache load
	// remains independent of this caller timestamp assignment.
	cache.LoadedAt = time.Now()
	cs := routingtestkit.ModelConfigFromData(cache)
	return cs
}

// newStubCatalogFromJSON decodes and validates the supplied catalog-entry JSON
// before constructing a snapshot service. Require failures terminate the test
// helper; successful callers receive the parsed prices without starting a
// remote catalog worker.
func newStubCatalogFromJSON(t *testing.T, body string) *provider.Service {
	t.Helper()
	raw, err := billingpricing.DecodeCatalogEntries([]byte(body))
	require.NoError(t, err)
	data, diagnostics, err := billingpricing.ParsePricingEntries(raw)
	require.NoError(t, diagnostics.ValidationError())
	require.NoError(t, err)
	service := newCatalogFixture(catalogFixture{pricingData: data})
	return service
}

// floatPtr returns independently addressed float storage for optional numeric
// price fixtures.
func floatPtr(v float64) *float64 { return &v }
