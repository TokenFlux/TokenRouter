package httpapi_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/TokenFlux/TokenRouter/internal/identity/httpapi/authctx"
	"github.com/TokenFlux/TokenRouter/internal/site"
	"github.com/TokenFlux/TokenRouter/internal/site/filesystem"
	"github.com/TokenFlux/TokenRouter/internal/site/httpapi"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

type pageMenuFixture struct{}

func (pageMenuFixture) GetCustomMenuItemsRaw(context.Context) string {
	return `[{"page_slug":"guide","visibility":"user"},{"page_slug":"private","visibility":"admin"}]`
}
func TestPageHTTPBoundaryAndVisibility(t *testing.T) {
	data := t.TempDir()
	store := filesystem.New(data)
	pages := filepath.Join(data, "pages")
	outside := filepath.Join(t.TempDir(), "outside.md")
	require.NoError(t, os.WriteFile(outside, []byte("private-fixture"), 0600))
	require.NoError(t, os.Symlink(outside, filepath.Join(pages, "guide.md")))
	require.NoError(t, os.WriteFile(filepath.Join(pages, "private.md"), []byte("admin content"), 0600))
	h := httpapi.NewPageHandler(site.NewPages(store, pageMenuFixture{}))
	router := gin.New()
	auth := func(c *gin.Context) {
		role := c.GetHeader("X-Fixture-Role")
		if role == "" {
			c.AbortWithStatus(http.StatusUnauthorized)
			return
		}
		c.Set(authctx.ContextKeyUserRole, role)
		c.Next()
	}
	admin := func(c *gin.Context) {
		if c.GetHeader("X-Fixture-Role") != "admin" {
			c.AbortWithStatus(http.StatusForbidden)
			return
		}
		c.Next()
	}
	h.Register(router.Group("/api/v1"), auth, admin)
	for _, test := range []struct {
		path, role string
		status     int
	}{
		{"/api/v1/pages/guide", "", 401}, {"/api/v1/pages/guide", "user", 404}, {"/api/v1/pages/private", "user", 404}, {"/api/v1/pages/private", "admin", 200}, {"/api/v1/pages", "user", 403}, {"/api/v1/pages", "admin", 200}, {"/api/v1/pages/private/images/x.png", "admin", 404},
	} {
		request := httptest.NewRequest(http.MethodGet, test.path, nil)
		request.Header.Set("X-Fixture-Role", test.role)
		response := httptest.NewRecorder()
		router.ServeHTTP(response, request)
		require.Equal(t, test.status, response.Code, test.path)
		require.NotContains(t, response.Body.String(), "private-fixture")
	}
}
