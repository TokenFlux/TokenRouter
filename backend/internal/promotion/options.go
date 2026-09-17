// 推广配置和副作用仅通过消费者端口注入，核心不持有旧服务。
package promotion

import (
	"context"
	"math"
	"time"
)

type SettingsReader interface {
	IsAffiliateEnabled(context.Context) bool
	GetAffiliateRebateRatePercent(context.Context) float64
	GetAffiliateRebateFreezeHours(context.Context) int
	GetAffiliateRebateDurationDays(context.Context) int
	GetAffiliateRebatePerInviteeCap(context.Context) float64
}
type AuthCacheInvalidator interface{ InvalidateAuthCacheByUserID(context.Context, int64) }
type BalanceCache interface {
	InvalidateUserBalance(context.Context, int64) error
}
type Runtime struct {
	Background func(string, func())
	Now        func() time.Time
	Warn       func(int64, error)
}

// Affiliate rebate settings
const (
	AffiliateRebateRateDefault          = 20.0
	AffiliateRebateRateMin              = 0.0
	AffiliateRebateRateMax              = 100.0
	AffiliateEnabledDefault             = false // 邀请返利总开关默认关闭
	AffiliateRebateFreezeHoursDefault   = 0     // 0 表示不冻结
	AffiliateRebateFreezeHoursMax       = 720   // 最大 30 天
	AffiliateRebateDurationDaysDefault  = 0     // 0 表示永久有效
	AffiliateRebateDurationDaysMax      = 3650  // 约 10 年
	AffiliateRebatePerInviteeCapDefault = 0.0   // 推理积分上限，0 表示无上限
	AdminRechargeRebateEnabledDefault   = false // 管理员充值默认不产生返利
)

func ClampRebateRate(value float64) float64 {
	if math.IsNaN(value) || math.IsInf(value, 0) {
		return AffiliateRebateRateDefault
	}
	if value < AffiliateRebateRateMin {
		return AffiliateRebateRateMin
	}
	if value > AffiliateRebateRateMax {
		return AffiliateRebateRateMax
	}
	return value
}
