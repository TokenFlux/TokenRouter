package account

import (
	"context"
	"time"
)

// AcquireSingletonLease 保留 Redis 竞争跳过、故障回退数据库和无后端执行的原规则。
func AcquireSingletonLease(ctx context.Context, leader CNMonitorLeader, advisory func(context.Context, string) (func(), bool), key, owner string, ttl time.Duration) (func(), bool) {
	if ctx == nil {
		ctx = context.Background()
	}
	if leader != nil {
		ok, err := leader.TryAcquireLeaderLock(ctx, key, owner, ttl)
		if err == nil {
			if !ok {
				return nil, false
			}
			return func() {
				cleanup, cancel := context.WithTimeout(context.Background(), 2*time.Second)
				defer cancel()
				_ = leader.ReleaseLeaderLock(cleanup, key, owner)
			}, true
		}
	}
	if advisory != nil {
		return advisory(ctx, key)
	}
	return func() {}, true
}
