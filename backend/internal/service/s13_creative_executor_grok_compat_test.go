//go:build unit

// 原私有入口仅为既有测试保留，生产消费者已经迁入所属模块。
package service

import (
	"context"

	creativeprovider "github.com/TokenFlux/TokenRouter/internal/creative/provider"
)

func (e *CreativeExecutor) executeGrok(ctx context.Context, run CreativeRun, payload CreativeRunPayload, account *Account, upstreamModel string) ([]CreativeOutput, error) {
	return e.nativeTarget(account).ExecuteGrok(ctx, run, payload, upstreamModel)
}
func buildCreativeGrokRequest(run CreativeRun, payload CreativeRunPayload, upstreamModel string) map[string]any {
	return creativeprovider.BuildCreativeGrokRequest(run, payload, upstreamModel)
}
func buildCreativeGrokEditRequest(run CreativeRun, payload CreativeRunPayload, upstreamModel string) map[string]any {
	return creativeprovider.BuildCreativeGrokEditRequest(run, payload, upstreamModel)
}
