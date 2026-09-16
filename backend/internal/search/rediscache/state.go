// State 保留搜索计数与代理标记的原 Redis 格式。
package rediscache

import (
	"context"
	"fmt"
	"time"

	"github.com/redis/go-redis/v9"
)

const quotaKeyPrefix = "websearch:quota:"
const proxyUnavailableKey = "websearch:proxy_unavailable:%d"

type State struct{ rdb *redis.Client }

func New(r *redis.Client) *State { return &State{rdb: r} }

// quotaIncrScript atomically increments the counter and sets TTL on first creation.
var quotaIncrScript = redis.NewScript(`
local val = redis.call('INCR', KEYS[1])
if val == 1 then
  redis.call('EXPIRE', KEYS[1], ARGV[1])
else
  local ttl = redis.call('TTL', KEYS[1])
  if ttl == -1 then
    redis.call('EXPIRE', KEYS[1], ARGV[1])
  end
end
return val
`)

func quotaRedisKey(provider string) string { return quotaKeyPrefix + provider }
func (s *State) Increment(ctx context.Context, provider string, ttl time.Duration) (int64, error) {
	return quotaIncrScript.Run(ctx, s.rdb, []string{quotaRedisKey(provider)}, int(ttl.Seconds())).Int64()
}
func (s *State) Decrement(ctx context.Context, provider string) error {
	return s.rdb.Decr(ctx, quotaRedisKey(provider)).Err()
}
func (s *State) Usage(ctx context.Context, provider string) (int64, error) {
	v, e := s.rdb.Get(ctx, quotaRedisKey(provider)).Int64()
	if e == redis.Nil {
		return 0, nil
	}
	return v, e
}
func (s *State) Reset(ctx context.Context, provider string) error {
	return s.rdb.Del(ctx, quotaRedisKey(provider)).Err()
}
func (s *State) MarkProxy(ctx context.Context, id int64, ttl time.Duration) error {
	return s.rdb.Set(ctx, fmt.Sprintf(proxyUnavailableKey, id), "1", ttl).Err()
}
func (s *State) ProxyAvailable(ctx context.Context, id int64) bool {
	v, e := s.rdb.Get(ctx, fmt.Sprintf(proxyUnavailableKey, id)).Result()
	return e != nil || v == ""
}
