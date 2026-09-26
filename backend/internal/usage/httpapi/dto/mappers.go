package dto

import (
	"github.com/TokenFlux/TokenRouter/internal/billing"
	billinghttpapi "github.com/TokenFlux/TokenRouter/internal/billing/httpapi"
	"github.com/TokenFlux/TokenRouter/internal/ops"
	"github.com/TokenFlux/TokenRouter/internal/usage"
)

func usageLogFromServiceUser(l *usage.UsageLog) UsageLog {
	// 普通用户 DTO：严禁包含管理员字段（例如 account_rate_multiplier、account、upstream_model）。
	requestType := l.EffectiveRequestType()
	stream, openAIWSMode := usage.ApplyLegacyRequestFields(requestType, l.Stream, l.OpenAIWSMode)
	requestedModel := l.RequestedModel
	if requestedModel == "" {
		requestedModel = l.Model
	}
	return UsageLog{
		ID:                        l.ID,
		UserID:                    l.UserID,
		TeamID:                    l.TeamID,
		APIKeyID:                  l.APIKeyID,
		AccountID:                 l.AccountID,
		RequestID:                 l.RequestID,
		Model:                     requestedModel,
		ServiceTier:               l.ServiceTier,
		ReasoningEffort:           l.ReasoningEffort,
		RequestedReasoningEffort:  l.RequestedReasoningEffort,
		InboundEndpoint:           l.InboundEndpoint,
		GroupID:                   l.GroupID,
		SubscriptionID:            l.SubscriptionID,
		InputTokens:               l.InputTokens,
		OutputTokens:              l.OutputTokens,
		CacheCreationTokens:       l.CacheCreationTokens,
		CacheReadTokens:           l.CacheReadTokens,
		CacheCreation5mTokens:     l.CacheCreation5mTokens,
		CacheCreation1hTokens:     l.CacheCreation1hTokens,
		InputCost:                 l.InputCost,
		OutputCost:                l.OutputCost,
		CacheCreationCost:         l.CacheCreationCost,
		CacheReadCost:             l.CacheReadCost,
		TotalCost:                 l.TotalCost,
		ActualCost:                l.ActualCost,
		SubscriptionAmountUSD:     l.SubscriptionAmountUSD,
		BalanceAmountUSD:          l.BalanceAmountUSD,
		BillingAllocations:        cloneBillingAllocationsDTO(l.BillingAllocations),
		RateMultiplier:            l.RateMultiplier,
		LongContextBillingApplied: l.LongContextBillingApplied,
		BillingType:               l.BillingType,
		RequestType:               requestType.String(),
		Stream:                    stream,
		OpenAIWSMode:              openAIWSMode,
		NativeCompactionV2:        l.NativeCompactionV2,
		DurationMs:                l.DurationMs,
		FirstTokenMs:              l.FirstTokenMs,
		ImageCount:                l.ImageCount,
		ImageSize:                 l.ImageSize,
		ImageInputSize:            l.ImageInputSize,
		ImageOutputSize:           l.ImageOutputSize,
		ImageInputTokens:          l.ImageInputTokens,
		ImageInputCost:            l.ImageInputCost,
		ImageOutputTokens:         l.ImageOutputTokens,
		ImageOutputCost:           l.ImageOutputCost,
		ImageSizeSource:           l.ImageSizeSource,
		ImageSizeBreakdown:        l.ImageSizeBreakdown,
		MediaType:                 l.MediaType,
		UserAgent:                 l.UserAgent,
		IPAddress:                 l.IPAddress,
		SessionID:                 l.SessionID,
		CacheTTLOverridden:        l.CacheTTLOverridden,
		BillingMode:               l.BillingMode,
		CreatedAt:                 l.CreatedAt,
		User:                      userFromView(l.User),
		APIKey:                    keyFromView(l.APIKey),
		Group:                     groupFromView(l.Group),
		Subscription:              billinghttpapi.UserSubscriptionFromService(l.Subscription),
	}
}

func cloneBillingAllocationsDTO(allocations []billing.BillingAllocation) []billing.BillingAllocation {
	if len(allocations) == 0 {
		return nil
	}
	out := make([]billing.BillingAllocation, 0, len(allocations))
	for i := range allocations {
		allocation := allocations[i]
		if allocation.SubscriptionID != nil {
			subscriptionID := *allocation.SubscriptionID
			allocation.SubscriptionID = &subscriptionID
		}
		if allocation.PlanID != nil {
			planID := *allocation.PlanID
			allocation.PlanID = &planID
		}
		out = append(out, allocation)
	}
	return out
}

// FromUsage converts a service UsageLog to DTO for regular users.
// FromUsage 转换普通用户可见的用量日志 DTO。
// 该 DTO 保留用户计费和请求元数据，但排除管理员专用的账号/上游内部字段。
func FromUsage(l *usage.UsageLog) *UsageLog {
	if l == nil {
		return nil
	}
	u := usageLogFromServiceUser(l)
	return &u
}

// FromUsageAdmin converts a service UsageLog to DTO for admin users.
// It includes minimal Account info (ID, Name only) and IP address.
func FromUsageAdmin(l *usage.UsageLog) *AdminUsageLog {
	if l == nil {
		return nil
	}
	usageLog := usageLogFromServiceUser(l)
	usageLog.UpstreamEndpoint = l.UpstreamEndpoint
	return &AdminUsageLog{
		UsageLog:              usageLog,
		UpstreamModel:         l.UpstreamModel,
		UpstreamRequestID:     l.UpstreamRequestID,
		PricingConfigID:       l.PricingConfigID,
		ModelMappingChain:     l.ModelMappingChain,
		BillingTier:           l.BillingTier,
		AccountRateMultiplier: l.AccountRateMultiplier,
		AccountStatsCost:      l.AccountStatsCost,
		IPAddress:             l.IPAddress,
		Account:               accountFromView(l.Account),
	}
}

// TimingFromOps 转换管理员使用记录需要展示的阶段耗时，避免暴露系统日志其它字段。
func TimingFromOps(timing *ops.OpsRequestTiming) *UsageLogTiming {
	if timing == nil {
		return nil
	}
	return &UsageLogTiming{
		RequestContentLength:           timing.RequestContentLength,
		AccountSlotAcquiredMs:          timing.AccountSlotAcquiredMs,
		UpstreamGetConnMs:              timing.UpstreamGetConnMs,
		UpstreamGotConnMs:              timing.UpstreamGotConnMs,
		UpstreamWroteRequestMs:         timing.UpstreamWroteRequestMs,
		UpstreamFirstResponseByteMs:    timing.UpstreamFirstResponseByteMs,
		UpstreamFirstSSEDataMs:         timing.UpstreamFirstSSEDataMs,
		FirstVisibleOutputMs:           timing.FirstVisibleOutputMs,
		FirstDownstreamFlushMs:         timing.FirstDownstreamFlushMs,
		UpstreamGetConnCount:           timing.UpstreamGetConnCount,
		UpstreamGotConnCount:           timing.UpstreamGotConnCount,
		UpstreamAttemptCount:           timing.UpstreamAttemptCount,
		UpstreamFirstResponseByteCount: timing.UpstreamFirstResponseByteCount,
		UpstreamConnectionReused:       timing.UpstreamConnectionReused,
		UpstreamWroteRequestError:      timing.UpstreamWroteRequestError,
	}
}

func CleanupFromUsage(task *usage.UsageCleanupTask) *UsageCleanupTask {
	if task == nil {
		return nil
	}
	return &UsageCleanupTask{
		ID:     task.ID,
		Status: task.Status,
		Filters: UsageCleanupFilters{
			StartTime:   task.Filters.StartTime,
			EndTime:     task.Filters.EndTime,
			UserID:      task.Filters.UserID,
			APIKeyID:    task.Filters.APIKeyID,
			AccountID:   task.Filters.AccountID,
			GroupID:     task.Filters.GroupID,
			Model:       task.Filters.Model,
			RequestType: RequestTypeStringPtr(task.Filters.RequestType),
			Stream:      task.Filters.Stream,
			BillingType: task.Filters.BillingType,
		},
		CreatedBy:    task.CreatedBy,
		DeletedRows:  task.DeletedRows,
		ErrorMessage: task.ErrorMsg,
		CanceledBy:   task.CanceledBy,
		CanceledAt:   task.CanceledAt,
		StartedAt:    task.StartedAt,
		FinishedAt:   task.FinishedAt,
		CreatedAt:    task.CreatedAt,
		UpdatedAt:    task.UpdatedAt,
	}
}

func RequestTypeStringPtr(requestType *int16) *string {
	if requestType == nil {
		return nil
	}
	value := usage.RequestTypeFromInt16(*requestType).String()
	return &value
}
