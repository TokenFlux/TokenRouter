package routes

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/TokenFlux/TokenRouter/internal/handler"
	"github.com/TokenFlux/TokenRouter/internal/server/middleware"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

// TestRemovedFeatureRoutesReturnNotFound 锁定下线功能的用户端和管理端路径不再注册。
func TestRemovedFeatureRoutesReturnNotFound(t *testing.T) {
	gin.SetMode(gin.TestMode)
	router := gin.New()
	allHandlers := &handler.Handlers{Admin: &handler.AdminHandlers{}}
	RegisterUserRoutes(
		router.Group("/api/v1"),
		allHandlers,
		middleware.JWTAuthMiddleware(func(c *gin.Context) { c.Next() }),
		middleware.AuditLogMiddleware(func(c *gin.Context) { c.Next() }),
		middleware.StepUpAuthMiddleware(func(c *gin.Context) { c.Next() }),
		nil,
		nil,
	)
	RegisterAdminRoutes(
		router.Group("/api/v1"),
		allHandlers,
		middleware.AdminAuthMiddleware(func(c *gin.Context) { c.Next() }),
		middleware.AuditLogMiddleware(func(c *gin.Context) { c.Next() }),
		middleware.StepUpAuthMiddleware(func(c *gin.Context) { c.Next() }),
		nil, func(c *gin.Context) { c.Status(http.StatusOK) })

	removedPath := "/api/v1/" + "data" + "-sharing"
	for _, path := range []string{
		removedPath,
		removedPath + "/export/download",
		"/api/v1/admin/" + "data" + "-sharing",
		"/api/v1/admin/" + "data" + "-sharing/exports/download",
	} {
		for _, method := range []string{http.MethodGet, http.MethodPost, http.MethodPut, http.MethodDelete} {
			recorder := httptest.NewRecorder()
			request := httptest.NewRequest(method, path, nil)
			router.ServeHTTP(recorder, request)
			require.Equal(t, http.StatusNotFound, recorder.Code, "method=%s path=%s", method, path)
		}
	}
}
