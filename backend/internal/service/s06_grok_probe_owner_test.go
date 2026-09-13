package service

import (
	"context"
	"github.com/stretchr/testify/require"
	"testing"
	"time"
)

// 调用方取消后，后台共享探测仍须有可等待的停止拥有者。
func TestGrokProbeDetachedWorkHasStopOwner(t *testing.T) {
	svc := &GrokQuotaService{}
	entered, exited, release := make(chan struct{}), make(chan struct{}), make(chan struct{})
	done := make(chan error, 1)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() {
		_, err := svc.runProbeFlight(ctx, "local-owner", func(shared context.Context) (*GrokQuotaProbeResult, error) {
			close(entered)
			defer close(exited)
			select {
			case <-shared.Done():
				return nil, shared.Err()
			case <-release:
				return &GrokQuotaProbeResult{Source: "local"}, nil
			}
		})
		done <- err
	}()
	t.Cleanup(func() {
		close(release)
		select {
		case <-exited:
		case <-time.After(time.Second):
			t.Error("共享探测未结束")
		}
	})
	<-entered
	cancel()
	require.ErrorIs(t, <-done, context.Canceled)
	select {
	case <-exited:
		t.Fatal("调用方取消不应结束共享探测")
	default:
	}
	if owner, ok := any(svc).(interface{ StopContext(context.Context) error }); ok {
		budget, stopCancel := context.WithTimeout(context.Background(), time.Second)
		defer stopCancel()
		require.NoError(t, owner.StopContext(budget))
	}
	select {
	case <-exited:
	default:
		t.Error("共享探测没有可停止的拥有者")
	}
}
