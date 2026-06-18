package routes

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/TokenFlux/TokenRouter/internal/handler"
	adminhandler "github.com/TokenFlux/TokenRouter/internal/handler/admin"
	"github.com/TokenFlux/TokenRouter/internal/service"
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
