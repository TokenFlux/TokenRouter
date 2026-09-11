package repository

import (
	redisinfra "github.com/TokenFlux/TokenRouter/internal/infra/redis"
	"github.com/TokenFlux/TokenRouter/internal/service"
	"github.com/redis/go-redis/v9"
)

// NewLeaderLockCache 兼容旧作用域拥有者，S08/S14 随其迁移删除。
func NewLeaderLockCache(rdb *redis.Client) service.LeaderLockCache {
	return redisinfra.NewLeaderLockCache(rdb)
}
