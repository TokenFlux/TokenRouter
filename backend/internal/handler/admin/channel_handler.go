// 本文件维护 admin 的所属能力；兼容入口复用唯一实现。
package admin

import (
	qoder "github.com/TokenFlux/TokenRouter/internal/pkg/qoder"
	routing "github.com/TokenFlux/TokenRouter/internal/routing"
	routinghttp "github.com/TokenFlux/TokenRouter/internal/routing/httpapi"
	service "github.com/TokenFlux/TokenRouter/internal/service"
)

type ChannelHandler = routinghttp.ChannelHandler

func NewChannelHandler(channels *service.ChannelService, billing *service.BillingService, prices *service.PricingService) *ChannelHandler {
	var core *routing.ChannelService
	if channels != nil {
		core = channels.ChannelService
	}
	var priceReader routing.ModelPriceReader
	if billing != nil {
		priceReader = billing.Calculator
	}
	return routinghttp.NewChannelHandler(core, &routing.ChannelCatalog{Prices: priceReader, NamesByProvider: prices.ListModelNamesByProvider, QoderModels: qoder.DefaultRequestModelIDs})
}
