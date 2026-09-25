// 本文件维护 accessview 的所属能力；兼容入口复用唯一实现。
package accessview

import (
	"maps"
	"slices"

	"github.com/TokenFlux/TokenRouter/internal/billing/pricing"
)

// CloneGroup 复制跨缓存/模块边界的可变值，保留省略与显式空集合。
func CloneGroupConfig(g *GroupConfig) *GroupConfig {
	if g == nil {
		return nil
	}
	out := *g
	out.WebSearchPricePerCall = cloneGroupPointer(g.WebSearchPricePerCall)
	out.SearchPricePer1k = cloneGroupPointer(g.SearchPricePer1k)
	out.AudioRealtimePricePerMin = cloneGroupPointer(g.AudioRealtimePricePerMin)
	out.AudioTTSPricePerMillionChars = cloneGroupPointer(g.AudioTTSPricePerMillionChars)
	out.AudioSTTPricePerHour = cloneGroupPointer(g.AudioSTTPricePerHour)
	if g.ModelPricing != nil {
		out.ModelPricing = make([]pricing.ChannelModelPricing, len(g.ModelPricing))
		for i := range g.ModelPricing {
			out.ModelPricing[i] = g.ModelPricing[i].Clone()
		}
	}
	out.FallbackGroupID = cloneGroupPointer(g.FallbackGroupID)
	out.FallbackGroupIDOnInvalidRequest = cloneGroupPointer(g.FallbackGroupIDOnInvalidRequest)
	out.UnavailableFallbackGroupID = cloneGroupPointer(g.UnavailableFallbackGroupID)
	out.ModelRouting = maps.Clone(g.ModelRouting)
	for key, value := range out.ModelRouting {
		out.ModelRouting[key] = slices.Clone(value)
	}
	out.SupportedModelScopes = slices.Clone(g.SupportedModelScopes)
	out.AllowedProtocols = slices.Clone(g.AllowedProtocols)
	out.ProtocolFallbacks = maps.Clone(g.ProtocolFallbacks)
	out.ReasoningEffortMappings = slices.Clone(g.ReasoningEffortMappings)
	out.AdvancedSchedulerOverrides = CloneGroupAdvancedSchedulerOverrides(g.AdvancedSchedulerOverrides)
	out.MessagesDispatchModelConfig.ExactModelMappings = maps.Clone(g.MessagesDispatchModelConfig.ExactModelMappings)
	out.ModelsListConfig.Models = slices.Clone(g.ModelsListConfig.Models)
	out.AvailabilityProbeConfig.MaxRetries = cloneGroupPointer(g.AvailabilityProbeConfig.MaxRetries)
	return &out
}
func cloneGroupPointer[T any](value *T) *T {
	if value == nil {
		return nil
	}
	out := *value
	return &out
}
