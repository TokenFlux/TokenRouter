package provider

import (
	"context"

	"github.com/TokenFlux/TokenRouter/internal/apikey"

	accountcore "github.com/TokenFlux/TokenRouter/internal/account"

	"github.com/TokenFlux/TokenRouter/internal/gateway/requeststate"

	protocolcore "github.com/TokenFlux/TokenRouter/internal/protocol"
	"github.com/TokenFlux/TokenRouter/internal/routing"
)

func GroupResponsesExplicitToolPolicy(group *routing.Group, inherited string) string {
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

func ResponsesPolicyGroup(ctx context.Context, group *routing.Group) *routing.Group {
	source, _ := requeststate.ClientProtocolFromContext(ctx)
	if source != "" && source != protocolcore.ProtocolOpenAIResponses && source != protocolcore.ProtocolResponsesWebSocket {
		return nil
	}
	return group
}

func APIKeyGroup(apiKey *apikey.APIKey) *routing.Group {
	if apiKey == nil {
		return nil
	}
	return apiKey.Group
}

// ResponseImagePolicy 按分组显式协议策略、账号覆盖和全局默认值的优先级读取图片桥接设置。
type ResponseImagePolicy struct {
	DefaultEnabled bool
}

func (s *ResponseImagePolicy) Enabled(ctx context.Context, account *ExecutionAccount, apiKey *apikey.APIKey) bool {
	if group := ResponsesPolicyGroup(ctx, APIKeyGroup(apiKey)); group != nil {
		switch group.ResponsesImagePolicy {
		case "enabled":
			return true
		case "disabled", "block":
			return false
		}
	}

	if override := ExecutionProtocolRecord(account).CodexImageGenerationBridgeOverride(); override != nil {
		return *override
	}
	return s != nil && s.DefaultEnabled
}
