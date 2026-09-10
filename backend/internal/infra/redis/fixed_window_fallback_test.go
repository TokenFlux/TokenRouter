package redis

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/require"
)

// pttlFailureHook 只拒绝独立的 PTTL 查询；Lua 内的原子计数和 TTL 操作仍真实执行。
type pttlFailureHook struct{}

func (pttlFailureHook) DialHook(next redis.DialHook) redis.DialHook {
	return next
}
func (pttlFailureHook) ProcessPipelineHook(next redis.ProcessPipelineHook) redis.ProcessPipelineHook {
	return next
}
func (pttlFailureHook) ProcessHook(next redis.ProcessHook) redis.ProcessHook {
	return func(ctx context.Context, cmd redis.Cmder) error {
		if cmd.Name() == "pttl" {
			return errors.New("PTTL unavailable")
		}
		return next(ctx, cmd)
	}
}

// TestFixedWindowKeyAndRetryAfterFallback 保留旧 Allow 场景，并通过命令级故障验证剩余时间回退。
func TestFixedWindowKeyAndRetryAfterFallback(t *testing.T) {
	server := miniredis.RunT(t)
	client := redis.NewClient(&redis.Options{Addr: server.Addr()})
	t.Cleanup(func() {
		_ = client.Close()
	})
	client.AddHook(pttlFailureHook{})
	limiter := NewFixedWindowLimiter(client, "rate_limit:")
	ctx := t.Context()
	allowed, count, retry, err := limiter.Allow(ctx, "panel:global:user:42", 1, time.Minute)
	require.NoError(t, err)
	require.True(t, allowed)
	require.Equal(t, int64(1), count)
	require.Zero(t, retry)
	value, err := server.Get("rate_limit:panel:global:user:42")
	require.NoError(t, err)
	require.Equal(t, "1", value)
	allowed, count, retry, err = limiter.Allow(ctx, "panel:global:user:42", 1, time.Minute)
	require.NoError(t, err)
	require.False(t, allowed)
	require.Equal(t, int64(2), count)
	require.Equal(t, time.Minute, retry)
}
