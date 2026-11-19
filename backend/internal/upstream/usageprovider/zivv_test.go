package usageprovider

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/TokenFlux/TokenRouter/internal/upstream/usageview"
)

// zivv_test.go tests Zivv wallet and key-quota normalization and error
// classification.

// TestParseZivvUsageNormalizesWalletAndKeyQuota supplies wallet money and
// finite key allowance, then a zero key limit. It checks separate wallet and
// key totals, subscription display, and unlimited representation without a
// zero-limit field.
func TestParseZivvUsageNormalizesWalletAndKeyQuota(t *testing.T) {
	usage, err := ParseZivvUsage([]byte(`{"balance":900.490184,"currency":"USD","is_available":true,"key_limit":1000,"key_used":206.4,"plan_name":"cc b","total_used":22460.664473}`))
	require.NoError(t, err)
	require.Equal(t, usageview.UpstreamUsageAdapterZivv, usage.Provider)
	require.Equal(t, "balance", usage.Mode)
	require.Equal(t, 900.490184, *usage.Balance.Remaining)
	require.Equal(t, 22460.664473, *usage.Balance.Used)
	// Allow floating-point roundoff when adding wallet remainder and cumulative usage.
	require.InDelta(t, 23361.154657, *usage.Balance.Total, 0.000001)
	require.Len(t, usage.Limits, 1)
	require.Equal(t, "key_quota", usage.Limits[0].Name)
	require.Equal(t, 793.6, *usage.Limits[0].Remaining)
	require.Equal(t, "cc b", usage.Subscription.PlanName)
	require.False(t, usage.Subscription.Unlimited)
	require.Equal(t, 793.6, *usage.Subscription.Remaining)

	unlimited, err := ParseZivvUsage([]byte(`{"balance":1,"currency":"USD","is_available":true,"key_limit":0,"key_used":20646.4,"plan_name":"cc b","total_used":2}`))
	require.NoError(t, err)
	require.True(t, unlimited.Subscription.Unlimited)
	require.Nil(t, unlimited.Subscription.Remaining)
	require.NotContains(t, string(mustJSONMarshal(t, unlimited)), `"limit":0`)
}

// TestParseZivvUsageRejectsUnavailableOrMalformedResponses supplies an
// unavailable key, unsupported currency, and missing cumulative usage. It
// checks authentication failure for the first case and invalid-response errors
// for the malformed shapes.
func TestParseZivvUsageRejectsUnavailableOrMalformedResponses(t *testing.T) {
	_, err := ParseZivvUsage([]byte(`{"balance":1,"currency":"USD","is_available":false,"key_limit":0,"key_used":0,"total_used":0}`))
	require.ErrorIs(t, err, usageview.ErrUpstreamUsageAuthFailed)
	_, err = ParseZivvUsage([]byte(`{"balance":1,"currency":"EUR","is_available":true,"key_limit":0,"key_used":0,"total_used":0}`))
	require.ErrorIs(t, err, usageview.ErrUpstreamUsageInvalidResponse)
	_, err = ParseZivvUsage([]byte(`{"balance":1,"currency":"USD","is_available":true,"key_limit":100,"key_used":0}`))
	require.ErrorIs(t, err, usageview.ErrUpstreamUsageInvalidResponse)
}
