// 本文件验证同步平台执行与依赖资源的停止顺序。
package lifecycle

import (
	"context"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestOperationsWaitsBeforeClosingDependencies(t *testing.T) {
	operations := NewOperations("fixture")
	finish, err := operations.Enter()
	require.NoError(t, err)
	manager := New()
	var closed atomic.Bool
	stopping := make(chan struct{})
	manager.Register(Hook{Name: "platform", StopOrder: 15, Stop: func(ctx context.Context) error { close(stopping); return operations.StopContext(ctx) }})
	manager.Register(Hook{Name: "redis", StopOrder: 900, Stop: func(context.Context) error { closed.Store(true); return nil }})
	done := make(chan error, 1)
	go func() { done <- manager.Stop(context.Background()) }()
	<-stopping
	require.False(t, closed.Load())
	finish()
	finish()
	require.NoError(t, <-done)
	require.True(t, closed.Load())
	_, err = operations.Enter()
	require.ErrorContains(t, err, "stopped")
	require.NoError(t, operations.StopContext(context.Background()))
}
func TestOperationsTimeoutDoesNotCloseDependencies(t *testing.T) {
	operations := NewOperations("QoderRequestsAndAttempts")
	finish, err := operations.Enter()
	require.NoError(t, err)
	defer finish()
	manager := New()
	var closed atomic.Bool
	manager.Register(Hook{Name: "QoderRequestsAndAttempts", StopOrder: 15, Stop: operations.StopContext})
	manager.Register(Hook{Name: "redis", StopOrder: 900, Stop: func(context.Context) error { closed.Store(true); return nil }})
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Millisecond)
	defer cancel()
	err = manager.Stop(ctx)
	require.ErrorIs(t, err, context.DeadlineExceeded)
	require.Contains(t, err.Error(), "QoderRequestsAndAttempts")
	require.False(t, closed.Load())
	finish()
	require.ErrorIs(t, operations.StopContext(context.Background()), context.DeadlineExceeded)
}
