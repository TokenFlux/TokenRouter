// 本文件维护 dto 的所属能力；兼容入口复用唯一实现。
package dto

import (
	newdto "github.com/TokenFlux/TokenRouter/internal/routing/httpapi/dto"
	service "github.com/TokenFlux/TokenRouter/internal/service"
)

type ModelMarketplaceStats = newdto.ModelMarketplaceStats

type ModelMarketplacePricing = newdto.ModelMarketplacePricing

type ModelMarketplacePricingInterval = newdto.ModelMarketplacePricingInterval

type ModelMarketplaceModel = newdto.ModelMarketplaceModel

type ModelMarketplaceCapacity = newdto.ModelMarketplaceCapacity

type ModelMarketplaceAvailabilityDay = newdto.ModelMarketplaceAvailabilityDay

type ModelMarketplaceAvailability = newdto.ModelMarketplaceAvailability

type ModelMarketplaceGroup = newdto.ModelMarketplaceGroup

func ModelMarketplaceGroupsFromService(groups []service.ModelMarketplaceGroup) []ModelMarketplaceGroup {
	return newdto.ModelMarketplaceGroupsFromRouting(groups)
}

func ModelMarketplaceStatsFromService(stats *service.DashboardPublicStats) ModelMarketplaceStats {
	return newdto.ModelMarketplaceStats{TodayTokens: stats.TodayTokens, TotalTokens: stats.TotalTokens, TotalUsers: stats.TotalUsers}
}
