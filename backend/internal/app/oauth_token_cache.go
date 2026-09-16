// 组合根构造唯一 OAuth token 缓存，所有平台复用同一个 Redis 客户端与键协议。
package app

import (
	"github.com/TokenFlux/TokenRouter/internal/account"
	"github.com/TokenFlux/TokenRouter/internal/account/rediscache"
	"github.com/redis/go-redis/v9"
)

func provideOAuthTokenCache(rdb *redis.Client) account.AccessTokenCache {
	return rediscache.NewOAuthTokenCache(rdb)
}
