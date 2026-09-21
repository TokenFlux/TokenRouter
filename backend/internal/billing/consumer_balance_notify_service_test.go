//go:build unit

package billing

import (
	"testing"

	"github.com/stretchr/testify/require"
)

// ---------- resolveBalanceThreshold ----------

func TestResolveBalanceThreshold_Fixed(t *testing.T) {
	// Fixed type always returns the raw threshold regardless of totalRecharged.
	require.Equal(t, 10.0, BalanceThreshold(10, thresholdTypeFixed, 1000))
	require.Equal(t, 10.0, BalanceThreshold(10, thresholdTypeFixed, 0))
	require.Equal(t, 0.0, BalanceThreshold(0, thresholdTypeFixed, 1000))
}

func TestResolveBalanceThreshold_Percentage(t *testing.T) {
	// 10% of 1000 = 100
	require.Equal(t, 100.0, BalanceThreshold(10, thresholdTypePercentage, 1000))
	// 50% of 200 = 100
	require.Equal(t, 100.0, BalanceThreshold(50, thresholdTypePercentage, 200))
}

func TestResolveBalanceThreshold_PercentageZeroRecharged(t *testing.T) {
	// When totalRecharged is 0, percentage falls through to raw threshold
	// (treated as fixed). This is the defensive behavior.
	require.Equal(t, 10.0, BalanceThreshold(10, thresholdTypePercentage, 0))
}

func TestResolveBalanceThreshold_EmptyType(t *testing.T) {
	// Empty type is treated as fixed (not percentage).
	require.Equal(t, 10.0, BalanceThreshold(10, "", 1000))
}

// ---------- quotaDim.resolvedThreshold ----------

func TestResolvedThreshold_FixedNormal(t *testing.T) {
	// threshold=400 remaining, limit=1000 → usage trigger at 600
	d := QuotaNotifyDimension{Threshold: 400, ThresholdType: thresholdTypeFixed, Limit: 1000}
	require.Equal(t, 600.0, d.UsageThreshold())
}

func TestResolvedThreshold_FixedThresholdExceedsLimit(t *testing.T) {
	// threshold=1200, limit=1000 → returns negative, callers must skip
	d := QuotaNotifyDimension{Threshold: 1200, ThresholdType: thresholdTypeFixed, Limit: 1000}
	require.Equal(t, -200.0, d.UsageThreshold())
}

func TestResolvedThreshold_FixedThresholdEqualsLimit(t *testing.T) {
	// threshold=1000, limit=1000 → returns 0 (alert fires at 0 usage)
	d := QuotaNotifyDimension{Threshold: 1000, ThresholdType: thresholdTypeFixed, Limit: 1000}
	require.Equal(t, 0.0, d.UsageThreshold())
}

func TestResolvedThreshold_PercentageNormal(t *testing.T) {
	// threshold=30%, limit=1000 → usage trigger at 700 (remaining drops to 30%)
	d := QuotaNotifyDimension{Threshold: 30, ThresholdType: thresholdTypePercentage, Limit: 1000}
	require.InDelta(t, 700.0, d.UsageThreshold(), 0.001)
}

func TestResolvedThreshold_PercentageZeroPercent(t *testing.T) {
	// threshold=0%, limit=1000 → fires when remaining drops to 0 (usage=1000)
	d := QuotaNotifyDimension{Threshold: 0, ThresholdType: thresholdTypePercentage, Limit: 1000}
	require.InDelta(t, 1000.0, d.UsageThreshold(), 0.001)
}

func TestResolvedThreshold_PercentageHundredPercent(t *testing.T) {
	// threshold=100%, limit=1000 → fires immediately (remaining drops to 100% i.e. nothing used yet)
	d := QuotaNotifyDimension{Threshold: 100, ThresholdType: thresholdTypePercentage, Limit: 1000}
	require.InDelta(t, 0.0, d.UsageThreshold(), 0.001)
}

func TestResolvedThreshold_PercentageOverHundred(t *testing.T) {
	// threshold=150%, limit=1000 → returns negative (never triggers; callers skip)
	d := QuotaNotifyDimension{Threshold: 150, ThresholdType: thresholdTypePercentage, Limit: 1000}
	require.Less(t, d.UsageThreshold(), 0.0)
}

func TestResolvedThreshold_ZeroLimit(t *testing.T) {
	// limit=0 → returns 0 to avoid division and false alerts on unlimited quotas
	d := QuotaNotifyDimension{Threshold: 100, ThresholdType: thresholdTypeFixed, Limit: 0}
	require.Equal(t, 0.0, d.UsageThreshold())
}

func TestResolvedThreshold_NegativeLimit(t *testing.T) {
	// Negative limit treated as 0
	d := QuotaNotifyDimension{Threshold: 100, ThresholdType: thresholdTypeFixed, Limit: -10}
	require.Equal(t, 0.0, d.UsageThreshold())
}

// ---------- sanitizeEmailHeader ----------

// ---------- buildQuotaDims ----------

// ---------- buildQuotaDimsFromState ----------

func TestBuildQuotaDimsFromState_UsesStateValues(t *testing.T) {
	// Usage values should come from the state, not the account.
	a := &QuotaNotifyAccount{
		Dimensions: []QuotaNotifyDimension{
			{Name: "daily", Enabled: true, Threshold: 100, ThresholdType: "fixed", CurrentUsed: 999, Limit: 999},
			{Name: "weekly"},
			{Name: "total"},
		},
	}
	state := &AccountQuotaState{
		DailyUsed:   77.0,
		DailyLimit:  500.0,
		WeeklyUsed:  88.0,
		WeeklyLimit: 2000.0,
		TotalUsed:   99.0,
		TotalLimit:  10000.0,
	}
	dims := quotaDimsFromCommitted(a, state)
	require.Len(t, dims, 3)
	// Settings from account (enabled, threshold, thresholdType)
	require.True(t, dims[0].Enabled)
	require.Equal(t, 100.0, dims[0].Threshold)
	// Usage from state
	require.Equal(t, 77.0, dims[0].CurrentUsed)
	require.Equal(t, 500.0, dims[0].Limit)
	require.Equal(t, 88.0, dims[1].CurrentUsed)
	require.Equal(t, 2000.0, dims[1].Limit)
	require.Equal(t, 99.0, dims[2].CurrentUsed)
	require.Equal(t, 10000.0, dims[2].Limit)
}

// ---------- collectBalanceNotifyRecipients ----------

func TestCollectBalanceNotifyRecipients_Empty(t *testing.T) {
	s := &BalanceNotifyService{}
	u := &UserSummary{BalanceNotifyExtraEmails: nil}
	require.Empty(t, s.CollectBalanceNotifyRecipients(u))
}

func TestCollectBalanceNotifyRecipients_FiltersDisabledAndUnverified(t *testing.T) {
	s := &BalanceNotifyService{}
	u := &UserSummary{
		BalanceNotifyExtraEmails: []NotifyEmailSummary{
			{Email: "a@example.com", Verified: true, Disabled: false},
			{Email: "b@example.com", Verified: true, Disabled: true},   // disabled
			{Email: "c@example.com", Verified: false, Disabled: false}, // unverified
			{Email: "d@example.com", Verified: true, Disabled: false},
		},
	}
	got := s.CollectBalanceNotifyRecipients(u)
	require.Equal(t, []string{"a@example.com", "d@example.com"}, got)
}

func TestCollectBalanceNotifyRecipients_DeduplicatesCaseInsensitive(t *testing.T) {
	s := &BalanceNotifyService{}
	u := &UserSummary{
		BalanceNotifyExtraEmails: []NotifyEmailSummary{
			{Email: "User@Example.com", Verified: true},
			{Email: "user@example.com", Verified: true},
			{Email: "USER@EXAMPLE.COM", Verified: true},
		},
	}
	got := s.CollectBalanceNotifyRecipients(u)
	require.Len(t, got, 1)
	// The original casing of the first entry is preserved.
	require.Equal(t, "User@Example.com", got[0])
}

func TestCollectBalanceNotifyRecipients_SkipsEmpty(t *testing.T) {
	s := &BalanceNotifyService{}
	u := &UserSummary{
		BalanceNotifyExtraEmails: []NotifyEmailSummary{
			{Email: "  ", Verified: true},
			{Email: "", Verified: true},
			{Email: "valid@example.com", Verified: true},
		},
	}
	got := s.CollectBalanceNotifyRecipients(u)
	require.Equal(t, []string{"valid@example.com"}, got)
}

func TestCollectBalanceNotifyRecipients_TrimsWhitespace(t *testing.T) {
	s := &BalanceNotifyService{}
	u := &UserSummary{
		BalanceNotifyExtraEmails: []NotifyEmailSummary{
			{Email: "  trimmed@example.com  ", Verified: true},
		},
	}
	got := s.CollectBalanceNotifyRecipients(u)
	require.Equal(t, []string{"trimmed@example.com"}, got)
}
