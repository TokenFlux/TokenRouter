// 本文件为阶段迁移兼容入口；剩余消费者和退出阶段见 refactor/S01-foundation.md。
package tlsfingerprint

import (
	context "context"
	net "net"
	url "net/url"

	foundation "github.com/TokenFlux/TokenRouter/internal/infra/httpclient/tlsfingerprint"
)

// Profile 保留旧调用方的类型身份；实现归目标包。
type Profile = foundation.Profile

// Dialer 保留旧调用方的类型身份；实现归目标包。
type Dialer = foundation.Dialer

// HTTPProxyDialer 保留旧调用方的类型身份；实现归目标包。
type HTTPProxyDialer = foundation.HTTPProxyDialer

// SOCKS5ProxyDialer 保留旧调用方的类型身份；实现归目标包。
type SOCKS5ProxyDialer = foundation.SOCKS5ProxyDialer

// NewDialer 兼容旧入口；仅转发到目标实现。
func NewDialer(profile *Profile, baseDialer func(ctx context.Context, network, addr string) (net.Conn, error)) *Dialer {
	return foundation.NewDialer(profile, baseDialer)
}

// NewHTTPProxyDialer 兼容旧入口；仅转发到目标实现。
func NewHTTPProxyDialer(profile *Profile, proxyURL *url.URL) *HTTPProxyDialer {
	return foundation.NewHTTPProxyDialer(profile, proxyURL)
}

// NewSOCKS5ProxyDialer 兼容旧入口；仅转发到目标实现。
func NewSOCKS5ProxyDialer(profile *Profile, proxyURL *url.URL) *SOCKS5ProxyDialer {
	return foundation.NewSOCKS5ProxyDialer(profile, proxyURL)
}
