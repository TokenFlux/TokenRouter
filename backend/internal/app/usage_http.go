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

// usage_http.go assembles usage HTTP handlers and adapts Key and user lookups
// for their queries and background exports.
// Usage HTTP reads narrow Key and user references through their owning
// services. Key ownership checks stay with API-Key operations, user search
// retains pagination and deleted status, and user/dashboard/admin handlers
// share the calendar and background export service used by the underlying usage
// services.

// provideUsageKeys binds Key lookup, ownership verification, and search for
// usage HTTP. Lookup retains missing Key results, search converts available
// rows into identity/name references, and ownership delegates directly to the
// supplied service.
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

// provideUsageUsers binds administrator user search and pagination for usage
// HTTP. Search and include-deleted filters pass through, available DeletedAt
// pointers are retained, and a query error returns the source pagination total
// alongside nil rows.
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

// provideUsageHTTP combines user usage, Key reads, observations, ranking
// settings, dates, and background export task handling. Nil exports leave the
// handler's export routes returning their unconfigured error response.
func provideUsageHTTP(s *usage.UsageService, keys ports.KeyReader, ops *opscore.OpsService, settings *usage.RuntimeSettings, exports *usage.UsageExportService, calendar timezone.Calendar) *usagehttp.UsageHandler {
	h := usagehttp.NewUsageHandler(s, keys, ops, settings, calendar)
	h.SetExportService(exports)
	return h
}

// provideAdminUsageHTTP combines administrator usage, identity reads, cleanup,
// background exports, observations, and dates. Nil exports leave administrator
// export routes returning their unconfigured error response.
func provideAdminUsageHTTP(s *usage.UsageService, keys ports.KeyReader, users ports.UserReader, cleanup *usage.UsageCleanupService, exports *usage.UsageExportService, ops *opscore.OpsService, calendar timezone.Calendar) *usageadmin.UsageHandler {
	h := usageadmin.NewUsageHandler(s, keys, users, cleanup, calendar)
	h.SetExportService(exports)
	return h
}

// provideDashboardHTTP exposes assembled dashboard queries with the shared
// application calendar.
func provideDashboardHTTP(s *usage.DashboardService, calendar timezone.Calendar) *usageadmin.DashboardHandler {
	return usageadmin.NewDashboardHandler(s, calendar)
}
