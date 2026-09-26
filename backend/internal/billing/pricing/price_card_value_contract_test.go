//go:build unit

package pricing_test

import (
	"testing"

	"github.com/TokenFlux/TokenRouter/internal/billing/pricing"

	"github.com/stretchr/testify/require"
)

func TestGetIntervalForContext(t *testing.T) {
	p := &pricing.ModelPricingEntry{
		Intervals: []pricing.PricingInterval{
			{MinTokens: 0, MaxTokens: new(int(128000)), InputPrice: new(float64(1e-6))},
			{MinTokens: 128000, MaxTokens: nil, InputPrice: new(float64(2e-6))},
		},
	}

	tests := []struct {
		name      string
		tokens    int
		wantPrice *float64
		wantNil   bool
	}{
		{"first interval", 50000, new(float64(1e-6)), false},
		// (min, max] — 128000 在第一个区间的 max，包含，所以匹配第一个
		{"boundary: max of first (inclusive)", 128000, new(float64(1e-6)), false},
		// 128001 > 128000，匹配第二个区间
		{"boundary: just above first max", 128001, new(float64(2e-6)), false},
		{"unbounded interval", 500000, new(float64(2e-6)), false},
		// (0, max] — 0 不匹配任何区间（左开）
		{"zero tokens: no match", 0, nil, true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := p.GetIntervalForContext(tt.tokens)
			if tt.wantNil {
				require.Nil(t, result)
				return
			}
			require.NotNil(t, result)
			require.InDelta(t, *tt.wantPrice, *result.InputPrice, 1e-12)
		})
	}
}

func TestGetIntervalForContext_NoMatch(t *testing.T) {
	p := &pricing.ModelPricingEntry{
		Intervals: []pricing.PricingInterval{
			{MinTokens: 10000, MaxTokens: new(int(50000))},
		},
	}
	require.Nil(t, p.GetIntervalForContext(5000))     // 5000 <= 10000, not > min
	require.Nil(t, p.GetIntervalForContext(10000))    // 10000 not > 10000 (left-open)
	require.NotNil(t, p.GetIntervalForContext(50000)) // 50000 <= 50000 (right-closed)
	require.Nil(t, p.GetIntervalForContext(50001))    // 50001 > 50000
}

func TestGetIntervalForContext_Empty(t *testing.T) {
	p := &pricing.ModelPricingEntry{Intervals: nil}
	require.Nil(t, p.GetIntervalForContext(1000))
}

func TestGetTierByLabel(t *testing.T) {
	p := &pricing.ModelPricingEntry{
		Intervals: []pricing.PricingInterval{
			{TierLabel: "1K", PerRequestPrice: new(float64(0.04))},
			{TierLabel: "2K", PerRequestPrice: new(float64(0.08))},
			{TierLabel: "HD", PerRequestPrice: new(float64(0.12))},
		},
	}

	tests := []struct {
		name    string
		label   string
		wantNil bool
		want    float64
	}{
		{"exact match", "1K", false, 0.04},
		{"case insensitive", "hd", false, 0.12},
		{"not found", "4K", true, 0},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := p.GetTierByLabel(tt.label)
			if tt.wantNil {
				require.Nil(t, result)
				return
			}
			require.NotNil(t, result)
			require.InDelta(t, tt.want, *result.PerRequestPrice, 1e-12)
		})
	}
}

func TestGetTierByLabel_Empty(t *testing.T) {
	p := &pricing.ModelPricingEntry{Intervals: nil}
	require.Nil(t, p.GetTierByLabel("1K"))
}

func TestModelPricingEntryClone(t *testing.T) {
	original := pricing.ModelPricingEntry{
		Models: []string{"a", "b"},
		Intervals: []pricing.PricingInterval{
			{MinTokens: 0, TierLabel: "tier1"},
		},
		TimePricing: &pricing.TimePricingConfig{
			Timezone:     "Asia/Shanghai",
			WeekdaysOnly: true,
			Periods: []pricing.TimePricingPeriod{{
				StartTime:  "09:00",
				EndTime:    "12:00",
				Multiplier: 2,
			}},
		},
	}

	cloned := original.Clone()

	// Modify clone slices — original unchanged
	cloned.Models[0] = "hacked"
	require.Equal(t, "a", original.Models[0])

	cloned.Intervals[0].TierLabel = "hacked"
	require.Equal(t, "tier1", original.Intervals[0].TierLabel)

	cloned.TimePricing.Timezone = "America/New_York"
	cloned.TimePricing.WeekdaysOnly = false
	cloned.TimePricing.Periods[0].StartTime = "10:00"
	cloned.TimePricing.Periods[0].Multiplier = 3
	require.Equal(t, "Asia/Shanghai", original.TimePricing.Timezone)
	require.True(t, original.TimePricing.WeekdaysOnly)
	require.Equal(t, "09:00", original.TimePricing.Periods[0].StartTime)
	require.Equal(t, 2.0, original.TimePricing.Periods[0].Multiplier)
}

func TestBillingModeIsValid(t *testing.T) {
	tests := []struct {
		name string
		mode pricing.BillingMode
		want bool
	}{
		{"token", pricing.BillingModeToken, true},
		{"per_request", pricing.BillingModePerRequest, true},
		{"image", pricing.BillingModeImage, true},
		{"empty", pricing.BillingMode(""), true},
		{"unknown", pricing.BillingMode("unknown"), false},
		{"random", pricing.BillingMode("xyz"), false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			require.Equal(t, tt.want, tt.mode.IsValid())
		})
	}
}

func TestModelPricingEntryClone_EdgeCases(t *testing.T) {
	t.Run("nil models", func(t *testing.T) {
		original := pricing.ModelPricingEntry{Models: nil}
		cloned := original.Clone()
		require.Nil(t, cloned.Models)
	})

	t.Run("nil intervals", func(t *testing.T) {
		original := pricing.ModelPricingEntry{Intervals: nil}
		cloned := original.Clone()
		require.Nil(t, cloned.Intervals)
	})

	t.Run("empty models", func(t *testing.T) {
		original := pricing.ModelPricingEntry{Models: []string{}}
		cloned := original.Clone()
		require.NotNil(t, cloned.Models)
		require.Empty(t, cloned.Models)
	})
}

func TestValidateIntervals_Empty(t *testing.T) {
	require.NoError(t, pricing.ValidateIntervals(nil, pricing.BillingModeToken))
	require.NoError(t, pricing.ValidateIntervals([]pricing.PricingInterval{}, pricing.BillingModeToken))
}

func TestValidateIntervals_ValidIntervals(t *testing.T) {
	tests := []struct {
		name      string
		intervals []pricing.PricingInterval
	}{
		{
			name: "single bounded interval",
			intervals: []pricing.PricingInterval{
				{MinTokens: 0, MaxTokens: new(int(128000)), InputPrice: new(float64(1e-6))},
			},
		},
		{
			name: "two intervals with gap",
			intervals: []pricing.PricingInterval{
				{MinTokens: 0, MaxTokens: new(int(100000)), InputPrice: new(float64(1e-6))},
				{MinTokens: 128000, MaxTokens: nil, InputPrice: new(float64(2e-6))},
			},
		},
		{
			name: "two contiguous intervals",
			intervals: []pricing.PricingInterval{
				{MinTokens: 0, MaxTokens: new(int(128000)), InputPrice: new(float64(1e-6))},
				{MinTokens: 128000, MaxTokens: nil, InputPrice: new(float64(2e-6))},
			},
		},
		{
			name: "unsorted input (auto-sorted by validator)",
			intervals: []pricing.PricingInterval{
				{MinTokens: 128000, MaxTokens: nil, InputPrice: new(float64(2e-6))},
				{MinTokens: 0, MaxTokens: new(int(128000)), InputPrice: new(float64(1e-6))},
			},
		},
		{
			name: "single unbounded interval",
			intervals: []pricing.PricingInterval{
				{MinTokens: 0, MaxTokens: nil, InputPrice: new(float64(1e-6))},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			require.NoError(t, pricing.ValidateIntervals(tt.intervals, pricing.BillingModeToken))
		})
	}
}

func TestValidateIntervals_NegativeMinTokens(t *testing.T) {
	intervals := []pricing.PricingInterval{
		{MinTokens: -1, MaxTokens: new(int(100)), InputPrice: new(float64(1e-6))},
	}
	err := pricing.ValidateIntervals(intervals, pricing.BillingModeToken)
	require.Error(t, err)
	require.Contains(t, err.Error(), "min_tokens")
	require.Contains(t, err.Error(), ">= 0")
}

func TestValidateIntervals_MaxTokensZero(t *testing.T) {
	intervals := []pricing.PricingInterval{
		{MinTokens: 0, MaxTokens: new(int(0)), InputPrice: new(float64(1e-6))},
	}
	err := pricing.ValidateIntervals(intervals, pricing.BillingModeToken)
	require.Error(t, err)
	require.Contains(t, err.Error(), "max_tokens")
	require.Contains(t, err.Error(), "> 0")
}

func TestValidateIntervals_MaxLessThanMin(t *testing.T) {
	intervals := []pricing.PricingInterval{
		{MinTokens: 100, MaxTokens: new(int(50)), InputPrice: new(float64(1e-6))},
	}
	err := pricing.ValidateIntervals(intervals, pricing.BillingModeToken)
	require.Error(t, err)
	require.Contains(t, err.Error(), "max_tokens")
	require.Contains(t, err.Error(), "> min_tokens")
}

func TestValidateIntervals_MaxEqualsMin(t *testing.T) {
	intervals := []pricing.PricingInterval{
		{MinTokens: 100, MaxTokens: new(int(100)), InputPrice: new(float64(1e-6))},
	}
	err := pricing.ValidateIntervals(intervals, pricing.BillingModeToken)
	require.Error(t, err)
	require.Contains(t, err.Error(), "max_tokens")
	require.Contains(t, err.Error(), "> min_tokens")
}

func TestValidateIntervals_NegativePrice(t *testing.T) {
	negPrice := -0.01
	intervals := []pricing.PricingInterval{
		{MinTokens: 0, MaxTokens: new(int(100)), InputPrice: &negPrice},
	}
	err := pricing.ValidateIntervals(intervals, pricing.BillingModeToken)
	require.Error(t, err)
	require.Contains(t, err.Error(), "input_price")
	require.Contains(t, err.Error(), ">= 0")
}

func TestValidateIntervals_OverlappingIntervals(t *testing.T) {
	intervals := []pricing.PricingInterval{
		{MinTokens: 0, MaxTokens: new(int(200)), InputPrice: new(float64(1e-6))},
		{MinTokens: 100, MaxTokens: new(int(300)), InputPrice: new(float64(2e-6))},
	}
	err := pricing.ValidateIntervals(intervals, pricing.BillingModeToken)
	require.Error(t, err)
	require.Contains(t, err.Error(), "overlap")
}

func TestValidateIntervals_UnboundedNotLast(t *testing.T) {
	intervals := []pricing.PricingInterval{
		{MinTokens: 0, MaxTokens: nil, InputPrice: new(float64(1e-6))},
		{MinTokens: 128000, MaxTokens: new(int(256000)), InputPrice: new(float64(2e-6))},
	}
	err := pricing.ValidateIntervals(intervals, pricing.BillingModeToken)
	require.Error(t, err)
	require.Contains(t, err.Error(), "unbounded")
	require.Contains(t, err.Error(), "last")
}

func TestValidateIntervals_ImageModeAllowsMultipleUnboundedTiers(t *testing.T) {
	// image / per_request 按 tier_label 匹配，多条 min=0/max=nil 是合法形态。
	intervals := []pricing.PricingInterval{
		{MinTokens: 0, MaxTokens: nil, TierLabel: "1K", PerRequestPrice: new(float64(0.04))},
		{MinTokens: 0, MaxTokens: nil, TierLabel: "2K", PerRequestPrice: new(float64(0.06))},
		{MinTokens: 0, MaxTokens: nil, TierLabel: "4K", PerRequestPrice: new(float64(0.08))},
	}
	require.NoError(t, pricing.ValidateIntervals(intervals, pricing.BillingModeImage))
	require.NoError(t, pricing.ValidateIntervals(intervals, pricing.BillingModePerRequest))
}

func TestValidateIntervals_ImageModeStillRejectsNegativePrice(t *testing.T) {
	// image 模式只跳过区间重叠校验，单条字段自洽（价格非负）仍要校验。
	intervals := []pricing.PricingInterval{
		{MinTokens: 0, MaxTokens: nil, TierLabel: "1K", PerRequestPrice: new(float64(-1))},
	}
	err := pricing.ValidateIntervals(intervals, pricing.BillingModeImage)
	require.Error(t, err)
	require.Contains(t, err.Error(), "must be >= 0")
}

func TestValidateIntervals_ImageModeStillRejectsBadMaxTokens(t *testing.T) {
	// image 模式仍校验 max <= min 这种单条不合法。
	intervals := []pricing.PricingInterval{
		{MinTokens: 100, MaxTokens: new(int(50)), TierLabel: "1K", PerRequestPrice: new(float64(0.04))},
	}
	err := pricing.ValidateIntervals(intervals, pricing.BillingModeImage)
	require.Error(t, err)
	require.Contains(t, err.Error(), "must be > min_tokens")
}
