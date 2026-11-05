package httpapi

import (
	"bytes"
	"context"
	"encoding/json"
	"maps"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"

	"github.com/TokenFlux/TokenRouter/internal/identity/httpapi/authctx"
	"github.com/TokenFlux/TokenRouter/internal/infra/telemetry/logging"
	"github.com/TokenFlux/TokenRouter/internal/ops"
	opsprovider "github.com/TokenFlux/TokenRouter/internal/ops/provider"
	settingscore "github.com/TokenFlux/TokenRouter/internal/settings"
)

// testSettingRepo stores unsynchronized string settings for serial
// runtime-handler tests.
type testSettingRepo struct {
	// values is allocated by newTestSettingRepo and mutated directly by write
	// methods. Fixture callers serialize access; complete and batch reads allocate
	// separate result maps.
	values map[string]string // retained fixture setting values
}

// TestOpsRuntimeLoggingHandler_GetConfig requests logging preferences from a
// configured service with in-memory settings. It checks a successful HTTP
// status; this case does not decode or assert individual configuration values.
func TestOpsRuntimeLoggingHandler_GetConfig(t *testing.T) {
	h := NewOpsHandler(newRuntimeOpsService(t), OpsWSOptions{})
	r := newOpsRuntimeRouter(h, false)

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/runtime/logging", nil)
	r.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("status=%d, want 200", w.Code)
	}
}

// TestOpsRuntimeLoggingHandler_UpdateUnauthorized submits a valid logging body
// through a router without a subject fixture. It checks the handler rejects
// that write with an unauthorized HTTP status for the absent subject.
func TestOpsRuntimeLoggingHandler_UpdateUnauthorized(t *testing.T) {
	h := NewOpsHandler(newRuntimeOpsService(t), OpsWSOptions{})
	r := newOpsRuntimeRouter(h, false)

	body := `{"level":"debug","enable_sampling":false,"sampling_initial":100,"sampling_thereafter":100,"caller":true,"stacktrace_level":"error","retention_days":30}`
	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPut, "/runtime/logging", bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	r.ServeHTTP(w, req)
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("status=%d, want 401", w.Code)
	}
}

// TestOpsRuntimeLoggingHandler_UpdateAndResetSuccess supplies a positive
// subject, updates logging preferences, then resets the override. It checks
// both HTTP operations succeed with the in-memory settings and process logging
// implementation.
func TestOpsRuntimeLoggingHandler_UpdateAndResetSuccess(t *testing.T) {
	h := NewOpsHandler(newRuntimeOpsService(t), OpsWSOptions{})
	r := newOpsRuntimeRouter(h, true)

	payload := map[string]any{
		"level":               "debug",
		"enable_sampling":     false,
		"sampling_initial":    100,
		"sampling_thereafter": 100,
		"caller":              true,
		"stacktrace_level":    "error",
		"retention_days":      30,
	}
	raw, _ := json.Marshal(payload)

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPut, "/runtime/logging", bytes.NewReader(raw))
	req.Header.Set("Content-Type", "application/json")
	r.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("update status=%d, want 200, body=%s", w.Code, w.Body.String())
	}

	w = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodPost, "/runtime/logging/reset", nil)
	r.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("reset status=%d, want 200, body=%s", w.Code, w.Body.String())
	}
}

// TestEmailReportRejectsLegacyProviderFields submits the retired
// account_health_enabled report member and its provider_health_enabled
// replacement. It checks strict decoding rejects the former while the supported
// member reaches a successful update.
func TestEmailReportRejectsLegacyProviderFields(t *testing.T) {
	handler := &OpsHandler{opsService: newRuntimeOpsService(t)}
	router := gin.New()
	router.PUT("/config", handler.UpdateEmailNotificationConfig)
	for _, tc := range []struct {
		body   string // supplied report JSON
		status int    // expected HTTP status
	}{
		{`{"report":{"account_health_enabled":false}}`, http.StatusBadRequest},
		{`{"report":{"provider_health_enabled":false}}`, http.StatusOK},
	} {
		request := httptest.NewRequest(http.MethodPut, "/config", strings.NewReader(tc.body))
		request.Header.Set("Content-Type", "application/json")
		recorder := httptest.NewRecorder()
		router.ServeHTTP(recorder, request)
		require.Equal(t, tc.status, recorder.Code, recorder.Body.String())
	}
}

// newTestSettingRepo allocates an empty settings map for one handler fixture.
func newTestSettingRepo() *testSettingRepo {
	return &testSettingRepo{values: map[string]string{}}
}

// Get returns a newly allocated settings row using GetValue's lookup result. A
// missing key forwards that error, and the supplied context is ignored by the
// fixture's storage methods.
func (s *testSettingRepo) Get(ctx context.Context, key string) (*settingscore.Setting, error) {
	v, err := s.GetValue(ctx, key)
	if err != nil {
		return nil, err
	}
	return &settingscore.Setting{Key: key, Value: v}, nil
}

// GetValue returns the stored string or ErrSettingNotFound when the key is
// absent. It ignores context and has no synchronization, matching the serial
// in-memory fixture used by these handlers.
func (s *testSettingRepo) GetValue(ctx context.Context, key string) (string, error) {
	v, ok := s.values[key]
	if !ok {
		return "", settingscore.ErrSettingNotFound
	}
	return v, nil
}

// Set stores the supplied string in the existing settings map and succeeds.
// Callers allocate that map through the constructor and serialize access;
// context is ignored.
func (s *testSettingRepo) Set(ctx context.Context, key, value string) error {
	s.values[key] = value
	return nil
}

// GetMultiple returns a new map containing requested keys that exist in the
// fixture. Missing keys are omitted successfully; context is ignored, and
// changing the returned map does not alter stored strings.
func (s *testSettingRepo) GetMultiple(ctx context.Context, keys []string) (map[string]string, error) {
	out := make(map[string]string, len(keys))
	for _, k := range keys {
		if v, ok := s.values[k]; ok {
			out[k] = v
		}
	}
	return out, nil
}

// SetMultiple merges supplied strings into the existing settings map, retaining
// unmentioned keys. It ignores context and succeeds; fixture callers serialize
// mutation and initialize the destination map before use.
func (s *testSettingRepo) SetMultiple(ctx context.Context, settings map[string]string) error {
	maps.Copy(s.values, settings)
	return nil
}

// GetAll returns an independently allocated map of the fixture's current
// strings. It ignores context and performs an unsynchronized read, so callers
// serialize it with test writes.
func (s *testSettingRepo) GetAll(ctx context.Context) (map[string]string, error) {
	out := make(map[string]string, len(s.values))
	maps.Copy(out, s.values)
	return out, nil
}

// Delete removes the supplied key from the fixture map and succeeds even when
// it was absent. It ignores context and stores no deletion audit; callers
// serialize mutation.
func (s *testSettingRepo) Delete(ctx context.Context, key string) error {
	delete(s.values, key)
	return nil
}

// newOpsRuntimeRouter registers logging read, update, and reset handlers for
// HTTP fixtures. withUser adds a subject with user ID seven directly to Gin
// context; it supplies no production authentication middleware.
func newOpsRuntimeRouter(handler *OpsHandler, withUser bool) *gin.Engine {
	r := gin.New()
	if withUser {
		r.Use(func(c *gin.Context) {
			c.Set(string(authctx.ContextKeyUser), authctx.AuthSubject{UserID: 7})
			c.Next()
		})
	}
	r.GET("/runtime/logging", handler.GetRuntimeLogConfig)
	r.PUT("/runtime/logging", handler.UpdateRuntimeLogConfig)
	r.POST("/runtime/logging/reset", handler.ResetRuntimeLogConfig)
	return r
}

// newRuntimeOpsService initializes process logging and creates an Ops service
// with in-memory settings and a logging-control adapter. Tests use it serially
// because initialization and subsequent runtime updates change process-wide
// logger state.
func newRuntimeOpsService(t *testing.T) *ops.OpsService {
	t.Helper()
	// Initialize process logging before constructing the Ops service, whose
	// startup configuration can apply through the real logging-control adapter in
	// these serial tests.
	if err := logging.Init(logging.InitOptions{
		Level:       "info",
		Format:      "json",
		ServiceName: "tokenrouter",
		Environment: "test",
		Output: logging.OutputOptions{
			ToStdout: false,
			ToFile:   false,
		},
	}); err != nil {
		t.Fatalf("init logger: %v", err)
	}

	settingRepo := newTestSettingRepo()
	cfg := &ops.Options{
		Ops: ops.RuntimeOptions{Enabled: true},
		Log: ops.LogOptions{
			Level:           "info",
			Caller:          true,
			StacktraceLevel: "error",
			Sampling: ops.SamplingOptions{
				Enabled:    false,
				Initial:    100,
				Thereafter: 100,
			},
		},
	}
	return ops.NewOpsService(nil, settingRepo, cfg, nil, nil, nil, nil, opsprovider.LogControl{})
}
