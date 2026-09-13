// Redis 格式保持原样，旧入口只转接。
package repository

import (
	"github.com/TokenFlux/TokenRouter/internal/ops/rediscache"
	"github.com/TokenFlux/TokenRouter/internal/service"
	"github.com/redis/go-redis/v9"
)

func NewUpdateCache(r *redis.Client) service.UpdateCache { return rediscache.NewUpdateCache(r) }
