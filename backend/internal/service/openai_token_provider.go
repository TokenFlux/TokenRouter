package service

import (
	"context"
	"log/slog"
	"time"

	accountcore "github.com/TokenFlux/TokenRouter/internal/account"
)

type OpenAITokenRuntimeMetrics = accountcore.OpenAITokenRuntimeMetrics

type openAITokenRuntimeMetricsStore = accountcore.OpenAITokenMetricsStore

// OpenAITokenCache token cache interface.
type OpenAITokenCache = GeminiTokenCache

// OpenAITokenProvider manages access_token for OpenAI OAuth accounts.
type OpenAITokenProvider struct {
	accountRepo        AccountRepository
	tokenCache         OpenAITokenCache
	openAIOAuthService *OpenAIOAuthService
	runtimeBlocker     AccountRuntimeBlocker
	metrics            *openAITokenRuntimeMetricsStore
	refreshAPI         *OAuthRefreshAPI
	executor           OAuthRefreshExecutor
	refreshPolicy      ProviderRefreshPolicy
}

func NewOpenAITokenProvider(
	accountRepo AccountRepository,
	tokenCache OpenAITokenCache,
	openAIOAuthService *OpenAIOAuthService,
) *OpenAITokenProvider {
	return &OpenAITokenProvider{
		accountRepo:        accountRepo,
		tokenCache:         tokenCache,
		openAIOAuthService: openAIOAuthService,
		metrics:            &openAITokenRuntimeMetricsStore{},
		refreshPolicy:      OpenAIProviderRefreshPolicy(),
	}
}

// SetRefreshAPI injects unified OAuth refresh API and executor.
func (p *OpenAITokenProvider) SetRefreshAPI(api *OAuthRefreshAPI, executor OAuthRefreshExecutor) {
	p.refreshAPI = api
	p.executor = executor
}

// SetRefreshPolicy injects caller-side refresh policy.
func (p *OpenAITokenProvider) SetRefreshPolicy(policy ProviderRefreshPolicy) {
	p.refreshPolicy = policy
}

func (p *OpenAITokenProvider) SetAccountRuntimeBlocker(blocker AccountRuntimeBlocker) {
	p.runtimeBlocker = blocker
}

func (p *OpenAITokenProvider) SnapshotRuntimeMetrics() OpenAITokenRuntimeMetrics {
	return p.coreSource().SnapshotRuntimeMetrics()
}

func (p *OpenAITokenProvider) ensureMetrics() {
	if p != nil && p.metrics == nil {
		p.metrics = &accountcore.OpenAITokenMetricsStore{}
	}
}

func (p *OpenAITokenProvider) GetAccessToken(ctx context.Context, account *Account) (string, error) {
	return p.coreSource().GetAccessToken(ctx, AccountRecordView(account))
}

// coreSource 的瞬时端口投影复用原缓存、刷新器和 metrics，不生成独立状态。
func (p *OpenAITokenProvider) coreSource() *accountcore.OpenAITokenSource {
	if p == nil {
		return nil
	}
	p.ensureMetrics()
	source := &accountcore.OpenAITokenSource{
		Cache:      p.tokenCache,
		Repository: legacyRefreshRepository(p.accountRepo),
		Metrics:    p.metrics,
		Policy:     p.refreshPolicy,
		Debug:      slog.Debug,
		Warn:       slog.Warn,
	}
	if p.accountRepo != nil {
		source.SetError = p.accountRepo.SetError
	}
	if p.runtimeBlocker != nil {
		source.Block = func(record *accountcore.Record, until time.Time, reason string) {
			p.runtimeBlocker.BlockAccountScheduling(AccountFromRecord(record), until, reason)
		}
	}
	if p.refreshAPI != nil && p.executor != nil {
		source.Refresh = func(ctx context.Context, record *accountcore.Record, window time.Duration) (*accountcore.OAuthRefreshResult, error) {
			result, err := p.refreshAPI.RefreshIfNeeded(ctx, AccountFromRecord(record), p.executor, window)
			if result == nil {
				return nil, err
			}
			return &accountcore.OAuthRefreshResult{Account: AccountRecordView(result.Account), Refreshed: result.Refreshed, NewCredentials: result.NewCredentials, LockHeld: result.LockHeld}, err
		}
	}
	return source
}
