// 本文件由 billing 拥有资金契约与规则；旧入口仅作过渡适配。
package service

import (
	context "context"
	billing "github.com/TokenFlux/TokenRouter/internal/billing"
	config "github.com/TokenFlux/TokenRouter/internal/config"
	logger "github.com/TokenFlux/TokenRouter/internal/pkg/logger"
)

var ErrSubscriptionInvalid = billing.ErrSubscriptionInvalid

var ErrBillingServiceUnavailable = billing.ErrBillingServiceUnavailable

var ErrGroupRPMExceeded = billing.ErrGroupRPMExceeded

var ErrUserRPMExceeded = billing.ErrUserRPMExceeded

var ErrUserPlatformDailyQuotaExhausted = billing.ErrUserPlatformDailyQuotaExhausted

var ErrUserPlatformWeeklyQuotaExhausted = billing.ErrUserPlatformWeeklyQuotaExhausted

var ErrUserPlatformMonthlyQuotaExhausted = billing.ErrUserPlatformMonthlyQuotaExhausted

func checkEffectiveSubscriptionEligibility(subscription *UserSubscription) error {
	return billing.CheckEffectiveSubscriptionEligibility(subscription)
}

// BillingCacheService 只保留旧准入输入投影与 RPM 编排，缓存状态唯一位于 billing。

type BillingCacheService struct {
	*billing.Eligibility
	cfg               *config.Config
	userRPMCache      UserRPMCache
	userGroupRateRepo UserGroupRateRepository
}

type billingBalanceProjection struct{ repo UserRepository }

func (p billingBalanceProjection) GetByID(ctx context.Context, id int64) (*billing.UserSummary, error) {
	u, err := p.repo.GetByID(ctx, id)
	return BillingUserSummary(u), err
}

// BillingOptionsFromConfig 保留旧构造器所需的配置转换，生产装配改由 app 提供独立 Options。

func BillingOptionsFromConfig(c *config.Config) billing.EligibilityOptions {
	return billing.EligibilityOptions{RunMode: c.RunMode, Billing: billing.BillingOptions{MinimumBalanceReserve: c.Billing.MinimumBalanceReserve, UserPlatformQuotaCacheTTLSeconds: c.Billing.UserPlatformQuotaCacheTTLSeconds, UserPlatformQuotaSentinelTTLSeconds: c.Billing.UserPlatformQuotaSentinelTTLSeconds, CircuitBreaker: billing.CircuitBreakerOptions{Enabled: c.Billing.CircuitBreaker.Enabled, FailureThreshold: c.Billing.CircuitBreaker.FailureThreshold, ResetTimeoutSeconds: c.Billing.CircuitBreaker.ResetTimeoutSeconds, HalfOpenRequests: c.Billing.CircuitBreaker.HalfOpenRequests}}, Database: billing.QuotaMirrorOptions{UserPlatformQuotaFlusherEnabled: c.Database.UserPlatformQuotaFlusherEnabled}}
}

func NewBillingCacheService(cache BillingCache, users UserRepository, _ UserSubscriptionRepository, keys APIKeyRepository, rpm UserRPMCache, rates UserGroupRateRepository, cfg *config.Config, quotas UserPlatformQuotaRepository) *BillingCacheService {

	return &BillingCacheService{Eligibility: billing.NewEligibility(cache, billingBalanceProjection{users}, keys, quotas, func() billing.EligibilityOptions { return BillingOptionsFromConfig(cfg) }, logger.LegacyPrintf, billing.NewQuotaCoordinator(), func(name string, fn func()) { RunBackgroundTask(name, BackgroundCall0(fn)) }), cfg: cfg, userRPMCache: rpm, userGroupRateRepo: rates}

}

func BillingKeySnapshot(key *APIKey) *billing.KeySnapshot {
	if key == nil {
		return nil
	}
	return &billing.KeySnapshot{ID: key.ID, BillingMode: key.BillingMode, RateLimit5h: key.RateLimit5h, RateLimit1d: key.RateLimit1d, RateLimit7d: key.RateLimit7d}
}

func (s *BillingCacheService) CheckBillingEligibility(ctx context.Context, user *User, key *APIKey, group *Group, subscription *UserSubscription, platform string) error {

	var g *billing.GroupSnapshot
	if group != nil {
		g = &billing.GroupSnapshot{ID: group.ID}
	}

	if err := s.Check(ctx, billing.CheckInput{Payer: BillingUserSummary(user), Key: BillingKeySnapshot(key), Group: g, Subscription: subscription, Platform: platform}); err != nil {
		return err
	}

	if s.cfg.RunMode == config.RunModeSimple {
		return nil
	}
	return s.checkRPM(ctx, user, group)

}

// checkRPM 执行并行 RPM 限流，所有适用的限制同时生效，任一超限即拒绝：
//
//  1. (用户, 分组) rpm_override       — 最细粒度：管理员为特定用户在特定分组设定的专属限额。
//     override=0 表示该用户在该分组免检（绿灯），但 user 级全局上限仍然生效。
//  2. group.rpm_limit                 — 分组级：该分组的统一 RPM 容量（仅当无 override 时生效）。
//  3. user.rpm_limit                  — 用户级全局硬上限：无论 override/group 如何配置，始终生效。
//
// 与旧版"级联互斥"设计不同，新版确保 user.rpm_limit 作为全局天花板不会被 group 或 override 覆盖。
// Redis 故障一律 fail-open（打 warning，不阻塞业务）。
func (s *BillingCacheService) checkRPM(ctx context.Context, user *User, group *Group) error {
	if s == nil || s.userRPMCache == nil || user == nil {
		return nil
	}

	// ── 第一层：分组级检查（override 或 group.rpm_limit） ──
	if group != nil {
		// 解析 override：优先从 auth cache snapshot，nil 时回退 DB。
		var override *int
		if user.UserGroupRPMOverride != nil {
			override = user.UserGroupRPMOverride
		} else if s.userGroupRateRepo != nil {
			dbOverride, err := s.userGroupRateRepo.GetRPMOverrideByUserAndGroup(ctx, user.ID, group.ID)
			if err != nil {
				logger.LegacyPrintf(
					"service.billing_cache",
					"Warning: rpm override lookup failed for user=%d group=%d: %v",
					user.ID, group.ID, err,
				)
			} else {
				override = dbOverride
			}
		}

		if override != nil {
			// override=0 → 该用户在该分组免检（但 user 级仍会在下面检查）。
			if *override > 0 {
				count, incErr := s.userRPMCache.IncrementUserGroupRPM(ctx, user.ID, group.ID)
				if incErr != nil {
					logger.LegacyPrintf(
						"service.billing_cache",
						"Warning: rpm increment (override) failed for user=%d group=%d: %v",
						user.ID, group.ID, incErr,
					)
					// fail-open
				} else if count > *override {
					return ErrGroupRPMExceeded
				}
			}
			// override 命中后跳过 group.rpm_limit（override 替代 group），但不 return——继续检查 user 级。
		} else if group.RPMLimit > 0 {
			// 无 override，检查 group.rpm_limit。
			count, err := s.userRPMCache.IncrementUserGroupRPM(ctx, user.ID, group.ID)
			if err != nil {
				logger.LegacyPrintf(
					"service.billing_cache",
					"Warning: rpm increment (group) failed for user=%d group=%d: %v",
					user.ID, group.ID, err,
				)
				// fail-open
			} else if count > group.RPMLimit {
				return ErrGroupRPMExceeded
			}
		}
	}

	// ── 第二层：用户级全局硬上限（始终生效） ──
	if user.RPMLimit > 0 {
		count, err := s.userRPMCache.IncrementUserRPM(ctx, user.ID)
		if err != nil {
			logger.LegacyPrintf(
				"service.billing_cache",
				"Warning: rpm increment (user) failed for user=%d: %v",
				user.ID, err,
			)
			return nil // fail-open
		}
		if count > user.RPMLimit {
			return ErrUserRPMExceeded
		}
	}

	return nil
}

// WrapBillingEligibility 保留旧 RPM 编排，生产缓存运行时由 app 唯一提供。
func WrapBillingEligibility(core *billing.Eligibility, cfg *config.Config, rpm UserRPMCache, rates UserGroupRateRepository) *BillingCacheService {
	return &BillingCacheService{Eligibility: core, cfg: cfg, userRPMCache: rpm, userGroupRateRepo: rates}
}
