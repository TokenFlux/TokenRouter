// 旧入口只投影配置，Redis 实现由任务模块唯一持有。
package repository

import (
	"github.com/TokenFlux/TokenRouter/internal/config"
	native "github.com/TokenFlux/TokenRouter/internal/creative/rediscache"
	"github.com/TokenFlux/TokenRouter/internal/service"
	"github.com/redis/go-redis/v9"
)

func NewCreativeTransientStore(rdb *redis.Client, cfg *config.Config) service.CreativeTransientStore {
	if cfg == nil {
		return native.NewCreativeTransientStore(rdb, nil)
	}
	return native.NewCreativeTransientStore(rdb, &native.TransientOptions{TransientTTLSeconds: cfg.Creative.TransientTTLSeconds})
}
