package routes

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/TokenFlux/TokenRouter/internal/handler"
	"github.com/TokenFlux/TokenRouter/internal/handler/admin"
	"github.com/TokenFlux/TokenRouter/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

type qoderModelRouteSyncServiceStub struct{}

func (qoderModelRouteSyncServiceStub) SyncModels(_ context.Context, input service.QoderModelSyncInput) (*service.QoderModelSyncResult, error) {
	return &service.QoderModelSyncResult{Source: input.Source, Applied: input.Apply}, nil
}

func TestAdminRoutesQoderOAuthPathsAreRegistered(t *testing.T) {
	gin.SetMode(gin.TestMode)
	router := gin.New()
	qoderOAuthService := service.NewQoderOAuthService(nil)
	defer qoderOAuthService.Stop()

	registerQoderOAuthRoutes(
		router.Group("/api/v1/admin"),
		&handler.Handlers{
			Admin: &handler.AdminHandlers{
				QoderOAuth: admin.NewQoderOAuthHandler(qoderOAuthService),
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

func TestAdminRoutesQoderModelSyncPathIsRegistered(t *testing.T) {
	gin.SetMode(gin.TestMode)
	router := gin.New()

	registerQoderModelRoutes(
		router.Group("/api/v1/admin"),
		&handler.Handlers{
			Admin: &handler.AdminHandlers{
				QoderModels: admin.NewQoderModelSyncHandler(qoderModelRouteSyncServiceStub{}),
			},
		},
	)

	req := httptest.NewRequest(http.MethodPost, "/api/v1/admin/qoder/models/sync", strings.NewReader(`{"source":"local"}`))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()

	router.ServeHTTP(rec, req)
	require.NotEqual(t, http.StatusNotFound, rec.Code)
}
