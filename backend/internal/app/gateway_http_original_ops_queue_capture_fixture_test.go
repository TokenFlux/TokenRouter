package app

import (
	"github.com/TokenFlux/TokenRouter/internal/ops"
)

type opsErrorLogJob struct {
	ops   *ops.OpsService
	entry *ops.OpsInsertErrorLogInput
}

// captureOpsErrorQueue 是单测试独立的同步观察替身，不安装全局队列。
type captureOpsErrorQueue struct {
	health ops.ErrorLogQueueHealth
	jobs   chan opsErrorLogJob
}

func newOpsCaptureQueue(size int) *captureOpsErrorQueue {
	return &captureOpsErrorQueue{jobs: make(chan opsErrorLogJob, size)}
}

func (q *captureOpsErrorQueue) Enqueue(s *ops.OpsService, e *ops.OpsInsertErrorLogInput) {
	if s == nil || e == nil {
		return
	}
	sanitized, err := ops.PrepareErrorLogInput(e)
	if sanitized {
		q.health.Sanitized++
	}
	if err != nil {
		q.health.Dropped++
		return
	}
	select {
	case q.jobs <- opsErrorLogJob{ops: s, entry: e}:
		q.health.Length++
		q.health.Enqueued++
	default:
		q.health.Dropped++
	}
}
