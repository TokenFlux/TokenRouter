// 旧 context 入口只投影模型追踪，状态和算法由 gateway/modeltrace 唯一拥有。
package httpapi

import (
	"context"
	"strings"

	"github.com/TokenFlux/TokenRouter/internal/gateway/modeltrace"
	"github.com/TokenFlux/TokenRouter/internal/infra/telemetry"
)

// PropagateAPIKeyModelRedirectTrace 保持旧异步任务的关联字段与模型来源。
func PropagateAPIKeyModelRedirectTrace(dst, src context.Context) context.Context {
	trace, ok := modeltrace.FromContext(src)
	if !ok {
		return dst
	}
	if dst == nil {
		dst = context.Background()
	}
	dst = modeltrace.WithContext(dst, trace)
	if strings.TrimSpace(trace.ClientModel) != "" {
		dst = context.WithValue(dst, telemetry.ClientModel, trace.ClientModel)
	}
	return dst
}
