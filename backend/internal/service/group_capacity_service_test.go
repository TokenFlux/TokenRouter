package service

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

type groupCapacityAccountRepoStub struct {
	AccountRepository
	accounts  []Account
	rows      []GroupAccountCapacityRow
	requested []int64
}

func (s *groupCapacityAccountRepoStub) ListSchedulableByGroupID(ctx context.Context, groupID int64) ([]Account, error) {
	out := make([]Account, len(s.accounts))
	copy(out, s.accounts)
	return out, nil
}

func (s *groupCapacityAccountRepoStub) ListSchedulableCapacityByGroupIDs(_ context.Context, groupIDs []int64) ([]GroupAccountCapacityRow, error) {
	s.requested = append([]int64(nil), groupIDs...)
	return append([]GroupAccountCapacityRow(nil), s.rows...), nil
}

type groupCapacitySettingsStub struct {
	settings OpsOpenAIAccountQuotaAutoPauseSettings
}

func (s groupCapacitySettingsStub) GetOpenAIQuotaAutoPauseSettings(ctx context.Context) OpsOpenAIAccountQuotaAutoPauseSettings {
	return s.settings
}

type groupCapacityGroupRepoStub struct {
	GroupRepository
	groupIDs  []int64
	listCalls int
}

func (s *groupCapacityGroupRepoStub) ListActiveIDs(context.Context) ([]int64, error) {
	s.listCalls++
	return append([]int64(nil), s.groupIDs...), nil
}

type groupCapacityConcurrencyCacheStub struct {
	ConcurrencyCache
	counts    map[int64]int
	requested []int64
}

func (s *groupCapacityConcurrencyCacheStub) GetAccountConcurrencyBatch(_ context.Context, accountIDs []int64) (map[int64]int, error) {
	s.requested = append([]int64(nil), accountIDs...)
	out := make(map[int64]int, len(accountIDs))
	for _, id := range accountIDs {
		out[id] = s.counts[id]
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
	concurrencyCache := &groupCapacityConcurrencyCacheStub{counts: map[int64]int{1: 5, 2: 2}}
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
	require.Equal(t, []int64{2}, concurrencyCache.requested)
}

func TestGetAllGroupCapacityBatchExcludesOpenAIQuotaAutoPausedAccounts(t *testing.T) {
	accountRepo := &groupCapacityAccountRepoStub{rows: []GroupAccountCapacityRow{
		{
			GroupID:     10,
			AccountID:   1,
			Platform:    PlatformOpenAI,
			Concurrency: 7,
			Extra:       map[string]any{"codex_5h_used_percent": 96.0},
		},
		{
			GroupID:     10,
			AccountID:   2,
			Platform:    PlatformOpenAI,
			Concurrency: 3,
			Extra:       map[string]any{"codex_5h_used_percent": 30.0},
		},
	}}
	groupRepo := &groupCapacityGroupRepoStub{groupIDs: []int64{10}}
	concurrencyCache := &groupCapacityConcurrencyCacheStub{counts: map[int64]int{1: 5, 2: 2}}
	svc := NewGroupCapacityService(
		accountRepo,
		groupRepo,
		NewConcurrencyService(concurrencyCache),
		nil,
		nil,
		groupCapacitySettingsStub{settings: OpsOpenAIAccountQuotaAutoPauseSettings{DefaultThreshold5h: 0.95}},
	)

	results, err := svc.GetAllGroupCapacity(context.Background())
	require.NoError(t, err)
	require.Equal(t, []GroupCapacitySummary{{
		GroupID:         10,
		ConcurrencyUsed: 2,
		ConcurrencyMax:  3,
	}}, results)
	require.Equal(t, []int64{2}, concurrencyCache.requested)
}

type groupCapacitySessionCacheStub struct {
	SessionLimitCache
	counts       map[int64]int
	requested    []int64
	idleTimeouts map[int64]time.Duration
}

func (s *groupCapacitySessionCacheStub) GetActiveSessionCountBatch(_ context.Context, accountIDs []int64, idleTimeouts map[int64]time.Duration) (map[int64]int, error) {
	s.requested = append([]int64(nil), accountIDs...)
	s.idleTimeouts = make(map[int64]time.Duration, len(idleTimeouts))
	for id, timeout := range idleTimeouts {
		s.idleTimeouts[id] = timeout
	}
	out := make(map[int64]int, len(accountIDs))
	for _, id := range accountIDs {
		out[id] = s.counts[id]
	}
	return out, nil
}

type groupCapacityRPMCacheStub struct {
	RPMCache
	counts    map[int64]int
	requested []int64
}

func (s *groupCapacityRPMCacheStub) GetRPMBatch(_ context.Context, accountIDs []int64) (map[int64]int, error) {
	s.requested = append([]int64(nil), accountIDs...)
	out := make(map[int64]int, len(accountIDs))
	for _, id := range accountIDs {
		out[id] = s.counts[id]
	}
	return out, nil
}

func TestGetAllGroupCapacityBatchAggregatesRuntimeAndLimits(t *testing.T) {
	accountRepo := &groupCapacityAccountRepoStub{
		rows: []GroupAccountCapacityRow{
			{
				GroupID:     10,
				AccountID:   1,
				Concurrency: 2,
				Extra: map[string]any{
					"max_sessions":                 3,
					"session_idle_timeout_minutes": 7,
					"base_rpm":                     11,
				},
			},
			{
				GroupID:     20,
				AccountID:   1,
				Concurrency: 2,
				Extra: map[string]any{
					"max_sessions":                 3,
					"session_idle_timeout_minutes": 7,
					"base_rpm":                     11,
				},
			},
			{
				GroupID:     20,
				AccountID:   2,
				Concurrency: 4,
				Extra: map[string]any{
					"max_sessions":                 1,
					"session_idle_timeout_minutes": 9,
					"base_rpm":                     13,
				},
			},
		},
	}
	groupRepo := &groupCapacityGroupRepoStub{groupIDs: []int64{10, 20}}
	concurrencyCache := &groupCapacityConcurrencyCacheStub{counts: map[int64]int{1: 1, 2: 2}}
	sessionCache := &groupCapacitySessionCacheStub{counts: map[int64]int{1: 2, 2: 1}}
	rpmCache := &groupCapacityRPMCacheStub{counts: map[int64]int{1: 5, 2: 7}}
	svc := NewGroupCapacityService(
		accountRepo,
		groupRepo,
		NewConcurrencyService(concurrencyCache),
		sessionCache,
		rpmCache,
		nil,
	)

	results, err := svc.GetAllGroupCapacity(context.Background())
	require.NoError(t, err)

	require.Equal(t, 1, groupRepo.listCalls)
	require.Equal(t, []int64{10, 20}, accountRepo.requested)
	require.Equal(t, []int64{1, 2}, concurrencyCache.requested)
	require.ElementsMatch(t, []int64{1, 2}, sessionCache.requested)
	require.ElementsMatch(t, []int64{1, 2}, rpmCache.requested)
	require.Equal(t, 7*time.Minute, sessionCache.idleTimeouts[1])
	require.Equal(t, 9*time.Minute, sessionCache.idleTimeouts[2])

	require.Equal(t, []GroupCapacitySummary{
		{
			GroupID:         10,
			ConcurrencyUsed: 1,
			ConcurrencyMax:  2,
			SessionsUsed:    2,
			SessionsMax:     3,
			RPMUsed:         5,
			RPMMax:          11,
		},
		{
			GroupID:         20,
			ConcurrencyUsed: 3,
			ConcurrencyMax:  6,
			SessionsUsed:    3,
			SessionsMax:     4,
			RPMUsed:         12,
			RPMMax:          24,
		},
	}, results)
}

func TestGetAllGroupCapacityBatchKeepsEmptyGroupRows(t *testing.T) {
	accountRepo := &groupCapacityAccountRepoStub{
		rows: []GroupAccountCapacityRow{
			{GroupID: 20, AccountID: 2, Concurrency: 4},
		},
	}
	groupRepo := &groupCapacityGroupRepoStub{groupIDs: []int64{10, 20}}
	svc := NewGroupCapacityService(accountRepo, groupRepo, nil, nil, nil, nil)

	results, err := svc.GetAllGroupCapacity(context.Background())
	require.NoError(t, err)

	require.Equal(t, []GroupCapacitySummary{
		{GroupID: 10},
		{GroupID: 20, ConcurrencyMax: 4},
	}, results)
}

func TestGetGroupCapacityByIDsUsesBatchPathAndDeduplicatesIDs(t *testing.T) {
	accountRepo := &groupCapacityAccountRepoStub{rows: []GroupAccountCapacityRow{
		{GroupID: 20, AccountID: 2, Concurrency: 4},
	}}
	svc := NewGroupCapacityService(accountRepo, nil, nil, nil, nil, nil)

	results, err := svc.GetGroupCapacityByIDs(context.Background(), []int64{20, 10, 20, 0, -1})
	require.NoError(t, err)
	require.Equal(t, []int64{20, 10}, accountRepo.requested)
	require.Equal(t, map[int64]GroupCapacitySummary{
		10: {GroupID: 10},
		20: {GroupID: 20, ConcurrencyMax: 4},
	}, results)
}
