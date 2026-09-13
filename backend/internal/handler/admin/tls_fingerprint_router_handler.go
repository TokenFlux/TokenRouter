// 本文件维护 admin 的所属能力；兼容入口复用唯一实现。
package admin

import (
	egresshttp "github.com/TokenFlux/TokenRouter/internal/egress/httpapi"
	service "github.com/TokenFlux/TokenRouter/internal/service"
)

type TLSFingerprintRouterHandler = egresshttp.TLSFingerprintRouterHandler

// NewTLSFingerprintRouterHandler 委托所属模块的唯一实现。
func NewTLSFingerprintRouterHandler(service *service.TLSFingerprintRouterService) *TLSFingerprintRouterHandler {
	return egresshttp.NewTLSFingerprintRouterHandler(service)
}

type CreateTLSFingerprintRouterRequest = egresshttp.CreateTLSFingerprintRouterRequest

type UpdateTLSFingerprintRouterRequest = egresshttp.UpdateTLSFingerprintRouterRequest
