package service

import "github.com/TokenFlux/TokenRouter/internal/egress"

// accountTLSSelection 只提取资格与配置 ID，策略选择由 egress 拥有。
func accountTLSSelection(value *Account, routerMatch []egress.TLSFingerprintRouterMatchResult) egress.TLSSelection {
	selection := egress.TLSSelection{}
	if value != nil {
		selection.Enabled = value.IsTLSFingerprintEnabled()
		selection.DirectProfileID = value.GetTLSFingerprintProfileID()
	}
	if len(routerMatch) > 0 {
		selection.RouterMatched = routerMatch[0].Matched
		selection.RouterID = routerMatch[0].RouterID
		selection.RouterProfileID = routerMatch[0].TLSFingerprintProfileID
	}
	return selection
}
