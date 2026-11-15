package admin

import (
	"github.com/TokenFlux/TokenRouter/internal/server/adminapi"
)

// management_operations.go describes registered administrative HTTP operations for
// MCP discovery and permission selection. Request schemas reference the same input
// types decoded by handlers; metadata records query arguments, side effects, and
// browser confirmation requirements before the shared executor admits a call.

// ManagementOperations supplies stable permission identities and parameter descriptions
// for this module. Registrars mount these descriptions together with existing HTTP handlers.
var ManagementOperations = map[string]*adminapi.Operation{
	"dashboard.api_keys_trend.list": adminapi.Define(adminapi.Operation{
		ID:     "dashboard.api_keys_trend.list",
		Module: "dashboard",
		Title:  "Get API Key Usage Trend",
		Description: "GetAPIKeyUsageTrend returns cached top-key buckets under the parsed chart bounds. An invalid " +
			"or nonpositive limit selects five; granularity is passed to the service, and the response " +
			"includes cache-hit status. This operation is available through read_operation.",
		Method:      "GET",
		Path:        "/api/v1/admin/dashboard/api-keys-trend",
		ReadOnly:    true,
		Response:    "json",
		BrowserPath: "/admin/dashboard",
		Input:       adminapi.Arguments("/api/v1/admin/dashboard/api-keys-trend", []string{"end_date", "granularity", "limit", "start_date", "timezone"}, nil),
	}),
	"dashboard.api_keys_usage.create": adminapi.Define(adminapi.Operation{
		ID:     "dashboard.api_keys_usage.create",
		Module: "dashboard",
		Title:  "Get Batch API Keys Usage",
		Description: "GetBatchAPIKeysUsage normalizes submitted IDs and returns cached batch key charges. An empty " +
			"normalized set returns an empty stats object; key-cache entries can reuse today totals " +
			"across midnight until expiration. Omitted body fields follow the HTTP handler defaults; " +
			"explicit null and empty collections retain their documented meanings.",
		Method:      "POST",
		Path:        "/api/v1/admin/dashboard/api-keys-usage",
		ReadOnly:    true,
		Response:    "json",
		BrowserPath: "/admin/dashboard",
		Input:       adminapi.Arguments("/api/v1/admin/dashboard/api-keys-usage", []string{}, (*BatchAPIKeysUsageRequest)(nil)),
	}),
	"dashboard.groups.list": adminapi.Define(adminapi.Operation{
		ID:      "dashboard.groups.list",
		Module:  "dashboard",
		Title:   "Get Group Stats",
		TitleZH: "查询分组统计",
		Description: "GetGroupStats returns cached group grouping under parsed bounds and optional selectors. " +
			"Request type takes precedence over stream text, invalid ID text leaves selectors unset, and " +
			"the response exposes cache-hit status. This operation is available through read_operation.",
		Method:      "GET",
		Path:        "/api/v1/admin/dashboard/groups",
		ReadOnly:    true,
		Response:    "json",
		BrowserPath: "/admin/dashboard",
		Input:       adminapi.Arguments("/api/v1/admin/dashboard/groups", []string{"api_key_id", "billing_type", "end_date", "group_id", "provider_id", "request_source", "request_type", "start_date", "stream", "team_id", "timezone", "user_id"}, nil),
	}),
	"dashboard.models.list": adminapi.Define(adminapi.Operation{
		ID:     "dashboard.models.list",
		Module: "dashboard",
		Title:  "Get Model Stats",
		Description: "GetModelStats returns cached model grouping under parsed bounds and selectors. It validates " +
			"the selected requested, upstream or mapping source, gives request type precedence over " +
			"stream text, and exposes cache-hit status. This operation is available through " +
			"read_operation.",
		Method:      "GET",
		Path:        "/api/v1/admin/dashboard/models",
		ReadOnly:    true,
		Response:    "json",
		BrowserPath: "/admin/dashboard",
		Input:       adminapi.Arguments("/api/v1/admin/dashboard/models", []string{"api_key_id", "billing_type", "end_date", "group_id", "model_source", "provider_id", "request_source", "request_type", "start_date", "stream", "team_id", "timezone", "user_id"}, nil),
	}),
	"dashboard.realtime.list": adminapi.Define(adminapi.Operation{
		ID:     "dashboard.realtime.list",
		Module: "dashboard",
		Title:  "Get Realtime Metrics",
		Description: "GetRealtimeMetrics returns fixed zero values for the dashboard's placeholder realtime " +
			"fields. It samples no live activity, so the response describes this endpoint's current " +
			"placeholder state even while other reports contain measured usage. This operation is " +
			"available through read_operation.",
		Method:      "GET",
		Path:        "/api/v1/admin/dashboard/realtime",
		ReadOnly:    true,
		Response:    "json",
		BrowserPath: "/admin/dashboard",
		Input:       adminapi.Arguments("/api/v1/admin/dashboard/realtime", []string{}, nil),
	}),
	"dashboard.snapshot_v2.list": adminapi.Define(adminapi.Operation{
		ID:     "dashboard.snapshot_v2.list",
		Module: "dashboard",
		Title:  "Get Snapshot V2",
		Description: "GetSnapshotV2 returns the selected administrator dashboard sections from a combined HTTP " +
			"cache. It normalizes granularity and inclusion defaults, validates selectors, then captures " +
			"those values before invoking the shared loader.  The captured values define both key " +
			"encoding and query inputs, keeping Gin access in the HTTP waiter. Matching keys share work " +
			"with independent waiter cancellation; query errors reject the complete response. The stored " +
			"payload supplies ETag and Vary headers. A matching If-None-Match ends with 304 before the " +
			"body and X-Snapshot-Cache header are written. This operation is available through " +
			"read_operation.",
		Method:      "GET",
		Path:        "/api/v1/admin/dashboard/snapshot-v2",
		ReadOnly:    true,
		Response:    "json",
		BrowserPath: "/admin/dashboard",
		Input:       adminapi.Arguments("/api/v1/admin/dashboard/snapshot-v2", []string{"api_key_id", "billing_type", "end_date", "granularity", "group_id", "include_group_stats", "include_model_stats", "include_stats", "include_trend", "include_users_trend", "model", "provider_id", "request_source", "request_type", "start_date", "stream", "team_id", "timezone", "user_id", "users_trend_limit"}, nil),
	}),
	"dashboard.stats.list": adminapi.Define(adminapi.Operation{
		ID:     "dashboard.stats.list",
		Module: "dashboard",
		Title:  "Get Stats",
		Description: "GetStats returns administrator dashboard metrics and whole-second uptime since handler " +
			"construction. The dashboard service determines cache and aggregation handling; a failed " +
			"query becomes the handler's generic internal-error response. This operation is available " +
			"through read_operation.",
		Method:      "GET",
		Path:        "/api/v1/admin/dashboard/stats",
		ReadOnly:    true,
		Response:    "json",
		BrowserPath: "/admin/dashboard",
		Input:       adminapi.Arguments("/api/v1/admin/dashboard/stats", []string{}, nil),
	}),
	"dashboard.trend.list": adminapi.Define(adminapi.Operation{
		ID:     "dashboard.trend.list",
		Module: "dashboard",
		Title:  "Get Usage Trend",
		Description: "GetUsageTrend returns cached trend buckets under the parsed dashboard bounds and optional " +
			"selectors. Request type takes precedence over stream text, invalid ID text leaves selectors " +
			"unset, and query failures return the generic trend error. This operation is available " +
			"through read_operation.",
		Method:      "GET",
		Path:        "/api/v1/admin/dashboard/trend",
		ReadOnly:    true,
		Response:    "json",
		BrowserPath: "/admin/dashboard",
		Input:       adminapi.Arguments("/api/v1/admin/dashboard/trend", []string{"api_key_id", "billing_type", "end_date", "granularity", "group_id", "model", "provider_id", "request_source", "request_type", "start_date", "stream", "team_id", "timezone", "user_id"}, nil),
	}),
	"dashboard.user_breakdown.list": adminapi.Define(adminapi.Operation{
		ID:     "dashboard.user_breakdown.list",
		Module: "dashboard",
		Title:  "Get User Breakdown",
		Description: "GetUserBreakdown returns payer rows within the requested dimensions and actor selectors. " +
			"Request type and stream are both retained; limit defaults to fifty unless a supplied value " +
			"is between one and two hundred. This operation is available through read_operation.",
		Method:      "GET",
		Path:        "/api/v1/admin/dashboard/user-breakdown",
		ReadOnly:    true,
		Response:    "json",
		BrowserPath: "/admin/dashboard",
		Input:       adminapi.Arguments("/api/v1/admin/dashboard/user-breakdown", []string{"api_key_id", "billing_type", "end_date", "endpoint", "endpoint_type", "group_id", "limit", "model", "model_source", "provider_id", "request_type", "sort_by", "start_date", "stream", "timezone", "user_id"}, nil),
	}),
	"dashboard.users_ranking.list": adminapi.Define(adminapi.Operation{
		ID:     "dashboard.users_ranking.list",
		Module: "dashboard",
		Title:  "Get User Spending Ranking",
		Description: "GetUserSpendingRanking returns cached payer spending rows and full-range totals under a " +
			"capped limit. Display dates use the stored RankingReport range, which can retain the first " +
			"cache entry's time locations. This operation is available through read_operation.",
		Method:      "GET",
		Path:        "/api/v1/admin/dashboard/users-ranking",
		ReadOnly:    true,
		Response:    "json",
		BrowserPath: "/admin/dashboard",
		Input:       adminapi.Arguments("/api/v1/admin/dashboard/users-ranking", []string{"end_date", "limit", "start_date", "timezone"}, nil),
	}),
	"dashboard.users_trend.list": adminapi.Define(adminapi.Operation{
		ID:     "dashboard.users_trend.list",
		Module: "dashboard",
		Title:  "Get User Usage Trend",
		Description: "GetUserUsageTrend returns cached top-payer buckets under the parsed chart bounds. An invalid " +
			"or nonpositive limit selects twelve; granularity is passed to the service, and the response " +
			"includes cache-hit status. This operation is available through read_operation.",
		Method:      "GET",
		Path:        "/api/v1/admin/dashboard/users-trend",
		ReadOnly:    true,
		Response:    "json",
		BrowserPath: "/admin/dashboard",
		Input:       adminapi.Arguments("/api/v1/admin/dashboard/users-trend", []string{"end_date", "granularity", "limit", "start_date", "timezone"}, nil),
	}),
	"dashboard.users_usage.create": adminapi.Define(adminapi.Operation{
		ID:     "dashboard.users_usage.create",
		Module: "dashboard",
		Title:  "Get Batch Users Usage",
		Description: "GetBatchUsersUsage normalizes submitted IDs and returns cached batch user charges. An empty " +
			"normalized set returns an empty stats object; user cache keys include the calendar date for " +
			"their today totals. Omitted body fields follow the HTTP handler defaults; explicit null and " +
			"empty collections retain their documented meanings.",
		Method:      "POST",
		Path:        "/api/v1/admin/dashboard/users-usage",
		ReadOnly:    true,
		Response:    "json",
		BrowserPath: "/admin/dashboard",
		Input:       adminapi.Arguments("/api/v1/admin/dashboard/users-usage", []string{}, (*BatchUsersUsageRequest)(nil)),
	}),
	"usage.cleanup_tasks.cancel.create": adminapi.Define(adminapi.Operation{
		ID:     "usage.cleanup_tasks.cancel.create",
		Module: "usage",
		Title:  "Cancel Cleanup Task",
		Description: "CancelCleanupTask records cancellation for a positive task ID and authenticated operator. A " +
			"missing cleanup service returns unavailable; the service checks eligible states and the " +
			"response reports canceled after its conditional update succeeds.",
		Method:      "POST",
		Path:        "/api/v1/admin/usage/cleanup-tasks/:id/cancel",
		Response:    "json",
		BrowserPath: "/admin/usage",
		Input:       adminapi.Arguments("/api/v1/admin/usage/cleanup-tasks/:id/cancel", []string{}, nil),
	}),
	"usage.cleanup_tasks.create": adminapi.Define(adminapi.Operation{
		ID:     "usage.cleanup_tasks.create",
		Module: "usage",
		Title:  "Create Cleanup Task",
		Description: "CreateCleanupTask validates date-only bounds and creates a persisted administrator deletion " +
			"task through the idempotency executor. A positive authenticated operator is required, and " +
			"the request category takes precedence over its legacy stream selector.  The end date becomes " +
			"an inclusive instant by adding a fixed twenty-four hours minus one nanosecond. The " +
			"idempotency payload includes the operator and date-trimmed request body, while " +
			"UsageCleanupService sanitizes stored selectors and dispatches execution after persistence. " +
			"The response represents accepted task creation, with deletion progress available through " +
			"later task reads. Omitted body fields follow the HTTP handler defaults; explicit null and " +
			"empty collections retain their documented meanings.",
		Method:      "POST",
		Path:        "/api/v1/admin/usage/cleanup-tasks",
		Response:    "json",
		BrowserPath: "/admin/usage",
		Idempotent:  true,
		Input:       adminapi.Arguments("/api/v1/admin/usage/cleanup-tasks", []string{}, (*CreateUsageCleanupTaskRequest)(nil)),
	}),
	"usage.cleanup_tasks.list": adminapi.Define(adminapi.Operation{
		ID:     "usage.cleanup_tasks.list",
		Module: "usage",
		Title:  "List Cleanup Tasks",
		Description: "ListCleanupTasks returns the persisted cleanup page formatted through CleanupFromUsage. A " +
			"missing cleanup service returns unavailable; administrator authentication belongs to the " +
			"registered parent route, and operator identity is recorded for diagnostics. This operation " +
			"is available through read_operation.",
		Method:      "GET",
		Path:        "/api/v1/admin/usage/cleanup-tasks",
		ReadOnly:    true,
		Response:    "json",
		BrowserPath: "/admin/usage",
		Input:       adminapi.Arguments("/api/v1/admin/usage/cleanup-tasks", []string{"page", "page_size"}, nil),
	}),
	"usage.export_tasks.cancel.create": adminapi.Define(adminapi.Operation{
		ID:     "usage.export_tasks.cancel.create",
		Module: "usage",
		Title:  "Cancel Export Task",
		Description: "CancelExportTask cancels a pending or running administrator usage export task and returns " +
			"the updated task state. Terminal tasks return conflict and missing tasks return not found.",
		Method:      "POST",
		Path:        "/api/v1/admin/usage/export-tasks/:id/cancel",
		Response:    "json",
		BrowserPath: "/admin/usage",
		Input:       adminapi.Arguments("/api/v1/admin/usage/export-tasks/:id/cancel", []string{}, nil),
	}),
	"usage.export_tasks.create": adminapi.Define(adminapi.Operation{
		ID:     "usage.export_tasks.create",
		Module: "usage",
		Title:  "Create Export Task",
		Description: "CreateExportTask validates explicit half-open timestamps, an IANA timezone, and optional " +
			"administrator usage filters before persisting a background CSV export task through the " +
			"idempotency executor. Start and end bounds accept RFC3339 or local YYYY-MM-DDTHH:mm:ss " +
			"timestamps interpreted in timezone as [start_time, end_time). Omitted body fields follow " +
			"the HTTP handler defaults.",
		Method:      "POST",
		Path:        "/api/v1/admin/usage/export-tasks",
		Response:    "json",
		BrowserPath: "/admin/usage",
		Idempotent:  true,
		Input:       adminapi.Arguments("/api/v1/admin/usage/export-tasks", []string{}, (*CreateAdminUsageExportTaskRequest)(nil)),
	}),
	"usage.export_tasks.download.get": adminapi.Define(adminapi.Operation{
		ID:     "usage.export_tasks.download.get",
		Module: "usage",
		Title:  "Download Export Task CSV",
		Description: "DownloadExportTask streams the committed UTF-8 BOM CSV file for a succeeded administrator " +
			"export task. Expired files return HTTP 410 and unfinished tasks return conflict. This " +
			"operation is available through read_operation and delivers a protected file artifact.",
		Method:      "GET",
		Path:        "/api/v1/admin/usage/export-tasks/:id/download",
		ReadOnly:    true,
		Response:    adminapi.ResponseFile,
		BrowserPath: "/admin/usage",
		Input:       adminapi.Arguments("/api/v1/admin/usage/export-tasks/:id/download", []string{}, nil),
	}),
	"usage.export_tasks.download_ticket.create": adminapi.Define(adminapi.Operation{
		ID:     "usage.export_tasks.download_ticket.create",
		Module: "usage",
		Title:  "Create Export Download Ticket",
		Description: "CreateExportDownloadTicket issues a 60-second signed ticket and relative download URL for " +
			"streaming a succeeded administrator CSV export directly in the browser.",
		Method:      "POST",
		Path:        "/api/v1/admin/usage/export-tasks/:id/download-ticket",
		Response:    "json",
		BrowserPath: "/admin/usage",
		Input:       adminapi.Arguments("/api/v1/admin/usage/export-tasks/:id/download-ticket", []string{}, nil),
	}),
	"usage.export_tasks.list": adminapi.Define(adminapi.Operation{
		ID:     "usage.export_tasks.list",
		Module: "usage",
		Title:  "List Export Tasks",
		Description: "ListExportTasks returns a paginated list of administrator usage export tasks ordered newest " +
			"first, including row progress and committed CSV metadata. This operation is available " +
			"through read_operation.",
		Method:      "GET",
		Path:        "/api/v1/admin/usage/export-tasks",
		ReadOnly:    true,
		Response:    "json",
		BrowserPath: "/admin/usage",
		Input:       adminapi.Arguments("/api/v1/admin/usage/export-tasks", []string{"page", "page_size", "watch_ids"}, nil),
	}),
	"usage.list": adminapi.Define(adminapi.Operation{
		ID:     "usage.list",
		Module: "usage",
		Title:  "List",
		Description: "List returns an administrator usage page under validated scalar selectors and optional " +
			"independent time bounds. Request category takes precedence over stream, date-only ends " +
			"advance by calendar day, and exact_total requests the repository's full count. This " +
			"operation is available through read_operation. request_source selects user, system, or an individual system source; omission includes all sources.",
		Method:      "GET",
		Path:        "/api/v1/admin/usage",
		ReadOnly:    true,
		Response:    "json",
		BrowserPath: "/admin/usage",
		Input:       adminapi.Arguments("/api/v1/admin/usage", []string{"api_key_id", "billing_mode", "billing_type", "end_date", "exact_total", "group_id", "model", "native_compaction_v2", "page", "page_size", "provider_id", "request_id", "request_source", "request_type", "sort_by", "sort_order", "start_date", "stream", "team_id", "timezone", "user_id"}, nil),
	}),
	"usage.search_api_keys.list": adminapi.Define(adminapi.Operation{
		ID:     "usage.search_api_keys.list",
		Module: "usage",
		Title:  "Search API Keys",
		Description: "SearchAPIKeys returns up to thirty key suggestions under an optional actor-user filter. " +
			"Invalid user-ID text returns bad request; each result exposes the key ID, name and owner for " +
			"filter selection. This operation is available through read_operation.",
		Method:      "GET",
		Path:        "/api/v1/admin/usage/search-api-keys",
		ReadOnly:    true,
		Response:    "json",
		BrowserPath: "/admin/usage",
		Input:       adminapi.Arguments("/api/v1/admin/usage/search-api-keys", []string{"q", "user_id"}, nil),
	}),
	"usage.search_users.list": adminapi.Define(adminapi.Operation{
		ID:     "usage.search_users.list",
		Module: "usage",
		Title:  "Search Users",
		Description: "SearchUsers returns up to thirty email-ordered account suggestions for nonempty query text. " +
			"It includes deleted accounts and exposes their deletion flag, while empty query text returns " +
			"an empty successful list. This operation is available through read_operation.",
		Method:      "GET",
		Path:        "/api/v1/admin/usage/search-users",
		ReadOnly:    true,
		Response:    "json",
		BrowserPath: "/admin/usage",
		Input:       adminapi.Arguments("/api/v1/admin/usage/search-users", []string{"q"}, nil),
	}),
	"usage.stats.list": adminapi.Define(adminapi.Operation{
		ID:     "usage.stats.list",
		Module: "usage",
		Title:  "Stats",
		Description: "Stats returns filtered administrator totals and endpoint charts, optionally using the " +
			"statistics cache. Both explicit dates select a parsed range; other cases use the requested " +
			"today, week or month period through the current instant.  A request-ID query supplying " +
			"neither dates nor period clears the default bounds for retained-history lookup. " +
			"Endpoint-source validation selects all or one chart, and request category takes precedence " +
			"over legacy stream. nocache bypasses lookup and insertion for this response; it leaves " +
			"existing entries available to later cached requests. This operation is available through " +
			"read_operation. request_source applies to all totals and endpoint charts; incomplete_cost_requests counts partial and unknown expenses.",
		Method:      "GET",
		Path:        "/api/v1/admin/usage/stats",
		ReadOnly:    true,
		Response:    "json",
		BrowserPath: "/admin/usage",
		Input:       adminapi.Arguments("/api/v1/admin/usage/stats", []string{"api_key_id", "billing_mode", "billing_type", "end_date", "endpoint_source", "group_id", "model", "native_compaction_v2", "nocache", "period", "provider_id", "request_id", "request_source", "request_type", "start_date", "stream", "team_id", "timezone", "user_id"}, nil),
	}),
}
