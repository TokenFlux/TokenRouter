package httpapi

import (
	"context"
	"errors"
	"io"
	"maps"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"

	"github.com/TokenFlux/TokenRouter/internal/billing"
	"github.com/TokenFlux/TokenRouter/internal/egress"
	"github.com/TokenFlux/TokenRouter/internal/gateway/forward"
	"github.com/TokenFlux/TokenRouter/internal/infra/httpclient/tlsfingerprint"
	"github.com/TokenFlux/TokenRouter/internal/pkg/apperror"
	providercore "github.com/TokenFlux/TokenRouter/internal/provider"
	provideradapter "github.com/TokenFlux/TokenRouter/internal/provider/provider"
	"github.com/TokenFlux/TokenRouter/internal/routing/capability"
	xai "github.com/TokenFlux/TokenRouter/internal/upstream/grok"
)

// Grok administrator quota responses combine controlled upstream observations
// and redact credentials, while authorization endpoints intentionally return
// exchanged token information. grok_oauth.go is exercised with session-owning
// authorization fixtures, mode-binding reconciliation captures and a shared
// follow-up probe queue for SSO imports.
//
// Request and Extra captures are synchronized independently; a request count
// alone is not a persistence completion barrier. The probe fixture publishes
// phase signals around a cancellable test hold and records maximum overlap
// under its mutex. Import task cleanup joins dispatch before stopping the
// queue, keeping created outcomes separate from a later probe failure.

// grokQuotaHandlerProviderRepo supplies one stable provider and captures
// borrowed Extra patches under mu.
type grokQuotaHandlerProviderRepo struct {
	providercore.GrokRateLimitWriter                          // unused embedded quota health operations
	provider                         *providercore.Record     // borrowed immutable selected provider
	updates                          map[int64]map[string]any // borrowed captured Extra patch map
	mu                               sync.Mutex               // fixture observation synchronization mutex
}

// grokQuotaHandlerUpstream captures request pointers and body bytes together
// under mu before fixed responses.
type grokQuotaHandlerUpstream struct {
	mu sync.Mutex // fixture observation synchronization mutex
	// requests and bodies are appended together under mu so each request keeps
	// its matching consumed body bytes. Readers copy the slices under that lock;
	// request pointers and inner byte slices remain shared.
	requests []*http.Request // captured borrowed HTTP request pointers
	bodies   [][]byte        // captured consumed request body bytes
}

// grokOAuthReconcilerStub captures serial reconciliation input and supplies
// controlled planned or applied outcomes.
type grokOAuthReconcilerStub struct {
	input  providercore.GrokOAuthReconcileInput   // last serial reconciliation selection
	calls  int                                    // serial logical reconciliation invocation count
	result *providercore.GrokOAuthReconcileResult // borrowed configured reconciliation outcome
	err    error                                  // configured reconciliation callback failure
}

// grokOAuthHandlerClient supplies fixed SSO/password token results and rejects
// unexpected browser exchange.
type grokOAuthHandlerClient struct{}

// grokImportOAuthClientStub supplies fixed token conversion for the controlled
// SSO import batch.
type grokImportOAuthClientStub struct{}

// grokImportProbeStub exposes synchronized call and deadline observations plus
// test-controlled probe phases. Calls and active-state counters use mu;
// failures are configured before dispatch, while started/done channel capacity
// governs phase publication.
type grokImportProbeStub struct {
	mu sync.Mutex // fixture observation synchronization mutex
	// calls, active, maxActive and deadlineSeen are observed under mu. snapshot
	// copies its selected observations while probe calls can continue updating the
	// shared counters.
	calls        map[int64]int   // synchronized per-provider invocation counts
	failures     map[int64]error // configured immutable per-ID probe failures
	active       int             // current logical quota probe calls
	maxActive    int             // maximum overlapping quota probe calls
	deadlineSeen bool            // observed probe context deadline flag
	// block holds a probe after start publication when configured. A closed
	// channel or context cancellation releases the wait before completion is
	// recorded.
	block   <-chan struct{} // optional test-owned probe hold channel
	started chan int64      // probe-start identifier publication channel
	done    chan int64      // probe-completion identifier publication channel
}

// TestGrokOAuthHandlerQueryQuotaProbesUpstream queries OAuth quota using
// controlled Responses, billing and model endpoints. Four captured requests
// carry the expected authorization, output is redacted and fixture Extra
// persistence is observed under its mutex.
func TestGrokOAuthHandlerQueryQuotaProbesUpstream(t *testing.T) {
	repo := &grokQuotaHandlerProviderRepo{provider: &providercore.Record{
		ID:          42,
		Platform:    capability.PlatformGrok,
		Type:        capability.ProviderTypeOAuth,
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
	tokens := &providercore.GrokTokenSource{Repository: repo, Policy: providercore.GrokProviderRefreshPolicy()}
	quotaService := provideradapter.NewGrokQuota(repo, tokens, &provideradapter.GrokQuotaTransport{Do: upstream.Do, MapStatus: forward.MapStatus}, nil)
	// Quota queries can admit shared probe work. Stop the fixture service after
	// assertions so its registered work completes before the borrowed store is released.
	t.Cleanup(func() { require.NoError(t, quotaService.StopContext(context.Background())) })
	handler := NewGrokOAuthHandler(nil, nil, quotaService, GrokOAuthHTTPOptions{})

	router := gin.New()
	router.GET("/api/v1/admin/grok/providers/:id/quota", handler.QueryQuota)
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/v1/admin/grok/providers/42/quota", nil)
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
	// Read captured Extra under the same mutex used by its writer. The separate
	// upstream request counter does not itself establish a completed fixture write.
	repo.mu.Lock()
	require.NotNil(t, repo.updates[42])
	repo.mu.Unlock()
}

// TestGrokOAuthHandlerResetQuotaReturnsUnsupported requests the Grok OAuth
// reset operation with a configured quota service. HTTP returns the unsupported
// domain reason with status 501 through the common error envelope. The
// fixture supplies no credentials, so this scenario checks unsupported-operation
// mapping.
func TestGrokOAuthHandlerResetQuotaReturnsUnsupported(t *testing.T) {
	repo := &grokQuotaHandlerProviderRepo{provider: &providercore.Record{
		ID:       43,
		Platform: capability.PlatformGrok,
		Type:     capability.ProviderTypeOAuth,
	}}
	quotaService := provideradapter.NewGrokQuota(repo, nil, nil, nil)
	// Quota queries can admit shared probe work. Stop the fixture service after
	// assertions so its registered work completes before the borrowed store is released.
	t.Cleanup(func() { require.NoError(t, quotaService.StopContext(context.Background())) })
	handler := NewGrokOAuthHandler(nil, nil, quotaService, GrokOAuthHTTPOptions{})

	router := gin.New()
	router.POST("/api/v1/admin/grok/providers/:id/reset-quota", handler.ResetQuota)
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/v1/admin/grok/providers/43/reset-quota", nil)
	router.ServeHTTP(rec, req)

	require.Equal(t, http.StatusNotImplemented, rec.Code)
	require.Contains(t, rec.Body.String(), `"reason":"GROK_QUOTA_RESET_UNSUPPORTED"`)
	require.NotContains(t, rec.Body.String(), "access-token")
}

// TestGrokOAuthHandlerRuntimeSanityDoesNotExposeSecrets configures invalid
// secret-bearing runtime values before diagnostics. The response identifies
// invalid configuration and public scope while omitting the raw query
// credential and configured client identifier.
func TestGrokOAuthHandlerRuntimeSanityDoesNotExposeSecrets(t *testing.T) {
	xai.ConfigureOverrides(xai.Overrides{BaseURL: "http://127.0.0.1:8080/v1?access_token=secret", ClientID: "client-secret-like-value"})
	t.Cleanup(func() { xai.ConfigureOverrides(xai.Overrides{}) })

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

// TestGrokOAuthHandlerValidateSSOTokenReturnsTokenInfo converts supplied SSO
// input through a controlled authorization client. The HTTP response includes
// exchanged access-token information and omits the raw SSO request field.
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

// TestGrokOAuthHandlerAuthorizePasswordReturnsTokenInfoWithoutPassword enables
// password authorization and submits a configured account login. The response
// includes exchanged access-token information while the submitted password text
// stays absent.
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

// TestGrokOAuthHandlerPasswordCapabilityDefaultsToDisabled reads capabilities
// from authorization using default password enablement. The HTTP response
// reports password_auth_enabled=false through the capability route and its
// shared success envelope.
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

// TestGrokOAuthHandlerReconcileDefaultsToDryRun submits empty JSON to a
// configured reconciliation fixture. The captured input selects dry-run with
// apply=false, and returned planned reasons remain visible without raw token
// fields.
func TestGrokOAuthHandlerReconcileDefaultsToDryRun(t *testing.T) {
	reconciler := &grokOAuthReconcilerStub{result: &providercore.GrokOAuthReconcileResult{
		DryRun:      true,
		Scanned:     2,
		Actionable:  1,
		WouldBlock:  1,
		Items:       []providercore.GrokOAuthReconcileItem{{ProviderID: 42, Reason: providercore.GrokOAuthReconcileReasonMissingRefreshToken, Action: providercore.GrokOAuthReconcileActionBlock, Outcome: providercore.GrokOAuthReconcileOutcomePlanned}},
		NextAfterID: 0,
	}}
	handler := NewGrokOAuthHandler(nil, nil, nil, GrokOAuthHTTPOptions{Reconciler: reconciler})
	router := gin.New()
	router.POST("/api/v1/admin/grok/oauth/reconcile", handler.ReconcileOAuthProviders)
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

// TestGrokOAuthHandlerReconcileRequiresExplicitApply submits dry_run=false
// while omitting apply. Handler validation returns 400 before the reconciler
// records a call, protecting the complementary flag requirement for mutation.
func TestGrokOAuthHandlerReconcileRequiresExplicitApply(t *testing.T) {
	reconciler := &grokOAuthReconcilerStub{}
	handler := NewGrokOAuthHandler(nil, nil, nil, GrokOAuthHTTPOptions{Reconciler: reconciler})
	router := gin.New()
	router.POST("/api/v1/admin/grok/oauth/reconcile", handler.ReconcileOAuthProviders)
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/v1/admin/grok/oauth/reconcile", strings.NewReader(`{"dry_run":false}`))
	req.Header.Set("Content-Type", "application/json")

	router.ServeHTTP(rec, req)

	require.Equal(t, http.StatusBadRequest, rec.Code)
	require.Zero(t, reconciler.calls)
	require.NotContains(t, rec.Body.String(), "credentials")
}

// TestGrokOAuthHandlerReconcileExplicitApply submits complementary apply flags
// with cursor, limit and refresh-window seconds. The fixture receives the
// selected population values and a one-hour duration, and HTTP returns success.
func TestGrokOAuthHandlerReconcileExplicitApply(t *testing.T) {
	reconciler := &grokOAuthReconcilerStub{result: &providercore.GrokOAuthReconcileResult{DryRun: false, Refreshed: 1}}
	handler := NewGrokOAuthHandler(nil, nil, nil, GrokOAuthHTTPOptions{Reconciler: reconciler})
	router := gin.New()
	router.POST("/api/v1/admin/grok/oauth/reconcile", handler.ReconcileOAuthProviders)
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

// TestGrokSSOBatchImportKeepsCreatedProvidersWhenOneAutomaticProbeFails imports
// three SSO items and injects a failure for one follow-up quota probe. HTTP
// returns created outcomes without that probe failure text, and each created
// fixture ID receives one probe.
func TestGrokSSOBatchImportKeepsCreatedProvidersWhenOneAutomaticProbeFails(t *testing.T) {
	adminService := newGrokImportAdminService()
	oauthService := newGrokAuthorizationForTest(nil, grokImportOAuthClientStub{})
	oauthService.Start()
	defer stopGrokAuthorizationForTest(t, oauthService)
	prober := newGrokImportProbeStub(3)
	prober.failures[502] = apperror.New(502, "GROK_TEST_PROBE_FAILED", "sensitive-upstream-body")
	queue := providercore.NewGrokImportProbeScheduler(providercore.GrokImportProbeOptions{Concurrency: 3, Timeout: 25 * time.Second})
	var tasks sync.WaitGroup
	// Import dispatch can continue after its HTTP response. Join task dispatch
	// before stopping the shared queue so accepted probe work has a completed owner.
	t.Cleanup(func() { tasks.Wait(); require.NoError(t, queue.StopContext(context.Background())) })
	imports := providercore.NewGrokProviderImport(oauthService, providercore.GrokProviderImportOptions{
		Get: adminService.GetProvider, Create: adminService.CreateProvider, Update: adminService.UpdateProvider,
		NormalizeToken: xai.NormalizeSSOToken, LogError: func(string, ...any) {},
		Schedule: func(value *providercore.Record) {
			snapshot := value.RoutingSnapshot()
			queue.Schedule(prober, &snapshot)
		},
		RunTask: func(_ string, run func()) { ; tasks.Go(func() { ; run() }) },
	})
	handler := NewGrokOAuthHandler(oauthService, imports, nil, GrokOAuthHTTPOptions{})

	router := gin.New()
	router.POST("/api/v1/admin/grok/sso-to-oauth", handler.CreateProvidersFromSSO)
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(
		http.MethodPost,
		"/api/v1/admin/grok/sso-to-oauth",
		strings.NewReader(`{"sso_tokens":["sso-one","sso-two","sso-three"]}`),
	)
	request.Header.Set("Content-Type", "application/json")
	router.ServeHTTP(recorder, request)

	require.Equal(t, http.StatusOK, recorder.Code)
	require.Contains(t, recorder.Body.String(), `"created"`)
	require.NotContains(t, recorder.Body.String(), `GROK_TEST_PROBE_FAILED`)
	for range 3 {
		awaitGrokProbeSignal(t, prober.done)
	}
	calls, _, _ := prober.snapshot()
	require.Equal(t, map[int64]int{501: 1, 502: 1, 503: 1}, calls)
}

// GetByID returns the borrowed configured record for its matching identifier.
// Missing IDs return provider-not-found; the test keeps this provider stable
// while quota maintenance records separate Extra observations.
func (r *grokQuotaHandlerProviderRepo) GetByID(_ context.Context, id int64) (*providercore.Record, error) {
	if r.provider != nil && r.provider.ID == id {
		return r.provider, nil
	}
	return nil, providercore.ErrProviderNotFound
}

// UpdateExtra stores the supplied borrowed Extra patch under mu, allocating the
// capture map when first used. Tests acquire the same mutex to inspect accepted
// writes after their controlled quota request.
func (r *grokQuotaHandlerProviderRepo) UpdateExtra(_ context.Context, id int64, updates map[string]any) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.updates == nil {
		r.updates = make(map[int64]map[string]any)
	}
	r.updates[id] = updates
	return nil
}

// ReconcileGrokOAuth records one serial call and its complete input selection.
// It returns the configured result pointer and error, allowing handler tests to
// isolate mode binding from actual credential reconciliation.
func (s *grokOAuthReconcilerStub) ReconcileGrokOAuth(_ context.Context, input providercore.GrokOAuthReconcileInput) (*providercore.GrokOAuthReconcileResult, error) {
	s.calls++
	s.input = input
	return s.result, s.err
}

// Do captures a borrowed request pointer and consumed body bytes under mu
// before returning a fixed endpoint response. Request-body read errors are
// ignored and any returned bytes remain available in capture; each call returns
// a fresh consumable response body.
func (u *grokQuotaHandlerUpstream) Do(req *http.Request, _ string, _ int64, _ int) (*http.Response, error) {
	var body []byte
	if req.Body != nil {
		// Capture whatever bytes the fixture reader supplies when reading fails.
		// Fixed responses still proceed, so these tests do not model body-read transport errors.
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

// DoWithTLS delegates fixed endpoint responses to Do and ignores the TLS
// profile. Request and body capture use that method's mutex, and the returned
// fresh response body is consumed by the quota caller.
func (u *grokQuotaHandlerUpstream) DoWithTLS(
	req *http.Request,
	proxyURL string,
	providerID int64,
	providerConcurrency int,
	_ *tlsfingerprint.Profile,
) (*http.Response, error) {
	return u.Do(req, proxyURL, providerID, providerConcurrency)
}

// ExchangeCode rejects the unexpected browser-code exchange path in this SSO
// fixture.
func (c *grokOAuthHandlerClient) ExchangeCode(context.Context, string, string, string, string, string) (*xai.TokenResponse, error) {
	return nil, errors.New("unexpected exchange")
}

// RefreshToken returns the fixed access and refresh tokens used by the
// authorization fixture.
func (c *grokOAuthHandlerClient) RefreshToken(context.Context, string, string, string) (*xai.TokenResponse, error) {
	return &xai.TokenResponse{AccessToken: "access-token", RefreshToken: "refresh-token", ExpiresIn: 3600}, nil
}

// LoginWithPassword echoes the supplied email and returns fixed SSO material.
// Password and transport inputs are ignored here, so HTTP tests establish
// returned-field redaction rather than login authentication.
func (c *grokOAuthHandlerClient) LoginWithPassword(_ context.Context, email, _ string, _ string) (*providercore.GrokPasswordLoginResult, error) {
	return &providercore.GrokPasswordLoginResult{
		Email:    email,
		SSOToken: "sso-from-password",
	}, nil
}

// ConvertSSOToBuild returns the fixed Build token information for supplied SSO
// input.
func (c *grokOAuthHandlerClient) ConvertSSOToBuild(context.Context, string, string) (*xai.TokenResponse, error) {
	return &xai.TokenResponse{AccessToken: "access-token", RefreshToken: "refresh-token", ExpiresIn: 3600}, nil
}

// newGrokAuthorizationForTest constructs authorization with the production
// option adapter and controlled client. Only a supplied true first enablement
// argument allows password auth; the caller starts and later stops session
// ownership.
func newGrokAuthorizationForTest(proxies egress.ProxyRepository, client providercore.GrokAuthorizationClient, enabled ...bool) *providercore.GrokAuthorization {
	return providercore.NewGrokAuthorization(client, provideradapter.GrokAuthorizationOptions(proxies, func() bool {
		return len(enabled) > 0 && enabled[0]
	}))
}

// stopGrokAuthorizationForTest joins authorization shutdown using a background
// context and fails the test on error. Call it after mounted authorization
// requests finish so the fixture session owner can release its work.
func stopGrokAuthorizationForTest(t *testing.T, authorization *providercore.GrokAuthorization) {
	t.Helper()
	require.NoError(t, authorization.StopContext(context.Background()))
}

// ExchangeCode returns fixed token information for the unused browser import
// exchange path.
func (grokImportOAuthClientStub) ExchangeCode(context.Context, string, string, string, string, string) (*xai.TokenResponse, error) {
	return &xai.TokenResponse{AccessToken: "access-token", RefreshToken: "refresh-token", ExpiresIn: 3600}, nil
}

// RefreshToken returns fixed token information for the import client's logical
// refresh path.
func (grokImportOAuthClientStub) RefreshToken(context.Context, string, string, string) (*xai.TokenResponse, error) {
	return &xai.TokenResponse{AccessToken: "access-token", RefreshToken: "refresh-token", ExpiresIn: 3600}, nil
}

// LoginWithPassword rejects unexpected password login in the SSO-only import
// fixture.
func (grokImportOAuthClientStub) LoginWithPassword(context.Context, string, string, string) (*providercore.GrokPasswordLoginResult, error) {
	return nil, errors.New("unexpected password login")
}

// ConvertSSOToBuild returns fixed Build credentials for each supplied SSO
// import item.
func (grokImportOAuthClientStub) ConvertSSOToBuild(context.Context, string, string) (*xai.TokenResponse, error) {
	return &xai.TokenResponse{AccessToken: "access-token", RefreshToken: "refresh-token", ExpiresIn: 3600}, nil
}

// newGrokImportProbeStub initializes synchronized call/failure maps and
// test-owned buffered phase channels. The supplied capacity determines whether
// started and done publication can proceed while the test awaits signals.
func newGrokImportProbeStub(buffer int) *grokImportProbeStub {
	return &grokImportProbeStub{
		calls:    make(map[int64]int),
		failures: make(map[int64]error),
		started:  make(chan int64, buffer),
		done:     make(chan int64, buffer),
	}
}

// QueryQuota records deadline presence and overlapping logical calls under mu
// before publishing started. The test can hold calls on block to inspect
// scheduler admission; only that wait responds to context cancellation. Started
// and done sends require channel capacity or active receivers.
//
// After waiting, active count is decreased and the selected failure is read
// under the same mutex. Done is published before returning cancellation,
// configured failure or a fixed successful observation. The fixture thus
// exposes work phases and joined call counts without reproducing upstream quota
// transport or persistence.
func (s *grokImportProbeStub) QueryQuota(ctx context.Context, providerID int64) (*providercore.GrokImportProbeResult, error) {
	_, deadlineSeen := ctx.Deadline()
	s.mu.Lock()
	s.calls[providerID]++
	s.active++
	if s.active > s.maxActive {
		s.maxActive = s.active
	}
	s.deadlineSeen = s.deadlineSeen || deadlineSeen
	s.mu.Unlock()

	// Publish the active phase before the test-controlled hold. Channel capacity
	// or an awaiting receiver is required because this fixture send does not select cancellation.
	s.started <- providerID
	var ctxErr error
	if s.block != nil {
		select {
		case <-s.block:
		case <-ctx.Done():
			ctxErr = ctx.Err()
		}
	}

	s.mu.Lock()
	s.active--
	failure := s.failures[providerID]
	s.mu.Unlock()
	// Completion publication follows the decremented active count and selected
	// failure observation, so an awaited done signal can precede the method's returned result.
	s.done <- providerID
	if ctxErr != nil {
		return nil, ctxErr
	}
	if failure != nil {
		return nil, failure
	}
	return &providercore.GrokImportProbeResult{
		Model:      "grok-4.5",
		StatusCode: 200,
	}, nil
}

// snapshot copies the logical call-count map together with maximum overlap and
// deadline observation under mu. The returned integer map is independent,
// letting tests inspect one consistent fixture observation while probe calls
// may continue.
func (s *grokImportProbeStub) snapshot() (map[int64]int, int, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	calls := make(map[int64]int, len(s.calls))
	maps.Copy(calls, s.calls)
	return calls, s.maxActive, s.deadlineSeen
}

// awaitGrokProbeSignal waits up to one second for a fixture phase identifier.
// Timeout fails the test and returns the unreachable zero fallback; the caller
// chooses which started or done channel to observe.
func awaitGrokProbeSignal(t *testing.T, signals <-chan int64) int64 {
	t.Helper()
	select {
	case id := <-signals:
		return id
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for Grok import probe")
		return 0
	}
}
