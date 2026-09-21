package postgres

import (
	"context"
	"database/sql"

	"github.com/TokenFlux/TokenRouter/migrations"
)

// applyEmbeddedMigrations 保留原测试对发布迁移集合的契约覆盖。
func applyEmbeddedMigrations(ctx context.Context, db *sql.DB) error {
	return ApplyMigrations(ctx, db, migrations.FS)
}
