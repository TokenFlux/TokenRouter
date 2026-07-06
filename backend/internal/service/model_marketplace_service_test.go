package service

import (
	"context"
	"testing"
	"time"
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
			wantWindowDays:    DefaultMarketplaceAvailabilityWindowDays,
			wantBucketMinutes: DefaultMarketplaceAvailabilityBucketMinutes,
		},
		{
			name: "uses stored settings",
			settings: map[string]string{
				SettingKeyMarketplaceAvailabilityWindowDays:    "14",
				SettingKeyMarketplaceAvailabilityBucketMinutes: "60",
			},
			wantWindowDays:    14,
			wantBucketMinutes: 60,
		},
		{
			name: "invalid settings fall back to defaults",
			settings: map[string]string{
				SettingKeyMarketplaceAvailabilityWindowDays:    "-1",
				SettingKeyMarketplaceAvailabilityBucketMinutes: "0",
			},
			wantWindowDays:    DefaultMarketplaceAvailabilityWindowDays,
			wantBucketMinutes: DefaultMarketplaceAvailabilityBucketMinutes,
		},
		{
			name: "bucket count is capped by widening bucket",
			settings: map[string]string{
				SettingKeyMarketplaceAvailabilityWindowDays:    "90",
				SettingKeyMarketplaceAvailabilityBucketMinutes: "5",
			},
			wantWindowDays:    90,
			wantBucketMinutes: 180,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			gotWindowDays, gotBucketMinutes := parseMarketplaceAvailabilityWindowSettings(tt.settings)
			if gotWindowDays != tt.wantWindowDays || gotBucketMinutes != tt.wantBucketMinutes {
				t.Fatalf("parseMarketplaceAvailabilityWindowSettings() = (%d, %d), want (%d, %d)", gotWindowDays, gotBucketMinutes, tt.wantWindowDays, tt.wantBucketMinutes)
			}
		})
	}
}

func TestModelMarketplaceQoderNonManualOnlyModelUsesStandardPricing(t *testing.T) {
	svc := NewModelMarketplaceService(nil, nil, nil, NewBillingService(nil, nil), nil, nil, nil)
	group := &Group{ID: 1, Platform: PlatformQoder, RateMultiplier: 1}

	pricing := svc.getPublicModelDisplayPricing(context.Background(), group, "claude-sonnet-4", nil)

	if pricing.PricingMode != "token" || pricing.PriceStatus != "priced" || pricing.InputPricePerToken <= 0 || pricing.OutputPricePerToken <= 0 {
		t.Fatalf("Qoder non-manual-only model pricing = (%q, %q, %g, %g), want token/priced with standard prices",
			pricing.PricingMode, pricing.PriceStatus, pricing.InputPricePerToken, pricing.OutputPricePerToken)
	}
}

func TestModelMarketplaceQoderChannelMappedStandardModelUsesStandardPricing(t *testing.T) {
	groupID := int64(902)
	cache := newEmptyChannelCache()
	cache.mappingByGroupModel[channelModelKey{groupID: groupID, platform: PlatformQoder, model: "gpt-5.4"}] = "qmodel"
	cache.channelByGroupID[groupID] = &Channel{ID: groupID, Status: StatusActive, BillingModelSource: BillingModelSourceChannelMapped}
	cache.groupPlatform[groupID] = PlatformQoder
	cache.loadedAt = time.Now()
	channelService := &ChannelService{}
	channelService.cache.Store(cache)

	billingService := NewBillingService(nil, nil)
	svc := NewModelMarketplaceService(nil, nil, &GatewayService{
		channelService: channelService,
		resolver:       NewModelPricingResolver(channelService, billingService),
	}, billingService, nil, nil, nil)
	group := &Group{ID: groupID, Platform: PlatformQoder, RateMultiplier: 1}

	pricing := svc.getPublicModelDisplayPricing(context.Background(), group, "gpt-5.4", nil)

	if pricing.PricingMode != "token" || pricing.PriceStatus != "priced" || len(pricing.ContextIntervals) == 0 {
		t.Fatalf("Qoder channel-mapped standard model pricing = (%q, %q, intervals=%d), want standard context pricing",
			pricing.PricingMode, pricing.PriceStatus, len(pricing.ContextIntervals))
	}
}

func TestModelMarketplaceQoderUpstreamRouteKeySourceUsesStandardRequestedPricing(t *testing.T) {
	groupID := int64(902)
	cache := newEmptyChannelCache()
	cache.mappingByGroupModel[channelModelKey{groupID: groupID, platform: PlatformQoder, model: "gpt-5.4-mini"}] = "qmodel"
	cache.channelByGroupID[groupID] = &Channel{ID: groupID, Status: StatusActive, BillingModelSource: BillingModelSourceUpstream}
	cache.groupPlatform[groupID] = PlatformQoder
	cache.loadedAt = time.Now()
	channelService := &ChannelService{}
	channelService.cache.Store(cache)

	billingService := NewBillingService(nil, nil)
	svc := NewModelMarketplaceService(nil, nil, &GatewayService{
		channelService: channelService,
		resolver:       NewModelPricingResolver(channelService, billingService),
	}, billingService, nil, nil, nil)
	group := &Group{ID: groupID, Platform: PlatformQoder, RateMultiplier: 1}

	pricing := svc.getPublicModelDisplayPricing(context.Background(), group, "gpt-5.4-mini", nil)

	if pricing.PricingMode != "token" || pricing.PriceStatus != "priced" || pricing.InputPricePerToken <= 0 || pricing.OutputPricePerToken <= 0 {
		t.Fatalf("Qoder upstream route-key source pricing = (%q, %q, %g, %g), want standard requested-model pricing",
			pricing.PricingMode, pricing.PriceStatus, pricing.InputPricePerToken, pricing.OutputPricePerToken)
	}
}

func TestModelMarketplaceQoderCustomImageAliasWithoutManualPricingRemainsUnknown(t *testing.T) {
	groupID := int64(902)
	cache := newEmptyChannelCache()
	cache.mappingByGroupModel[channelModelKey{groupID: groupID, platform: PlatformQoder, model: "custom-image-alias"}] = "qmodel"
	cache.channelByGroupID[groupID] = &Channel{ID: groupID, Status: StatusActive, BillingModelSource: BillingModelSourceChannelMapped}
	cache.groupPlatform[groupID] = PlatformQoder
	cache.loadedAt = time.Now()
	channelService := &ChannelService{}
	channelService.cache.Store(cache)

	billingService := NewBillingService(nil, nil)
	svc := NewModelMarketplaceService(nil, nil, &GatewayService{
		channelService: channelService,
		resolver:       NewModelPricingResolver(channelService, billingService),
	}, billingService, nil, nil, nil)
	group := &Group{ID: groupID, Platform: PlatformQoder, RateMultiplier: 1}

	pricing := svc.getPublicModelDisplayPricing(context.Background(), group, "custom-image-alias", nil)

	if pricing.PricingMode != "unknown" || pricing.PriceStatus != "unpriced" {
		t.Fatalf("Qoder custom image alias pricing = (%q, %q), want unknown/unpriced", pricing.PricingMode, pricing.PriceStatus)
	}
}

func TestModelMarketplaceQoderDefaultAliasesWithoutManualPricingRemainUnknown(t *testing.T) {
	billingService := NewBillingService(nil, nil)
	svc := NewModelMarketplaceService(nil, nil, nil, billingService, nil, nil, nil)
	group := &Group{ID: 1, Platform: PlatformQoder, RateMultiplier: 1.25}

	pricing := svc.getPublicModelDisplayPricing(context.Background(), group, "auto", nil)

	if pricing.PricingMode != "unknown" || pricing.PriceStatus != "unpriced" {
		t.Fatalf("Qoder default alias pricing = (%q, %q), want unknown/unpriced", pricing.PricingMode, pricing.PriceStatus)
	}
}

func TestModelMarketplaceQoderManualChannelPricingOverridesDefaultAliasDisplayPricing(t *testing.T) {
	groupID := int64(902)
	inputPrice := 0.01
	outputPrice := 0.02
	cache := newEmptyChannelCache()
	cache.pricingByGroupModel[channelModelKey{groupID: groupID, platform: PlatformQoder, model: "auto"}] = &ChannelModelPricing{
		BillingMode: BillingModeToken,
		InputPrice:  &inputPrice,
		OutputPrice: &outputPrice,
	}
	cache.channelByGroupID[groupID] = &Channel{ID: groupID, Status: StatusActive}
	cache.groupPlatform[groupID] = PlatformQoder
	cache.loadedAt = time.Now()
	channelService := &ChannelService{}
	channelService.cache.Store(cache)

	billingService := NewBillingService(nil, nil)
	svc := NewModelMarketplaceService(nil, nil, &GatewayService{
		resolver: NewModelPricingResolver(channelService, billingService),
	}, billingService, nil, nil, nil)
	group := &Group{ID: groupID, Platform: PlatformQoder, RateMultiplier: 1}

	pricing := svc.getPublicModelDisplayPricing(context.Background(), group, "auto", nil)

	if pricing.InputPricePerToken != inputPrice || pricing.OutputPricePerToken != outputPrice {
		t.Fatalf("Qoder manual alias price = (%g, %g), want (%g, %g)", pricing.InputPricePerToken, pricing.OutputPricePerToken, inputPrice, outputPrice)
	}
}

func TestModelMarketplaceQoderBlankChannelPricingRemainsUnknown(t *testing.T) {
	groupID := int64(902)
	cache := newEmptyChannelCache()
	cache.pricingByGroupModel[channelModelKey{groupID: groupID, platform: PlatformQoder, model: "auto"}] = &ChannelModelPricing{
		BillingMode: BillingModeToken,
	}
	cache.channelByGroupID[groupID] = &Channel{ID: groupID, Status: StatusActive}
	cache.groupPlatform[groupID] = PlatformQoder
	cache.loadedAt = time.Now()
	channelService := &ChannelService{}
	channelService.cache.Store(cache)

	billingService := NewBillingService(nil, nil)
	svc := NewModelMarketplaceService(nil, nil, &GatewayService{
		resolver: NewModelPricingResolver(channelService, billingService),
	}, billingService, nil, nil, nil)
	group := &Group{ID: groupID, Platform: PlatformQoder, RateMultiplier: 1}

	pricing := svc.getPublicModelDisplayPricing(context.Background(), group, "auto", nil)

	if pricing.PricingMode != "unknown" || pricing.PriceStatus != "unpriced" {
		t.Fatalf("Qoder blank channel alias pricing = (%q, %q), want unknown/unpriced", pricing.PricingMode, pricing.PriceStatus)
	}
}

func TestModelMarketplaceQoderBlankRouteKeyPricingShowsAliasManualPricing(t *testing.T) {
	groupID := int64(902)
	aliasInputPrice := 0.01
	aliasOutputPrice := 0.02
	cache := newEmptyChannelCache()
	cache.pricingByGroupModel[channelModelKey{groupID: groupID, platform: PlatformQoder, model: "qmodel"}] = &ChannelModelPricing{
		BillingMode: BillingModeToken,
	}
	cache.pricingByGroupModel[channelModelKey{groupID: groupID, platform: PlatformQoder, model: "qwen3.7-plus"}] = &ChannelModelPricing{
		BillingMode: BillingModeToken,
		InputPrice:  &aliasInputPrice,
		OutputPrice: &aliasOutputPrice,
	}
	cache.channelByGroupID[groupID] = &Channel{ID: groupID, Status: StatusActive}
	cache.groupPlatform[groupID] = PlatformQoder
	cache.loadedAt = time.Now()
	channelService := &ChannelService{}
	channelService.cache.Store(cache)

	billingService := NewBillingService(nil, nil)
	svc := NewModelMarketplaceService(nil, nil, &GatewayService{
		channelService: channelService,
		resolver:       NewModelPricingResolver(channelService, billingService),
	}, billingService, nil, nil, nil)
	group := &Group{ID: groupID, Platform: PlatformQoder, RateMultiplier: 1}

	pricing := svc.getPublicModelDisplayPricing(context.Background(), group, "qwen3.7-plus", nil)

	if pricing.InputPricePerToken != aliasInputPrice || pricing.OutputPricePerToken != aliasOutputPrice {
		t.Fatalf("Qoder alias display price = (%g, %g), want (%g, %g)", pricing.InputPricePerToken, pricing.OutputPricePerToken, aliasInputPrice, aliasOutputPrice)
	}
}

func TestModelMarketplaceQoderDefaultAliasUsesRouteKeyManualPricing(t *testing.T) {
	groupID := int64(902)
	inputPrice := 0.01
	outputPrice := 0.02
	cache := newEmptyChannelCache()
	cache.pricingByGroupModel[channelModelKey{groupID: groupID, platform: PlatformQoder, model: "qmodel"}] = &ChannelModelPricing{
		BillingMode: BillingModeToken,
		InputPrice:  &inputPrice,
		OutputPrice: &outputPrice,
	}
	cache.channelByGroupID[groupID] = &Channel{ID: groupID, Status: StatusActive}
	cache.groupPlatform[groupID] = PlatformQoder
	cache.loadedAt = time.Now()
	channelService := &ChannelService{}
	channelService.cache.Store(cache)

	billingService := NewBillingService(nil, nil)
	svc := NewModelMarketplaceService(nil, nil, &GatewayService{
		channelService: channelService,
		resolver:       NewModelPricingResolver(channelService, billingService),
	}, billingService, nil, nil, nil)
	group := &Group{ID: groupID, Platform: PlatformQoder, RateMultiplier: 1}

	pricing := svc.getPublicModelDisplayPricing(context.Background(), group, "qwen3.7-plus", nil)

	if pricing.InputPricePerToken != inputPrice || pricing.OutputPricePerToken != outputPrice {
		t.Fatalf("Qoder route key display price = (%g, %g), want (%g, %g)", pricing.InputPricePerToken, pricing.OutputPricePerToken, inputPrice, outputPrice)
	}
}

func TestModelMarketplaceQoderAccountMappedCustomModelUsesRouteKeyManualPricing(t *testing.T) {
	groupID := int64(903)
	inputPrice := 0.01
	outputPrice := 0.02
	cache := newEmptyChannelCache()
	cache.pricingByGroupModel[channelModelKey{groupID: groupID, platform: PlatformQoder, model: "qmodel"}] = &ChannelModelPricing{
		BillingMode: BillingModeToken,
		InputPrice:  &inputPrice,
		OutputPrice: &outputPrice,
	}
	cache.channelByGroupID[groupID] = &Channel{ID: groupID, Status: StatusActive}
	cache.groupPlatform[groupID] = PlatformQoder
	cache.loadedAt = time.Now()
	channelService := &ChannelService{}
	channelService.cache.Store(cache)

	billingService := NewBillingService(nil, nil)
	svc := NewModelMarketplaceService(nil, nil, &GatewayService{
		accountRepo: &modelsListAccountRepoStub{byGroup: map[int64][]Account{
			groupID: {
				{
					ID:       1,
					Platform: PlatformQoder,
					Type:     AccountTypeCosy,
					Credentials: map[string]any{
						"model_mapping": map[string]any{
							"custom-qoder-model": "qmodel",
						},
					},
				},
			},
		}},
		channelService: channelService,
		resolver:       NewModelPricingResolver(channelService, billingService),
	}, billingService, nil, nil, nil)
	group := &Group{ID: groupID, Platform: PlatformQoder, RateMultiplier: 1}

	models := svc.listPublicModelsForGroup(context.Background(), group)

	if len(models) != 1 || models[0].ID != "custom-qoder-model" {
		t.Fatalf("Qoder account-mapped marketplace models = %#v, want custom-qoder-model only", models)
	}
	pricing := models[0].Pricing
	if pricing.InputPricePerToken != inputPrice || pricing.OutputPricePerToken != outputPrice {
		t.Fatalf("Qoder account-mapped route key display price = (%g, %g), want (%g, %g)", pricing.InputPricePerToken, pricing.OutputPricePerToken, inputPrice, outputPrice)
	}
}

func TestModelMarketplaceQoderAliasManualPricingOverridesRouteKeyManualPricing(t *testing.T) {
	groupID := int64(902)
	aliasInputPrice := 0.01
	aliasOutputPrice := 0.02
	routeInputPrice := 0.50
	routeOutputPrice := 0.75
	cache := newEmptyChannelCache()
	cache.pricingByGroupModel[channelModelKey{groupID: groupID, platform: PlatformQoder, model: "qmodel"}] = &ChannelModelPricing{
		BillingMode: BillingModeToken,
		InputPrice:  &routeInputPrice,
		OutputPrice: &routeOutputPrice,
	}
	cache.pricingByGroupModel[channelModelKey{groupID: groupID, platform: PlatformQoder, model: "qwen3.7-plus"}] = &ChannelModelPricing{
		BillingMode: BillingModeToken,
		InputPrice:  &aliasInputPrice,
		OutputPrice: &aliasOutputPrice,
	}
	cache.channelByGroupID[groupID] = &Channel{ID: groupID, Status: StatusActive}
	cache.groupPlatform[groupID] = PlatformQoder
	cache.loadedAt = time.Now()
	channelService := &ChannelService{}
	channelService.cache.Store(cache)

	billingService := NewBillingService(nil, nil)
	svc := NewModelMarketplaceService(nil, nil, &GatewayService{
		channelService: channelService,
		resolver:       NewModelPricingResolver(channelService, billingService),
	}, billingService, nil, nil, nil)
	group := &Group{ID: groupID, Platform: PlatformQoder, RateMultiplier: 1}

	pricing := svc.getPublicModelDisplayPricing(context.Background(), group, "qwen3.7-plus", nil)

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
	cache := newEmptyChannelCache()
	cache.pricingByGroupModel[channelModelKey{groupID: groupID, platform: PlatformQoder, model: "qwen3.7-plus"}] = &ChannelModelPricing{
		BillingMode: BillingModeToken,
		Intervals: []PricingInterval{
			{MinTokens: 0, MaxTokens: &maxTokens, InputPrice: &firstInput, OutputPrice: &firstOutput},
			{MinTokens: maxTokens, InputPrice: &secondInput, OutputPrice: &secondOutput},
		},
	}
	cache.channelByGroupID[groupID] = &Channel{ID: groupID, Status: StatusActive}
	cache.groupPlatform[groupID] = PlatformQoder
	cache.loadedAt = time.Now()
	channelService := &ChannelService{}
	channelService.cache.Store(cache)

	billingService := NewBillingService(nil, nil)
	svc := NewModelMarketplaceService(nil, nil, &GatewayService{
		channelService: channelService,
		resolver:       NewModelPricingResolver(channelService, billingService),
	}, billingService, nil, nil, nil)
	group := &Group{ID: groupID, Platform: PlatformQoder, RateMultiplier: 1}

	pricing := svc.getPublicModelDisplayPricing(context.Background(), group, "qwen3.7-plus", nil)

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
	cache := newEmptyChannelCache()
	cache.pricingByGroupModel[channelModelKey{groupID: groupID, platform: PlatformQoder, model: "gpt-5.4"}] = &ChannelModelPricing{
		BillingMode: BillingModeToken,
		Intervals: []PricingInterval{
			{MinTokens: 0, InputPrice: &inputPrice},
		},
	}
	cache.channelByGroupID[groupID] = &Channel{ID: groupID, Status: StatusActive}
	cache.groupPlatform[groupID] = PlatformQoder
	cache.loadedAt = time.Now()
	channelService := &ChannelService{}
	channelService.cache.Store(cache)

	billingService := NewBillingService(nil, nil)
	basePricing, err := billingService.GetModelPricing("gpt-5.4")
	if err != nil {
		t.Fatalf("GetModelPricing(gpt-5.4) error = %v", err)
	}
	svc := NewModelMarketplaceService(nil, nil, &GatewayService{
		channelService: channelService,
		resolver:       NewModelPricingResolver(channelService, billingService),
	}, billingService, nil, nil, nil)
	group := &Group{ID: groupID, Platform: PlatformQoder, RateMultiplier: 1}

	pricing := svc.getPublicModelDisplayPricing(context.Background(), group, "gpt-5.4", nil)

	if pricing.InputPricePerToken != inputPrice || pricing.OutputPricePerToken != basePricing.OutputPricePerToken {
		t.Fatalf("Qoder standard partial interval display price = (%g, %g), want (%g, %g)",
			pricing.InputPricePerToken, pricing.OutputPricePerToken, inputPrice, basePricing.OutputPricePerToken)
	}
}

func TestModelMarketplaceQoderOmitsOfficialPriceDiscount(t *testing.T) {
	settingRepo := &marketplaceSettingRepoStub{settings: map[string]string{
		SettingKeyReasoningPointRMBUnitPrice: "1",
		SettingKeyUSDExchangeRate:            "7",
	}}
	svc := NewModelMarketplaceService(
		&marketplaceGroupRepoStub{groups: []Group{{
			ID:                 1,
			Name:               "Qoder",
			Platform:           PlatformQoder,
			Status:             StatusActive,
			RateMultiplier:     1,
			ActiveAccountCount: 1,
		}}},
		settingRepo,
		nil,
		NewBillingService(nil, nil),
		nil,
		nil,
		nil,
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
		if model.Pricing.PriceStatus != "unpriced" {
			t.Fatalf("Qoder model %s price status = %q, want unpriced", model.ID, model.Pricing.PriceStatus)
		}
	}
}

type marketplaceGroupRepoStub struct {
	GroupRepository
	groups []Group
}

func (s *marketplaceGroupRepoStub) ListActive(context.Context) ([]Group, error) {
	return s.groups, nil
}

type marketplaceSettingRepoStub struct {
	SettingRepository
	settings map[string]string
}

func (s *marketplaceSettingRepoStub) GetMultiple(_ context.Context, keys []string) (map[string]string, error) {
	out := make(map[string]string, len(keys))
	for _, key := range keys {
		out[key] = s.settings[key]
	}
	return out, nil
}
func TestModelMarketplaceDisplayPricing_UsesIndependentImageRateMultiplier(t *testing.T) {
	image1K := 10.0
	group := &Group{
		ID:                   1,
		RateMultiplier:       2.0,
		ImageRateIndependent: true,
		ImageRateMultiplier:  0.5,
		ImagePrice1K:         &image1K,
	}
	svc := &ModelMarketplaceService{
		billingService: &BillingService{},
	}

	pricing := svc.getPublicModelDisplayPricing(context.Background(), group, "gpt-image-1", &ImagePriceConfig{
		Price1K: group.ImagePrice1K,
	})

	if pricing.PricingMode != "image" {
		t.Fatalf("pricing mode = %q, want image", pricing.PricingMode)
	}
	if pricing.ImagePrice1K != 5 {
		t.Fatalf("image 1K price = %v, want 5", pricing.ImagePrice1K)
	}
}

func TestModelMarketplaceDisplayPricing_SharedImageRateUsesGroupMultiplier(t *testing.T) {
	image1K := 10.0
	group := &Group{
		ID:                   1,
		RateMultiplier:       2.0,
		ImageRateIndependent: false,
		ImageRateMultiplier:  0.5,
		ImagePrice1K:         &image1K,
	}
	svc := &ModelMarketplaceService{
		billingService: &BillingService{},
	}

	pricing := svc.getPublicModelDisplayPricing(context.Background(), group, "gpt-image-1", &ImagePriceConfig{
		Price1K: group.ImagePrice1K,
	})

	if pricing.PricingMode != "image" {
		t.Fatalf("pricing mode = %q, want image", pricing.PricingMode)
	}
	if pricing.ImagePrice1K != 20 {
		t.Fatalf("image 1K price = %v, want 20", pricing.ImagePrice1K)
	}
}
