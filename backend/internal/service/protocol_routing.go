package service

import (
	"context"
	"fmt"

	accountcore "github.com/TokenFlux/TokenRouter/internal/account"
	accountprovider "github.com/TokenFlux/TokenRouter/internal/account/provider"
	"github.com/TokenFlux/TokenRouter/internal/creative"
	gatewayprovider "github.com/TokenFlux/TokenRouter/internal/gateway/provider"
	"github.com/TokenFlux/TokenRouter/internal/gateway/requeststate"

	protocolcore "github.com/TokenFlux/TokenRouter/internal/protocol"
	"github.com/TokenFlux/TokenRouter/internal/routing"
)

// accountForProtocolAttempt 使用当前计划重新验证候选，模型规则仍在原匹配时机读取。
func accountForProtocolAttempt(ctx context.Context, value *gatewayprovider.ExecutionAccount) (*gatewayprovider.ExecutionAccount, error) {
	if value == nil {
		return nil, fmt.Errorf("account is nil")
	}
	attempt, resolved, err := requeststate.RoutingStateFromContext(ctx).ResolveAttempt(gatewayprovider.ExecutionSnapshot(value), value.Route)
	if err != nil {
		return nil, err
	}
	if !resolved {
		return value, nil
	}
	copied := *value
	copied.Route = attempt
	return &copied, nil
}

func groupResponsesExplicitToolPolicy(group *routing.Group, inherited string) string {
	if group == nil {
		return inherited
	}
	switch group.ResponsesImagePolicy {
	case "block":
		return accountcore.CodexImagePolicyStrip
	case "enabled", "disabled":
		return accountcore.CodexImagePolicyAllow
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

func (s *OpenAIGatewayService) shadowProtocolsAllowed(ctx context.Context, account *gatewayprovider.ExecutionAccount) bool {
	if account == nil || !account.View().IsShadow() {
		return true
	}
	if source, _ := requeststate.ClientProtocolFromContext(ctx); source == "" {
		return true
	}
	parent := s.parentAccountLookup(ctx)(*account.Record.ParentAccountID)
	return parent != nil && gatewayprovider.ExecutionModelPolicy(parent).AllowsProtocol(ctx)
}

func supportsOpenAIRequestCapability(ctx context.Context, account *gatewayprovider.ExecutionAccount, capability accountcore.OpenAIEndpointCapability) bool {
	if account == nil {
		return false
	}
	source, _ := requeststate.ClientProtocolFromContext(ctx)
	if source == protocolcore.ProtocolResponsesWebSocket || source == protocolcore.ProtocolResponsesCompact {
		if capability == accountcore.OpenAIEndpointCapabilityTextGeneration || capability == accountcore.OpenAIEndpointCapabilityResponses {
			return gatewayprovider.ExecutionModelPolicy(account).AllowsProtocol(ctx)
		}
		if capability == accountcore.OpenAIEndpointCapabilityRemoteCompactionV2 {
			return gatewayprovider.ExecutionModelPolicy(account).AllowsProtocol(ctx) && account.View().AllowsOpenAINativeCompactionV2()
		}
	}
	return accountprovider.SupportsOpenAIEndpoint(gatewayprovider.ExecutionProtocolRecord(account), capability)
}

func creativeOperationsForGroup(group *routing.Group) []string {
	return creative.OperationsForGroup(group.Platform, group.ResponsesImagePolicy != "" || group.ProtocolFallbacks != nil, group.AllowsClientProtocol)
}
