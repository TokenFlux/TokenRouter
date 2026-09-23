// 旧入口只组合投影端口，快照、重建和 outbox 状态唯一位于 scheduler。
package service

import (
	"github.com/TokenFlux/TokenRouter/internal/scheduler"
)

// LegacySnapshotCachePort 只处理旧缓存接口形状，生产路径直接返回新缓存。
func LegacySnapshotCachePort(cache SchedulerCache) scheduler.SnapshotCache {
	var cachePort scheduler.SnapshotCache
	if cache != nil {
		base := legacySnapshotCache{SchedulerCache: cache}
		cachePort = base
		if writer, ok := cache.(legacySnapshotAccountIDWriter); ok {
			cachePort = legacySnapshotCacheWithIDs{legacySnapshotCache: base, writer: writer}
		}
	}
	if direct, ok := cache.(interface {
		SnapshotCoreCache() scheduler.SnapshotCache
	}); ok {
		cachePort = direct.SnapshotCoreCache()
	}
	return cachePort
}
