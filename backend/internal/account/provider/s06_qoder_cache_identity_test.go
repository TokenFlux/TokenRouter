package provider

import (
	"context"
	"testing"

	"github.com/TokenFlux/TokenRouter/internal/account"
	"github.com/TokenFlux/TokenRouter/internal/routing/capability"
	"github.com/stretchr/testify/require"
)

// 同一个账号换凭据后不能复用旧身份的内存用量展示；key 与 TTL 不变。
func TestQoderUsageCacheDoesNotCrossCredentialIdentity(t *testing.T) {
	repo := &accountUsageCodexProbeRepo{usageRecordFixture: usageRecordFixture{accounts: []account.Record{{ID: 919, Platform: capability.PlatformQoder, Type: capability.AccountTypeCosy, Credentials: qoderUsageCredentials("first")}}}}
	upstream := &qoderUsageHTTPUpstreamStub{bodies: []string{`{"userType":"teams","userQuota":{"total":100,"used":1,"remaining":99}}`, `{"userType":"teams","userQuota":{"total":100,"used":2,"remaining":98}}`}}
	svc := newOAuthUsageFixture(oauthUsageFixtureOptions{accountRepo: repo, cache: account.NewOAuthUsageCache(), httpUpstream: upstream})
	first, err := svc.GetUsage(context.Background(), 919)
	require.NoError(t, err)
	require.Equal(t, float64(1), first.QoderQuota.UserQuota.Used)
	repo.accounts[0].Credentials = qoderUsageCredentials("second")
	second, err := svc.GetUsage(context.Background(), 919)
	require.NoError(t, err)
	require.Equal(t, float64(2), second.QoderQuota.UserQuota.Used)
	require.Equal(t, int32(2), upstream.calls)
}
