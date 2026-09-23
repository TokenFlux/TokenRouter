//go:build unit

package account

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/TokenFlux/TokenRouter/internal/routing/capability"
	"github.com/stretchr/testify/require"
)

type grokFreeQuotaUsageRepoStub struct {
	mu      sync.Mutex
	stats   map[int64]*quotaTestStats
	err     error
	calls   int
	lastIDs []int64
	start   time.Time
}

func (r *grokFreeQuotaUsageRepoStub) GetAccountWindowStatsBatch(_ context.Context, accountIDs []int64, start time.Time) (map[int64]*quotaTestStats, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.calls++
	r.lastIDs = append([]int64(nil), accountIDs...)
	r.start = start
	if r.err != nil {
		return nil, r.err
	}
	result := make(map[int64]*quotaTestStats, len(accountIDs))
	for _, accountID := range accountIDs {
		if stats := r.stats[accountID]; stats != nil {
			copyStats := *stats
			result[accountID] = &copyStats
		}
	}
	return result, nil
}

type quotaTestStats struct{ Tokens int64 }

func grokFreeQuotaTestConfig() FreeQuotaOptions {
	return FreeQuotaOptions{Enabled: true, TokenLimit: 500_000, Percent: 95, WindowHours: 24, CacheSeconds: 60}
}

func TestFilterGrokFreeQuotaAccountsOnlyBlocksExplicitFreeOAuth(t *testing.T) {
	repo := &grokFreeQuotaUsageRepoStub{stats: map[int64]*quotaTestStats{
		1: {Tokens: 475_000}, // 95% of 500k
	}}
	scheduler := newQuotaTestRuntime(repo)
	t.Cleanup(func() { waitGrokFreeQuotaTestGate(t, scheduler.gate) })
	accounts := []Record{
		{ID: 1, Platform: capability.PlatformGrok, Type: capability.AccountTypeOAuth, Credentials: map[string]any{"subscription_tier": "FREE"}},
		{ID: 2, Platform: capability.PlatformGrok, Type: capability.AccountTypeOAuth, Credentials: map[string]any{"subscription_tier": "PRO"}},
		{ID: 3, Platform: capability.PlatformGrok, Type: capability.AccountTypeOAuth},
		{ID: 4, Platform: capability.PlatformGrok, Type: capability.AccountTypeAPIKey, Credentials: map[string]any{"subscription_tier": "FREE"}},
	}

	// 首次执行时缓存未命中并失败开放，同时安排后台刷新，不阻断账号。
	filtered := scheduler.filterGrokFreeQuotaAccounts(context.Background(), accounts)
	require.Equal(t, []int64{1, 2, 3, 4}, accountIDs(filtered), "miss fails open on hot path")

	require.Eventually(t, func() bool {
		repo.mu.Lock()
		defer repo.mu.Unlock()
		return repo.calls >= 1
	}, 2*time.Second, 10*time.Millisecond)
	waitGrokFreeQuotaTestGate(t, scheduler.gate)

	// 第二次执行使用已刷新缓存，并阻断超过门禁的免费 OAuth 账号。
	filtered = scheduler.filterGrokFreeQuotaAccounts(context.Background(), accounts)
	require.Equal(t, []int64{2, 3, 4}, accountIDs(filtered), "paid and unknown fail-open; API-key free marker is not gated")
	require.Equal(t, []int64{1}, repo.lastIDs, "paid, unknown, and API-key accounts must not enter the local free-tier query")
	require.WithinDuration(t, time.Now().UTC().Add(-24*time.Hour), repo.start, time.Second)
}

func TestFilterGrokFreeQuotaAccountsStatsFailureFailsOpen(t *testing.T) {
	repo := &grokFreeQuotaUsageRepoStub{err: errors.New("usage database unavailable")}
	scheduler := newQuotaTestRuntime(repo)
	t.Cleanup(func() { waitGrokFreeQuotaTestGate(t, scheduler.gate) })
	accounts := []Record{{
		ID: 1, Platform: capability.PlatformGrok, Type: capability.AccountTypeOAuth,
		Credentials: map[string]any{"subscription_tier": "free"},
	}}

	filtered := scheduler.filterGrokFreeQuotaAccounts(context.Background(), accounts)
	require.Equal(t, []int64{1}, accountIDs(filtered))
	require.Eventually(t, func() bool {
		repo.mu.Lock()
		defer repo.mu.Unlock()
		return repo.calls >= 1
	}, 2*time.Second, 10*time.Millisecond)
	waitGrokFreeQuotaTestGate(t, scheduler.gate)
	// 负缓存条目使后续热点调用保持失败开放，同时避免频繁刷新。
	filtered = scheduler.filterGrokFreeQuotaAccounts(context.Background(), accounts)
	require.Equal(t, []int64{1}, accountIDs(filtered))
	require.Equal(t, 1, repo.calls)
}

func TestFilterGrokFreeQuotaAccountsUnknownTierFailOpen(t *testing.T) {
	repo := &grokFreeQuotaUsageRepoStub{stats: map[int64]*quotaTestStats{
		1: {Tokens: 9_999_999},
	}}
	scheduler := newQuotaTestRuntime(repo)
	t.Cleanup(func() { waitGrokFreeQuotaTestGate(t, scheduler.gate) })
	accounts := []Record{
		{ID: 1, Platform: capability.PlatformGrok, Type: capability.AccountTypeOAuth},
		{ID: 2, Platform: capability.PlatformGrok, Type: capability.AccountTypeOAuth, Credentials: map[string]any{"subscription_tier": "unknown"}},
		{ID: 3, Platform: capability.PlatformGrok, Type: capability.AccountTypeOAuth, Extra: map[string]any{"subscription_tier": "pro"}},
	}

	filtered := scheduler.filterGrokFreeQuotaAccounts(context.Background(), accounts)
	require.Equal(t, []int64{1, 2, 3}, accountIDs(filtered))
	require.Zero(t, repo.calls, "unknown/paid tiers must not query free-quota stats")
}

func TestFilterGrokFreeQuotaAccountsRecoversAfterRollingUsageFalls(t *testing.T) {
	repo := &grokFreeQuotaUsageRepoStub{stats: map[int64]*quotaTestStats{
		1: {Tokens: 490_000},
	}}
	scheduler := newQuotaTestRuntime(repo)
	t.Cleanup(func() { waitGrokFreeQuotaTestGate(t, scheduler.gate) })
	accounts := []Record{{
		ID: 1, Platform: capability.PlatformGrok, Type: capability.AccountTypeOAuth,
		Credentials: map[string]any{"plan_type": "free"},
	}}

	// 未命中时失败开放，后台填充后阻断超过门禁的账号。
	require.Equal(t, []int64{1}, accountIDs(scheduler.filterGrokFreeQuotaAccounts(context.Background(), accounts)))
	require.Eventually(t, func() bool {
		filtered := scheduler.filterGrokFreeQuotaAccounts(context.Background(), accounts)
		return len(filtered) == 0
	}, 2*time.Second, 10*time.Millisecond)

	repo.mu.Lock()
	repo.stats[1] = &quotaTestStats{Tokens: 100_000}
	repo.mu.Unlock()
	// 新鲜的正缓存会持续执行软性门禁，直到 TTL 过期。
	require.Empty(t, scheduler.filterGrokFreeQuotaAccounts(context.Background(), accounts), "fresh cache keeps the soft-gate hold")

	// 使条目过期后，缓存未命中会失败开放，并使用恢复后的用量安排刷新。
	waitGrokFreeQuotaTestGate(t, scheduler.gate)
	repo.mu.Lock()
	callsBeforeExpire := repo.calls
	repo.mu.Unlock()
	scheduler.gate.cache.Store(int64(1), grokFreeQuotaGateCacheEntry{
		tokens: 490_000, checkedAt: time.Now().Add(-2 * time.Minute), known: true, // TTL=60s → stale
	})
	// 刷新进行期间热点路径保持失败开放。
	require.Equal(t, []int64{1}, accountIDs(scheduler.filterGrokFreeQuotaAccounts(context.Background(), accounts)))
	require.Eventually(t, func() bool {
		filtered := scheduler.filterGrokFreeQuotaAccounts(context.Background(), accounts)
		repo.mu.Lock()
		defer repo.mu.Unlock()
		return len(filtered) == 1 && filtered[0].ID == 1 &&
			repo.calls > callsBeforeExpire
	}, 2*time.Second, 10*time.Millisecond)
}

func TestResolveGrokFreeQuotaGateSettingsDefaultsToNinetyFivePercent(t *testing.T) {
	settings, ok := resolveGrokFreeQuotaGateSettings(grokFreeQuotaTestConfig())
	require.True(t, ok)
	require.Equal(t, int64(500_000), settings.limitTokens)
	require.Equal(t, int64(475_000), settings.gateTokens) // 95% of 500k
	require.Equal(t, 24*time.Hour, settings.window)
}

func TestIsExplicitGrokFreeOAuthAccount_OnlyExactFree(t *testing.T) {
	t.Parallel()
	require.False(t, IsExplicitGrokFreeOAuthAccount(nil))
	require.False(t, IsExplicitGrokFreeOAuthAccount(&Record{Platform: capability.PlatformGrok, Type: capability.AccountTypeAPIKey, Credentials: map[string]any{"subscription_tier": "free"}}))
	require.True(t, IsExplicitGrokFreeOAuthAccount(&Record{Platform: capability.PlatformGrok, Type: capability.AccountTypeOAuth, Credentials: map[string]any{"subscription_tier": "FREE"}}))
	require.True(t, IsExplicitGrokFreeOAuthAccount(&Record{Platform: capability.PlatformGrok, Type: capability.AccountTypeOAuth, Credentials: map[string]any{"plan_type": "free"}}))
	// basic 或推断出的免费状态不参与软性门禁，只有明确的 free 层参与。
	require.False(t, IsExplicitGrokFreeOAuthAccount(&Record{Platform: capability.PlatformGrok, Type: capability.AccountTypeOAuth, Credentials: map[string]any{"subscription_tier": "basic"}}))
	require.False(t, IsExplicitGrokFreeOAuthAccount(&Record{Platform: capability.PlatformGrok, Type: capability.AccountTypeOAuth}))
}

// 管理端 QueryQuota 与导入探测路径绝不调用 filterGrokFreeQuotaAccounts。
// 此测试记录并断言调度过滤器是唯一门禁入口。
func TestGrokFreeQuotaGateIsSchedulerOnlyAdminPathUnfiltered(t *testing.T) {
	// 构造管理端探测会检查的相同账号。GrokQuotaService.QueryQuota 与 GetUsage
	// 不会调用该过滤器；只通过调度器类型调用可确保免费账号超过软性门禁时，
	// 管理端流量仍不受阻断。
	// 基本校验：只有调度过滤器运行时才过滤超过门禁的免费账号。
	repo := &grokFreeQuotaUsageRepoStub{stats: map[int64]*quotaTestStats{
		9: {Tokens: 500_000},
	}}
	scheduler := newQuotaTestRuntime(repo)
	t.Cleanup(func() { waitGrokFreeQuotaTestGate(t, scheduler.gate) })
	overGate := Record{ID: 9, Platform: capability.PlatformGrok, Type: capability.AccountTypeOAuth, Credentials: map[string]any{"subscription_tier": "FREE"}}
	require.Eventually(t, func() bool {
		_ = scheduler.filterGrokFreeQuotaAccounts(context.Background(), []Record{overGate})
		return len(scheduler.filterGrokFreeQuotaAccounts(context.Background(), []Record{overGate})) == 0
	}, 2*time.Second, 10*time.Millisecond)
	// 未经过调度过滤器时，账号对象本身保持不变。
	require.True(t, IsExplicitGrokFreeOAuthAccount(&overGate))
	require.Equal(t, int64(9), overGate.ID)
}

func TestSweepGrokFreeQuotaGateCacheDropsStaleEntries(t *testing.T) {
	now := time.Now().UTC()
	cacheTTL := 5 * time.Second
	// maxAge 下限为 grokFreeQuotaGateCacheMinSweepAge，而不是 cacheTTL 的 20 倍。
	var cache sync.Map
	cache.Store(int64(1), grokFreeQuotaGateCacheEntry{tokens: 10, checkedAt: now, known: true})
	cache.Store(int64(2), grokFreeQuotaGateCacheEntry{tokens: 20, checkedAt: now.Add(-time.Minute), known: true})
	cache.Store(int64(3), grokFreeQuotaGateCacheEntry{tokens: 30, checkedAt: now.Add(-time.Hour), known: true})
	cache.Store(int64(4), "not-an-entry")

	sweepGrokFreeQuotaGateCache(&cache, now, cacheTTL)

	remaining := make([]int64, 0, 4)
	cache.Range(func(key, _ any) bool {
		if id, ok := key.(int64); ok {
			remaining = append(remaining, id)
		}
		return true
	})
	require.ElementsMatch(t, []int64{1, 2}, remaining)

	// 禁用缓存（TTL 为零）表示调用方从未填充缓存，应保持不变。
	var untouched sync.Map
	untouched.Store(int64(7), grokFreeQuotaGateCacheEntry{checkedAt: now.Add(-time.Hour), known: true})
	sweepGrokFreeQuotaGateCache(&untouched, now, 0)
	_, stillThere := untouched.Load(int64(7))
	require.True(t, stillThere)
}

func TestFilterGrokFreeQuotaAccountsEvictsDepartedAccounts(t *testing.T) {
	repo := &grokFreeQuotaUsageRepoStub{stats: map[int64]*quotaTestStats{
		1: {Tokens: 1_000},
	}}
	runtime := newQuotaTestRuntime(repo)
	cache := &runtime.gate.cache
	// 账号 99 很久以前参与过调度，当前不再出现在任何批次中。
	// 查询其他账号后，其条目不得继续保留。
	cache.Store(int64(99), grokFreeQuotaGateCacheEntry{tokens: 5, checkedAt: time.Now().UTC().Add(-2 * time.Hour), known: true})

	accounts := []Record{
		{ID: 1, Platform: capability.PlatformGrok, Type: capability.AccountTypeOAuth, Credentials: map[string]any{"subscription_tier": "FREE"}},
	}
	// 首次调用会安排异步刷新，此时清理过程可能尚未完成。
	_ = runtime.filterGrokFreeQuotaAccounts(context.Background(), accounts)
	require.Eventually(t, func() bool {
		_, departedStillCached := cache.Load(int64(99))
		_, freshCached := cache.Load(int64(1))
		return !departedStillCached && freshCached
	}, 2*time.Second, 10*time.Millisecond)
	filtered := runtime.filterGrokFreeQuotaAccounts(context.Background(), accounts)
	require.Equal(t, []int64{1}, accountIDs(filtered))
}

func accountIDs(accounts []Record) []int64 {
	ids := make([]int64, 0, len(accounts))
	for i := range accounts {
		ids = append(ids, accounts[i].ID)
	}
	return ids
}

// 夹具只将账号资格投影给真实门禁，并按原顺序还原结果。
type quotaTestRuntime struct{ gate *FreeQuotaGate }

func newQuotaTestRuntime(repo *grokFreeQuotaUsageRepoStub) *quotaTestRuntime {
	return &quotaTestRuntime{gate: NewFreeQuotaGate(grokFreeQuotaTestConfig, func(ctx context.Context, ids []int64, start time.Time) (map[int64]int64, error) {
		stats, err := repo.GetAccountWindowStatsBatch(ctx, ids, start)
		result := make(map[int64]int64, len(stats))
		for id, v := range stats {
			if v != nil {
				result[id] = v.Tokens
			}
		}
		return result, err
	}, func(_ string, fn func()) bool { go fn(); return true }, time.Now, nil, nil)}
}
func (r *quotaTestRuntime) filterGrokFreeQuotaAccounts(_ context.Context, values []Record) []Record {
	candidates := make([]FreeQuotaCandidate, len(values))
	for i := range values {
		candidates[i] = FreeQuotaCandidate{ID: values[i].ID, Eligible: IsExplicitGrokFreeOAuthAccount(&values[i])}
	}
	blocked := r.gate.Blocked(candidates)
	result := make([]Record, 0, len(values))
	for i, c := range candidates {
		if !c.Eligible || !blocked[c.ID] {
			result = append(result, values[i])
		}
	}
	return result
}

// 修改缓存前等待原生刷新退出，避免测试自身与后台状态竞争。
func waitGrokFreeQuotaTestGate(t *testing.T, gate *FreeQuotaGate) {
	t.Helper()
	require.Eventually(t, func() bool {
		empty := true
		gate.inFlight.Range(func(_, _ any) bool { empty = false; return false })
		return empty
	}, 2*time.Second, time.Millisecond)
}
