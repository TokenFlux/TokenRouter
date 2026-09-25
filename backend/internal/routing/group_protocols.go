// 本文件维护 routing 的所属能力；兼容入口复用唯一实现。
package routing

import (
	"slices"

	infraerrors "github.com/TokenFlux/TokenRouter/internal/pkg/apperror"
	"github.com/TokenFlux/TokenRouter/internal/protocol"
	"github.com/TokenFlux/TokenRouter/internal/routing/capability"
)

// EffectiveAllowedProtocols 为响应映射和编辑快照返回独立协议集合。
// 返回独立副本，并把 nil 统一表达为合法的空集合。
func (g *Group) EffectiveAllowedProtocols() []protocol.ProtocolID {
	if g == nil {
		return []protocol.ProtocolID{}
	}
	return append([]protocol.ProtocolID{}, g.AllowedProtocols...)
}

// AllowsClientProtocol 判断分组是否允许指定客户端协议。
func (g *Group) AllowsClientProtocol(protocol protocol.ProtocolID) bool {
	if g == nil {
		return false
	}
	return slices.Contains(g.AllowedProtocols, protocol)
}

// FilterGroupClientProtocolsForPlatform 在平台切换时只保留新平台支持的协议。
func FilterGroupClientProtocolsForPlatform(platform string, protocols []protocol.ProtocolID) []protocol.ProtocolID {
	supportedProtocols := capability.SupportedGroupClientProtocols(platform)
	selectedSet := make(map[protocol.ProtocolID]struct{}, len(protocols))
	for _, protocol := range protocols {
		selectedSet[protocol] = struct{}{}
	}
	selected := make([]protocol.ProtocolID, 0, len(selectedSet))
	for _, protocol := range supportedProtocols {
		if _, ok := selectedSet[protocol]; ok {
			selected = append(selected, protocol)
		}
	}
	return selected
}

// NormalizeGroupProtocolPolicy 校验一次原始集合，再应用兼容输入并生成旧字段镜像。
// @project-doc docs/interfaces/protocol_capabilities.md#group_protocol_routes
func NormalizeGroupProtocolPolicy(group *Group, legacy *LegacyGroupProtocolPatch) error {
	normalized, err := capability.ValidateGroupClientProtocols(group.Platform, group.AllowedProtocols)
	if err != nil {
		return infraerrors.BadRequest("INVALID_ALLOWED_CLIENT_PROTOCOLS", err.Error())
	}
	group.AllowedProtocols = normalized
	ApplyLegacyGroupProtocolPatch(group, legacy)
	if err := capability.ValidateProtocolFallbacks(group.Platform, group.ProtocolFallbacks); err != nil {
		return infraerrors.BadRequest("GROUP_PROTOCOL_FALLBACK_INVALID", err.Error())
	}
	if group.ProtocolFallbacks == nil {
		group.ProtocolFallbacks = map[protocol.ProtocolID]protocol.ProtocolID{}
	}
	policy, err := capability.NormalizeResponsesImagePolicy(group.ResponsesImagePolicy)
	if err != nil {
		return infraerrors.BadRequest("GROUP_RESPONSES_IMAGE_POLICY_INVALID", err.Error())
	}
	group.ResponsesImagePolicy = policy
	// 旧服务仍读取这些派生值；它们不再作为独立配置写入。
	group.AllowMessagesDispatch = group.Platform == PlatformOpenAI && slices.Contains(group.AllowedProtocols, protocol.ProtocolAnthropicMessages)
	group.AllowImageGeneration = slices.Contains(group.AllowedProtocols, protocol.ProtocolImagesGenerations) || slices.Contains(group.AllowedProtocols, protocol.ProtocolImagesEdits) || slices.Contains(group.AllowedProtocols, protocol.ProtocolImageBatches) || slices.Contains(group.AllowedProtocols, protocol.ProtocolGeminiGenerateContent)
	group.AllowBatchImageGeneration = slices.Contains(group.AllowedProtocols, protocol.ProtocolImageBatches)
	group.AllowLive = slices.Contains(group.AllowedProtocols, protocol.ProtocolLive)
	return nil
}

// LegacyGroupProtocolPatch 只表达旧客户端明确修改的开关，缺省字段保持现状。
type LegacyGroupProtocolPatch struct {
	Messages, Image, Batch, Live *bool
}

// ApplyLegacyGroupProtocolPatch 只修改已校验的集合，避免兼容转换掩盖非法输入。
func ApplyLegacyGroupProtocolPatch(group *Group, patch *LegacyGroupProtocolPatch) {
	if patch == nil {
		return
	}
	supported := capability.SupportedGroupClientProtocols(group.Platform)
	set := func(protocol protocol.ProtocolID, value *bool) {
		if value != nil && slices.Contains(supported, protocol) {
			group.AllowedProtocols = capability.SetGroupClientProtocol(group.AllowedProtocols, protocol, *value)
		}
	}
	set(protocol.ProtocolAnthropicMessages, patch.Messages)
	set(protocol.ProtocolImagesGenerations, patch.Image)
	set(protocol.ProtocolImagesEdits, patch.Image)
	if patch.Image != nil && !*patch.Image {
		set(protocol.ProtocolImageBatches, patch.Image)
	}
	set(protocol.ProtocolImageBatches, patch.Batch)
	set(protocol.ProtocolLive, patch.Live)
}
