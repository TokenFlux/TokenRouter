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

func TestModelMarketplaceQoderNonAliasPricingRemainsUnknown(t *testing.T) {
	svc := NewModelMarketplaceService(nil, nil, nil, NewBillingService(nil, nil), nil, nil, nil)
	group := &Group{ID: 1, Platform: PlatformQoder, RateMultiplier: 1}

	pricing := svc.getPublicModelDisplayPricing(context.Background(), group, "claude-sonnet-4", nil)

	if pricing.PricingMode != "unknown" || pricing.PriceStatus != "unpriced" {
		t.Fatalf("Qoder marketplace pricing = (%q, %q), want unknown/unpriced", pricing.PricingMode, pricing.PriceStatus)
	}
}

func TestModelMarketplaceQoderDefaultAliasesUseOpus48DisplayPricing(t *testing.T) {
	billingService := NewBillingService(nil, nil)
	svc := NewModelMarketplaceService(nil, nil, nil, billingService, nil, nil, nil)
	group := &Group{ID: 1, Platform: PlatformQoder, RateMultiplier: 1.25}

	pricing := svc.getPublicModelDisplayPricing(context.Background(), group, "auto", nil)
	expected := billingService.GetDisplayPricing(qoderDefaultAliasFallbackBillingModel, group.RateMultiplier, nil)

	if pricing.PricingMode != "token" || pricing.PriceStatus != "priced" {
		t.Fatalf("Qoder default alias pricing = (%q, %q), want token/priced", pricing.PricingMode, pricing.PriceStatus)
	}
	if pricing.InputPricePerToken != expected.InputPricePerToken || pricing.OutputPricePerToken != expected.OutputPricePerToken {
		t.Fatalf("Qoder default alias price = (%g, %g), want (%g, %g)", pricing.InputPricePerToken, pricing.OutputPricePerToken, expected.InputPricePerToken, expected.OutputPricePerToken)
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
		if model.Pricing.PriceStatus != "priced" {
			t.Fatalf("Qoder model %s price status = %q, want priced", model.ID, model.Pricing.PriceStatus)
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
