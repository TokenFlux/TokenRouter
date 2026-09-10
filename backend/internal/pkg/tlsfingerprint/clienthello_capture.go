// 本文件为阶段迁移兼容入口；剩余消费者和退出阶段见 refactor/S01-foundation.md。
package tlsfingerprint

import (
	foundation "github.com/TokenFlux/TokenRouter/internal/infra/httpclient/tlsfingerprint"
)

// CapturedClientHello 保留旧调用方的类型身份；实现归目标包。
type CapturedClientHello = foundation.CapturedClientHello

// ParseCapturedClientHello 兼容旧入口；仅转发到目标实现。
func ParseCapturedClientHello(record []byte) (*CapturedClientHello, error) {
	return foundation.ParseCapturedClientHello(record)
}
