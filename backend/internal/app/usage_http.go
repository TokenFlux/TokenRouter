// 用量 HTTP 直接读取新用量实例和身份/Key 投影，旧网关 metadata 只在此转换。
package app

import (
	"context"

	"github.com/TokenFlux/TokenRouter/internal/apikey"
	"github.com/TokenFlux/TokenRouter/internal/identity"

	opscore "github.com/TokenFlux/TokenRouter/internal/ops"
	"github.com/TokenFlux/TokenRouter/internal/pkg/timezone"
	"github.com/TokenFlux/TokenRouter/internal/usage"

	usagehttp "github.com/TokenFlux/TokenRouter/internal/usage/httpapi"

	usageadmin "github.com/TokenFlux/TokenRouter/internal/usage/httpapi/admin"
	"github.com/TokenFlux/TokenRouter/internal/usage/httpapi/ports"
)

func provideUsageKeys(wrapper *apikey.APIKeyService) ports.KeyReader {
	keys := wrapper
	return ports.KeyQueries{Lookup: func(ctx context.Context, id int64) (*ports.KeyReference, error) {
		v, e := keys.GetByID(ctx, id)
		if v == nil {
			return nil, e
		}
		return &ports.KeyReference{ID: v.ID, UserID: v.UserID, Name: v.Name}, e
	}, Ownership: func(ctx context.Context, id int64, ids []int64) ([]int64, error) {
		return keys.VerifyOwnership(ctx, id, ids)
	}, Search: func(ctx context.Context, id int64, q string, n int) ([]ports.KeyReference, error) {
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
func provideUsageUsers(users *identity.UserAdmin) ports.UserReader {
	return ports.UserQueries(func(ctx context.Context, page, size int, f ports.UserListFilters, sort, order string) ([]ports.UserReference, int64, error) {
		rows, total, e := users.ListUsers(ctx, page, size, identity.UserListFilters{Search: f.Search, IncludeDeleted: f.IncludeDeleted}, sort, order)
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
func provideUsageHTTP(s *usage.UsageService, keys ports.KeyReader, ops *opscore.OpsService, settings *usage.RuntimeSettings, calendar timezone.Calendar) *usagehttp.UsageHandler {
	return usagehttp.NewUsageHandler(s, keys, ops, settings, calendar)
}
func provideAdminUsageHTTP(s *usage.UsageService, keys ports.KeyReader, users ports.UserReader, cleanup *usage.UsageCleanupService, ops *opscore.OpsService, calendar timezone.Calendar) *usageadmin.UsageHandler {
	return usageadmin.NewUsageHandler(s, keys, users, cleanup, ops, calendar)
}
func provideDashboardHTTP(s *usage.DashboardService, calendar timezone.Calendar) *usageadmin.DashboardHandler {
	return usageadmin.NewDashboardHandler(s, calendar)
}
