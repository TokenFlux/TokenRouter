package service

import (
	"github.com/TokenFlux/TokenRouter/internal/egress"
	gatewayprovider "github.com/TokenFlux/TokenRouter/internal/gateway/provider"
)

// resolveOpenAIWSTransport 按原时机投影当前账号和启动配置，传输规则只有原生实现。
func (s *OpenAIGatewayService) resolveOpenAIWSTransport(value *Account) egress.OpenAIWSProtocolDecision {
	view := protocolRecord(value)
	if view != nil {
		view.Concurrency = value.Concurrency
	}
	defaultMode := ""
	var options *egress.OpenAIWSOptions
	if s != nil && s.cfg != nil {
		cfg := s.cfg.Gateway.OpenAIWS
		options = &egress.OpenAIWSOptions{Enabled: cfg.Enabled, ForceHTTP: cfg.ForceHTTP, OAuthEnabled: cfg.OAuthEnabled, APIKeyEnabled: cfg.APIKeyEnabled, ModeRouterV2Enabled: cfg.ModeRouterV2Enabled, ResponsesWebsockets: cfg.ResponsesWebsockets, ResponsesWebsocketsV2: cfg.ResponsesWebsocketsV2}
		defaultMode = cfg.IngressModeDefault
	}
	return gatewayprovider.ResolveOpenAIWSTransport(view, options, defaultMode)
}
