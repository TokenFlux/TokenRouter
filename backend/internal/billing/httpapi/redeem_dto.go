// 本文件维护 httpapi 的所属能力；兼容入口复用唯一实现。
package httpapi

import (
	billingdto "github.com/TokenFlux/TokenRouter/internal/billing/httpapi/dto"
)

type RedeemCode = billingdto.RedeemCode

type AdminRedeemCode = billingdto.AdminRedeemCode

type NullableTimeField = billingdto.NullableTimeField

type BatchUpdateRedeemCodeFields = billingdto.BatchUpdateRedeemCodeFields

type BatchUpdateRedeemCodesRequest = billingdto.BatchUpdateRedeemCodesRequest
