package completion

import (
	"context"

	"github.com/TokenFlux/TokenRouter/internal/billing"
)

func (s *Recorder) WriteUsage(ctx context.Context, usageLog *UsageLog, logKey string) {
	if s.logs == nil || usageLog == nil {
		return
	}
	applyClientModel(ctx, usageLog)
	usageCtx, cancel := detachedBillingContext(ctx)
	defer cancel()

	if writer, ok := s.logs.(BestEffortLogWriter); ok {
		if err := writer.CreateBestEffort(usageCtx, usageLog); err != nil {
			s.printf(logKey, "Create usage log failed: %v", err)
			// 已结算或待对账的用量事实都必须尽力落库：dropped（批处理队列超时）同样走同步兜底，
			// 否则会出现缺少 usage_log 的对账缺口；结算失败记录以 ActualCost=0 标识未实际扣费。
			// 重复写入由 usage_logs 的 ON CONFLICT (request_id, api_key_id) DO NOTHING 防护。
			fallbackCtx := usageCtx
			if usageCtx.Err() != nil {
				// usageCtx 已耗尽（best-effort 入队阻塞到期限）：换新的 detached 窗口，避免兜底必然失败。
				var fallbackCancel context.CancelFunc
				fallbackCtx, fallbackCancel = detachedBillingContext(context.Background())
				defer fallbackCancel()
			}
			if _, syncErr := s.logs.Create(fallbackCtx, usageLog); syncErr != nil {
				s.printf(logKey, "Create usage log sync fallback failed: %v", syncErr)
			}
		}
		return
	}

	if _, err := s.logs.Create(usageCtx, usageLog); err != nil {
		s.printf(logKey, "Create usage log failed: %v", err)
	}
}

// applyAccountStatsCost 保留原查价时机和账号基础成本与用户实扣的独立性。
func (s *Recorder) applyAccountStatsCost(ctx context.Context, row *UsageLog, accountID, groupID int64, upstream, requested, mapped string, tokens UsageTokens) {
	if upstream == "" {
		upstream = requested
	}
	count := 1
	if row.ImageCount > 0 {
		count = row.ImageCount
	}
	source := s.stats
	if s.resolver != nil {
		source = s.resolver
	}
	if source == nil {
		return
	}
	row.AccountStatsCost = source.ResolveAccountStats(ctx, billing.AccountStatsCostInput{AccountID: accountID, GroupID: groupID, UpstreamModel: upstream, RequestedModel: requested, MappedModel: mapped, Tokens: tokens, RequestCount: count, ServiceTier: stringValueOrEmpty(row.ServiceTier), ReasoningEffort: stringValueOrEmpty(row.ReasoningEffort)})
}
