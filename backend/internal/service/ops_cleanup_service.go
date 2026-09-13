// 旧入口只投影依赖，生产算法由 Ops 唯一持有。
package service

import (
	"database/sql"

	"github.com/TokenFlux/TokenRouter/internal/config"
	"github.com/TokenFlux/TokenRouter/internal/ops"
	"github.com/TokenFlux/TokenRouter/internal/ops/postgres"
	"github.com/TokenFlux/TokenRouter/internal/ops/rediscache"
	"github.com/redis/go-redis/v9"
)

type OpsCleanupService = ops.OpsCleanupService

func NewOpsCleanupService(repo OpsRepository, db *sql.DB, r *redis.Client, cfg *config.Config, settings SettingRepository) *OpsCleanupService {
	return ops.NewOpsCleanupService(repo, postgres.NewCleanupStore(db), rediscache.NewRuntime(r), LegacyOpsOptions(cfg), settings)
}
