// 本文件为阶段迁移兼容入口；剩余消费者和退出阶段见 refactor/S01-foundation.md。
package proxyurl

import (
	url "net/url"

	foundation "github.com/TokenFlux/TokenRouter/internal/infra/httpclient/proxy"
)

// Parse 兼容旧入口；仅转发到目标实现。
func Parse(raw string) (trimmed string, parsed *url.URL, err error) {
	return foundation.Parse(raw)
}
