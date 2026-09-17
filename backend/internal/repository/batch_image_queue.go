// 旧入口只投影配置，Redis 实现由任务模块唯一持有。
package repository

import (
	"time"

	native "github.com/TokenFlux/TokenRouter/internal/batchimage/rediscache"
	"github.com/TokenFlux/TokenRouter/internal/config"
	"github.com/TokenFlux/TokenRouter/internal/service"
	"github.com/redis/go-redis/v9"
)

func NewBatchImageQueue(rdb *redis.Client, cfg *config.Config) service.BatchImageQueue {
	if cfg == nil {
		return native.NewBatchImageQueue(rdb, nil)
	}
	return native.NewBatchImageQueue(rdb, &native.QueueOptions{ReadyKey: cfg.BatchImage.QueueReadyKey, DelayedKey: cfg.BatchImage.QueueDelayedKey, ActiveKey: cfg.BatchImage.QueueActiveKey, InflightPrefix: cfg.BatchImage.InflightKeyPrefix, LockPrefix: cfg.BatchImage.LockKeyPrefix, InflightTTL: time.Duration(cfg.BatchImage.InflightTTLSeconds) * time.Second, LockTTL: time.Duration(cfg.BatchImage.JobLockTTLSeconds) * time.Second})
}
