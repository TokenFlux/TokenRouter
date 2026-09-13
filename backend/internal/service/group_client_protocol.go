// 本文件维护 service 的所属能力；兼容入口复用唯一实现。
package service

import (
	domain "github.com/TokenFlux/TokenRouter/internal/domain"
	routing "github.com/TokenFlux/TokenRouter/internal/routing"
)

func (g *Group) EffectiveAllowedProtocols() []domain.ProtocolID {
	return groupRules(g).EffectiveAllowedProtocols()
}

func (g *Group) AllowsClientProtocol(protocol domain.ProtocolID) bool {
	if g == nil {
		return false
	}
	// 准入热路径只投影所需集合，避免构造完整分组快照。
	view := routing.Group{AllowedProtocols: g.AllowedProtocols}
	return view.AllowsClientProtocol(protocol)
}

func normalizeGroupProtocolPolicy(group *Group, legacy *legacyGroupProtocolPatch) error {
	value := groupRules(group)
	err := routing.NormalizeGroupProtocolPolicy(value, routingLegacyGroupPatch(legacy))
	ApplyRoutingGroup(group, value)
	return err
}
