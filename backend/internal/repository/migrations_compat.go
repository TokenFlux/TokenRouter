package repository

import (
	"context"
	"database/sql"
	postgresinfra "github.com/TokenFlux/TokenRouter/internal/infra/postgres"
	"github.com/TokenFlux/TokenRouter/migrations"
)

// ApplyMigrations 仅供尚未迁出的维护与测试入口使用，S14/S16 清理。
func ApplyMigrations(ctx context.Context, db *sql.DB) error {
	return postgresinfra.ApplyMigrations(ctx, db, migrations.FS)
}
