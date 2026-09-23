package openaiattempt

import (
	"strings"

	forwardcore "github.com/TokenFlux/TokenRouter/internal/gateway/forward"
	gatewayhttp "github.com/TokenFlux/TokenRouter/internal/gateway/httpapi"
	gatewayprovider "github.com/TokenFlux/TokenRouter/internal/gateway/provider"
	"github.com/gin-gonic/gin"
)

// ResolveOpenAIUpstreamEndpoint 返回 OpenAI 兼容账号的实际上游端点。
// 同一入站路由可能在运行时选择原始 Chat 或 Responses 桥接，因此优先采用转发结果；
// 尚未报告端点的转发路径则回退到当前 attempt 上下文和平台规范端点；这里不再
// 根据账号配置猜测实际路径，避免与 Responses、Messages 或专用传输的真实分支漂移。
func ResolveOpenAIUpstreamEndpoint(c *gin.Context, account *gatewayprovider.ExecutionAccount, result *forwardcore.OpenAIResult) string {
	if result != nil {
		if endpoint := strings.TrimSpace(result.UpstreamEndpoint); endpoint != "" {
			return endpoint
		}
	}
	if endpoint := gatewayhttp.GetActualOpenAIUpstreamEndpoint(c); endpoint != "" {
		return endpoint
	}
	return gatewayhttp.GetUpstreamEndpoint(c, account.Record.Platform)
}
