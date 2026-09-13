package repository

import (
	"github.com/TokenFlux/TokenRouter/internal/account"
	"github.com/TokenFlux/TokenRouter/internal/account/rediscache"
	"github.com/redis/go-redis/v9"
)

// NewOpenAI403CounterCache 保留旧构造入口，仅委托账号所属的唯一 Redis 实现。
func NewOpenAI403CounterCache(rdb *redis.Client) account.OpenAI403CounterCache {
	return rediscache.NewOpenAI403CounterCache(rdb)
}
