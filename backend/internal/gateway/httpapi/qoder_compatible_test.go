package httpapi

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/TokenFlux/TokenRouter/internal/apikey"
	forwardcore "github.com/TokenFlux/TokenRouter/internal/gateway/forward"
	gatewayprovider "github.com/TokenFlux/TokenRouter/internal/gateway/provider"
	"github.com/TokenFlux/TokenRouter/internal/gateway/session"
	"github.com/TokenFlux/TokenRouter/internal/identity"
	"github.com/TokenFlux/TokenRouter/internal/identity/httpapi/authctx"
	"github.com/TokenFlux/TokenRouter/internal/infra/telemetry/logging"
	"github.com/TokenFlux/TokenRouter/internal/protocol"
	providercore "github.com/TokenFlux/TokenRouter/internal/provider"
	"github.com/TokenFlux/TokenRouter/internal/routing"
	"github.com/TokenFlux/TokenRouter/internal/scheduler"
	"github.com/TokenFlux/TokenRouter/internal/upstream/qoder"
)

// Qoder compatibility must record served partial results once while reserving
// sticky binding for success. Messages and Responses fixtures protect that
// completion boundary alongside explicit-session precedence, detached sticky
// writes and protocol-specific failure output after keepalive bytes.
//
// The slot cache fixtures expose queue registration and acquisition separately
// so a full queue can reject before polling and a counted waiter can be
// released after timeout or success. Their scripted slot sequences and
// counters use one mutex; the wait hook is copied under that lock and invoked
// after unlocking. Stream cleanup is observed through buffered channels,
// separating client cancellation from explicit completion without blocking the
// release callback.

// qoderProviderWaitCacheStub adds recorded provider-queue admission to the
// shared slot fixture.
type qoderProviderWaitCacheStub struct {
	*helperConcurrencyCacheStub // shared synchronized slot fixture

	// providerWaitAllowed and the provider queue counters use the embedded mutex
	// during runtime calls. Tests configure admission before calling the resource
	// helper.
	providerWaitAllowed        bool // scripted provider queue permission
	providerWaitIncrementCalls int  // recorded provider queue registrations
	providerWaitDecrementCalls int  // recorded provider queue releases
	providerWaitMaxWaiting     int  // last provider queue allowance
}

// qoderStickyBindCall records one group-scoped session association with its
// selected provider.
type qoderStickyBindCall struct {
	groupID     int64  // bound group identifier
	sessionHash string // bound sticky session hash
	providerID  int64  // bound provider identifier
}

// qoderStickyBindCacheStub records serial sticky writes and their context
// observations.
type qoderStickyBindCacheStub struct {
	// calls, sawCanceledContext and sawDeadline are updated synchronously by
	// sticky binding and inspected after it returns. This fixture does not add
	// synchronization for concurrent cache callers.
	calls              []qoderStickyBindCall // recorded ordered sticky associations
	sawCanceledContext bool                  // observed canceled sticky write
	sawDeadline        bool                  // observed bounded sticky write
}

// qoderStickyExecutionStub routes only sticky binding into its configured
// cache.
type qoderStickyExecutionStub struct {
	QoderCompatibleExecution                      // unused execution methods from embedding
	cache                    session.GatewayCache // configured sticky binding store
}

// helperConcurrencyCacheStub provides scripted slot admission and synchronized
// call counters.
type helperConcurrencyCacheStub struct {
	// mu protects providerSeq, userSeq, slot/queue/API-key counters and
	// apiKeyTrackIDs during mutations. IncrementWaitCount also captures
	// waitAllowed and waitIncrementHook under mu, then runs the hook unlocked.
	mu sync.Mutex // slot fixture state mutex

	providerSeq []bool // remaining provider acquisition results
	userSeq     []bool // remaining user acquisition results

	providerAcquireCalls int     // recorded provider acquisition count
	userAcquireCalls     int     // recorded user acquisition count
	providerReleaseCalls int     // recorded provider cleanup count
	userReleaseCalls     int     // recorded user cleanup count
	waitAllowed          bool    // scripted user queue permission
	waitIncrementCalls   int     // recorded user queue registrations
	waitDecrementCalls   int     // recorded user queue releases
	waitMaxWait          int     // last user queue allowance
	waitIncrementHook    func()  // optional after-unlock wait callback
	apiKeyTrackCalls     int     // recorded API-key tracking count
	apiKeyReleaseCalls   int     // recorded API-key cleanup count
	apiKeyTrackIDs       []int64 // ordered tracked API-key identifiers
}

// localRejectionFixture records feedback from the real compatibility bridge.
// The embedded contract supplies the unused selection methods for this path.
type localRejectionFixture struct {
	qoderRuntimeContract
	reports []bool // observed scheduling results
}

// TestQoderCompatibleNativeHTTPCompletionBoundary runs successful and partial
// native-compatible requests through Messages and Responses. Each path must
// forward and record once, with release before feedback and sticky bindings
// only after success.
func TestQoderCompatibleNativeHTTPCompletionBoundary(t *testing.T) {
	for _, endpoint := range []QoderEndpoint{QoderMessages, QoderResponses} {
		for _, partial := range []bool{false, true} {
			label := string(endpoint) + "/success"
			if partial {
				label = string(endpoint) + "/partial"
			}
			t.Run(label, func(t *testing.T) {
				wire := protocol.ProtocolAnthropicMessages
				if endpoint == QoderResponses {
					wire = protocol.ProtocolOpenAIResponses
				}
				fixture := &qoderRuntimeContract{t: t, wire: wire, partial: partial}
				group := int64(4)
				key := &apikey.APIKey{ID: 2, GroupID: &group, Group: &routing.Group{}}
				options := QoderCompatibleOptions{Execution: fixture, Funding: fixture, Recorder: fixture, PlatformAvailable: true, ReadAccess: func(*gin.Context) (*apikey.APIKey, bool) { return key, true }, MayRefresh: func(error) bool { return false }, MaySwitch: func(error) bool { return false }, Errors: QoderErrorPresenter{Describe: func(error) forwardcore.QoderErrorView { return forwardcore.QoderErrorView{} }}}
				h := NewQoderCompatibleHandler(NewQoderCompatibleRuntime(options), nil, 3)
				response := httptest.NewRecorder()
				c, _ := gin.CreateTestContext(response)
				c.Request = httptest.NewRequest(http.MethodPost, "/v1/"+string(endpoint), strings.NewReader(`{"model":"client-model","messages":[{"role":"user","content":"hello"}]}`))
				authctx.SetPrincipal(c, identity.Principal{UserID: 3}, 1, "")
				if endpoint == QoderMessages {
					h.Messages(c)
				} else {
					h.Responses(c)
				}
				require.Equal(t, []string{"funding", "select", "forward", "release", "report"}, fixture.events)
				require.Equal(t, 1, fixture.captures)
				require.Equal(t, 1, fixture.records)
				require.Zero(t, fixture.refreshes)
				if partial {
					require.Zero(t, fixture.binds)
					require.Contains(t, response.Body.String(), "partial")
					require.Contains(t, response.Body.String(), "Upstream request failed")
				} else {
					require.Equal(t, 1, fixture.binds)
				}
			})
		}
	}
}

// TestQoderGatewaySessionHashHeaderPrecedence supplies both a session header
// and body prompt-cache key. The resulting sticky hash must use the header
// identity rather than the competing body seed.
func TestQoderGatewaySessionHashHeaderPrecedence(t *testing.T) {
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/messages", nil)
	c.Request.Header.Set("session_id", "header-session")
	body := []byte(`{"model":"deepseek-v4-pro","prompt_cache_key":"body-session","messages":[{"role":"user","content":"hi"}]}`)

	h := &QoderCompatibleRuntime{}
	require.Equal(t, qoderStickySessionHashFromSeed("header-session"), h.qoderSessionHash(c, QoderMessages, body, 42))
}

// TestQoderBindStickySessionsUsesDetachedContextAfterCancel publishes one request
// affinity after client cancellation. A blank request hash contributes no substitute
// response-ID key, and the write retains its detached time budget.
func TestQoderBindStickySessionsUsesDetachedContextAfterCancel(t *testing.T) {
	parent, cancel := context.WithCancel(context.Background())
	cancel()

	cache := &qoderStickyBindCacheStub{}
	h := &QoderCompatibleRuntime{options: QoderCompatibleOptions{Execution: &qoderStickyExecutionStub{cache: cache}}}
	groupID := int64(12)

	h.bindQoderStickySession(parent, &groupID, "request-session-hash", 99, nil)
	h.bindQoderStickySession(parent, &groupID, "", 99, nil)

	require.False(t, cache.sawCanceledContext)
	require.True(t, cache.sawDeadline)
	require.Len(t, cache.calls, 1)
	require.Equal(t, qoderStickyBindCall{groupID: 12, sessionHash: "request-session-hash", providerID: 99}, cache.calls[0])
}

// TestQoderGatewayShouldRefreshProviderOnlyForUnwrittenAuthErrors covers
// refreshable authentication errors, agent-limit codes, rate limits, started
// output and missing platform setup. Only permitted authentication failures
// before output must authorize refresh.
func TestQoderGatewayShouldRefreshProviderOnlyForUnwrittenAuthErrors(t *testing.T) {
	handler := &QoderCompatibleRuntime{
		options: QoderCompatibleOptions{PlatformAvailable: true, MayRefresh: qoder.MayRefreshAttempt},
	}

	require.True(t, handler.shouldRefreshQoderProvider(&qoder.APIError{StatusCode: http.StatusUnauthorized}, false))
	require.True(t, handler.shouldRefreshQoderProvider(&qoder.APIError{StatusCode: http.StatusForbidden}, false))
	require.False(t, handler.shouldRefreshQoderProvider(&qoder.APIError{StatusCode: http.StatusForbidden, Code: "115"}, false))
	require.False(t, handler.shouldRefreshQoderProvider(&qoder.APIError{StatusCode: http.StatusForbidden, Code: "112"}, false))
	require.False(t, handler.shouldRefreshQoderProvider(&qoder.APIError{StatusCode: http.StatusTooManyRequests}, false))
	require.False(t, handler.shouldRefreshQoderProvider(&qoder.APIError{StatusCode: http.StatusUnauthorized}, true))
	require.False(t, (&QoderCompatibleRuntime{}).shouldRefreshQoderProvider(&qoder.APIError{StatusCode: http.StatusUnauthorized}, false))
}

// TestQoderGatewayFailoverExhaustedUsesLastQoderError supplies a classified
// final rate-limit failure after attempts end. Exhaustion must report
// presentation and retain HTTP 429, rate-limit category and upstream
// agent-busy message.
func TestQoderGatewayFailoverExhaustedUsesLastQoderError(t *testing.T) {
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodPost, "/qoder/v1/chat/completions", nil)

	h := &QoderCompatibleRuntime{options: QoderCompatibleOptions{Errors: QoderErrorPresenter{Describe: gatewayprovider.DescribeQoderError}}}
	wrote := h.writeQoderFailoverExhaustedError(c, QoderChat, false, &qoder.APIError{
		StatusCode: http.StatusTooManyRequests,
		Message:    "agent busy",
	})

	require.True(t, wrote)
	require.Equal(t, http.StatusTooManyRequests, w.Code)
	require.Contains(t, w.Body.String(), `"type":"rate_limit_error"`)
	require.Contains(t, w.Body.String(), `"message":"agent busy"`)
}

// TestQoderGatewayStreamingAwareError_MessagesKeepsGenericSSEError observes
// streaming Messages input before failure. The response must contain the
// Anthropic error type and message while omitting the Responses failure event.
func TestQoderGatewayStreamingAwareError_MessagesKeepsGenericSSEError(t *testing.T) {
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/messages", nil)
	SetOpsRequestContext(c, "claude-opus-4-6", true)

	h := &QoderCompatibleRuntime{}
	h.streamingAwareError(c, http.StatusBadGateway, "upstream_error", "Upstream request failed", true, QoderMessages)

	body := w.Body.String()
	assert.NotContains(t, body, "event: response.failed")
	assert.Contains(t, body, `"type":"error"`)
	assert.Contains(t, body, `"message":"Upstream request failed"`)
}

// TestQoderGatewayStreamingAwareError_ChatCompletionsStreamingEmitsOpenAIErrorAndDone
// observes streaming Chat input before failure. Output must use the OpenAI
// error object followed by DONE and omit Anthropic and Responses terminal
// shapes.
func TestQoderGatewayStreamingAwareError_ChatCompletionsStreamingEmitsOpenAIErrorAndDone(t *testing.T) {
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil)
	SetOpsRequestContext(c, "qwen3.7-plus", true)

	h := &QoderCompatibleRuntime{}
	h.streamingAwareError(c, http.StatusBadGateway, "upstream_error", "Upstream request failed", true, QoderChat)

	body := w.Body.String()
	assert.NotContains(t, body, "event: response.failed")
	assert.NotContains(t, body, `"type":"error"`)
	assert.Contains(t, body, `data: {"error":{"type":"upstream_error","message":"Upstream request failed"}}`)
	assert.Contains(t, body, "data: [DONE]\n\n")
}

// TestQoderGatewayStreamingAwareError_NonStreamingAfterKeepaliveKeepsJSON
// writes keepalive whitespace for a buffered Chat request before failure. The
// complete body must remain valid JSON with upstream error details and no SSE
// data fields.
func TestQoderGatewayStreamingAwareError_NonStreamingAfterKeepaliveKeepsJSON(t *testing.T) {
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil)
	SetOpsRequestContext(c, "qwen3.7-plus", false)
	c.Writer.Header().Set("Content-Type", "application/json; charset=utf-8")
	_, err := c.Writer.WriteString("\n")
	require.NoError(t, err)

	h := &QoderCompatibleRuntime{}
	h.streamingAwareError(c, http.StatusBadGateway, "upstream_error", "Upstream request failed", true, QoderChat)

	body := w.Body.Bytes()
	require.True(t, json.Valid(body), "body should remain parseable JSON after keepalive whitespace: %q", string(body))
	assert.NotContains(t, string(body), "data:")
	assert.Contains(t, string(body), `"type":"upstream_error"`)
	assert.Contains(t, string(body), `"message":"Upstream request failed"`)
}

// TestQoderGatewaySubmitUsageRecordIgnoresRequestCancellation submits usage
// work with an already canceled request and no pool. The callback must run
// within the observation budget and receive a context without that
// cancellation.
func TestQoderGatewaySubmitUsageRecordIgnoresRequestCancellation(t *testing.T) {
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	reqCtx, cancel := context.WithCancel(context.Background())
	cancel()
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/messages", nil).WithContext(reqCtx)

	// The submission can finish its callback before this receiver starts waiting.
	// Buffer the completion result so inline execution cannot block on the test.
	done := make(chan error, 1)
	h := &QoderCompatibleRuntime{}
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

// TestQoderGatewayRequestCanceledDetectsErrorOrContext checks cancellation
// from either an explicit error or the active context. An unrelated upstream
// error under a live background context must remain false.
func TestQoderGatewayRequestCanceledDetectsErrorOrContext(t *testing.T) {
	require.True(t, qoderRequestCanceled(context.Background(), context.Canceled))

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	require.True(t, qoderRequestCanceled(ctx, fmt.Errorf("wrapped upstream error")))

	require.False(t, qoderRequestCanceled(context.Background(), fmt.Errorf("upstream error")))
}

// TestQoderStreamReleaseDoesNotFireOnClientCancel cancels a streamed slot and
// observes its release channel before and after explicit completion.
// Cancellation must leave it held during the window, and repeated explicit
// cleanup must run only once.
func TestQoderStreamReleaseDoesNotFireOnClientCancel(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	// Buffer release observation so the cleanup callback cannot block on the
	// test’s later select while cancellation and explicit completion are exercised.
	released := make(chan struct{}, 1)

	release := wrapQoderReleaseOnDone(ctx, func() {
		released <- struct{}{}
	}, true)

	cancel()
	select {
	case <-released:
		t.Fatal("stream release should wait for explicit completion")
	// Observe cleanup absence within this bounded window.
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
	// Observe cleanup absence within this bounded window.
	case <-time.After(20 * time.Millisecond):
	}
}

// TestQoderNonStreamReleaseStillFiresOnClientCancel cancels a buffered request
// after wrapping its release callback. The callback must signal cleanup within
// the observation budget without waiting for explicit forwarding completion.
func TestQoderNonStreamReleaseStillFiresOnClientCancel(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	// Cancellation can run cleanup before the select begins. Buffer its signal
	// so observing that release does not block the cancellation callback.
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

// TestQoderGatewayProviderSlotWaitQueueFullReturnsRateLimitBeforePollingSlot
// scripts an explicitly full provider wait queue. The request must reject with
// provider queue failure before acquiring any slot or decrementing an
// unregistered waiter.
func TestQoderGatewayProviderSlotWaitQueueFullReturnsRateLimitBeforePollingSlot(t *testing.T) {
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodPost, "/qoder/v1/chat/completions", nil)

	cache := &qoderProviderWaitCacheStub{
		helperConcurrencyCacheStub: &helperConcurrencyCacheStub{},
		providerWaitAllowed:        false,
	}
	h := &QoderCompatibleRuntime{
		concurrencyHelper: NewConcurrencyHelper(scheduler.NewConcurrencyService(cache, scheduler.Diagnostics{
			Logf:  logging.LegacyPrintf,
			Event: logging.Event,
		},
		), SSEPingFormatNone, 0),
	}
	streamStarted := false

	release, err := h.acquireQoderProviderSlotWithWait(c, &providercore.ProviderSnapshot{ID: 77, Concurrency: 1}, &scheduler.ProviderWaitPlan{
		ProviderID:     77,
		MaxConcurrency: 1,
		Timeout:        time.Millisecond,
		MaxWaiting:     2,
	}, false, &streamStarted, nil)

	require.Nil(t, release)
	var waitErr *WaitQueueFullError
	require.ErrorAs(t, err, &waitErr)
	require.Equal(t, "provider", waitErr.SlotType)
	require.Equal(t, 1, cache.providerWaitIncrementCalls)
	require.Equal(t, 2, cache.providerWaitMaxWaiting)
	require.Equal(t, 0, cache.providerAcquireCalls, "full wait queue should reject before polling provider slots")
	require.Equal(t, 0, cache.providerWaitDecrementCalls)
}

// TestQoderGatewayProviderSlotWaitCountDecrementsWhenWaitTimesOut registers a
// provider waiter whose slot remains unavailable until timeout. The helper
// must return a provider concurrency error and release the counted queue entry
// once.
func TestQoderGatewayProviderSlotWaitCountDecrementsWhenWaitTimesOut(t *testing.T) {
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodPost, "/qoder/v1/chat/completions", nil)

	cache := &qoderProviderWaitCacheStub{
		helperConcurrencyCacheStub: &helperConcurrencyCacheStub{providerSeq: []bool{false}},
		providerWaitAllowed:        true,
	}
	h := &QoderCompatibleRuntime{
		concurrencyHelper: NewConcurrencyHelper(scheduler.NewConcurrencyService(cache, scheduler.Diagnostics{
			Logf:  logging.LegacyPrintf,
			Event: logging.Event,
		},
		), SSEPingFormatNone, 0),
	}
	streamStarted := false

	release, err := h.acquireQoderProviderSlotWithWait(c, &providercore.ProviderSnapshot{ID: 78, Concurrency: 1}, &scheduler.ProviderWaitPlan{
		ProviderID:     78,
		MaxConcurrency: 1,
		Timeout:        time.Millisecond,
		MaxWaiting:     3,
	}, false, &streamStarted, nil)

	require.Nil(t, release)
	var concurrencyErr *ConcurrencyError
	require.ErrorAs(t, err, &concurrencyErr)
	require.Equal(t, "provider", concurrencyErr.SlotType)
	require.Equal(t, 1, cache.providerWaitIncrementCalls)
	require.Equal(t, 3, cache.providerWaitMaxWaiting)
	require.Equal(t, 1, cache.providerAcquireCalls)
	require.Equal(t, 1, cache.providerWaitDecrementCalls)
}

// TestQoderGatewayProviderSlotWaitCountDecrementsAfterAcquire registers a
// provider waiter with an immediately available slot. Wait bookkeeping must
// release once at acquisition, and returned slot cleanup must separately
// record provider release.
func TestQoderGatewayProviderSlotWaitCountDecrementsAfterAcquire(t *testing.T) {
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodPost, "/qoder/v1/chat/completions", nil)

	cache := &qoderProviderWaitCacheStub{
		helperConcurrencyCacheStub: &helperConcurrencyCacheStub{providerSeq: []bool{true}},
		providerWaitAllowed:        true,
	}
	h := &QoderCompatibleRuntime{
		concurrencyHelper: NewConcurrencyHelper(scheduler.NewConcurrencyService(cache, scheduler.Diagnostics{
			Logf:  logging.LegacyPrintf,
			Event: logging.Event,
		},
		), SSEPingFormatNone, 0),
	}
	streamStarted := false

	release, err := h.acquireQoderProviderSlotWithWait(c, &providercore.ProviderSnapshot{ID: 79, Concurrency: 1}, &scheduler.ProviderWaitPlan{
		ProviderID:     79,
		MaxConcurrency: 1,
		Timeout:        time.Second,
		MaxWaiting:     4,
	}, false, &streamStarted, nil)

	require.NoError(t, err)
	require.NotNil(t, release)
	require.Equal(t, 1, cache.providerWaitIncrementCalls)
	require.Equal(t, 4, cache.providerWaitMaxWaiting)
	require.Equal(t, 1, cache.providerAcquireCalls)
	require.Equal(t, 1, cache.providerWaitDecrementCalls)
	release()
	require.Equal(t, 1, cache.providerReleaseCalls)
}

// IncrementProviderWaitCount records registration and the supplied queue
// allowance under the shared mutex. It returns the fixture’s scripted
// permission without error; tests configure that permission before concurrent
// runtime access.
func (s *qoderProviderWaitCacheStub) IncrementProviderWaitCount(ctx context.Context, providerID int64, maxWait int) (bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	// Record registration and its allowance together under the shared mutex so
	// queue assertions describe the same attempted admission decision.
	s.providerWaitIncrementCalls++
	s.providerWaitMaxWaiting = maxWait
	return s.providerWaitAllowed, nil
}

// DecrementProviderWaitCount records one provider queue cleanup under the
// shared mutex.
func (s *qoderProviderWaitCacheStub) DecrementProviderWaitCount(ctx context.Context, providerID int64) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	// Count provider wait cleanup under the acquisition fixture’s mutex so
	// synchronous assertions see complete cleanup after resource handling
	// returns.
	s.providerWaitDecrementCalls++
	return nil
}

// GetSessionProviderID always returns no cached provider and no error. This
// recording fixture supplies sticky writes only, so lookup does not reuse
// previously recorded associations for later selection.
func (s *qoderStickyBindCacheStub) GetSessionProviderID(context.Context, int64, string) (int64, error) {
	return 0, nil
}

// SetSessionProviderID records a serial association when its context is live,
// noting any deadline. A canceled context is recorded and returned as an error
// before appending; callers synchronize access externally if they add
// concurrency.
func (s *qoderStickyBindCacheStub) SetSessionProviderID(ctx context.Context, groupID int64, sessionHash string, providerID int64, _ time.Duration) error {
	if ctx.Err() != nil {
		s.sawCanceledContext = true
		return ctx.Err()
	}
	if _, ok := ctx.Deadline(); ok {
		s.sawDeadline = true
	}
	s.calls = append(s.calls, qoderStickyBindCall{groupID: groupID, sessionHash: sessionHash, providerID: providerID})
	return nil
}

// RefreshSessionTTL accepts fixture refresh requests without persisting TTL
// state. It returns nil so sticky-write tests can focus on association and
// detached-context observations rather than expiration behavior.
func (s *qoderStickyBindCacheStub) RefreshSessionTTL(context.Context, int64, string, time.Duration) error {
	return nil
}

// DeleteSessionProviderID accepts deletion without modifying the fixture’s
// recorded call history. It returns nil because these tests inspect write
// attempts rather than modeling persistent cache removal or expiration.
func (s *qoderStickyBindCacheStub) DeleteSessionProviderID(context.Context, int64, string) error {
	return nil
}

// SetSessionOwnerGroupID always permits fixture session ownership and returns
// no error. Ownership policy is outside these sticky-association tests, so it
// does not add or consult an owner record.
func (s *qoderStickyBindCacheStub) SetSessionOwnerGroupID(context.Context, int64, string, string, int64, time.Duration) (bool, error) {
	return true, nil
}

// GetSessionOwnerGroupID returns zero ownership and no error for every fixture
// lookup. The recording cache does not model isolation decisions; callers use
// it to observe association writes and their contexts.
func (s *qoderStickyBindCacheStub) GetSessionOwnerGroupID(context.Context, int64, string, string) (int64, error) {
	return 0, nil
}

// RefreshSessionOwnerTTL accepts owner lifetime refresh without maintaining
// fixture expiration state. It returns nil, leaving ownership and TTL policy
// to the real cache rather than this write-recording fixture.
func (s *qoderStickyBindCacheStub) RefreshSessionOwnerTTL(context.Context, int64, string, string, time.Duration) error {
	return nil
}

// BindStickySession delegates association storage to the configured cache with
// a one-hour TTL. A nil group pointer becomes group zero; the supplied work
// context and provider identity pass through unchanged.
func (s *qoderStickyExecutionStub) BindStickySession(ctx context.Context, id *int64, hash string, providerID int64) error {
	var groupID int64
	if id != nil {
		groupID = *id
	}
	return s.cache.SetSessionProviderID(ctx, groupID, hash, providerID, time.Hour)
}

// AcquireProviderSlot consumes one scripted provider admission under the
// shared mutex and records the call. An exhausted sequence returns false
// without error, letting timeout tests retain an unavailable slot.
func (s *helperConcurrencyCacheStub) AcquireProviderSlot(ctx context.Context, providerID int64, maxConcurrency int, requestID string) (bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	// Consume the next provider result and record acquisition under one mutex so
	// polling cannot separate its outcome from that call count.
	s.providerAcquireCalls++
	if len(s.providerSeq) == 0 {
		return false, nil
	}
	v := s.providerSeq[0]
	s.providerSeq = s.providerSeq[1:]
	return v, nil
}

// ReleaseProviderSlot records provider resource cleanup under the shared
// fixture mutex.
func (s *helperConcurrencyCacheStub) ReleaseProviderSlot(ctx context.Context, providerID int64, requestID string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	// Record provider cleanup under the same mutex as acquisition so cancellation
	// cleanup and runtime polling cannot race on fixture counters.
	s.providerReleaseCalls++
	return nil
}

// GetProviderConcurrency returns zero fixture load with no store lookup or
// error. Recorded acquire/release calls remain separate observations and do
// not implement an accumulated live concurrency value.
func (s *helperConcurrencyCacheStub) GetProviderConcurrency(ctx context.Context, providerID int64) (int, error) {
	return 0, nil
}

// GetProviderConcurrencyBatch returns a fresh zero-load entry for each
// requested provider. The caller owns the result map; fixture acquisition
// counters do not alter these synthetic scheduling load values.
func (s *helperConcurrencyCacheStub) GetProviderConcurrencyBatch(ctx context.Context, providerIDs []int64) (map[int64]int, error) {
	out := make(map[int64]int, len(providerIDs))
	for _, providerID := range providerIDs {
		out[providerID] = 0
	}
	return out, nil
}

// IncrementProviderWaitCount always permits base-fixture provider wait
// registration with no error. Provider queue tests override it in
// qoderProviderWaitCacheStub to record allowance and simulate an explicit
// refusal.
func (s *helperConcurrencyCacheStub) IncrementProviderWaitCount(ctx context.Context, providerID int64, maxWait int) (bool, error) {
	return true, nil
}

// DecrementProviderWaitCount accepts base-fixture provider queue cleanup
// without recording state. The provider-wait fixture overrides it when tests
// need to assert registration ownership and decrement counts after
// acquisition.
func (s *helperConcurrencyCacheStub) DecrementProviderWaitCount(ctx context.Context, providerID int64) error {
	return nil
}

// GetProviderWaitingCount returns zero synthetic waiting load without
// consulting recorded counters. This fixture tracks calls for assertions,
// while queue-load behavior remains outside the slot admission scenarios.
func (s *helperConcurrencyCacheStub) GetProviderWaitingCount(ctx context.Context, providerID int64) (int, error) {
	return 0, nil
}

// AcquireUserSlot consumes one scripted user-slot admission under the fixture
// mutex and records the call. Empty scripts return false without error,
// permitting wait/timeout paths to run without synthetic store failure.
func (s *helperConcurrencyCacheStub) AcquireUserSlot(ctx context.Context, userID int64, maxConcurrency int, requestID string) (bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	// Consume the next user result and record acquisition under one mutex so
	// concurrent admission observes one scripted answer per call.
	s.userAcquireCalls++
	if len(s.userSeq) == 0 {
		return false, nil
	}
	v := s.userSeq[0]
	s.userSeq = s.userSeq[1:]
	return v, nil
}

// ReleaseUserSlot records user resource cleanup under the shared fixture
// mutex.
func (s *helperConcurrencyCacheStub) ReleaseUserSlot(ctx context.Context, userID int64, requestID string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	// Record user cleanup under the shared mutex because cancellation may invoke
	// this callback independently of slot acquisition and test observation.
	s.userReleaseCalls++
	return nil
}

// GetUserConcurrency returns zero synthetic user load and nil error. Its
// result does not accumulate scripted slot grants or releases; tests inspect
// those separate call counters instead.
func (s *helperConcurrencyCacheStub) GetUserConcurrency(ctx context.Context, userID int64) (int, error) {
	return 0, nil
}

// TrackAPIKeySlot records tracking count and the ordered API-key identity
// under the mutex.
func (s *helperConcurrencyCacheStub) TrackAPIKeySlot(ctx context.Context, apiKeyID int64, requestID string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	// Record tracking count and Key identity together under the mutex so the
	// ordered identity list remains consistent with its call count.
	s.apiKeyTrackCalls++
	s.apiKeyTrackIDs = append(s.apiKeyTrackIDs, apiKeyID)
	return nil
}

// ReleaseAPIKeySlot records API-key resource cleanup under the shared fixture
// mutex.
func (s *helperConcurrencyCacheStub) ReleaseAPIKeySlot(ctx context.Context, apiKeyID int64, requestID string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	// Keep Key cleanup counter mutation under the tracking mutex so
	// resource-release callbacks cannot race with concurrent runtime fixture
	// access.
	s.apiKeyReleaseCalls++
	return nil
}

// GetAPIKeyConcurrencyBatch allocates a fresh map with zero load for each
// requested Key. Tracking calls remain separate fixture evidence, and callers
// can inspect or mutate the returned synthetic map.
func (s *helperConcurrencyCacheStub) GetAPIKeyConcurrencyBatch(ctx context.Context, apiKeyIDs []int64) (map[int64]int, error) {
	out := make(map[int64]int, len(apiKeyIDs))
	for _, apiKeyID := range apiKeyIDs {
		out[apiKeyID] = 0
	}
	return out, nil
}

// IncrementWaitCount records user queue registration and snapshots permission
// plus the optional hook under mu. It returns the captured permission with nil
// error; callers configure the hook before runtime access.
//
// The hook runs after unlocking so test code can observe or change surrounding
// admission state without holding the cache mutex. Capturing permission first
// keeps that call’s answer stable even if the hook changes fixture
// configuration. This fixture exposes registration ordering without making
// callbacks part of the synchronized counter mutation.
func (s *helperConcurrencyCacheStub) IncrementWaitCount(ctx context.Context, userID int64, maxWait int) (bool, error) {
	s.mu.Lock()
	s.waitIncrementCalls++
	s.waitMaxWait = maxWait
	waitAllowed := s.waitAllowed
	hook := s.waitIncrementHook
	// Release the counter lock before the hook so injected test behavior can
	// interact with admission without running inside this fixture’s synchronized mutation.
	s.mu.Unlock()

	if hook != nil {
		hook()
	}
	if !waitAllowed {
		return false, nil
	}
	return true, nil
}

// DecrementWaitCount records user queue cleanup under the shared fixture
// mutex.
func (s *helperConcurrencyCacheStub) DecrementWaitCount(ctx context.Context, userID int64) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	// Record user-wait cleanup under the registration mutex so queue release
	// cannot race with another admission hook updating fixture observations.
	s.waitDecrementCalls++
	return nil
}

// GetProvidersLoadBatch builds fresh zero-load records keyed by requested
// provider identity. Slot and queue counters remain assertion evidence rather
// than inputs to these synthetic scheduling records, which callers own.
func (s *helperConcurrencyCacheStub) GetProvidersLoadBatch(ctx context.Context, providers []scheduler.ProviderWithConcurrency) (map[int64]*scheduler.ProviderLoadInfo, error) {
	out := make(map[int64]*scheduler.ProviderLoadInfo, len(providers))
	for _, acc := range providers {
		out[acc.ID] = &scheduler.ProviderLoadInfo{ProviderID: acc.ID}
	}
	return out, nil
}

// GetUsersLoadBatch builds fresh zero-load records for each requested user
// identity. It does not derive live load from recorded acquire/release calls;
// callers own the returned map and its fixture records.
func (s *helperConcurrencyCacheStub) GetUsersLoadBatch(ctx context.Context, users []scheduler.UserWithConcurrency) (map[int64]*scheduler.UserLoadInfo, error) {
	out := make(map[int64]*scheduler.UserLoadInfo, len(users))
	for _, user := range users {
		out[user.ID] = &scheduler.UserLoadInfo{UserID: user.ID}
	}
	return out, nil
}

// CleanupExpiredProviderSlots accepts cleanup without maintaining or deleting
// fixture slot state. It returns nil because the fixture records admission
// calls rather than modeling a persistent store’s expiration behavior.
func (s *helperConcurrencyCacheStub) CleanupExpiredProviderSlots(ctx context.Context, providerID int64) error {
	return nil
}

// CleanupExpiredProviderSlotKeys accepts expired-key cleanup without changing
// recorded call counters. It returns nil; provider key lifetimes are outside
// the scripted admission and queue-order observations exercised here.
func (s *helperConcurrencyCacheStub) CleanupExpiredProviderSlotKeys(ctx context.Context) error {
	return nil
}

// CleanupStaleProcessSlots accepts stale-process cleanup without tracking
// request prefixes or persistent leases. It returns nil, leaving process
// recovery outside the fixture’s scripted slot outcomes and call history.
func (s *helperConcurrencyCacheStub) CleanupStaleProcessSlots(ctx context.Context, activeRequestPrefix string) error {
	return nil
}

// Forward returns the native parser's decision for this request without I/O.
func (f *localRejectionFixture) Forward(_ context.Context, _ *gin.Context, b []byte, _ protocol.ProtocolID, _ string) (*forwardcore.MessagesResult, error) {
	_, err := qoder.ParseQoderResponsesPayload(b)
	return nil, err
}

// Report retains each sample that would enter the advanced scheduler.
func (f *localRejectionFixture) Report(_ int64, success bool, _ *forwardcore.MessagesResult) {
	f.reports = append(f.reports, success)
}

// TestLocalQoderRejectionSkipsProviderFeedback exercises the compatible Responses
// attempt with a storage request. Releasing the provider slot is required even
// though the local refusal supplies no health or success-rate sample.
func TestLocalQoderRejectionSkipsProviderFeedback(t *testing.T) {
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", nil)
	fixture := &localRejectionFixture{}
	released := false
	bridge := qoderCompatibleAttemptBridge{
		c:         c,
		endpoint:  QoderResponses,
		provider:  fixture,
		selection: fixture,
		release:   func() { released = true },
		body:      []byte(`{"model":"route","input":"test","store":true}`),
		h:         &QoderCompatibleRuntime{options: QoderCompatibleOptions{MaySwitch: qoder.MaySwitchCompatibleAttempt}},
	}
	out := bridge.Forward()
	require.True(t, qoder.IsRequestError(out.Err))
	require.True(t, released)
	require.Empty(t, fixture.reports)
}
