package service

import (
	"context"
	"log/slog"
	"time"

	accountcore "github.com/TokenFlux/TokenRouter/internal/account"
)

// ClaudeTokenCache token cache interface.
type ClaudeTokenCache = GeminiTokenCache

// ClaudeTokenProvider manages access_token for Claude OAuth and Vertex service account accounts.
type ClaudeTokenProvider struct {
	accountRepo   AccountRepository
	tokenCache    ClaudeTokenCache
	oauthService  *OAuthService
	refreshAPI    *OAuthRefreshAPI
	executor      OAuthRefreshExecutor
	refreshPolicy ProviderRefreshPolicy
}

func NewClaudeTokenProvider(
	accountRepo AccountRepository,
	tokenCache ClaudeTokenCache,
	oauthService *OAuthService,
) *ClaudeTokenProvider {
	return &ClaudeTokenProvider{
		accountRepo:   accountRepo,
		tokenCache:    tokenCache,
		oauthService:  oauthService,
		refreshPolicy: ClaudeProviderRefreshPolicy(),
	}
}

// SetRefreshAPI injects unified OAuth refresh API and executor.
func (p *ClaudeTokenProvider) SetRefreshAPI(api *OAuthRefreshAPI, executor OAuthRefreshExecutor) {
	p.refreshAPI = api
	p.executor = executor
}

// SetRefreshPolicy injects caller-side refresh policy.
func (p *ClaudeTokenProvider) SetRefreshPolicy(policy ProviderRefreshPolicy) {
	p.refreshPolicy = policy
}

func (p *ClaudeTokenProvider) GetAccessToken(ctx context.Context, account *Account) (string, error) {
	options := accountcore.ClaudeTokenOptions{Debug: slog.Debug, Warn: slog.Warn, Cache: p.tokenCache, Repository: legacyRefreshRepository(p.accountRepo), Policy: p.refreshPolicy, Vertex: func(ctx context.Context, value *accountcore.Record) (string, error) {
		return p.getServiceAccountAccessToken(ctx, AccountFromRecord(value))
	}}
	if p.refreshAPI != nil && p.executor != nil {
		options.Refresh = func(ctx context.Context, value *accountcore.Record, window time.Duration) (*accountcore.OAuthRefreshResult, error) {
			result, err := p.refreshAPI.RefreshIfNeeded(ctx, AccountFromRecord(value), p.executor, window)
			if result == nil {
				return nil, err
			}
			return &accountcore.OAuthRefreshResult{Refreshed: result.Refreshed, NewCredentials: result.NewCredentials, Account: AccountRecordView(result.Account), LockHeld: result.LockHeld}, err
		}
	}
	return accountcore.GetClaudeAccessToken(ctx, AccountRecordView(account), options)
}

func (p *ClaudeTokenProvider) getServiceAccountAccessToken(ctx context.Context, account *Account) (string, error) {
	return getVertexServiceAccountAccessToken(ctx, p.tokenCache, account)
}
