package httpapi

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"

	"github.com/TokenFlux/TokenRouter/internal/audit"
	"github.com/TokenFlux/TokenRouter/internal/identity/httpapi/authctx"
	"github.com/TokenFlux/TokenRouter/internal/pkg/apperror"
)

// partialErrorBody injects one read error after returning a captured prefix.
type partialErrorBody struct {
	// data and first track unread bytes and whether the injected error was emitted.
	// The first read advances data before failing, reproducing consumption during capture.
	data  []byte // remaining request body bytes
	first bool   // first read already completed
}

// auditCaptureRepository retains middleware events for assertions after writer shutdown.
type auditCaptureRepository struct {
	// mu protects logs while the background audit writer appends records and tests
	// take snapshots. Stored pointers remain shared after insertion for assertion reads.
	mu   sync.Mutex        // captured event collection lock
	logs []*audit.AuditLog // captured management audit events
}

// clearHTTPRepository implements ClearWithTrace so tests fail if the handler switches to separate clear and insert calls.
type clearHTTPRepository struct {
	// AuditLogRepository leaves unrelated operations unimplemented so this test fails
	// if clearing switches away from the single overridden transactional trace call.
	audit.AuditLogRepository
	trace *audit.AuditLog // last supplied clearing trace
	err   error           // injected clearing operation failure
	// order points to the test's shared event slice. ClearWithTrace appends after
	// verification so assertions can distinguish authorization from attempted storage changes.
	order *[]string
}

// clearTOTPVerifier records fresh TOTP verification for the clearing operation.
type clearTOTPVerifier struct {
	err  error  // injected verification failure
	user int64  // last verified user identifier
	code string // last submitted verification code
	// order points to the event slice shared with clearHTTPRepository. VerifyCode
	// appends first even when the injected error prevents any later clearing call.
	order *[]string
}

// Read consumes a prefix and returns an injected error on its first call.
// Subsequent calls consume remaining bytes normally, and exhausted input returns
// EOF, allowing handler reads to recover the body after middleware prefix capture.
func (b *partialErrorBody) Read(p []byte) (int, error) {
	if len(b.data) == 0 {
		return 0, io.EOF
	}
	limit := len(b.data)
	if !b.first && limit > 5 {
		// Leave unread bytes for replay after the injected capture failure.
		limit = 5
	}
	n := copy(p, b.data[:limit])
	b.data = b.data[n:]
	if !b.first {
		b.first = true
		return n, errors.New("injected read error")
	}
	return n, nil
}

// Close completes the synthetic body lifetime with no external resources.
func (b *partialErrorBody) Close() error { return nil }

// BatchInsert appends shared record pointers under the capture lock for later
// middleware assertions. It reports the supplied count and leaves context unused
// because this test repository models successful in-memory persistence.
func (r *auditCaptureRepository) BatchInsert(_ context.Context, logs []*audit.AuditLog) (int64, error) {
	// Synchronize collection mutation with test snapshots so the writer cannot append
	// while assertions copy the captured events after a request has finished.
	r.mu.Lock()
	defer r.mu.Unlock()
	r.logs = append(r.logs, logs...)
	return int64(len(logs)), nil
}

// Insert appends one shared record under the capture lock and succeeds.
// Tests use the same collection for synchronous and batch writes so both paths
// remain visible to middleware assertions.
func (r *auditCaptureRepository) Insert(_ context.Context, log *audit.AuditLog) error {
	// Use the writer's collection lock for synchronous insertion too, keeping snapshots
	// consistent when the same repository observes both persistence paths during a scenario.
	r.mu.Lock()
	defer r.mu.Unlock()
	r.logs = append(r.logs, log)
	return nil
}

// List returns an empty page because these scenarios assert captured events directly.
func (r *auditCaptureRepository) List(context.Context, *audit.AuditLogFilter) (*audit.AuditLogList, error) {
	return &audit.AuditLogList{}, nil
}

// GetByID returns ErrAuditLogNotFound because middleware tests perform no detail lookup.
func (r *auditCaptureRepository) GetByID(context.Context, int64) (*audit.AuditLog, error) {
	return nil, audit.ErrAuditLogNotFound
}

// Count returns zero because event assertions inspect the captured slice directly.
func (r *auditCaptureRepository) Count(context.Context) (int64, error) { return 0, nil }

// TruncateAll succeeds without clearing the collection in these capture-only scenarios.
func (r *auditCaptureRepository) TruncateAll(context.Context) error { return nil }

// DeleteBefore returns zero so retention cannot modify middleware test event history.
func (r *auditCaptureRepository) DeleteBefore(context.Context, time.Time, int) (int64, error) {
	return 0, nil
}

// TestDeriveAuditAction supplies resource routes with dynamic identifiers and
// different HTTP methods. Derived actions must identify operations consistently,
// and subscription revocation must retain its fixed user action override.
func TestDeriveAuditAction(t *testing.T) {
	cases := []struct {
		method string // operation HTTP method
		path   string // Gin resource route template
		want   string // expected classified action name
	}{
		{"PUT", "/api/v1/admin/providers/:id", "admin.providers.update"},
		{"POST", "/api/v1/admin/providers", "admin.providers.create"},
		{"DELETE", "/api/v1/admin/backups/:id", "admin.backups.delete"},
		{"GET", "/api/v1/admin/users/:id/api-keys", "admin.users.api_keys.read"},
		{"POST", "/api/v1/admin/redeem-codes/batch", "admin.redeem_codes.batch.create"},
	}
	for _, tc := range cases {
		if got := deriveAuditAction(tc.method, tc.path); got != tc.want {
			t.Fatalf("deriveAuditAction(%q, %q) = %q, want %q", tc.method, tc.path, got, tc.want)
		}
	}
	if got := auditActionOverrides["POST /api/v1/subscriptions/:id/revoke"]; got != audit.AuditActionUserSubscriptionRevoke {
		t.Fatalf("subscription revoke audit action = %q, want %q", got, audit.AuditActionUserSubscriptionRevoke)
	}
}

// TestAuditSensitiveReadsIncludesProtectedDownloads checks backup and MCP workflow
// read routes. They remain audited GET operations so administrators can inspect
// protected file access and confirmation reviews alongside their initiating actions.
func TestAuditSensitiveReadsIncludesProtectedDownloads(t *testing.T) {
	want := map[string]string{
		"GET /api/v1/admin/backups/:id/download":      "admin.backups.download",
		"GET /api/v1/admin/backups/storage-config":    "admin.backups.storage_config.read",
		"GET /api/v1/admin/mcp/tokens/:id/credential": "admin.mcp.tokens.copy",
		"GET /api/v1/admin/mcp/files/:id":             "admin.mcp.files.download",
		"GET /api/v1/admin/mcp/confirmations/:id":     "admin.mcp.confirmations.read",
	}
	for route, action := range want {
		if got := auditSensitiveReads[route]; got != action {
			t.Fatalf("auditSensitiveReads[%q] = %q, want %q", route, got, action)
		}
	}
}

// TestAuditMiddlewareRestoresPartialBodyAfterReadError injects a capture failure
// after the original body yields its first bytes. The downstream handler must
// still receive the complete request and return its successful status.
func TestAuditMiddlewareRestoresPartialBodyAfterReadError(t *testing.T) {
	want := []byte(`{"name":"完整请求"}`)
	var got []byte

	router := gin.New()
	router.POST("/api/v1/admin/test", gin.HandlerFunc(NewAuditLogMiddleware(nil, audit.NewRedactor(nil))), func(c *gin.Context) {
		var err error
		got, err = io.ReadAll(c.Request.Body)
		if err != nil {
			c.Status(http.StatusBadRequest)
			return
		}
		c.Status(http.StatusNoContent)
	})

	req := httptest.NewRequest(http.MethodPost, "/api/v1/admin/test", nil)
	req.Header.Set("Content-Type", "application/json")
	req.Body = &partialErrorBody{data: append([]byte(nil), want...)}
	resp := httptest.NewRecorder()
	router.ServeHTTP(resp, req)

	if resp.Code != http.StatusNoContent {
		t.Fatalf("status = %d, want %d", resp.Code, http.StatusNoContent)
	}
	if string(got) != string(want) {
		t.Fatalf("restored body = %q, want %q", got, want)
	}
}

// TestOllamaCloudUsageSessionRouteOmitsAuditBody submits cookies embedded in an
// Ollama usage session string. The completed request must produce one audit event
// with the full-body omission marker and no cookie canary text.
func TestOllamaCloudUsageSessionRouteOmitsAuditBody(t *testing.T) {
	require.Contains(t, auditBodyOmittedRoutes, "PUT /api/v1/admin/providers/:id/ollama-cloud-usage/session")

	repository := &auditCaptureRepository{}
	auditService := audit.NewAuditLogService(repository, nil)
	auditService.Start()

	router := gin.New()
	router.Use(func(c *gin.Context) {
		c.Set(string(authctx.ContextKeyUser), authctx.AuthSubject{UserID: 77})
		c.Set(string(authctx.ContextKeyUserRole), "admin")
		c.Next()
	})
	router.Use(gin.HandlerFunc(NewAuditLogMiddleware(auditService, audit.NewRedactor(nil))))
	router.PUT("/api/v1/admin/providers/:id/ollama-cloud-usage/session", func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{"ok": true})
	})

	request := httptest.NewRequest(http.MethodPut, "/api/v1/admin/providers/7/ollama-cloud-usage/session",
		bytes.NewBufferString(`{"session":"wos-session=audit-canary-cookie; __Secure-authjs.session-token.0=audit-canary-shard"}`))
	request.Header.Set("Content-Type", "application/json")
	recorder := httptest.NewRecorder()
	router.ServeHTTP(recorder, request)
	require.Equal(t, http.StatusOK, recorder.Code)
	// Stop waits for the writer's final drain, making the captured collection complete
	// before assertions snapshot it instead of racing asynchronous request observation delivery.
	auditService.Stop()

	// Read through the repository's collection lock after the drain, keeping snapshot
	// access consistent with the synchronization used by both synchronous and batch insert helpers.
	repository.mu.Lock()
	logs := append([]*audit.AuditLog(nil), repository.logs...)
	repository.mu.Unlock()
	require.Len(t, logs, 1)
	require.Equal(t, "<credential-bearing body omitted>", logs[0].RequestBody)
	require.NotContains(t, logs[0].RequestBody, "audit-canary")
}

// TestCredentialPayloadRoutesOmitAuditBody checks endpoints carrying opaque authentication
// payloads. WebAuthn assertions and encoded MCP imports require whole-body omission,
// because nested JSON-key redaction cannot inspect encoded values or identify
// secret configuration fields whose names are chosen by individual plugins.
func TestCredentialPayloadRoutesOmitAuditBody(t *testing.T) {
	for _, route := range []string{
		"POST /api/v1/auth/passkey/login/finish",
		"POST /api/v1/user/passkeys/register/finish",
		"POST /api/v1/admin/mcp/files",
		"PUT /api/v1/admin/plugins/:id/config",
		"POST /api/v1/admin/plugins/uploads",
	} {
		require.Contains(t, auditBodyOmittedRoutes, route)
	}
}

// ClearWithTrace replaces the captured collection with its supplied clearing trace
// under the writer lock and returns the previous record count. Tests receive the
// same shared trace pointer that would be passed to transactional persistence.
func (r *auditCaptureRepository) ClearWithTrace(_ context.Context, trace *audit.AuditLog) (int64, error) {
	// Serialize counting and replacement against event appends so the test's deleted
	// count corresponds to the exact collection replaced by this clearing trace.
	r.mu.Lock()
	defer r.mu.Unlock()
	n := len(r.logs)
	r.logs = []*audit.AuditLog{trace}
	return int64(n), nil
}

// TestMCPBrowserContinuationAuditRetainsActorAndToken downloads an owned protected
// result through a JWT fixture. The audit keeps browser authentication while
// retaining the originating token and operation for its related-history filter.
func TestMCPBrowserContinuationAuditRetainsActorAndToken(t *testing.T) {
	repository := &auditCaptureRepository{}
	service := audit.NewAuditLogService(repository, nil)
	service.Start()
	router := gin.New()
	router.Use(func(c *gin.Context) {
		c.Set(string(authctx.ContextKeyUser), authctx.AuthSubject{UserID: 77})
		c.Set("auth_method", audit.AuditAuthMethodJWT)
		c.Next()
	})
	router.Use(gin.HandlerFunc(NewAuditLogMiddleware(service, audit.NewRedactor(nil))))
	router.GET("/api/v1/admin/mcp/files/:id", func(c *gin.Context) {
		c.Set("mcp_related_token_id", int64(12))
		c.Set("mcp_related_operation_id", "providers.data.list")
		c.Set("mcp_related_token_prefix", "tr_mcp_example")
		c.Data(200, "application/json", []byte(`{"result":true}`))
	})
	response := httptest.NewRecorder()
	router.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/api/v1/admin/mcp/files/owned-file", nil))
	// Stop drains queued audit writes before assertions inspect the repository's
	// captured event, so completion does not depend on asynchronous writer scheduling.
	service.Stop()
	repository.mu.Lock()
	defer repository.mu.Unlock()
	require.Len(t, repository.logs, 1)
	entry := repository.logs[0]
	require.Equal(t, audit.AuditAuthMethodJWT, entry.AuthMethod)
	require.Equal(t, int64(77), *entry.ActorUserID)
	require.Equal(t, "tr_mcp_example", entry.CredentialMasked)
	require.Equal(t, int64(12), entry.Extra["mcp_token_id"])
	require.Equal(t, "providers.data.list", entry.Extra["mcp_operation_id"])
}

// ClearWithTrace records its position after TOTP verification and captures the
// supplied trace. It returns a fixed deleted count together with the injected
// storage error, letting handler tests distinguish success from failed trace persistence.
func (r *clearHTTPRepository) ClearWithTrace(_ context.Context, trace *audit.AuditLog) (int64, error) {
	*r.order = append(*r.order, "clear-with-trace")
	r.trace = trace
	return 7, r.err
}

// VerifyCode records the supplied actor and code before returning the
// injected authorization error. The shared order slice lets tests establish that
// clearing follows verification and is skipped when verification fails.
func (v *clearTOTPVerifier) VerifyCode(_ context.Context, user int64, code string) error {
	*v.order = append(*v.order, "totp")
	v.user, v.code = user, code
	return v.err
}

// TestAuditClearHTTPAuthorizationAndTrace exercises missing identity, malformed
// request JSON, verification failures, and storage failures before successful clearing.
// Verification must precede the single trace call, and middleware skipping must
// occur only after that call succeeds.
func TestAuditClearHTTPAuthorizationAndTrace(t *testing.T) {
	cases := []struct {
		// name identifies the scenario; auth selects the request authentication method;
		// body supplies the JSON request; reason identifies the expected structured error.
		name, auth, body, reason string
		user                     int64    // authenticated test actor identifier
		verifyErr, storeErr      error    // verifyErr: injected authorization failure; storeErr: injected trace persistence failure
		status                   int      // expected HTTP response status
		wantOrder                []string // expected authorization and clearing order
	}{
		{name: "admin-key-rejected-before-body", auth: audit.AuditAuthMethodAdminAPIKey, status: http.StatusForbidden, reason: "STEP_UP_ADMIN_API_KEY_FORBIDDEN"},
		{name: "missing-subject", auth: audit.AuditAuthMethodJWT, status: http.StatusUnauthorized},
		{name: "malformed-body", user: 77, body: "{", status: http.StatusBadRequest, reason: "TOTP_CODE_REQUIRED"},
		{name: "missing-code", user: 77, body: `{}`, status: http.StatusBadRequest, reason: "TOTP_CODE_REQUIRED"},
		{name: "totp-not-enabled", user: 77, body: `{"totp_code":"123456"}`, verifyErr: apperror.Forbidden("TOTP_NOT_SETUP", "not enabled"), status: http.StatusForbidden, reason: "TOTP_NOT_SETUP", wantOrder: []string{"totp"}},
		{name: "totp-invalid", user: 77, body: `{"totp_code":"123456"}`, verifyErr: apperror.Forbidden("TOTP_INVALID_CODE", "invalid code"), status: http.StatusForbidden, reason: "TOTP_INVALID_CODE", wantOrder: []string{"totp"}},
		{name: "trace-failure", user: 77, body: `{"totp_code":"123456"}`, storeErr: errors.New("trace failed"), status: http.StatusInternalServerError, wantOrder: []string{"totp", "clear-with-trace"}},
		{name: "success", user: 77, body: `{"totp_code":" 123456 "}`, status: http.StatusOK, wantOrder: []string{"totp", "clear-with-trace"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var order []string
			repo := &clearHTTPRepository{err: tc.storeErr, order: &order}
			verifier := &clearTOTPVerifier{err: tc.verifyErr, order: &order}
			svc := audit.NewAuditLogService(repo, nil)
			t.Cleanup(svc.Stop)
			h := NewAuditLogHandler(svc, verifier)
			router := gin.New()
			var skipped bool
			router.POST("/api/v1/admin/audit-logs/clear", func(c *gin.Context) {
				c.Set("auth_method", tc.auth)
				if tc.user > 0 {
					c.Set(string(authctx.ContextKeyUser), authctx.AuthSubject{UserID: tc.user})
					c.Set("user_role", "admin")
					c.Set(ContextKeyAuthEmail, "admin@example.test")
				}
				c.Header("X-Request-ID", "test-clear")
				h.Clear(c)
				skipped = c.GetBool(auditCtxKeySkip)
			})
			w := httptest.NewRecorder()
			req := httptest.NewRequest(http.MethodPost, "/api/v1/admin/audit-logs/clear", bytes.NewBufferString(tc.body))
			req.Header.Set("Content-Type", "application/json")
			req.Header.Set("Authorization", "Bearer test-contract-credential")
			router.ServeHTTP(w, req)
			require.Equal(t, tc.status, w.Code, w.Body.String())
			require.Equal(t, tc.wantOrder, order)
			if tc.reason != "" {
				require.Contains(t, w.Body.String(), tc.reason)
			}
			if len(order) > 0 {
				require.Equal(t, tc.user, verifier.user)
				require.Equal(t, "123456", verifier.code)
			}
			if repo.trace != nil {
				require.Equal(t, tc.user, *repo.trace.ActorUserID)
				require.Equal(t, "admin", repo.trace.ActorRole)
				require.Equal(t, "test-clear", repo.trace.RequestID)
				require.Equal(t, audit.AuditActionAuditLogClear, repo.trace.Action)
				require.NotEqual(t, "test-contract-credential", repo.trace.CredentialMasked)
			}
			require.Equal(t, tc.status == http.StatusOK, skipped)
			if skipped {
				require.Contains(t, w.Body.String(), `"deleted":7`)
			}
		})
	}
}
