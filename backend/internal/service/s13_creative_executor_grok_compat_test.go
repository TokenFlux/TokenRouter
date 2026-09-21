//go:build unit

// 原私有入口仅为既有测试保留，生产消费者已经迁入所属模块。
package service

import (
	"context"

	"github.com/TokenFlux/TokenRouter/internal/creative"
	creativeprovider "github.com/TokenFlux/TokenRouter/internal/creative/provider"
)

func (e *CreativeExecutor) executeGrok(ctx context.Context, run creative.CreativeRun, payload creative.CreativeRunPayload, account *Account, upstreamModel string) ([]creative.CreativeOutput, error) {
	return e.nativeTarget(account).ExecuteGrok(ctx, run, payload, upstreamModel)
}
func buildCreativeGrokRequest(run creative.CreativeRun, payload creative.CreativeRunPayload, upstreamModel string) map[string]any {
	return creativeprovider.BuildCreativeGrokRequest(run, payload, upstreamModel)
}
func buildCreativeGrokEditRequest(run creative.CreativeRun, payload creative.CreativeRunPayload, upstreamModel string) map[string]any {
	return creativeprovider.BuildCreativeGrokEditRequest(run, payload, upstreamModel)
}
