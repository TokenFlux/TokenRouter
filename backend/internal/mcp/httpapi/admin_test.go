package httpapi

import (
	"bytes"
	"encoding/json"
	"io"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/TokenFlux/TokenRouter/internal/identity"
	"github.com/TokenFlux/TokenRouter/internal/infra/crypto"
)

// admin_test.go exercises browser-only recovery of encrypted MCP credentials.
// The mounted fixture uses production token authorization and encryption with
// synthetic browser identities, protecting owner checks and preventing complete
// secrets from appearing in MCP tool results or failed recovery responses.

// TestCredentialCopyRequiresOwnerBrowser verifies full-secret copying and token lifecycle restrictions.
// Other administrators, MCP bearers, revoked tokens, and demoted owners cannot retrieve plaintext.
func TestCredentialCopyRequiresOwnerBrowser(t *testing.T) {
	f := newMCPFixture(t)
	path := "/api/v1/admin/mcp/tokens/1/credential"
	response := f.browser(t, "GET", path, "browser")
	require.Equal(t, 200, response.StatusCode)
	require.Equal(t, "no-store", response.Header.Get("Cache-Control"))
	var result struct {
		Data struct {
			Credential string // Complete recovered fixture secret.
		} // Standard successful response data.
	}
	require.NoError(t, json.NewDecoder(response.Body).Decode(&result))
	require.Equal(t, f.credential, result.Data.Credential)
	require.Equal(t, 404, f.browser(t, "GET", path, "other-browser").StatusCode)
	require.Equal(t, 401, f.browser(t, "GET", path, f.credential).StatusCode)

	f.repo.mu.Lock()
	f.repo.token.Permissions = append(f.repo.token.Permissions, "mcp.tokens.credential")
	f.repo.token.SkipBrowserConfirmation = true
	f.repo.mu.Unlock()
	tool, data := f.invoke(t, "read_operation", map[string]any{"operation_id": "mcp.tokens.credential", "path": map[string]string{"id": "1"}})
	require.False(t, tool.IsError)
	require.Equal(t, "interaction_required", data["status"])
	encoded, err := json.Marshal(tool)
	require.NoError(t, err)
	require.NotContains(t, string(encoded), f.credential)

	f.repo.mu.Lock()
	expired := time.Now().Add(-time.Hour)
	f.repo.token.Enabled = false
	f.repo.token.ExpiresAt = &expired
	f.repo.mu.Unlock()
	require.Equal(t, 200, f.browser(t, "GET", path, "browser").StatusCode)
	f.accounts.role.Store(identity.RoleUser)
	require.Equal(t, 401, f.browser(t, "GET", path, "browser").StatusCode)
	f.accounts.role.Store(identity.RoleAdmin)
	f.repo.mu.Lock()
	f.repo.token.RevokedAt = &expired
	f.repo.mu.Unlock()
	require.Equal(t, 404, f.browser(t, "GET", path, "browser").StatusCode)
}

// TestCredentialCopyRejectsCorruption verifies that authenticated encryption and digest matching
// both protect recovery. Invalid or mismatched ciphertext produces a fixed error and no plaintext.
func TestCredentialCopyRejectsCorruption(t *testing.T) {
	f := newMCPFixture(t)
	cipher, err := crypto.NewAESEncryptor(bytes.Repeat([]byte{1}, 32))
	require.NoError(t, err)
	mismatched, err := cipher.Encrypt("another-token-secret")
	require.NoError(t, err)
	for _, sealed := range []string{"damaged-ciphertext", mismatched} {
		f.repo.mu.Lock()
		f.repo.sealed = sealed
		f.repo.mu.Unlock()
		response := f.browser(t, "GET", "/api/v1/admin/mcp/tokens/1/credential", "browser")
		require.Equal(t, 500, response.StatusCode)
		body, err := io.ReadAll(response.Body)
		require.NoError(t, err)
		require.Contains(t, string(body), "MCP_CREDENTIAL_UNAVAILABLE")
		require.NotContains(t, string(body), f.credential)
		require.NotContains(t, string(body), "another-token-secret")
	}
}

// TestCredentialIssuanceRequiresDurableEncryption prevents temporary startup encryption from
// producing persistent credentials. Existing digest authentication remains available when copy encryption is unavailable.
func TestCredentialIssuanceRequiresDurableEncryption(t *testing.T) {
	f := newMCPFixture(t)
	service := identity.NewMCPTokenService(f.repo, f.accounts, nil)
	_, secret, err := service.Create(t.Context(), 1, identity.MCPTokenInput{Name: "Unavailable", Enabled: true})
	require.ErrorContains(t, err, "security.secret_encryption_key")
	require.Empty(t, secret)
	_, secret, err = service.Credential(t.Context(), 1, 1)
	require.ErrorContains(t, err, "security.secret_encryption_key")
	require.Empty(t, secret)
	_, _, err = service.Authenticate(t.Context(), f.credential)
	require.NoError(t, err)
}
