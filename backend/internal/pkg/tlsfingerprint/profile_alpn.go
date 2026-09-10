// 本文件为阶段迁移兼容入口；剩余消费者和退出阶段见 refactor/S01-foundation.md。
package tlsfingerprint

import (
	net "net"

	foundation "github.com/TokenFlux/TokenRouter/internal/infra/httpclient/tlsfingerprint"
)

// SupportsHTTP2 兼容旧入口；仅转发到目标实现。
func SupportsHTTP2(profile *Profile) bool {
	return foundation.SupportsHTTP2(profile)
}

// HTTP1OnlyProfile 兼容旧入口；仅转发到目标实现。
func HTTP1OnlyProfile(profile *Profile) *Profile {
	return foundation.HTTP1OnlyProfile(profile)
}

// NegotiatedProtocol 兼容旧入口；仅转发到目标实现。
func NegotiatedProtocol(conn net.Conn) string {
	return foundation.NegotiatedProtocol(conn)
}
