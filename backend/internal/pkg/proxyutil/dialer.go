// 本文件为阶段迁移兼容入口；剩余消费者和退出阶段见 refactor/S01-foundation.md。
package proxyutil

import (
	http "net/http"
	url "net/url"

	foundation "github.com/TokenFlux/TokenRouter/internal/infra/httpclient/proxy"
)

// ConfigureTransportProxy 兼容旧入口；仅转发到目标实现。
func ConfigureTransportProxy(transport *http.Transport, proxyURL *url.URL) error {
	return foundation.ConfigureTransportProxy(transport, proxyURL)
}
