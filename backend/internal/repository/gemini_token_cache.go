// 旧构造器只转接账号模块的唯一 token 缓存，所有平台共用原 Redis 实例。
package repository

import (
	"github.com/TokenFlux/TokenRouter/internal/account/rediscache"
	"github.com/TokenFlux/TokenRouter/internal/service"
	"github.com/redis/go-redis/v9"
)

func NewGeminiTokenCache(rdb *redis.Client) service.GeminiTokenCache {
	return rediscache.NewOAuthTokenCache(rdb)
}
