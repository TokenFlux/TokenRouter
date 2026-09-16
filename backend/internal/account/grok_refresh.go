package account

import (
	"context"
	"errors"
	"hash/fnv"
	"strings"
	"time"
)

// 基础预热窗口：访问令牌剩余有效期低于该值时刷新。
// Grok 访问令牌通常约一小时有效，提前刷新可在请求路径缓存未命中时保持账号池可用。
const GrokTokenRefreshSkew = time.Hour

// 错峰窗口：每个账号的实际预热窗口减去一个确定性的偏移量，范围为
// [0, GrokTokenRefreshJitterMax]，避免同批导入的账号在同一轮刷新周期内集中刷新。
const GrokTokenRefreshJitterMax = 3 * time.Minute

// 设置下限，避免错峰偏移把刷新窗口缩短到失去作用。
const GrokTokenRefreshSkewMin = 30 * time.Minute

type GrokTokenRefresher struct {
	grokOAuthService GrokRefreshTokenService
}

func NewGrokTokenRefresher(grokOAuthService GrokRefreshTokenService) *GrokTokenRefresher {
	return &GrokTokenRefresher{grokOAuthService: grokOAuthService}
}

func (r *GrokTokenRefresher) CacheKey(account *Record) string {
	return GrokTokenCacheKey(account)
}

func (r *GrokTokenRefresher) CanRefresh(account *Record) bool {
	return account != nil && account.Platform == PlatformGrok && account.Type == AccountTypeOAuth &&
		strings.TrimSpace(account.GetGrokRefreshToken()) != ""
}

func (r *GrokTokenRefresher) NeedsRefresh(account *Record, refreshWindow time.Duration) bool {
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
	if refreshWindow < GrokTokenRefreshSkew {
		refreshWindow = GrokTokenRefreshSkew
	}
	// 根据账号 ID 哈希生成确定性偏移，在错开预热刷新的同时保证测试结果稳定。
	refreshWindow = GrokTokenRefreshWindowWithJitter(account.ID, refreshWindow)
	return time.Until(*expiresAt) < refreshWindow
}

// GrokTokenRefreshWindowWithJitter 返回 refreshWindow 减去由 accountID 决定的稳定偏移量，
// 偏移范围为 [0, jitterMax]；基础窗口不低于 GrokTokenRefreshSkewMin 时，结果也不会低于该值。
func GrokTokenRefreshWindowWithJitter(accountID int64, refreshWindow time.Duration) time.Duration {
	if accountID <= 0 || refreshWindow <= GrokTokenRefreshSkewMin {
		return refreshWindow
	}
	h := fnv.New32a()
	var b [8]byte
	id := uint64(accountID)
	for i := 0; i < 8; i++ {
		b[i] = byte(id >> (8 * i))
	}
	_, _ = h.Write(b[:])
	// 偏移范围为 [0, GrokTokenRefreshJitterMax)。
	jitter := time.Duration(h.Sum32()%uint32(GrokTokenRefreshJitterMax/time.Second)) * time.Second
	out := refreshWindow - jitter
	if out < GrokTokenRefreshSkewMin {
		return GrokTokenRefreshSkewMin
	}
	return out
}

func (r *GrokTokenRefresher) Refresh(ctx context.Context, account *Record) (map[string]any, error) {
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

// 刷新器只接受账号拥有的令牌与凭据投影能力。
type GrokRefreshTokenService interface {
	RefreshAccountToken(context.Context, *Record) (*GrokTokenInfo, error)
	BuildAccountCredentials(*GrokTokenInfo) map[string]any
}
