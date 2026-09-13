// 本文件由 billing 拥有资金契约与规则；旧入口仅作过渡适配。
package service

import (
	context "context"

	billing "github.com/TokenFlux/TokenRouter/internal/billing"
	config "github.com/TokenFlux/TokenRouter/internal/config"
	logger "github.com/TokenFlux/TokenRouter/internal/pkg/logger"
	"github.com/TokenFlux/TokenRouter/internal/scheduler"
)

var ErrSubscriptionInvalid = billing.ErrSubscriptionInvalid

var ErrBillingServiceUnavailable = billing.ErrBillingServiceUnavailable

var ErrGroupRPMExceeded = scheduler.ErrGroupRPMExceeded

var ErrUserRPMExceeded = scheduler.ErrUserRPMExceeded

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

// checkRPM 仅投影旧用户和分组；资金检查后的执行位置保持不变。
func (s *BillingCacheService) checkRPM(ctx context.Context, user *User, group *Group) error {
	if s == nil || user == nil {
		return nil
	}
	var projected *scheduler.RPMGroup
	if group != nil {
		projected = &scheduler.RPMGroup{ID: group.ID, RPMLimit: group.RPMLimit}
	}
	return scheduler.NewRPMAdmission(s.userRPMCache, s.userGroupRateRepo, scheduler.Diagnostics{Logf: logger.LegacyPrintf}).Check(ctx, &scheduler.RPMUser{ID: user.ID, RPMLimit: user.RPMLimit, UserGroupRPMOverride: user.UserGroupRPMOverride}, projected)
}

// WrapBillingEligibility 保留旧 RPM 编排，生产缓存运行时由 app 唯一提供。
func WrapBillingEligibility(core *billing.Eligibility, cfg *config.Config, rpm UserRPMCache, rates UserGroupRateRepository) *BillingCacheService {
	return &BillingCacheService{Eligibility: core, cfg: cfg, userRPMCache: rpm, userGroupRateRepo: rates}
}
