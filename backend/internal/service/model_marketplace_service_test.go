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
