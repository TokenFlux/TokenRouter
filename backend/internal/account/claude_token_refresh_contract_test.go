//go:build unit

package account

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	"github.com/TokenFlux/TokenRouter/internal/routing/capability"
	"github.com/stretchr/testify/require"
)

// claudeAccountRepoStub is a minimal stub implementing only the methods used by ClaudeTokenProvider
type claudeAccountRepoStub struct {
	account      *Record
	getErr       error
	updateErr    error
	getCalled    int32
	updateCalled int32
}

func (r *claudeAccountRepoStub) GetByID(ctx context.Context, id int64) (*Record, error) {
	atomic.AddInt32(&r.getCalled, 1)
	if r.getErr != nil {
		return nil, r.getErr
	}
	return r.account, nil
}

func (r *claudeAccountRepoStub) Update(ctx context.Context, account *Record) error {
	atomic.AddInt32(&r.updateCalled, 1)
	if r.updateErr != nil {
		return r.updateErr
	}
	r.account = account
	return nil
}

// claudeOAuthServiceStub implements OAuthService methods for testing
type claudeOAuthServiceStub struct {
	tokenInfo     *ClaudeTokenInfo
	refreshErr    error
	refreshCalled int32
}

func (s *claudeOAuthServiceStub) RefreshAccountToken(ctx context.Context, account *Record) (*ClaudeTokenInfo, error) {
	atomic.AddInt32(&s.refreshCalled, 1)
	if s.refreshErr != nil {
		return nil, s.refreshErr
	}
	return s.tokenInfo, nil
}

func TestClaudeTokenProvider_TokenRefresh(t *testing.T) {
	cache := newClaudeTokenCacheStub()
	accountRepo := &claudeAccountRepoStub{}
	oauthService := &claudeOAuthServiceStub{
		tokenInfo: &ClaudeTokenInfo{
			AccessToken:  "refreshed-token",
			RefreshToken: "new-refresh-token",
			TokenType:    "Bearer",
			ExpiresIn:    3600,
			ExpiresAt:    time.Now().Add(time.Hour).Unix(),
		},
	}

	// Token expires soon (within refresh skew)
	expiresAt := time.Now().Add(1 * time.Minute).Format(time.RFC3339)
	account := &Record{
		ID:       102,
		Platform: capability.PlatformAnthropic,
		Type:     capability.AccountTypeOAuth,
		Status:   StatusActive,
		Credentials: map[string]any{
			"access_token":  "old-token",
			"refresh_token": "old-refresh-token",
			"expires_at":    expiresAt,
		},
	}
	accountRepo.account = account

	provider := newClaudeRefreshSourceFixture(accountRepo, cache, oauthService)

	token, err := provider.GetAccessToken(context.Background(), account)
	require.NoError(t, err)
	require.Equal(t, "refreshed-token", token)
	require.Equal(t, int32(1), atomic.LoadInt32(&oauthService.refreshCalled))
}

func TestClaudeTokenProvider_LockRaceCondition(t *testing.T) {
	cache := newClaudeTokenCacheStub()
	cache.simulateLockRace = true
	accountRepo := &claudeAccountRepoStub{}

	// Token expires soon
	expiresAt := time.Now().Add(1 * time.Minute).Format(time.RFC3339)
	account := &Record{
		ID:       103,
		Platform: capability.PlatformAnthropic,
		Type:     capability.AccountTypeOAuth,
		Status:   StatusActive,
		Credentials: map[string]any{
			"access_token": "race-token",
			"expires_at":   expiresAt,
		},
	}
	accountRepo.account = account

	// Simulate another worker already refreshed and cached
	cacheKey := ClaudeTokenCacheKey(account)
	go func() {
		time.Sleep(5 * time.Millisecond)
		cache.mu.Lock()
		cache.tokens[cacheKey] = "winner-token"
		cache.mu.Unlock()
	}()

	provider := newClaudeRefreshSourceFixture(accountRepo, cache, nil)

	token, err := provider.GetAccessToken(context.Background(), account)
	require.NoError(t, err)
	require.NotEmpty(t, token)
}

func TestClaudeTokenProvider_RefreshError(t *testing.T) {
	cache := newClaudeTokenCacheStub()
	accountRepo := &claudeAccountRepoStub{}
	oauthService := &claudeOAuthServiceStub{
		refreshErr: errors.New("oauth refresh failed"),
	}

	// Token expires soon
	expiresAt := time.Now().Add(1 * time.Minute).Format(time.RFC3339)
	account := &Record{
		ID:       111,
		Platform: capability.PlatformAnthropic,
		Type:     capability.AccountTypeOAuth,
		Status:   StatusActive,
		Credentials: map[string]any{
			"access_token":  "old-token",
			"refresh_token": "old-refresh-token",
			"expires_at":    expiresAt,
		},
	}
	accountRepo.account = account

	provider := newClaudeRefreshSourceFixture(accountRepo, cache, oauthService)

	// Now with fallback behavior, should return existing token even if refresh fails
	token, err := provider.GetAccessToken(context.Background(), account)
	require.NoError(t, err)
	require.Equal(t, "old-token", token) // Fallback to existing token
}

func TestClaudeTokenProvider_OAuthServiceNotConfigured(t *testing.T) {
	cache := newClaudeTokenCacheStub()
	accountRepo := &claudeAccountRepoStub{}

	// Token expires soon
	expiresAt := time.Now().Add(1 * time.Minute).Format(time.RFC3339)
	account := &Record{
		ID:       112,
		Platform: capability.PlatformAnthropic,
		Type:     capability.AccountTypeOAuth,
		Status:   StatusActive,
		Credentials: map[string]any{
			"access_token": "old-token",
			"expires_at":   expiresAt,
		},
	}
	accountRepo.account = account

	provider := newClaudeRefreshSourceFixture(accountRepo, cache, nil)

	// Now with fallback behavior, should return existing token even if oauth service not configured
	token, err := provider.GetAccessToken(context.Background(), account)
	require.NoError(t, err)
	require.Equal(t, "old-token", token) // Fallback to existing token
}

func TestClaudeTokenProvider_AccountRepoGetError(t *testing.T) {
	cache := newClaudeTokenCacheStub()
	accountRepo := &claudeAccountRepoStub{
		getErr: errors.New("db connection failed"),
	}
	oauthService := &claudeOAuthServiceStub{
		tokenInfo: &ClaudeTokenInfo{
			AccessToken:  "refreshed-token",
			RefreshToken: "new-refresh",
			TokenType:    "Bearer",
			ExpiresIn:    3600,
			ExpiresAt:    time.Now().Add(time.Hour).Unix(),
		},
	}

	// Token expires soon
	expiresAt := time.Now().Add(1 * time.Minute).Format(time.RFC3339)
	account := &Record{
		ID:       113,
		Platform: capability.PlatformAnthropic,
		Type:     capability.AccountTypeOAuth,
		Status:   StatusActive,
		Credentials: map[string]any{
			"access_token":  "old-token",
			"refresh_token": "old-refresh",
			"expires_at":    expiresAt,
		},
	}

	provider := newClaudeRefreshSourceFixture(accountRepo, cache, oauthService)

	// 原生产协调器读取失败时不交换，调用方按原策略回退已有令牌。
	token, err := provider.GetAccessToken(context.Background(), account)
	require.NoError(t, err)
	require.Equal(t, "old-token", token)
	require.Zero(t, atomic.LoadInt32(&oauthService.refreshCalled))
	require.Zero(t, atomic.LoadInt32(&accountRepo.updateCalled))
	require.Equal(t, "old-refresh", account.Credentials["refresh_token"])
}

func TestClaudeTokenProvider_AccountUpdateError(t *testing.T) {
	cache := newClaudeTokenCacheStub()
	accountRepo := &claudeAccountRepoStub{
		updateErr: errors.New("db write failed"),
	}
	oauthService := &claudeOAuthServiceStub{
		tokenInfo: &ClaudeTokenInfo{
			AccessToken:  "refreshed-token",
			RefreshToken: "new-refresh",
			TokenType:    "Bearer",
			ExpiresIn:    3600,
			ExpiresAt:    time.Now().Add(time.Hour).Unix(),
		},
	}

	// Token expires soon
	expiresAt := time.Now().Add(1 * time.Minute).Format(time.RFC3339)
	account := &Record{
		ID:       114,
		Platform: capability.PlatformAnthropic,
		Type:     capability.AccountTypeOAuth,
		Status:   StatusActive,
		Credentials: map[string]any{
			"access_token":  "old-token",
			"refresh_token": "old-refresh",
			"expires_at":    expiresAt,
		},
	}
	accountRepo.account = account

	provider := newClaudeRefreshSourceFixture(accountRepo, cache, oauthService)

	// 持久化失败不能发布未保存的令牌，也不能污染传入的凭据快照。
	token, err := provider.GetAccessToken(context.Background(), account)
	require.NoError(t, err)
	require.Equal(t, "old-token", token)
	require.Equal(t, int32(1), atomic.LoadInt32(&oauthService.refreshCalled))
	require.Equal(t, int32(1), atomic.LoadInt32(&accountRepo.updateCalled))
	require.Equal(t, "old-token", accountRepo.account.Credentials["access_token"])
	require.Equal(t, "old-refresh", account.Credentials["refresh_token"])
}

func TestClaudeTokenProvider_RefreshPreservesExistingCredentials(t *testing.T) {
	cache := newClaudeTokenCacheStub()
	accountRepo := &claudeAccountRepoStub{}
	oauthService := &claudeOAuthServiceStub{
		tokenInfo: &ClaudeTokenInfo{
			AccessToken:  "new-access-token",
			RefreshToken: "new-refresh-token",
			TokenType:    "Bearer",
			ExpiresIn:    3600,
			ExpiresAt:    time.Now().Add(time.Hour).Unix(),
		},
	}

	// Token expires soon
	expiresAt := time.Now().Add(1 * time.Minute).Format(time.RFC3339)
	account := &Record{
		ID:       115,
		Platform: capability.PlatformAnthropic,
		Type:     capability.AccountTypeOAuth,
		Status:   StatusActive,
		Credentials: map[string]any{
			"access_token":  "old-access-token",
			"refresh_token": "old-refresh-token",
			"expires_at":    expiresAt,
			"custom_field":  "should-be-preserved",
			"organization":  "test-org",
		},
	}
	accountRepo.account = account

	provider := newClaudeRefreshSourceFixture(accountRepo, cache, oauthService)

	token, err := provider.GetAccessToken(context.Background(), account)
	require.NoError(t, err)
	require.Equal(t, "new-access-token", token)

	// Verify existing fields are preserved
	require.Equal(t, "should-be-preserved", accountRepo.account.Credentials["custom_field"])
	require.Equal(t, "test-org", accountRepo.account.Credentials["organization"])
	// Verify new fields are updated
	require.Equal(t, "new-access-token", accountRepo.account.Credentials["access_token"])
	require.Equal(t, "new-refresh-token", accountRepo.account.Credentials["refresh_token"])
}

func TestClaudeTokenProvider_DoubleCheckCacheAfterLock(t *testing.T) {
	cache := newClaudeTokenCacheStub()
	accountRepo := &claudeAccountRepoStub{}
	oauthService := &claudeOAuthServiceStub{
		tokenInfo: &ClaudeTokenInfo{
			AccessToken:  "refreshed-token",
			RefreshToken: "new-refresh",
			TokenType:    "Bearer",
			ExpiresIn:    3600,
			ExpiresAt:    time.Now().Add(time.Hour).Unix(),
		},
	}

	// Token expires soon
	expiresAt := time.Now().Add(1 * time.Minute).Format(time.RFC3339)
	account := &Record{
		ID:       116,
		Platform: capability.PlatformAnthropic,
		Type:     capability.AccountTypeOAuth,
		Status:   StatusActive,
		Credentials: map[string]any{
			"access_token": "old-token",
			"expires_at":   expiresAt,
		},
	}
	accountRepo.account = account
	cacheKey := ClaudeTokenCacheKey(account)

	// After lock is acquired, cache should have the token (simulating another worker)
	go func() {
		time.Sleep(5 * time.Millisecond)
		cache.mu.Lock()
		cache.tokens[cacheKey] = "cached-by-other-worker"
		cache.mu.Unlock()
	}()

	provider := newClaudeRefreshSourceFixture(accountRepo, cache, oauthService)

	token, err := provider.GetAccessToken(context.Background(), account)
	require.NoError(t, err)
	require.NotEmpty(t, token)
}
