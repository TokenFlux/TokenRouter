package service

import (
	"context"
	"encoding/json"
	"errors"
	"net/url"
	"testing"

	"github.com/TokenFlux/TokenRouter/internal/pkg/qoder"
	"github.com/stretchr/testify/require"
)

type fakeQoderOAuthClient struct {
	token       *qoder.DeviceTokenResponse
	ready       bool
	pollErr     error
	userInfo    *qoder.UserInfo
	userErr     error
	orgTags     *qoder.OrganizationTags
	orgErr      error
	gotNonce    string
	gotVerifier string
}

func (f *fakeQoderOAuthClient) PollDeviceToken(ctx context.Context, nonce, verifier string) (*qoder.DeviceTokenResponse, bool, error) {
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
	if f.orgErr != nil {
		return nil, f.orgErr
	}
	return f.orgTags, nil
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
	svc.clientFactory = func(proxyURL string) (qoderOAuthClient, error) {
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
	svc.clientFactory = func(proxyURL string) (qoderOAuthClient, error) {
		return &fakeQoderOAuthClient{
			ready: true,
			token: &qoder.DeviceTokenResponse{
				Token:        "security-token",
				RefreshToken: "refresh-token",
				UserID:       "user-from-token",
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
	require.Equal(t, authMachineID, tokenInfo.MachineID)
	require.NotEmpty(t, tokenInfo.MachineToken)
	require.NotEmpty(t, tokenInfo.MachineType)

	_, ok := svc.sessionStore.Get(result.SessionID)
	require.False(t, ok, "completed authorization should consume the session")

	credentials := svc.BuildAccountCredentials(tokenInfo)
	require.Equal(t, "security-token", credentials["security_oauth_token"])
	require.Equal(t, tokenInfo.MachineID, credentials["machine_id"])
	require.Equal(t, "org-from-tags", credentials["organization_id"])
	require.Equal(t, "Qoder Org", credentials["organization_name"])

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
	svc.clientFactory = func(proxyURL string) (qoderOAuthClient, error) {
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
