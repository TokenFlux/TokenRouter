package repository

import (
	"github.com/TokenFlux/TokenRouter/internal/account"
	"github.com/TokenFlux/TokenRouter/internal/account/rediscache"
	"github.com/redis/go-redis/v9"
)

// NewTimeoutCounterCache 保留旧构造入口，仅委托账号所属的唯一 Redis 实现。
func NewTimeoutCounterCache(rdb *redis.Client) account.TimeoutCounterCache {
	return rediscache.NewTimeoutCounterCache(rdb)
}
