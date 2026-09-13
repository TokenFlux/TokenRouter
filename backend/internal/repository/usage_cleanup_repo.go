// 旧清理存储只保留构造转接，SQL 由 usage/postgres 唯一持有。
package repository

import (
	"database/sql"

	dbent "github.com/TokenFlux/TokenRouter/ent"
	"github.com/TokenFlux/TokenRouter/internal/service"
	pg "github.com/TokenFlux/TokenRouter/internal/usage/postgres"
)

func NewUsageCleanupRepository(client *dbent.Client, db *sql.DB) service.UsageCleanupRepository {
	return pg.NewUsageCleanupRepository(client, db)
}
