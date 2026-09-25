package app

import (
	"github.com/TokenFlux/TokenRouter/internal/gateway/completion"
	gatewayhttp "github.com/TokenFlux/TokenRouter/internal/gateway/httpapi"
	"github.com/TokenFlux/TokenRouter/internal/gateway/provider"
	"github.com/TokenFlux/TokenRouter/internal/gateway/session"
)

// gatewayExecutionFixture 只保存测试显式构造的原生组件与输入，不承载规则、锁或缓存。
type gatewayExecutionFixture struct {
	Text       *gatewayhttp.OpenAITextExecutor
	Requests   *gatewayhttp.OpenAIRequests
	Responses  *gatewayhttp.OpenAIResponsesExecutor
	WebSockets *gatewayhttp.OpenAIWebSocketExecutor
	Grok       *gatewayhttp.GrokExecutor
	Auxiliary  *gatewayhttp.OpenAIAuxiliary
	Recorder   *completion.Recorder
	Blocks     *session.CyberBlocks
	Cache      session.GatewayCache
	Planner    *provider.RoutePlanner
	Background func(string, func()) bool
}
