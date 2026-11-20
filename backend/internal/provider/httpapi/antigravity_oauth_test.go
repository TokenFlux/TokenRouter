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

	"github.com/TokenFlux/TokenRouter/internal/provider"
)

// antigravity_oauth_test.go exercises Antigravity OAuth HTTP handlers for
// code exchange and refresh-token validation, verifying optional project_id
// forwarding on both endpoints.

// stubAntigravityAuthUseCase captures ExchangeCode and
// ValidateRefreshTokenWithProject arguments for handler assertions.
type stubAntigravityAuthUseCase struct {
	exchangeInput    *provider.AntigravityExchangeCodeInput // captured ExchangeCode payload
	refreshToken     string                                 // captured refresh_token input
	refreshProjectID string                                 // captured refresh project_id hint
}

// GenerateAuthURL returns a static Antigravity session response for handler
// routing tests.
func (s *stubAntigravityAuthUseCase) GenerateAuthURL(context.Context, *int64) (*provider.AntigravityAuthURLResult, error) {
	return &provider.AntigravityAuthURLResult{AuthURL: "https://accounts.google.com/o/oauth2/v2/auth", SessionID: "ag-s1", State: "ag-st1"}, nil
}

// ExchangeCode records input and echoes its ProjectID in the returned token
// metadata.
func (s *stubAntigravityAuthUseCase) ExchangeCode(_ context.Context, input *provider.AntigravityExchangeCodeInput) (*provider.AntigravityTokenInfo, error) {
	s.exchangeInput = input
	return &provider.AntigravityTokenInfo{
		AccessToken:  "ag-at",
		RefreshToken: "ag-rt",
		ProjectID:    input.ProjectID,
		PlanType:     "Pro",
	}, nil
}

// ValidateRefreshTokenWithProject records refreshToken and projectID and
// returns a populated AntigravityTokenInfo payload.
func (s *stubAntigravityAuthUseCase) ValidateRefreshTokenWithProject(_ context.Context, refreshToken, projectID string, _ *int64) (*provider.AntigravityTokenInfo, error) {
	s.refreshToken = refreshToken
	s.refreshProjectID = projectID
	return &provider.AntigravityTokenInfo{
		AccessToken:  "ag-rt-at",
		RefreshToken: refreshToken,
		ProjectID:    projectID,
		PlanType:     "Ultra",
	}, nil
}

// TestAntigravityOAuthHandlerForwardsProjectIDOnExchangeAndRefresh submits
// optional project_id values to both exchange-code and refresh-token endpoints
// and checks that the use case receives both hints intact.
func TestAntigravityOAuthHandlerForwardsProjectIDOnExchangeAndRefresh(t *testing.T) {
	t.Parallel()

	stub := &stubAntigravityAuthUseCase{}
	handler := NewAntigravityOAuthHandler(stub)
	router := gin.New()
	router.POST("/api/v1/admin/antigravity/oauth/exchange-code", handler.ExchangeCode)
	router.POST("/api/v1/admin/antigravity/oauth/refresh-token", handler.RefreshToken)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/v1/admin/antigravity/oauth/exchange-code", bytes.NewBufferString(`{"session_id":"ag-s1","state":"ag-st1","code":"ag-c1","project_id":"ag-proj-1"}`))
	req.Header.Set("Content-Type", "application/json")
	router.ServeHTTP(rec, req)
	require.Equal(t, http.StatusOK, rec.Code)
	require.NotNil(t, stub.exchangeInput)
	require.Equal(t, "ag-proj-1", stub.exchangeInput.ProjectID)

	rec = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodPost, "/api/v1/admin/antigravity/oauth/refresh-token", bytes.NewBufferString(`{"refresh_token":"rt-99","project_id":"ag-proj-2"}`))
	req.Header.Set("Content-Type", "application/json")
	router.ServeHTTP(rec, req)
	require.Equal(t, http.StatusOK, rec.Code)
	require.Equal(t, "rt-99", stub.refreshToken)
	require.Equal(t, "ag-proj-2", stub.refreshProjectID)

	var body struct {
		Data provider.AntigravityTokenInfo `json:"data"` // decoded token response
	}
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &body))
	require.Equal(t, "ag-proj-2", body.Data.ProjectID)
	require.Equal(t, "Ultra", body.Data.PlanType)
}
