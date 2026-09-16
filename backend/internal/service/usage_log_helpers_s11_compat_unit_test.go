//go:build unit

// 仅保留既有测试的私有兼容入口；生产实现已迁出。
package service

import completion "github.com/TokenFlux/TokenRouter/internal/gateway/completion"

func forwardResultBillingModel(requestedModel, upstreamModel string) string {
	return completion.ForwardResultBillingModel(requestedModel, upstreamModel)
}
