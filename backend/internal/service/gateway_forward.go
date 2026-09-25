package service

import (
	"context"

	gatewayprovider "github.com/TokenFlux/TokenRouter/internal/gateway/provider"
)

// resolveAccountUpstreamModel 解析真正发送给平台上游的最终模型。
func resolveAccountUpstreamModel(ctx context.Context, account *gatewayprovider.ExecutionAccount, requestedModel string) string {
	return gatewayprovider.ExecutionModelPolicy(account).UpstreamModel(ctx, requestedModel)
}
