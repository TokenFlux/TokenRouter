package service

import (
	"context"
	"fmt"

	accountcore "github.com/TokenFlux/TokenRouter/internal/account"
	"github.com/TokenFlux/TokenRouter/internal/creative"
	"github.com/TokenFlux/TokenRouter/internal/gateway/requeststate"
	protocolcore "github.com/TokenFlux/TokenRouter/internal/protocol"
	"github.com/TokenFlux/TokenRouter/internal/routing"
	"github.com/TokenFlux/TokenRouter/internal/scheduler"
)

// ResolveProtocolRoute 对每个候选提取独立能力快照，旧上下文与账号归属留 S06。
// @project-doc docs/interfaces/protocol_capabilities.md#group_protocol_routes
func ResolveProtocolRoute(account *Account, group *routing.Group, source protocolcore.ProtocolID) (protocolcore.ProtocolID, bool) {
	if account == nil {
		return "", false
	}
	var routeGroup *routing.Group
	if group != nil {
		routeGroup = &routing.Group{ID: group.ID, Platform: group.Platform, SchedulerType: group.SchedulerType, AllowedProtocols: group.AllowedProtocols, ProtocolFallbacks: group.ProtocolFallbacks}
	}
	plan := routing.Plan(routing.PlanInput{Group: routeGroup, ClientProtocol: source})
	candidate, ok := (scheduler.SelectionInput{RoutePlan: plan}).ResolveCandidate(AccountSnapshotView(account))
	return candidate.UpstreamProtocol, ok
}

func (a *Account) allowsProtocolRequest(ctx context.Context) bool {
	source, _ := requeststate.ClientProtocolFromContext(ctx)
	if source == "" {
		return true
	}
	group, _ := requeststate.GroupFromContext(ctx)
	_, ok := ResolveProtocolRoute(a, group, source)
	return ok
}

// accountForProtocolAttempt 使用当前计划重新验证候选，模型规则仍在原匹配时机读取。
func accountForProtocolAttempt(ctx context.Context, value *Account) (*Account, error) {
	if value == nil {
		return nil, fmt.Errorf("account is nil")
	}
	attempt, resolved, err := requeststate.RoutingStateFromContext(ctx).ResolveAttempt(AccountSnapshotView(value), value.attemptRoute)
	if err != nil {
		return nil, err
	}
	if !resolved {
		return value, nil
	}
	copied := *value
	copied.attemptRoute = attempt
	return &copied, nil
}

func GroupAllowsResponsesImages(group *routing.Group) bool {
	// 新配置由独立四态管理；旧内存对象保留历史权限语义以兼容内部调用。
	return group == nil || group.ResponsesImagePolicy != "" || group.AllowImageGeneration
}

func groupResponsesExplicitToolPolicy(group *routing.Group, inherited string) string {
	if group == nil {
		return inherited
	}
	switch group.ResponsesImagePolicy {
	case "block":
		return codexImageGenerationExplicitToolPolicyStrip
	case "enabled", "disabled":
		return codexImageGenerationExplicitToolPolicyAllow
	default:
		return inherited
	}
}

func responsesPolicyGroup(ctx context.Context, group *routing.Group) *routing.Group {
	source, _ := requeststate.ClientProtocolFromContext(ctx)
	if source != "" && source != protocolcore.ProtocolOpenAIResponses && source != protocolcore.ProtocolResponsesWebSocket {
		return nil
	}
	return group
}

func (s *OpenAIGatewayService) shadowProtocolsAllowed(ctx context.Context, account *Account) bool {
	if account == nil || !account.IsShadow() {
		return true
	}
	if source, _ := requeststate.ClientProtocolFromContext(ctx); source == "" {
		return true
	}
	parent := s.parentAccountLookup(ctx)(*account.ParentAccountID)
	return parent != nil && parent.allowsProtocolRequest(ctx)
}

func supportsOpenAIRequestCapability(ctx context.Context, account *Account, capability accountcore.OpenAIEndpointCapability) bool {
	if account == nil {
		return false
	}
	source, _ := requeststate.ClientProtocolFromContext(ctx)
	if source == protocolcore.ProtocolResponsesWebSocket || source == protocolcore.ProtocolResponsesCompact {
		if capability == accountcore.OpenAIEndpointCapabilityTextGeneration || capability == accountcore.OpenAIEndpointCapabilityResponses {
			return account.allowsProtocolRequest(ctx)
		}
		if capability == accountcore.OpenAIEndpointCapabilityRemoteCompactionV2 {
			return account.allowsProtocolRequest(ctx) && account.AllowsOpenAINativeCompactionV2()
		}
	}
	return account.SupportsOpenAIEndpointCapability(capability)
}

func creativeOperationsForGroup(group *routing.Group) []string {
	return creative.OperationsForGroup(group.Platform, group.ResponsesImagePolicy != "" || group.ProtocolFallbacks != nil, group.AllowsClientProtocol)
}
