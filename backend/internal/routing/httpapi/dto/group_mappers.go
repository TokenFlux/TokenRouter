// 本文件维护 dto 的所属能力；兼容入口复用唯一实现。
package dto

import (
	protocol "github.com/TokenFlux/TokenRouter/internal/protocol"
	routing "github.com/TokenFlux/TokenRouter/internal/routing"
	policy "github.com/TokenFlux/TokenRouter/internal/scheduler/policy"
)

func GroupFromRoutingBase(g *routing.Group) Group {
	return Group{
		ID:                              g.ID,
		Name:                            g.Name,
		Description:                     g.Description,
		Platform:                        g.Platform,
		DisplayBrand:                    g.DisplayBrand,
		RateMultiplier:                  g.RateMultiplier,
		IsExclusive:                     g.IsExclusive,
		IsDefault:                       g.IsDefault,
		Status:                          g.Status,
		SessionIsolationEnabled:         g.SessionIsolationEnabled,
		LongContextPricingEnabled:       g.LongContextPricingEnabled,
		AllowImageGeneration:            g.AllowImageGeneration,
		AllowBatchImageGeneration:       g.AllowBatchImageGeneration,
		BatchImageDiscountMultiplier:    g.BatchImageDiscountMultiplier,
		BatchImageHoldMultiplier:        g.BatchImageHoldMultiplier,
		PeakRateEnabled:                 g.PeakRateEnabled,
		PeakStart:                       g.PeakStart,
		PeakEnd:                         g.PeakEnd,
		PeakRateMultiplier:              g.PeakRateMultiplier,
		WebSearchPricePerCall:           g.WebSearchPricePerCall,
		SearchPricePer1k:                g.SearchPricePer1k,
		AudioRealtimePricePerMin:        g.AudioRealtimePricePerMin,
		AudioTtsPricePerMillionChars:    g.AudioTTSPricePerMillionChars,
		AudioSttPricePerHour:            g.AudioSTTPricePerHour,
		ClaudeCodeOnly:                  g.ClaudeCodeOnly,
		FallbackGroupID:                 g.FallbackGroupID,
		FallbackGroupIDOnInvalidRequest: g.FallbackGroupIDOnInvalidRequest,
		UnavailableFallbackGroupID:      g.UnavailableFallbackGroupID,
		AllowedProtocols:                g.EffectiveAllowedProtocols(),
		ProtocolFallbacks:               g.ProtocolFallbacks,
		ResponsesImagePolicy:            g.ResponsesImagePolicy,
		AllowMessagesDispatch:           g.AllowsClientProtocol(protocol.ProtocolAnthropicMessages),
		AllowLive:                       g.AllowLive,
		RequireOAuthOnly:                g.RequireOAuthOnly,
		RequirePrivacySet:               g.RequirePrivacySet,
		RPMLimit:                        g.RPMLimit,
		MaxReasoningEffort:              g.MaxReasoningEffort,
		MaxReasoningEffortOverLimit:     g.MaxReasoningEffortOverLimit,
		ReasoningEffortMappings:         g.ReasoningEffortMappings,
		CreatedAt:                       g.CreatedAt,
		UpdatedAt:                       g.UpdatedAt,
	}
}

// GroupFromServiceAdmin converts a service Group to DTO for admin users.
// It includes internal fields like model_routing and account_count.
func AdminGroupFromRouting[A any](g *routing.Group) *AdminGroup[A] {
	if g == nil {
		return nil
	}
	out := &AdminGroup[A]{
		Group:                       GroupFromRoutingBase(g),
		ForceOpenAIFast:             g.ForceOpenAIFast,
		OpenAIFastPolicy:            g.EffectiveOpenAIFastPolicy(),
		FreeOpenAIFast:              g.FreeOpenAIFast,
		SchedulerType:               string(g.SchedulerType),
		AdvancedSchedulerOverrides:  policy.CloneGroupAdvancedSchedulerOverrides(g.AdvancedSchedulerOverrides),
		ModelPricing:                g.ModelPricing,
		ModelRouting:                g.ModelRouting,
		ModelRoutingEnabled:         g.ModelRoutingEnabled,
		MCPXMLInject:                g.MCPXMLInject,
		DefaultMappedModel:          g.DefaultMappedModel,
		MessagesDispatchModelConfig: g.MessagesDispatchModelConfig,
		ModelsListConfig:            g.ModelsListConfig,
		AvailabilityProbeConfig:     g.AvailabilityProbeConfig,
		SupportedModelScopes:        g.SupportedModelScopes,
		AccountCount:                g.AccountCount,
		ActiveAccountCount:          g.ActiveAccountCount,
		RateLimitedAccountCount:     g.RateLimitedAccountCount,
		SortOrder:                   g.SortOrder,
	}

	return out
}

// GroupFromRouting 不输出内部字段，保留用户与管理员 DTO 的字段边界。
func GroupFromRouting(g *routing.Group) *Group {
	if g == nil {
		return nil
	}
	out := GroupFromRoutingBase(g)
	return &out
}

// GroupCapacityFromSummary 仅投影既有容量字段，不改变 nil 与零值表示。
func GroupCapacityFromSummary(v *routing.GroupCapacitySummary) *GroupCapacity {
	if v == nil {
		return nil
	}
	return &GroupCapacity{ConcurrencyUsed: v.ConcurrencyUsed, ConcurrencyMax: v.ConcurrencyMax, SessionsUsed: v.SessionsUsed, SessionsMax: v.SessionsMax, RPMUsed: v.RPMUsed, RPMMax: v.RPMMax}
}
