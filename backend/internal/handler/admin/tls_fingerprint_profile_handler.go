// 本文件维护 admin 的所属能力；兼容入口复用唯一实现。
package admin

import (
	egress "github.com/TokenFlux/TokenRouter/internal/egress"
	egresshttp "github.com/TokenFlux/TokenRouter/internal/egress/httpapi"
	service "github.com/TokenFlux/TokenRouter/internal/service"
)

type TLSFingerprintProfileHandler = egresshttp.TLSFingerprintProfileHandler

// NewTLSFingerprintProfileHandler 委托所属模块的唯一实现。
func NewTLSFingerprintProfileHandler(profileService *service.TLSFingerprintProfileService, collectors ...*service.TLSFingerprintCollectorService) *TLSFingerprintProfileHandler {
	var core *egress.TLSFingerprintProfileService
	if profileService != nil {
		core = profileService.TLSFingerprintProfileService
	}
	var values []egress.Collector
	for _, collector := range collectors {
		if collector != nil {
			values = append(values, collector)
		} else {
			values = append(values, nil)
		}
	}
	return egresshttp.NewTLSFingerprintProfileHandler(core, values...)
}

type CreateTLSFingerprintProfileRequest = egresshttp.CreateTLSFingerprintProfileRequest

type UpdateTLSFingerprintProfileRequest = egresshttp.UpdateTLSFingerprintProfileRequest
