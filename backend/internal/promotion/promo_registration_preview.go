// 本文件维护 service 的所属能力；兼容入口复用唯一实现。
package promotion

import (
	context "context"
)

// RegistrationPromotionPreview 保留公开注册预览的结果形状和原错误分类。
type RegistrationPromotionPreview struct {
	Valid       bool
	BonusAmount float64
	ErrorCode   string
}

func (s *PromoService) PreviewRegistrationPromotion(ctx context.Context, code string) RegistrationPromotionPreview {
	v, e := s.ValidatePromoCode(ctx, code)
	if e != nil {
		reason := "PROMO_CODE_INVALID"
		switch e {
		case ErrPromoCodeNotFound:
			reason = "PROMO_CODE_NOT_FOUND"
		case ErrPromoCodeExpired:
			reason = "PROMO_CODE_EXPIRED"
		case ErrPromoCodeDisabled:
			reason = "PROMO_CODE_DISABLED"
		case ErrPromoCodeMaxUsed:
			reason = "PROMO_CODE_MAX_USED"
		case ErrPromoCodeAlreadyUsed:
			reason = "PROMO_CODE_ALREADY_USED"
		}
		return RegistrationPromotionPreview{ErrorCode: reason}
	}
	if v == nil {
		return RegistrationPromotionPreview{ErrorCode: "PROMO_CODE_INVALID"}
	}
	return RegistrationPromotionPreview{Valid: true, BonusAmount: v.BonusAmount}
}
