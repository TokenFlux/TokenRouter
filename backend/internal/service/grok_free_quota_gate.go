package service

import (
	"context"
	"log/slog"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/BrandonVee/TokenRouter/internal/config"
	"github.com/BrandonVee/TokenRouter/internal/pkg/usagestats"
)

// Grok OAuth 调度使用本地免费层软门禁。
//
// 配置键均位于 gateway.grok：开关默认开启，名义额度默认 500000 token，
// 停调阈值默认 95%，滚动窗口默认 24 小时，统计缓存默认 60 秒。
//
// 软门禁只作用于 subscription_tier 或 plan_type 明确为 free 的 OAuth 账号；
// 媒体与缓存的免费层识别使用更宽松的 isKnownGrokFreeAccount，管理端额度查询和导入探测不调用此过滤器。

type GrokFreeQuotaPolicy struct {
	Enabled         bool  `json:"enabled"`
	TokenLimit      int64 `json:"token_limit"`
	SoftGatePercent int   `json:"soft_gate_percent"`
	SoftGateTokens  int64 `json:"soft_gate_tokens"`
	WindowHours     int   `json:"window_hours"`
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

var grokFreeQuotaGateQueryFailureTotal atomic.Int64
var grokFreeQuotaGateBlockedTotal atomic.Int64

func resolveGrokFreeQuotaGateSettings(cfg *config.Config) (grokFreeQuotaGateSettings, bool) {
	if cfg == nil || !cfg.Gateway.Grok.FreeQuotaSoftGateEnabled {
		return grokFreeQuotaGateSettings{}, false
	}
	limit := cfg.Gateway.Grok.FreeQuotaTokenLimit
	percent := cfg.Gateway.Grok.FreeQuotaSoftGatePercent
	windowHours := cfg.Gateway.Grok.FreeQuotaWindowHours
	cacheSeconds := cfg.Gateway.Grok.FreeQuotaStatsCacheSeconds
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

// isExplicitGrokFreeOAuthAccount 判断免费层软门禁是否适用；只有凭据或 extra 中
// subscription_tier/plan_type 明确等于 free 的 OAuth 账号命中，推断免费、basic 或空套餐均不命中。
func isExplicitGrokFreeOAuthAccount(account *Account) bool {
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

// filterGrokFreeQuotaAccounts 仅在 OpenAI 调度热路径过滤明确的 Grok Free OAuth 账号；
// 统计缺失或失败始终 fail-open，上游额度与限流仍是权威状态。
func (s *defaultOpenAIAccountScheduler) filterGrokFreeQuotaAccounts(ctx context.Context, accounts []Account) []Account {
	if s == nil || s.service == nil {
		return accounts
	}
	return filterGrokFreeQuotaAccountsCore(ctx, s.service.cfg, s.service.usageLogRepo, &s.grokFreeQuotaGateCache, accounts)
}

// filterGrokFreeQuotaAccountsForGateway 在普通 Gateway 调度中应用同一软门禁，
// 避免 Responses 已停调的免费账号仍被原生搜索选中。
func (s *GatewayService) filterGrokFreeQuotaAccountsForGateway(ctx context.Context, accounts []Account) []Account {
	if s == nil {
		return accounts
	}
	return filterGrokFreeQuotaAccountsCore(ctx, s.cfg, s.usageLogRepo, &gatewayGrokFreeQuotaGateCache, accounts)
}

// 以下缓存供非高级调度选择路径共享；高级调度器在实例内维护独立 sync.Map。
var gatewayGrokFreeQuotaGateCache sync.Map
var openaiGrokFreeQuotaGateCache sync.Map

// freeQuotaRefreshInFlight 按缓存实例合并并发后台刷新。
var freeQuotaRefreshInFlight sync.Map // *sync.Map -> *sync.Map (accountID -> struct{})

func filterGrokFreeQuotaAccountsCore(
	ctx context.Context,
	cfg *config.Config,
	usageLogRepo UsageLogRepository,
	cache *sync.Map,
	accounts []Account,
) []Account {
	if cache == nil {
		return accounts
	}
	settings, enabled := resolveGrokFreeQuotaGateSettings(cfg)
	if !enabled || len(accounts) == 0 || usageLogRepo == nil {
		return accounts
	}
	now := time.Now().UTC()
	tokensByID := make(map[int64]int64)
	missingIDs := make([]int64, 0, len(accounts))
	seenMissing := make(map[int64]struct{})
	for i := range accounts {
		account := &accounts[i]
		if !isExplicitGrokFreeOAuthAccount(account) || account.ID <= 0 {
			continue
		}
		if cached, ok := cache.Load(account.ID); ok {
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
		scheduleGrokFreeQuotaStatsRefresh(usageLogRepo, cache, settings, missingIDs)
	}

	filtered := make([]Account, 0, len(accounts))
	for i := range accounts {
		account := &accounts[i]
		if isExplicitGrokFreeOAuthAccount(account) {
			if tokens, known := tokensByID[account.ID]; known && tokens >= settings.gateTokens {
				continue
			}
		}
		filtered = append(filtered, *account)
	}
	return filtered
}

// scheduleGrokFreeQuotaStatsRefresh 在请求路径外加载用量统计，并用进行中标记合并同账号并发刷新。
func scheduleGrokFreeQuotaStatsRefresh(
	usageLogRepo UsageLogRepository,
	cache *sync.Map,
	settings grokFreeQuotaGateSettings,
	accountIDs []int64,
) {
	if usageLogRepo == nil || cache == nil || len(accountIDs) == 0 {
		return
	}
	inFlightRoot, _ := freeQuotaRefreshInFlight.LoadOrStore(cache, &sync.Map{})
	inFlight, ok := inFlightRoot.(*sync.Map)
	if !ok || inFlight == nil {
		return
	}

	toFetch := make([]int64, 0, len(accountIDs))
	for _, id := range accountIDs {
		if _, loaded := inFlight.LoadOrStore(id, struct{}{}); !loaded {
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
	go func() {
		defer func() {
			for _, id := range toFetch {
				inFlight.Delete(id)
			}
		}()
		now := time.Now().UTC()
		statsByID, err := queryGrokFreeQuotaWindowStats(context.Background(), usageLogRepo, toFetch, now.Add(-window))
		if err != nil {
			grokFreeQuotaGateQueryFailureTotal.Add(1)
			// 保存负缓存防止热路径反复查询；known=false 会持续放行，直到成功刷新。
			for _, accountID := range toFetch {
				cache.Store(accountID, grokFreeQuotaGateCacheEntry{checkedAt: now})
			}
			slog.Warn("grok_free_quota_soft_gate_stats_failed",
				"account_count", len(toFetch),
				"window_hours", window.Hours(),
				"error", err)
			sweepGrokFreeQuotaGateCache(cache, now, cacheTTL)
			return
		}
		for _, accountID := range toFetch {
			tokens := int64(0)
			if stats := statsByID[accountID]; stats != nil && stats.Tokens > 0 {
				tokens = stats.Tokens
			}
			cache.Store(accountID, grokFreeQuotaGateCacheEntry{tokens: tokens, checkedAt: now, known: true})
			if tokens >= gateTokens {
				grokFreeQuotaGateBlockedTotal.Add(1)
				slog.Info("grok_free_quota_soft_gate_blocked",
					"account_id", accountID,
					"tokens", tokens,
					"gate_tokens", gateTokens,
					"limit_tokens", limitTokens,
					"window_hours", window.Hours())
			}
		}
		sweepGrokFreeQuotaGateCache(cache, now, cacheTTL)
	}()
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

func queryGrokFreeQuotaWindowStats(ctx context.Context, usageLogRepo UsageLogRepository, accountIDs []int64, start time.Time) (map[int64]*usagestats.AccountStats, error) {
	if usageLogRepo == nil {
		return nil, nil
	}
	if batch, ok := usageLogRepo.(accountWindowStatsBatchReader); ok {
		return batch.GetAccountWindowStatsBatch(ctx, accountIDs, start)
	}
	statsByID := make(map[int64]*usagestats.AccountStats, len(accountIDs))
	for _, accountID := range accountIDs {
		stats, err := usageLogRepo.GetAccountWindowStats(ctx, accountID, start)
		if err != nil {
			return nil, err
		}
		statsByID[accountID] = stats
	}
	return statsByID, nil
}
