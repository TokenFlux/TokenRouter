// Dashboard 缓存保留原 key、TTL 和故障语义，参数由 app 投影。
package rediscache

import (
	"context"
	"strings"
	"time"

	"github.com/TokenFlux/TokenRouter/internal/usage"
	"github.com/redis/go-redis/v9"
)

const dashboardStatsCacheKey = "dashboard:stats:v1"

type DashboardCache struct {
	rdb       *redis.Client
	keyPrefix string
}

func NewDashboardCache(rdb *redis.Client, keyPrefix string) *DashboardCache {
	prefix := strings.TrimSpace(keyPrefix)
	if prefix != "" && !strings.HasSuffix(prefix, ":") {
		prefix += ":"
	}
	return &DashboardCache{
		rdb:       rdb,
		keyPrefix: prefix,
	}
}

func (c *DashboardCache) GetDashboardStats(ctx context.Context) (string, error) {
	val, err := c.rdb.Get(ctx, c.buildKey()).Result()
	if err != nil {
		if err == redis.Nil {
			return "", usage.ErrDashboardStatsCacheMiss
		}
		return "", err
	}
	return val, nil
}

func (c *DashboardCache) SetDashboardStats(ctx context.Context, data string, ttl time.Duration) error {
	return c.rdb.Set(ctx, c.buildKey(), data, ttl).Err()
}

func (c *DashboardCache) buildKey() string {
	if c.keyPrefix == "" {
		return dashboardStatsCacheKey
	}
	return c.keyPrefix + dashboardStatsCacheKey
}

func (c *DashboardCache) DeleteDashboardStats(ctx context.Context) error {
	return c.rdb.Del(ctx, c.buildKey()).Err()
}
