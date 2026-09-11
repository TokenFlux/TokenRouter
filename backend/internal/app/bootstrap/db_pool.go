// 本文件只把启动配置转换为 PostgreSQL 连接池参数。
package bootstrap

import (
	"database/sql"

	"github.com/TokenFlux/TokenRouter/internal/config"
	postgresinfra "github.com/TokenFlux/TokenRouter/internal/infra/postgres"
)

type dbPoolSettings = postgresinfra.PoolSettings

func postgresPoolOptions(cfg *config.Config) postgresinfra.PoolOptions {
	return postgresinfra.PoolOptions{
		MaxOpenConns:           cfg.Database.MaxOpenConns,
		MaxIdleConns:           cfg.Database.MaxIdleConns,
		ConnMaxLifetimeMinutes: cfg.Database.ConnMaxLifetimeMinutes,
		ConnMaxIdleTimeMinutes: cfg.Database.ConnMaxIdleTimeMinutes,
	}
}
func clampDBPoolSettings(cfg *config.Config) dbPoolSettings {
	return postgresinfra.ResolvePoolSettings(postgresPoolOptions(cfg))
}
func applyDBPoolSettings(db *sql.DB, cfg *config.Config) {
	postgresinfra.ApplyPoolSettings(db, postgresPoolOptions(cfg))
}
