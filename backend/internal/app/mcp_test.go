package app

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"

	"github.com/TokenFlux/TokenRouter/internal/config"
	identityprovider "github.com/TokenFlux/TokenRouter/internal/identity/provider"
	"github.com/TokenFlux/TokenRouter/internal/infra/crypto"
	"github.com/TokenFlux/TokenRouter/internal/settings"
)

// mcp_test.go verifies browser-origin admission and durable MCP artifact encryption.
// The tests check that backend restarts can decrypt surviving files and configured
// TOTP encryption keeps its existing key. Browser admission and OAuth discovery
// use the same configured frontend origin.

// TestMCPArtifactCipherUsesDurableOAuthKey recreates encryption after a backend restart.
// A different temporary TOTP key must not prevent reading files encrypted with
// the persisted OAuth key, while a configured TOTP cipher retains its identity.
func TestMCPArtifactCipherUsesDurableOAuthKey(t *testing.T) {
	first, err := crypto.NewAESEncryptor(bytes.Repeat([]byte{1}, 32))
	require.NoError(t, err)
	second, err := crypto.NewAESEncryptor(bytes.Repeat([]byte{2}, 32))
	require.NoError(t, err)
	cfg := &config.Config{MCPOAuthSigningSecret: strings.Repeat("ab", 32)}
	a, err := mcpArtifactCipher(first, cfg)
	require.NoError(t, err)
	b, err := mcpArtifactCipher(second, cfg)
	require.NoError(t, err)
	ciphertext, err := a.Encrypt("protected fixture")
	require.NoError(t, err)
	plaintext, err := b.Decrypt(ciphertext)
	require.NoError(t, err)
	require.Equal(t, "protected fixture", plaintext)
	_, err = first.Decrypt(ciphertext)
	require.Error(t, err)
	cfg.Security.SecretEncryptionKeyConfigured = true
	original, err := mcpArtifactCipher(first, cfg)
	require.NoError(t, err)
	require.Same(t, first, original)
	cfg.Security.SecretEncryptionKeyConfigured = false
	cfg.MCPOAuthSigningSecret = ""
	_, err = mcpArtifactCipher(first, cfg)
	require.Error(t, err)
}

// TestMCPBrowserOriginUsesFrontendURL assembles the transport with a deployment site address.
// The configured browser must reach credential authentication, and the challenge
// must advertise discovery at that same public origin.
func TestMCPBrowserOriginUsesFrontendURL(t *testing.T) {
	repo := &mcpSettingsFixture{values: map[string]string{}}
	store := settings.New(repo)
	cfg := &config.Config{MCPOAuthSigningSecret: strings.Repeat("ab", 32)}
	cfg.Site.FrontendURL = "https://frontend.example"
	oauth := identityprovider.NewMCPOAuthServer(nil, nil, &mcpOAuthFrontend{store: store, frontendURL: cfg.Site.FrontendURL}, bytes.Repeat([]byte{4}, 32))
	handler, err := provideMCPHTTP(oauth, nil, nil, nil, nil, nil, nil, store, cfg)
	require.NoError(t, err)
	router := gin.New()
	require.NoError(t, handler.Mount(router, func(c *gin.Context) { c.Next() }, func(c *gin.Context) { c.Next() }))
	request := httptest.NewRequest(http.MethodPost, "/mcp", nil)
	request.Header.Set("Origin", cfg.Site.FrontendURL)
	response := httptest.NewRecorder()
	router.ServeHTTP(response, request)
	require.Equal(t, http.StatusUnauthorized, response.Code)
	require.Contains(t, response.Header().Get("WWW-Authenticate"), "https://frontend.example/.well-known/oauth-protected-resource/mcp")
}
