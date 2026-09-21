package provider

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	accountcore "github.com/TokenFlux/TokenRouter/internal/account"
	"github.com/TokenFlux/TokenRouter/internal/egress"
	providercore "github.com/TokenFlux/TokenRouter/internal/egress/provider"
	"github.com/TokenFlux/TokenRouter/internal/infra/httpclient/tlsfingerprint"
	"github.com/TokenFlux/TokenRouter/internal/routing/capability"
	"github.com/TokenFlux/TokenRouter/internal/upstream/qoder"
	"github.com/stretchr/testify/require"
	"maps"
)

func TestQoderTokenProviderBuildsAndCachesDirectSession(t *testing.T) {
	provider := NewQoderTokenProvider(qoder.SessionBuilder{})
	account := &accountcore.Record{
		ID:       101,
		Platform: capability.PlatformQoder,
		Type:     capability.AccountTypeCosy,
		Credentials: map[string]any{
			"security_oauth_token": "dt-token",
			"machine_id":           "machine-1",
			"uid":                  "uid-1",
			"organization_id":      "org-1",
			"organization_name":    "Org 1",
		},
	}

	session1, err := provider.GetSession(context.Background(), account)
	require.NoError(t, err)
	require.NotNil(t, session1)
	require.Equal(t, "dt-token", session1.Identity.SecurityOauthToken)
	require.Equal(t, "uid-1", session1.Identity.UID)
	require.Equal(t, "uid-1", session1.Identity.AID)
	require.Equal(t, "org-1", session1.Identity.OrganizationID)
	require.Equal(t, "Org 1", session1.Identity.OrganizationName)
	require.Equal(t, "machine-1", session1.Machine.MachineID)

	session2, err := provider.GetSession(context.Background(), account)
	require.NoError(t, err)
	require.Same(t, session1, session2, "session should be cached per account credentials")

	account.Credentials["organization_id"] = "org-2"
	session3, err := provider.GetSession(context.Background(), account)
	require.NoError(t, err)
	require.NotSame(t, session1, session3)
	require.Equal(t, "org-2", session3.Identity.OrganizationID)
}

func TestQoderTokenProviderRejectsUnsupportedCredentialShape(t *testing.T) {
	provider := NewQoderTokenProvider(qoder.SessionBuilder{})
	account := &accountcore.Record{
		ID:          102,
		Platform:    capability.PlatformQoder,
		Type:        capability.AccountTypeCosy,
		Credentials: map[string]any{"security_oauth_token": "dt-token"},
	}

	_, err := provider.GetSession(context.Background(), account)
	require.ErrorContains(t, err, "machine_id")
}

func TestQoderTokenProviderRejectsDirectTokenWithoutIdentity(t *testing.T) {
	provider := NewQoderTokenProvider(qoder.SessionBuilder{})
	account := &accountcore.Record{
		ID:       105,
		Platform: capability.PlatformQoder,
		Type:     capability.AccountTypeCosy,
		Credentials: map[string]any{
			"security_oauth_token": "dt-token",
			"machine_id":           "machine-1",
		},
	}

	_, err := provider.GetSession(context.Background(), account)
	require.ErrorContains(t, err, "uid or aid")
}

func TestQoderTokenProviderRejectsCN20RefreshModeOnGlobalSite(t *testing.T) {
	provider := NewQoderTokenProvider(qoder.SessionBuilder{})
	account := &accountcore.Record{
		ID:       32,
		Platform: capability.PlatformQoder,
		Type:     capability.AccountTypeCosy,
		Credentials: map[string]any{
			"site":                 "global",
			"refresh_mode":         qoder.RefreshModeQoderCN20,
			"security_oauth_token": "token",
			"machine_id":           "machine",
			"uid":                  "uid",
		},
	}

	_, err := provider.GetSession(context.Background(), account)
	require.ErrorContains(t, err, "require cn site")
}

func TestQoderTokenProviderRejectsMachineIDOnlyWithoutReadingLocalAuth(t *testing.T) {
	provider := NewQoderTokenProvider(qoder.SessionBuilder{})
	account := &accountcore.Record{
		ID:       106,
		Platform: capability.PlatformQoder,
		Type:     capability.AccountTypeCosy,
		Credentials: map[string]any{
			"machine_id": "machine-1",
		},
	}

	_, err := provider.GetSession(context.Background(), account)
	require.ErrorContains(t, err, "pat or security_oauth_token")
}

func TestQoderTokenProviderRejectsExplicitAuthDirWithoutReadingLocalAuth(t *testing.T) {
	provider := NewQoderTokenProvider(qoder.SessionBuilder{})
	account := &accountcore.Record{
		ID:       106,
		Platform: capability.PlatformQoder,
		Type:     capability.AccountTypeCosy,
		Credentials: map[string]any{
			"machine_id": "machine-1",
			"auth_dir":   "/tmp/qoder-auth",
		},
	}

	_, err := provider.GetSession(context.Background(), account)
	require.ErrorContains(t, err, "pat or security_oauth_token")
}

func TestQoderTokenProviderSupportsInjectedPATExchange(t *testing.T) {
	calls := 0
	orgCalls := 0
	var exchangedPATs []string
	provider := NewQoderTokenProvider(qoder.SessionBuilder{})
	provider.exchangePAT = func(_ context.Context, pat string, machine *qoder.MachineIdentity) (*qoder.AuthIdentity, error) {
		calls++
		exchangedPATs = append(exchangedPATs, pat)
		require.NotEmpty(t, machine.MachineID)
		return &qoder.AuthIdentity{
			Name:               "PAT User",
			UID:                "uid-1",
			AID:                "uid-1",
			UserType:           "personal_standard",
			SecurityOauthToken: "dt-from-pat",
			RefreshToken:       "rt-from-pat",
		}, nil
	}
	provider.getOrgTags = func(context.Context, string, string) (*qoder.OrganizationTags, error) {
		orgCalls++
		return nil, nil
	}

	account := &accountcore.Record{
		ID:       103,
		Platform: capability.PlatformQoder,
		Type:     capability.AccountTypeCosy,
		Credentials: map[string]any{
			"pat":               "pat-123",
			"organization_id":   "org-from-account",
			"organization_name": "Org From accountcore.Record",
		},
	}

	session1, err := provider.GetSession(context.Background(), account)
	require.NoError(t, err)
	require.Equal(t, "dt-from-pat", session1.Identity.SecurityOauthToken)
	require.Equal(t, "org-from-account", session1.Identity.OrganizationID)
	require.Equal(t, "Org From accountcore.Record", session1.Identity.OrganizationName)

	session2, err := provider.GetSession(context.Background(), account)
	require.NoError(t, err)
	require.Same(t, session1, session2)
	require.Equal(t, 1, calls, "PAT exchange should not run after cache hit")
	require.Equal(t, 0, orgCalls, "account-provided organization metadata should skip org lookup")

	account.Credentials["pat"] = "pat-456"
	session3, err := provider.GetSession(context.Background(), account)
	require.NoError(t, err)
	require.NotSame(t, session1, session3)
	require.Equal(t, []string{"pat-123", "pat-456"}, exchangedPATs)
}

func TestQoderRefreshCredentialsHashTracksAuthenticationContext(t *testing.T) {
	base := map[string]any{
		"pat":             "pat-123",
		"site":            "global",
		"refresh_mode":    "cosy",
		"organization_id": "org-1",
		"model_mapping":   map[string]any{"auto": "auto"},
	}
	baseHash := accountcore.QoderRefreshCredentialsHash(base)
	for _, change := range []map[string]any{
		{"pat": "pat-456"},
		{"site": "cn"},
		{"refresh_mode": qoder.RefreshModeQoderCN20},
		{"organization_id": "org-2"},
	} {
		credentials := accountcore.MergeCredentials(base, change)
		require.NotEqual(t, baseHash, accountcore.QoderRefreshCredentialsHash(credentials))
	}
	unrelated := accountcore.MergeCredentials(base, map[string]any{
		"model_mapping": map[string]any{"custom": "auto"},
	})
	require.Equal(t, baseHash, accountcore.QoderRefreshCredentialsHash(unrelated))
}

func TestQoderTokenProviderPATExchangePopulatesOrganizationFromAPI(t *testing.T) {
	provider := NewQoderTokenProvider(qoder.SessionBuilder{})
	provider.exchangePAT = func(_ context.Context, _ string, _ *qoder.MachineIdentity) (*qoder.AuthIdentity, error) {
		return &qoder.AuthIdentity{
			Name:               "PAT User",
			UID:                "uid-1",
			AID:                "aid-1",
			UserType:           "personal_standard",
			SecurityOauthToken: "dt-from-pat",
		}, nil
	}
	provider.getOrgTags = func(_ context.Context, token, uid string) (*qoder.OrganizationTags, error) {
		require.Equal(t, "dt-from-pat", token)
		require.Equal(t, "uid-1", uid)
		return &qoder.OrganizationTags{
			OrganizationID:   "org-from-api",
			OrganizationName: "Org From API",
		}, nil
	}

	account := &accountcore.Record{
		ID:       104,
		Platform: capability.PlatformQoder,
		Type:     capability.AccountTypeCosy,
		Credentials: map[string]any{
			"pat": "pat-123",
		},
	}

	session, err := provider.GetSession(context.Background(), account)
	require.NoError(t, err)
	require.Equal(t, "org-from-api", session.Identity.OrganizationID)
	require.Equal(t, "Org From API", session.Identity.OrganizationName)
}

func TestQoderTokenProviderRoutesCNPATAndBuildsCNSession(t *testing.T) {
	provider := NewQoderTokenProvider(qoder.SessionBuilder{})
	provider.exchangeCNPAT = func(_ context.Context, pat string, machine *qoder.MachineIdentity) (*qoder.AuthIdentity, time.Time, error) {
		require.Equal(t, "cn-pat", pat)
		require.Equal(t, "machine-cn", machine.MachineID)
		require.Empty(t, machine.MachineToken)
		require.Empty(t, machine.MachineType)
		return &qoder.AuthIdentity{
			UID:                "uid-cn",
			AID:                "aid-cn",
			OrganizationID:     "org-cn",
			SecurityOauthToken: "cosy-cn",
			RefreshToken:       "refresh-cn",
		}, time.Now().Add(time.Hour), nil
	}
	account := &accountcore.Record{
		ID:       31,
		Platform: capability.PlatformQoder,
		Type:     capability.AccountTypeCosy,
		Credentials: map[string]any{
			"site":          "cn",
			"pat":           "cn-pat",
			"machine_id":    "machine-cn",
			"machine_token": "machine-token-cn",
			"machine_type":  "machine-type-cn",
		},
	}

	session, err := provider.GetSession(context.Background(), account)
	require.NoError(t, err)
	require.Equal(t, qoder.SiteCN, session.Site)
	require.Equal(t, qoder.CNClientVersion, session.ClientVersion)
	require.Equal(t, "cosy-cn", session.Identity.SecurityOauthToken)
	require.Empty(t, session.Machine.MachineToken)
	require.Empty(t, session.Machine.MachineType)
}

func TestQoderTokenProviderRebuildsExpiredCNPATSession(t *testing.T) {
	provider := NewQoderTokenProvider(qoder.SessionBuilder{})
	expiresAt := time.Now().Add(time.Hour)
	exchangeCalls := 0
	provider.exchangeCNPAT = func(_ context.Context, _ string, _ *qoder.MachineIdentity) (*qoder.AuthIdentity, time.Time, error) {
		exchangeCalls++
		return &qoder.AuthIdentity{
			UID:                "uid-cn",
			AID:                "uid-cn",
			SecurityOauthToken: fmt.Sprintf("cosy-cn-%d", exchangeCalls),
			RefreshToken:       "refresh-cn",
		}, expiresAt, nil
	}
	account := &accountcore.Record{
		ID:       32,
		Platform: capability.PlatformQoder,
		Type:     capability.AccountTypeCosy,
		Credentials: map[string]any{
			"site":       "cn",
			"pat":        "cn-pat",
			"machine_id": "machine-cn",
		},
	}

	first, err := provider.GetSession(context.Background(), account)
	require.NoError(t, err)
	require.Equal(t, expiresAt, provider.qoderState().Sessions[account.ID].ExpiresAt)

	// 模拟已缓存的 OpenAPI token 到期，下一次读取必须重新执行 PAT exchange。
	entry := provider.qoderState().Sessions[account.ID]
	entry.ExpiresAt = time.Now().Add(-time.Second)
	provider.qoderState().Sessions[account.ID] = entry
	second, err := provider.GetSession(context.Background(), account)

	require.NoError(t, err)
	require.NotSame(t, first, second)
	require.Equal(t, "cosy-cn-2", second.Identity.SecurityOauthToken)
	require.Equal(t, 2, exchangeCalls)
}

func TestQoderTokenProviderSingleflightsConcurrentExpiredCNPATSession(t *testing.T) {
	provider := NewQoderTokenProvider(qoder.SessionBuilder{})
	var exchangeCalls atomic.Int32
	var blockRebuild atomic.Bool
	rebuildStarted := make(chan struct{})
	releaseRebuild := make(chan struct{})
	provider.exchangeCNPAT = func(_ context.Context, _ string, _ *qoder.MachineIdentity) (*qoder.AuthIdentity, time.Time, error) {
		call := exchangeCalls.Add(1)
		if blockRebuild.Load() {
			if call == 2 {
				close(rebuildStarted)
			}
			<-releaseRebuild
		}
		return &qoder.AuthIdentity{
			UID:                "uid-cn",
			AID:                "uid-cn",
			SecurityOauthToken: fmt.Sprintf("cosy-cn-%d", call),
		}, time.Now().Add(time.Hour), nil
	}
	account := &accountcore.Record{
		ID:       3201,
		Platform: capability.PlatformQoder,
		Type:     capability.AccountTypeCosy,
		Credentials: map[string]any{
			"site":       "cn",
			"pat":        "cn-pat",
			"machine_id": "machine-cn",
		},
	}

	_, err := provider.GetSession(context.Background(), account)
	require.NoError(t, err)
	provider.qoderState().Mu.Lock()
	entry := provider.qoderState().Sessions[account.ID]
	entry.ExpiresAt = time.Now().Add(-time.Second)
	provider.qoderState().Sessions[account.ID] = entry
	provider.qoderState().Mu.Unlock()
	blockRebuild.Store(true)

	const workers = 32
	start := make(chan struct{})
	results := make(chan *qoder.SessionContext, workers)
	errorsCh := make(chan error, workers)
	var ready sync.WaitGroup
	var done sync.WaitGroup
	ready.Add(workers)
	done.Add(workers)
	for range workers {
		go func() {
			defer done.Done()
			ready.Done()
			<-start
			session, getErr := provider.GetSession(context.Background(), account)
			results <- session
			errorsCh <- getErr
		}()
	}
	ready.Wait()
	close(start)
	<-rebuildStarted
	// 第一个交换保持阻塞，让其余调用进入相同 singleflight。
	time.Sleep(20 * time.Millisecond)
	require.Equal(t, int32(2), exchangeCalls.Load())
	close(releaseRebuild)
	done.Wait()
	close(results)
	close(errorsCh)

	for getErr := range errorsCh {
		require.NoError(t, getErr)
	}
	var shared *qoder.SessionContext
	for session := range results {
		require.NotNil(t, session)
		if shared == nil {
			shared = session
		}
		require.Same(t, shared, session)
	}
	require.Equal(t, int32(2), exchangeCalls.Load(), "初次构建加一次过期重建应只交换两次")
}

func TestQoderTokenProviderSingleflightSeparatesWaiterCancellation(t *testing.T) {
	provider := NewQoderTokenProvider(qoder.SessionBuilder{})
	var exchangeCalls atomic.Int32
	buildStarted := make(chan struct{})
	releaseBuild := make(chan struct{})
	provider.exchangeCNPAT = func(ctx context.Context, _ string, _ *qoder.MachineIdentity) (*qoder.AuthIdentity, time.Time, error) {
		exchangeCalls.Add(1)
		close(buildStarted)
		select {
		case <-ctx.Done():
			return nil, time.Time{}, ctx.Err()
		case <-releaseBuild:
			return &qoder.AuthIdentity{
				UID:                "uid-cn",
				AID:                "uid-cn",
				SecurityOauthToken: "cosy-cn-shared",
			}, time.Now().Add(time.Hour), nil
		}
	}
	account := &accountcore.Record{
		ID:       3204,
		Platform: capability.PlatformQoder,
		Type:     capability.AccountTypeCosy,
		Credentials: map[string]any{
			"site":       "cn",
			"pat":        "cn-pat",
			"machine_id": "machine-cn",
		},
	}

	firstCtx, cancelFirst := context.WithCancel(context.Background())
	firstResult := make(chan error, 1)
	go func() {
		_, getErr := provider.GetSession(firstCtx, account)
		firstResult <- getErr
	}()
	<-buildStarted
	cancelFirst()
	require.ErrorIs(t, <-firstResult, context.Canceled)

	secondResult := make(chan struct {
		session *qoder.SessionContext
		err     error
	}, 1)
	go func() {
		session, getErr := provider.GetSession(context.Background(), account)
		secondResult <- struct {
			session *qoder.SessionContext
			err     error
		}{session: session, err: getErr}
	}()
	require.Eventually(t, func() bool {
		return exchangeCalls.Load() == 1
	}, time.Second, 10*time.Millisecond)
	close(releaseBuild)

	second := <-secondResult
	require.NoError(t, second.err)
	require.NotNil(t, second.session)
	require.Equal(t, "cosy-cn-shared", second.session.Identity.SecurityOauthToken)
	require.Equal(t, int32(1), exchangeCalls.Load())
}

func TestQoderTokenProviderDetachedBuildHasHardTimeout(t *testing.T) {
	provider := NewQoderTokenProvider(qoder.SessionBuilder{})
	provider.qoderState().SessionBuildTimeout = 20 * time.Millisecond
	provider.exchangeCNPAT = func(ctx context.Context, _ string, _ *qoder.MachineIdentity) (*qoder.AuthIdentity, time.Time, error) {
		<-ctx.Done()
		return nil, time.Time{}, ctx.Err()
	}
	account := &accountcore.Record{ID: 3205, Platform: capability.PlatformQoder, Type: capability.AccountTypeCosy, Credentials: map[string]any{
		"site": "cn", "pat": "cn-pat", "machine_id": "machine-cn",
	}}

	startedAt := time.Now()
	_, err := provider.GetSession(context.Background(), account)
	require.ErrorIs(t, err, context.DeadlineExceeded)
	require.Less(t, time.Since(startedAt), time.Second)
}

func TestQoderTokenProviderBuildUsesAccountSnapshot(t *testing.T) {
	provider := NewQoderTokenProvider(qoder.SessionBuilder{})
	buildStarted := make(chan struct{})
	releaseBuild := make(chan struct{})
	provider.exchangePAT = func(_ context.Context, _ string, _ *qoder.MachineIdentity) (*qoder.AuthIdentity, error) {
		close(buildStarted)
		<-releaseBuild
		return &qoder.AuthIdentity{
			UID:                "uid-global",
			AID:                "uid-global",
			SecurityOauthToken: "cosy-global",
		}, nil
	}
	account := &accountcore.Record{ID: 3206, Platform: capability.PlatformQoder, Type: capability.AccountTypeCosy, Credentials: map[string]any{
		"site": "global", "pat": "global-pat", "machine_id": "machine-global", "organization_id": "org-original",
	}}

	type sessionResult struct {
		session *qoder.SessionContext
		err     error
	}
	resultCh := make(chan sessionResult, 1)
	go func() {
		session, getErr := provider.GetSession(context.Background(), account)
		resultCh <- sessionResult{session: session, err: getErr}
	}()
	<-buildStarted
	account.Credentials["organization_id"] = "org-mutated"
	close(releaseBuild)

	result := <-resultCh
	require.NoError(t, result.err)
	require.Equal(t, "org-original", result.session.Identity.OrganizationID)
}

func TestQoderTokenProviderInvalidateDoesNotReturnOrCacheInflightSession(t *testing.T) {
	provider := NewQoderTokenProvider(qoder.SessionBuilder{})
	var exchangeCalls atomic.Int32
	firstStarted := make(chan struct{})
	releaseFirst := make(chan struct{})
	provider.exchangeCNPAT = func(_ context.Context, _ string, _ *qoder.MachineIdentity) (*qoder.AuthIdentity, time.Time, error) {
		call := exchangeCalls.Add(1)
		if call == 1 {
			close(firstStarted)
			<-releaseFirst
		}
		return &qoder.AuthIdentity{
			UID:                "uid-cn",
			AID:                "uid-cn",
			SecurityOauthToken: fmt.Sprintf("cosy-cn-%d", call),
		}, time.Now().Add(time.Hour), nil
	}
	account := &accountcore.Record{
		ID:       3202,
		Platform: capability.PlatformQoder,
		Type:     capability.AccountTypeCosy,
		Credentials: map[string]any{
			"site":       "cn",
			"pat":        "cn-pat",
			"machine_id": "machine-cn",
		},
	}

	type sessionResult struct {
		session *qoder.SessionContext
		err     error
	}
	firstResult := make(chan sessionResult, 1)
	go func() {
		session, getErr := provider.GetSession(context.Background(), account)
		firstResult <- sessionResult{session: session, err: getErr}
	}()
	<-firstStarted
	provider.Invalidate(account.ID)

	second, err := provider.GetSession(context.Background(), account)
	require.NoError(t, err)
	require.Equal(t, "cosy-cn-2", second.Identity.SecurityOauthToken)
	close(releaseFirst)
	stale := <-firstResult
	require.Nil(t, stale.session)
	require.ErrorIs(t, stale.err, errQoderSessionBuildInvalidated)

	provider.qoderState().Mu.Lock()
	cached := provider.qoderState().Sessions[account.ID].Session
	provider.qoderState().Mu.Unlock()
	require.Same(t, second, cached)
	require.Equal(t, int32(2), exchangeCalls.Load())
}

func TestQoderTokenProviderDoesNotMergeDifferentCredentialHashes(t *testing.T) {
	provider := NewQoderTokenProvider(qoder.SessionBuilder{})
	var exchangeCalls atomic.Int32
	oldStarted := make(chan struct{})
	releaseOld := make(chan struct{})
	provider.exchangeCNPAT = func(_ context.Context, pat string, _ *qoder.MachineIdentity) (*qoder.AuthIdentity, time.Time, error) {
		exchangeCalls.Add(1)
		if pat == "old-pat" {
			close(oldStarted)
			<-releaseOld
		}
		return &qoder.AuthIdentity{
			UID:                "uid-cn",
			AID:                "uid-cn",
			SecurityOauthToken: "cosy-" + pat,
		}, time.Now().Add(time.Hour), nil
	}
	oldAccount := &accountcore.Record{ID: 3203, Platform: capability.PlatformQoder, Type: capability.AccountTypeCosy, Credentials: map[string]any{
		"site": "cn", "pat": "old-pat", "machine_id": "machine-cn", "_token_version": int64(1),
	}}
	newAccount := &accountcore.Record{ID: 3203, Platform: capability.PlatformQoder, Type: capability.AccountTypeCosy, Credentials: map[string]any{
		"site": "cn", "pat": "new-pat", "machine_id": "machine-cn", "_token_version": int64(2),
	}}

	oldResult := make(chan error, 1)
	go func() {
		_, getErr := provider.GetSession(context.Background(), oldAccount)
		oldResult <- getErr
	}()
	<-oldStarted
	newSession, err := provider.GetSession(context.Background(), newAccount)
	require.NoError(t, err)
	require.Equal(t, "cosy-new-pat", newSession.Identity.SecurityOauthToken)
	close(releaseOld)
	require.ErrorIs(t, <-oldResult, errQoderSessionBuildInvalidated)

	provider.qoderState().Mu.Lock()
	cached := provider.qoderState().Sessions[newAccount.ID]
	provider.qoderState().Mu.Unlock()
	require.Equal(t, accountcore.QoderCredentialsHash(newAccount.Credentials), cached.CredentialsHash)
	require.Same(t, newSession, cached.Session)
	require.Equal(t, int32(2), exchangeCalls.Load())
}

func TestQoderTokenProviderDoesNotRegressToOlderCredentialVersion(t *testing.T) {
	provider := NewQoderTokenProvider(qoder.SessionBuilder{})
	var exchangeCalls atomic.Int32
	provider.exchangeCNPAT = func(_ context.Context, pat string, _ *qoder.MachineIdentity) (*qoder.AuthIdentity, time.Time, error) {
		exchangeCalls.Add(1)
		return &qoder.AuthIdentity{
			UID:                "uid-cn",
			AID:                "uid-cn",
			SecurityOauthToken: "cosy-" + pat,
		}, time.Now().Add(time.Hour), nil
	}
	newAccount := &accountcore.Record{ID: 3207, Platform: capability.PlatformQoder, Type: capability.AccountTypeCosy, Credentials: map[string]any{
		"site": "cn", "pat": "new-pat", "machine_id": "machine-cn", "_token_version": int64(2),
	}}
	staleAccount := &accountcore.Record{ID: 3207, Platform: capability.PlatformQoder, Type: capability.AccountTypeCosy, Credentials: map[string]any{
		"site": "cn", "pat": "old-pat", "machine_id": "machine-cn", "_token_version": int64(1),
	}}

	latest, err := provider.GetSession(context.Background(), newAccount)
	require.NoError(t, err)
	require.Equal(t, "cosy-new-pat", latest.Identity.SecurityOauthToken)

	fromStaleSnapshot, err := provider.GetSession(context.Background(), staleAccount)
	require.NoError(t, err)
	require.Same(t, latest, fromStaleSnapshot)
	require.Equal(t, int32(1), exchangeCalls.Load())

	provider.qoderState().Mu.Lock()
	cached := provider.qoderState().Sessions[newAccount.ID]
	currentState := provider.qoderState().AccountStates[newAccount.ID]
	provider.qoderState().Mu.Unlock()
	require.Equal(t, accountcore.QoderCredentialsHash(newAccount.Credentials), currentState.CredentialsHash)
	require.Equal(t, int64(2), currentState.CredentialVersion)
	require.Same(t, latest, cached.Session)
}

func TestQoderTokenProviderAuthoritativeInvalidationBlocksUnobservedStaleVersion(t *testing.T) {
	provider := NewQoderTokenProvider(qoder.SessionBuilder{})
	var exchangeCalls atomic.Int32
	provider.exchangeCNPAT = func(_ context.Context, pat string, _ *qoder.MachineIdentity) (*qoder.AuthIdentity, time.Time, error) {
		exchangeCalls.Add(1)
		return &qoder.AuthIdentity{
			UID:                "uid-cn",
			AID:                "uid-cn",
			SecurityOauthToken: "cosy-" + pat,
		}, time.Now().Add(time.Hour), nil
	}
	staleAccount := &accountcore.Record{ID: 3210, Platform: capability.PlatformQoder, Type: capability.AccountTypeCosy, Credentials: map[string]any{
		"site": "cn", "pat": "old-pat", "machine_id": "machine-cn", "_token_version": int64(1),
	}}
	refreshedAccount := &accountcore.Record{ID: 3210, Platform: capability.PlatformQoder, Type: capability.AccountTypeCosy, Credentials: map[string]any{
		"site": "cn", "pat": "new-pat", "machine_id": "machine-cn", "_token_version": int64(2),
	}}

	oldSession, err := provider.GetSession(context.Background(), staleAccount)
	require.NoError(t, err)
	require.Equal(t, "cosy-old-pat", oldSession.Identity.SecurityOauthToken)
	provider.InvalidateAccount(refreshedAccount)

	fromStaleSnapshot, err := provider.GetSession(context.Background(), staleAccount)
	require.NoError(t, err)
	require.Equal(t, "cosy-new-pat", fromStaleSnapshot.Identity.SecurityOauthToken)
	require.Equal(t, int32(2), exchangeCalls.Load())

	provider.qoderState().Mu.Lock()
	currentState := provider.qoderState().AccountStates[staleAccount.ID]
	provider.qoderState().Mu.Unlock()
	require.Equal(t, int64(2), currentState.CredentialVersion)
	require.Equal(t, accountcore.QoderCredentialsHash(refreshedAccount.Credentials), currentState.CredentialsHash)
}

func TestQoderTokenProviderAuthoritativeInvalidationRejectsOlderSnapshot(t *testing.T) {
	provider := NewQoderTokenProvider(qoder.SessionBuilder{})
	var exchangeCalls atomic.Int32
	provider.exchangeCNPAT = func(_ context.Context, pat string, _ *qoder.MachineIdentity) (*qoder.AuthIdentity, time.Time, error) {
		exchangeCalls.Add(1)
		return &qoder.AuthIdentity{
			UID:                "uid-cn",
			AID:                "uid-cn",
			SecurityOauthToken: "cosy-" + pat,
		}, time.Now().Add(time.Hour), nil
	}
	now := time.Now()
	latestAccount := &accountcore.Record{ID: 3211, Platform: capability.PlatformQoder, Type: capability.AccountTypeCosy, UpdatedAt: now, Credentials: map[string]any{
		"site": "cn", "pat": "v3-pat", "machine_id": "machine-cn", "_token_version": int64(3),
	}}
	olderAccount := &accountcore.Record{ID: 3211, Platform: capability.PlatformQoder, Type: capability.AccountTypeCosy, UpdatedAt: now.Add(-time.Minute), Credentials: map[string]any{
		"site": "cn", "pat": "v2-pat", "machine_id": "machine-cn", "_token_version": int64(2),
	}}

	provider.InvalidateAccount(latestAccount)
	latestSession, err := provider.GetSession(context.Background(), latestAccount)
	require.NoError(t, err)
	provider.InvalidateAccount(olderAccount)

	provider.qoderState().Mu.Lock()
	cached := provider.qoderState().Sessions[latestAccount.ID]
	currentState := provider.qoderState().AccountStates[latestAccount.ID]
	provider.qoderState().Mu.Unlock()
	require.Same(t, latestSession, cached.Session)
	require.Equal(t, int64(3), currentState.CredentialVersion)
	require.Equal(t, accountcore.QoderCredentialsHash(latestAccount.Credentials), currentState.CredentialsHash)

	fromOlderSnapshot, err := provider.GetSession(context.Background(), olderAccount)
	require.NoError(t, err)
	require.Same(t, latestSession, fromOlderSnapshot)
	require.Equal(t, int32(1), exchangeCalls.Load())
}

func TestQoderTokenProviderNewerVersionOverridesOlderUpdatedAt(t *testing.T) {
	provider := NewQoderTokenProvider(qoder.SessionBuilder{})
	var exchangeCalls atomic.Int32
	provider.exchangeCNPAT = func(_ context.Context, pat string, _ *qoder.MachineIdentity) (*qoder.AuthIdentity, time.Time, error) {
		exchangeCalls.Add(1)
		return &qoder.AuthIdentity{
			UID:                "uid-cn",
			AID:                "uid-cn",
			SecurityOauthToken: "cosy-" + pat,
		}, time.Now().Add(time.Hour), nil
	}
	now := time.Now()
	currentAccount := &accountcore.Record{ID: 3212, Platform: capability.PlatformQoder, Type: capability.AccountTypeCosy, UpdatedAt: now, Credentials: map[string]any{
		"site": "cn", "pat": "v2-pat", "machine_id": "machine-cn", "_token_version": int64(2),
	}}
	refreshedAccount := &accountcore.Record{ID: 3212, Platform: capability.PlatformQoder, Type: capability.AccountTypeCosy, UpdatedAt: now.Add(-time.Minute), Credentials: map[string]any{
		"site": "cn", "pat": "v3-pat", "machine_id": "machine-cn", "_token_version": int64(3),
	}}

	currentSession, err := provider.GetSession(context.Background(), currentAccount)
	require.NoError(t, err)
	require.Equal(t, "cosy-v2-pat", currentSession.Identity.SecurityOauthToken)
	refreshedSession, err := provider.GetSession(context.Background(), refreshedAccount)
	require.NoError(t, err)
	require.Equal(t, "cosy-v3-pat", refreshedSession.Identity.SecurityOauthToken)
	require.Equal(t, int32(2), exchangeCalls.Load())
}

func TestQoderTokenProviderAuthoritativeNewerVersionOverridesOlderUpdatedAt(t *testing.T) {
	provider := NewQoderTokenProvider(qoder.SessionBuilder{})
	var exchangeCalls atomic.Int32
	provider.exchangeCNPAT = func(_ context.Context, pat string, _ *qoder.MachineIdentity) (*qoder.AuthIdentity, time.Time, error) {
		exchangeCalls.Add(1)
		return &qoder.AuthIdentity{
			UID:                "uid-cn",
			AID:                "uid-cn",
			SecurityOauthToken: "cosy-" + pat,
		}, time.Now().Add(time.Hour), nil
	}
	now := time.Now()
	currentAccount := &accountcore.Record{ID: 3213, Platform: capability.PlatformQoder, Type: capability.AccountTypeCosy, UpdatedAt: now, Credentials: map[string]any{
		"site": "cn", "pat": "v2-pat", "machine_id": "machine-cn", "_token_version": int64(2),
	}}
	refreshedAccount := &accountcore.Record{ID: 3213, Platform: capability.PlatformQoder, Type: capability.AccountTypeCosy, UpdatedAt: now.Add(-time.Minute), Credentials: map[string]any{
		"site": "cn", "pat": "v3-pat", "machine_id": "machine-cn", "_token_version": int64(3),
	}}

	_, err := provider.GetSession(context.Background(), currentAccount)
	require.NoError(t, err)
	provider.InvalidateAccount(refreshedAccount)
	fromStaleSnapshot, err := provider.GetSession(context.Background(), currentAccount)
	require.NoError(t, err)
	require.Equal(t, "cosy-v3-pat", fromStaleSnapshot.Identity.SecurityOauthToken)
	require.Equal(t, int32(2), exchangeCalls.Load())

	provider.qoderState().Mu.Lock()
	currentState := provider.qoderState().AccountStates[currentAccount.ID]
	provider.qoderState().Mu.Unlock()
	require.Equal(t, int64(3), currentState.CredentialVersion)
	require.Equal(t, accountcore.QoderCredentialsHash(refreshedAccount.Credentials), currentState.CredentialsHash)
}

func TestQoderTokenProviderOlderCredentialVersionJoinsNewerInflightBuild(t *testing.T) {
	provider := NewQoderTokenProvider(qoder.SessionBuilder{})
	var exchangeCalls atomic.Int32
	newBuildStarted := make(chan struct{})
	releaseNewBuild := make(chan struct{})
	provider.exchangeCNPAT = func(_ context.Context, pat string, _ *qoder.MachineIdentity) (*qoder.AuthIdentity, time.Time, error) {
		exchangeCalls.Add(1)
		if pat == "new-pat" {
			close(newBuildStarted)
			<-releaseNewBuild
		}
		return &qoder.AuthIdentity{
			UID:                "uid-cn",
			AID:                "uid-cn",
			SecurityOauthToken: "cosy-" + pat,
		}, time.Now().Add(time.Hour), nil
	}
	newAccount := &accountcore.Record{ID: 3208, Platform: capability.PlatformQoder, Type: capability.AccountTypeCosy, Credentials: map[string]any{
		"site": "cn", "pat": "new-pat", "machine_id": "machine-cn", "_token_version": int64(2),
	}}
	staleAccount := &accountcore.Record{ID: 3208, Platform: capability.PlatformQoder, Type: capability.AccountTypeCosy, Credentials: map[string]any{
		"site": "cn", "pat": "old-pat", "machine_id": "machine-cn", "_token_version": int64(1),
	}}

	type sessionResult struct {
		session *qoder.SessionContext
		err     error
	}
	newResult := make(chan sessionResult, 1)
	go func() {
		session, getErr := provider.GetSession(context.Background(), newAccount)
		newResult <- sessionResult{session: session, err: getErr}
	}()
	<-newBuildStarted

	staleResult := make(chan sessionResult, 1)
	go func() {
		session, getErr := provider.GetSession(context.Background(), staleAccount)
		staleResult <- sessionResult{session: session, err: getErr}
	}()
	// 旧版本若错误地发起独立交换会立即返回；正确行为是等待新版本的同一个 flight。
	select {
	case result := <-staleResult:
		close(releaseNewBuild)
		require.FailNowf(t, "stale build returned early", "session=%v err=%v", result.session, result.err)
	case <-time.After(30 * time.Millisecond):
	}
	close(releaseNewBuild)

	latest := <-newResult
	fromStaleSnapshot := <-staleResult
	require.NoError(t, latest.err)
	require.NoError(t, fromStaleSnapshot.err)
	require.Same(t, latest.session, fromStaleSnapshot.session)
	require.Equal(t, "cosy-new-pat", latest.session.Identity.SecurityOauthToken)
	require.Equal(t, int32(1), exchangeCalls.Load())
}

func TestQoderTokenProviderUpdatesSnapshotForSameCredentials(t *testing.T) {
	provider := NewQoderTokenProvider(qoder.SessionBuilder{})
	provider.exchangeCNPAT = func(_ context.Context, _ string, _ *qoder.MachineIdentity) (*qoder.AuthIdentity, time.Time, error) {
		return &qoder.AuthIdentity{UID: "uid-cn", AID: "uid-cn", SecurityOauthToken: "cosy-cn"}, time.Now().Add(time.Hour), nil
	}
	credentials := map[string]any{
		"site": "cn", "pat": "cn-pat", "machine_id": "machine-cn", "_token_version": int64(2),
	}
	oldProxy := &egress.Proxy{Protocol: "http", Host: "old-proxy.example", Port: 8080}
	newProxy := &egress.Proxy{Protocol: "http", Host: "new-proxy.example", Port: 8080}
	firstAccount := &accountcore.Record{ID: 3209, Platform: capability.PlatformQoder, Type: capability.AccountTypeCosy, Credentials: credentials, Proxy: oldProxy}
	latestAccount := &accountcore.Record{ID: 3209, Platform: capability.PlatformQoder, Type: capability.AccountTypeCosy, Credentials: maps.Clone(credentials), Proxy: newProxy}

	first, err := provider.GetSession(context.Background(), firstAccount)
	require.NoError(t, err)
	second, err := provider.GetSession(context.Background(), latestAccount)
	require.NoError(t, err)
	require.Same(t, first, second)

	provider.qoderState().Mu.Lock()
	storedSnapshot := provider.qoderState().AccountStates[latestAccount.ID].AccountSnapshot
	provider.qoderState().Mu.Unlock()
	require.NotNil(t, storedSnapshot)
	require.Equal(t, "new-proxy.example", storedSnapshot.Proxy.Host)
}

func TestQoderTokenProviderDirectTokenPopulatesOrganizationFromAPI(t *testing.T) {
	provider := NewQoderTokenProvider(qoder.SessionBuilder{})
	provider.getOrgTags = func(_ context.Context, token, uid string) (*qoder.OrganizationTags, error) {
		require.Equal(t, "dt-token", token)
		require.Equal(t, "uid-1", uid)
		return &qoder.OrganizationTags{
			OrganizationID:   "org-from-api",
			OrganizationName: "Org From API",
		}, nil
	}

	account := &accountcore.Record{
		ID:       110,
		Platform: capability.PlatformQoder,
		Type:     capability.AccountTypeCosy,
		Credentials: map[string]any{
			"security_oauth_token": "dt-token",
			"machine_id":           "machine-1",
			"uid":                  "uid-1",
		},
	}

	session, err := provider.GetSession(context.Background(), account)
	require.NoError(t, err)
	require.Equal(t, "org-from-api", session.Identity.OrganizationID)
	require.Equal(t, "Org From API", session.Identity.OrganizationName)
}

func TestQoderTokenProviderPATExchangeUsesAccountDoer(t *testing.T) {
	upstream := &qoderCenterHTTPUpstreamStub{}
	provider := NewQoderTokenProvider(qoder.SessionBuilder{})
	provider.SetHTTPUpstream(upstream, nil)
	account := &accountcore.Record{
		ID:          107,
		Platform:    capability.PlatformQoder,
		Type:        capability.AccountTypeCosy,
		Concurrency: 3,
		ProxyID:     ptrInt64ForQoderTest(9),
		Proxy:       &egress.Proxy{Protocol: "http", Host: "proxy.example.com", Port: 8080},
		Credentials: map[string]any{"pat": "pat-123"},
	}

	session, err := provider.GetSession(context.Background(), account)

	require.NoError(t, err)
	require.Equal(t, "dt-from-center", session.Identity.SecurityOauthToken)
	require.Equal(t, "http://proxy.example.com:8080", upstream.proxyURL)
	require.Equal(t, int64(107), upstream.accountID)
	require.Equal(t, 3, upstream.accountConcurrency)
}

func TestQoderTokenProviderPATOrganizationTagsUsesAccountDoer(t *testing.T) {
	upstream := &qoderCenterHTTPUpstreamStub{
		body: `{"organization_id":"org-via-upstream","organization_name":"Org Via Upstream"}`,
	}
	provider := NewQoderTokenProvider(qoder.SessionBuilder{})
	provider.SetHTTPUpstream(upstream, &providercore.TLSProfiles{})
	provider.exchangePAT = func(_ context.Context, _ string, _ *qoder.MachineIdentity) (*qoder.AuthIdentity, error) {
		return &qoder.AuthIdentity{
			Name:               "PAT User",
			UID:                "uid-1",
			AID:                "uid-1",
			UserType:           "personal_standard",
			SecurityOauthToken: "dt-from-pat",
		}, nil
	}
	account := &accountcore.Record{
		ID:          108,
		Platform:    capability.PlatformQoder,
		Type:        capability.AccountTypeCosy,
		Concurrency: 4,
		ProxyID:     ptrInt64ForQoderTest(10),
		Proxy:       &egress.Proxy{Protocol: "http", Host: "proxy.example.com", Port: 8081},
		Credentials: map[string]any{"pat": "pat-123"},
		Extra:       map[string]any{"enable_tls_fingerprint": true},
	}

	session, err := provider.GetSession(context.Background(), account)

	require.NoError(t, err)
	require.Equal(t, "org-via-upstream", session.Identity.OrganizationID)
	require.Equal(t, "Org Via Upstream", session.Identity.OrganizationName)
	require.Len(t, upstream.requests, 1)
	require.Contains(t, upstream.requests[0].URL.Path, qoder.OrganizationTagsPathPrefix+"uid-1/tags")
	require.Equal(t, "http://proxy.example.com:8081", upstream.proxyURL)
	require.Equal(t, int64(108), upstream.accountID)
	require.Equal(t, 4, upstream.accountConcurrency)
	require.True(t, upstream.profileSet)
}

func TestQoderTokenProviderOrganizationTagsErrorRedactsSensitiveBody(t *testing.T) {
	upstream := &qoderCenterHTTPUpstreamStub{
		statusCode: http.StatusInternalServerError,
		body:       `{"message":"failed","securityOauthToken":"sec-secret","refresh_token":"rt-secret","uid":"uid-secret","cookie":"sid=secret"}`,
	}
	provider := NewQoderTokenProvider(qoder.SessionBuilder{})
	provider.SetHTTPUpstream(upstream, nil)
	account := &accountcore.Record{
		ID:       109,
		Platform: capability.PlatformQoder,
		Type:     capability.AccountTypeCosy,
		Credentials: map[string]any{
			"security_oauth_token": "sec-token",
			"machine_id":           "machine-1",
			"uid":                  "uid-1",
		},
	}

	_, err := provider.getOrganizationTagsForAccount(context.Background(), account, "sec-token", "uid-1")

	require.Error(t, err)
	errText := err.Error()
	require.Contains(t, errText, "status 500")
	require.NotContains(t, errText, "sec-secret")
	require.NotContains(t, errText, "rt-secret")
	require.NotContains(t, errText, "uid-secret")
	require.NotContains(t, errText, "sid=secret")
	require.Contains(t, errText, "***")
}

type qoderCenterHTTPUpstreamStub struct {
	proxyURL           string
	accountID          int64
	accountConcurrency int
	statusCode         int
	body               string
	profileSet         bool
	requests           []*http.Request
}

func (s *qoderCenterHTTPUpstreamStub) Do(req *http.Request, proxyURL string, accountID int64, accountConcurrency int) (*http.Response, error) {
	return s.DoWithTLS(req, proxyURL, accountID, accountConcurrency, nil)
}

func (s *qoderCenterHTTPUpstreamStub) DoWithTLS(req *http.Request, proxyURL string, accountID int64, accountConcurrency int, profile *tlsfingerprint.Profile) (*http.Response, error) {
	s.proxyURL = proxyURL
	s.accountID = accountID
	s.accountConcurrency = accountConcurrency
	s.profileSet = profile != nil
	s.requests = append(s.requests, req)
	body := s.body
	if body == "" {
		body = `{
			"id":"user-1",
			"name":"User",
			"userType":"personal_standard",
			"securityOauthToken":"dt-from-center",
			"refreshToken":"rt-from-center"
		}`
	}
	statusCode := s.statusCode
	if statusCode == 0 {
		statusCode = http.StatusOK
	}
	return &http.Response{
		StatusCode: statusCode,
		Header:     make(http.Header),
		Body:       io.NopCloser(strings.NewReader(body)),
		Request:    req,
	}, nil
}

func ptrInt64ForQoderTest(v int64) *int64 {
	return &v
}
