package service

import (
	"context"
	"time"

	accountcore "github.com/TokenFlux/TokenRouter/internal/account"
)

var errGrokOAuthRefreshNotConfigured = accountcore.ErrGrokOAuthRefreshNotConfigured
var errGrokOAuthRefreshTokenMissing = accountcore.ErrGrokOAuthRefreshTokenMissing
var errGrokOAuthAccessTokenMissing = accountcore.ErrGrokOAuthAccessTokenMissing
var errGrokOAuthAccessTokenExpired = accountcore.ErrGrokOAuthAccessTokenExpired
var errGrokOAuthConfiguredProxyMiss = accountcore.ErrGrokOAuthConfiguredProxyMiss

type GrokTokenCache = GeminiTokenCache

type GrokTokenProvider struct {
	accountRepo      AccountRepository
	tokenCache       GrokTokenCache
	refreshAPI       *OAuthRefreshAPI
	executor         OAuthRefreshExecutor
	refreshPolicy    ProviderRefreshPolicy
	tempUnschedCache TempUnschedCache
}

func NewGrokTokenProvider(
	accountRepo AccountRepository,
	tokenCache GrokTokenCache,
) *GrokTokenProvider {
	return &GrokTokenProvider{
		accountRepo:   accountRepo,
		tokenCache:    tokenCache,
		refreshPolicy: GrokProviderRefreshPolicy(),
	}
}

func (p *GrokTokenProvider) SetRefreshAPI(api *OAuthRefreshAPI, executor OAuthRefreshExecutor) {
	p.refreshAPI = api
	p.executor = executor
}

func (p *GrokTokenProvider) SetRefreshPolicy(policy ProviderRefreshPolicy) {
	p.refreshPolicy = policy
}

func (p *GrokTokenProvider) SetTempUnschedCache(cache TempUnschedCache) {
	p.tempUnschedCache = cache
}

func (p *GrokTokenProvider) GetAccessToken(ctx context.Context, account *Account) (string, error) {
	return p.coreSource().GetAccessToken(ctx, AccountRecordView(account))
}

func (p *GrokTokenProvider) GetAccessTokenForManualTest(ctx context.Context, account *Account) (string, error) {
	return p.coreSource().GetAccessTokenForManualTest(ctx, AccountRecordView(account))
}

func grokOAuthRequestAccountEligibilityError(account *Account) error {
	return accountcore.GrokOAuthRequestAccountEligibilityError(AccountRecordView(account))
}

func (p *GrokTokenProvider) InvalidateToken(ctx context.Context, account *Account) error {
	return p.coreSource().InvalidateToken(ctx, AccountRecordView(account))
}

func GrokTokenCacheKey(account *Account) string {
	return accountcore.GrokTokenCacheKey(AccountRecordView(account))
}

// 适配已有刷新实例；不复制锁，也不重新进行 token 交换。
func (p *GrokTokenProvider) coreSource() *accountcore.GrokTokenSource {
	if p == nil {
		return nil
	}
	source := &accountcore.GrokTokenSource{Cache: p.tokenCache, Repository: legacyRefreshRepository(p.accountRepo), Policy: p.refreshPolicy}
	if p.refreshAPI != nil && p.executor != nil {
		source.Refresh = func(ctx context.Context, value *accountcore.Record, window time.Duration) (*accountcore.OAuthRefreshResult, error) {
			result, err := p.refreshAPI.RefreshIfNeeded(ctx, AccountFromRecord(value), p.executor, window)
			if result == nil {
				return nil, err
			}
			return &accountcore.OAuthRefreshResult{Refreshed: result.Refreshed, NewCredentials: result.NewCredentials, Account: AccountRecordView(result.Account), LockHeld: result.LockHeld}, err
		}
	}
	return source
}
