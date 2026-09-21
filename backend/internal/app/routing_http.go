package app

import (
	"context"
	"time"

	"github.com/TokenFlux/TokenRouter/internal/apikey"
	"github.com/TokenFlux/TokenRouter/internal/usage"

	keydto "github.com/TokenFlux/TokenRouter/internal/apikey/httpapi/dto"
	"github.com/TokenFlux/TokenRouter/internal/billing"
	"github.com/TokenFlux/TokenRouter/internal/pkg/timezone"
	"github.com/TokenFlux/TokenRouter/internal/routing"

	routinghttp "github.com/TokenFlux/TokenRouter/internal/routing/httpapi"

	groupdto "github.com/TokenFlux/TokenRouter/internal/routing/httpapi/dto"
	"github.com/TokenFlux/TokenRouter/internal/upstream/openai/liveattestation"
)

func provideGroupRateAdmin(repo billing.UserGroupRateRepository, keys *apikey.APIKeyService) *billing.GroupRateAdmin {
	return billing.NewGroupRateAdmin(repo, keys)
}

// provideRoutingGroupHTTP 组合已迁用例和只读展示投影，HTTP 直接使用 routing handler。
func provideRoutingGroupHTTP(core *routing.GroupAdmin, capacity *routing.CapacityService, keys *apikey.Admin, rates *billing.GroupRateAdmin, dashboard *usage.DashboardService, calendar timezone.Calendar) *routinghttp.GroupHandler {
	resources := routinghttp.GroupResources{
		Capacity: capacity, Rates: rates, Today: calendar.Today,
		UsageSummary: func(ctx context.Context, today time.Time) ([]routinghttp.GroupUsageSummary, error) {
			values, err := dashboard.GetGroupUsageSummary(ctx, today)
			if values == nil {
				return nil, err
			}
			out := make([]routinghttp.GroupUsageSummary, len(values))
			for i, value := range values {
				out[i] = routinghttp.GroupUsageSummary{GroupID: value.GroupID, TodayCost: value.TodayCost, YesterdayCost: value.YesterdayCost, TotalCost: value.TotalCost}
			}
			return out, err
		},
		LiveCapability: func(ctx context.Context) error {
			return liveattestation.NewProvider().Check(ctx)
		},
		Keys: func(ctx context.Context, id int64, page, size int) ([]keydto.APIKey[groupdto.Group], int64, error) {
			values, total, err := keys.GetGroupAPIKeys(ctx, id, page, size)
			if err != nil {
				return nil, 0, err
			}
			out := make([]keydto.APIKey[groupdto.Group], 0, len(values))
			for i := range values {
				out = append(out, *keydto.APIKeyFromKey(&values[i], func(g *routing.Group) *groupdto.Group { return groupdto.GroupFromRouting(apikey.RoutingGroup(g)) }))
			}
			return out, total, nil
		},
	}
	return routinghttp.NewGroupHandler(core, resources)
}
