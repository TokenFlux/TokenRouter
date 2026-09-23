package httpapi

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"

	authctx "github.com/TokenFlux/TokenRouter/internal/identity/httpapi/authctx"

	opsprovider "github.com/TokenFlux/TokenRouter/internal/ops/provider"

	"github.com/TokenFlux/TokenRouter/internal/ops"
	"github.com/gin-gonic/gin"
)

type responseEnvelope struct {
	Code    int             `json:"code"`
	Message string          `json:"message"`
	Data    json.RawMessage `json:"data"`
}

type opsSystemLogCaptureRepo struct {
	ops.OpsRepository
	listFilter    *ops.OpsSystemLogFilter
	cleanupFilter *ops.OpsSystemLogCleanupFilter
}

func (r *opsSystemLogCaptureRepo) ListSystemLogs(_ context.Context, filter *ops.OpsSystemLogFilter) (*ops.OpsSystemLogList, error) {
	r.listFilter = filter
	return &ops.OpsSystemLogList{Logs: []*ops.OpsSystemLog{}, Page: filter.Page, PageSize: filter.PageSize}, nil
}

func (r *opsSystemLogCaptureRepo) DeleteSystemLogs(_ context.Context, filter *ops.OpsSystemLogCleanupFilter) (int64, error) {
	r.cleanupFilter = filter
	return 1, nil
}

func (r *opsSystemLogCaptureRepo) InsertSystemLogCleanupAudit(_ context.Context, _ *ops.OpsSystemLogCleanupAudit) error {
	return nil
}

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

func TestOpsSystemLogHandler_ListUnavailable(t *testing.T) {
	h := NewOpsHandler(nil)
	r := newOpsSystemLogTestRouter(h, false)

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/logs", nil)
	r.ServeHTTP(w, req)
	if w.Code != http.StatusServiceUnavailable {
		t.Fatalf("status=%d, want 503", w.Code)
	}
}

func TestOpsSystemLogHandler_ListInvalidUserID(t *testing.T) {
	svc := ops.NewOpsService(nil, nil, nil, nil, nil, nil, nil, opsprovider.LogControl{})
	h := NewOpsHandler(svc)
	r := newOpsSystemLogTestRouter(h, false)

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/logs?user_id=abc", nil)
	r.ServeHTTP(w, req)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("status=%d, want 400", w.Code)
	}
}

func TestOpsSystemLogHandler_ListInvalidAccountID(t *testing.T) {
	svc := ops.NewOpsService(nil, nil, nil, nil, nil, nil, nil, opsprovider.LogControl{})
	h := NewOpsHandler(svc)
	r := newOpsSystemLogTestRouter(h, false)

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/logs?account_id=-1", nil)
	r.ServeHTTP(w, req)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("status=%d, want 400", w.Code)
	}
}

func TestOpsSystemLogHandler_ListInvalidAPIKeyID(t *testing.T) {
	svc := ops.NewOpsService(nil, nil, nil, nil, nil, nil, nil, opsprovider.LogControl{})
	h := NewOpsHandler(svc)
	r := newOpsSystemLogTestRouter(h, false)

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/logs?api_key_id=abc", nil)
	r.ServeHTTP(w, req)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("status=%d, want 400", w.Code)
	}
}

func TestOpsSystemLogHandler_ListMonitoringDisabled(t *testing.T) {
	svc := ops.NewOpsService(nil, nil, &ops.Options{Ops: ops.RuntimeOptions{Enabled: false}}, nil, nil, nil, nil, opsprovider.LogControl{})
	h := NewOpsHandler(svc)
	r := newOpsSystemLogTestRouter(h, false)

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/logs", nil)
	r.ServeHTTP(w, req)
	if w.Code != http.StatusNotFound {
		t.Fatalf("status=%d, want 404", w.Code)
	}
}

func TestOpsSystemLogHandler_ListSuccess(t *testing.T) {
	svc := ops.NewOpsService(nil, nil, nil, nil, nil, nil, nil, opsprovider.LogControl{})
	h := NewOpsHandler(svc)
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

func TestOpsSystemLogHandler_ListAcceptsHost(t *testing.T) {
	repo := &opsSystemLogCaptureRepo{}
	svc := ops.NewOpsService(repo, nil, nil, nil, nil, nil, nil, opsprovider.LogControl{})
	h := NewOpsHandler(svc)
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

func TestOpsSystemLogHandler_CleanupUnauthorized(t *testing.T) {
	svc := ops.NewOpsService(nil, nil, nil, nil, nil, nil, nil, opsprovider.LogControl{})
	h := NewOpsHandler(svc)
	r := newOpsSystemLogTestRouter(h, false)

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/logs/cleanup", bytes.NewBufferString(`{"request_id":"r1"}`))
	req.Header.Set("Content-Type", "application/json")
	r.ServeHTTP(w, req)
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("status=%d, want 401", w.Code)
	}
}

func TestOpsSystemLogHandler_CleanupInvalidPayload(t *testing.T) {
	svc := ops.NewOpsService(nil, nil, nil, nil, nil, nil, nil, opsprovider.LogControl{})
	h := NewOpsHandler(svc)
	r := newOpsSystemLogTestRouter(h, true)

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/logs/cleanup", bytes.NewBufferString(`{bad-json`))
	req.Header.Set("Content-Type", "application/json")
	r.ServeHTTP(w, req)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("status=%d, want 400", w.Code)
	}
}

func TestOpsSystemLogHandler_CleanupInvalidTime(t *testing.T) {
	svc := ops.NewOpsService(nil, nil, nil, nil, nil, nil, nil, opsprovider.LogControl{})
	h := NewOpsHandler(svc)
	r := newOpsSystemLogTestRouter(h, true)

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/logs/cleanup", bytes.NewBufferString(`{"start_time":"bad","request_id":"r1"}`))
	req.Header.Set("Content-Type", "application/json")
	r.ServeHTTP(w, req)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("status=%d, want 400", w.Code)
	}
}

func TestOpsSystemLogHandler_CleanupInvalidEndTime(t *testing.T) {
	svc := ops.NewOpsService(nil, nil, nil, nil, nil, nil, nil, opsprovider.LogControl{})
	h := NewOpsHandler(svc)
	r := newOpsSystemLogTestRouter(h, true)

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/logs/cleanup", bytes.NewBufferString(`{"end_time":"bad","request_id":"r1"}`))
	req.Header.Set("Content-Type", "application/json")
	r.ServeHTTP(w, req)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("status=%d, want 400", w.Code)
	}
}

func TestOpsSystemLogHandler_CleanupServiceUnavailable(t *testing.T) {
	svc := ops.NewOpsService(nil, nil, nil, nil, nil, nil, nil, opsprovider.LogControl{})
	h := NewOpsHandler(svc)
	r := newOpsSystemLogTestRouter(h, true)

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/logs/cleanup", bytes.NewBufferString(`{"request_id":"r1"}`))
	req.Header.Set("Content-Type", "application/json")
	r.ServeHTTP(w, req)
	if w.Code != http.StatusServiceUnavailable {
		t.Fatalf("status=%d, want 503", w.Code)
	}
}

func TestOpsSystemLogHandler_CleanupAcceptsAPIKeyID(t *testing.T) {
	svc := ops.NewOpsService(nil, nil, nil, nil, nil, nil, nil, opsprovider.LogControl{})
	h := NewOpsHandler(svc)
	r := newOpsSystemLogTestRouter(h, true)

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/logs/cleanup", bytes.NewBufferString(`{"api_key_id":123}`))
	req.Header.Set("Content-Type", "application/json")
	r.ServeHTTP(w, req)
	if w.Code != http.StatusServiceUnavailable {
		t.Fatalf("status=%d, want 503", w.Code)
	}
}

func TestOpsSystemLogHandler_CleanupAcceptsHost(t *testing.T) {
	repo := &opsSystemLogCaptureRepo{}
	svc := ops.NewOpsService(repo, nil, nil, nil, nil, nil, nil, opsprovider.LogControl{})
	h := NewOpsHandler(svc)
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

func TestOpsSystemLogHandler_CleanupInvalidAPIKeyID(t *testing.T) {
	svc := ops.NewOpsService(nil, nil, nil, nil, nil, nil, nil, opsprovider.LogControl{})
	h := NewOpsHandler(svc)
	r := newOpsSystemLogTestRouter(h, true)

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/logs/cleanup", bytes.NewBufferString(`{"api_key_id":0}`))
	req.Header.Set("Content-Type", "application/json")
	r.ServeHTTP(w, req)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("status=%d, want 400", w.Code)
	}
}

func TestOpsSystemLogHandler_CleanupMonitoringDisabled(t *testing.T) {
	svc := ops.NewOpsService(nil, nil, &ops.Options{Ops: ops.RuntimeOptions{Enabled: false}}, nil, nil, nil, nil, opsprovider.LogControl{})
	h := NewOpsHandler(svc)
	r := newOpsSystemLogTestRouter(h, true)

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/logs/cleanup", bytes.NewBufferString(`{"request_id":"r1"}`))
	req.Header.Set("Content-Type", "application/json")
	r.ServeHTTP(w, req)
	if w.Code != http.StatusNotFound {
		t.Fatalf("status=%d, want 404", w.Code)
	}
}

func TestOpsSystemLogHandler_Health(t *testing.T) {
	host, hostErr := os.Hostname()
	sink := ops.NewOpsSystemLogSink(nil, ops.SystemLogSinkOptions{Host: host, HostError: hostErr})
	svc := ops.NewOpsService(nil, nil, nil, nil, nil, nil, sink, opsprovider.LogControl{})
	h := NewOpsHandler(svc)
	r := newOpsSystemLogTestRouter(h, false)

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/logs/health", nil)
	r.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("status=%d, want 200", w.Code)
	}
}

func TestOpsSystemLogHandler_HealthUnavailableAndMonitoringDisabled(t *testing.T) {
	h := NewOpsHandler(nil)
	r := newOpsSystemLogTestRouter(h, false)

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/logs/health", nil)
	r.ServeHTTP(w, req)
	if w.Code != http.StatusServiceUnavailable {
		t.Fatalf("status=%d, want 503", w.Code)
	}

	svc := ops.NewOpsService(nil, nil, &ops.Options{Ops: ops.RuntimeOptions{Enabled: false}}, nil, nil, nil, nil, opsprovider.LogControl{})
	h = NewOpsHandler(svc)
	r = newOpsSystemLogTestRouter(h, false)
	w = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodGet, "/logs/health", nil)
	r.ServeHTTP(w, req)
	if w.Code != http.StatusNotFound {
		t.Fatalf("status=%d, want 404", w.Code)
	}
}
