// 本文件维护 service 的所属能力；兼容入口复用唯一实现。
package service

import (
	routing "github.com/TokenFlux/TokenRouter/internal/routing"
)

// groupRules 只供立即执行的只读规则使用；对外快照经过 CloneGroup。
func groupRules(g *Group) *routing.Group {
	if g == nil {
		return nil
	}
	return &routing.Group{
		ID:                              g.ID,
		Name:                            g.Name,
		Description:                     g.Description,
		Platform:                        g.Platform,
		SchedulerType:                   g.SchedulerType,
		AdvancedSchedulerOverrides:      g.AdvancedSchedulerOverrides,
		DisplayBrand:                    g.DisplayBrand,
		RateMultiplier:                  g.RateMultiplier,
		PeakRateEnabled:                 g.PeakRateEnabled,
		PeakStart:                       g.PeakStart,
		PeakEnd:                         g.PeakEnd,
		PeakRateMultiplier:              g.PeakRateMultiplier,
		IsExclusive:                     g.IsExclusive,
		IsDefault:                       g.IsDefault,
		Status:                          g.Status,
		Hydrated:                        g.Hydrated,
		DuplicateOperationID:            g.DuplicateOperationID,
		SessionIsolationEnabled:         g.SessionIsolationEnabled,
		AllowImageGeneration:            g.AllowImageGeneration,
		AllowBatchImageGeneration:       g.AllowBatchImageGeneration,
		BatchImageDiscountMultiplier:    g.BatchImageDiscountMultiplier,
		BatchImageHoldMultiplier:        g.BatchImageHoldMultiplier,
		WebSearchPricePerCall:           g.WebSearchPricePerCall,
		SearchPricePer1k:                g.SearchPricePer1k,
		AudioRealtimePricePerMin:        g.AudioRealtimePricePerMin,
		AudioTTSPricePerMillionChars:    g.AudioTTSPricePerMillionChars,
		AudioSTTPricePerHour:            g.AudioSTTPricePerHour,
		LongContextPricingEnabled:       g.LongContextPricingEnabled,
		ModelPricing:                    g.ModelPricing,
		ClaudeCodeOnly:                  g.ClaudeCodeOnly,
		FallbackGroupID:                 g.FallbackGroupID,
		FallbackGroupIDOnInvalidRequest: g.FallbackGroupIDOnInvalidRequest,
		UnavailableFallbackGroupID:      g.UnavailableFallbackGroupID,
		ModelRouting:                    g.ModelRouting,
		ModelRoutingEnabled:             g.ModelRoutingEnabled,
		MCPXMLInject:                    g.MCPXMLInject,
		SupportedModelScopes:            g.SupportedModelScopes,
		SortOrder:                       g.SortOrder,
		AllowedProtocols:                g.AllowedProtocols,
		ProtocolFallbacks:               g.ProtocolFallbacks,
		ResponsesImagePolicy:            g.ResponsesImagePolicy,
		AllowMessagesDispatch:           g.AllowMessagesDispatch,
		AllowLive:                       g.AllowLive,
		ForceOpenAIFast:                 g.ForceOpenAIFast,
		OpenAIFastPolicy:                g.OpenAIFastPolicy,
		FreeOpenAIFast:                  g.FreeOpenAIFast,
		RequireOAuthOnly:                g.RequireOAuthOnly,
		RequirePrivacySet:               g.RequirePrivacySet,
		DefaultMappedModel:              g.DefaultMappedModel,
		MessagesDispatchModelConfig:     g.MessagesDispatchModelConfig,
		ModelsListConfig:                g.ModelsListConfig,
		AvailabilityProbeConfig:         g.AvailabilityProbeConfig,
		RPMLimit:                        g.RPMLimit,
		MaxReasoningEffort:              g.MaxReasoningEffort,
		MaxReasoningEffortOverLimit:     g.MaxReasoningEffortOverLimit,
		ReasoningEffortMappings:         g.ReasoningEffortMappings,
		CreatedAt:                       g.CreatedAt,
		UpdatedAt:                       g.UpdatedAt,
		AccountCount:                    g.AccountCount,
		ActiveAccountCount:              g.ActiveAccountCount,
		RateLimitedAccountCount:         g.RateLimitedAccountCount,
	}
}

// RoutingGroupView 隔离旧配置对象与新模块接收到的快照。
func RoutingGroupView(g *Group) *routing.Group { return routing.CloneGroup(groupRules(g)) }
func GroupFromRouting(value *routing.Group) *Group {
	if value == nil {
		return nil
	}
	out := &Group{}
	ApplyRoutingGroup(out, value)
	return out
}

// ApplyRoutingGroup 更新配置字段，不覆盖旧调用方临时持有的递归关联展示。
func ApplyRoutingGroup(out *Group, value *routing.Group) {
	if out == nil || value == nil {
		return
	}
	value = routing.CloneGroup(value)
	out.ID = value.ID
	out.Name = value.Name
	out.Description = value.Description
	out.Platform = value.Platform
	out.SchedulerType = value.SchedulerType
	out.AdvancedSchedulerOverrides = value.AdvancedSchedulerOverrides
	out.DisplayBrand = value.DisplayBrand
	out.RateMultiplier = value.RateMultiplier
	out.PeakRateEnabled = value.PeakRateEnabled
	out.PeakStart = value.PeakStart
	out.PeakEnd = value.PeakEnd
	out.PeakRateMultiplier = value.PeakRateMultiplier
	out.IsExclusive = value.IsExclusive
	out.IsDefault = value.IsDefault
	out.Status = value.Status
	out.Hydrated = value.Hydrated
	out.DuplicateOperationID = value.DuplicateOperationID
	out.SessionIsolationEnabled = value.SessionIsolationEnabled
	out.AllowImageGeneration = value.AllowImageGeneration
	out.AllowBatchImageGeneration = value.AllowBatchImageGeneration
	out.BatchImageDiscountMultiplier = value.BatchImageDiscountMultiplier
	out.BatchImageHoldMultiplier = value.BatchImageHoldMultiplier
	out.WebSearchPricePerCall = value.WebSearchPricePerCall
	out.SearchPricePer1k = value.SearchPricePer1k
	out.AudioRealtimePricePerMin = value.AudioRealtimePricePerMin
	out.AudioTTSPricePerMillionChars = value.AudioTTSPricePerMillionChars
	out.AudioSTTPricePerHour = value.AudioSTTPricePerHour
	out.LongContextPricingEnabled = value.LongContextPricingEnabled
	out.ModelPricing = value.ModelPricing
	out.ClaudeCodeOnly = value.ClaudeCodeOnly
	out.FallbackGroupID = value.FallbackGroupID
	out.FallbackGroupIDOnInvalidRequest = value.FallbackGroupIDOnInvalidRequest
	out.UnavailableFallbackGroupID = value.UnavailableFallbackGroupID
	out.ModelRouting = value.ModelRouting
	out.ModelRoutingEnabled = value.ModelRoutingEnabled
	out.MCPXMLInject = value.MCPXMLInject
	out.SupportedModelScopes = value.SupportedModelScopes
	out.SortOrder = value.SortOrder
	out.AllowedProtocols = value.AllowedProtocols
	out.ProtocolFallbacks = value.ProtocolFallbacks
	out.ResponsesImagePolicy = value.ResponsesImagePolicy
	out.AllowMessagesDispatch = value.AllowMessagesDispatch
	out.AllowLive = value.AllowLive
	out.ForceOpenAIFast = value.ForceOpenAIFast
	out.OpenAIFastPolicy = value.OpenAIFastPolicy
	out.FreeOpenAIFast = value.FreeOpenAIFast
	out.RequireOAuthOnly = value.RequireOAuthOnly
	out.RequirePrivacySet = value.RequirePrivacySet
	out.DefaultMappedModel = value.DefaultMappedModel
	out.MessagesDispatchModelConfig = value.MessagesDispatchModelConfig
	out.ModelsListConfig = value.ModelsListConfig
	out.AvailabilityProbeConfig = value.AvailabilityProbeConfig
	out.RPMLimit = value.RPMLimit
	out.MaxReasoningEffort = value.MaxReasoningEffort
	out.MaxReasoningEffortOverLimit = value.MaxReasoningEffortOverLimit
	out.ReasoningEffortMappings = value.ReasoningEffortMappings
	out.CreatedAt = value.CreatedAt
	out.UpdatedAt = value.UpdatedAt
	out.AccountCount = value.AccountCount
	out.ActiveAccountCount = value.ActiveAccountCount
	out.RateLimitedAccountCount = value.RateLimitedAccountCount
}

// GroupsFromRouting 保留旧 API 的集合形状，逐项复制可变配置。
func GroupsFromRouting(values []routing.Group) []Group {
	if values == nil {
		return nil
	}
	out := make([]Group, len(values))
	for i := range values {
		out[i] = *GroupFromRouting(&values[i])
	}
	return out
}
