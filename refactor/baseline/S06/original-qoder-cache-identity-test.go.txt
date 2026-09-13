package service

import (
	"context"
	"github.com/stretchr/testify/require"
	"testing"
)

// 同一个账号换凭据后不能复用旧身份的内存用量展示；key 与 TTL 不变。
func TestQoderUsageCacheDoesNotCrossCredentialIdentity(t *testing.T) {
	repo := &accountUsageCodexProbeRepo{stubOpenAIAccountRepo: stubOpenAIAccountRepo{accounts: []Account{{ID: 919, Platform: PlatformQoder, Type: AccountTypeCosy, Credentials: qoderUsageCredentials("first")}}}}
	upstream := &qoderUsageHTTPUpstreamStub{bodies: []string{`{"userType":"teams","userQuota":{"total":100,"used":1,"remaining":99}}`, `{"userType":"teams","userQuota":{"total":100,"used":2,"remaining":98}}`}}
	svc := &AccountUsageService{accountRepo: repo, cache: NewUsageCache(), httpUpstream: upstream}
	first, err := svc.GetUsage(context.Background(), 919)
	require.NoError(t, err)
	require.Equal(t, float64(1), first.QoderQuota.UserQuota.Used)
	repo.accounts[0].Credentials = qoderUsageCredentials("second")
	second, err := svc.GetUsage(context.Background(), 919)
	require.NoError(t, err)
	require.Equal(t, float64(2), second.QoderQuota.UserQuota.Used)
	require.Equal(t, int32(2), upstream.calls)
}
