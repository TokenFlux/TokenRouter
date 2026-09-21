// 旧入口只投影账号配置并委托唯一 Bedrock 算法。
package service

import (
	"github.com/TokenFlux/TokenRouter/internal/upstream/bedrock"
)

func ResolveBedrockModelID(account *Account, requestedModel string) (string, bool) {
	return bedrock.ResolveBedrockModelID(bedrockRouteInput(account, requestedModel), requestedModel)
}

func bedrockRouteInput(value *Account, model string) *bedrock.RouteInput {
	if value == nil {
		return nil
	}
	return &bedrock.RouteInput{Region: value.GetCredential("aws_region"), ForceGlobal: value.GetCredential("aws_force_global") == "true", Model: value.GetMappedModel(model)}
}
