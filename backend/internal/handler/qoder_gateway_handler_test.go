package handler

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/TokenFlux/TokenRouter/internal/model"
	"github.com/TokenFlux/TokenRouter/internal/pkg/qoder"
	"github.com/TokenFlux/TokenRouter/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestQoderGatewayErrorDetailsUsesAPIErrorMessage(t *testing.T) {
	err := fmt.Errorf("forward qoder: %w", &qoder.APIError{
		StatusCode: http.StatusForbidden,
		Code:       "101",
		Message:    "Signature invalid",
	})

	status, errType, message, ok := qoderGatewayErrorDetails(err)
	require.True(t, ok)
	require.Equal(t, http.StatusUnauthorized, status)
	require.Equal(t, "upstream_error", errType)
	require.Equal(t, "Qoder upstream error 101: Signature invalid", message)
}

func TestQoderGatewayErrorDetailsKeepsEntitlementDeniedAsForbidden(t *testing.T) {
	status, errType, message, ok := qoderGatewayErrorDetails(&qoder.APIError{
		StatusCode: http.StatusForbidden,
		Code:       "112",
		Message:    "model not available for this account",
	})

	require.True(t, ok)
	require.Equal(t, http.StatusForbidden, status)
	require.Equal(t, "upstream_error", errType)
	require.Equal(t, "Qoder upstream error 112: model not available for this account", message)
}

func TestQoderGatewayErrorDetailsMapsRateLimit(t *testing.T) {
	status, errType, message, ok := qoderGatewayErrorDetails(&qoder.APIError{
		StatusCode: http.StatusTooManyRequests,
		Message:    "Too many requests",
	})

	require.True(t, ok)
	require.Equal(t, http.StatusTooManyRequests, status)
	require.Equal(t, "rate_limit_error", errType)
	require.Equal(t, "Too many requests", message)
}

func TestQoderGatewayErrorDetailsMapsAgentLimitToRateLimit(t *testing.T) {
	status, errType, message, ok := qoderGatewayErrorDetails(&qoder.APIError{
		StatusCode:          http.StatusBadGateway,
		Code:                "115",
		Message:             `{"agentLimitResetTime":1783841289162}`,
		AgentLimitResetTime: 1783841289162,
	})

	require.True(t, ok)
	require.Equal(t, http.StatusTooManyRequests, status)
	require.Equal(t, "rate_limit_error", errType)
	require.Equal(t, "Qoder agent limit reached; resets at 2026-07-12 15:28:09 Asia/Shanghai", message)
}

func TestQoderGatewayErrorDetailsAppliesPassthroughRule(t *testing.T) {
	customMessage := "Use another Qoder account"
	responseCode := http.StatusTeapot
	svc := service.NewErrorPassthroughService(&qoderErrorPassthroughRepoStub{
		rules: []*model.ErrorPassthroughRule{
			{
				Name:            "qoder custom",
				Enabled:         true,
				Priority:        1,
				ErrorCodes:      []int{http.StatusUnprocessableEntity},
				MatchMode:       model.MatchModeAny,
				Platforms:       []string{service.PlatformQoder},
				PassthroughCode: false,
				ResponseCode:    &responseCode,
				PassthroughBody: false,
				CustomMessage:   &customMessage,
				SkipMonitoring:  true,
			},
		},
	}, nil)
	h := &QoderGatewayHandler{errorPassthroughService: svc}

	gin.SetMode(gin.TestMode)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	status, errType, message, ok := h.qoderGatewayErrorDetails(c, &qoder.APIError{
		StatusCode: http.StatusUnprocessableEntity,
		Body:       `{"message":"original upstream message"}`,
		Message:    "original upstream message",
	})

	require.True(t, ok)
	require.Equal(t, responseCode, status)
	require.Equal(t, "upstream_error", errType)
	require.Equal(t, customMessage, message)
	skip, exists := c.Get(service.OpsSkipPassthroughKey)
	require.True(t, exists)
	require.Equal(t, true, skip)
}

func TestQoderGatewaySessionHashUsesPreviousResponseID(t *testing.T) {
	gin.SetMode(gin.TestMode)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", nil)

	h := &QoderGatewayHandler{}
	body := []byte(`{"model":"deepseek-v4-pro","previous_response_id":"resp_qoder_123","input":"next"}`)

	require.Equal(t, qoderStickySessionHashFromSeed("resp_qoder_123"), h.qoderSessionHash(c, qoderEndpointResponses, body, 42))
}

func TestQoderGatewaySessionHashHeaderPrecedence(t *testing.T) {
	gin.SetMode(gin.TestMode)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/messages", nil)
	c.Request.Header.Set("session_id", "header-session")
	body := []byte(`{"model":"deepseek-v4-pro","prompt_cache_key":"body-session","messages":[{"role":"user","content":"hi"}]}`)

	h := &QoderGatewayHandler{}
	require.Equal(t, qoderStickySessionHashFromSeed("header-session"), h.qoderSessionHash(c, qoderEndpointMessages, body, 42))
}

func TestQoderBindStickySessionsUsesDetachedContextAfterCancel(t *testing.T) {
	parent, cancel := context.WithCancel(context.Background())
	cancel()

	cache := &qoderStickyBindCacheStub{}
	h := &QoderGatewayHandler{gatewayService: newQoderGatewayServiceWithCache(cache)}
	groupID := int64(12)

	h.bindQoderStickySessions(parent, &groupID, "request-session-hash", 99, qoderEndpointResponses, &service.ForwardResult{
		RequestID: "resp_qoder_bind",
	}, nil)

	require.False(t, cache.sawCanceledContext)
	require.True(t, cache.sawDeadline)
	require.Len(t, cache.calls, 2)
	require.Equal(t, qoderStickyBindCall{groupID: 12, sessionHash: "request-session-hash", accountID: 99}, cache.calls[0])
	require.Equal(t, qoderStickyBindCall{groupID: 12, sessionHash: qoderStickySessionHashFromSeed("resp_qoder_bind"), accountID: 99}, cache.calls[1])
}

func TestQoderGatewayShouldRefreshAccountOnlyForUnwrittenAuthErrors(t *testing.T) {
	handler := &QoderGatewayHandler{
		qoderGatewayService: &service.QoderGatewayService{},
	}

	require.True(t, handler.shouldRefreshQoderAccount(&qoder.APIError{StatusCode: http.StatusUnauthorized}, false))
	require.True(t, handler.shouldRefreshQoderAccount(&qoder.APIError{StatusCode: http.StatusForbidden}, false))
	require.False(t, handler.shouldRefreshQoderAccount(&qoder.APIError{StatusCode: http.StatusForbidden, Code: "115"}, false))
	require.False(t, handler.shouldRefreshQoderAccount(&qoder.APIError{StatusCode: http.StatusForbidden, Code: "112"}, false))
	require.False(t, handler.shouldRefreshQoderAccount(&qoder.APIError{StatusCode: http.StatusTooManyRequests}, false))
	require.False(t, handler.shouldRefreshQoderAccount(&qoder.APIError{StatusCode: http.StatusUnauthorized}, true))
	require.False(t, (&QoderGatewayHandler{}).shouldRefreshQoderAccount(&qoder.APIError{StatusCode: http.StatusUnauthorized}, false))
}

func TestQoderGatewayShouldFailoverRetryableUpstreamErrors(t *testing.T) {
	require.True(t, qoderShouldFailover(&qoder.APIError{StatusCode: http.StatusTooManyRequests}))
	require.True(t, qoderShouldFailover(&qoder.APIError{StatusCode: http.StatusBadGateway, Code: "115"}))
	require.True(t, qoderShouldFailover(&qoder.APIError{StatusCode: http.StatusForbidden, Code: "115"}))
	require.True(t, qoderShouldFailover(&qoder.APIError{StatusCode: http.StatusForbidden, Code: "112"}))
	require.True(t, qoderShouldFailover(&qoder.APIError{StatusCode: http.StatusInternalServerError}))
	require.False(t, qoderShouldFailover(&qoder.APIError{StatusCode: http.StatusUnauthorized}))
	require.False(t, qoderShouldFailover(fmt.Errorf("plain error")))
}

func TestQoderGatewayRefreshInProgressMarksAccountForFailoverUntilBudgetExhausted(t *testing.T) {
	fs := NewFailoverState(3, false)

	require.True(t, qoderMarkRefreshInProgressAccountFailed(fs, 101, 3))
	require.Contains(t, fs.FailedAccountIDs, int64(101))
	require.True(t, qoderMarkRefreshInProgressAccountFailed(fs, 102, 3))
	require.Contains(t, fs.FailedAccountIDs, int64(102))
	require.False(t, qoderMarkRefreshInProgressAccountFailed(fs, 103, 3))
	require.Contains(t, fs.FailedAccountIDs, int64(103))
	require.False(t, qoderMarkRefreshInProgressAccountFailed(nil, 104, 3))
}

func TestQoderGatewayFailoverExhaustedUsesLastQoderError(t *testing.T) {
	gin.SetMode(gin.TestMode)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodPost, "/qoder/v1/chat/completions", nil)

	h := &QoderGatewayHandler{}
	wrote := h.writeQoderFailoverExhaustedError(c, qoderEndpointChatCompletions, false, &qoder.APIError{
		StatusCode: http.StatusTooManyRequests,
		Message:    "agent busy",
	})

	require.True(t, wrote)
	require.Equal(t, http.StatusTooManyRequests, w.Code)
	require.Contains(t, w.Body.String(), `"type":"rate_limit_error"`)
	require.Contains(t, w.Body.String(), `"message":"agent busy"`)
}

func TestQoderGatewayStreamingAwareError_ResponsesStreamingEmitsResponseFailed(t *testing.T) {
	gin.SetMode(gin.TestMode)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", nil)
	setOpsRequestContext(c, "deepseek-v4-pro", true)

	h := &QoderGatewayHandler{}
	h.streamingAwareError(c, http.StatusBadGateway, "upstream_error", "Upstream request failed", true, qoderEndpointResponses)

	body := w.Body.String()
	assert.Contains(t, body, "event: response.failed\n")
	assert.NotContains(t, body, `"type":"error"`)
	resp, errObj := parseResponsesFailedSSE(t, body)
	assert.Equal(t, "failed", resp["status"])
	assert.Equal(t, "deepseek-v4-pro", resp["model"])
	assert.Equal(t, "upstream_error", errObj["code"])
	assert.Equal(t, "Upstream request failed", errObj["message"])
}

func TestQoderGatewayStreamingAwareError_MessagesKeepsGenericSSEError(t *testing.T) {
	gin.SetMode(gin.TestMode)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/messages", nil)
	setOpsRequestContext(c, "claude-opus-4-6", true)

	h := &QoderGatewayHandler{}
	h.streamingAwareError(c, http.StatusBadGateway, "upstream_error", "Upstream request failed", true, qoderEndpointMessages)

	body := w.Body.String()
	assert.NotContains(t, body, "event: response.failed")
	assert.Contains(t, body, `"type":"error"`)
	assert.Contains(t, body, `"message":"Upstream request failed"`)
}

func TestQoderGatewayStreamingAwareError_ChatCompletionsStreamingEmitsOpenAIErrorAndDone(t *testing.T) {
	gin.SetMode(gin.TestMode)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil)
	setOpsRequestContext(c, "qwen3.7-plus", true)

	h := &QoderGatewayHandler{}
	h.streamingAwareError(c, http.StatusBadGateway, "upstream_error", "Upstream request failed", true, qoderEndpointChatCompletions)

	body := w.Body.String()
	assert.NotContains(t, body, "event: response.failed")
	assert.NotContains(t, body, `"type":"error"`)
	assert.Contains(t, body, `data: {"error":{"type":"upstream_error","message":"Upstream request failed"}}`)
	assert.Contains(t, body, "data: [DONE]\n\n")
}

func TestQoderGatewayStreamingAwareError_NonStreamingAfterKeepaliveKeepsJSON(t *testing.T) {
	gin.SetMode(gin.TestMode)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil)
	setOpsRequestContext(c, "qwen3.7-plus", false)
	c.Writer.Header().Set("Content-Type", "application/json; charset=utf-8")
	_, err := c.Writer.WriteString("\n")
	require.NoError(t, err)

	h := &QoderGatewayHandler{}
	h.streamingAwareError(c, http.StatusBadGateway, "upstream_error", "Upstream request failed", true, qoderEndpointChatCompletions)

	body := w.Body.Bytes()
	require.True(t, json.Valid(body), "body should remain parseable JSON after keepalive whitespace: %q", string(body))
	assert.NotContains(t, string(body), "data:")
	assert.Contains(t, string(body), `"type":"upstream_error"`)
	assert.Contains(t, string(body), `"message":"Upstream request failed"`)
}

func TestQoderGatewaySubmitUsageRecordIgnoresRequestCancellation(t *testing.T) {
	gin.SetMode(gin.TestMode)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	reqCtx, cancel := context.WithCancel(context.Background())
	cancel()
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/messages", nil).WithContext(reqCtx)

	done := make(chan error, 1)
	h := &QoderGatewayHandler{}
	h.submitUsageRecordTask(c, func(ctx context.Context) {
		select {
		case <-ctx.Done():
			done <- ctx.Err()
		default:
			done <- nil
		}
	})

	select {
	case err := <-done:
		require.NoError(t, err)
	case <-time.After(time.Second):
		t.Fatal("usage task did not run")
	}
}

func TestQoderGatewayRequestCanceledDetectsErrorOrContext(t *testing.T) {
	require.True(t, qoderRequestCanceled(context.Background(), context.Canceled))

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	require.True(t, qoderRequestCanceled(ctx, fmt.Errorf("wrapped upstream error")))

	require.False(t, qoderRequestCanceled(context.Background(), fmt.Errorf("upstream error")))
}

func TestQoderStreamReleaseDoesNotFireOnClientCancel(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	released := make(chan struct{}, 1)

	release := wrapQoderReleaseOnDone(ctx, func() {
		released <- struct{}{}
	}, true)

	cancel()
	select {
	case <-released:
		t.Fatal("stream release should wait for explicit completion")
	case <-time.After(20 * time.Millisecond):
	}

	release()
	select {
	case <-released:
	case <-time.After(time.Second):
		t.Fatal("explicit release did not run")
	}
	release()
	select {
	case <-released:
		t.Fatal("release should run at most once")
	case <-time.After(20 * time.Millisecond):
	}
}

func TestQoderNonStreamReleaseStillFiresOnClientCancel(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	released := make(chan struct{}, 1)

	release := wrapQoderReleaseOnDone(ctx, func() {
		released <- struct{}{}
	}, false)
	defer release()

	cancel()
	select {
	case <-released:
	case <-time.After(time.Second):
		t.Fatal("non-stream release should run on client cancel")
	}
}

func TestQoderGatewayAccountSlotWaitQueueFullReturnsRateLimitBeforePollingSlot(t *testing.T) {
	gin.SetMode(gin.TestMode)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodPost, "/qoder/v1/chat/completions", nil)

	cache := &qoderAccountWaitCacheStub{
		helperConcurrencyCacheStub: &helperConcurrencyCacheStub{},
		accountWaitAllowed:         false,
	}
	h := &QoderGatewayHandler{
		concurrencyHelper: NewConcurrencyHelper(service.NewConcurrencyService(cache), SSEPingFormatNone, 0),
	}
	streamStarted := false

	release, err := h.acquireQoderAccountSlotWithWait(c, &service.Account{ID: 77, Concurrency: 1}, &service.AccountWaitPlan{
		AccountID:      77,
		MaxConcurrency: 1,
		Timeout:        time.Millisecond,
		MaxWaiting:     2,
	}, false, &streamStarted, nil)

	require.Nil(t, release)
	var waitErr *WaitQueueFullError
	require.ErrorAs(t, err, &waitErr)
	require.Equal(t, "account", waitErr.SlotType)
	require.Equal(t, 1, cache.accountWaitIncrementCalls)
	require.Equal(t, 2, cache.accountWaitMaxWaiting)
	require.Equal(t, 0, cache.accountAcquireCalls, "full wait queue should reject before polling account slots")
	require.Equal(t, 0, cache.accountWaitDecrementCalls)
}

func TestQoderGatewayAccountSlotWaitCountDecrementsWhenWaitTimesOut(t *testing.T) {
	gin.SetMode(gin.TestMode)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodPost, "/qoder/v1/chat/completions", nil)

	cache := &qoderAccountWaitCacheStub{
		helperConcurrencyCacheStub: &helperConcurrencyCacheStub{accountSeq: []bool{false}},
		accountWaitAllowed:         true,
	}
	h := &QoderGatewayHandler{
		concurrencyHelper: NewConcurrencyHelper(service.NewConcurrencyService(cache), SSEPingFormatNone, 0),
	}
	streamStarted := false

	release, err := h.acquireQoderAccountSlotWithWait(c, &service.Account{ID: 78, Concurrency: 1}, &service.AccountWaitPlan{
		AccountID:      78,
		MaxConcurrency: 1,
		Timeout:        time.Millisecond,
		MaxWaiting:     3,
	}, false, &streamStarted, nil)

	require.Nil(t, release)
	var concurrencyErr *ConcurrencyError
	require.ErrorAs(t, err, &concurrencyErr)
	require.Equal(t, "account", concurrencyErr.SlotType)
	require.Equal(t, 1, cache.accountWaitIncrementCalls)
	require.Equal(t, 3, cache.accountWaitMaxWaiting)
	require.Equal(t, 1, cache.accountAcquireCalls)
	require.Equal(t, 1, cache.accountWaitDecrementCalls)
}

func TestQoderGatewayAccountSlotWaitCountDecrementsAfterAcquire(t *testing.T) {
	gin.SetMode(gin.TestMode)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodPost, "/qoder/v1/chat/completions", nil)

	cache := &qoderAccountWaitCacheStub{
		helperConcurrencyCacheStub: &helperConcurrencyCacheStub{accountSeq: []bool{true}},
		accountWaitAllowed:         true,
	}
	h := &QoderGatewayHandler{
		concurrencyHelper: NewConcurrencyHelper(service.NewConcurrencyService(cache), SSEPingFormatNone, 0),
	}
	streamStarted := false

	release, err := h.acquireQoderAccountSlotWithWait(c, &service.Account{ID: 79, Concurrency: 1}, &service.AccountWaitPlan{
		AccountID:      79,
		MaxConcurrency: 1,
		Timeout:        time.Second,
		MaxWaiting:     4,
	}, false, &streamStarted, nil)

	require.NoError(t, err)
	require.NotNil(t, release)
	require.Equal(t, 1, cache.accountWaitIncrementCalls)
	require.Equal(t, 4, cache.accountWaitMaxWaiting)
	require.Equal(t, 1, cache.accountAcquireCalls)
	require.Equal(t, 1, cache.accountWaitDecrementCalls)
	release()
	require.Equal(t, 1, cache.accountReleaseCalls)
}

type qoderAccountWaitCacheStub struct {
	*helperConcurrencyCacheStub

	accountWaitAllowed        bool
	accountWaitIncrementCalls int
	accountWaitDecrementCalls int
	accountWaitMaxWaiting     int
}

func (s *qoderAccountWaitCacheStub) IncrementAccountWaitCount(ctx context.Context, accountID int64, maxWait int) (bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.accountWaitIncrementCalls++
	s.accountWaitMaxWaiting = maxWait
	return s.accountWaitAllowed, nil
}

func (s *qoderAccountWaitCacheStub) DecrementAccountWaitCount(ctx context.Context, accountID int64) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.accountWaitDecrementCalls++
	return nil
}

type qoderStickyBindCall struct {
	groupID     int64
	sessionHash string
	accountID   int64
}

type qoderStickyBindCacheStub struct {
	calls              []qoderStickyBindCall
	sawCanceledContext bool
	sawDeadline        bool
}

func (s *qoderStickyBindCacheStub) GetSessionAccountID(context.Context, int64, string) (int64, error) {
	return 0, nil
}

func (s *qoderStickyBindCacheStub) SetSessionAccountID(ctx context.Context, groupID int64, sessionHash string, accountID int64, _ time.Duration) error {
	if ctx.Err() != nil {
		s.sawCanceledContext = true
		return ctx.Err()
	}
	if _, ok := ctx.Deadline(); ok {
		s.sawDeadline = true
	}
	s.calls = append(s.calls, qoderStickyBindCall{groupID: groupID, sessionHash: sessionHash, accountID: accountID})
	return nil
}

func (s *qoderStickyBindCacheStub) RefreshSessionTTL(context.Context, int64, string, time.Duration) error {
	return nil
}

func (s *qoderStickyBindCacheStub) DeleteSessionAccountID(context.Context, int64, string) error {
	return nil
}

func (s *qoderStickyBindCacheStub) SetSessionOwnerGroupID(context.Context, int64, string, string, int64, time.Duration) (bool, error) {
	return true, nil
}

func (s *qoderStickyBindCacheStub) GetSessionOwnerGroupID(context.Context, int64, string, string) (int64, error) {
	return 0, nil
}

func (s *qoderStickyBindCacheStub) RefreshSessionOwnerTTL(context.Context, int64, string, string, time.Duration) error {
	return nil
}

func newQoderGatewayServiceWithCache(cache service.GatewayCache) *service.GatewayService {
	return service.NewGatewayService(
		nil, nil, nil, nil, nil, nil, nil,
		cache,
		nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil,
	)
}

type qoderErrorPassthroughRepoStub struct {
	rules []*model.ErrorPassthroughRule
}

func (r *qoderErrorPassthroughRepoStub) List(context.Context) ([]*model.ErrorPassthroughRule, error) {
	return r.rules, nil
}

func (r *qoderErrorPassthroughRepoStub) GetByID(context.Context, int64) (*model.ErrorPassthroughRule, error) {
	return nil, nil
}

func (r *qoderErrorPassthroughRepoStub) Create(context.Context, *model.ErrorPassthroughRule) (*model.ErrorPassthroughRule, error) {
	return nil, nil
}

func (r *qoderErrorPassthroughRepoStub) Update(context.Context, *model.ErrorPassthroughRule) (*model.ErrorPassthroughRule, error) {
	return nil, nil
}

func (r *qoderErrorPassthroughRepoStub) Delete(context.Context, int64) error {
	return nil
}
