package selection

import (
	"github.com/TokenFlux/TokenRouter/internal/egress"
	gatewayprovider "github.com/TokenFlux/TokenRouter/internal/gateway/provider"
)

// resolveOpenAIWSTransport 按原时机投影当前账号和启动配置，传输规则只有原生实现。
func (s *Compatible) ResolveTransport(value *gatewayprovider.ExecutionAccount) egress.OpenAIWSProtocolDecision {
	view := gatewayprovider.ExecutionProtocolRecord(value)
	if view !=
		nil {
		view.Concurrency = value.Record.Concurrency
	}
	var options *egress.OpenAIWSOptions

	mode := ""
	if s != nil {
		options = s.options.WS
		mode = s.options.WSIngressMode
	}
	return gatewayprovider.ResolveOpenAIWSTransport(view, options, mode)
}
