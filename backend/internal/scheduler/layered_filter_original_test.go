//go:build unit

package scheduler

import (
	"testing"
	"time"

	"github.com/TokenFlux/TokenRouter/internal/routing/capability"
	"github.com/stretchr/testify/require"
)

func TestFilterByMinPriority(t *testing.T) {
	t.Run("empty slice", func(t *testing.T) {
		result := FilterByMinPriority(nil)
		require.Empty(t, result)
	})

	t.Run("single account", func(t *testing.T) {
		accounts := []BasicCandidate{
			{Account: &BasicAccount{ID: 1, Priority: 5}, Load: &AccountLoadInfo{}},
		}
		result := FilterByMinPriority(accounts)
		require.Len(t, result, 1)
		require.Equal(t, int64(1), result[0].Account.ID)
	})

	t.Run("multiple accounts same priority", func(t *testing.T) {
		accounts := []BasicCandidate{
			{Account: &BasicAccount{ID: 1, Priority: 3}, Load: &AccountLoadInfo{}},
			{Account: &BasicAccount{ID: 2, Priority: 3}, Load: &AccountLoadInfo{}},
			{Account: &BasicAccount{ID: 3, Priority: 3}, Load: &AccountLoadInfo{}},
		}
		result := FilterByMinPriority(accounts)
		require.Len(t, result, 3)
	})

	t.Run("filters to min priority only", func(t *testing.T) {
		accounts := []BasicCandidate{
			{Account: &BasicAccount{ID: 1, Priority: 5}, Load: &AccountLoadInfo{}},
			{Account: &BasicAccount{ID: 2, Priority: 1}, Load: &AccountLoadInfo{}},
			{Account: &BasicAccount{ID: 3, Priority: 3}, Load: &AccountLoadInfo{}},
			{Account: &BasicAccount{ID: 4, Priority: 1}, Load: &AccountLoadInfo{}},
		}
		result := FilterByMinPriority(accounts)
		require.Len(t, result, 2)
		require.Equal(t, int64(2), result[0].Account.ID)
		require.Equal(t, int64(4), result[1].Account.ID)
	})
}

func TestFilterByMinLoadRate(t *testing.T) {
	t.Run("empty slice", func(t *testing.T) {
		result := FilterByMinLoadRate(nil)
		require.Empty(t, result)
	})

	t.Run("single account", func(t *testing.T) {
		accounts := []BasicCandidate{
			{Account: &BasicAccount{ID: 1}, Load: &AccountLoadInfo{LoadRate: 50}},
		}
		result := FilterByMinLoadRate(accounts)
		require.Len(t, result, 1)
		require.Equal(t, int64(1), result[0].Account.ID)
	})

	t.Run("multiple accounts same load rate", func(t *testing.T) {
		accounts := []BasicCandidate{
			{Account: &BasicAccount{ID: 1}, Load: &AccountLoadInfo{LoadRate: 20}},
			{Account: &BasicAccount{ID: 2}, Load: &AccountLoadInfo{LoadRate: 20}},
			{Account: &BasicAccount{ID: 3}, Load: &AccountLoadInfo{LoadRate: 20}},
		}
		result := FilterByMinLoadRate(accounts)
		require.Len(t, result, 3)
	})

	t.Run("filters to min load rate only", func(t *testing.T) {
		accounts := []BasicCandidate{
			{Account: &BasicAccount{ID: 1}, Load: &AccountLoadInfo{LoadRate: 80}},
			{Account: &BasicAccount{ID: 2}, Load: &AccountLoadInfo{LoadRate: 10}},
			{Account: &BasicAccount{ID: 3}, Load: &AccountLoadInfo{LoadRate: 50}},
			{Account: &BasicAccount{ID: 4}, Load: &AccountLoadInfo{LoadRate: 10}},
		}
		result := FilterByMinLoadRate(accounts)
		require.Len(t, result, 2)
		require.Equal(t, int64(2), result[0].Account.ID)
		require.Equal(t, int64(4), result[1].Account.ID)
	})

	t.Run("zero load rate", func(t *testing.T) {
		accounts := []BasicCandidate{
			{Account: &BasicAccount{ID: 1}, Load: &AccountLoadInfo{LoadRate: 0}},
			{Account: &BasicAccount{ID: 2}, Load: &AccountLoadInfo{LoadRate: 50}},
			{Account: &BasicAccount{ID: 3}, Load: &AccountLoadInfo{LoadRate: 0}},
		}
		result := FilterByMinLoadRate(accounts)
		require.Len(t, result, 2)
		require.Equal(t, int64(1), result[0].Account.ID)
		require.Equal(t, int64(3), result[1].Account.ID)
	})
}

func TestSelectByLRU(t *testing.T) {
	now := time.Now()
	earlier := now.Add(-1 * time.Hour)
	muchEarlier := now.Add(-2 * time.Hour)

	t.Run("empty slice", func(t *testing.T) {
		result := SelectByLRU(nil, false)
		require.Nil(t, result)
	})

	t.Run("single account", func(t *testing.T) {
		accounts := []BasicCandidate{
			{Account: &BasicAccount{ID: 1, LastUsedAt: &now}, Load: &AccountLoadInfo{}},
		}
		result := SelectByLRU(accounts, false)
		require.NotNil(t, result)
		require.Equal(t, int64(1), result.Account.ID)
	})

	t.Run("selects least recently used", func(t *testing.T) {
		accounts := []BasicCandidate{
			{Account: &BasicAccount{ID: 1, LastUsedAt: &now}, Load: &AccountLoadInfo{}},
			{Account: &BasicAccount{ID: 2, LastUsedAt: &muchEarlier}, Load: &AccountLoadInfo{}},
			{Account: &BasicAccount{ID: 3, LastUsedAt: &earlier}, Load: &AccountLoadInfo{}},
		}
		result := SelectByLRU(accounts, false)
		require.NotNil(t, result)
		require.Equal(t, int64(2), result.Account.ID)
	})

	t.Run("nil LastUsedAt preferred over non-nil", func(t *testing.T) {
		accounts := []BasicCandidate{
			{Account: &BasicAccount{ID: 1, LastUsedAt: &now}, Load: &AccountLoadInfo{}},
			{Account: &BasicAccount{ID: 2, LastUsedAt: nil}, Load: &AccountLoadInfo{}},
			{Account: &BasicAccount{ID: 3, LastUsedAt: &earlier}, Load: &AccountLoadInfo{}},
		}
		result := SelectByLRU(accounts, false)
		require.NotNil(t, result)
		require.Equal(t, int64(2), result.Account.ID)
	})

	t.Run("multiple nil LastUsedAt random selection", func(t *testing.T) {
		accounts := []BasicCandidate{
			{Account: &BasicAccount{ID: 1, LastUsedAt: nil, Type: "session"}, Load: &AccountLoadInfo{}},
			{Account: &BasicAccount{ID: 2, LastUsedAt: nil, Type: "session"}, Load: &AccountLoadInfo{}},
			{Account: &BasicAccount{ID: 3, LastUsedAt: nil, Type: "session"}, Load: &AccountLoadInfo{}},
		}
		// 多次调用应该随机选择，验证结果都在候选范围内
		validIDs := map[int64]bool{1: true, 2: true, 3: true}
		for i := 0; i < 10; i++ {
			result := SelectByLRU(accounts, false)
			require.NotNil(t, result)
			require.True(t, validIDs[result.Account.ID], "selected ID should be one of the candidates")
		}
	})

	t.Run("multiple same LastUsedAt random selection", func(t *testing.T) {
		sameTime := now
		accounts := []BasicCandidate{
			{Account: &BasicAccount{ID: 1, LastUsedAt: &sameTime}, Load: &AccountLoadInfo{}},
			{Account: &BasicAccount{ID: 2, LastUsedAt: &sameTime}, Load: &AccountLoadInfo{}},
		}
		// 多次调用应该随机选择
		validIDs := map[int64]bool{1: true, 2: true}
		for i := 0; i < 10; i++ {
			result := SelectByLRU(accounts, false)
			require.NotNil(t, result)
			require.True(t, validIDs[result.Account.ID], "selected ID should be one of the candidates")
		}
	})

	t.Run("preferOAuth selects from OAuth accounts when multiple nil", func(t *testing.T) {
		accounts := []BasicCandidate{
			{Account: &BasicAccount{ID: 1, LastUsedAt: nil, Type: "session"}, Load: &AccountLoadInfo{}},
			{Account: &BasicAccount{ID: 2, LastUsedAt: nil, Type: capability.AccountTypeOAuth}, Load: &AccountLoadInfo{}},
			{Account: &BasicAccount{ID: 3, LastUsedAt: nil, Type: capability.AccountTypeOAuth}, Load: &AccountLoadInfo{}},
		}
		// preferOAuth 时，应该从 OAuth 类型中选择
		oauthIDs := map[int64]bool{2: true, 3: true}
		for i := 0; i < 10; i++ {
			result := SelectByLRU(accounts, true)
			require.NotNil(t, result)
			require.True(t, oauthIDs[result.Account.ID], "should select from OAuth accounts")
		}
	})

	t.Run("preferOAuth falls back to all when no OAuth", func(t *testing.T) {
		accounts := []BasicCandidate{
			{Account: &BasicAccount{ID: 1, LastUsedAt: nil, Type: "session"}, Load: &AccountLoadInfo{}},
			{Account: &BasicAccount{ID: 2, LastUsedAt: nil, Type: "session"}, Load: &AccountLoadInfo{}},
		}
		// 没有 OAuth 时，从所有候选中选择
		validIDs := map[int64]bool{1: true, 2: true}
		for i := 0; i < 10; i++ {
			result := SelectByLRU(accounts, true)
			require.NotNil(t, result)
			require.True(t, validIDs[result.Account.ID])
		}
	})

	t.Run("preferOAuth only affects same LastUsedAt accounts", func(t *testing.T) {
		accounts := []BasicCandidate{
			{Account: &BasicAccount{ID: 1, LastUsedAt: &earlier, Type: "session"}, Load: &AccountLoadInfo{}},
			{Account: &BasicAccount{ID: 2, LastUsedAt: &now, Type: capability.AccountTypeOAuth}, Load: &AccountLoadInfo{}},
		}
		result := SelectByLRU(accounts, true)
		require.NotNil(t, result)
		// 有不同 LastUsedAt 时，按时间选择最早的，不受 preferOAuth 影响
		require.Equal(t, int64(1), result.Account.ID)
	})
}

func TestLayeredFilterIntegration(t *testing.T) {
	now := time.Now()
	earlier := now.Add(-1 * time.Hour)
	muchEarlier := now.Add(-2 * time.Hour)

	t.Run("full layered selection", func(t *testing.T) {
		// 模拟真实场景：多个账号，不同优先级、负载率、最后使用时间
		accounts := []BasicCandidate{
			// 优先级 1，负载 50%
			{Account: &BasicAccount{ID: 1, Priority: 1, LastUsedAt: &now}, Load: &AccountLoadInfo{LoadRate: 50}},
			// 优先级 1，负载 20%（最低）
			{Account: &BasicAccount{ID: 2, Priority: 1, LastUsedAt: &earlier}, Load: &AccountLoadInfo{LoadRate: 20}},
			// 优先级 1，负载 20%（最低），更早使用
			{Account: &BasicAccount{ID: 3, Priority: 1, LastUsedAt: &muchEarlier}, Load: &AccountLoadInfo{LoadRate: 20}},
			// 优先级 2（较低优先）
			{Account: &BasicAccount{ID: 4, Priority: 2, LastUsedAt: &muchEarlier}, Load: &AccountLoadInfo{LoadRate: 0}},
		}

		// 1. 取优先级最小的集合 → ID: 1, 2, 3
		step1 := FilterByMinPriority(accounts)
		require.Len(t, step1, 3)

		// 2. 取负载率最低的集合 → ID: 2, 3
		step2 := FilterByMinLoadRate(step1)
		require.Len(t, step2, 2)

		// 3. LRU 选择 → ID: 3（muchEarlier 最早）
		selected := SelectByLRU(step2, false)
		require.NotNil(t, selected)
		require.Equal(t, int64(3), selected.Account.ID)
	})

	t.Run("all same priority and load rate", func(t *testing.T) {
		accounts := []BasicCandidate{
			{Account: &BasicAccount{ID: 1, Priority: 1, LastUsedAt: &now}, Load: &AccountLoadInfo{LoadRate: 50}},
			{Account: &BasicAccount{ID: 2, Priority: 1, LastUsedAt: &earlier}, Load: &AccountLoadInfo{LoadRate: 50}},
			{Account: &BasicAccount{ID: 3, Priority: 1, LastUsedAt: &muchEarlier}, Load: &AccountLoadInfo{LoadRate: 50}},
		}

		step1 := FilterByMinPriority(accounts)
		require.Len(t, step1, 3)

		step2 := FilterByMinLoadRate(step1)
		require.Len(t, step2, 3)

		// LRU 选择最早的
		selected := SelectByLRU(step2, false)
		require.NotNil(t, selected)
		require.Equal(t, int64(3), selected.Account.ID)
	})
}
