package httpapi

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"

	"github.com/gin-gonic/gin"

	"github.com/TokenFlux/TokenRouter/internal/identity/httpapi/authctx"
	"github.com/TokenFlux/TokenRouter/internal/ops"
	opsprovider "github.com/TokenFlux/TokenRouter/internal/ops/provider"
)

// responseEnvelope decodes the application status and raw data from system-log
// HTTP fixture responses.
type responseEnvelope struct {
	Code    int             `json:"code"`    // application response status code
	Message string          `json:"message"` // application response message text
	Data    json.RawMessage `json:"data"`    // raw JSON payload
}

// opsSystemLogCaptureRepo retains filters submitted by handlers and accepts a
// synthetic cleanup result.
type opsSystemLogCaptureRepo struct {
	// OpsRepository remains nil for unimplemented methods. These handler fixtures
	// call only the overridden log methods after service setup.
	ops.OpsRepository // unused repository methods placeholder
	// listFilter retains the supplied pointer without cloning. The service can
	// normalize pagination before this fixture captures it for assertions.
	listFilter *ops.OpsSystemLogFilter // retained listing filter pointer
	// cleanupFilter retains the requested deletion filter without cloning. Tests
	// read it after the synchronous HTTP call completes.
	cleanupFilter *ops.OpsSystemLogCleanupFilter // retained deletion filter pointer
}

// ListSystemLogs retains the filter pointer and returns a new empty log list
// using its pagination values. It ignores context and succeeds, allowing HTTP
// tests to inspect forwarded host filtering.
func (r *opsSystemLogCaptureRepo) ListSystemLogs(_ context.Context, filter *ops.OpsSystemLogFilter) (*ops.OpsSystemLogList, error) {
	r.listFilter = filter
	return &ops.OpsSystemLogList{Logs: []*ops.OpsSystemLog{}, Page: filter.Page, PageSize: filter.PageSize}, nil
}

// DeleteSystemLogs retains the deletion filter pointer and reports one affected
// row. It ignores context and performs no deletion, letting tests inspect
// accepted cleanup fields after the handler returns.
func (r *opsSystemLogCaptureRepo) DeleteSystemLogs(_ context.Context, filter *ops.OpsSystemLogCleanupFilter) (int64, error) {
	r.cleanupFilter = filter
	return 1, nil
}

// InsertSystemLogCleanupAudit accepts fixture audit attempts successfully
// without retaining their input.
func (r *opsSystemLogCaptureRepo) InsertSystemLogCleanupAudit(_ context.Context, _ *ops.OpsSystemLogCleanupAudit) error {
	return nil
}

// newOpsSystemLogTestRouter registers log listing, cleanup, and health routes
// for fixtures. withUser inserts subject ID ninety-nine into Gin context
// directly, providing handler input without production authentication
// middleware.
func newOpsSystemLogTestRouter(handler *OpsHandler, withUser bool) *gin.Engine {
	r := gin.New()
	if withUser {
		r.Use(func(c *gin.Context) {
			c.Set(string(authctx.ContextKeyUser), authctx.AuthSubject{UserID: 99})
			c.Next()
		})
	}
	r.GET("/logs", handler.ListSystemLogs)
	r.POST("/logs/cleanup", handler.CleanupSystemLogs)
	r.GET("/logs/health", handler.GetSystemLogIngestionHealth)
	return r
}

// TestOpsSystemLogHandler_ListUnavailable serves listing through a handler with
// no Ops service. It checks this absent-service state produces an unavailable
// HTTP response for the listing request.
func TestOpsSystemLogHandler_ListUnavailable(t *testing.T) {
	h := NewOpsHandler(nil, OpsWSOptions{})
	r := newOpsSystemLogTestRouter(h, false)

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/logs", nil)
	r.ServeHTTP(w, req)
	if w.Code != http.StatusServiceUnavailable {
		t.Fatalf("status=%d, want 503", w.Code)
	}
}

// TestOpsSystemLogHandler_ListInvalidUserID supplies a nonnumeric user filter
// to an otherwise enabled fixture. It checks bad request occurs before the
// absent repository's empty result is returned.
func TestOpsSystemLogHandler_ListInvalidUserID(t *testing.T) {
	svc := ops.NewOpsService(nil, nil, nil, nil, nil, nil, nil, opsprovider.LogControl{})
	h := NewOpsHandler(svc, OpsWSOptions{})
	r := newOpsSystemLogTestRouter(h, false)

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/logs?user_id=abc", nil)
	r.ServeHTTP(w, req)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("status=%d, want 400", w.Code)
	}
}

// TestOpsSystemLogHandler_ListInvalidProviderID supplies a negative provider
// filter to an enabled service fixture. It checks the HTTP identity guard
// returns bad request before listing storage.
func TestOpsSystemLogHandler_ListInvalidProviderID(t *testing.T) {
	svc := ops.NewOpsService(nil, nil, nil, nil, nil, nil, nil, opsprovider.LogControl{})
	h := NewOpsHandler(svc, OpsWSOptions{})
	r := newOpsSystemLogTestRouter(h, false)

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/logs?provider_id=-1", nil)
	r.ServeHTTP(w, req)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("status=%d, want 400", w.Code)
	}
}

// TestOpsSystemLogHandler_ListInvalidAPIKeyID supplies a nonnumeric API-key
// filter to an enabled service fixture. It checks bad request occurs during
// parameter validation before the service's empty listing.
func TestOpsSystemLogHandler_ListInvalidAPIKeyID(t *testing.T) {
	svc := ops.NewOpsService(nil, nil, nil, nil, nil, nil, nil, opsprovider.LogControl{})
	h := NewOpsHandler(svc, OpsWSOptions{})
	r := newOpsSystemLogTestRouter(h, false)

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/logs?api_key_id=abc", nil)
	r.ServeHTTP(w, req)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("status=%d, want 400", w.Code)
	}
}

// TestOpsSystemLogHandler_ListMonitoringDisabled reads logs with the Ops hard
// switch disabled. It checks the monitoring guard returns not found before the
// ordinary list response path.
func TestOpsSystemLogHandler_ListMonitoringDisabled(t *testing.T) {
	svc := ops.NewOpsService(nil, nil, &ops.Options{Ops: ops.RuntimeOptions{Enabled: false}}, nil, nil, nil, nil, opsprovider.LogControl{})
	h := NewOpsHandler(svc, OpsWSOptions{})
	r := newOpsSystemLogTestRouter(h, false)

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/logs", nil)
	r.ServeHTTP(w, req)
	if w.Code != http.StatusNotFound {
		t.Fatalf("status=%d, want 404", w.Code)
	}
}

// TestOpsSystemLogHandler_ListSuccess sends a valid thirty-minute listing
// request to a service without storage. It checks HTTP success and application
// code zero for that service's empty listing response.
func TestOpsSystemLogHandler_ListSuccess(t *testing.T) {
	svc := ops.NewOpsService(nil, nil, nil, nil, nil, nil, nil, opsprovider.LogControl{})
	h := NewOpsHandler(svc, OpsWSOptions{})
	r := newOpsSystemLogTestRouter(h, false)

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/logs?time_range=30m&page=1&page_size=20", nil)
	r.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("status=%d, want 200", w.Code)
	}

	var resp responseEnvelope
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal response: %v", err)
	}
	if resp.Code != 0 {
		t.Fatalf("unexpected response code: %+v", resp)
	}
}

// TestOpsSystemLogHandler_ListAcceptsHost supplies a host filter through the
// HTTP listing route. It checks success and the same host reaches the
// repository's captured filter pointer.
func TestOpsSystemLogHandler_ListAcceptsHost(t *testing.T) {
	repo := &opsSystemLogCaptureRepo{}
	svc := ops.NewOpsService(repo, nil, nil, nil, nil, nil, nil, opsprovider.LogControl{})
	h := NewOpsHandler(svc, OpsWSOptions{})
	r := newOpsSystemLogTestRouter(h, false)

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/logs?host=api-node-1", nil)
	r.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("status=%d, want 200", w.Code)
	}
	if repo.listFilter == nil || repo.listFilter.Host != "api-node-1" {
		t.Fatalf("host filter = %+v, want api-node-1", repo.listFilter)
	}
}

// TestOpsSystemLogHandler_CleanupUnauthorized submits a filtered deletion body
// without a context subject. It checks that the supplied deletion request
// receives an unauthorized HTTP status in this fixture.
func TestOpsSystemLogHandler_CleanupUnauthorized(t *testing.T) {
	svc := ops.NewOpsService(nil, nil, nil, nil, nil, nil, nil, opsprovider.LogControl{})
	h := NewOpsHandler(svc, OpsWSOptions{})
	r := newOpsSystemLogTestRouter(h, false)

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/logs/cleanup", bytes.NewBufferString(`{"request_id":"r1"}`))
	req.Header.Set("Content-Type", "application/json")
	r.ServeHTTP(w, req)
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("status=%d, want 401", w.Code)
	}
}

// TestOpsSystemLogHandler_CleanupInvalidPayload gives a positive subject
// fixture malformed JSON. It checks the cleanup handler returns bad request
// during body binding before repository availability matters.
func TestOpsSystemLogHandler_CleanupInvalidPayload(t *testing.T) {
	svc := ops.NewOpsService(nil, nil, nil, nil, nil, nil, nil, opsprovider.LogControl{})
	h := NewOpsHandler(svc, OpsWSOptions{})
	r := newOpsSystemLogTestRouter(h, true)

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/logs/cleanup", bytes.NewBufferString(`{bad-json`))
	req.Header.Set("Content-Type", "application/json")
	r.ServeHTTP(w, req)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("status=%d, want 400", w.Code)
	}
}

// TestOpsSystemLogHandler_CleanupInvalidTime gives a subject-bearing fixture an
// invalid start timestamp and a request filter. It checks local timestamp
// parsing returns bad request before deletion.
func TestOpsSystemLogHandler_CleanupInvalidTime(t *testing.T) {
	svc := ops.NewOpsService(nil, nil, nil, nil, nil, nil, nil, opsprovider.LogControl{})
	h := NewOpsHandler(svc, OpsWSOptions{})
	r := newOpsSystemLogTestRouter(h, true)

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/logs/cleanup", bytes.NewBufferString(`{"start_time":"bad","request_id":"r1"}`))
	req.Header.Set("Content-Type", "application/json")
	r.ServeHTTP(w, req)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("status=%d, want 400", w.Code)
	}
}

// TestOpsSystemLogHandler_CleanupInvalidEndTime gives a subject-bearing fixture
// an invalid end timestamp and a request filter. It checks the endpoint returns
// bad request during local time parsing.
func TestOpsSystemLogHandler_CleanupInvalidEndTime(t *testing.T) {
	svc := ops.NewOpsService(nil, nil, nil, nil, nil, nil, nil, opsprovider.LogControl{})
	h := NewOpsHandler(svc, OpsWSOptions{})
	r := newOpsSystemLogTestRouter(h, true)

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/logs/cleanup", bytes.NewBufferString(`{"end_time":"bad","request_id":"r1"}`))
	req.Header.Set("Content-Type", "application/json")
	r.ServeHTTP(w, req)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("status=%d, want 400", w.Code)
	}
}

// TestOpsSystemLogHandler_CleanupServiceUnavailable submits valid filtered
// cleanup with a positive subject and no repository. It checks service
// unavailable reaches the HTTP response after local validation passes.
func TestOpsSystemLogHandler_CleanupServiceUnavailable(t *testing.T) {
	svc := ops.NewOpsService(nil, nil, nil, nil, nil, nil, nil, opsprovider.LogControl{})
	h := NewOpsHandler(svc, OpsWSOptions{})
	r := newOpsSystemLogTestRouter(h, true)

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/logs/cleanup", bytes.NewBufferString(`{"request_id":"r1"}`))
	req.Header.Set("Content-Type", "application/json")
	r.ServeHTTP(w, req)
	if w.Code != http.StatusServiceUnavailable {
		t.Fatalf("status=%d, want 503", w.Code)
	}
}

// TestOpsSystemLogHandler_CleanupAcceptsAPIKeyID submits a positive Key filter
// to a service without storage. It checks service unavailable rather than bad
// request, demonstrating the supplied ID passes the local guard.
func TestOpsSystemLogHandler_CleanupAcceptsAPIKeyID(t *testing.T) {
	svc := ops.NewOpsService(nil, nil, nil, nil, nil, nil, nil, opsprovider.LogControl{})
	h := NewOpsHandler(svc, OpsWSOptions{})
	r := newOpsSystemLogTestRouter(h, true)

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/logs/cleanup", bytes.NewBufferString(`{"api_key_id":123}`))
	req.Header.Set("Content-Type", "application/json")
	r.ServeHTTP(w, req)
	if w.Code != http.StatusServiceUnavailable {
		t.Fatalf("status=%d, want 503", w.Code)
	}
}

// TestOpsSystemLogHandler_CleanupAcceptsHost submits a host-only filter to an
// enabled repository fixture. It checks HTTP success and captures the host
// value forwarded to deletion.
func TestOpsSystemLogHandler_CleanupAcceptsHost(t *testing.T) {
	repo := &opsSystemLogCaptureRepo{}
	svc := ops.NewOpsService(repo, nil, nil, nil, nil, nil, nil, opsprovider.LogControl{})
	h := NewOpsHandler(svc, OpsWSOptions{})
	r := newOpsSystemLogTestRouter(h, true)

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/logs/cleanup", bytes.NewBufferString(`{"host":"api-node-1"}`))
	req.Header.Set("Content-Type", "application/json")
	r.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("status=%d, want 200", w.Code)
	}
	if repo.cleanupFilter == nil || repo.cleanupFilter.Host != "api-node-1" {
		t.Fatalf("host filter = %+v, want api-node-1", repo.cleanupFilter)
	}
}

// TestOpsSystemLogHandler_CleanupInvalidAPIKeyID submits zero as an explicit
// Key identity with a positive subject. It checks bad request occurs before the
// absent repository could report unavailable.
func TestOpsSystemLogHandler_CleanupInvalidAPIKeyID(t *testing.T) {
	svc := ops.NewOpsService(nil, nil, nil, nil, nil, nil, nil, opsprovider.LogControl{})
	h := NewOpsHandler(svc, OpsWSOptions{})
	r := newOpsSystemLogTestRouter(h, true)

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/logs/cleanup", bytes.NewBufferString(`{"api_key_id":0}`))
	req.Header.Set("Content-Type", "application/json")
	r.ServeHTTP(w, req)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("status=%d, want 400", w.Code)
	}
}

// TestOpsSystemLogHandler_CleanupMonitoringDisabled submits a cleanup request
// with a subject while Ops is hard-disabled. It checks that this disabled
// service state produces a not-found HTTP response for the cleanup request.
func TestOpsSystemLogHandler_CleanupMonitoringDisabled(t *testing.T) {
	svc := ops.NewOpsService(nil, nil, &ops.Options{Ops: ops.RuntimeOptions{Enabled: false}}, nil, nil, nil, nil, opsprovider.LogControl{})
	h := NewOpsHandler(svc, OpsWSOptions{})
	r := newOpsSystemLogTestRouter(h, true)

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/logs/cleanup", bytes.NewBufferString(`{"request_id":"r1"}`))
	req.Header.Set("Content-Type", "application/json")
	r.ServeHTTP(w, req)
	if w.Code != http.StatusNotFound {
		t.Fatalf("status=%d, want 404", w.Code)
	}
}

// TestOpsSystemLogHandler_Health binds a constructed system-log sink with host
// metadata to an Ops service. It checks the health endpoint returns HTTP
// success without asserting individual ingestion counters.
func TestOpsSystemLogHandler_Health(t *testing.T) {
	host, hostErr := os.Hostname()
	sink := ops.NewOpsSystemLogSink(nil, ops.SystemLogSinkOptions{Host: host, HostError: hostErr})
	svc := ops.NewOpsService(nil, nil, nil, nil, nil, nil, sink, opsprovider.LogControl{})
	h := NewOpsHandler(svc, OpsWSOptions{})
	r := newOpsSystemLogTestRouter(h, false)

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/logs/health", nil)
	r.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("status=%d, want 200", w.Code)
	}
}

// TestOpsSystemLogHandler_HealthUnavailableAndMonitoringDisabled covers an
// absent service and a hard-disabled service on separate routers. It checks
// health returns unavailable or not found according to that service state.
func TestOpsSystemLogHandler_HealthUnavailableAndMonitoringDisabled(t *testing.T) {
	h := NewOpsHandler(nil, OpsWSOptions{})
	r := newOpsSystemLogTestRouter(h, false)

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/logs/health", nil)
	r.ServeHTTP(w, req)
	if w.Code != http.StatusServiceUnavailable {
		t.Fatalf("status=%d, want 503", w.Code)
	}

	svc := ops.NewOpsService(nil, nil, &ops.Options{Ops: ops.RuntimeOptions{Enabled: false}}, nil, nil, nil, nil, opsprovider.LogControl{})
	h = NewOpsHandler(svc, OpsWSOptions{})
	r = newOpsSystemLogTestRouter(h, false)
	w = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodGet, "/logs/health", nil)
	r.ServeHTTP(w, req)
	if w.Code != http.StatusNotFound {
		t.Fatalf("status=%d, want 404", w.Code)
	}
}
