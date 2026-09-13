package rediscache

import (
	"context"
	"time"

	"github.com/TokenFlux/TokenRouter/internal/ops"
	"github.com/redis/go-redis/v9"
)

// Runtime 保留各个调用者原有键，不追加技术层前缀。
type Runtime struct{ client *redis.Client }

func NewRuntime(c *redis.Client) ops.RuntimeCache {
	if c == nil {
		return nil
	}
	return &Runtime{c}
}

var releaseOwner = redis.NewScript(`if redis.call("GET", KEYS[1]) == ARGV[1] then return redis.call("DEL", KEYS[1]) end return 0`)

func (r *Runtime) Claim(ctx context.Context, key, owner string, ttl time.Duration) (bool, error) {
	return r.client.SetNX(ctx, key, owner, ttl).Result()
}
func (r *Runtime) Release(ctx context.Context, key, owner string) error {
	return releaseOwner.Run(ctx, r.client, []string{key}, owner).Err()
}
func (r *Runtime) Get(ctx context.Context, key string) (string, error) {
	return r.client.Get(ctx, key).Result()
}
func (r *Runtime) Put(ctx context.Context, key, value string, ttl time.Duration) error {
	return r.client.Set(ctx, key, value, ttl).Err()
}
