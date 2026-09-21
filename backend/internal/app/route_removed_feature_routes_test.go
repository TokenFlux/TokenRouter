package app

import (
	"net/http"
	"net/http/httptest"
	"testing"

	identityhttp "github.com/TokenFlux/TokenRouter/internal/identity/httpapi"

	"github.com/TokenFlux/TokenRouter/internal/server/middleware"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

// TestRemovedFeatureRoutesReturnNotFound 锁定下线功能的用户端和管理端路径不再注册。
func TestRemovedFeatureRoutesReturnNotFound(t *testing.T) {

	router := gin.New()
	// 身份处理器已通过模块嵌入组合，夹具需提供外层接收者后才能登记方法值。
	allHandlers := &routeTestHandlers{User: &identityhttp.UserHandler{}, Admin: &routeTestAdminHandlers{}}
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
