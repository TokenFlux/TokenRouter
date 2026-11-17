package provider

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// TestQoderDedicatedCreditsPreventGlobalPause keeps restricted credits outside
// general capacity totals. An available package prevents a provider-wide pause
// that would otherwise also block models able to spend those restricted credits.
func TestQoderDedicatedCreditsPreventGlobalPause(t *testing.T) {
	now := time.Now()
	expires := now.Add(time.Hour)
	quota := &QoderQuotaInfo{ExpiresAt: &expires, IsQuotaExceeded: true, UserQuota: &QoderQuotaProgress{Total: 100, Used: 100}, Dedicated: []QoderDedicatedQuota{{QoderQuotaProgress: QoderQuotaProgress{Total: 20, Remaining: 20, Available: true}}}}
	require.Equal(t, 100.0, QoderQuotaTotalCapacity(quota))
	remaining, known := QoderQuotaTotalRemaining(quota)
	require.True(t, known)
	require.Zero(t, remaining)
	_, limited := QoderQuotaRateLimitResetAt(quota, now)
	require.False(t, limited)
	quota.Dedicated[0].Available = false
	_, limited = QoderQuotaRateLimitResetAt(quota, now)
	require.True(t, limited)
	quota.DisplayMode = "enterprise"
	_, limited = QoderQuotaRateLimitResetAt(quota, now)
	require.False(t, limited)
}

// TestQoderUsageCloneOwnsDedicatedMetadata mutates a cached response copy's
// package expiry, scope label, and enterprise destination. The source observation
// remains unchanged for another caller reading the same account usage cache.
func TestQoderUsageCloneOwnsDedicatedMetadata(t *testing.T) {
	expires := time.Now()
	source := &UsageInfo{QoderQuota: &QoderQuotaInfo{Enterprise: &QoderEnterpriseUsage{DetailURL: "https://qoder.com/usage"}, Dedicated: []QoderDedicatedQuota{{ExpiresAt: &expires, Labels: []QoderQuotaLabel{{Dimension: "model", Value: "one"}}}}}}
	cloned := CloneUsageInfo(source)
	cloned.QoderQuota.Enterprise.DetailURL = "https://qoder.com/other"
	cloned.QoderQuota.Dedicated[0].Labels[0].Value = "two"
	*cloned.QoderQuota.Dedicated[0].ExpiresAt = expires.Add(time.Hour)
	require.Equal(t, "https://qoder.com/usage", source.QoderQuota.Enterprise.DetailURL)
	require.Equal(t, "one", source.QoderQuota.Dedicated[0].Labels[0].Value)
	require.Equal(t, expires, *source.QoderQuota.Dedicated[0].ExpiresAt)
}
