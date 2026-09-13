// HTTP 捕获层只提交观测输入；生产队列由 app 绑定，独立旧入口可按需构造。
package handler

import (
	"context"
	"log"
	"runtime"
	"runtime/debug"
	"sync"
	"time"

	"github.com/TokenFlux/TokenRouter/internal/ops"
	"github.com/TokenFlux/TokenRouter/internal/service"
)

type OpsErrorQueue interface {
	Enqueue(*ops.OpsService, *ops.OpsInsertErrorLogInput)
	Shutdown(context.Context) error
	Health() ops.ErrorLogQueueHealth
}

var opsErrorQueueBinding struct {
	sync.Mutex
	queue OpsErrorQueue
}

func currentOpsErrorQueue() OpsErrorQueue {
	opsErrorQueueBinding.Lock()
	defer opsErrorQueueBinding.Unlock()
	if opsErrorQueueBinding.queue == nil {
		opsErrorQueueBinding.queue = ops.NewErrorLogQueue(ops.ErrorLogQueueOptions{Processors: func() int { return runtime.GOMAXPROCS(0) }, Logf: log.Printf, Stack: debug.Stack})
	}
	return opsErrorQueueBinding.queue
}

// BindOpsErrorQueue 仅在装配/测试边界替换消费者，不启动任何工作。
func BindOpsErrorQueue(q OpsErrorQueue) func() {
	opsErrorQueueBinding.Lock()
	previous := opsErrorQueueBinding.queue
	opsErrorQueueBinding.queue = q
	opsErrorQueueBinding.Unlock()
	return func() {
		opsErrorQueueBinding.Lock()
		opsErrorQueueBinding.queue = previous
		opsErrorQueueBinding.Unlock()
	}
}
func enqueueOpsErrorLog(s *service.OpsService, e *service.OpsInsertErrorLogInput) {
	currentOpsErrorQueue().Enqueue(s, e)
}
func ShutdownOpsErrorLogWorkers(ctx context.Context) error {
	return currentOpsErrorQueue().Shutdown(ctx)
}
func StopOpsErrorLogWorkers() bool {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	return ShutdownOpsErrorLogWorkers(ctx) == nil
}

func OpsErrorLogQueueLength() int64        { return currentOpsErrorQueue().Health().Length }
func OpsErrorLogQueueBytes() int64         { return currentOpsErrorQueue().Health().Bytes }
func OpsErrorLogQueueBytesCapacity() int64 { return currentOpsErrorQueue().Health().BytesCapacity }
func OpsErrorLogQueueCapacity() int        { return currentOpsErrorQueue().Health().Capacity }
func OpsErrorLogDroppedTotal() int64       { return currentOpsErrorQueue().Health().Dropped }
func OpsErrorLogEnqueuedTotal() int64      { return currentOpsErrorQueue().Health().Enqueued }
func OpsErrorLogProcessedTotal() int64     { return currentOpsErrorQueue().Health().Processed }
func OpsErrorLogSanitizedTotal() int64     { return currentOpsErrorQueue().Health().Sanitized }
