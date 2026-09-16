// 旧请求上下文只提取状态并委托唯一模型恢复实现。
package service

import (
	"context"

	"github.com/TokenFlux/TokenRouter/internal/gateway/modeltrace"
)

func ReplaceModelMetadata(data []byte, from, to string) []byte {
	return modeltrace.ReplaceModelMetadata(data, from, to)
}
func RestoreAPIKeyModelResponse(ctx context.Context, data []byte) []byte {
	trace, _ := APIKeyModelRedirectTraceFromContext(ctx)
	return trace.Restore(data)
}
