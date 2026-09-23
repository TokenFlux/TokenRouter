package policy

import (
	"math"
	"testing"

	"github.com/stretchr/testify/require"
)

func groupAdvancedSchedulerOverrideTestPointer[T any](value T) *T {
	return &value
}

func groupAdvancedSchedulerOverrideTestWeights() ScoreWeights {
	return ScoreWeights{
		Priority:      1,
		Load:          2,
		Queue:         3,
		ErrorRate:     4,
		TTFT:          5,
		Reset:         6,
		QuotaHeadroom: 7,
		Previous:      8,
		SessionSticky: 9,
	}
}

func TestResolveAdvancedSchedulerEffectiveSettingsPrefersGroupOverrides(t *testing.T) {
	settings := ResolveEffective(
		7,
		groupAdvancedSchedulerOverrideTestWeights(),
		RuntimeSettings{
			StickyWeightedEnabled:       true,
			SubscriptionPriorityEnabled: false,
			LbTopKOverride:              11,
			EwmaErrorRateAlpha:          0.4,
			EwmaTTFTAlpha:               0.3,
			StickyEscape:                StickyEscapeConfig{Enabled: true, TtftMs: 12000, ErrorRate: 0.6},
			WeightOverrides: map[string]float64{
				"priority":          12,
				"load":              13,
				"queue":             14,
				"error_rate":        15,
				"ttft":              16,
				"reset":             17,
				"quota_headroom":    18,
				"previous_response": 19,
				"session_sticky":    20,
			},
		},
		GroupAdvancedSchedulerOverrides{
			StickyWeightedEnabled:       groupAdvancedSchedulerOverrideTestPointer(false),
			SubscriptionPriorityEnabled: groupAdvancedSchedulerOverrideTestPointer(true),
			EWMAErrorRateAlpha:          groupAdvancedSchedulerOverrideTestPointer(0.8),
			EWMATTFTAlpha:               groupAdvancedSchedulerOverrideTestPointer(0.7),
			StickyEscapeEnabled:         groupAdvancedSchedulerOverrideTestPointer(false),
			StickyEscapeTTFTMs:          groupAdvancedSchedulerOverrideTestPointer(9000),
			StickyEscapeErrorRate:       groupAdvancedSchedulerOverrideTestPointer(0.25),
			LBTopK:                      groupAdvancedSchedulerOverrideTestPointer(3),
			WeightPriority:              groupAdvancedSchedulerOverrideTestPointer(21.0),
			WeightQueue:                 groupAdvancedSchedulerOverrideTestPointer(0.0),
			WeightPreviousResponse:      groupAdvancedSchedulerOverrideTestPointer(22.0),
		},
	)

	require.False(t, settings.StickyWeightedEnabled)
	require.True(t, settings.SubscriptionPriorityEnabled)
	require.Equal(t, 3, settings.TopK)
	require.Equal(t, 21.0, settings.Weights.Priority)
	require.Equal(t, 13.0, settings.Weights.Load)
	require.Equal(t, 0.0, settings.Weights.Queue)
	require.Equal(t, 15.0, settings.Weights.ErrorRate)
	require.Equal(t, 16.0, settings.Weights.TTFT)
	require.Equal(t, 17.0, settings.Weights.Reset)
	require.Equal(t, 18.0, settings.Weights.QuotaHeadroom)
	require.Equal(t, 22.0, settings.Weights.Previous)
	require.Equal(t, 20.0, settings.Weights.SessionSticky)
	require.InDelta(t, 0.8, settings.Feedback.ErrorRateAlpha, 0.000001)
	require.InDelta(t, 0.7, settings.Feedback.TtftAlpha, 0.000001)
	require.False(t, settings.StickyEscape.Enabled)
	require.InDelta(t, 9000, settings.StickyEscape.TtftMs, 0.000001)
	require.InDelta(t, 0.25, settings.StickyEscape.ErrorRate, 0.000001)
}

func TestResolveAdvancedSchedulerEffectiveSettingsKeepsExplicitZeroWeights(t *testing.T) {
	settings := ResolveEffective(
		7,
		ScoreWeights{
			Priority: 1,
		},
		RuntimeSettings{},
		GroupAdvancedSchedulerOverrides{
			WeightPriority: groupAdvancedSchedulerOverrideTestPointer(0.0),
		},
	)

	require.Zero(t, settings.Weights.Priority)
	require.Zero(t, settings.Weights.Load)
	require.Zero(t, settings.Weights.Queue)
}

func TestResolveAdvancedSchedulerEffectiveSettingsRejectsOverflowingMergedWeightsAtRuntime(t *testing.T) {
	globalWeights := ScoreWeights{
		Priority: 1,
		Previous: 2,
	}
	settings := ResolveEffective(
		7,
		globalWeights,
		RuntimeSettings{},
		GroupAdvancedSchedulerOverrides{
			WeightPriority: groupAdvancedSchedulerOverrideTestPointer(math.MaxFloat64),
			WeightLoad:     groupAdvancedSchedulerOverrideTestPointer(math.MaxFloat64),
		},
	)

	require.Equal(t, globalWeights, settings.Weights)
	require.False(t, math.IsNaN(settings.Weights.TotalWeightSum()))
	require.False(t, math.IsInf(settings.Weights.TotalWeightSum(), 0))
}

func TestValidateAdvancedSchedulerEffectiveWeightsAllowsZeroBaseAndRejectsOverflow(t *testing.T) {
	require.NoError(t, ValidateEffectiveWeights(ScoreWeights{
		Previous:      5,
		SessionSticky: 3,
	}))
	require.Error(t, ValidateEffectiveWeights(ScoreWeights{
		Priority: math.MaxFloat64,
		Load:     math.MaxFloat64,
	}))
}

func TestValidateGroupAdvancedSchedulerOverrides(t *testing.T) {
	tests := []struct {
		name      string
		overrides GroupAdvancedSchedulerOverrides
		wantError bool
	}{
		{
			name: "sparse explicit zero is valid",
			overrides: GroupAdvancedSchedulerOverrides{
				StickyWeightedEnabled: groupAdvancedSchedulerOverrideTestPointer(false),
				WeightQueue:           groupAdvancedSchedulerOverrideTestPointer(0.0),
			},
		},
		{
			name: "top k must be positive",
			overrides: GroupAdvancedSchedulerOverrides{
				LBTopK: groupAdvancedSchedulerOverrideTestPointer(0),
			},
			wantError: true,
		},
		{
			name: "ewma alpha must be within range",
			overrides: GroupAdvancedSchedulerOverrides{
				EWMAErrorRateAlpha: groupAdvancedSchedulerOverrideTestPointer(0.0),
			},
			wantError: true,
		},
		{
			name: "sticky escape thresholds are validated",
			overrides: GroupAdvancedSchedulerOverrides{
				StickyEscapeTTFTMs:    groupAdvancedSchedulerOverrideTestPointer(0),
				StickyEscapeErrorRate: groupAdvancedSchedulerOverrideTestPointer(1.1),
			},
			wantError: true,
		},
		{
			name: "negative weight is rejected",
			overrides: GroupAdvancedSchedulerOverrides{
				WeightLoad: groupAdvancedSchedulerOverrideTestPointer(-0.1),
			},
			wantError: true,
		},
		{
			name: "non finite weight is rejected",
			overrides: GroupAdvancedSchedulerOverrides{
				WeightTTFT: groupAdvancedSchedulerOverrideTestPointer(math.Inf(1)),
			},
			wantError: true,
		},
		{
			name: "all explicit base weights can be zero",
			overrides: GroupAdvancedSchedulerOverrides{
				WeightPriority:      groupAdvancedSchedulerOverrideTestPointer(0.0),
				WeightLoad:          groupAdvancedSchedulerOverrideTestPointer(0.0),
				WeightQueue:         groupAdvancedSchedulerOverrideTestPointer(0.0),
				WeightErrorRate:     groupAdvancedSchedulerOverrideTestPointer(0.0),
				WeightTTFT:          groupAdvancedSchedulerOverrideTestPointer(0.0),
				WeightReset:         groupAdvancedSchedulerOverrideTestPointer(0.0),
				WeightQuotaHeadroom: groupAdvancedSchedulerOverrideTestPointer(0.0),
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := ValidateGroupOverrides(tt.overrides)
			if tt.wantError {
				require.Error(t, err)
				return
			}
			require.NoError(t, err)
		})
	}
}

func TestCloneGroupAdvancedSchedulerOverridesDeepCopiesPointers(t *testing.T) {
	overrides := GroupAdvancedSchedulerOverrides{
		StickyWeightedEnabled:       groupAdvancedSchedulerOverrideTestPointer(false),
		LBTopK:                      groupAdvancedSchedulerOverrideTestPointer(3),
		WeightPreviousResponse:      groupAdvancedSchedulerOverrideTestPointer(5.0),
		SubscriptionPriorityEnabled: groupAdvancedSchedulerOverrideTestPointer(true),
	}

	cloned := CloneGroupAdvancedSchedulerOverrides(overrides)
	require.NotSame(t, overrides.StickyWeightedEnabled, cloned.StickyWeightedEnabled)
	require.NotSame(t, overrides.LBTopK, cloned.LBTopK)
	require.NotSame(t, overrides.WeightPreviousResponse, cloned.WeightPreviousResponse)
	*cloned.LBTopK = 99
	*cloned.WeightPreviousResponse = 88

	require.Equal(t, 3, *overrides.LBTopK)
	require.Equal(t, 5.0, *overrides.WeightPreviousResponse)
}
