//go:build unit

// 原私有入口仅为既有测试保留，生产消费者已经迁入所属模块。
package service

import (
	"context"

	creativeprovider "github.com/TokenFlux/TokenRouter/internal/creative/provider"
)

type creativeGeminiGenerateRequest = creativeprovider.CreativeGeminiGenerateRequest

func (e *CreativeExecutor) executeGemini(ctx context.Context, run CreativeRun, payload CreativeRunPayload, account *Account, upstreamModel string) ([]CreativeOutput, error) {
	return e.nativeTarget(account).ExecuteGemini(ctx, run, payload, upstreamModel)
}
func buildCreativeGeminiRequest(run CreativeRun, payload CreativeRunPayload, upstreamModel string) creativeGeminiGenerateRequest {
	return creativeprovider.BuildCreativeGeminiRequest(run, payload, upstreamModel)
}
