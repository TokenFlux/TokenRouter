package service

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
)

type groupCapacityAccountRepoStub struct {
	AccountRepository
	accounts []Account
}

func (s *groupCapacityAccountRepoStub) ListSchedulableByGroupID(ctx context.Context, groupID int64) ([]Account, error) {
	out := make([]Account, len(s.accounts))
	copy(out, s.accounts)
	return out, nil
}

type groupCapacitySettingsStub struct {
	settings OpsOpenAIAccountQuotaAutoPauseSettings
}

func (s groupCapacitySettingsStub) GetOpenAIQuotaAutoPauseSettings(ctx context.Context) OpsOpenAIAccountQuotaAutoPauseSettings {
	return s.settings
}

type groupCapacityConcurrencyCacheStub struct {
	ConcurrencyCache
	values map[int64]int
	seen   []int64
}

func (s *groupCapacityConcurrencyCacheStub) GetAccountConcurrencyBatch(ctx context.Context, accountIDs []int64) (map[int64]int, error) {
	s.seen = append([]int64(nil), accountIDs...)
	out := make(map[int64]int, len(accountIDs))
	for _, id := range accountIDs {
		out[id] = s.values[id]
	}
	return out, nil
}

func TestGroupCapacityService_ExcludesOpenAIQuotaAutoPausedAccounts(t *testing.T) {
	accounts := []Account{
		{
			ID:          1,
			Platform:    PlatformOpenAI,
			Type:        AccountTypeOAuth,
			Concurrency: 7,
			Extra: map[string]any{
				"codex_5h_used_percent": 96.0,
			},
		},
		{
			ID:          2,
			Platform:    PlatformOpenAI,
			Type:        AccountTypeOAuth,
			Concurrency: 3,
			Extra: map[string]any{
				"codex_5h_used_percent": 30.0,
			},
		},
	}
	concurrencyCache := &groupCapacityConcurrencyCacheStub{values: map[int64]int{1: 5, 2: 2}}
	svc := NewGroupCapacityService(
		&groupCapacityAccountRepoStub{accounts: accounts},
		nil,
		NewConcurrencyService(concurrencyCache),
		nil,
		nil,
		groupCapacitySettingsStub{settings: OpsOpenAIAccountQuotaAutoPauseSettings{DefaultThreshold5h: 0.95}},
	)

	capacity, err := svc.getGroupCapacity(context.Background(), 10)
	require.NoError(t, err)
	require.Equal(t, 3, capacity.ConcurrencyMax)
	require.Equal(t, 2, capacity.ConcurrencyUsed)
	require.Equal(t, []int64{2}, concurrencyCache.seen)
}
