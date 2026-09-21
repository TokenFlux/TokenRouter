//go:build unit

package provider

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/TokenFlux/TokenRouter/internal/pkg/querycache"

	accountcore "github.com/TokenFlux/TokenRouter/internal/account"
	"github.com/TokenFlux/TokenRouter/internal/billing"
	"github.com/TokenFlux/TokenRouter/internal/routing/capability"
	xai "github.com/TokenFlux/TokenRouter/internal/upstream/grok"
	"github.com/stretchr/testify/require"
)

type grokTokenCacheForProviderTest struct {
	token        string
	setKey       string
	setToken     string
	setTTL       time.Duration
	lockResult   bool
	releaseCalls int
	deletedKeys  []string
	deleteErr    error
	getCalls     int
	mu           sync.Mutex
}

type grokCredentialRaceRepo struct {
	*tokenRefreshAccountRepo
	mu sync.RWMutex
}

func (r *grokCredentialRaceRepo) GetByID(ctx context.Context, id int64) (*accountcore.Record, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.tokenRefreshAccountRepo.GetByID(ctx, id)
}

func (r *grokCredentialRaceRepo) setAccount(account *accountcore.Record) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.accountsByID[account.ID] = account
}

func (c *grokTokenCacheForProviderTest) GetAccessToken(context.Context, string) (string, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.getCalls++
	if c.token == "" {
		return "", errors.New("not cached")
	}
	return c.token, nil
}

func (c *grokTokenCacheForProviderTest) SetAccessToken(_ context.Context, key string, token string, ttl time.Duration) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.setKey = key
	c.setToken = token
	c.setTTL = ttl
	return nil
}

func (c *grokTokenCacheForProviderTest) DeleteAccessToken(_ context.Context, key string) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.deletedKeys = append(c.deletedKeys, key)
	return c.deleteErr
}

func (c *grokTokenCacheForProviderTest) AcquireRefreshLock(context.Context, string, time.Duration) (bool, error) {
	return c.lockResult, nil
}

func (c *grokTokenCacheForProviderTest) ReleaseRefreshLock(context.Context, string) error {
	c.releaseCalls++
	return nil
}

func TestGrokTokenProviderRefreshesExpiredTokenOnRequestPath(t *testing.T) {
	t.Setenv(xai.EnvBaseURL, xai.DefaultCLIBaseURL)

	expiredAt := time.Now().Add(-time.Minute).UTC().Format(time.RFC3339)
	account := &accountcore.Record{
		ID:          54,
		Platform:    capability.PlatformGrok,
		Type:        capability.AccountTypeOAuth,
		Status:      billing.StatusActive,
		Schedulable: true,
		Credentials: map[string]any{
			"access_token":  "expired-access-token",
			"refresh_token": "refresh-token",
			"expires_at":    expiredAt,
			"base_url":      xai.DefaultCLIBaseURL,
			"client_id":     "client-id",
		},
	}
	repo := &tokenRefreshAccountRepo{}
	repo.accountsByID = map[int64]*accountcore.Record{54: account}
	cache := &grokTokenCacheForProviderTest{lockResult: true}
	oauthSvc := newGrokAuthorizationForTest(nil, &grokOAuthClientStub{
		refreshResponse: &xai.TokenResponse{
			AccessToken: "new-access-token",
			TokenType:   "Bearer",
			ExpiresIn:   3600,
		},
	})
	oauthSvc.Start()
	defer stopGrokAuthorizationForTest(t, oauthSvc)

	provider := newGrokTokenSourceForTest(repo, cache)
	bindGrokRefreshForTest(provider, newRefreshAPI(repo, cache), accountcore.NewGrokTokenRefresher(oauthSvc))

	token, err := provider.GetAccessToken(context.Background(), accountcore.CloneRecord(account))
	require.NoError(t, err)
	require.Equal(t, "new-access-token", token)
	require.Equal(t, 1, repo.updateCredentialsCalls)
	require.Equal(t, "new-access-token", repo.accountsByID[54].GetGrokAccessToken())
	require.Equal(t, "refresh-token", repo.accountsByID[54].GetGrokRefreshToken())
	require.Equal(t, xai.DefaultCLIBaseURL, GrokAccountBaseURL(repo.accountsByID[54]))
	require.Equal(t, "grok:account:54", cache.setKey)
	require.Equal(t, "new-access-token", cache.setToken)
	require.Greater(t, cache.setTTL, time.Duration(0))
	require.Equal(t, 1, cache.releaseCalls)
}

func TestGrokTokenProviderRefreshFailureUnschedulesWithRedactedReason(t *testing.T) {
	expiredAt := time.Now().Add(-time.Minute).UTC().Format(time.RFC3339)
	account := &accountcore.Record{
		ID:          55,
		Platform:    capability.PlatformGrok,
		Type:        capability.AccountTypeOAuth,
		Status:      billing.StatusActive,
		Schedulable: true,
		Credentials: map[string]any{
			"access_token":  "expired-access-token",
			"refresh_token": "refresh-token",
			"expires_at":    expiredAt,
			"base_url":      xai.DefaultCLIBaseURL,
		},
	}
	repo := &tokenRefreshAccountRepo{}
	repo.accountsByID = map[int64]*accountcore.Record{55: account}
	cache := &grokTokenCacheForProviderTest{lockResult: true}
	provider := newGrokTokenSourceForTest(repo, cache)
	bindGrokRefreshForTest(provider, newRefreshAPI(repo, cache), &tokenRefresherStub{
		err: errors.New("temporary refresh failure access_token=leaked-access refresh_token=leaked-refresh"),
	})

	token, err := provider.GetAccessToken(context.Background(), accountcore.CloneRecord(account))
	require.Error(t, err)
	require.Empty(t, token)
	require.Equal(t, 0, repo.setTempUnschedCalls)
	require.Equal(t, 0, repo.setErrorCalls)
}

func TestGrokTokenProviderLockHeldWaitsForRefreshedCacheAndNeverUsesExpiredToken(t *testing.T) {
	account := expiredGrokOAuthAccountForCredentialTest(56)
	baseRepo := &tokenRefreshAccountRepo{}
	baseRepo.accountsByID = map[int64]*accountcore.Record{account.ID: account}
	repo := &grokCredentialRaceRepo{tokenRefreshAccountRepo: baseRepo}
	cache := &grokTokenCacheForProviderTest{lockResult: false, token: "expired-access-token"}
	provider := newGrokTokenSourceForTest(repo, cache)
	bindGrokRefreshForTest(provider, newRefreshAPI(repo, cache), &tokenRefresherStub{})

	go func() {
		time.Sleep(40 * time.Millisecond)
		refreshed := *account
		refreshed.Credentials = querycache.ShallowMap(account.Credentials)
		refreshed.Credentials["access_token"] = "refreshed-after-lock"
		refreshed.Credentials["expires_at"] = time.Now().Add(time.Hour).UTC().Format(time.RFC3339)
		refreshed.Credentials["_token_version"] = time.Now().UnixMilli()
		repo.setAccount(&refreshed)
		cache.mu.Lock()
		cache.token = "refreshed-after-lock"
		cache.mu.Unlock()
	}()

	startedAt := time.Now()
	token, err := provider.GetAccessToken(context.Background(), accountcore.CloneRecord(account))
	require.NoError(t, err)
	require.Equal(t, "refreshed-after-lock", token)
	require.NotEqual(t, "expired-access-token", token)
	require.GreaterOrEqual(t, time.Since(startedAt), 25*time.Millisecond,
		"expired account metadata must prevent returning the old cached token")
}

func TestGrokTokenProviderLockHeldTimeoutDoesNotReturnExpiredToken(t *testing.T) {
	account := expiredGrokOAuthAccountForCredentialTest(57)
	repo := &tokenRefreshAccountRepo{}
	repo.accountsByID = map[int64]*accountcore.Record{account.ID: account}
	cache := &grokTokenCacheForProviderTest{lockResult: false}
	provider := newGrokTokenSourceForTest(repo, cache)
	bindGrokRefreshForTest(provider, newRefreshAPI(repo, cache), &tokenRefresherStub{})
	ctx, cancel := context.WithTimeout(context.Background(), 80*time.Millisecond)
	defer cancel()

	token, err := provider.GetAccessToken(ctx, accountcore.CloneRecord(account))
	require.Error(t, err)
	require.Empty(t, token)
}

func TestGrokTokenProviderLockHeldRejectsChangedTokenWithoutExpiry(t *testing.T) {
	account := expiredGrokOAuthAccountForCredentialTest(58)
	baseRepo := &tokenRefreshAccountRepo{}
	baseRepo.accountsByID = map[int64]*accountcore.Record{account.ID: account}
	repo := &grokCredentialRaceRepo{tokenRefreshAccountRepo: baseRepo}
	cache := &grokTokenCacheForProviderTest{lockResult: false, token: "expired-access-token"}
	provider := newGrokTokenSourceForTest(repo, cache)
	bindGrokRefreshForTest(provider, newRefreshAPI(repo, cache), &tokenRefresherStub{})

	go func() {
		time.Sleep(30 * time.Millisecond)
		refreshed := *account
		refreshed.Credentials = querycache.ShallowMap(account.Credentials)
		refreshed.Credentials["access_token"] = "changed-without-expiry"
		delete(refreshed.Credentials, "expires_at")
		refreshed.Credentials["_token_version"] = time.Now().UnixMilli()
		repo.setAccount(&refreshed)
		cache.mu.Lock()
		cache.token = "changed-without-expiry"
		cache.mu.Unlock()
	}()

	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	token, err := provider.GetAccessToken(ctx, accountcore.CloneRecord(account))

	require.Error(t, err)
	require.Empty(t, token, "an unbounded credential must not win the lock-held race")
}

func TestGrokTokenProviderLockHeldUsesVersionedDBTokenAndRepairsStaleCache(t *testing.T) {
	account := expiredGrokOAuthAccountForCredentialTest(60)
	baseRepo := &tokenRefreshAccountRepo{}
	baseRepo.accountsByID = map[int64]*accountcore.Record{account.ID: account}
	repo := &grokCredentialRaceRepo{tokenRefreshAccountRepo: baseRepo}
	cache := &grokTokenCacheForProviderTest{lockResult: false, token: "expired-access-token"}
	provider := newGrokTokenSourceForTest(repo, cache)
	bindGrokRefreshForTest(provider, newRefreshAPI(repo, cache), &tokenRefresherStub{})

	go func() {
		time.Sleep(30 * time.Millisecond)
		refreshed := *account
		refreshed.Credentials = querycache.ShallowMap(account.Credentials)
		refreshed.Credentials["access_token"] = "db-authoritative-token"
		refreshed.Credentials["expires_at"] = time.Now().Add(2 * time.Hour).UTC().Format(time.RFC3339)
		refreshed.Credentials["_token_version"] = time.Now().UnixMilli()
		repo.setAccount(&refreshed)
	}()

	token, err := provider.GetAccessToken(context.Background(), accountcore.CloneRecord(account))

	require.NoError(t, err)
	require.Equal(t, "db-authoritative-token", token)
	require.Equal(t, "db-authoritative-token", cache.setToken)
	require.Greater(t, cache.setTTL, time.Duration(0))
}

func TestGrokTokenProviderRejectsStaleDBTokenWithoutExpiry(t *testing.T) {
	expiresAt := time.Now().Add(2 * accountcore.GrokTokenRefreshSkew).UTC().Format(time.RFC3339)
	account := &accountcore.Record{
		ID:          59,
		Platform:    capability.PlatformGrok,
		Type:        capability.AccountTypeOAuth,
		Status:      billing.StatusActive,
		Schedulable: true,
		Credentials: map[string]any{
			"access_token":  "old-access-token",
			"refresh_token": "refresh-token",
			"expires_at":    expiresAt,
		},
	}
	latest := *account
	latest.Credentials = querycache.ShallowMap(account.Credentials)
	latest.Credentials["access_token"] = "new-access-token-without-expiry"
	latest.Credentials["_token_version"] = time.Now().UnixMilli()
	delete(latest.Credentials, "expires_at")
	repo := &tokenRefreshAccountRepo{}
	repo.accountsByID = map[int64]*accountcore.Record{account.ID: &latest}
	cache := &grokTokenCacheForProviderTest{}
	provider := newGrokTokenSourceForTest(repo, cache)

	token, err := provider.GetAccessToken(context.Background(), accountcore.CloneRecord(account))

	require.ErrorIs(t, err, accountcore.ErrGrokOAuthAccessTokenExpired)
	require.Empty(t, token)
}

// TestGrokTokenProviderManualTestBypassesSchedulingGate 复现 #4598：管理员必须
// 能对调度器当前排除的账号（手动关闭、限流、过载或临时冷却）运行“测试连接”，
// 同时生产请求路径仍应拒绝这些账号。
func TestGrokTokenProviderManualTestBypassesSchedulingGate(t *testing.T) {
	future := time.Now().Add(time.Hour)
	tests := []struct {
		name   string
		mutate func(*accountcore.Record)
	}{
		{name: "not schedulable", mutate: func(account *accountcore.Record) { account.Schedulable = false }},
		{name: "temporarily unschedulable", mutate: func(account *accountcore.Record) { account.TempUnschedulableUntil = &future }},
		{name: "rate limited", mutate: func(account *accountcore.Record) { account.RateLimitResetAt = &future }},
		{name: "overloaded", mutate: func(account *accountcore.Record) { account.OverloadUntil = &future }},
		{name: "disabled by error", mutate: func(account *accountcore.Record) { account.Status = accountcore.StatusError }},
	}

	for index, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			account := expiredGrokOAuthAccountForCredentialTest(int64(120 + index))
			account.Credentials["access_token"] = "still-valid-token"
			account.Credentials["expires_at"] = time.Now().Add(2 * accountcore.GrokTokenRefreshSkew).UTC().Format(time.RFC3339)
			tt.mutate(account)
			provider := newGrokTokenSourceForTest(&tokenRefreshAccountRepo{}, &grokTokenCacheForProviderTest{})

			_, requestErr := provider.GetAccessToken(context.Background(), accountcore.CloneRecord(account))
			require.ErrorIs(t, requestErr, accountcore.ErrRefreshAccountStateChanged)

			token, err := provider.GetAccessTokenForManualTest(context.Background(), accountcore.CloneRecord(account))
			require.NoError(t, err)
			require.Equal(t, "still-valid-token", token)
		})
	}
}

func TestGrokTokenProviderManualTestRefreshesExpiredTokenWhileUnschedulable(t *testing.T) {
	t.Setenv(xai.EnvBaseURL, xai.DefaultCLIBaseURL)

	expiredAt := time.Now().Add(-time.Minute).UTC().Format(time.RFC3339)
	account := &accountcore.Record{
		ID:          130,
		Platform:    capability.PlatformGrok,
		Type:        capability.AccountTypeOAuth,
		Status:      billing.StatusActive,
		Schedulable: false,
		Credentials: map[string]any{
			"access_token":  "expired-access-token",
			"refresh_token": "refresh-token",
			"expires_at":    expiredAt,
			"base_url":      xai.DefaultCLIBaseURL,
			"client_id":     "client-id",
		},
	}
	repo := &tokenRefreshAccountRepo{}
	repo.accountsByID = map[int64]*accountcore.Record{130: account}
	cache := &grokTokenCacheForProviderTest{lockResult: true}
	oauthSvc := newGrokAuthorizationForTest(nil, &grokOAuthClientStub{
		refreshResponse: &xai.TokenResponse{
			AccessToken: "manual-test-refreshed-token",
			TokenType:   "Bearer",
			ExpiresIn:   3600,
		},
	})
	oauthSvc.Start()
	defer stopGrokAuthorizationForTest(t, oauthSvc)

	provider := newGrokTokenSourceForTest(repo, cache)
	bindGrokRefreshForTest(provider, newRefreshAPI(repo, cache), accountcore.NewGrokTokenRefresher(oauthSvc))

	token, err := provider.GetAccessTokenForManualTest(context.Background(), accountcore.CloneRecord(account))
	require.NoError(t, err)
	require.Equal(t, "manual-test-refreshed-token", token)
	require.Equal(t, 1, repo.updateCredentialsCalls)
}

func TestGrokTokenProviderManualTestFallsBackToValidTokenOnRefreshFailure(t *testing.T) {
	account := &accountcore.Record{
		ID:          131,
		Platform:    capability.PlatformGrok,
		Type:        capability.AccountTypeOAuth,
		Status:      billing.StatusActive,
		Schedulable: false,
		Credentials: map[string]any{
			"access_token":  "near-expiry-token",
			"refresh_token": "refresh-token",

			"expires_at": time.Now().Add(10 * time.Minute).UTC().Format(time.RFC3339),
		},
	}
	repo := &tokenRefreshAccountRepo{}
	repo.accountsByID = map[int64]*accountcore.Record{131: account}
	cache := &grokTokenCacheForProviderTest{lockResult: true}
	provider := newGrokTokenSourceForTest(repo, cache)
	bindGrokRefreshForTest(provider, newRefreshAPI(repo, cache), &tokenRefresherStub{
		err: errors.New("upstream refresh unavailable"),
	})

	token, err := provider.GetAccessTokenForManualTest(context.Background(), accountcore.CloneRecord(account))
	require.NoError(t, err)
	require.Equal(t, "near-expiry-token", token)
}

func TestGrokTokenProviderManualTestReportsRefreshFailureWhenTokenExpired(t *testing.T) {
	account := expiredGrokOAuthAccountForCredentialTest(132)
	account.Schedulable = false
	repo := &tokenRefreshAccountRepo{}
	repo.accountsByID = map[int64]*accountcore.Record{account.ID: account}
	cache := &grokTokenCacheForProviderTest{lockResult: true}
	provider := newGrokTokenSourceForTest(repo, cache)
	bindGrokRefreshForTest(provider, newRefreshAPI(repo, cache), &tokenRefresherStub{
		err: errors.New("invalid_client: client credentials rejected"),
	})

	token, err := provider.GetAccessTokenForManualTest(context.Background(), accountcore.CloneRecord(account))
	require.Error(t, err)
	require.Empty(t, token)
	require.Contains(t, err.Error(), "invalid_client")
}

func TestGrokTokenProviderManualTestLockHeldWithExpiredTokenReturnsSpecificError(t *testing.T) {
	account := expiredGrokOAuthAccountForCredentialTest(133)
	repo := &tokenRefreshAccountRepo{}
	repo.accountsByID = map[int64]*accountcore.Record{account.ID: account}
	cache := &grokTokenCacheForProviderTest{lockResult: false}
	provider := newGrokTokenSourceForTest(repo, cache)
	bindGrokRefreshForTest(provider, newRefreshAPI(repo, cache), &tokenRefresherStub{})

	token, err := provider.GetAccessTokenForManualTest(context.Background(), accountcore.CloneRecord(account))
	require.Error(t, err)
	require.Empty(t, token)
	require.Contains(t, err.Error(), "refresh is already in progress")
}

func TestGrokTokenProviderManualTestRequiresRefreshToken(t *testing.T) {
	account := expiredGrokOAuthAccountForCredentialTest(134)
	delete(account.Credentials, "refresh_token")
	provider := newGrokTokenSourceForTest(&tokenRefreshAccountRepo{}, &grokTokenCacheForProviderTest{})

	token, err := provider.GetAccessTokenForManualTest(context.Background(), accountcore.CloneRecord(account))
	require.ErrorIs(t, err, accountcore.ErrGrokOAuthRefreshTokenMissing)
	require.Empty(t, token)
}

func TestGrokTokenProviderRejectsIneligibleSelectedAccountBeforeWarmCache(t *testing.T) {
	future := time.Now().Add(time.Hour)
	tests := []struct {
		name   string
		mutate func(*accountcore.Record)
	}{
		{name: "disabled", mutate: func(account *accountcore.Record) { account.Status = billing.StatusDisabled }},
		{name: "not schedulable", mutate: func(account *accountcore.Record) { account.Schedulable = false }},
		{name: "temporarily unschedulable", mutate: func(account *accountcore.Record) { account.TempUnschedulableUntil = &future }},
		{name: "rate limited", mutate: func(account *accountcore.Record) { account.RateLimitResetAt = &future }},
		{name: "overloaded", mutate: func(account *accountcore.Record) { account.OverloadUntil = &future }},
	}

	for index, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			account := expiredGrokOAuthAccountForCredentialTest(int64(90 + index))
			account.Credentials["access_token"] = "warm-cache-token"
			account.Credentials["expires_at"] = time.Now().Add(2 * accountcore.GrokTokenRefreshSkew).UTC().Format(time.RFC3339)
			tt.mutate(account)
			cache := &grokTokenCacheForProviderTest{token: "warm-cache-token"}
			provider := newGrokTokenSourceForTest(&tokenRefreshAccountRepo{}, cache)

			token, err := provider.GetAccessToken(context.Background(), accountcore.CloneRecord(account))

			require.ErrorIs(t, err, accountcore.ErrRefreshAccountStateChanged)
			require.Empty(t, token)
			require.Zero(t, cache.getCalls, "an ineligible selected account must be rejected before cache lookup")
		})
	}
}

// newGrokTokenSourceForTest 直接组合原生账号读取、缓存和令牌策略。
func newGrokTokenSourceForTest(repo accountcore.RefreshRepository, cache accountcore.AccessTokenCache) *accountcore.GrokTokenSource {
	return &accountcore.GrokTokenSource{Repository: repo, Cache: cache, Policy: accountcore.GrokProviderRefreshPolicy()}
}

// bindGrokRefreshForTest 只注入已有协调器，不复制锁、刷新或持久化算法。
func bindGrokRefreshForTest(source *accountcore.GrokTokenSource, refresh *accountcore.OAuthRefreshAPI, executor accountcore.OAuthRefreshExecutor) {
	source.Refresh = func(ctx context.Context, value *accountcore.Record, window time.Duration) (*accountcore.OAuthRefreshResult, error) {
		return refresh.RefreshIfNeeded(ctx, value, executor, window)
	}
}

func expiredGrokOAuthAccountForCredentialTest(id int64) *accountcore.Record {
	return &accountcore.Record{
		ID:          id,
		Platform:    capability.PlatformGrok,
		Type:        capability.AccountTypeOAuth,
		Status:      billing.StatusActive,
		Schedulable: true,
		Credentials: map[string]any{
			"access_token":  "expired-access-token",
			"refresh_token": "refresh-token",
			"expires_at":    time.Now().Add(-time.Minute).UTC().Format(time.RFC3339),
			"base_url":      xai.DefaultCLIBaseURL,
		},
	}
}
