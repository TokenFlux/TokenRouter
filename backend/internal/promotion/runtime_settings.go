package promotion

import (
	"context"
	"math"
	"strconv"
	"strings"
)

// 推广设置键沿用既有数据库格式，由推广模块拥有解释权。
const (
	SettingKeyAffiliateAdminRechargeEnabled = "affiliate_admin_recharge_enabled"
	SettingKeyAffiliateEnabled              = "affiliate_enabled"
	SettingKeyAffiliateRebateDurationDays   = "affiliate_rebate_duration_days"
	SettingKeyAffiliateRebateFreezeHours    = "affiliate_rebate_freeze_hours"
	SettingKeyAffiliateRebatePerInviteeCap  = "affiliate_rebate_per_invitee_cap"
	SettingKeyAffiliateRebateRate           = "affiliate_rebate_rate"
	SettingKeyInvitationCodeEnabled         = "invitation_code_enabled"
	SettingKeyPromoCodeEnabled              = "promo_code_enabled"
)

// RuntimeSettingsStore 仅提供运行设置读取，不访问身份或资金实体。
type RuntimeSettingsStore interface {
	GetValue(context.Context, string) (string, error)
}

// RuntimeSettings 保持每个入口的原有回源时机，不新增缓存。
type RuntimeSettings struct{ settingRepo RuntimeSettingsStore }

// NewRuntimeSettings 构造无副作用的推广设置读取器。
func NewRuntimeSettings(repo RuntimeSettingsStore) *RuntimeSettings {
	return &RuntimeSettings{settingRepo: repo}
}

// IsPromoCodeEnabled 保留原设置的缺省、边界和读取时点。
func (s *RuntimeSettings) IsPromoCodeEnabled(ctx context.Context) bool {
	value, err := s.settingRepo.GetValue(ctx, SettingKeyPromoCodeEnabled)
	if err != nil {
		return true // 默认启用
	}
	return value != "false"
}

// IsInvitationCodeEnabled 保留原设置的缺省、边界和读取时点。
func (s *RuntimeSettings) IsInvitationCodeEnabled(ctx context.Context) bool {
	value, err := s.settingRepo.GetValue(ctx, SettingKeyInvitationCodeEnabled)
	if err != nil {
		return false // 默认关闭
	}
	return value == "true"
}

// IsAffiliateEnabled 保留原设置的缺省、边界和读取时点。
func (s *RuntimeSettings) IsAffiliateEnabled(ctx context.Context) bool {
	value, err := s.settingRepo.GetValue(ctx, SettingKeyAffiliateEnabled)
	if err != nil {
		return AffiliateEnabledDefault
	}
	return value == "true"
}

// IsAffiliateAdminRechargeEnabled 保留原设置的缺省、边界和读取时点。
func (s *RuntimeSettings) IsAffiliateAdminRechargeEnabled(ctx context.Context) bool {
	value, err := s.settingRepo.GetValue(ctx, SettingKeyAffiliateAdminRechargeEnabled)
	if err != nil {
		return AdminRechargeRebateEnabledDefault
	}
	return value == "true"
}

// GetAffiliateRebateRatePercent 保留原设置的缺省、边界和读取时点。
func (s *RuntimeSettings) GetAffiliateRebateRatePercent(ctx context.Context) float64 {
	value, err := s.settingRepo.GetValue(ctx, SettingKeyAffiliateRebateRate)
	if err != nil {
		return AffiliateRebateRateDefault
	}
	rate, err := strconv.ParseFloat(strings.TrimSpace(value), 64)
	if err != nil || math.IsNaN(rate) || math.IsInf(rate, 0) {
		return AffiliateRebateRateDefault
	}
	return ClampRebateRate(rate)
}

// GetAffiliateRebateFreezeHours 保留原设置的缺省、边界和读取时点。
func (s *RuntimeSettings) GetAffiliateRebateFreezeHours(ctx context.Context) int {
	value, err := s.settingRepo.GetValue(ctx, SettingKeyAffiliateRebateFreezeHours)
	if err != nil {
		return AffiliateRebateFreezeHoursDefault
	}
	hours, err := strconv.Atoi(strings.TrimSpace(value))
	if err != nil || hours < 0 {
		return AffiliateRebateFreezeHoursDefault
	}
	if hours > AffiliateRebateFreezeHoursMax {
		return AffiliateRebateFreezeHoursMax
	}
	return hours
}

// GetAffiliateRebateDurationDays 保留原设置的缺省、边界和读取时点。
func (s *RuntimeSettings) GetAffiliateRebateDurationDays(ctx context.Context) int {
	value, err := s.settingRepo.GetValue(ctx, SettingKeyAffiliateRebateDurationDays)
	if err != nil {
		return AffiliateRebateDurationDaysDefault
	}
	days, err := strconv.Atoi(strings.TrimSpace(value))
	if err != nil || days < 0 {
		return AffiliateRebateDurationDaysDefault
	}
	if days > AffiliateRebateDurationDaysMax {
		return AffiliateRebateDurationDaysMax
	}
	return days
}

// GetAffiliateRebatePerInviteeCap 保留原设置的缺省、边界和读取时点。
func (s *RuntimeSettings) GetAffiliateRebatePerInviteeCap(ctx context.Context) float64 {
	value, err := s.settingRepo.GetValue(ctx, SettingKeyAffiliateRebatePerInviteeCap)
	if err != nil {
		return AffiliateRebatePerInviteeCapDefault
	}
	capValue, err := strconv.ParseFloat(strings.TrimSpace(value), 64)
	if err != nil || capValue < 0 || math.IsNaN(capValue) || math.IsInf(capValue, 0) {
		return AffiliateRebatePerInviteeCapDefault
	}
	return capValue
}
