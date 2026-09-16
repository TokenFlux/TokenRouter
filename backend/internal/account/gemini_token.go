// Gemini 请求侧凭据编排保持缓存作用域、自动发现与 TTL 差异。
package account

import (
	"context"
	"errors"
	"strconv"
	"strings"
	"time"
)

const (
	GeminiTokenRefreshSkew = 3 * time.Minute
	GeminiTokenCacheSkew   = 5 * time.Minute
)

type GeminiTokenOptions struct {
	Debug, Warn  func(string, ...any)
	Cache        AccessTokenCache
	Repository   RefreshRepository
	Policy       ProviderRefreshPolicy
	Refresh      func(context.Context, *Record, time.Duration) (*OAuthRefreshResult, error)
	Vertex       func(context.Context, *Record) (string, error)
	Project      func(context.Context, string, string) (string, string, error)
	ResolveProxy func(context.Context, int64) (string, bool)
	Persist      func(context.Context, *Record, map[string]any) error
	Logf         func(string, ...any)
}

func GeminiOAuthTokenCacheKey(account *Record) string {
	projectID := strings.TrimSpace(account.GetCredential("project_id"))
	if projectID != "" {
		return "gemini:" + projectID
	}
	return "gemini:account:" + strconv.FormatInt(account.ID, 10)
}
func GetGeminiAccessToken(ctx context.Context, account *Record, options GeminiTokenOptions) (string, error) {
	if account == nil {
		return "", errors.New("account is nil")
	}
	if account.Platform != PlatformGemini || (account.Type != AccountTypeOAuth && account.Type != AccountTypeServiceAccount) {
		return "", errors.New("not a gemini oauth or service account")
	}
	if account.Type == AccountTypeServiceAccount {
		return options.Vertex(ctx, account)
	}

	cacheKey := GeminiOAuthTokenCacheKey(account)

	// 1) Try cache first.
	if options.Cache != nil {
		if token, err := options.Cache.GetAccessToken(ctx, cacheKey); err == nil && strings.TrimSpace(token) != "" {
			return token, nil
		}
	}

	// 2) Refresh if needed (pre-expiry skew).
	expiresAt := account.GetCredentialAsTime("expires_at")
	needsRefresh := expiresAt == nil || time.Until(*expiresAt) <= GeminiTokenRefreshSkew

	if needsRefresh && options.Refresh != nil {
		result, err := options.Refresh(ctx, account, GeminiTokenRefreshSkew)
		if err != nil {
			if options.Policy.OnRefreshError == ProviderRefreshErrorReturn {
				return "", err
			}
		} else if result.LockHeld {
			if options.Policy.OnLockHeld == ProviderLockHeldWaitForCache && options.Cache != nil {
				if token, cacheErr := options.Cache.GetAccessToken(ctx, cacheKey); cacheErr == nil && strings.TrimSpace(token) != "" {
					return token, nil
				}
			}
			options.debug("gemini_token_lock_held_use_old", "account_id", account.ID)
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
			options.warn("gemini_token_lock_failed", "account_id", account.ID, "error", lockErr)
		}
	}

	accessToken := account.GetCredential("access_token")
	if strings.TrimSpace(accessToken) == "" {
		return "", errors.New("access_token not found in credentials")
	}

	// project_id is optional now:
	// - If present: use Code Assist API (requires project_id)
	// - If absent: use AI Studio API with OAuth token.
	projectID := strings.TrimSpace(account.GetCredential("project_id"))
	autoDetectProjectID := account.GetCredential("auto_detect_project_id") == "true"

	if projectID == "" && autoDetectProjectID {
		if options.Project == nil {
			return accessToken, nil
		}

		var proxyURL string
		if account.ProxyID != nil && options.ResolveProxy != nil {
			if resolved, ok := options.ResolveProxy(ctx, *account.ProxyID); ok {
				proxyURL = resolved
			}
		}
		detected, tierID, err := options.Project(ctx, accessToken, proxyURL)
		if err != nil {
			options.Logf("[GeminiTokenProvider] Auto-detect project_id failed: %v, fallback to AI Studio API mode", err)
			return accessToken, nil
		}
		detected = strings.TrimSpace(detected)
		tierID = strings.TrimSpace(tierID)
		if detected != "" {
			if account.Credentials == nil {
				account.Credentials = make(map[string]any)
			}
			account.Credentials["project_id"] = detected
			if tierID != "" {
				account.Credentials["tier_id"] = tierID
			}
			_ = options.Persist(ctx, account, account.Credentials)
		}
	}

	// 3) Populate cache with TTL.
	if options.Cache != nil {
		latestAccount, isStale := CheckTokenVersion(ctx, account, options.Repository, options.Debug)
		if isStale && latestAccount != nil {
			options.debug("gemini_token_version_stale_use_latest", "account_id", account.ID)
			accessToken = latestAccount.GetCredential("access_token")
			if strings.TrimSpace(accessToken) == "" {
				return "", errors.New("access_token not found after version check")
			}
		} else {
			ttl := 30 * time.Minute
			if expiresAt != nil {
				until := time.Until(*expiresAt)
				switch {
				case until > GeminiTokenCacheSkew:
					ttl = until - GeminiTokenCacheSkew
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

func (o GeminiTokenOptions) debug(message string, args ...any) {
	if o.Debug != nil {
		o.Debug(message, args...)
	}
}
func (o GeminiTokenOptions) warn(message string, args ...any) {
	if o.Warn != nil {
		o.Warn(message, args...)
	}
}
