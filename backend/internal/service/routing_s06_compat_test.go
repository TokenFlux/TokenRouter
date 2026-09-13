//go:build unit

// 本文件维护 service 的所属能力；兼容入口复用唯一实现。
package service

import (
	context "context"
	provider "github.com/TokenFlux/TokenRouter/internal/billing/provider"
	domain "github.com/TokenFlux/TokenRouter/internal/domain"
	routing "github.com/TokenFlux/TokenRouter/internal/routing"
)

func (s *adminServiceImpl) validateFallbackGroup(ctx context.Context, currentGroupID, fallbackGroupID int64) error {
	return s.routingAdmin.ValidateFallbackGroup(ctx, currentGroupID, fallbackGroupID)
}

func normalizeGroupModelPricing(platform string, pricing []ChannelModelPricing) ([]ChannelModelPricing, error) {
	return (routing.ChannelValidation{LoadLocation: loadChannelTimePricingLocation}).NormalizeGroupPricing(platform, pricing)
}

const maxGroupNameRunes = routing.MaxGroupNameRunes

const duplicateGroupInactiveStatus = routing.DuplicateGroupInactiveStatus

func duplicateGroupName(sourceName string, copyNumber int) string {
	return routing.DuplicateGroupName(sourceName, copyNumber)
}

func cloneGroupValuePointer[T any](value *T) *T { return routing.CloneGroupValuePointer(value) }

func cloneGroupModelRouting(value map[string][]int64) map[string][]int64 {
	return routing.CloneGroupModelRouting(value)
}

func cloneGroupMessagesDispatchModelConfig(value OpenAIMessagesDispatchModelConfig) OpenAIMessagesDispatchModelConfig {
	return routing.CloneGroupMessagesDispatchModelConfig(value)
}

func cloneGroupForDuplicate(source *Group, operationID string) *Group {
	return GroupFromRouting(routing.CloneGroupForDuplicate(groupRules(source), operationID))
}

func cloneGroupClientProtocols(protocols []domain.ProtocolID) []domain.ProtocolID {
	return routing.CloneGroupClientProtocols(protocols)
}

func (s *adminServiceImpl) advancedSchedulerGlobalWeightsForValidation(ctx context.Context) (GatewayAdvancedSchedulerScoreWeightsView, error) {
	return GroupValidationWeights(ctx, s.settingService)
}

func validatePricingEntries(pricing []ChannelModelPricing) error {
	return (routing.ChannelValidation{LoadLocation: provider.LoadPricingLocation}).PricingEntries(pricing)
}

func validateChannelTimePricing(config *ChannelTimePricing) error {
	return (routing.ChannelValidation{LoadLocation: provider.LoadPricingLocation}).TimePricing(config)
}

func groupAccountViews(accounts []Account) []routing.GroupAccount {
	if accounts == nil {
		return nil
	}
	out := make([]routing.GroupAccount, len(accounts))
	for i := range accounts {
		out[i] = routing.GroupAccount{ID: accounts[i].ID, Platform: accounts[i].Platform, Type: accounts[i].Type, Models: accounts[i].GetConfiguredRequestModels()}
	}
	return out
}

func checkBillingModeRequirements(p ChannelModelPricing) error {
	return routing.CheckBillingModeRequirements(p)
}
