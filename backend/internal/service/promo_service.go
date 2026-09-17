// 兼容入口仅保留所属模块类型与错误，S15/S16 清理。
package service

import native "github.com/TokenFlux/TokenRouter/internal/promotion"

type PromoService = native.PromoService

var ErrPromoCodeNotFound = native.ErrPromoCodeNotFound
var ErrPromoCodeExpired = native.ErrPromoCodeExpired
var ErrPromoCodeDisabled = native.ErrPromoCodeDisabled
var ErrPromoCodeMaxUsed = native.ErrPromoCodeMaxUsed
var ErrPromoCodeAlreadyUsed = native.ErrPromoCodeAlreadyUsed
var ErrPromoCodeInvalid = native.ErrPromoCodeInvalid
