package service

import "github.com/TokenFlux/TokenRouter/internal/domain"

// EffectiveAllowedProtocols 返回可用于热路径判定的协议集合。
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
	return domain.HasGroupClientProtocol(g.EffectiveAllowedProtocols(), protocol)
}

// normalizeExplicitGroupClientProtocols 校验显式 API 输入并保持固定顺序。
func normalizeExplicitGroupClientProtocols(platform string, protocols []domain.ProtocolID) ([]domain.ProtocolID, error) {
	return domain.ValidateGroupClientProtocols(platform, protocols)
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

// defaultGroupClientProtocols 返回新建分组的默认协议集合。
func defaultGroupClientProtocols(platform string) []domain.ProtocolID {
	return domain.DefaultGroupClientProtocols(platform)
}

// setGroupClientProtocol 更新兼容字段对应的单个协议。
func setGroupClientProtocol(protocols []domain.ProtocolID, protocol domain.ProtocolID, enabled bool) []domain.ProtocolID {
	return domain.SetGroupClientProtocol(protocols, protocol, enabled)
}
