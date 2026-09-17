// 旧 worker 入口只投影配置和观测，调度循环由 batchimage 唯一实现。
package service

import (
	"context"
	"time"

	native "github.com/TokenFlux/TokenRouter/internal/batchimage"
	"github.com/TokenFlux/TokenRouter/internal/config"
	"github.com/TokenFlux/TokenRouter/internal/pkg/logger"
	"go.uber.org/zap"
)

type BatchImageProcessor = native.BatchImageProcessor
type BatchImageProcessResult = native.BatchImageProcessResult
type BatchImageWorkerOptions = native.BatchImageWorkerOptions
type BatchImageWorker = native.BatchImageWorker

func NewBatchImageWorker(queue BatchImageQueue, processor BatchImageProcessor, opts BatchImageWorkerOptions) *BatchImageWorker {
	if opts.Observe == nil {
		opts.Observe = func(event string, values ...any) {
			fields := make([]zap.Field, 0, len(values)/2)
			for i := 0; i+1 < len(values); i += 2 {
				key, _ := values[i].(string)
				fields = append(fields, zap.Any(key, values[i+1]))
			}
			logger.L().Warn(event, fields...)
		}
	}
	return native.NewBatchImageWorker(queue, processor, opts)
}
func NewBatchImageWorkerOptionsFromConfig(cfg *config.Config) BatchImageWorkerOptions {
	if cfg == nil {
		return normalizeBatchImageWorkerOptions(BatchImageWorkerOptions{})
	}
	return normalizeBatchImageWorkerOptions(BatchImageWorkerOptions{
		JobLockTTL:          time.Duration(cfg.BatchImage.JobLockTTLSeconds) * time.Second,
		LockConflictDelay:   time.Duration(cfg.BatchImage.LockConflictDelaySeconds) * time.Second,
		DefaultRequeueDelay: time.Duration(cfg.BatchImage.DefaultRequeueDelaySeconds) * time.Second,
		ErrorRetryDelay:     time.Duration(cfg.BatchImage.ErrorRetryDelaySeconds) * time.Second,
		DelayedPollInterval: time.Duration(cfg.BatchImage.DelayedMoverIntervalSeconds) * time.Second,
		RecoveryInterval:    time.Duration(cfg.BatchImage.RecoveryIntervalSeconds) * time.Second,
		StaleActiveAfter:    time.Duration(cfg.BatchImage.StaleActiveAfterSeconds) * time.Second,
		DelayedMoveLimit:    cfg.BatchImage.DelayedMoveLimit,
		RecoverLimit:        cfg.BatchImage.RecoverLimit,
	})
}
func normalizeBatchImageWorkerOptions(opts BatchImageWorkerOptions) BatchImageWorkerOptions {
	return native.NormalizeBatchImageWorkerOptions(opts)
}

type BatchImageJobLockRefresher = native.BatchImageJobLockRefresher

func sleepOrDone(ctx context.Context, d time.Duration) { native.SleepOrDone(ctx, d) }
