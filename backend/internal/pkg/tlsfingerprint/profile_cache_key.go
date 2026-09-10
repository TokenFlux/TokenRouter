// 本文件为阶段迁移兼容入口；剩余消费者和退出阶段见 refactor/S01-foundation.md。
package tlsfingerprint

import (
	foundation "github.com/TokenFlux/TokenRouter/internal/infra/httpclient/tlsfingerprint"
)

// CacheKey 兼容旧入口；仅转发到目标实现。
func CacheKey(profile *Profile) string {
	return foundation.CacheKey(profile)
}
