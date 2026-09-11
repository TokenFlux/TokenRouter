package billing

import (
	"context"
	"slices"
	"sync"
)

// QuotaCoordinator 协调单个服务进程内的平台额度管理、缓存更新与镜像写回。
// 锁必须覆盖读取和写回的完整区间；它不提供跨进程或跨存储原子性。
// @project-doc docs/domains/platform_quotas.md#platform_quota_settlement_and_flush
type QuotaCoordinator struct {
	mu    sync.Mutex
	users map[int64]*quotaUserLock
}

type quotaUserLock struct {
	gate chan struct{}
	refs int
}

// NewQuotaCoordinator 由 app 构造并共享，不能为管理入口和后台分别创建实例。
func NewQuotaCoordinator() *QuotaCoordinator {
	return &QuotaCoordinator{users: make(map[int64]*quotaUserLock)}
}

// Acquire 按用户 ID 升序取得可取消的锁；返回的释放函数可以重复调用。
// 等待者也持有引用，防止锁表清理时为同一用户创建第二把锁。
func (c *QuotaCoordinator) Acquire(ctx context.Context, userIDs ...int64) (func(), error) {
	ids := slices.Clone(userIDs)
	slices.Sort(ids)
	ids = slices.Compact(ids)
	type heldLock struct {
		id   int64
		lock *quotaUserLock
	}
	held := make([]heldLock, 0, len(ids))
	var once sync.Once
	release := func() {
		once.Do(func() {
			for i := len(held) - 1; i >= 0; i-- {
				c.release(held[i].id, held[i].lock, true)
			}
		})
	}
	for _, id := range ids {
		if err := ctx.Err(); err != nil {
			release()
			return nil, err
		}
		c.mu.Lock()
		if c.users == nil {
			c.users = make(map[int64]*quotaUserLock)
		}
		entry := c.users[id]
		if entry == nil {
			entry = &quotaUserLock{gate: make(chan struct{}, 1)}
			entry.gate <- struct{}{}
			c.users[id] = entry
		}
		entry.refs++
		c.mu.Unlock()

		select {
		case <-entry.gate:
			held = append(held, heldLock{id: id, lock: entry})
		case <-ctx.Done():
			c.release(id, entry, false)
			release()
			return nil, ctx.Err()
		}
	}
	if err := ctx.Err(); err != nil {
		release()
		return nil, err
	}
	return release, nil
}

func (c *QuotaCoordinator) release(id int64, entry *quotaUserLock, acquired bool) {
	if acquired {
		entry.gate <- struct{}{}
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	entry.refs--
	if entry.refs == 0 {
		delete(c.users, id)
	}
}
