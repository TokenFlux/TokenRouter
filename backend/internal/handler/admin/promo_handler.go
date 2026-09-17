// 兼容入口只引用所属模块的唯一实现，S15/S16 清理。
package admin

import (
	"github.com/TokenFlux/TokenRouter/internal/promotion"
	native "github.com/TokenFlux/TokenRouter/internal/promotion/httpapi"
)

type PromoHandler = native.PromoHandler

func NewPromoHandler(promoService *promotion.PromoService) *PromoHandler {
	return native.NewPromoHandler(promoService)
}

type CreatePromoCodeRequest = native.CreatePromoCodeRequest
type UpdatePromoCodeRequest = native.UpdatePromoCodeRequest
