package selection

import (
	"context"
	"errors"
	"fmt"
	"strings"

	accountcore "github.com/TokenFlux/TokenRouter/internal/account"
	gatewayprovider "github.com/TokenFlux/TokenRouter/internal/gateway/provider"
)

// ResolveOpenAIWSRoutingModelForAccount 为已选定的 WebSocket 账号逐轮解析并校验分组映射模型。
// 长连接不能在后续 turn 重新调度账号，因此模型不再适配当前账号时直接拒绝该帧。
func (s *Compatible) ResolveOpenAIWSRoutingModelForAccount(
	ctx context.Context,
	groupID *int64,
	account *gatewayprovider.ExecutionAccount,
	requestedModel string,
	requiredCapability accountcore.OpenAIEndpointCapability,
) (string, error) {
	requestedModel = strings.TrimSpace(requestedModel)
	if requestedModel == "" {
		return "", errors.New("websocket request model is empty")
	}
	if s.CheckGroupModelRestriction(ctx, groupID, requestedModel) {
		return "", fmt.Errorf("model %s is restricted by group model policy", requestedModel)
	}

	routingModel := strings.TrimSpace(s.resolveGroupRoutingModel(ctx, groupID, requestedModel))
	if routingModel == "" {
		routingModel = requestedModel
	}
	if account == nil || !gatewayprovider.
		CompatibleAccountEligible(
			ctx,
			account,
			account.Record.Platform,
			routingModel,
			false,
			requiredCapability,
		) {
		return "", fmt.Errorf("model %s is not supported by the selected websocket account", requestedModel)
	}
	if s.isOpenAIAccountRequestRuntimeBlocked(account, routingModel) {
		return "", fmt.Errorf("model %s is temporarily unavailable on the selected websocket account", requestedModel)
	}
	if groupID != nil && s.NeedsUpstreamGroupRestriction(ctx, groupID) &&
		s.UpstreamRoutingModelRestricted(ctx, *groupID, account, routingModel, false) {
		return "", fmt.Errorf("model %s is restricted after account mapping", requestedModel)
	}
	return routingModel, nil
}
