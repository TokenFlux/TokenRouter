// 兼容字段只在此转换，任务结算实现由 batchimage 唯一持有。
package service

import (
	"context"
	"time"

	"github.com/TokenFlux/TokenRouter/internal/batchimage"
	"github.com/TokenFlux/TokenRouter/internal/usage"
)

func (s *BatchImageSettlementService) nativeSettlement() *batchimage.Settlement {
	if s == nil {
		return nil
	}
	out := &batchimage.Settlement{Repo: s.Repo, Funding: batchFundingProjection(s.BillingRepo), Observe: creativeLegacyObserve}
	if s.Config != nil {
		out.Retention = time.Duration(s.Config.BatchImage.OutputRetentionAfterTerminalHours) * time.Hour
	}
	if s.Pricing != nil {
		out.Quote = func(ctx context.Context, model string, group *int64, size string) (float64, error) {
			return s.Pricing.BatchImageUnitPrice(ctx, BatchImagePriceInput{Model: model, GroupID: group, ImageSize: size})
		}
	}
	if s.AuthCache != nil {
		out.InvalidateAuth = s.AuthCache.InvalidateAuthCacheByUserID
	}
	if s.UsageLogRepo != nil {
		out.RecordUsage = func(ctx context.Context, v *usage.UsageLog) {
			writeUsageLogBestEffort(ctx, s.UsageLogRepo, UsageLogFromView(v), "service.batch_image_settlement")
		}
	}
	return out
}
