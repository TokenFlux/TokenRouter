//go:build unit

// 仅保留既有测试的私有兼容入口；生产实现已迁出。
package middleware

import (
	gatewayhttp "github.com/TokenFlux/TokenRouter/internal/gateway/httpapi"
)

func isAPIKeyNonConsumingRequest(method, path string) bool {
	return gatewayhttp.IsAPIKeyNonConsumingRequest(method, path)
}
