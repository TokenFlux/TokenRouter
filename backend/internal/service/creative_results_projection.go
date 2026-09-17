// 旧实体形状只在此转换，结果用例保持无旧服务依赖。
package service

import (
	"context"

	"github.com/TokenFlux/TokenRouter/internal/billing"

	"github.com/TokenFlux/TokenRouter/internal/creative"
	"github.com/TokenFlux/TokenRouter/internal/pkg/logger"
	"github.com/TokenFlux/TokenRouter/internal/usage"
	"go.uber.org/zap"
)

func creativeLegacyObserve(event string, values ...any) {
	fields := make([]zap.Field, 0, len(values)/2)
	for i := 0; i+1 < len(values); i += 2 {
		key, _ := values[i].(string)
		fields = append(fields, zap.Any(key, values[i+1]))
	}
	logger.L().Warn(event, fields...)
}
func (s *CreativePublicService) nativeResults() *creative.Results {
	if s != nil && s.Core != nil {
		return s.Core.Results
	}
	if s == nil {
		return nil
	}
	result := &creative.Results{Repo: s.Repo, TransientStore: s.TransientStore, Queue: s.Queue, Outbox: s.Outbox, Funding: creativeFundingProjection(s.BillingRepo), TransientTTL: s.transientTTL(), Observe: creativeLegacyObserve}
	if s.AuthCache != nil {
		result.InvalidateAuth = s.AuthCache.InvalidateAuthCacheByUserID
	}
	if s.UsageLogRepo != nil {
		result.RecordUsage = func(ctx context.Context, log *usage.UsageLog) {
			writeUsageLogBestEffort(ctx, s.UsageLogRepo, UsageLogFromView(log), "service.creative_settlement")
		}
	}
	return result
}

func (s *CreativePublicService) nativeQueries() *creative.Queries {
	if s == nil {
		return nil
	}
	return &creative.Queries{Repo: s.Repo, TransientStore: s.TransientStore, Enabled: s.enabled, Observe: creativeLegacyObserve}
}
func creativeFundingProjection(repo UsageBillingRepository) creative.Funding {
	result := creative.Funding{Observe: creativeLegacyObserve}
	if source, ok := repo.(interface{ BillingFunds() *billing.Funds }); ok {
		result.Store = source.BillingFunds()
		return result
	}
	if repo != nil {
		result.Store = creativeLegacyFunds{repo}
	}
	return result
}
