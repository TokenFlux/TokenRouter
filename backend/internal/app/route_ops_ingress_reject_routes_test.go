package app

import (
	"net/http"
	"net/http/httptest"
	"testing"

	identityhttp "github.com/TokenFlux/TokenRouter/internal/identity/httpapi"

	httpx "github.com/TokenFlux/TokenRouter/internal/server/httpx"

	opshttp "github.com/TokenFlux/TokenRouter/internal/ops/httpapi"
	servermiddleware "github.com/TokenFlux/TokenRouter/internal/server/middleware"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestOpsAdminRoutesRequireAdminAuthentication(t *testing.T) {

	router := gin.New()
	handlers := &routeTestHandlers{Admin: &routeTestAdminHandlers{Ops: opshttp.NewOpsHandler(nil)}}
	adminAuth := identityhttp.AdminAuthMiddleware(func(c *gin.Context) {
		if c.GetHeader("Authorization") == "" {
			httpx.AbortWithError(c, http.StatusUnauthorized, "UNAUTHORIZED", "Authorization required")
			return
		}
		httpx.AbortWithError(c, http.StatusForbidden, "FORBIDDEN", "Admin access required")
	})
	auditLog := servermiddleware.AuditLogMiddleware(func(c *gin.Context) { c.Next() })
	stepUp := identityhttp.StepUpAuthMiddleware(func(c *gin.Context) { c.Next() })
	RegisterAdminRoutes(router.Group("/api/v1"), handlers, adminAuth, auditLog, stepUp, nil, func(c *gin.Context) { c.Status(http.StatusOK) })

	for _, path := range []string{
		"/api/v1/admin/ops/ingress-rejections",
		"/api/v1/admin/ops/ingress-rejections/health",
		"/api/v1/admin/ops/dashboard/token-stats",
		"/api/v1/admin/ops/dashboard/openai-token-stats",
	} {
		for _, tc := range []struct {
			name       string
			auth       string
			wantStatus int
		}{
			{name: "unauthenticated", wantStatus: http.StatusUnauthorized},
			{name: "non-admin", auth: "Bearer user-token", wantStatus: http.StatusForbidden},
		} {
			t.Run(path+"/"+tc.name, func(t *testing.T) {
				recorder := httptest.NewRecorder()
				request := httptest.NewRequest(http.MethodGet, path, nil)
				if tc.auth != "" {
					request.Header.Set("Authorization", tc.auth)
				}
				router.ServeHTTP(recorder, request)
				require.Equal(t, tc.wantStatus, recorder.Code)
			})
		}
	}
}
