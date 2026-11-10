package postgres

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"net/url"
	"strconv"
	"time"

	"github.com/TokenFlux/TokenRouter/internal/identity"
	"github.com/TokenFlux/TokenRouter/internal/pkg/apperror"
)

// mcp_oauth.go persists OAuth client registrations, application permissions,
// browser requests, and sanitized credential records. Exchanges run on one SQL
// transaction carried in context. Grant and family advisory locks precede final
// credential reads, so refresh requests using either an old or current credential
// observe committed rotation and revocation decisions before issuing another token.
//
// Consumed credentials remain available for replay detection until the whole
// family expires. Revoking a family updates every credential in that family;
// changing an application grant is independently enforced by uncached reads.
// Cleanup takes the exchange locks before rechecking family expiration, so a
// replacement inserted by an admitted refresh retains its consumed predecessor.
// Consent serializes its owner/client/resource tuple before looking up a grant,
// giving concurrent first authorizations the same advisory-before-row lock order
// as subsequent authorizations and revocations.

const (
	// Grant reads retain the shared permission shape and calculate connection expiry
	// from unrevoked credentials instead of storing a second independently mutable deadline.
	oauthGrantColumns = `
        g.id,g.user_id,g.name,g.permissions,g.enabled,g.skip_browser_confirmation,g.redact_tool_results,
        g.expires_at,g.last_used_at,g.created_at,g.updated_at,g.revoked_at,g.client_id,g.client_name,g.resource,(SELECT MAX(s.expires_at)
        FROM mcp_oauth_sessions s
        WHERE s.grant_id=g.id AND s.kind IN ('refresh','access') AND s.active)
    `
)

// oauthPendingForm stores the received parameters beside their validated effective callback.
// An omitted redirect_uri must remain omitted for code redemption, while browser
// review and decision revalidation use the callback selected before consent began.
type oauthPendingForm struct {
	Parameters  url.Values `json:"parameters"`   // original validated authorization parameters
	RedirectURI string     `json:"redirect_uri"` // validated effective callback address
}

// oauthTransactionKey isolates the exchange transaction from unrelated database contexts.
type oauthTransactionKey struct{}

// oauthSQL supplies the SQL operations shared by a database pool and transaction.
type oauthSQL interface {
	// ExecContext executes a parameterized statement with the caller's cancellation scope.
	ExecContext(context.Context, string, ...any) (sql.Result, error)
	// QueryContext reads rows with the caller responsible for closing the result.
	QueryContext(context.Context, string, ...any) (*sql.Rows, error)
	// QueryRowContext reads one parameterized result with errors deferred until scanning.
	QueryRowContext(context.Context, string, ...any) *sql.Row
}

// MCPOAuthStore implements shared, uncached OAuth persistence on the primary database.
type MCPOAuthStore struct {
	db *sql.DB // shared primary database pool
}

// NewMCPOAuthStore binds the primary connection pool without starting background work.
func NewMCPOAuthStore(db *sql.DB) *MCPOAuthStore { return &MCPOAuthStore{db: db} }

// query selects the current exchange transaction when one belongs to this context.
func (s *MCPOAuthStore) query(ctx context.Context) oauthSQL {
	if tx, ok := ctx.Value(oauthTransactionKey{}).(*sql.Tx); ok {
		return tx
	}
	return s.db
}

// Transaction commits an entire consent or credential exchange after callback success.
// Nested calls reuse the same context transaction. The callback's SQL operations
// must retain that context so its mutations and advisory locks share one lifetime.
//
// An error or panic triggers deferred rollback, which also releases exchange locks.
// The callback prepares protocol results but leaves response publication to its
// caller after commit. A commit failure therefore cannot be mistaken for successful
// credential issuance, while replay handlers can deliberately return success to
// persist revocation before exposing their OAuth error.
func (s *MCPOAuthStore) Transaction(ctx context.Context, fn func(context.Context) error) error {
	if _, ok := ctx.Value(oauthTransactionKey{}).(*sql.Tx); ok {
		return fn(ctx)
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	// Rollback also releases advisory locks when validation, persistence, or the
	// response preparation fails; successful commit makes this deferred rollback harmless.
	defer func() { _ = tx.Rollback() }()
	if err := fn(context.WithValue(ctx, oauthTransactionKey{}, tx)); err != nil {
		return err
	}
	return tx.Commit()
}

// IsAdministrator reads current account eligibility on the exchange connection.
// Transaction contexts keep this query on the held connection, so token issuance
// remains possible when every connection in the pool already serves an exchange.
func (s *MCPOAuthStore) IsAdministrator(ctx context.Context, userID int64) (bool, error) {
	var allowed bool
	err := s.query(ctx).QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM users WHERE id=$1 AND role='admin' AND status='active' AND deleted_at IS NULL)`, userID).Scan(&allowed)
	return allowed, err
}

// PutClient stores validated metadata and the separate confidential-client verifier.
// Existing DCR registrations are immutable. CIMD entries can replace cached
// metadata only when the existing entry was also a discovered document.
func (s *MCPOAuthStore) PutClient(ctx context.Context, client *identity.MCPOAuthClient) error {
	raw, err := json.Marshal(client)
	if err != nil {
		return err
	}
	_, err = s.query(ctx).ExecContext(ctx, `
        INSERT INTO mcp_oauth_clients(id,metadata,secret_hash,expires_at)
        VALUES($1,$2,$3,$4)
        ON CONFLICT(id)
        DO UPDATE SET metadata=EXCLUDED.metadata,expires_at=EXCLUDED.expires_at
        WHERE mcp_oauth_clients.expires_at IS NOT NULL
    `, client.ID, string(raw), client.SecretHash, client.ExpiresAt)
	return err
}

// GetClient reads usable registration metadata and its separate authentication verifier.
func (s *MCPOAuthStore) GetClient(ctx context.Context, id string) (*identity.MCPOAuthClient, error) {
	var raw []byte
	client := new(identity.MCPOAuthClient)
	err := s.query(ctx).QueryRowContext(ctx, `SELECT metadata,secret_hash,expires_at FROM mcp_oauth_clients WHERE id=$1 AND (expires_at IS NULL OR expires_at>NOW())`, id).Scan(&raw, &client.SecretHash, &client.ExpiresAt)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, identity.ErrMCPTokenNotFound
	}
	if err != nil {
		return nil, err
	}
	if err := json.Unmarshal(raw, client); err != nil {
		return nil, err
	}
	return client, nil
}

// PutPending persists validated public request parameters before exposing the browser ticket.
// The form envelope keeps the effective callback beside the received parameters.
// This retains omission of redirect_uri for token redemption while providing the
// selected address for browser review and subsequent metadata revalidation.
func (s *MCPOAuthStore) PutPending(ctx context.Context, pending *identity.MCPOAuthPending) error {
	form, err := json.Marshal(oauthPendingForm{Parameters: pending.Form, RedirectURI: pending.RedirectURI})
	if err != nil {
		return err
	}
	client, err := json.Marshal(pending.Client)
	if err != nil {
		return err
	}
	_, err = s.query(ctx).ExecContext(ctx, `INSERT INTO mcp_oauth_pending(id,form,client,expires_at) VALUES($1,$2,$3,$4)`, pending.ID, string(form), string(client), pending.ExpiresAt)
	return err
}

// BindPending atomically binds a ticket to the first authenticated browser session.
// Refreshing in that session can read it again. Another account or browser
// session cannot claim the same request, including after the first user logs out.
func (s *MCPOAuthStore) BindPending(ctx context.Context, id string, userID int64, sessionID string) (*identity.MCPOAuthPending, error) {
	return s.pending(ctx, `
        UPDATE mcp_oauth_pending
        SET user_id=$2,session_id=$3
        WHERE id=$1 AND NOT consumed AND expires_at>NOW() AND (user_id IS NULL
        OR (user_id=$2 AND session_id=$3))
        RETURNING id,form,client,user_id,session_id,expires_at,consumed
    `, id, userID, sessionID)
}

// ConsumePending claims one decision inside its caller's surrounding SQL transaction.
// Failed response construction rolls back this transition together with any new
// grant or authorization code, allowing the same browser to retry safely.
func (s *MCPOAuthStore) ConsumePending(ctx context.Context, id string, userID int64, sessionID string) (*identity.MCPOAuthPending, error) {
	return s.pending(ctx, `
        UPDATE mcp_oauth_pending
        SET consumed=TRUE
        WHERE id=$1 AND user_id=$2 AND session_id=$3 AND NOT consumed AND expires_at>NOW()
        RETURNING id,form,client,user_id,session_id,expires_at,consumed
    `, id, userID, sessionID)
}

// pending decodes one conditional browser-ticket mutation into its domain representation.
// Expired, consumed, or foreign-session tickets return the same conflict. The
// persisted envelope restores both received parameters and the selected callback.
func (s *MCPOAuthStore) pending(ctx context.Context, query string, args ...any) (*identity.MCPOAuthPending, error) {
	p := new(identity.MCPOAuthPending)
	var form, client []byte
	err := s.query(ctx).QueryRowContext(ctx, query, args...).Scan(&p.ID, &form, &client, &p.UserID, &p.SessionID, &p.ExpiresAt, &p.Consumed)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, apperror.Conflict("MCP_OAUTH_REQUEST_EXPIRED", "Authorization request expired or belongs to another session")
	}
	if err != nil {
		return nil, err
	}
	var stored oauthPendingForm
	if err := json.Unmarshal(form, &stored); err != nil {
		return nil, err
	}
	p.Form = stored.Parameters
	p.RedirectURI = stored.RedirectURI
	if err := json.Unmarshal(client, &p.Client); err != nil {
		return nil, err
	}
	return p, nil
}

// SaveGrant replaces consented permissions and revokes the application's previous exchanges.
// Call inside the decision transaction so the browser ticket, application grant,
// and new authorization code commit together. The live owner/client/resource tuple
// selects the existing grant; revoked grants remain history and allow a new record.
//
// The owner/client/resource lock precedes lookup, including when no grant exists.
// A concurrent first consent therefore sees the committed grant before acquiring
// its application lock and changing its row. Previous credential families are
// invalidated before the new code is stored, so a concurrent refresh cannot survive
// committed reauthorization. An expired explicit grant deadline is cleared when
// the owner authorizes that application again.
func (s *MCPOAuthStore) SaveGrant(ctx context.Context, userID int64, client *identity.MCPOAuthClient, resource string, permissions []string) (*identity.MCPOAuthGrant, error) {
	raw, err := json.Marshal(permissions)
	if err != nil {
		return nil, err
	}
	// Serialize first consents before lookup: otherwise an upsert can discover a
	// concurrently created row and hold it while waiting for its grant advisory lock.
	if _, err := s.query(ctx).ExecContext(ctx, `SELECT pg_advisory_xact_lock(hashtextextended('mcp-oauth-consent:' || jsonb_build_array($1::bigint,$2::text,$3::text)::text,0))`, userID, client.ID, resource); err != nil {
		return nil, err
	}
	var id int64
	lookupErr := s.query(ctx).QueryRowContext(ctx, `SELECT id FROM mcp_oauth_grants WHERE user_id=$1 AND client_id=$2 AND resource=$3 AND revoked_at IS NULL`, userID, client.ID, resource).Scan(&id)
	if lookupErr != nil && !errors.Is(lookupErr, sql.ErrNoRows) {
		return nil, lookupErr
	}
	// Existing grants take the same lock as refresh before the upsert acquires
	// a row lock, preventing consent and credential issuance from reversing lock order.
	if lookupErr == nil {
		if err := s.lockGrant(ctx, id); err != nil {
			return nil, err
		}
	}
	err = s.query(ctx).QueryRowContext(ctx, `
        INSERT INTO mcp_oauth_grants(user_id,client_id,client_name,resource,name,permissions)
        VALUES($1,$2,$3::text,$4,$3::text,$5)
        ON CONFLICT(user_id,client_id,resource)
        WHERE revoked_at IS NULL
        DO UPDATE SET permissions=EXCLUDED.permissions,client_name=EXCLUDED.client_name,enabled=TRUE,expires_at=CASE WHEN mcp_oauth_grants.expires_at<=NOW() THEN NULL ELSE mcp_oauth_grants.expires_at END,updated_at=clock_timestamp()
        RETURNING id
    `, userID, client.ID, client.Name, resource, string(raw)).Scan(&id)
	if err != nil {
		return nil, err
	}
	if err := s.lockGrant(ctx, id); err != nil {
		return nil, err
	}
	// Consent creates a fresh credential family. Invalidate older exchanges before
	// issuing its code so a previously connected client cannot retain stale authorization.
	if _, err := s.query(ctx).ExecContext(ctx, `UPDATE mcp_oauth_sessions SET active=FALSE WHERE grant_id=$1`, id); err != nil {
		return nil, err
	}
	return s.GetGrant(ctx, id)
}

// GetGrant reads current permissions and the latest active connection expiry.
func (s *MCPOAuthStore) GetGrant(ctx context.Context, id int64) (*identity.MCPOAuthGrant, error) {
	return scanOAuthGrant(s.query(ctx).QueryRowContext(ctx, `SELECT `+oauthGrantColumns+` FROM mcp_oauth_grants g WHERE g.id=$1`, id))
}

// scanOAuthGrant decodes grant fields while assigning the OAuth source discriminator.
func scanOAuthGrant(row mcpTokenScanner) (*identity.MCPOAuthGrant, error) {
	g := new(identity.MCPOAuthGrant)
	var permissions []byte
	err := row.Scan(
		&g.ID, &g.UserID, &g.Name, &permissions, &g.Enabled, &g.SkipBrowserConfirmation,
		&g.RedactToolResults, &g.ExpiresAt, &g.LastUsedAt, &g.CreatedAt, &g.UpdatedAt,
		&g.RevokedAt, &g.ClientID, &g.ClientName, &g.Resource, &g.ConnectionExpiresAt,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, identity.ErrMCPTokenNotFound
	}
	if err != nil {
		return nil, err
	}
	if err := json.Unmarshal(permissions, &g.Permissions); err != nil {
		return nil, err
	}
	g.Source = "oauth"
	g.Prefix = "oauth:" + strconv.FormatInt(g.ID, 10)
	return g, nil
}

// ListGrants returns application authorization history for the specified administrator.
func (s *MCPOAuthStore) ListGrants(ctx context.Context, userID int64) ([]identity.MCPOAuthGrant, error) {
	rows, err := s.query(ctx).QueryContext(ctx, `SELECT `+oauthGrantColumns+` FROM mcp_oauth_grants g WHERE g.user_id=$1 ORDER BY g.id DESC`, userID)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	items := []identity.MCPOAuthGrant{}
	for rows.Next() {
		g, err := scanOAuthGrant(rows)
		if err != nil {
			return nil, err
		}
		items = append(items, *g)
	}
	return items, rows.Err()
}

// UpdateGrant replaces an owned application's settings using optimistic concurrency.
// The caller validates operations and browser identity. A stale configuration
// or revoked grant returns conflict and cannot be silently re-enabled. Clients
// can omit RedactToolResults to keep the stored preference.
func (s *MCPOAuthStore) UpdateGrant(ctx context.Context, userID, id int64, input identity.MCPTokenInput) (*identity.MCPOAuthGrant, error) {
	raw, err := json.Marshal(input.Permissions)
	if err != nil {
		return nil, err
	}
	result, err := s.query(ctx).ExecContext(ctx, `
        UPDATE mcp_oauth_grants
        SET name=$3,permissions=$4,enabled=$5,skip_browser_confirmation=$6,expires_at=$7,
            redact_tool_results=COALESCE($8,redact_tool_results),updated_at=clock_timestamp()
        WHERE id=$1 AND user_id=$2 AND revoked_at IS NULL AND updated_at=$9
    `, id, userID, input.Name, string(raw), input.Enabled, input.SkipBrowserConfirmation,
		input.ExpiresAt, input.RedactToolResults, input.ExpectedUpdatedAt)
	if err != nil {
		return nil, err
	}
	n, err := result.RowsAffected()
	if err != nil {
		return nil, err
	}
	if n != 1 {
		return nil, apperror.Conflict("MCP_TOKEN_CHANGED", "Authorization changed; reload before saving")
	}
	return s.GetGrant(ctx, id)
}

// RevokeGrant permanently disables an owned application and invalidates its credentials.
// Both writes share a transaction. The owner predicate prevents another
// administrator from revoking a grant even when its identifier is known.
//
// The application lock precedes row mutation and is shared with token exchanges.
// A refresh that finishes first is included in the revocation update; one that
// starts later observes the disabled grant. Repeated revocation retains the first
// revocation timestamp, and any storage failure rolls back the complete mutation.
func (s *MCPOAuthStore) RevokeGrant(ctx context.Context, userID, id int64) error {
	return s.Transaction(ctx, func(ctx context.Context) error {
		if err := s.lockGrant(ctx, id); err != nil {
			return err
		}
		result, err := s.query(ctx).ExecContext(ctx, `UPDATE mcp_oauth_grants SET enabled=FALSE,revoked_at=COALESCE(revoked_at,NOW()),updated_at=clock_timestamp() WHERE id=$1 AND user_id=$2`, id, userID)
		if err != nil {
			return err
		}
		n, err := result.RowsAffected()
		if err != nil {
			return err
		}
		if n != 1 {
			return identity.ErrMCPTokenNotFound
		}
		// Persist grant revocation before invalidating credentials in this same transaction;
		// either both effects become visible or the caller receives a failed mutation.
		_, err = s.query(ctx).ExecContext(ctx, `UPDATE mcp_oauth_sessions SET active=FALSE WHERE grant_id=$1`, id)
		return err
	})
}

// PutSession stores sanitized protocol state under an irreversible credential signature.
func (s *MCPOAuthStore) PutSession(ctx context.Context, session *identity.MCPOAuthSession) error {
	_, err := s.query(ctx).ExecContext(ctx, `INSERT INTO mcp_oauth_sessions(kind,signature,family_id,grant_id,payload,expires_at,active) VALUES($1,$2,$3,$4,$5,$6,$7)`, session.Kind, session.Signature, session.FamilyID, session.GrantID, string(session.Payload), session.ExpiresAt, session.Active)
	return err
}

// GetSession serializes application and family exchanges before returning credential state.
// The preliminary lookup obtains lock identities and carries no authorization
// decision. Outside an exchange transaction, reads remain ordinary uncached lookups.
//
// Within an exchange, the application advisory lock precedes the family lock for
// both old and current refresh signatures. The final read observes earlier
// committed rotation or revocation before Fosite can consume the credential.
// Application-wide revocation uses the same ordering, preventing a refresh from
// inserting a new credential after the revocation update has scanned its family.
func (s *MCPOAuthStore) GetSession(ctx context.Context, kind, signature string) (*identity.MCPOAuthSession, error) {
	q := s.query(ctx)
	if _, ok := ctx.Value(oauthTransactionKey{}).(*sql.Tx); ok {
		var family string
		var grantID int64
		err := q.QueryRowContext(ctx, `SELECT family_id,grant_id FROM mcp_oauth_sessions WHERE kind=$1 AND signature=$2`, kind, signature).Scan(&family, &grantID)
		if errors.Is(err, sql.ErrNoRows) {
			return nil, identity.ErrMCPTokenNotFound
		}
		if err != nil {
			return nil, err
		}
		if err := s.lockGrant(ctx, grantID); err != nil {
			return nil, err
		}
		// A family lock coordinates exchanges across rotating signatures. Re-read below
		// observes the previous lock holder's committed invalidation before trusting this credential.
		if err := s.lockFamily(ctx, family); err != nil {
			return nil, err
		}
	}
	v := &identity.MCPOAuthSession{Kind: kind, Signature: signature}
	err := q.QueryRowContext(ctx, `SELECT family_id,grant_id,payload,expires_at,active FROM mcp_oauth_sessions WHERE kind=$1 AND signature=$2`, kind, signature).Scan(&v.FamilyID, &v.GrantID, &v.Payload, &v.ExpiresAt, &v.Active)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, identity.ErrMCPTokenNotFound
	}
	return v, err
}

// InvalidateSession retains consumed credentials so later reuse remains detectable.
func (s *MCPOAuthStore) InvalidateSession(ctx context.Context, kind, signature string) error {
	_, err := s.query(ctx).ExecContext(ctx, `UPDATE mcp_oauth_sessions SET active=FALSE WHERE kind=$1 AND signature=$2`, kind, signature)
	return err
}

// RevokeFamily invalidates all access, refresh, and code records in one family.
func (s *MCPOAuthStore) RevokeFamily(ctx context.Context, family string) error {
	_, err := s.query(ctx).ExecContext(ctx, `UPDATE mcp_oauth_sessions SET active=FALSE WHERE family_id=$1`, family)
	return err
}

// Cleanup removes expired requests and cached client documents in bounded batches.
// Call from maintenance outside an exchange transaction.
// Credential deletion locks each candidate family before rechecking its latest
// expiration, including consumed records retained for replay detection.
//
// A consumed refresh token remains stored while any family member is unexpired.
// This allows an old credential replay to revoke a connection that has refreshed
// several times since that credential was consumed. Each family uses a separate
// transaction so grant locks are released before the next candidate is processed.
// Durable client registrations and application grant history are retained, and a
// failed cleanup statement stops the pass.
func (s *MCPOAuthStore) Cleanup(ctx context.Context) error {
	if _, err := s.db.ExecContext(ctx, `DELETE FROM mcp_oauth_pending WHERE id IN (SELECT id FROM mcp_oauth_pending WHERE expires_at<NOW() LIMIT 1000)`); err != nil {
		return err
	}
	if err := s.cleanupSessions(ctx); err != nil {
		return err
	}
	_, err := s.db.ExecContext(ctx, `DELETE FROM mcp_oauth_clients WHERE id IN (
            SELECT id FROM mcp_oauth_clients
            WHERE expires_at<NOW()-INTERVAL '1 day' LIMIT 1000
	)`)
	return err
}

// cleanupSessions removes bounded candidates after excluding concurrent renewal.
// Candidate selection only identifies families to inspect; it cannot authorize
// deletion because an admitted refresh may be between consumption and insertion.
// Each candidate takes the grant lock followed by the family lock, matching
// credential exchanges, then executes deletion in a fresh statement snapshot.
// A replacement credential committed while cleanup waited therefore prevents
// deletion of every older record needed to detect replay in that family.
func (s *MCPOAuthStore) cleanupSessions(ctx context.Context) error {
	rows, err := s.db.QueryContext(ctx, `
        SELECT family_id,grant_id FROM mcp_oauth_sessions
        GROUP BY family_id,grant_id HAVING MAX(expires_at)<NOW() LIMIT 100
    `)
	if err != nil {
		return err
	}
	// Materialize candidates and close their cursor before transactions acquire
	// pooled connections, so an idle candidate scan cannot exhaust a small pool.
	defer func() { _ = rows.Close() }()
	// candidateFamily carries the identities required to lock one cleanup candidate.
	type candidateFamily struct {
		family  string // Candidate credential family identifier.
		grantID int64  // Owning application grant identifier.
	}
	var candidates []candidateFamily
	for rows.Next() {
		var candidate candidateFamily
		if err := rows.Scan(&candidate.family, &candidate.grantID); err != nil {
			return err
		}
		candidates = append(candidates, candidate)
	}
	if err := rows.Err(); err != nil {
		return err
	}
	if err := rows.Close(); err != nil {
		return err
	}
	for _, candidate := range candidates {
		// Separate transactions release each grant before taking another grant's
		// lock, while deletion stays atomic with the selected family's expiry recheck.
		if err := s.Transaction(ctx, func(ctx context.Context) error {
			if err := s.lockGrant(ctx, candidate.grantID); err != nil {
				return err
			}
			if err := s.lockFamily(ctx, candidate.family); err != nil {
				return err
			}
			_, err := s.query(ctx).ExecContext(ctx, `
                DELETE FROM mcp_oauth_sessions
                WHERE family_id=$1 AND NOT EXISTS (
                    SELECT 1 FROM mcp_oauth_sessions
                    WHERE family_id=$1 AND expires_at>=NOW()
                )
            `, candidate.family)
			return err
		}); err != nil {
			return err
		}
	}
	return nil
}

// TouchGrant records admission without invalidating pending browser confirmations.
func (s *MCPOAuthStore) TouchGrant(ctx context.Context, id int64) error {
	_, err := s.query(ctx).ExecContext(ctx, `UPDATE mcp_oauth_grants SET last_used_at=$2 WHERE id=$1 AND (last_used_at IS NULL OR last_used_at<$2-INTERVAL '1 minute')`, id, time.Now().UTC())
	return err
}

// lockGrant serializes application-wide revocation with its credential exchanges.
// Call inside a transaction before taking any family lock. This shared ordering
// prevents a refresh from inserting credentials after revocation scans the family.
func (s *MCPOAuthStore) lockGrant(ctx context.Context, id int64) error {
	_, err := s.query(ctx).ExecContext(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1,0))`, "mcp-oauth-grant:"+strconv.FormatInt(id, 10))
	return err
}

// lockFamily serializes rotation, replay handling, and expiration cleanup for a
// credential family. Call inside a transaction after taking its grant lock so
// all exchange and cleanup paths acquire shared locks in the same order.
func (s *MCPOAuthStore) lockFamily(ctx context.Context, family string) error {
	_, err := s.query(ctx).ExecContext(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1,0))`, "mcp-oauth:"+family)
	return err
}
