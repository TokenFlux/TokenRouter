//go:build unit

package provider

import (
	"bytes"
	"context"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	accountcore "github.com/TokenFlux/TokenRouter/internal/account"
	"github.com/TokenFlux/TokenRouter/internal/egress"
	"github.com/TokenFlux/TokenRouter/internal/pkg/apperror"
	"github.com/TokenFlux/TokenRouter/internal/routing/capability"

	"github.com/TokenFlux/TokenRouter/internal/gateway/forward"
	s15httpx "github.com/TokenFlux/TokenRouter/internal/server/httpx"

	xai "github.com/TokenFlux/TokenRouter/internal/upstream/grok"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

type grokQuotaAccountRepo struct {
	*grokQuotaReadStore
	updates               map[int64]map[string]any
	updateCalls           int
	rateLimitedCalls      int
	lastRateLimitedID     int64
	lastRateLimitResetAt  time.Time
	tempUnschedCalls      int
	lastTempUnschedID     int64
	lastTempUnschedUntil  time.Time
	lastTempUnschedReason string
	recoveryClearCalls    int
	recoveryObservedAt    time.Time
	recoveryObservedReset time.Time
	recoveryClearResult   bool
}

func (r *grokQuotaAccountRepo) UpdateExtra(_ context.Context, id int64, updates map[string]any) error {
	r.updateCalls++
	if r.updates == nil {
		r.updates = make(map[int64]map[string]any)
	}
	r.updates[id] = updates
	if r.grokQuotaReadStore != nil {
		account := r.accountsByID[id]
		if account == nil {
			return nil
		}
		if account.Extra == nil {
			account.Extra = make(map[string]any)
		}
		for key, value := range updates {
			account.Extra[key] = value
		}
	}
	return nil
}

func (r *grokQuotaAccountRepo) SetRateLimited(_ context.Context, id int64, resetAt time.Time) error {
	r.rateLimitedCalls++
	r.lastRateLimitedID = id
	r.lastRateLimitResetAt = resetAt
	return nil
}

func (r *grokQuotaAccountRepo) SetRateLimitedIfLater(ctx context.Context, id int64, resetAt time.Time) error {
	return r.SetRateLimited(ctx, id, resetAt)
}

func (r *grokQuotaAccountRepo) ClearRateLimitIfObserved(_ context.Context, _ int64, observedLimitedAt, observedResetAt time.Time) (bool, error) {
	r.recoveryClearCalls++
	r.recoveryObservedAt = observedLimitedAt
	r.recoveryObservedReset = observedResetAt
	return r.recoveryClearResult, nil
}

func (r *grokQuotaAccountRepo) SetTempUnschedulable(_ context.Context, id int64, until time.Time, reason string) error {
	r.tempUnschedCalls++
	r.lastTempUnschedID = id
	r.lastTempUnschedUntil = until
	r.lastTempUnschedReason = reason
	return nil
}

func TestSyncGrokObservedModelsRejectsOAuthCustomURLOutsideOperatorPolicy(t *testing.T) {
	account := &accountcore.Record{
		ID:       901,
		Platform: capability.PlatformGrok,
		Type:     capability.AccountTypeOAuth,
		Credentials: map[string]any{
			"access_token": "secret-token",
			"base_url":     "https://blocked.example.test/v1",
		},
	}
	repo := &grokQuotaAccountRepo{grokQuotaReadStore: &grokQuotaReadStore{
		accountsByID: map[int64]*accountcore.Record{account.ID: account},
	}}
	upstream := &grokQuotaHTTPRecorder{}
	policy := egress.OperatorURLPolicy{Enabled: true, UpstreamHosts: []string{"allowed.example.test"}}
	requests := &GrokQuotaTransport{Do: upstream.Do, OperatorValidator: policy.Validate}

	err := accountcore.SyncGrokObservedModels(context.Background(), accountcore.GrokModelsOptions{Fetch: requests.FetchModels, UpdateExtra: repo.UpdateExtra}, account)
	require.ErrorContains(t, err, "base URL rejected by URL security policy")
	require.Nil(t, upstream.lastReq)
}

func TestSyncGrokObservedModelsUsesCLIIdentityAndAccountHeaders(t *testing.T) {
	account := &accountcore.Record{
		ID:       902,
		Platform: capability.PlatformGrok,
		Type:     capability.AccountTypeOAuth,
		Credentials: map[string]any{
			"access_token": "secret-token",
			"sub":          "user-902",
			"email":        "user902@example.test",
		},
	}
	repo := &grokQuotaAccountRepo{grokQuotaReadStore: &grokQuotaReadStore{
		accountsByID: map[int64]*accountcore.Record{account.ID: account},
	}}
	upstream := &grokQuotaHTTPRecorder{resp: &http.Response{
		StatusCode: http.StatusOK,
		Body:       io.NopCloser(strings.NewReader(`{"data":[{"id":"grok-4.5"}]}`)),
	}}
	requests := &GrokQuotaTransport{Do: upstream.Do}

	require.NoError(t, accountcore.SyncGrokObservedModels(context.Background(), accountcore.GrokModelsOptions{Fetch: requests.FetchModels, UpdateExtra: repo.UpdateExtra}, account))
	require.Equal(t, xai.DefaultCLIBaseURL+"/models", upstream.lastReq.URL.String())
	require.Equal(t, xai.CLIClientVersion, upstream.lastReq.Header.Get("x-grok-client-version"))
	require.Equal(t, xai.CLIClientIdentifier, upstream.lastReq.Header.Get("x-grok-client-identifier"))
	require.Equal(t, xai.CLIUserAgent(xai.CLIClientVersion), upstream.lastReq.Header.Get("User-Agent"))
	require.Equal(t, "interactive", upstream.lastReq.Header.Get("X-Grok-Client-Mode"))
	require.Equal(t, "user-902", upstream.lastReq.Header.Get("X-UserID"))
	require.Equal(t, "user902@example.test", upstream.lastReq.Header.Get("X-Email"))
	require.Contains(t, repo.updates[account.ID], accountcore.GrokObservedModelsExtraKey)
}

type grokQuotaProxyRepo struct {
	proxies map[int64]*egress.Proxy
	calls   int
}

type grokQuotaUsageLogRepo struct {
	stats      *accountcore.WindowStats
	err        error
	calls      int
	startTimes []time.Time
}

func (r *grokQuotaUsageLogRepo) GetAccountWindowStats(_ context.Context, _ int64, start time.Time) (*accountcore.WindowStats, error) {
	r.calls++
	r.startTimes = append(r.startTimes, start)
	return r.stats, r.err
}

func (r *grokQuotaUsageLogRepo) GetAccountTodayStats(context.Context, int64) (*accountcore.WindowStats, error) {
	return nil, nil
}

type grokHybridUpstream struct {
	mu                   sync.Mutex
	requests             []*http.Request
	bodies               [][]byte
	weeklyUsagePercent   *float64
	monthlyLimitCents    *float64
	activeStatus         int
	activeHeaders        http.Header
	billingStarted       chan struct{}
	billingRelease       <-chan struct{}
	billingStartOnce     sync.Once
	billingStatus        int
	weeklyBillingStatus  int
	monthlyBillingStatus int
	billingHeaders       http.Header
}

func (u *grokHybridUpstream) Do(req *http.Request, _ string, _ int64, _ int) (*http.Response, error) {
	var body []byte
	if req != nil && req.Body != nil {
		body, _ = io.ReadAll(req.Body)
	}
	u.mu.Lock()
	u.requests = append(u.requests, req)
	u.bodies = append(u.bodies, body)
	u.mu.Unlock()

	if req.URL.Path == "/v1/responses" {
		status := u.activeStatus
		if status == 0 {
			status = http.StatusOK
		}
		headers := u.activeHeaders
		if headers == nil {
			headers = http.Header{
				"X-Ratelimit-Limit-Tokens":     []string{"2000000"},
				"X-Ratelimit-Remaining-Tokens": []string{"1500000"},
			}
		}
		return &http.Response{StatusCode: status, Header: headers, Body: io.NopCloser(strings.NewReader(`{"id":"resp_probe"}`))}, nil
	}
	if u.billingStarted != nil {
		u.billingStartOnce.Do(func() { close(u.billingStarted) })
	}
	if u.billingRelease != nil {
		select {
		case <-u.billingRelease:
		case <-req.Context().Done():
			return nil, req.Context().Err()
		}
	}
	billingStatus := u.billingStatus
	if req.URL.RawQuery == "format=credits" && u.weeklyBillingStatus != 0 {
		billingStatus = u.weeklyBillingStatus
	}
	if req.URL.RawQuery != "format=credits" && u.monthlyBillingStatus != 0 {
		billingStatus = u.monthlyBillingStatus
	}
	if billingStatus != 0 && billingStatus != http.StatusOK {
		return &http.Response{
			StatusCode: billingStatus,
			Header:     u.billingHeaders,
			Body:       io.NopCloser(strings.NewReader(`{"error":{"message":"billing limited"}}`)),
		}, nil
	}

	if req.URL.RawQuery == "format=credits" {
		usage := ""
		if u.weeklyUsagePercent != nil {
			usage = `,"creditUsagePercent":` + strconv.FormatFloat(*u.weeklyUsagePercent, 'f', -1, 64)
		}
		payload := `{"config":{"currentPeriod":{"type":"WEEKLY","start":"2026-07-09T03:25:00Z","end":"2026-07-16T03:25:00Z"}` + usage + `}}`
		return &http.Response{StatusCode: http.StatusOK, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(payload))}, nil
	}
	monthlyLimit := ""
	if u.monthlyLimitCents != nil {
		monthlyLimit = `,"monthlyLimit":{"val":` + strconv.FormatFloat(*u.monthlyLimitCents, 'f', -1, 64) + `}`
	}
	monthlyPayload := `{"config":{"billingPeriodStart":"2026-07-01T00:00:00Z","billingPeriodEnd":"2026-08-01T00:00:00Z"` + monthlyLimit + `}}`
	return &http.Response{
		StatusCode: http.StatusOK,
		Header:     make(http.Header),
		Body:       io.NopCloser(strings.NewReader(monthlyPayload)),
	}, nil
}

func (u *grokHybridUpstream) snapshot() ([]*http.Request, [][]byte) {
	u.mu.Lock()
	defer u.mu.Unlock()
	requests := append([]*http.Request(nil), u.requests...)
	bodies := make([][]byte, len(u.bodies))
	for i := range u.bodies {
		bodies[i] = append([]byte(nil), u.bodies[i]...)
	}
	return requests, bodies
}

func (r *grokQuotaProxyRepo) GetByID(_ context.Context, id int64) (*egress.Proxy, error) {
	r.calls++
	return r.proxies[id], nil
}

func TestIsRetryableGrokBillingStatus(t *testing.T) {
	tests := []struct {
		status int
		want   bool
	}{
		{status: http.StatusBadGateway, want: true},
		{status: http.StatusServiceUnavailable, want: true},
		{status: http.StatusGatewayTimeout, want: true},
		{status: http.StatusUnauthorized, want: false},
		{status: http.StatusForbidden, want: false},
		{status: http.StatusTooManyRequests, want: false},
	}

	for _, tt := range tests {
		t.Run(strconv.Itoa(tt.status), func(t *testing.T) {
			require.Equal(t, tt.want, xai.IsRetryableBillingStatus(tt.status))
		})
	}
}

func TestGrokQuotaServiceProbeUsageDoesNotRetryResponsesPost(t *testing.T) {
	account := healthyGrokQuotaOAuthAccount(405)
	repo := &grokQuotaAccountRepo{grokQuotaReadStore: &grokQuotaReadStore{
		accountsByID: map[int64]*accountcore.Record{account.ID: account},
	}}
	upstream := &grokQuotaSequenceUpstream{steps: []grokQuotaUpstreamStep{
		{status: http.StatusBadGateway, body: `cloudflare failure`},
		{status: http.StatusOK, body: `{"id":"unexpected_retry"}`},
	}}
	svc := newGrokQuotaFixture(repo, nil, &accountcore.GrokTokenSource{Repository: repo, Policy: accountcore.GrokProviderRefreshPolicy()}, upstream, nil)

	result, err := svc.ProbeUsage(context.Background(), account.ID)

	require.Error(t, err)
	require.Nil(t, result)
	require.Equal(t, "GROK_QUOTA_PROBE_UPSTREAM_ERROR", apperror.Reason(err))
	requests := upstream.snapshotRequests()
	require.Len(t, requests, 1)
	require.Equal(t, http.MethodPost, requests[0].Method)
	require.Equal(t, "/v1/responses", requests[0].URL.Path)
}

func TestGrokQuotaServiceProbeUsageStoresHeaders(t *testing.T) {
	t.Parallel()

	account := healthyGrokQuotaOAuthAccount(42)
	repo := &grokQuotaAccountRepo{
		grokQuotaReadStore: &grokQuotaReadStore{
			accountsByID: map[int64]*accountcore.Record{42: account},
		},
	}
	upstream := &grokQuotaHTTPRecorder{resp: &http.Response{
		StatusCode: http.StatusOK,
		Header: http.Header{
			"X-Ratelimit-Limit-Requests":     []string{"10"},
			"X-Ratelimit-Remaining-Requests": []string{"7"},
			"X-Ratelimit-Reset-Requests":     []string{"2000000000"},
			"X-Ratelimit-Limit-Tokens":       []string{"1000"},
			"X-Ratelimit-Remaining-Tokens":   []string{"900"},
		},
		Body: io.NopCloser(strings.NewReader(`{"id":"resp_probe"}`)),
	}}
	svc := newGrokQuotaFixture(repo, nil, &accountcore.GrokTokenSource{Repository: repo, Policy: accountcore.GrokProviderRefreshPolicy()}, upstream, nil)

	result, err := svc.ProbeUsage(context.Background(), 42)
	require.NoError(t, err)
	require.Equal(t, http.StatusOK, result.StatusCode)
	require.Equal(t, "grok-4.5", result.Model)
	require.True(t, result.HeadersObserved)
	require.NotNil(t, result.Snapshot)
	require.True(t, result.Snapshot.HeadersObserved)
	require.Equal(t, "active_probe", result.Snapshot.ObservationSource)
	require.NotEmpty(t, result.Snapshot.LastProbeAt)
	require.NotEmpty(t, result.Snapshot.LastHeadersSeenAt)
	require.NotNil(t, result.Snapshot.Requests)
	require.EqualValues(t, 10, *result.Snapshot.Requests.Limit)
	require.EqualValues(t, 7, *result.Snapshot.Requests.Remaining)
	require.Equal(t, "https://cli-chat-proxy.grok.com/v1/responses", upstream.lastReq.URL.String())
	require.Equal(t, "Bearer access-token", upstream.lastReq.Header.Get("Authorization"))
	require.Equal(t, xai.CLIClientVersion, upstream.lastReq.Header.Get("X-Grok-Client-Version"))
	require.Equal(t, "application/json, text/event-stream", upstream.lastReq.Header.Get("Accept"))
	require.Equal(t, "grok-4.5", gjson.GetBytes(upstream.lastBody, "model").String())
	require.Equal(t, "hi", gjson.GetBytes(upstream.lastBody, "input").String())
	require.True(t, gjson.GetBytes(upstream.lastBody, "stream").Bool())
	require.False(t, gjson.GetBytes(upstream.lastBody, "max_output_tokens").Exists())
	require.False(t, gjson.GetBytes(upstream.lastBody, "store").Exists())
	require.NotNil(t, repo.updates[42]["grok_usage_snapshot"])
}

func TestGrokQuotaServiceProbeUsageIgnoresAccountGrokMapping(t *testing.T) {
	t.Parallel()

	account := healthyGrokQuotaOAuthAccount(47)
	account.Credentials["model_mapping"] = map[string]any{
		"grok":          "grok-composer",
		"grok-composer": "grok-composer-2.5-fast",
	}
	repo := &grokQuotaAccountRepo{
		grokQuotaReadStore: &grokQuotaReadStore{
			accountsByID: map[int64]*accountcore.Record{47: account},
		},
	}
	upstream := &grokQuotaHTTPRecorder{resp: &http.Response{
		StatusCode: http.StatusOK,
		Header:     http.Header{},
		Body:       io.NopCloser(strings.NewReader(`{"id":"resp_probe"}`)),
	}}
	svc := newGrokQuotaFixture(repo, nil, &accountcore.GrokTokenSource{Repository: repo, Policy: accountcore.GrokProviderRefreshPolicy()}, upstream, nil)

	result, err := svc.ProbeUsage(context.Background(), 47)
	require.NoError(t, err)
	require.Equal(t, "grok-4.5", result.Model)
	require.Equal(t, "grok-4.5", gjson.GetBytes(upstream.lastBody, "model").String())
	require.NotContains(t, string(upstream.lastBody), "grok-composer")
}

func TestGrokQuotaServiceProbeUsageReportsProbeModelOnUpstreamError(t *testing.T) {
	t.Parallel()

	account := healthyGrokQuotaOAuthAccount(48)
	repo := &grokQuotaAccountRepo{
		grokQuotaReadStore: &grokQuotaReadStore{
			accountsByID: map[int64]*accountcore.Record{48: account},
		},
	}
	upstream := &grokQuotaHTTPRecorder{resp: &http.Response{
		StatusCode: http.StatusBadRequest,
		Header:     http.Header{},
		Body:       io.NopCloser(strings.NewReader(`{"code":"invalid-argument","error":"Model not found"}`)),
	}}
	svc := newGrokQuotaFixture(repo, nil, &accountcore.GrokTokenSource{Repository: repo, Policy: accountcore.GrokProviderRefreshPolicy()}, upstream, nil)

	_, err := svc.ProbeUsage(context.Background(), 48)
	require.Error(t, err)
	require.Equal(t, "GROK_QUOTA_PROBE_UPSTREAM_ERROR", apperror.Reason(err))
	require.Contains(t, apperror.Message(err), `probe model "grok-4.5"`)
}

func TestGrokQuotaServiceProbeUsageRedactsUpstreamErrorBodyFromErrorAndLogs(t *testing.T) {
	const upstreamSecret = "upstream-secret-refresh-token"
	account := healthyGrokQuotaOAuthAccount(49)
	repo := &grokQuotaAccountRepo{
		grokQuotaReadStore: &grokQuotaReadStore{
			accountsByID: map[int64]*accountcore.Record{49: account},
		},
	}
	upstream := &grokQuotaHTTPRecorder{resp: &http.Response{
		StatusCode: http.StatusBadRequest,
		Header:     http.Header{},
		Body: io.NopCloser(strings.NewReader(
			`{"error":"` + upstreamSecret + `","detail":"credential rejected"}`,
		)),
	}}
	svc := newGrokQuotaFixture(
		repo,
		nil,
		&accountcore.GrokTokenSource{Repository: repo, Policy: accountcore.GrokProviderRefreshPolicy()},
		upstream,
		nil,
	)

	var logs bytes.Buffer
	previousLogger := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&logs, nil)))
	defer slog.SetDefault(previousLogger)

	_, err := svc.ProbeUsage(context.Background(), account.ID)
	require.Error(t, err)
	require.Equal(t, "GROK_QUOTA_PROBE_UPSTREAM_ERROR", apperror.Reason(err))
	require.Contains(t, apperror.Message(err), `probe model "grok-4.5"`)
	require.NotContains(t, err.Error(), upstreamSecret)
	require.NotContains(t, apperror.Message(err), upstreamSecret)
	require.Contains(t, logs.String(), "GROK_QUOTA_PROBE_UPSTREAM_ERROR")
	require.NotContains(t, logs.String(), upstreamSecret)
	require.NotContains(t, logs.String(), "credential rejected")
	require.Equal(t, xai.DefaultCLIBaseURL+"/responses", upstream.lastReq.URL.String())
}

func TestGrokQuotaServiceProbeUsageLoadsProxyWhenAccountEdgeMissing(t *testing.T) {
	t.Parallel()

	proxyID := int64(7)
	account := healthyGrokQuotaOAuthAccount(46)
	account.ProxyID = &proxyID
	repo := &grokQuotaAccountRepo{
		grokQuotaReadStore: &grokQuotaReadStore{
			accountsByID: map[int64]*accountcore.Record{46: account},
		},
	}
	proxyRepo := &grokQuotaProxyRepo{
		proxies: map[int64]*egress.Proxy{
			proxyID: {
				ID:       proxyID,
				Protocol: "http",
				Host:     "proxy.test",
				Port:     3128,
			},
		},
	}
	upstream := &grokQuotaHTTPRecorder{resp: &http.Response{
		StatusCode: http.StatusOK,
		Header:     http.Header{},
		Body:       io.NopCloser(strings.NewReader(`{"id":"resp_probe"}`)),
	}}
	svc := newGrokQuotaFixture(repo, proxyRepo, &accountcore.GrokTokenSource{Repository: repo, Policy: accountcore.GrokProviderRefreshPolicy()}, upstream, nil)

	_, err := svc.ProbeUsage(context.Background(), 46)
	require.NoError(t, err)
	require.Equal(t, 1, proxyRepo.calls)
	require.Equal(t, "http://proxy.test:3128", upstream.lastProxyURL)
}

func TestGrokQuotaServiceProbeUsageStoresNoHeadersState(t *testing.T) {
	t.Parallel()

	account := healthyGrokQuotaOAuthAccount(45)
	observedResetAt := time.Now().Add(-time.Second).UTC().Truncate(time.Second)
	observedLimitedAt := observedResetAt.Add(-(10 * time.Minute))
	account.RateLimitedAt = &observedLimitedAt
	account.RateLimitResetAt = &observedResetAt
	repo := &grokQuotaAccountRepo{
		grokQuotaReadStore: &grokQuotaReadStore{
			accountsByID: map[int64]*accountcore.Record{45: account},
		},
		recoveryClearResult: true,
	}
	upstream := &grokQuotaHTTPRecorder{resp: &http.Response{
		StatusCode: http.StatusOK,
		Header:     http.Header{},
		Body:       io.NopCloser(strings.NewReader(`{"id":"resp_probe"}`)),
	}}
	svc := newGrokQuotaFixture(repo, nil, &accountcore.GrokTokenSource{Repository: repo, Policy: accountcore.GrokProviderRefreshPolicy()}, upstream, nil)

	result, err := svc.ProbeUsage(context.Background(), 45)
	require.NoError(t, err)
	require.Equal(t, http.StatusOK, result.StatusCode)
	require.False(t, result.HeadersObserved)
	require.NotNil(t, result.Snapshot)
	require.False(t, result.Snapshot.HeadersObserved)
	require.Equal(t, "active_probe", result.Snapshot.ObservationSource)
	require.NotEmpty(t, result.Snapshot.LastProbeAt)
	require.Empty(t, result.Snapshot.LastHeadersSeenAt)

	stored, ok := repo.updates[45]["grok_usage_snapshot"].(*xai.QuotaSnapshot)
	require.True(t, ok)
	require.False(t, stored.HeadersObserved)
	require.Equal(t, http.StatusOK, stored.StatusCode)
	require.Equal(t, 1, repo.recoveryClearCalls)
	require.Equal(t, observedLimitedAt, repo.recoveryObservedAt)
	require.Equal(t, observedResetAt, repo.recoveryObservedReset)
}

func TestGrokQuotaServiceProbeUsageDoesNotOverwriteSnapshotOnUnauthorized(t *testing.T) {
	t.Parallel()

	account := healthyGrokQuotaOAuthAccount(44)
	previous := &xai.QuotaSnapshot{StatusCode: http.StatusOK, HeadersObserved: true}
	account.Extra = map[string]any{"grok_usage_snapshot": previous}
	repo := &grokQuotaAccountRepo{grokQuotaReadStore: &grokQuotaReadStore{
		accountsByID: map[int64]*accountcore.Record{account.ID: account},
	}}
	upstream := &grokQuotaHTTPRecorder{resp: &http.Response{
		StatusCode: http.StatusUnauthorized,
		Header:     http.Header{},
		Body:       io.NopCloser(strings.NewReader(`{"error":"unauthorized"}`)),
	}}
	svc := newGrokQuotaFixture(repo, nil, &accountcore.GrokTokenSource{Repository: repo, Policy: accountcore.GrokProviderRefreshPolicy()}, upstream, nil)

	_, err := svc.ProbeUsage(context.Background(), account.ID)
	require.Error(t, err)
	require.Equal(t, 0, repo.updateCalls)
	require.Same(t, previous, account.Extra["grok_usage_snapshot"])
}

func TestGrokQuotaServiceProbeUsageReturnsRateLimitedSnapshot(t *testing.T) {
	t.Parallel()

	account := healthyGrokQuotaOAuthAccount(43)
	repo := &grokQuotaAccountRepo{
		grokQuotaReadStore: &grokQuotaReadStore{
			accountsByID: map[int64]*accountcore.Record{43: account},
		},
	}
	upstream := &grokQuotaHTTPRecorder{resp: &http.Response{
		StatusCode: http.StatusTooManyRequests,
		Header:     http.Header{"Retry-After": []string{"45"}},
		Body:       io.NopCloser(strings.NewReader(`{"error":{"message":"rate limited"}}`)),
	}}
	svc := newGrokQuotaFixture(repo, nil, &accountcore.GrokTokenSource{Repository: repo, Policy: accountcore.GrokProviderRefreshPolicy()}, upstream, nil)

	result, err := svc.ProbeUsage(context.Background(), 43)
	require.NoError(t, err)
	require.Equal(t, http.StatusTooManyRequests, result.StatusCode)
	require.NotNil(t, result.Snapshot)
	require.NotNil(t, result.Snapshot.RetryAfterSeconds)
	require.Equal(t, 45, *result.Snapshot.RetryAfterSeconds)
	require.Equal(t, 1, repo.rateLimitedCalls)
	require.Equal(t, account.ID, repo.lastRateLimitedID)
	require.WithinDuration(t, time.Now().Add(45*time.Second), repo.lastRateLimitResetAt, time.Second)
	require.Zero(t, repo.tempUnschedCalls)
}

func TestGrokQuotaServiceQueryQuotaFreeFallsBackToGrok45(t *testing.T) {
	t.Parallel()

	account := healthyGrokQuotaOAuthAccount(51)
	repo := &grokQuotaAccountRepo{grokQuotaReadStore: &grokQuotaReadStore{
		accountsByID: map[int64]*accountcore.Record{account.ID: account},
	}}
	upstream := &grokHybridUpstream{}
	usageRepo := &grokQuotaUsageLogRepo{stats: &accountcore.WindowStats{Tokens: 1_000_000}}
	svc := newGrokQuotaFixture(repo, nil, &accountcore.GrokTokenSource{Repository: repo, Policy: accountcore.GrokProviderRefreshPolicy()}, upstream, nil, usageRepo)

	result, err := svc.QueryQuota(context.Background(), account.ID)
	require.NoError(t, err)
	require.Equal(t, "hybrid_probe", result.Source)
	require.Equal(t, "grok-4.5", result.Model)
	require.NotNil(t, result.Billing)
	require.Nil(t, result.Billing.UsagePercent)
	require.NotNil(t, result.LocalUsage24h)
	require.EqualValues(t, 1_000_000, result.LocalUsage24h.Tokens)
	require.Equal(t, 1, usageRepo.calls)
	require.WithinDuration(t, time.Now().UTC().Add(-24*time.Hour), usageRepo.startTimes[0], time.Second)
	require.NotNil(t, result.Snapshot)
	require.NotNil(t, result.Snapshot.Tokens)
	require.EqualValues(t, 2_000_000, *result.Snapshot.Tokens.Limit)
	require.True(t, result.HeadersObserved)

	requests, bodies := upstream.snapshot()
	require.Len(t, requests, 3)
	responseCalls := 0
	for i, req := range requests {
		if req.URL.Path != "/v1/responses" {
			continue
		}
		responseCalls++
		require.Equal(t, http.MethodPost, req.Method)
		require.Equal(t, "application/json, text/event-stream", req.Header.Get("Accept"))
		require.Equal(t, "grok-4.5", gjson.GetBytes(bodies[i], "model").String())
		require.Equal(t, "hi", gjson.GetBytes(bodies[i], "input").String())
		require.True(t, gjson.GetBytes(bodies[i], "stream").Bool())
		require.False(t, gjson.GetBytes(bodies[i], "max_output_tokens").Exists())
		require.False(t, gjson.GetBytes(bodies[i], "store").Exists())
	}
	require.Equal(t, 1, responseCalls)
}

func TestGrokQuotaServiceQueryQuotaPaidBillingSkipsActiveProbe(t *testing.T) {
	t.Parallel()

	account := healthyGrokQuotaOAuthAccount(52)
	// 本用例只检查账单与主动额度请求；有效模型缓存隔离原有后台目录同步。
	account.Extra = map[string]any{accountcore.GrokObservedModelsExtraKey: map[string]any{
		"models": []string{"grok-4.5"}, "fetched_at": time.Now().UTC().Format(time.RFC3339),
	}}
	repo := &grokQuotaAccountRepo{grokQuotaReadStore: &grokQuotaReadStore{
		accountsByID: map[int64]*accountcore.Record{account.ID: account},
	}}
	usagePercent := 25.0
	upstream := &grokHybridUpstream{weeklyUsagePercent: &usagePercent}
	usageRepo := &grokQuotaUsageLogRepo{stats: &accountcore.WindowStats{Tokens: 1_000_000}}
	svc := newGrokQuotaFixture(repo, nil, &accountcore.GrokTokenSource{Repository: repo, Policy: accountcore.GrokProviderRefreshPolicy()}, upstream, nil, usageRepo)

	result, err := svc.QueryQuota(context.Background(), account.ID)
	require.NoError(t, err)
	require.Equal(t, "billing_probe", result.Source)
	require.NotNil(t, result.Billing)
	require.InDelta(t, usagePercent, *result.Billing.UsagePercent, 1e-9)
	require.Nil(t, result.Snapshot)
	require.Empty(t, result.Model)
	require.Nil(t, result.LocalUsage24h)
	require.NoError(t, svc.StopContext(context.Background()))

	requests, _ := upstream.snapshot()
	require.Len(t, requests, 2)
	for _, req := range requests {
		require.Equal(t, "/v1/billing", req.URL.Path)
	}
}

func TestGrokQuotaServiceQueryQuotaCustomPaidMonthlyLimitSkipsActiveProbe(t *testing.T) {
	t.Parallel()

	account := healthyGrokQuotaOAuthAccount(57)
	repo := &grokQuotaAccountRepo{grokQuotaReadStore: &grokQuotaReadStore{
		accountsByID: map[int64]*accountcore.Record{account.ID: account},
	}}
	monthlyLimit := 25_000.0
	upstream := &grokHybridUpstream{monthlyLimitCents: &monthlyLimit}
	svc := newGrokQuotaFixture(repo, nil, &accountcore.GrokTokenSource{Repository: repo, Policy: accountcore.GrokProviderRefreshPolicy()}, upstream, nil)

	result, err := svc.QueryQuota(context.Background(), account.ID)
	require.NoError(t, err)
	require.Equal(t, "billing_probe", result.Source)
	require.NotNil(t, result.Billing)
	require.InDelta(t, monthlyLimit, *result.Billing.MonthlyLimitCents, 1e-9)
	require.Nil(t, result.Snapshot)

	requests, _ := upstream.snapshot()
	require.Len(t, requests, 2)
	for _, req := range requests {
		require.Equal(t, "/v1/billing", req.URL.Path)
	}
}

func TestAccountUsageServiceGrokRefreshUsesBillingOnly(t *testing.T) {
	t.Parallel()

	account := healthyGrokQuotaOAuthAccount(54)
	repo := &grokQuotaAccountRepo{grokQuotaReadStore: &grokQuotaReadStore{
		accountsByID: map[int64]*accountcore.Record{account.ID: account},
	}}
	upstream := &grokHybridUpstream{}
	usageRepo := &grokQuotaUsageLogRepo{stats: &accountcore.WindowStats{Tokens: 750_000}}
	quotaService := newGrokQuotaFixture(repo, nil, &accountcore.GrokTokenSource{Repository: repo, Policy: accountcore.GrokProviderRefreshPolicy()}, upstream, nil, usageRepo)
	cache := accountcore.NewOAuthUsageCache()
	statistics := accountcore.NewLocalUsageStatistics(usageRepo, cache, accountcore.LocalUsageStatisticsOptions{Now: time.Now})
	usageService := accountcore.NewOAuthUsageService(nil, cache, statistics, accountcore.OAuthUsageOptions{Grok: accountcore.GrokUsageOptions{
		Available: func() bool { return true }, StatsAvailable: func() bool { return true },
		Build: NewGrokQuotaView().BuildUsageInfo, Enrich: EnrichUsageWithAccountError,
		Probe: func(ctx context.Context, id int64) (*accountcore.GrokUsageProbe, error) {
			value, err := quotaService.ProbeBilling(ctx, id)
			if value == nil {
				return nil, err
			}
			return &accountcore.GrokUsageProbe{Billing: value.Billing, LocalUsage24h: value.LocalUsage24h, LocalUsage7d: value.LocalUsage7d, LocalUsageMonthly: value.LocalUsageMonthly}, err
		},
	}})

	usage, err := usageService.GetGrokUsage(context.Background(), account, false)
	require.NoError(t, err)
	require.NotNil(t, usage.GrokBilling)
	require.Nil(t, usage.GrokBilling.UsagePercent)
	require.NotNil(t, usage.GrokLocalUsage24h)
	require.EqualValues(t, 750_000, usage.GrokLocalUsage24h.Tokens)
	require.Equal(t, 1, usageRepo.calls)
	require.Len(t, usageRepo.startTimes, 1)
	require.WithinDuration(t, time.Now().UTC().Add(-24*time.Hour), usageRepo.startTimes[0], time.Second)

	requests, _ := upstream.snapshot()
	require.Len(t, requests, 2)
	for _, req := range requests {
		require.Equal(t, http.MethodGet, req.Method)
		require.Equal(t, "/v1/billing", req.URL.Path)
	}
}

func TestGrokQuotaServiceProbeFlightsDeduplicateBillingAndSeparateActive(t *testing.T) {
	t.Parallel()

	account := healthyGrokQuotaOAuthAccount(55)
	repo := &grokQuotaAccountRepo{grokQuotaReadStore: &grokQuotaReadStore{
		accountsByID: map[int64]*accountcore.Record{account.ID: account},
	}}
	billingStarted := make(chan struct{})
	billingRelease := make(chan struct{})
	upstream := &grokHybridUpstream{billingStarted: billingStarted, billingRelease: billingRelease}
	svc := newGrokQuotaFixture(repo, nil, &accountcore.GrokTokenSource{Repository: repo, Policy: accountcore.GrokProviderRefreshPolicy()}, upstream, nil)

	type probeOutcome struct {
		result *accountcore.GrokQuotaProbeResult
		err    error
	}
	billingOutcomes := make(chan probeOutcome, 2)
	go func() {
		result, err := svc.ProbeBilling(context.Background(), account.ID)
		billingOutcomes <- probeOutcome{result: result, err: err}
	}()
	<-billingStarted
	secondStarted := make(chan struct{})
	go func() {
		close(secondStarted)
		result, err := svc.ProbeBilling(context.Background(), account.ID)
		billingOutcomes <- probeOutcome{result: result, err: err}
	}()
	<-secondStarted
	time.Sleep(25 * time.Millisecond)

	activeResult, err := svc.ProbeUsage(context.Background(), account.ID)
	require.NoError(t, err)
	require.NotNil(t, activeResult.Snapshot)
	close(billingRelease)
	for range 2 {
		outcome := <-billingOutcomes
		require.NoError(t, outcome.err)
		require.NotNil(t, outcome.result.Billing)
	}

	requests, _ := upstream.snapshot()
	billingCalls := 0
	activeCalls := 0
	for _, req := range requests {
		switch req.URL.Path {
		case "/v1/billing":
			billingCalls++
		case "/v1/responses":
			activeCalls++
		}
	}
	require.Equal(t, 2, billingCalls)
	require.Equal(t, 1, activeCalls)
}

func TestGrokQuotaServiceBilling429DoesNotPauseModelScheduling(t *testing.T) {
	t.Parallel()

	account := healthyGrokQuotaOAuthAccount(56)
	repo := &grokQuotaAccountRepo{grokQuotaReadStore: &grokQuotaReadStore{
		accountsByID: map[int64]*accountcore.Record{account.ID: account},
	}}
	upstream := &grokHybridUpstream{
		billingStatus:  http.StatusTooManyRequests,
		billingHeaders: http.Header{"Retry-After": []string{"45"}},
	}
	svc := newGrokQuotaFixture(repo, nil, &accountcore.GrokTokenSource{Repository: repo, Policy: accountcore.GrokProviderRefreshPolicy()}, upstream, nil)

	result, err := svc.ProbeBilling(context.Background(), account.ID)

	require.Error(t, err)
	require.Nil(t, result)
	require.Zero(t, repo.rateLimitedCalls)
}

func TestGrokQuotaServiceBilling403PersistsMediaEligibilitySignal(t *testing.T) {
	t.Parallel()

	account := healthyGrokQuotaOAuthAccount(58)
	repo := &grokQuotaAccountRepo{grokQuotaReadStore: &grokQuotaReadStore{
		accountsByID: map[int64]*accountcore.Record{account.ID: account},
	}}
	upstream := &grokHybridUpstream{billingStatus: http.StatusForbidden}
	svc := newGrokQuotaFixture(repo, nil, &accountcore.GrokTokenSource{Repository: repo, Policy: accountcore.GrokProviderRefreshPolicy()}, upstream, nil)

	result, err := svc.ProbeBilling(context.Background(), account.ID)

	require.Error(t, err)
	require.Nil(t, result)
	require.Equal(t, 1, repo.updateCalls)
	raw := repo.updates[account.ID][accountcore.GrokUsageBillingExtraKey]
	billing, ok := raw.(*xai.BillingSummary)
	require.True(t, ok)
	require.Equal(t, http.StatusForbidden, billing.StatusCode)
	require.Equal(t, http.StatusForbidden, billing.WeeklyStatusCode)
	require.Equal(t, http.StatusForbidden, billing.MonthlyStatusCode)
	require.True(t, billing.Partial)

	account.Extra = map[string]any{accountcore.GrokUsageBillingExtraKey: billing}
	eligible, reason := accountcore.GrokMediaGenerationEligibility(account, GrokTierRules())
	require.False(t, eligible)
	require.Equal(t, "billing_forbidden", reason)
}

func TestGrokQuotaServicePartialBilling403PersistsMediaEligibilitySignal(t *testing.T) {
	t.Parallel()

	account := healthyGrokQuotaOAuthAccount(59)
	repo := &grokQuotaAccountRepo{grokQuotaReadStore: &grokQuotaReadStore{
		accountsByID: map[int64]*accountcore.Record{account.ID: account},
	}}
	upstream := &grokHybridUpstream{
		weeklyBillingStatus:  http.StatusForbidden,
		monthlyBillingStatus: http.StatusOK,
	}
	svc := newGrokQuotaFixture(repo, nil, &accountcore.GrokTokenSource{Repository: repo, Policy: accountcore.GrokProviderRefreshPolicy()}, upstream, nil)

	result, err := svc.ProbeBilling(context.Background(), account.ID)

	require.NoError(t, err)
	require.NotNil(t, result)
	require.NotNil(t, result.Billing)
	require.Equal(t, http.StatusOK, result.StatusCode)
	require.Equal(t, http.StatusForbidden, result.Billing.WeeklyStatusCode)
	require.Equal(t, http.StatusOK, result.Billing.MonthlyStatusCode)
	require.True(t, result.Billing.Partial)
	require.Contains(t, result.Billing.FailedWindows, "weekly")
	require.Equal(t, 1, repo.updateCalls)

	account.Extra = map[string]any{accountcore.GrokUsageBillingExtraKey: result.Billing}
	eligible, reason := accountcore.GrokMediaGenerationEligibility(account, GrokTierRules())
	require.False(t, eligible)
	require.Equal(t, "billing_forbidden", reason)
}

func TestGrokQuotaServiceProbeMediaEligibility(t *testing.T) {
	t.Run("positive paid evidence enables media", func(t *testing.T) {
		usagePercent := 10.0
		monthlyLimit := 15_000.0
		account := healthyGrokQuotaOAuthAccount(60)
		repo := &grokQuotaAccountRepo{grokQuotaReadStore: &grokQuotaReadStore{
			accountsByID: map[int64]*accountcore.Record{account.ID: account},
		}}
		upstream := &grokHybridUpstream{weeklyUsagePercent: &usagePercent, monthlyLimitCents: &monthlyLimit}
		svc := newGrokQuotaFixture(repo, nil, &accountcore.GrokTokenSource{Repository: repo, Policy: accountcore.GrokProviderRefreshPolicy()}, upstream, nil)

		eligible, reason, err := svc.ProbeMediaEligibility(context.Background(), account.ID)

		require.NoError(t, err)
		require.True(t, eligible)
		require.Equal(t, "eligible", reason)
	})

	t.Run("successful empty billing identifies free account", func(t *testing.T) {
		account := healthyGrokQuotaOAuthAccount(61)
		repo := &grokQuotaAccountRepo{grokQuotaReadStore: &grokQuotaReadStore{
			accountsByID: map[int64]*accountcore.Record{account.ID: account},
		}}
		svc := newGrokQuotaFixture(repo, nil, &accountcore.GrokTokenSource{Repository: repo, Policy: accountcore.GrokProviderRefreshPolicy()}, &grokHybridUpstream{}, nil)

		eligible, reason, err := svc.ProbeMediaEligibility(context.Background(), account.ID)

		require.NoError(t, err)
		require.False(t, eligible)
		require.Equal(t, "billing_free_tier", reason)
	})

	t.Run("forbidden billing is deterministic ineligibility", func(t *testing.T) {
		account := healthyGrokQuotaOAuthAccount(62)
		repo := &grokQuotaAccountRepo{grokQuotaReadStore: &grokQuotaReadStore{
			accountsByID: map[int64]*accountcore.Record{account.ID: account},
		}}
		svc := newGrokQuotaFixture(repo, nil, &accountcore.GrokTokenSource{Repository: repo, Policy: accountcore.GrokProviderRefreshPolicy()}, &grokHybridUpstream{billingStatus: http.StatusForbidden}, nil)

		eligible, reason, err := svc.ProbeMediaEligibility(context.Background(), account.ID)

		require.NoError(t, err)
		require.False(t, eligible)
		require.Equal(t, "billing_forbidden", reason)
	})
}

func TestPreferBillingObservationStatus(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name          string
		weeklyStatus  int
		monthlyStatus int
		want          int
	}{
		{name: "weekly forbidden wins", weeklyStatus: http.StatusForbidden, monthlyStatus: http.StatusBadGateway, want: http.StatusForbidden},
		{name: "monthly forbidden wins", weeklyStatus: http.StatusBadGateway, monthlyStatus: http.StatusForbidden, want: http.StatusForbidden},
		{name: "weekly observation otherwise wins", weeklyStatus: http.StatusTooManyRequests, monthlyStatus: http.StatusBadGateway, want: http.StatusTooManyRequests},
		{name: "monthly observation is fallback", monthlyStatus: http.StatusBadGateway, want: http.StatusBadGateway},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			require.Equal(t, tt.want, (&accountcore.GrokQuotaService{Options: accountcore.GrokQuotaOptions{MapStatus: forward.MapStatus, Warn: slog.Warn}}).PreferBillingObservationStatus(tt.weeklyStatus, tt.monthlyStatus))
		})
	}
}

func TestGrokQuotaServiceQueryQuotaFree429PersistsLimitAndKeepsBilling(t *testing.T) {
	t.Parallel()

	account := healthyGrokQuotaOAuthAccount(53)
	repo := &grokQuotaAccountRepo{grokQuotaReadStore: &grokQuotaReadStore{
		accountsByID: map[int64]*accountcore.Record{account.ID: account},
	}}
	upstream := &grokHybridUpstream{
		activeStatus:  http.StatusTooManyRequests,
		activeHeaders: http.Header{"Retry-After": []string{"45"}},
	}
	svc := newGrokQuotaFixture(repo, nil, &accountcore.GrokTokenSource{Repository: repo, Policy: accountcore.GrokProviderRefreshPolicy()}, upstream, nil)

	result, err := svc.QueryQuota(context.Background(), account.ID)
	require.NoError(t, err)
	require.Equal(t, http.StatusTooManyRequests, result.StatusCode)
	require.NotNil(t, result.Billing)
	require.NotNil(t, result.Snapshot)
	require.Equal(t, 45, *result.Snapshot.RetryAfterSeconds)
	require.Equal(t, 1, repo.rateLimitedCalls)
	require.Equal(t, account.ID, repo.lastRateLimitedID)
	require.WithinDuration(t, time.Now().Add(45*time.Second), repo.lastRateLimitResetAt, time.Second)
}

func TestGrokQuotaServiceResetQuotaUnsupported(t *testing.T) {
	t.Parallel()

	account := &accountcore.Record{
		ID:       44,
		Platform: capability.PlatformGrok,
		Type:     capability.AccountTypeOAuth,
	}
	repo := &grokQuotaAccountRepo{
		grokQuotaReadStore: &grokQuotaReadStore{
			accountsByID: map[int64]*accountcore.Record{44: account},
		},
	}
	svc := newGrokQuotaFixture(repo, nil, nil, nil, nil)

	_, err := svc.ResetQuota(context.Background(), 44)
	require.Error(t, err)
	require.Equal(t, http.StatusNotImplemented, s15httpx.ErrorCode(err))
	require.Equal(t, "GROK_QUOTA_RESET_UNSUPPORTED", apperror.Reason(err))
}

// 夹具只组合生产构造器与窄存储、传输替身，不重建旧聚合服务。
func newGrokQuotaFixture(store GrokQuotaStore, proxy *grokQuotaProxyRepo, token *accountcore.GrokTokenSource, transport interface {
	Do(*http.Request, string, int64, int) (*http.Response, error)
}, validator xai.BaseURLValidator, readers ...accountcore.LocalUsageStats) *accountcore.GrokQuotaService {
	requests := &GrokQuotaTransport{OperatorValidator: validator, MapStatus: forward.MapStatus}
	if proxy != nil {
		requests.Proxy = proxy.GetByID
	}
	if transport != nil {
		requests.Do = transport.Do
	}
	var stats accountcore.LocalUsageStats
	if len(readers) > 0 {
		stats = readers[0]
	}
	return NewGrokQuota(store, token, requests, stats)
}

// 读取边界与原适配器一样返回独立记录，写入断言保留在存储替身。
type grokQuotaReadStore struct {
	accountsByID map[int64]*accountcore.Record
	getByIDCalls int
}

func (r *grokQuotaReadStore) GetByID(_ context.Context, id int64) (*accountcore.Record, error) {
	r.getByIDCalls++
	if value, ok := r.accountsByID[id]; ok {
		return accountcore.CloneRecord(value), nil
	}
	return nil, errors.New("account not found")
}

// HTTP 记录器保留每次请求与原始正文，断言不只检查最终响应。
type grokQuotaHTTPRecorder struct {
	lastReq      *http.Request
	lastBody     []byte
	lastProxyURL string
	requests     []*http.Request
	bodies       [][]byte
	resp         *http.Response
	responses    []*http.Response
	err          error
}

func (u *grokQuotaHTTPRecorder) Do(req *http.Request, proxyURL string, _ int64, _ int) (*http.Response, error) {
	u.lastReq = req
	u.lastProxyURL = proxyURL
	if req != nil && req.Body != nil {
		body, err := io.ReadAll(req.Body)
		if err != nil {
			return nil, err
		}
		u.lastBody = body
		u.bodies = append(u.bodies, append([]byte(nil), body...))
		if err := req.Body.Close(); err != nil {
			return nil, err
		}
		req.Body = io.NopCloser(bytes.NewReader(body))
	}
	u.requests = append(u.requests, req)
	if u.err != nil {
		return nil, u.err
	}
	if len(u.responses) > 0 {
		response := u.responses[0]
		u.responses = u.responses[1:]
		return response, nil
	}
	return u.resp, nil
}
