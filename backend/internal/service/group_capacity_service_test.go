package service

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

type batchCapacityAccountRepoStub struct {
	AccountRepository
	calls            int
	listByGroupCalls int
	accountsByGroup  map[int64][]GroupCapacityAccount
}

func (s *batchCapacityAccountRepoStub) ListGroupCapacityAccounts(ctx context.Context, groupIDs []int64) (map[int64][]GroupCapacityAccount, error) {
	s.calls++
	out := make(map[int64][]GroupCapacityAccount, len(s.accountsByGroup))
	for _, groupID := range groupIDs {
		if accounts, ok := s.accountsByGroup[groupID]; ok {
			out[groupID] = append([]GroupCapacityAccount(nil), accounts...)
		}
	}
	return out, nil
}

func (s *batchCapacityAccountRepoStub) ListSchedulableByGroupID(ctx context.Context, groupID int64) ([]Account, error) {
	s.listByGroupCalls++
	return nil, nil
}

type capacityConcurrencyCacheStub struct {
	values map[int64]int
	seen   []int64
}

func (c *capacityConcurrencyCacheStub) AcquireAccountSlot(context.Context, int64, int, string) (bool, error) {
	return true, nil
}
func (c *capacityConcurrencyCacheStub) ReleaseAccountSlot(context.Context, int64, string) error {
	return nil
}
func (c *capacityConcurrencyCacheStub) GetAccountConcurrency(context.Context, int64) (int, error) {
	return 0, nil
}
func (c *capacityConcurrencyCacheStub) GetAccountConcurrencyBatch(_ context.Context, accountIDs []int64) (map[int64]int, error) {
	c.seen = append([]int64(nil), accountIDs...)
	out := make(map[int64]int, len(accountIDs))
	for _, id := range accountIDs {
		out[id] = c.values[id]
	}
	return out, nil
}
func (c *capacityConcurrencyCacheStub) IncrementAccountWaitCount(context.Context, int64, int) (bool, error) {
	return true, nil
}
func (c *capacityConcurrencyCacheStub) DecrementAccountWaitCount(context.Context, int64) error {
	return nil
}
func (c *capacityConcurrencyCacheStub) GetAccountWaitingCount(context.Context, int64) (int, error) {
	return 0, nil
}
func (c *capacityConcurrencyCacheStub) AcquireUserSlot(context.Context, int64, int, string) (bool, error) {
	return true, nil
}
func (c *capacityConcurrencyCacheStub) ReleaseUserSlot(context.Context, int64, string) error {
	return nil
}
func (c *capacityConcurrencyCacheStub) GetUserConcurrency(context.Context, int64) (int, error) {
	return 0, nil
}
func (c *capacityConcurrencyCacheStub) IncrementWaitCount(context.Context, int64, int) (bool, error) {
	return true, nil
}
func (c *capacityConcurrencyCacheStub) DecrementWaitCount(context.Context, int64) error { return nil }
func (c *capacityConcurrencyCacheStub) GetAccountsLoadBatch(context.Context, []AccountWithConcurrency) (map[int64]*AccountLoadInfo, error) {
	return nil, nil
}
func (c *capacityConcurrencyCacheStub) GetUsersLoadBatch(context.Context, []UserWithConcurrency) (map[int64]*UserLoadInfo, error) {
	return nil, nil
}
func (c *capacityConcurrencyCacheStub) CleanupExpiredAccountSlots(context.Context, int64) error {
	return nil
}
func (c *capacityConcurrencyCacheStub) CleanupStaleProcessSlots(context.Context, string) error {
	return nil
}

type capacitySessionCacheStub struct {
	values   map[int64]int
	timeouts map[int64]time.Duration
}

func (s *capacitySessionCacheStub) RegisterSession(context.Context, int64, string, int, time.Duration) (bool, error) {
	return true, nil
}
func (s *capacitySessionCacheStub) RefreshSession(context.Context, int64, string, time.Duration) error {
	return nil
}
func (s *capacitySessionCacheStub) ReleaseSession(context.Context, int64, string) error { return nil }
func (s *capacitySessionCacheStub) GetActiveSessionCount(context.Context, int64) (int, error) {
	return 0, nil
}
func (s *capacitySessionCacheStub) GetActiveSessionCountBatch(_ context.Context, accountIDs []int64, idleTimeouts map[int64]time.Duration) (map[int64]int, error) {
	s.timeouts = make(map[int64]time.Duration, len(idleTimeouts))
	for k, v := range idleTimeouts {
		s.timeouts[k] = v
	}
	out := make(map[int64]int, len(accountIDs))
	for _, id := range accountIDs {
		out[id] = s.values[id]
	}
	return out, nil
}
func (s *capacitySessionCacheStub) IsSessionActive(context.Context, int64, string) (bool, error) {
	return false, nil
}
func (s *capacitySessionCacheStub) GetWindowCost(context.Context, int64) (float64, bool, error) {
	return 0, false, nil
}
func (s *capacitySessionCacheStub) SetWindowCost(context.Context, int64, float64) error { return nil }
func (s *capacitySessionCacheStub) GetWindowCostBatch(context.Context, []int64) (map[int64]float64, error) {
	return nil, nil
}

type capacityRPMCacheStub struct {
	values map[int64]int
	seen   []int64
}

func (r *capacityRPMCacheStub) IncrementRPM(context.Context, int64) (int, error) { return 0, nil }
func (r *capacityRPMCacheStub) GetRPM(context.Context, int64) (int, error)       { return 0, nil }
func (r *capacityRPMCacheStub) GetRPMBatch(_ context.Context, accountIDs []int64) (map[int64]int, error) {
	r.seen = append([]int64(nil), accountIDs...)
	out := make(map[int64]int, len(accountIDs))
	for _, id := range accountIDs {
		out[id] = r.values[id]
	}
	return out, nil
}

func TestGroupCapacityByIDsUsesBatchCapacityAccountsWhenAvailable(t *testing.T) {
	repo := &batchCapacityAccountRepoStub{accountsByGroup: map[int64][]GroupCapacityAccount{
		10: {
			{ID: 1, Concurrency: 3, MaxSessions: 2, SessionIdleTimeoutMinutes: 7, BaseRPM: 100},
			{ID: 2, Concurrency: 4, MaxSessions: 0, SessionIdleTimeoutMinutes: 5, BaseRPM: 0},
		},
		20: {
			{ID: 3, Concurrency: 5, MaxSessions: 1, SessionIdleTimeoutMinutes: 0, BaseRPM: 60},
		},
	}}
	concurrencyCache := &capacityConcurrencyCacheStub{values: map[int64]int{1: 1, 2: 2, 3: 3}}
	concurrencySvc := NewConcurrencyService(concurrencyCache)
	sessionCache := &capacitySessionCacheStub{values: map[int64]int{1: 1, 3: 1}}
	rpmCache := &capacityRPMCacheStub{values: map[int64]int{1: 9, 3: 6}}
	svc := NewGroupCapacityService(repo, nil, concurrencySvc, sessionCache, rpmCache)

	got, err := svc.GetGroupCapacityByIDs(context.Background(), []int64{10, 20, 10, 0})
	require.NoError(t, err)
	require.Equal(t, 1, repo.calls)
	require.Zero(t, repo.listByGroupCalls)

	require.Equal(t, GroupCapacitySummary{GroupID: 10, ConcurrencyUsed: 3, ConcurrencyMax: 7, SessionsUsed: 1, SessionsMax: 2, RPMUsed: 9, RPMMax: 100}, got[10])
	require.Equal(t, GroupCapacitySummary{GroupID: 20, ConcurrencyUsed: 3, ConcurrencyMax: 5, SessionsUsed: 1, SessionsMax: 1, RPMUsed: 6, RPMMax: 60}, got[20])
	require.ElementsMatch(t, []int64{1, 2, 3}, concurrencyCache.seen)
	require.ElementsMatch(t, []int64{1, 2, 3}, rpmCache.seen)
	require.Equal(t, 7*time.Minute, sessionCache.timeouts[1])
	require.Equal(t, 5*time.Minute, sessionCache.timeouts[3])
}
