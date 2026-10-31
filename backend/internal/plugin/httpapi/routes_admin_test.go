package httpapi

import (
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"

	"github.com/TokenFlux/TokenRouter/internal/server/adminapi"
)

// TestPluginWritesEnforceStepUpOnDirectHTTP dispatches every operation marked for additional administrator verification.
// The guard aborts before nil service dependencies are reached, proving metadata and actual handler protection agree.
func TestPluginWritesEnforceStepUpOnDirectHTTP(t *testing.T) {
	gin.SetMode(gin.TestMode)
	router := gin.New()
	RegisterAdminRoutes(router.Group("/api/v1/admin"), &Handler{}, func(c *gin.Context) { c.AbortWithStatus(418) })
	catalog, err := adminapi.FromRoutes(router.Routes())
	require.NoError(t, err)
	require.Len(t, catalog.Operations(), len(ManagementOperations))
	for _, operation := range ManagementOperations {
		if !operation.StepUp {
			continue
		}
		request := httptest.NewRequest(operation.Method, strings.ReplaceAll(operation.Path, ":id", "test.plugin"), nil)
		response := httptest.NewRecorder()
		router.ServeHTTP(response, request)
		require.Equal(t, 418, response.Code, operation.ID)
	}
}
