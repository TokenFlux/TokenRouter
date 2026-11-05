package bootstrap

import (
	"database/sql"
	"time"

	dbent "github.com/TokenFlux/TokenRouter/ent"
	"github.com/TokenFlux/TokenRouter/internal/config"
	"github.com/TokenFlux/TokenRouter/internal/identity"
	identitypostgres "github.com/TokenFlux/TokenRouter/internal/identity/postgres"
)

// jwt_identity.go constructs database-backed user lookup and JWT signing for
// maintenance commands.
// Maintenance token generation needs user lookup and access-token signing from
// the bootstrapped database. The command owns that database pool, while
// the constructed identity helpers use process token settings and the current
// clock for signing.

// JWTIdentity provides user lookup and access-token signing for maintenance commands.
type JWTIdentity struct {
	// Users supplies the user loaded before Tokens signs it. Tokens is configured
	// with that same store; the command keeps the database open while using these
	// helpers.
	Users  *identitypostgres.UserStore // maintenance user lookup
	Tokens *identity.SessionService    // maintenance access-token signer
}

// NewJWTIdentity constructs user lookup and token signing over a bootstrapped
// database using cfg's token lifetimes and secret. Keep the supplied database
// open while using the helpers, then close the owning Ent client when command
// work finishes.
func NewJWTIdentity(client *dbent.Client, db *sql.DB, cfg *config.Config) JWTIdentity {
	users := identitypostgres.NewUserStore(client, db)
	options := identity.SessionOptions{Now: time.Now, Secret: cfg.Auth.JWT.Secret, AccessTokenExpireMinutes: cfg.Auth.JWT.AccessTokenTTLMinutes, RefreshTokenExpireDays: cfg.Auth.JWT.RefreshTokenTTLDays}
	return JWTIdentity{Users: users, Tokens: identity.NewSessionService(options, users, nil, nil, nil)}
}
