//go:build unit

package httpapi

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/TokenFlux/TokenRouter/internal/billing"
	"github.com/TokenFlux/TokenRouter/internal/routing/capability"

	accountcore "github.com/TokenFlux/TokenRouter/internal/account"

	"github.com/TokenFlux/TokenRouter/internal/account/provider"
	"github.com/TokenFlux/TokenRouter/internal/gateway/forward"
	"github.com/TokenFlux/TokenRouter/internal/infra/httpclient/tlsfingerprint"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"

	xai "github.com/TokenFlux/TokenRouter/internal/upstream/grok"
)

type grokQuotaHandlerAccountRepo struct {
	accountcore.GrokRateLimitWriter
	account *accountcore.Record
	updates map[int64]map[string]any
	mu      sync.Mutex
}

func (r *grokQuotaHandlerAccountRepo) GetByID(_ context.Context, id int64) (*accountcore.Record, error) {
	if r.account != nil && r.account.ID == id {
		return r.account, nil
	}
	return nil, accountcore.ErrAccountNotFound
}

func (r *grokQuotaHandlerAccountRepo) UpdateExtra(_ context.Context, id int64, updates map[string]any) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.updates == nil {
		r.updates = make(map[int64]map[string]any)
	}
	r.updates[id] = updates
	return nil
}

type grokQuotaHandlerUpstream struct {
	mu       sync.Mutex
	requests []*http.Request
	bodies   [][]byte
}

type grokOAuthReconcilerStub struct {
	input  accountcore.GrokOAuthReconcileInput
	calls  int
	result *accountcore.GrokOAuthReconcileResult
	err    error
}

func (s *grokOAuthReconcilerStub) ReconcileGrokOAuth(_ context.Context, input accountcore.GrokOAuthReconcileInput) (*accountcore.GrokOAuthReconcileResult, error) {
	s.calls++
	s.input = input
	return s.result, s.err
}

func (u *grokQuotaHandlerUpstream) Do(req *http.Request, _ string, _ int64, _ int) (*http.Response, error) {
	var body []byte
	if req.Body != nil {
		body, _ = io.ReadAll(req.Body)
	}
	u.mu.Lock()
	u.requests = append(u.requests, req)
	u.bodies = append(u.bodies, body)
	u.mu.Unlock()
	if req.URL.Path == "/v1/responses" {
		return &http.Response{
			StatusCode: http.StatusOK,
			Header: http.Header{
				"X-Ratelimit-Limit-Requests":     []string{"10"},
				"X-Ratelimit-Remaining-Requests": []string{"8"},
			},
			Body: io.NopCloser(strings.NewReader(`{"id":"resp_probe"}`)),
		}, nil
	}
	payload := `{"config":{"billingPeriodStart":"2026-07-01T00:00:00Z","billingPeriodEnd":"2026-08-01T00:00:00Z"}}`
	if req.URL.RawQuery == "format=credits" {
		payload = `{"config":{"currentPeriod":{"type":"WEEKLY","start":"2026-07-09T03:25:00Z","end":"2026-07-16T03:25:00Z"}}}`
	}
	return &http.Response{StatusCode: http.StatusOK, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(payload))}, nil
}

func (u *grokQuotaHandlerUpstream) DoWithTLS(
	req *http.Request,
	proxyURL string,
	accountID int64,
	accountConcurrency int,
	_ *tlsfingerprint.Profile,
) (*http.Response, error) {
	return u.Do(req, proxyURL, accountID, accountConcurrency)
}

func TestGrokOAuthHandlerQueryQuotaProbesUpstream(t *testing.T) {

	repo := &grokQuotaHandlerAccountRepo{account: &accountcore.Record{
		ID:          42,
		Platform:    capability.PlatformGrok,
		Type:        capability.AccountTypeOAuth,
		Status:      billing.StatusActive,
		Schedulable: true,
		Concurrency: 1,
		Credentials: map[string]any{
			"access_token":  "access-token",
			"refresh_token": "refresh-token",
			"expires_at":    time.Now().Add(2 * time.Hour).UTC().Format(time.RFC3339),
		},
	}}
	upstream := &grokQuotaHandlerUpstream{}
	tokens := &accountcore.GrokTokenSource{Repository: repo, Policy: accountcore.GrokProviderRefreshPolicy()}
	quotaService := provider.NewGrokQuota(repo, tokens, &provider.GrokQuotaTransport{Do: upstream.Do, MapStatus: forward.MapStatus}, nil)
	t.Cleanup(func() { require.NoError(t, quotaService.StopContext(context.Background())) })
	handler := NewGrokOAuthHandler(nil, nil, quotaService, GrokOAuthHTTPOptions{})

	router := gin.New()
	router.GET("/api/v1/admin/grok/accounts/:id/quota", handler.QueryQuota)
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/v1/admin/grok/accounts/42/quota", nil)
	router.ServeHTTP(rec, req)

	require.Equal(t, http.StatusOK, rec.Code)
	require.Contains(t, rec.Body.String(), `"source":"hybrid_probe"`)
	require.Contains(t, rec.Body.String(), `"billing":`)
	require.Contains(t, rec.Body.String(), `"snapshot":`)
	require.Contains(t, rec.Body.String(), `"headers_observed":true`)
	require.NotContains(t, rec.Body.String(), "access-token")
	require.Eventually(t, func() bool {
		upstream.mu.Lock()
		defer upstream.mu.Unlock()
		return len(upstream.requests) == 4
	}, time.Second, 10*time.Millisecond)
	upstream.mu.Lock()
	requests := append([]*http.Request(nil), upstream.requests...)
	bodies := append([][]byte(nil), upstream.bodies...)
	upstream.mu.Unlock()
	require.Len(t, requests, 4)
	responsesProbeSeen := false
	modelsSyncSeen := false
	for i, upstreamReq := range requests {
		require.Equal(t, "Bearer access-token", upstreamReq.Header.Get("Authorization"))
		if upstreamReq.URL.String() == xai.DefaultCLIBaseURL+"/responses" {
			responsesProbeSeen = true
			require.Equal(t, "application/json, text/event-stream", upstreamReq.Header.Get("Accept"))
			require.Contains(t, string(bodies[i]), `"model":"grok-4.5"`)
			require.Contains(t, string(bodies[i]), `"input":"hi"`)
			require.Contains(t, string(bodies[i]), `"stream":true`)
			require.NotContains(t, string(bodies[i]), `"max_output_tokens"`)
			require.NotContains(t, string(bodies[i]), `"store"`)
		}
		if upstreamReq.URL.String() == xai.DefaultCLIBaseURL+"/models" {
			modelsSyncSeen = true
		}
	}
	require.True(t, responsesProbeSeen)
	require.True(t, modelsSyncSeen)
	// 后台同步与断言共享锁，等待请求数不等于已经完成持久化。
	repo.mu.Lock()
	require.NotNil(t, repo.updates[42])
	repo.mu.Unlock()
}

func TestGrokOAuthHandlerResetQuotaReturnsUnsupported(t *testing.T) {

	repo := &grokQuotaHandlerAccountRepo{account: &accountcore.Record{
		ID:       43,
		Platform: capability.PlatformGrok,
		Type:     capability.AccountTypeOAuth,
	}}
	quotaService := provider.NewGrokQuota(repo, nil, nil, nil)
	t.Cleanup(func() { require.NoError(t, quotaService.StopContext(context.Background())) })
	handler := NewGrokOAuthHandler(nil, nil, quotaService, GrokOAuthHTTPOptions{})

	router := gin.New()
	router.POST("/api/v1/admin/grok/accounts/:id/reset-quota", handler.ResetQuota)
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/v1/admin/grok/accounts/43/reset-quota", nil)
	router.ServeHTTP(rec, req)

	require.Equal(t, http.StatusNotImplemented, rec.Code)
	require.Contains(t, rec.Body.String(), `"reason":"GROK_QUOTA_RESET_UNSUPPORTED"`)
	require.NotContains(t, rec.Body.String(), "access-token")
}

func TestGrokOAuthHandlerRuntimeSanityDoesNotExposeSecrets(t *testing.T) {

	t.Setenv(xai.EnvBaseURL, "http://127.0.0.1:8080/v1?access_token=secret")
	t.Setenv(xai.EnvClientID, "client-secret-like-value")

	handler := NewGrokOAuthHandler(nil, nil, nil, GrokOAuthHTTPOptions{RuntimeSanity: func() any { return xai.RuntimeSanity() }})
	router := gin.New()
	router.GET("/api/v1/admin/grok/runtime-sanity", handler.RuntimeSanity)
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/v1/admin/grok/runtime-sanity", nil)
	router.ServeHTTP(rec, req)

	require.Equal(t, http.StatusOK, rec.Code)
	require.Contains(t, rec.Body.String(), `"public_gateway_scope":"responses_only"`)
	require.Contains(t, rec.Body.String(), `"valid":false`)
	require.NotContains(t, rec.Body.String(), "access_token")
	require.NotContains(t, rec.Body.String(), "secret")
	require.NotContains(t, rec.Body.String(), "client-secret-like-value")
}

type grokOAuthHandlerClient struct{}

func (c *grokOAuthHandlerClient) ExchangeCode(context.Context, string, string, string, string, string) (*xai.TokenResponse, error) {
	return nil, errors.New("unexpected exchange")
}

func (c *grokOAuthHandlerClient) RefreshToken(context.Context, string, string, string) (*xai.TokenResponse, error) {
	return &xai.TokenResponse{AccessToken: "access-token", RefreshToken: "refresh-token", ExpiresIn: 3600}, nil
}

func (c *grokOAuthHandlerClient) LoginWithPassword(_ context.Context, email, _ string, _ string) (*accountcore.GrokPasswordLoginResult, error) {
	return &accountcore.GrokPasswordLoginResult{
		Email:    email,
		SSOToken: "sso-from-password",
	}, nil
}

func (c *grokOAuthHandlerClient) ConvertSSOToBuild(context.Context, string, string) (*xai.TokenResponse, error) {
	return &xai.TokenResponse{AccessToken: "access-token", RefreshToken: "refresh-token", ExpiresIn: 3600}, nil
}

func TestGrokOAuthHandlerValidateSSOTokenReturnsTokenInfo(t *testing.T) {

	oauthClient := &grokOAuthHandlerClient{}
	oauthService := newGrokAuthorizationForTest(nil, oauthClient)
	oauthService.Start()
	defer stopGrokAuthorizationForTest(t, oauthService)
	handler := NewGrokOAuthHandler(oauthService, nil, nil, GrokOAuthHTTPOptions{})

	router := gin.New()
	router.POST("/api/v1/admin/grok/oauth/sso-token", handler.ValidateSSOToken)
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/v1/admin/grok/oauth/sso-token", strings.NewReader(`{"sso_token":"sso-token"}`))
	req.Header.Set("Content-Type", "application/json")
	router.ServeHTTP(rec, req)

	require.Equal(t, http.StatusOK, rec.Code)
	require.Contains(t, rec.Body.String(), `"access_token":"access-token"`)
	require.NotContains(t, rec.Body.String(), `"sso_token"`)
}

func TestGrokOAuthHandlerAuthorizePasswordReturnsTokenInfoWithoutPassword(t *testing.T) {

	oauthClient := &grokOAuthHandlerClient{}
	oauthService := newGrokAuthorizationForTest(nil, oauthClient, true)
	oauthService.Start()
	defer stopGrokAuthorizationForTest(t, oauthService)
	handler := NewGrokOAuthHandler(oauthService, nil, nil, GrokOAuthHTTPOptions{})

	router := gin.New()
	router.POST("/api/v1/admin/grok/oauth/password", handler.AuthorizePassword)
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/v1/admin/grok/oauth/password", strings.NewReader(`{"email":"user@example.com","password":"super-secret"}`))
	req.Header.Set("Content-Type", "application/json")
	router.ServeHTTP(rec, req)

	require.Equal(t, http.StatusOK, rec.Code)
	require.Contains(t, rec.Body.String(), `"access_token":"access-token"`)
	require.NotContains(t, rec.Body.String(), "super-secret")
}

func TestGrokOAuthHandlerPasswordCapabilityDefaultsToDisabled(t *testing.T) {

	oauthService := newGrokAuthorizationForTest(nil, &grokOAuthHandlerClient{})
	oauthService.Start()
	defer stopGrokAuthorizationForTest(t, oauthService)
	handler := NewGrokOAuthHandler(oauthService, nil, nil, GrokOAuthHTTPOptions{})

	router := gin.New()
	router.GET("/api/v1/admin/grok/oauth/capabilities", handler.GetCapabilities)
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/v1/admin/grok/oauth/capabilities", nil))

	require.Equal(t, http.StatusOK, rec.Code)
	require.Contains(t, rec.Body.String(), `"password_auth_enabled":false`)
}

func TestGrokOAuthHandlerReconcileDefaultsToDryRun(t *testing.T) {

	reconciler := &grokOAuthReconcilerStub{result: &accountcore.GrokOAuthReconcileResult{
		DryRun:      true,
		Scanned:     2,
		Actionable:  1,
		WouldBlock:  1,
		Items:       []accountcore.GrokOAuthReconcileItem{{AccountID: 42, Reason: accountcore.GrokOAuthReconcileReasonMissingRefreshToken, Action: accountcore.GrokOAuthReconcileActionBlock, Outcome: accountcore.GrokOAuthReconcileOutcomePlanned}},
		NextAfterID: 0,
	}}
	handler := NewGrokOAuthHandler(nil, nil, nil, GrokOAuthHTTPOptions{Reconciler: reconciler})
	router := gin.New()
	router.POST("/api/v1/admin/grok/oauth/reconcile", handler.ReconcileOAuthAccounts)
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/v1/admin/grok/oauth/reconcile", strings.NewReader(`{}`))
	req.Header.Set("Content-Type", "application/json")

	router.ServeHTTP(rec, req)

	require.Equal(t, http.StatusOK, rec.Code)
	require.Equal(t, 1, reconciler.calls)
	require.True(t, reconciler.input.DryRun)
	require.False(t, reconciler.input.Apply)
	require.Contains(t, rec.Body.String(), `"reason":"missing_refresh_token"`)
	require.NotContains(t, rec.Body.String(), `"refresh_token":`)
	require.NotContains(t, rec.Body.String(), `"access_token":`)
}

func TestGrokOAuthHandlerReconcileRequiresExplicitApply(t *testing.T) {

	reconciler := &grokOAuthReconcilerStub{}
	handler := NewGrokOAuthHandler(nil, nil, nil, GrokOAuthHTTPOptions{Reconciler: reconciler})
	router := gin.New()
	router.POST("/api/v1/admin/grok/oauth/reconcile", handler.ReconcileOAuthAccounts)
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/v1/admin/grok/oauth/reconcile", strings.NewReader(`{"dry_run":false}`))
	req.Header.Set("Content-Type", "application/json")

	router.ServeHTTP(rec, req)

	require.Equal(t, http.StatusBadRequest, rec.Code)
	require.Zero(t, reconciler.calls)
	require.NotContains(t, rec.Body.String(), "credentials")
}

func TestGrokOAuthHandlerReconcileExplicitApply(t *testing.T) {

	reconciler := &grokOAuthReconcilerStub{result: &accountcore.GrokOAuthReconcileResult{DryRun: false, Refreshed: 1}}
	handler := NewGrokOAuthHandler(nil, nil, nil, GrokOAuthHTTPOptions{Reconciler: reconciler})
	router := gin.New()
	router.POST("/api/v1/admin/grok/oauth/reconcile", handler.ReconcileOAuthAccounts)
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/v1/admin/grok/oauth/reconcile", strings.NewReader(`{"apply":true,"dry_run":false,"after_id":10,"limit":25,"refresh_window_seconds":3600}`))
	req.Header.Set("Content-Type", "application/json")

	router.ServeHTTP(rec, req)

	require.Equal(t, http.StatusOK, rec.Code)
	require.Equal(t, 1, reconciler.calls)
	require.True(t, reconciler.input.Apply)
	require.False(t, reconciler.input.DryRun)
	require.Equal(t, int64(10), reconciler.input.AfterID)
	require.Equal(t, 25, reconciler.input.Limit)
	require.Equal(t, time.Hour, reconciler.input.RefreshWindow)
}
