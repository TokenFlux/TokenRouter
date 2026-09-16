package rediscache

import (
	"context"
	"testing"
	"time"

	"github.com/TokenFlux/TokenRouter/internal/gateway/session"
	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/require"
)

func TestGatewayCacheReasoningContentRoundTrip(t *testing.T) {
	redisServer := miniredis.RunT(t)
	client := redis.NewClient(&redis.Options{Addr: redisServer.Addr()})
	cache, ok := NewGatewayCache(client).(session.ReasoningContentCache)
	require.True(t, ok)

	ctx := context.Background()
	require.NoError(t, cache.SetReasoningContent(ctx, "item_reasoning", "cached thought", time.Hour))
	value, err := cache.GetReasoningContent(ctx, "item_reasoning")
	require.NoError(t, err)
	require.Equal(t, "cached thought", value)

	_, err = cache.GetReasoningContent(ctx, "missing")
	require.ErrorIs(t, err, session.ErrReasoningContentNotFound)
	_, err = cache.GetReasoningContent(ctx, "")
	require.ErrorIs(t, err, session.ErrReasoningContentNotFound)
}
