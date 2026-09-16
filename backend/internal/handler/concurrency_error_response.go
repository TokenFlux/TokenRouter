// 旧入口共享 gateway 的并发错误映射。
package handler

import gatewayhttp "github.com/TokenFlux/TokenRouter/internal/gateway/httpapi"

const statusClientClosedRequest = gatewayhttp.StatusClientClosedRequest
const gatewayQueueFullCode = gatewayhttp.GatewayQueueFullCode
const gatewayConcurrencyLimitCode = gatewayhttp.GatewayConcurrencyLimitCode

func concurrencyErrorResponse(err error, slot string) (int, string, string, string) {
	return gatewayhttp.ConcurrencyErrorResponse(err, slot)
}
