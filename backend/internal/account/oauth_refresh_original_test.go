//go:build unit

package account

import (
	"context"
	"errors"
	"log/slog"
	"reflect"
	"sync"
	"testing"
	"time"

	"github.com/TokenFlux/TokenRouter/internal/pkg/querycache"

	"github.com/TokenFlux/TokenRouter/internal/billing"
	"github.com/TokenFlux/TokenRouter/internal/routing/capability"

	"github.com/stretchr/testify/require"
)

// ---------- mock helpers ----------

// refreshAPIAccountRepo implements AccountRepository for OAuthRefreshAPI tests.
type refreshAPIAccountRepo struct {
	account * // returned by GetByID
	Record
	getByIDErr              error
	getByIDCalls            int
	getByIDErrAfterCall     int
	getByIDErrAfterCallErr  error
	updateErr               error
	updateCalls             int
	updateCredentialsCalls  int
	successCASCalls         int
	beforeSuccessCAS        func(*refreshAPIAccountRepo)
	lastExpectedCredentials map[string]any
	lastExpectedProxyID     *int64
}

func (r *refreshAPIAccountRepo) GetByID(_ context.Context, _ int64) (*Record, error) {
	r.getByIDCalls++
	if r.getByIDErrAfterCall > 0 && r.getByIDCalls >= r.getByIDErrAfterCall {
		return nil, r.getByIDErrAfterCallErr
	}
	if r.getByIDErr != nil {
		return nil, r.getByIDErr
	}
	return activeRefreshAPITestAccount(r.account), nil
}

func activeRefreshAPITestAccount(account *Record) *Record {
	if account == nil || account.Status != "" {
		return account
	}
	copy := *account
	copy.Status = billing.StatusActive
	return &copy
}

func (r *refreshAPIAccountRepo) Update(_ context.Context, _ *Record) error {
	r.updateCalls++
	return r.updateErr
}

func (r *refreshAPIAccountRepo) UpdateCredentials(_ context.Context, id int64, credentials map[string]any) error {
	r.updateCalls++
	r.updateCredentialsCalls++
	if r.updateErr != nil {
		return r.updateErr
	}
	if r.account == nil || r.account.ID != id {
		r.account = &Record{ID: id}
	}
	r.account.Credentials = querycache.ShallowMap(credentials)
	return nil
}

func (r *refreshAPIAccountRepo) UpdateGrokOAuthCredentialsIfUnchanged(
	_ context.Context,
	id int64,
	expectedCredentials map[string]any,
	expectedProxyID *int64,
	credentials map[string]any,
) (bool, error) {
	r.successCASCalls++
	r.lastExpectedCredentials = querycache.ShallowMap(expectedCredentials)
	if expectedProxyID != nil {
		proxyID := *expectedProxyID
		r.lastExpectedProxyID = &proxyID
	} else {
		r.lastExpectedProxyID = nil
	}
	if r.beforeSuccessCAS != nil {
		r.beforeSuccessCAS(r)
	}
	if r.updateErr != nil {
		return false, r.updateErr
	}
	if r.account == nil || r.account.ID != id || r.account.Platform != capability.PlatformGrok ||
		r.account.Type != capability.AccountTypeOAuth ||
		!reflect.DeepEqual(r.account.Credentials, expectedCredentials) ||
		!reflect.DeepEqual(r.account.ProxyID, expectedProxyID) {
		return false, nil
	}
	r.updateCalls++
	r.updateCredentialsCalls++
	r.account.Credentials = querycache.ShallowMap(credentials)
	return true, nil
}

// refreshAPIExecutorStub implements OAuthRefreshExecutor for tests.
type refreshAPIExecutorStub struct {
	needsRefresh  bool
	cannotRefresh bool
	credentials   map[string]any
	err           error
	refreshCalls  int
	canRefresh    func(*Record) bool
	onRefresh     func()
	delay         time.Duration
}

func (e *refreshAPIExecutorStub) CanRefresh(account *Record) bool {
	if e.cannotRefresh {
		return false
	}
	if e.canRefresh != nil {
		return e.canRefresh(account)
	}
	return true
}

func (e *refreshAPIExecutorStub) NeedsRefresh(_ *Record, _ time.Duration) bool {
	return e.needsRefresh
}

func (e *refreshAPIExecutorStub) Refresh(_ context.Context, _ *Record) (map[string]any, error) {
	e.refreshCalls++
	if e.delay > 0 {
		time.Sleep(e.delay)
	}
	if e.onRefresh != nil {
		e.onRefresh()
	}
	if e.err != nil {
		return nil, e.err
	}
	return e.credentials, nil
}

func (e *refreshAPIExecutorStub) CacheKey(account *Record) string {
	return "test:api:" + account.Platform
}

// refreshAPICacheStub implements GeminiTokenCache for OAuthRefreshAPI tests.
type refreshAPICacheStub struct {
	lockResult    bool
	lockErr       error
	releaseCalls  int
	releaseCtxErr error
	deleteCalls   int
	deleteKey     string
	deleteCtxErr  error
}

func (c *refreshAPICacheStub) GetAccessToken(context.Context, string) (string, error) {
	return "", nil
}

func (c *refreshAPICacheStub) SetAccessToken(context.Context, string, string, time.Duration) error {
	return nil
}

func (c *refreshAPICacheStub) DeleteAccessToken(ctx context.Context, key string) error {
	c.deleteCalls++
	c.deleteKey = key
	c.deleteCtxErr = ctx.Err()
	return nil
}

func (c *refreshAPICacheStub) AcquireRefreshLock(context.Context, string, time.Duration) (bool, error) {
	return c.lockResult, c.lockErr
}

func (c *refreshAPICacheStub) ReleaseRefreshLock(ctx context.Context, _ string) error {
	c.releaseCalls++
	c.releaseCtxErr = ctx.Err()
	return nil
}

// ========== RefreshIfNeeded tests ==========

func TestRefreshIfNeeded_Success(t *testing.T) {
	account := &Record{ID: 1, Platform: capability.PlatformAnthropic, Type: capability.AccountTypeOAuth, Status: billing.StatusActive}
	repo := &refreshAPIAccountRepo{account: account}
	cache := &refreshAPICacheStub{lockResult: true}
	executor := &refreshAPIExecutorStub{
		needsRefresh: true,
		credentials:  map[string]any{"access_token": "new-token"},
	}

	api := newOriginalRefreshAPI(repo, cache)
	result, err := api.RefreshIfNeeded(context.Background(), account, executor, 3*time.Minute)

	require.NoError(t, err)
	require.True(t, result.Refreshed)
	require.NotNil(t, result.NewCredentials)
	require.Equal(t, "new-token", result.NewCredentials["access_token"])
	require.NotNil(t, result.NewCredentials["_token_version"]) // version stamp set
	require.Equal(t, 1, repo.updateCalls)                      // DB updated
	require.Equal(t, 1, repo.updateCredentialsCalls)
	require.Equal(t, 1, cache.releaseCalls) // lock released
	require.Equal(t, 1, executor.refreshCalls)
}

func TestRefreshIfNeeded_UpdateCredentialsPreservesRateLimitState(t *testing.T) {
	resetAt := time.Now().Add(45 * time.Minute)
	account := &Record{
		ID:               11,
		Platform:         capability.PlatformGemini,
		Type:             capability.AccountTypeOAuth,
		Status:           billing.StatusActive,
		RateLimitResetAt: &resetAt,
	}
	repo := &refreshAPIAccountRepo{account: account}
	cache := &refreshAPICacheStub{lockResult: true}
	executor := &refreshAPIExecutorStub{
		needsRefresh: true,
		credentials:  map[string]any{"access_token": "safe-token"},
	}

	api := newOriginalRefreshAPI(repo, cache)
	result, err := api.RefreshIfNeeded(context.Background(), account, executor, 3*time.Minute)

	require.NoError(t, err)
	require.True(t, result.Refreshed)
	require.Equal(t, 1, repo.updateCredentialsCalls)
	require.NotNil(t, repo.account.RateLimitResetAt)
	require.WithinDuration(t, resetAt, *repo.account.RateLimitResetAt, time.Second)
}

func TestRefreshIfNeeded_LockHeld(t *testing.T) {
	account := &Record{ID: 2, Platform: capability.PlatformAnthropic, Status: billing.StatusActive}
	repo := &refreshAPIAccountRepo{account: account}
	cache := &refreshAPICacheStub{lockResult: false} // lock not acquired
	executor := &refreshAPIExecutorStub{needsRefresh: true}

	api := newOriginalRefreshAPI(repo, cache)
	result, err := api.RefreshIfNeeded(context.Background(), account, executor, 3*time.Minute)

	require.NoError(t, err)
	require.True(t, result.LockHeld)
	require.False(t, result.Refreshed)
	require.Equal(t, 0, repo.updateCalls)
	require.Equal(t, 0, executor.refreshCalls)
}

func TestRefreshIfNeeded_LockErrorDegrades(t *testing.T) {
	account := &Record{ID: 3, Platform: capability.PlatformGemini, Type: capability.AccountTypeOAuth, Status: billing.StatusActive}
	repo := &refreshAPIAccountRepo{account: account}
	cache := &refreshAPICacheStub{lockErr: errors.New("redis down")} // lock error
	executor := &refreshAPIExecutorStub{
		needsRefresh: true,
		credentials:  map[string]any{"access_token": "degraded-token"},
	}

	api := newOriginalRefreshAPI(repo, cache)
	result, err := api.RefreshIfNeeded(context.Background(), account, executor, 3*time.Minute)

	require.NoError(t, err)
	require.True(t, result.Refreshed)       // still refreshed (degraded mode)
	require.Equal(t, 1, repo.updateCalls)   // DB updated
	require.Equal(t, 0, cache.releaseCalls) // no lock to release
	require.Equal(t, 1, executor.refreshCalls)
}

func TestRefreshIfNeeded_NoCacheNoLock(t *testing.T) {
	account := &Record{ID: 4, Platform: capability.PlatformGemini, Type: capability.AccountTypeOAuth, Status: billing.StatusActive}
	repo := &refreshAPIAccountRepo{account: account}
	executor := &refreshAPIExecutorStub{
		needsRefresh: true,
		credentials:  map[string]any{"access_token": "no-cache-token"},
	}

	api := newOriginalRefreshAPI(repo, nil) // no cache = no lock
	result, err := api.RefreshIfNeeded(context.Background(), account, executor, 3*time.Minute)

	require.NoError(t, err)
	require.True(t, result.Refreshed)
	require.Equal(t, 1, repo.updateCalls)
}

func TestRefreshIfNeeded_AlreadyRefreshed(t *testing.T) {
	account := &Record{ID: 5, Platform: capability.PlatformAnthropic, Status: billing.StatusActive}
	repo := &refreshAPIAccountRepo{account: account}
	cache := &refreshAPICacheStub{lockResult: true}
	executor := &refreshAPIExecutorStub{needsRefresh: false} // already refreshed

	api := newOriginalRefreshAPI(repo, cache)
	result, err := api.RefreshIfNeeded(context.Background(), account, executor, 3*time.Minute)

	require.NoError(t, err)
	require.False(t, result.Refreshed)
	require.False(t, result.LockHeld)
	require.NotNil(t, result.Account) // returns fresh account
	require.Equal(t, 0, repo.updateCalls)
	require.Equal(t, 0, executor.refreshCalls)
}

func TestRefreshIfNeeded_RefreshError(t *testing.T) {
	account := &Record{ID: 6, Platform: capability.PlatformAnthropic, Status: billing.StatusActive}
	repo := &refreshAPIAccountRepo{account: account}
	cache := &refreshAPICacheStub{lockResult: true}
	executor := &refreshAPIExecutorStub{
		needsRefresh: true,
		err:          errors.New("invalid_grant: token revoked"),
	}

	api := newOriginalRefreshAPI(repo, cache)
	result, err := api.RefreshIfNeeded(context.Background(), account, executor, 3*time.Minute)

	require.Error(t, err)
	require.NotNil(t, result)
	require.NotNil(t, result.Account)
	require.Equal(t, account.ID, result.Account.ID)
	require.Contains(t, err.Error(), "invalid_grant")
	require.Equal(t, 0, repo.updateCalls)   // no DB update on refresh error
	require.Equal(t, 1, cache.releaseCalls) // lock still released via defer
}

func TestRefreshIfNeeded_DBUpdateError(t *testing.T) {
	account := &Record{ID: 7, Platform: capability.PlatformGemini, Type: capability.AccountTypeOAuth, Status: billing.StatusActive}
	repo := &refreshAPIAccountRepo{
		account:   account,
		updateErr: errors.New("db connection lost"),
	}
	cache := &refreshAPICacheStub{lockResult: true}
	executor := &refreshAPIExecutorStub{
		needsRefresh: true,
		credentials:  map[string]any{"access_token": "token"},
	}

	api := newOriginalRefreshAPI(repo, cache)
	result, err := api.RefreshIfNeeded(context.Background(), account, executor, 3*time.Minute)

	require.Error(t, err)
	require.Nil(t, result)
	require.ErrorIs(t, err, ErrRefreshCredentialPersist)
	require.Equal(t, 1, repo.updateCalls) // attempted
}

func TestRefreshIfNeeded_GrokSuccessCASLetsConcurrentReauthorizationWin(t *testing.T) {
	proxyID := int64(17)
	account := &Record{
		ID:       70,
		Platform: capability.PlatformGrok,
		Type:     capability.AccountTypeOAuth,
		Status:   billing.StatusActive,
		ProxyID:  &proxyID,
		Credentials: map[string]any{
			"access_token":   "attempted-access",
			"refresh_token":  "attempted-refresh",
			"_token_version": int64(1),
		},
	}
	repo := &refreshAPIAccountRepo{account: account}
	repo.beforeSuccessCAS = func(r *refreshAPIAccountRepo) {
		repairedProxyID := int64(23)
		r.account.ProxyID = &repairedProxyID
		r.account.Credentials = map[string]any{
			"access_token":   "reauthorized-access",
			"refresh_token":  "reauthorized-refresh",
			"_token_version": int64(2),
		}
	}
	executor := &refreshAPIExecutorStub{
		needsRefresh: true,
		credentials: map[string]any{
			"access_token":  "provider-access",
			"refresh_token": "provider-refresh",
		},
	}

	result, err := newOriginalRefreshAPI(repo, nil).RefreshIfNeeded(context.Background(), account, executor, time.Hour)

	require.NoError(t, err)
	require.NotNil(t, result)
	require.False(t, result.Refreshed, "a lost success CAS is an already-refreshed skip")
	require.Nil(t, result.NewCredentials)
	require.Equal(t, "reauthorized-refresh", result.Account.GetGrokRefreshToken())
	require.NotNil(t, result.Account.ProxyID)
	require.Equal(t, int64(23), *result.Account.ProxyID)
	require.Equal(t, 1, repo.successCASCalls)
	require.Equal(t, "attempted-refresh", repo.lastExpectedCredentials["refresh_token"])
	require.NotNil(t, repo.lastExpectedProxyID)
	require.Equal(t, proxyID, *repo.lastExpectedProxyID)
	require.Zero(t, repo.updateCredentialsCalls, "the provider result must not overwrite a concurrent repair")
}

func TestRefreshIfNeeded_GrokSuccessPersistenceFailureIsProviderContainment(t *testing.T) {
	account := &Record{
		ID:       71,
		Platform: capability.PlatformGrok,
		Type:     capability.AccountTypeOAuth,
		Status:   billing.StatusActive,
		Credentials: map[string]any{
			"access_token":  "attempted-access",
			"refresh_token": "attempted-refresh",
		},
	}
	repo := &refreshAPIAccountRepo{account: account, updateErr: errors.New("database unavailable")}
	executor := &refreshAPIExecutorStub{
		needsRefresh: true,
		credentials: map[string]any{
			"access_token":  "provider-access",
			"refresh_token": "provider-refresh",
		},
	}

	result, err := newOriginalRefreshAPI(repo, nil).RefreshIfNeeded(context.Background(), account, executor, time.Hour)

	require.Error(t, err)
	require.Nil(t, result)
	var containmentErr *ProviderCycleContainmentRefreshError
	require.ErrorAs(t, err, &containmentErr)
	require.Equal(t, "attempted-refresh", account.GetGrokRefreshToken(),
		"an ambiguous persistence result must not mutate the in-memory account")
	require.Equal(t, 1, repo.successCASCalls)
	require.Zero(t, repo.updateCredentialsCalls)
}

func TestRefreshIfNeeded_GrokSuccessDurableRereadFailureIsProviderContainment(t *testing.T) {
	account := &Record{
		ID:       72,
		Platform: capability.PlatformGrok,
		Type:     capability.AccountTypeOAuth,
		Status:   billing.StatusActive,
		Credentials: map[string]any{
			"access_token":  "attempted-access",
			"refresh_token": "attempted-refresh",
		},
	}
	repo := &refreshAPIAccountRepo{
		account:                account,
		getByIDErrAfterCall:    2,
		getByIDErrAfterCallErr: errors.New("durable state unavailable"),
	}
	cache := &refreshAPICacheStub{lockResult: true}
	executor := &refreshAPIExecutorStub{
		needsRefresh: true,
		credentials: map[string]any{
			"access_token":  "provider-access",
			"refresh_token": "provider-refresh",
		},
	}

	result, err := newOriginalRefreshAPI(repo, cache).RefreshIfNeeded(context.Background(), account, executor, time.Hour)

	require.Error(t, err)
	require.Nil(t, result)
	var containmentErr *ProviderCycleContainmentRefreshError
	require.ErrorAs(t, err, &containmentErr)
	require.Equal(t, 2, repo.getByIDCalls)
	require.Equal(t, 1, repo.successCASCalls)
	require.Equal(t, 1, cache.deleteCalls, "a committed credential rotation must invalidate the pre-rotation access-token cache")
	require.NoError(t, cache.deleteCtxErr)
}

func TestRefreshIfNeeded_DBRereadFails(t *testing.T) {
	account := &Record{ID: 8, Platform: capability.PlatformAnthropic, Type: capability.AccountTypeOAuth, Status: billing.StatusActive}
	repo := &refreshAPIAccountRepo{
		account:    nil, // GetByID returns nil
		getByIDErr: errors.New("db timeout"),
	}
	cache := &refreshAPICacheStub{lockResult: true}
	executor := &refreshAPIExecutorStub{
		needsRefresh: true,
		credentials:  map[string]any{"access_token": "fallback-token"},
	}

	api := newOriginalRefreshAPI(repo, cache)
	result, err := api.RefreshIfNeeded(context.Background(), account, executor, 3*time.Minute)

	require.Error(t, err)
	var stateUnavailable *RefreshStateUnavailableError
	require.ErrorAs(t, err, &stateUnavailable)
	require.Nil(t, result)
	require.Zero(t, executor.refreshCalls, "a failed DB reread must not refresh stale credentials")
	require.Zero(t, repo.updateCalls)
	require.Equal(t, 1, cache.releaseCalls)
}

func TestRefreshIfNeeded_RequestPathDBRereadNilFailsClosed(t *testing.T) {
	account := &Record{ID: 81, Platform: capability.PlatformGrok, Type: capability.AccountTypeOAuth, Status: billing.StatusActive, Schedulable: true}
	repo := &refreshAPIAccountRepo{}
	cache := &refreshAPICacheStub{lockResult: true}
	executor := &refreshAPIExecutorStub{needsRefresh: true}

	api := newOriginalRefreshAPI(repo, cache)
	result, err := api.RefreshIfNeeded(WithRefreshRequestPath(context.Background()), account, executor, 3*time.Minute)

	require.ErrorIs(t, err, ErrRefreshAccountStateChanged)
	require.Nil(t, result)
	require.Zero(t, executor.refreshCalls)
	require.Zero(t, repo.updateCalls)
	require.Equal(t, 1, cache.releaseCalls)
}

func TestRefreshIfNeeded_RequestPathDBRereadInactiveFailsClosed(t *testing.T) {
	account := &Record{ID: 82, Platform: capability.PlatformGrok, Type: capability.AccountTypeOAuth, Status: billing.StatusActive, Schedulable: true}
	freshAccount := &Record{ID: account.ID, Platform: capability.PlatformGrok, Type: capability.AccountTypeOAuth, Status: billing.StatusDisabled}
	repo := &refreshAPIAccountRepo{account: freshAccount}
	executor := &refreshAPIExecutorStub{needsRefresh: true}

	api := newOriginalRefreshAPI(repo, nil)
	result, err := api.RefreshIfNeeded(WithRefreshRequestPath(context.Background()), account, executor, 3*time.Minute)

	require.ErrorContains(t, err, "account is not active")
	require.Nil(t, result)
	require.Zero(t, executor.refreshCalls)
	require.Zero(t, repo.updateCalls)
}

func TestRefreshIfNeeded_RequestPathDBRereadRevalidatesExecutorContract(t *testing.T) {
	tests := []struct {
		name          string
		freshPlatform string
		freshType     string
	}{
		{name: "platform changed", freshPlatform: capability.PlatformAnthropic, freshType: capability.AccountTypeOAuth},
		{name: "type changed", freshPlatform: capability.PlatformGrok, freshType: capability.AccountTypeUpstream},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			account := &Record{ID: 83, Platform: capability.PlatformGrok, Type: capability.AccountTypeOAuth, Status: billing.StatusActive, Schedulable: true}
			freshAccount := &Record{ID: account.ID, Platform: tt.freshPlatform, Type: tt.freshType, Status: billing.StatusActive, Schedulable: true}
			repo := &refreshAPIAccountRepo{account: freshAccount}
			executor := NewGrokTokenRefresher(nil)

			api := newOriginalRefreshAPI(repo, nil)
			result, err := api.RefreshIfNeeded(WithRefreshRequestPath(context.Background()), account, executor, 3*time.Minute)

			require.ErrorIs(t, err, ErrRefreshAccountStateChanged)
			require.Nil(t, result)
			require.Zero(t, repo.updateCalls)
		})
	}
}

func TestRefreshIfNeeded_ReleasesDistributedLockAfterParentCancellation(t *testing.T) {
	account := &Record{ID: 81, Platform: capability.PlatformGrok, Type: capability.AccountTypeOAuth, Status: billing.StatusActive}
	repo := &refreshAPIAccountRepo{account: account}
	cache := &refreshAPICacheStub{lockResult: true}
	ctx, cancel := context.WithCancel(context.Background())
	executor := &refreshAPIExecutorStub{
		needsRefresh: true,
		err:          errors.New("temporary provider error"),
		onRefresh:    cancel,
	}
	api := newOriginalRefreshAPI(repo, cache)

	_, err := api.RefreshIfNeeded(ctx, account, executor, time.Hour)

	require.Error(t, err)
	require.Equal(t, 1, cache.releaseCalls)
	require.NoError(t, cache.releaseCtxErr, "lock cleanup must not reuse the canceled attempt context")
}

func TestRefreshIfNeeded_RevalidatesFreshAccountBeforeRefresh(t *testing.T) {
	selected := &Record{ID: 82, Platform: capability.PlatformGrok, Type: capability.AccountTypeOAuth, Status: billing.StatusActive}
	tests := []struct {
		name  string
		fresh *Record
	}{
		{name: "converted to API key", fresh: &Record{ID: 82, Platform: capability.PlatformGrok, Type: capability.AccountTypeAPIKey, Status: billing.StatusActive}},
		{name: "disabled", fresh: &Record{ID: 82, Platform: capability.PlatformGrok, Type: capability.AccountTypeOAuth, Status: billing.StatusDisabled}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			repo := &refreshAPIAccountRepo{account: tt.fresh}
			executor := &refreshAPIExecutorStub{
				needsRefresh: true,
				canRefresh: func(account *Record) bool {
					return account.Platform == capability.PlatformGrok && account.Type == capability.AccountTypeOAuth
				},
			}
			api := newOriginalRefreshAPI(repo, nil)

			result, err := api.RefreshIfNeeded(context.Background(), selected, executor, time.Hour)

			require.NoError(t, err)
			require.False(t, result.Refreshed)
			require.Zero(t, executor.refreshCalls)
			require.Zero(t, repo.updateCalls)
		})
	}
}

func TestRefreshIfNeeded_RequestPathDBRereadMissingGrokRefreshCredentialReturnsPermanentSignal(t *testing.T) {
	account := &Record{
		ID:          84,
		Platform:    capability.PlatformGrok,
		Type:        capability.AccountTypeOAuth,
		Status:      billing.StatusActive,
		Schedulable: true,
		Credentials: map[string]any{
			"refresh_token": "caller-snapshot-refresh-token",
		},
	}
	freshAccount := &Record{ID: account.ID, Platform: capability.PlatformGrok, Type: capability.AccountTypeOAuth, Status: billing.StatusActive, Schedulable: true}
	repo := &refreshAPIAccountRepo{account: freshAccount}
	executor := NewGrokTokenRefresher(nil)

	api := newOriginalRefreshAPI(repo, nil)
	result, err := api.RefreshIfNeeded(WithRefreshRequestPath(context.Background()), account, executor, 3*time.Minute)

	require.ErrorIs(t, err, ErrGrokOAuthRefreshTokenMissing)
	require.Nil(t, result)
	require.Zero(t, repo.updateCalls)
}

func TestRefreshIfNeeded_LateSuccessAfterDeadlineDoesNotPersist(t *testing.T) {
	account := &Record{ID: 85, Platform: capability.PlatformGrok, Type: capability.AccountTypeOAuth, Status: billing.StatusActive}
	repo := &refreshAPIAccountRepo{account: account}
	executor := &refreshAPIExecutorStub{
		needsRefresh: true,
		credentials:  map[string]any{"access_token": "late-token"},
		delay:        30 * time.Millisecond,
	}
	api := newOriginalRefreshAPI(repo, nil)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Millisecond)
	defer cancel()

	result, err := api.RefreshIfNeeded(ctx, account, executor, time.Hour)

	require.ErrorIs(t, err, context.DeadlineExceeded)
	require.Nil(t, result)
	require.Zero(t, repo.updateCredentialsCalls, "late credentials must not cross the unified API persistence boundary")
}

func TestRefreshIfNeeded_NilCredentials(t *testing.T) {
	account := &Record{ID: 9, Platform: capability.PlatformGemini, Type: capability.AccountTypeOAuth, Status: billing.StatusActive}
	repo := &refreshAPIAccountRepo{account: account}
	cache := &refreshAPICacheStub{lockResult: true}
	executor := &refreshAPIExecutorStub{
		needsRefresh: true,
		credentials:  nil, // Refresh returns nil credentials
	}

	api := newOriginalRefreshAPI(repo, cache)
	result, err := api.RefreshIfNeeded(context.Background(), account, executor, 3*time.Minute)

	require.NoError(t, err)
	require.True(t, result.Refreshed)
	require.Nil(t, result.NewCredentials)
	require.Equal(t, 0, repo.updateCalls) // no DB update when credentials are nil
}

// ========== MergeCredentials tests ==========

func TestMergeCredentials_Basic(t *testing.T) {
	old := map[string]any{"a": "1", "b": "2", "c": "3"}
	new := map[string]any{"a": "new", "d": "4"}

	result := MergeCredentials(old, new)

	require.Equal(t, "new", result["a"]) // new value preserved
	require.Equal(t, "2", result["b"])   // old value kept
	require.Equal(t, "3", result["c"])   // old value kept
	require.Equal(t, "4", result["d"])   // new value preserved
}

func TestMergeCredentials_NilNew(t *testing.T) {
	old := map[string]any{"a": "1"}

	result := MergeCredentials(old, nil)

	require.NotNil(t, result)
	require.Equal(t, "1", result["a"])
}

func TestMergeCredentials_NilOld(t *testing.T) {
	new := map[string]any{"a": "1"}

	result := MergeCredentials(nil, new)

	require.Equal(t, "1", result["a"])
}

func TestMergeCredentials_BothNil(t *testing.T) {
	result := MergeCredentials(nil, nil)
	require.NotNil(t, result)
	require.Empty(t, result)
}

func TestMergeCredentials_NewOverridesOld(t *testing.T) {
	old := map[string]any{"access_token": "old-token", "refresh_token": "old-refresh"}
	new := map[string]any{"access_token": "new-token"}

	result := MergeCredentials(old, new)

	require.Equal(t, "new-token", result["access_token"])    // overridden
	require.Equal(t, "old-refresh", result["refresh_token"]) // preserved
}

// ========== BuildClaudeAccountCredentials tests ==========

func TestBuildClaudeAccountCredentials_Full(t *testing.T) {
	tokenInfo := &ClaudeTokenInfo{
		AccessToken:  "at-123",
		TokenType:    "Bearer",
		ExpiresIn:    3600,
		ExpiresAt:    1700000000,
		RefreshToken: "rt-456",
		Scope:        "openid",
	}

	creds := BuildClaudeAccountCredentials(tokenInfo)

	require.Equal(t, "at-123", creds["access_token"])
	require.Equal(t, "Bearer", creds["token_type"])
	require.Equal(t, "3600", creds["expires_in"])
	require.Equal(t, "1700000000", creds["expires_at"])
	require.Equal(t, "rt-456", creds["refresh_token"])
	require.Equal(t, "openid", creds["scope"])
}

func TestBuildClaudeAccountCredentials_Minimal(t *testing.T) {
	tokenInfo := &ClaudeTokenInfo{
		AccessToken: "at-789",
		TokenType:   "Bearer",
		ExpiresIn:   7200,
		ExpiresAt:   1700003600,
	}

	creds := BuildClaudeAccountCredentials(tokenInfo)

	require.Equal(t, "at-789", creds["access_token"])
	require.Equal(t, "Bearer", creds["token_type"])
	require.Equal(t, "7200", creds["expires_in"])
	require.Equal(t, "1700003600", creds["expires_at"])
	_, hasRefresh := creds["refresh_token"]
	_, hasScope := creds["scope"]
	require.False(t, hasRefresh, "refresh_token should not be set when empty")
	require.False(t, hasScope, "scope should not be set when empty")
}

// refreshAPIAccountRepoWithRace supports returning a different account on subsequent GetByID calls
// to simulate race conditions where another worker has refreshed the token.
type refreshAPIAccountRepoWithRace struct {
	refreshAPIAccountRepo
	raceAccount * // returned on 2nd+ GetByID call
	Record
	getByIDCalls int
}

func (r *refreshAPIAccountRepoWithRace) GetByID(_ context.Context, _ int64) (*Record, error) {
	r.getByIDCalls++
	if r.getByIDCalls > 1 && r.raceAccount != nil {
		return activeRefreshAPITestAccount(r.raceAccount), nil
	}
	if r.getByIDErr != nil {
		return nil, r.getByIDErr
	}
	return activeRefreshAPITestAccount(r.account), nil
}

// ========== Race recovery tests ==========

func TestRefreshIfNeeded_InvalidGrantRaceRecovered(t *testing.T) {
	// Account with old refresh token
	account := &Record{
		ID:          10,
		Platform:    capability.PlatformAnthropic,
		Type:        capability.AccountTypeOAuth,
		Status:      billing.StatusActive,
		Credentials: map[string]any{"refresh_token": "old-rt", "access_token": "old-at"},
	}
	// After race, DB has new refresh token from another worker
	racedAccount := &Record{
		ID:          10,
		Platform:    capability.PlatformAnthropic,
		Type:        capability.AccountTypeOAuth,
		Status:      billing.StatusActive,
		Credentials: map[string]any{"refresh_token": "new-rt", "access_token": "new-at"},
	}
	repo := &refreshAPIAccountRepoWithRace{
		refreshAPIAccountRepo: refreshAPIAccountRepo{account: account},
		raceAccount:           racedAccount,
	}
	cache := &refreshAPICacheStub{lockResult: true}
	executor := &refreshAPIExecutorStub{
		needsRefresh: true,
		err:          errors.New("invalid_grant: refresh token not found or invalid"),
	}

	api := newOriginalRefreshAPI(repo, cache)
	result, err := api.RefreshIfNeeded(context.Background(), account, executor, 3*time.Minute)

	require.NoError(t, err, "race-recovered invalid_grant should not return error")
	require.False(t, result.Refreshed)
	require.False(t, result.LockHeld)
	require.NotNil(t, result.Account)
	require.Equal(t, "new-rt", result.Account.GetCredential("refresh_token"))
	require.Equal(t, 0, repo.updateCalls) // no DB update needed, another worker did it
}

func TestRefreshIfNeeded_InvalidGrantGenuine(t *testing.T) {
	// Account with revoked refresh token - DB still has the same token
	account := &Record{
		ID:          11,
		Platform:    capability.PlatformAnthropic,
		Type:        capability.AccountTypeOAuth,
		Status:      billing.StatusActive,
		Credentials: map[string]any{"refresh_token": "revoked-rt", "access_token": "old-at"},
	}
	repo := &refreshAPIAccountRepoWithRace{
		refreshAPIAccountRepo: refreshAPIAccountRepo{account: account},
		raceAccount:           account, // same refresh_token on re-read
	}
	cache := &refreshAPICacheStub{lockResult: true}
	executor := &refreshAPIExecutorStub{
		needsRefresh: true,
		err:          errors.New("invalid_grant: refresh token revoked"),
	}

	api := newOriginalRefreshAPI(repo, cache)
	result, err := api.RefreshIfNeeded(context.Background(), account, executor, 3*time.Minute)

	require.Error(t, err, "genuine invalid_grant should propagate error")
	require.NotNil(t, result)
	require.NotNil(t, result.Account)
	require.Equal(t, "revoked-rt", result.Account.GetCredential("refresh_token"))
	require.Contains(t, err.Error(), "invalid_grant")
}

func TestRefreshIfNeeded_InvalidGrantDBRereadFailsOnRecovery(t *testing.T) {
	account := &Record{
		ID:          12,
		Platform:    capability.PlatformAnthropic,
		Type:        capability.AccountTypeOAuth,
		Status:      billing.StatusActive,
		Credentials: map[string]any{"refresh_token": "old-rt"},
	}
	repo := &refreshAPIAccountRepoWithRace{
		refreshAPIAccountRepo: refreshAPIAccountRepo{account: account},
		raceAccount:           nil, // GetByID returns nil on recovery attempt
	}
	cache := &refreshAPICacheStub{lockResult: true}
	executor := &refreshAPIExecutorStub{
		needsRefresh: true,
		err:          errors.New("invalid_grant"),
	}

	api := newOriginalRefreshAPI(repo, cache)
	result, err := api.RefreshIfNeeded(context.Background(), account, executor, 3*time.Minute)

	require.Error(t, err, "should propagate error when recovery DB re-read fails")
	require.NotNil(t, result)
	require.NotNil(t, result.Account)
	require.Equal(t, "old-rt", result.Account.GetCredential("refresh_token"))
}

func TestRefreshIfNeeded_LocalMutexSerializesConcurrent(t *testing.T) {
	// Test that two goroutines for the same account are serialized by the local mutex.
	// The first goroutine refreshes successfully; the second sees NeedsRefresh=false.
	refreshed := &Record{
		ID:          20,
		Platform:    capability.PlatformAnthropic,
		Type:        capability.AccountTypeOAuth,
		Status:      billing.StatusActive,
		Credentials: map[string]any{"refresh_token": "new-rt", "access_token": "new-at"},
	}
	callCount := 0
	repo := &refreshAPIAccountRepo{account: &Record{
		ID:          20,
		Platform:    capability.PlatformAnthropic,
		Type:        capability.AccountTypeOAuth,
		Status:      billing.StatusActive,
		Credentials: map[string]any{"refresh_token": "old-rt"},
	}}

	// After first refresh, NeedsRefresh should return false
	// We simulate this by using an executor that decrements needsRefresh after first call
	var mu sync.Mutex
	dynamicExecutor := &dynamicRefreshExecutor{
		canRefresh: true,
		cacheKey:   "test:mutex:anthropic",
		refreshFunc: func(_ context.Context, _ *Record) (map[string]any, error) {
			mu.Lock()
			callCount++
			mu.Unlock()
			time.Sleep(50 * time.Millisecond) // slow refresh
			return map[string]any{"access_token": "new-at"}, nil
		},
		needsRefreshFunc: func() bool {
			mu.Lock()
			defer mu.Unlock()
			return callCount == 0 // only first call needs refresh
		},
	}

	_ = refreshed

	api := newOriginalRefreshAPI(repo, nil) // no distributed lock, only local mutex

	var wg sync.WaitGroup
	results := make([]*OAuthRefreshResult, 2)
	errs := make([]error, 2)

	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			results[idx], errs[idx] = api.RefreshIfNeeded(context.Background(), repo.account, dynamicExecutor, 3*time.Minute)
		}(i)
	}
	wg.Wait()

	require.NoError(t, errs[0])
	require.NoError(t, errs[1])

	// Only one goroutine should have actually called Refresh
	mu.Lock()
	require.Equal(t, 1, callCount, "only one refresh call should have been made")
	mu.Unlock()
}

func TestRefreshIfNeeded_LocalLockWaitHonorsContextCancellation(t *testing.T) {
	account := &Record{ID: 21, Platform: capability.PlatformGrok, Type: capability.AccountTypeOAuth, Status: billing.StatusActive}
	repo := &refreshAPIAccountRepo{account: account}
	refreshStarted := make(chan struct{})
	releaseRefresh := make(chan struct{})
	var once sync.Once
	executor := &dynamicRefreshExecutor{
		canRefresh:       true,
		cacheKey:         "test:context-lock:grok",
		needsRefreshFunc: func() bool { return true },
		refreshFunc: func(context.Context, *Record) (map[string]any, error) {
			once.Do(func() { close(refreshStarted) })
			<-releaseRefresh
			return map[string]any{"access_token": "new-at"}, nil
		},
	}
	api := newOriginalRefreshAPI(repo, nil)
	firstDone := make(chan error, 1)
	go func() {
		_, err := api.RefreshIfNeeded(context.Background(), account, executor, 3*time.Minute)
		firstDone <- err
	}()
	<-refreshStarted

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Millisecond)
	defer cancel()
	startedAt := time.Now()
	result, err := api.RefreshIfNeeded(ctx, account, executor, 3*time.Minute)

	require.Nil(t, result)
	require.ErrorIs(t, err, context.DeadlineExceeded)
	require.Less(t, time.Since(startedAt), 500*time.Millisecond)
	close(releaseRefresh)
	require.NoError(t, <-firstDone)
}

func TestRefreshIfNeeded_ReleasesDistributedLockWithCleanupContext(t *testing.T) {
	account := &Record{
		ID:       22,
		Platform: capability.PlatformGrok,
		Type:     capability.AccountTypeOAuth,
		Status:   billing.StatusActive,
		Credentials: map[string]any{
			"access_token":  "old-access",
			"refresh_token": "old-refresh",
		},
	}
	repo := &refreshAPIAccountRepo{account: account}
	cache := &refreshAPICacheStub{lockResult: true}
	ctx, cancel := context.WithCancel(context.Background())
	executor := &dynamicRefreshExecutor{
		canRefresh:       true,
		cacheKey:         "test:cleanup:grok",
		needsRefreshFunc: func() bool { return true },
		refreshFunc: func(context.Context, *Record) (map[string]any, error) {
			cancel()
			return map[string]any{"access_token": "new-at"}, nil
		},
	}
	api := newOriginalRefreshAPI(repo, cache)

	result, err := api.RefreshIfNeeded(ctx, account, executor, 3*time.Minute)

	require.ErrorIs(t, err, context.Canceled)
	require.Nil(t, result)
	require.Zero(t, repo.updateCalls)
	require.Equal(t, "old-access", account.GetGrokAccessToken())
	require.Zero(t, account.GetCredentialAsInt64("_token_version"))
	require.Equal(t, 1, cache.releaseCalls)
	require.NoError(t, cache.releaseCtxErr)
}

// dynamicRefreshExecutor is a test helper with function-based NeedsRefresh and Refresh.
type dynamicRefreshExecutor struct {
	canRefresh       bool
	cacheKey         string
	needsRefreshFunc func() bool
	refreshFunc      func(context.Context, *Record) (map[string]any, error)
}

func (e *dynamicRefreshExecutor) CanRefresh(_ *Record) bool { return e.canRefresh }

func (e *dynamicRefreshExecutor) NeedsRefresh(_ *Record, _ time.Duration) bool {
	return e.needsRefreshFunc()
}

func (e *dynamicRefreshExecutor) Refresh(ctx context.Context, account *Record) (map[string]any, error) {
	return e.refreshFunc(ctx, account)
}

func (e *dynamicRefreshExecutor) CacheKey(_ *Record) string {
	return e.cacheKey
}

// ========== NewOAuthRefreshAPI TTL tests ==========

// ========== isInvalidGrantError tests ==========

// ========== BackgroundRefreshPolicy tests ==========

func TestBackgroundRefreshPolicy_DefaultSkips(t *testing.T) {
	p := DefaultBackgroundRefreshPolicy()

	require.ErrorIs(t, p.HandleLockHeld(), ErrRefreshSkipped)
	require.ErrorIs(t, p.HandleAlreadyRefreshed(), ErrRefreshSkipped)
}

func TestBackgroundRefreshPolicy_SuccessOverride(t *testing.T) {
	p := BackgroundRefreshPolicy{
		OnLockHeld:       BackgroundSkipAsSuccess,
		OnAlreadyRefresh: BackgroundSkipAsSuccess,
	}

	require.NoError(t, p.HandleLockHeld())
	require.NoError(t, p.HandleAlreadyRefreshed())
}

// ========== ProviderRefreshPolicy tests ==========

func TestClaudeProviderRefreshPolicy(t *testing.T) {
	p := ClaudeProviderRefreshPolicy()
	require.Equal(t, ProviderRefreshErrorUseExistingToken, p.OnRefreshError)
	require.Equal(t, ProviderLockHeldWaitForCache, p.OnLockHeld)
	require.Equal(t, time.Minute, p.FailureTTL)
}

func TestOpenAIProviderRefreshPolicy(t *testing.T) {
	p := OpenAIProviderRefreshPolicy()
	require.Equal(t, ProviderRefreshErrorUseExistingToken, p.OnRefreshError)
	require.Equal(t, ProviderLockHeldWaitForCache, p.OnLockHeld)
	require.Equal(t, time.Minute, p.FailureTTL)
}

func TestGeminiProviderRefreshPolicy(t *testing.T) {
	p := GeminiProviderRefreshPolicy()
	require.Equal(t, ProviderRefreshErrorReturn, p.OnRefreshError)
	require.Equal(t, ProviderLockHeldUseExistingToken, p.OnLockHeld)
	require.Equal(t, time.Duration(0), p.FailureTTL)
}

func TestAntigravityProviderRefreshPolicy(t *testing.T) {
	p := AntigravityProviderRefreshPolicy()
	require.Equal(t, ProviderRefreshErrorReturn, p.OnRefreshError)
	require.Equal(t, ProviderLockHeldUseExistingToken, p.OnLockHeld)
	require.Equal(t, time.Duration(0), p.FailureTTL)
}

func (r *refreshAPIAccountRepo) UpdateOAuthCredentialsIfUnchanged(ctx context.Context, version CredentialVersion, credentials map[string]any) (bool, error) {
	current := activeRefreshAPITestAccount(r.account)
	if current == nil || current.ID != version.ID || current.Platform != version.Platform || current.Type != version.Type || current.Status != version.Status || !reflect.DeepEqual(querycache.ShallowMap(current.Credentials), version.Credentials) || !reflect.DeepEqual(current.ProxyID, version.ProxyID) {
		return false, nil
	}
	err := r.UpdateCredentials(ctx, version.ID, credentials)
	return err == nil, err
}

// 每轮返回值必须与执行器持有的嵌套凭据及返回账号相互隔离。
func TestS06RefreshResultDoesNotShareExecutorValues(t *testing.T) {
	value := &Record{ID: 1001, Platform: capability.PlatformOpenAI, Type: capability.AccountTypeOAuth, Status: billing.StatusActive}
	repo := &refreshAPIAccountRepo{account: value}
	credentials := map[string]any{"access_token": "new", "session": map[string]any{"cookie": "initial"}}
	executor := &refreshAPIExecutorStub{needsRefresh: true, credentials: credentials}
	result, err := newOriginalRefreshAPI(repo, nil).RefreshIfNeeded(context.Background(), value, executor, time.Minute)
	require.NoError(t, err)
	require.True(t, result.Refreshed)
	session, ok := result.NewCredentials["session"].(map[string]any)
	require.True(t, ok)
	session["cookie"] = "caller"
	original, ok := credentials["session"].(map[string]any)
	require.True(t, ok)
	require.Equal(t, "initial", original["cookie"])
	returned, ok := result.Account.Credentials["session"].(map[string]any)
	require.True(t, ok)
	require.Equal(t, "initial", returned["cookie"])
}

// newOriginalRefreshAPI 保留原构造输入，直接配置唯一刷新协调器。
func newOriginalRefreshAPI(repo RefreshRepository, cache AccessTokenCache, lockTTL ...time.Duration) *OAuthRefreshAPI {
	options := RefreshOptions{Now: time.Now, Warn: slog.Warn, Info: slog.Info, Error: slog.Error, Platform: AccountRefreshPlatformPolicy()}
	if len(lockTTL) > 0 {
		options.LockTTL = lockTTL[0]
	}
	return NewOAuthRefreshAPI(repo, cache, options)
}
