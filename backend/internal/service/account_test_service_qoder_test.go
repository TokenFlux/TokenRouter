package service

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/TokenFlux/TokenRouter/internal/pkg/qoder"
	"github.com/TokenFlux/TokenRouter/internal/pkg/tlsfingerprint"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

type qoderAccountTestSessionProviderStub struct {
	session *qoder.SessionContext
	err     error
}

func (s *qoderAccountTestSessionProviderStub) GetSession(context.Context, *Account) (*qoder.SessionContext, error) {
	if s.err != nil {
		return nil, s.err
	}
	return s.session, nil
}

type qoderAccountTestClientStub struct {
	request *http.Request
	body    string
	err     error
}

func (s *qoderAccountTestClientStub) StreamRequestContext(ctx context.Context, _ *qoder.SessionContext, _ string, bodyJSON []byte, _ map[string]string) (*http.Response, error) {
	req, _ := http.NewRequestWithContext(ctx, http.MethodPost, "https://api1.qoder.sh/test", strings.NewReader(string(bodyJSON)))
	s.request = req
	if s.err != nil {
		return nil, s.err
	}
	return &http.Response{
		StatusCode: http.StatusOK,
		Body:       io.NopCloser(strings.NewReader(s.body)),
	}, nil
}

func (s *qoderAccountTestClientStub) StreamRequestContextWithDoer(ctx context.Context, _ *qoder.SessionContext, _ string, bodyJSON []byte, _ map[string]string, doer qoder.RequestDoer) (*http.Response, error) {
	req, _ := http.NewRequestWithContext(ctx, http.MethodPost, "https://api1.qoder.sh/test", strings.NewReader(string(bodyJSON)))
	s.request = req
	if s.err != nil {
		return nil, s.err
	}
	return doer(req)
}

type qoderAccountTestOAuthClientStub struct {
	token string
	err   error
}

func (s *qoderAccountTestOAuthClientStub) GetUserInfo(_ context.Context, token string) (*qoder.UserInfo, error) {
	s.token = token
	if s.err != nil {
		return nil, s.err
	}
	return &qoder.UserInfo{ID: "user-1", Name: "Qoder User"}, nil
}

type qoderHTTPUpstreamRecorder struct {
	body       string
	proxyURL   string
	accountID  int64
	profileSet bool
}

func (u *qoderHTTPUpstreamRecorder) Do(req *http.Request, proxyURL string, accountID int64, accountConcurrency int) (*http.Response, error) {
	return u.DoWithTLS(req, proxyURL, accountID, accountConcurrency, nil)
}

func (u *qoderHTTPUpstreamRecorder) DoWithTLS(req *http.Request, proxyURL string, accountID int64, _ int, profile *tlsfingerprint.Profile) (*http.Response, error) {
	u.proxyURL = proxyURL
	u.accountID = accountID
	u.profileSet = profile != nil
	return &http.Response{
		StatusCode: http.StatusOK,
		Body:       io.NopCloser(strings.NewReader(u.body)),
	}, nil
}

func newQoderAccountTestContext() (*gin.Context, *httptest.ResponseRecorder) {
	gin.SetMode(gin.TestMode)
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodPost, "/api/v1/admin/accounts/7/test", nil)
	return c, rec
}

func TestAccountTestService_QoderCosyUsesNativeTestPath(t *testing.T) {
	ctx, recorder := newQoderAccountTestContext()
	account := &Account{
		ID:          7,
		Name:        "qoder",
		Platform:    PlatformQoder,
		Type:        AccountTypeCosy,
		Concurrency: 1,
		Credentials: map[string]any{
			"security_oauth_token": "token",
			"machine_id":           "machine",
		},
	}
	repo := stubOpenAIAccountRepo{accounts: []Account{*account}}
	client := &qoderAccountTestClientStub{
		body: "data: {\"body\":\"{\\\"choices\\\":[{\\\"delta\\\":{\\\"reasoning_content\\\":\\\"hidden thought\\\"}}]}\"}\n\n" +
			"data: {\"body\":\"{\\\"choices\\\":[{\\\"delta\\\":{\\\"content\\\":\\\"OK\\\"}}]}\"}\n\n" +
			"data: {\"body\":\"[DONE]\"}\n\n",
	}
	svc := &AccountTestService{
		accountRepo: repo,
		qoderSessionProvider: &qoderAccountTestSessionProviderStub{
			session: &qoder.SessionContext{Identity: &qoder.AuthIdentity{SecurityOauthToken: "token"}},
		},
		qoderClient:      client,
		qoderOAuthClient: &qoderAccountTestOAuthClientStub{},
	}

	err := svc.TestAccountConnection(ctx, account.ID, "gpt-5-codex", "hi", "")

	require.NoError(t, err)
	body := recorder.Body.String()
	require.Contains(t, body, `"type":"content"`)
	require.Contains(t, body, `"text":"OK"`)
	require.NotContains(t, body, "hidden thought")
	require.Contains(t, body, `"type":"test_complete"`)
	require.NotContains(t, body, "Unsupported account type: cosy")
	require.NotNil(t, client.request)
}

func TestAccountTestService_QoderProbesUserInfoBeforeStream(t *testing.T) {
	ctx, recorder := newQoderAccountTestContext()
	account := &Account{
		ID:       11,
		Name:     "qoder",
		Platform: PlatformQoder,
		Type:     AccountTypeCosy,
	}
	client := &qoderAccountTestClientStub{
		body: "data: {\"body\":\"[DONE]\"}\n\n",
	}
	oauthClient := &qoderAccountTestOAuthClientStub{}
	svc := &AccountTestService{
		accountRepo: stubOpenAIAccountRepo{accounts: []Account{*account}},
		qoderSessionProvider: &qoderAccountTestSessionProviderStub{
			session: &qoder.SessionContext{Identity: &qoder.AuthIdentity{SecurityOauthToken: "session-token"}},
		},
		qoderClient:      client,
		qoderOAuthClient: oauthClient,
	}

	err := svc.TestAccountConnection(ctx, account.ID, "", "", "")

	require.NoError(t, err)
	require.Equal(t, "session-token", oauthClient.token)
	require.Contains(t, recorder.Body.String(), `"type":"test_complete"`)
}

func TestAccountTestService_QoderWrappedErrorIsVisible(t *testing.T) {
	ctx, recorder := newQoderAccountTestContext()
	account := &Account{
		ID:       8,
		Name:     "qoder",
		Platform: PlatformQoder,
		Type:     AccountTypeCosy,
	}
	client := &qoderAccountTestClientStub{
		body: "data: {\"body\":\"{\\\"code\\\":\\\"101\\\",\\\"message\\\":\\\"Signature invalid\\\"}\",\"statusCodeValue\":403,\"statusCode\":\"FORBIDDEN\"}\n\n",
	}
	svc := &AccountTestService{
		accountRepo: stubOpenAIAccountRepo{accounts: []Account{*account}},
		qoderSessionProvider: &qoderAccountTestSessionProviderStub{
			session: &qoder.SessionContext{Identity: &qoder.AuthIdentity{SecurityOauthToken: "token"}},
		},
		qoderClient:      client,
		qoderOAuthClient: &qoderAccountTestOAuthClientStub{},
	}

	err := svc.TestAccountConnection(ctx, account.ID, "", "", "")

	require.Error(t, err)
	body := recorder.Body.String()
	require.Contains(t, body, `"type":"error"`)
	require.Contains(t, body, "Qoder upstream error 101: Signature invalid")
	require.NotContains(t, body, "Unsupported account type: cosy")
}

func TestAccountTestService_QoderReasoningOnlyDoesNotEmitContent(t *testing.T) {
	ctx, recorder := newQoderAccountTestContext()
	account := &Account{
		ID:       9,
		Name:     "qoder",
		Platform: PlatformQoder,
		Type:     AccountTypeCosy,
	}
	client := &qoderAccountTestClientStub{
		body: "data: {\"body\":\"{\\\"choices\\\":[{\\\"delta\\\":{\\\"reasoning_content\\\":\\\"hidden thought\\\"}}]}\"}\n\n" +
			"data: {\"body\":\"[DONE]\"}\n\n",
	}
	svc := &AccountTestService{
		accountRepo: stubOpenAIAccountRepo{accounts: []Account{*account}},
		qoderSessionProvider: &qoderAccountTestSessionProviderStub{
			session: &qoder.SessionContext{Identity: &qoder.AuthIdentity{SecurityOauthToken: "token"}},
		},
		qoderClient:      client,
		qoderOAuthClient: &qoderAccountTestOAuthClientStub{},
	}

	err := svc.TestAccountConnection(ctx, account.ID, "", "", "")

	require.NoError(t, err)
	body := recorder.Body.String()
	require.NotContains(t, body, `"type":"content"`)
	require.NotContains(t, body, "hidden thought")
	require.Contains(t, body, `"type":"test_complete"`)
}

func TestAccountTestService_QoderUsesHTTPUpstreamForProxyAndTLS(t *testing.T) {
	ctx, recorder := newQoderAccountTestContext()
	proxyID := int64(12)
	account := &Account{
		ID:          10,
		Name:        "qoder",
		Platform:    PlatformQoder,
		Type:        AccountTypeCosy,
		Concurrency: 2,
		ProxyID:     &proxyID,
		Proxy: &Proxy{
			ID:       proxyID,
			Protocol: "http",
			Host:     "proxy.example.com",
			Port:     8080,
		},
		Extra: map[string]any{
			"enable_tls_fingerprint": true,
		},
	}
	client := &qoderAccountTestClientStub{}
	upstream := &qoderHTTPUpstreamRecorder{
		body: "data: {\"body\":\"{\\\"choices\\\":[{\\\"delta\\\":{\\\"content\\\":\\\"OK\\\"}}]}\"}\n\n" +
			"data: {\"body\":\"[DONE]\"}\n\n",
	}
	svc := &AccountTestService{
		accountRepo: stubOpenAIAccountRepo{accounts: []Account{*account}},
		qoderSessionProvider: &qoderAccountTestSessionProviderStub{
			session: &qoder.SessionContext{Identity: &qoder.AuthIdentity{SecurityOauthToken: "token"}},
		},
		qoderClient:         client,
		qoderOAuthClient:    &qoderAccountTestOAuthClientStub{},
		httpUpstream:        upstream,
		tlsFPProfileService: &TLSFingerprintProfileService{},
	}

	err := svc.TestAccountConnection(ctx, account.ID, "", "", "")

	require.NoError(t, err)
	require.Contains(t, recorder.Body.String(), `"text":"OK"`)
	require.Equal(t, "http://proxy.example.com:8080", upstream.proxyURL)
	require.Equal(t, int64(10), upstream.accountID)
	require.True(t, upstream.profileSet)
	require.NotNil(t, client.request)
}
