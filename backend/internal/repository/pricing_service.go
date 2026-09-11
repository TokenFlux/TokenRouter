package repository

import (
	"github.com/TokenFlux/TokenRouter/internal/billing/provider"
	"github.com/TokenFlux/TokenRouter/internal/service"
)

// NewPricingRemoteClient 保留原代理失败策略与构造签名，S04/S16 清理。
func NewPricingRemoteClient(proxyURL string, allowDirectOnProxyError bool) service.PricingRemoteClient {
	return provider.NewPricingRemoteClient(proxyURL, allowDirectOnProxyError)
}
