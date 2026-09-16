package app

import (
	"context"
	"sync/atomic"
	"testing"
	"time"

	"github.com/TokenFlux/TokenRouter/internal/app/lifecycle"
	"github.com/stretchr/testify/require"
)

// 同一应用拥有者覆盖 HTTP 请求和嵌套上游尝试；任一未结束时不得停止完成队列或共享存储。
func TestGatewayRequestActivityTimeoutKeepsCompletionAndStorageOpen(t *testing.T) {
	manager := lifecycle.New()
	activity := provideGatewayRequestActivity(manager)
	requestDone, err := activity.Enter()
	require.NoError(t, err)
	defer requestDone()
	attemptDone, err := activity.Enter()
	require.NoError(t, err)
	defer attemptDone()
	var completionStopped, redisClosed atomic.Bool
	manager.Register(lifecycle.Hook{Name: "completion", StopOrder: 40, Stop: func(context.Context) error { completionStopped.Store(true); return nil }})
	manager.Register(lifecycle.Hook{Name: "redis", StopOrder: 900, Stop: func(context.Context) error { redisClosed.Store(true); return nil }})
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	err = manager.Stop(ctx)
	require.ErrorIs(t, err, context.DeadlineExceeded)
	// Manager 报告未完成阶段；拥有者自己的停止结果保留具体活动数量。
	require.ErrorContains(t, err, "GatewayRequestsAndAttempts")
	detailCtx, detailCancel := context.WithTimeout(context.Background(), time.Second)
	defer detailCancel()
	require.ErrorContains(t, activity.StopContext(detailCtx), "2 operations unfinished")
	require.False(t, completionStopped.Load())
	require.False(t, redisClosed.Load())
	_, err = activity.Enter()
	require.Error(t, err)
	attemptDone()
	requestDone()
	require.ErrorIs(t, manager.Stop(context.Background()), context.DeadlineExceeded)
}
func TestGatewayRequestActivityFinishesBeforeCompletionAndStorage(t *testing.T) {
	manager := lifecycle.New()
	activity := provideGatewayRequestActivity(manager)
	done, err := activity.Enter()
	require.NoError(t, err)
	var order []string
	manager.Register(lifecycle.Hook{Name: "completion", StopOrder: 40, Stop: func(context.Context) error { order = append(order, "completion"); return nil }})
	manager.Register(lifecycle.Hook{Name: "redis", StopOrder: 900, Stop: func(context.Context) error { order = append(order, "redis"); return nil }})
	done()
	require.NoError(t, manager.Stop(context.Background()))
	require.Equal(t, []string{"completion", "redis"}, order)
}
