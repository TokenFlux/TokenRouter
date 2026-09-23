package app

import (
	"github.com/TokenFlux/TokenRouter/internal/billing"
	"github.com/TokenFlux/TokenRouter/internal/config"
	"github.com/TokenFlux/TokenRouter/internal/gateway/admission"
)

// newFundingAdmissionFixture 复用原测试已创建的资金缓存，原夹具均未配置 RPM 后端。
func newFundingAdmissionFixture(funds *billing.Eligibility, cfg *config.Config) *admission.FundingAdmission {
	return admission.NewFundingAdmission(funds, nil, func() bool { return cfg != nil && cfg.RunMode == config.RunModeSimple })
}

func billingEligibilityFixtureOptions(c *config.Config) billing.EligibilityOptions {
	return billing.EligibilityOptions{RunMode: c.RunMode, Billing: billing.BillingOptions{MinimumBalanceReserve: c.Billing.MinimumBalanceReserve, UserPlatformQuotaCacheTTLSeconds: c.Billing.UserPlatformQuotaCacheTTLSeconds, UserPlatformQuotaSentinelTTLSeconds: c.Billing.UserPlatformQuotaSentinelTTLSeconds, CircuitBreaker: billing.CircuitBreakerOptions{Enabled: c.Billing.CircuitBreaker.Enabled, FailureThreshold: c.Billing.CircuitBreaker.FailureThreshold, ResetTimeoutSeconds: c.Billing.CircuitBreaker.ResetTimeoutSeconds, HalfOpenRequests: c.Billing.CircuitBreaker.HalfOpenRequests}}, Database: billing.QuotaMirrorOptions{UserPlatformQuotaFlusherEnabled: c.Database.UserPlatformQuotaFlusherEnabled}}
}

// newBillingEligibilityFixture 直接构造原资金缓存，保持配置读取及异步回填语义。
func newBillingEligibilityFixture(cfg *config.Config) *billing.Eligibility {
	return billing.NewEligibility(nil, nil, nil, nil, func() billing.EligibilityOptions { return billingEligibilityFixtureOptions(cfg) }, nil, billing.NewQuotaCoordinator(), func(_ string, fn func()) { go fn() })
}
