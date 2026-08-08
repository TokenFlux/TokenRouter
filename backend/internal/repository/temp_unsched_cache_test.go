package repository

import (
	"context"
	"testing"
	"time"

	"github.com/TokenFlux/TokenRouter/internal/service"
	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/require"
)

func newTempUnschedCacheTest(t *testing.T) (service.TempUnschedCache, *redis.Client) {
	t.Helper()

	// 每个测试使用独立 Redis 实例，避免账号键和 TTL 相互影响。
	server := miniredis.RunT(t)
	client := redis.NewClient(&redis.Options{Addr: server.Addr()})
	t.Cleanup(func() {
		_ = client.Close()
	})

	return NewTempUnschedCache(client), client
}

func TestTempUnschedCacheShorterStateDoesNotReplaceLongerState(t *testing.T) {
	cache, _ := newTempUnschedCacheTest(t)
	ctx := context.Background()
	now := time.Now().Unix()
	longer := &service.TempUnschedState{
		UntilUnix:       now + 300,
		TriggeredAtUnix: now,
		StatusCode:      429,
		MatchedKeyword:  "rate limit",
		RuleIndex:       1,
		ErrorMessage:    "longer state",
	}
	shorter := &service.TempUnschedState{
		UntilUnix:       now + 120,
		TriggeredAtUnix: now + 1,
		StatusCode:      503,
		MatchedKeyword:  "overloaded",
		RuleIndex:       2,
		ErrorMessage:    "shorter state",
	}

	require.NoError(t, cache.SetTempUnsched(ctx, 101, longer))
	require.NoError(t, cache.SetTempUnsched(ctx, 101, shorter))

	got, err := cache.GetTempUnsched(ctx, 101)
	require.NoError(t, err)
	require.Equal(t, longer, got)
}

func TestTempUnschedCacheLongerStateReplacesShorterState(t *testing.T) {
	cache, _ := newTempUnschedCacheTest(t)
	ctx := context.Background()
	now := time.Now().Unix()
	shorter := &service.TempUnschedState{
		UntilUnix:       now + 120,
		TriggeredAtUnix: now,
		StatusCode:      429,
		ErrorMessage:    "shorter state",
	}
	longer := &service.TempUnschedState{
		UntilUnix:       now + 300,
		TriggeredAtUnix: now + 1,
		StatusCode:      503,
		ErrorMessage:    "longer state",
	}

	require.NoError(t, cache.SetTempUnsched(ctx, 102, shorter))
	require.NoError(t, cache.SetTempUnsched(ctx, 102, longer))

	got, err := cache.GetTempUnsched(ctx, 102)
	require.NoError(t, err)
	require.Equal(t, longer, got)
}

func TestTempUnschedCacheExpiredStateIsNotStored(t *testing.T) {
	cache, _ := newTempUnschedCacheTest(t)
	ctx := context.Background()
	expired := &service.TempUnschedState{
		UntilUnix:       time.Now().Add(-time.Minute).Unix(),
		TriggeredAtUnix: time.Now().Add(-2 * time.Minute).Unix(),
		StatusCode:      429,
		ErrorMessage:    "expired state",
	}

	require.NoError(t, cache.SetTempUnsched(ctx, 103, expired))

	got, err := cache.GetTempUnsched(ctx, 103)
	require.NoError(t, err)
	require.Nil(t, got)
}

func TestTempUnschedCacheMalformedStateReturnsClearError(t *testing.T) {
	cache, client := newTempUnschedCacheTest(t)
	ctx := context.Background()
	require.NoError(t, client.Set(ctx, tempUnschedPrefix+"104", "{invalid-json", time.Minute).Err())

	got, err := cache.GetTempUnsched(ctx, 104)

	require.Nil(t, got)
	require.ErrorContains(t, err, "unmarshal state")
}

func TestTempUnschedCacheDeleteIsIdempotent(t *testing.T) {
	cache, _ := newTempUnschedCacheTest(t)
	ctx := context.Background()
	state := &service.TempUnschedState{
		UntilUnix:       time.Now().Add(5 * time.Minute).Unix(),
		TriggeredAtUnix: time.Now().Unix(),
		StatusCode:      429,
		ErrorMessage:    "temporary state",
	}

	require.NoError(t, cache.SetTempUnsched(ctx, 105, state))
	require.NoError(t, cache.DeleteTempUnsched(ctx, 105))
	require.NoError(t, cache.DeleteTempUnsched(ctx, 105))

	got, err := cache.GetTempUnsched(ctx, 105)
	require.NoError(t, err)
	require.Nil(t, got)
}
