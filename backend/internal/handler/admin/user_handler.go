// 本文件维护 admin 的所属能力；兼容入口复用唯一实现。
package admin

import (
	context "context"
	dto "github.com/TokenFlux/TokenRouter/internal/handler/dto"
	identity "github.com/TokenFlux/TokenRouter/internal/identity"
	identityhttp "github.com/TokenFlux/TokenRouter/internal/identity/httpapi"
	middleware "github.com/TokenFlux/TokenRouter/internal/server/middleware"
	service "github.com/TokenFlux/TokenRouter/internal/service"
	gin "github.com/gin-gonic/gin"
)

type UserHandler = identityhttp.AdminUserHandler[dto.APIKey]
type UserWithConcurrency = identityhttp.UserWithConcurrency[dto.APIKey]

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
	keys := func(ctx context.Context, id int64, page, size int, sortBy, order string) ([]dto.APIKey, int64, error) {
		keys, total, e := adminService.GetUserAPIKeys(ctx, id, page, size, sortBy, order)
		if e != nil {
			return nil, 0, e
		}
		out := make([]dto.APIKey, 0, len(keys))
		for i := range keys {
			out = append(out, *dto.APIKeyFromService(&keys[i]))
		}
		return out, total, nil
	}
	return identityhttp.NewAdminUserHandler(newIdentityUserAdministration(adminService), keys, concurrency, func(c *gin.Context) bool {
		return middleware.EnforceStepUp(c, totpService, userService, settingService)
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
