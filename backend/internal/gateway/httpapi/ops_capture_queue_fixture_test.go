package httpapi

import (
	"context"
	"testing"

	"github.com/TokenFlux/TokenRouter/internal/apikey"
	"github.com/gin-gonic/gin"

	"github.com/TokenFlux/TokenRouter/internal/ops"
)

type opsErrorLogJob struct {
	ops   *ops.OpsService
	entry *ops.OpsInsertErrorLogInput
}

var opsErrorLogQueue chan opsErrorLogJob
var testOpsCaptureQueue *captureOpsErrorQueue

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
	previous, previousQueue := opsErrorLogQueue, testOpsCaptureQueue
	opsErrorLogQueue = make(chan opsErrorLogJob, size)
	testOpsCaptureQueue = &captureOpsErrorQueue{}
	t.Cleanup(func() { opsErrorLogQueue, testOpsCaptureQueue = previous, previousQueue })
}
func flushOpsErrorLogBatch(batch []opsErrorLogJob) {
	for _, job := range batch {
		if job.ops != nil && job.entry != nil {
			_ = job.ops.RecordErrorBatch(context.Background(), []*ops.OpsInsertErrorLogInput{job.entry})
		}
	}
}

// 测试队列只观察同步提交；真实工作线程与停机契约在 ops 集成测试中验证。
func OpsErrorLogQueueLength() int64   { return testOpsCaptureQueue.Health().Length }
func OpsErrorLogEnqueuedTotal() int64 { return testOpsCaptureQueue.Health().Enqueued }

func newOpsServiceFixture(repo ops.OpsRepository, settings ops.Settings) *ops.OpsService {
	return ops.NewOpsService(repo, settings, nil, nil, nil, nil, nil, nil)
}

// opsAccessFixture 注入只读观测端口，不模拟完整鉴权或伪造已验证主体。
func opsAccessFixture() OpsObservationAccess {
	return OpsObservationAccess{
		APIKey: func(c *gin.Context) *apikey.APIKey {
			if key, ok := EffectiveAPIKey(c); ok && key != nil {
				return key
			}
			value, _ := c.Get("ops_fallback_api_key")
			key, _ := value.(*apikey.APIKey)
			return key
		},
		Rejected: func(c *gin.Context) bool { return c != nil && c.GetBool("ops_test_rejected") },
	}
}
func markOpsIngressRejectedFixture(c *gin.Context) { c.Set("ops_test_rejected", true) }
func opsLoggerFixture(service *ops.OpsService) gin.HandlerFunc {
	return OpsErrorLoggerMiddleware(service, testOpsCaptureQueue, opsAccessFixture())
}
