package service

import "testing"

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
