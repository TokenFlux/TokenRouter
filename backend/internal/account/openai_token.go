// OpenAI 凭据回源、TTL 与锁等待归账号，复用同一缓存、刷新协调器和指标实例。
package account

import (
	"context"
	"errors"
	"math/rand/v2"
	"strconv"
	"strings"
	"sync/atomic"
	"time"
)

type OpenAITokenSource struct {
	Cache       AccessTokenCache
	Repository  RefreshRepository
	SetError    func(context.Context, int64, string) error
	Metrics     *OpenAITokenMetricsStore
	Policy      ProviderRefreshPolicy
	Refresh     func(context.Context, *Record, time.Duration) (*OAuthRefreshResult, error)
	Block       func(*Record, time.Time, string)
	Debug, Warn func(string, ...any)
}

func OpenAITokenCacheKey(account *Record) string {
	return "openai:account:" + strconv.FormatInt(account.ID, 10)
}

const (
	openAITokenRefreshSkew    = 3 * time.Minute
	openAITokenCacheSkew      = 5 * time.Minute
	openAILockInitialWait     = 20 * time.Millisecond
	openAILockMaxWait         = 120 * time.Millisecond
	openAILockMaxAttempts     = 5
	openAILockJitterRatio     = 0.2
	openAILockWarnThresholdMs = 250
)

// OpenAITokenRuntimeMetrics is a snapshot of refresh and lock contention metrics.
type OpenAITokenRuntimeMetrics struct {
	RefreshRequests    int64
	RefreshSuccess     int64
	RefreshFailure     int64
	LockAcquireFailure int64
	LockContention     int64
	LockWaitSamples    int64
	LockWaitTotalMs    int64
	LockWaitHit        int64
	LockWaitMiss       int64
	LastObservedUnixMs int64
}
type OpenAITokenMetricsStore struct {
	refreshRequests    atomic.Int64
	refreshSuccess     atomic.Int64
	refreshFailure     atomic.Int64
	lockAcquireFailure atomic.Int64
	lockContention     atomic.Int64
	lockWaitSamples    atomic.Int64
	lockWaitTotalMs    atomic.Int64
	lockWaitHit        atomic.Int64
	lockWaitMiss       atomic.Int64
	lastObservedUnixMs atomic.Int64
}

func (m *OpenAITokenMetricsStore) snapshot() OpenAITokenRuntimeMetrics {
	if m == nil {
		return OpenAITokenRuntimeMetrics{}
	}
	return OpenAITokenRuntimeMetrics{
		RefreshRequests:    m.refreshRequests.Load(),
		RefreshSuccess:     m.refreshSuccess.Load(),
		RefreshFailure:     m.refreshFailure.Load(),
		LockAcquireFailure: m.lockAcquireFailure.Load(),
		LockContention:     m.lockContention.Load(),
		LockWaitSamples:    m.lockWaitSamples.Load(),
		LockWaitTotalMs:    m.lockWaitTotalMs.Load(),
		LockWaitHit:        m.lockWaitHit.Load(),
		LockWaitMiss:       m.lockWaitMiss.Load(),
		LastObservedUnixMs: m.lastObservedUnixMs.Load(),
	}
}
func (m *OpenAITokenMetricsStore) touchNow() {
	if m == nil {
		return
	}
	m.lastObservedUnixMs.Store(time.Now().UnixMilli())
}
func (p *OpenAITokenSource) SnapshotRuntimeMetrics() OpenAITokenRuntimeMetrics {
	if p == nil {
		return OpenAITokenRuntimeMetrics{}
	}
	p.EnsureMetrics()
	return p.Metrics.snapshot()
}
func (p *OpenAITokenSource) EnsureMetrics() {
	if p != nil && p.Metrics == nil {
		p.Metrics = &OpenAITokenMetricsStore{}
	}
}

// GetAccessToken returns a valid access_token.
func (p *OpenAITokenSource) GetAccessToken(ctx context.Context, account *Record) (string, error) {
	p.EnsureMetrics()
	if account == nil {
		return "", errors.New("account is nil")
	}
	if account.Platform != PlatformOpenAI || account.Type != AccountTypeOAuth {
		return "", errors.New("not an openai oauth account")
	}

	cacheKey := OpenAITokenCacheKey(account)

	// 1) Try cache first.
	if p.Cache != nil {
		if token, err := p.Cache.GetAccessToken(ctx, cacheKey); err == nil && strings.TrimSpace(token) != "" {
			p.Debug("openai_token_cache_hit", "account_id", account.ID)
			return token, nil
		} else if err != nil {
			p.Warn("openai_token_cache_get_failed", "account_id", account.ID, "error", err)
		}
	}

	p.Debug("openai_token_cache_miss", "account_id", account.ID)

	// 2) Refresh if needed (pre-expiry skew).
	expiresAt := account.GetCredentialAsTime("expires_at")
	needsRefresh := !account.IsOpenAIPersonalAccessToken() && (expiresAt == nil || time.Until(*expiresAt) <= openAITokenRefreshSkew)
	if needsRefresh && strings.TrimSpace(account.GetOpenAIRefreshToken()) == "" {
		if expiresAt != nil && !time.Now().Before(*expiresAt) {
			const reason = "openai access_token expired and refresh_token is missing"
			// 缺失 refresh_token 的过期 OAuth 账号无法自愈，需要立即剔出调度池。
			p.DisableAccountMissingRefreshToken(account, reason)
			return "", errors.New(reason)
		}
		needsRefresh = false
	}
	refreshFailed := false

	if needsRefresh && p.Refresh != nil {
		p.Metrics.refreshRequests.Add(1)
		p.Metrics.touchNow()

		result, err := p.Refresh(ctx, account, openAITokenRefreshSkew)
		if err != nil {
			if p.Policy.OnRefreshError == ProviderRefreshErrorReturn {
				return "", err
			}
			p.Warn("openai_token_refresh_failed", "account_id", account.ID, "error", err)
			p.Metrics.refreshFailure.Add(1)
			refreshFailed = true
		} else if result.LockHeld {
			if p.Policy.OnLockHeld == ProviderLockHeldWaitForCache {
				p.Metrics.lockContention.Add(1)
				p.Metrics.touchNow()
				token, waitErr := p.WaitForTokenAfterLockRace(ctx, cacheKey)
				if waitErr != nil {
					return "", waitErr
				}
				if strings.TrimSpace(token) != "" {
					p.Debug("openai_token_cache_hit_after_wait", "account_id", account.ID)
					return token, nil
				}
			}
		} else if result.Refreshed {
			p.Metrics.refreshSuccess.Add(1)
			account = result.Account
			expiresAt = account.GetCredentialAsTime("expires_at")
		} else {
			account = result.Account
			expiresAt = account.GetCredentialAsTime("expires_at")
		}
	} else if needsRefresh && p.Cache != nil {
		// Backward-compatible test path when refreshAPI is not injected.
		p.Metrics.refreshRequests.Add(1)
		p.Metrics.touchNow()
		locked, lockErr := p.Cache.AcquireRefreshLock(ctx, cacheKey, 30*time.Second)
		if lockErr == nil && locked {
			defer func() { _ = p.Cache.ReleaseRefreshLock(ctx, cacheKey) }()
		} else if lockErr != nil {
			p.Metrics.lockAcquireFailure.Add(1)
			p.Metrics.touchNow()
			p.Warn("openai_token_lock_failed", "account_id", account.ID, "error", lockErr)
		} else {
			p.Metrics.lockContention.Add(1)
			p.Metrics.touchNow()
			token, waitErr := p.WaitForTokenAfterLockRace(ctx, cacheKey)
			if waitErr != nil {
				return "", waitErr
			}
			if strings.TrimSpace(token) != "" {
				p.Debug("openai_token_cache_hit_after_wait", "account_id", account.ID)
				return token, nil
			}
		}
	}

	accessToken := account.GetCredential("access_token")
	if strings.TrimSpace(accessToken) == "" {
		return "", errors.New("access_token not found in credentials")
	}

	// 3) Populate cache with TTL.
	if p.Cache != nil {
		latestAccount, isStale := CheckTokenVersion(ctx, account, p.Repository)
		if isStale && latestAccount != nil {
			p.Debug("openai_token_version_stale_use_latest", "account_id", account.ID)
			accessToken = latestAccount.GetOpenAIAccessToken()
			if strings.TrimSpace(accessToken) == "" {
				return "", errors.New("access_token not found after version check")
			}
		} else {
			ttl := 30 * time.Minute
			if refreshFailed {
				if p.Policy.FailureTTL > 0 {
					ttl = p.Policy.FailureTTL
				} else {
					ttl = time.Minute
				}
				p.Debug("openai_token_cache_short_ttl", "account_id", account.ID, "reason", "refresh_failed")
			} else if expiresAt != nil {
				until := time.Until(*expiresAt)
				switch {
				case until > openAITokenCacheSkew:
					ttl = until - openAITokenCacheSkew
				case until > 0:
					ttl = until
				default:
					ttl = time.Minute
				}
			}
			if err := p.Cache.SetAccessToken(ctx, cacheKey, accessToken, ttl); err != nil {
				p.Warn("openai_token_cache_set_failed", "account_id", account.ID, "error", err)
			}
		}
	}

	return accessToken, nil
}

// DisableAccountMissingRefreshToken 将缺失 refresh_token 的过期 OpenAI OAuth 账号标记为 error。
// 请求 context 可能已经取消，因此这里使用后台 context 完成状态落库和缓存清理。
func (p *OpenAITokenSource) DisableAccountMissingRefreshToken(account *Record, reason string) {
	if p == nil || p.Repository == nil || account == nil {
		return
	}
	if p.Block != nil {
		p.Block(account, time.Time{}, "missing_refresh_token")
	}
	bgCtx := context.Background()
	if err := p.SetError(bgCtx, account.ID, reason); err != nil {
		p.Warn("openai_token_provider.set_error_failed",
			"account_id", account.ID,
			"error", err,
		)
		return
	}
	if p.Cache != nil {
		if err := p.Cache.DeleteAccessToken(bgCtx, OpenAITokenCacheKey(account)); err != nil {
			p.Warn("openai_token_provider.cache_delete_failed",
				"account_id", account.ID,
				"error", err,
			)
		}
	}
	p.Warn("openai_token_provider.account_disabled_missing_refresh_token",
		"account_id", account.ID,
		"reason", reason,
	)
}
func (p *OpenAITokenSource) WaitForTokenAfterLockRace(ctx context.Context, cacheKey string) (string, error) {
	wait := openAILockInitialWait
	totalWaitMs := int64(0)
	for i := 0; i < openAILockMaxAttempts; i++ {
		actualWait := JitterOpenAILockWait(wait)
		timer := time.NewTimer(actualWait)
		select {
		case <-ctx.Done():
			if !timer.Stop() {
				select {
				case <-timer.C:
				default:
				}
			}
			return "", ctx.Err()
		case <-timer.C:
		}

		waitMs := actualWait.Milliseconds()
		if waitMs < 0 {
			waitMs = 0
		}
		totalWaitMs += waitMs
		p.Metrics.lockWaitSamples.Add(1)
		p.Metrics.lockWaitTotalMs.Add(waitMs)
		p.Metrics.touchNow()

		token, err := p.Cache.GetAccessToken(ctx, cacheKey)
		if err == nil && strings.TrimSpace(token) != "" {
			p.Metrics.lockWaitHit.Add(1)
			if totalWaitMs >= openAILockWarnThresholdMs {
				p.Warn("openai_token_lock_wait_high", "wait_ms", totalWaitMs, "attempts", i+1)
			}
			return token, nil
		}

		if wait < openAILockMaxWait {
			wait *= 2
			if wait > openAILockMaxWait {
				wait = openAILockMaxWait
			}
		}
	}

	p.Metrics.lockWaitMiss.Add(1)
	if totalWaitMs >= openAILockWarnThresholdMs {
		p.Warn("openai_token_lock_wait_high", "wait_ms", totalWaitMs, "attempts", openAILockMaxAttempts)
	}
	return "", nil
}
func JitterOpenAILockWait(base time.Duration) time.Duration {
	if base <= 0 {
		return 0
	}
	minFactor := 1 - openAILockJitterRatio
	maxFactor := 1 + openAILockJitterRatio
	factor := minFactor + rand.Float64()*(maxFactor-minFactor)
	return time.Duration(float64(base) * factor)
}
