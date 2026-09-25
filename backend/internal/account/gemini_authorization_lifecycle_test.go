// 验证授权会话清理任务的停止等待。
package account

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	"github.com/TokenFlux/TokenRouter/internal/protocol/google"
	"github.com/stretchr/testify/require"
)

type stoppingGeminiClient struct {
	entered chan struct{}
	calls   atomic.Int32
}

func (c *stoppingGeminiClient) ExchangeCode(context.Context, string, string, string, string, string) (*google.TokenResponse, error) {
	return nil, errors.New("not used")
}
func (c *stoppingGeminiClient) RefreshToken(ctx context.Context, _ string, _ string, _ string) (*google.TokenResponse, error) {
	if c.calls.Add(1) == 1 {
		close(c.entered)
	}
	<-ctx.Done()
	return nil, ctx.Err()
}

func TestGeminiAuthorizationStopBoundsInFlightRefresh(t *testing.T) {
	client := &stoppingGeminiClient{entered: make(chan struct{})}
	authorization := NewGeminiAuthorization(client, nil, nil, GeminiAuthorizationOptions{})
	require.False(t, authorization.Store.runtimeStarted, "构造不启动后台清理")
	authorization.Start()
	authorization.Start()
	done := make(chan error, 1)
	go func() {
		_, err := authorization.RefreshToken(context.Background(), "code_assist", "fixture-token", "")
		done <- err
	}()
	select {
	case <-client.entered:
	case <-time.After(time.Second):
		t.Fatal("刷新未进入")
	}
	budget, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	start := time.Now()
	err := authorization.StopContext(budget)
	require.ErrorIs(t, err, context.DeadlineExceeded)
	require.Less(t, time.Since(start), time.Second)
	_, err = authorization.RefreshToken(context.Background(), "code_assist", "fixture-token", "")
	require.ErrorContains(t, err, "stopped")
	select {
	case <-done:
	case <-time.After(10 * time.Second):
		t.Fatal("原有有限刷新流程未结束")
	}
	// 等待超时结果固定，不能在流程稍后结束时把第一次关闭改称成功。
	require.ErrorIs(t, authorization.StopContext(context.Background()), context.DeadlineExceeded)
	require.EqualValues(t, 4, client.calls.Load(), "本次未改变已进入流程的原重试次数")
}
