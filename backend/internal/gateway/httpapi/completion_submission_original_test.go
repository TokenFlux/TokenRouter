package httpapi

import (
	"context"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/TokenFlux/TokenRouter/internal/infra/telemetry"

	"github.com/TokenFlux/TokenRouter/internal/gateway/completion"
	gatewaytelemetry "github.com/TokenFlux/TokenRouter/internal/gateway/telemetry"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func newUsageRecordTestPool(t *testing.T) *completion.UsageRecordWorkerPool {
	t.Helper()
	pool := completion.NewUsageRecordWorkerPoolWithOptions(completion.UsageRecordWorkerPoolOptions{Observe: gatewaytelemetry.Completion,
		WorkerCount:           1,
		QueueSize:             8,
		TaskTimeout:           time.Second,
		OverflowPolicy:        "drop",
		OverflowSamplePercent: 0,
		AutoScaleEnabled:      false,
	})
	pool.Start()
	t.Cleanup(pool.Stop)
	return pool
}

func newStoppedUsageRecordPoolForTest() *completion.UsageRecordWorkerPool {
	pool := completion.NewUsageRecordWorkerPoolWithOptions(completion.UsageRecordWorkerPoolOptions{Observe: gatewaytelemetry.Completion,
		WorkerCount:    1,
		QueueSize:      1,
		TaskTimeout:    time.Second,
		OverflowPolicy: "sync",
	})
	pool.Start()
	pool.Stop()
	return pool
}

func TestGatewayHandlerSubmitUsageRecordTask_WithPool(t *testing.T) {
	pool := newUsageRecordTestPool(t)
	h := NewCompletionSubmission(pool, false)

	done := make(chan struct{})
	h.Submit(nil, func(ctx context.Context) {
		close(done)
	})

	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("task not executed")
	}
}

func TestGatewayHandlerSubmitUsageRecordTask_WithoutPoolSyncFallback(t *testing.T) {
	h := NewCompletionSubmission(nil, false)
	var called atomic.Bool

	h.Submit(nil, func(ctx context.Context) {
		if _, ok := ctx.Deadline(); !ok {
			t.Fatal("expected deadline in fallback context")
		}
		called.Store(true)
	})

	require.True(t, called.Load())
}

func TestGatewayHandlerSubmitUsageRecordTask_StoppedPoolFallsBackToSync(t *testing.T) {
	h := NewCompletionSubmission(newStoppedUsageRecordPoolForTest(), false)

	var executed atomic.Bool
	h.Submit(nil, func(ctx context.Context) {
		executed.Store(true)
	})
	require.True(t, executed.Load(), "池已停止时计费任务必须内联同步执行")
}

func TestGatewayHandlerSubmitUsageRecordTask_DropPolicyOverflowStillDrops(t *testing.T) {
	pool := completion.NewUsageRecordWorkerPoolWithOptions(completion.UsageRecordWorkerPoolOptions{Observe: gatewaytelemetry.Completion,
		WorkerCount:    1,
		QueueSize:      1,
		TaskTimeout:    time.Minute,
		OverflowPolicy: "drop",
	})
	pool.Start()
	t.Cleanup(pool.Stop)
	h := NewCompletionSubmission(pool, false)

	started := make(chan struct{})
	block := make(chan struct{})
	t.Cleanup(func() { close(block) })
	require.Equal(t, completion.UsageRecordSubmitModeEnqueued, pool.Submit(func(ctx context.Context) {
		close(started)
		<-block
	}))
	<-started
	require.Equal(t, completion.UsageRecordSubmitModeEnqueued, pool.Submit(func(ctx context.Context) {
		<-block
	}))

	var executed atomic.Bool
	h.Submit(nil, func(ctx context.Context) {
		executed.Store(true)
	})
	time.Sleep(50 * time.Millisecond)
	require.False(t, executed.Load(), "drop 溢出策略是运维显式配置，不应被同步兜底覆盖")
}

func TestGatewayHandlerSubmitUsageRecordTask_NilTask(t *testing.T) {
	h := NewCompletionSubmission(nil, false)
	require.NotPanics(t, func() {
		h.Submit(nil, nil)
	})
}

func TestGatewayHandlerSubmitUsageRecordTask_WithoutPool_TaskPanicRecovered(t *testing.T) {
	h := NewCompletionSubmission(nil, false)
	var called atomic.Bool

	require.NotPanics(t, func() {
		h.Submit(nil, func(ctx context.Context) {
			panic("usage task panic")
		})
	})

	h.Submit(nil, func(ctx context.Context) {
		called.Store(true)
	})
	require.True(t, called.Load(), "panic 后后续任务应仍可执行")
}

func TestOpenAIGatewayHandlerSubmitUsageRecordTask_WithPool(t *testing.T) {
	pool := newUsageRecordTestPool(t)
	h := NewCompletionSubmission(pool, true)

	done := make(chan struct{})
	h.Submit(nil, func(ctx context.Context) {
		close(done)
	})

	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("task not executed")
	}
}

func TestOpenAIGatewayHandlerSubmitUsageRecordTask_WithoutPoolSyncFallback(t *testing.T) {
	h := NewCompletionSubmission(nil, true)
	var called atomic.Bool

	h.Submit(nil, func(ctx context.Context) {
		if _, ok := ctx.Deadline(); !ok {
			t.Fatal("expected deadline in fallback context")
		}
		called.Store(true)
	})

	require.True(t, called.Load())
}

func TestOpenAIGatewayHandlerSubmitUsageRecordTask_StoppedPoolFallsBackToSync(t *testing.T) {
	h := NewCompletionSubmission(newStoppedUsageRecordPoolForTest(), true)

	var executed atomic.Bool
	h.Submit(nil, func(ctx context.Context) {
		executed.Store(true)
	})
	require.True(t, executed.Load(), "池已停止时 OpenAI 计费任务必须内联同步执行")
}

func TestOpenAIGatewayHandlerSubmitUsageRecordTask_NilTask(t *testing.T) {
	h := NewCompletionSubmission(nil, true)
	require.NotPanics(t, func() {
		h.Submit(nil, nil)
	})
}

func TestOpenAIGatewayHandlerSubmitUsageRecordTask_WithoutPool_TaskPanicRecovered(t *testing.T) {
	h := NewCompletionSubmission(nil, true)
	var called atomic.Bool

	require.NotPanics(t, func() {
		h.Submit(nil, func(ctx context.Context) {
			panic("usage task panic")
		})
	})

	h.Submit(nil, func(ctx context.Context) {
		called.Store(true)
	})
	require.True(t, called.Load(), "panic 后后续任务应仍可执行")
}

func TestOpenAIGatewayHandlerSubmitMandatoryUsageRecordTask_DroppedTaskSyncFallback(t *testing.T) {
	pool := completion.NewUsageRecordWorkerPoolWithOptions(completion.UsageRecordWorkerPoolOptions{Observe: gatewaytelemetry.Completion,
		WorkerCount:           1,
		QueueSize:             1,
		TaskTimeout:           time.Second,
		OverflowPolicy:        "drop",
		OverflowSamplePercent: 0,
		AutoScaleEnabled:      false,
	})
	pool.Start()
	t.Cleanup(pool.Stop)
	h := NewCompletionSubmission(pool, true)

	block := make(chan struct{})
	release := make(chan struct{})
	pool.Submit(func(ctx context.Context) {
		close(block)
		<-release
	})
	<-block
	pool.Submit(func(ctx context.Context) {})

	var called atomic.Bool
	h.SubmitMandatory(nil, func(ctx context.Context) {
		called.Store(true)
	})
	close(release)

	require.True(t, called.Load(), "mandatory usage task must run synchronously when async submit is dropped")
}

func TestOpenAIGatewayHandlerSubmitOpenAIUsageRecordTask_ImageResultUsesMandatoryFallback(t *testing.T) {
	pool := completion.NewUsageRecordWorkerPoolWithOptions(completion.UsageRecordWorkerPoolOptions{Observe: gatewaytelemetry.Completion,
		WorkerCount:           1,
		QueueSize:             1,
		TaskTimeout:           time.Second,
		OverflowPolicy:        "drop",
		OverflowSamplePercent: 0,
		AutoScaleEnabled:      false,
	})
	pool.Start()
	t.Cleanup(pool.Stop)
	h := NewCompletionSubmission(pool, true)

	block := make(chan struct{})
	release := make(chan struct{})
	pool.Submit(func(ctx context.Context) {
		close(block)
		<-release
	})
	<-block
	pool.Submit(func(ctx context.Context) {})

	var called atomic.Bool
	h.SubmitImages(nil, 1, func(ctx context.Context) {
		called.Store(true)
	})
	close(release)

	require.True(t, called.Load(), "image usage task must be mandatory when async submit is dropped")
}

func TestOpenAIGatewayHandlerSubmitUsageRecordTask_PreservesRequestIDs(t *testing.T) {

	pool := newUsageRecordTestPool(t)
	h := NewCompletionSubmission(pool, true)

	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	req := httptest.NewRequest("POST", "/v1/chat/completions", nil)
	ctx := context.WithValue(req.Context(), telemetry.RequestID, "req-123")
	ctx = context.WithValue(ctx, telemetry.ClientRequestID, "client-456")
	c.Request = req.WithContext(ctx)

	got := make(chan [2]string, 1)
	h.Submit(c, func(ctx context.Context) {
		requestID, _ := ctx.Value(telemetry.RequestID).(string)
		clientRequestID, _ := ctx.Value(telemetry.ClientRequestID).(string)
		got <- [2]string{requestID, clientRequestID}
	})

	select {
	case ids := <-got:
		require.Equal(t, [2]string{"req-123", "client-456"}, ids)
	case <-time.After(time.Second):
		t.Fatal("task not executed")
	}
}

func TestGatewayHandlerSubmitUsageRecordTask_PreservesRequestIDs(t *testing.T) {

	pool := newUsageRecordTestPool(t)
	h := NewCompletionSubmission(pool, false)

	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	req := httptest.NewRequest("POST", "/v1/messages", nil)
	ctx := context.WithValue(req.Context(), telemetry.RequestID, "req-gateway")
	ctx = context.WithValue(ctx, telemetry.ClientRequestID, "client-gateway")
	c.Request = req.WithContext(ctx)

	got := make(chan [2]string, 1)
	h.Submit(c, func(ctx context.Context) {
		requestID, _ := ctx.Value(telemetry.RequestID).(string)
		clientRequestID, _ := ctx.Value(telemetry.ClientRequestID).(string)
		got <- [2]string{requestID, clientRequestID}
	})

	select {
	case ids := <-got:
		require.Equal(t, [2]string{"req-gateway", "client-gateway"}, ids)
	case <-time.After(time.Second):
		t.Fatal("task not executed")
	}
}
