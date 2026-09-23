package service

import (
	"context"
	"log/slog"
	"time"

	accountcore "github.com/TokenFlux/TokenRouter/internal/account"
	accountprovider "github.com/TokenFlux/TokenRouter/internal/account/provider"

	openaiprotocol "github.com/TokenFlux/TokenRouter/internal/protocol/openai"

	forwardcore "github.com/TokenFlux/TokenRouter/internal/gateway/forward"
	"github.com/TokenFlux/TokenRouter/internal/gateway/modeltrace"
	gatewayprovider "github.com/TokenFlux/TokenRouter/internal/gateway/provider"
	"github.com/TokenFlux/TokenRouter/internal/gateway/requeststate"

	routing "github.com/TokenFlux/TokenRouter/internal/routing"

	"github.com/gin-gonic/gin"
)

// 重试相关常量
const (
	// 最大尝试次数（包含首次请求）。过多重试会导致请求堆积与资源耗尽。
	maxRetryAttempts = 5

	// 最大重试耗时（包含请求本身耗时 + 退避等待时间）。
	// 用于防止极端情况下 goroutine 长时间堆积导致资源耗尽。
	maxRetryElapsed = 10 * time.Second
)

// 兼容入口委托原生单账号重试规则。
func (s *GatewayService) shouldRetryUpstreamError(account *gatewayprovider.ExecutionAccount, statusCode int) bool {
	return forwardcore.ShouldRetry(account.View().IsOAuth(), statusCode)
}
func (s *GatewayService) shouldFailoverUpstreamError(statusCode int) bool {
	return forwardcore.ShouldFailover(statusCode)
}

// Forward 转发请求到Claude API
func (s *GatewayService) Forward(ctx context.Context, c *gin.Context, account *gatewayprovider.ExecutionAccount, parsed *requeststate.ParsedRequest) (*forwardcore.MessagesResult, error) {
	adapter := newMessageExecutionAdapter(s, c, account)
	result, err := forwardcore.Messages(ctx, adapter, adapter.input(), parsed)
	return legacyForwardExecutionResult(result), err
}

// ResolveChannelMapping 委托渠道服务解析模型映射
func (s *GatewayService) ResolveChannelMapping(ctx context.Context, groupID int64, model string) routing.ChannelMappingResult {
	if s.channelService == nil {
		return routing.ChannelMappingResult{MappedModel: model}
	}
	return s.channelService.ResolveChannelMapping(ctx, groupID, model)
}

// ReplaceModelInBody 替换请求体中的模型名（导出供 handler 使用）
func (s *GatewayService) ReplaceModelInBody(body []byte, newModel string) []byte {
	return openaiprotocol.ReplaceModelInBody(body, newModel)
}

// IsModelRestricted 检查模型是否被渠道限制
func (s *GatewayService) IsModelRestricted(ctx context.Context, groupID int64, model string) bool {
	if s.channelService == nil {
		return false
	}
	return s.channelService.IsModelRestricted(ctx, groupID, model)
}

// ResolveChannelMappingAndRestrict 解析渠道映射。
// 模型限制检查已移至调度阶段（checkChannelPricingRestriction），restricted 始终返回 false。
func (s *GatewayService) ResolveChannelMappingAndRestrict(ctx context.Context, groupID *int64, model string) (routing.ChannelMappingResult, bool) {
	if s.channelService == nil {
		return modeltrace.WithChannelRedirect((routing.ChannelMappingResult{MappedModel: model}), ctx, model), false
	}
	result, restricted := s.channelService.ResolveChannelMappingAndRestrict(ctx, groupID, model)
	return modeltrace.WithChannelRedirect(result, ctx, model), restricted
}

// checkChannelPricingRestriction 根据渠道计费基准检查模型是否受定价列表限制。
// 供调度阶段预检查（requested / channel_mapped）。
// upstream 需逐账号检查，此处返回 false。
func (s *GatewayService) checkChannelPricingRestriction(ctx context.Context, groupID *int64, requestedModel string) bool {
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
func (s *GatewayService) isUpstreamModelRestrictedByChannel(ctx context.Context, groupID int64, account *gatewayprovider.ExecutionAccount, requestedModel string) bool {
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
func (s *GatewayService) channelMappedModelForGroup(ctx context.Context, groupID *int64, requestedModel string) string {
	if s == nil {
		return requestedModel

	}
	return s.
		channelService.
		ResolveRoutingModel(ctx, groupID, requestedModel)
}

// resolveAccountMappedModelForForward 执行账号模型映射，并对空映射结果保持原模型透传。
// 所有实际转发和调度检查都应从渠道映射后的模型调用本函数。
func resolveAccountMappedModelForForward(value *gatewayprovider.ExecutionAccount, requestedModel string) string {
	return accountcore.ResolveForwardMappedModel(gatewayprovider.ExecutionRecord(value), requestedModel, accountprovider.ModelDefaults())
}

// resolveAnthropicAccountUpstreamModel 执行账号映射后的 Anthropic 平台最终模型规范化。
func resolveAnthropicAccountUpstreamModel(account *gatewayprovider.ExecutionAccount, accountMappedModel string) string {
	return gatewayprovider.ExecutionModelPolicy(account).AnthropicUpstream(accountMappedModel)
}

// resolveAccountUpstreamModel 解析真正发送给平台上游的最终模型。
func resolveAccountUpstreamModel(ctx context.Context, account *gatewayprovider.ExecutionAccount, requestedModel string) string {
	return gatewayprovider.ExecutionModelPolicy(account).UpstreamModel(ctx, requestedModel)
}

// needsUpstreamChannelRestrictionCheck 判断是否需要在调度循环中逐账号检查上游模型的渠道限制。
func (s *GatewayService) needsUpstreamChannelRestrictionCheck(ctx context.Context, groupID *int64) bool {
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
