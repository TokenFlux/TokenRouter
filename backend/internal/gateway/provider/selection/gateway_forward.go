package selection

import (
	"context"
	"log/slog"

	gatewayprovider "github.com/TokenFlux/TokenRouter/internal/gateway/provider"

	"github.com/TokenFlux/TokenRouter/internal/routing"
)

// checkGroupModelRestriction 根据分组白名单检查阶段检查模型是否受独立白名单限制。
// 供调度阶段预检查（requested / group_mapped）。
// upstream 需逐账号检查，此处返回 false。
func (s *Generic) checkGroupModelRestriction(ctx context.Context, groupID *int64, requestedModel string) bool {
	if groupID == nil || s.groupPolicies == nil || requestedModel == "" {
		return false
	}
	mapping := s.groupPolicies.ResolveGroupMapping(ctx, *groupID, requestedModel)
	billingModel := routing.ModelForRestriction(mapping.RestrictionModelSource, requestedModel, mapping.MappedModel)
	if billingModel == "" {
		return false
	}
	return s.groupPolicies.IsModelRestricted(ctx, *groupID, billingModel)
}

// isUpstreamModelRestrictedByGroup 检查账号映射后的上游模型是否受分组白名单限制。
// 仅在 RestrictionModelSource="upstream" 且 RestrictModels=true 时由调度循环调用。
func (s *Generic) isUpstreamModelRestrictedByGroup(ctx context.Context, groupID int64, account *gatewayprovider.ExecutionAccount, requestedModel string) bool {
	if s.groupPolicies == nil {
		return false
	}
	routingModel := requestedModel
	if mapping := s.groupPolicies.ResolveGroupMapping(ctx, groupID, requestedModel); mapping.Mapped {
		routingModel = mapping.MappedModel
	}
	upstreamModel := resolveAccountUpstreamModel(ctx, account, routingModel)
	if upstreamModel == "" {
		return false
	}
	return s.groupPolicies.IsModelRestricted(ctx, groupID, upstreamModel)
}

// groupMappedModelForGroup 返回账号调度层使用的分组映射后模型。
func (s *Generic) groupMappedModelForGroup(ctx context.Context, groupID *int64, requestedModel string) string {
	if s == nil {
		return requestedModel
	}
	return s.groupPolicies.ResolveRoutingModel(ctx, groupID, requestedModel)
}

// needsUpstreamGroupRestrictionCheck 判断是否需要在调度循环中逐账号检查上游模型的分组白名单。
func (s *Generic) needsUpstreamGroupRestrictionCheck(ctx context.Context, groupID *int64) bool {
	if groupID == nil || s.groupPolicies == nil {
		return false
	}
	ch, err := s.groupPolicies.GetGroupPolicy(ctx, *groupID)
	if err != nil {
		slog.Warn("failed to check group upstream model restriction", "group_id", *groupID, "error", err)
		return false
	}
	if ch == nil || !ch.RestrictModels {
		return false
	}
	return ch.RestrictionSource() == routing.BillingModelSourceUpstream
}
