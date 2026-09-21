package app

import (
	"net/http"
	"net/http/httptest"
	"testing"

	accounthttp "github.com/TokenFlux/TokenRouter/internal/account/httpapi"
	schedulerhttp "github.com/TokenFlux/TokenRouter/internal/scheduler/httpapi"

	backuphttp "github.com/TokenFlux/TokenRouter/internal/backup/httpapi"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

// TestAdminImageStorageRoutesAreRemoved 验证异步图片存储配置下线且备份配置仍可用。
func TestAdminImageStorageRoutesAreRemoved(t *testing.T) {

	router := gin.New()
	admin := router.Group("/api/v1/admin")
	h := &routeTestHandlers{
		Admin: &routeTestAdminHandlers{
			Backup: backuphttp.NewBackupHandler(nil, nil),
		},
	}
	registerBackupRoutes(admin, h, func(c *gin.Context) { c.Next() })

	registered := make(map[string]bool)
	for _, route := range router.Routes() {
		registered[route.Method+" "+route.Path] = true
	}

	removed := []struct {
		method string
		path   string
	}{
		{method: http.MethodGet, path: "/api/v1/admin/backups/image-storage"},
		{method: http.MethodPut, path: "/api/v1/admin/backups/image-storage"},
		{method: http.MethodPost, path: "/api/v1/admin/backups/image-storage/test"},
	}
	for _, route := range removed {
		routeKey := route.method + " " + route.path
		require.False(t, registered[routeKey], "%s should not be registered", routeKey)

		w := httptest.NewRecorder()
		req := httptest.NewRequest(route.method, route.path, nil)
		router.ServeHTTP(w, req)
		require.Equal(t, http.StatusNotFound, w.Code, "method=%s path=%s", route.method, route.path)
	}

	for _, route := range []string{
		"GET /api/v1/admin/backups/storage-config",
		"PUT /api/v1/admin/backups/storage-config",
		"POST /api/v1/admin/backups/storage-config/test",
		"GET /api/v1/admin/backups/content-config",
		"PUT /api/v1/admin/backups/content-config",
		"GET /api/v1/admin/backups/s3-config",
		"PUT /api/v1/admin/backups/s3-config",
		"POST /api/v1/admin/backups/s3-config/test",
		"GET /api/v1/admin/backups/schedule",
		"PUT /api/v1/admin/backups/schedule",
	} {
		require.True(t, registered[route], "%s should remain registered", route)
	}
}

// TestAdminUpstreamBillingProbeRoutesAreRemoved 锁定声明倍率探测管理接口全部返回普通 404。
func TestAdminUpstreamBillingProbeRoutesAreRemoved(t *testing.T) {

	router := gin.New()
	admin := router.Group("/api/v1/admin")
	h := &routeTestHandlers{Admin: &routeTestAdminHandlers{
		AccountManagement:    &accounthttp.ManagementHandler{},
		SchedulerDiagnostics: &schedulerhttp.DiagnosticsHandler{},
		OAuth:                &accounthttp.ClaudeOAuthHandler{},
		OpenAIOAuth:          &accounthttp.OpenAIOAuthHandler{},
		CodexInviteReset:     &accounthttp.CodexInviteResetHandler{},
	}}
	registerAccountRoutes(admin, h, func(c *gin.Context) { c.Next() })

	registered := make(map[string]bool)
	for _, route := range router.Routes() {
		registered[route.Method+" "+route.Path] = true
	}
	removed := []struct {
		method      string
		routePath   string
		requestPath string
	}{
		{method: http.MethodGet, routePath: "/api/v1/admin/accounts/upstream-billing-probe/settings", requestPath: "/api/v1/admin/accounts/upstream-billing-probe/settings"},
		{method: http.MethodPut, routePath: "/api/v1/admin/accounts/upstream-billing-probe/settings", requestPath: "/api/v1/admin/accounts/upstream-billing-probe/settings"},
		{method: http.MethodPost, routePath: "/api/v1/admin/accounts/upstream-billing-probe/batch", requestPath: "/api/v1/admin/accounts/upstream-billing-probe/batch"},
		{method: http.MethodPut, routePath: "/api/v1/admin/accounts/:id/upstream-billing-probe", requestPath: "/api/v1/admin/accounts/42/upstream-billing-probe"},
		{method: http.MethodPost, routePath: "/api/v1/admin/accounts/:id/upstream-billing-probe", requestPath: "/api/v1/admin/accounts/42/upstream-billing-probe"},
	}
	for _, route := range removed {
		require.False(t, registered[route.method+" "+route.routePath])
		w := httptest.NewRecorder()
		req := httptest.NewRequest(route.method, route.requestPath, nil)
		router.ServeHTTP(w, req)
		require.Equal(t, http.StatusNotFound, w.Code, "method=%s path=%s", route.method, route.requestPath)
	}
}

func TestAdminAdvancedSchedulerScoreRoutesAreRegistered(t *testing.T) {

	router := gin.New()
	admin := router.Group("/api/v1/admin")
	h := &routeTestHandlers{Admin: &routeTestAdminHandlers{
		AccountManagement:    &accounthttp.ManagementHandler{},
		SchedulerDiagnostics: &schedulerhttp.DiagnosticsHandler{},
		OAuth:                &accounthttp.ClaudeOAuthHandler{},
		OpenAIOAuth:          &accounthttp.OpenAIOAuthHandler{},
		CodexInviteReset:     &accounthttp.CodexInviteResetHandler{},
	}}
	registerAccountRoutes(admin, h, func(c *gin.Context) { c.Next() })

	registered := make(map[string]bool)
	for _, route := range router.Routes() {
		registered[route.Method+" "+route.Path] = true
	}
	require.True(t, registered["GET /api/v1/admin/accounts/:id/advanced-scheduler-score"])
	require.True(t, registered["POST /api/v1/admin/accounts/:id/advanced-scheduler-score/preview"])
}

// TestCanonicalBackupIDRouteGuard 验证备份通配路由只接受服务实际生成的 ID 格式。
func TestCanonicalBackupIDRouteGuard(t *testing.T) {

	router := gin.New()
	router.GET("/backups/:id", requireCanonicalBackupID, func(c *gin.Context) {
		c.Status(http.StatusNoContent)
	})

	tests := []struct {
		name       string
		id         string
		wantStatus int
	}{
		{name: "canonical", id: "0a1b2c3d", wantStatus: http.StatusNoContent},
		{name: "removed fixed path", id: "image-storage", wantStatus: http.StatusNotFound},
		{name: "uppercase", id: "0A1B2C3D", wantStatus: http.StatusNotFound},
		{name: "invalid character", id: "0a1b2c3g", wantStatus: http.StatusNotFound},
		{name: "wrong length", id: "0a1b2c3", wantStatus: http.StatusNotFound},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			w := httptest.NewRecorder()
			req := httptest.NewRequest(http.MethodGet, "/backups/"+tt.id, nil)
			router.ServeHTTP(w, req)
			require.Equal(t, tt.wantStatus, w.Code)
		})
	}
}
