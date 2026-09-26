package apikey

import (
	"github.com/TokenFlux/TokenRouter/internal/billing/pricing"
	"github.com/TokenFlux/TokenRouter/internal/routing"
	"github.com/TokenFlux/TokenRouter/internal/routing/accessview"
)

// 分组配置值沿用其唯一所有者，Key 只持有独立的读取快照。
type (
	GroupSchedulerType                = accessview.GroupSchedulerType
	GroupAdvancedSchedulerOverrides   = accessview.GroupAdvancedSchedulerOverrides
	OpenAIMessagesDispatchModelConfig = accessview.OpenAIMessagesDispatchModelConfig
	GroupModelsListConfig             = accessview.GroupModelsListConfig
	GroupAvailabilityProbeConfig      = accessview.GroupAvailabilityProbeConfig
	ReasoningEffortMapping            = routing.ReasoningEffortMapping
	ModelPricingEntry                 = pricing.ModelPricingEntry
)
