package routing_test

import (
	"context"
	"testing"
	"time"

	"github.com/TokenFlux/TokenRouter/internal/billing"
	billingpricing "github.com/TokenFlux/TokenRouter/internal/billing/pricing"
	"github.com/TokenFlux/TokenRouter/internal/routing"
	"github.com/TokenFlux/TokenRouter/internal/routing/capability"
	routingtestkit "github.com/TokenFlux/TokenRouter/internal/routing/testkit"
	settingscore "github.com/TokenFlux/TokenRouter/internal/settings"
	"github.com/stretchr/testify/require"
)

func TestParseMarketplaceAvailabilityWindowSettings(t *testing.T) {
	tests := []struct {
		name              string
		settings          map[string]string
		wantWindowDays    int
		wantBucketMinutes int
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

func TestModelMarketplaceQoderModelUsesStandardPricing(t *testing.T) {
	svc := newMarketplaceFixture(nil, nil, newMarketplaceCalculator(nil, nil), nil)
	group := &routing.Group{ID: 1, Platform: capability.PlatformQoder, RateMultiplier: 1}

	pricing := svc.PublicModelPricing(context.Background(), group, "claude-sonnet-4")

	if pricing.PricingMode != "token" || pricing.PriceStatus != "priced" || pricing.InputPricePerToken <= 0 || pricing.OutputPricePerToken <= 0 {
		t.Fatalf("Qoder model pricing = (%q, %q, %g, %g), want token/priced with standard prices",
			pricing.PricingMode, pricing.PriceStatus, pricing.InputPricePerToken, pricing.OutputPricePerToken)
	}
}

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
	group := &routing.Group{ID: groupID, Platform: capability.PlatformQoder, RateMultiplier: 1}

	pricing := svc.RequestableModelPricing(context.Background(), group, routing.MarketplaceModelDef{ID: "gpt-5.4", PricingModel: "qmodel"})

	if pricing.PricingMode != "unknown" || pricing.PriceStatus != "unpriced" {
		t.Fatalf("Qoder channel-mapped pricing = (%q, %q, intervals=%d), want unknown/unpriced",
			pricing.PricingMode, pricing.PriceStatus, len(pricing.ContextIntervals))
	}
}

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
	group := &routing.Group{ID: groupID, Platform: capability.PlatformQoder, RateMultiplier: 1}

	pricing := svc.RequestableModelPricing(context.Background(), group, routing.MarketplaceModelDef{ID: "gpt-5.4-mini", PricingModel: "qmodel"})

	if pricing.PricingMode != "unknown" || pricing.PriceStatus != "unpriced" {
		t.Fatalf("Qoder upstream route-key source pricing = (%q, %q, %g, %g), want unknown/unpriced",
			pricing.PricingMode, pricing.PriceStatus, pricing.InputPricePerToken, pricing.OutputPricePerToken)
	}
}

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
	group := &routing.Group{ID: groupID, Platform: capability.PlatformQoder, RateMultiplier: 1}

	pricing := svc.RequestableModelPricing(context.Background(), group, routing.MarketplaceModelDef{ID: "custom-image-alias", PricingModel: "qmodel"})

	if pricing.PricingMode != "unknown" || pricing.PriceStatus != "unpriced" {
		t.Fatalf("Qoder custom image alias pricing = (%q, %q), want unknown/unpriced", pricing.PricingMode, pricing.PriceStatus)
	}
}

func TestModelMarketplaceQoderAliasesWithoutAnyBasePricingRemainUnknown(t *testing.T) {
	billingService := newMarketplaceCalculator(nil, nil)
	svc := newMarketplaceFixture(nil, nil, billingService, nil)
	group := &routing.Group{ID: 1, Platform: capability.PlatformQoder, RateMultiplier: 1.25}

	for _, model := range []string{"auto", "qwen3.8-max", "qmodel_38max"} {
		pricing := svc.PublicModelPricing(context.Background(), group, model)
		if pricing.PricingMode != "unknown" || pricing.PriceStatus != "unpriced" {
			t.Fatalf("Qoder model %s pricing = (%q, %q), want unknown/unpriced", model, pricing.PricingMode, pricing.PriceStatus)
		}
	}
}

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
	group := &routing.Group{ID: groupID, Platform: capability.PlatformQoder, RateMultiplier: 1}

	pricing := svc.PublicModelPricing(context.Background(), group, "auto")

	if pricing.InputPricePerToken != inputPrice || pricing.OutputPricePerToken != outputPrice {
		t.Fatalf("Qoder manual alias price = (%g, %g), want (%g, %g)", pricing.InputPricePerToken, pricing.OutputPricePerToken, inputPrice, outputPrice)
	}
}

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
	group := &routing.Group{ID: groupID, Platform: capability.PlatformOpenAI, RateMultiplier: 1.5}

	pricing := svc.PublicModelPricing(context.Background(), group, "gpt-image-edit")

	if pricing.PricingMode != "token" || pricing.PriceStatus != "priced" {
		t.Fatalf("image edit pricing = (%q, %q), want token/priced", pricing.PricingMode, pricing.PriceStatus)
	}
	if pricing.ImageInputPricePerToken != imageInputPrice*group.RateMultiplier {
		t.Fatalf("image input price = %g, want %g", pricing.ImageInputPricePerToken, imageInputPrice*group.RateMultiplier)
	}
}

func TestModelDisplayPricingImageInputFastRates(t *testing.T) {
	fastModeMultiplier := 3.0
	tests := []struct {
		name          string
		pricing       billingpricing.ModelPricing
		wantImage     float64
		wantFastImage float64
	}{
		{
			name: "priority 倍率同步应用到图片输入价",
			pricing: billingpricing.ModelPricing{
				InputPricePerToken:      0.01,
				ImageInputPricePerToken: 0.03,
				SupportsServiceTier:     true,
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
	group := &routing.Group{ID: groupID, Platform: capability.PlatformQoder, RateMultiplier: 1}

	pricing := svc.PublicModelPricing(context.Background(), group, "auto")

	if pricing.PricingMode != "unknown" || pricing.PriceStatus != "unpriced" {
		t.Fatalf("Qoder blank channel alias pricing = (%q, %q), want unknown/unpriced", pricing.PricingMode, pricing.PriceStatus)
	}
}

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
	group := &routing.Group{ID: groupID, Platform: capability.PlatformQoder, RateMultiplier: 1}

	pricing := svc.PublicModelPricing(context.Background(), group, "qwen3.7-plus")

	if pricing.InputPricePerToken != aliasInputPrice || pricing.OutputPricePerToken != aliasOutputPrice {
		t.Fatalf("Qoder alias display price = (%g, %g), want (%g, %g)", pricing.InputPricePerToken, pricing.OutputPricePerToken, aliasInputPrice, aliasOutputPrice)
	}
}

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
	group := &routing.Group{ID: groupID, Platform: capability.PlatformQoder, RateMultiplier: 1}

	pricing := svc.PublicModelPricing(context.Background(), group, "qwen3.7-plus")

	if pricing.PricingMode != "unknown" || pricing.PriceStatus != "unpriced" {
		t.Fatalf("Qoder requested-basis display price = (%q, %q), want unknown/unpriced", pricing.PricingMode, pricing.PriceStatus)
	}
}

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
	group := &routing.Group{ID: groupID, Platform: capability.PlatformQoder, RateMultiplier: 1}

	pricing := svc.PublicModelPricing(context.Background(), group, "qwen3.7-plus")

	if pricing.InputPricePerToken != aliasInputPrice || pricing.OutputPricePerToken != aliasOutputPrice {
		t.Fatalf("Qoder alias display price = (%g, %g), want (%g, %g)", pricing.InputPricePerToken, pricing.OutputPricePerToken, aliasInputPrice, aliasOutputPrice)
	}
}

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
	group := &routing.Group{ID: groupID, Platform: capability.PlatformQoder, RateMultiplier: 1}

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
	group := &routing.Group{ID: groupID, Platform: capability.PlatformQoder, RateMultiplier: 1}

	pricing := svc.PublicModelPricing(context.Background(), group, "gpt-5.4")

	if pricing.InputPricePerToken != inputPrice || pricing.OutputPricePerToken != basePricing.OutputPricePerToken {
		t.Fatalf("Qoder standard partial interval display price = (%g, %g), want (%g, %g)",
			pricing.InputPricePerToken, pricing.OutputPricePerToken, inputPrice, basePricing.OutputPricePerToken)
	}
}

func TestModelMarketplaceGroupPricingOverridesConfigPricing(t *testing.T) {
	groupID := int64(905)
	pricingConfigInput := 0.5
	pricingConfigOutput := 0.75
	groupInput := 0.01
	groupOutput := 0.02
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
	group := &routing.Group{
		ID: groupID, Platform: capability.PlatformOpenAI, RateMultiplier: 2, LongContextPricingEnabled: true,
		ModelPricing: []routing.ModelPricingEntry{{
			Models: []string{"gpt-5.4-mini"}, BillingMode: routing.BillingModeToken,
			InputPrice: &groupInput, OutputPrice: &groupOutput,
		}},
	}

	pricing := svc.PublicModelPricing(context.Background(), group, "gpt-5.4-mini")

	if pricing.InputPricePerToken != groupInput*group.RateMultiplier || pricing.OutputPricePerToken != groupOutput*group.RateMultiplier {
		t.Fatalf("group display price = (%g, %g), want (%g, %g)",
			pricing.InputPricePerToken, pricing.OutputPricePerToken,
			groupInput*group.RateMultiplier, groupOutput*group.RateMultiplier)
	}
}

func TestModelMarketplaceGroupExplicitZeroPricingRemainsPriced(t *testing.T) {
	zero := 0.0
	billingService := newMarketplaceCalculator(nil, nil)
	svc := newMarketplaceFixture(nil, nil,

		billingService, NewModelPricingResolver(nil, billingService),
	)
	group := &routing.Group{
		ID: 906, Platform: capability.PlatformOpenAI, RateMultiplier: 1, LongContextPricingEnabled: true,
		ModelPricing: []routing.ModelPricingEntry{{
			Models: []string{"gpt-5.4"}, BillingMode: routing.BillingModeToken,
			InputPrice: &zero, OutputPrice: &zero,
		}},
	}

	pricing := svc.PublicModelPricing(context.Background(), group, "gpt-5.4")

	if pricing.PricingMode != "token" || pricing.PriceStatus != "priced" || pricing.InputPricePerToken != 0 || pricing.OutputPricePerToken != 0 {
		t.Fatalf("free group display pricing = %#v, want token/priced with zero prices", pricing)
	}
}

func TestModelMarketplaceGroupCanDisableBuiltInLongContextDisplay(t *testing.T) {
	billingService := newMarketplaceCalculator(nil, nil)
	svc := newMarketplaceFixture(nil, nil,

		billingService, NewModelPricingResolver(nil, billingService),
	)
	group := &routing.Group{ID: 907, Platform: capability.PlatformOpenAI, RateMultiplier: 1, LongContextPricingEnabled: false}

	pricing := svc.PublicModelPricing(context.Background(), group, "gpt-5.4")

	if pricing.PricingMode != "token" || pricing.PriceStatus != "priced" || len(pricing.ContextIntervals) != 0 {
		t.Fatalf("long-context-disabled display pricing = %#v, want flat token pricing", pricing)
	}
}

func TestModelMarketplaceQoderOmitsOfficialPriceDiscount(t *testing.T) {
	settingRepo := &marketplaceSettingRepoStub{settings: map[string]string{
		billing.SettingKeyReasoningPointRMBUnitPrice: "1",
		billing.SettingKeyUSDExchangeRate:            "7",
	}}
	svc := newMarketplaceFixture(
		&marketplaceGroupRepoStub{groups: []routing.Group{{
			ID:                 1,
			Name:               "Qoder",
			Platform:           capability.PlatformQoder,
			Status:             billing.StatusActive,
			RateMultiplier:     1,
			ActiveAccountCount: 1,
		}}},
		settingRepo, newMarketplaceCalculator(nil, nil), nil,
	)

	groups, err := svc.ListPublic(context.Background())
	if err != nil {
		t.Fatalf("ListPublic returned error: %v", err)
	}
	if len(groups) != 1 {
		t.Fatalf("ListPublic returned %d groups, want 1", len(groups))
	}
	if groups[0].OfficialPriceRatio != nil || groups[0].OfficialPriceRMBEquivalent != nil {
		t.Fatalf("Qoder official price discount should be omitted, got ratio=%v rmb=%v", groups[0].OfficialPriceRatio, groups[0].OfficialPriceRMBEquivalent)
	}
	if len(groups[0].Models) == 0 {
		t.Fatal("Qoder marketplace should still list public models")
	}
	for _, model := range groups[0].Models {
		if model.ID == "claude-opus-4-6" {
			require.Equal(t, "priced", model.Pricing.PriceStatus)
			require.Positive(t, model.Pricing.InputPricePerToken)
		}
	}
}

type marketplaceGroupRepoStub struct {
	routing.GroupRepository

	groups []routing.Group
}

func (s *marketplaceGroupRepoStub) ListActive(context.Context) ([]routing.Group, error) {
	return s.groups, nil
}

type marketplaceSettingRepoStub struct {
	settingscore.Repository
	settings map[string]string
}

func (s *marketplaceSettingRepoStub) GetMultiple(_ context.Context, keys []string) (map[string]string, error) {
	out := make(map[string]string, len(keys))
	for _, key := range keys {
		out[key] = s.settings[key]
	}
	return out, nil
}

func TestModelMarketplaceDisplayPricing_SharedImageRateUsesGroupMultiplier(t *testing.T) {
	image1K := 10.0
	group := &routing.Group{
		ID:             1,
		RateMultiplier: 2.0,
		ModelPricing:   testImageModelPricing(map[string]*float64{"1K": &image1K}),
	}
	svc := newMarketplaceFixture(nil, nil, newMarketplaceCalculator(nil, map[string]*billingpricing.ModelPricing{}), nil)

	pricing := svc.PublicModelPricing(context.Background(), group, "gpt-image-1")

	if pricing.PricingMode != "image" {
		t.Fatalf("pricing mode = %q, want image", pricing.PricingMode)
	}
	if pricing.ImagePrice1K != 20 {
		t.Fatalf("image 1K price = %v, want 20", pricing.ImagePrice1K)
	}
}

func TestModelMarketplaceModelModalitiesComeFromPricingMetadata(t *testing.T) {
	pricingSvc := newPricingServiceFixture(pricingServiceFixture{pricingData: map[string]*billingpricing.LiteLLMModelPricing{
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

func TestModelMarketplacePublicModelsIncludeModalities(t *testing.T) {
	pricingSvc := newPricingServiceFixture(pricingServiceFixture{pricingData: map[string]*billingpricing.LiteLLMModelPricing{
		"gpt-image-2": {Mode: "image_generation", InputCostPerImageToken: 8e-6},
	}})
	billingService := newMarketplaceCalculator(pricingSvc, nil)
	svc := newMarketplaceFixture(nil, nil, billingService, nil)
	group := &routing.Group{ID: 1, Platform: capability.PlatformOpenAI, RateMultiplier: 1}

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

// 市场使用解析后的 PricingModel 查询能力，保留公开 ID 和完整音视频输入标记。
func TestModelMarketplaceGeminiTierModalitiesPreservePublicIDs(t *testing.T) {
	pricing := &billingpricing.LiteLLMModelPricing{
		InputCostPerToken: 2e-6, OutputCostPerToken: 1e-5,
		CacheCreationInputTokenCost: 2.5e-6, CacheReadInputTokenCost: 2e-7,
		LongContextInputTokenThreshold: 200000, LongContextInputCostMultiplier: 2,
		LongContextOutputCostMultiplier: 1.5, Mode: "chat",
		SupportedModalities:       []string{"text", "image", "audio", "video"},
		SupportedOutputModalities: []string{"text"},
	}
	pricingSvc := newPricingServiceFixture(pricingServiceFixture{pricingData: map[string]*billingpricing.LiteLLMModelPricing{
		"gemini-3.7-flash": pricing, "gemini-3.8-flash": pricing,
	}})
	svc := newMarketplaceFixture(nil, nil, newMarketplaceCalculator(pricingSvc, nil), nil)
	defs := []routing.MarketplaceModelDef{
		{ID: "gemini-3.7-flash-tiered"},
		{ID: "gemini-3.8-flash-tiered"},
		{ID: "public-google", PricingModel: "gemini-3.8-flash-tiered"},
	}
	models := svc.BuildPublicModels(context.Background(), &routing.Group{ID: 1, Platform: capability.PlatformGemini, RateMultiplier: 1}, defs)
	require.Len(t, models, len(defs))
	for i, model := range models {
		require.Equal(t, defs[i].ID, model.ID)
		require.Equal(t, []string{"text", "image", "audio", "video"}, model.InputModalities)
		require.Equal(t, []string{"text"}, model.OutputModalities)
	}
}
