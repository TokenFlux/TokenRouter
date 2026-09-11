package postgres

import (
	"context"
	"database/sql"
	"errors"
	dbent "github.com/TokenFlux/TokenRouter/ent"
	_ "github.com/TokenFlux/TokenRouter/ent/runtime"
	postgresinfra "github.com/TokenFlux/TokenRouter/internal/infra/postgres"
	"github.com/TokenFlux/TokenRouter/internal/site"
)

// clientFromContext 沿用现有 Ent 事务键，不另建事务状态。
func clientFromContext(ctx context.Context, fallback *dbent.Client) *dbent.Client {
	if tx := dbent.TxFromContext(ctx); tx != nil {
		return tx.Client()
	}
	return fallback
}
func announcementPersistenceError(err error) error {
	if errors.Is(err, sql.ErrNoRows) || dbent.IsNotFound(err) {
		return site.ErrAnnouncementNotFound.WithCause(err)
	}
	return err
}
func isSQLNoRowsError(err error) bool { return postgresinfra.IsNoRows(err) }
