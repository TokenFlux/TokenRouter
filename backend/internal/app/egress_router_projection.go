package app

import "github.com/TokenFlux/TokenRouter/internal/egress"

// provideOpenAITLSRouters 为原可变参数构造提供应用唯一 Router，不创建状态副本。
func provideOpenAITLSRouters(router *egress.TLSFingerprintRouterService) []*egress.TLSFingerprintRouterService {
	return []*egress.TLSFingerprintRouterService{router}
}
