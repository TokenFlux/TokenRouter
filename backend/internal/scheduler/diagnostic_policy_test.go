package scheduler

import (
	"testing"

	"github.com/TokenFlux/TokenRouter/internal/routing/capability"
	"github.com/TokenFlux/TokenRouter/internal/scheduler/policy"
	"github.com/stretchr/testify/require"
)

func TestDiagnosticPolicySignalsOnlyReturnsContextualOrEnabledStrategies(t *testing.T) {
	group := &DiagnosticGroup{ID: 601, Platform: capability.PlatformOpenAI, Advanced: true}
	effective := policy.EffectiveSettings{}

	// 基准诊断明确标出请求未提供、因而无法评估的能力门禁。
	baselineSignals := diagnosticPolicySignals(group, AdvancedSchedulerScoreDiagnosticRequest{}, effective, diagnosticPolicyOutcome{})
	require.Len(t, baselineSignals, 1)
	require.Equal(t, "request_capabilities", baselineSignals[0].Key)
	require.Equal(t, "not_evaluated", baselineSignals[0].State)

	effective.StickyWeightedEnabled = true
	effective.SubscriptionPriorityEnabled = true
	signals := diagnosticPolicySignals(group, AdvancedSchedulerScoreDiagnosticRequest{StickyAccountID: 99}, effective, diagnosticPolicyOutcome{
		sessionStickyState:     "weighted",
		subscriptionPoolActive: true,
	})
	require.Len(t, signals, 3)
	require.Equal(t, "session_sticky", signals[0].Key)
	require.Equal(t, "weighted", signals[0].State)
	require.Equal(t, "subscription_priority", signals[1].Key)
	require.Equal(t, "active_pool", signals[1].State)
}
func TestDiagnosticWeightedPreviousResponseIsIgnoredOutsideOpenAI(t *testing.T) {
	group := &DiagnosticGroup{ID: 602, Platform: capability.PlatformGemini, Advanced: true}
	outcome := diagnosticHardStickyPolicyOutcome(
		[]*DiagnosticAccount{{ID: 99, Platform: capability.PlatformGemini}},
		group,
		AdvancedSchedulerScoreDiagnosticRequest{PreviousResponseAccountID: 99, StickyAccountID: 99},
		policy.EffectiveSettings{StickyWeightedEnabled: true},
		nil,
		policy.StickyEscapeConfig{},
	)

	require.Zero(t, outcome.forcedAccountID)
	require.Equal(t, "ignored", outcome.previousResponseState)
	require.Equal(t, "weighted", outcome.sessionStickyState)
}
