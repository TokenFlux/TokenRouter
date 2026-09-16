package service

import (
	"context"
	"time"

	accountcore "github.com/TokenFlux/TokenRouter/internal/account"
)

type GeminiTokenRefresher struct {
	geminiOAuthService *GeminiOAuthService
}

func NewGeminiTokenRefresher(geminiOAuthService *GeminiOAuthService) *GeminiTokenRefresher {
	return &GeminiTokenRefresher{geminiOAuthService: geminiOAuthService}
}

// CacheKey 返回用于分布式锁的缓存键
func (r *GeminiTokenRefresher) CacheKey(account *Account) string {
	return GeminiTokenCacheKey(account)
}

func (r *GeminiTokenRefresher) CanRefresh(account *Account) bool {
	return accountcore.CanRefreshGemini(AccountRecordView(account))
}

func (r *GeminiTokenRefresher) NeedsRefresh(account *Account, refreshWindow time.Duration) bool {
	return accountcore.NeedsRefreshGemini(AccountRecordView(account), refreshWindow)
}

func (r *GeminiTokenRefresher) Refresh(ctx context.Context, account *Account) (map[string]any, error) {
	return accountcore.RefreshGeminiCredentials(ctx, AccountRecordView(account), r.geminiOAuthService.GeminiAuthorization)
}
