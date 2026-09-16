// 邮箱凭据缓存由 identity 唯一持有，旧构造保留兼容。
package repository

import (
	"github.com/TokenFlux/TokenRouter/internal/identity"
	"github.com/TokenFlux/TokenRouter/internal/identity/rediscache"
	"github.com/redis/go-redis/v9"
)

func NewEmailCache(r *redis.Client) identity.EmailCache { return rediscache.NewEmailCache(r) }
