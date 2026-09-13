package app

import (
	"context"
	"github.com/TokenFlux/TokenRouter/internal/apikey"
	keydto "github.com/TokenFlux/TokenRouter/internal/apikey/httpapi/dto"
	"github.com/TokenFlux/TokenRouter/internal/app/legacybridge"
	"github.com/TokenFlux/TokenRouter/internal/billing"
	"github.com/TokenFlux/TokenRouter/internal/pkg/timezone"
	"github.com/TokenFlux/TokenRouter/internal/routing"
	routinghttp "github.com/TokenFlux/TokenRouter/internal/routing/httpapi"
	groupdto "github.com/TokenFlux/TokenRouter/internal/routing/httpapi/dto"
	"github.com/TokenFlux/TokenRouter/internal/service"
)

func provideGroupRateAdmin(repo billing.UserGroupRateRepository, keys *apikey.APIKeyService) *billing.GroupRateAdmin {
	return billing.NewGroupRateAdmin(repo, keys)
}

// provideRoutingGroupHTTP 组合已迁用例和只读展示投影，HTTP 直接使用 routing handler。
func provideRoutingGroupHTTP(core *routing.GroupAdmin, capacity *routing.CapacityService, keys *apikey.Admin, rates *billing.GroupRateAdmin, dashboard *service.DashboardService) *routinghttp.GroupHandler {
	resources := routinghttp.GroupResources{
		Capacity: capacity, Rates: rates, Today: timezone.Today,
		UsageSummary: legacybridge.GroupUsageReports(dashboard), LiveCapability: legacybridge.GroupLiveCapability,
		Keys: func(ctx context.Context, id int64, page, size int) ([]keydto.APIKey[groupdto.Group], int64, error) {
			values, total, err := keys.GetGroupAPIKeys(ctx, id, page, size)
			if err != nil {
				return nil, 0, err
			}
			out := make([]keydto.APIKey[groupdto.Group], 0, len(values))
			for i := range values {
				out = append(out, *keydto.APIKeyFromKey(&values[i], func(g *apikey.Group) *groupdto.Group { return groupdto.GroupFromRouting(apikey.RoutingGroup(g)) }))
			}
			return out, total, nil
		},
	}
	return routinghttp.NewGroupHandler(core, resources)
}
