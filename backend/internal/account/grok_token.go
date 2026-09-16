// Grok 凭据查询复用账号刷新协调器；源对象不复制缓存、锁或策略状态。
package account

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"
)

type GrokTokenSource struct {
	Cache      AccessTokenCache
	Repository RefreshRepository
	Policy     ProviderRefreshPolicy
	Refresh    func(context.Context, *Record, time.Duration) (*OAuthRefreshResult, error)
}

const (
	GrokTokenCacheSkew          = 5 * time.Minute
	GrokRequestRefreshTimeout   = 8 * time.Second
	GrokRefreshLockWaitTimeout  = 2 * time.Second
	GrokRefreshLockPollInterval = 25 * time.Millisecond
)

var (
	ErrGrokOAuthRefreshNotConfigured = errors.New("grok oauth refresh is not configured")
	ErrGrokOAuthRefreshTokenMissing  = errors.New("grok oauth refresh token is missing")
	ErrGrokOAuthAccessTokenMissing   = errors.New("grok oauth access token is missing")
	ErrGrokOAuthAccessTokenExpired   = errors.New("grok oauth access token is expired")
	ErrGrokOAuthConfiguredProxyMiss  = errors.New("grok oauth configured proxy is missing")
)

// @project-doc docs/interfaces/grok_upstream.md#grok_account_contract
func (p *GrokTokenSource) GetAccessToken(ctx context.Context, account *Record) (string, error) {
	if account == nil {
		return "", errors.New("account is nil")
	}
	if account.Platform != PlatformGrok || account.Type != AccountTypeOAuth {
		return "", errors.New("not a grok oauth account")
	}
	selectedProxyID := CloneGrokProxyID(account.ProxyID)
	if eligibilityErr := GrokOAuthRequestAccountEligibilityError(account); eligibilityErr != nil {
		return "", WithGrokCredentialFailureSnapshot(eligibilityErr, account)
	}

	expiresAt := account.GetCredentialAsTime("expires_at")
	accountAccessToken := strings.TrimSpace(account.GetGrokAccessToken())
	if accountAccessToken == "" {
		return "", WithGrokCredentialFailureSnapshot(ErrGrokOAuthAccessTokenMissing, account)
	}
	if strings.TrimSpace(account.GetGrokRefreshToken()) == "" {
		return "", WithGrokCredentialFailureSnapshot(ErrGrokOAuthRefreshTokenMissing, account)
	}
	cacheKey := GrokTokenCacheKey(account)
	if p.Cache != nil {
		if token, err := p.Cache.GetAccessToken(ctx, cacheKey); err == nil {
			cachedToken := strings.TrimSpace(token)
			if cachedToken != "" && accountAccessToken != "" && cachedToken == accountAccessToken &&
				expiresAt != nil && time.Until(*expiresAt) > GrokTokenRefreshSkew {
				return cachedToken, nil
			}
		}
	}

	needsRefresh := expiresAt == nil || time.Until(*expiresAt) <= GrokTokenRefreshSkew
	if needsRefresh {
		if p.Refresh == nil {
			return "", ErrGrokOAuthRefreshNotConfigured
		}
		refreshCtx, cancel := context.WithTimeout(ctx, GrokRequestRefreshTimeout)
		defer cancel()
		result, err := p.Refresh(WithRefreshRequestPath(refreshCtx), account, GrokTokenRefreshSkew)
		if err != nil {
			if p.Policy.OnRefreshError == ProviderRefreshErrorReturn {
				return "", err
			}
		} else if result != nil && result.LockHeld {
			if p.Policy.OnLockHeld == ProviderLockHeldWaitForCache {
				token, waitErr := p.WaitForRefreshedToken(refreshCtx, account, cacheKey)
				return token, WithGrokCredentialFailureSnapshot(waitErr, account)
			}
			if expiresAt == nil || !time.Now().Before(*expiresAt) {
				return "", WithGrokCredentialFailureSnapshot(ErrGrokOAuthAccessTokenExpired, account)
			}
		} else if result != nil && result.Account != nil {
			if eligibilityErr := GrokOAuthRequestAccountEligibilityError(result.Account); eligibilityErr != nil {
				return "", WithGrokCredentialFailureSnapshot(eligibilityErr, result.Account)
			}
			if !GrokCredentialProxyIDsEqual(result.Account.ProxyID, selectedProxyID) {
				return "", WithGrokCredentialFailureSnapshot(ErrRefreshAccountStateChanged, result.Account)
			}
			account = result.Account
			expiresAt = account.GetCredentialAsTime("expires_at")
		}
	}

	accessToken := account.GetGrokAccessToken()
	if strings.TrimSpace(accessToken) == "" {
		return "", WithGrokCredentialFailureSnapshot(ErrGrokOAuthAccessTokenMissing, account)
	}
	if expiresAt != nil && !time.Now().Before(*expiresAt) {
		return "", WithGrokCredentialFailureSnapshot(ErrGrokOAuthAccessTokenExpired, account)
	}

	if p.Cache != nil {
		latestAccount, isStale := CheckTokenVersion(ctx, account, p.Repository)
		if isStale && latestAccount != nil {
			if eligibilityErr := GrokOAuthRequestAccountEligibilityError(latestAccount); eligibilityErr != nil {
				return "", WithGrokCredentialFailureSnapshot(eligibilityErr, latestAccount)
			}
			if !GrokCredentialProxyIDsEqual(latestAccount.ProxyID, selectedProxyID) {
				return "", WithGrokCredentialFailureSnapshot(ErrRefreshAccountStateChanged, latestAccount)
			}
			accessToken = latestAccount.GetGrokAccessToken()
			if strings.TrimSpace(accessToken) == "" {
				return "", WithGrokCredentialFailureSnapshot(ErrGrokOAuthAccessTokenMissing, latestAccount)
			}
			latestExpiry := latestAccount.GetCredentialAsTime("expires_at")
			if latestExpiry == nil || !time.Now().Before(*latestExpiry) {
				return "", WithGrokCredentialFailureSnapshot(ErrGrokOAuthAccessTokenExpired, latestAccount)
			}
		} else {
			ttl := 30 * time.Minute
			if expiresAt != nil {
				until := time.Until(*expiresAt)
				switch {
				case until > GrokTokenCacheSkew:
					ttl = until - GrokTokenCacheSkew
				case until > 0:
					ttl = until
				default:
					ttl = time.Minute
				}
			}
			_ = p.Cache.SetAccessToken(ctx, cacheKey, accessToken, ttl)
		}
	}

	return accessToken, nil
}

// GetAccessTokenForManualTest 为管理员发起的“测试连接”返回访问令牌。它与
// GetAccessToken 不同，不应用请求路径的调度资格门（手动调度开关、限流、过载或
// 临时冷却），因为手动测试正是用来检查这些状态中的账号，与 Codex/OpenAI
// 测试不受调度状态影响的行为一致（#4598）。
//
// 凭据完整性检查仍然生效，包括配置代理缺失、共享刷新锁协议和刷新 API 的账号重读。
// 非 active（禁用或错误）账号的凭据轮换仍由 RefreshIfNeeded 拦截，其尚未过期的
// 令牌只用于原样探测。
func (p *GrokTokenSource) GetAccessTokenForManualTest(ctx context.Context, account *Record) (string, error) {
	if account == nil {
		return "", errors.New("account is nil")
	}
	if account.Platform != PlatformGrok || account.Type != AccountTypeOAuth {
		return "", errors.New("not a grok oauth account")
	}
	if account.ProxyID != nil && account.Proxy == nil {
		return "", ErrGrokOAuthConfiguredProxyMiss
	}
	if strings.TrimSpace(account.GetGrokRefreshToken()) == "" {
		return "", ErrGrokOAuthRefreshTokenMissing
	}

	accessToken := strings.TrimSpace(account.GetGrokAccessToken())
	expiresAt := account.GetCredentialAsTime("expires_at")
	tokenValid := accessToken != "" && expiresAt != nil && time.Now().Before(*expiresAt)
	if accessToken != "" && expiresAt != nil && time.Until(*expiresAt) > GrokTokenRefreshSkew {
		return accessToken, nil
	}

	if p.Refresh == nil {
		if tokenValid {
			return accessToken, nil
		}
		return "", ErrGrokOAuthRefreshNotConfigured
	}

	// 刻意不标记为请求路径刷新：该路径会在 RefreshIfNeeded 内再次应用调度资格，
	// 而这正是手动测试需要绕过的门禁。
	refreshCtx, cancel := context.WithTimeout(ctx, GrokRequestRefreshTimeout)
	defer cancel()
	result, err := p.Refresh(refreshCtx, account, GrokTokenRefreshSkew)
	if err != nil {
		if tokenValid {
			return accessToken, nil
		}
		return "", err
	}
	if result != nil && result.LockHeld {
		if tokenValid {
			return accessToken, nil
		}
		return "", errors.New("token refresh is already in progress on another worker; retry in a few seconds")
	}
	if result != nil && result.Account != nil {
		account = result.Account
	}

	accessToken = strings.TrimSpace(account.GetGrokAccessToken())
	if accessToken == "" {
		return "", ErrGrokOAuthAccessTokenMissing
	}
	if latestExpiry := account.GetCredentialAsTime("expires_at"); latestExpiry != nil && !time.Now().Before(*latestExpiry) {
		return "", ErrGrokOAuthAccessTokenExpired
	}
	return accessToken, nil
}
func (p *GrokTokenSource) WaitForRefreshedToken(ctx context.Context, account *Record, cacheKey string) (string, error) {
	waitCtx, cancel := context.WithTimeout(ctx, GrokRefreshLockWaitTimeout)
	defer cancel()

	initialToken := strings.TrimSpace(account.GetGrokAccessToken())
	initialVersion := account.GetCredentialAsInt64("_token_version")
	selectedProxyID := CloneGrokProxyID(account.ProxyID)
	sawAuthoritativeState := false
	var lastAccountReadErr error
	ticker := time.NewTicker(GrokRefreshLockPollInterval)
	defer ticker.Stop()

	for {
		cachedToken := ""
		if p.Cache != nil {
			if token, err := p.Cache.GetAccessToken(waitCtx, cacheKey); err == nil {
				cachedToken = strings.TrimSpace(token)
			}
		}

		if p.Repository != nil {
			latest, err := p.Repository.GetByID(waitCtx, account.ID)
			if err != nil {
				lastAccountReadErr = err
			} else if latest == nil {
				return "", ErrRefreshAccountStateChanged
			} else {
				sawAuthoritativeState = true
				if eligibilityErr := GrokOAuthRequestAccountEligibilityError(latest); eligibilityErr != nil {
					return "", WithGrokCredentialFailureSnapshot(eligibilityErr, latest)
				}
				if !GrokCredentialProxyIDsEqual(latest.ProxyID, selectedProxyID) {
					return "", WithGrokCredentialFailureSnapshot(ErrRefreshAccountStateChanged, latest)
				}
				token := strings.TrimSpace(latest.GetGrokAccessToken())
				version := latest.GetCredentialAsInt64("_token_version")
				expiresAt := latest.GetCredentialAsTime("expires_at")
				changed := token != initialToken || (version > 0 && version > initialVersion)
				valid := expiresAt != nil && time.Now().Before(*expiresAt)
				if token != "" && changed && valid {
					// 带版本的数据库凭据是权威状态；旧缓存不得让请求继续使用过期令牌，需尽力修复。
					if cachedToken != "" && cachedToken != token {
						ttl := time.Until(*expiresAt)
						if ttl > GrokTokenCacheSkew {
							ttl -= GrokTokenCacheSkew
						}
						_ = p.Cache.SetAccessToken(waitCtx, cacheKey, token, ttl)
					}
					return token, nil
				}
			}
		}

		select {
		case <-waitCtx.Done():
			if ctx.Err() != nil {
				return "", ctx.Err()
			}
			if !sawAuthoritativeState {
				if lastAccountReadErr == nil {
					lastAccountReadErr = waitCtx.Err()
				}
				return "", fmt.Errorf("%w: %v", ErrRefreshAccountRereadFailed, lastAccountReadErr)
			}
			// 另一个 worker 仍持有刷新权且权威账号行未变，不隔离旧凭据；
			// 对方可能在本次有界等待结束后立即提交刷新结果。
			return "", ErrRefreshAccountStateChanged
		case <-ticker.C:
		}
	}
}
func GrokOAuthRequestAccountEligibilityError(account *Record) error {
	if account == nil || !account.IsGrokOAuth() || !account.IsSchedulable() {
		return ErrRefreshAccountStateChanged
	}
	if account.ProxyID != nil && account.Proxy == nil {
		return ErrGrokOAuthConfiguredProxyMiss
	}
	return nil
}
func CloneGrokProxyID(proxyID *int64) *int64 {
	if proxyID == nil {
		return nil
	}
	value := *proxyID
	return &value
}
func (p *GrokTokenSource) InvalidateToken(ctx context.Context, account *Record) error {
	if p == nil || p.Cache == nil || account == nil {
		return nil
	}
	return p.Cache.DeleteAccessToken(ctx, GrokTokenCacheKey(account))
}
func GrokTokenCacheKey(account *Record) string {
	if account == nil {
		return "grok:account:0"
	}
	return "grok:account:" + strconv.FormatInt(account.ID, 10)
}
