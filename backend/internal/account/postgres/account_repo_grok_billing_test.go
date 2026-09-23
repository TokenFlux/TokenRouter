package postgres

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestGrokBillingSnapshotIsSchedulerNeutral(t *testing.T) {
	t.Parallel()

	require.True(t, IsSchedulerNeutralExtraKey("grok_billing_snapshot"))
	require.False(t, ShouldEnqueueSchedulerOutboxForExtraUpdates(map[string]any{
		"grok_billing_snapshot": map[string]any{"usage_percent": 50},
	}))
}

func TestOpenAIResetCreditSnapshotIsSchedulerNeutral(t *testing.T) {
	t.Parallel()

	require.True(t, IsSchedulerNeutralExtraKey("codex_reset_credit_snapshot"))
	require.False(t, ShouldEnqueueSchedulerOutboxForExtraUpdates(map[string]any{
		"codex_reset_credit_snapshot": map[string]any{"available_count": 1},
	}))
}

func TestCNUsageMonitorSnapshotIsSchedulerNeutralForGenericUpdates(t *testing.T) {
	t.Parallel()
	require.True(t, IsSchedulerNeutralExtraKey("cn_usage_monitor_snapshot"))
	require.False(t, ShouldEnqueueSchedulerOutboxForExtraUpdates(map[string]any{
		"cn_usage_monitor_snapshot": map[string]any{"version": 1},
	}))
}
