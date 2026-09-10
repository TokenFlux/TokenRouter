// 本文件为阶段迁移兼容入口；剩余消费者和退出阶段见 refactor/S01-foundation.md。
package httpclient

import (
	http "net/http"

	foundation "github.com/TokenFlux/TokenRouter/internal/infra/httpclient"
)

// Options 保留旧调用方的类型身份；实现归目标包。
type Options = foundation.Options

// GetClient 兼容旧入口；仅转发到目标实现。
func GetClient(opts Options) (*http.Client, error) {
	return foundation.GetClient(opts)
}
