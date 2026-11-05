package postgres

import (
	"context"
	"database/sql"

	dbent "github.com/TokenFlux/TokenRouter/ent"
	identitycore "github.com/TokenFlux/TokenRouter/internal/identity"
	postgresinfra "github.com/TokenFlux/TokenRouter/internal/infra/postgres"
)

// user_entity.go converts stored account entities into identity values and
// defines the SQL executor used by account storage.
// Stored account entities are converted into identity values before HTTP or
// authentication consumers read them. Scalar fields and optional pointers pass
// through directly, while the stored notification-recipient JSON is decoded
// into domain entries when its text is populated.

// SQLExecutor supplies raw account mutations and row queries beside Ent entity
// operations.
type SQLExecutor interface {
	// ExecContext executes the supplied statement with arguments and returns its
	// SQL result. Account adapters inspect affected rows when needed and retain
	// the caller responsibility for any surrounding transaction.
	ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error)
	// QueryContext executes the supplied row query with arguments and returns its
	// rows. Account adapters own closing successful rows and inspect scan and
	// iteration errors before using results.
	QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error)
}

// sqlQueryer names the shared infrastructure row-query interface used by
// account single-row scans.
type sqlQueryer = postgresinfra.Queryer

// UserFromEntity maps a stored account to an identity value, returning nil for
// nil input. Optional account pointers remain shared; nonempty notification
// JSON receives the recipient parser, while grants and subscriptions are
// loaded separately.
func UserFromEntity(u *dbent.User) *identitycore.User {
	if u == nil {
		return nil
	}
	out := &identitycore.User{
		PreferredLocale:            u.PreferredLocale,
		ID:                         u.ID,
		Email:                      u.Email,
		Username:                   u.Username,
		Notes:                      u.Notes,
		PasswordHash:               u.PasswordHash,
		Role:                       u.Role,
		Balance:                    u.Balance,
		BalanceRechargeMultiplier:  u.BalanceRechargeMultiplier,
		FrozenBalance:              u.FrozenBalance,
		Concurrency:                u.Concurrency,
		Status:                     u.Status,
		SignupSource:               u.SignupSource,
		LastLoginAt:                u.LastLoginAt,
		LastActiveAt:               u.LastActiveAt,
		TotpSecretEncrypted:        u.TotpSecretEncrypted,
		TotpEnabled:                u.TotpEnabled,
		TotpEnabledAt:              u.TotpEnabledAt,
		BalanceNotifyEnabled:       u.BalanceNotifyEnabled,
		BalanceNotifyThresholdType: u.BalanceNotifyThresholdType,
		BalanceNotifyThreshold:     u.BalanceNotifyThreshold,
		TotalRecharged:             u.TotalRecharged,
		RPMLimit:                   u.RpmLimit,
		APIKeyLimit:                u.APIKeyLimit,
		CreatedAt:                  u.CreatedAt,
		UpdatedAt:                  u.UpdatedAt,
		DeletedAt:                  u.DeletedAt,
	}
	// Notification recipients were stored as both string lists and structured
	// entries. The shared parser accepts those representations before account
	// consumers inspect delivery and verification flags.
	if u.BalanceNotifyExtraEmails != "" && u.BalanceNotifyExtraEmails != "[]" {
		out.BalanceNotifyExtraEmails = identitycore.ParseNotifyEmails(u.BalanceNotifyExtraEmails)
	}
	return out
}
