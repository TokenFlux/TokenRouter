package repository

import (
	"github.com/TokenFlux/TokenRouter/internal/account"
	"github.com/TokenFlux/TokenRouter/internal/account/rediscache"
	"github.com/redis/go-redis/v9"
)

// NewTempUnschedCache 保留旧构造入口，仅委托账号所属的唯一 Redis 实现。
func NewTempUnschedCache(rdb *redis.Client) account.TempUnschedCache {
	return rediscache.NewTempUnschedCache(rdb)
}
