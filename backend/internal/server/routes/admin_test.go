package routes

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/BrandonVee/TokenRouter/internal/handler"
	adminhandler "github.com/BrandonVee/TokenRouter/internal/handler/admin"
	"github.com/BrandonVee/TokenRouter/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func newAdminRoutesDataSharingTestRouter() *gin.Engine {
	gin.SetMode(gin.TestMode)
	router := gin.New()
	v1 := router.Group("/api/v1")
	h := &handler.Handlers{
		Admin: &handler.AdminHandlers{
			DataSharing: adminhandler.NewDataSharingHandler(service.NewDataSharingService(nil, nil)),
		},
	}
	// 管理端预生成文件下载是公开票据路由，认证后的管理路由只注册 data-sharing 子集。
	v1.GET("/admin/data-sharing/exports/download", h.Admin.DataSharing.DownloadExportArtifact)
	admin := v1.Group("/admin")
	admin.Use(func(c *gin.Context) {
		c.Next()
	})
	registerDataSharingRoutes(admin, h)
	return router
}

func TestAdminDataSharingRealtimeExportRoutesAreRemoved(t *testing.T) {
	router := newAdminRoutesDataSharingTestRouter()
	removedRoutes := []struct {
		method string
		path   string
	}{
		{method: http.MethodGet, path: "/api/v1/admin/data-sharing/export/download?ticket=old"},
		{method: http.MethodPost, path: "/api/v1/admin/data-sharing/export-ticket"},
		{method: http.MethodPost, path: "/api/v1/admin/data-sharing/sessions/7/export-ticket"},
	}

	for _, route := range removedRoutes {
		w := httptest.NewRecorder()
		req := httptest.NewRequest(route.method, route.path, nil)
		router.ServeHTTP(w, req)
		require.Equal(t, http.StatusNotFound, w.Code, "method=%s path=%s", route.method, route.path)
	}
}

func TestAdminDataSharingArtifactDownloadRouteIsStillRegistered(t *testing.T) {
	router := newAdminRoutesDataSharingTestRouter()

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/v1/admin/data-sharing/exports/download?ticket=bad", nil)
	router.ServeHTTP(w, req)

	require.NotEqual(t, http.StatusNotFound, w.Code)
}

// TestAdminImageStorageRoutesAreRemoved 验证异步图片存储配置下线且备份配置仍可用。
func TestAdminImageStorageRoutesAreRemoved(t *testing.T) {
	gin.SetMode(gin.TestMode)
	router := gin.New()
	admin := router.Group("/api/v1/admin")
	h := &handler.Handlers{
		Admin: &handler.AdminHandlers{
			Backup: adminhandler.NewBackupHandler(nil, nil),
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
	gin.SetMode(gin.TestMode)
	router := gin.New()
	admin := router.Group("/api/v1/admin")
	h := &handler.Handlers{Admin: &handler.AdminHandlers{
		Account:          &adminhandler.AccountHandler{},
		OAuth:            &adminhandler.OAuthHandler{},
		OpenAIOAuth:      &adminhandler.OpenAIOAuthHandler{},
		CodexInviteReset: &adminhandler.CodexInviteResetHandler{},
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
	gin.SetMode(gin.TestMode)
	router := gin.New()
	admin := router.Group("/api/v1/admin")
	h := &handler.Handlers{Admin: &handler.AdminHandlers{
		Account:          &adminhandler.AccountHandler{},
		OAuth:            &adminhandler.OAuthHandler{},
		OpenAIOAuth:      &adminhandler.OpenAIOAuthHandler{},
		CodexInviteReset: &adminhandler.CodexInviteResetHandler{},
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
	gin.SetMode(gin.TestMode)
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
