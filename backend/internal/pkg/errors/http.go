// 本文件为阶段迁移兼容入口；剩余消费者和退出阶段见 refactor/S01-foundation.md。
package errors

import (
	foundation "github.com/TokenFlux/TokenRouter/internal/server/httpx"
)

// ToHTTP 兼容旧入口；仅转发到目标实现。
func ToHTTP(err error) (statusCode int, body Status) {
	return foundation.ToHTTP(err)
}
