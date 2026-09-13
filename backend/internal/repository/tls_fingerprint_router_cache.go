// 本文件维护 repository 的所属能力；兼容入口复用唯一实现。
package repository

import (
	rediscache "github.com/TokenFlux/TokenRouter/internal/egress/rediscache"
	service "github.com/TokenFlux/TokenRouter/internal/service"
	redis "github.com/redis/go-redis/v9"
)

// NewTLSFingerprintRouterCache 委托所属模块的唯一实现。
func NewTLSFingerprintRouterCache(rdb *redis.Client) service.TLSFingerprintRouterCache {
	return rediscache.NewTLSFingerprintRouterCache(rdb)
}
