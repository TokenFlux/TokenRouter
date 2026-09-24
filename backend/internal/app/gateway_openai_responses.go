package app

import (
	"github.com/TokenFlux/TokenRouter/internal/config"
	gatewayhttp "github.com/TokenFlux/TokenRouter/internal/gateway/httpapi"
	"github.com/TokenFlux/TokenRouter/internal/gateway/provider"
	"github.com/TokenFlux/TokenRouter/internal/gateway/provider/selection"
	"github.com/TokenFlux/TokenRouter/internal/routing"
	"github.com/TokenFlux/TokenRouter/internal/service"
)

// bindOpenAIResponses 在请求开放前绑定同一会话状态、原生策略及 WS 传输端口。
func bindOpenAIResponses(source *service.OpenAIGatewayService, cfg *config.Config, channels *routing.ChannelService, choices *selection.Compatible) {
	imagePolicy := &provider.ResponseImagePolicy{}
	if channels != nil {
		imagePolicy.Channels = channels
	}
	if cfg != nil {
		imagePolicy.DefaultEnabled = cfg.Gateway.CodexImageGenerationBridgeEnabled
	}
	lineage := &gatewayhttp.OpenAIEncryptedLineage{Store: source.ResponseStateStore(), TTL: choices.SessionStickyTTL}
	source.Lineage = lineage
	source.Responses = &gatewayhttp.OpenAIResponsesExecutor{
		Requests: source.Requests, Output: source.Text.Output, Text: source.Text, Grok: source.Grok,
		Lineage: lineage, ImageBridge: imagePolicy, ResolveTransport: choices.ResolveTransport,
		WebSocket: source.ForwardHTTPWebSocket,
	}
}
