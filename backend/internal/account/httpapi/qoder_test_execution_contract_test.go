package httpapi

import (
	"context"
	"io"
	"log"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	accountprovider "github.com/TokenFlux/TokenRouter/internal/account/provider"
	openaiprotocol "github.com/TokenFlux/TokenRouter/internal/protocol/openai"

	accountcore "github.com/TokenFlux/TokenRouter/internal/account"
	"github.com/TokenFlux/TokenRouter/internal/egress"
	providercore "github.com/TokenFlux/TokenRouter/internal/egress/provider"
	"github.com/TokenFlux/TokenRouter/internal/infra/httpclient/tlsfingerprint"
	"github.com/TokenFlux/TokenRouter/internal/routing/capability"
	"github.com/TokenFlux/TokenRouter/internal/upstream/qoder"
	"github.com/stretchr/testify/require"
)

type qoderAccountTestSessionProviderStub struct {
	session     *qoder.SessionContext
	err         error
	invalidated []int64
}

func (s *qoderAccountTestSessionProviderStub) GetSession(context.Context, *accountcore.Record) (*qoder.SessionContext, error) {
	if s.err != nil {
		return nil, s.err
	}
	return s.session, nil
}

func (s *qoderAccountTestSessionProviderStub) Invalidate(accountID int64) {
	s.invalidated = append(s.invalidated, accountID)
}

type qoderAccountTestClientStub struct {
	request  *http.Request
	requests []*http.Request
	body     string
	bodies   [][]byte
	err      error
	headers  map[string]string
}

func (s *qoderAccountTestClientStub) StreamRequestContext(ctx context.Context, _ *qoder.SessionContext, _ string, bodyJSON []byte, headers map[string]string) (*http.Response, error) {
	req, _ := http.NewRequestWithContext(ctx, http.MethodPost, "https://api1.qoder.sh/test", strings.NewReader(string(bodyJSON)))
	s.request = req
	s.requests = append(s.requests, req)
	s.bodies = append(s.bodies, append([]byte(nil), bodyJSON...))
	s.headers = headers
	if s.err != nil {
		return nil, s.err
	}
	return &http.Response{
		StatusCode: http.StatusOK,
		Body:       io.NopCloser(strings.NewReader(s.body)),
	}, nil
}

func (s *qoderAccountTestClientStub) StreamRequestContextWithDoer(ctx context.Context, _ *qoder.SessionContext, _ string, bodyJSON []byte, headers map[string]string, doer qoder.RequestDoer) (*http.Response, error) {
	req, _ := http.NewRequestWithContext(ctx, http.MethodPost, "https://api1.qoder.sh/test", strings.NewReader(string(bodyJSON)))
	s.request = req
	s.requests = append(s.requests, req)
	s.bodies = append(s.bodies, append([]byte(nil), bodyJSON...))
	s.headers = headers
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
	body               string
	userInfoBody       string
	userInfoStatusCode int
	proxyURL           string
	accountID          int64
	accountConcurrency int
	profileSet         bool
	requests           []*http.Request
}

func (u *qoderHTTPUpstreamRecorder) Do(req *http.Request, proxyURL string, accountID int64, accountConcurrency int) (*http.Response, error) {
	return u.DoWithTLS(req, proxyURL, accountID, accountConcurrency, nil)
}

func (u *qoderHTTPUpstreamRecorder) DoWithTLS(req *http.Request, proxyURL string, accountID int64, accountConcurrency int, profile *tlsfingerprint.Profile) (*http.Response, error) {
	u.proxyURL = proxyURL
	u.accountID = accountID
	u.accountConcurrency = accountConcurrency
	u.profileSet = profile != nil
	u.requests = append(u.requests, req)
	body := u.body
	if req.Method == http.MethodGet && strings.Contains(req.URL.Path, qoder.UserInfoPath) {
		body = u.userInfoBody
		if body == "" {
			body = `{"id":"user-1","name":"Qoder User"}`
		}
	}
	statusCode := http.StatusOK
	if req.Method == http.MethodGet && strings.Contains(req.URL.Path, qoder.UserInfoPath) && u.userInfoStatusCode != 0 {
		statusCode = u.userInfoStatusCode
	}
	return &http.Response{
		StatusCode: statusCode,
		Body:       io.NopCloser(strings.NewReader(body)),
	}, nil
}

func TestAccountTestService_QoderCosyUsesNativeTestPath(t *testing.T) {
	ctx, recorder := newQoderAccountTestContext()
	account := &accountcore.Record{
		ID:          7,
		Name:        "qoder",
		Platform:    capability.PlatformQoder,
		Type:        capability.AccountTypeCosy,
		Concurrency: 1,
		Credentials: map[string]any{
			"security_oauth_token": "token",
			"machine_id":           "machine",
		},
	}
	client := &qoderAccountTestClientStub{
		body: "data: {\"body\":\"{\\\"choices\\\":[{\\\"delta\\\":{\\\"reasoning_content\\\":\\\"hidden thought\\\"}}]}\"}\n\n" +
			"data: {\"body\":\"{\\\"choices\\\":[{\\\"delta\\\":{\\\"content\\\":\\\"OK\\\"}}]}\"}\n\n" +
			"data: {\"body\":\"[DONE]\"}\n\n",
	}
	svc := &accountprovider.QoderAccountTest{
		Sessions: &qoderAccountTestSessionProviderStub{
			session: &qoder.SessionContext{Identity: &qoder.AuthIdentity{SecurityOauthToken: "token"}},
		},
		Client:   client,
		UserInfo: &qoderAccountTestOAuthClientStub{},
	}

	err := executeQoderTest(t, ctx, svc, account, "auto", "hi", "")

	require.NoError(t, err)
	body := recorder.Body.String()
	require.Contains(t, body, `"type":"content"`)
	require.Contains(t, body, `"text":"OK"`)
	require.NotContains(t, body, "hidden thought")
	require.Contains(t, body, `"type":"test_complete"`)
	require.NotContains(t, body, "Unsupported account type: cosy")
	require.NotNil(t, client.request)
}

func TestAccountTestService_QoderPATRebuildsSessionForConnectionTest(t *testing.T) {
	ctx, _ := newQoderAccountTestContext()
	account := &accountcore.Record{
		ID:       12,
		Name:     "qoder-pat",
		Platform: capability.PlatformQoder,
		Type:     capability.AccountTypeCosy,
		Credentials: map[string]any{
			"pat": "pat-123",
		},
	}
	provider := &qoderAccountTestSessionProviderStub{
		session: &qoder.SessionContext{Identity: &qoder.AuthIdentity{SecurityOauthToken: "token"}},
	}
	svc := &accountprovider.QoderAccountTest{
		Sessions: provider,
		Client: &qoderAccountTestClientStub{
			body: "data: {\"body\":\"[DONE]\"}\n\n",
		},
		UserInfo: &qoderAccountTestOAuthClientStub{},
	}

	err := executeQoderTest(t, ctx, svc, account, "", "", "")

	require.NoError(t, err)
	require.Equal(t, []int64{account.ID}, provider.invalidated)
}

func TestAccountTestService_QoderProbesUserInfoBeforeStream(t *testing.T) {
	ctx, recorder := newQoderAccountTestContext()
	account := &accountcore.Record{
		ID:       11,
		Name:     "qoder",
		Platform: capability.PlatformQoder,
		Type:     capability.AccountTypeCosy,
	}
	client := &qoderAccountTestClientStub{
		body: "data: {\"body\":\"[DONE]\"}\n\n",
	}
	oauthClient := &qoderAccountTestOAuthClientStub{}
	svc := &accountprovider.QoderAccountTest{
		Sessions: &qoderAccountTestSessionProviderStub{
			session: &qoder.SessionContext{Identity: &qoder.AuthIdentity{SecurityOauthToken: "session-token"}},
		},
		Client:   client,
		UserInfo: oauthClient,
	}

	err := executeQoderTest(t, ctx, svc, account, "", "", "")

	require.NoError(t, err)
	require.Equal(t, "session-token", oauthClient.token)
	require.Contains(t, recorder.Body.String(), `"type":"test_complete"`)
	require.Equal(t, "auto", client.headers["x-model-key"])
}

func TestAccountTestService_QoderWrappedErrorIsVisible(t *testing.T) {
	ctx, recorder := newQoderAccountTestContext()
	account := &accountcore.Record{
		ID:       8,
		Name:     "qoder",
		Platform: capability.PlatformQoder,
		Type:     capability.AccountTypeCosy,
	}
	client := &qoderAccountTestClientStub{
		body: "data: {\"body\":\"{\\\"code\\\":\\\"101\\\",\\\"message\\\":\\\"Signature invalid\\\"}\",\"statusCodeValue\":403,\"statusCode\":\"FORBIDDEN\"}\n\n",
	}
	svc := &accountprovider.QoderAccountTest{
		Sessions: &qoderAccountTestSessionProviderStub{
			session: &qoder.SessionContext{Identity: &qoder.AuthIdentity{SecurityOauthToken: "token"}},
		},
		Client:   client,
		UserInfo: &qoderAccountTestOAuthClientStub{},
	}

	err := executeQoderTest(t, ctx, svc, account, "", "", "")

	require.Error(t, err)
	body := recorder.Body.String()
	require.Contains(t, body, `"type":"error"`)
	require.Contains(t, body, "Qoder upstream error 101: Signature invalid")
	require.NotContains(t, body, "Unsupported account type: cosy")
}

func TestAccountTestService_QoderReasoningOnlyDoesNotEmitContent(t *testing.T) {
	ctx, recorder := newQoderAccountTestContext()
	account := &accountcore.Record{
		ID:       9,
		Name:     "qoder",
		Platform: capability.PlatformQoder,
		Type:     capability.AccountTypeCosy,
	}
	client := &qoderAccountTestClientStub{
		body: "data: {\"body\":\"{\\\"choices\\\":[{\\\"delta\\\":{\\\"reasoning_content\\\":\\\"hidden thought\\\"}}]}\"}\n\n" +
			"data: {\"body\":\"[DONE]\"}\n\n",
	}
	svc := &accountprovider.QoderAccountTest{
		Sessions: &qoderAccountTestSessionProviderStub{
			session: &qoder.SessionContext{Identity: &qoder.AuthIdentity{SecurityOauthToken: "token"}},
		},
		Client:   client,
		UserInfo: &qoderAccountTestOAuthClientStub{},
	}

	err := executeQoderTest(t, ctx, svc, account, "", "", "")

	require.NoError(t, err)
	body := recorder.Body.String()
	require.NotContains(t, body, `"type":"content"`)
	require.NotContains(t, body, "hidden thought")
	require.Contains(t, body, `"type":"test_complete"`)
}

func TestAccountTestService_QoderUsesHTTPUpstreamForProxyAndTLS(t *testing.T) {
	ctx, recorder := newQoderAccountTestContext()
	proxyID := int64(12)
	account := &accountcore.Record{
		ID:          10,
		Name:        "qoder",
		Platform:    capability.PlatformQoder,
		Type:        capability.AccountTypeCosy,
		Concurrency: 2,
		ProxyID:     &proxyID,
		Proxy: &egress.Proxy{
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
	svc := &accountprovider.QoderAccountTest{
		Sessions: &qoderAccountTestSessionProviderStub{
			session: &qoder.SessionContext{Identity: &qoder.AuthIdentity{SecurityOauthToken: "token"}},
		},
		Client:    client,
		UserInfo:  &qoderAccountTestOAuthClientStub{},
		Transport: upstream,
		Profiles:  &providercore.TLSProfiles{},
	}

	err := executeQoderTest(t, ctx, svc, account, "", "", "")

	require.NoError(t, err)
	require.Contains(t, recorder.Body.String(), `"text":"OK"`)
	require.Equal(t, "http://proxy.example.com:8080", upstream.proxyURL)
	require.Equal(t, int64(10), upstream.accountID)
	require.True(t, upstream.profileSet)
	require.NotNil(t, client.request)
}

func TestAccountTestService_QoderDefaultUserInfoProbeUsesHTTPUpstream(t *testing.T) {
	ctx, recorder := newQoderAccountTestContext()
	proxyID := int64(12)
	account := &accountcore.Record{
		ID:          12,
		Name:        "qoder",
		Platform:    capability.PlatformQoder,
		Type:        capability.AccountTypeCosy,
		Concurrency: 3,
		ProxyID:     &proxyID,
		Proxy: &egress.Proxy{
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
		userInfoBody: `{"id":"user-12","name":"Qoder User"}`,
		body: "data: {\"body\":\"{\\\"choices\\\":[{\\\"delta\\\":{\\\"content\\\":\\\"OK\\\"}}]}\"}\n\n" +
			"data: {\"body\":\"[DONE]\"}\n\n",
	}
	svc := &accountprovider.QoderAccountTest{
		Sessions: &qoderAccountTestSessionProviderStub{
			session: &qoder.SessionContext{Identity: &qoder.AuthIdentity{SecurityOauthToken: "token"}},
		},
		Client:    client,
		Transport: upstream,
		Profiles:  &providercore.TLSProfiles{},
	}

	err := executeQoderTest(t, ctx, svc, account, "", "", "")

	require.NoError(t, err)
	require.Contains(t, recorder.Body.String(), `"text":"OK"`)
	require.Len(t, upstream.requests, 2)
	require.Equal(t, http.MethodGet, upstream.requests[0].Method)
	require.Contains(t, upstream.requests[0].URL.Path, qoder.UserInfoPath)
	require.Equal(t, "http://proxy.example.com:8080", upstream.proxyURL)
	require.Equal(t, int64(12), upstream.accountID)
	require.Equal(t, 3, upstream.accountConcurrency)
	require.True(t, upstream.profileSet)
	// 确认探测路径使用注入的 Qoder session provider，而不是绕过代理/TLS 的默认客户端。
	sessionProvider, ok := svc.Sessions.(*qoderAccountTestSessionProviderStub)
	require.True(t, ok)
	require.Equal(t, "user-12", sessionProvider.session.Identity.UID)
}

func TestAccountTestService_QoderUserInfoProbeRedactsSensitiveErrorBody(t *testing.T) {
	ctx, recorder := newQoderAccountTestContext()
	account := &accountcore.Record{
		ID:          13,
		Name:        "qoder",
		Platform:    capability.PlatformQoder,
		Type:        capability.AccountTypeCosy,
		Concurrency: 1,
		Credentials: map[string]any{
			"security_oauth_token": "token",
		},
	}
	upstream := &qoderHTTPUpstreamRecorder{
		userInfoStatusCode: http.StatusInternalServerError,
		userInfoBody:       `{"message":"failed","securityOauthToken":"sec-secret","refresh_token":"rt-secret","uid":"uid-secret","cookie":"sid=secret"}`,
	}
	svc := &accountprovider.QoderAccountTest{
		Sessions: &qoderAccountTestSessionProviderStub{
			session: &qoder.SessionContext{Identity: &qoder.AuthIdentity{SecurityOauthToken: "token"}},
		},
		Client:    &qoderAccountTestClientStub{},
		Transport: upstream,
		UserInfo:  nil,
	}

	err := executeQoderTest(t, ctx, svc, account, "", "", "")

	require.Error(t, err)
	body := recorder.Body.String()
	require.Contains(t, body, "qoder userinfo probe failed")
	require.NotContains(t, body, "sec-secret")
	require.NotContains(t, body, "rt-secret")
	require.NotContains(t, body, "uid-secret")
	require.NotContains(t, body, "sid=secret")
	require.Contains(t, body, "***")
}

// 夹具只绑定真实测试用例、平台目标和 HTTP 输出器，不复制执行分支。
type qoderTestOutputFixture struct {
	context.Context
	recorder *httptest.ResponseRecorder
}

func newQoderAccountTestContext() (*qoderTestOutputFixture, *httptest.ResponseRecorder) {
	recorder := httptest.NewRecorder()
	return &qoderTestOutputFixture{Context: context.Background(), recorder: recorder}, recorder
}

type qoderTargetFixture struct{ target accountcore.TestTarget }

func (l qoderTargetFixture) LoadTestTarget(context.Context, accountcore.TestRequest) (accountcore.TestTarget, error) {
	return l.target, nil
}
func executeQoderTest(t *testing.T, output *qoderTestOutputFixture, executor *accountprovider.QoderAccountTest, value *accountcore.Record, model, prompt, mode string) error {
	t.Helper()
	executor.RewriteModel = openaiprotocol.ReplaceModelInBody
	core := accountcore.NewTestService(qoderTargetFixture{target: executor.Target(value)}, accountcore.TestOptions{
		Now:        time.Now,
		Error:      func(message string) { log.Printf("Account test error: %s", message) },
		WriteError: func(err error) { log.Printf("failed to write SSE event: %v", err) },
	})
	return core.Test(output.Context, accountcore.TestRequest{AccountID: value.ID, Model: model, Prompt: prompt, Mode: mode}, NewTestEventSink(output.recorder))
}
