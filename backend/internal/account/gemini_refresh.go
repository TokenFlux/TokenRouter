// Gemini 刷新资格和凭据合并归账号，原生交换由同一授权实例提供。
package account

import (
	"context"
	"time"
)

// GeminiTokenRefresher 保留平台资格与合并规则，技术缓存身份由外层注入。
type GeminiTokenRefresher struct {
	Authorization *GeminiAuthorization
	Key           func(*Record) string
}

func (r *GeminiTokenRefresher) CanRefresh(value *Record) bool {
	return CanRefreshGemini(value)
}

func (r *GeminiTokenRefresher) NeedsRefresh(value *Record, window time.Duration) bool {
	return NeedsRefreshGemini(value, window)
}

func (r *GeminiTokenRefresher) CacheKey(value *Record) string {
	return r.Key(value)
}

func (r *GeminiTokenRefresher) Refresh(ctx context.Context, value *Record) (map[string]any, error) {
	return RefreshGeminiCredentials(ctx, value, r.Authorization)
}

func CanRefreshGemini(account *Record) bool {
	return account.Platform == PlatformGemini && account.Type == AccountTypeOAuth
}
func NeedsRefreshGemini(account *Record, refreshWindow time.Duration) bool {
	if !CanRefreshGemini(account) {
		return false
	}
	expiresAt := account.GetCredentialAsTime("expires_at")
	if expiresAt == nil {
		return false
	}
	return time.Until(*expiresAt) < refreshWindow
}
func RefreshGeminiCredentials(ctx context.Context, account *Record, authorization *GeminiAuthorization) (map[string]any, error) {
	tokenInfo, err := authorization.RefreshAccountToken(ctx, account)
	if err != nil {
		return nil, err
	}

	newCredentials := authorization.BuildAccountCredentials(tokenInfo)
	newCredentials = MergeCredentials(account.Credentials, newCredentials)

	return newCredentials, nil
}
