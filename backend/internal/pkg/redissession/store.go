// 本文件为阶段迁移兼容入口；剩余消费者和退出阶段见 refactor/S01-foundation.md。
package redissession

import (
	time "time"

	foundation "github.com/TokenFlux/TokenRouter/internal/infra/redis/session"

	"github.com/redis/go-redis/v9"
)

// ErrNotConfigured 兼容旧入口，值由唯一实现提供。
var ErrNotConfigured = foundation.ErrNotConfigured

// Store 保留旧调用方的类型身份；实现归目标包。
type Store = foundation.Store

// New 兼容旧入口；仅转发到目标实现。
func New(rdb *redis.Client, prefix string, ttl time.Duration) *Store {
	return foundation.New(rdb, prefix, ttl)
}
