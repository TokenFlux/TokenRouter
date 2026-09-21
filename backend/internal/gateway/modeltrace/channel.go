package modeltrace

import (
	"context"

	"github.com/TokenFlux/TokenRouter/internal/routing"
)

// WithChannelRedirect 在网关边界组合 Key 与渠道的模型阶段，路由缓存保持独立。
func WithChannelRedirect(result routing.ChannelMappingResult, ctx context.Context, requestedModel string) routing.ChannelMappingResult {
	trace, ok := FromContext(ctx)
	if !ok {
		return result
	}
	result.ClientModel = trace.ClientModel
	result.APIKeyRedirected = true
	RegisterStage(ctx, requestedModel)
	RegisterStage(ctx, result.MappedModel)
	return result
}

// RegisterStage 登记当前请求已使用的内部模型，供响应元数据恢复。
func RegisterStage(ctx context.Context, model string) {
	if trace, ok := FromContext(ctx); ok {
		trace.RegisterModel(model)
	}
}
