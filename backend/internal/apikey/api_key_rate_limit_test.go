package apikey_test

import (
	"testing"
	"time"

	"github.com/TokenFlux/TokenRouter/internal/apikey"
)

func TestIsWindowExpired(t *testing.T) {
	now := time.Now()

	tests := []struct {
		name     string
		start    *time.Time
		duration time.Duration
		want     bool
	}{
		{
			name:     "nil window start (treated as expired)",
			start:    nil,
			duration: apikey.RateLimitWindow5h,
			want:     true,
		},
		{
			name:     "active window (started 1h ago, 5h window)",
			start:    rateLimitTimePtr(now.Add(-1 * time.Hour)),
			duration: apikey.RateLimitWindow5h,
			want:     false,
		},
		{
			name:     "expired window (started 6h ago, 5h window)",
			start:    rateLimitTimePtr(now.Add(-6 * time.Hour)),
			duration: apikey.RateLimitWindow5h,
			want:     true,
		},
		{
			name:     "exactly at boundary (started 5h ago, 5h window)",
			start:    rateLimitTimePtr(now.Add(-5 * time.Hour)),
			duration: apikey.RateLimitWindow5h,
			want:     true,
		},
		{
			name:     "active 1d window (started 12h ago)",
			start:    rateLimitTimePtr(now.Add(-12 * time.Hour)),
			duration: apikey.RateLimitWindow1d,
			want:     false,
		},
		{
			name:     "expired 1d window (started 25h ago)",
			start:    rateLimitTimePtr(now.Add(-25 * time.Hour)),
			duration: apikey.RateLimitWindow1d,
			want:     true,
		},
		{
			name:     "active 7d window (started 3d ago)",
			start:    rateLimitTimePtr(now.Add(-3 * 24 * time.Hour)),
			duration: apikey.RateLimitWindow7d,
			want:     false,
		},
		{
			name:     "expired 7d window (started 8d ago)",
			start:    rateLimitTimePtr(now.Add(-8 * 24 * time.Hour)),
			duration: apikey.RateLimitWindow7d,
			want:     true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := apikey.IsWindowExpired(tt.start, tt.duration)
			if got != tt.want {
				t.Errorf("IsWindowExpired() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestAPIKey_EffectiveUsage(t *testing.T) {
	now := time.Now()

	tests := []struct {
		name   string
		key    apikey.APIKey
		want5h float64
		want1d float64
		want7d float64
	}{
		{
			name: "all windows active",
			key: apikey.APIKey{
				Usage5h:       5.0,
				Usage1d:       10.0,
				Usage7d:       50.0,
				Window5hStart: rateLimitTimePtr(now.Add(-1 * time.Hour)),
				Window1dStart: rateLimitTimePtr(now.Add(-12 * time.Hour)),
				Window7dStart: rateLimitTimePtr(now.Add(-3 * 24 * time.Hour)),
			},
			want5h: 5.0,
			want1d: 10.0,
			want7d: 50.0,
		},
		{
			name: "all windows expired",
			key: apikey.APIKey{
				Usage5h:       5.0,
				Usage1d:       10.0,
				Usage7d:       50.0,
				Window5hStart: rateLimitTimePtr(now.Add(-6 * time.Hour)),
				Window1dStart: rateLimitTimePtr(now.Add(-25 * time.Hour)),
				Window7dStart: rateLimitTimePtr(now.Add(-8 * 24 * time.Hour)),
			},
			want5h: 0,
			want1d: 0,
			want7d: 0,
		},
		{
			name: "nil window starts return 0 (stale usage reset)",
			key: apikey.APIKey{
				Usage5h:       5.0,
				Usage1d:       10.0,
				Usage7d:       50.0,
				Window5hStart: nil,
				Window1dStart: nil,
				Window7dStart: nil,
			},
			want5h: 0,
			want1d: 0,
			want7d: 0,
		},
		{
			name: "mixed: 5h expired, 1d active, 7d nil",
			key: apikey.APIKey{
				Usage5h:       5.0,
				Usage1d:       10.0,
				Usage7d:       50.0,
				Window5hStart: rateLimitTimePtr(now.Add(-6 * time.Hour)),
				Window1dStart: rateLimitTimePtr(now.Add(-12 * time.Hour)),
				Window7dStart: nil,
			},
			want5h: 0,
			want1d: 10.0,
			want7d: 0,
		},
		{
			name: "zero usage with active windows",
			key: apikey.APIKey{
				Usage5h:       0,
				Usage1d:       0,
				Usage7d:       0,
				Window5hStart: rateLimitTimePtr(now.Add(-1 * time.Hour)),
				Window1dStart: rateLimitTimePtr(now.Add(-1 * time.Hour)),
				Window7dStart: rateLimitTimePtr(now.Add(-1 * time.Hour)),
			},
			want5h: 0,
			want1d: 0,
			want7d: 0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.key.EffectiveUsage5h(); got != tt.want5h {
				t.Errorf("EffectiveUsage5h() = %v, want %v", got, tt.want5h)
			}
			if got := tt.key.EffectiveUsage1d(); got != tt.want1d {
				t.Errorf("EffectiveUsage1d() = %v, want %v", got, tt.want1d)
			}
			if got := tt.key.EffectiveUsage7d(); got != tt.want7d {
				t.Errorf("EffectiveUsage7d() = %v, want %v", got, tt.want7d)
			}
		})
	}
}

func TestAPIKeyRateLimitData_EffectiveUsage(t *testing.T) {
	now := time.Now()

	tests := []struct {
		name   string
		data   apikey.APIKeyRateLimitData
		want5h float64
		want1d float64
		want7d float64
	}{
		{
			name: "all windows active",
			data: apikey.APIKeyRateLimitData{
				Usage5h:       3.0,
				Usage1d:       8.0,
				Usage7d:       40.0,
				Window5hStart: rateLimitTimePtr(now.Add(-2 * time.Hour)),
				Window1dStart: rateLimitTimePtr(now.Add(-10 * time.Hour)),
				Window7dStart: rateLimitTimePtr(now.Add(-2 * 24 * time.Hour)),
			},
			want5h: 3.0,
			want1d: 8.0,
			want7d: 40.0,
		},
		{
			name: "all windows expired",
			data: apikey.APIKeyRateLimitData{
				Usage5h:       3.0,
				Usage1d:       8.0,
				Usage7d:       40.0,
				Window5hStart: rateLimitTimePtr(now.Add(-10 * time.Hour)),
				Window1dStart: rateLimitTimePtr(now.Add(-48 * time.Hour)),
				Window7dStart: rateLimitTimePtr(now.Add(-10 * 24 * time.Hour)),
			},
			want5h: 0,
			want1d: 0,
			want7d: 0,
		},
		{
			name: "nil window starts return 0 (stale usage reset)",
			data: apikey.APIKeyRateLimitData{
				Usage5h:       3.0,
				Usage1d:       8.0,
				Usage7d:       40.0,
				Window5hStart: nil,
				Window1dStart: nil,
				Window7dStart: nil,
			},
			want5h: 0,
			want1d: 0,
			want7d: 0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.data.EffectiveUsage5h(); got != tt.want5h {
				t.Errorf("EffectiveUsage5h() = %v, want %v", got, tt.want5h)
			}
			if got := tt.data.EffectiveUsage1d(); got != tt.want1d {
				t.Errorf("EffectiveUsage1d() = %v, want %v", got, tt.want1d)
			}
			if got := tt.data.EffectiveUsage7d(); got != tt.want7d {
				t.Errorf("EffectiveUsage7d() = %v, want %v", got, tt.want7d)
			}
		})
	}
}

func rateLimitTimePtr(t time.Time) *time.Time {
	return &t
}
