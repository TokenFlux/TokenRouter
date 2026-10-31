package postgres

import (
	"context"
	"database/sql"

	"github.com/lib/pq"

	"github.com/TokenFlux/TokenRouter/internal/identity"
)

// user_lookup.go reads exact account profiles through a small SQL projection.
// ID, username, and case-insensitive email equality are the only selectors; this
// query never calls the administrator search union that also examines notes and
// API keys. Deleted users remain excluded even when another caller carries a
// general soft-delete bypass in its context.
//
// Avatar URLs join directly from the account's separate avatar row, avoiding a
// per-user hydration loop for one hundred matches. The projection contains no
// password hashes, notes, tokens, or key records. A fixed result limit and ID
// ordering bound duplicate-username matches while keeping ID-based batches stable.
// Context-selected SQL execution preserves the owning repository's transaction rules.

// LookupUsersExact reads a normalized criterion and returns bounded nondeleted profile matches.
// Unsupported selector combinations fail before SQL execution. Email uses LOWER
// equality instead of pattern matching, so percent signs and underscores remain literal.
func (r *UserStore) LookupUsersExact(ctx context.Context, input identity.UserLookup) ([]identity.UserLookupRecord, error) {
	var condition string
	var argument any
	switch {
	case len(input.IDs) > 0 && input.Username == "" && input.Email == "":
		condition = "u.id = ANY($1)"
		argument = pq.Array(input.IDs)
	case len(input.IDs) == 0 && input.Username != "" && input.Email == "":
		condition = "u.username = $1"
		argument = input.Username
	case len(input.IDs) == 0 && input.Username == "" && input.Email != "":
		condition = "LOWER(u.email) = LOWER($1)"
		argument = input.Email
	default:
		return nil, identity.ErrUserLookupInvalid
	}
	executor, err := r.IdentityUserProfileIdentitySQL(ctx)
	if err != nil {
		return nil, err
	}
	rows, err := executor.QueryContext(ctx, `SELECT u.id,COALESCE(u.email,''),COALESCE(u.username,''),COALESCE(u.role,''),COALESCE(u.status,''),u.created_at,u.last_active_at,
COALESCE(u.signup_source,''),COALESCE(a.url,'') FROM users u
LEFT JOIN user_avatars a ON a.user_id=u.id
WHERE u.deleted_at IS NULL AND `+condition+` ORDER BY u.id LIMIT 100`, argument)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	result := make([]identity.UserLookupRecord, 0)
	for rows.Next() {
		var record identity.UserLookupRecord
		var active sql.NullTime
		if err := rows.Scan(&record.ID, &record.Email, &record.Username, &record.Role, &record.Status, &record.CreatedAt, &active, &record.SignupSource, &record.AvatarURL); err != nil {
			return nil, err
		}
		if active.Valid {
			record.LastActiveAt = &active.Time
		}
		result = append(result, record)
	}
	return result, rows.Err()
}
