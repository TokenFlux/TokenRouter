// 旧管理员用量构造只投影查询端口，不持有规则或缓存。
package admin

import (
	"context"

	"github.com/TokenFlux/TokenRouter/internal/service"
	"github.com/TokenFlux/TokenRouter/internal/usage"
	native "github.com/TokenFlux/TokenRouter/internal/usage/httpapi/admin"
	"github.com/TokenFlux/TokenRouter/internal/usage/httpapi/ports"
)

type UsageHandler = native.UsageHandler
type CreateUsageCleanupTaskRequest = native.CreateUsageCleanupTaskRequest

func NewUsageHandler(s *service.UsageService, keys *service.APIKeyService, users service.AdminService, cleanup *service.UsageCleanupService, timing *service.OpsService) *UsageHandler {
	var core *usage.UsageService
	if s != nil {
		core = s.UsageService
	}
	var times ports.Timings
	if timing != nil {
		times = timing
	}
	return native.NewUsageHandler(core, legacyUsageKeys(keys), legacyUsageUsers(users), cleanup, times)
}
func legacyUsageKeys(keys *service.APIKeyService) ports.KeyReader {
	if keys == nil {
		return nil
	}
	return ports.KeyQueries{Lookup: func(ctx context.Context, id int64) (*ports.KeyReference, error) {
		v, e := keys.GetByID(ctx, id)
		if v == nil {
			return nil, e
		}
		return &ports.KeyReference{ID: v.ID, UserID: v.UserID, Name: v.Name}, e
	}, Ownership: keys.VerifyOwnership, Search: func(ctx context.Context, id int64, q string, n int) ([]ports.KeyReference, error) {
		rows, e := keys.SearchAPIKeys(ctx, id, q, n)
		if e != nil {
			return nil, e
		}
		out := make([]ports.KeyReference, len(rows))
		for i, v := range rows {
			out[i] = ports.KeyReference{ID: v.ID, UserID: v.UserID, Name: v.Name}
		}
		return out, nil
	}}
}
func legacyUsageUsers(users service.AdminService) ports.UserReader {
	if users == nil {
		return nil
	}
	return ports.UserQueries(func(ctx context.Context, page, size int, f ports.UserListFilters, sort, order string) ([]ports.UserReference, int64, error) {
		rows, total, e := users.ListUsers(ctx, page, size, service.UserListFilters{Search: f.Search, IncludeDeleted: f.IncludeDeleted}, sort, order)
		if e != nil {
			return nil, total, e
		}
		out := make([]ports.UserReference, len(rows))
		for i, v := range rows {
			out[i] = ports.UserReference{ID: v.ID, Email: v.Email, DeletedAt: v.DeletedAt}
		}
		return out, total, nil
	})
}
