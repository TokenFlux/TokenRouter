// 兼容入口只引用所属模块的唯一实现，S15/S16 清理。
package service

import native "github.com/TokenFlux/TokenRouter/internal/promotion"

type PromoCode = native.PromoCode
type PromoCodeUsage = native.PromoCodeUsage
type CreatePromoCodeInput = native.CreatePromoCodeInput
type UpdatePromoCodeInput = native.UpdatePromoCodeInput
