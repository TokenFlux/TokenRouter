// 旧入口只投影账号配置并委托唯一 Bedrock 算法。
package service

import (
	"github.com/TokenFlux/TokenRouter/internal/upstream/bedrock"
)

func resolveBedrockModelRoute(account *Account, requestedModel string) (bedrock.BedrockModelRoute, error) {
	return bedrock.ResolveBedrockModelRoute(bedrockRouteInput(account, requestedModel), requestedModel)
}
