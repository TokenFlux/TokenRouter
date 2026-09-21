package apikey

import (
	"github.com/TokenFlux/TokenRouter/internal/billing/pricing"
	"github.com/TokenFlux/TokenRouter/internal/routing"
	"github.com/TokenFlux/TokenRouter/internal/routing/accessview"
)

// 分组配置值沿用其唯一所有者，Key 只持有独立的读取快照。
type GroupSchedulerType = accessview.GroupSchedulerType
type GroupAdvancedSchedulerOverrides = accessview.GroupAdvancedSchedulerOverrides
type OpenAIMessagesDispatchModelConfig = accessview.OpenAIMessagesDispatchModelConfig
type GroupModelsListConfig = accessview.GroupModelsListConfig
type GroupAvailabilityProbeConfig = accessview.GroupAvailabilityProbeConfig
type ReasoningEffortMapping = routing.ReasoningEffortMapping
type ChannelModelPricing = pricing.ChannelModelPricing
