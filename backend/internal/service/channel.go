// 本文件维护 service 的所属能力；兼容入口复用唯一实现。
package service

import (
	routing "github.com/TokenFlux/TokenRouter/internal/routing"
)

type BillingMode = routing.BillingMode

const BillingModeToken = routing.BillingModeToken

const BillingModePerRequest = routing.BillingModePerRequest

const BillingModeImage = routing.BillingModeImage

const BillingModeVideo = routing.BillingModeVideo

const BillingModelSourceRequested = routing.BillingModelSourceRequested

const BillingModelSourceUpstream = routing.BillingModelSourceUpstream

const BillingModelSourceChannelMapped = routing.BillingModelSourceChannelMapped

type Channel = routing.Channel

type AccountStatsPricingRule = routing.AccountStatsPricingRule

type ChannelModelPricing = routing.ChannelModelPricing

type ChannelTimePricing = routing.ChannelTimePricing

type ChannelTimePricingPeriod = routing.ChannelTimePricingPeriod

type PricingInterval = routing.PricingInterval

// FindMatchingInterval 委托所属模块的唯一实现。
func FindMatchingInterval(intervals []PricingInterval, totalTokens int) *PricingInterval {
	return routing.FindMatchingInterval(intervals, totalTokens)
}

// ValidateIntervals 委托所属模块的唯一实现。
func ValidateIntervals(intervals []PricingInterval, mode BillingMode) error {
	return routing.ValidateIntervals(intervals, mode)
}

type ChannelUsageFields = routing.ChannelUsageFields
