package app

import (
	"github.com/TokenFlux/TokenRouter/internal/config"
	gatewayhttp "github.com/TokenFlux/TokenRouter/internal/gateway/httpapi"
	"github.com/TokenFlux/TokenRouter/internal/gateway/promptpolicy"
	"github.com/TokenFlux/TokenRouter/internal/gateway/provider"
	"github.com/TokenFlux/TokenRouter/internal/gateway/provider/selection"
	"github.com/TokenFlux/TokenRouter/internal/gateway/session"
	"github.com/TokenFlux/TokenRouter/internal/routing"
	"github.com/TokenFlux/TokenRouter/internal/service"
)

// bindOpenAIResponses 在请求开放前绑定同一会话状态、原生策略及 WS 传输端口。
func bindOpenAIResponses(source *service.OpenAIGatewayService, cfg *config.Config, channels *routing.ChannelService, choices *selection.Compatible, cache session.GatewayCache, prompts *promptpolicy.Service) {
	imagePolicy := &provider.ResponseImagePolicy{}
	if channels != nil {
		imagePolicy.Channels = channels
	}
	if cfg != nil {
		imagePolicy.DefaultEnabled = cfg.Gateway.CodexImageGenerationBridgeEnabled
	}
	lineage := &gatewayhttp.OpenAIEncryptedLineage{Store: source.ResponseStateStore(), TTL: choices.SessionStickyTTL}
	source.Lineage = lineage
	source.WebSockets = gatewayhttp.NewOpenAIWebSocketExecutor(gatewayhttp.OpenAIWSDependencies{
		Options: openAIWSExecutionOptions(cfg), Connections: source.Connections,
		Requests: source.Requests, Output: source.Text.Output, Grok: source.Grok,
		FastPolicy: source.Text.FastPolicy, Prompts: prompts, Selection: choices,
		State: lineage.Store, Lineage: lineage, ImageBridge: imagePolicy, Cache: cache,
	})
	source.Responses = &gatewayhttp.OpenAIResponsesExecutor{
		Requests: source.Requests, Output: source.Text.Output, Text: source.Text, Grok: source.Grok,
		Lineage: lineage, ImageBridge: imagePolicy, ResolveTransport: choices.ResolveTransport,
		WebSocket: source.WebSockets.ForwardHTTPWebSocket,
	}
}
