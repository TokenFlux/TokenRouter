package testutil

import (
	"testing"

	gatewayredis "github.com/TokenFlux/TokenRouter/internal/gateway/rediscache"
	"github.com/TokenFlux/TokenRouter/internal/gateway/session"
	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"
)

// NewRedisGatewayCache 返回基于真实 Redis 的网关缓存，供测试使用。
func NewRedisGatewayCache(t *testing.T) session.GatewayCache {
	t.Helper()

	redisServer := miniredis.RunT(t)
	redisClient := redis.NewClient(&redis.Options{Addr: redisServer.Addr()})
	t.Cleanup(func() { _ = redisClient.Close() })

	return gatewayredis.NewGatewayCache(redisClient)
}
