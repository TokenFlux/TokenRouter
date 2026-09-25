package usageprovider_test

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/TokenFlux/TokenRouter/internal/infra/httpclient"

	"github.com/TokenFlux/TokenRouter/internal/upstream/usageprovider"
	"github.com/TokenFlux/TokenRouter/internal/upstream/usageview"
	"github.com/stretchr/testify/require"
)

// 序列化断言仍校验对外数值形状，不使用查询服务包装解析器。
func mustJSONMarshal(t *testing.T, value any) []byte {
	t.Helper()
	encoded, err := json.Marshal(value)
	require.NoError(t, err)
	return encoded
}

func TestParseSub2APIUsageModes(t *testing.T) {
	balance, err := usageprovider.ParseSub2APIUsage([]byte(`{"isValid":true,"mode":"unrestricted","unit":"USD","planName":"payg","remaining":12.5,"balance":12.5}`))
	require.NoError(t, err)
	require.Equal(t, "balance", balance.Mode)
	require.Equal(t, 12.5, *balance.Balance.Remaining)

	quota, err := usageprovider.ParseSub2APIUsage([]byte(`{"isValid":true,"mode":"quota_limited","status":"active","unit":"USD","remaining":75,"quota":{"limit":100,"used":25,"remaining":75,"unit":"USD"},"rate_limits":[{"window":"5h","limit":10,"used":2,"remaining":8,"window_start":null}]}`))
	require.NoError(t, err)
	require.Equal(t, "quota", quota.Mode)
	require.Len(t, quota.Limits, 1)

	unlimited, err := usageprovider.ParseSub2APIUsage([]byte(`{"isValid":true,"mode":"unrestricted","unit":"USD","planName":"pro","remaining":-1,"subscription":{"daily_usage_usd":0,"weekly_usage_usd":0,"monthly_usage_usd":0,"expires_at":"2030-01-01T00:00:00Z","unlimited":true}}`))
	require.NoError(t, err)
	require.True(t, unlimited.Subscription.Unlimited)
	require.Nil(t, unlimited.Subscription.Remaining)
	require.NotContains(t, string(mustJSONMarshal(t, unlimited)), `-1`)

	_, err = usageprovider.ParseSub2APIUsage([]byte(`{"isValid":true,"mode":"quota_limited","status":"active","unit":"USD","remaining":90,"quota":{"limit":100,"used":25,"remaining":90,"unit":"USD"}}`))
	require.Error(t, err)
}

func TestParseSub2APIUsageNormalizesNegativeWalletAndRejectsMalformedWindowStart(t *testing.T) {
	balance, err := usageprovider.ParseSub2APIUsage([]byte(`{"isValid":true,"mode":"unrestricted","unit":"USD","planName":"wallet","remaining":-2.5,"balance":-2.5,"expires_at":"2030-01-01T00:00:00+08:00"}`))
	require.NoError(t, err)
	require.Equal(t, -2.5, *balance.Balance.Remaining)
	require.Equal(t, time.Date(2029, 12, 31, 16, 0, 0, 0, time.UTC), *balance.ExpiresAt)

	_, err = usageprovider.ParseSub2APIUsage([]byte(`{"isValid":true,"mode":"quota_limited","status":"active","rate_limits":[{"window":"5h","limit":10,"used":2,"remaining":8,"window_start":"not-a-time"}]}`))
	require.ErrorIs(t, err, usageview.ErrUpstreamUsageInvalidResponse)
	_, err = usageprovider.ParseSub2APIUsage([]byte(`{"isValid":true,"mode":"quota_limited","status":"active","rate_limits":[{"window":"5h","limit":10,"used":2,"remaining":8}]}`))
	require.ErrorIs(t, err, usageview.ErrUpstreamUsageInvalidResponse)
}

func TestParseSub2APIUsageDistinguishesMissingValidityFromRejectedKey(t *testing.T) {
	_, err := usageprovider.ParseSub2APIUsage([]byte(`{"mode":"unrestricted"}`))
	require.ErrorIs(t, err, usageview.ErrUpstreamUsageInvalidResponse)

	_, err = usageprovider.ParseSub2APIUsage([]byte(`{"isValid":false}`))
	require.ErrorIs(t, err, usageview.ErrUpstreamUsageAuthFailed)
}

func TestUpstreamUsageEndpointUsesExistingVersionedBaseURLRules(t *testing.T) {
	tests := map[string]string{
		"https://gateway.example/v1":     "https://gateway.example/v1/usage",
		"https://gateway.example/v4":     "https://gateway.example/v4/usage",
		"https://gateway.example/v1beta": "https://gateway.example/v1beta/usage",
		"https://gateway.example/api":    "https://gateway.example/api/v1/usage",
	}
	for base, want := range tests {
		got, err := usageprovider.UpstreamUsageEndpoint(base, "/v1/usage", httpclient.BuildOpenAIEndpointURL)
		require.NoError(t, err, base)
		require.Equal(t, want, got, base)
	}

	statusTests := map[string]string{
		"https://gateway.example/v1":         "https://gateway.example/api/status",
		"https://gateway.example/subpath/v1": "https://gateway.example/subpath/api/status",
		"https://gateway.example/v4":         "https://gateway.example/api/status",
	}
	for base, want := range statusTests {
		got, err := usageprovider.UpstreamUsageStatusEndpoint(base)
		require.NoError(t, err, base)
		require.Equal(t, want, got, base)
	}
	tokenEndpoint, err := usageprovider.UpstreamUsageRootEndpoint("https://gateway.example/v1", "/api/usage/token")
	require.NoError(t, err)
	require.Equal(t, "https://gateway.example/api/usage/token", tokenEndpoint)
	tokenEndpoint, err = usageprovider.UpstreamUsageTokenEndpoint("https://gateway.example/v1")
	require.NoError(t, err)
	require.Equal(t, "https://gateway.example/api/usage/token/", tokenEndpoint)
	walletEndpoint, err := usageprovider.UpstreamUsageWalletEndpoint("https://gateway.example/v1")
	require.NoError(t, err)
	require.Equal(t, "https://gateway.example/user/balance", walletEndpoint)
	selfEndpoint, err := usageprovider.UpstreamUsageUserSelfEndpoint("https://gateway.example/v1")
	require.NoError(t, err)
	require.Equal(t, "https://gateway.example/api/user/self", selfEndpoint)
}

func TestParseZivvUsageNormalizesWalletAndKeyQuota(t *testing.T) {
	usage, err := usageprovider.ParseZivvUsage([]byte(`{"balance":900.490184,"currency":"USD","is_available":true,"key_limit":1000,"key_used":206.4,"plan_name":"cc b","total_used":22460.664473}`))
	require.NoError(t, err)
	require.Equal(t, usageview.UpstreamUsageAdapterZivv, usage.Provider)
	require.Equal(t, "balance", usage.Mode)
	require.Equal(t, 900.490184, *usage.Balance.Remaining)
	require.Equal(t, 22460.664473, *usage.Balance.Used)
	require.InDelta(t, 23361.154657, *usage.Balance.Total, 0.000001)
	require.Len(t, usage.Limits, 1)
	require.Equal(t, "key_quota", usage.Limits[0].Name)
	require.Equal(t, 793.6, *usage.Limits[0].Remaining)
	require.Equal(t, "cc b", usage.Subscription.PlanName)
	require.False(t, usage.Subscription.Unlimited)
	require.Equal(t, 793.6, *usage.Subscription.Remaining)

	unlimited, err := usageprovider.ParseZivvUsage([]byte(`{"balance":1,"currency":"USD","is_available":true,"key_limit":0,"key_used":20646.4,"plan_name":"cc b","total_used":2}`))
	require.NoError(t, err)
	require.True(t, unlimited.Subscription.Unlimited)
	require.Nil(t, unlimited.Subscription.Remaining)
	require.NotContains(t, string(mustJSONMarshal(t, unlimited)), `"limit":0`)
}

func TestParseZivvUsageRejectsUnavailableOrMalformedResponses(t *testing.T) {
	_, err := usageprovider.ParseZivvUsage([]byte(`{"balance":1,"currency":"USD","is_available":false,"key_limit":0,"key_used":0,"total_used":0}`))
	require.ErrorIs(t, err, usageview.ErrUpstreamUsageAuthFailed)
	_, err = usageprovider.ParseZivvUsage([]byte(`{"balance":1,"currency":"EUR","is_available":true,"key_limit":0,"key_used":0,"total_used":0}`))
	require.ErrorIs(t, err, usageview.ErrUpstreamUsageInvalidResponse)
	_, err = usageprovider.ParseZivvUsage([]byte(`{"balance":1,"currency":"USD","is_available":true,"key_limit":100,"key_used":0}`))
	require.ErrorIs(t, err, usageview.ErrUpstreamUsageInvalidResponse)
}

func TestParseNewAPITokenUsageConvertsQuotaUnits(t *testing.T) {
	response, err := usageprovider.ParseNewAPITokenUsage([]byte(`{"code":true,"message":"ok","data":{"object":"token_usage","name":"Default Token","total_granted":632500000,"total_used":360000,"total_available":632140000,"unlimited_quota":false,"expires_at":1893456000}}`))
	require.NoError(t, err)
	settings := usageprovider.NewAPIUsageDisplaySettings{Unit: "USD", QuotaPerUnit: 500000}
	usage, err := usageprovider.NormalizeNewAPITokenUsage(response, settings)
	require.NoError(t, err)
	require.Nil(t, usage.Balance, "token quota must not be exposed as wallet balance")
	require.Len(t, usage.Limits, 1)
	require.Equal(t, 1264.28, *usage.Limits[0].Remaining)
	require.Equal(t, "Default Token", usage.Subscription.PlanName)
	require.Equal(t, time.Unix(1893456000, 0).UTC(), *usage.ExpiresAt)

	// 无限量 token 的 quota 字段可能因上游整数溢出而出现负数；这些字段
	// 不参与归一化，不能因此拒绝整个查询结果。
	unlimitedResponse, err := usageprovider.ParseNewAPITokenUsage([]byte(`{"code":true,"data":{"object":"token_usage","name":"Unlimited","total_granted":-16015,"total_used":1995297521,"total_available":-1995313536,"unlimited_quota":true,"expires_at":0}}`))
	require.NoError(t, err)
	unlimited, err := usageprovider.NormalizeNewAPITokenUsage(unlimitedResponse, settings)
	require.NoError(t, err)
	require.True(t, unlimited.Subscription.Unlimited)
	require.Nil(t, unlimited.Balance)
	require.NotContains(t, string(mustJSONMarshal(t, unlimited)), "-1995313536")

	_, err = usageprovider.ParseNewAPITokenUsage([]byte(`{"success":false,"message":"token not found"}`))
	require.ErrorIs(t, err, usageview.ErrUpstreamUsageAuthFailed)

	inconsistentResponse, err := usageprovider.ParseNewAPITokenUsage([]byte(`{"code":true,"data":{"object":"token_usage","total_granted":10,"total_used":2,"total_available":2,"unlimited_quota":false,"expires_at":0}}`))
	require.NoError(t, err)
	_, err = usageprovider.NormalizeNewAPITokenUsage(inconsistentResponse, settings)
	require.ErrorIs(t, err, usageview.ErrUpstreamUsageInvalidResponse)
}

func TestParseNewAPIWalletBalanceSupportsDisplayAndQuotaUnits(t *testing.T) {
	display, err := usageprovider.ParseNewAPIWalletBalance([]byte(`{"balance_infos":[{"currency":"USD","total_balance":"1264.28"}]}`), usageprovider.NewAPIUsageDisplaySettings{Unit: "USD", QuotaPerUnit: 500000})
	require.NoError(t, err)
	require.Equal(t, 1264.28, *display.Balance.Remaining)
	require.Equal(t, "USD", display.Unit)
	display, err = usageprovider.ParseNewAPIWalletBalance([]byte(`{"balance_infos":[{"total_balance":1264.28}]}`), usageprovider.NewAPIUsageDisplaySettings{Unit: "USD", QuotaPerUnit: 500000})
	require.NoError(t, err)
	require.Equal(t, 1264.28, *display.Balance.Remaining)

	quota, err := usageprovider.ParseNewAPIUserSelfWallet([]byte(`{"success":true,"data":{"id":42,"quota":632140000,"used_quota":360000}}`), usageprovider.NewAPIUsageDisplaySettings{Unit: "USD", QuotaPerUnit: 500000}, "42")
	require.NoError(t, err)
	require.Equal(t, 1264.28, *quota.Balance.Remaining)
	require.Nil(t, quota.Balance.Used)
	require.Nil(t, quota.Balance.Total)

	_, err = usageprovider.ParseNewAPIWalletBalance([]byte(`<html>frontend</html>`), usageprovider.NewAPIUsageDisplaySettings{Unit: "USD", QuotaPerUnit: 500000})
	require.ErrorIs(t, err, usageview.ErrUpstreamUsageInvalidResponse)
}

func TestNormalizeNewAPIQuotaUsesConfiguredCNYExchangeRate(t *testing.T) {
	settings := usageprovider.NewAPIUsageDisplaySettings{Unit: "CNY", QuotaPerUnit: 500000, USDExchangeRate: 7.3}
	require.Equal(t, 7.3, usageprovider.NormalizeNewAPIQuota(500000, settings))
}
