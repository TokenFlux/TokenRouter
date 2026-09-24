package service

import (
	"context"

	accountcore "github.com/TokenFlux/TokenRouter/internal/account"
	"github.com/TokenFlux/TokenRouter/internal/creative"

	"github.com/TokenFlux/TokenRouter/internal/gateway/requeststate"

	protocolcore "github.com/TokenFlux/TokenRouter/internal/protocol"
	"github.com/TokenFlux/TokenRouter/internal/routing"
)

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

func creativeOperationsForGroup(group *routing.Group) []string {
	return creative.OperationsForGroup(group.Platform, group.ResponsesImagePolicy != "" || group.ProtocolFallbacks != nil, group.AllowsClientProtocol)
}
