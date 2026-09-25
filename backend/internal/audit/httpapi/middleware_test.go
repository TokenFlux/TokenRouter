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

	"github.com/TokenFlux/TokenRouter/internal/identity/httpapi/authctx"

	service "github.com/TokenFlux/TokenRouter/internal/audit"
	"github.com/TokenFlux/TokenRouter/internal/pkg/apperror"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

type partialErrorBody struct {
	data  []byte
	first bool
}

func (b *partialErrorBody) Read(p []byte) (int, error) {
	if len(b.data) == 0 {
		return 0, io.EOF
	}
	limit := len(b.data)
	if !b.first && limit > 5 {
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

func (b *partialErrorBody) Close() error { return nil }

type auditCaptureRepository struct {
	mu   sync.Mutex
	logs []*service.AuditLog
}

func (r *auditCaptureRepository) BatchInsert(_ context.Context, logs []*service.AuditLog) (int64, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.logs = append(r.logs, logs...)
	return int64(len(logs)), nil
}

func (r *auditCaptureRepository) Insert(_ context.Context, log *service.AuditLog) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.logs = append(r.logs, log)
	return nil
}

func (r *auditCaptureRepository) List(context.Context, *service.AuditLogFilter) (*service.AuditLogList, error) {
	return &service.AuditLogList{}, nil
}

func (r *auditCaptureRepository) GetByID(context.Context, int64) (*service.AuditLog, error) {
	return nil, service.ErrAuditLogNotFound
}

func (r *auditCaptureRepository) Count(context.Context) (int64, error) { return 0, nil }
func (r *auditCaptureRepository) TruncateAll(context.Context) error    { return nil }
func (r *auditCaptureRepository) DeleteBefore(context.Context, time.Time, int) (int64, error) {
	return 0, nil
}

func TestDeriveAuditAction(t *testing.T) {
	cases := []struct {
		method string
		path   string
		want   string
	}{
		{"PUT", "/api/v1/admin/accounts/:id", "admin.accounts.update"},
		{"POST", "/api/v1/admin/accounts", "admin.accounts.create"},
		{"DELETE", "/api/v1/admin/backups/:id", "admin.backups.delete"},
		{"GET", "/api/v1/admin/users/:id/api-keys", "admin.users.api_keys.read"},
		{"POST", "/api/v1/admin/redeem-codes/batch", "admin.redeem_codes.batch.create"},
	}
	for _, tc := range cases {
		if got := deriveAuditAction(tc.method, tc.path); got != tc.want {
			t.Fatalf("deriveAuditAction(%q, %q) = %q, want %q", tc.method, tc.path, got, tc.want)
		}
	}
	if got := auditActionOverrides["POST /api/v1/subscriptions/:id/revoke"]; got != service.AuditActionUserSubscriptionRevoke {
		t.Fatalf("subscription revoke audit action = %q, want %q", got, service.AuditActionUserSubscriptionRevoke)
	}
}

func TestAuditSensitiveReadsIncludesForkBackupRoutes(t *testing.T) {
	want := map[string]string{
		"GET /api/v1/admin/backups/:id/download":   "admin.backups.download",
		"GET /api/v1/admin/backups/storage-config": "admin.backups.storage_config.read",
	}
	for route, action := range want {
		if got := auditSensitiveReads[route]; got != action {
			t.Fatalf("auditSensitiveReads[%q] = %q, want %q", route, got, action)
		}
	}
}

func TestAuditMiddlewareRestoresPartialBodyAfterReadError(t *testing.T) {

	want := []byte(`{"name":"完整请求"}`)
	var got []byte

	router := gin.New()
	router.POST("/api/v1/admin/test", gin.HandlerFunc(NewAuditLogMiddleware(nil, service.NewRedactor(nil))), func(c *gin.Context) {
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

// Ollama 会话保存的请求体整体就是浏览器 Cookie 明文，键级脱敏清单曾漏掉裸键
// "session"，必须走整体不入库路径，防止会话凭证长期留存在 audit_logs。
func TestOllamaCloudUsageSessionRouteOmitsAuditBody(t *testing.T) {

	require.Contains(t, auditBodyOmittedRoutes, "PUT /api/v1/admin/accounts/:id/ollama-cloud-usage/session")

	repository := &auditCaptureRepository{}
	auditService := service.NewAuditLogService(repository, nil)
	auditService.Start()

	router := gin.New()
	router.Use(func(c *gin.Context) {
		c.Set(string(authctx.ContextKeyUser), authctx.AuthSubject{UserID: 77})
		c.Set(string(authctx.ContextKeyUserRole), "admin")
		c.Next()
	})
	router.Use(gin.HandlerFunc(NewAuditLogMiddleware(auditService, service.NewRedactor(nil))))
	router.PUT("/api/v1/admin/accounts/:id/ollama-cloud-usage/session", func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{"ok": true})
	})

	request := httptest.NewRequest(http.MethodPut, "/api/v1/admin/accounts/7/ollama-cloud-usage/session",
		bytes.NewBufferString(`{"session":"wos-session=audit-canary-cookie; __Secure-authjs.session-token.0=audit-canary-shard"}`))
	request.Header.Set("Content-Type", "application/json")
	recorder := httptest.NewRecorder()
	router.ServeHTTP(recorder, request)
	require.Equal(t, http.StatusOK, recorder.Code)
	auditService.Stop()

	repository.mu.Lock()
	logs := append([]*service.AuditLog(nil), repository.logs...)
	repository.mu.Unlock()
	require.Len(t, logs, 1)
	require.Equal(t, "<credential-bearing body omitted>", logs[0].RequestBody)
	require.NotContains(t, logs[0].RequestBody, "audit-canary")
}

func TestPasskeyFinishRoutesOmitAuditBody(t *testing.T) {
	// WebAuthn finish body 含完整 assertion/attestation，不能作为普通审计 JSON 入库。
	for _, route := range []string{
		"POST /api/v1/auth/passkey/login/finish",
		"POST /api/v1/user/passkeys/register/finish",
	} {
		require.Contains(t, auditBodyOmittedRoutes, route)
	}
}

func (r *auditCaptureRepository) ClearWithTrace(_ context.Context, trace *service.AuditLog) (int64, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	n := len(r.logs)
	r.logs = []*service.AuditLog{trace}
	return int64(n), nil
}

// clearHTTPRepository 只实现清空端口，避免 HTTP 测试把分步清空误当作闭合操作。
type clearHTTPRepository struct {
	service.AuditLogRepository
	trace *service.AuditLog
	err   error
	order *[]string
}

func (r *clearHTTPRepository) ClearWithTrace(_ context.Context, trace *service.AuditLog) (int64, error) {
	*r.order = append(*r.order, "clear-with-trace")
	r.trace = trace
	return 7, r.err
}

// clearTOTPVerifier 记录此次操作是否重新验证 TOTP；不复用其他 step-up 凭据。
type clearTOTPVerifier struct {
	err   error
	user  int64
	code  string
	order *[]string
}

func (v *clearTOTPVerifier) VerifyCode(_ context.Context, user int64, code string) error {
	*v.order = append(*v.order, "totp")
	v.user, v.code = user, code
	return v.err
}

func TestS08AuditClearHTTPAuthorizationAndTrace(t *testing.T) {
	cases := []struct {
		name, auth, body, reason string
		user                     int64
		verifyErr, storeErr      error
		status                   int
		wantOrder                []string
	}{
		{name: "admin-key-rejected-before-body", auth: service.AuditAuthMethodAdminAPIKey, status: http.StatusForbidden, reason: "STEP_UP_ADMIN_API_KEY_FORBIDDEN"},
		{name: "missing-subject", auth: service.AuditAuthMethodJWT, status: http.StatusUnauthorized},
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
			svc := service.NewAuditLogService(repo, nil)
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
				c.Header("X-Request-ID", "s08-clear")
				h.Clear(c)
				skipped = c.GetBool(auditCtxKeySkip)
			})
			w := httptest.NewRecorder()
			req := httptest.NewRequest(http.MethodPost, "/api/v1/admin/audit-logs/clear", bytes.NewBufferString(tc.body))
			req.Header.Set("Content-Type", "application/json")
			req.Header.Set("Authorization", "Bearer s08-contract-credential")
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
				require.Equal(t, "s08-clear", repo.trace.RequestID)
				require.Equal(t, service.AuditActionAuditLogClear, repo.trace.Action)
				require.NotEqual(t, "s08-contract-credential", repo.trace.CredentialMasked)
			}
			require.Equal(t, tc.status == http.StatusOK, skipped)
			if skipped {
				require.Contains(t, w.Body.String(), `"deleted":7`)
			}
		})
	}
}
