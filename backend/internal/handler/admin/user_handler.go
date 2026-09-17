// 本文件维护 admin 的所属能力；兼容入口复用唯一实现。
package admin

import (
	apikey "github.com/TokenFlux/TokenRouter/internal/apikey"
	keydto "github.com/TokenFlux/TokenRouter/internal/apikey/httpapi/dto"
	routingdto "github.com/TokenFlux/TokenRouter/internal/routing/httpapi/dto"

	context "context"

	identity "github.com/TokenFlux/TokenRouter/internal/identity"
	identityhttp "github.com/TokenFlux/TokenRouter/internal/identity/httpapi"
	service "github.com/TokenFlux/TokenRouter/internal/service"
	gin "github.com/gin-gonic/gin"
)

type UserHandler = identityhttp.AdminUserHandler[keydto.APIKey[routingdto.Group]]
type UserWithConcurrency = identityhttp.UserWithConcurrency[keydto.APIKey[routingdto.Group]]

func NewUserHandler(
	adminService service.AdminService,
	concurrencyService *service.ConcurrencyService,
	totpService *service.TotpService,
	userService *service.UserService,
	settingService *service.SettingService,
) *UserHandler {
	var concurrency func(context.Context, []identity.User) (map[int64]int, error)
	if concurrencyService != nil {
		concurrency = func(ctx context.Context, users []identity.User) (map[int64]int, error) {
			views := make([]service.UserWithConcurrency, len(users))
			for i, u := range users {
				views[i] = service.UserWithConcurrency{ID: u.ID, MaxConcurrency: u.Concurrency}
			}
			loads, e := concurrencyService.GetUsersLoadBatch(ctx, views)
			out := make(map[int64]int, len(loads))
			for id, v := range loads {
				if v != nil {
					out[id] = v.CurrentConcurrency
				}
			}
			return out, e
		}
	}
	keys := func(ctx context.Context, id int64, page, size int, sortBy, order string) ([]keydto.APIKey[routingdto.Group], int64, error) {
		keys, total, e := adminService.GetUserAPIKeys(ctx, id, page, size, sortBy, order)
		if e != nil {
			return nil, 0, e
		}
		out := make([]keydto.APIKey[routingdto.Group], 0, len(keys))
		for i := range keys {
			out = append(out, *keydto.APIKeyFromKey(service.APIKeyView(&keys[i]), func(g *apikey.Group) *routingdto.Group { return routingdto.GroupFromRouting(apikey.RoutingGroup(g)) }))
		}
		return out, total, nil
	}
	return identityhttp.NewAdminUserHandler(newIdentityUserAdministration(adminService), keys, concurrency, func(c *gin.Context) bool {
		return identityhttp.EnforceStepUp(c, totpService, identityStepUpUser(userService), identityStepUpSettings(settingService))
	})
}

type CreateUserRequest = identityhttp.CreateUserRequest
type UpdateUserRequest = identityhttp.UpdateUserRequest
type UpdateBalanceRequest = identityhttp.UpdateBalanceRequest
type BatchUpdateConcurrencyRequest = identityhttp.BatchUpdateConcurrencyRequest
type BindUserAuthIdentityRequest = identityhttp.BindUserAuthIdentityRequest
type BindUserAuthIdentityChannelRequest = identityhttp.BindUserAuthIdentityChannelRequest

type ReplaceGroupRequest = identityhttp.ReplaceGroupRequest
type BatchUpdateLimitsRequest = identityhttp.BatchUpdateLimitsRequest

// identityStepUpSettings 保留旧可空设置参数的门控语义，规则由 identity 解释。
func identityStepUpSettings(s *service.SettingService) identityhttp.StepUpSettingReader {
	if s == nil {
		return nil
	}
	return s.IdentitySettings()
}

// identityStepUpUser 保留旧构造可空依赖，未启用门控时不提前解引用。
func identityStepUpUser(s *service.UserService) identityhttp.UserReader {
	if s == nil {
		return nil
	}
	return s.UserService
}
