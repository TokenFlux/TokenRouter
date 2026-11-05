package grok

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// TestGrokOAuthClientExchangeAndRefreshUseFormFields serves a loopback token
// endpoint for code and refresh grants. It asserts the posted form fields and
// decoded token values, including verifier use for code exchange and rotated
// refresh-token data.
func TestGrokOAuthClientExchangeAndRefreshUseFormFields(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, http.MethodPost, r.Method)
		require.NoError(t, r.ParseForm())
		require.Equal(t, "client-id", r.Form.Get("client_id"))

		switch r.Form.Get("grant_type") {
		case "authorization_code":
			require.Equal(t, "auth-code", r.Form.Get("code"))
			require.Equal(t, "http://127.0.0.1:56121/callback", r.Form.Get("redirect_uri"))
			require.Equal(t, "verifier", r.Form.Get("code_verifier"))
			require.Empty(t, r.Form.Get("code_challenge"))
			require.Empty(t, r.Form.Get("code_challenge_method"))
			_ = json.NewEncoder(w).Encode(map[string]any{
				"access_token": "exchange-access",

				"refresh_token": "exchange-refresh",

				"token_type": "Bearer",

				"expires_in": 3600,

				"scope": "openid api:access",
			})
		case "refresh_token":
			require.Equal(t, "refresh-token", r.Form.Get("refresh_token"))
			_ = json.NewEncoder(w).Encode(map[string]any{
				"access_token": "refresh-access",

				"refresh_token": "refresh-rotated",

				"token_type": "Bearer",

				"expires_in": 7200,
			})
		default:
			http.Error(w, "unexpected grant_type", http.StatusBadRequest)
		}
	}))
	defer server.Close()
	// A local token endpoint supplies deterministic grant responses. Enable format-only
	// overrides so this fixture can use HTTP loopback while production defaults remain trusted-host based.
	setGrokTestOverride(t, "AllowUnsafeURLOverrides", "true")
	setGrokTestOverride(t, "TokenURL", server.URL)

	client := NewOAuthClient()
	exchanged, err := client.ExchangeCode(context.Background(), "auth-code", "verifier", "http://127.0.0.1:56121/callback", "", "client-id")
	require.NoError(t, err)
	require.Equal(t, "exchange-access", exchanged.AccessToken)
	require.Equal(t, "exchange-refresh", exchanged.RefreshToken)
	require.Equal(t, int64(3600), exchanged.ExpiresIn)
	require.Equal(t, "openid api:access", exchanged.Scope)

	refreshed, err := client.RefreshToken(context.Background(), "refresh-token", "", "client-id")
	require.NoError(t, err)
	require.Equal(t, "refresh-access", refreshed.AccessToken)
	require.Equal(t, "refresh-rotated", refreshed.RefreshToken)
	require.Equal(t, int64(7200), refreshed.ExpiresIn)
}

// TestGrokOAuthClientRefreshForbiddenClassifiesOnlyExplicitEntitlement
// returns two forbidden token bodies from loopback servers. It asserts an
// explicit access_denied marker selects entitlement denial while generic
// forbidden retains the ordinary refresh failure reason.
func TestGrokOAuthClientRefreshForbiddenClassifiesOnlyExplicitEntitlement(t *testing.T) {
	tests := []struct {
		name       string // refresh classification case name
		body       string // failed token response JSON
		wantReason string // expected refresh error reason
	}{
		{name: "explicit entitlement", body: `{"error":"access_denied"}`, wantReason: "GROK_OAUTH_ENTITLEMENT_DENIED"},
		{name: "generic forbidden", body: `{"error":"forbidden"}`, wantReason: "GROK_OAUTH_TOKEN_REFRESH_FAILED"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.WriteHeader(http.StatusForbidden)
				_, _ = w.Write([]byte(tt.body))
			}))
			defer server.Close()
			setGrokTestOverride(t, "AllowUnsafeURLOverrides", "true")
			setGrokTestOverride(t, "TokenURL", server.URL)

			client := NewOAuthClient()
			_, err := client.RefreshToken(context.Background(), "refresh-token", "", "client-id")
			require.Error(t, err)
			require.Contains(t, strings.ToUpper(err.Error()), tt.wantReason)
		})
	}
}

// TestGrokOAuthClientStatusErrorRedactsSensitiveResponseBody returns a failed
// grant body containing access, refresh, and verifier secrets. It asserts the
// error keeps HTTP status and redaction markers while omitting each supplied
// secret value.
func TestGrokOAuthClientStatusErrorRedactsSensitiveResponseBody(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte(`{"error":"invalid_grant","access_token":"access-secret","refresh_token":"refresh-secret","code_verifier":"verifier-secret"}`))
	}))
	defer server.Close()
	setGrokTestOverride(t, "AllowUnsafeURLOverrides", "true")
	setGrokTestOverride(t, "TokenURL", server.URL)

	client := NewOAuthClient()
	_, err := client.RefreshToken(context.Background(), "refresh-secret", "", "client-id")
	require.Error(t, err)

	errText := err.Error()
	require.Contains(t, errText, "status 400")
	require.Contains(t, errText, `\"refresh_token\":\"***\"`)
	require.NotContains(t, errText, "access-secret")
	require.NotContains(t, errText, "refresh-secret")
	require.NotContains(t, errText, "verifier-secret")
}

// TestGrokOAuthEntitlementDenialRequiresExplicitEvidence compares configured
// denial markers with generic forbidden and quota-exhaustion bodies. It
// asserts explicit markers qualify while generic text and exhausted-credit
// phrases suppress entitlement denial classification.
func TestGrokOAuthEntitlementDenialRequiresExplicitEvidence(t *testing.T) {
	t.Parallel()

	require.True(t, grokOAuthHasExplicitEntitlementDenial(`{"error":"access_denied"}`))
	require.True(t, grokOAuthHasExplicitEntitlementDenial(`{"code":"entitlement_denied"}`))
	require.True(t, grokOAuthHasExplicitEntitlementDenial(`{"message":"no active Grok subscription"}`))
	require.False(t, grokOAuthHasExplicitEntitlementDenial(`{"error":"forbidden","message":"request forbidden"}`))
	require.False(t, grokOAuthHasExplicitEntitlementDenial(`<html>403 Forbidden</html>`))
	require.False(t, grokOAuthHasExplicitEntitlementDenial(`{"error":"access_denied","message":"You have run out of credits"}`))
	require.False(t, grokOAuthHasExplicitEntitlementDenial(`{"code":"subscription_required","message":"included free usage exhausted"}`))
}

// TestNewGrokOAuthClient_UnvalidatedTokenURLFallsBackToDefault configures an
// untrusted token host with unsafe overrides disabled. It asserts client
// construction pins DefaultTokenURL rather than accepting that environment
// endpoint for later credential exchange.
func TestNewGrokOAuthClient_UnvalidatedTokenURLFallsBackToDefault(t *testing.T) {
	// Disable the unsafe switch before constructing this client so an untrusted
	// environment endpoint is rejected during selection, before a
	// credential-bearing request can be built.
	setGrokTestOverride(t, "AllowUnsafeURLOverrides", "")
	setGrokTestOverride(t, "TokenURL", "https://evil.example/oauth/token")

	client := NewOAuthClient()
	require.Equal(t, DefaultTokenURL, client.tokenURL)
}
