package provider

import (
	"context"
	"errors"
	"log/slog"
	"reflect"
	"testing"
	"time"

	"github.com/TokenFlux/TokenRouter/internal/pkg/querycache"

	accountcore "github.com/TokenFlux/TokenRouter/internal/account"
	"github.com/TokenFlux/TokenRouter/internal/billing"
	"github.com/TokenFlux/TokenRouter/internal/routing/capability"

	"github.com/TokenFlux/TokenRouter/internal/upstream/qoder"
	"github.com/stretchr/testify/require"
)

func cloneQoderRequestCredentials(src map[string]any) map[string]any {
	if src == nil {
		return nil
	}
	dst := make(map[string]any, len(src))
	for key, value := range src {
		if nested, ok := value.(map[string]any); ok {
			dst[key] = cloneQoderRequestCredentials(nested)
			continue
		}
		dst[key] = value
	}
	return dst
}

type qoderRefreshAccountRepoStub struct {
	qoderRequestAccountsFixture
	updatedCredentials map[string]any
	updateCalls        int
}

func (r *qoderRefreshAccountRepoStub) UpdateCredentials(_ context.Context, id int64, credentials map[string]any) error {
	r.updateCalls++
	r.updatedCredentials = cloneQoderRequestCredentials(credentials)
	for i := range r.accounts {
		if r.accounts[i].ID == id {
			r.accounts[i].Credentials = cloneQoderRequestCredentials(credentials)
			return nil
		}
	}
	return nil
}

type qoderRefreshRaceRepoStub struct {
	qoderRefreshAccountRepoStub
	raceAccount  *accountcore.Record
	getByIDCalls int
}

func (r *qoderRefreshRaceRepoStub) GetByID(ctx context.Context, id int64) (*accountcore.Record, error) {
	r.getByIDCalls++
	if r.getByIDCalls > 1 && r.raceAccount != nil {
		return r.raceAccount, nil
	}
	return r.qoderRequestAccountsFixture.GetByID(ctx, id)
}

type qoderRefreshLockCacheStub struct{}

func (qoderRefreshLockCacheStub) GetAccessToken(context.Context, string) (string, error) {
	return "", nil
}

func (qoderRefreshLockCacheStub) SetAccessToken(context.Context, string, string, time.Duration) error {
	return nil
}

func (qoderRefreshLockCacheStub) DeleteAccessToken(context.Context, string) error {
	return nil
}

func (qoderRefreshLockCacheStub) AcquireRefreshLock(context.Context, string, time.Duration) (bool, error) {
	return false, nil
}

func (qoderRefreshLockCacheStub) ReleaseRefreshLock(context.Context, string) error {
	return nil
}

func TestQoderGatewayRefreshAccountSessionPersistsCredentialsAndInvalidatesCache(t *testing.T) {
	now := time.Now()
	expiredAt := now.Add(-1 * time.Hour)
	account := accountcore.Record{
		ID:       91,
		Name:     "qoder",
		Platform: capability.PlatformQoder,
		Type:     capability.AccountTypeCosy,
		Status:   billing.StatusActive,
		Credentials: map[string]any{
			"security_oauth_token": "old-token",
			"refresh_token":        "old-refresh",
			"machine_id":           "machine-1",
			"expires_at":           expiredAt.Format(time.RFC3339),
		},
	}
	repo := &qoderRefreshAccountRepoStub{
		qoderRequestAccountsFixture: qoderRequestAccountsFixture{accounts: []accountcore.Record{account}},
	}
	provider := &QoderTokenProvider{Core: &accountcore.QoderSessions[*qoder.SessionContext]{Sessions: map[int64]accountcore.QoderSessionCacheEntry[*qoder.SessionContext]{
		account.ID: {
			CredentialsHash: "old-hash",
			Session: &qoder.SessionContext{
				Identity: &qoder.AuthIdentity{SecurityOauthToken: "old-token"},
				Machine:  &qoder.MachineIdentity{MachineID: "machine-1"},
			},
		},
	}},
	}
	refresher := NewQoderTokenRefresher(QoderRefreshOptions{
		Exchange: qoder.RefreshExchange{
			RefreshSession: func(_ context.Context, refreshToken, securityOauthToken string, machine *qoder.MachineIdentity) (*qoder.AuthIdentity, error) {
				require.Equal(t, "old-refresh", refreshToken)
				require.Equal(t, "old-token", securityOauthToken)
				require.Equal(t, "machine-1", machine.MachineID)
				return &qoder.AuthIdentity{
					SecurityOauthToken: "new-token",
					RefreshToken:       "new-refresh",
					UID:                "user-1",
					AID:                "user-1",
					UserType:           "personal_standard",
				}, nil
			},
		},
	})

	svc := &QoderRequestRefresh{
		Tokens:       provider,
		Store:        repo,
		NewRefresher: func() *QoderTokenRefresher { return refresher },
		Coordinator:  newQoderRequestCoordinator(repo, nil),
	}

	refreshed, err := svc.RefreshAccountSession(context.Background(), &account)

	require.NoError(t, err)
	require.NotNil(t, refreshed)
	require.Equal(t, 1, repo.updateCalls)
	require.Equal(t, "new-token", repo.updatedCredentials["security_oauth_token"])
	require.Equal(t, "new-refresh", repo.updatedCredentials["refresh_token"])
	require.NotNil(t, repo.updatedCredentials["_token_version"])
	_, cached := provider.Core.Sessions[account.ID]
	require.False(t, cached)
}

func TestQoderGatewayRefreshAccountSessionRequiresInjectedRefreshAPI(t *testing.T) {
	svc := &QoderRequestRefresh{
		Store: &qoderRefreshAccountRepoStub{},
		NewRefresher: func() *QoderTokenRefresher {
			return NewQoderTokenRefresher(QoderRefreshOptions{})
		},
	}

	refreshed, err := svc.RefreshAccountSession(context.Background(), &accountcore.Record{ID: 1})

	require.Nil(t, refreshed)
	require.EqualError(t, err, "qoder refresh API is not configured")
}

func TestQoderGatewayRefreshAccountSessionIgnoresNonAuthCredentialDrift(t *testing.T) {
	now := time.Now()
	failedAccount := accountcore.Record{
		ID:       91,
		Name:     "qoder",
		Platform: capability.PlatformQoder,
		Type:     capability.AccountTypeCosy,
		Status:   billing.StatusActive,
		Credentials: map[string]any{
			"security_oauth_token": "old-token",
			"refresh_token":        "old-refresh",
			"machine_id":           "machine-1",
			"uid":                  "user-1",
			"expires_at":           now.Add(1 * time.Hour).Format(time.RFC3339),
		},
	}
	freshAccount := failedAccount
	freshAccount.Credentials = cloneQoderRequestCredentials(failedAccount.Credentials)
	freshAccount.Credentials["model_mapping"] = map[string]any{"qwen3.7-plus": "qmodel"}
	repo := &qoderRefreshAccountRepoStub{
		qoderRequestAccountsFixture: qoderRequestAccountsFixture{accounts: []accountcore.Record{freshAccount}},
	}
	refresher := NewQoderTokenRefresher(QoderRefreshOptions{
		Exchange: qoder.RefreshExchange{
			RefreshSession: func(_ context.Context, refreshToken, _ string, _ *qoder.MachineIdentity) (*qoder.AuthIdentity, error) {
				require.Equal(t, "old-refresh", refreshToken)
				return &qoder.AuthIdentity{
					UID:                "user-1",
					AID:                "user-1",
					SecurityOauthToken: "new-token",
					RefreshToken:       "new-refresh",
				}, nil
			},
		},
	})

	svc := &QoderRequestRefresh{
		Tokens:       NewQoderTokenProvider(qoder.SessionBuilder{}),
		Store:        repo,
		NewRefresher: func() *QoderTokenRefresher { return refresher },
		Coordinator:  newQoderRequestCoordinator(repo, nil),
	}

	refreshed, err := svc.RefreshAccountSession(context.Background(), &failedAccount)

	require.NoError(t, err)
	require.NotNil(t, refreshed)
	require.Equal(t, 1, repo.updateCalls)
	require.Equal(t, "new-token", repo.updatedCredentials["security_oauth_token"])
	require.Equal(t, "new-refresh", repo.updatedCredentials["refresh_token"])
	require.Equal(t, map[string]any{"qwen3.7-plus": "qmodel"}, repo.updatedCredentials["model_mapping"])
}

func TestQoderGatewayRefreshAccountSessionRecoversRotatedRefreshTokenRace(t *testing.T) {
	now := time.Now()
	expiredAt := now.Add(-1 * time.Hour)
	account := accountcore.Record{
		ID:       92,
		Name:     "qoder",
		Platform: capability.PlatformQoder,
		Type:     capability.AccountTypeCosy,
		Status:   billing.StatusActive,
		Credentials: map[string]any{
			"security_oauth_token": "old-token",
			"refresh_token":        "old-refresh",
			"machine_id":           "machine-1",
			"expires_at":           expiredAt.Format(time.RFC3339),
		},
	}
	racedAccount := account
	racedAccount.Credentials = map[string]any{
		"security_oauth_token": "new-token",
		"refresh_token":        "new-refresh",
		"machine_id":           "machine-1",
		"expires_at":           now.Add(1 * time.Hour).Format(time.RFC3339),
	}
	repo := &qoderRefreshRaceRepoStub{
		qoderRefreshAccountRepoStub: qoderRefreshAccountRepoStub{
			qoderRequestAccountsFixture: qoderRequestAccountsFixture{accounts: []accountcore.Record{account}},
		},
		raceAccount: &racedAccount,
	}
	refresher := NewQoderTokenRefresher(QoderRefreshOptions{
		Exchange: qoder.RefreshExchange{
			RefreshSession: func(_ context.Context, refreshToken, _ string, _ *qoder.MachineIdentity) (*qoder.AuthIdentity, error) {
				require.Equal(t, "old-refresh", refreshToken)
				return nil, errors.New("invalid_grant: refresh token has already been used")
			},
		},
	})

	svc := &QoderRequestRefresh{
		Tokens:       NewQoderTokenProvider(qoder.SessionBuilder{}),
		Store:        repo,
		NewRefresher: func() *QoderTokenRefresher { return refresher },
		Coordinator:  newQoderRequestCoordinator(repo, nil),
	}

	refreshed, err := svc.RefreshAccountSession(context.Background(), &account)

	require.NoError(t, err)
	require.NotNil(t, refreshed)
	require.Equal(t, "new-refresh", refreshed.GetCredential("refresh_token"))
	require.Equal(t, 0, repo.updateCalls)
	require.GreaterOrEqual(t, repo.getByIDCalls, 2)
}

func TestQoderGatewayRefreshAccountSessionWaitsForLockHolderRotation(t *testing.T) {
	now := time.Now()
	account := accountcore.Record{
		ID:       94,
		Name:     "qoder",
		Platform: capability.PlatformQoder,
		Type:     capability.AccountTypeCosy,
		Credentials: map[string]any{
			"security_oauth_token": "old-token",
			"refresh_token":        "old-refresh",
			"machine_id":           "machine-1",
			"uid":                  "user-1",
			"expires_at":           now.Add(1 * time.Hour).Format(time.RFC3339),
		},
	}
	rotatedAccount := account
	rotatedAccount.Credentials = map[string]any{
		"security_oauth_token": "new-token",
		"refresh_token":        "new-refresh",
		"machine_id":           "machine-1",
		"uid":                  "user-1",
		"expires_at":           now.Add(2 * time.Hour).Format(time.RFC3339),
	}
	repo := &qoderRefreshRaceRepoStub{
		qoderRefreshAccountRepoStub: qoderRefreshAccountRepoStub{
			qoderRequestAccountsFixture: qoderRequestAccountsFixture{accounts: []accountcore.Record{account}},
		},
		raceAccount: &rotatedAccount,
	}
	provider := NewQoderTokenProvider(qoder.SessionBuilder{})
	provider.Core.Sessions[account.ID] = accountcore.QoderSessionCacheEntry[*qoder.SessionContext]{
		CredentialsHash: accountcore.QoderCredentialsHash(account.Credentials),
		Session:         &qoder.SessionContext{},
	}
	svc := &QoderRequestRefresh{
		Tokens: provider,
		Store:  repo,
		NewRefresher: func() *QoderTokenRefresher {
			return NewQoderTokenRefresher(QoderRefreshOptions{})
		},
		Coordinator: newQoderRequestCoordinator(repo, qoderRefreshLockCacheStub{}),
	}
	ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
	defer cancel()

	refreshed, err := svc.RefreshAccountSession(ctx, &account)

	require.NoError(t, err)
	require.NotNil(t, refreshed)
	require.Equal(t, "new-token", refreshed.GetCredential("security_oauth_token"))
	require.Equal(t, "new-refresh", refreshed.GetCredential("refresh_token"))
	require.GreaterOrEqual(t, repo.getByIDCalls, 2)
	_, cached := provider.Core.Sessions[account.ID]
	require.False(t, cached)
}

func TestQoderGatewayRefreshAccountSessionLockHeldReturnsRefreshInProgressWithoutStaleAccount(t *testing.T) {
	now := time.Now()
	account := accountcore.Record{
		ID:       95,
		Name:     "qoder",
		Platform: capability.PlatformQoder,
		Type:     capability.AccountTypeCosy,
		Credentials: map[string]any{
			"security_oauth_token": "old-token",
			"refresh_token":        "old-refresh",
			"machine_id":           "machine-1",
			"uid":                  "user-1",
			"expires_at":           now.Add(1 * time.Hour).Format(time.RFC3339),
		},
	}
	repo := &qoderRefreshAccountRepoStub{
		qoderRequestAccountsFixture: qoderRequestAccountsFixture{accounts: []accountcore.Record{account}},
	}
	provider := NewQoderTokenProvider(qoder.SessionBuilder{})
	provider.Core.Sessions[account.ID] = accountcore.QoderSessionCacheEntry[*qoder.SessionContext]{
		CredentialsHash: accountcore.QoderCredentialsHash(account.Credentials),
		Session:         &qoder.SessionContext{},
	}
	svc := &QoderRequestRefresh{
		Tokens: provider,
		Store:  repo,
		NewRefresher: func() *QoderTokenRefresher {
			return NewQoderTokenRefresher(QoderRefreshOptions{})
		},
		Coordinator: newQoderRequestCoordinator(repo, qoderRefreshLockCacheStub{}),
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()

	refreshed, err := svc.RefreshAccountSession(ctx, &account)

	require.Nil(t, refreshed)
	require.ErrorIs(t, err, accountcore.ErrQoderRefreshInProgress)
	_, cached := provider.Core.Sessions[account.ID]
	require.True(t, cached)
	require.Equal(t, "old-token", repo.accounts[0].GetCredential("security_oauth_token"))
}

// 条件写入替身保留 Qoder 测试对真实持久化参数和缓存失效的断言。
func (r *qoderRefreshAccountRepoStub) UpdateOAuthCredentialsIfUnchanged(ctx context.Context, version accountcore.CredentialVersion, credentials map[string]any) (bool, error) {
	current, err := r.GetByID(ctx, version.ID)
	if err != nil {
		return false, err
	}
	if current.Platform != version.Platform || current.Type != version.Type || current.Status != version.Status || !reflect.DeepEqual(querycache.ShallowMap(current.Credentials), version.Credentials) || !reflect.DeepEqual(current.ProxyID, version.ProxyID) {
		return false, nil
	}
	err = r.UpdateCredentials(ctx, version.ID, credentials)
	return err == nil, err
}

// qoderRequestAccountsFixture 保留原读取夹具的对象与查询错误行为。
type qoderRequestAccountsFixture struct{ accounts []accountcore.Record }

func (r qoderRequestAccountsFixture) GetByID(_ context.Context, id int64) (*accountcore.Record, error) {
	for i := range r.accounts {
		if r.accounts[i].ID == id {
			return &r.accounts[i], nil
		}
	}
	return nil, errors.New("account not found")
}

// newQoderRequestCoordinator 使用真实协调器及原时钟/平台策略，不复制刷新算法。
func newQoderRequestCoordinator(repo accountcore.RefreshRepository, cache accountcore.AccessTokenCache) *accountcore.OAuthRefreshAPI {
	return accountcore.NewOAuthRefreshAPI(repo, cache, accountcore.RefreshOptions{Now: time.Now, Warn: slog.Warn, Info: slog.Info, Error: slog.Error, Platform: accountcore.AccountRefreshPlatformPolicy()})
}
