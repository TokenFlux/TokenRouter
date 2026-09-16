// Antigravity 请求凭据与 project 回填冷却只有此实例拥有，旧入口仅注入端口。
package account

import (
	"context"
	"errors"
	"strconv"
	"strings"
	"sync"
	"time"
)

type AntigravityTokenState struct{ backfillCooldown sync.Map }
type AntigravityTokenOptions struct {
	Cache                AccessTokenCache
	Repository           RefreshRepository
	Policy               ProviderRefreshPolicy
	Refresh              func(context.Context, *Record, time.Duration) (*OAuthRefreshResult, error)
	FillProject          func(context.Context, *Record, string) (string, error)
	Persist              func(context.Context, *Record, map[string]any) error
	SetTempUnschedulable func(context.Context, int64, time.Time, string) error
	TempUnschedCache     TempUnschedCache
	Warn, Debug          func(string, ...any)
}

const (
	antigravityTokenRefreshSkew = 3 * time.Minute
	antigravityTokenCacheSkew   = 5 * time.Minute
	antigravityBackfillCooldown = 5 * time.Minute
	// antigravityRequestRefreshTimeout 请求路径上 token 刷新的最大等待时间。
	// 超过此时间直接放弃刷新、标记账号临时不可调度并触发 failover，
	// 让后台 TokenRefreshService 在下个周期继续重试。
	antigravityRequestRefreshTimeout = 8 * time.Second
)

// GetAccessToken returns a valid access_token.
// @project-doc docs/interfaces/antigravity_upstream.md#antigravity_account_contract
func (p *AntigravityTokenState) GetAccessToken(ctx context.Context, account *Record, options AntigravityTokenOptions) (string, error) {
	if account == nil {
		return "", errors.New("account is nil")
	}
	if account.Platform != PlatformAntigravity {
		return "", errors.New("not an antigravity account")
	}

	// upstream accounts use static api_key and never refresh oauth token.
	if account.Type == AccountTypeUpstream {
		apiKey := account.GetCredential("api_key")
		if apiKey == "" {
			return "", errors.New("upstream account missing api_key in credentials")
		}
		return apiKey, nil
	}
	if account.Type != AccountTypeOAuth {
		return "", errors.New("not an antigravity oauth account")
	}

	cacheKey := AntigravityTokenCacheKey(account)

	// 1) Try cache first.
	if options.Cache != nil {
		if token, err := options.Cache.GetAccessToken(ctx, cacheKey); err == nil && strings.TrimSpace(token) != "" {
			return token, nil
		}
	}

	// 2) Refresh if needed (pre-expiry skew).
	expiresAt := account.GetCredentialAsTime("expires_at")
	needsRefresh := expiresAt == nil || time.Until(*expiresAt) <= antigravityTokenRefreshSkew
	if needsRefresh && options.Refresh != nil {
		// 请求路径使用短超时，避免代理不通时阻塞过久（后台刷新服务会继续重试）
		refreshCtx, cancel := context.WithTimeout(ctx, antigravityRequestRefreshTimeout)
		defer cancel()
		result, err := options.Refresh(refreshCtx, account, antigravityTokenRefreshSkew)
		if err != nil {
			// 标记账号临时不可调度，避免后续请求继续命中
			p.markTempUnschedulable(account, err, options)
			if options.Policy.OnRefreshError == ProviderRefreshErrorReturn {
				return "", err
			}
		} else if result.LockHeld {
			if options.Policy.OnLockHeld == ProviderLockHeldWaitForCache && options.Cache != nil {
				if token, cacheErr := options.Cache.GetAccessToken(ctx, cacheKey); cacheErr == nil && strings.TrimSpace(token) != "" {
					return token, nil
				}
			}
			// default policy: continue with existing token.
		} else {
			account = result.Account
			expiresAt = account.GetCredentialAsTime("expires_at")
		}
	} else if needsRefresh && options.Cache != nil {
		// Backward-compatible test path when refreshAPI is not injected.
		locked, err := options.Cache.AcquireRefreshLock(ctx, cacheKey, 30*time.Second)
		if err == nil && locked {
			defer func() { _ = options.Cache.ReleaseRefreshLock(ctx, cacheKey) }()
		}
	}

	accessToken := account.GetCredential("access_token")
	if strings.TrimSpace(accessToken) == "" {
		return "", errors.New("access_token not found in credentials")
	}

	// Backfill project_id online when missing, with cooldown to avoid hammering.
	if strings.TrimSpace(account.GetCredential("project_id")) == "" && options.FillProject != nil {
		if p.shouldAttemptBackfill(account.ID) {
			p.markBackfillAttempted(account.ID)
			if projectID, err := options.FillProject(ctx, account, accessToken); err == nil && projectID != "" {
				account.Credentials["project_id"] = projectID
				if updateErr := options.Persist(ctx, account, account.Credentials); updateErr != nil {
					options.Warn("antigravity_project_id_backfill_persist_failed",
						"account_id", account.ID,
						"error", updateErr,
					)
				}
			}
		}
	}

	// 3) Populate cache with TTL.
	if options.Cache != nil {
		latestAccount, isStale := CheckTokenVersion(ctx, account, options.Repository, options.Debug)
		if isStale && latestAccount != nil {
			options.Debug("antigravity_token_version_stale_use_latest", "account_id", account.ID)
			accessToken = latestAccount.GetCredential("access_token")
			if strings.TrimSpace(accessToken) == "" {
				return "", errors.New("access_token not found after version check")
			}
		} else {
			ttl := 30 * time.Minute
			if expiresAt != nil {
				until := time.Until(*expiresAt)
				switch {
				case until > antigravityTokenCacheSkew:
					ttl = until - antigravityTokenCacheSkew
				case until > 0:
					ttl = until
				default:
					ttl = time.Minute
				}
			}
			_ = options.Cache.SetAccessToken(ctx, cacheKey, accessToken, ttl)
		}
	}

	return accessToken, nil
}

// shouldAttemptBackfill checks backfill cooldown.
func (p *AntigravityTokenState) shouldAttemptBackfill(accountID int64) bool {
	if v, ok := p.backfillCooldown.Load(accountID); ok {
		if lastAttempt, ok := v.(time.Time); ok {
			return time.Since(lastAttempt) > antigravityBackfillCooldown
		}
	}
	return true
}

// markTempUnschedulable 在请求路径上 token 刷新失败时标记账号临时不可调度。
// 同时写 DB 和 Redis 缓存，确保调度器立即跳过该账号。
// 使用 background context 因为请求 context 可能已超时。
func (p *AntigravityTokenState) markTempUnschedulable(account *Record, refreshErr error, options AntigravityTokenOptions) {
	if options.SetTempUnschedulable == nil || account == nil {
		return
	}
	now := time.Now()
	until := now.Add(TokenRefreshTempUnschedDuration)
	reason := "token refresh failed on request path: " + refreshErr.Error()
	bgCtx := context.Background()
	if err := options.SetTempUnschedulable(bgCtx, account.ID, until, reason); err != nil {
		options.Warn("antigravity_token_provider.set_temp_unschedulable_failed",
			"account_id", account.ID,
			"error", err,
		)
		return
	}
	options.Warn("antigravity_token_provider.temp_unschedulable_set",
		"account_id", account.ID,
		"until", until.Format(time.RFC3339),
		"reason", reason,
	)
	// 同步写 Redis 缓存，调度器立即生效
	if options.TempUnschedCache != nil {
		state := &TempUnschedState{
			UntilUnix:       until.Unix(),
			TriggeredAtUnix: now.Unix(),
			ErrorMessage:    reason,
		}
		if err := options.TempUnschedCache.SetTempUnsched(bgCtx, account.ID, state); err != nil {
			options.Warn("antigravity_token_provider.temp_unsched_cache_set_failed",
				"account_id", account.ID,
				"error", err,
			)
		}
	}
}
func (p *AntigravityTokenState) markBackfillAttempted(accountID int64) {
	p.backfillCooldown.Store(accountID, time.Now())
}
func AntigravityTokenCacheKey(account *Record) string {
	projectID := strings.TrimSpace(account.GetCredential("project_id"))
	if projectID != "" {
		return "ag:" + projectID
	}
	return "ag:account:" + strconv.FormatInt(account.ID, 10)
}
