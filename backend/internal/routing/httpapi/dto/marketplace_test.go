package dto

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/TokenFlux/TokenRouter/internal/billing/pricing"
	"github.com/TokenFlux/TokenRouter/internal/modelcatalog"
	"github.com/TokenFlux/TokenRouter/internal/protocol"
	"github.com/TokenFlux/TokenRouter/internal/routing"
)

// marketplace_test.go verifies JSON conversion of public model quotes and group
// metadata. Tests distinguish free prices from missing fields and confirm that
// visibility settings and protocol attributes survive the routing response mapper.

// TestMarketplaceHeaderTagVisibilityJSON converts each combination of group
// display flags. The public response carries both booleans and retains the
// configured multiplier and official-price comparison metadata.
func TestMarketplaceHeaderTagVisibilityJSON(t *testing.T) {
	for _, hideDiscount := range []bool{false, true} {
		for _, hideMultiplier := range []bool{false, true} {
			ratio := 0.2
			result := ModelMarketplaceGroupsFromRouting([]routing.ModelMarketplaceGroup{{
				ID: 1, RateMultiplier: 2, OfficialPriceRatio: &ratio,
				HideMaxDiscount: hideDiscount, HideRateMultiplier: hideMultiplier,
			}})
			body, err := json.Marshal(result[0])
			require.NoError(t, err)
			var fields map[string]any
			require.NoError(t, json.Unmarshal(body, &fields))
			require.Equal(t, hideDiscount, fields["hide_max_discount"])
			require.Equal(t, hideMultiplier, fields["hide_rate_multiplier"])
			require.Equal(t, 2.0, fields["rate_multiplier"])
			require.Equal(t, ratio, fields["official_price_ratio"])
		}
	}
}

// TestImagePriceJSONPreservesFreeAndMissingSizes serializes a quote with two
// declared sizes and one absent size. It checks that zero-valued 1K remains
// free in JSON, 2K keeps its amount, and 4K is omitted.
func TestImagePriceJSONPreservesFreeAndMissingSizes(t *testing.T) {
	row := modelMarketplacePricingFromRouting(pricing.ModelDisplayPricing{PricingMode: "image", PriceStatus: "priced", ImagePriceSizes: []string{"1K", "2K"}, ImagePrice1K: 0, ImagePrice2K: 0.1})
	body, err := json.Marshal(row)
	require.NoError(t, err)
	var fields map[string]any
	require.NoError(t, json.Unmarshal(body, &fields))
	require.Contains(t, fields, "image_price_1k")
	require.Equal(t, 0.0, fields["image_price_1k"])
	require.Equal(t, 0.1, fields["image_price_2k"])
	require.NotContains(t, fields, "image_price_4k")
}

// TestMarketplacePricingJSONIncludesTokenModifiers serializes a token quote
// with a Max multiplier and a weekday time schedule. It checks the complete
// JSON object retains the factor, timezone, weekday restriction, and
// end-of-day clock spelling.
func TestMarketplacePricingJSONIncludesTokenModifiers(t *testing.T) {
	maxMultiplier := 1.5
	row := modelMarketplacePricingFromRouting(pricing.ModelDisplayPricing{
		PricingMode:                  "token",
		PriceStatus:                  "priced",
		InputPricePerToken:           1e-6,
		MaxReasoningEffortMultiplier: &maxMultiplier,
		TimePricing: &pricing.TimePricingConfig{
			Timezone:     "Asia/Shanghai",
			WeekdaysOnly: true,
			Periods:      []pricing.TimePricingPeriod{{StartTime: "20:00", EndTime: "00:00", Multiplier: 0.5}},
		},
	})
	body, err := json.Marshal(row)
	require.NoError(t, err)
	require.JSONEq(t, `{
		"pricing_mode": "token",
		"price_status": "priced",
		"input_price_per_token": 0.000001,
		"max_reasoning_effort_multiplier": 1.5,
		"time_pricing": {
			"timezone": "Asia/Shanghai",
			"weekdays_only": true,
			"periods": [{"start_time": "20:00", "end_time": "00:00", "multiplier": 0.5}]
		}
	}`, string(body))
}

// TestMarketplacePricingJSONOmitsAbsentTokenModifiers serializes a token
// quote with neither optional modifier set. It checks that both Max effort
// and time schedule members are absent from the JSON object.
func TestMarketplacePricingJSONOmitsAbsentTokenModifiers(t *testing.T) {
	row := modelMarketplacePricingFromRouting(pricing.ModelDisplayPricing{PricingMode: "token", PriceStatus: "priced", InputPricePerToken: 1e-6})
	body, err := json.Marshal(row)
	require.NoError(t, err)
	var fields map[string]any
	require.NoError(t, json.Unmarshal(body, &fields))
	require.NotContains(t, fields, "max_reasoning_effort_multiplier")
	require.NotContains(t, fields, "time_pricing")
}

// TestMarketplaceProtocolAttributesJSON passes display unions through the public
// DTO mapper. Protocol summaries and explicit empty modalities must survive
// serialization alongside the overall attributes and legacy modality fields.
func TestMarketplaceProtocolAttributesJSON(t *testing.T) {
	text := []string{"text"}
	empty := []string{}
	presentation := modelcatalog.Presentation{Attributes: modelcatalog.Attributes{OutputModalities: &text}, RouteDifferences: true, Variants: []modelcatalog.Attributes{{OutputModalities: &text}, {OutputModalities: &empty}}}
	result := ModelMarketplaceGroupsFromRouting([]routing.ModelMarketplaceGroup{{ID: 1, Models: []routing.ModelMarketplaceModel{{
		ID: "alias", Attributes: &presentation, OutputModalities: text,
		ProtocolAttributes: map[protocol.ProtocolID]modelcatalog.Presentation{"openai_responses": {Attributes: modelcatalog.Attributes{OutputModalities: &text}}, "openai_images_generations": {Attributes: modelcatalog.Attributes{OutputModalities: &empty}}},
	}}}})
	body, err := json.Marshal(result[0].Models[0])
	require.NoError(t, err)
	var fields map[string]json.RawMessage
	require.NoError(t, json.Unmarshal(body, &fields))
	require.JSONEq(t, `["text"]`, string(fields["output_modalities"]))
	require.JSONEq(t, `{"openai_responses":{"output_modalities":["text"]},"openai_images_generations":{"output_modalities":[]}}`, string(fields["protocol_attributes"]))
	require.Contains(t, string(fields["attributes"]), `"variants"`)
	require.NotContains(t, string(body), "base_url")
	require.NotContains(t, string(body), "provider_type")
}

// TestVideoQuoteJSONPreservesFreeAndMissingPrices encodes dynamic video dimensions
// and an explicit free default. Optional fields remain absent when unavailable,
// while zero prices survive conversion and JSON serialization unchanged.
func TestVideoQuoteJSONPreservesFreeAndMissingPrices(t *testing.T) {
	zero := 0.0
	row := modelMarketplacePricingFromRouting(pricing.ModelDisplayPricing{PricingMode: "video", PriceStatus: "priced", VideoPrices: map[string]float64{"768p": 0, "2k": 0.2}, VideoPricePerSecond: &zero})
	body, err := json.Marshal(row)
	require.NoError(t, err)
	var fields map[string]any
	require.NoError(t, json.Unmarshal(body, &fields))
	require.Equal(t, map[string]any{"768p": float64(0), "2k": 0.2}, fields["video_prices"])
	require.Equal(t, float64(0), fields["video_price_per_second"])
	body, err = json.Marshal(modelMarketplacePricingFromRouting(pricing.ModelDisplayPricing{PricingMode: "unknown", PriceStatus: "unpriced"}))
	require.NoError(t, err)
	require.NotContains(t, string(body), "video_prices")
	require.NotContains(t, string(body), "video_price_per_second")
}

// TestMarketplaceExclusiveFlagJSON serializes public and private group metadata.
// Both responses contain an explicit boolean so clients can identify exclusive
// cards without inferring authorization from names, prices, or sorting positions.
func TestMarketplaceExclusiveFlagJSON(t *testing.T) {
	for _, exclusive := range []bool{false, true} {
		groups := ModelMarketplaceGroupsFromRouting([]routing.ModelMarketplaceGroup{{ID: 1, IsExclusive: exclusive}})
		encoded, err := json.Marshal(groups[0])
		require.NoError(t, err)
		var fields map[string]any
		require.NoError(t, json.Unmarshal(encoded, &fields))
		require.Equal(t, exclusive, fields["is_exclusive"])
	}
}
