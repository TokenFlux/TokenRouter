//go:build unit

// 本文件维护 service 的所属能力；兼容入口复用唯一实现。
package service

import (
	provider "github.com/TokenFlux/TokenRouter/internal/billing/provider"

	routing "github.com/TokenFlux/TokenRouter/internal/routing"
)

func normalizeGroupModelPricing(platform string, pricing []routing.ChannelModelPricing) ([]routing.ChannelModelPricing, error) {
	return (routing.ChannelValidation{LoadLocation: provider.LoadPricingLocation}).NormalizeGroupPricing(platform, pricing)
}

func cloneGroupForDuplicate(source *routing.Group, operationID string) *routing.Group {
	return routing.CloneGroup(routing.CloneGroupForDuplicate(source, operationID))
}

func validatePricingEntries(pricing []routing.ChannelModelPricing) error {
	return (routing.ChannelValidation{LoadLocation: provider.LoadPricingLocation}).PricingEntries(pricing)
}

func validateChannelTimePricing(config *routing.ChannelTimePricing) error {
	return (routing.ChannelValidation{LoadLocation: provider.LoadPricingLocation}).TimePricing(config)
}

func checkBillingModeRequirements(p routing.ChannelModelPricing) error {
	return routing.CheckBillingModeRequirements(p)
}
