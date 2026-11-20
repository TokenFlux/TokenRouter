package httpapi

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/TokenFlux/TokenRouter/internal/gateway/requeststate"
	"github.com/TokenFlux/TokenRouter/internal/gateway/session"
	"github.com/TokenFlux/TokenRouter/internal/infra/telemetry"
	"github.com/TokenFlux/TokenRouter/internal/infra/telemetry/timing"
	opscore "github.com/TokenFlux/TokenRouter/internal/ops"
	"github.com/TokenFlux/TokenRouter/internal/protocol"
	settingscore "github.com/TokenFlux/TokenRouter/internal/settings"
	"github.com/TokenFlux/TokenRouter/internal/upstream/openai"
)

// ingressRejectSettingRepo counts settings lookups that an ingress skip should
// bypass.
type ingressRejectSettingRepo struct {
	settingscore.Repository     // unimplemented fixture repository methods
	getValueCalls           int // counted settings read calls
}

// ingressRejectOpsRepo retains submitted diagnostic pointers for persistence
// assertions in serial tests.
type ingressRejectOpsRepo struct {
	opscore.OpsRepository     // unimplemented fixture repository methods
	insertCalls           int // counted repository submission calls
	// entries retains submitted pointers in order. Tests serialize access and keep
	// referenced inputs stable before checking the observation passed to
	// persistence.
	entries []*opscore.OpsInsertErrorLogInput
}

// blockingOpsResponseWriter delays one delegated write to expose capture
// release ordering.
type blockingOpsResponseWriter struct {
	gin.ResponseWriter // embedded client response writer
	// writeStarted closes when the sole delegated write begins, while writeRelease
	// blocks its return. The test coordinates release and assertions through those
	// channels before reusing pooled state.
	writeStarted chan struct{} // delegated write entry signal
	writeRelease chan struct{} // delegated write return gate
}

// deterministicOpsCaptureWriterStatePool retains states in LIFO order for
// deterministic serial lease-reuse tests.
type deterministicOpsCaptureWriterStatePool struct {
	// states retains unowned reusable capture states for LIFO acquisition. This
	// fixture has no mutex, so tests serialize pool access. Pending-release
	// inspection occurs behind the blocked writer gate, and reuse waits for the
	// release-completion channel.
	states []*opsCaptureWriterState
}

// setupOpsErrorLogTestQueue installs isolated synchronous submission state for
// a serial test. Cleanup restores the prior global queue and counters; callers
// keep these tests nonparallel because the fixture swaps shared package
// variables.
func setupOpsErrorLogTestQueue(t *testing.T, size int) {
	t.Helper()
	// Swap fixture globals before the serial request runs and restore both in
	// cleanup, so subsequent tests observe their own queue and health counters.
	previous, previousQueue := opsErrorLogQueue, testOpsCaptureQueue
	opsErrorLogQueue = make(chan opsErrorLogJob, size)
	testOpsCaptureQueue = &captureOpsErrorQueue{}
	t.Cleanup(func() { opsErrorLogQueue, testOpsCaptureQueue = previous, previousQueue })
}

// flushOpsErrorLogBatch records each usable queued fixture job with a
// background context. Nil jobs are skipped and service errors are discarded, so
// callers inspect their repository fixture to assert observable persistence
// effects.
func flushOpsErrorLogBatch(batch []opsErrorLogJob) {
	for _, job := range batch {
		if job.ops != nil && job.entry != nil {
			// Discard fixture recording errors because these tests assert the repository
			// submission itself; background context keeps the explicit flush independent
			// of a completed request.
			_ = job.ops.RecordErrorBatch(context.Background(), []*opscore.OpsInsertErrorLogInput{job.entry})
		}
	}
}

// OpsErrorLogQueueLength returns the fixture health length counted at
// synchronous submission. The fixture does not decrement this observation when
// tests manually receive channel jobs; callers use it as submitted-job
// evidence.
func OpsErrorLogQueueLength() int64 { return testOpsCaptureQueue.Health().Length }

// OpsErrorLogEnqueuedTotal returns the fixture total of accepted synchronous
// error submissions.
func OpsErrorLogEnqueuedTotal() int64 { return testOpsCaptureQueue.Health().Enqueued }

// newOpsServiceFixture constructs Ops with supplied repository and settings
// dependencies for capture tests.
func newOpsServiceFixture(repo opscore.OpsRepository, settings opscore.Settings) *opscore.OpsService {
	return opscore.NewOpsService(repo, settings, nil, nil, nil, nil, nil, nil)
}

// markOpsIngressRejectedFixture marks ingress denial for the fixture
// observation accessor.
func markOpsIngressRejectedFixture(c *gin.Context) { c.Set("ops_test_rejected", true) }

// GetValue counts a fixture settings read and returns the missing-setting
// sentinel.
func (r *ingressRejectSettingRepo) GetValue(context.Context, string) (string, error) {
	r.getValueCalls++
	return "", settingscore.ErrSettingNotFound
}

// GetMultiple counts a fixture settings read and returns an empty settings map.
func (r *ingressRejectSettingRepo) GetMultiple(context.Context, []string) (map[string]string, error) {
	r.getValueCalls++
	return map[string]string{}, nil
}

// Set accepts a fixture settings write and returns success without storing its
// input.
func (r *ingressRejectSettingRepo) Set(context.Context, string, string) error {
	return nil
}

// InsertErrorLog counts one fixture repository submission and retains the
// supplied entry pointer. Tests serialize access and keep that input stable
// before examining retained persistence evidence; the returned ID is zero.
func (r *ingressRejectOpsRepo) InsertErrorLog(_ context.Context, entry *opscore.OpsInsertErrorLogInput) (int64, error) {
	r.insertCalls++
	r.entries = append(r.entries, entry)
	return 0, nil
}

// BatchInsertErrorLogs counts one fixture batch and appends the supplied entry
// pointers in order. Inputs remain shared, so tests keep them stable while
// inspecting the repository and use the returned zero ID only as a successful
// fixture result.
func (r *ingressRejectOpsRepo) BatchInsertErrorLogs(_ context.Context, entries []*opscore.OpsInsertErrorLogInput) (int64, error) {
	r.insertCalls++
	r.entries = append(r.entries, entries...)
	return 0, nil
}

// TestOpsCaptureWriterPool_ResetOnRelease writes an error body, releases
// capture and acquires another handle. The acquired handle must expose an empty
// diagnostic body rather than bytes belonging to the earlier request.
func TestOpsCaptureWriterPool_ResetOnRelease(t *testing.T) {
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodGet, "/test", nil)

	writer := acquireOpsCaptureWriter(c.Writer)
	require.NotNil(t, writer)
	c.Writer.WriteHeader(http.StatusInternalServerError)
	_, err := writer.WriteString("temp-error-body")
	require.NoError(t, err)
	require.NotEmpty(t, writer.capturedBytes())

	releaseOpsCaptureWriter(writer)

	reused := acquireOpsCaptureWriter(c.Writer)
	defer releaseOpsCaptureWriter(reused)

	require.Empty(t, reused.capturedBytes(), "writer should be reset before reuse")
}

// TestOpsCaptureWriterPool_DropsLargeBuffers grows capture storage beyond its
// retention budget. The release-time eligibility predicate must reject that
// oversized capacity instead of admitting it for reuse.
func TestOpsCaptureWriterPool_DropsLargeBuffers(t *testing.T) {
	state := &opsCaptureWriterState{}
	state.buf.Grow(opsCaptureWriterPoolMaxRetainedCapacity + 1)
	require.False(t, shouldPoolOpsCaptureWriterState(state))
}

// TestOpsErrorLoggerMiddleware_HardSkipsIngressRejection marks an
// authentication denial as ingress rejected. The client body must remain intact
// while the request performs no monitoring settings read, repository insert or
// error-queue submission.
func TestOpsErrorLoggerMiddleware_HardSkipsIngressRejection(t *testing.T) {
	setupOpsErrorLogTestQueue(t, 4)

	settings := &ingressRejectSettingRepo{}
	repo := &ingressRejectOpsRepo{}
	ops := newOpsServiceFixture(repo, settings)
	// Construction may read other settings; count reads from the request path only.
	settings.getValueCalls = 0

	router := gin.New()
	router.Use(opsLoggerFixture(ops))
	router.GET("/v1/messages", func(c *gin.Context) {
		markOpsIngressRejectedFixture(c)
		c.JSON(http.StatusUnauthorized, gin.H{"code": "INVALID_API_KEY", "message": "Invalid API key"})
	})

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/v1/messages", nil)
	router.ServeHTTP(w, req)

	require.Equal(t, http.StatusUnauthorized, w.Code)
	require.JSONEq(t, `{"code":"INVALID_API_KEY","message":"Invalid API key"}`, w.Body.String())
	require.Zero(t, settings.getValueCalls, "ingress rejection must bypass monitoring settings reads")
	require.Zero(t, repo.insertCalls, "ingress rejection must bypass inserts")
	require.Zero(t, OpsErrorLogEnqueuedTotal(), "ingress rejection must not enter the error queue")
}

// TestOpsErrorLoggerMiddleware_OrdinaryPermissionStillRecords returns a
// permission error without an ingress rejection marker. Normal capture must
// queue one permission failure with its forbidden HTTP status.
func TestOpsErrorLoggerMiddleware_OrdinaryPermissionStillRecords(t *testing.T) {
	setupOpsErrorLogTestQueue(t, 2)

	ops := newOpsServiceFixture(nil, nil)
	router := gin.New()
	router.Use(opsLoggerFixture(ops))
	router.POST("/v1/responses", func(c *gin.Context) {
		c.JSON(http.StatusForbidden, gin.H{"error": gin.H{
			"type": "permission_error", "code": "permission_denied", "message": "denied",
		}})
	})

	recorder := httptest.NewRecorder()
	router.ServeHTTP(recorder, httptest.NewRequest(http.MethodPost, "/v1/responses", nil))

	require.Equal(t, int64(1), OpsErrorLogQueueLength())
	job := <-opsErrorLogQueue
	require.Equal(t, "permission_error", job.entry.ErrorType)
	require.Equal(t, http.StatusForbidden, job.entry.StatusCode)
}

// TestOpsErrorLoggerMiddleware_RecordsRecoveredUpstreamTelemetryOutsideFailureSLA
// returns success after an upstream rate-limit observation. Queued and
// persisted diagnostics must keep status 200 while retaining the earlier
// attempt in prepared JSON and its recovered summary.
func TestOpsErrorLoggerMiddleware_RecordsRecoveredUpstreamTelemetryOutsideFailureSLA(t *testing.T) {
	setupOpsErrorLogTestQueue(t, 2)

	repo := &ingressRejectOpsRepo{}
	ops := newOpsServiceFixture(repo, nil)
	router := gin.New()
	router.Use(opsLoggerFixture(ops))
	router.POST("/v1/responses", func(c *gin.Context) {
		c.Set(OpsUpstreamErrorsKey, []*opscore.OpsUpstreamErrorEvent{{
			UpstreamStatusCode: http.StatusTooManyRequests,
			Message:            "earlier attempt was rate limited",
		}})
		c.JSON(http.StatusOK, gin.H{"status": "completed"})
	})

	recorder := httptest.NewRecorder()
	router.ServeHTTP(recorder, httptest.NewRequest(http.MethodPost, "/v1/responses", nil))

	require.Equal(t, http.StatusOK, recorder.Code)
	require.Equal(t, int64(1), OpsErrorLogQueueLength())
	job := <-opsErrorLogQueue
	require.Nil(t, job.entry.UpstreamErrors, "raw attempts must be released before async queueing")
	require.NotNil(t, job.entry.UpstreamErrorsJSON)
	queuedEvents, err := opscore.ParseOpsUpstreamErrors(*job.entry.UpstreamErrorsJSON)
	require.NoError(t, err)
	require.Len(t, queuedEvents, 1)
	require.Equal(t, http.StatusTooManyRequests, queuedEvents[0].UpstreamStatusCode)

	flushOpsErrorLogBatch([]opsErrorLogJob{job})
	require.Equal(t, 1, repo.insertCalls)
	require.Len(t, repo.entries, 1)
	persisted := repo.entries[0]
	require.Equal(t, http.StatusOK, persisted.StatusCode, "recovered telemetry must remain outside failed-request SLA")
	require.Equal(t, "upstream", persisted.ErrorPhase)
	require.Equal(t, "upstream_error", persisted.ErrorType)
	require.Equal(t, "Recovered upstream error 429: earlier attempt was rate limited", persisted.ErrorMessage)
	require.NotNil(t, persisted.UpstreamErrorsJSON)
	persistedEvents, err := opscore.ParseOpsUpstreamErrors(*persisted.UpstreamErrorsJSON)
	require.NoError(t, err)
	require.Len(t, persistedEvents, 1)
	require.Equal(t, http.StatusTooManyRequests, persistedEvents[0].UpstreamStatusCode)
}

// TestOpsErrorLoggerMiddleware_RecoveredTelemetryFiltersSkipMonitoringAttempts
// recovers after one visible and one hidden attempt. The queued summary and
// serialized event list must contain the visible rate-limit evidence and omit
// the hidden retry.
func TestOpsErrorLoggerMiddleware_RecoveredTelemetryFiltersSkipMonitoringAttempts(t *testing.T) {
	setupOpsErrorLogTestQueue(t, 2)

	ops := newOpsServiceFixture(nil, nil)
	router := gin.New()
	router.Use(opsLoggerFixture(ops))
	router.POST("/v1/responses", func(c *gin.Context) {
		c.Set(OpsUpstreamErrorsKey, []*opscore.OpsUpstreamErrorEvent{
			{UpstreamStatusCode: http.StatusTooManyRequests, Message: "visible retry"},
			{UpstreamStatusCode: http.StatusBadGateway, Message: "hidden retry", SkipMonitoring: true},
		})
		c.JSON(http.StatusOK, gin.H{"status": "completed"})
	})

	recorder := httptest.NewRecorder()
	router.ServeHTTP(recorder, httptest.NewRequest(http.MethodPost, "/v1/responses", nil))

	require.Equal(t, int64(1), OpsErrorLogQueueLength())
	job := <-opsErrorLogQueue
	require.Equal(t, "Recovered upstream error 429: visible retry", job.entry.ErrorMessage)
	require.NotNil(t, job.entry.UpstreamErrorsJSON)
	events, err := opscore.ParseOpsUpstreamErrors(*job.entry.UpstreamErrorsJSON)
	require.NoError(t, err)
	require.Len(t, events, 1)
	require.Equal(t, "visible retry", events[0].Message)
}

// TestOpsErrorLoggerMiddleware_RecoveredTelemetrySkipsAllHiddenAttempts returns
// success after a single hidden upstream attempt. The middleware must queue no
// recovered diagnostic entry when filtering leaves no visible attempt evidence.
func TestOpsErrorLoggerMiddleware_RecoveredTelemetrySkipsAllHiddenAttempts(t *testing.T) {
	setupOpsErrorLogTestQueue(t, 2)

	ops := newOpsServiceFixture(nil, nil)
	router := gin.New()
	router.Use(opsLoggerFixture(ops))
	router.POST("/v1/responses", func(c *gin.Context) {
		c.Set(OpsUpstreamErrorsKey, []*opscore.OpsUpstreamErrorEvent{{
			UpstreamStatusCode: http.StatusTooManyRequests,
			Message:            "hidden retry",
			SkipMonitoring:     true,
		}})
		c.JSON(http.StatusOK, gin.H{"status": "completed"})
	})

	recorder := httptest.NewRecorder()
	router.ServeHTTP(recorder, httptest.NewRequest(http.MethodPost, "/v1/responses", nil))

	require.Equal(t, int64(0), OpsErrorLogQueueLength())
}

// TestOpsErrorLoggerMiddleware_IntermediateSkipMonitoringDoesNotHideFinalVisibleFailure
// records a hidden retry before a visible final failure. Normal monitoring must
// still queue the final 503 and its message.
func TestOpsErrorLoggerMiddleware_IntermediateSkipMonitoringDoesNotHideFinalVisibleFailure(t *testing.T) {
	setupOpsErrorLogTestQueue(t, 2)

	ops := newOpsServiceFixture(nil, nil)
	router := gin.New()
	router.Use(opsLoggerFixture(ops))
	router.POST("/v1/responses", func(c *gin.Context) {
		c.Set(OpsUpstreamErrorsKey, []*opscore.OpsUpstreamErrorEvent{
			{UpstreamStatusCode: http.StatusBadGateway, Message: "hidden retry", SkipMonitoring: true},
			{UpstreamStatusCode: http.StatusServiceUnavailable, Message: "visible final"},
		})
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": gin.H{"type": "upstream_error", "message": "visible final"}})
	})

	recorder := httptest.NewRecorder()
	router.ServeHTTP(recorder, httptest.NewRequest(http.MethodPost, "/v1/responses", nil))
	require.Equal(t, int64(1), OpsErrorLogQueueLength())
	job := <-opsErrorLogQueue
	require.Equal(t, http.StatusServiceUnavailable, job.entry.StatusCode)
	require.Equal(t, "visible final", job.entry.ErrorMessage)
}

// TestOpsErrorLoggerMiddleware_CapturesSplitResponsesFailedSSE splits a
// Responses terminal event name and payload across writes under HTTP 200. The
// queued entry must infer 429 and retain the final rate-limit message.
func TestOpsErrorLoggerMiddleware_CapturesSplitResponsesFailedSSE(t *testing.T) {
	setupOpsErrorLogTestQueue(t, 2)

	ops := newOpsServiceFixture(nil, nil)
	router := gin.New()
	router.Use(opsLoggerFixture(ops))
	router.POST("/v1/responses", func(c *gin.Context) {
		SetOpsRequestContext(c, "gpt-5.5", true)
		c.Status(http.StatusOK)
		_, _ = c.Writer.Write([]byte("event: response."))
		_, _ = c.Writer.Write([]byte("failed\n"))
		_, _ = c.Writer.Write([]byte(`data: {"type":"response.failed","response":{"error":{"code":"rate_limit_exceeded","message":"Too many pending requests"}}}`))
		_, _ = c.Writer.Write([]byte("\n\n"))
	})

	recorder := httptest.NewRecorder()
	router.ServeHTTP(recorder, httptest.NewRequest(http.MethodPost, "/v1/responses", nil))

	require.Equal(t, http.StatusOK, recorder.Code)
	require.Equal(t, int64(1), OpsErrorLogQueueLength())
	job := <-opsErrorLogQueue
	require.Equal(t, http.StatusTooManyRequests, job.entry.StatusCode)
	require.Equal(t, "rate_limit_error", job.entry.ErrorType)
	require.Contains(t, job.entry.ErrorMessage, "Too many pending requests")
}

// TestOpsCaptureWriter_CapturesSplitDataOnlyTerminalMarkers splits terminal
// payload type spellings across data-only writes. Capture must retain no
// partial body until recognition and then extract each expected category, code
// and message within the probe budget.
func TestOpsCaptureWriter_CapturesSplitDataOnlyTerminalMarkers(t *testing.T) {
	tests := []struct {
		name      string // named fixture case label
		prefix    string // first attempted frame fragment
		suffix    string // remaining attempted frame fragment
		wantType  string // expected extracted error category
		wantCode  string // expected extracted error code
		wantError string // expected extracted failure message
	}{
		{
			name:      "response failed with space",
			prefix:    `data: {"type":"response.`,
			suffix:    `failed","response":{"error":{"code":"server_is_overloaded","message":"busy"}}}`,
			wantType:  "overloaded_error",
			wantCode:  "server_is_overloaded",
			wantError: "busy",
		},
		{
			name:      "response failed without space",
			prefix:    `data:{"type":"response.`,
			suffix:    `failed","error":{"code":"rate_limit_exceeded","message":"slow down"}}`,
			wantType:  "rate_limit_error",
			wantCode:  "rate_limit_exceeded",
			wantError: "slow down",
		},
		{
			name:      "error with space",
			prefix:    `data: {"type":"er`,
			suffix:    `ror","error":{"type":"invalid_request_error","code":"invalid_request","message":"bad input"}}`,
			wantType:  "invalid_request_error",
			wantCode:  "invalid_request",
			wantError: "bad input",
		},
		{
			name:      "error without space",
			prefix:    `data:{"type":"er`,
			suffix:    `ror","error":{"type":"authentication_error","code":"authentication_failed","message":"sign in"}}`,
			wantType:  "authentication_error",
			wantCode:  "authentication_failed",
			wantError: "sign in",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			state := &opsCaptureWriterState{limit: opsCaptureWriterLimit}
			state.captureResponseChunk([]byte(tt.prefix), http.StatusOK)
			require.Empty(t, state.buf.Bytes(), "partial frame must remain in the bounded probe")
			state.captureResponseChunk([]byte(tt.suffix+"\n\n"), http.StatusOK)

			parsed := opscore.ParseOpsErrorResponse(state.buf.Bytes())
			require.True(t, state.sseCapturing)
			require.True(t, parsed.StreamFailure)
			require.Equal(t, tt.wantType, parsed.ErrorType)
			require.Equal(t, tt.wantCode, parsed.Code)
			require.Equal(t, tt.wantError, parsed.Message)
			require.LessOrEqual(t, len(state.probe), opsTerminalSSEFrameProbeLimit)
		})
	}
}

// TestOpsErrorLoggerMiddleware_StreamFailureUsesTerminalErrorOverAttemptContext
// stores an earlier transport error before a terminal context-length failure.
// The queued logical status and upstream summary must use the terminal 400,
// leaving the client wire at 200.
func TestOpsErrorLoggerMiddleware_StreamFailureUsesTerminalErrorOverAttemptContext(t *testing.T) {
	setupOpsErrorLogTestQueue(t, 2)

	ops := newOpsServiceFixture(nil, nil)
	router := gin.New()
	router.Use(opsLoggerFixture(ops))
	router.POST("/v1/responses", func(c *gin.Context) {
		SetOpsUpstreamError(c, http.StatusBadGateway, "Upstream transport error", "earlier attempt failed")
		c.Status(http.StatusOK)
		_, _ = c.Writer.WriteString("event: er")
		_, _ = c.Writer.WriteString("ror\n")
		_, _ = c.Writer.WriteString(`data: {"type":"error","error":{"type":"invalid_request_error","code":"context_length_exceeded","message":"input exceeds the context window"}}`)
		_, _ = c.Writer.WriteString("\n\n")
	})

	recorder := httptest.NewRecorder()
	router.ServeHTTP(recorder, httptest.NewRequest(http.MethodPost, "/v1/responses", nil))

	require.Equal(t, http.StatusOK, recorder.Code)
	require.Equal(t, int64(1), OpsErrorLogQueueLength())
	job := <-opsErrorLogQueue
	require.Equal(t, http.StatusBadRequest, job.entry.StatusCode)
	require.Equal(t, "invalid_request_error", job.entry.ErrorType)
	require.NotNil(t, job.entry.UpstreamStatusCode)
	require.Equal(t, http.StatusBadRequest, *job.entry.UpstreamStatusCode)
	require.NotNil(t, job.entry.UpstreamErrorMessage)
	require.Equal(t, "input exceeds the context window", *job.entry.UpstreamErrorMessage)
}

// TestOpsErrorLoggerMiddleware_PrefersContextRequestID supplies different
// request IDs in telemetry and the response header. The queued observation must
// retain the platform context ID for correlation.
func TestOpsErrorLoggerMiddleware_PrefersContextRequestID(t *testing.T) {
	setupOpsErrorLogTestQueue(t, 2)

	ops := newOpsServiceFixture(nil, nil)
	router := gin.New()
	router.Use(opsLoggerFixture(ops))
	router.POST("/v1/responses", func(c *gin.Context) {
		c.Header("X-Request-Id", "response-header-id")
		c.JSON(http.StatusBadRequest, gin.H{"error": gin.H{"type": "invalid_request_error", "message": "bad input"}})
	})

	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPost, "/v1/responses", nil)
	request = request.WithContext(context.WithValue(request.Context(), telemetry.RequestID, "context-request-id"))
	router.ServeHTTP(recorder, request)

	require.Equal(t, int64(1), OpsErrorLogQueueLength())
	job := <-opsErrorLogQueue
	require.Equal(t, "context-request-id", job.entry.RequestID)
}

// TestLogOpsStreamError_RecordsInBandConcurrencyLimit marks a concurrency
// timeout after successful HTTP commitment. Logging must classify severity from
// intended 429 while retaining wire status 200 and client business restriction
// attribution.
func TestLogOpsStreamError_RecordsInBandConcurrencyLimit(t *testing.T) {
	setupOpsErrorLogTestQueue(t, 4)

	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/messages", nil)
	c.Set(OpsModelKey, "test-model")
	MarkOpsStreamError(c, "rate_limit_error",
		"Concurrency limit exceeded for provider, please retry later", http.StatusTooManyRequests)

	ops := newOpsServiceFixture(nil, nil)
	logOpsStreamError(c, ops, http.StatusOK, testOpsCaptureQueue, opsAccessFixture())

	require.Equal(t, int64(1), OpsErrorLogEnqueuedTotal())
	require.Equal(t, int64(1), OpsErrorLogQueueLength())

	job := <-opsErrorLogQueue
	require.NotNil(t, job.entry)
	require.Equal(t, "rate_limit_error", job.entry.ErrorType)
	require.Equal(t, "request", job.entry.ErrorPhase)
	require.True(t, job.entry.IsBusinessLimited)
	require.True(t, job.entry.Stream)
	require.Equal(t, http.StatusOK, job.entry.StatusCode) // Retain the actual wire status of 200.
	require.Equal(t, "P1", job.entry.Severity)            // Classify severity using IntendedStatus 429.
	require.Equal(t, "test-model", job.entry.Model)
	require.Equal(t, "Concurrency limit exceeded for provider, please retry later", job.entry.ErrorMessage)
}

// TestLogOpsStreamError_UpstreamFailureCountsTowardsSLA marks an upstream
// stream failure after HTTP 200 commitment. The queued entry must record
// logical 502 for SLA accounting while retaining the supplied failure code.
func TestLogOpsStreamError_UpstreamFailureCountsTowardsSLA(t *testing.T) {
	setupOpsErrorLogTestQueue(t, 4)

	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil)
	c.Set(OpsModelKey, "gpt-5.6-sol")
	MarkOpsStreamFailure(
		c,
		"upstream_error",
		openai.OpenAIUpstreamHTTP2StreamErrorCode,
		"Upstream HTTP/2 stream failed",
		http.StatusBadGateway,
	)

	ops := newOpsServiceFixture(nil, nil)
	logOpsStreamError(c, ops, http.StatusOK, testOpsCaptureQueue, opsAccessFixture())

	job := <-opsErrorLogQueue
	require.NotNil(t, job.entry)
	require.Equal(t, http.StatusBadGateway, job.entry.StatusCode)
	require.Equal(t, "upstream_error", job.entry.ErrorType)
	require.Equal(t, "upstream", job.entry.ErrorPhase)
	require.Equal(t, "provider", job.entry.ErrorOwner)
	require.False(t, job.entry.IsBusinessLimited)
	require.Contains(t, job.entry.ErrorBody, openai.OpenAIUpstreamHTTP2StreamErrorCode)
}

// TestLogOpsStreamError_NoopWhenNotMarked invokes stream logging with no
// failure marker. No diagnostic job may enter the queue merely because the
// request uses a stream path.
func TestLogOpsStreamError_NoopWhenNotMarked(t *testing.T) {
	setupOpsErrorLogTestQueue(t, 4)

	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/messages", nil)

	ops := newOpsServiceFixture(nil, nil)
	logOpsStreamError(c, ops, http.StatusOK, testOpsCaptureQueue, opsAccessFixture())

	require.Equal(t, int64(0), OpsErrorLogEnqueuedTotal())
}

// TestLogOpsStreamError_SkipWhenPassthroughSkipMonitoring marks a stream
// failure covered by a request suppression flag. Stream logging must leave the
// error queue empty for that hidden failure.
func TestLogOpsStreamError_SkipWhenPassthroughSkipMonitoring(t *testing.T) {
	setupOpsErrorLogTestQueue(t, 4)

	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/messages", nil)
	MarkOpsStreamError(c, "upstream_error", "Upstream request failed", http.StatusBadGateway)
	c.Set(OpsSkipPassthroughKey, true)

	ops := newOpsServiceFixture(nil, nil)
	logOpsStreamError(c, ops, http.StatusOK, testOpsCaptureQueue, opsAccessFixture())

	require.Equal(t, int64(0), OpsErrorLogEnqueuedTotal())
}

// TestShouldSkipFinalOpsFailureUsesOnlyFinalAttemptRule supplies attempts with
// different monitoring decisions and a nil element. The final nonnil attempt
// must decide whether this failure is suppressed, independently of the earlier
// attempt.
func TestShouldSkipFinalOpsFailureUsesOnlyFinalAttemptRule(t *testing.T) {
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Set(OpsUpstreamErrorsKey, []*opscore.OpsUpstreamErrorEvent{
		{UpstreamStatusCode: http.StatusBadGateway, Message: "hidden intermediate", SkipMonitoring: true},
		{UpstreamStatusCode: http.StatusServiceUnavailable, Message: "visible final"},
	})
	require.False(t, shouldSkipFinalOpsFailure(c))

	c.Set(OpsUpstreamErrorsKey, []*opscore.OpsUpstreamErrorEvent{
		{UpstreamStatusCode: http.StatusBadGateway, Message: "visible intermediate"},
		nil,
		{UpstreamStatusCode: http.StatusServiceUnavailable, Message: "hidden final", SkipMonitoring: true},
	})
	require.True(t, shouldSkipFinalOpsFailure(c))
}

// TestMarkOpsStreamError_FirstWins records two HTTP in-band failures in
// sequence. The first marker must retain its category, message and intended
// status after a later generic observation.
func TestMarkOpsStreamError_FirstWins(t *testing.T) {
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	MarkOpsStreamError(c, "rate_limit_error", "Concurrency limit exceeded for provider", http.StatusTooManyRequests)
	MarkOpsStreamError(c, "upstream_error", "Upstream request failed", http.StatusBadGateway)

	se, ok := GetOpsStreamError(c)
	require.True(t, ok)
	require.Equal(t, "rate_limit_error", se.ErrType)
	require.Equal(t, "Concurrency limit exceeded for provider", se.Message)
	require.Equal(t, http.StatusTooManyRequests, se.IntendedStatus)
}

// TestLogOpsStreamError_RecordsOneFailurePerWebSocketTurn records repeated
// observations across two ordered WebSocket turns. Logging must queue one
// retained failure per turn with its corresponding message and intended status.
func TestLogOpsStreamError_RecordsOneFailurePerWebSocketTurn(t *testing.T) {
	setupOpsErrorLogTestQueue(t, 4)

	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest(http.MethodGet, "/v1/responses", nil)
	SetOpenAIClientTransport(c, OpenAIClientTransportWS)
	BeginOpsStreamTurn(c, 1)
	MarkOpsStreamFailure(c, "rate_limit_error", "rate_limit_exceeded", "turn one failed", http.StatusTooManyRequests)
	MarkOpsStreamError(c, "upstream_error", "generic duplicate for turn one", http.StatusBadGateway)
	BeginOpsStreamTurn(c, 2)
	MarkOpsStreamFailure(c, "permission_error", "permission_denied", "turn two failed", http.StatusForbidden)

	streamErrors := GetOpsStreamErrors(c)
	require.Len(t, streamErrors, 2)
	require.Equal(t, 1, streamErrors[0].Turn)
	require.Equal(t, 2, streamErrors[1].Turn)
	ops := newOpsServiceFixture(nil, nil)
	logOpsStreamError(c, ops, http.StatusSwitchingProtocols, testOpsCaptureQueue, opsAccessFixture())

	require.Equal(t, int64(2), OpsErrorLogQueueLength())
	first := <-opsErrorLogQueue
	second := <-opsErrorLogQueue
	require.Equal(t, "turn one failed", first.entry.ErrorMessage)
	require.Equal(t, http.StatusTooManyRequests, first.entry.StatusCode)
	require.Equal(t, "turn two failed", second.entry.ErrorMessage)
	require.Equal(t, http.StatusForbidden, second.entry.StatusCode)
}

// TestClassifyOpsNoAvailableProvidersExcludedFromSLA marks a no-provider
// routing error as capacity limited. Classification must select routing
// ownership and business restriction evidence for SLA exclusion.
func TestClassifyOpsNoAvailableProvidersExcludedFromSLA(t *testing.T) {
	const message = "No available providers"

	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	MarkOpsRoutingCapacityLimited(c)

	errType := opscore.NormalizeErrorType("api_error", "")
	phase, isBusinessLimited, errorOwner, errorSource := classifyOpsErrorLog(c, errType, message, "", http.StatusServiceUnavailable)

	require.Equal(t, "api_error", errType)
	require.Equal(t, "routing", phase)
	require.True(t, isBusinessLimited)
	require.Equal(t, "platform", errorOwner)
	require.Equal(t, "gateway", errorSource)
}

// TestClassifyOpsRoutingCapacityMarkerExcludesMaskedSelectionFailureFromSLA
// uses a generic error message with a routing-capacity marker. That explicit
// evidence must retain routing ownership and business-limit classification
// despite masked selection text.
func TestClassifyOpsRoutingCapacityMarkerExcludesMaskedSelectionFailureFromSLA(t *testing.T) {
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	MarkOpsRoutingCapacityLimited(c)

	phase, isBusinessLimited, errorOwner, errorSource := classifyOpsErrorLog(
		c,
		"api_error",
		"Service temporarily unavailable",
		"",
		http.StatusServiceUnavailable,
	)

	require.Equal(t, "routing", phase)
	require.True(t, isBusinessLimited)
	require.Equal(t, "platform", errorOwner)
	require.Equal(t, "gateway", errorSource)
}

// TestClassifyOpsLocalModelConfigurationRejection marks a missing locally
// configured model as a client business restriction. Classification must assign
// the failure to gateway routing and platform ownership.
func TestClassifyOpsLocalModelConfigurationRejection(t *testing.T) {
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	MarkOpsClientBusinessLimited(c, OpsClientBusinessLimitedReasonLocalModelConfiguration)

	phase, isBusinessLimited, errorOwner, errorSource := classifyOpsErrorLog(
		c,
		"model_not_found",
		"Model \"gpt-missing\" is not supported by any configured provider in this group",
		"",
		http.StatusNotFound,
	)

	require.Equal(t, "routing", phase)
	require.True(t, isBusinessLimited)
	require.Equal(t, "platform", errorOwner)
	require.Equal(t, "gateway", errorSource)
}

// TestClassifyOpsLocalModelConfigurationOverridesStaleUpstreamMarkers combines
// a local model rejection marker with stale provider-auth evidence. Local
// routing attribution must win, retaining business restriction and gateway
// ownership.
func TestClassifyOpsLocalModelConfigurationOverridesStaleUpstreamMarkers(t *testing.T) {
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	MarkOpsClientBusinessLimited(c, OpsClientBusinessLimitedReasonLocalModelConfiguration)
	c.Set(OpsUpstreamStatusCodeKey, http.StatusUnauthorized)
	c.Set(OpsUpstreamErrorsKey, []*opscore.OpsUpstreamErrorEvent{{
		Stage:              opscore.ErrorPhaseProviderAuth,
		UpstreamStatusCode: http.StatusUnauthorized,
	}})

	phase, limited, owner, source := classifyOpsErrorLog(c, "model_not_found", "unsupported configured model", "", http.StatusNotFound)

	require.Equal(t, "routing", phase)
	require.True(t, limited)
	require.Equal(t, "platform", owner)
	require.Equal(t, "gateway", source)
}

// TestClassifyOpsLocalModelConfigurationRequiresMarkerAndReason stores the
// local configuration reason without the business marker. Existing upstream
// failure evidence must retain provider ownership and remain a counted failure.
func TestClassifyOpsLocalModelConfigurationRequiresMarkerAndReason(t *testing.T) {
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Set(OpsClientBusinessLimitedReasonKey, OpsClientBusinessLimitedReasonLocalModelConfiguration)
	c.Set(OpsUpstreamStatusCodeKey, http.StatusBadGateway)

	phase, limited, owner, source := classifyOpsErrorLog(c, "upstream_error", "provider failed", "", http.StatusBadGateway)

	require.Equal(t, "upstream", phase)
	require.False(t, limited)
	require.Equal(t, "provider", owner)
	require.Equal(t, "upstream_http", source)
}

// TestOpsErrorLoggerMiddleware_LocalModelConfigurationFields returns a marked
// local model rejection with stale provider and attempt fields. Queued routing
// diagnostics must clear provider identity, upstream endpoint/model and prior
// error evidence.
func TestOpsErrorLoggerMiddleware_LocalModelConfigurationFields(t *testing.T) {
	setupOpsErrorLogTestQueue(t, 1)

	ops := newOpsServiceFixture(nil, nil)
	router := gin.New()
	router.Use(opsLoggerFixture(ops))
	router.POST("/v1/chat/completions", func(c *gin.Context) {
		MarkOpsClientBusinessLimited(c, OpsClientBusinessLimitedReasonLocalModelConfiguration)
		c.Set(OpsProviderIDKey, int64(99))
		c.Set(OpsUpstreamModelKey, "stale-upstream-model")
		SetActualUpstreamEndpoint(c, "/v1/chat/completions")
		c.Set(OpsUpstreamStatusCodeKey, http.StatusUnauthorized)
		c.Set(OpsUpstreamErrorMessageKey, "stale upstream error")
		c.Set(OpsUpstreamErrorDetailKey, "stale upstream detail")
		c.Set(OpsUpstreamErrorsKey, []*opscore.OpsUpstreamErrorEvent{{
			Stage:              opscore.ErrorPhaseProviderAuth,
			UpstreamStatusCode: http.StatusUnauthorized,
			Message:            "stale auth failure",
		}})
		c.JSON(http.StatusNotFound, gin.H{
			"error": gin.H{
				"type":    "model_not_found",
				"message": "Model \"gpt-missing\" is not supported by any configured provider in this group",
			},
		})
	})

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil)
	router.ServeHTTP(w, req)

	require.Equal(t, http.StatusNotFound, w.Code)
	require.JSONEq(t, `{"error":{"type":"model_not_found","message":"Model \"gpt-missing\" is not supported by any configured provider in this group"}}`, w.Body.String())
	job := <-opsErrorLogQueue
	require.Equal(t, http.StatusNotFound, job.entry.StatusCode)
	require.Equal(t, "routing", job.entry.ErrorPhase)
	require.True(t, job.entry.IsBusinessLimited)
	require.Equal(t, "platform", job.entry.ErrorOwner)
	require.Equal(t, "gateway", job.entry.ErrorSource)
	require.Nil(t, job.entry.ProviderID)
	require.Nil(t, job.entry.UpstreamStatusCode)
	require.Nil(t, job.entry.UpstreamErrors)
	require.Nil(t, job.entry.UpstreamErrorMessage)
	require.Nil(t, job.entry.UpstreamErrorDetail)
	require.Empty(t, job.entry.UpstreamModel)
	require.Empty(t, job.entry.UpstreamEndpoint)
}

// TestClassifyOpsAuthClientErrorsExcludedFromSLA supplies recognized client
// authentication error types, codes and messages. Each case must be classified
// as a client business restriction while retaining its expected authentication
// attribution.
func TestClassifyOpsAuthClientErrorsExcludedFromSLA(t *testing.T) {
	tests := []struct {
		name    string // named fixture case label
		errType string // supplied client error category
		message string // supplied client failure message
		code    string // supplied client error code
		status  int    // supplied client HTTP status
	}{
		{
			name:    "standard invalid API key",
			errType: "api_error",
			message: "Invalid API key",
			code:    "INVALID_API_KEY",
			status:  http.StatusUnauthorized,
		},
		{
			name:    "standard missing API key",
			errType: "api_error",
			message: "API key is required in Authorization header (Bearer scheme), x-api-key header, or x-goog-api-key header",
			code:    "API_KEY_REQUIRED",
			status:  http.StatusUnauthorized,
		},
		{
			name:    "expired local API key",
			errType: "api_error",
			message: "API key 已过期",
			code:    "API_KEY_EXPIRED",
			status:  http.StatusForbidden,
		},
		{
			name:    "disabled local API key",
			errType: "api_error",
			message: "API key is disabled",
			code:    "API_KEY_DISABLED",
			status:  http.StatusUnauthorized,
		},
		{
			name:    "local API key user missing",
			errType: "api_error",
			message: "User associated with API key not found",
			code:    "USER_NOT_FOUND",
			status:  http.StatusUnauthorized,
		},
		{
			name:    "inactive local API key user",
			errType: "api_error",
			message: "User account is not active",
			code:    "USER_INACTIVE",
			status:  http.StatusUnauthorized,
		},
		{
			name:    "deleted local API key group",
			errType: "api_error",
			message: "API Key 所属分组已删除",
			code:    "GROUP_DELETED",
			status:  http.StatusForbidden,
		},
		{
			name:    "disabled local API key group",
			errType: "api_error",
			message: "API Key 所属分组已停用",
			code:    "GROUP_DISABLED",
			status:  http.StatusForbidden,
		},
		{
			name:    "google deleted API key group message without semantic code",
			errType: "api_error",
			message: "API Key 所属分组已删除",
			code:    "403",
			status:  http.StatusForbidden,
		},
		{
			name:    "anthropic unassigned API key group",
			errType: "permission_error",
			message: "API Key is not assigned to any group and cannot be used. Please contact the administrator to assign it to a group.",
			code:    "",
			status:  http.StatusForbidden,
		},
		{
			name:    "google invalid API key",
			errType: "api_error",
			message: "Invalid API key",
			code:    "401",
			status:  http.StatusUnauthorized,
		},
		{
			name:    "google missing API key",
			errType: "api_error",
			message: "API key is required",
			code:    "401",
			status:  http.StatusUnauthorized,
		},
		{
			name:    "google disabled API key",
			errType: "api_error",
			message: "API key is disabled",
			code:    "401",
			status:  http.StatusUnauthorized,
		},
		{
			name:    "google local API key user missing",
			errType: "api_error",
			message: "User associated with API key not found",
			code:    "401",
			status:  http.StatusUnauthorized,
		},
		{
			name:    "google inactive local API key user",
			errType: "api_error",
			message: "User account is not active",
			code:    "401",
			status:  http.StatusUnauthorized,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rec := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(rec)

			errType := opscore.NormalizeErrorType(tt.errType, tt.code)
			phase, isBusinessLimited, errorOwner, errorSource := classifyOpsErrorLog(c, errType, tt.message, tt.code, tt.status)

			wantErrType := "api_error"
			if tt.errType == "permission_error" {
				wantErrType = "permission_error"
			}
			require.Equal(t, wantErrType, errType)
			require.Equal(t, "auth", phase)
			require.True(t, isBusinessLimited)
			require.Equal(t, "client", errorOwner)
			require.Equal(t, "client_request", errorSource)
		})
	}
}

// TestClassifyOpsClientHTTPAuthStatusesUseQueryTimeExclusion supplies generic
// local 401 and 403 failures. It requires a false business-limit flag and
// classifications other than provider ownership or upstream_http source; query
// exclusion remains outside these assertions.
func TestClassifyOpsClientHTTPAuthStatusesUseQueryTimeExclusion(t *testing.T) {
	tests := []struct {
		name    string // named fixture case label
		errType string // supplied client error category
		message string // supplied client failure message
		code    string // supplied client error code
		status  int    // supplied client HTTP status
	}{
		{
			name:    "generic local unauthorized",
			errType: "api_error",
			message: "Unauthorized",
			code:    "401",
			status:  http.StatusUnauthorized,
		},
		{
			name:    "generic local forbidden",
			errType: "forbidden_error",
			message: "Forbidden",
			code:    "403",
			status:  http.StatusForbidden,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rec := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(rec)

			errType := opscore.NormalizeErrorType(tt.errType, tt.code)
			_, isBusinessLimited, errorOwner, errorSource := classifyOpsErrorLog(c, errType, tt.message, tt.code, tt.status)

			require.False(t, isBusinessLimited)
			require.NotEqual(t, "provider", errorOwner)
			require.NotEqual(t, "upstream_http", errorSource)
		})
	}
}

// TestClassifyOpsLocalBusinessLimitErrorsExcludedFromSLA supplies local
// funding, quota, feature and policy denials. Each case must retain the
// expected normalized type and phase while identifying client business
// restriction evidence.
func TestClassifyOpsLocalBusinessLimitErrorsExcludedFromSLA(t *testing.T) {
	tests := []struct {
		name        string // named fixture case label
		errType     string // supplied client error category
		message     string // supplied client failure message
		code        string // supplied client error code
		status      int    // supplied client HTTP status
		wantErrType string // expected normalized error category
		wantPhase   string // expected classified failure phase
	}{
		{
			name:        "standard API key quota exhausted",
			errType:     "api_error",
			message:     "API key 额度已用完",
			code:        "API_KEY_QUOTA_EXHAUSTED",
			status:      http.StatusTooManyRequests,
			wantErrType: "api_error",
			wantPhase:   "request",
		},
		{
			name:        "standard query API key deprecated",
			errType:     "api_error",
			message:     "API key in query parameter is deprecated. Please use Authorization header instead.",
			code:        "api_key_in_query_deprecated",
			status:      http.StatusBadRequest,
			wantErrType: "api_error",
			wantPhase:   "request",
		},
		{
			name:        "google query API key deprecated",
			errType:     "api_error",
			message:     "Query parameter api_key is deprecated. Use Authorization header or key instead.",
			code:        "400",
			status:      http.StatusBadRequest,
			wantErrType: "api_error",
			wantPhase:   "request",
		},
		{
			name:        "google no active subscription",
			errType:     "api_error",
			message:     "No active subscription found for this group",
			code:        "403",
			status:      http.StatusForbidden,
			wantErrType: "api_error",
			wantPhase:   "request",
		},
		{
			name:        "gateway subscription invalid cache recheck",
			errType:     "billing_error",
			message:     "subscription is invalid or expired",
			code:        "billing_error",
			status:      http.StatusForbidden,
			wantErrType: "billing_error",
			wantPhase:   "request",
		},
		{
			name:        "google insufficient account balance",
			errType:     "api_error",
			message:     "Insufficient account balance",
			code:        "403",
			status:      http.StatusForbidden,
			wantErrType: "api_error",
			wantPhase:   "request",
		},
		{
			name:        "gateway billing cache insufficient balance",
			errType:     "billing_error",
			message:     "insufficient balance",
			code:        "",
			status:      http.StatusForbidden,
			wantErrType: "billing_error",
			wantPhase:   "request",
		},
		{
			name:        "gemini group platform mismatch",
			errType:     "api_error",
			message:     "API key group platform is not gemini",
			code:        "400",
			status:      http.StatusBadRequest,
			wantErrType: "api_error",
			wantPhase:   "request",
		},
		{
			name:        "gateway API key 5h rate limit",
			errType:     "api_error",
			message:     "api key 5小时限额已用完",
			code:        "rate_limit_exceeded",
			status:      http.StatusTooManyRequests,
			wantErrType: "api_error",
			wantPhase:   "request",
		},
		{
			name:        "gateway group RPM limit",
			errType:     "api_error",
			message:     "group requests-per-minute limit exceeded",
			code:        "rate_limit_exceeded",
			status:      http.StatusTooManyRequests,
			wantErrType: "api_error",
			wantPhase:   "request",
		},
		{
			name:        "google subscription daily limit",
			errType:     "api_error",
			message:     "daily usage limit exceeded",
			code:        "429",
			status:      http.StatusTooManyRequests,
			wantErrType: "api_error",
			wantPhase:   "request",
		},
		{
			name:        "user platform daily quota exhausted",
			errType:     "api_error",
			message:     "Daily usage quota exhausted for this platform.",
			code:        "rate_limit_exceeded",
			status:      http.StatusTooManyRequests,
			wantErrType: "api_error",
			wantPhase:   "request",
		},
		{
			name:        "local pending queue limit",
			errType:     "rate_limit_error",
			message:     "Too many pending requests, please retry later",
			code:        "",
			status:      http.StatusTooManyRequests,
			wantErrType: "rate_limit_error",
			wantPhase:   "request",
		},
		{
			name:        "local concurrency limit",
			errType:     "rate_limit_error",
			message:     "Concurrency limit exceeded for user, please retry later",
			code:        "",
			status:      http.StatusTooManyRequests,
			wantErrType: "rate_limit_error",
			wantPhase:   "request",
		},
		{
			name:        "group claude code only feature gate",
			errType:     "permission_error",
			message:     "This group is restricted to Claude Code clients (/v1/messages only)",
			code:        "",
			status:      http.StatusForbidden,
			wantErrType: "permission_error",
			wantPhase:   "request",
		},
		{
			name:        "group image generation feature gate",
			errType:     "permission_error",
			message:     "Image generation is not enabled for this group",
			code:        "",
			status:      http.StatusForbidden,
			wantErrType: "permission_error",
			wantPhase:   "request",
		},
		{
			name:        "group reasoning effort over limit deny",
			errType:     "permission_error",
			message:     `reasoning effort "high" exceeds this group's limit of "low"`,
			code:        "",
			status:      http.StatusForbidden,
			wantErrType: "permission_error",
			wantPhase:   "request",
		},
		{
			name:        "route token counting platform unsupported",
			errType:     "not_found_error",
			message:     "Token counting is not supported for this platform",
			code:        "",
			status:      http.StatusNotFound,
			wantErrType: "not_found_error",
			wantPhase:   "request",
		},
		{
			name:        "route images API platform unsupported",
			errType:     "not_found_error",
			message:     "Images API is not supported for this platform",
			code:        "",
			status:      http.StatusNotFound,
			wantErrType: "not_found_error",
			wantPhase:   "request",
		},
		{
			name:        "antigravity model whitelist feature gate",
			errType:     "permission_error",
			message:     "model claude-3-5-sonnet not in whitelist",
			code:        "",
			status:      http.StatusForbidden,
			wantErrType: "permission_error",
			wantPhase:   "request",
		},
		{
			name:        "google antigravity model whitelist feature gate",
			errType:     "api_error",
			message:     "model gemini-2.5-pro not in whitelist",
			code:        "403",
			status:      http.StatusForbidden,
			wantErrType: "api_error",
			wantPhase:   "request",
		},
		{
			name:        "openai group model unsupported feature gate",
			errType:     "permission_error",
			message:     `The current group does not support the requested model "o1-preview". Available models: gpt-5.4, gpt-5.4-mini`,
			code:        "",
			status:      http.StatusForbidden,
			wantErrType: "api_error",
			wantPhase:   "request",
		},
		{
			name:        "claude beta policy block",
			errType:     "invalid_request_error",
			message:     "beta feature interleaved-thinking-2025-05-14 is not allowed",
			code:        "",
			status:      http.StatusBadRequest,
			wantErrType: "invalid_request_error",
			wantPhase:   "request",
		},
		{
			name:        "openai fast policy block",
			errType:     "permission_error",
			message:     "openai service_tier=priority is not allowed for model gpt-5.5",
			code:        "",
			status:      http.StatusForbidden,
			wantErrType: "permission_error",
			wantPhase:   "request",
		},
		{
			name:        "codex official client policy block",
			errType:     "forbidden_error",
			message:     "This provider only allows Codex official clients",
			code:        "",
			status:      http.StatusForbidden,
			wantErrType: "forbidden_error",
			wantPhase:   "request",
		},
		{
			name:        "tls router matched-only policy block",
			errType:     "forbidden_error",
			message:     "This provider only allows clients matched by the configured TLS router",
			code:        "",
			status:      http.StatusForbidden,
			wantErrType: "forbidden_error",
			wantPhase:   "request",
		},
		{
			name:        "session isolation conflict",
			errType:     "permission_error",
			message:     session.SessionIsolationConflictMessage,
			code:        "",
			status:      http.StatusForbidden,
			wantErrType: "api_error",
			wantPhase:   "request",
		},
		{
			name:        "openai wsv1 unsupported feature gate",
			errType:     "invalid_request_error",
			message:     "OpenAI WSv1 is temporarily unsupported. Please enable responses_websockets_v2.",
			code:        "",
			status:      http.StatusBadRequest,
			wantErrType: "invalid_request_error",
			wantPhase:   "request",
		},
		{
			name:        "openai passthrough instructions policy block",
			errType:     "forbidden_error",
			message:     "OpenAI codex passthrough requires a non-empty instructions field",
			code:        "",
			status:      http.StatusForbidden,
			wantErrType: "forbidden_error",
			wantPhase:   "request",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rec := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(rec)

			errType := opscore.NormalizeErrorType(tt.errType, tt.code, tt.message)
			phase, isBusinessLimited, errorOwner, errorSource := classifyOpsErrorLog(c, errType, tt.message, tt.code, tt.status)

			require.Equal(t, tt.wantErrType, errType)
			require.Equal(t, tt.wantPhase, phase)
			require.True(t, isBusinessLimited)
			require.Equal(t, "client", errorOwner)
			require.Equal(t, "client_request", errorSource)
		})
	}
}

// TestClassifyOpsIPRestrictionAccessDeniedExcludedFromSLA combines access
// denial with a Key IP-restriction marker. Classification must retain a client
// business restriction for that local authorization failure.
func TestClassifyOpsIPRestrictionAccessDeniedExcludedFromSLA(t *testing.T) {
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	MarkOpsClientBusinessLimited(c, OpsClientBusinessLimitedReasonIPRestriction)

	errType := opscore.NormalizeErrorType("api_error", "ACCESS_DENIED")
	phase, isBusinessLimited, errorOwner, errorSource := classifyOpsErrorLog(c, errType, "Access denied", "ACCESS_DENIED", http.StatusForbidden)

	require.Equal(t, "api_error", errType)
	require.Equal(t, "auth", phase)
	require.True(t, isBusinessLimited)
	require.Equal(t, "client", errorOwner)
	require.Equal(t, "client_request", errorSource)
}

// TestClassifyOpsClientBusinessLimitedMarkerExcludesCustomPolicyDenialFromSLA
// uses custom denial text with a client restriction marker. That marker must
// supply business-limit evidence even when the message is outside built-in
// matching spellings.
func TestClassifyOpsClientBusinessLimitedMarkerExcludesCustomPolicyDenialFromSLA(t *testing.T) {
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	MarkOpsClientBusinessLimited(c, OpsClientBusinessLimitedReasonLocalPolicyDenied)

	errType := opscore.NormalizeErrorType("invalid_request_error", "")
	phase, isBusinessLimited, errorOwner, errorSource := classifyOpsErrorLog(c, errType, "custom admin policy message", "", http.StatusBadRequest)

	require.Equal(t, "invalid_request_error", errType)
	require.Equal(t, "auth", phase)
	require.True(t, isBusinessLimited)
	require.Equal(t, "client", errorOwner)
	require.Equal(t, "client_request", errorSource)
}

// TestClassifyOpsUpstreamInvalidRequest400ExcludedFromSLA supplies the
// property-name-length code with both upstream and client 400 status. This
// specific exception must classify as a client request restriction even though
// upstream response evidence exists.
func TestClassifyOpsUpstreamInvalidRequest400ExcludedFromSLA(t *testing.T) {
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	SetOpsUpstreamError(c, http.StatusBadRequest, "Invalid property name in input arguments", "")

	errType := opscore.NormalizeErrorType("invalid_request_error", "property_name_above_max_length")
	phase, isBusinessLimited, errorOwner, errorSource := classifyOpsErrorLog(
		c,
		errType,
		"Invalid property name in input arguments",
		"property_name_above_max_length",
		http.StatusBadRequest,
	)

	require.Equal(t, "request", phase)
	require.True(t, isBusinessLimited)
	require.Equal(t, "client", errorOwner)
	require.Equal(t, "client_request", errorSource)
}

// TestClassifyOpsUpstreamServerErrorStillCountsForSLA supplies upstream and
// client 502 evidence. Classification must retain provider ownership and leave
// that server failure outside the client business restriction category.
func TestClassifyOpsUpstreamServerErrorStillCountsForSLA(t *testing.T) {
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	SetOpsUpstreamError(c, http.StatusBadGateway, "Upstream request failed", "")

	phase, isBusinessLimited, errorOwner, errorSource := classifyOpsErrorLog(
		c,
		"upstream_error",
		"Upstream service temporarily unavailable",
		"",
		http.StatusBadGateway,
	)

	require.Equal(t, "upstream", phase)
	require.False(t, isBusinessLimited)
	require.Equal(t, "provider", errorOwner)
	require.Equal(t, "upstream_http", errorSource)
}

// TestClassifyOpsOtherUpstreamInvalidRequestStillCountsForSLA supplies an
// ordinary upstream invalid request outside the property-name exception. The
// provider failure must remain eligible for SLA accounting despite
// client-facing status 400.
func TestClassifyOpsOtherUpstreamInvalidRequestStillCountsForSLA(t *testing.T) {
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	SetOpsUpstreamError(c, http.StatusBadRequest, "Unknown parameter", "")

	phase, isBusinessLimited, errorOwner, errorSource := classifyOpsErrorLog(
		c,
		"invalid_request_error",
		"Unknown parameter: input[0].namespace",
		"unknown_parameter",
		http.StatusBadRequest,
	)

	require.Equal(t, "upstream", phase)
	require.False(t, isBusinessLimited)
	require.Equal(t, "provider", errorOwner)
	require.Equal(t, "upstream_http", errorSource)
}

// TestClassifyOpsOtherErrorsStillCountForSLA classifies an ordinary internal
// server error without client restriction evidence. The result must remain a
// counted failure with its server attribution.
func TestClassifyOpsOtherErrorsStillCountForSLA(t *testing.T) {
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)

	errType := opscore.NormalizeErrorType("api_error", "INTERNAL_ERROR")
	phase, isBusinessLimited, errorOwner, errorSource := classifyOpsErrorLog(c, errType, "Failed to validate API key", "INTERNAL_ERROR", http.StatusInternalServerError)

	require.Equal(t, "api_error", errType)
	require.Equal(t, "internal", phase)
	require.False(t, isBusinessLimited)
	require.Equal(t, "platform", errorOwner)
	require.Equal(t, "gateway", errorSource)
}

// TestClassifyOpsUnsupportedModelExcludedFromSLA marks a locally unsupported
// model failure. Classification must identify routing and business restriction
// evidence rather than a provider inference failure.
func TestClassifyOpsUnsupportedModelExcludedFromSLA(t *testing.T) {
	tests := []string{
		"No available providers: no available providers supporting model: made-up-model",
		"No available providers: no available OpenAI providers supporting model: made-up-model",
		"No available Gemini providers: no available Gemini providers supporting model: made-up-model",
		"No available providers: no available providers supporting model: made-up-model (group model restriction)",
	}

	for _, message := range tests {
		t.Run(message, func(t *testing.T) {
			rec := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(rec)
			MarkOpsRoutingCapacityLimited(c)

			errType := opscore.NormalizeErrorType("api_error", "")
			phase, isBusinessLimited, errorOwner, errorSource := classifyOpsErrorLog(c, errType, message, "", http.StatusServiceUnavailable)

			require.Equal(t, "api_error", errType)
			require.Equal(t, "routing", phase)
			require.True(t, isBusinessLimited)
			require.Equal(t, "platform", errorOwner)
			require.Equal(t, "gateway", errorSource)
		})
	}
}

// TestClassifyOpsUnmarkedNoAvailableTextStillCountsForSLA uses no-provider text
// without the routing-capacity marker. Text alone must not label the failure
// business limited for SLA exclusion.
func TestClassifyOpsUnmarkedNoAvailableTextStillCountsForSLA(t *testing.T) {
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)

	phase, isBusinessLimited, errorOwner, errorSource := classifyOpsErrorLog(
		c,
		"api_error",
		"No available providers",
		"",
		http.StatusServiceUnavailable,
	)

	require.Equal(t, "routing", phase)
	require.False(t, isBusinessLimited)
	require.Equal(t, "platform", errorOwner)
	require.Equal(t, "gateway", errorSource)
}

// TestClassifyOpsUpstreamAuthTextStillCountsForSLA supplies upstream status
// evidence with authentication or local-looking denial text. Classification
// must retain provider failure ownership rather than treating those messages as
// local client restrictions.
func TestClassifyOpsUpstreamAuthTextStillCountsForSLA(t *testing.T) {
	tests := []struct {
		name    string // named fixture case label
		message string // supplied client failure message
		code    string // supplied client error code
		status  int    // supplied client HTTP status
	}{
		{
			name:    "invalid API key",
			message: "Invalid API key",
			code:    "401",
			status:  http.StatusUnauthorized,
		},
		{
			name:    "generic upstream unauthorized",
			message: "Unauthorized",
			code:    "401",
			status:  http.StatusUnauthorized,
		},
		{
			name:    "generic upstream forbidden",
			message: "Forbidden",
			code:    "403",
			status:  http.StatusForbidden,
		},
		{
			name:    "disabled API key",
			message: "API key is disabled",
			code:    "API_KEY_DISABLED",
			status:  http.StatusUnauthorized,
		},
		{
			name:    "gemini group platform mismatch",
			message: "API key group platform is not gemini",
			code:    "400",
			status:  http.StatusBadRequest,
		},
		{
			name:    "provider balance error",
			message: "Insufficient account balance",
			code:    "INSUFFICIENT_BALANCE",
			status:  http.StatusForbidden,
		},
		{
			name:    "provider subscription error",
			message: "No active subscription found for this group",
			code:    "SUBSCRIPTION_NOT_FOUND",
			status:  http.StatusForbidden,
		},
		{
			name:    "provider quota error",
			message: "api key 额度已用完",
			code:    "API_KEY_QUOTA_EXHAUSTED",
			status:  http.StatusTooManyRequests,
		},
		{
			name:    "provider deleted group shaped error",
			message: "API Key 所属分组已删除",
			code:    "GROUP_DELETED",
			status:  http.StatusForbidden,
		},
		{
			name:    "provider unassigned group shaped error",
			message: "API Key is not assigned to any group and cannot be used. Please contact the administrator to assign it to a group.",
			code:    "403",
			status:  http.StatusForbidden,
		},
		{
			name:    "provider local quota shaped error",
			message: "Daily usage quota exhausted for this platform.",
			code:    "rate_limit_exceeded",
			status:  http.StatusTooManyRequests,
		},
		{
			name:    "provider feature gate shaped error",
			message: "Image generation is not enabled for this group",
			code:    "403",
			status:  http.StatusForbidden,
		},
		{
			name:    "provider reasoning effort over limit shaped error",
			message: `reasoning effort "high" exceeds this group's limit of "low"`,
			code:    "403",
			status:  http.StatusForbidden,
		},
		{
			name:    "provider token counting unsupported shaped error",
			message: "Token counting is not supported for this platform",
			code:    "404",
			status:  http.StatusNotFound,
		},
		{
			name:    "provider image API unsupported shaped error",
			message: "Images API is not supported for this platform",
			code:    "404",
			status:  http.StatusNotFound,
		},
		{
			name:    "provider antigravity whitelist shaped error",
			message: "model claude-3-5-sonnet not in whitelist",
			code:    "403",
			status:  http.StatusForbidden,
		},
		{
			name:    "provider group model unsupported shaped error",
			message: `The current group does not support the requested model "o1-preview". Available models: gpt-5.4, gpt-5.4-mini`,
			code:    "403",
			status:  http.StatusForbidden,
		},
		{
			name:    "provider beta policy shaped error",
			message: "beta feature interleaved-thinking-2025-05-14 is not allowed",
			code:    "400",
			status:  http.StatusBadRequest,
		},
		{
			name:    "provider openai fast policy shaped error",
			message: "openai service_tier=priority is not allowed for model gpt-5.5",
			code:    "403",
			status:  http.StatusForbidden,
		},
		{
			name:    "provider codex client policy shaped error",
			message: "This provider only allows Codex official clients",
			code:    "403",
			status:  http.StatusForbidden,
		},
		{
			name:    "provider tls router policy shaped error",
			message: "This provider only allows clients matched by the configured TLS router",
			code:    "403",
			status:  http.StatusForbidden,
		},
		{
			name:    "provider session isolation shaped error",
			message: session.SessionIsolationConflictMessage,
			code:    "403",
			status:  http.StatusForbidden,
		},
		{
			name:    "provider wsv1 unsupported shaped error",
			message: "OpenAI WSv1 is temporarily unsupported. Please enable responses_websockets_v2.",
			code:    "400",
			status:  http.StatusBadRequest,
		},
		{
			name:    "provider passthrough instructions shaped error",
			message: "OpenAI codex passthrough requires a non-empty instructions field",
			code:    "403",
			status:  http.StatusForbidden,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rec := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(rec)
			SetOpsUpstreamError(c, tt.status, tt.message, "")

			phase, isBusinessLimited, errorOwner, errorSource := classifyOpsErrorLog(
				c,
				"api_error",
				tt.message,
				tt.code,
				tt.status,
			)

			require.Equal(t, "upstream", phase)
			require.False(t, isBusinessLimited)
			require.Equal(t, "provider", errorOwner)
			require.Equal(t, "upstream_http", errorSource)
		})
	}
}

// TestClassifyOpsUpstreamNoAvailableTextStillCountsForSLA combines no-provider
// wording with real upstream failure evidence. Classification must keep
// upstream ownership and leave the failure counted for SLA.
func TestClassifyOpsUpstreamNoAvailableTextStillCountsForSLA(t *testing.T) {
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	SetOpsUpstreamError(c, http.StatusServiceUnavailable, "No available providers", "")

	phase, isBusinessLimited, errorOwner, errorSource := classifyOpsErrorLog(
		c,
		"api_error",
		"No available providers",
		"",
		http.StatusServiceUnavailable,
	)

	require.Equal(t, "upstream", phase)
	require.False(t, isBusinessLimited)
	require.Equal(t, "provider", errorOwner)
	require.Equal(t, "upstream_http", errorSource)
}

// TestParseOpsErrorResponsePreservesNestedStringCode parses a nested gateway
// error containing a string code. The extracted metadata must retain that exact
// code alongside its error category and message.
func TestParseOpsErrorResponsePreservesNestedStringCode(t *testing.T) {
	parsed := opscore.ParseOpsErrorResponse([]byte(`{"error":{"type":"permission_error","code":"GROUP_DELETED","message":"API Key 所属分组已删除"}}`))

	require.Equal(t, "permission_error", parsed.ErrorType)
	require.Equal(t, "GROUP_DELETED", parsed.Code)
	require.Equal(t, "API Key 所属分组已删除", parsed.Message)
}

// TestParseOpsErrorResponsePreservesStructuredTopLevelSemantics parses
// top-level and string-error response forms. Each case must extract its
// supported type, scalar code and message instead of reducing structured
// evidence to raw body text.
func TestParseOpsErrorResponsePreservesStructuredTopLevelSemantics(t *testing.T) {
	tests := []struct {
		name     string // named fixture case label
		body     string // supplied encoded error response
		wantType string // expected extracted error category
		wantCode string // expected extracted error code
		wantMsg  string // expected extracted failure message
	}{
		{
			name:     "model not found",
			body:     `{"type":"model_not_found","code":404,"message":"model unavailable"}`,
			wantType: "model_not_found",
			wantCode: "404",
			wantMsg:  "model unavailable",
		},
		{
			name:     "string error",
			body:     `{"type":"service_unavailable","code":"temporarily_unavailable","error":"capacity exhausted"}`,
			wantType: "service_unavailable",
			wantCode: "temporarily_unavailable",
			wantMsg:  "capacity exhausted",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			parsed := opscore.ParseOpsErrorResponse([]byte(tt.body))
			require.Equal(t, tt.wantType, opscore.NormalizeErrorType(parsed.ErrorType, parsed.Code))
			require.Equal(t, tt.wantCode, parsed.Code)
			require.Equal(t, tt.wantMsg, parsed.Message)
		})
	}
}

// TestApplyOpsUpstreamFieldsUsesLastNonNilAttempt combines stale context
// diagnostics with an ordered attempt list containing nil entries. The final
// nonnil attempt must provide status, message and detail while the complete
// list remains retained.
func TestApplyOpsUpstreamFieldsUsesLastNonNilAttempt(t *testing.T) {
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	SetOpsUpstreamError(c, http.StatusUnauthorized, "stale context", "stale detail")
	c.Set(OpsUpstreamErrorsKey, []*opscore.OpsUpstreamErrorEvent{
		{UpstreamStatusCode: http.StatusTooManyRequests, Message: "first attempt", Detail: "first detail"},
		nil,
		{UpstreamStatusCode: http.StatusServiceUnavailable, Message: "final attempt", Detail: "final detail"},
		nil,
	})
	entry := &opscore.OpsInsertErrorLogInput{}

	applyOpsUpstreamFieldsFromContext(c, entry)

	require.NotNil(t, entry.UpstreamStatusCode)
	require.Equal(t, http.StatusServiceUnavailable, *entry.UpstreamStatusCode)
	require.NotNil(t, entry.UpstreamErrorMessage)
	require.Equal(t, "final attempt", *entry.UpstreamErrorMessage)
	require.NotNil(t, entry.UpstreamErrorDetail)
	require.Equal(t, "final detail", *entry.UpstreamErrorDetail)
	require.Len(t, entry.UpstreamErrors, 4)
}

// TestApplyOpsUpstreamFieldsFinalStatuslessAttemptClearsStaleContext ends with
// a statusless transport failure after an earlier response. Its message and
// detail must replace prior evidence while the upstream status becomes absent.
func TestApplyOpsUpstreamFieldsFinalStatuslessAttemptClearsStaleContext(t *testing.T) {
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	SetOpsUpstreamError(c, http.StatusBadGateway, "stale response", "stale body")
	c.Set(OpsUpstreamErrorsKey, []*opscore.OpsUpstreamErrorEvent{
		{UpstreamStatusCode: http.StatusBadGateway, Message: "first response"},
		{Kind: "request_error", Message: "final transport failure", Detail: "connection reset"},
	})
	entry := &opscore.OpsInsertErrorLogInput{}

	applyOpsUpstreamFieldsFromContext(c, entry)

	require.Nil(t, entry.UpstreamStatusCode)
	require.NotNil(t, entry.UpstreamErrorMessage)
	require.Equal(t, "final transport failure", *entry.UpstreamErrorMessage)
	require.NotNil(t, entry.UpstreamErrorDetail)
	require.Equal(t, "connection reset", *entry.UpstreamErrorDetail)
}

// TestOpsCaptureWriter_ProtocolLevelTerminalFrameDetection splits a padded
// event field and payload across CRLF writes. Protocol parsing must recover the
// terminal code, message and logical service-unavailable status.
func TestOpsCaptureWriter_ProtocolLevelTerminalFrameDetection(t *testing.T) {
	state := &opsCaptureWriterState{limit: opsCaptureWriterLimit}
	chunks := []string{
		"event : response.failed\r\n",
		"data: { \"response\" : { \"error\" : { \"message\" : \"busy\", \"code\" : \"service_unavailable\" } },",
		" \"type\" : \"response.failed\" }\r\n\r\n",
	}
	for _, chunk := range chunks {
		state.captureResponseChunk([]byte(chunk), http.StatusOK)
	}

	parsed := opscore.ParseOpsErrorResponse(state.buf.Bytes())
	require.True(t, state.sseCapturing)
	require.True(t, parsed.StreamFailure)
	require.Equal(t, "service_unavailable_error", parsed.ErrorType)
	require.Equal(t, "service_unavailable", parsed.Code)
	require.Equal(t, "busy", parsed.Message)
	require.Equal(t, http.StatusServiceUnavailable, opscore.InferStreamFailureStatus(parsed))
}

// TestParseOpsSSEFailure_TopLevelErrorsAndUnknownStatus supplies flattened
// terminal errors, unknown codes and an explicit status. Inference must use
// terminal evidence for each expected status despite stale unauthorized
// context.
func TestParseOpsSSEFailure_TopLevelErrorsAndUnknownStatus(t *testing.T) {
	tests := []struct {
		name       string // named fixture case label
		body       string // supplied encoded error response
		wantType   string // expected extracted error category
		wantStatus int    // expected inferred HTTP status
	}{
		{
			name:       "top-level permission",
			body:       "event: error\ndata: {\"message\":\"denied\",\"code\":\"permission_denied\",\"type\":\"error\"}\n\n",
			wantType:   "permission_error",
			wantStatus: http.StatusForbidden,
		},
		{
			name:       "top-level unavailable",
			body:       "data: {\"message\":\"busy\",\"type\":\"error\",\"code\":\"service_unavailable\"}\n\n",
			wantType:   "service_unavailable_error",
			wantStatus: http.StatusServiceUnavailable,
		},
		{
			name:       "unknown terminal",
			body:       "event: response.failed\ndata: {\"type\":\"response.failed\",\"error\":{\"code\":\"new_provider_code\",\"message\":\"failed\"}}\n\n",
			wantType:   "upstream_error",
			wantStatus: http.StatusBadGateway,
		},
		{
			name:       "explicit terminal status",
			body:       "event: error\ndata: {\"type\":\"error\",\"status_code\":429,\"code\":\"new_rate_code\",\"message\":\"slow down\"}\n\n",
			wantType:   "api_error",
			wantStatus: http.StatusTooManyRequests,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			parsed := opscore.ParseOpsErrorResponse([]byte(tt.body))
			require.True(t, parsed.StreamFailure)
			require.Equal(t, tt.wantType, parsed.ErrorType)

			c, _ := gin.CreateTestContext(httptest.NewRecorder())
			SetOpsUpstreamError(c, http.StatusUnauthorized, "old attempt", "")
			require.Equal(t, tt.wantStatus, opscore.InferStreamFailureStatus(parsed), "terminal status must not inherit an earlier attempt")
		})
	}
}

// TestOpsCaptureWriter_OversizedNonTerminalFrameRemainsBounded writes an
// oversized successful data frame before a terminal error. Probe capacity must
// remain bounded, and the following valid failure must still activate capture.
func TestOpsCaptureWriter_OversizedNonTerminalFrameRemainsBounded(t *testing.T) {
	state := &opsCaptureWriterState{limit: opsCaptureWriterLimit}
	state.captureResponseChunk([]byte("data: "+strings.Repeat("x", opsTerminalSSEFrameProbeLimit*2)+"\n\n"), http.StatusOK)
	require.Empty(t, state.buf.Bytes())
	require.LessOrEqual(t, cap(state.probe), opsTerminalSSEFrameProbeLimit)

	state.captureResponseChunk([]byte("event: error\ndata: {\"type\":\"error\",\"code\":\"permission_denied\",\"message\":\"denied\"}\n\n"), http.StatusOK)
	require.True(t, state.sseCapturing)
	require.NotEmpty(t, state.buf.Bytes())
}

// TestOpsCaptureWriter_TerminalMetadataSurvivesBodyCaptureTruncation puts
// terminal details beyond the captured-body limit but inside the metadata
// probe. Known failure and its richer parsed category/message must survive the
// bounded diagnostic prefix.
func TestOpsCaptureWriter_TerminalMetadataSurvivesBodyCaptureTruncation(t *testing.T) {
	state := &opsCaptureWriterState{limit: opsCaptureWriterLimit}
	frame := "event: response.failed\ndata: {\"padding\":\"" + strings.Repeat("x", opsCaptureWriterLimit) + "\",\"type\":\"response.failed\",\"error\":{\"code\":\"service_unavailable\",\"message\":\"busy\"}}\n\n"
	state.captureResponseChunk([]byte(frame), http.StatusOK)
	state.finalizeResponseCapture()

	require.Len(t, state.buf.Bytes(), opsCaptureWriterLimit)
	require.True(t, opscore.ParseOpsErrorResponse(state.buf.Bytes()).StreamFailure, "the bounded parser must fail closed from the terminal event line")
	require.True(t, state.terminalFound)
	require.Equal(t, "service_unavailable_error", state.terminalError.ErrorType)
	require.Equal(t, "busy", state.terminalError.Message)
}

// TestOpsErrorLoggerMiddleware_LargeTerminalFrameUsesEventFallback writes a
// terminal event with oversized credential-bearing JSON. Queueing must retain
// generic logical failure and replace the truncated payload so the secret
// appears in neither message nor body.
func TestOpsErrorLoggerMiddleware_LargeTerminalFrameUsesEventFallback(t *testing.T) {
	setupOpsErrorLogTestQueue(t, 2)

	ops := newOpsServiceFixture(nil, nil)
	router := gin.New()
	router.Use(opsLoggerFixture(ops))
	router.POST("/v1/responses", func(c *gin.Context) {
		c.Status(http.StatusOK)
		_, _ = c.Writer.WriteString("event: response.failed\n")
		_, _ = c.Writer.WriteString("data: {\"authorization\":\"Bearer must-not-persist\",\"padding\":\"" + strings.Repeat("x", opsTerminalSSEFrameProbeLimit*2) + "\"}")
	})

	recorder := httptest.NewRecorder()
	router.ServeHTTP(recorder, httptest.NewRequest(http.MethodPost, "/v1/responses", nil))
	require.Equal(t, http.StatusOK, recorder.Code)
	require.Equal(t, int64(1), OpsErrorLogQueueLength())
	job := <-opsErrorLogQueue
	require.Equal(t, http.StatusBadGateway, job.entry.StatusCode)
	require.Equal(t, "upstream_error", job.entry.ErrorType)
	require.Equal(t, "upstream stream failed", job.entry.ErrorMessage)
	require.NotContains(t, job.entry.ErrorMessage, "must-not-persist")
	require.NotContains(t, job.entry.ErrorBody, "must-not-persist")
	require.Contains(t, job.entry.ErrorBody, `"payload_truncated":true`)
}

// TestOpsErrorLoggerMiddleware_DetectsTerminalDataAtEOFWithoutBlankLine ends a
// data-only terminal payload without a blank frame terminator. Middleware
// finalization must still queue its permission error and logical forbidden
// status.
func TestOpsErrorLoggerMiddleware_DetectsTerminalDataAtEOFWithoutBlankLine(t *testing.T) {
	setupOpsErrorLogTestQueue(t, 2)

	ops := newOpsServiceFixture(nil, nil)
	router := gin.New()
	router.Use(opsLoggerFixture(ops))
	router.POST("/v1/responses", func(c *gin.Context) {
		c.Status(http.StatusOK)
		_, _ = c.Writer.WriteString(`data: {"message":"denied","code":"permission_denied","type":"error"}`)
	})

	recorder := httptest.NewRecorder()
	router.ServeHTTP(recorder, httptest.NewRequest(http.MethodPost, "/v1/responses", nil))
	require.Equal(t, http.StatusOK, recorder.Code)
	require.Equal(t, int64(1), OpsErrorLogQueueLength())
	job := <-opsErrorLogQueue
	require.Equal(t, http.StatusForbidden, job.entry.StatusCode)
	require.Equal(t, "permission_error", job.entry.ErrorType)
	require.Equal(t, "denied", job.entry.ErrorMessage)
}

// TestOpsCaptureWriter_DetectsCROnlySSEFrame terminates a data-only error frame
// using carriage returns alone. Capture must recognize terminal evidence and
// extract the expected service-unavailable error category.
func TestOpsCaptureWriter_DetectsCROnlySSEFrame(t *testing.T) {
	state := &opsCaptureWriterState{limit: opsCaptureWriterLimit}
	state.captureResponseChunk([]byte("data: {\"type\":\"error\",\"code\":\"service_unavailable\",\"message\":\"busy\"}\r\r"), http.StatusOK)

	require.True(t, state.sseCapturing)
	parsed := opscore.ParseOpsErrorResponse(state.buf.Bytes())
	require.True(t, parsed.StreamFailure)
	require.Equal(t, "service_unavailable_error", parsed.ErrorType)
}

// TestSanitizeOpsSSEDataForPersistence_RedactsJSONFields splits one JSON
// payload across two SSE data lines containing credentials. Persistence
// sanitation must join and redact both direct authorization and nested API Key
// values.
func TestSanitizeOpsSSEDataForPersistence_RedactsJSONFields(t *testing.T) {
	body := []byte("event: error\ndata: {\"type\":\"error\",\"authorization\":\"Bearer secret\",\ndata: \"nested\":{\"api_key\":\"sk-secret\"}}\n\n")
	sanitized := opscore.SanitizeOpsSSEDataForPersistence(body)
	require.NotContains(t, sanitized, "Bearer secret")
	require.NotContains(t, sanitized, "sk-secret")
	require.Contains(t, sanitized, `"authorization":"[REDACTED]"`)
	require.Contains(t, sanitized, `"api_key":"[REDACTED]"`)
}

// TestSanitizeOpsSSEDataForPersistence_DropsTruncatedJSONFragment supplies an
// incomplete JSON payload containing authorization text. Sanitation must
// replace that fragment with the payload-truncated marker and remove the
// secret.
func TestSanitizeOpsSSEDataForPersistence_DropsTruncatedJSONFragment(t *testing.T) {
	body := []byte("event: error\ndata: {\"type\":\"error\",\"authorization\":\"Bearer leaked")
	sanitized := opscore.SanitizeOpsSSEDataForPersistence(body)
	require.NotContains(t, sanitized, "Bearer leaked")
	require.Contains(t, sanitized, `data: {"payload_truncated":true}`)
}

// BenchmarkOpsCaptureWriterSuccessfulSSEFrames repeatedly observes complete
// successful SSE frames and reports allocations. Its guard requires the
// final diagnostic body to stay empty after the successful-stream workload.
func BenchmarkOpsCaptureWriterSuccessfulSSEFrames(b *testing.B) {
	frame := []byte("event: response.output_text.delta\ndata: {\"type\":\"response.output_text.delta\",\"delta\":\"ok\"}\n\n")
	state := &opsCaptureWriterState{limit: opsCaptureWriterLimit}
	b.ReportAllocs()
	for range b.N {
		state.captureResponseChunk(frame, http.StatusOK)
	}
	if state.buf.Len() != 0 {
		b.Fatal("successful frames must not be captured")
	}
}

// TestSetOpsEndpointContext_SetsContextKeys stores an upstream model with a
// stream request-type value. The Gin observations must retain the supplied
// model string and the exact int16 request type.
func TestSetOpsEndpointContext_SetsContextKeys(t *testing.T) {
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/messages", nil)
	SetOpsEndpointContext(c, "claude-3-5-sonnet-20241022", int16(2)) // stream

	v, ok := c.Get(OpsUpstreamModelKey)
	require.True(t, ok)
	vStr, ok := v.(string)
	require.True(t, ok)
	require.Equal(t, "claude-3-5-sonnet-20241022", vStr)

	rt, ok := c.Get(opsRequestTypeKey)
	require.True(t, ok)
	rtVal, ok := rt.(int16)
	require.True(t, ok)
	require.Equal(t, int16(2), rtVal)
}

// TestSetOpsEndpointContext_EmptyModelNotStored supplies an empty model on a
// fresh context. The setter must leave model attribution absent while still
// publishing the int16 request type.
func TestSetOpsEndpointContext_EmptyModelNotStored(t *testing.T) {
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/messages", nil)
	SetOpsEndpointContext(c, "", int16(1))

	_, ok := c.Get(OpsUpstreamModelKey)
	require.False(t, ok, "empty upstream model should not be stored")

	rt, ok := c.Get(opsRequestTypeKey)
	require.True(t, ok)
	rtVal, ok := rt.(int16)
	require.True(t, ok)
	require.Equal(t, int16(1), rtVal)
}

// TestSetOpsEndpointContext_NilContext passes no context to endpoint
// observation setup. The nil guard must permit the call to return without a
// panic or request state.
func TestSetOpsEndpointContext_NilContext(t *testing.T) {
	require.NotPanics(t, func() {
		SetOpsEndpointContext(nil, "model", int16(1))
	})
}

// WriteString signals entry into one delegated fixture write and waits for its
// release channel. It then returns the embedded writer result; callers invoke
// it once because closing writeStarted again would panic.
func (w *blockingOpsResponseWriter) WriteString(s string) (int, error) {
	// Signal the delegated call before blocking it, allowing the test to probe the
	// capture mutex during this write and then start release while work remains
	// active.
	close(w.writeStarted)
	<-w.writeRelease
	return w.ResponseWriter.WriteString(s)
}

// Get removes the most recently returned fixture state, or allocates one when
// empty. Tests serialize pool access because this deterministic LIFO fixture
// has no mutex and is used to prove reuse of specific storage.
func (p *deterministicOpsCaptureWriterStatePool) Get() any {
	if len(p.states) == 0 {
		return &opsCaptureWriterState{limit: opsCaptureWriterLimit}
	}
	last := len(p.states) - 1
	state := p.states[last]
	p.states = p.states[:last]
	return state
}

// Put retains a nonnil capture-state pointer for deterministic fixture reuse.
// Other values are ignored; tests serialize mutation with reads and acquires,
// since this fixture intentionally supplies no concurrent pool synchronization.
func (p *deterministicOpsCaptureWriterStatePool) Put(value any) {
	if state, ok := value.(*opsCaptureWriterState); ok && state != nil {
		p.states = append(p.states, state)
	}
}

// TestOpsCaptureWriter_NilInnerWriter_NoPanic calls every supported writer
// operation on an inactive handle. It checks scalar and write-result defaults,
// nonnil fallback headers/channels and a Hijack error with no connection or
// buffered reader.
func TestOpsCaptureWriter_NilInnerWriter_NoPanic(t *testing.T) {
	w := &opsCaptureWriter{}

	assert.NotPanics(t, func() {
		assert.Equal(t, 0, w.Status())
	})
	assert.NotPanics(t, func() {
		assert.Equal(t, -1, w.Size())
	})
	assert.NotPanics(t, func() {
		assert.False(t, w.Written())
	})
	assert.NotPanics(t, func() {
		n, err := w.Write([]byte("test"))
		assert.Equal(t, 0, n)
		assert.NoError(t, err)
	})
	assert.NotPanics(t, func() {
		n, err := w.WriteString("test")
		assert.Equal(t, 0, n)
		assert.NoError(t, err)
	})
	assert.NotPanics(t, func() {
		h := w.Header()
		assert.NotNil(t, h)
	})
	assert.NotPanics(t, func() {
		w.WriteHeader(http.StatusOK)
	})
	assert.NotPanics(t, func() {
		w.WriteHeaderNow()
	})
	assert.NotPanics(t, func() {
		w.Flush()
	})
	assert.NotPanics(t, func() {
		conn, rw, err := w.Hijack()
		assert.Nil(t, conn)
		assert.Nil(t, rw)
		assert.Error(t, err)
	})
	assert.NotPanics(t, func() {
		ch := w.CloseNotify()
		assert.NotNil(t, ch)
	})
	assert.NotPanics(t, func() {
		p := w.Pusher()
		assert.Nil(t, p)
	})
}

// TestOpsCaptureWriter_StaleLeaseCannotReachReacquiredState reuses the same
// state through a fresh handle. The stale handle must neither write nor read
// the new request and must not return its active state to the pool.
func TestOpsCaptureWriter_StaleLeaseCannotReachReacquiredState(t *testing.T) {
	pool := &deterministicOpsCaptureWriterStatePool{}

	firstRecorder := httptest.NewRecorder()
	firstContext, _ := gin.CreateTestContext(firstRecorder)
	stale := acquireOpsCaptureWriterFromPool(pool, firstContext.Writer)
	releaseOpsCaptureWriter(stale)

	secondRecorder := httptest.NewRecorder()
	secondContext, _ := gin.CreateTestContext(secondRecorder)
	current := acquireOpsCaptureWriterFromPool(pool, secondContext.Writer)
	defer releaseOpsCaptureWriter(current)
	require.NotSame(t, stale, current)
	require.Same(t, stale.state, current.state)

	current.WriteHeader(http.StatusInternalServerError)
	_, err := current.WriteString("current")
	require.NoError(t, err)
	require.Equal(t, []byte("current"), current.capturedBytes())

	n, err := stale.WriteString("stale")
	require.NoError(t, err)
	require.Zero(t, n)
	require.Nil(t, stale.capturedBytes())
	require.Equal(t, []byte("current"), current.capturedBytes())
	require.NotContains(t, secondRecorder.Body.String(), "stale")

	// Releasing an older generation must leave the current request outside the
	// pool, so a third acquire cannot steal state still owned by that active handle.
	releaseOpsCaptureWriter(stale)
	thirdRecorder := httptest.NewRecorder()
	thirdContext, _ := gin.CreateTestContext(thirdRecorder)
	other := acquireOpsCaptureWriterFromPool(pool, thirdContext.Writer)
	defer releaseOpsCaptureWriter(other)
	require.NotSame(t, current.state, other.state)
}

// TestOpsCaptureWriter_ReleaseWaitsForDelegatedWriteWithoutHoldingStateMutex
// blocks a registered network write while starting release. The state mutex
// is probed during the blocked write. Pooling must wait until that write returns,
// and the completed release must then make one state available for reuse.
func TestOpsCaptureWriter_ReleaseWaitsForDelegatedWriteWithoutHoldingStateMutex(t *testing.T) {
	pool := &deterministicOpsCaptureWriterStatePool{}
	recorder := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(recorder)
	inner := &blockingOpsResponseWriter{
		ResponseWriter: ctx.Writer,
		writeStarted:   make(chan struct{}),
		writeRelease:   make(chan struct{}),
	}
	w := acquireOpsCaptureWriterFromPool(pool, inner)

	// Keep the delegated operation active behind its channel gate while probing
	// the state mutex, then start release before permitting the network write to
	// finish.
	writeDone := make(chan struct{})
	go func() {
		defer close(writeDone)
		_, _ = w.WriteString("body")
	}()
	<-inner.writeStarted

	if !w.state.mu.TryLock() {
		t.Fatal("state mutex remained held across the delegated network write")
	}
	w.state.mu.Unlock()

	// Release proceeds independently of the blocked writer; its completion channel
	// lets this test distinguish premature pooling from cleanup after the
	// registered operation joins.
	releaseDone := make(chan struct{})
	go func() {
		releaseOpsCaptureWriter(w)
		close(releaseDone)
	}()
	select {
	case <-releaseDone:
		t.Fatal("release returned while a delegated write was still active")
	case <-time.After(20 * time.Millisecond):
	}
	require.Empty(t, pool.states)

	close(inner.writeRelease)
	<-writeDone
	select {
	case <-releaseDone:
	case <-time.After(time.Second):
		t.Fatal("release did not finish after the delegated write returned")
	}
	require.Len(t, pool.states, 1)
}

// TestOpsClassificationTreatsCredentialFailureAsAuthNotInference ends an
// inference error list with a statusless credential failure. Classification
// must select provider authentication, clear old inference status/detail and
// retain both attempt records.
func TestOpsClassificationTreatsCredentialFailureAsAuthNotInference(t *testing.T) {
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Set(OpsUpstreamStatusCodeKey, http.StatusForbidden)
	c.Set(OpsUpstreamErrorMessageKey, "stale inference message")
	c.Set(OpsUpstreamErrorDetailKey, "stale inference detail")
	c.Set(OpsUpstreamErrorsKey, []*opscore.OpsUpstreamErrorEvent{
		{Stage: "inference", UpstreamStatusCode: http.StatusForbidden, Message: "stale inference message", Detail: "stale inference detail"},
		{
			Stage:              opscore.ErrorPhaseProviderAuth,
			Scope:              "provider",
			Reason:             "grok_oauth_credential_revoked",
			UpstreamStatusCode: 0,
			Message:            "Grok OAuth credentials require provider action",
		},
	})

	phase, _, owner, source := classifyOpsErrorLog(c, "upstream_error", "No healthy Grok OAuth provider is currently available", "", http.StatusServiceUnavailable)
	require.Equal(t, "provider_auth", phase)
	require.Equal(t, "provider", owner)
	require.Equal(t, "gateway", source)

	entry := &opscore.OpsInsertErrorLogInput{}
	applyOpsUpstreamFieldsFromContext(c, entry)
	require.NotNil(t, entry.UpstreamStatusCode)
	require.Zero(t, *entry.UpstreamStatusCode)
	require.NotNil(t, entry.UpstreamErrorMessage)
	require.Equal(t, "Grok OAuth credentials require provider action", *entry.UpstreamErrorMessage)
	require.Nil(t, entry.UpstreamErrorDetail)
	require.Len(t, entry.UpstreamErrors, 2)
	require.Equal(t, http.StatusForbidden, entry.UpstreamErrors[0].UpstreamStatusCode)
}

// TestOpsCaptureWriterDoesNotCopyIngressRejectBody marks ingress rejection
// before writing an authentication body. The write must return no error while
// the wrapper keeps its diagnostic buffer empty.
func TestOpsCaptureWriterDoesNotCopyIngressRejectBody(t *testing.T) {
	context, _ := gin.CreateTestContext(httptest.NewRecorder())
	writer := acquireOpsCaptureWriter(context.Writer)
	defer releaseOpsCaptureWriter(writer)
	writer.setContext(context, opsAccessFixture().Rejected)
	context.Writer = writer
	markOpsIngressRejectedFixture(context)
	context.Status(http.StatusUnauthorized)
	_, err := context.Writer.WriteString(`{"code":"INVALID_API_KEY","message":"Invalid API key"}`)
	require.NoError(t, err)
	require.Empty(t, writer.capturedBytes())
}

// TestKeyPrefix supplies long and short credential strings to byte-prefix
// extraction. The long key must be cut to eight bytes, while the shorter key
// remains whole.
func TestKeyPrefix(t *testing.T) {
	if got := keyPrefix("sk-3f2a9c7e", 8); got != "sk-3f2a9" {
		t.Errorf("keyPrefix=%q want %q", got, "sk-3f2a9")
	}
	if got := keyPrefix("abc", 8); got != "abc" {
		t.Errorf("short key should be returned as-is, got %q", got)
	}
}

// TestOpsErrorLoggerMiddleware_HeartbeatCommittedSSEFailureNotRecovered
// reproduces an upstream 502 after DownstreamSSEKeepalive commits HTTP 200
// text/event-stream on Messages. Ops must record a 502 stream failure and mark
// the request telemetry failed rather than logging Recovered upstream error.
func TestOpsErrorLoggerMiddleware_HeartbeatCommittedSSEFailureNotRecovered(t *testing.T) {
	setupOpsErrorLogTestQueue(t, 2)

	repo := &ingressRejectOpsRepo{}
	ops := newOpsServiceFixture(repo, nil)
	var observed telemetry.RequestRecord
	router := gin.New()
	router.Use(func(c *gin.Context) {
		ctx := telemetry.WithRequestCapture(c.Request.Context(), telemetry.RequestRecord{State: "completed"}, func(r telemetry.RequestRecord) {
			observed = r
		})
		c.Request = c.Request.WithContext(ctx)
		c.Next()
	})
	router.Use(opsLoggerFixture(ops))
	router.Use(TextRequestTiming(), DownstreamSSEKeepalive(5*time.Millisecond, time.Second))
	router.POST(EndpointMessages, func(c *gin.Context) {
		c.Request = c.Request.WithContext(requeststate.WithClientProtocol(c.Request.Context(), protocol.ProtocolAnthropicMessages))
		timing.StagesFromContext(c.Request.Context()).SetStream(true)
		w, ok := c.Writer.(*sseKeepaliveWriter)
		require.True(t, ok)
		w.openHeaderWait(c.Request.Context())
		require.Eventually(t, func() bool { return CommittedSSE(c) }, time.Second, time.Millisecond)

		AppendOpsUpstreamError(c, opscore.OpsUpstreamErrorEvent{
			UpstreamStatusCode: http.StatusBadGateway,
			Kind:               "failover",
			Message:            "Upstream transport failure",
		})
		DefaultOpenAIErrorOutput().WriteAnthropicStreamingError(c, http.StatusBadGateway, "api_error", "Upstream service temporarily unavailable", false)
	})

	recorder := httptest.NewRecorder()
	router.ServeHTTP(recorder, httptest.NewRequest(http.MethodPost, EndpointMessages, nil))

	require.Equal(t, http.StatusOK, recorder.Code)
	require.Contains(t, recorder.Body.String(), "event: ping\n")
	require.Contains(t, recorder.Body.String(), "event: error\n")
	require.Equal(t, "failed", observed.State)
	require.Equal(t, "stream_error", observed.ErrorCode)
	require.Equal(t, int64(1), OpsErrorLogQueueLength())

	job := <-opsErrorLogQueue
	flushOpsErrorLogBatch([]opsErrorLogJob{job})
	require.Equal(t, 1, repo.insertCalls)
	require.Len(t, repo.entries, 1)
	persisted := repo.entries[0]
	require.Equal(t, http.StatusBadGateway, persisted.StatusCode)
	require.NotContains(t, persisted.ErrorMessage, "Recovered upstream error")
	require.Equal(t, "Upstream service temporarily unavailable", persisted.ErrorMessage)
}
