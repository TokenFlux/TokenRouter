// 旧入口只投影账号配置并委托唯一 Bedrock 算法。
package service

import (
	native "github.com/TokenFlux/TokenRouter/internal/upstream/bedrock"
)

type bedrockModelRoute = native.BedrockModelRoute
type bedrockRoutingFailure = native.BedrockRoutingFailure

const bedrockRoutingInvalidModel = native.BedrockRoutingInvalidModel
const bedrockRoutingUnsupportedRegion = native.BedrockRoutingUnsupportedRegion
const bedrockRoutingUnverifiedRegion = native.BedrockRoutingUnverifiedRegion

type bedrockModelRoutingError = native.BedrockModelRoutingError

func bedrockRoutingDiagnostic(err error) string { return native.BedrockRoutingDiagnostic(err) }
func resolveBedrockModelRoute(account *Account, requestedModel string) (bedrockModelRoute, error) {
	return native.ResolveBedrockModelRoute(bedrockRouteInput(account, requestedModel), requestedModel)
}
func bedrockBaseModelID(modelID string) string { return native.BedrockBaseModelID(modelID) }

var bedrockModelRegionRules = native.BedrockModelRegionRules
