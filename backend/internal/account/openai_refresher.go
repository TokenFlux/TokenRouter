// 账号后台与请求刷新复用同一资格、合并和 PAT 清理规则，交换从账号端口取得。
package account

import (
	"context"
	"strings"
	"time"
)

type OpenAIRefreshTokenService interface {
	RefreshAccountToken(context.Context, *Record) (*OpenAITokenInfo, error)
}
type OpenAITokenRefresher struct{ Authorization OpenAIRefreshTokenService }

// CacheKey 返回用于分布式锁的缓存键
func (r *OpenAITokenRefresher) CacheKey(account *Record) string {
	return OpenAITokenCacheKey(account)
}

// CanRefresh 检查是否能处理此账号
func (r *OpenAITokenRefresher) CanRefresh(account *Record) bool {
	if account.IsCredentialShadow() {
		return false
	}
	return account.Platform == PlatformOpenAI && account.Type == AccountTypeOAuth
}

// NeedsRefresh 检查token是否需要刷新
// expires_at 缺失且处于限流状态时需要刷新，防止限流期间 token 静默过期
func (r *OpenAITokenRefresher) NeedsRefresh(account *Record, refreshWindow time.Duration) bool {
	if account.IsOpenAIPersonalAccessToken() {
		return false
	}
	if strings.TrimSpace(account.GetOpenAIRefreshToken()) == "" {
		return false
	}
	expiresAt := account.GetCredentialAsTime("expires_at")
	if expiresAt == nil {
		return account.IsRateLimited()
	}

	return time.Until(*expiresAt) < refreshWindow
}

// Refresh 执行token刷新
// 保留原有credentials中的所有字段，只更新token相关字段
func (r *OpenAITokenRefresher) Refresh(ctx context.Context, account *Record) (map[string]any, error) {
	tokenInfo, err := r.Authorization.RefreshAccountToken(ctx, account)
	if err != nil {
		return nil, err
	}

	// 使用服务提供的方法构建新凭证，并保留原有字段
	newCredentials := BuildOpenAIAccountCredentials(tokenInfo)
	newCredentials = MergeCredentials(account.Credentials, newCredentials)
	newCredentials = NormalizeOpenAIPersonalAccessTokenCredentials(account, tokenInfo, newCredentials)

	return newCredentials, nil
}
