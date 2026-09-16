// Claude 凭据读取拥有缓存与刷新政策，交换和条件持久化使用注入的唯一协调器。
package account

import (
	"context"
	"errors"
	"strconv"
	"strings"
	"time"
)

const (
	ClaudeTokenRefreshSkew = 3 * time.Minute
	ClaudeTokenCacheSkew   = 5 * time.Minute
	ClaudeLockWaitTime     = 200 * time.Millisecond
)

type AccessTokenCache interface {
	GetAccessToken(context.Context, string) (string, error)
	SetAccessToken(context.Context, string, string, time.Duration) error
	DeleteAccessToken(context.Context, string) error
	AcquireRefreshLock(context.Context, string, time.Duration) (bool, error)
	ReleaseRefreshLock(context.Context, string) error
}
type ClaudeTokenOptions struct {
	Debug, Warn func(string, ...any)
	Cache       AccessTokenCache
	Repository  RefreshRepository
	Policy      ProviderRefreshPolicy
	Refresh     func(context.Context, *Record, time.Duration) (*OAuthRefreshResult, error)
	Vertex      func(context.Context, *Record) (string, error)
}

func ClaudeTokenCacheKey(value *Record) string {
	return "claude:account:" + strconv.FormatInt(value.ID, 10)
}

// GetAccessToken returns a valid access_token.
func GetClaudeAccessToken(ctx context.Context, account *Record, options ClaudeTokenOptions) (string, error) {
	if account == nil {
		return "", errors.New("account is nil")
	}
	if account.Platform != PlatformAnthropic || (account.Type != AccountTypeOAuth && account.Type != AccountTypeServiceAccount) {
		return "", errors.New("not an anthropic oauth or service account")
	}
	if account.Type == AccountTypeServiceAccount {
		return options.Vertex(ctx, account)
	}

	cacheKey := ClaudeTokenCacheKey(account)

	// 1) Try cache first.
	if options.Cache != nil {
		if token, err := options.Cache.GetAccessToken(ctx, cacheKey); err == nil && strings.TrimSpace(token) != "" {
			options.debug("claude_token_cache_hit", "account_id", account.ID)
			return token, nil
		} else if err != nil {
			options.warn("claude_token_cache_get_failed", "account_id", account.ID, "error", err)
		}
	}

	options.debug("claude_token_cache_miss", "account_id", account.ID)

	// 2) Refresh if needed (pre-expiry skew).
	expiresAt := account.GetCredentialAsTime("expires_at")
	needsRefresh := expiresAt == nil || time.Until(*expiresAt) <= ClaudeTokenRefreshSkew
	refreshFailed := false

	if needsRefresh && options.Refresh != nil {
		result, err := options.Refresh(ctx, account, ClaudeTokenRefreshSkew)
		if err != nil {
			if options.Policy.OnRefreshError == ProviderRefreshErrorReturn {
				return "", err
			}
			options.warn("claude_token_refresh_failed", "account_id", account.ID, "error", err)
			refreshFailed = true
		} else if result.LockHeld {
			if options.Policy.OnLockHeld == ProviderLockHeldWaitForCache && options.Cache != nil {
				time.Sleep(ClaudeLockWaitTime)
				if token, cacheErr := options.Cache.GetAccessToken(ctx, cacheKey); cacheErr == nil && strings.TrimSpace(token) != "" {
					options.debug("claude_token_cache_hit_after_wait", "account_id", account.ID)
					return token, nil
				}
			}
		} else {
			account = result.Account
			expiresAt = account.GetCredentialAsTime("expires_at")
		}
	} else if needsRefresh && options.Cache != nil {
		// Backward-compatible test path when refreshAPI is not injected.
		locked, lockErr := options.Cache.AcquireRefreshLock(ctx, cacheKey, 30*time.Second)
		if lockErr == nil && locked {
			defer func() { _ = options.Cache.ReleaseRefreshLock(ctx, cacheKey) }()
		} else if lockErr != nil {
			options.warn("claude_token_lock_failed", "account_id", account.ID, "error", lockErr)
		} else {
			time.Sleep(ClaudeLockWaitTime)
			if token, err := options.Cache.GetAccessToken(ctx, cacheKey); err == nil && strings.TrimSpace(token) != "" {
				options.debug("claude_token_cache_hit_after_wait", "account_id", account.ID)
				return token, nil
			}
		}
	}

	accessToken := account.GetCredential("access_token")
	if strings.TrimSpace(accessToken) == "" {
		return "", errors.New("access_token not found in credentials")
	}

	// 3) Populate cache with TTL.
	if options.Cache != nil {
		latestAccount, isStale := CheckTokenVersion(ctx, account, options.Repository, options.Debug)
		if isStale && latestAccount != nil {
			options.debug("claude_token_version_stale_use_latest", "account_id", account.ID)
			accessToken = latestAccount.GetCredential("access_token")
			if strings.TrimSpace(accessToken) == "" {
				return "", errors.New("access_token not found after version check")
			}
		} else {
			ttl := 30 * time.Minute
			if refreshFailed {
				if options.Policy.FailureTTL > 0 {
					ttl = options.Policy.FailureTTL
				} else {
					ttl = time.Minute
				}
				options.debug("claude_token_cache_short_ttl", "account_id", account.ID, "reason", "refresh_failed")
			} else if expiresAt != nil {
				until := time.Until(*expiresAt)
				switch {
				case until > ClaudeTokenCacheSkew:
					ttl = until - ClaudeTokenCacheSkew
				case until > 0:
					ttl = until
				default:
					ttl = time.Minute
				}
			}
			if err := options.Cache.SetAccessToken(ctx, cacheKey, accessToken, ttl); err != nil {
				options.warn("claude_token_cache_set_failed", "account_id", account.ID, "error", err)
			}
		}
	}

	return accessToken, nil
}

func (o ClaudeTokenOptions) debug(message string, args ...any) {
	if o.Debug != nil {
		o.Debug(message, args...)
	}
}
func (o ClaudeTokenOptions) warn(message string, args ...any) {
	if o.Warn != nil {
		o.Warn(message, args...)
	}
}
