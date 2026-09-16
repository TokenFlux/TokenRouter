package search

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestNewManager_PreservesOrder(t *testing.T) {
	configs := []ProviderConfig{
		{Type: "brave", APIKey: "k3"},
		{Type: "tavily", APIKey: "k1"},
	}
	m := newTestManager(configs, nil)
	require.Equal(t, "brave", m.ProviderConfigs()[0].Type)
	require.Equal(t, "tavily", m.ProviderConfigs()[1].Type)
}
func TestManager_SearchWithBestProvider_EmptyQuery(t *testing.T) {
	m := newTestManager([]ProviderConfig{{Type: "brave", APIKey: "k"}}, nil)
	_, _, err := m.SearchWithBestProvider(context.Background(), SearchRequest{Query: ""})
	require.ErrorContains(t, err, "empty search query")

	_, _, err = m.SearchWithBestProvider(context.Background(), SearchRequest{Query: "   "})
	require.ErrorContains(t, err, "empty search query")
}
func TestManager_SearchWithBestProvider_SkipEmptyAPIKey(t *testing.T) {
	m := newTestManager([]ProviderConfig{{Type: "brave", APIKey: ""}}, nil)
	_, _, err := m.SearchWithBestProvider(context.Background(), SearchRequest{Query: "test"})
	require.ErrorContains(t, err, "no available provider")
}
func TestManager_SearchWithBestProvider_SkipExpired(t *testing.T) {
	past := time.Now().Add(-1 * time.Hour).Unix()
	m := newTestManager([]ProviderConfig{
		{Type: "brave", APIKey: "k", ExpiresAt: &past},
	}, nil)
	_, _, err := m.SearchWithBestProvider(context.Background(), SearchRequest{Query: "test"})
	require.ErrorContains(t, err, "no available provider")
}
func TestManager_GetUsage_NilRedis(t *testing.T) {
	m := newTestManager(nil, nil)
	used, err := m.GetUsage(context.Background(), "brave")
	require.NoError(t, err)
	require.Equal(t, int64(0), used)
}
func TestManager_GetAllUsage_NilRedis(t *testing.T) {
	m := newTestManager([]ProviderConfig{
		{Type: "brave"},
	}, nil)
	usage := m.GetAllUsage(context.Background())
	require.Equal(t, int64(0), usage["brave"])
}
func TestQuotaTTLFromSubscription_NilSubscription(t *testing.T) {
	ttl := quotaTTLFromSubscription(nil)
	require.Equal(t, defaultQuotaTTL, ttl)
}
func TestQuotaTTLFromSubscription_ZeroSubscription(t *testing.T) {
	zero := int64(0)
	ttl := quotaTTLFromSubscription(&zero)
	require.Equal(t, defaultQuotaTTL, ttl)
}
func TestQuotaTTLFromSubscription_ValidSubscription(t *testing.T) {
	// Subscribed 10 days ago — next reset in ~20 days
	sub := time.Now().Add(-10 * 24 * time.Hour).Unix()
	ttl := quotaTTLFromSubscription(&sub)
	require.Greater(t, ttl, 15*24*time.Hour) // at least 15 days
	require.Less(t, ttl, 25*24*time.Hour+quotaTTLBuffer)
}
func TestNextMonthlyReset_SubscribedRecentPast(t *testing.T) {
	// Subscribed on the 10th of this month (always valid day)
	now := time.Now().UTC()
	sub := time.Date(now.Year(), now.Month(), 10, 0, 0, 0, 0, time.UTC)
	next := nextMonthlyReset(sub)
	require.True(t, next.After(now) || next.Equal(now), "next reset should be in the future or now")
	require.True(t, next.Before(now.AddDate(0, 1, 1)))
}
func TestNextMonthlyReset_SubscribedLongAgo(t *testing.T) {
	// Subscribed 6 months ago on the 1st
	sub := time.Now().UTC().AddDate(0, -6, 0)
	sub = time.Date(sub.Year(), sub.Month(), 1, 0, 0, 0, 0, time.UTC)
	next := nextMonthlyReset(sub)
	require.True(t, next.After(time.Now().UTC()))
	// Should be within the next 31 days
	require.True(t, next.Before(time.Now().UTC().AddDate(0, 1, 1)))
}
func TestNextMonthlyReset_FutureSubscription(t *testing.T) {
	sub := time.Now().UTC().AddDate(0, 0, 5)
	next := nextMonthlyReset(sub)
	require.True(t, next.After(time.Now().UTC()))
}
func TestAddMonthsClamped_Jan31ToFeb(t *testing.T) {
	sub := time.Date(2026, 1, 31, 0, 0, 0, 0, time.UTC)
	next := addMonthsClamped(sub, 1)
	require.Equal(t, time.Month(2), next.Month())
	require.Equal(t, 28, next.Day()) // Feb 28 (2026 is not a leap year)
}
func TestAddMonthsClamped_Jan31ToFebLeapYear(t *testing.T) {
	sub := time.Date(2028, 1, 31, 0, 0, 0, 0, time.UTC)
	next := addMonthsClamped(sub, 1)
	require.Equal(t, time.Month(2), next.Month())
	require.Equal(t, 29, next.Day()) // Feb 29 (2028 is a leap year)
}
func TestAddMonthsClamped_Mar31ToApr(t *testing.T) {
	sub := time.Date(2026, 3, 31, 0, 0, 0, 0, time.UTC)
	next := addMonthsClamped(sub, 1)
	require.Equal(t, time.Month(4), next.Month())
	require.Equal(t, 30, next.Day()) // Apr has 30 days
}
func TestAddMonthsClamped_NormalDay(t *testing.T) {
	sub := time.Date(2026, 1, 15, 0, 0, 0, 0, time.UTC)
	next := addMonthsClamped(sub, 1)
	require.Equal(t, time.Month(2), next.Month())
	require.Equal(t, 15, next.Day()) // no clamping needed
}
func TestIsProviderAvailable_EmptyAPIKey(t *testing.T) {
	m := newTestManager(nil, nil)
	require.False(t, m.isProviderAvailable(ProviderConfig{APIKey: ""}))
}
func TestIsProviderAvailable_Expired(t *testing.T) {
	m := newTestManager(nil, nil)
	past := time.Now().Add(-1 * time.Hour).Unix()
	require.False(t, m.isProviderAvailable(ProviderConfig{APIKey: "k", ExpiresAt: &past}))
}
func TestIsProviderAvailable_Valid(t *testing.T) {
	m := newTestManager(nil, nil)
	future := time.Now().Add(1 * time.Hour).Unix()
	require.True(t, m.isProviderAvailable(ProviderConfig{APIKey: "k", ExpiresAt: &future}))
	require.True(t, m.isProviderAvailable(ProviderConfig{APIKey: "k"})) // no expiry
}
func TestResolveProxyID_AccountProxyOverrides(t *testing.T) {
	cfg := ProviderConfig{ProxyID: 42}
	require.Equal(t, int64(0), resolveProxyID(cfg, "http://account-proxy:8080"))
	require.Equal(t, int64(42), resolveProxyID(cfg, ""))
}
func TestIsProxyAvailable_NilRedis(t *testing.T) {
	m := newTestManager(nil, nil)
	require.True(t, m.isProxyAvailable(context.Background(), 42))
}
func TestIsProxyAvailable_ZeroID(t *testing.T) {
	m := newTestManager(nil, nil)
	require.True(t, m.isProxyAvailable(context.Background(), 0))
}
func TestSelectByQuotaWeight_NoQuotaLast(t *testing.T) {
	m := newTestManager(nil, nil)
	candidates := []ProviderConfig{
		{Type: "brave", APIKey: "k1", QuotaLimit: 0},
		{Type: "tavily", APIKey: "k2", QuotaLimit: 100},
	}
	result := m.selectByQuotaWeight(context.Background(), candidates)
	require.Len(t, result, 2)
	require.Equal(t, "tavily", result[0].Type)
	require.Equal(t, "brave", result[1].Type)
}
func TestSelectByQuotaWeight_AllNoQuota(t *testing.T) {
	m := newTestManager(nil, nil)
	candidates := []ProviderConfig{
		{Type: "brave", APIKey: "k1", QuotaLimit: 0},
		{Type: "tavily", APIKey: "k2", QuotaLimit: 0},
	}
	result := m.selectByQuotaWeight(context.Background(), candidates)
	require.Len(t, result, 2)
}
func TestSelectByQuotaWeight_Empty(t *testing.T) {
	m := newTestManager(nil, nil)
	result := m.selectByQuotaWeight(context.Background(), nil)
	require.Empty(t, result)
}
func TestManager_ResetUsage_NilRedis(t *testing.T) {
	m := newTestManager(nil, nil)
	err := m.ResetUsage(context.Background(), "brave")
	require.NoError(t, err)
}

func newTestManager(configs []ProviderConfig, _ any) *Manager {
	return NewManager(configs, nil, nil, nil)
}
