//go:build unit

// 测试私有兼容入口委托所属模块的生产实现。
package middleware

import (
	gatewayhttp "github.com/TokenFlux/TokenRouter/internal/gateway/httpapi"
)

func isAPIKeyNonConsumingRequest(method, path string) bool {
	return gatewayhttp.IsAPIKeyNonConsumingRequest(method, path)
}
