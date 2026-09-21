package app

import (
	"context"

	"github.com/TokenFlux/TokenRouter/internal/apikey"
	keydto "github.com/TokenFlux/TokenRouter/internal/apikey/httpapi/dto"
	"github.com/TokenFlux/TokenRouter/internal/identity"
	identityhttp "github.com/TokenFlux/TokenRouter/internal/identity/httpapi"
	routing "github.com/TokenFlux/TokenRouter/internal/routing"
	routingdto "github.com/TokenFlux/TokenRouter/internal/routing/httpapi/dto"
	"github.com/TokenFlux/TokenRouter/internal/scheduler"
	"github.com/gin-gonic/gin"
)

// provideIdentityAdminHTTP 只投影 Key 展示和实时并发，管理规则由 identity 执行。
func provideIdentityAdminHTTP(admin *identity.UserAdmin, keys *apikey.Admin, concurrency *scheduler.ConcurrencyService, totp *identity.TotpService, users *identity.UserService, settings *identity.RuntimeSettings) *identityhttp.AdminUserHandler[keydto.APIKey[routingdto.Group]] {
	listKeys := func(ctx context.Context, id int64, page, size int, sortBy, order string) ([]keydto.APIKey[routingdto.Group], int64, error) {
		rows, total, err := keys.GetUserAPIKeys(ctx, id, page, size, sortBy, order)
		if err != nil {
			return nil, 0, err
		}
		result := make([]keydto.APIKey[routingdto.Group], 0, len(rows))
		for i := range rows {
			result = append(result, *keydto.APIKeyFromKey(&rows[i], func(group *routing.Group) *routingdto.Group {
				return routingdto.GroupFromRouting(apikey.RoutingGroup(group))
			}))
		}
		return result, total, nil
	}
	readConcurrency := func(ctx context.Context, users []identity.User) (map[int64]int, error) {
		views := make([]scheduler.UserWithConcurrency, len(users))
		for i, user := range users {
			views[i] = scheduler.UserWithConcurrency{ID: user.ID, MaxConcurrency: user.Concurrency}
		}
		loads, err := concurrency.GetUsersLoadBatch(ctx, views)
		result := make(map[int64]int, len(loads))
		for id, load := range loads {
			if load != nil {
				result[id] = load.CurrentConcurrency
			}
		}
		return result, err
	}
	return identityhttp.NewAdminUserHandler(admin, listKeys, readConcurrency, func(c *gin.Context) bool {
		return identityhttp.EnforceStepUp(c, totp, users, settings)
	})
}
