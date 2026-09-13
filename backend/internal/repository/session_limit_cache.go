// 旧构造只组合调度与资金缓存；不保留 Lua、算法或额外状态。
package repository

import (
	"github.com/TokenFlux/TokenRouter/internal/billing"
	billingredis "github.com/TokenFlux/TokenRouter/internal/billing/rediscache"
	"github.com/TokenFlux/TokenRouter/internal/scheduler"
	schedulerredis "github.com/TokenFlux/TokenRouter/internal/scheduler/rediscache"
	"github.com/TokenFlux/TokenRouter/internal/service"
	"github.com/redis/go-redis/v9"
)

type sessionLimitCompatibility struct {
	scheduler.SessionLimitCache
	billing.WindowCostCache
}

func NewSessionLimitCache(rdb *redis.Client, minutes int) service.SessionLimitCache {
	return sessionLimitCompatibility{schedulerredis.NewSessionLimitCache(rdb, minutes), billingredis.NewWindowCostCache(rdb)}
}
