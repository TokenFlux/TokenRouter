// 原缓存构造仅委托 scheduler/rediscache，键、Lua 和状态只有一份实现。
package repository

import (
	"github.com/TokenFlux/TokenRouter/internal/scheduler"
	schedulerredis "github.com/TokenFlux/TokenRouter/internal/scheduler/rediscache"
	"github.com/redis/go-redis/v9"
)

func NewUserMsgQueueCache(rdb *redis.Client) scheduler.UserMsgQueueCache {
	return schedulerredis.NewUserMsgQueueCache(rdb)
}
