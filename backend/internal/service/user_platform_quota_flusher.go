// 本文件由 billing 拥有资金契约与规则；旧入口仅作过渡适配。
package service

import (
	billing "github.com/TokenFlux/TokenRouter/internal/billing"
	config "github.com/TokenFlux/TokenRouter/internal/config"
	logger "github.com/TokenFlux/TokenRouter/internal/pkg/logger"
)

type UserPlatformQuotaUsageFlusher = billing.UserPlatformQuotaUsageFlusher
type FlusherMetrics = billing.FlusherMetrics

// NewUserPlatformQuotaUsageFlusher 保留旧构造签名，生产构造改由 app 显式共享协调器。
func NewUserPlatformQuotaUsageFlusher(cfg *config.Config, cache BillingCache, repo UserPlatformQuotaRepository, tw *TimingWheelService) *UserPlatformQuotaUsageFlusher {
	var scheduler billing.QuotaScheduler
	if tw != nil {
		scheduler = tw
	}
	return billing.NewUserPlatformQuotaUsageFlusher(billing.FlusherOptions{UserPlatformQuotaFlushBatchSize: cfg.Database.UserPlatformQuotaFlushBatchSize, UserPlatformQuotaFlushIntervalMs: cfg.Database.UserPlatformQuotaFlushIntervalMs, UserPlatformQuotaFlusherEnabled: cfg.Database.UserPlatformQuotaFlusherEnabled}, cache, repo, scheduler, billing.NewQuotaCoordinator(), logger.LegacyPrintf)
}
