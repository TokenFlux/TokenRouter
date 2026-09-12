// 本文件维护 httpapi 的所属能力；兼容入口复用唯一实现。
package httpapi

import (
	billing "github.com/TokenFlux/TokenRouter/internal/billing"
	billingdto "github.com/TokenFlux/TokenRouter/internal/billing/httpapi/dto"
)

// RedeemCodeFromService 委托所属模块的唯一实现。
func RedeemCodeFromService(rc *billing.RedeemCode) *RedeemCode {
	return billingdto.RedeemCodeFromService(rc)
}

// RedeemCodeFromServiceAdmin 委托所属模块的唯一实现。
func RedeemCodeFromServiceAdmin(rc *billing.RedeemCode) *AdminRedeemCode {
	return billingdto.RedeemCodeFromServiceAdmin(rc)
}
