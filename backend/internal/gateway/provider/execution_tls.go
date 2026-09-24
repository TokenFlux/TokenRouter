package provider

import (
	"github.com/TokenFlux/TokenRouter/internal/egress"
)

// ExecutionTLSSelection 只提取资格与配置 ID，策略选择由 egress 拥有。
func ExecutionTLSSelection(value *ExecutionAccount, routerMatch []egress.TLSFingerprintRouterMatchResult) egress.TLSSelection {
	selection := egress.TLSSelection{}
	if value != nil {
		selection.Enabled = value.View().IsTLSFingerprintEnabled()
		selection.DirectProfileID = value.View().GetTLSFingerprintProfileID()
	}
	if len(routerMatch) > 0 {
		selection.RouterMatched = routerMatch[0].Matched
		selection.RouterID = routerMatch[0].RouterID
		selection.RouterProfileID = routerMatch[0].TLSFingerprintProfileID
	}
	return selection
}
