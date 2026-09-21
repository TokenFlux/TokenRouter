//go:build unit

package billing

import (
	"context"
	"testing"

	"github.com/TokenFlux/TokenRouter/internal/routing/capability"
	"github.com/stretchr/testify/require"
)

// newBalanceNotifyServiceForTest constructs a BalanceNotifyService with an
// in-memory settings repo and a non-nil emailService so that the guard-clause
// nil-checks pass. The emailService is intentionally minimal — tests must
// avoid crossing scenarios that would actually dispatch emails.

// EmailService is a concrete type; construct with the same repo so that
// any accidental fallback reads still succeed. Tests should not trigger a
// crossing that reaches SendEmail.

// ---------- guard clauses ----------

func TestCheckBalanceAfterDeduction_NilUser(t *testing.T) {
	s, _ := newBalanceNotifyServiceForTest()
	// Should not panic.
	s.CheckBalanceAfterDeduction(context.Background(), nil, 100, 50)
}

func TestCheckBalanceAfterDeduction_UserNotifyDisabled(t *testing.T) {
	s, repo := newBalanceNotifyServiceForTest()
	repo.data[SettingKeyBalanceLowNotifyEnabled] = "true"
	repo.data[SettingKeyBalanceLowNotifyThreshold] = "10"
	u := &UserSummary{ID: 1, BalanceNotifyEnabled: false}
	// Even with a crossing, disabled flag short-circuits.
	s.CheckBalanceAfterDeduction(context.Background(), u, 20, 15)
}

func TestCheckBalanceAfterDeduction_GlobalDisabled(t *testing.T) {
	s, repo := newBalanceNotifyServiceForTest()
	repo.data[SettingKeyBalanceLowNotifyEnabled] = "false"
	u := &UserSummary{ID: 1, BalanceNotifyEnabled: true}
	s.CheckBalanceAfterDeduction(context.Background(), u, 20, 15)
}

func TestCheckBalanceAfterDeduction_ThresholdZero(t *testing.T) {
	s, repo := newBalanceNotifyServiceForTest()
	repo.data[SettingKeyBalanceLowNotifyEnabled] = "true"
	repo.data[SettingKeyBalanceLowNotifyThreshold] = "0"
	u := &UserSummary{ID: 1, BalanceNotifyEnabled: true}
	s.CheckBalanceAfterDeduction(context.Background(), u, 20, 15)
}

func TestCheckBalanceAfterDeduction_UserThresholdOverride(t *testing.T) {
	s, repo := newBalanceNotifyServiceForTest()
	repo.data[SettingKeyBalanceLowNotifyEnabled] = "true"
	repo.data[SettingKeyBalanceLowNotifyThreshold] = "100" // global default
	customThreshold := 5.0
	u := &UserSummary{
		ID:                     1,
		BalanceNotifyEnabled:   true,
		BalanceNotifyThreshold: &customThreshold,
	}
	// User's 5.0 threshold takes precedence over global 100. 20 -> 15 does not
	// cross 5, so nothing fires (verified by absence of panic).
	s.CheckBalanceAfterDeduction(context.Background(), u, 20, 15)
}

func TestCheckBalanceAfterDeduction_NoCrossingNotFired(t *testing.T) {
	s, repo := newBalanceNotifyServiceForTest()
	repo.data[SettingKeyBalanceLowNotifyEnabled] = "true"
	repo.data[SettingKeyBalanceLowNotifyThreshold] = "10"
	u := &UserSummary{ID: 1, BalanceNotifyEnabled: true}

	// 100 -> 95, both remain above threshold=10, no crossing.
	s.CheckBalanceAfterDeduction(context.Background(), u, 100, 5)
	// 5 -> 3, both already below threshold, no crossing (only fires on first
	// cross from above-to-below).
	s.CheckBalanceAfterDeduction(context.Background(), u, 5, 2)
}

// ---------- nil-service guards on CheckAccountQuotaAfterIncrement ----------

func TestCheckAccountQuotaAfterIncrement_NilAccount(t *testing.T) {
	s, _ := newBalanceNotifyServiceForTest()
	// Should not panic.
	s.CheckAccountQuotaAfterIncrement(context.Background(), nil, 10, nil)
}

func TestCheckAccountQuotaAfterIncrement_ZeroCost(t *testing.T) {
	s, _ := newBalanceNotifyServiceForTest()
	a := &QuotaNotifyAccount{ID: 1, Platform: capability.PlatformAnthropic}
	s.CheckAccountQuotaAfterIncrement(context.Background(), a, 0, nil)
}

func TestCheckAccountQuotaAfterIncrement_NegativeCost(t *testing.T) {
	s, _ := newBalanceNotifyServiceForTest()
	a := &QuotaNotifyAccount{ID: 1, Platform: capability.PlatformAnthropic}
	s.CheckAccountQuotaAfterIncrement(context.Background(), a, -5, nil)
}

func TestCheckAccountQuotaAfterIncrement_GlobalDisabled(t *testing.T) {
	s, repo := newBalanceNotifyServiceForTest()
	repo.data[SettingKeyAccountQuotaNotifyEnabled] = "false"
	a := &QuotaNotifyAccount{
		ID:       1,
		Platform: capability.PlatformAnthropic,
		Dimensions: []QuotaNotifyDimension{
			{Name: "daily", Enabled: true, Threshold: 100, ThresholdType: "fixed", Limit: 1000, CurrentUsed: 950},
		},
	}
	// Global disabled → no processing even if a dim would cross.
	s.CheckAccountQuotaAfterIncrement(context.Background(), a, 100, nil)
}

// ---------- sanity: internal helpers still work ----------

func TestGetBalanceNotifyConfig_AllFields(t *testing.T) {
	s, repo := newBalanceNotifyServiceForTest()
	repo.data[SettingKeyBalanceLowNotifyEnabled] = "true"
	repo.data[SettingKeyBalanceLowNotifyThreshold] = "12.5"
	repo.data[SettingKeyBalanceLowNotifyRechargeURL] = "https://example.com/pay"

	enabled, threshold, url := s.GetBalanceNotifyConfig(context.Background())
	require.True(t, enabled)
	require.Equal(t, 12.5, threshold)
	require.Equal(t, "https://example.com/pay", url)
}

func TestGetBalanceNotifyConfig_Disabled(t *testing.T) {
	s, repo := newBalanceNotifyServiceForTest()
	repo.data[SettingKeyBalanceLowNotifyEnabled] = "false"

	enabled, _, _ := s.GetBalanceNotifyConfig(context.Background())
	require.False(t, enabled)
}

func TestGetBalanceNotifyConfig_InvalidThreshold(t *testing.T) {
	s, repo := newBalanceNotifyServiceForTest()
	repo.data[SettingKeyBalanceLowNotifyEnabled] = "true"
	repo.data[SettingKeyBalanceLowNotifyThreshold] = "not-a-number"

	enabled, threshold, _ := s.GetBalanceNotifyConfig(context.Background())
	require.True(t, enabled)
	require.Equal(t, 0.0, threshold)
}

func TestIsAccountQuotaNotifyEnabled(t *testing.T) {
	s, repo := newBalanceNotifyServiceForTest()

	// Missing key → false
	require.False(t, s.IsAccountQuotaNotifyEnabled(context.Background()))

	// Explicit "false"
	repo.data[SettingKeyAccountQuotaNotifyEnabled] = "false"
	require.False(t, s.IsAccountQuotaNotifyEnabled(context.Background()))

	// Explicit "true"
	repo.data[SettingKeyAccountQuotaNotifyEnabled] = "true"
	require.True(t, s.IsAccountQuotaNotifyEnabled(context.Background()))
}

func TestGetSiteName_FallsBackToDefault(t *testing.T) {
	s, _ := newBalanceNotifyServiceForTest()
	name := s.GetSiteName(context.Background())
	require.Equal(t, defaultSiteName, name)
}

func TestGetSiteName_Configured(t *testing.T) {
	s, repo := newBalanceNotifyServiceForTest()
	repo.data[SettingKeySiteName] = "My Site"
	require.Equal(t, "My Site", s.GetSiteName(context.Background()))
}

// ---------- crossedDownward ----------

func TestCrossedDownward_CrossesBelow(t *testing.T) {
	// oldBalance > threshold, newBalance < threshold → true
	require.True(t, CrossedDownward(100, 5, 10))
}

func TestCrossedDownward_ExactlyAtThreshold(t *testing.T) {
	// oldBalance > threshold, newBalance == threshold → false (not below)
	require.False(t, CrossedDownward(100, 10, 10))
}

func TestCrossedDownward_OldExactlyAtThreshold_NewBelow(t *testing.T) {
	// oldBalance == threshold, newBalance < threshold → true
	// (at-or-above → below counts as a crossing)
	require.True(t, CrossedDownward(10, 5, 10))
}

func TestCrossedDownward_AlreadyBelow(t *testing.T) {
	// oldBalance < threshold → false (already below, no new crossing)
	require.False(t, CrossedDownward(5, 3, 10))
}

func TestCrossedDownward_BothAbove(t *testing.T) {
	// oldBalance > threshold, newBalance > threshold → false (no crossing)
	require.False(t, CrossedDownward(100, 50, 10))
}

func TestCrossedDownward_ZeroThreshold(t *testing.T) {
	// threshold == 0 → oldV >= 0 is always true, but newV < 0 only for negatives
	// Typical case: positive balances should not fire when threshold is 0.
	require.False(t, CrossedDownward(10, 5, 0))
	require.False(t, CrossedDownward(0, 0, 0))
}

func TestCrossedDownward_ZeroThreshold_NegativeNew(t *testing.T) {
	// Edge case: newBalance goes negative with threshold=0.
	require.True(t, CrossedDownward(5, -1, 0))
}

func TestCrossedDownward_NegativeValues(t *testing.T) {
	// Both already negative, threshold is positive → no crossing (already below).
	require.False(t, CrossedDownward(-5, -10, 10))
}

func TestCrossedDownward_LargeDecrement(t *testing.T) {
	// A single large deduction crosses the threshold.
	require.True(t, CrossedDownward(1000, 0.5, 100))
}

func TestCrossedDownward_SmallDecrement_NoCrossing(t *testing.T) {
	// A tiny deduction stays above threshold.
	require.False(t, CrossedDownward(100, 99.99, 10))
}

// ---------- checkQuotaDimCrossings ----------

func TestCheckQuotaDimCrossings_NoDimensions(t *testing.T) {
	s, _ := newBalanceNotifyServiceForTest()
	account := &QuotaNotifyAccount{ID: 1, Name: "test", Platform: capability.PlatformAnthropic}
	// Empty dims → no crossing, no panic.
	s.CheckQuotaDimCrossings(account, nil, 10, []string{"admin@example.com"}, "TestSite")
	s.CheckQuotaDimCrossings(account, []QuotaNotifyDimension{}, 10, []string{"admin@example.com"}, "TestSite")
}

func TestCheckQuotaDimCrossings_DisabledDimension(t *testing.T) {
	s, _ := newBalanceNotifyServiceForTest()
	account := &QuotaNotifyAccount{ID: 1, Name: "test", Platform: capability.PlatformAnthropic}
	dims := []QuotaNotifyDimension{
		{
			Name:          quotaDimDaily,
			Enabled:       false, // disabled
			Threshold:     100,
			ThresholdType: thresholdTypeFixed,
			CurrentUsed:   950,
			Limit:         1000,
		},
	}
	// Disabled dimension should be skipped even if crossing would occur.
	s.CheckQuotaDimCrossings(account, dims, 50, []string{"admin@example.com"}, "TestSite")
}

func TestCheckQuotaDimCrossings_ZeroThresholdSkipped(t *testing.T) {
	s, _ := newBalanceNotifyServiceForTest()
	account := &QuotaNotifyAccount{ID: 1, Name: "test", Platform: capability.PlatformAnthropic}
	dims := []QuotaNotifyDimension{
		{
			Name:          quotaDimDaily,
			Enabled:       true,
			Threshold:     0, // zero threshold
			ThresholdType: thresholdTypeFixed,
			CurrentUsed:   950,
			Limit:         1000,
		},
	}
	// Zero threshold → skipped.
	s.CheckQuotaDimCrossings(account, dims, 50, []string{"admin@example.com"}, "TestSite")
}

func TestCheckQuotaDimCrossings_NoCrossing_BothBelowThreshold(t *testing.T) {
	s, _ := newBalanceNotifyServiceForTest()
	account := &QuotaNotifyAccount{ID: 1, Name: "test", Platform: capability.PlatformAnthropic}
	// threshold=400 remaining, limit=1000 → effectiveThreshold = 600 (usage trigger)
	// currentUsed=300 (after), oldUsed=300-50=250 (before). Both < 600, no crossing.
	dims := []QuotaNotifyDimension{
		{
			Name:          quotaDimDaily,
			Enabled:       true,
			Threshold:     400,
			ThresholdType: thresholdTypeFixed,
			CurrentUsed:   300,
			Limit:         1000,
		},
	}
	s.CheckQuotaDimCrossings(account, dims, 50, []string{"admin@example.com"}, "TestSite")
}

func TestCheckQuotaDimCrossings_NoCrossing_BothAboveThreshold(t *testing.T) {
	s, _ := newBalanceNotifyServiceForTest()
	account := &QuotaNotifyAccount{ID: 1, Name: "test", Platform: capability.PlatformAnthropic}
	// threshold=400 remaining, limit=1000 → effectiveThreshold = 600 (usage trigger)
	// currentUsed=800 (after), oldUsed=800-50=750 (before). Both >= 600, no crossing.
	dims := []QuotaNotifyDimension{
		{
			Name:          quotaDimDaily,
			Enabled:       true,
			Threshold:     400,
			ThresholdType: thresholdTypeFixed,
			CurrentUsed:   800,
			Limit:         1000,
		},
	}
	s.CheckQuotaDimCrossings(account, dims, 50, []string{"admin@example.com"}, "TestSite")
}

func TestCheckQuotaDimCrossings_NegativeResolvedThreshold_Skipped(t *testing.T) {
	s, _ := newBalanceNotifyServiceForTest()
	account := &QuotaNotifyAccount{ID: 1, Name: "test", Platform: capability.PlatformAnthropic}
	// threshold=1200 remaining, limit=1000 → effectiveThreshold = 1000-1200 = -200
	// Negative resolved threshold → skipped.
	dims := []QuotaNotifyDimension{
		{
			Name:          quotaDimDaily,
			Enabled:       true,
			Threshold:     1200,
			ThresholdType: thresholdTypeFixed,
			CurrentUsed:   950,
			Limit:         1000,
		},
	}
	s.CheckQuotaDimCrossings(account, dims, 50, []string{"admin@example.com"}, "TestSite")
}

func TestCheckQuotaDimCrossings_PercentageThreshold_NoCrossing(t *testing.T) {
	s, _ := newBalanceNotifyServiceForTest()
	account := &QuotaNotifyAccount{ID: 1, Name: "test", Platform: capability.PlatformAnthropic}
	// threshold=30%, limit=1000 → effectiveThreshold = 1000 * (1 - 0.30) = 700
	// currentUsed=500, oldUsed=500-50=450. Both < 700, no crossing.
	dims := []QuotaNotifyDimension{
		{
			Name:          quotaDimWeekly,
			Enabled:       true,
			Threshold:     30,
			ThresholdType: thresholdTypePercentage,
			CurrentUsed:   500,
			Limit:         1000,
		},
	}
	s.CheckQuotaDimCrossings(account, dims, 50, []string{"admin@example.com"}, "TestSite")
}

func TestCheckQuotaDimCrossings_ZeroLimit_Skipped(t *testing.T) {
	s, _ := newBalanceNotifyServiceForTest()
	account := &QuotaNotifyAccount{ID: 1, Name: "test", Platform: capability.PlatformAnthropic}
	// limit=0 → resolvedThreshold returns 0 → skipped.
	dims := []QuotaNotifyDimension{
		{
			Name:          quotaDimTotal,
			Enabled:       true,
			Threshold:     100,
			ThresholdType: thresholdTypeFixed,
			CurrentUsed:   50,
			Limit:         0,
		},
	}
	s.CheckQuotaDimCrossings(account, dims, 50, []string{"admin@example.com"}, "TestSite")
}

func TestCheckQuotaDimCrossings_MultipleDims_MixedResults(t *testing.T) {
	s, _ := newBalanceNotifyServiceForTest()
	account := &QuotaNotifyAccount{ID: 1, Name: "test", Platform: capability.PlatformAnthropic}
	// dim1: no crossing (both below effective threshold)
	// dim2: disabled (skipped)
	// dim3: zero threshold (skipped)
	dims := []QuotaNotifyDimension{
		{
			Name:          quotaDimDaily,
			Enabled:       true,
			Threshold:     400,
			ThresholdType: thresholdTypeFixed,
			CurrentUsed:   300, // oldUsed=250, effectiveThreshold=600, both below
			Limit:         1000,
		},
		{
			Name:          quotaDimWeekly,
			Enabled:       false,
			Threshold:     100,
			ThresholdType: thresholdTypeFixed,
			CurrentUsed:   900,
			Limit:         1000,
		},
		{
			Name:          quotaDimTotal,
			Enabled:       true,
			Threshold:     0,
			ThresholdType: thresholdTypeFixed,
			CurrentUsed:   500,
			Limit:         1000,
		},
	}
	// None should trigger. No panic expected.
	s.CheckQuotaDimCrossings(account, dims, 50, []string{"admin@example.com"}, "TestSite")
}
