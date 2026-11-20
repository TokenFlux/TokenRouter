package httpapi

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"

	"github.com/TokenFlux/TokenRouter/internal/provider"
)

// gemini_oauth_test.go exercises Gemini OAuth HTTP request validation,
// google_one rejection, invalid project_id error mapping, and ProjectID
// forwarding during code exchange.

// stubGeminiAuthUseCase records GenerateAuthURL and ExchangeCode arguments for
// handler verification.
type stubGeminiAuthUseCase struct {
	authURLProject string                            // captured GenerateAuthURL project_id
	authURLType    string                            // captured GenerateAuthURL oauth_type
	authURLErr     error                             // configured GenerateAuthURL failure
	exchangeInput  *provider.GeminiExchangeCodeInput // captured ExchangeCode input
}

// GetOAuthConfig returns a static capability snapshot for handler tests.
func (s *stubGeminiAuthUseCase) GetOAuthConfig() *provider.GeminiOAuthCapabilities {
	return &provider.GeminiOAuthCapabilities{AIStudioOAuthEnabled: true, RequiredRedirectURIs: []string{"http://localhost:1455/auth/callback"}}
}

// GenerateAuthURL records projectID and oauthType and returns authURLErr or a
// stub session payload.
func (s *stubGeminiAuthUseCase) GenerateAuthURL(_ context.Context, _ *int64, _, projectID, oauthType, _ string) (*provider.GeminiAuthURLResult, error) {
	s.authURLProject = projectID
	s.authURLType = oauthType
	if s.authURLErr != nil {
		return nil, s.authURLErr
	}
	return &provider.GeminiAuthURLResult{AuthURL: "https://accounts.google.com/o/oauth2/v2/auth", SessionID: "sess-1", State: "state-1"}, nil
}

// ExchangeCode records input and returns a populated GeminiTokenInfo payload.
func (s *stubGeminiAuthUseCase) ExchangeCode(_ context.Context, input *provider.GeminiExchangeCodeInput) (*provider.GeminiTokenInfo, error) {
	s.exchangeInput = input
	return &provider.GeminiTokenInfo{
		AccessToken:  "at-1",
		RefreshToken: "rt-1",
		ProjectID:    input.ProjectID,
		OAuthType:    input.OAuthType,
		TierID:       provider.GeminiTierGCPStandard,
	}, nil
}

// TestGeminiOAuthHandlerRejectsGoogleOneAndMapsInvalidProjectTo400 verifies
// that auth-url and exchange-code reject legacy google_one variants with 400
// and classify invalid project_id errors from the use case as 400 Bad Request.
func TestGeminiOAuthHandlerRejectsGoogleOneAndMapsInvalidProjectTo400(t *testing.T) {
	t.Parallel()

	stub := &stubGeminiAuthUseCase{}
	handler := NewGeminiOAuthHandler(stub)
	router := gin.New()
	router.POST("/api/v1/admin/gemini/oauth/auth-url", handler.GenerateAuthURL)
	router.POST("/api/v1/admin/gemini/oauth/exchange-code", handler.ExchangeCode)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/v1/admin/gemini/oauth/auth-url", bytes.NewBufferString(`{"oauth_type":"google_one"}`))
	req.Header.Set("Content-Type", "application/json")
	router.ServeHTTP(rec, req)
	require.Equal(t, http.StatusBadRequest, rec.Code)
	require.Contains(t, rec.Body.String(), "Antigravity")

	rec = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodPost, "/api/v1/admin/gemini/oauth/exchange-code", bytes.NewBufferString(`{"session_id":"s1","state":"st","code":"cd","oauth_type":"google_one"}`))
	req.Header.Set("Content-Type", "application/json")
	router.ServeHTTP(rec, req)
	require.Equal(t, http.StatusBadRequest, rec.Code)
	require.Contains(t, rec.Body.String(), "Antigravity")

	stub.authURLErr = errors.New(`invalid project_id "123456": appears to be a numeric GCP Project Number`)
	rec = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodPost, "/api/v1/admin/gemini/oauth/auth-url", bytes.NewBufferString(`{"oauth_type":"code_assist","project_id":"123456"}`))
	req.Header.Set("Content-Type", "application/json")
	router.ServeHTTP(rec, req)
	require.Equal(t, http.StatusBadRequest, rec.Code)
	require.Contains(t, rec.Body.String(), "numeric GCP Project Number")
}

// TestGeminiOAuthHandlerExchangeCodeForwardsProjectID submits an exchange-code
// body carrying an explicit project_id and checks that the use case receives
// the override alongside default code_assist normalization.
func TestGeminiOAuthHandlerExchangeCodeForwardsProjectID(t *testing.T) {
	t.Parallel()

	stub := &stubGeminiAuthUseCase{}
	handler := NewGeminiOAuthHandler(stub)
	router := gin.New()
	router.POST("/api/v1/admin/gemini/oauth/exchange-code", handler.ExchangeCode)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/v1/admin/gemini/oauth/exchange-code", bytes.NewBufferString(`{"session_id":"s1","state":"st1","code":"c1","project_id":"my-gcp-proj"}`))
	req.Header.Set("Content-Type", "application/json")
	router.ServeHTTP(rec, req)

	require.Equal(t, http.StatusOK, rec.Code)
	require.NotNil(t, stub.exchangeInput)
	require.Equal(t, "my-gcp-proj", stub.exchangeInput.ProjectID)
	require.Equal(t, "code_assist", stub.exchangeInput.OAuthType)

	var body struct {
		Data provider.GeminiTokenInfo `json:"data"` // decoded token response
	}
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &body))
	require.Equal(t, "my-gcp-proj", body.Data.ProjectID)
}
