package handler

import (
	"context"
	"testing"

	"github.com/TokenFlux/TokenRouter/internal/ops"
	"github.com/TokenFlux/TokenRouter/internal/service"
)

type opsErrorLogJob struct {
	ops   *service.OpsService
	entry *service.OpsInsertErrorLogInput
}

var opsErrorLogQueue chan opsErrorLogJob

type captureOpsErrorQueue struct{ health ops.ErrorLogQueueHealth }

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
	case opsErrorLogQueue <- opsErrorLogJob{ops: s, entry: e}:
		q.health.Length++
		q.health.Enqueued++
	default:
		q.health.Dropped++
	}
}
func (q *captureOpsErrorQueue) Shutdown(context.Context) error  { return nil }
func (q *captureOpsErrorQueue) Health() ops.ErrorLogQueueHealth { return q.health }
func setupOpsErrorLogTestQueue(t *testing.T, size int) {
	t.Helper()
	opsErrorLogQueue = make(chan opsErrorLogJob, size)
	restore := BindOpsErrorQueue(&captureOpsErrorQueue{})
	t.Cleanup(restore)
}
func flushOpsErrorLogBatch(batch []opsErrorLogJob) {
	for _, job := range batch {
		if job.ops != nil && job.entry != nil {
			_ = job.ops.RecordErrorBatch(context.Background(), []*ops.OpsInsertErrorLogInput{job.entry})
		}
	}
}
