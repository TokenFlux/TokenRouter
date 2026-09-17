// 旧下载限流入口只投影配置，不复制 Redis 计数状态。
package repository

import (
	native "github.com/TokenFlux/TokenRouter/internal/batchimage/rediscache"
	"github.com/TokenFlux/TokenRouter/internal/config"
	"github.com/TokenFlux/TokenRouter/internal/service"
	"github.com/redis/go-redis/v9"
)

func NewBatchImageDownloadLimiter(rdb *redis.Client, cfg *config.Config) service.BatchImageDownloadLimiter {
	if cfg == nil {
		return native.NewBatchImageDownloadLimiter(rdb, nil)
	}
	return native.NewBatchImageDownloadLimiter(rdb, &native.DownloadOptions{MaxDownloadConcurrencyPerUser: cfg.BatchImage.MaxDownloadConcurrencyPerUser, MaxDownloadDurationSeconds: cfg.BatchImage.MaxDownloadDurationSeconds})
}
