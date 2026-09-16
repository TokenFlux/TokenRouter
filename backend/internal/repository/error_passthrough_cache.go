// 兼容构造器委托 gateway 的唯一存储实现。
package repository

import (
	"github.com/TokenFlux/TokenRouter/internal/gateway/rediscache"
	"github.com/TokenFlux/TokenRouter/internal/service"
	"github.com/redis/go-redis/v9"
)

func NewErrorPassthroughCache(rdb *redis.Client) service.ErrorPassthroughCache {
	return rediscache.NewErrorPassthroughCache(rdb)
}
