// 本文件维护 admin 的所属能力；兼容入口复用唯一实现。
package admin

import (
	httpx "github.com/TokenFlux/TokenRouter/internal/server/httpx"
	time "time"
)

type snapshotCache = httpx.SnapshotCache

// newSnapshotCache 委托所属模块的唯一实现。
func newSnapshotCache(ttl time.Duration) *snapshotCache { return httpx.NewSnapshotCache(ttl) }

// parseBoolQueryWithDefault 委托所属模块的唯一实现。
func parseBoolQueryWithDefault(raw string, def bool) bool {
	return httpx.ParseBoolQueryWithDefault(raw, def)
}
