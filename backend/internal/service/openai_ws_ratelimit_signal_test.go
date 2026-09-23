package service

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	accountcore "github.com/TokenFlux/TokenRouter/internal/account"
	"github.com/TokenFlux/TokenRouter/internal/billing"
	forwardcore "github.com/TokenFlux/TokenRouter/internal/gateway/forward"
	gatewayprovider "github.com/TokenFlux/TokenRouter/internal/gateway/provider"
	"github.com/TokenFlux/TokenRouter/internal/infra/httpclient/tlsfingerprint"
	"github.com/TokenFlux/TokenRouter/internal/pkg/pagination"
	"github.com/TokenFlux/TokenRouter/internal/protocol/openai"
	"github.com/TokenFlux/TokenRouter/internal/routing/capability"
	upstreamcore "github.com/TokenFlux/TokenRouter/internal/upstream"
	upstreamopenai "github.com/TokenFlux/TokenRouter/internal/upstream/openai"

	coderws "github.com/coder/websocket"
	"github.com/gin-gonic/gin"
	"github.com/gorilla/websocket"
	"github.com/stretchr/testify/require"
)

type openAIWSRateLimitSignalRepo struct {
	stubOpenAIAccountRepo
	rateLimitCalls []time.Time
	tempCalls      []time.Time
	errorCalls     []string
	updateExtra    []map[string]any
}

type openAICodexSnapshotAsyncRepo struct {
	stubOpenAIAccountRepo
	updateExtraCh chan map[string]any
	rateLimitCh   chan time.Time
}

type openAICodexExtraListRepo struct {
	stubOpenAIAccountRepo
	rateLimitCh chan time.Time
}

type openAIWS403CounterCacheStub struct {
	counts []int64
}

func (s *openAIWS403CounterCacheStub) IncrementOpenAI403Count(_ context.Context, _ int64, _ int) (int64, error) {
	if len(s.counts) == 0 {
		return 1, nil
	}
	count := s.counts[0]
	s.counts = s.counts[1:]
	return count, nil
}

func (s *openAIWS403CounterCacheStub) ResetOpenAI403Count(_ context.Context, _ int64) error {
	return nil
}

func (r *openAIWSRateLimitSignalRepo) SetRateLimited(_ context.Context, _ int64, resetAt time.Time) error {
	r.rateLimitCalls = append(r.rateLimitCalls, resetAt)
	return nil
}

func (r *openAIWSRateLimitSignalRepo) SetTempUnschedulable(_ context.Context, _ int64, until time.Time, _ string) error {
	r.tempCalls = append(r.tempCalls, until)
	return nil
}

func (r *openAIWSRateLimitSignalRepo) SetError(_ context.Context, _ int64, errorMsg string) error {
	r.errorCalls = append(r.errorCalls, errorMsg)
	return nil
}

func (r *openAIWSRateLimitSignalRepo) UpdateExtra(_ context.Context, _ int64, updates map[string]any) error {
	copied := make(map[string]any, len(updates))
	for k, v := range updates {
		copied[k] = v
	}
	r.updateExtra = append(r.updateExtra, copied)
	return nil
}

func (r *openAICodexSnapshotAsyncRepo) SetRateLimited(_ context.Context, _ int64, resetAt time.Time) error {
	if r.rateLimitCh != nil {
		r.rateLimitCh <- resetAt
	}
	return nil
}

func (r *openAICodexSnapshotAsyncRepo) UpdateExtra(_ context.Context, _ int64, updates map[string]any) error {
	if r.updateExtraCh != nil {
		copied := make(map[string]any, len(updates))
		for k, v := range updates {
			copied[k] = v
		}
		r.updateExtraCh <- copied
	}
	return nil
}

func (r *openAICodexExtraListRepo) SetRateLimited(_ context.Context, _ int64, resetAt time.Time) error {
	if r.rateLimitCh != nil {
		r.rateLimitCh <- resetAt
	}
	return nil
}

func (r *openAICodexExtraListRepo) ListWithFilters(_ context.Context, params pagination.PaginationParams, platform, accountType, status, search string, groupID int64, privacyMode string) ([]gatewayprovider.ExecutionAccount, *pagination.PaginationResult, error) {
	_ = platform
	_ = accountType
	_ = status
	_ = search
	_ = groupID
	_ = privacyMode
	return r.accounts, &pagination.PaginationResult{Total: int64(len(r.accounts)), Page: params.Page, PageSize: params.PageSize}, nil
}

type openAIWSStatusErrorDialer struct {
	status int
	header http.Header
	err    error
}

func (d *openAIWSStatusErrorDialer) Dial(context.Context, string, http.Header, string, *tlsfingerprint.Profile) (upstreamopenai.WSClientConn, int, http.Header, error) {
	err := d.err
	if err == nil {
		err = errors.New("openai ws dial failed")
	}
	return nil, d.status, upstreamcore.CloneHeader(d.header), err
}

func TestOpenAIGatewayService_Forward_WSv2ErrorEventUsageLimitPersistsRateLimit(t *testing.T) {

	resetAt := time.Now().Add(2 * time.Hour).Unix()
	upgrader := websocket.Upgrader{CheckOrigin: func(r *http.Request) bool { return true }}
	wsServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			t.Errorf("upgrade websocket failed: %v", err)
			return
		}
		defer func() { _ = conn.Close() }()

		var req map[string]any
		if err := conn.ReadJSON(&req); err != nil {
			t.Errorf("read ws request failed: %v", err)
			return
		}
		_ = conn.WriteJSON(map[string]any{
			"type": "error",
			"error": map[string]any{
				"code":      "rate_limit_exceeded",
				"type":      "usage_limit_reached",
				"message":   "The usage limit has been reached",
				"resets_at": resetAt,
			},
		})
	}))
	defer wsServer.Close()

	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodPost, "/openai/v1/responses", nil)
	c.Request.Header.Set("User-Agent", "unit-test-agent/1.0")

	upstream := &httpUpstreamRecorder{
		resp: &http.Response{
			StatusCode: http.StatusOK,
			Header:     http.Header{"Content-Type": []string{"application/json"}},
			Body:       io.NopCloser(strings.NewReader(`{"id":"resp_http_should_not_run"}`)),
		},
	}

	cfg := newOpenAIWSV2TestConfig()
	cfg.Security.URLAllowlist.Enabled = false
	cfg.Security.URLAllowlist.AllowInsecureHTTP = true

	account := gatewayprovider.ExecutionAccount{Record: accountcore.Record{LoadLocation: time.LoadLocation, ID: 501,
		Name:        "openai-ws-rate-limit-event",
		Platform:    capability.PlatformOpenAI,
		Type:        capability.AccountTypeAPIKey,
		Status:      billing.StatusActive,
		Schedulable: true,
		Concurrency: 1,
		Credentials: map[string]any{
			"api_key":  "sk-test",
			"base_url": wsServer.URL,
		},
		Extra: map[string]any{
			"responses_websockets_v2_enabled": true,
		}},
	}
	repo := &openAIWSRateLimitSignalRepo{stubOpenAIAccountRepo: stubOpenAIAccountRepo{accounts: []gatewayprovider.ExecutionAccount{account}}}
	rateSvc := newUpstreamHealthForTest(repo, nil, nil, accountcore.HealthOptions{ForbiddenCounter: &openAIWS403CounterCacheStub{counts: []int64{1}}}, nil)

	svc := withOpenAIExecutionCredentialsForTest(withSchedulerParametersForTest(&OpenAIGatewayService{
		accountRepo:    repo,
		healthObserver: rateSvc,
		httpUpstream:   upstream,
		cache:          &stubGatewayCache{},
		cfg:            cfg,

		toolCorrector: upstreamopenai.NewCodexToolCorrector(),
	}))

	body := []byte(`{"model":"gpt-5.1","stream":false,"input":[{"type":"input_text","text":"hello"}]}`)
	result, err := svc.Forward(context.Background(), c, &account, body)
	require.Error(t, err)
	require.Nil(t, result)
	require.Equal(t, http.StatusTooManyRequests, rec.Code)
	require.Nil(t, upstream.lastReq, "WS 限流 error event 不应回退到同账号 HTTP")
	require.Len(t, repo.rateLimitCalls, 1)
	require.WithinDuration(t, time.Unix(resetAt, 0), repo.rateLimitCalls[0], 2*time.Second)
}

func TestOpenAIGatewayService_Forward_WSv2ErrorEventForbiddenPersistsTempUnschedulable(t *testing.T) {

	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodPost, "/openai/v1/responses", nil)
	c.Request.Header.Set("User-Agent", "unit-test-agent/1.0")

	upstream := &httpUpstreamRecorder{
		resp: &http.Response{
			StatusCode: http.StatusOK,
			Header:     http.Header{"Content-Type": []string{"application/json"}},
			Body:       io.NopCloser(strings.NewReader(`{"id":"resp_http_should_not_run"}`)),
		},
	}

	cfg := newOpenAIWSV2TestConfig()
	cfg.Security.URLAllowlist.Enabled = false
	cfg.Security.URLAllowlist.AllowInsecureHTTP = true
	pool := newOpenAIWSConnPool(cfg)
	pool.SetClientDialerForTest(&openAIWSCaptureDialer{
		conn: &openAIWSCaptureConn{
			events: [][]byte{
				[]byte(`{"type":"error","error":{"code":"forbidden","type":"permission_error","message":"temporary forbidden"}}`),
			},
		},
	})

	account := gatewayprovider.ExecutionAccount{Record: accountcore.Record{LoadLocation: time.LoadLocation, ID: 506,
		Name:        "openai-ws-forbidden-event",
		Platform:    capability.PlatformOpenAI,
		Type:        capability.AccountTypeOAuth,
		Status:      billing.StatusActive,
		Schedulable: true,
		Concurrency: 1,
		Credentials: map[string]any{
			"access_token": "test-access-token",
		},
		Extra: map[string]any{
			"responses_websockets_v2_enabled": true,
		}},
	}
	repo := &openAIWSRateLimitSignalRepo{stubOpenAIAccountRepo: stubOpenAIAccountRepo{accounts: []gatewayprovider.ExecutionAccount{account}}}
	rateSvc := newUpstreamHealthForTest(repo, nil, nil, accountcore.HealthOptions{ForbiddenCounter: &openAIWS403CounterCacheStub{counts: []int64{1}}}, nil)

	svc := withOpenAIExecutionCredentialsForTest(withSchedulerParametersForTest(&OpenAIGatewayService{
		accountRepo:    repo,
		healthObserver: rateSvc,
		httpUpstream:   upstream,
		cache:          &stubGatewayCache{},
		cfg:            cfg,

		toolCorrector: upstreamopenai.NewCodexToolCorrector(),
		openaiWSPool:  pool,
	}))

	before := time.Now()
	body := []byte(`{"model":"gpt-5.1","stream":false,"input":[{"type":"input_text","text":"hello"}]}`)
	result, err := svc.Forward(context.Background(), c, &account, body)
	require.Error(t, err)
	require.Nil(t, result)
	require.Equal(t, http.StatusForbidden, rec.Code)
	require.Nil(t, upstream.lastReq, "WS 运行中 403 error event 不应回退到同账号 HTTP")
	require.Len(t, repo.tempCalls, 1)
	require.Len(t, repo.rateLimitCalls, 0)
	require.WithinDuration(t, before.Add(10*time.Minute), repo.tempCalls[0], 2*time.Second)
}

func TestOpenAIGatewayService_Forward_WSv2Handshake429PersistsRateLimit(t *testing.T) {

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("x-codex-primary-used-percent", "100")
		w.Header().Set("x-codex-primary-reset-after-seconds", "7200")
		w.Header().Set("x-codex-primary-window-minutes", "10080")
		w.Header().Set("x-codex-secondary-used-percent", "3")
		w.Header().Set("x-codex-secondary-reset-after-seconds", "1800")
		w.Header().Set("x-codex-secondary-window-minutes", "300")
		w.WriteHeader(http.StatusTooManyRequests)
		_, _ = w.Write([]byte(`{"error":{"type":"rate_limit_exceeded","message":"rate limited"}}`))
	}))
	defer server.Close()

	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodPost, "/openai/v1/responses", nil)
	c.Request.Header.Set("User-Agent", "unit-test-agent/1.0")

	upstream := &httpUpstreamRecorder{
		resp: &http.Response{
			StatusCode: http.StatusOK,
			Header:     http.Header{"Content-Type": []string{"application/json"}},
			Body:       io.NopCloser(strings.NewReader(`{"id":"resp_http_should_not_run"}`)),
		},
	}

	cfg := newOpenAIWSV2TestConfig()
	cfg.Security.URLAllowlist.Enabled = false
	cfg.Security.URLAllowlist.AllowInsecureHTTP = true

	account := gatewayprovider.ExecutionAccount{Record: accountcore.Record{LoadLocation: time.LoadLocation, ID: 502,
		Name:        "openai-ws-rate-limit-handshake",
		Platform:    capability.PlatformOpenAI,
		Type:        capability.AccountTypeAPIKey,
		Status:      billing.StatusActive,
		Schedulable: true,
		Concurrency: 1,
		Credentials: map[string]any{
			"api_key":  "sk-test",
			"base_url": server.URL,
		},
		Extra: map[string]any{
			"responses_websockets_v2_enabled": true,
		}},
	}
	repo := &openAIWSRateLimitSignalRepo{stubOpenAIAccountRepo: stubOpenAIAccountRepo{accounts: []gatewayprovider.ExecutionAccount{account}}}
	rateSvc := newUpstreamHealthForTest(repo, nil, nil, accountcore.HealthOptions{ForbiddenCounter: &openAIWS403CounterCacheStub{counts: []int64{1}}}, nil)

	svc := withOpenAIExecutionCredentialsForTest(withSchedulerParametersForTest(&OpenAIGatewayService{
		accountRepo:    repo,
		healthObserver: rateSvc,
		httpUpstream:   upstream,
		cache:          &stubGatewayCache{},
		cfg:            cfg,

		toolCorrector: upstreamopenai.NewCodexToolCorrector(),
	}))

	body := []byte(`{"model":"gpt-5.1","stream":false,"input":[{"type":"input_text","text":"hello"}]}`)
	result, err := svc.Forward(context.Background(), c, &account, body)
	require.Error(t, err)
	require.Nil(t, result)
	require.Equal(t, http.StatusTooManyRequests, rec.Code)
	require.Nil(t, upstream.lastReq, "WS 握手 429 不应回退到同账号 HTTP")
	require.Len(t, repo.rateLimitCalls, 1)
	require.NotEmpty(t, repo.updateExtra, "握手 429 的 x-codex 头应立即落库")
	require.Contains(t, repo.updateExtra[0], "codex_usage_updated_at")
}

func TestOpenAIGatewayService_Forward_WSv2Handshake403PersistsTempUnschedulable(t *testing.T) {

	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodPost, "/openai/v1/responses", nil)
	c.Request.Header.Set("User-Agent", "unit-test-agent/1.0")

	upstream := &httpUpstreamRecorder{
		resp: &http.Response{
			StatusCode: http.StatusOK,
			Header:     http.Header{"Content-Type": []string{"application/json"}},
			Body:       io.NopCloser(strings.NewReader(`{"id":"resp_http_should_not_run"}`)),
		},
	}

	cfg := newOpenAIWSV2TestConfig()
	cfg.Security.URLAllowlist.Enabled = false
	cfg.Security.URLAllowlist.AllowInsecureHTTP = true
	pool := newOpenAIWSConnPool(cfg)
	pool.SetClientDialerForTest(&openAIWSStatusErrorDialer{
		status: http.StatusForbidden,
		err:    errors.New("temporary forbidden"),
	})

	account := gatewayprovider.ExecutionAccount{Record: accountcore.Record{LoadLocation: time.LoadLocation, ID: 504,
		Name:        "openai-ws-forbidden-handshake",
		Platform:    capability.PlatformOpenAI,
		Type:        capability.AccountTypeOAuth,
		Status:      billing.StatusActive,
		Schedulable: true,
		Concurrency: 1,
		Credentials: map[string]any{
			"access_token": "test-access-token",
		},
		Extra: map[string]any{
			"responses_websockets_v2_enabled": true,
		}},
	}
	repo := &openAIWSRateLimitSignalRepo{stubOpenAIAccountRepo: stubOpenAIAccountRepo{accounts: []gatewayprovider.ExecutionAccount{account}}}
	rateSvc := newUpstreamHealthForTest(repo, nil, nil, accountcore.HealthOptions{ForbiddenCounter: &openAIWS403CounterCacheStub{counts: []int64{1}}}, nil)

	svc := withOpenAIExecutionCredentialsForTest(withSchedulerParametersForTest(&OpenAIGatewayService{
		accountRepo:    repo,
		healthObserver: rateSvc,
		httpUpstream:   upstream,
		cache:          &stubGatewayCache{},
		cfg:            cfg,

		toolCorrector: upstreamopenai.NewCodexToolCorrector(),
		openaiWSPool:  pool,
	}))

	before := time.Now()
	body := []byte(`{"model":"gpt-5.1","stream":false,"input":[{"type":"input_text","text":"hello"}]}`)
	result, err := svc.Forward(context.Background(), c, &account, body)
	require.Error(t, err)
	require.Nil(t, result)
	require.Equal(t, http.StatusForbidden, rec.Code)
	require.Nil(t, upstream.lastReq, "WS 握手 403 不应回退到同账号 HTTP")
	require.Len(t, repo.tempCalls, 1)
	require.WithinDuration(t, before.Add(10*time.Minute), repo.tempCalls[0], 2*time.Second)
}

func TestOpenAIGatewayService_Forward_WSv2Handshake502RecordsModelTransient(t *testing.T) {

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("x-request-id", "req-ws-502")
		w.WriteHeader(http.StatusBadGateway)
		_, _ = w.Write([]byte(`{"error":{"type":"server_error","message":"bad gateway"}}`))
	}))
	defer server.Close()

	cfg := newOpenAIWSV2TestConfig()
	cfg.Security.URLAllowlist.Enabled = false
	cfg.Security.URLAllowlist.AllowInsecureHTTP = true
	account := gatewayprovider.ExecutionAccount{Record: accountcore.Record{LoadLocation: time.LoadLocation, ID: 504,
		Platform:    capability.PlatformOpenAI,
		Type:        capability.AccountTypeAPIKey,
		Status:      billing.StatusActive,
		Schedulable: true,
		Concurrency: 1,
		Credentials: map[string]any{"api_key": "sk-test", "base_url": server.URL},
		Extra:       map[string]any{"responses_websockets_v2_enabled": true}},
	}
	svc := withSchedulerParametersForTest(&OpenAIGatewayService{
		cfg:            cfg,
		healthObserver: newUpstreamHealthForTest(transientCooldownAccountRepo{}, cfg, nil, accountcore.HealthOptions{}, nil),

		httpUpstream: &httpUpstreamRecorder{},
		cache:        &stubGatewayCache{},

		toolCorrector: upstreamopenai.NewCodexToolCorrector(),
	})
	body := []byte(`{"model":"gpt-5.5","stream":false,"input":"hello"}`)

	for range 2 {
		rec := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(rec)
		c.Request = httptest.NewRequest(http.MethodPost, "/openai/v1/responses", nil)
		c.Request.Header.Set("User-Agent", "unit-test-agent/1.0")
		result, err := svc.Forward(context.Background(), c, &account, body)
		require.Error(t, err)
		require.Nil(t, result)
	}

	require.True(t, svc.isOpenAIAccountModelRuntimeBlocked(&account, "gpt-5.5"))
}

func TestOpenAIGatewayService_ProxyResponsesWebSocketFromClient_ErrorEventUsageLimitPersistsRateLimit(t *testing.T) {

	cfg := newOpenAIWSV2TestConfig()
	cfg.Security.URLAllowlist.Enabled = false
	cfg.Security.URLAllowlist.AllowInsecureHTTP = true
	cfg.Gateway.OpenAIWS.MaxConnsPerAccount = 1
	cfg.Gateway.OpenAIWS.MinIdlePerAccount = 0
	cfg.Gateway.OpenAIWS.MaxIdlePerAccount = 1
	cfg.Gateway.OpenAIWS.QueueLimitPerConn = 8
	cfg.Gateway.OpenAIWS.DialTimeoutSeconds = 3
	cfg.Gateway.OpenAIWS.ReadTimeoutSeconds = 3
	cfg.Gateway.OpenAIWS.WriteTimeoutSeconds = 3

	resetAt := time.Now().Add(90 * time.Minute).Unix()
	captureConn := &openAIWSCaptureConn{
		events: [][]byte{
			[]byte(`{"type":"error","error":{"code":"rate_limit_exceeded","type":"usage_limit_reached","message":"The usage limit has been reached","resets_at":PLACEHOLDER}}`),
		},
	}
	captureConn.events[0] = []byte(strings.ReplaceAll(string(captureConn.events[0]), "PLACEHOLDER", strconv.FormatInt(resetAt, 10)))
	captureDialer := &openAIWSCaptureDialer{conn: captureConn}
	pool := newOpenAIWSConnPool(cfg)
	pool.SetClientDialerForTest(captureDialer)

	account := gatewayprovider.ExecutionAccount{Record: accountcore.Record{LoadLocation: time.LoadLocation, ID: 503,
		Name:        "openai-ingress-rate-limit",
		Platform:    capability.PlatformOpenAI,
		Type:        capability.AccountTypeAPIKey,
		Status:      billing.StatusActive,
		Schedulable: true,
		Concurrency: 1,
		Credentials: map[string]any{
			"api_key": "sk-test",
		},
		Extra: map[string]any{
			"responses_websockets_v2_enabled": true,
		}},
	}
	repo := &openAIWSRateLimitSignalRepo{stubOpenAIAccountRepo: stubOpenAIAccountRepo{accounts: []gatewayprovider.ExecutionAccount{account}}}
	rateSvc := newUpstreamHealthForTest(repo, nil, nil, accountcore.HealthOptions{ForbiddenCounter: &openAIWS403CounterCacheStub{counts: []int64{1}}}, nil)

	svc := withOpenAIExecutionCredentialsForTest(withSchedulerParametersForTest(&OpenAIGatewayService{
		accountRepo:    repo,
		healthObserver: rateSvc,
		httpUpstream:   &httpUpstreamRecorder{},
		cache:          &stubGatewayCache{},
		cfg:            cfg,

		toolCorrector: upstreamopenai.NewCodexToolCorrector(),
		openaiWSPool:  pool,
	}))

	serverErrCh := make(chan error, 1)
	wsServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := coderws.Accept(w, r, &coderws.AcceptOptions{CompressionMode: coderws.CompressionContextTakeover})
		if err != nil {
			serverErrCh <- err
			return
		}
		defer func() { _ = conn.CloseNow() }()

		rec := httptest.NewRecorder()
		ginCtx, _ := gin.CreateTestContext(rec)
		req := r.Clone(r.Context())
		req.Header = req.Header.Clone()
		req.Header.Set("User-Agent", "unit-test-agent/1.0")
		ginCtx.Request = req

		readCtx, cancel := context.WithTimeout(r.Context(), 3*time.Second)
		msgType, firstMessage, readErr := conn.Read(readCtx)
		cancel()
		if readErr != nil {
			serverErrCh <- readErr
			return
		}
		if msgType != coderws.MessageText && msgType != coderws.MessageBinary {
			serverErrCh <- io.ErrUnexpectedEOF
			return
		}

		serverErrCh <- svc.ProxyResponsesWebSocketFromClient(r.Context(), ginCtx, conn, &account, "sk-test", firstMessage, nil)
	}))
	defer wsServer.Close()

	dialCtx, cancelDial := context.WithTimeout(context.Background(), 3*time.Second)
	clientConn, _, err := coderws.Dial(dialCtx, "ws"+strings.TrimPrefix(wsServer.URL, "http"), nil)
	cancelDial()
	require.NoError(t, err)
	defer func() { _ = clientConn.CloseNow() }()

	writeCtx, cancelWrite := context.WithTimeout(context.Background(), 3*time.Second)
	err = clientConn.Write(writeCtx, coderws.MessageText, []byte(`{"type":"response.create","model":"gpt-5.1","stream":false}`))
	cancelWrite()
	require.NoError(t, err)

	select {
	case serverErr := <-serverErrCh:
		require.Error(t, serverErr)
		var failoverErr *forwardcore.UpstreamFailoverError
		require.ErrorAs(t, serverErr, &failoverErr)
		require.Equal(t, http.StatusTooManyRequests, failoverErr.StatusCode)
		require.Len(t, repo.rateLimitCalls, 1)
		require.WithinDuration(t, time.Unix(resetAt, 0), repo.rateLimitCalls[0], 2*time.Second)
	case <-time.After(5 * time.Second):
		t.Fatal("等待 ingress websocket 结束超时")
	}
}

func TestOpenAIGatewayService_ProxyResponsesWebSocketFromClient_Handshake403PersistsTempUnschedulable(t *testing.T) {

	cfg := newOpenAIWSV2TestConfig()
	cfg.Security.URLAllowlist.Enabled = false
	cfg.Security.URLAllowlist.AllowInsecureHTTP = true
	cfg.Gateway.OpenAIWS.MaxConnsPerAccount = 1
	cfg.Gateway.OpenAIWS.MinIdlePerAccount = 0
	cfg.Gateway.OpenAIWS.MaxIdlePerAccount = 1
	cfg.Gateway.OpenAIWS.QueueLimitPerConn = 8
	cfg.Gateway.OpenAIWS.DialTimeoutSeconds = 3
	cfg.Gateway.OpenAIWS.ReadTimeoutSeconds = 3
	cfg.Gateway.OpenAIWS.WriteTimeoutSeconds = 3
	pool := newOpenAIWSConnPool(cfg)
	pool.SetClientDialerForTest(&openAIWSStatusErrorDialer{
		status: http.StatusForbidden,
		err:    errors.New("temporary forbidden"),
	})

	account := gatewayprovider.ExecutionAccount{Record: accountcore.Record{LoadLocation: time.LoadLocation, ID: 505,
		Name:        "openai-ingress-forbidden-handshake",
		Platform:    capability.PlatformOpenAI,
		Type:        capability.AccountTypeOAuth,
		Status:      billing.StatusActive,
		Schedulable: true,
		Concurrency: 1,
		Credentials: map[string]any{
			"access_token": "test-access-token",
		},
		Extra: map[string]any{
			"responses_websockets_v2_enabled": true,
		}},
	}
	repo := &openAIWSRateLimitSignalRepo{stubOpenAIAccountRepo: stubOpenAIAccountRepo{accounts: []gatewayprovider.ExecutionAccount{account}}}
	rateSvc := newUpstreamHealthForTest(repo, nil, nil, accountcore.HealthOptions{ForbiddenCounter: &openAIWS403CounterCacheStub{counts: []int64{1}}}, nil)

	svc := withOpenAIExecutionCredentialsForTest(withSchedulerParametersForTest(&OpenAIGatewayService{
		accountRepo:    repo,
		healthObserver: rateSvc,
		httpUpstream:   &httpUpstreamRecorder{},
		cache:          &stubGatewayCache{},
		cfg:            cfg,

		toolCorrector: upstreamopenai.NewCodexToolCorrector(),
		openaiWSPool:  pool,
	}))

	serverErrCh := make(chan error, 1)
	wsServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := coderws.Accept(w, r, &coderws.AcceptOptions{CompressionMode: coderws.CompressionContextTakeover})
		if err != nil {
			serverErrCh <- err
			return
		}
		defer func() { _ = conn.CloseNow() }()

		rec := httptest.NewRecorder()
		ginCtx, _ := gin.CreateTestContext(rec)
		req := r.Clone(r.Context())
		req.Header = req.Header.Clone()
		req.Header.Set("User-Agent", "unit-test-agent/1.0")
		ginCtx.Request = req

		readCtx, cancel := context.WithTimeout(r.Context(), 3*time.Second)
		msgType, firstMessage, readErr := conn.Read(readCtx)
		cancel()
		if readErr != nil {
			serverErrCh <- readErr
			return
		}
		if msgType != coderws.MessageText && msgType != coderws.MessageBinary {
			serverErrCh <- io.ErrUnexpectedEOF
			return
		}

		serverErrCh <- svc.ProxyResponsesWebSocketFromClient(r.Context(), ginCtx, conn, &account, "test-access-token", firstMessage, nil)
	}))
	defer wsServer.Close()

	dialCtx, cancelDial := context.WithTimeout(context.Background(), 3*time.Second)
	clientConn, _, err := coderws.Dial(dialCtx, "ws"+strings.TrimPrefix(wsServer.URL, "http"), nil)
	cancelDial()
	require.NoError(t, err)
	defer func() { _ = clientConn.CloseNow() }()

	before := time.Now()
	writeCtx, cancelWrite := context.WithTimeout(context.Background(), 3*time.Second)
	err = clientConn.Write(writeCtx, coderws.MessageText, []byte(`{"type":"response.create","model":"gpt-5.1","stream":false}`))
	cancelWrite()
	require.NoError(t, err)

	select {
	case serverErr := <-serverErrCh:
		require.Error(t, serverErr)
		require.Len(t, repo.tempCalls, 1)
		require.WithinDuration(t, before.Add(10*time.Minute), repo.tempCalls[0], 2*time.Second)
	case <-time.After(5 * time.Second):
		t.Fatal("等待 ingress websocket 结束超时")
	}
}

func TestOpenAIGatewayService_ProxyResponsesWebSocketFromClient_ErrorEventForbiddenPersistsTempUnschedulable(t *testing.T) {

	cfg := newOpenAIWSV2TestConfig()
	cfg.Security.URLAllowlist.Enabled = false
	cfg.Security.URLAllowlist.AllowInsecureHTTP = true
	cfg.Gateway.OpenAIWS.MaxConnsPerAccount = 1
	cfg.Gateway.OpenAIWS.MinIdlePerAccount = 0
	cfg.Gateway.OpenAIWS.MaxIdlePerAccount = 1
	cfg.Gateway.OpenAIWS.QueueLimitPerConn = 8
	cfg.Gateway.OpenAIWS.DialTimeoutSeconds = 3
	cfg.Gateway.OpenAIWS.ReadTimeoutSeconds = 3
	cfg.Gateway.OpenAIWS.WriteTimeoutSeconds = 3
	pool := newOpenAIWSConnPool(cfg)
	pool.SetClientDialerForTest(&openAIWSCaptureDialer{
		conn: &openAIWSCaptureConn{
			events: [][]byte{
				[]byte(`{"type":"error","error":{"code":"forbidden","type":"permission_error","message":"temporary forbidden"}}`),
			},
		},
	})

	account := gatewayprovider.ExecutionAccount{Record: accountcore.Record{LoadLocation: time.LoadLocation, ID: 507,
		Name:        "openai-ingress-forbidden-event",
		Platform:    capability.PlatformOpenAI,
		Type:        capability.AccountTypeOAuth,
		Status:      billing.StatusActive,
		Schedulable: true,
		Concurrency: 1,
		Credentials: map[string]any{
			"access_token": "test-access-token",
		},
		Extra: map[string]any{
			"responses_websockets_v2_enabled": true,
		}},
	}
	repo := &openAIWSRateLimitSignalRepo{stubOpenAIAccountRepo: stubOpenAIAccountRepo{accounts: []gatewayprovider.ExecutionAccount{account}}}
	rateSvc := newUpstreamHealthForTest(repo, nil, nil, accountcore.HealthOptions{ForbiddenCounter: &openAIWS403CounterCacheStub{counts: []int64{1}}}, nil)

	svc := withOpenAIExecutionCredentialsForTest(withSchedulerParametersForTest(&OpenAIGatewayService{
		accountRepo:    repo,
		healthObserver: rateSvc,
		httpUpstream:   &httpUpstreamRecorder{},
		cache:          &stubGatewayCache{},
		cfg:            cfg,

		toolCorrector: upstreamopenai.NewCodexToolCorrector(),
		openaiWSPool:  pool,
	}))

	serverErrCh := make(chan error, 1)
	wsServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := coderws.Accept(w, r, &coderws.AcceptOptions{CompressionMode: coderws.CompressionContextTakeover})
		if err != nil {
			serverErrCh <- err
			return
		}
		defer func() { _ = conn.CloseNow() }()

		rec := httptest.NewRecorder()
		ginCtx, _ := gin.CreateTestContext(rec)
		req := r.Clone(r.Context())
		req.Header = req.Header.Clone()
		req.Header.Set("User-Agent", "unit-test-agent/1.0")
		ginCtx.Request = req

		readCtx, cancel := context.WithTimeout(r.Context(), 3*time.Second)
		msgType, firstMessage, readErr := conn.Read(readCtx)
		cancel()
		if readErr != nil {
			serverErrCh <- readErr
			return
		}
		if msgType != coderws.MessageText && msgType != coderws.MessageBinary {
			serverErrCh <- io.ErrUnexpectedEOF
			return
		}

		serverErrCh <- svc.ProxyResponsesWebSocketFromClient(r.Context(), ginCtx, conn, &account, "test-access-token", firstMessage, nil)
	}))
	defer wsServer.Close()

	dialCtx, cancelDial := context.WithTimeout(context.Background(), 3*time.Second)
	clientConn, _, err := coderws.Dial(dialCtx, "ws"+strings.TrimPrefix(wsServer.URL, "http"), nil)
	cancelDial()
	require.NoError(t, err)
	defer func() { _ = clientConn.CloseNow() }()

	before := time.Now()
	writeCtx, cancelWrite := context.WithTimeout(context.Background(), 3*time.Second)
	err = clientConn.Write(writeCtx, coderws.MessageText, []byte(`{"type":"response.create","model":"gpt-5.1","stream":false}`))
	cancelWrite()
	require.NoError(t, err)

	select {
	case serverErr := <-serverErrCh:
		require.Error(t, serverErr)
		require.Len(t, repo.tempCalls, 1)
		require.Len(t, repo.rateLimitCalls, 0)
		require.WithinDuration(t, before.Add(10*time.Minute), repo.tempCalls[0], 2*time.Second)
	case <-time.After(5 * time.Second):
		t.Fatal("等待 ingress websocket 结束超时")
	}
}

func TestOpenAIGatewayService_UpdateCodexUsageSnapshot_ExhaustedSnapshotDoesNotSetRateLimit(t *testing.T) {
	repo := &openAICodexSnapshotAsyncRepo{
		updateExtraCh: make(chan map[string]any, 1),
		rateLimitCh:   make(chan time.Time, 1),
	}
	svc := withOpenAIExecutionCredentialsForTest(withSchedulerParametersForTest(&OpenAIGatewayService{accountRepo: repo}))
	snapshot := &openai.OpenAICodexUsageSnapshot{
		PrimaryUsedPercent:         ptrFloat64WS(100),
		PrimaryResetAfterSeconds:   ptrIntWS(3600),
		PrimaryWindowMinutes:       ptrIntWS(10080),
		SecondaryUsedPercent:       ptrFloat64WS(12),
		SecondaryResetAfterSeconds: ptrIntWS(1200),
		SecondaryWindowMinutes:     ptrIntWS(300),
	}
	svc.updateCodexUsageSnapshot(context.Background(), 601, snapshot)

	select {
	case updates := <-repo.updateExtraCh:
		require.Equal(t, 100.0, updates["codex_7d_used_percent"])
	case <-time.After(2 * time.Second):
		t.Fatal("等待 codex 快照落库超时")
	}

	select {
	case resetAt := <-repo.rateLimitCh:
		t.Fatalf("不应因仅写入快照而生成运行时限流时间: %v", resetAt)
	case <-time.After(2 * time.Second):
	}
}

func TestOpenAIGatewayService_UpdateCodexUsageSnapshot_NonExhaustedSnapshotDoesNotSetRateLimit(t *testing.T) {
	repo := &openAICodexSnapshotAsyncRepo{
		updateExtraCh: make(chan map[string]any, 1),
		rateLimitCh:   make(chan time.Time, 1),
	}
	svc := withOpenAIExecutionCredentialsForTest(withSchedulerParametersForTest(&OpenAIGatewayService{accountRepo: repo}))
	snapshot := &openai.OpenAICodexUsageSnapshot{
		PrimaryUsedPercent:         ptrFloat64WS(94),
		PrimaryResetAfterSeconds:   ptrIntWS(3600),
		PrimaryWindowMinutes:       ptrIntWS(10080),
		SecondaryUsedPercent:       ptrFloat64WS(22),
		SecondaryResetAfterSeconds: ptrIntWS(1200),
		SecondaryWindowMinutes:     ptrIntWS(300),
	}
	svc.updateCodexUsageSnapshot(context.Background(), 602, snapshot)

	select {
	case <-repo.updateExtraCh:
	case <-time.After(2 * time.Second):
		t.Fatal("等待 codex 快照落库超时")
	}

	select {
	case resetAt := <-repo.rateLimitCh:
		t.Fatalf("不应写入运行时限流时间: %v", resetAt)
	case <-time.After(200 * time.Millisecond):
	}
}

func TestOpenAIGatewayService_UpdateCodexUsageSnapshot_ThrottlesExtraWrites(t *testing.T) {
	repo := &openAICodexSnapshotAsyncRepo{
		updateExtraCh: make(chan map[string]any, 2),
	}
	svc := withOpenAIExecutionCredentialsForTest(withSchedulerParametersForTest(&OpenAIGatewayService{
		accountRepo:           repo,
		codexSnapshotThrottle: newAccountWriteThrottle(time.Hour),
	}))
	snapshot := &openai.OpenAICodexUsageSnapshot{
		PrimaryUsedPercent:         ptrFloat64WS(94),
		PrimaryResetAfterSeconds:   ptrIntWS(3600),
		PrimaryWindowMinutes:       ptrIntWS(10080),
		SecondaryUsedPercent:       ptrFloat64WS(22),
		SecondaryResetAfterSeconds: ptrIntWS(1200),
		SecondaryWindowMinutes:     ptrIntWS(300),
	}

	svc.updateCodexUsageSnapshot(context.Background(), 777, snapshot)
	svc.updateCodexUsageSnapshot(context.Background(), 777, snapshot)

	select {
	case <-repo.updateExtraCh:
	case <-time.After(2 * time.Second):
		t.Fatal("等待第一次 codex 快照落库超时")
	}

	select {
	case updates := <-repo.updateExtraCh:
		t.Fatalf("unexpected second codex snapshot write: %v", updates)
	case <-time.After(200 * time.Millisecond):
	}
}

func ptrFloat64WS(v float64) *float64 { return &v }
func ptrIntWS(v int) *int             { return &v }

func TestOpenAIGatewayService_GetSchedulableAccount_ExhaustedCodexExtraDoesNotSetRateLimit(t *testing.T) {
	resetAt := time.Now().Add(6 * 24 * time.Hour)
	account := gatewayprovider.ExecutionAccount{Record: accountcore.Record{LoadLocation: time.LoadLocation, ID: 701,
		Platform:    capability.PlatformOpenAI,
		Type:        capability.AccountTypeOAuth,
		Status:      billing.StatusActive,
		Schedulable: true,
		Concurrency: 1,
		Extra: map[string]any{
			"codex_7d_used_percent": 100.0,
			"codex_7d_reset_at":     resetAt.UTC().Format(time.RFC3339),
		}},
	}
	repo := &openAICodexExtraListRepo{stubOpenAIAccountRepo: stubOpenAIAccountRepo{accounts: []gatewayprovider.ExecutionAccount{account}}, rateLimitCh: make(chan time.Time, 1)}
	svc := withOpenAIExecutionCredentialsForTest(withSchedulerParametersForTest(&OpenAIGatewayService{accountRepo: repo}))

	fresh, err := svc.getSchedulableAccount(context.Background(), account.Record.ID)
	require.NoError(t, err)
	require.NotNil(t, fresh)
	require.Nil(t, fresh.Record.RateLimitResetAt)
	select {
	case persisted := <-repo.rateLimitCh:
		t.Fatalf("不应将已耗尽的 codex extra 提升为运行时限流状态: %v", persisted)
	case <-time.After(2 * time.Second):
	}
}

func TestOpenAIWSErrorHTTPStatusFromRaw_UsageLimitReachedIs429(t *testing.T) {
	require.Equal(t, http.StatusTooManyRequests, upstreamopenai.WSErrorHTTPStatusFromRaw("", "usage_limit_reached"))
	require.Equal(t, http.StatusTooManyRequests, upstreamopenai.WSErrorHTTPStatusFromRaw("rate_limit_exceeded", ""))
}

func TestOpenAIWSRateLimitFailoverError_OAuthKeepsSameAccountDeadline(t *testing.T) {
	svc := withSchedulerParametersForTest(&OpenAIGatewayService{})
	headers := http.Header{"Retry-After": []string{"30"}}
	body := []byte(`{"error":{"type":"rate_limit_error","message":"limited"}}`)

	oauthErr := svc.newOpenAIWSRateLimitFailoverError(&gatewayprovider.ExecutionAccount{Record: accountcore.Record{LoadLocation: time.LoadLocation, ID: 904,
		Platform: capability.PlatformOpenAI,
		Type:     capability.AccountTypeOAuth},
	}, headers, body, "limited")
	require.True(t, oauthErr.RetryableOnSameAccount)
	require.False(t, oauthErr.SameAccountRetryDeadline.IsZero())
	require.Positive(t, oauthErr.SameAccountRetryDelay)
	require.LessOrEqual(t, oauthErr.SameAccountRetryDelay, openAIOAuth429MaxRetryDelay)
	require.Equal(t, body, oauthErr.ResponseBody)
	require.Equal(t, "30", http.Header(oauthErr.ResponseHeaders).Get("Retry-After"))

	apiKeyErr := svc.newOpenAIWSRateLimitFailoverError(&gatewayprovider.ExecutionAccount{Record: accountcore.Record{LoadLocation: time.LoadLocation, ID: 905,
		Platform: capability.PlatformOpenAI,
		Type:     capability.AccountTypeAPIKey},
	}, headers, body, "limited")
	require.False(t, apiKeyErr.RetryableOnSameAccount)
	require.True(t, apiKeyErr.SameAccountRetryDeadline.IsZero())
	require.Zero(t, apiKeyErr.SameAccountRetryDelay)
}
