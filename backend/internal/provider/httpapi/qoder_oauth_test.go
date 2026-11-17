package httpapi

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"

	provideradapter "github.com/TokenFlux/TokenRouter/internal/provider/provider"
)

// Qoder site selection and required session fields are exercised through
// qoder_oauth.go with the production authorization adapter. Device sessions are
// generated locally, and each fixture starts then cleans up its session owner
// so repeated tests retain their authorization lifetime.

// TestQoderOAuthHandlerGenerateAuthURL generates a device session with empty
// JSON and production option defaults. The response contains the global browser
// URL, session, state, expiration and polling interval.
func TestQoderOAuthHandlerGenerateAuthURL(t *testing.T) {
	router := gin.New()
	svc := provideradapter.NewQoderAuthorization(func(context.Context, *int64) (string, error) { return "", nil }, nil)
	svc.Core.Start()
	// Core.Start runs session cleanup for this fixture. Stop that owner after
	// the handler assertions so cleanup and any registered authorization work finish.
	t.Cleanup(func() { require.NoError(t, svc.Core.StopContext(context.Background())) })
	handler := NewQoderOAuthHandler(svc)
	router.POST("/api/v1/admin/qoder/oauth/auth-url", handler.GenerateAuthURL)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/v1/admin/qoder/oauth/auth-url", bytes.NewBufferString(`{}`))
	req.Header.Set("Content-Type", "application/json")
	router.ServeHTTP(rec, req)

	require.Equal(t, http.StatusOK, rec.Code)
	var resp map[string]any
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &resp))
	require.Equal(t, float64(0), resp["code"])
	data, ok := resp["data"].(map[string]any)
	require.True(t, ok)
	require.Contains(t, data["auth_url"], "https://qoder.com/device/selectAccounts")
	require.NotEmpty(t, data["session_id"])
	require.NotEmpty(t, data["state"])
	require.NotZero(t, data["expires_in"])
	require.NotZero(t, data["interval"])
}

// TestQoderOAuthHandlerExchangeCodeValidatesRequest submits state while
// omitting the required session identifier. HTTP binding returns 400 and names
// SessionID before device authorization can perform completion.
func TestQoderOAuthHandlerExchangeCodeValidatesRequest(t *testing.T) {
	router := gin.New()
	svc := provideradapter.NewQoderAuthorization(func(context.Context, *int64) (string, error) { return "", nil }, nil)
	svc.Core.Start()
	// Core.Start runs session cleanup for this fixture. Stop that owner after
	// the handler assertions so cleanup and any registered authorization work finish.
	t.Cleanup(func() { require.NoError(t, svc.Core.StopContext(context.Background())) })
	handler := NewQoderOAuthHandler(svc)
	router.POST("/api/v1/admin/qoder/oauth/exchange-code", handler.ExchangeCode)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/v1/admin/qoder/oauth/exchange-code", bytes.NewBufferString(`{"state":"state"}`))
	req.Header.Set("Content-Type", "application/json")
	router.ServeHTTP(rec, req)

	require.Equal(t, http.StatusBadRequest, rec.Code)
	require.Contains(t, rec.Body.String(), "SessionID")
}

// TestQoderOAuthHandlerGenerateAuthURLAcceptsCNAndRejectsUnknownSite requests a
// CN device session and an unsupported site. CN output identifies its browser
// host and site, while unsupported input returns 400 with the site validation
// message.
func TestQoderOAuthHandlerGenerateAuthURLAcceptsCNAndRejectsUnknownSite(t *testing.T) {
	router := gin.New()
	svc := provideradapter.NewQoderAuthorization(func(context.Context, *int64) (string, error) { return "", nil }, nil)
	svc.Core.Start()
	// Core.Start runs session cleanup for this fixture. Stop that owner after
	// the handler assertions so cleanup and any registered authorization work finish.
	t.Cleanup(func() { require.NoError(t, svc.Core.StopContext(context.Background())) })
	handler := NewQoderOAuthHandler(svc)
	router.POST("/api/v1/admin/qoder/oauth/auth-url", handler.GenerateAuthURL)

	cnRecorder := httptest.NewRecorder()
	cnRequest := httptest.NewRequest(http.MethodPost, "/api/v1/admin/qoder/oauth/auth-url", bytes.NewBufferString(`{"site":"cn"}`))
	cnRequest.Header.Set("Content-Type", "application/json")
	router.ServeHTTP(cnRecorder, cnRequest)
	require.Equal(t, http.StatusOK, cnRecorder.Code)
	require.Contains(t, cnRecorder.Body.String(), "qoder.cn")
	require.Contains(t, cnRecorder.Body.String(), `"site":"cn"`)

	invalidRecorder := httptest.NewRecorder()
	invalidRequest := httptest.NewRequest(http.MethodPost, "/api/v1/admin/qoder/oauth/auth-url", bytes.NewBufferString(`{"site":"invalid"}`))
	invalidRequest.Header.Set("Content-Type", "application/json")
	router.ServeHTTP(invalidRecorder, invalidRequest)
	require.Equal(t, http.StatusBadRequest, invalidRecorder.Code)
	require.Contains(t, invalidRecorder.Body.String(), "unsupported site")
}
