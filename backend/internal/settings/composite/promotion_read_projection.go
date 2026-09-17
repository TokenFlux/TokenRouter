package composite

import "github.com/TokenFlux/TokenRouter/internal/promotion"

// ApplyPromotionAdminReadSettings 仅转换所属模块值，不读取设置或发布状态。
func (s *Snapshot) ApplyPromotionAdminReadSettings(value *promotion.AdminReadSettings) {
	s.AdminRechargeRebateEnabled = value.AdminRechargeRebateEnabled
	s.AffiliateEnabled = value.AffiliateEnabled
	s.AffiliateRebateDurationDays = value.AffiliateRebateDurationDays
	s.AffiliateRebateFreezeHours = value.AffiliateRebateFreezeHours
	s.AffiliateRebatePerInviteeCap = value.AffiliateRebatePerInviteeCap
	s.AffiliateRebateRate = value.AffiliateRebateRate
	s.InvitationCodeEnabled = value.InvitationCodeEnabled
	s.PromoCodeEnabled = value.PromoCodeEnabled
}
