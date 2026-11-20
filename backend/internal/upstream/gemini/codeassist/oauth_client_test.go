package codeassist

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// oauth_client_test.go verifies HTTP client pooling and client-credential
// selection across Code Assist and AI Studio OAuth token exchanges.

// TestCreateOAuthHTTPClient_TimeoutAndReuse constructs two OAuth HTTP clients
// configured with the same HTTP proxy URL. Both calls must return the same
// pooled client instance with a sixty-second timeout.
func TestCreateOAuthHTTPClient_TimeoutAndReuse(t *testing.T) {
	first, err := CreateOAuthHTTPClient("http://proxy.local:8080")
	require.NoError(t, err)
	require.Equal(t, 60*time.Second, first.Timeout)

	second, err := CreateOAuthHTTPClient("http://proxy.local:8080")
	require.NoError(t, err)
	require.Same(t, first, second)
}

// TestOAuthClient_ExchangeAndRefreshClientSelection posts code exchanges and
// token refreshes for Code Assist and AI Studio when custom credentials exist.
// Code Assist must send the built-in CLI client ID, while AI Studio must send
// the configured custom client ID.
func TestOAuthClient_ExchangeAndRefreshClientSelection(t *testing.T) {
	var seenClientIDs []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.NoError(t, r.ParseForm())
		seenClientIDs = append(seenClientIDs, r.FormValue("client_id"))
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(TokenResponse{
			AccessToken:  "at",
			RefreshToken: "rt",
			ExpiresIn:    3600,
			TokenType:    "Bearer",
		})
	}))
	defer srv.Close()

	client := &OAuthClient{
		TokenURL: srv.URL,
		Config: func() OAuthConfig {
			return OAuthConfig{
				ClientID:     "custom-ai-studio-id",
				ClientSecret: "custom-ai-studio-secret",
			}
		},
	}

	_, err := client.ExchangeCode(context.Background(), "code_assist", "code-1", "verifier", GeminiCLIRedirectURI, "")
	require.NoError(t, err)
	_, err = client.RefreshToken(context.Background(), "code_assist", "rt-1", "")
	require.NoError(t, err)
	_, err = client.ExchangeCode(context.Background(), "ai_studio", "code-2", "verifier", AIStudioOAuthRedirectURI, "")
	require.NoError(t, err)

	require.Equal(t, []string{
		GeminiCLIOAuthClientID,
		GeminiCLIOAuthClientID,
		"custom-ai-studio-id",
	}, seenClientIDs)
}
