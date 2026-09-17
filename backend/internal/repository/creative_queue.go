// 旧入口只投影配置，Redis 实现由任务模块唯一持有。
package repository

import (
	"github.com/TokenFlux/TokenRouter/internal/config"
	native "github.com/TokenFlux/TokenRouter/internal/creative/rediscache"
	"github.com/TokenFlux/TokenRouter/internal/service"
	"github.com/redis/go-redis/v9"
)

func NewCreativeQueue(rdb *redis.Client, cfg *config.Config) service.CreativeRunQueue {
	if cfg == nil {
		return native.NewCreativeQueue(rdb, nil)
	}
	return native.NewCreativeQueue(rdb, &native.QueueOptions{InflightKeyPrefix: cfg.Creative.InflightKeyPrefix, InflightTTLSeconds: cfg.Creative.InflightTTLSeconds, JobLockTTLSeconds: cfg.Creative.JobLockTTLSeconds, LockKeyPrefix: cfg.Creative.LockKeyPrefix, QueueActiveKey: cfg.Creative.QueueActiveKey, QueueDelayedKey: cfg.Creative.QueueDelayedKey, QueueReadyKey: cfg.Creative.QueueReadyKey})
}
