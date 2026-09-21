//go:build unit

// 原私有入口仅为既有测试保留，生产消费者已经迁入所属模块。
package service

import (
	"github.com/TokenFlux/TokenRouter/internal/creative"
	creativeprovider "github.com/TokenFlux/TokenRouter/internal/creative/provider"
)

func buildCreativeOpenAIRequestBody(run creative.CreativeRun, payload creative.CreativeRunPayload, upstreamModel string) ([]byte, string, error) {
	return creativeprovider.BuildCreativeOpenAIRequestBody(run, payload, upstreamModel)
}
func parseCreativeOpenAIImageOutputs(body []byte) ([]creative.CreativeOutput, error) {
	return creativeprovider.ParseCreativeOpenAIImageOutputs(body)
}
