package service

import (
	"context"
	"encoding/json"
	"errors"
	"net/url"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/BrandonVee/TokenRouter/internal/pkg/qoder"
	"github.com/stretchr/testify/require"
)

type fakeQoderOAuthClient struct {
	token             *qoder.DeviceTokenResponse
	ready             bool
	pollErr           error
	pollCalls         int
	userInfo          *qoder.UserInfo
	userErr           error
	orgTags           *qoder.OrganizationTags
	orgErr            error
	orgCalls          int
	gotOrgUID         string
	gotNonce          string
	gotVerifier       string
	completedIdentity *qoder.AuthIdentity
	completedExpiry   time.Time
	completionErr     error
	completionCalls   int
	completedMachine  *qoder.MachineIdentity
}

type blockingQoderOAuthClient struct {
	started   chan struct{}
	release   chan struct{}
	startOnce sync.Once
	pollCalls atomic.Int32
}

func (f *fakeQoderOAuthClient) PollDeviceToken(ctx context.Context, nonce, verifier string) (*qoder.DeviceTokenResponse, bool, error) {
	f.pollCalls++
	f.gotNonce = nonce
	f.gotVerifier = verifier
	return f.token, f.ready, f.pollErr
}

func (f *fakeQoderOAuthClient) GetUserInfo(ctx context.Context, token string) (*qoder.UserInfo, error) {
	if f.userErr != nil {
		return nil, f.userErr
	}
	return f.userInfo, nil
}

func (f *fakeQoderOAuthClient) GetOrganizationTags(ctx context.Context, token, uid string) (*qoder.OrganizationTags, error) {
	f.orgCalls++
	f.gotOrgUID = uid
	if f.orgErr != nil {
		return nil, f.orgErr
	}
	return f.orgTags, nil
}

func (f *fakeQoderOAuthClient) CompleteQoderCN20Identity(
	_ context.Context,
	_ *qoder.DeviceTokenResponse,
	_ *qoder.UserInfo,
	machine *qoder.MachineIdentity,
) (*qoder.AuthIdentity, time.Time, error) {
	f.completionCalls++
	if machine != nil {
		f.completedMachine = &qoder.MachineIdentity{
			MachineID:    machine.MachineID,
			MachineToken: machine.MachineToken,
			MachineType:  machine.MachineType,
		}
	}
	return f.completedIdentity, f.completedExpiry, f.completionErr
}

func (f *blockingQoderOAuthClient) PollDeviceToken(ctx context.Context, nonce, verifier string) (*qoder.DeviceTokenResponse, bool, error) {
	f.pollCalls.Add(1)
	f.startOnce.Do(func() { close(f.started) })
	select {
	case <-ctx.Done():
		return nil, false, ctx.Err()
	case <-f.release:
		return &qoder.DeviceTokenResponse{
			AccessToken:  "access-token",
			RefreshToken: "refresh-token",
			UserID:       "user-1",
		}, true, nil
	}
}

func (f *blockingQoderOAuthClient) GetUserInfo(ctx context.Context, token string) (*qoder.UserInfo, error) {
	return &qoder.UserInfo{ID: "user-1", Name: "Qoder User"}, nil
}

func (f *blockingQoderOAuthClient) GetOrganizationTags(ctx context.Context, token, uid string) (*qoder.OrganizationTags, error) {
	return nil, nil
}

func TestQoderOAuthServiceGenerateAuthURLCreatesSession(t *testing.T) {
	svc := NewQoderOAuthService(nil)
	defer svc.Stop()

	result, err := svc.GenerateAuthURL(context.Background(), nil)
	require.NoError(t, err)
	require.NotEmpty(t, result.AuthURL)
	require.NotEmpty(t, result.SessionID)
	require.NotEmpty(t, result.State)
	require.Positive(t, result.ExpiresIn)
	require.Equal(t, qoderOAuthPollInterval, result.Interval)

	session, ok := svc.sessionStore.Get(result.SessionID)
	require.True(t, ok)
	require.Equal(t, result.State, session.State)
	require.NotNil(t, session.Machine)
	require.Contains(t, result.AuthURL, "nonce="+session.Nonce)
	require.Contains(t, result.AuthURL, "challenge=")
	require.Contains(t, result.AuthURL, "client_id="+qoder.OAuthClientID)
	authURL, err := url.Parse(result.AuthURL)
	require.NoError(t, err)
	require.Equal(t, session.Machine.MachineID, authURL.Query().Get("machine_id"))
}

func TestQoderOAuthServiceCNFreezesSiteAndIgnoresPollProxyOverride(t *testing.T) {
	svc := NewQoderOAuthService(nil)
	defer svc.Stop()
	expiresAt := time.Now().Add(time.Hour).UTC().Truncate(time.Second)
	client := &fakeQoderOAuthClient{
		ready: true,
		token: &qoder.DeviceTokenResponse{
			Token:        "openapi-access",
			RefreshToken: "openapi-refresh",
			UserID:       "user-1",
			ExpiresIn:    3600,
		},
		userInfo: &qoder.UserInfo{UserID: "user-1", UserName: "CN User"},
		completedIdentity: &qoder.AuthIdentity{
			UID:                "cosy-uid",
			AID:                "cosy-aid",
			OrganizationID:     "status-org",
			OrganizationName:   "Status Org",
			SecurityOauthToken: "cosy-token",
			RefreshToken:       "openapi-refresh",
			UserType:           "personal_standard",
		},
		orgTags: &qoder.OrganizationTags{
			OrganizationID:   "tags-org",
			OrganizationName: "Tags Org",
		},
		completedExpiry: expiresAt,
	}
	var capturedProfile qoder.Profile
	var capturedProxy string
	svc.clientFactory = func(profile qoder.Profile, proxyURL string) (qoderOAuthClient, error) {
		capturedProfile = profile
		capturedProxy = proxyURL
		return client, nil
	}

	result, err := svc.GenerateAuthURLForSite(context.Background(), qoder.SiteCN, nil)
	require.NoError(t, err)
	require.Equal(t, "cn", result.Site)
	parsed, err := url.Parse(result.AuthURL)
	require.NoError(t, err)
	require.Equal(t, "qoder.com.cn", parsed.Host)
	require.Equal(t, qoder.CNOAuthClientID, parsed.Query().Get("client_id"))
	require.NotContains(t, parsed.Query().Get("nonce"), "-")
	require.Len(t, parsed.Query().Get("machine_id"), 36)
	session, ok := svc.sessionStore.Get(result.SessionID)
	require.True(t, ok)
	require.Empty(t, session.Machine.MachineToken)
	require.Empty(t, session.Machine.MachineType)

	ignoredProxyID := int64(9999)
	completed, err := svc.Poll(context.Background(), result.SessionID, result.State, &ignoredProxyID)
	require.NoError(t, err)
	require.Equal(t, "completed", completed.Status)
	require.Equal(t, qoder.SiteCN, capturedProfile.Site)
	require.Empty(t, capturedProxy)
	require.Equal(t, 1, client.completionCalls)
	require.Equal(t, "cosy-token", completed.TokenInfo.SecurityOauthToken)
	require.Equal(t, "cn", completed.TokenInfo.Site)
	require.Equal(t, qoder.RefreshModeQoderCN20, completed.TokenInfo.RefreshMode)
	require.Equal(t, expiresAt.Format(time.RFC3339), completed.TokenInfo.ExpiresAt)
	require.Equal(t, "status-org", completed.TokenInfo.OrganizationID)
	require.Equal(t, "Status Org", completed.TokenInfo.OrganizationName)
	require.Zero(t, client.orgCalls)
	require.NotNil(t, client.completedMachine)
	require.Equal(t, parsed.Query().Get("machine_id"), client.completedMachine.MachineID)
	require.Empty(t, client.completedMachine.MachineToken)
	require.Empty(t, client.completedMachine.MachineType)
	require.Empty(t, completed.TokenInfo.MachineToken)
	require.Empty(t, completed.TokenInfo.MachineType)

	credentials := svc.BuildAccountCredentials(completed.TokenInfo)
	require.Equal(t, "cn", credentials["site"])
	require.Equal(t, qoder.RefreshModeQoderCN20, credentials["refresh_mode"])
	require.Equal(t, expiresAt.Format(time.RFC3339), credentials["expires_at"])
	require.NotContains(t, credentials, "machine_token")
	require.NotContains(t, credentials, "machine_type")
}

func TestQoderOAuthServiceCNUsesOrganizationTagsAsStatusFallback(t *testing.T) {
	svc := NewQoderOAuthService(nil)
	defer svc.Stop()
	client := &fakeQoderOAuthClient{
		ready: true,
		token: &qoder.DeviceTokenResponse{
			Token:        "openapi-access",
			RefreshToken: "openapi-refresh",
			UserID:       "user-1",
			ExpiresIn:    3600,
		},
		userInfo: &qoder.UserInfo{UserID: "user-1", UserName: "CN User"},
		completedIdentity: &qoder.AuthIdentity{
			UID:                "cosy-uid",
			AID:                "cosy-aid",
			SecurityOauthToken: "cosy-token",
			RefreshToken:       "openapi-refresh",
		},
		completedExpiry: time.Now().Add(time.Hour),
		orgTags: &qoder.OrganizationTags{
			OrganizationID:   "tags-org",
			OrganizationName: "Tags Org",
		},
	}
	svc.clientFactory = func(_ qoder.Profile, _ string) (qoderOAuthClient, error) {
		return client, nil
	}

	result, err := svc.GenerateAuthURLForSite(context.Background(), qoder.SiteCN, nil)
	require.NoError(t, err)
	completed, err := svc.Poll(context.Background(), result.SessionID, result.State, nil)
	require.NoError(t, err)
	require.Equal(t, "tags-org", completed.TokenInfo.OrganizationID)
	require.Equal(t, "Tags Org", completed.TokenInfo.OrganizationName)
	require.Equal(t, 1, client.orgCalls)
	require.Equal(t, "user-1", client.gotOrgUID)
}

func TestQoderOAuthServiceExchangeRejectsInvalidSessionState(t *testing.T) {
	svc := NewQoderOAuthService(nil)
	defer svc.Stop()

	_, err := svc.ExchangeCode(context.Background(), &QoderExchangeCodeInput{
		SessionID: "missing",
		State:     "state",
	})
	require.ErrorContains(t, err, "session not found")

	result, err := svc.GenerateAuthURL(context.Background(), nil)
	require.NoError(t, err)
	_, err = svc.ExchangeCode(context.Background(), &QoderExchangeCodeInput{
		SessionID: result.SessionID,
		State:     "wrong-state",
	})
	require.ErrorContains(t, err, "state is invalid")
}

func TestQoderOAuthServiceExchangePendingKeepsSession(t *testing.T) {
	svc := NewQoderOAuthService(nil)
	defer svc.Stop()
	client := &fakeQoderOAuthClient{ready: false}
	svc.clientFactory = func(_ qoder.Profile, proxyURL string) (qoderOAuthClient, error) {
		return client, nil
	}

	result, err := svc.GenerateAuthURL(context.Background(), nil)
	require.NoError(t, err)
	_, err = svc.ExchangeCode(context.Background(), &QoderExchangeCodeInput{
		SessionID: result.SessionID,
		State:     result.State,
		Code:      "completed",
	})
	require.ErrorContains(t, err, "still pending")

	_, ok := svc.sessionStore.Get(result.SessionID)
	require.True(t, ok, "pending authorization should keep the session available")
	session, _ := svc.sessionStore.Get(result.SessionID)
	require.Equal(t, session.Nonce, client.gotNonce)
	require.Equal(t, session.CodeVerifier, client.gotVerifier)
}

func TestQoderOAuthServiceExchangeParsesCallbackURLAndBuildsUsableCredentials(t *testing.T) {
	svc := NewQoderOAuthService(nil)
	defer svc.Stop()
	expiresAt := time.Unix(1_893_456_000, 0).UTC()
	svc.clientFactory = func(_ qoder.Profile, proxyURL string) (qoderOAuthClient, error) {
		return &fakeQoderOAuthClient{
			ready: true,
			token: &qoder.DeviceTokenResponse{
				Token:        "security-token",
				RefreshToken: "refresh-token",
				UserID:       "user-from-token",
				ExpiresAt:    qoder.FlexibleInt64(expiresAt.Unix()),
			},
			userInfo: &qoder.UserInfo{
				ID:       "user-from-info",
				Name:     "Qoder User",
				UserType: "personal_pro",
			},
			orgTags: &qoder.OrganizationTags{
				OrganizationID:   "org-from-tags",
				OrganizationName: "Qoder Org",
			},
		}, nil
	}

	result, err := svc.GenerateAuthURL(context.Background(), nil)
	require.NoError(t, err)
	authURL, err := url.Parse(result.AuthURL)
	require.NoError(t, err)
	authMachineID := authURL.Query().Get("machine_id")
	require.NotEmpty(t, authMachineID)
	tokenInfo, err := svc.ExchangeCode(context.Background(), &QoderExchangeCodeInput{
		SessionID:   result.SessionID,
		CallbackURL: "http://localhost:12345/callback?code=ignored-by-device-flow&state=" + result.State,
	})
	require.NoError(t, err)
	require.Equal(t, "security-token", tokenInfo.SecurityOauthToken)
	require.Equal(t, "refresh-token", tokenInfo.RefreshToken)
	require.Equal(t, "user-from-info", tokenInfo.UID)
	require.Equal(t, "user-from-info", tokenInfo.AID)
	require.Equal(t, "org-from-tags", tokenInfo.OrganizationID)
	require.Equal(t, "Qoder Org", tokenInfo.OrganizationName)
	require.Equal(t, "Qoder User", tokenInfo.Name)
	require.Equal(t, "personal_pro", tokenInfo.UserType)
	require.Equal(t, "global", tokenInfo.Site)
	require.Equal(t, qoder.RefreshModeCosy, tokenInfo.RefreshMode)
	require.Equal(t, expiresAt.Format(time.RFC3339), tokenInfo.ExpiresAt)
	require.Equal(t, authMachineID, tokenInfo.MachineID)
	require.NotEmpty(t, tokenInfo.MachineToken)
	require.NotEmpty(t, tokenInfo.MachineType)

	sessionAfterComplete, ok := svc.sessionStore.Get(result.SessionID)
	require.True(t, ok, "completed authorization should remain available for idempotent retry")
	require.NotNil(t, sessionAfterComplete.CompletedTokenInfo)

	credentials := svc.BuildAccountCredentials(tokenInfo)
	require.Equal(t, "security-token", credentials["security_oauth_token"])
	require.Equal(t, tokenInfo.MachineID, credentials["machine_id"])
	require.Equal(t, "org-from-tags", credentials["organization_id"])
	require.Equal(t, "Qoder Org", credentials["organization_name"])
	require.Equal(t, expiresAt.Format(time.RFC3339), credentials["expires_at"])

	provider := NewQoderTokenProvider()
	session, err := provider.GetSession(context.Background(), &Account{
		ID:          991,
		Name:        "qoder-oauth",
		Platform:    PlatformQoder,
		Type:        AccountTypeCosy,
		Credentials: credentials,
	})
	require.NoError(t, err)
	require.Equal(t, "security-token", session.Identity.SecurityOauthToken)
	require.Equal(t, "org-from-tags", session.Identity.OrganizationID)
	require.Equal(t, "Qoder Org", session.Identity.OrganizationName)
	require.Equal(t, tokenInfo.MachineID, session.Machine.MachineID)
	require.Equal(t, tokenInfo.MachineToken, session.Machine.MachineToken)
}

func TestQoderOAuthServicePollReturnsPendingAndCompleted(t *testing.T) {
	svc := NewQoderOAuthService(nil)
	defer svc.Stop()
	client := &fakeQoderOAuthClient{ready: false}
	svc.clientFactory = func(_ qoder.Profile, proxyURL string) (qoderOAuthClient, error) {
		return client, nil
	}

	result, err := svc.GenerateAuthURL(context.Background(), nil)
	require.NoError(t, err)
	authURL, err := url.Parse(result.AuthURL)
	require.NoError(t, err)
	pending, err := svc.Poll(context.Background(), result.SessionID, result.State, nil)
	require.NoError(t, err)
	require.Equal(t, "pending", pending.Status)
	require.Nil(t, pending.TokenInfo)

	client.ready = true
	client.token = &qoder.DeviceTokenResponse{AccessToken: "access-token", UserID: "user-1"}
	client.userErr = errors.New("userinfo unavailable")
	client.orgErr = errors.New("organization unavailable")
	completed, err := svc.Poll(context.Background(), result.SessionID, result.State, nil)
	require.NoError(t, err)
	require.Equal(t, "completed", completed.Status)
	require.Equal(t, "access-token", completed.TokenInfo.SecurityOauthToken)
	require.Equal(t, authURL.Query().Get("machine_id"), completed.TokenInfo.MachineID)
	require.Equal(t, "user-1", completed.TokenInfo.UID)
	require.Equal(t, map[string]string{
		"code":    "userinfo_unavailable",
		"message": "Qoder user info could not be loaded",
	}, completed.TokenInfo.Extra["userinfo_warning"])
	require.Equal(t, map[string]string{
		"code":    "organization_unavailable",
		"message": "Qoder organization info could not be loaded",
	}, completed.TokenInfo.Extra["organization_warning"])
}

func TestQoderOAuthServiceCompletedSessionIsIdempotent(t *testing.T) {
	svc := NewQoderOAuthService(nil)
	defer svc.Stop()
	client := &fakeQoderOAuthClient{
		ready: true,
		token: &qoder.DeviceTokenResponse{
			AccessToken:  "access-token",
			RefreshToken: "refresh-token",
			UserID:       "user-1",
		},
		userInfo: &qoder.UserInfo{ID: "user-1", Name: "Qoder User"},
	}
	svc.clientFactory = func(_ qoder.Profile, proxyURL string) (qoderOAuthClient, error) {
		return client, nil
	}

	result, err := svc.GenerateAuthURL(context.Background(), nil)
	require.NoError(t, err)
	completed, err := svc.Poll(context.Background(), result.SessionID, result.State, nil)
	require.NoError(t, err)
	require.Equal(t, "completed", completed.Status)
	require.Equal(t, "access-token", completed.TokenInfo.SecurityOauthToken)

	tokenInfo, err := svc.ExchangeCode(context.Background(), &QoderExchangeCodeInput{
		SessionID: result.SessionID,
		State:     result.State,
		Code:      "ignored-by-device-flow",
	})
	require.NoError(t, err)
	require.Equal(t, "access-token", tokenInfo.SecurityOauthToken)
	require.Equal(t, 1, client.pollCalls, "completed session should return cached token info")
}

func TestQoderOAuthServiceConcurrentCompletionReusesSingleResult(t *testing.T) {
	svc := NewQoderOAuthService(nil)
	defer svc.Stop()
	client := &blockingQoderOAuthClient{
		started: make(chan struct{}),
		release: make(chan struct{}),
	}
	svc.clientFactory = func(_ qoder.Profile, proxyURL string) (qoderOAuthClient, error) {
		return client, nil
	}

	result, err := svc.GenerateAuthURL(context.Background(), nil)
	require.NoError(t, err)

	pollDone := make(chan *QoderPollResult, 1)
	pollErr := make(chan error, 1)
	go func() {
		pollResult, pollErrValue := svc.Poll(context.Background(), result.SessionID, result.State, nil)
		pollDone <- pollResult
		pollErr <- pollErrValue
	}()
	<-client.started

	exchangeDone := make(chan *QoderTokenInfo, 1)
	exchangeErr := make(chan error, 1)
	go func() {
		tokenInfo, exchangeErrValue := svc.ExchangeCode(context.Background(), &QoderExchangeCodeInput{
			SessionID: result.SessionID,
			State:     result.State,
			Code:      "ignored-by-device-flow",
		})
		exchangeDone <- tokenInfo
		exchangeErr <- exchangeErrValue
	}()

	select {
	case <-exchangeDone:
		t.Fatal("exchange should wait for the in-flight completion")
	case <-time.After(20 * time.Millisecond):
	}

	close(client.release)
	pollResult := <-pollDone
	require.NoError(t, <-pollErr)
	require.Equal(t, "completed", pollResult.Status)

	tokenInfo := <-exchangeDone
	require.NoError(t, <-exchangeErr)
	require.Equal(t, "access-token", tokenInfo.SecurityOauthToken)
	require.Equal(t, int32(1), client.pollCalls.Load())
}

func TestQoderOAuthServiceWarningsDoNotPersistRawUpstreamErrors(t *testing.T) {
	rawErr := errors.New(`upstream 500 {"access_token":"secret-token","email":"user@example.com","account_id":"aid-123"}`)

	tokenInfo := buildQoderTokenInfo(&qoder.AuthIdentity{
		SecurityOauthToken: "security-token",
		UID:                "user-1",
	}, &qoder.MachineIdentity{MachineID: "machine-1"}, rawErr, rawErr)
	body, err := json.Marshal(tokenInfo.Extra)
	require.NoError(t, err)

	require.NotContains(t, string(body), "secret-token")
	require.NotContains(t, string(body), "user@example.com")
	require.NotContains(t, string(body), "aid-123")
	require.Contains(t, string(body), "userinfo_unavailable")
	require.Contains(t, string(body), "organization_unavailable")
}

func TestQoderParseCallbackSupportsQueryFragmentAndPlainCode(t *testing.T) {
	state, code := parseQoderCallback("http://localhost/callback?code=query-code&state=query-state")
	require.Equal(t, "query-state", state)
	require.Equal(t, "query-code", code)

	state, code = parseQoderCallback("http://localhost/callback#code=fragment-code&state=fragment-state")
	require.Equal(t, "fragment-state", state)
	require.Equal(t, "fragment-code", code)

	state, code = parseQoderCallback("plain-code")
	require.Empty(t, state)
	require.Equal(t, "plain-code", code)
}
