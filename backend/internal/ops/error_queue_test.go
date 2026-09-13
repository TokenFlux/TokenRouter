package ops

import (
	"context"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/stretchr/testify/require"
)

var testErrorQueue = NewErrorLogQueue(ErrorLogQueueOptions{})

func TestOpsErrorLogQueueByteBudget(t *testing.T) {
	previousBytes := testErrorQueue.opsErrorLogQueueBytes.Load()
	previousLen := testErrorQueue.opsErrorLogQueueLen.Load()
	testErrorQueue.opsErrorLogQueueBytes.Store(0)
	testErrorQueue.opsErrorLogQueueLen.Store(0)
	t.Cleanup(func() {
		testErrorQueue.opsErrorLogQueueBytes.Store(previousBytes)
		testErrorQueue.opsErrorLogQueueLen.Store(previousLen)
	})

	if !testErrorQueue.reserveOpsErrorLogQueueBytes(opsErrorLogMaxQueueBytes - 1) {
		t.Fatal("first reservation within byte budget should succeed")
	}
	if testErrorQueue.reserveOpsErrorLogQueueBytes(2) {
		t.Fatal("reservation beyond byte budget should be rejected")
	}
	if got := testErrorQueue.OpsErrorLogQueueBytes(); got != opsErrorLogMaxQueueBytes-1 {
		t.Fatalf("queued bytes = %d, want %d", got, opsErrorLogMaxQueueBytes-1)
	}
	if got := testErrorQueue.OpsErrorLogQueueLength(); got != 1 {
		t.Fatalf("queue length = %d, want 1", got)
	}
}
func TestEstimateOpsErrorLogJobBytesIncludesVariablePayloads(t *testing.T) {
	base := estimateOpsErrorLogJobBytes(&OpsInsertErrorLogInput{})
	message := "upstream message"
	detail := "upstream detail"
	events := `[{"error":"x"}]`
	entry := &OpsInsertErrorLogInput{
		ErrorBody:            strings.Repeat("x", 1024),
		ErrorMessage:         "client error",
		UserAgent:            "test-agent",
		UpstreamErrorMessage: &message,
		UpstreamErrorDetail:  &detail,
		UpstreamErrorsJSON:   &events,
	}
	if got := estimateOpsErrorLogJobBytes(entry); got <= base+1024 {
		t.Fatalf("estimated bytes = %d, expected variable payloads above %d", got, base+1024)
	}
}
func TestEnqueueOpsErrorLog_QueueFullDrop(t *testing.T) {
	resetOpsErrorLoggerStateForTest(t)

	// 禁止 enqueueOpsErrorLog 触发 workers，使用测试队列验证满队列降级。
	testErrorQueue.opsErrorLogOnce.Do(func() {})

	testErrorQueue.opsErrorLogMu.Lock()
	testErrorQueue.opsErrorLogQueue = make(chan opsErrorLogJob, 1)
	testErrorQueue.opsErrorLogMu.Unlock()

	ops := newLegacyShapeOpsService(nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil)
	entry := &OpsInsertErrorLogInput{ErrorPhase: "upstream", ErrorType: "upstream_error"}

	testErrorQueue.Enqueue(ops, entry)
	testErrorQueue.Enqueue(ops, entry)

	require.Equal(t, int64(1), testErrorQueue.OpsErrorLogEnqueuedTotal())
	require.Equal(t, int64(1), testErrorQueue.OpsErrorLogDroppedTotal())
	require.Equal(t, int64(1), testErrorQueue.OpsErrorLogQueueLength())
}
func TestEnqueueOpsErrorLog_EarlyReturnBranches(t *testing.T) {
	resetOpsErrorLoggerStateForTest(t)

	ops := newLegacyShapeOpsService(nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil)
	entry := &OpsInsertErrorLogInput{ErrorPhase: "upstream", ErrorType: "upstream_error"}

	// nil 入参分支
	testErrorQueue.Enqueue(nil, entry)
	testErrorQueue.Enqueue(ops, nil)
	require.Equal(t, int64(0), testErrorQueue.OpsErrorLogEnqueuedTotal())

	// shutdown 分支
	testErrorQueue.opsErrorLogShutdownOnce.Do(func() { close(testErrorQueue.opsErrorLogShutdownCh) })
	testErrorQueue.Enqueue(ops, entry)
	require.Equal(t, int64(0), testErrorQueue.OpsErrorLogEnqueuedTotal())

	// stopping 分支
	resetOpsErrorLoggerStateForTest(t)
	testErrorQueue.opsErrorLogMu.Lock()
	testErrorQueue.opsErrorLogStopping = true
	testErrorQueue.opsErrorLogMu.Unlock()
	testErrorQueue.Enqueue(ops, entry)
	require.Equal(t, int64(0), testErrorQueue.OpsErrorLogEnqueuedTotal())

	// queue nil 分支（防止启动 worker 干扰）
	resetOpsErrorLoggerStateForTest(t)
	testErrorQueue.opsErrorLogOnce.Do(func() {})
	testErrorQueue.opsErrorLogMu.Lock()
	testErrorQueue.opsErrorLogQueue = nil
	testErrorQueue.opsErrorLogMu.Unlock()
	testErrorQueue.Enqueue(ops, entry)
	require.Equal(t, int64(0), testErrorQueue.OpsErrorLogEnqueuedTotal())
}
func TestEnqueueOpsErrorLog_SanitizesAndBoundsBodyBeforeQueue(t *testing.T) {
	setupOpsErrorLogTestQueue(t, 1)
	ops := newLegacyShapeOpsService(nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil)
	secret := strings.Repeat("s", OpsErrorLogQueueBodyMaxBytes)
	entry := &OpsInsertErrorLogInput{
		ErrorPhase: "request",
		ErrorType:  "api_error",
		ErrorBody:  `{"authorization":"Bearer ` + secret + `","message":"failed"}`,
	}

	testErrorQueue.Enqueue(ops, entry)
	job := <-testErrorQueue.opsErrorLogQueue
	require.LessOrEqual(t, len(job.entry.ErrorBody), OpsErrorLogQueueBodyMaxBytes)
	require.NotContains(t, job.entry.ErrorBody, secret)
	require.Equal(t, int64(1), testErrorQueue.OpsErrorLogSanitizedTotal())
}
func TestNormalizeOpsPersistentUserAgentBoundsAndPreservesUTF8(t *testing.T) {
	value := strings.Repeat("a", opsErrorLogMaxUserAgentBytes-1) + "你" + strings.Repeat("b", 32)
	got := normalizeOpsPersistentUserAgent("  " + value + "  ")
	require.LessOrEqual(t, len(got), opsErrorLogMaxUserAgentBytes)
	require.True(t, utf8.ValidString(got))
	require.NotContains(t, got, "b")
}

// 关闭清空全局队列引用后，worker 仍须继续消费自己已经取得的队列。
func TestOpsErrorLogShutdownDrainsCapturedQueue(t *testing.T) {
	resetOpsErrorLoggerStateForTest(t)
	t.Cleanup(func() { resetOpsErrorLoggerStateForTest(t) })
	testErrorQueue.opsErrorLogOnce.Do(testErrorQueue.startOpsErrorLogWorkers)
	testErrorQueue.opsErrorLogMu.RLock()
	queue := testErrorQueue.opsErrorLogQueue
	for range 64 {
		queue <- opsErrorLogJob{}
	}
	testErrorQueue.opsErrorLogMu.RUnlock()
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	require.NoError(t, testErrorQueue.Shutdown(ctx))
	require.NoError(t, testErrorQueue.Shutdown(ctx))
	require.Zero(t, testErrorQueue.OpsErrorLogQueueLength())
	require.True(t, testErrorQueue.opsErrorLogDrained.Load())
}

func resetOpsErrorLoggerStateForTest(t *testing.T) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	require.NoError(t, testErrorQueue.Shutdown(ctx))
	testErrorQueue = NewErrorLogQueue(ErrorLogQueueOptions{})
}
func setupOpsErrorLogTestQueue(t *testing.T, size int) {
	resetOpsErrorLoggerStateForTest(t)
	testErrorQueue.opsErrorLogOnce.Do(func() {})
	testErrorQueue.opsErrorLogQueue = make(chan opsErrorLogJob, size)
}

// 构造与停止不启动懒队列，停止后的提交也不能重新开启 worker。
func TestS08ErrorQueueConstructAndStopDoNotStartWorkers(t *testing.T) {
	starts := 0
	q := NewErrorLogQueue(ErrorLogQueueOptions{Processors: func() int { starts++; return 2 }})
	require.Zero(t, starts)
	require.Zero(t, q.Health().Capacity)
	require.NoError(t, q.Shutdown(context.Background()))
	q.Enqueue(NewOpsService(nil, nil, nil, nil, nil, nil, nil, nil), &OpsInsertErrorLogInput{ErrorMessage: "after stop"})
	require.Zero(t, starts)
	require.Zero(t, q.Health().Enqueued)
}
