//go:build unit

package service

import (
	"testing"

	"github.com/TokenFlux/TokenRouter/internal/billing"
	"github.com/TokenFlux/TokenRouter/internal/routing"
	"github.com/stretchr/testify/require"
)

func TestGetModelPricing(t *testing.T) {
	ch := &routing.Channel{
		ModelPricing: []routing.ChannelModelPricing{
			{ID: 1, Models: []string{"claude-sonnet-4"}, BillingMode: routing.BillingModeToken, InputPrice: testPtrFloat64(3e-6)},
			{ID: 3, Models: []string{"gpt-5.1"}, BillingMode: routing.BillingModePerRequest},
		},
	}

	tests := []struct {
		name    string
		model   string
		wantID  int64
		wantNil bool
	}{
		{"exact match", "claude-sonnet-4", 1, false},
		{"case insensitive", "Claude-Sonnet-4", 1, false},
		{"not found", "gemini-3.1-pro", 0, true},
		{"wildcard pattern not matched", "claude-opus-4-20250514", 0, true},
		{"per_request model", "gpt-5.1", 3, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := ch.GetModelPricing(tt.model)
			if tt.wantNil {
				require.Nil(t, result)
				return
			}
			require.NotNil(t, result)
			require.Equal(t, tt.wantID, result.ID)
		})
	}
}

func TestGetModelPricing_ReturnsCopy(t *testing.T) {
	ch := &routing.Channel{
		ModelPricing: []routing.ChannelModelPricing{
			{ID: 1, Models: []string{"claude-sonnet-4"}, InputPrice: testPtrFloat64(3e-6)},
		},
	}

	result := ch.GetModelPricing("claude-sonnet-4")
	require.NotNil(t, result)

	// Modify the returned copy's slice — original should be unchanged
	result.Models = append(result.Models, "hacked")

	// Original should be unchanged
	require.Equal(t, 1, len(ch.ModelPricing[0].Models))
}

func TestGetModelPricing_EmptyPricing(t *testing.T) {
	ch := &routing.Channel{ModelPricing: nil}
	require.Nil(t, ch.GetModelPricing("any-model"))

	ch2 := &routing.Channel{ModelPricing: []routing.ChannelModelPricing{}}
	require.Nil(t, ch2.GetModelPricing("any-model"))
}

func TestGetIntervalForContext(t *testing.T) {
	p := &routing.ChannelModelPricing{
		Intervals: []routing.PricingInterval{
			{MinTokens: 0, MaxTokens: testPtrInt(128000), InputPrice: testPtrFloat64(1e-6)},
			{MinTokens: 128000, MaxTokens: nil, InputPrice: testPtrFloat64(2e-6)},
		},
	}

	tests := []struct {
		name      string
		tokens    int
		wantPrice *float64
		wantNil   bool
	}{
		{"first interval", 50000, testPtrFloat64(1e-6), false},
		// (min, max] — 128000 在第一个区间的 max，包含，所以匹配第一个
		{"boundary: max of first (inclusive)", 128000, testPtrFloat64(1e-6), false},
		// 128001 > 128000，匹配第二个区间
		{"boundary: just above first max", 128001, testPtrFloat64(2e-6), false},
		{"unbounded interval", 500000, testPtrFloat64(2e-6), false},
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
	p := &routing.ChannelModelPricing{
		Intervals: []routing.PricingInterval{
			{MinTokens: 10000, MaxTokens: testPtrInt(50000)},
		},
	}
	require.Nil(t, p.GetIntervalForContext(5000))     // 5000 <= 10000, not > min
	require.Nil(t, p.GetIntervalForContext(10000))    // 10000 not > 10000 (left-open)
	require.NotNil(t, p.GetIntervalForContext(50000)) // 50000 <= 50000 (right-closed)
	require.Nil(t, p.GetIntervalForContext(50001))    // 50001 > 50000
}

func TestGetIntervalForContext_Empty(t *testing.T) {
	p := &routing.ChannelModelPricing{Intervals: nil}
	require.Nil(t, p.GetIntervalForContext(1000))
}

func TestGetTierByLabel(t *testing.T) {
	p := &routing.ChannelModelPricing{
		Intervals: []routing.PricingInterval{
			{TierLabel: "1K", PerRequestPrice: testPtrFloat64(0.04)},
			{TierLabel: "2K", PerRequestPrice: testPtrFloat64(0.08)},
			{TierLabel: "HD", PerRequestPrice: testPtrFloat64(0.12)},
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
	p := &routing.ChannelModelPricing{Intervals: nil}
	require.Nil(t, p.GetTierByLabel("1K"))
}

func TestChannelClone(t *testing.T) {
	original := &routing.Channel{
		ID:       1,
		Name:     "test",
		GroupIDs: []int64{10, 20},
		ModelPricing: []routing.ChannelModelPricing{
			{
				ID:         100,
				Models:     []string{"model-a"},
				InputPrice: testPtrFloat64(5e-6),
			},
		},
	}

	cloned := original.Clone()
	require.NotNil(t, cloned)
	require.Equal(t, original.ID, cloned.ID)
	require.Equal(t, original.Name, cloned.Name)

	// Modify clone slices — original should not change
	cloned.GroupIDs[0] = 999
	require.Equal(t, int64(10), original.GroupIDs[0])

	cloned.ModelPricing[0].Models[0] = "hacked"
	require.Equal(t, "model-a", original.ModelPricing[0].Models[0])
}

func TestChannelClone_Nil(t *testing.T) {
	var ch *routing.Channel
	require.Nil(t, ch.Clone())
}

func TestChannelModelPricingClone(t *testing.T) {
	original := routing.ChannelModelPricing{
		Models: []string{"a", "b"},
		Intervals: []routing.PricingInterval{
			{MinTokens: 0, TierLabel: "tier1"},
		},
		TimePricing: &routing.ChannelTimePricing{
			Timezone:     "Asia/Shanghai",
			WeekdaysOnly: true,
			Periods: []routing.ChannelTimePricingPeriod{{
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

// --- BillingMode.IsValid ---

func TestBillingModeIsValid(t *testing.T) {
	tests := []struct {
		name string
		mode routing.BillingMode
		want bool
	}{
		{"token", routing.BillingModeToken, true},
		{"per_request", routing.BillingModePerRequest, true},
		{"image", routing.BillingModeImage, true},
		{"empty", routing.BillingMode(""), true},
		{"unknown", routing.BillingMode("unknown"), false},
		{"random", routing.BillingMode("xyz"), false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			require.Equal(t, tt.want, tt.mode.IsValid())
		})
	}
}

// --- Channel.IsActive ---

func TestChannelIsActive(t *testing.T) {
	tests := []struct {
		name   string
		status string
		want   bool
	}{
		{"active", billing.StatusActive, true},
		{"disabled", "disabled", false},
		{"empty", "", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ch := &routing.Channel{Status: tt.status}
			require.Equal(t, tt.want, ch.IsActive())
		})
	}
}

// --- ChannelModelPricing.Clone edge cases ---

func TestChannelModelPricingClone_EdgeCases(t *testing.T) {
	t.Run("nil models", func(t *testing.T) {
		original := routing.ChannelModelPricing{Models: nil}
		cloned := original.Clone()
		require.Nil(t, cloned.Models)
	})

	t.Run("nil intervals", func(t *testing.T) {
		original := routing.ChannelModelPricing{Intervals: nil}
		cloned := original.Clone()
		require.Nil(t, cloned.Intervals)
	})

	t.Run("empty models", func(t *testing.T) {
		original := routing.ChannelModelPricing{Models: []string{}}
		cloned := original.Clone()
		require.NotNil(t, cloned.Models)
		require.Empty(t, cloned.Models)
	})
}

// --- Channel.Clone edge cases ---

func TestChannelClone_EdgeCases(t *testing.T) {
	t.Run("nil model mapping", func(t *testing.T) {
		original := &routing.Channel{ID: 1, ModelMapping: nil}
		cloned := original.Clone()
		require.Nil(t, cloned.ModelMapping)
	})

	t.Run("nil model pricing", func(t *testing.T) {
		original := &routing.Channel{ID: 1, ModelPricing: nil}
		cloned := original.Clone()
		require.Nil(t, cloned.ModelPricing)
	})

	t.Run("deep copy model mapping", func(t *testing.T) {
		original := &routing.Channel{
			ID: 1,
			ModelMapping: map[string]map[string]string{
				"openai": {"gpt-4": "gpt-4-turbo"},
			},
		}
		cloned := original.Clone()

		// Modify the cloned nested map
		cloned.ModelMapping["openai"]["gpt-4"] = "hacked"

		// Original must remain unchanged
		require.Equal(t, "gpt-4-turbo", original.ModelMapping["openai"]["gpt-4"])
	})
}

// --- ValidateIntervals ---

func TestValidateIntervals_Empty(t *testing.T) {
	require.NoError(t, routing.ValidateIntervals(nil, routing.BillingModeToken))
	require.NoError(t, routing.ValidateIntervals([]routing.PricingInterval{}, routing.BillingModeToken))
}

func TestValidateIntervals_ValidIntervals(t *testing.T) {
	tests := []struct {
		name      string
		intervals []routing.PricingInterval
	}{
		{
			name: "single bounded interval",
			intervals: []routing.PricingInterval{
				{MinTokens: 0, MaxTokens: testPtrInt(128000), InputPrice: testPtrFloat64(1e-6)},
			},
		},
		{
			name: "two intervals with gap",
			intervals: []routing.PricingInterval{
				{MinTokens: 0, MaxTokens: testPtrInt(100000), InputPrice: testPtrFloat64(1e-6)},
				{MinTokens: 128000, MaxTokens: nil, InputPrice: testPtrFloat64(2e-6)},
			},
		},
		{
			name: "two contiguous intervals",
			intervals: []routing.PricingInterval{
				{MinTokens: 0, MaxTokens: testPtrInt(128000), InputPrice: testPtrFloat64(1e-6)},
				{MinTokens: 128000, MaxTokens: nil, InputPrice: testPtrFloat64(2e-6)},
			},
		},
		{
			name: "unsorted input (auto-sorted by validator)",
			intervals: []routing.PricingInterval{
				{MinTokens: 128000, MaxTokens: nil, InputPrice: testPtrFloat64(2e-6)},
				{MinTokens: 0, MaxTokens: testPtrInt(128000), InputPrice: testPtrFloat64(1e-6)},
			},
		},
		{
			name: "single unbounded interval",
			intervals: []routing.PricingInterval{
				{MinTokens: 0, MaxTokens: nil, InputPrice: testPtrFloat64(1e-6)},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			require.NoError(t, routing.ValidateIntervals(tt.intervals, routing.BillingModeToken))
		})
	}
}

func TestValidateIntervals_NegativeMinTokens(t *testing.T) {
	intervals := []routing.PricingInterval{
		{MinTokens: -1, MaxTokens: testPtrInt(100), InputPrice: testPtrFloat64(1e-6)},
	}
	err := routing.ValidateIntervals(intervals, routing.BillingModeToken)
	require.Error(t, err)
	require.Contains(t, err.Error(), "min_tokens")
	require.Contains(t, err.Error(), ">= 0")
}

func TestValidateIntervals_MaxTokensZero(t *testing.T) {
	intervals := []routing.PricingInterval{
		{MinTokens: 0, MaxTokens: testPtrInt(0), InputPrice: testPtrFloat64(1e-6)},
	}
	err := routing.ValidateIntervals(intervals, routing.BillingModeToken)
	require.Error(t, err)
	require.Contains(t, err.Error(), "max_tokens")
	require.Contains(t, err.Error(), "> 0")
}

func TestValidateIntervals_MaxLessThanMin(t *testing.T) {
	intervals := []routing.PricingInterval{
		{MinTokens: 100, MaxTokens: testPtrInt(50), InputPrice: testPtrFloat64(1e-6)},
	}
	err := routing.ValidateIntervals(intervals, routing.BillingModeToken)
	require.Error(t, err)
	require.Contains(t, err.Error(), "max_tokens")
	require.Contains(t, err.Error(), "> min_tokens")
}

func TestValidateIntervals_MaxEqualsMin(t *testing.T) {
	intervals := []routing.PricingInterval{
		{MinTokens: 100, MaxTokens: testPtrInt(100), InputPrice: testPtrFloat64(1e-6)},
	}
	err := routing.ValidateIntervals(intervals, routing.BillingModeToken)
	require.Error(t, err)
	require.Contains(t, err.Error(), "max_tokens")
	require.Contains(t, err.Error(), "> min_tokens")
}

func TestValidateIntervals_NegativePrice(t *testing.T) {
	negPrice := -0.01
	intervals := []routing.PricingInterval{
		{MinTokens: 0, MaxTokens: testPtrInt(100), InputPrice: &negPrice},
	}
	err := routing.ValidateIntervals(intervals, routing.BillingModeToken)
	require.Error(t, err)
	require.Contains(t, err.Error(), "input_price")
	require.Contains(t, err.Error(), ">= 0")
}

func TestValidateIntervals_OverlappingIntervals(t *testing.T) {
	intervals := []routing.PricingInterval{
		{MinTokens: 0, MaxTokens: testPtrInt(200), InputPrice: testPtrFloat64(1e-6)},
		{MinTokens: 100, MaxTokens: testPtrInt(300), InputPrice: testPtrFloat64(2e-6)},
	}
	err := routing.ValidateIntervals(intervals, routing.BillingModeToken)
	require.Error(t, err)
	require.Contains(t, err.Error(), "overlap")
}

func TestValidateIntervals_UnboundedNotLast(t *testing.T) {
	intervals := []routing.PricingInterval{
		{MinTokens: 0, MaxTokens: nil, InputPrice: testPtrFloat64(1e-6)},
		{MinTokens: 128000, MaxTokens: testPtrInt(256000), InputPrice: testPtrFloat64(2e-6)},
	}
	err := routing.ValidateIntervals(intervals, routing.BillingModeToken)
	require.Error(t, err)
	require.Contains(t, err.Error(), "unbounded")
	require.Contains(t, err.Error(), "last")
}

func TestValidateIntervals_ImageModeAllowsMultipleUnboundedTiers(t *testing.T) {
	// image / per_request 按 tier_label 匹配，多条 min=0/max=nil 是合法形态。
	intervals := []routing.PricingInterval{
		{MinTokens: 0, MaxTokens: nil, TierLabel: "1K", PerRequestPrice: testPtrFloat64(0.04)},
		{MinTokens: 0, MaxTokens: nil, TierLabel: "2K", PerRequestPrice: testPtrFloat64(0.06)},
		{MinTokens: 0, MaxTokens: nil, TierLabel: "4K", PerRequestPrice: testPtrFloat64(0.08)},
	}
	require.NoError(t, routing.ValidateIntervals(intervals, routing.BillingModeImage))
	require.NoError(t, routing.ValidateIntervals(intervals, routing.BillingModePerRequest))
}

func TestValidateIntervals_ImageModeStillRejectsNegativePrice(t *testing.T) {
	// image 模式只跳过区间重叠校验，单条字段自洽（价格非负）仍要校验。
	intervals := []routing.PricingInterval{
		{MinTokens: 0, MaxTokens: nil, TierLabel: "1K", PerRequestPrice: testPtrFloat64(-1)},
	}
	err := routing.ValidateIntervals(intervals, routing.BillingModeImage)
	require.Error(t, err)
	require.Contains(t, err.Error(), "must be >= 0")
}

func TestValidateIntervals_ImageModeStillRejectsBadMaxTokens(t *testing.T) {
	// image 模式仍校验 max <= min 这种单条不合法。
	intervals := []routing.PricingInterval{
		{MinTokens: 100, MaxTokens: testPtrInt(50), TierLabel: "1K", PerRequestPrice: testPtrFloat64(0.04)},
	}
	err := routing.ValidateIntervals(intervals, routing.BillingModeImage)
	require.Error(t, err)
	require.Contains(t, err.Error(), "must be > min_tokens")
}
