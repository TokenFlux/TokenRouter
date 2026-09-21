package app

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	accounthttp "github.com/TokenFlux/TokenRouter/internal/account/httpapi"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestAdminRoutesQoderOAuthPathsAreRegistered(t *testing.T) {

	router := gin.New()
	qoderOAuthService := provideQoderAuthorization(nil)
	qoderOAuthService.Core.Start()
	t.Cleanup(func() { require.NoError(t, qoderOAuthService.Core.StopContext(context.Background())) })

	registerQoderOAuthRoutes(
		router.Group("/api/v1/admin"),
		&routeTestHandlers{
			Admin: &routeTestAdminHandlers{
				QoderOAuth: accounthttp.NewQoderOAuthHandler(qoderOAuthService),
			},
		},
	)

	for _, tc := range []struct {
		path string
		body string
	}{
		{path: "/api/v1/admin/qoder/oauth/auth-url", body: `{}`},
		{path: "/api/v1/admin/qoder/oauth/exchange-code", body: `{"session_id":"missing","state":"state","code":"code"}`},
		{path: "/api/v1/admin/qoder/oauth/poll", body: `{"session_id":"missing","state":"state"}`},
	} {
		req := httptest.NewRequest(http.MethodPost, tc.path, strings.NewReader(tc.body))
		req.Header.Set("Content-Type", "application/json")
		rec := httptest.NewRecorder()

		router.ServeHTTP(rec, req)
		require.NotEqual(t, http.StatusNotFound, rec.Code, "path=%s should hit Qoder OAuth handler", tc.path)
	}
}
