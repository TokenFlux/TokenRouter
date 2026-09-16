package service

import (
	"context"
	"log/slog"
	"time"

	accountcore "github.com/TokenFlux/TokenRouter/internal/account"
)

// AntigravityTokenCache token cache interface.
type AntigravityTokenCache = GeminiTokenCache

// AntigravityTokenProvider manages access_token for antigravity accounts.
type AntigravityTokenProvider struct {
	accountRepo             AccountRepository
	tokenCache              AntigravityTokenCache
	antigravityOAuthService *AntigravityOAuthService
	tokenState              accountcore.AntigravityTokenState
	refreshAPI              *OAuthRefreshAPI
	executor                OAuthRefreshExecutor
	refreshPolicy           ProviderRefreshPolicy
	tempUnschedCache        TempUnschedCache // 用于同步更新 Redis 临时不可调度缓存
}

func NewAntigravityTokenProvider(
	accountRepo AccountRepository,
	tokenCache AntigravityTokenCache,
	antigravityOAuthService *AntigravityOAuthService,
) *AntigravityTokenProvider {
	return &AntigravityTokenProvider{
		accountRepo:             accountRepo,
		tokenCache:              tokenCache,
		antigravityOAuthService: antigravityOAuthService,
		refreshPolicy:           AntigravityProviderRefreshPolicy(),
	}
}

// SetRefreshAPI injects unified OAuth refresh API and executor.
func (p *AntigravityTokenProvider) SetRefreshAPI(api *OAuthRefreshAPI, executor OAuthRefreshExecutor) {
	p.refreshAPI = api
	p.executor = executor
}

// SetRefreshPolicy injects caller-side refresh policy.
func (p *AntigravityTokenProvider) SetRefreshPolicy(policy ProviderRefreshPolicy) {
	p.refreshPolicy = policy
}

// SetTempUnschedCache injects temp unschedulable cache for immediate scheduler sync.
func (p *AntigravityTokenProvider) SetTempUnschedCache(cache TempUnschedCache) {
	p.tempUnschedCache = cache
}

func (p *AntigravityTokenProvider) GetAccessToken(ctx context.Context, account *Account) (string, error) {
	value := AccountRecordView(account)
	options := accountcore.AntigravityTokenOptions{Cache: p.tokenCache, Repository: legacyRefreshRepository(p.accountRepo), Policy: p.refreshPolicy, TempUnschedCache: p.tempUnschedCache, Warn: slog.Warn, Debug: slog.Debug,
		Persist: func(ctx context.Context, value *accountcore.Record, credentials map[string]any) error {
			legacy := AccountFromRecord(value)
			err := persistAccountCredentials(ctx, p.accountRepo, legacy, credentials)
			value.Credentials = legacy.Credentials
			return err
		}}
	if p.accountRepo != nil {
		options.SetTempUnschedulable = p.accountRepo.SetTempUnschedulable
	}
	if p.antigravityOAuthService != nil {
		options.FillProject = p.antigravityOAuthService.AntigravityAuthorization.FillProjectID
	}
	if p.refreshAPI != nil && p.executor != nil {
		options.Refresh = func(ctx context.Context, value *accountcore.Record, window time.Duration) (*accountcore.OAuthRefreshResult, error) {
			result, err := p.refreshAPI.RefreshIfNeeded(ctx, AccountFromRecord(value), p.executor, window)
			if result == nil {
				return nil, err
			}
			return &accountcore.OAuthRefreshResult{Refreshed: result.Refreshed, NewCredentials: result.NewCredentials, Account: AccountRecordView(result.Account), LockHeld: result.LockHeld}, err
		}
	}
	token, err := p.tokenState.GetAccessToken(ctx, value, options)
	if account != nil && value != nil {
		account.Credentials = value.Credentials
	}
	return token, err
}
func AntigravityTokenCacheKey(account *Account) string {
	return accountcore.AntigravityTokenCacheKey(AccountRecordView(account))
}
