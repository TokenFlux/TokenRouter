// 本文件维护 service 的所属能力；兼容入口复用唯一实现。
package service

import (
	egress "github.com/TokenFlux/TokenRouter/internal/egress"
	time "time"
)

// ResolveProxyFallbackTarget 委托所属模块的唯一实现。
func ResolveProxyFallbackTarget(start Proxy, byID map[int64]Proxy, now time.Time) (*int64, bool) {
	return egress.ResolveProxyFallbackTarget(start, byID, now)
}
