package postgres

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"

	"github.com/TokenFlux/TokenRouter/internal/identity"
	"github.com/TokenFlux/TokenRouter/internal/pkg/apperror"
)

// mcp_tokens.go persists MCP credential digests, ciphertext, and operation grants.
// Owner checks are part of each mutation predicate, and revoked rows remain
// readable as management history. Authorization reads use the primary SQL
// connection directly so subsequent calls observe committed permission and
// redaction changes.

const (
	// mcpTokenColumns fixes the scan order used by creation, reads, and updates;
	// digests and ciphertext are absent from management result records.
	mcpTokenColumns = `id,user_id,name,token_prefix,permissions,enabled,skip_browser_confirmation,redact_tool_results,expires_at,last_used_at,created_at,updated_at,revoked_at`
)

// MCPTokenStore implements uncached identity credential persistence using PostgreSQL.
type MCPTokenStore struct {
	db *sql.DB // shared primary database pool
}

// mcpTokenScanner accepts both row and result-set scanning without exposing SQL outside storage.
type mcpTokenScanner interface {
	// Scan decodes the selected columns into their ordered destination values.
	Scan(...any) error
}

// NewMCPTokenStore binds the shared SQL pool used for token authorization.
func NewMCPTokenStore(db *sql.DB) *MCPTokenStore {
	return &MCPTokenStore{db: db}
}

// Create persists a digest and ciphertext, then fills generated token metadata.
// A successful insert returns its row through the same statement. The digest
// and ciphertext are the only stored forms of the credential and stay out of
// the returned metadata.
func (s *MCPTokenStore) Create(ctx context.Context, token *identity.MCPToken, digest, sealed string) error {
	permissions, err := json.Marshal(token.Permissions)
	if err != nil {
		return err
	}
	stored, err := scanMCPToken(s.db.QueryRowContext(ctx, `
        INSERT INTO mcp_access_tokens (
            user_id,name,token_hash,token_prefix,permissions,enabled,
            expires_at,skip_browser_confirmation,redact_tool_results,token_ciphertext
        ) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10) RETURNING `+mcpTokenColumns,
		token.UserID, token.Name, digest, token.Prefix, string(permissions),
		token.Enabled, token.ExpiresAt, token.SkipBrowserConfirmation, token.RedactToolResults, sealed,
	))
	if err == nil {
		*token = *stored
	}
	return err
}

// Credential reads the encrypted secret for one owned, unrevoked credential.
// The owner predicate applies before disclosure, and ordinary metadata queries
// keep this ciphertext column outside their selected and returned fields.
func (s *MCPTokenStore) Credential(ctx context.Context, userID, id int64) (string, string, error) {
	var sealed, digest string
	err := s.db.QueryRowContext(ctx, `SELECT token_ciphertext,token_hash FROM mcp_access_tokens WHERE id=$1 AND user_id=$2 AND revoked_at IS NULL`, id, userID).Scan(&sealed, &digest)
	if errors.Is(err, sql.ErrNoRows) {
		return "", "", identity.ErrMCPTokenNotFound
	}
	return sealed, digest, err
}

// List reads an administrator's credential history in newest-first order.
// Returned metadata excludes hashes and ciphertext; rows and iterator failures are checked
// before the complete list is made available to the caller.
func (s *MCPTokenStore) List(ctx context.Context, userID int64) ([]identity.MCPToken, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT `+mcpTokenColumns+` FROM mcp_access_tokens WHERE user_id=$1 ORDER BY id DESC`, userID)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	items := []identity.MCPToken{}
	for rows.Next() {
		value, err := scanMCPToken(rows)
		if err != nil {
			return nil, err
		}
		items = append(items, *value)
	}
	return items, rows.Err()
}

// Get reads persisted token metadata by its immutable numeric identifier.
func (s *MCPTokenStore) Get(ctx context.Context, id int64) (*identity.MCPToken, error) {
	return scanMCPToken(s.db.QueryRowContext(ctx, `SELECT `+mcpTokenColumns+` FROM mcp_access_tokens WHERE id=$1`, id))
}

// FindHash resolves a presented credential digest to its current stored grants.
func (s *MCPTokenStore) FindHash(ctx context.Context, digest string) (*identity.MCPToken, error) {
	return scanMCPToken(s.db.QueryRowContext(ctx, `SELECT `+mcpTokenColumns+` FROM mcp_access_tokens WHERE token_hash=$1`, digest))
}

// Update saves the editable settings of one owned, unrevoked credential.
// The conditional write prevents a concurrent revocation from being undone;
// an optional observed timestamp also prevents stale forms from replacing a newer
// configuration. Conditional misses return conflict so callers can reload their
// draft. Clients can omit RedactToolResults to keep the stored preference.
func (s *MCPTokenStore) Update(ctx context.Context, userID, id int64, input identity.MCPTokenInput) (*identity.MCPToken, error) {
	permissions, err := json.Marshal(input.Permissions)
	if err != nil {
		return nil, err
	}
	stored, err := scanMCPToken(s.db.QueryRowContext(ctx, `
        UPDATE mcp_access_tokens SET name=$3,permissions=$4,enabled=$5,expires_at=$6,
            skip_browser_confirmation=$7,redact_tool_results=COALESCE($8,redact_tool_results),updated_at=NOW()
        WHERE id=$1 AND user_id=$2 AND revoked_at IS NULL
            AND ($9::timestamptz IS NULL OR updated_at=$9)
        RETURNING `+mcpTokenColumns,
		id, userID, input.Name, string(permissions), input.Enabled, input.ExpiresAt,
		input.SkipBrowserConfirmation, input.RedactToolResults, input.ExpectedUpdatedAt,
	))
	if errors.Is(err, identity.ErrMCPTokenNotFound) && input.ExpectedUpdatedAt != nil {
		return nil, apperror.Conflict("MCP_TOKEN_CHANGED", "Token changed or is unavailable; reload before saving")
	}
	return stored, err
}

// Revoke disables one owned token and retains its first revocation timestamp.
// Repeated revocation is harmless, but a missing or differently owned identifier
// returns not-found without changing any credential.
func (s *MCPTokenStore) Revoke(ctx context.Context, userID, id int64) error {
	result, err := s.db.ExecContext(ctx, `UPDATE mcp_access_tokens SET enabled=FALSE,revoked_at=COALESCE(revoked_at,NOW()),updated_at=NOW() WHERE id=$1 AND user_id=$2`, id, userID)
	if err != nil {
		return err
	}
	n, err := result.RowsAffected()
	if err == nil && n == 0 {
		return identity.ErrMCPTokenNotFound
	}
	return err
}

// Touch updates last-use observations at most once per minute per token.
// The time predicate reduces write amplification for discovery requests and
// does not alter the configuration timestamp used by browser confirmation.
func (s *MCPTokenStore) Touch(ctx context.Context, id int64) error {
	_, err := s.db.ExecContext(ctx, `UPDATE mcp_access_tokens SET last_used_at=NOW() WHERE id=$1 AND (last_used_at IS NULL OR last_used_at<NOW()-INTERVAL '1 minute')`, id)
	return err
}

// scanMCPToken decodes a complete metadata row and its JSON grant set.
// Invalid JSON is a storage error; missing rows map to the identity not-found
// error so callers can enforce ownership without exposing SQL details.
func scanMCPToken(row mcpTokenScanner) (*identity.MCPToken, error) {
	var token identity.MCPToken
	var permissions []byte
	err := row.Scan(
		&token.ID, &token.UserID, &token.Name, &token.Prefix, &permissions,
		&token.Enabled, &token.SkipBrowserConfirmation, &token.RedactToolResults,
		&token.ExpiresAt, &token.LastUsedAt, &token.CreatedAt, &token.UpdatedAt, &token.RevokedAt,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, identity.ErrMCPTokenNotFound
	}
	if err != nil {
		return nil, err
	}
	if err := json.Unmarshal(permissions, &token.Permissions); err != nil {
		return nil, err
	}
	return &token, nil
}
