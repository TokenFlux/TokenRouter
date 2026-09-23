package service

import "github.com/TokenFlux/TokenRouter/internal/gateway/provider"

// BindAgentIdentity 仅在构造时保存原生身份适配，不在旧执行器安装锁或默认实例。
func (s *OpenAIGatewayService) BindAgentIdentity(value *provider.ExecutionAgentIdentity) {
	s.agentIdentity = value
}
