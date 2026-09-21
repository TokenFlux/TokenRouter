package service

import (
	"math"
	"testing"

	"github.com/TokenFlux/TokenRouter/internal/routing"
	"github.com/TokenFlux/TokenRouter/internal/routing/accessview"
	"github.com/TokenFlux/TokenRouter/internal/scheduler/policy"
	"github.com/stretchr/testify/require"
)

func groupAdvancedSchedulerOverrideTestPointer[T any](value T) *T {
	return &value
}

func groupAdvancedSchedulerOverrideTestWeights() GatewayAdvancedSchedulerScoreWeightsView {
	return GatewayAdvancedSchedulerScoreWeightsView{
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
	settings := resolveAdvancedSchedulerEffectiveSettings(
		7,
		groupAdvancedSchedulerOverrideTestWeights(),
		advancedSchedulerRuntimeSettings{
			stickyWeightedEnabled:       true,
			subscriptionPriorityEnabled: false,
			lbTopKOverride:              11,
			ewmaErrorRateAlpha:          0.4,
			ewmaTTFTAlpha:               0.3,
			stickyEscape:                advancedStickyEscapeConfig{enabled: true, ttftMs: 12000, errorRate: 0.6},
			weightOverrides: map[string]float64{
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
		routing.GroupAdvancedSchedulerOverrides{
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

	require.False(t, settings.stickyWeightedEnabled)
	require.True(t, settings.subscriptionPriorityEnabled)
	require.Equal(t, 3, settings.topK)
	require.Equal(t, 21.0, settings.weights.Priority)
	require.Equal(t, 13.0, settings.weights.Load)
	require.Equal(t, 0.0, settings.weights.Queue)
	require.Equal(t, 15.0, settings.weights.ErrorRate)
	require.Equal(t, 16.0, settings.weights.TTFT)
	require.Equal(t, 17.0, settings.weights.Reset)
	require.Equal(t, 18.0, settings.weights.QuotaHeadroom)
	require.Equal(t, 22.0, settings.weights.Previous)
	require.Equal(t, 20.0, settings.weights.SessionSticky)
	require.InDelta(t, 0.8, settings.feedback.errorRateAlpha, 0.000001)
	require.InDelta(t, 0.7, settings.feedback.ttftAlpha, 0.000001)
	require.False(t, settings.stickyEscape.enabled)
	require.InDelta(t, 9000, settings.stickyEscape.ttftMs, 0.000001)
	require.InDelta(t, 0.25, settings.stickyEscape.errorRate, 0.000001)
}

func TestResolveAdvancedSchedulerEffectiveSettingsKeepsExplicitZeroWeights(t *testing.T) {
	settings := resolveAdvancedSchedulerEffectiveSettings(
		7,
		GatewayAdvancedSchedulerScoreWeightsView{
			Priority: 1,
		},
		advancedSchedulerRuntimeSettings{},
		routing.GroupAdvancedSchedulerOverrides{
			WeightPriority: groupAdvancedSchedulerOverrideTestPointer(0.0),
		},
	)

	require.Zero(t, settings.weights.Priority)
	require.Zero(t, settings.weights.Load)
	require.Zero(t, settings.weights.Queue)
}

func TestResolveAdvancedSchedulerEffectiveSettingsRejectsOverflowingMergedWeightsAtRuntime(t *testing.T) {
	globalWeights := GatewayAdvancedSchedulerScoreWeightsView{
		Priority: 1,
		Previous: 2,
	}
	settings := resolveAdvancedSchedulerEffectiveSettings(
		7,
		globalWeights,
		advancedSchedulerRuntimeSettings{},
		routing.GroupAdvancedSchedulerOverrides{
			WeightPriority: groupAdvancedSchedulerOverrideTestPointer(math.MaxFloat64),
			WeightLoad:     groupAdvancedSchedulerOverrideTestPointer(math.MaxFloat64),
		},
	)

	require.Equal(t, globalWeights, settings.weights)
	require.False(t, math.IsNaN(settings.weights.configWeights().TotalWeightSum()))
	require.False(t, math.IsInf(settings.weights.configWeights().TotalWeightSum(), 0))
}

func TestValidateAdvancedSchedulerEffectiveWeightsAllowsZeroBaseAndRejectsOverflow(t *testing.T) {
	require.NoError(t, validateAdvancedSchedulerEffectiveWeights(GatewayAdvancedSchedulerScoreWeightsView{
		Previous:      5,
		SessionSticky: 3,
	}))
	require.Error(t, validateAdvancedSchedulerEffectiveWeights(GatewayAdvancedSchedulerScoreWeightsView{
		Priority: math.MaxFloat64,
		Load:     math.MaxFloat64,
	}))
}

func TestValidateGroupAdvancedSchedulerOverrides(t *testing.T) {
	tests := []struct {
		name      string
		overrides routing.GroupAdvancedSchedulerOverrides
		wantError bool
	}{
		{
			name: "sparse explicit zero is valid",
			overrides: routing.GroupAdvancedSchedulerOverrides{
				StickyWeightedEnabled: groupAdvancedSchedulerOverrideTestPointer(false),
				WeightQueue:           groupAdvancedSchedulerOverrideTestPointer(0.0),
			},
		},
		{
			name: "top k must be positive",
			overrides: routing.GroupAdvancedSchedulerOverrides{
				LBTopK: groupAdvancedSchedulerOverrideTestPointer(0),
			},
			wantError: true,
		},
		{
			name: "ewma alpha must be within range",
			overrides: routing.GroupAdvancedSchedulerOverrides{
				EWMAErrorRateAlpha: groupAdvancedSchedulerOverrideTestPointer(0.0),
			},
			wantError: true,
		},
		{
			name: "sticky escape thresholds are validated",
			overrides: routing.GroupAdvancedSchedulerOverrides{
				StickyEscapeTTFTMs:    groupAdvancedSchedulerOverrideTestPointer(0),
				StickyEscapeErrorRate: groupAdvancedSchedulerOverrideTestPointer(1.1),
			},
			wantError: true,
		},
		{
			name: "negative weight is rejected",
			overrides: routing.GroupAdvancedSchedulerOverrides{
				WeightLoad: groupAdvancedSchedulerOverrideTestPointer(-0.1),
			},
			wantError: true,
		},
		{
			name: "non finite weight is rejected",
			overrides: routing.GroupAdvancedSchedulerOverrides{
				WeightTTFT: groupAdvancedSchedulerOverrideTestPointer(math.Inf(1)),
			},
			wantError: true,
		},
		{
			name: "all explicit base weights can be zero",
			overrides: routing.GroupAdvancedSchedulerOverrides{
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
			err := policy.ValidateGroupOverrides(tt.overrides)
			if tt.wantError {
				require.Error(t, err)
				return
			}
			require.NoError(t, err)
		})
	}
}

func TestCloneGroupAdvancedSchedulerOverridesDeepCopiesPointers(t *testing.T) {
	overrides := routing.GroupAdvancedSchedulerOverrides{
		StickyWeightedEnabled:       groupAdvancedSchedulerOverrideTestPointer(false),
		LBTopK:                      groupAdvancedSchedulerOverrideTestPointer(3),
		WeightPreviousResponse:      groupAdvancedSchedulerOverrideTestPointer(5.0),
		SubscriptionPriorityEnabled: groupAdvancedSchedulerOverrideTestPointer(true),
	}

	cloned := accessview.CloneGroupAdvancedSchedulerOverrides(overrides)
	require.NotSame(t, overrides.StickyWeightedEnabled, cloned.StickyWeightedEnabled)
	require.NotSame(t, overrides.LBTopK, cloned.LBTopK)
	require.NotSame(t, overrides.WeightPreviousResponse, cloned.WeightPreviousResponse)
	*cloned.LBTopK = 99
	*cloned.WeightPreviousResponse = 88

	require.Equal(t, 3, *overrides.LBTopK)
	require.Equal(t, 5.0, *overrides.WeightPreviousResponse)
}
