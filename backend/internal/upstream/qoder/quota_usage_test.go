package qoder

import (
	"testing"

	"github.com/stretchr/testify/require"
)

// TestParseUsagePresentationKeepsScopedCreditsSeparate reads a dedicated
// package alongside exhausted general credits. The package keeps its scope
// labels and availability instead of changing the general pool's remaining amount.
func TestParseUsagePresentationKeepsScopedCreditsSeparate(t *testing.T) {
	quota, err := ParseUsagePresentation([]byte(`{"displayMode":"qoder","qoderUsage":{"userId":"test","userType":"teams","usageType":"credits","isQuotaExceeded":true,"isPlanQuotaProrated":false,"userQuota":{"total":10,"remaining":0},"dedicatedResourcePackages":[{"id":"package","name":"Model credits","remaining":20,"available":true,"displayLabels":[{"dimension":"model","value":"Qwen3.8-Flash"}]}]}}`))
	require.NoError(t, err)
	require.Equal(t, 0.0, quota.UserQuota.Remaining)
	require.Len(t, quota.Dedicated, 1)
	require.Equal(t, 20.0, quota.Dedicated[0].Remaining)
	require.Equal(t, "model", quota.Dedicated[0].Labels[0].Dimension)
}

// TestParseUsagePresentationEnterpriseIsNotZeroQuota preserves external usage
// navigation without synthesizing a numeric balance. Mixed, missing, and unsafe
// presentations are rejected before the provider can persist a quota observation.
func TestParseUsagePresentationEnterpriseIsNotZeroQuota(t *testing.T) {
	quota, err := ParseUsagePresentation([]byte(`{"displayMode":"enterprise","enterpriseUsage":{"openMode":"externalBrowser","detailUrl":"https://qoder.com/usage"}}`))
	require.NoError(t, err)
	require.Equal(t, "enterprise", quota.DisplayMode)
	require.Nil(t, quota.UserQuota)
	require.False(t, quota.IsQuotaExceeded)
	for _, raw := range []string{`{}`, `{"displayMode":"qoder","qoderUsage":{}}`, `{"displayMode":"enterprise","enterpriseUsage":{"openMode":"externalBrowser","detailUrl":"javascript:alert(1)"}}`, `{"displayMode":"enterprise","qoderUsage":{},"enterpriseUsage":{"openMode":"externalBrowser","detailUrl":"https://qoder.com"}}`} {
		_, err := ParseUsagePresentation([]byte(raw))
		require.Error(t, err)
	}
}

// TestCreditPresentationRequiresCompleteObservation checks every required flag before
// a quota observation can enter storage. Explicit false remains authoritative,
// while missing, null and mistyped flags or invalid fractions fail the query.
func TestCreditPresentationRequiresCompleteObservation(t *testing.T) {
	for _, suffix := range []string{
		"",
		`,"isQuotaExceeded":false`,
		`,"isPlanQuotaProrated":false`,
		`,"isQuotaExceeded":null,"isPlanQuotaProrated":false`,
		`,"isQuotaExceeded":"false","isPlanQuotaProrated":true`,
		`,"isQuotaExceeded":true,"isPlanQuotaProrated":false,"totalUsagePercentage":2`,
		`,"isQuotaExceeded":false,"isPlanQuotaProrated":false,"userQuota":null`,
		`,"isQuotaExceeded":false,"isPlanQuotaProrated":false,"dedicatedResourcePackages":[{"id":"pack"}]`,
	} {
		_, err := ParseUsagePresentation([]byte(`{"displayMode":"qoder","qoderUsage":{"userId":"u","userType":"teams","usageType":"credits"` + suffix + `}}`))
		require.Error(t, err, suffix)
	}
	valid, err := ParseUsagePresentation([]byte(`{"displayMode":"qoder","qoderUsage":{"userId":"u","userType":"teams","usageType":"credits","isQuotaExceeded":false,"isPlanQuotaProrated":false}}`))
	require.NoError(t, err)
	require.False(t, valid.IsQuotaExceeded)
}
