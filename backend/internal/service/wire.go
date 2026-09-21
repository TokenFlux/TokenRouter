package service

import (
	"github.com/TokenFlux/TokenRouter/internal/egress"

	"github.com/google/wire"
)

// ProvideOpenAIGatewayTLSFingerprintRouterServices 为 Wire 的可变参数构造显式 slice。
func ProvideOpenAIGatewayTLSFingerprintRouterServices(tlsFPRouterService *egress.TLSFingerprintRouterService) []*egress.TLSFingerprintRouterService {
	return []*egress.TLSFingerprintRouterService{tlsFPRouterService}
}

// ProviderSet is the Wire provider set for all services
var ProviderSet = wire.NewSet(
	// 核心服务

	ProvideOpenAIGatewayTLSFingerprintRouterServices,
	NewOpenAIGatewayService,
	NewCreativeExecutor,
	wire.Bind(new(AccountRuntimeBlocker), new(*OpenAIGatewayService)),
	NewGeminiMessagesCompatService,
	NewAntigravityGatewayService,
)
