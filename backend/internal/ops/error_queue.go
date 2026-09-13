// ErrorLogQueue 唯一拥有错误采集批次、字节容量和停止状态；网关只提供观测输入。
package ops

import (
	"context"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

type ErrorLogQueueOptions struct {
	Processors func() int
	Logf       func(string, ...any)
	Stack      func() []byte
}
type ErrorLogQueue struct {
	options                                                                                                                                                   ErrorLogQueueOptions
	opsErrorLogOnce                                                                                                                                           sync.Once
	opsErrorLogQueue                                                                                                                                          chan opsErrorLogJob
	opsErrorLogStopOnce                                                                                                                                       sync.Once
	opsErrorLogStopDone                                                                                                                                       chan struct{}
	opsErrorLogWorkersWg                                                                                                                                      sync.WaitGroup
	opsErrorLogMu                                                                                                                                             sync.RWMutex
	opsErrorLogStopping                                                                                                                                       bool
	opsErrorLogQueueLen, opsErrorLogQueueBytes, opsErrorLogEnqueued, opsErrorLogDropped, opsErrorLogProcessed, opsErrorLogSanitized, opsErrorLogLastDropLogAt atomic.Int64
	opsErrorLogShutdownCh                                                                                                                                     chan struct{}
	opsErrorLogShutdownOnce                                                                                                                                   sync.Once
	opsErrorLogDrained                                                                                                                                        atomic.Bool
}

func NewErrorLogQueue(options ErrorLogQueueOptions) *ErrorLogQueue {
	return &ErrorLogQueue{options: options, opsErrorLogShutdownCh: make(chan struct{})}
}
func (q *ErrorLogQueue) report(format string, args ...any) {
	if q.options.Logf != nil {
		q.options.Logf(format, args...)
	}
}
func (q *ErrorLogQueue) stack() []byte {
	if q.options.Stack != nil {
		return q.options.Stack()
	}
	return nil
}
func (q *ErrorLogQueue) processors() int {
	if q.options.Processors != nil {
		return q.options.Processors()
	}
	return 1
}

type ErrorLogQueueHealth struct {
	Length, Bytes, BytesCapacity            int64
	Capacity                                int
	Dropped, Enqueued, Processed, Sanitized int64
}

func (q *ErrorLogQueue) Health() ErrorLogQueueHealth {
	return ErrorLogQueueHealth{q.OpsErrorLogQueueLength(), q.OpsErrorLogQueueBytes(), q.OpsErrorLogQueueBytesCapacity(), q.OpsErrorLogQueueCapacity(), q.OpsErrorLogDroppedTotal(), q.OpsErrorLogEnqueuedTotal(), q.OpsErrorLogProcessedTotal(), q.OpsErrorLogSanitizedTotal()}
}

// PrepareErrorLogInput 在入队前移除原始敏感字段并限制载荷，供边界适配复用。
func PrepareErrorLogInput(entry *OpsInsertErrorLogInput) (bool, error) {
	entry.UserAgent = normalizeOpsPersistentUserAgent(entry.UserAgent)
	sanitized := false
	if entry.ErrorBody != "" {
		before := entry.ErrorBody
		body, truncated := SanitizeOpsErrorBodyForQueue(before)
		entry.ErrorBody = body
		sanitized = truncated || body != before
	}
	return sanitized, SanitizeOpsUpstreamErrorsForQueue(entry)
}
func opsErrorLogConfig(processors int) (workerCount int, queueSize int) {
	workerCount = processors * 2
	if workerCount < opsErrorLogMinWorkerCount {
		workerCount = opsErrorLogMinWorkerCount
	}
	if workerCount > opsErrorLogMaxWorkerCount {
		workerCount = opsErrorLogMaxWorkerCount
	}

	queueSize = workerCount * opsErrorLogQueueSizePerWorker
	if queueSize < opsErrorLogMinQueueSize {
		queueSize = opsErrorLogMinQueueSize
	}
	if queueSize > opsErrorLogMaxQueueSize {
		queueSize = opsErrorLogMaxQueueSize
	}

	return workerCount, queueSize
}
func estimateOpsErrorLogJobBytes(entry *OpsInsertErrorLogInput) int64 {
	if entry == nil {
		return 1
	}
	const fixedOverhead = 512
	size := fixedOverhead + len(entry.RequestID) + len(entry.ClientRequestID) +
		len(entry.Platform) + len(entry.Model) + len(entry.RequestPath) +
		len(entry.InboundEndpoint) + len(entry.UpstreamEndpoint) +
		len(entry.RequestedModel) + len(entry.UpstreamModel) + len(entry.UserAgent) +
		len(entry.ErrorPhase) + len(entry.ErrorType) + len(entry.Severity) +
		len(entry.ErrorMessage) + len(entry.ErrorBody) + len(entry.ErrorSource) +
		len(entry.ErrorOwner) + len(entry.APIKeyPrefix)
	if entry.UpstreamErrorMessage != nil {
		size += len(*entry.UpstreamErrorMessage)
	}
	if entry.UpstreamErrorDetail != nil {
		size += len(*entry.UpstreamErrorDetail)
	}
	if entry.UpstreamErrorsJSON != nil {
		size += len(*entry.UpstreamErrorsJSON)
	}
	return int64(size)
}
func (q *ErrorLogQueue) reserveOpsErrorLogQueueBytes(size int64) bool {
	if size < 1 {
		size = 1
	}
	for {
		current := q.opsErrorLogQueueBytes.Load()
		if current > opsErrorLogMaxQueueBytes-size {
			return false
		}
		if q.opsErrorLogQueueBytes.CompareAndSwap(current, current+size) {
			q.opsErrorLogQueueLen.Add(1)
			return true
		}
	}
}
func (q *ErrorLogQueue) maybeLogOpsErrorLogDrop() {
	now := time.Now().Unix()

	for {
		last := q.opsErrorLogLastDropLogAt.Load()
		if last != 0 && now-last < 60 {
			return
		}
		if q.opsErrorLogLastDropLogAt.CompareAndSwap(last, now) {
			break
		}
	}

	queued := q.opsErrorLogQueueLen.Load()
	queuedBytes := q.opsErrorLogQueueBytes.Load()
	queueCap := q.OpsErrorLogQueueCapacity()

	q.report(
		"[OpsErrorLogger] queue is full; dropping logs (queued=%d cap=%d queued_bytes=%d bytes_cap=%d enqueued_total=%d dropped_total=%d processed_total=%d sanitized_total=%d)",
		queued,
		queueCap,
		queuedBytes,
		opsErrorLogMaxQueueBytes,
		q.opsErrorLogEnqueued.Load(),
		q.opsErrorLogDropped.Load(),
		q.opsErrorLogProcessed.Load(),
		q.opsErrorLogSanitized.Load(),
	)
}
func (q *ErrorLogQueue) OpsErrorLogSanitizedTotal() int64 {
	return q.opsErrorLogSanitized.Load()
}
func (q *ErrorLogQueue) OpsErrorLogProcessedTotal() int64 {
	return q.opsErrorLogProcessed.Load()
}
func (q *ErrorLogQueue) OpsErrorLogEnqueuedTotal() int64 {
	return q.opsErrorLogEnqueued.Load()
}
func (q *ErrorLogQueue) OpsErrorLogDroppedTotal() int64 {
	return q.opsErrorLogDropped.Load()
}
func (q *ErrorLogQueue) OpsErrorLogQueueCapacity() int {
	q.opsErrorLogMu.RLock()
	ch := q.opsErrorLogQueue
	q.opsErrorLogMu.RUnlock()
	if ch == nil {
		return 0
	}
	return cap(ch)
}
func (q *ErrorLogQueue) OpsErrorLogQueueBytesCapacity() int64 {
	return opsErrorLogMaxQueueBytes
}
func (q *ErrorLogQueue) OpsErrorLogQueueBytes() int64 {
	return q.opsErrorLogQueueBytes.Load()
}
func (q *ErrorLogQueue) OpsErrorLogQueueLength() int64 {
	return q.opsErrorLogQueueLen.Load()
}

// ShutdownOpsErrorLogWorkers 封闭入队并等待批次真正处理完毕，由组合根提供总预算。
func (q *ErrorLogQueue) Shutdown(ctx context.Context) error {
	q.opsErrorLogStopOnce.Do(func() {
		q.opsErrorLogShutdownOnce.Do(func() { close(q.opsErrorLogShutdownCh) })
		q.opsErrorLogMu.Lock()
		q.opsErrorLogStopping = true
		queue := q.opsErrorLogQueue
		q.opsErrorLogQueue = nil
		q.opsErrorLogStopDone = make(chan struct{})
		if queue != nil {
			close(queue)
		}
		q.opsErrorLogMu.Unlock()
		go func() {
			q.opsErrorLogWorkersWg.Wait()
			q.opsErrorLogQueueLen.Store(0)
			q.opsErrorLogQueueBytes.Store(0)
			q.opsErrorLogDrained.Store(true)
			close(q.opsErrorLogStopDone)
		}()
	})
	select {
	case <-q.opsErrorLogStopDone:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

// StopOpsErrorLogWorkers 保留旧十秒调用约定，实际关闭只执行一次。
func (q *ErrorLogQueue) Stop() bool {
	ctx, cancel := context.WithTimeout(context.Background(), opsErrorLogDrainTimeout)
	defer cancel()
	return q.Shutdown(ctx) == nil
}
func normalizeOpsPersistentUserAgent(value string) string {
	return truncateString(strings.TrimSpace(strings.ToValidUTF8(value, "")), opsErrorLogMaxUserAgentBytes)
}

// @project-doc docs/operations/observability_and_data_lifecycle.md#background_runtimes
func (q *ErrorLogQueue) Enqueue(ops *OpsService, entry *OpsInsertErrorLogInput) {
	if ops == nil || entry == nil {
		return
	}
	sanitized, err := PrepareErrorLogInput(entry)
	if sanitized {
		q.opsErrorLogSanitized.Add(1)
	}
	if err != nil {
		q.opsErrorLogDropped.Add(1)
		q.maybeLogOpsErrorLogDrop()
		return
	}

	select {
	case <-q.opsErrorLogShutdownCh:
		return
	default:
	}

	q.opsErrorLogMu.RLock()
	stopping := q.opsErrorLogStopping
	q.opsErrorLogMu.RUnlock()
	if stopping {
		return
	}

	q.opsErrorLogOnce.Do(q.startOpsErrorLogWorkers)

	q.opsErrorLogMu.RLock()
	defer q.opsErrorLogMu.RUnlock()
	if q.opsErrorLogStopping || q.opsErrorLogQueue == nil {
		return
	}
	queuedBytes := estimateOpsErrorLogJobBytes(entry)
	if !q.reserveOpsErrorLogQueueBytes(queuedBytes) {
		q.opsErrorLogDropped.Add(1)
		q.maybeLogOpsErrorLogDrop()
		return
	}

	select {
	case q.opsErrorLogQueue <- opsErrorLogJob{ops: ops, entry: entry, queuedBytes: queuedBytes}:
		q.opsErrorLogEnqueued.Add(1)
	default:
		q.opsErrorLogQueueLen.Add(-1)
		q.opsErrorLogQueueBytes.Add(-queuedBytes)
		// Queue is full; drop to avoid blocking request handling.
		q.opsErrorLogDropped.Add(1)
		q.maybeLogOpsErrorLogDrop()
	}
}
func (q *ErrorLogQueue) flushOpsErrorLogBatch(batch []opsErrorLogJob) {
	if len(batch) == 0 {
		return
	}
	defer func() {
		if r := recover(); r != nil {
			q.report("[OpsErrorLogger] worker panic: %v\n%s", r, q.stack())
		}
	}()

	grouped := make(map[*OpsService][]*OpsInsertErrorLogInput, len(batch))
	var processed int64
	for _, job := range batch {
		if job.ops == nil || job.entry == nil {
			continue
		}
		grouped[job.ops] = append(grouped[job.ops], job.entry)
		processed++
	}
	if processed == 0 {
		return
	}

	for opsSvc, entries := range grouped {
		if opsSvc == nil || len(entries) == 0 {
			continue
		}
		ctx, cancel := context.WithTimeout(context.Background(), opsErrorLogTimeout)
		_ = opsSvc.RecordErrorBatch(ctx, entries)
		cancel()
	}
	q.opsErrorLogProcessed.Add(processed)
}
func (q *ErrorLogQueue) startOpsErrorLogWorkers() {
	q.opsErrorLogMu.Lock()
	defer q.opsErrorLogMu.Unlock()

	if q.opsErrorLogStopping {
		return
	}

	workerCount, queueSize := opsErrorLogConfig(q.processors())
	q.opsErrorLogQueue = make(chan opsErrorLogJob, queueSize)
	q.opsErrorLogQueueLen.Store(0)
	q.opsErrorLogQueueBytes.Store(0)

	queue := q.opsErrorLogQueue
	q.opsErrorLogWorkersWg.Add(workerCount)
	for i := 0; i < workerCount; i++ {
		go func() {
			defer q.opsErrorLogWorkersWg.Done()
			for {
				job, ok := <-queue
				if !ok {
					return
				}
				q.opsErrorLogQueueLen.Add(-1)
				q.opsErrorLogQueueBytes.Add(-job.queuedBytes)
				batch := make([]opsErrorLogJob, 0, opsErrorLogBatchSize)
				batch = append(batch, job)

				timer := time.NewTimer(opsErrorLogBatchWindow)
			batchLoop:
				for len(batch) < opsErrorLogBatchSize {
					select {
					case nextJob, ok := <-queue:
						if !ok {
							if !timer.Stop() {
								select {
								case <-timer.C:
								default:
								}
							}
							q.flushOpsErrorLogBatch(batch)
							return
						}
						q.opsErrorLogQueueLen.Add(-1)
						q.opsErrorLogQueueBytes.Add(-nextJob.queuedBytes)
						batch = append(batch, nextJob)
					case <-timer.C:
						break batchLoop
					}
				}
				if !timer.Stop() {
					select {
					case <-timer.C:
					default:
					}
				}
				q.flushOpsErrorLogBatch(batch)
			}
		}()
	}
}

type opsErrorLogJob struct {
	ops         *OpsService
	entry       *OpsInsertErrorLogInput
	queuedBytes int64
}

const (
	opsErrorLogTimeout      = 5 * time.Second
	opsErrorLogDrainTimeout = 10 * time.Second
	opsErrorLogBatchWindow  = 200 * time.Millisecond

	opsErrorLogMinWorkerCount = 4
	opsErrorLogMaxWorkerCount = 32

	opsErrorLogQueueSizePerWorker = 128
	opsErrorLogMinQueueSize       = 256
	opsErrorLogMaxQueueSize       = 8192
	opsErrorLogBatchSize          = 32
	opsErrorLogMaxQueueBytes      = 32 * 1024 * 1024
	opsErrorLogMaxUserAgentBytes  = 512
)

func NormalizeOpsPersistentUserAgent(value string) string {
	return normalizeOpsPersistentUserAgent(value)
}
func EstimateOpsErrorLogJobBytes(entry *OpsInsertErrorLogInput) int64 {
	return estimateOpsErrorLogJobBytes(entry)
}
