package repository

import (
	"context"
	"time"

	"github.com/BrandonVee/TokenRouter/internal/service"
	"github.com/redis/go-redis/v9"
)

const leaderLockKeyPrefix = "leader:lock:"

// leaderLockReleaseScript 只释放仍由当前 owner 持有的锁。
// 这样可以避免旧持有者的延迟释放误删已经被其他实例重新获取的锁。
var leaderLockReleaseScript = redis.NewScript(`
if redis.call("GET", KEYS[1]) == ARGV[1] then
  return redis.call("DEL", KEYS[1])
end
return 0
`)

type leaderLockCache struct {
	rdb *redis.Client
}

// NewLeaderLockCache 创建缓存版周期任务主实例锁。
func NewLeaderLockCache(rdb *redis.Client) service.LeaderLockCache {
	return &leaderLockCache{rdb: rdb}
}

func (c *leaderLockCache) TryAcquireLeaderLock(ctx context.Context, key, owner string, ttl time.Duration) (bool, error) {
	return c.rdb.SetNX(ctx, leaderLockKeyPrefix+key, owner, ttl).Result()
}

func (c *leaderLockCache) ReleaseLeaderLock(ctx context.Context, key, owner string) error {
	return leaderLockReleaseScript.Run(ctx, c.rdb, []string{leaderLockKeyPrefix + key}, owner).Err()
}
