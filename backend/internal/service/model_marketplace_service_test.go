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

func TestModelMarketplaceQoderChannelMappedBasisDoesNotUseRequestedStandardPricing(t *testing.T) {
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

	if pricing.PricingMode != "unknown" || pricing.PriceStatus != "unpriced" {
		t.Fatalf("Qoder channel-mapped pricing = (%q, %q, intervals=%d), want unknown/unpriced",
			pricing.PricingMode, pricing.PriceStatus, len(pricing.ContextIntervals))
	}
}

func TestModelMarketplaceQoderUpstreamBasisDoesNotUseRequestedStandardPricing(t *testing.T) {
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

	if pricing.PricingMode != "unknown" || pricing.PriceStatus != "unpriced" {
		t.Fatalf("Qoder upstream route-key source pricing = (%q, %q, %g, %g), want unknown/unpriced",
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

	// 正式公开名和 raw route 都不能从通用模型价格推断计费。
	for _, model := range []string{"auto", "qwen3.8-max", "qmodel_38max"} {
		pricing := svc.getPublicModelDisplayPricing(context.Background(), group, model, nil)
		if pricing.PricingMode != "unknown" || pricing.PriceStatus != "unpriced" {
			t.Fatalf("Qoder model %s pricing = (%q, %q), want unknown/unpriced", model, pricing.PricingMode, pricing.PriceStatus)
		}
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

func TestModelMarketplaceChannelImageInputPricingIsDisplayed(t *testing.T) {
	groupID := int64(904)
	inputPrice := 0.01
	imageInputPrice := 0.03
	outputPrice := 0.02
	cache := newEmptyChannelCache()
	cache.pricingByGroupModel[channelModelKey{groupID: groupID, platform: PlatformOpenAI, model: "gpt-image-edit"}] = &ChannelModelPricing{
		BillingMode:     BillingModeToken,
		InputPrice:      &inputPrice,
		ImageInputPrice: &imageInputPrice,
		OutputPrice:     &outputPrice,
	}
	cache.channelByGroupID[groupID] = &Channel{ID: groupID, Status: StatusActive}
	cache.groupPlatform[groupID] = PlatformOpenAI
	cache.loadedAt = time.Now()
	channelService := &ChannelService{}
	channelService.cache.Store(cache)

	billingService := NewBillingService(nil, nil)
	svc := NewModelMarketplaceService(nil, nil, &GatewayService{
		resolver: NewModelPricingResolver(channelService, billingService),
	}, billingService, nil, nil, nil)
	group := &Group{ID: groupID, Platform: PlatformOpenAI, RateMultiplier: 1.5}

	pricing := svc.getPublicModelDisplayPricing(context.Background(), group, "gpt-image-edit", nil)

	if pricing.PricingMode != "token" || pricing.PriceStatus != "priced" {
		t.Fatalf("image edit pricing = (%q, %q), want token/priced", pricing.PricingMode, pricing.PriceStatus)
	}
	if pricing.ImageInputPricePerToken != imageInputPrice*group.RateMultiplier {
		t.Fatalf("image input price = %g, want %g", pricing.ImageInputPricePerToken, imageInputPrice*group.RateMultiplier)
	}
}

func TestModelDisplayPricingImageInputFastRates(t *testing.T) {
	// 此测试不带 unit 构建标签，因此使用局部值，避免依赖标签专用测试 helper。
	fastModeMultiplier := 3.0
	tests := []struct {
		name          string
		pricing       ModelPricing
		wantImage     float64
		wantFastImage float64
	}{
		{
			name: "priority 倍率同步应用到图片输入价",
			pricing: ModelPricing{
				InputPricePerToken:      0.01,
				ImageInputPricePerToken: 0.03,
				SupportsServiceTier:     true,
			},
			wantImage:     0.06,
			wantFastImage: 0.12,
		},
		{
			name: "独立 priority 文本价不改变显式图片输入价",
			pricing: ModelPricing{
				InputPricePerToken:         0.01,
				InputPricePerTokenPriority: 0.04,
				ImageInputPricePerToken:    0.03,
			},
			wantImage:     0.06,
			wantFastImage: 0.06,
		},
		{
			name: "渠道 Fast 倍率同步应用到显式图片输入价",
			pricing: ModelPricing{
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
			pricing := buildTokenDisplayPricing(&tt.pricing, 2)
			if pricing.ImageInputPricePerToken != tt.wantImage {
				t.Fatalf("image input price = %g, want %g", pricing.ImageInputPricePerToken, tt.wantImage)
			}
			if pricing.FastImageInputPricePerToken != tt.wantFastImage {
				t.Fatalf("fast image input price = %g, want %g", pricing.FastImageInputPricePerToken, tt.wantFastImage)
			}
		})
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

func TestModelMarketplaceQoderRequestedBasisDoesNotInferRouteKeyPricing(t *testing.T) {
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

	if pricing.PricingMode != "unknown" || pricing.PriceStatus != "unpriced" {
		t.Fatalf("Qoder requested-basis display price = (%q, %q), want unknown/unpriced", pricing.PricingMode, pricing.PriceStatus)
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
	cache.channelByGroupID[groupID] = &Channel{ID: groupID, Status: StatusActive, BillingModelSource: BillingModelSourceUpstream}
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

	var customModel *ModelMarketplaceModel
	for i := range models {
		if models[i].ID == "custom-qoder-model" {
			customModel = &models[i]
			break
		}
	}
	if customModel == nil {
		t.Fatalf("Qoder account-mapped marketplace models = %#v, want custom-qoder-model", models)
	}
	pricing := customModel.Pricing
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

func TestModelMarketplaceGroupPricingOverridesChannelPricing(t *testing.T) {
	groupID := int64(905)
	channelInput := 0.5
	channelOutput := 0.75
	groupInput := 0.01
	groupOutput := 0.02
	cache := newEmptyChannelCache()
	cache.pricingByGroupModel[channelModelKey{groupID: groupID, platform: PlatformOpenAI, model: "gpt-5.4-mini"}] = &ChannelModelPricing{
		BillingMode: BillingModeToken,
		InputPrice:  &channelInput,
		OutputPrice: &channelOutput,
	}
	cache.channelByGroupID[groupID] = &Channel{ID: groupID, Status: StatusActive}
	cache.groupPlatform[groupID] = PlatformOpenAI
	cache.loadedAt = time.Now()
	channelService := &ChannelService{}
	channelService.cache.Store(cache)

	billingService := NewBillingService(nil, nil)
	svc := NewModelMarketplaceService(nil, nil, &GatewayService{
		resolver: NewModelPricingResolver(channelService, billingService),
	}, billingService, nil, nil, nil)
	group := &Group{
		ID: groupID, Platform: PlatformOpenAI, RateMultiplier: 2, LongContextPricingEnabled: true,
		ModelPricing: []ChannelModelPricing{{
			Models: []string{"gpt-5.4-mini"}, BillingMode: BillingModeToken,
			InputPrice: &groupInput, OutputPrice: &groupOutput,
		}},
	}

	pricing := svc.getPublicModelDisplayPricing(context.Background(), group, "gpt-5.4-mini", nil)

	if pricing.InputPricePerToken != groupInput*group.RateMultiplier || pricing.OutputPricePerToken != groupOutput*group.RateMultiplier {
		t.Fatalf("group display price = (%g, %g), want (%g, %g)",
			pricing.InputPricePerToken, pricing.OutputPricePerToken,
			groupInput*group.RateMultiplier, groupOutput*group.RateMultiplier)
	}
}

func TestModelMarketplaceGroupExplicitZeroPricingRemainsPriced(t *testing.T) {
	zero := 0.0
	billingService := NewBillingService(nil, nil)
	svc := NewModelMarketplaceService(nil, nil, &GatewayService{
		resolver: NewModelPricingResolver(nil, billingService),
	}, billingService, nil, nil, nil)
	group := &Group{
		ID: 906, Platform: PlatformOpenAI, RateMultiplier: 1, LongContextPricingEnabled: true,
		ModelPricing: []ChannelModelPricing{{
			Models: []string{"gpt-5.4"}, BillingMode: BillingModeToken,
			InputPrice: &zero, OutputPrice: &zero,
		}},
	}

	pricing := svc.getPublicModelDisplayPricing(context.Background(), group, "gpt-5.4", nil)

	if pricing.PricingMode != "token" || pricing.PriceStatus != "priced" || pricing.InputPricePerToken != 0 || pricing.OutputPricePerToken != 0 {
		t.Fatalf("free group display pricing = %#v, want token/priced with zero prices", pricing)
	}
}

func TestModelMarketplaceGroupCanDisableBuiltInLongContextDisplay(t *testing.T) {
	billingService := NewBillingService(nil, nil)
	svc := NewModelMarketplaceService(nil, nil, &GatewayService{
		resolver: NewModelPricingResolver(nil, billingService),
	}, billingService, nil, nil, nil)
	group := &Group{ID: 907, Platform: PlatformOpenAI, RateMultiplier: 1, LongContextPricingEnabled: false}

	pricing := svc.getPublicModelDisplayPricing(context.Background(), group, "gpt-5.4", nil)

	if pricing.PricingMode != "token" || pricing.PriceStatus != "priced" || len(pricing.ContextIntervals) != 0 {
		t.Fatalf("long-context-disabled display pricing = %#v, want flat token pricing", pricing)
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

func TestModelMarketplaceListPublicPrefetchesAccountsOnce(t *testing.T) {
	groups := []Group{
		{ID: 4101, Name: "OpenAI A", Platform: PlatformOpenAI, Status: StatusActive, RateMultiplier: 1, ActiveAccountCount: 1},
		{ID: 4102, Name: "OpenAI B", Platform: PlatformOpenAI, Status: StatusActive, RateMultiplier: 1, ActiveAccountCount: 1},
	}
	accounts := []Account{
		{
			ID:       5101,
			Platform: PlatformOpenAI,
			GroupIDs: []int64{4101},
			AccountGroups: []AccountGroup{{
				AccountID: 5101,
				GroupID:   4101,
			}},
			Credentials: map[string]any{
				"model_mapping":   map[string]any{"client-a": "upstream-a"},
				"model_whitelist": []any{"upstream-a"},
			},
		},
		{
			ID:       5102,
			Platform: PlatformOpenAI,
			GroupIDs: []int64{4102},
			AccountGroups: []AccountGroup{{
				AccountID: 5102,
				GroupID:   4102,
			}},
			Credentials: map[string]any{
				"model_mapping":   map[string]any{"client-b": "upstream-b"},
				"model_whitelist": []any{"upstream-b"},
			},
		},
	}
	accountRepo := &modelsListAccountRepoStub{
		all: accounts,
		byGroup: map[int64][]Account{
			4101: {accounts[0]},
			4102: {accounts[1]},
		},
	}
	gatewayService := &GatewayService{accountRepo: accountRepo}
	service := NewModelMarketplaceService(
		&marketplaceGroupRepoStub{groups: groups},
		nil,
		gatewayService,
		NewBillingService(nil, nil),
		nil,
		nil,
		nil,
	)

	result, err := service.ListPublic(context.Background())
	if err != nil {
		t.Fatalf("ListPublic returned error: %v", err)
	}
	if len(result) != 2 {
		t.Fatalf("ListPublic returned %d groups, want 2", len(result))
	}
	if accountRepo.listAllCalls.Load() != 1 || accountRepo.listByGroupCalls.Load() != 0 {
		t.Fatalf("account queries = all:%d by_group:%d, want all:1 by_group:0", accountRepo.listAllCalls.Load(), accountRepo.listByGroupCalls.Load())
	}
	groupAModels := make(map[string]struct{}, len(result[0].Models))
	for _, model := range result[0].Models {
		groupAModels[model.ID] = struct{}{}
	}
	groupBModels := make(map[string]struct{}, len(result[1].Models))
	for _, model := range result[1].Models {
		groupBModels[model.ID] = struct{}{}
	}
	if _, ok := groupAModels["client-a"]; !ok {
		t.Fatalf("group A models = %#v, want client-a", result[0].Models)
	}
	if _, leaked := groupAModels["client-b"]; leaked {
		t.Fatalf("group A models = %#v, must not contain client-b", result[0].Models)
	}
	if _, ok := groupBModels["client-b"]; !ok {
		t.Fatalf("group B models = %#v, want client-b", result[1].Models)
	}
	if _, leaked := groupBModels["client-a"]; leaked {
		t.Fatalf("group B models = %#v, must not contain client-a", result[1].Models)
	}
}

func TestModelMarketplacePrefetchSortsByGlobalAccountPriority(t *testing.T) {
	accountRepo := &modelsListAccountRepoStub{all: []Account{
		{ID: 5103, Priority: 10, GroupIDs: []int64{4101}},
		{ID: 5102, Priority: 5, GroupIDs: []int64{4101}},
		{ID: 5101, Priority: 5, GroupIDs: []int64{4101}},
	}}
	svc := NewModelMarketplaceService(nil, nil, &GatewayService{accountRepo: accountRepo}, nil, nil, nil, nil)

	accountsByGroup, ok := svc.prefetchPublicGroupAccounts(context.Background())

	if !ok {
		t.Fatal("prefetchPublicGroupAccounts should succeed")
	}
	accounts := accountsByGroup[4101]
	if len(accounts) != 3 {
		t.Fatalf("prefetched accounts = %d, want 3", len(accounts))
	}
	if accounts[0].ID != 5101 || accounts[1].ID != 5102 || accounts[2].ID != 5103 {
		t.Fatalf("prefetched account order = [%d %d %d], want [5101 5102 5103]", accounts[0].ID, accounts[1].ID, accounts[2].ID)
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
