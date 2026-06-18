package service

import (
	"context"
	"testing"
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

func TestModelMarketplaceQoderPricingDeferredByDefault(t *testing.T) {
	svc := NewModelMarketplaceService(nil, nil, nil, NewBillingService(nil, nil), nil, nil, nil)
	group := &Group{ID: 1, Platform: PlatformQoder, RateMultiplier: 1}

	pricing := svc.getPublicModelDisplayPricing(context.Background(), group, "claude-sonnet-4", nil)

	if pricing.PricingMode != "unknown" || pricing.PriceStatus != "unpriced" {
		t.Fatalf("Qoder marketplace pricing = (%q, %q), want unknown/unpriced", pricing.PricingMode, pricing.PriceStatus)
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
