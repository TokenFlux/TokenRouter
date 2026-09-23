package selection

import (
	"context"
	"log/slog"

	gatewayprovider "github.com/TokenFlux/TokenRouter/internal/gateway/provider"

	routing "github.com/TokenFlux/TokenRouter/internal/routing"
)

// checkChannelPricingRestriction 根据渠道计费基准检查模型是否受定价列表限制。
// 供调度阶段预检查（requested / channel_mapped）。
// upstream 需逐账号检查，此处返回 false。
func (s *Generic) checkChannelPricingRestriction(ctx context.Context, groupID *int64, requestedModel string) bool {
	if groupID == nil || s.channelService == nil || requestedModel == "" {
		return false
	}
	mapping := s.channelService.ResolveChannelMapping(ctx, *groupID, requestedModel)
	billingModel := routing.BillingModelForRestriction(mapping.BillingModelSource, requestedModel, mapping.MappedModel)
	if billingModel == "" {
		return false
	}
	return s.channelService.IsModelRestricted(ctx, *groupID, billingModel)
}

// isUpstreamModelRestrictedByChannel 检查账号映射后的上游模型是否受渠道定价限制。
// 仅在 BillingModelSource="upstream" 且 RestrictModels=true 时由调度循环调用。
func (s *Generic) isUpstreamModelRestrictedByChannel(ctx context.Context, groupID int64, account *gatewayprovider.ExecutionAccount, requestedModel string) bool {
	if s.channelService == nil {
		return false
	}
	routingModel := requestedModel
	if mapping := s.channelService.ResolveChannelMapping(ctx, groupID, requestedModel); mapping.Mapped {
		routingModel = mapping.MappedModel
	}
	upstreamModel := resolveAccountUpstreamModel(ctx, account, routingModel)
	if upstreamModel == "" {
		return false
	}
	return s.channelService.IsModelRestricted(ctx, groupID, upstreamModel)
}

// channelMappedModelForGroup 返回账号调度层使用的渠道映射后模型。
func (s *Generic) channelMappedModelForGroup(ctx context.Context, groupID *int64, requestedModel string) string {
	if s == nil {
		return requestedModel

	}
	return s.channelService.ResolveRoutingModel(ctx, groupID, requestedModel)
}

// needsUpstreamChannelRestrictionCheck 判断是否需要在调度循环中逐账号检查上游模型的渠道限制。
func (s *Generic) needsUpstreamChannelRestrictionCheck(ctx context.Context, groupID *int64) bool {
	if groupID == nil || s.channelService == nil {
		return false
	}
	ch, err := s.channelService.GetChannelForGroup(ctx, *groupID)
	if err != nil {
		slog.Warn("failed to check channel upstream restriction", "group_id", *groupID, "error", err)
		return false
	}
	if ch == nil || !ch.RestrictModels {
		return false
	}
	return ch.BillingModelSource == routing.BillingModelSourceUpstream
}
