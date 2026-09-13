// 旧后台构造器只完成参数投影，S15/S16 清理。
package service

import (
	"database/sql"

	"github.com/TokenFlux/TokenRouter/internal/config"
	"github.com/TokenFlux/TokenRouter/internal/ops"
	"github.com/TokenFlux/TokenRouter/internal/ops/postgres"
	"github.com/TokenFlux/TokenRouter/internal/ops/rediscache"
	"github.com/redis/go-redis/v9"
)

type OpsAggregationService = ops.OpsAggregationService

func NewOpsAggregationService(repo OpsRepository, settings SettingRepository, db *sql.DB, r *redis.Client, cfg *config.Config, pre *PreAggregationSettingsService) *OpsAggregationService {
	var p ops.PreAggregationRuntimeReader
	if pre != nil {
		p = pre
	}
	return ops.NewOpsAggregationService(repo, settings, postgres.NewAdvisory(db), rediscache.NewRuntime(r), LegacyOpsOptions(cfg), p)
}
