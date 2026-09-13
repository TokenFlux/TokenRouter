// 本文件维护 handler 的所属能力；兼容入口复用唯一实现。
package handler

import (
	context "context"
	routinghttp "github.com/TokenFlux/TokenRouter/internal/routing/httpapi"
	dto "github.com/TokenFlux/TokenRouter/internal/routing/httpapi/dto"
	service "github.com/TokenFlux/TokenRouter/internal/service"
)

// ModelMarketplaceHandler 保留旧构造名称；生产路由直接绑定 routing/httpapi。
type ModelMarketplaceHandler = routinghttp.MarketplaceHandler

func NewModelMarketplaceHandler(marketplace *service.ModelMarketplaceService, dashboard *service.DashboardService) *ModelMarketplaceHandler {
	return routinghttp.NewMarketplaceHandler(marketplace.CoreMarketplace(), legacyMarketplaceStats{dashboard})
}

type legacyMarketplaceStats struct{ source *service.DashboardService }

func (s legacyMarketplaceStats) PublicStats(ctx context.Context) (dto.ModelMarketplaceStats, error) {
	stats, err := s.source.GetPublicDashboardStats(ctx)
	if err != nil {
		return dto.ModelMarketplaceStats{}, err
	}
	return dto.ModelMarketplaceStats{TodayTokens: stats.TodayTokens, TotalTokens: stats.TotalTokens, TotalUsers: stats.TotalUsers}, nil
}
