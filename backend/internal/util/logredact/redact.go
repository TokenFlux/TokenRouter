// 本文件为阶段迁移兼容入口；剩余消费者和退出阶段见 refactor/S01-foundation.md。
package logredact

import (
	foundation "github.com/TokenFlux/TokenRouter/internal/pkg/logredact"
)

// RedactMap 兼容旧入口；仅转发到目标实现。
func RedactMap(input map[string]any, extraKeys ...string) map[string]any {
	return foundation.RedactMap(input, extraKeys...)
}

// RedactJSON 兼容旧入口；仅转发到目标实现。
func RedactJSON(raw []byte, extraKeys ...string) string {
	return foundation.RedactJSON(raw, extraKeys...)
}

// RedactText 兼容旧入口；仅转发到目标实现。
func RedactText(input string, extraKeys ...string) string {
	return foundation.RedactText(input, extraKeys...)
}
