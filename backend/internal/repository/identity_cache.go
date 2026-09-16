// 兼容构造委托唯一 Anthropic 指纹存储，S15/S16 删除。
package repository

import (
	"github.com/TokenFlux/TokenRouter/internal/service"
	native "github.com/TokenFlux/TokenRouter/internal/upstream/anthropic/rediscache"
	"github.com/redis/go-redis/v9"
)

func NewIdentityCache(rdb *redis.Client) service.IdentityCache {
	return native.NewFingerprintStore(rdb)
}
