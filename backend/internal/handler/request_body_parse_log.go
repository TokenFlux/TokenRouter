// 解析诊断兼容入口委托 HTTP 适配器。
package handler

import (
	gatewayhttp "github.com/TokenFlux/TokenRouter/internal/gateway/httpapi"
	"go.uber.org/zap"
)

func logRequestBodyParseFailure(log *zap.Logger, body []byte, err error) {
	gatewayhttp.LogRequestBodyParseFailure(log, body, err)
}
