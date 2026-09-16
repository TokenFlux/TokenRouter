package handler

import gatewayhttp "github.com/TokenFlux/TokenRouter/internal/gateway/httpapi"

const invalidStreamFieldTypeMessage = gatewayhttp.InvalidStreamFieldTypeMessage

// 旧入口保持严格字段验证，唯一实现由协议 HTTP 适配器持有。
func parseOpenAICompatibleStream(body []byte) (bool, bool) {
	return gatewayhttp.ParseOpenAICompatibleStream(body)
}
