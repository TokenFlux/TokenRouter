package selection

import (
	"context"
	"log/slog"
	"time"

	"github.com/TokenFlux/TokenRouter/internal/gateway/requeststate"

	gatewayprovider "github.com/TokenFlux/TokenRouter/internal/gateway/provider"

	"github.com/TokenFlux/TokenRouter/internal/routing"
)

const openaiStickySessionTTL = time.Hour // 粘性会话TTL

func (s *Compatible) CheckChannelPricingRestriction(ctx context.Context, groupID *int64, requestedModel string) bool {
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

// resolveChannelRoutingModel 返回 OpenAI 账号调度层使用的渠道映射后模型。
func (s *Compatible) resolveChannelRoutingModel(ctx context.Context, groupID *int64, requestedModel string) string {
	if s == nil {
		return requestedModel

	}
	return s.channelService.ResolveRoutingModel(ctx, groupID,
		requestedModel,
	)
}

// isUpstreamRoutingModelRestrictedByChannel 使用已经完成渠道及分组映射的账号层模型检查最终上游模型。
func (s *Compatible) UpstreamRoutingModelRestricted(ctx context.Context, groupID int64, account *gatewayprovider.ExecutionAccount, routingModel string, requireCompact bool) bool {
	if s.channelService == nil {
		return false
	}
	upstreamModel := gatewayprovider.ExecutionModelPolicy(account).OpenAIUpstream(

		routingModel,
		requireCompact,
		requeststate.OpenAIHTTPPassthroughRoutingFromContext(ctx),
	)
	if upstreamModel == "" {
		return false
	}
	return s.channelService.IsModelRestricted(ctx, groupID, upstreamModel)
}

func (s *Compatible) NeedsUpstreamChannelRestriction(ctx context.Context, groupID *int64) bool {
	if groupID == nil || s.channelService == nil {
		return false
	}
	ch, err := s.channelService.GetChannelForGroup(ctx, *groupID)
	if err != nil {
		slog.Warn("failed to check openai channel upstream restriction", "group_id", *groupID, "error", err)
		return false
	}
	if ch == nil || !ch.RestrictModels {
		return false
	}
	return ch.BillingModelSource == routing.BillingModelSourceUpstream
}
