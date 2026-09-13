package service

import (
	routing "github.com/TokenFlux/TokenRouter/internal/routing"
)

// legacyGroupProtocolPatch 只表达旧客户端明确修改的开关，缺省字段保持现状。
type legacyGroupProtocolPatch struct {
	messages, image, batch, live *bool
}

func routingLegacyGroupPatch(patch *legacyGroupProtocolPatch) *routing.LegacyGroupProtocolPatch {
	if patch == nil {
		return nil
	}
	return &routing.LegacyGroupProtocolPatch{Messages: patch.messages, Image: patch.image, Batch: patch.batch, Live: patch.live}
}
