package legacybridge

import (
	"context"
	"github.com/TokenFlux/TokenRouter/internal/routing"
	dto "github.com/TokenFlux/TokenRouter/internal/routing/httpapi/dto"
	"github.com/TokenFlux/TokenRouter/internal/service"
)

// MarketplaceModels 只绑定原平台模型资格与账号读取，数据按请求投影，不持有第二份缓存。
func MarketplaceModels(gateway *service.GatewayService) routing.MarketplaceModels {
	return service.LegacyMarketplaceModels(gateway)
}

func RequestableModels(gateway *service.GatewayService) routing.RequestableResolver {
	return service.LegacyRequestableResolver(gateway)
}

// MarketplaceDefaults 仅投影旧平台目录；时间、时区及日志由 app 覆盖为显式装配。
func MarketplaceDefaults() routing.MarketplaceOptions {
	return service.LegacyMarketplaceOptions("")
}

// MarketplaceStats 只把原公开统计结果投影给消费者，查询与聚合仍归 S08。
type MarketplaceStats struct{ Source *service.DashboardService }

func (s MarketplaceStats) PublicStats(ctx context.Context) (dto.ModelMarketplaceStats, error) {
	v, err := s.Source.GetPublicDashboardStats(ctx)
	if err != nil {
		return dto.ModelMarketplaceStats{}, err
	}
	return dto.ModelMarketplaceStats{TodayTokens: v.TodayTokens, TotalTokens: v.TotalTokens, TotalUsers: v.TotalUsers}, nil
}

// ModelListReader 只投影原缓存回源入口，模型集合与失效规则归 routing。
func ModelListReader(repo service.AccountRepository) func(context.Context, *int64) ([]routing.CatalogueAccount, error) {
	return service.LegacyModelListReader(repo)
}
