package service

import (
	"context"
	"fmt"
	"slices"

	"github.com/TokenFlux/TokenRouter/internal/domain"
	"github.com/TokenFlux/TokenRouter/internal/pkg/ctxkey"
	"github.com/TokenFlux/TokenRouter/internal/routing/capability"
)

type clientProtocolContextKey struct{}

// WithClientProtocol 标记客户端业务入口；内部转换不能覆盖此标记。
func WithClientProtocol(ctx context.Context, protocol domain.ProtocolID) context.Context {
	return context.WithValue(ctx, clientProtocolContextKey{}, protocol)
}

// ResolveProtocolRoute 对每个候选提取独立能力快照，旧上下文与账号归属留 S06。
// @project-doc docs/interfaces/protocol_capabilities.md#group_protocol_routes
func ResolveProtocolRoute(account *Account, group *Group, source domain.ProtocolID) (domain.ProtocolID, bool) {
	if account == nil {
		return "", false
	}
	var fallbacks map[domain.ProtocolID]domain.ProtocolID
	if group != nil {
		fallbacks = group.ProtocolFallbacks
	}
	return capability.ResolveRoute(capability.AccountProtocols{Platform: account.Platform, Type: account.Type, AuthMode: protocolAuthMode(account), Enabled: account.UpstreamProtocols()}, source, fallbacks)
}

func (a *Account) allowsProtocolRequest(ctx context.Context) bool {
	source, _ := ctx.Value(clientProtocolContextKey{}).(domain.ProtocolID)
	if source == "" {
		return true
	}
	group, _ := ctx.Value(ctxkey.Group).(*Group)
	_, ok := ResolveProtocolRoute(a, group, source)
	return ok
}

// accountForProtocolAttempt 不修改共享账号或持久配置；每次切号都重新计算目标。
func accountForProtocolAttempt(ctx context.Context, account *Account) (*Account, error) {
	if account == nil {
		return nil, fmt.Errorf("account is nil")
	}
	if account.resolvedProtocol != "" {
		return account, nil
	}
	source, _ := ctx.Value(clientProtocolContextKey{}).(domain.ProtocolID)
	if source == "" {
		return account, nil
	}
	group, _ := ctx.Value(ctxkey.Group).(*Group)
	target, ok := ResolveProtocolRoute(account, group, source)
	if !ok {
		return nil, fmt.Errorf("account %d has no enabled route for %s", account.ID, source)
	}
	copied := *account
	copied.resolvedProtocol = target
	return &copied, nil
}

func GroupAllowsResponsesImages(group *Group) bool {
	// 新配置由独立四态管理；旧内存对象保留历史权限语义以兼容内部调用。
	return group == nil || group.ResponsesImagePolicy != "" || group.AllowImageGeneration
}

func groupResponsesExplicitToolPolicy(group *Group, inherited string) string {
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

func responsesPolicyGroup(ctx context.Context, group *Group) *Group {
	source, _ := ctx.Value(clientProtocolContextKey{}).(domain.ProtocolID)
	if source != "" && source != domain.ProtocolOpenAIResponses && source != domain.ProtocolResponsesWebSocket {
		return nil
	}
	return group
}

func (s *OpenAIGatewayService) shadowProtocolsAllowed(ctx context.Context, account *Account) bool {
	if account == nil || !account.IsShadow() {
		return true
	}
	if source, _ := ctx.Value(clientProtocolContextKey{}).(domain.ProtocolID); source == "" {
		return true
	}
	parent := s.parentAccountLookup(ctx)(*account.ParentAccountID)
	return parent != nil && parent.allowsProtocolRequest(ctx)
}

func supportsOpenAIRequestCapability(ctx context.Context, account *Account, capability OpenAIEndpointCapability) bool {
	if account == nil {
		return false
	}
	source, _ := ctx.Value(clientProtocolContextKey{}).(domain.ProtocolID)
	if source == domain.ProtocolResponsesWebSocket || source == domain.ProtocolResponsesCompact {
		if capability == OpenAIEndpointCapabilityTextGeneration || capability == OpenAIEndpointCapabilityResponses {
			return account.allowsProtocolRequest(ctx)
		}
		if capability == OpenAIEndpointCapabilityRemoteCompactionV2 {
			return account.allowsProtocolRequest(ctx) && account.AllowsOpenAINativeCompactionV2()
		}
	}
	return account.SupportsOpenAIEndpointCapability(capability)
}

// 创作台复用相同业务协议；已创建任务的读取与清理不经过此准入。
func creativeOperationProtocol(platform, operation string) domain.ProtocolID {
	if platform == PlatformGemini {
		return domain.ProtocolGeminiGenerateContent
	}
	if operation == CreativeOperationGenerate {
		return domain.ProtocolImagesGenerations
	}
	return domain.ProtocolImagesEdits
}

func creativeOperationsForGroup(group *Group) []string {
	operations := creativeOperationsForPlatform(group.Platform)
	if group.ResponsesImagePolicy == "" && group.ProtocolFallbacks == nil {
		return operations
	}
	return slices.DeleteFunc(operations, func(operation string) bool {
		return !group.AllowsClientProtocol(creativeOperationProtocol(group.Platform, operation))
	})
}
