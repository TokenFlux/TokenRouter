package account

import (
	"context"
	"errors"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

type quotaLifecycleClient struct {
	entered chan struct{}
	release chan struct{}
}

func (c *quotaLifecycleClient) GetJSON(ctx context.Context, _ string, _ map[string]string) (map[string]any, error) {
	close(c.entered)
	if c.release != nil {
		<-c.release
	} else {
		<-ctx.Done()
	}
	return nil, ctx.Err()
}
func (*quotaLifecycleClient) GetJSONRaw(context.Context, string, map[string]string) ([]byte, error) {
	return nil, errors.New("unexpected credit detail request")
}
func (*quotaLifecycleClient) PostJSON(context.Context, string, map[string]any) (map[string]any, error) {
	return nil, errors.New("unexpected quota reset")
}

// 停止须取消并等待实际请求；构造不读取账号，关闭后也不能重新认领。
func TestOpenAIQuotaLifecycleCancelsAndWaits(t *testing.T) {
	client := &quotaLifecycleClient{entered: make(chan struct{})}
	var reads atomic.Int32
	service := NewOpenAIQuotaService(OpenAIQuotaOptions{

		Configured: func() bool { return true },

		Read: func(context.Context, int64) (*Record, error) {
			reads.Add(1)
			return &Record{
				ID:          1,
				Platform:    "openai",
				Type:        "oauth",
				Credentials: map[string]any{"chatgpt_account_id": "fixture-account", "access_token": "fixture-token"},
			}, nil
		},

		Client: func(context.Context, *Record, string) (OpenAIQuotaClient, error) { return client, nil },
	})
	require.Zero(t, reads.Load())
	finished := make(chan error, 1)
	go func() { _, err := service.QueryUsage(context.Background(), 1); finished <- err }()
	<-client.entered
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	require.NoError(t, service.StopContext(ctx))
	require.ErrorIs(t, <-finished, context.Canceled)
	require.NoError(t, service.StopContext(ctx))
	_, err := service.QueryUsage(context.Background(), 1)
	require.ErrorIs(t, err, ErrOpenAIQuotaStopped)
	require.EqualValues(t, 1, reads.Load())
}

// 不配合取消的外部端口仍受停止等待预算约束，重复停止保留同一次未完成结果。
func TestOpenAIQuotaLifecycleReportsUnfinishedRequest(t *testing.T) {
	client := &quotaLifecycleClient{entered: make(chan struct{}), release: make(chan struct{})}
	service := NewOpenAIQuotaService(OpenAIQuotaOptions{

		Configured: func() bool { return true },

		Read: func(context.Context, int64) (*Record, error) {
			return &Record{
				ID:          1,
				Platform:    "openai",
				Type:        "oauth",
				Credentials: map[string]any{"chatgpt_account_id": "fixture-account", "access_token": "fixture-token"},
			}, nil
		},

		Client: func(context.Context, *Record, string) (OpenAIQuotaClient, error) { return client, nil },
	})
	finished := make(chan error, 1)
	go func() { _, err := service.QueryUsage(context.Background(), 1); finished <- err }()
	<-client.entered
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	err := service.StopContext(ctx)
	require.ErrorIs(t, err, context.DeadlineExceeded)
	require.True(t, strings.Contains(err.Error(), "OpenAIQuotaService"))
	close(client.release)
	require.ErrorIs(t, <-finished, context.Canceled)
	require.Equal(t, err, service.StopContext(context.Background()))
}
