package service

import (
	"context"
	"errors"
	"hash/fnv"
	"strings"
	"time"
)

// 基础预热窗口：访问令牌剩余有效期低于该值时刷新。
// Grok 访问令牌通常约一小时有效，提前刷新可在请求路径缓存未命中时保持账号池可用。
const grokTokenRefreshSkew = time.Hour

// 错峰窗口：每个账号的实际预热窗口减去一个确定性的偏移量，范围为
// [0, grokTokenRefreshJitterMax]，避免同批导入的账号在同一轮刷新周期内集中刷新。
const grokTokenRefreshJitterMax = 3 * time.Minute

// 设置下限，避免错峰偏移把刷新窗口缩短到失去作用。
const grokTokenRefreshSkewMin = 30 * time.Minute

type GrokTokenRefresher struct {
	grokOAuthService GrokOAuthTokenService
}

func NewGrokTokenRefresher(grokOAuthService GrokOAuthTokenService) *GrokTokenRefresher {
	return &GrokTokenRefresher{grokOAuthService: grokOAuthService}
}

func (r *GrokTokenRefresher) CacheKey(account *Account) string {
	return GrokTokenCacheKey(account)
}

func (r *GrokTokenRefresher) CanRefresh(account *Account) bool {
	return account != nil && account.Platform == PlatformGrok && account.Type == AccountTypeOAuth &&
		strings.TrimSpace(account.GetGrokRefreshToken()) != ""
}

func (r *GrokTokenRefresher) NeedsRefresh(account *Account, refreshWindow time.Duration) bool {
	if account == nil || strings.TrimSpace(account.GetGrokRefreshToken()) == "" {
		return false
	}
	if strings.TrimSpace(account.GetGrokAccessToken()) == "" {
		return true
	}
	expiresAt := account.GetCredentialAsTime("expires_at")
	if expiresAt == nil {
		return true
	}
	if refreshWindow < grokTokenRefreshSkew {
		refreshWindow = grokTokenRefreshSkew
	}
	// 根据账号 ID 哈希生成确定性偏移，在错开预热刷新的同时保证测试结果稳定。
	refreshWindow = grokTokenRefreshWindowWithJitter(account.ID, refreshWindow)
	return time.Until(*expiresAt) < refreshWindow
}

// grokTokenRefreshWindowWithJitter 返回 refreshWindow 减去由 accountID 决定的稳定偏移量，
// 偏移范围为 [0, jitterMax]；基础窗口不低于 grokTokenRefreshSkewMin 时，结果也不会低于该值。
func grokTokenRefreshWindowWithJitter(accountID int64, refreshWindow time.Duration) time.Duration {
	if accountID <= 0 || refreshWindow <= grokTokenRefreshSkewMin {
		return refreshWindow
	}
	h := fnv.New32a()
	var b [8]byte
	id := uint64(accountID)
	for i := 0; i < 8; i++ {
		b[i] = byte(id >> (8 * i))
	}
	_, _ = h.Write(b[:])
	// 偏移范围为 [0, grokTokenRefreshJitterMax)。
	jitter := time.Duration(h.Sum32()%uint32(grokTokenRefreshJitterMax/time.Second)) * time.Second
	out := refreshWindow - jitter
	if out < grokTokenRefreshSkewMin {
		return grokTokenRefreshSkewMin
	}
	return out
}

func (r *GrokTokenRefresher) Refresh(ctx context.Context, account *Account) (map[string]any, error) {
	if r == nil || r.grokOAuthService == nil {
		return nil, errors.New("grok oauth service is not configured")
	}
	tokenInfo, err := r.grokOAuthService.RefreshAccountToken(ctx, account)
	if err != nil {
		return nil, err
	}
	newCredentials := r.grokOAuthService.BuildAccountCredentials(tokenInfo)
	newCredentials = MergeCredentials(account.Credentials, newCredentials)
	if baseURL := strings.TrimSpace(account.GetCredential("base_url")); baseURL != "" {
		newCredentials["base_url"] = baseURL
	}
	return newCredentials, nil
}
