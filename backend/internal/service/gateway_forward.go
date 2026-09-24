package service

import (
	"context"

	accountcore "github.com/TokenFlux/TokenRouter/internal/account"
	accountprovider "github.com/TokenFlux/TokenRouter/internal/account/provider"

	gatewayprovider "github.com/TokenFlux/TokenRouter/internal/gateway/provider"
)

// resolveAccountMappedModelForForward 执行账号模型映射，并对空映射结果保持原模型透传。
// 所有实际转发和调度检查都应从渠道映射后的模型调用本函数。
func resolveAccountMappedModelForForward(value *gatewayprovider.ExecutionAccount, requestedModel string) string {
	return accountcore.ResolveForwardMappedModel(gatewayprovider.ExecutionRecord(value), requestedModel, accountprovider.ModelDefaults())
}

// resolveAccountUpstreamModel 解析真正发送给平台上游的最终模型。
func resolveAccountUpstreamModel(ctx context.Context, account *gatewayprovider.ExecutionAccount, requestedModel string) string {
	return gatewayprovider.ExecutionModelPolicy(account).UpstreamModel(ctx, requestedModel)
}
