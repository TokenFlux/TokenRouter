package app

import (
	"github.com/TokenFlux/TokenRouter/internal/billing"
	billingredis "github.com/TokenFlux/TokenRouter/internal/billing/rediscache"
	"github.com/redis/go-redis/v9"
)

// provideWindowCostCache 直接构造唯一资金窗口缓存，沿用原 Redis 客户端与命名空间。
func provideWindowCostCache(rdb *redis.Client) billing.WindowCostCache {
	return billingredis.NewWindowCostCache(rdb)
}
