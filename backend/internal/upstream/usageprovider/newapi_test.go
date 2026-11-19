package usageprovider

import (
	"math"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/TokenFlux/TokenRouter/internal/upstream/usageview"
)

// newapi_test.go tests New API token quota conversion, overflow rejection, and
// wallet balance normalization.

// TestParseNewAPITokenUsageConvertsQuotaUnits converts finite raw quota to USD,
// then supplies finite negative counters for an unlimited key and overflowing
// counters for a limited key. It checks separate key limits, expiry, omitted
// unlimited amounts, rejected-key classification, and inconsistent or
// overflowed counter rejection.
func TestParseNewAPITokenUsageConvertsQuotaUnits(t *testing.T) {
	response, err := ParseNewAPITokenUsage([]byte(`{"code":true,"message":"ok","data":{"object":"token_usage","name":"Default Token","total_granted":632500000,"total_used":360000,"total_available":632140000,"unlimited_quota":false,"expires_at":1893456000}}`))
	require.NoError(t, err)
	settings := NewAPIUsageDisplaySettings{Unit: "USD", QuotaPerUnit: 500000}
	usage, err := NormalizeNewAPITokenUsage(response, settings)
	require.NoError(t, err)
	require.Nil(t, usage.Balance, "token quota must not be exposed as wallet balance")
	require.Len(t, usage.Limits, 1)
	require.Equal(t, 1264.28, *usage.Limits[0].Remaining)
	require.Equal(t, "Default Token", usage.Subscription.PlanName)
	require.Equal(t, time.Unix(1893456000, 0).UTC(), *usage.ExpiresAt)

	// Supply finite negative counters to exercise unlimited representation. The
	// normalized JSON omits those values while retaining the subscription's unlimited flag.
	unlimitedResponse, err := ParseNewAPITokenUsage([]byte(`{"code":true,"data":{"object":"token_usage","name":"Unlimited","total_granted":-16015,"total_used":1995297521,"total_available":-1995313536,"unlimited_quota":true,"expires_at":0}}`))
	require.NoError(t, err)
	unlimited, err := NormalizeNewAPITokenUsage(unlimitedResponse, settings)
	require.NoError(t, err)
	require.True(t, unlimited.Subscription.Unlimited)
	require.Nil(t, unlimited.Balance)
	require.NotContains(t, string(mustJSONMarshal(t, unlimited)), "-1995313536")

	_, err = ParseNewAPITokenUsage([]byte(`{"success":false,"message":"token not found"}`))
	require.ErrorIs(t, err, usageview.ErrUpstreamUsageAuthFailed)

	inconsistentResponse, err := ParseNewAPITokenUsage([]byte(`{"code":true,"data":{"object":"token_usage","total_granted":10,"total_used":2,"total_available":2,"unlimited_quota":false,"expires_at":0}}`))
	require.NoError(t, err)
	_, err = NormalizeNewAPITokenUsage(inconsistentResponse, settings)
	require.ErrorIs(t, err, usageview.ErrUpstreamUsageInvalidResponse)

	maxFloat := math.MaxFloat64
	overflowResponse, err := ParseNewAPITokenUsage([]byte(`{"code":true,"data":{"object":"token_usage","total_granted":1,"total_used":1,"total_available":1,"unlimited_quota":false,"expires_at":0}}`))
	require.NoError(t, err)
	overflowResponse.Data.TotalGranted = &maxFloat
	overflowResponse.Data.TotalUsed = &maxFloat
	overflowResponse.Data.TotalAvailable = &maxFloat
	_, err = NormalizeNewAPITokenUsage(overflowResponse, settings)
	require.ErrorIs(t, err, usageview.ErrUpstreamUsageInvalidResponse)
}

// TestParseNewAPIWalletBalanceSupportsDisplayAndQuotaUnits compares labeled and
// unlabeled display balances with a user-self raw-quota response. It checks
// equal normalized remainders, absent lifetime-derived wallet totals, and
// rejection of an HTML response.
func TestParseNewAPIWalletBalanceSupportsDisplayAndQuotaUnits(t *testing.T) {
	display, err := ParseNewAPIWalletBalance([]byte(`{"balance_infos":[{"currency":"USD","total_balance":"1264.28"}]}`), NewAPIUsageDisplaySettings{Unit: "USD", QuotaPerUnit: 500000})
	require.NoError(t, err)
	require.Equal(t, 1264.28, *display.Balance.Remaining)
	require.Equal(t, "USD", display.Unit)
	display, err = ParseNewAPIWalletBalance([]byte(`{"balance_infos":[{"total_balance":1264.28}]}`), NewAPIUsageDisplaySettings{Unit: "USD", QuotaPerUnit: 500000})
	require.NoError(t, err)
	require.Equal(t, 1264.28, *display.Balance.Remaining)

	quota, err := ParseNewAPIUserSelfWallet([]byte(`{"success":true,"data":{"id":42,"quota":632140000,"used_quota":360000}}`), NewAPIUsageDisplaySettings{Unit: "USD", QuotaPerUnit: 500000}, "42")
	require.NoError(t, err)
	require.Equal(t, 1264.28, *quota.Balance.Remaining)
	require.Nil(t, quota.Balance.Used)
	require.Nil(t, quota.Balance.Total)

	_, err = ParseNewAPIWalletBalance([]byte(`<html>frontend</html>`), NewAPIUsageDisplaySettings{Unit: "USD", QuotaPerUnit: 500000})
	require.ErrorIs(t, err, usageview.ErrUpstreamUsageInvalidResponse)
}

// TestNormalizeNewAPIQuotaUsesConfiguredCNYExchangeRate supplies one USD's raw
// quota with a configured CNY exchange factor. It checks that the converted
// amount uses that factor.
func TestNormalizeNewAPIQuotaUsesConfiguredCNYExchangeRate(t *testing.T) {
	settings := NewAPIUsageDisplaySettings{Unit: "CNY", QuotaPerUnit: 500000, USDExchangeRate: 7.3}
	require.Equal(t, 7.3, NormalizeNewAPIQuota(500000, settings))
}
