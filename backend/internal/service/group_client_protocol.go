package service

import (
	"slices"

	"github.com/TokenFlux/TokenRouter/internal/domain"
	infraerrors "github.com/TokenFlux/TokenRouter/internal/pkg/errors"
	"github.com/TokenFlux/TokenRouter/internal/routing/capability"
)

// EffectiveAllowedProtocols 为响应映射和编辑快照返回独立协议集合。
// 返回独立副本，并把 nil 统一表达为合法的空集合。
func (g *Group) EffectiveAllowedProtocols() []domain.ProtocolID {
	if g == nil {
		return []domain.ProtocolID{}
	}
	return append([]domain.ProtocolID{}, g.AllowedProtocols...)
}

// AllowsClientProtocol 判断分组是否允许指定客户端协议。
func (g *Group) AllowsClientProtocol(protocol domain.ProtocolID) bool {
	if g == nil {
		return false
	}
	return slices.Contains(g.AllowedProtocols, protocol)
}

// filterGroupClientProtocolsForPlatform 在平台切换时只保留新平台支持的协议。
func filterGroupClientProtocolsForPlatform(platform string, protocols []domain.ProtocolID) []domain.ProtocolID {
	supportedProtocols := domain.SupportedGroupClientProtocols(platform)
	selectedSet := make(map[domain.ProtocolID]struct{}, len(protocols))
	for _, protocol := range protocols {
		selectedSet[protocol] = struct{}{}
	}
	selected := make([]domain.ProtocolID, 0, len(selectedSet))
	for _, protocol := range supportedProtocols {
		if _, ok := selectedSet[protocol]; ok {
			selected = append(selected, protocol)
		}
	}
	return selected
}

// normalizeGroupProtocolPolicy 校验一次原始集合，再应用兼容输入并生成旧字段镜像。
// @project-doc docs/interfaces/protocol_capabilities.md#group_protocol_routes
func normalizeGroupProtocolPolicy(group *Group, legacy *legacyGroupProtocolPatch) error {
	normalized, err := domain.ValidateGroupClientProtocols(group.Platform, group.AllowedProtocols)
	if err != nil {
		return infraerrors.BadRequest("INVALID_ALLOWED_CLIENT_PROTOCOLS", err.Error())
	}
	group.AllowedProtocols = normalized
	applyLegacyGroupProtocolPatch(group, legacy)
	if err := capability.ValidateProtocolFallbacks(group.Platform, group.ProtocolFallbacks); err != nil {
		return infraerrors.BadRequest("GROUP_PROTOCOL_FALLBACK_INVALID", err.Error())
	}
	if group.ProtocolFallbacks == nil {
		group.ProtocolFallbacks = map[domain.ProtocolID]domain.ProtocolID{}
	}
	policy, err := capability.NormalizeResponsesImagePolicy(group.ResponsesImagePolicy)
	if err != nil {
		return infraerrors.BadRequest("GROUP_RESPONSES_IMAGE_POLICY_INVALID", err.Error())
	}
	group.ResponsesImagePolicy = policy
	// 旧服务仍读取这些派生值；它们不再作为独立配置写入。
	group.AllowMessagesDispatch = group.Platform == PlatformOpenAI && slices.Contains(group.AllowedProtocols, domain.ProtocolAnthropicMessages)
	group.AllowImageGeneration = slices.Contains(group.AllowedProtocols, domain.ProtocolImagesGenerations) || slices.Contains(group.AllowedProtocols, domain.ProtocolImagesEdits) || slices.Contains(group.AllowedProtocols, domain.ProtocolImageBatches) || slices.Contains(group.AllowedProtocols, domain.ProtocolGeminiGenerateContent)
	group.AllowBatchImageGeneration = slices.Contains(group.AllowedProtocols, domain.ProtocolImageBatches)
	group.AllowLive = slices.Contains(group.AllowedProtocols, domain.ProtocolLive)
	return nil
}
