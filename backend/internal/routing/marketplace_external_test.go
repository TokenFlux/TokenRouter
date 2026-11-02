package routing_test

import (
	"context"
	"errors"
	"log/slog"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/TokenFlux/TokenRouter/internal/billing"
	billingpricing "github.com/TokenFlux/TokenRouter/internal/billing/pricing"
	billingtestkit "github.com/TokenFlux/TokenRouter/internal/billing/testkit"
	"github.com/TokenFlux/TokenRouter/internal/gateway/provider/modelidentity"
	"github.com/TokenFlux/TokenRouter/internal/modelcatalog"
	"github.com/TokenFlux/TokenRouter/internal/modelcatalog/provider"
	"github.com/TokenFlux/TokenRouter/internal/modelcatalog/testkit"
	"github.com/TokenFlux/TokenRouter/internal/routing"
	"github.com/TokenFlux/TokenRouter/internal/routing/capability"
	routingtestkit "github.com/TokenFlux/TokenRouter/internal/routing/testkit"
	settingscore "github.com/TokenFlux/TokenRouter/internal/settings"
)

// marketplace_external_test.go verifies public marketplace assembly of model
// identity, pricing identity, route attributes, and observations. The batch
// fixture returns one alias for two visible groups, exposing which identities
// reach pricing and attribute callbacks and which groups receive optional
// capacity and probe-history reads.
//
// Pricing scenarios use the shared production resolver with offline catalog
// data or explicit configuration cards, covering missing prices, free
// amounts, image inputs, context intervals, and the group's displayed factor.
// Separate modality checks exercise the legacy pricing-metadata reader, while
// the route-attribute scenario supplies an OAuth Responses target. Fixture
// readers record inputs directly, allowing assertions to describe the
// assembly calls and returned presentation rather than an upstream request's
// execution.

// marketplaceLoadingProtocols supplies two client protocols for the alias fixture.
// Its first entry also forms the fixture's returned native-protocol selection.
var marketplaceLoadingProtocols = []capability.ProtocolID{capability.ProtocolAnthropicMessages, capability.ProtocolOpenAIChatCompletions}

// modelCatalogFixture supplies offline price records for snapshot-backed
// catalog construction.
type modelCatalogFixture struct {
	// pricingData is passed directly to NewServiceFromSnapshot, which retains
	// the map and its entries. Tests keep that storage stable while the catalog
	// reads it.
	pricingData map[string]*billingpricing.CatalogModelPricing // offline catalog price records
}

// marketplaceLoadingModels returns one alias for the two selected visible
// fixture groups.
type marketplaceLoadingModels struct{}

// marketplaceLoadingObservations records capacity and history queries for
// visible-group assertions.
type marketplaceLoadingObservations struct {
	capacityCalls int // recorded capacity batch count
	// capacityGroups and availabilityGroups retain the caller's ID slices
	// directly. Tests inspect them after synchronous ListPublic returns and keep
	// the supplied slices unchanged.
	capacityGroups     []int64 // borrowed queried capacity group IDs
	availabilityGroups []int64 // borrowed queried history group IDs
}

// marketplaceLoadingPrices records quote requests and legacy modality-reader
// call counts.
type marketplaceLoadingPrices struct {
	requests      []routing.MarketplaceQuoteRequest // recorded market quote inputs
	modalityCalls int                               // recorded legacy modality query count
}

// marketplaceQuoteFixture connects market display queries to a calculator and
// shared price resolver.
type marketplaceQuoteFixture struct {
	// calculator supplies legacy modality lookup even when resolver is provided.
	// newMarketplaceFixture installs this quote fixture only for a nonnil
	// calculator.
	calculator *billing.Calculator // supplied offline billing calculator
	// resolver supplies Quote when nonnil. A nil resolver causes that method to
	// construct a catalog-based resolver from calculator for the current query.
	resolver *billing.PriceResolver // optional shared pricing resolver
}

// marketplaceGroupRepoStub supplies a configured active-group list for public
// assembly tests.
type marketplaceGroupRepoStub struct {
	// GroupRepository supplies unused methods through a nil embedded interface.
	// Public assembly uses the ListActive override; another promoted call panics.
	routing.GroupRepository // unused group repository methods

	// groups is returned directly from ListActive. Tests seed the list before
	// synchronous assembly, including deliberately excluded rows whose fields
	// exercise visibility filtering.
	groups []routing.Group // supplied active listing rows
}

// marketplaceSettingRepoStub supplies stored setting strings requested during
// public assembly.
type marketplaceSettingRepoStub struct {
	// Repository supplies unused methods through a nil embedded interface. Public
	// assembly uses GetMultiple; calling another promoted repository method panics.
	settingscore.Repository                   // unused settings repository methods
	settings                map[string]string // supplied stored setting strings
}

// TestMarketplaceHeaderTagVisibility decodes stored display flags and lists a
// public group. Only boolean true hides each tag, and quote requests continue
// to receive the configured group multiplier for every flag combination.
func TestMarketplaceHeaderTagVisibility(t *testing.T) {
	for _, test := range []struct {
		name           string // named visibility scenario
		features       string // saved feature JSON
		hideDiscount   bool   // expected discount visibility flag
		hideMultiplier bool   // expected multiplier visibility flag
	}{
		{"missing", `{}`, false, false},
		{"explicit false", `{"hide_marketplace_max_discount":false,"hide_marketplace_rate_multiplier":false}`, false, false},
		{"discount", `{"hide_marketplace_max_discount":true}`, true, false},
		{"multiplier", `{"hide_marketplace_rate_multiplier":true}`, false, true},
		{"both", `{"hide_marketplace_max_discount":true,"hide_marketplace_rate_multiplier":true}`, true, true},
		{"invalid values", `{"hide_marketplace_max_discount":"true","hide_marketplace_rate_multiplier":1}`, false, false},
	} {
		t.Run(test.name, func(t *testing.T) {
			policy := routing.DecodeGroupRoutingPolicy([]byte(`{"features_config":` + test.features + `}`))
			groups := &marketplaceGroupRepoStub{groups: []routing.Group{{
				ID: 1, ActiveProviderCount: 1, RateMultiplier: 2, RoutingPolicy: policy,
			}}}
			prices := &marketplaceLoadingPrices{}
			svc := routing.NewMarketplace(groups, nil, marketplaceLoadingModels{}, routing.RequestableResolver{}, prices, nil, nil, routing.MarketplaceOptions{})
			result, err := svc.ListPublic(context.Background(), routing.MarketplaceListOptions{})
			require.NoError(t, err)
			require.Len(t, result, 1)
			require.Equal(t, test.hideDiscount, result[0].HideMaxDiscount)
			require.Equal(t, test.hideMultiplier, result[0].HideRateMultiplier)
			require.Equal(t, 2.0, result[0].RateMultiplier)
			require.Len(t, prices.requests, 1)
			require.Equal(t, 2.0, prices.requests[0].RateMultiplier)
			require.Equal(t, 2.0, result[0].Models[0].Pricing.InputPricePerToken)
		})
	}
}

// TestMarketplaceBatchAttributesAndOptionalCapacity varies attribute failure
// and capacity inclusion over mixed visible and excluded groups. It checks
// one attribute batch, quote identity, protocol forwarding, repository order,
// probe filtering, and unknown attributes after failure.
func TestMarketplaceBatchAttributesAndOptionalCapacity(t *testing.T) {
	for _, attributesFail := range []bool{false, true} {
		for _, includeCapacity := range []bool{false, true} {
			groups := &marketplaceGroupRepoStub{groups: []routing.Group{
				{ID: 2, ActiveProviderCount: 1, RateMultiplier: 2},
				{ID: 1, ActiveProviderCount: 1, RateMultiplier: 1, AvailabilityProbeConfig: routing.GroupAvailabilityProbeConfig{Enabled: true}},
				{ID: 3, ActiveProviderCount: 1},
				{ID: 4, ActiveProviderCount: 1, IsExclusive: true},
				{ID: 5},
			}}
			observations := &marketplaceLoadingObservations{}
			prices := &marketplaceLoadingPrices{}
			calls := 0
			name := "display"
			modalities := []string{"image"}
			options := routing.MarketplaceOptions{
				Now:  time.Now,
				Warn: func(string, ...any) {},
				Attributes: func(_ context.Context, models map[int64][]routing.RequestableModel) (map[int64]map[string]routing.MarketplaceModelAttributes, error) {
					calls++
					require.Len(t, models, 2)
					for _, id := range []int64{1, 2} {
						require.Equal(t, []routing.RequestableModel{{ID: "alias", UpstreamModels: []string{"upstream"}}}, models[id])
					}
					if attributesFail {
						return nil, errors.New("database unavailable")
					}
					return map[int64]map[string]routing.MarketplaceModelAttributes{1: {"alias": {Presentation: modelcatalog.Presentation{Attributes: modelcatalog.Attributes{DisplayName: &name, InputModalities: &modalities}}}}}, nil
				},
			}
			svc := routing.NewMarketplace(groups, nil, marketplaceLoadingModels{}, routing.RequestableResolver{}, prices, observations, observations, options)
			result, err := svc.ListPublic(context.Background(), routing.MarketplaceListOptions{IncludeCapacity: includeCapacity})
			require.NoError(t, err)
			require.Len(t, result, 2)
			require.Equal(t, int64(2), result[0].ID)
			require.Equal(t, int64(1), result[1].ID)
			require.Equal(t, 1, calls)
			require.Zero(t, prices.modalityCalls)
			require.Len(t, prices.requests, 2)
			require.Equal(t, "priced", prices.requests[0].Model)
			require.Equal(t, float64(2), result[0].Models[0].Pricing.InputPricePerToken)
			require.Equal(t, marketplaceLoadingProtocols, result[0].Models[0].Protocols)
			require.Equal(t, marketplaceLoadingProtocols[:1], result[0].Models[0].NativeProtocols)
			require.Equal(t, []int64{1}, observations.availabilityGroups)
			require.NotNil(t, result[1].Availability)
			if includeCapacity {
				require.Equal(t, 1, observations.capacityCalls)
				require.Equal(t, []int64{2, 1}, observations.capacityGroups)
				require.NotNil(t, result[1].Capacity)
			} else {
				require.Zero(t, observations.capacityCalls)
				require.Nil(t, result[1].Capacity)
			}
			if attributesFail {
				require.Nil(t, result[1].Models[0].Attributes)
				require.Nil(t, result[1].Models[0].InputModalities)
			} else {
				require.Equal(t, name, result[1].Models[0].DisplayName)
				require.Equal(t, modalities, result[1].Models[0].InputModalities)
			}
		}
	}
}

// TestParseMarketplaceAvailabilityWindowSettings supplies missing, valid,
// invalid, and oversized history settings. Assertions check defaults and
// widening a ninety-day window's buckets to one hundred eighty minutes.
func TestParseMarketplaceAvailabilityWindowSettings(t *testing.T) {
	tests := []struct {
		name              string            // subtest window settings label
		settings          map[string]string // supplied stored window strings
		wantWindowDays    int               // expected displayed history days
		wantBucketMinutes int               // expected history bucket minutes
	}{
		{
			name:              "missing settings use defaults",
			settings:          nil,
			wantWindowDays:    routing.DefaultMarketplaceAvailabilityWindowDays,
			wantBucketMinutes: routing.DefaultMarketplaceAvailabilityBucketMinutes,
		},
		{
			name: "uses stored settings",
			settings: map[string]string{
				routing.SettingKeyMarketplaceAvailabilityWindowDays:    "14",
				routing.SettingKeyMarketplaceAvailabilityBucketMinutes: "60",
			},
			wantWindowDays:    14,
			wantBucketMinutes: 60,
		},
		{
			name: "invalid settings fall back to defaults",
			settings: map[string]string{
				routing.SettingKeyMarketplaceAvailabilityWindowDays:    "-1",
				routing.SettingKeyMarketplaceAvailabilityBucketMinutes: "0",
			},
			wantWindowDays:    routing.DefaultMarketplaceAvailabilityWindowDays,
			wantBucketMinutes: routing.DefaultMarketplaceAvailabilityBucketMinutes,
		},
		{
			name: "bucket count is capped by widening bucket",
			settings: map[string]string{
				routing.SettingKeyMarketplaceAvailabilityWindowDays:    "90",
				routing.SettingKeyMarketplaceAvailabilityBucketMinutes: "5",
			},
			wantWindowDays:    90,
			wantBucketMinutes: 180,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			gotWindowDays, gotBucketMinutes := routing.ParseMarketplaceAvailabilityWindowSettings(tt.settings)
			if gotWindowDays != tt.wantWindowDays || gotBucketMinutes != tt.wantBucketMinutes {
				t.Fatalf("parseMarketplaceAvailabilityWindowSettings() = (%d, %d), want (%d, %d)", gotWindowDays, gotBucketMinutes, tt.wantWindowDays, tt.wantBucketMinutes)
			}
		})
	}
}

// TestModelMarketplaceQoderModelUsesStandardPricing asks the offline catalog
// for a known Claude model through PublicModelPricing. It checks positive
// token input and output amounts and priced status.
func TestModelMarketplaceQoderModelUsesStandardPricing(t *testing.T) {
	svc := newMarketplaceFixture(nil, nil, newMarketplaceCalculator(nil, nil), nil)
	group := &routing.Group{ID: 1, RateMultiplier: 1}

	pricing := svc.PublicModelPricing(context.Background(), group, "claude-sonnet-4")

	if pricing.PricingMode != "token" || pricing.PriceStatus != "priced" || pricing.InputPricePerToken <= 0 || pricing.OutputPricePerToken <= 0 {
		t.Fatalf("Qoder model pricing = (%q, %q, %g, %g), want token/priced with standard prices",
			pricing.PricingMode, pricing.PriceStatus, pricing.InputPricePerToken, pricing.OutputPricePerToken)
	}
}

// TestModelMarketplaceQoderGroupMappedBasisDoesNotUseRequestedStandardPricing
// supplies a requestable definition whose pricing model is an unpriced mapped
// target. Display pricing remains unknown even though the public request name
// has a catalog price.
func TestModelMarketplaceQoderGroupMappedBasisDoesNotUseRequestedStandardPricing(t *testing.T) {
	groupID := int64(902)
	cache := routingtestkit.NewModelConfigData()
	cache.Models[routingtestkit.ModelKey{GroupID: groupID, Platform: capability.PlatformQoder, Model: "gpt-5.4"}] = "qmodel"
	cache.ByGroup[groupID] = &routingtestkit.Configuration{ID: groupID, Status: billing.StatusActive, BillingModelSource: routing.BillingModelSourceGroupMapped}
	cache.Platforms[groupID] = capability.PlatformQoder
	cache.LoadedAt = time.Now()

	pricingConfigService := routingtestkit.ModelConfigFromData(cache)

	billingService := newMarketplaceCalculator(nil, nil)
	svc := newMarketplaceFixture(nil, nil,

		billingService, NewModelPricingResolver(pricingConfigService, billingService),
	)
	group := &routing.Group{ID: groupID, RateMultiplier: 1}

	pricing := svc.RequestableModelPricing(context.Background(), group, routing.MarketplaceModelDef{ID: "gpt-5.4", PricingModel: "qmodel"})

	if pricing.PricingMode != "unknown" || pricing.PriceStatus != "unpriced" {
		t.Fatalf("Qoder channel-mapped pricing = (%q, %q, intervals=%d), want unknown/unpriced",
			pricing.PricingMode, pricing.PriceStatus, len(pricing.ContextIntervals))
	}
}

// TestModelMarketplaceQoderUpstreamBasisDoesNotUseRequestedStandardPricing
// supplies upstream-source metadata and an unpriced pricing-model target. The
// returned presentation stays unknown instead of borrowing the public request
// name's catalog amount.
func TestModelMarketplaceQoderUpstreamBasisDoesNotUseRequestedStandardPricing(t *testing.T) {
	groupID := int64(902)
	cache := routingtestkit.NewModelConfigData()
	cache.Models[routingtestkit.ModelKey{GroupID: groupID, Platform: capability.PlatformQoder, Model: "gpt-5.4-mini"}] = "qmodel"
	cache.ByGroup[groupID] = &routingtestkit.Configuration{ID: groupID, Status: billing.StatusActive, BillingModelSource: routing.BillingModelSourceUpstream}
	cache.Platforms[groupID] = capability.PlatformQoder
	cache.LoadedAt = time.Now()

	pricingConfigService := routingtestkit.ModelConfigFromData(cache)

	billingService := newMarketplaceCalculator(nil, nil)
	svc := newMarketplaceFixture(nil, nil,

		billingService, NewModelPricingResolver(pricingConfigService, billingService),
	)
	group := &routing.Group{ID: groupID, RateMultiplier: 1}

	pricing := svc.RequestableModelPricing(context.Background(), group, routing.MarketplaceModelDef{ID: "gpt-5.4-mini", PricingModel: "qmodel"})

	if pricing.PricingMode != "unknown" || pricing.PriceStatus != "unpriced" {
		t.Fatalf("Qoder upstream route-key source pricing = (%q, %q, %g, %g), want unknown/unpriced",
			pricing.PricingMode, pricing.PriceStatus, pricing.InputPricePerToken, pricing.OutputPricePerToken)
	}
}

// TestModelMarketplaceQoderCustomImageAliasWithoutManualPricingRemainsUnknown
// assigns an unpriced target to a custom image-named alias. The requestable
// definition produces unknown pricing, so the alias spelling supplies no
// image amount.
func TestModelMarketplaceQoderCustomImageAliasWithoutManualPricingRemainsUnknown(t *testing.T) {
	groupID := int64(902)
	cache := routingtestkit.NewModelConfigData()
	cache.Models[routingtestkit.ModelKey{GroupID: groupID, Platform: capability.PlatformQoder, Model: "custom-image-alias"}] = "qmodel"
	cache.ByGroup[groupID] = &routingtestkit.Configuration{ID: groupID, Status: billing.StatusActive, BillingModelSource: routing.BillingModelSourceGroupMapped}
	cache.Platforms[groupID] = capability.PlatformQoder
	cache.LoadedAt = time.Now()

	pricingConfigService := routingtestkit.ModelConfigFromData(cache)

	billingService := newMarketplaceCalculator(nil, nil)
	svc := newMarketplaceFixture(nil, nil,

		billingService, NewModelPricingResolver(pricingConfigService, billingService),
	)
	group := &routing.Group{ID: groupID, RateMultiplier: 1}

	pricing := svc.RequestableModelPricing(context.Background(), group, routing.MarketplaceModelDef{ID: "custom-image-alias", PricingModel: "qmodel"})

	if pricing.PricingMode != "unknown" || pricing.PriceStatus != "unpriced" {
		t.Fatalf("Qoder custom image alias pricing = (%q, %q), want unknown/unpriced", pricing.PricingMode, pricing.PriceStatus)
	}
}

// TestModelMarketplaceQoderAliasesWithoutAnyBasePricingRemainUnknown queries
// three names absent from the offline price fixture. Every displayed result
// for those full model IDs retains unknown mode and unpriced status.
func TestModelMarketplaceQoderAliasesWithoutAnyBasePricingRemainUnknown(t *testing.T) {
	billingService := newMarketplaceCalculator(nil, nil)
	svc := newMarketplaceFixture(nil, nil, billingService, nil)
	group := &routing.Group{ID: 1, RateMultiplier: 1.25}

	for _, model := range []string{"auto", "qwen3.8-max", "qmodel_38max"} {
		pricing := svc.PublicModelPricing(context.Background(), group, model)
		if pricing.PricingMode != "unknown" || pricing.PriceStatus != "unpriced" {
			t.Fatalf("Qoder model %s pricing = (%q, %q), want unknown/unpriced", model, pricing.PricingMode, pricing.PriceStatus)
		}
	}
}

// TestModelMarketplaceQoderManualConfigPricingOverridesDefaultAliasDisplayPricing
// supplies explicit input and output amounts for the auto request name.
// Public pricing returns those exact configured token values.
func TestModelMarketplaceQoderManualConfigPricingOverridesDefaultAliasDisplayPricing(t *testing.T) {
	groupID := int64(902)
	inputPrice := 0.01
	outputPrice := 0.02
	cache := routingtestkit.NewModelConfigData()
	cache.Prices[routingtestkit.ModelKey{GroupID: groupID, Platform: capability.PlatformQoder, Model: "auto"}] = &routing.ModelPricingEntry{
		BillingMode: routing.BillingModeToken,
		InputPrice:  &inputPrice,
		OutputPrice: &outputPrice,
	}
	cache.ByGroup[groupID] = &routingtestkit.Configuration{ID: groupID, Status: billing.StatusActive}
	cache.Platforms[groupID] = capability.PlatformQoder
	cache.LoadedAt = time.Now()

	pricingConfigService := routingtestkit.ModelConfigFromData(cache)

	billingService := newMarketplaceCalculator(nil, nil)
	svc := newMarketplaceFixture(nil, nil,

		billingService, NewModelPricingResolver(pricingConfigService, billingService),
	)
	group := &routing.Group{ID: groupID, RateMultiplier: 1}

	pricing := svc.PublicModelPricing(context.Background(), group, "auto")

	if pricing.InputPricePerToken != inputPrice || pricing.OutputPricePerToken != outputPrice {
		t.Fatalf("Qoder manual alias price = (%g, %g), want (%g, %g)", pricing.InputPricePerToken, pricing.OutputPricePerToken, inputPrice, outputPrice)
	}
}

// TestModelMarketplacePricingConfigImageInputPricingIsDisplayed supplies
// token prices with a separate image-input amount and a group factor of 1.5.
// The quote reports priced token mode and an image-input rate scaled by the
// group factor.
func TestModelMarketplacePricingConfigImageInputPricingIsDisplayed(t *testing.T) {
	groupID := int64(904)
	inputPrice := 0.01
	imageInputPrice := 0.03
	outputPrice := 0.02
	cache := routingtestkit.NewModelConfigData()
	cache.Prices[routingtestkit.ModelKey{GroupID: groupID, Platform: capability.PlatformOpenAI, Model: "gpt-image-edit"}] = &routing.ModelPricingEntry{
		BillingMode:     routing.BillingModeToken,
		InputPrice:      &inputPrice,
		ImageInputPrice: &imageInputPrice,
		OutputPrice:     &outputPrice,
	}
	cache.ByGroup[groupID] = &routingtestkit.Configuration{ID: groupID, Status: billing.StatusActive}
	cache.Platforms[groupID] = capability.PlatformOpenAI
	cache.LoadedAt = time.Now()

	pricingConfigService := routingtestkit.ModelConfigFromData(cache)

	billingService := newMarketplaceCalculator(nil, nil)
	svc := newMarketplaceFixture(nil, nil,

		billingService, NewModelPricingResolver(pricingConfigService, billingService),
	)
	group := &routing.Group{ID: groupID, RateMultiplier: 1.5}

	pricing := svc.PublicModelPricing(context.Background(), group, "gpt-image-edit")

	if pricing.PricingMode != "token" || pricing.PriceStatus != "priced" {
		t.Fatalf("image edit pricing = (%q, %q), want token/priced", pricing.PricingMode, pricing.PriceStatus)
	}
	if pricing.ImageInputPricePerToken != imageInputPrice*group.RateMultiplier {
		t.Fatalf("image input price = %g, want %g", pricing.ImageInputPricePerToken, imageInputPrice*group.RateMultiplier)
	}
}

// TestModelDisplayPricingImageInputFastRates compares a priority multiplier,
// independent priority text price, and configuration Fast multiplier.
// Standard and Fast image-input fields follow the corresponding supplied
// image-price rule and group factor.
func TestModelDisplayPricingImageInputFastRates(t *testing.T) {
	fastModeMultiplier := 3.0
	priorityMultiplier := 2.0
	tests := []struct {
		name          string                      // subtest image rate label
		pricing       billingpricing.ModelPricing // supplied token price record
		wantImage     float64                     // expected standard image token rate
		wantFastImage float64                     // expected Fast image token rate
	}{
		{
			name: "显式 priority 倍率同步应用到图片输入价",
			pricing: billingpricing.ModelPricing{
				InputPricePerToken:      0.01,
				ImageInputPricePerToken: 0.03,
				SupportsServiceTier:     true,
				FastMultiplier:          &priorityMultiplier,
			},
			wantImage:     0.06,
			wantFastImage: 0.12,
		},
		{
			name: "独立 priority 文本价不改变显式图片输入价",
			pricing: billingpricing.ModelPricing{
				InputPricePerToken:         0.01,
				InputPricePerTokenPriority: 0.04,
				ImageInputPricePerToken:    0.03,
			},
			wantImage:     0.06,
			wantFastImage: 0.06,
		},
		{
			name: "共享价格配置 Fast 倍率同步应用到显式图片输入价",
			pricing: billingpricing.ModelPricing{
				InputPricePerToken:      0.01,
				ImageInputPricePerToken: 0.03,
				FastModeMultiplier:      &fastModeMultiplier,
			},
			wantImage:     0.06,
			wantFastImage: 0.18,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			pricing := billingpricing.BuildTokenDisplayPricing(&tt.pricing, 2)
			if pricing.ImageInputPricePerToken != tt.wantImage {
				t.Fatalf("image input price = %g, want %g", pricing.ImageInputPricePerToken, tt.wantImage)
			}
			if pricing.FastImageInputPricePerToken != tt.wantFastImage {
				t.Fatalf("fast image input price = %g, want %g", pricing.FastImageInputPricePerToken, tt.wantFastImage)
			}
		})
	}
}

// TestModelMarketplaceQoderBlankConfigPricingRemainsUnknown supplies a token
// card with no price buckets for auto. The missing catalog amount and blank
// card yield unknown mode and unpriced status.
func TestModelMarketplaceQoderBlankConfigPricingRemainsUnknown(t *testing.T) {
	groupID := int64(902)
	cache := routingtestkit.NewModelConfigData()
	cache.Prices[routingtestkit.ModelKey{GroupID: groupID, Platform: capability.PlatformQoder, Model: "auto"}] = &routing.ModelPricingEntry{
		BillingMode: routing.BillingModeToken,
	}
	cache.ByGroup[groupID] = &routingtestkit.Configuration{ID: groupID, Status: billing.StatusActive}
	cache.Platforms[groupID] = capability.PlatformQoder
	cache.LoadedAt = time.Now()

	pricingConfigService := routingtestkit.ModelConfigFromData(cache)

	billingService := newMarketplaceCalculator(nil, nil)
	svc := newMarketplaceFixture(nil, nil,

		billingService, NewModelPricingResolver(pricingConfigService, billingService),
	)
	group := &routing.Group{ID: groupID, RateMultiplier: 1}

	pricing := svc.PublicModelPricing(context.Background(), group, "auto")

	if pricing.PricingMode != "unknown" || pricing.PriceStatus != "unpriced" {
		t.Fatalf("Qoder blank channel alias pricing = (%q, %q), want unknown/unpriced", pricing.PricingMode, pricing.PriceStatus)
	}
}

// TestModelMarketplaceQoderBlankRouteKeyPricingShowsAliasManualPricing
// supplies a blank qmodel card and explicit prices for qwen3.7-plus. Direct
// public pricing for that name returns its configured amounts.
func TestModelMarketplaceQoderBlankRouteKeyPricingShowsAliasManualPricing(t *testing.T) {
	groupID := int64(902)
	aliasInputPrice := 0.01
	aliasOutputPrice := 0.02
	cache := routingtestkit.NewModelConfigData()
	cache.Prices[routingtestkit.ModelKey{GroupID: groupID, Platform: capability.PlatformQoder, Model: "qmodel"}] = &routing.ModelPricingEntry{
		BillingMode: routing.BillingModeToken,
	}
	cache.Prices[routingtestkit.ModelKey{GroupID: groupID, Platform: capability.PlatformQoder, Model: "qwen3.7-plus"}] = &routing.ModelPricingEntry{
		BillingMode: routing.BillingModeToken,
		InputPrice:  &aliasInputPrice,
		OutputPrice: &aliasOutputPrice,
	}
	cache.ByGroup[groupID] = &routingtestkit.Configuration{ID: groupID, Status: billing.StatusActive}
	cache.Platforms[groupID] = capability.PlatformQoder
	cache.LoadedAt = time.Now()

	pricingConfigService := routingtestkit.ModelConfigFromData(cache)

	billingService := newMarketplaceCalculator(nil, nil)
	svc := newMarketplaceFixture(nil, nil,

		billingService, NewModelPricingResolver(pricingConfigService, billingService),
	)
	group := &routing.Group{ID: groupID, RateMultiplier: 1}

	pricing := svc.PublicModelPricing(context.Background(), group, "qwen3.7-plus")

	if pricing.InputPricePerToken != aliasInputPrice || pricing.OutputPricePerToken != aliasOutputPrice {
		t.Fatalf("Qoder alias display price = (%g, %g), want (%g, %g)", pricing.InputPricePerToken, pricing.OutputPricePerToken, aliasInputPrice, aliasOutputPrice)
	}
}

// TestModelMarketplaceQoderRequestedBasisDoesNotInferRouteKeyPricing stores
// prices for qmodel and queries qwen3.7-plus directly. The unmatched request
// identity remains unknown despite a priced different name.
func TestModelMarketplaceQoderRequestedBasisDoesNotInferRouteKeyPricing(t *testing.T) {
	groupID := int64(902)
	inputPrice := 0.01
	outputPrice := 0.02
	cache := routingtestkit.NewModelConfigData()
	cache.Prices[routingtestkit.ModelKey{GroupID: groupID, Platform: capability.PlatformQoder, Model: "qmodel"}] = &routing.ModelPricingEntry{
		BillingMode: routing.BillingModeToken,
		InputPrice:  &inputPrice,
		OutputPrice: &outputPrice,
	}
	cache.ByGroup[groupID] = &routingtestkit.Configuration{ID: groupID, Status: billing.StatusActive}
	cache.Platforms[groupID] = capability.PlatformQoder
	cache.LoadedAt = time.Now()

	pricingConfigService := routingtestkit.ModelConfigFromData(cache)

	billingService := newMarketplaceCalculator(nil, nil)
	svc := newMarketplaceFixture(nil, nil,

		billingService, NewModelPricingResolver(pricingConfigService, billingService),
	)
	group := &routing.Group{ID: groupID, RateMultiplier: 1}

	pricing := svc.PublicModelPricing(context.Background(), group, "qwen3.7-plus")

	if pricing.PricingMode != "unknown" || pricing.PriceStatus != "unpriced" {
		t.Fatalf("Qoder requested-basis display price = (%q, %q), want unknown/unpriced", pricing.PricingMode, pricing.PriceStatus)
	}
}

// TestModelMarketplaceQoderAliasManualPricingOverridesRouteKeyManualPricing
// supplies different amounts for qmodel and qwen3.7-plus. Direct public
// pricing for qwen3.7-plus returns its own input and output values.
func TestModelMarketplaceQoderAliasManualPricingOverridesRouteKeyManualPricing(t *testing.T) {
	groupID := int64(902)
	aliasInputPrice := 0.01
	aliasOutputPrice := 0.02
	routeInputPrice := 0.50
	routeOutputPrice := 0.75
	cache := routingtestkit.NewModelConfigData()
	cache.Prices[routingtestkit.ModelKey{GroupID: groupID, Platform: capability.PlatformQoder, Model: "qmodel"}] = &routing.ModelPricingEntry{
		BillingMode: routing.BillingModeToken,
		InputPrice:  &routeInputPrice,
		OutputPrice: &routeOutputPrice,
	}
	cache.Prices[routingtestkit.ModelKey{GroupID: groupID, Platform: capability.PlatformQoder, Model: "qwen3.7-plus"}] = &routing.ModelPricingEntry{
		BillingMode: routing.BillingModeToken,
		InputPrice:  &aliasInputPrice,
		OutputPrice: &aliasOutputPrice,
	}
	cache.ByGroup[groupID] = &routingtestkit.Configuration{ID: groupID, Status: billing.StatusActive}
	cache.Platforms[groupID] = capability.PlatformQoder
	cache.LoadedAt = time.Now()

	pricingConfigService := routingtestkit.ModelConfigFromData(cache)

	billingService := newMarketplaceCalculator(nil, nil)
	svc := newMarketplaceFixture(nil, nil,

		billingService, NewModelPricingResolver(pricingConfigService, billingService),
	)
	group := &routing.Group{ID: groupID, RateMultiplier: 1}

	pricing := svc.PublicModelPricing(context.Background(), group, "qwen3.7-plus")

	if pricing.InputPricePerToken != aliasInputPrice || pricing.OutputPricePerToken != aliasOutputPrice {
		t.Fatalf("Qoder alias display price = (%g, %g), want (%g, %g)", pricing.InputPricePerToken, pricing.OutputPricePerToken, aliasInputPrice, aliasOutputPrice)
	}
}

// TestModelMarketplaceQoderNonUniformIntervalsDisplayAsContextIntervals
// supplies two token ranges with different prices for one model. It checks
// priced token mode, two displayed ranges, and their distinct input amounts.
func TestModelMarketplaceQoderNonUniformIntervalsDisplayAsContextIntervals(t *testing.T) {
	groupID := int64(902)
	firstInput := 0.01
	firstOutput := 0.02
	secondInput := 0.03
	secondOutput := 0.04
	maxTokens := 100
	cache := routingtestkit.NewModelConfigData()
	cache.Prices[routingtestkit.ModelKey{GroupID: groupID, Platform: capability.PlatformQoder, Model: "qwen3.7-plus"}] = &routing.ModelPricingEntry{
		BillingMode: routing.BillingModeToken,
		Intervals: []routing.PricingInterval{
			{MinTokens: 0, MaxTokens: &maxTokens, InputPrice: &firstInput, OutputPrice: &firstOutput},
			{MinTokens: maxTokens, InputPrice: &secondInput, OutputPrice: &secondOutput},
		},
	}
	cache.ByGroup[groupID] = &routingtestkit.Configuration{ID: groupID, Status: billing.StatusActive}
	cache.Platforms[groupID] = capability.PlatformQoder
	cache.LoadedAt = time.Now()

	pricingConfigService := routingtestkit.ModelConfigFromData(cache)

	billingService := newMarketplaceCalculator(nil, nil)
	svc := newMarketplaceFixture(nil, nil,

		billingService, NewModelPricingResolver(pricingConfigService, billingService),
	)
	group := &routing.Group{ID: groupID, RateMultiplier: 1}

	pricing := svc.PublicModelPricing(context.Background(), group, "qwen3.7-plus")

	if pricing.PricingMode != "token" || pricing.PriceStatus != "priced" {
		t.Fatalf("Qoder interval display pricing = (%q, %q), want token/priced", pricing.PricingMode, pricing.PriceStatus)
	}
	if len(pricing.ContextIntervals) != 2 {
		t.Fatalf("ContextIntervals len = %d, want 2: %#v", len(pricing.ContextIntervals), pricing.ContextIntervals)
	}
	if pricing.ContextIntervals[0].InputPricePerToken != firstInput || pricing.ContextIntervals[1].InputPricePerToken != secondInput {
		t.Fatalf("interval input prices = (%g, %g), want (%g, %g)", pricing.ContextIntervals[0].InputPricePerToken, pricing.ContextIntervals[1].InputPricePerToken, firstInput, secondInput)
	}
}

// TestModelMarketplaceQoderStandardModelPartialIntervalKeepsBaseDisplayFields
// overrides input price in one interval for a catalog model. The displayed
// input changes while output matches the offline catalog's base amount.
func TestModelMarketplaceQoderStandardModelPartialIntervalKeepsBaseDisplayFields(t *testing.T) {
	groupID := int64(902)
	inputPrice := 0.01
	cache := routingtestkit.NewModelConfigData()
	cache.Prices[routingtestkit.ModelKey{GroupID: groupID, Platform: capability.PlatformQoder, Model: "gpt-5.4"}] = &routing.ModelPricingEntry{
		BillingMode: routing.BillingModeToken,
		Intervals: []routing.PricingInterval{
			{MinTokens: 0, InputPrice: &inputPrice},
		},
	}
	cache.ByGroup[groupID] = &routingtestkit.Configuration{ID: groupID, Status: billing.StatusActive}
	cache.Platforms[groupID] = capability.PlatformQoder
	cache.LoadedAt = time.Now()

	pricingConfigService := routingtestkit.ModelConfigFromData(cache)

	billingService := newMarketplaceCalculator(nil, nil)
	basePricing, err := billingService.GetModelPricing("gpt-5.4")
	if err != nil {
		t.Fatalf("GetModelPricing(gpt-5.4) error = %v", err)
	}
	svc := newMarketplaceFixture(nil, nil,

		billingService, NewModelPricingResolver(pricingConfigService, billingService),
	)
	group := &routing.Group{ID: groupID, RateMultiplier: 1}

	pricing := svc.PublicModelPricing(context.Background(), group, "gpt-5.4")

	if pricing.InputPricePerToken != inputPrice || pricing.OutputPricePerToken != basePricing.OutputPricePerToken {
		t.Fatalf("Qoder standard partial interval display price = (%g, %g), want (%g, %g)",
			pricing.InputPricePerToken, pricing.OutputPricePerToken, inputPrice, basePricing.OutputPricePerToken)
	}
}

// TestModelMarketplaceUsesConfigPricingWithGroupMultiplier supplies explicit
// input and output card amounts and a group factor of two. Both displayed
// token amounts equal their configured values multiplied by that factor.
func TestModelMarketplaceUsesConfigPricingWithGroupMultiplier(t *testing.T) {
	groupID := int64(905)
	pricingConfigInput := 0.5
	pricingConfigOutput := 0.75
	cache := routingtestkit.NewModelConfigData()
	cache.Prices[routingtestkit.ModelKey{GroupID: groupID, Platform: capability.PlatformOpenAI, Model: "gpt-5.4-mini"}] = &routing.ModelPricingEntry{
		BillingMode: routing.BillingModeToken,
		InputPrice:  &pricingConfigInput,
		OutputPrice: &pricingConfigOutput,
	}
	cache.ByGroup[groupID] = &routingtestkit.Configuration{ID: groupID, Status: billing.StatusActive}
	cache.Platforms[groupID] = capability.PlatformOpenAI
	cache.LoadedAt = time.Now()

	pricingConfigService := routingtestkit.ModelConfigFromData(cache)

	billingService := newMarketplaceCalculator(nil, nil)
	svc := newMarketplaceFixture(nil, nil,

		billingService, NewModelPricingResolver(pricingConfigService, billingService),
	)
	group := &routing.Group{ID: groupID, RateMultiplier: 2}

	pricing := svc.PublicModelPricing(context.Background(), group, "gpt-5.4-mini")

	if pricing.InputPricePerToken != pricingConfigInput*group.RateMultiplier || pricing.OutputPricePerToken != pricingConfigOutput*group.RateMultiplier {
		t.Fatalf("group display price = (%g, %g), want (%g, %g)",
			pricing.InputPricePerToken, pricing.OutputPricePerToken,
			pricingConfigInput*group.RateMultiplier, pricingConfigOutput*group.RateMultiplier)
	}
}

// TestModelMarketplaceGroupExplicitZeroPricingRemainsPriced supplies explicit
// zero input and output prices for a catalog model. The display remains token
// and priced with free amounts.
func TestModelMarketplaceGroupExplicitZeroPricingRemainsPriced(t *testing.T) {
	zero := 0.0
	billingService := newMarketplaceCalculator(nil, nil)
	settings := billingpricing.DefaultBillingSettings()
	svc := marketplaceWithConfig(billingService, 906, settings, []routing.ModelPricingEntry{{Models: []string{"gpt-5.4"}, BillingMode: routing.BillingModeToken, InputPrice: &zero, OutputPrice: &zero}})
	group := &routing.Group{ID: 906, RateMultiplier: 1}

	pricing := svc.PublicModelPricing(context.Background(), group, "gpt-5.4")

	if pricing.PricingMode != "token" || pricing.PriceStatus != "priced" || pricing.InputPricePerToken != 0 || pricing.OutputPricePerToken != 0 {
		t.Fatalf("free group display pricing = %#v, want token/priced with zero prices", pricing)
	}
}

// TestModelMarketplaceGroupCanDisableBuiltInLongContextDisplay disables
// catalog context pricing in shared settings without supplying model cards.
// The known model remains priced token usage and exposes no context
// intervals.
func TestModelMarketplaceGroupCanDisableBuiltInLongContextDisplay(t *testing.T) {
	billingService := newMarketplaceCalculator(nil, nil)
	settings := billingpricing.DefaultBillingSettings()
	settings.LongContextPricingEnabled = false
	svc := marketplaceWithConfig(billingService, 907, settings, nil)
	group := &routing.Group{ID: 907, RateMultiplier: 1}

	pricing := svc.PublicModelPricing(context.Background(), group, "gpt-5.4")

	if pricing.PricingMode != "token" || pricing.PriceStatus != "priced" || len(pricing.ContextIntervals) != 0 {
		t.Fatalf("long-context-disabled display pricing = %#v, want flat token pricing", pricing)
	}
}

// TestModelMarketplaceDoesNotInventModelsWithoutCandidates supplies an active
// group with a positive provider count but no model reader. ListPublic
// returns an empty group list despite valid currency settings.
func TestModelMarketplaceDoesNotInventModelsWithoutCandidates(t *testing.T) {
	settingRepo := &marketplaceSettingRepoStub{settings: map[string]string{
		billing.SettingKeyReasoningPointRMBUnitPrice: "1",
		billing.SettingKeyUSDExchangeRate:            "7",
	}}
	svc := newMarketplaceFixture(
		&marketplaceGroupRepoStub{groups: []routing.Group{{
			ID:                  1,
			Name:                "Qoder",
			Status:              billing.StatusActive,
			RateMultiplier:      1,
			ActiveProviderCount: 1,
		}}},
		settingRepo, newMarketplaceCalculator(nil, nil), nil,
	)

	groups, err := svc.ListPublic(context.Background(), routing.MarketplaceListOptions{IncludeCapacity: true})
	if err != nil {
		t.Fatalf("ListPublic returned error: %v", err)
	}
	require.Empty(t, groups, "提供商计数不能替代实际可请求能力")
}

// TestModelMarketplaceDisplayPricing_SharedImageRateUsesGroupMultiplier
// supplies a wildcard image card with one size amount and a group factor of
// two. Display mode is image and its 1K amount doubles.
func TestModelMarketplaceDisplayPricing_SharedImageRateUsesGroupMultiplier(t *testing.T) {
	image1K := 10.0
	group := &routing.Group{
		ID:             1,
		RateMultiplier: 2.0,
	}
	svc := marketplaceWithConfig(newMarketplaceCalculator(nil, map[string]*billingpricing.ModelPricing{}), 1, billingpricing.DefaultBillingSettings(), testImageModelPricing(map[string]*float64{"1K": &image1K}))

	pricing := svc.PublicModelPricing(context.Background(), group, "gpt-image-1")

	if pricing.PricingMode != "image" {
		t.Fatalf("pricing mode = %q, want image", pricing.PricingMode)
	}
	if pricing.ImagePrice1K != 20 {
		t.Fatalf("image 1K price = %v, want 20", pricing.ImagePrice1K)
	}
}

// TestModelMarketplaceModelModalitiesComeFromPricingMetadata queries legacy
// modalities from image and vision-chat catalog fixtures. It checks the
// supplied input/output shapes and nil modalities for an unknown name.
func TestModelMarketplaceModelModalitiesComeFromPricingMetadata(t *testing.T) {
	pricingSvc := newModelCatalogFixture(modelCatalogFixture{pricingData: map[string]*billingpricing.CatalogModelPricing{
		"gpt-image-2": {Mode: "image_generation", InputCostPerImageToken: 8e-6},
		"gpt-5.5":     {Mode: "chat", SupportsVision: true},
	}})
	billingService := newMarketplaceCalculator(pricingSvc, nil)
	svc := newMarketplaceFixture(nil, nil, billingService, nil)

	input, output := svc.ModelModalities(routing.MarketplaceModelDef{ID: "gpt-image-2"})
	require.Equal(t, []string{"text", "image"}, input)
	require.Equal(t, []string{"image"}, output)

	input, output = svc.ModelModalities(routing.MarketplaceModelDef{ID: "gpt-5.5"})
	require.Equal(t, []string{"text", "image"}, input)
	require.Equal(t, []string{"text"}, output)

	input, output = svc.ModelModalities(routing.MarketplaceModelDef{ID: "totally-unknown-model"})
	require.Nil(t, input)
	require.Nil(t, output)
}

// TestModelMarketplacePublicModelsIncludeModalities assembles two explicit
// public definitions using legacy price metadata. Both models remain present;
// the known image model gets modalities and the unknown model retains nil
// input modalities.
func TestModelMarketplacePublicModelsIncludeModalities(t *testing.T) {
	pricingSvc := newModelCatalogFixture(modelCatalogFixture{pricingData: map[string]*billingpricing.CatalogModelPricing{
		"gpt-image-2": {Mode: "image_generation", InputCostPerImageToken: 8e-6},
	}})
	billingService := newMarketplaceCalculator(pricingSvc, nil)
	svc := newMarketplaceFixture(nil, nil, billingService, nil)
	group := &routing.Group{ID: 1, RateMultiplier: 1}

	models := svc.BuildPublicModels(context.Background(), group, []routing.MarketplaceModelDef{
		{ID: "gpt-image-2", DisplayName: "GPT Image 2"},
		{ID: "custom-unknown", DisplayName: "Custom Unknown"},
	})

	require.Len(t, models, 2)
	require.Equal(t, []string{"text", "image"}, models[0].InputModalities)
	require.Equal(t, []string{"image"}, models[0].OutputModalities)
	require.Nil(t, models[1].InputModalities)
	require.Nil(t, models[1].OutputModalities)
}

// TestModelMarketplaceGeminiTierModalitiesPreservePublicIDs supplies full
// Gemini price identities and one distinct public alias with a PricingModel.
// Assembly retains each public ID while returning the priced target's text,
// image, audio, and video modalities.
func TestModelMarketplaceGeminiTierModalitiesPreservePublicIDs(t *testing.T) {
	pricing := &billingpricing.CatalogModelPricing{
		InputCostPerToken: 2e-6, OutputCostPerToken: 1e-5,
		CacheCreationInputTokenCost: 2.5e-6, CacheReadInputTokenCost: 2e-7,
		LongContextInputTokenThreshold: 200000, LongContextInputCostMultiplier: 2,
		LongContextOutputCostMultiplier: 1.5, Mode: "chat",
		SupportedModalities:       []string{"text", "image", "audio", "video"},
		SupportedOutputModalities: []string{"text"},
	}
	pricingSvc := newModelCatalogFixture(modelCatalogFixture{pricingData: map[string]*billingpricing.CatalogModelPricing{
		"gemini-3.7-flash-tiered": pricing, "gemini-3.8-flash-tiered": pricing,
	}})
	svc := newMarketplaceFixture(nil, nil, newMarketplaceCalculator(pricingSvc, nil), nil)
	defs := []routing.MarketplaceModelDef{
		{ID: "gemini-3.7-flash-tiered"},
		{ID: "gemini-3.8-flash-tiered"},
		{ID: "public-google", PricingModel: "gemini-3.8-flash-tiered"},
	}
	models := svc.BuildPublicModels(context.Background(), &routing.Group{ID: 1, RateMultiplier: 1}, defs)
	require.Len(t, models, len(defs))
	for i, model := range models {
		require.Equal(t, defs[i].ID, model.ID)
		require.Equal(t, []string{"text", "image", "audio", "video"}, model.InputModalities)
		require.Equal(t, []string{"text"}, model.OutputModalities)
	}
}

// newModelCatalogFixture builds a snapshot-backed catalog from the supplied
// price map and production lookup candidates. The service retains map
// storage, so tests keep its entries stable during queries.
func newModelCatalogFixture(fixture modelCatalogFixture) *provider.Service {
	return provider.NewServiceFromSnapshot(provider.Options{
		ModelLookupCandidates: modelidentity.CandidatesFactory,
	}, nil, provider.Snapshot{Data: fixture.pricingData})
}

// newMarketplaceFixture connects groups and settings with an offline quote
// adapter when calculator is supplied. Provide a group reader for ListPublic;
// direct pricing and modality queries can use a nil group reader. Model,
// observation, and attribute readers remain absent, while clock and warning
// callbacks are time.Now and slog.Warn.
func newMarketplaceFixture(groups routing.MarketplaceGroups, settings routing.MarketplaceSettings, calculator *billing.Calculator, resolver *billing.PriceResolver) *routing.Marketplace {
	var prices routing.MarketplacePrices
	if calculator != nil {
		prices = marketplaceQuoteFixture{calculator: calculator, resolver: resolver}
	}
	return routing.NewMarketplace(groups, settings, nil, routing.RequestableResolver{}, prices, nil, nil, routing.MarketplaceOptions{Now: time.Now, Warn: slog.Warn})
}

// newMarketplaceCalculator delegates offline calculator construction to
// billingtestkit.Calculator. When catalog and prices are both nil, that
// helper loads historical token and media fixtures; supplied data follows its
// catalog precedence rules.
func newMarketplaceCalculator(catalog *provider.Service, prices map[string]*billingpricing.ModelPricing) *billing.Calculator {
	return billingtestkit.Calculator(catalog, prices)
}

// NewModelPricingResolver connects optional shared pricing with the supplied
// calculator and model identity rules. A nil pricing service leaves
// configuration lookup absent, and catalog lookup failures are recorded
// through the diagnostic callback.
func NewModelPricingResolver(pricingConfigs *routing.PricingConfigService, calculator *billing.Calculator) *billing.PriceResolver {
	var source billing.ConfigPrices
	if pricingConfigs != nil {
		source = pricingConfigs
	}
	return billing.NewPriceResolver(source, calculator, modelidentity.Identity, func(model string, err error) {
		slog.Debug("failed to get model pricing from model catalog, using fallback", "model", model, "error", err)
	})
}

// Prefetch reports prefetch unavailable so assembly uses per-group fixture
// resolution.
func (marketplaceLoadingModels) Prefetch(context.Context) ([]routing.CatalogueProvider, bool, error) {
	return nil, false, nil
}

// ResolveRequestableModels returns the alias fixture for group IDs at most
// two and an empty result for larger IDs. It requires a nonnil ID pointer and
// returns shared protocol slice storage.
func (marketplaceLoadingModels) ResolveRequestableModels(_ context.Context, id *int64, _ string) routing.RequestableModelsResult {
	if *id > 2 {
		return routing.RequestableModelsResult{}
	}
	return routing.RequestableModelsResult{Models: []routing.RequestableModel{{ID: "alias", PricingModel: "priced", UpstreamModels: []string{"upstream"}, Protocols: marketplaceLoadingProtocols, NativeProtocols: marketplaceLoadingProtocols[:1]}}}
}

// GetGroupCapacityByIDs records the call and retains the supplied group-ID
// slice directly. It returns a present zero summary for group 1, letting
// assembly distinguish that observation from missing groups.
func (o *marketplaceLoadingObservations) GetGroupCapacityByIDs(_ context.Context, ids []int64) (map[int64]routing.GroupCapacitySummary, error) {
	o.capacityCalls++
	o.capacityGroups = ids
	return map[int64]routing.GroupCapacitySummary{1: {}}, nil
}

// GetSummaryByGroupIDs retains the supplied group-ID slice directly and
// returns a group 1 summary with a three-day window. Other query inputs are
// ignored by this observation fixture.
func (o *marketplaceLoadingObservations) GetSummaryByGroupIDs(_ context.Context, ids []int64, _ int, _ int, _ string, _ time.Time) (map[int64]*routing.GroupAvailabilitySummary, error) {
	o.availabilityGroups = ids
	return map[int64]*routing.GroupAvailabilitySummary{1: {WindowDays: 3}}, nil
}

// Quote records the request and returns a priced input token amount equal to
// its RateMultiplier for marketplace forwarding assertions.
func (p *marketplaceLoadingPrices) Quote(_ context.Context, request routing.MarketplaceQuoteRequest) billingpricing.ModelDisplayPricing {
	p.requests = append(p.requests, request)
	return billingpricing.ModelDisplayPricing{PriceStatus: "priced", InputPricePerToken: request.RateMultiplier}
}

// GetModelModalities counts the legacy metadata query and returns text-only
// input and output.
func (p *marketplaceLoadingPrices) GetModelModalities(string) ([]string, []string) {
	p.modalityCalls++
	return []string{"text"}, []string{"text"}
}

// testImageModelPricing returns one wildcard image card from supplied
// size-price pointers. It delegates interval construction to
// testMediaModelPricing, whose map traversal order is unspecified and whose
// amount pointers remain shared.
func testImageModelPricing(prices map[string]*float64) []routing.ModelPricingEntry {
	return testMediaModelPricing(routing.BillingModeImage, prices)
}

// testMediaModelPricing returns one wildcard card with a labeled interval for
// every supplied map entry. It retains the price pointers, keeps unspecified
// map iteration order, and performs no validation.
func testMediaModelPricing(mode routing.BillingMode, prices map[string]*float64) []routing.ModelPricingEntry {
	card := routing.ModelPricingEntry{Models: []string{"*"}, BillingMode: mode}
	for tier, price := range prices {
		card.Intervals = append(card.Intervals, routing.PricingInterval{TierLabel: tier, PerRequestPrice: price})
	}
	return []routing.ModelPricingEntry{card}
}

// Quote sends the selected model, group ID, factor, and free-Fast flag
// through the shared PublicQuote calculation. A nil resolver constructs a
// catalog-only resolver from calculator for this call.
func (p marketplaceQuoteFixture) Quote(ctx context.Context, req routing.MarketplaceQuoteRequest) billingpricing.ModelDisplayPricing {
	resolver := p.resolver
	// Construct a resolver when this quote adapter has no supplied one, letting
	// direct catalog-price scenarios use the same PublicQuote path as
	// configured-price fixtures.
	if resolver == nil {
		resolver = billing.NewPriceResolver(nil, p.calculator, modelidentity.Identity, func(model string, err error) {
			slog.Debug("failed to get model pricing from model catalog, using fallback", "model", model, "error", err)
		})
	}
	return resolver.PublicQuote(ctx, billing.PublicQuoteInput{PricingInput: billing.PricingInput{Model: req.Model, GroupID: &req.GroupID}, RateMultiplier: req.RateMultiplier, FreeFastApplicable: req.FreeFastApplicable})
}

// GetModelModalities delegates legacy price-metadata lookup to the supplied
// calculator.
func (p marketplaceQuoteFixture) GetModelModalities(model string) ([]string, []string) {
	return p.calculator.GetModelModalities(model)
}

// marketplaceWithConfig builds one active shared configuration for the
// supplied group, settings, and model cards. It connects that service to the
// calculator through NewModelPricingResolver for marketplace quotes.
func marketplaceWithConfig(calculator *billing.Calculator, groupID int64, settings billingpricing.BillingSettings, cards []routing.ModelPricingEntry) *routing.Marketplace {
	configs := routingtestkit.ModelConfigFromData(routingtestkit.ModelConfigDataFromRows([]routingtestkit.Configuration{{ID: 1, Status: routing.StatusActive, GroupIDs: []int64{groupID}, BillingSettings: &settings, ModelPricing: cards}}, nil))
	return newMarketplaceFixture(nil, nil, calculator, NewModelPricingResolver(configs, calculator))
}

// ListActive returns the fixture's group slice directly with no error. Tests
// provide visibility data before assembly and keep the retained backing
// storage stable during the call.
func (s *marketplaceGroupRepoStub) ListActive(context.Context) ([]routing.Group, error) {
	return s.groups, nil
}

// GetMultiple returns a new map for the requested keys, using empty strings
// for missing fixture settings. The stored settings map remains separate from
// the returned map.
func (s *marketplaceSettingRepoStub) GetMultiple(_ context.Context, keys []string) (map[string]string, error) {
	out := make(map[string]string, len(keys))
	for _, key := range keys {
		out[key] = s.settings[key]
	}
	return out, nil
}

// TestMarketplaceUsesRouteAttributes supplies an OAuth Responses route behind
// a public alias and resolves attributes from the offline catalog. Assembly
// returns that route's text input and disabled temperature capability.
func TestMarketplaceUsesRouteAttributes(t *testing.T) {
	attrs := &routing.ModelAttributeService{Catalog: routing.ModelAttributeCatalog{View: testkit.OfflineView}}
	service := routing.NewMarketplace(nil, nil, nil, routing.RequestableResolver{}, nil, nil, nil, routing.MarketplaceOptions{Attributes: attrs.ResolveMarketplaceGroups})
	target := modelcatalog.Target{Model: "gpt-5.3-codex-spark", Platform: "openai", ProviderType: "oauth", ClientProtocol: "openai_responses", UpstreamProtocol: "openai_responses"}
	result := service.BuildPublicModels(context.Background(), &routing.Group{ID: 1}, []routing.MarketplaceModelDef{{ID: "alias", UpstreamModels: []string{target.Model}, AttributeTargets: []modelcatalog.Target{target}}})
	require.Len(t, result, 1)
	require.NotNil(t, result[0].Attributes)
	require.Equal(t, []string{"text"}, *result[0].Attributes.InputModalities)
	require.False(t, *result[0].Attributes.Temperature)
}

// TestMarketplaceExclusiveVisibility grants a private group alongside a public
// catalog. Grants control every enrichment read, while provider/model exclusions,
// public-only access, category order, and unchanged pricing remain observable.
func TestMarketplaceExclusiveVisibility(t *testing.T) {
	for _, includeCapacity := range []bool{false, true} {
		groups := &marketplaceGroupRepoStub{groups: []routing.Group{
			{ID: 2, ActiveProviderCount: 1, RateMultiplier: 3},
			{ID: 1, ActiveProviderCount: 1, RateMultiplier: 2, IsExclusive: true, AvailabilityProbeConfig: routing.GroupAvailabilityProbeConfig{Enabled: true}},
			{ID: 3, ActiveProviderCount: 1, IsExclusive: true},
			{ID: 4, IsExclusive: true},
		}}
		observations := &marketplaceLoadingObservations{}
		prices := &marketplaceLoadingPrices{}
		svc := routing.NewMarketplace(groups, nil, marketplaceLoadingModels{}, routing.RequestableResolver{}, prices, observations, observations, routing.MarketplaceOptions{Now: time.Now})
		result, err := svc.ListVisible(context.Background(), []int64{1, 3, 4, 99}, routing.MarketplaceListOptions{IncludeCapacity: includeCapacity})
		require.NoError(t, err)
		require.Len(t, result, 2)
		require.Equal(t, int64(1), result[0].ID)
		require.True(t, result[0].IsExclusive)
		require.Equal(t, int64(2), result[1].ID)
		require.False(t, result[1].IsExclusive)
		require.Equal(t, 2.0, result[0].Models[0].Pricing.InputPricePerToken)
		require.Equal(t, []int64{1}, observations.availabilityGroups)
		require.NotNil(t, result[0].Availability)
		if includeCapacity {
			require.Equal(t, []int64{1, 2}, observations.capacityGroups)
			require.NotNil(t, result[0].Capacity)
		} else {
			require.Zero(t, observations.capacityCalls)
			require.Nil(t, result[0].Capacity)
		}
		public, err := svc.ListPublic(context.Background(), routing.MarketplaceListOptions{})
		require.NoError(t, err)
		require.Len(t, public, 1)
		require.Equal(t, int64(2), public[0].ID)
		denied, err := svc.ListVisible(context.Background(), []int64{99}, routing.MarketplaceListOptions{})
		require.NoError(t, err)
		require.Equal(t, public, denied)
	}
}
