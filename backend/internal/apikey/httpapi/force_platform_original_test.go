//go:build unit

package httpapi_test

import (
	"net/http"
	"net/http/httptest"
	"testing"

	keyhttp "github.com/TokenFlux/TokenRouter/internal/apikey/httpapi"

	"github.com/TokenFlux/TokenRouter/internal/apikey"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestForcePlatform_SetsContextAndGinValue(t *testing.T) {

	r := gin.New()
	r.Use(keyhttp.ForcePlatform("anthropic"))
	r.GET("/t", func(c *gin.Context) {
		require.True(t, keyhttp.HasForcePlatform(c))
		v, ok := keyhttp.GetForcePlatformFromContext(c)
		require.True(t, ok)
		require.Equal(t, "anthropic", v)

		ctxV, present := apikey.ForcePlatformFromContext(c.Request.Context())
		require.True(t, present)
		require.Equal(t, "anthropic", ctxV)
		c.Status(http.StatusOK)
	})

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/t", nil)
	r.ServeHTTP(w, req)
	require.Equal(t, http.StatusOK, w.Code)
}
