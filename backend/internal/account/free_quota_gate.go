package account

import (
	"context"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

// FreeQuotaOptions 是免费层调度门禁的显式配置投影。
type FreeQuotaOptions struct {
	Enabled      bool
	TokenLimit   int64
	Percent      int
	WindowHours  int
	CacheSeconds int
}

// FreeQuotaCandidate 不携带凭据，只表达已判定的资格和账号 ID。
type FreeQuotaCandidate struct {
	ID       int64
	Eligible bool
}

// FreeQuotaGate 拥有一个选择作用域的缓存与刷新去重；后台任务由应用管理。
type FreeQuotaGate struct {
	cache      sync.Map
	inFlight   sync.Map
	options    func() FreeQuotaOptions
	load       func(context.Context, []int64, time.Time) (map[int64]int64, error)
	background func(string, func()) bool
	now        func() time.Time
	observe    func(bool, string, ...any)
	metrics    *FreeQuotaMetrics
}

// NewFreeQuotaGate 不启动任务，保留按调用时点取得配置与异步回源的语义。
func NewFreeQuotaGate(options func() FreeQuotaOptions, load func(context.Context, []int64, time.Time) (map[int64]int64, error), background func(string, func()) bool, now func() time.Time, observe func(bool, string, ...any), metrics *FreeQuotaMetrics) *FreeQuotaGate {
	if now == nil {
		now = time.Now
	}
	if metrics == nil {
		metrics = &FreeQuotaMetrics{}
	}
	if observe == nil {
		observe = func(bool, string, ...any) {}
	}
	if options == nil {
		options = func() FreeQuotaOptions { return FreeQuotaOptions{} }
	}
	return &FreeQuotaGate{options: options, load: load, background: background, now: now, observe: observe, metrics: metrics}
}

type grokFreeQuotaGateSettings struct {
	limitTokens int64
	gateTokens  int64
	window      time.Duration
	cacheTTL    time.Duration
}

type grokFreeQuotaGateCacheEntry struct {
	tokens    int64
	checkedAt time.Time
	known     bool
}

// FreeQuotaMetrics 保留各选择作用域共用的累计观测。
type FreeQuotaMetrics struct {
	QueryFailureTotal atomic.Int64
	BlockedTotal      atomic.Int64
}

func resolveGrokFreeQuotaGateSettings(cfg FreeQuotaOptions) (grokFreeQuotaGateSettings, bool) {
	if !cfg.Enabled {
		return grokFreeQuotaGateSettings{}, false
	}
	limit := cfg.TokenLimit
	percent := cfg.Percent
	windowHours := cfg.WindowHours
	cacheSeconds := cfg.CacheSeconds
	if limit <= 0 || percent < 1 || percent > 100 || windowHours <= 0 || cacheSeconds < 0 {
		return grokFreeQuotaGateSettings{}, false
	}
	gate := calculateGrokFreeQuotaSoftGateTokens(limit, percent)
	if gate <= 0 {
		return grokFreeQuotaGateSettings{}, false
	}
	return grokFreeQuotaGateSettings{
		limitTokens: limit,
		gateTokens:  gate,
		window:      time.Duration(windowHours) * time.Hour,
		cacheTTL:    time.Duration(cacheSeconds) * time.Second,
	}, true
}

func calculateGrokFreeQuotaSoftGateTokens(limit int64, percent int) int64 {
	if limit <= 0 || percent <= 0 {
		return 0
	}
	return (limit/100)*int64(percent) + (limit%100)*int64(percent)/100
}

// IsExplicitGrokFreeOAuthAccount 判断免费层软门禁是否适用；只有凭据或 extra 中
// subscription_tier/plan_type 明确等于 free 的 OAuth 账号命中，推断免费、basic 或空套餐均不命中。
func IsExplicitGrokFreeOAuthAccount(account *Record) bool {
	if account == nil || !account.IsGrokOAuth() {
		return false
	}
	for _, tier := range []string{
		account.GetCredential("subscription_tier"),
		account.GetCredential("plan_type"),
		account.GetExtraString("subscription_tier"),
		account.GetExtraString("plan_type"),
	} {
		if strings.EqualFold(strings.TrimSpace(tier), "free") {
			return true
		}
	}
	return false
}

// Blocked 返回已知超限账号；未命中与失败保持故障放行，并在后台加载。
func (g *FreeQuotaGate) Blocked(accounts []FreeQuotaCandidate) map[int64]bool {
	if g == nil {
		return nil
	}
	settings, enabled := resolveGrokFreeQuotaGateSettings(g.options())
	if !enabled || len(accounts) == 0 || g.load == nil {
		return nil
	}
	now := g.now().UTC()
	tokensByID := make(map[int64]int64)
	missingIDs := make([]int64, 0, len(accounts))
	seenMissing := make(map[int64]struct{})
	for i := range accounts {
		account := &accounts[i]
		if !account.Eligible || account.ID <= 0 {
			continue
		}
		if cached, ok := g.cache.Load(account.ID); ok {
			entry, valid := cached.(grokFreeQuotaGateCacheEntry)
			if valid {
				age := now.Sub(entry.checkedAt)
				// cacheTTL 为 0 表示已知条目不过期；首次未命中仍放行并安排刷新。
				fresh := settings.cacheTTL <= 0 || (age >= 0 && age < settings.cacheTTL)
				if fresh {
					if entry.known {
						tokensByID[account.ID] = entry.tokens
					}
					continue
				}
			}
		}
		// 缓存未命中或过期时本次请求放行，并异步刷新。
		if _, exists := seenMissing[account.ID]; !exists {
			seenMissing[account.ID] = struct{}{}
			missingIDs = append(missingIDs, account.ID)
		}
	}

	if len(missingIDs) > 0 {
		g.refresh(settings, missingIDs)
	}

	blocked := make(map[int64]bool)
	for id, tokens := range tokensByID {
		if tokens >= settings.gateTokens {
			blocked[id] = true
		}
	}
	return blocked
}

// refresh 合并同一缓存作用域的在途加载，不改变查询的独立 context。
func (g *FreeQuotaGate) refresh(settings grokFreeQuotaGateSettings, accountIDs []int64) {
	toFetch := make([]int64, 0, len(accountIDs))
	for _, id := range accountIDs {
		if _, loaded := g.inFlight.LoadOrStore(id, struct{}{}); !loaded {
			toFetch = append(toFetch, id)
		}
	}
	if len(toFetch) == 0 {
		return
	}

	window := settings.window
	gateTokens := settings.gateTokens
	limitTokens := settings.limitTokens
	cacheTTL := settings.cacheTTL
	release := func() {
		for _, id := range toFetch {
			g.inFlight.Delete(id)
		}
	}
	// 任务接入应用完成屏障，保留原独立查询 context。
	if !g.background("account/free_quota_gate.go:stats_refresh", func() {
		defer release()
		now := g.now().UTC()
		statsByID, err := g.load(context.Background(), toFetch, now.Add(-window))
		if err != nil {
			g.metrics.QueryFailureTotal.Add(1)
			// 保存负缓存防止热路径反复查询；known=false 会持续放行，直到成功刷新。
			for _, accountID := range toFetch {
				g.cache.Store(accountID, grokFreeQuotaGateCacheEntry{checkedAt: now})
			}
			g.observe(true, "grok_free_quota_soft_gate_stats_failed",
				"account_count", len(toFetch),
				"window_hours", window.Hours(),
				"error", err)
			sweepGrokFreeQuotaGateCache(&g.cache, now, cacheTTL)
			return
		}
		for _, accountID := range toFetch {
			tokens := int64(0)
			if stats := statsByID[accountID]; stats > 0 {
				tokens = stats
			}
			g.cache.Store(accountID, grokFreeQuotaGateCacheEntry{tokens: tokens, checkedAt: now, known: true})
			if tokens >= gateTokens {
				g.metrics.BlockedTotal.Add(1)
				g.observe(false, "grok_free_quota_soft_gate_blocked",
					"account_id", accountID,
					"tokens", tokens,
					"gate_tokens", gateTokens,
					"limit_tokens", limitTokens,
					"window_hours", window.Hours())
			}
		}
		sweepGrokFreeQuotaGateCache(&g.cache, now, cacheTTL)
	}) {
		release()
	}
}

// grokFreeQuotaGateCacheMinSweepAge 设置最小清理年龄，避免极短 TTL 导致每请求重查。
const grokFreeQuotaGateCacheMinSweepAge = 5 * time.Minute

// sweepGrokFreeQuotaGateCache 清理远超 TTL 的条目，避免已删除或离开免费层的账号常驻进程内存；
// 仍活跃的账号会在下次未命中时重新填充。
func sweepGrokFreeQuotaGateCache(cache *sync.Map, now time.Time, cacheTTL time.Duration) {
	if cache == nil || cacheTTL <= 0 {
		return
	}
	maxAge := cacheTTL * 20
	if maxAge < grokFreeQuotaGateCacheMinSweepAge {
		maxAge = grokFreeQuotaGateCacheMinSweepAge
	}
	cache.Range(func(key, value any) bool {
		entry, ok := value.(grokFreeQuotaGateCacheEntry)
		if !ok || now.Sub(entry.checkedAt) > maxAge {
			cache.Delete(key)
		}
		return true
	})
}
