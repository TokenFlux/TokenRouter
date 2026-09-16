package completion

import (
	"context"
	"testing"

	"github.com/TokenFlux/TokenRouter/internal/gateway/modeltrace"
	"github.com/TokenFlux/TokenRouter/internal/infra/telemetry"
	"github.com/stretchr/testify/require"
)

// 交接后继续推进 turn，已排队任务只能看到提交时的模型链。
func TestCompletionContextSnapshotsModelTraceAndKeepsWorkerBudget(t *testing.T) {
	trace := modeltrace.NewAPIKeyModelRedirectTrace("client", "source", "first")
	source, cancelSource := context.WithCancel(modeltrace.WithContext(context.WithValue(context.Background(), telemetry.RequestID, "request-id"), trace))
	task := WrapTaskContext(source, func(worker context.Context) {
		require.ErrorIs(t, worker.Err(), context.Canceled)
		require.Equal(t, "request-id", worker.Value(telemetry.RequestID))
		value, ok := modeltrace.FromContext(worker)
		require.True(t, ok)
		require.NotSame(t, trace, value)
		require.Equal(t, []string{"first"}, value.ResponseModels())
	})
	trace.RegisterModel("next-turn")
	cancelSource()
	snapshot := SnapshotContext(source)
	require.NoError(t, snapshot.Err())
	worker, cancelWorker := context.WithCancel(context.Background())
	cancelWorker()
	task(worker)
}
