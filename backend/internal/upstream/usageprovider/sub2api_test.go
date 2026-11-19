package usageprovider

import (
	"math"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/TokenFlux/TokenRouter/internal/upstream/usageview"
)

// sub2api_test.go tests Sub2API usage normalization modes, window validation,
// and finite numeric tolerance comparisons.

// TestParseSub2APIUsageModes exercises wallet, key-quota, and unlimited
// subscription responses. It checks their selected modes, rolling-limit
// presence, sentinel omission from JSON, and rejection of inconsistent quota
// arithmetic.
func TestParseSub2APIUsageModes(t *testing.T) {
	balance, err := ParseSub2APIUsage([]byte(`{"isValid":true,"mode":"unrestricted","unit":"USD","planName":"payg","remaining":12.5,"balance":12.5}`))
	require.NoError(t, err)
	require.Equal(t, "balance", balance.Mode)
	require.Equal(t, 12.5, *balance.Balance.Remaining)

	quota, err := ParseSub2APIUsage([]byte(`{"isValid":true,"mode":"quota_limited","status":"active","unit":"USD","remaining":75,"quota":{"limit":100,"used":25,"remaining":75,"unit":"USD"},"rate_limits":[{"window":"5h","limit":10,"used":2,"remaining":8,"window_start":null}]}`))
	require.NoError(t, err)
	require.Equal(t, "quota", quota.Mode)
	require.Len(t, quota.Limits, 1)

	unlimited, err := ParseSub2APIUsage([]byte(`{"isValid":true,"mode":"unrestricted","unit":"USD","planName":"pro","remaining":-1,"subscription":{"daily_usage_usd":0,"weekly_usage_usd":0,"monthly_usage_usd":0,"expires_at":"2030-01-01T00:00:00Z","unlimited":true}}`))
	require.NoError(t, err)
	require.True(t, unlimited.Subscription.Unlimited)
	require.Nil(t, unlimited.Subscription.Remaining)
	require.NotContains(t, string(mustJSONMarshal(t, unlimited)), `-1`)

	_, err = ParseSub2APIUsage([]byte(`{"isValid":true,"mode":"quota_limited","status":"active","unit":"USD","remaining":90,"quota":{"limit":100,"used":25,"remaining":90,"unit":"USD"}}`))
	require.Error(t, err)
}

// TestParseSub2APIUsageNormalizesNegativeWalletAndRejectsMalformedWindowStart
// supplies a negative wallet and an offset expiry, then invalid or missing
// window starts. It checks retained debt, UTC expiry, and both metadata
// validation failures.
func TestParseSub2APIUsageNormalizesNegativeWalletAndRejectsMalformedWindowStart(t *testing.T) {
	balance, err := ParseSub2APIUsage([]byte(`{"isValid":true,"mode":"unrestricted","unit":"USD","planName":"wallet","remaining":-2.5,"balance":-2.5,"expires_at":"2030-01-01T00:00:00+08:00"}`))
	require.NoError(t, err)
	require.Equal(t, -2.5, *balance.Balance.Remaining)
	require.Equal(t, time.Date(2029, 12, 31, 16, 0, 0, 0, time.UTC), *balance.ExpiresAt)

	_, err = ParseSub2APIUsage([]byte(`{"isValid":true,"mode":"quota_limited","status":"active","rate_limits":[{"window":"5h","limit":10,"used":2,"remaining":8,"window_start":"not-a-time"}]}`))
	require.ErrorIs(t, err, usageview.ErrUpstreamUsageInvalidResponse)
	_, err = ParseSub2APIUsage([]byte(`{"isValid":true,"mode":"quota_limited","status":"active","rate_limits":[{"window":"5h","limit":10,"used":2,"remaining":8}]}`))
	require.ErrorIs(t, err, usageview.ErrUpstreamUsageInvalidResponse)
}

// TestParseSub2APIUsageDistinguishesMissingValidityFromRejectedKey compares an
// absent isValid marker with an explicit false marker. It checks that malformed
// usage and a rejected key receive distinct shared error categories.
func TestParseSub2APIUsageDistinguishesMissingValidityFromRejectedKey(t *testing.T) {
	_, err := ParseSub2APIUsage([]byte(`{"mode":"unrestricted"}`))
	require.ErrorIs(t, err, usageview.ErrUpstreamUsageInvalidResponse)

	_, err = ParseSub2APIUsage([]byte(`{"isValid":false}`))
	require.ErrorIs(t, err, usageview.ErrUpstreamUsageAuthFailed)
}

// TestCloseEnoughRejectsNonFiniteAndAcceptsTolerance checks NaN, positive and
// negative infinities, and finite rounding differences. Nonfinite inputs must
// return false even when compared against finite or infinite operands.
func TestCloseEnoughRejectsNonFiniteAndAcceptsTolerance(t *testing.T) {
	require.True(t, CloseEnough(10.0, 10.0000005))
	require.False(t, CloseEnough(10.0, 10.01))
	require.False(t, CloseEnough(math.Inf(1), 100.0))
	require.False(t, CloseEnough(100.0, math.Inf(1)))
	require.False(t, CloseEnough(math.Inf(-1), math.Inf(-1)))
	require.False(t, CloseEnough(math.NaN(), 0))
	require.False(t, CloseEnough(0, math.NaN()))
}
