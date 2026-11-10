//go:build integration

package postgres

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/TokenFlux/TokenRouter/internal/identity"
	"github.com/TokenFlux/TokenRouter/internal/infra/crypto"
	"github.com/TokenFlux/TokenRouter/internal/testutil/postgrescontainer"
)

// mcp_tokens_integration_test.go verifies dedicated credential persistence and
// authorization against a migrated PostgreSQL database. The account reader
// reloads current status for each call, allowing this scenario to observe
// committed role, expiry, permission, ownership, and revocation changes.

// mcpAccountReader supplies current database account state to identity token authorization.
type mcpAccountReader struct {
	db *sql.DB // migrated fixture database
}

// GetByID reads the account fields consulted by MCP administrator admission.
func (r mcpAccountReader) GetByID(ctx context.Context, id int64) (*identity.User, error) {
	user := &identity.User{}
	err := r.db.QueryRowContext(ctx, `SELECT id,role,status FROM users WHERE id=$1 AND deleted_at IS NULL`, id).Scan(&user.ID, &user.Role, &user.Status)
	return user, err
}

// TestMCPTokenPersistenceAndLiveAuthorization exercises issuance, permission changes,
// ownership rejection, expiry, role demotion, and permanent revocation. It also
// verifies that persisted and listed records do not contain the complete credential.
// Response masking starts disabled and survives updates that omit the preference.
func TestMCPTokenPersistenceAndLiveAuthorization(t *testing.T) {
	db := postgrescontainer.New(t)
	ctx := t.Context()
	var userID int64
	require.NoError(t, db.QueryRowContext(ctx, `INSERT INTO users(email,password_hash,role,status) VALUES ('mcp-fixture@example.invalid','fixture','admin','active') RETURNING id`).Scan(&userID))
	repo := NewMCPTokenStore(db)
	cipher, err := crypto.NewAESEncryptor(bytes.Repeat([]byte{42}, 32))
	require.NoError(t, err)
	service := identity.NewMCPTokenService(repo, mcpAccountReader{db}, cipher)
	token, credential, err := service.Create(ctx, userID, identity.MCPTokenInput{Name: "Gateway assistant", Enabled: true, Permissions: []string{"providers.list"}})
	require.NoError(t, err)
	var storedHash string
	require.NoError(t, db.QueryRowContext(ctx, `SELECT token_hash FROM mcp_access_tokens WHERE id=$1`, token.ID).Scan(&storedHash))
	require.Equal(t, identity.MCPTokenDigest(credential), storedHash)
	require.NotEqual(t, credential, storedHash)
	var sealed string
	require.NoError(t, db.QueryRowContext(ctx, `SELECT token_ciphertext FROM mcp_access_tokens WHERE id=$1`, token.ID).Scan(&sealed))
	require.NotContains(t, sealed, credential)
	_, copied, err := service.Credential(ctx, userID, token.ID)
	require.NoError(t, err)
	require.Equal(t, credential, copied)
	_, _, err = repo.Credential(ctx, userID+1, token.ID)
	require.ErrorIs(t, err, identity.ErrMCPTokenNotFound)
	current, _, err := service.Authenticate(ctx, credential)
	require.NoError(t, err)
	require.True(t, current.Allows("providers.list"))
	require.False(t, current.SkipBrowserConfirmation)
	require.False(t, current.RedactToolResults)
	_, err = service.Update(ctx, userID+1, token.ID, identity.MCPTokenInput{Name: "wrong owner", Enabled: true})
	require.ErrorIs(t, err, identity.ErrMCPTokenNotFound)
	_, err = service.Update(ctx, userID, token.ID, identity.MCPTokenInput{
		Name: "Read groups", Enabled: true, Permissions: []string{"groups.list"},
		SkipBrowserConfirmation: true, RedactToolResults: new(true),
	})
	require.NoError(t, err)
	current, _, err = service.Authenticate(ctx, credential)
	require.NoError(t, err)
	require.False(t, current.Allows("providers.list"))
	require.True(t, current.Allows("groups.list"))
	require.True(t, current.SkipBrowserConfirmation)
	require.True(t, current.RedactToolResults)
	_, err = db.ExecContext(ctx, `UPDATE users SET role='user' WHERE id=$1`, userID)
	require.NoError(t, err)
	_, _, err = service.Authenticate(ctx, credential)
	require.ErrorIs(t, err, identity.ErrMCPTokenInvalid)
	_, err = db.ExecContext(ctx, `UPDATE users SET role='admin' WHERE id=$1`, userID)
	require.NoError(t, err)
	_, err = db.ExecContext(ctx, `UPDATE mcp_access_tokens SET expires_at=$2 WHERE id=$1`, token.ID, time.Now().Add(-time.Minute))
	require.NoError(t, err)
	_, _, err = service.Authenticate(ctx, credential)
	require.ErrorIs(t, err, identity.ErrMCPTokenInvalid)
	_, err = service.Update(ctx, userID, token.ID, identity.MCPTokenInput{Name: "No grants", Enabled: true})
	require.NoError(t, err)
	current, _, err = service.Authenticate(ctx, credential)
	require.NoError(t, err)
	require.Empty(t, current.Permissions)
	require.False(t, current.SkipBrowserConfirmation)
	require.True(t, current.RedactToolResults)
	current, err = service.Update(ctx, userID, token.ID, identity.MCPTokenInput{Name: "No grants", Enabled: true, RedactToolResults: new(false)})
	require.NoError(t, err)
	require.False(t, current.RedactToolResults)
	require.NoError(t, service.Revoke(ctx, userID, token.ID))
	_, _, err = repo.Credential(ctx, userID, token.ID)
	require.ErrorIs(t, err, identity.ErrMCPTokenNotFound)
	_, _, err = service.Authenticate(ctx, credential)
	require.ErrorIs(t, err, identity.ErrMCPTokenInvalid)
	_, err = service.Update(ctx, userID, token.ID, identity.MCPTokenInput{Name: "Restore", Enabled: true})
	require.ErrorIs(t, err, identity.ErrMCPTokenNotFound)
	items, err := service.List(ctx, userID)
	require.NoError(t, err)
	require.Len(t, items, 1)
	encoded, err := json.Marshal(items)
	require.NoError(t, err)
	require.NotContains(t, string(encoded), credential)
	require.NotContains(t, string(encoded), storedHash)
	require.NotContains(t, string(encoded), sealed)
	direct, _, err := service.Create(ctx, userID, identity.MCPTokenInput{
		Name: "Direct operations", Enabled: true, SkipBrowserConfirmation: true, RedactToolResults: new(true),
	})
	require.NoError(t, err)
	stored, err := repo.Get(ctx, direct.ID)
	require.NoError(t, err)
	require.True(t, stored.SkipBrowserConfirmation)
	require.True(t, stored.RedactToolResults)
	_, err = db.ExecContext(ctx, `DELETE FROM users WHERE id=$1`, userID)
	require.NoError(t, err)
	_, err = repo.Get(ctx, token.ID)
	require.ErrorIs(t, err, identity.ErrMCPTokenNotFound)
}

// TestMCPTokenUpdateRejectsStaleConfiguration creates a draft before another operation disables its token.
// The database compare prevents that draft from restoring enabled state or replacing the newer permission set.
func TestMCPTokenUpdateRejectsStaleConfiguration(t *testing.T) {
	db := postgrescontainer.New(t)
	ctx := t.Context()
	var userID int64
	require.NoError(t, db.QueryRowContext(ctx, `INSERT INTO users(email,password_hash,role,status) VALUES ('mcp-version@example.invalid','fixture','admin','active') RETURNING id`).Scan(&userID))
	repo := NewMCPTokenStore(db)
	cipher, err := crypto.NewAESEncryptor(bytes.Repeat([]byte{21}, 32))
	require.NoError(t, err)
	service := identity.NewMCPTokenService(repo, mcpAccountReader{db}, cipher)
	token, _, err := service.Create(ctx, userID, identity.MCPTokenInput{Name: "original", Enabled: true, Permissions: []string{"groups.list"}})
	require.NoError(t, err)
	observed := token.UpdatedAt
	_, err = db.ExecContext(ctx, `UPDATE mcp_access_tokens SET enabled=false,permissions='[]',updated_at=updated_at+INTERVAL '1 second' WHERE id=$1`, token.ID)
	require.NoError(t, err)
	_, err = service.Update(ctx, userID, token.ID, identity.MCPTokenInput{Name: "stale", Enabled: true, Permissions: []string{"groups.list"}, ExpectedUpdatedAt: &observed})
	require.ErrorContains(t, err, "MCP_TOKEN_CHANGED")
	current, err := repo.Get(ctx, token.ID)
	require.NoError(t, err)
	require.False(t, current.Enabled)
	require.Empty(t, current.Permissions)
	require.Equal(t, "original", current.Name)
	_, err = service.Update(ctx, userID, token.ID, identity.MCPTokenInput{Name: "fresh", Enabled: false, ExpectedUpdatedAt: &current.UpdatedAt})
	require.NoError(t, err)
}
