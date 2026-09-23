package service

import (
	gatewayprovider "github.com/TokenFlux/TokenRouter/internal/gateway/provider"
	"github.com/TokenFlux/TokenRouter/internal/routing"
)

// modelRejectionSources 只投影旧候选的原读取端口；过滤、聚合及错误归 routing。
func modelRejectionSources(accounts []gatewayprovider.ExecutionAccount) []routing.ModelRejectionSource {
	sources := make([]routing.ModelRejectionSource, len(accounts))
	for i := range accounts {
		value := &accounts[i]
		sources[i] = gatewayprovider.ModelRejectionAccount(gatewayprovider.ExecutionRecord(value))
	}
	return sources
}
