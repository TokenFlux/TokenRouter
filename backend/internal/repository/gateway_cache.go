// 旧缓存构造只委托 gateway 的存储 Adapter，全部 namespace 保持原实现。
package repository

import (
	"github.com/TokenFlux/TokenRouter/internal/gateway/rediscache"
	"github.com/TokenFlux/TokenRouter/internal/service"
	"github.com/redis/go-redis/v9"
)

func NewGatewayCache(rdb *redis.Client) service.GatewayCache { return rediscache.NewGatewayCache(rdb) }
func HashLiveCallID(callID string) string                    { return rediscache.HashLiveCallID(callID) }
