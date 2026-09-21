//go:build unit

package integration

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/TokenFlux/TokenRouter/internal/batchimage"
	batchredis "github.com/TokenFlux/TokenRouter/internal/batchimage/rediscache"
	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/require"
)

func TestBatchImageWorkerRuntime_StartupDoesNotCreateRedisBatchImageKeys(t *testing.T) {
	mr := miniredis.RunT(t)
	rdb := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	t.Cleanup(func() {
		_ = rdb.Close()
	})

	queue := batchredis.NewBatchImageQueue(rdb, &batchredis.QueueOptions{
		ReadyKey: "batch_image:queue:ready", DelayedKey: "batch_image:queue:delayed",
		ActiveKey: "batch_image:queue:active", InflightPrefix: "batch_image:queue:inflight:",
		LockPrefix: "batch_image:queue:lock:", InflightTTL: time.Minute, LockTTL: time.Minute,
	})
	worker := batchimage.NewBatchImageWorker(queue, noopBatchImageProcessor{}, batchimage.BatchImageWorkerOptions{
		JobLockTTL: time.Minute, DelayedPollInterval: time.Minute,
		RecoveryInterval: time.Minute, StaleActiveAfter: time.Minute,
		DelayedMoveLimit: 10, RecoverLimit: 10,
	})
	// 生产无恢复器时仅运行三条队列循环，构造期间不得创建 Redis 状态。
	runtime := batchimage.NewRuntime("batch image worker", true, worker.Run, worker.RunDelayedMover, worker.RunStaleActiveRecovery)

	runtime.Start()
	require.Eventually(t, runtime.Running, time.Second, 10*time.Millisecond)
	runtime.Stop()

	for _, key := range mr.Keys() {
		require.False(t, strings.HasPrefix(key, "batch_image:"), "unexpected Redis key created at startup: %s", key)
	}
}

type noopBatchImageProcessor struct{}

func (noopBatchImageProcessor) Process(context.Context, string) (batchimage.BatchImageProcessResult, error) {
	return batchimage.BatchImageProcessResult{}, nil
}
