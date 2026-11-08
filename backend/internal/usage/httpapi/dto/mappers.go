package dto

import (
	"github.com/TokenFlux/TokenRouter/internal/billing"
	billinghttpapi "github.com/TokenFlux/TokenRouter/internal/billing/httpapi"
	"github.com/TokenFlux/TokenRouter/internal/usage"
)

// mappers.go maps usage records and cleanup tasks into user and administrator
// HTTP responses.
// Usage detail responses display the client model while administrator
// responses add outbound observations and provider costs. Request categories
// also supply older transport flags, so converting a record resolves those
// representations before filling the response.
//
// The mappers keep absent observations absent and restrict administrator
// additions to AdminUsageLog. Billing allocations receive their own entries
// and copied subscription and plan identifiers. TTFT, TPS and detailed timing
// use copied scalar values derived from the usage snapshot. Other optional log
// values and image-size maps retain their source references, which callers
// treat as immutable while a response is being encoded.

// usageLogFromServiceUser builds the common usage response from a nonnil
// record and optional display language. It selects RequestedModel when
// present, otherwise Model, and resolves the effective request category
// before deriving the legacy stream and WebSocket flags.
//
// These choices let client-facing names and older transport fields remain
// useful together. Billing allocations are copied by
// cloneBillingAllocationsDTO; other optional pointers and ImageSizeBreakdown
// remain shared with l. Administrator additions are supplied later by
// FromUsageAdmin.
func usageLogFromServiceUser(l *usage.UsageLog, language ...string) UsageLog {
	requestType := l.EffectiveRequestType()
	stream, openAIWSMode := usage.ApplyLegacyRequestFields(requestType, l.Stream, l.OpenAIWSMode)
	requestedModel := l.RequestedModel
	// Historical records can lack the client display name. Use their stored model
	// so both user and administrator responses still identify the recorded request.
	if requestedModel == "" {
		requestedModel = l.Model
	}
	return UsageLog{
		ID:                        l.ID,
		UserID:                    positiveUsageID(l.UserID),
		TeamID:                    l.TeamID,
		APIKeyID:                  positiveUsageID(l.APIKeyID),
		ProviderID:                l.ProviderID,
		Platform:                  l.Platform,
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
		TTFTMs:                    l.Timings.TTFTMs(),
		TPS:                       l.Timings.TPS(l.OutputTokens),
		TTFTMode:                  recordedTimingMode(l),
		ImageCount:                l.ImageCount,
		ImageFrameCount:           l.ImageFrameCount,
		ImageSize:                 l.ImageSize,
		ImageInputSize:            l.ImageInputSize,
		ImageOutputSize:           l.ImageOutputSize,
		ImageInputTokens:          l.ImageInputTokens,
		ImageCacheReadTokens:      l.ImageCacheReadTokens,
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
		APIKey:                    keyFromView(l.APIKey, language...),
		Group:                     groupFromView(l.Group, language...),
		Subscription:              billinghttpapi.UserSubscriptionFromService(l.Subscription, language...),
	}
}

// cloneBillingAllocationsDTO copies allocation entries and their optional
// SubscriptionID and PlanID values. Empty input returns nil, while each
// populated identifier gets independent storage for the response's billing
// details.
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

// FromUsage builds the regular user's usage response, returning nil for a nil
// record. The response retains optional log pointers and the image-size map;
// callers treat those shared values as immutable during encoding.
func FromUsage(l *usage.UsageLog, language ...string) *UsageLog {
	if l == nil {
		return nil
	}
	u := usageLogFromServiceUser(l, language...)
	return &u
}

// FromUsageAdmin builds an administrator response, returning nil for a nil
// record. It adds outbound observations, provider costs and the provider
// identity; optional source pointers remain shared during response encoding.
func FromUsageAdmin(l *usage.UsageLog) *AdminUsageLog {
	if l == nil {
		return nil
	}
	usageLog := usageLogFromServiceUser(l)
	usageLog.UpstreamEndpoint = l.UpstreamEndpoint
	return &AdminUsageLog{
		RequestSource:          l.EffectiveRequestSource(),
		SystemUsage:            l.SystemUsage,
		UsageLog:               usageLog,
		DetailedTiming:         TimingFromUsage(l),
		UpstreamModel:          l.UpstreamModel,
		UpstreamResponseModel:  l.UpstreamResponseModel,
		UpstreamModelMismatch:  l.UpstreamModelMismatch,
		UpstreamRequestID:      l.UpstreamRequestID,
		PricingConfigID:        l.PricingConfigID,
		ModelMappingChain:      l.ModelMappingChain,
		BillingTier:            l.BillingTier,
		ProviderRateMultiplier: l.ProviderRateMultiplier,
		ProviderStatsCost:      l.ProviderStatsCost,
		IPAddress:              l.IPAddress,
		Provider:               providerFromView(l.Provider),
	}
}

// CleanupFromUsage builds a cleanup-task response, returning nil for a nil
// task. It maps the DTO's declared filter fields and converts request type to
// text; other filter and lifecycle pointers remain shared.
func CleanupFromUsage(task *usage.UsageCleanupTask) *UsageCleanupTask {
	if task == nil {
		return nil
	}
	return &UsageCleanupTask{
		ID:     task.ID,
		Status: task.Status,
		Filters: UsageCleanupFilters{
			RequestSource: task.Filters.RequestSource,
			StartTime:     task.Filters.StartTime,
			EndTime:       task.Filters.EndTime,
			UserID:        task.Filters.UserID,
			APIKeyID:      task.Filters.APIKeyID,
			ProviderID:    task.Filters.ProviderID,
			GroupID:       task.Filters.GroupID,
			Model:         task.Filters.Model,
			RequestType:   RequestTypeStringPtr(task.Filters.RequestType),
			Stream:        task.Filters.Stream,
			BillingType:   task.Filters.BillingType,
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

// RequestTypeStringPtr returns a new display-string pointer for a stored
// request category. Nil input returns nil, while invalid stored integers
// normalize to unknown before their lowercase label is produced.
func RequestTypeStringPtr(requestType *int16) *string {
	if requestType == nil {
		return nil
	}
	value := usage.RequestTypeFromInt16(*requestType).String()
	return &value
}

// TimingFromUsage presents only measurements retained by this usage record.
// Every returned pointer owns a copied scalar; absent observations remain absent
// and cannot fall back to unrelated access-log or historical SSE values.
func TimingFromUsage(log *usage.UsageLog) *UsageLogTiming {
	if log == nil || log.Timings == nil {
		return nil
	}
	p := log.Timings
	value := func(key string) *int64 {
		v, ok := p.Values[key]
		if !ok {
			return nil
		}
		return &v
	}
	flag := func(key string) *bool {
		v, ok := p.Values[key]
		if !ok {
			return nil
		}
		result := v == 1
		return &result
	}
	return &UsageLogTiming{
		UpstreamStream:                 p.Stream,
		UpstreamComplete:               p.Complete,
		UsageComplete:                  p.UsageComplete,
		UpstreamFinishedMs:             value("upstream_finished_ms"),
		UpstreamStartedMs:              value("upstream_started_ms"),
		UpstreamFirstOutputMs:          value("upstream_first_output_ms"),
		UpstreamLastOutputMs:           value("upstream_last_output_ms"),
		UpstreamCompletedMs:            value("upstream_completed_ms"),
		AttemptNumber:                  value("attempt_number"),
		RequestContentLength:           value("request_content_length"),
		TimingStream:                   value("timing_stream"),
		TimingAdmitted:                 value("timing_admitted"),
		RequestBodyReadMs:              value("request_body_read_ms"),
		PreprocessMs:                   value("preprocess_ms"),
		UserSlotWaitMs:                 value("user_slot_wait_ms"),
		ProviderSlotWaitMs:             value("provider_slot_wait_ms"),
		RetryProviderSlotWaitMs:        value("retry_provider_slot_wait_ms"),
		ProviderSlotAcquiredMs:         value("provider_slot_acquired_ms"),
		UpstreamGetConnMs:              value("upstream_get_conn_ms"),
		UpstreamGotConnMs:              value("upstream_got_conn_ms"),
		UpstreamWroteRequestMs:         value("upstream_wrote_request_ms"),
		UpstreamFirstResponseByteMs:    value("upstream_first_response_byte_ms"),
		UpstreamFirstEventMs:           value("upstream_first_event_ms"),
		FirstVisibleOutputMs:           value("first_visible_output_ms"),
		FirstDownstreamFlushMs:         value("first_downstream_flush_ms"),
		UpstreamGetConnCount:           value("upstream_get_conn_count"),
		UpstreamGotConnCount:           value("upstream_got_conn_count"),
		UpstreamAttemptCount:           value("upstream_attempt_count"),
		UpstreamFirstResponseByteCount: value("upstream_first_response_byte_count"),
		UpstreamConnectionReused:       flag("upstream_connection_reused"),
		UpstreamWroteRequestError:      flag("upstream_wrote_request_error"),
	}
}

// recordedTimingMode reports the captured policy without inventing historical defaults.
func recordedTimingMode(log *usage.UsageLog) string {
	if log == nil || log.Timings == nil {
		return ""
	}
	return log.Timings.Mode
}

// positiveUsageID preserves real customer identities and exposes absent system
// attribution as JSON null. Positive identifiers receive a new pointer, while
// zero and negative values return nil instead of representing an account.
func positiveUsageID(id int64) *int64 {
	if id <= 0 {
		return nil
	}
	return &id
}
