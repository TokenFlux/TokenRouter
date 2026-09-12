// 本文件维护 repository 的所属能力；兼容入口复用唯一实现。
package repository

import (
	rediscache "github.com/TokenFlux/TokenRouter/internal/identity/rediscache"
	service "github.com/TokenFlux/TokenRouter/internal/service"
	redis "github.com/redis/go-redis/v9"
)

func NewRefreshTokenCache(rdb *redis.Client) service.RefreshTokenCache {
	return rediscache.NewRefreshTokenCache(rdb)
}
