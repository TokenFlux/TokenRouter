// 旧 HTTP 入口只委托新 Adapter，S15/S16 清理。
package admin

import (
	service "github.com/TokenFlux/TokenRouter/internal/ops"

	native "github.com/TokenFlux/TokenRouter/internal/ops/httpapi"
)

type OpsHandler = native.OpsHandler

func NewOpsHandler(opsService *service.OpsService) *OpsHandler {
	return native.NewOpsHandler(opsService)
}
