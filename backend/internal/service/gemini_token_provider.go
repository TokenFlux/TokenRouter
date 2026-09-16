package service

import (
	"context"
	"log"
	"log/slog"
	"time"

	accountcore "github.com/TokenFlux/TokenRouter/internal/account"
)

// GeminiTokenProvider manages access_token for Gemini OAuth and Vertex service account accounts.
type GeminiTokenProvider struct {
	accountRepo        AccountRepository
	tokenCache         GeminiTokenCache
	geminiOAuthService *GeminiOAuthService
	refreshAPI         *OAuthRefreshAPI
	executor           OAuthRefreshExecutor
	refreshPolicy      ProviderRefreshPolicy
}

func NewGeminiTokenProvider(
	accountRepo AccountRepository,
	tokenCache GeminiTokenCache,
	geminiOAuthService *GeminiOAuthService,
) *GeminiTokenProvider {
	return &GeminiTokenProvider{
		accountRepo:        accountRepo,
		tokenCache:         tokenCache,
		geminiOAuthService: geminiOAuthService,
		refreshPolicy:      GeminiProviderRefreshPolicy(),
	}
}

// SetRefreshAPI injects unified OAuth refresh API and executor.
func (p *GeminiTokenProvider) SetRefreshAPI(api *OAuthRefreshAPI, executor OAuthRefreshExecutor) {
	p.refreshAPI = api
	p.executor = executor
}

// SetRefreshPolicy injects caller-side refresh policy.
func (p *GeminiTokenProvider) SetRefreshPolicy(policy ProviderRefreshPolicy) {
	p.refreshPolicy = policy
}

func (p *GeminiTokenProvider) GetAccessToken(ctx context.Context, account *Account) (string, error) {
	value := AccountRecordView(account)
	options := accountcore.GeminiTokenOptions{Debug: slog.Debug, Warn: slog.Warn, Cache: p.tokenCache, Repository: legacyRefreshRepository(p.accountRepo), Policy: p.refreshPolicy, Logf: log.Printf, Vertex: func(ctx context.Context, value *accountcore.Record) (string, error) {
		return p.getServiceAccountAccessToken(ctx, AccountFromRecord(value))
	}, Persist: func(ctx context.Context, value *accountcore.Record, credentials map[string]any) error {
		legacy := AccountFromRecord(value)
		err := persistAccountCredentials(ctx, p.accountRepo, legacy, credentials)
		value.Credentials = legacy.Credentials
		return err
	}}
	if p.geminiOAuthService != nil {
		options.Project = p.geminiOAuthService.FetchProject
		if p.geminiOAuthService.proxyRepo != nil {
			options.ResolveProxy = p.geminiOAuthService.Options.ResolveProxy
		}
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
	token, err := accountcore.GetGeminiAccessToken(ctx, value, options)
	if account != nil && value != nil {
		account.Credentials = value.Credentials
	}
	return token, err
}

func (p *GeminiTokenProvider) getServiceAccountAccessToken(ctx context.Context, account *Account) (string, error) {
	return getVertexServiceAccountAccessToken(ctx, p.tokenCache, account)
}

func GeminiTokenCacheKey(account *Account) string {
	if account != nil && account.Type == AccountTypeServiceAccount {
		if key, err := parseVertexServiceAccountKey(account); err == nil {
			return vertexServiceAccountCacheKey(account, key)
		}
	}
	return accountcore.GeminiOAuthTokenCacheKey(AccountRecordView(account))
}
