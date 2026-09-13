package app

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/TokenFlux/TokenRouter/internal/handler"
	"github.com/TokenFlux/TokenRouter/internal/handler/admin"
	"github.com/TokenFlux/TokenRouter/internal/protocol"
	routinghttpapi "github.com/TokenFlux/TokenRouter/internal/routing/httpapi"
	"github.com/TokenFlux/TokenRouter/internal/server/middleware"
	"github.com/TokenFlux/TokenRouter/internal/server/routes"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

// 管理员目录沿用认证和审计顺序，注入后修改原投影不改变 HTTP 输出。
func TestProtocolCatalogHTTPContract(t *testing.T) {
	gin.SetMode(gin.TestMode)
	endpoints := testEndpoints()
	expected, err := json.Marshal(routinghttpapi.AdminProtocolCatalog(endpoints))
	require.NoError(t, err)
	catalog := routinghttpapi.NewProtocolCatalogHandler(endpoints)
	endpoints[protocol.ProtocolOpenAIResponses] = "unexpected-change"
	var calls []string
	auth := middleware.AdminAuthMiddleware(func(c *gin.Context) {
		calls = append(calls, "auth")
		switch c.GetHeader("Authorization") {
		case "admin":
			c.Next()
		case "user":
			c.AbortWithStatus(http.StatusForbidden)
		default:
			c.AbortWithStatus(http.StatusUnauthorized)
		}
	})
	audit := middleware.AuditLogMiddleware(func(c *gin.Context) {
		calls = append(calls, "audit")
		c.Next()
	})
	router := gin.New()
	routes.RegisterAdminRoutes(router.Group("/api/v1"), &handler.Handlers{Admin: &handler.AdminHandlers{Proxy: admin.NewProxyHandler(nil), Group: routinghttpapi.NewGroupHandler(nil)}}, auth, audit, nil, nil, func(c *gin.Context) {
		calls = append(calls, "catalog")
		catalog(c)
	})
	for _, tc := range []struct {
		auth   string
		status int
	}{
		{"", http.StatusUnauthorized},
		{"user", http.StatusForbidden},
		{"admin", http.StatusOK},
	} {
		calls = nil
		recorder := httptest.NewRecorder()
		request := httptest.NewRequest(http.MethodGet, "/api/v1/admin/protocol-capabilities", nil)
		request.Header.Set("Authorization", tc.auth)
		router.ServeHTTP(recorder, request)
		require.Equal(t, tc.status, recorder.Code)
		if tc.status != http.StatusOK {
			require.Equal(t, []string{"auth"}, calls)
			continue
		}
		require.Equal(t, []string{"auth", "audit", "catalog"}, calls)
		var response struct {
			Code int             `json:"code"`
			Data json.RawMessage `json:"data"`
		}
		require.NoError(t, json.Unmarshal(recorder.Body.Bytes(), &response))
		require.Zero(t, response.Code)
		require.JSONEq(t, string(expected), string(response.Data))
	}
}
