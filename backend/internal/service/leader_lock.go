package service

import (
	"context"
	"database/sql"
	"time"

	"github.com/TokenFlux/TokenRouter/internal/account"
)

// LeaderLockCache 为周期性后台任务提供跨实例互斥。
// 具体实现放在 repository 层，避免 service 层直接依赖缓存客户端。
type LeaderLockCache interface {
	// TryAcquireLeaderLock 在 key 不存在时写入 owner 和 TTL。
	TryAcquireLeaderLock(ctx context.Context, key, owner string, ttl time.Duration) (bool, error)
	// ReleaseLeaderLock 仅当 key 仍归 owner 持有时删除锁。
	ReleaseLeaderLock(ctx context.Context, key, owner string) error
}

// tryAcquireSingletonLeaderLock 为周期性后台任务提供单实例执行保护。
//
// 语义：
//   - 成功抢锁：返回 release 和 true，调用方应在任务结束时释放。
//   - 其他实例持锁：返回 nil 和 false，本轮任务跳过。
//   - 无 Redis/DB 后端：返回空 release 和 true，保持单实例部署与单测的旧行为。
//
// 缓存不可用时回退到数据库咨询锁，避免缓存抖动导致所有实例同时跑重任务。
func tryAcquireSingletonLeaderLock(ctx context.Context, cache LeaderLockCache, db *sql.DB, key, owner string, ttl time.Duration) (func(), bool) {
	var advisory func(context.Context, string) (func(), bool)
	if db != nil {
		advisory = func(ctx context.Context, key string) (func(), bool) {
			return tryAcquireDBAdvisoryLock(ctx, db, hashAdvisoryLockID(key))
		}
	}
	return account.AcquireSingletonLease(ctx, cache, advisory, key, owner, ttl)
}

// AcquireSingletonLeaderLock 仅向组合根暴露现有技术参与适配，S14/S16 清理。
func AcquireSingletonLeaderLock(ctx context.Context, cache LeaderLockCache, db *sql.DB, key, owner string, ttl time.Duration) (func(), bool) {
	return tryAcquireSingletonLeaderLock(ctx, cache, db, key, owner, ttl)
}
