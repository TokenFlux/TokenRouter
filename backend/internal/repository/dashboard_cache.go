// 旧构造入口只投影配置，Redis 实现由 usage 唯一拥有。
package repository

import (
	"github.com/TokenFlux/TokenRouter/internal/config"
	"github.com/TokenFlux/TokenRouter/internal/service"
	"github.com/TokenFlux/TokenRouter/internal/usage/rediscache"
	"github.com/redis/go-redis/v9"
)

func NewDashboardCache(r *redis.Client, cfg *config.Config) service.DashboardStatsCache {
	prefix := "sub2api:"
	if cfg != nil {
		prefix = cfg.Dashboard.KeyPrefix
	}
	return rediscache.NewDashboardCache(r, prefix)
}
