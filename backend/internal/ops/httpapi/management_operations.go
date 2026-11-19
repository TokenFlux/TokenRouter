package httpapi

import (
	"github.com/google/jsonschema-go/jsonschema"

	"github.com/TokenFlux/TokenRouter/internal/ops"
	"github.com/TokenFlux/TokenRouter/internal/server/adminapi"
)

// management_operations.go describes registered administrative HTTP operations for
// MCP discovery and permission selection. Request schemas reference the same input
// types decoded by handlers; metadata records query arguments, side effects, and
// browser confirmation requirements before the shared executor admits a call.

// ManagementOperations supplies stable permission identities and parameter descriptions
// for this module. Registrars mount these descriptions together with existing HTTP handlers.
var ManagementOperations = map[string]*adminapi.Operation{
	"ops.advanced_settings.list": adminapi.Define(adminapi.Operation{
		ID:     "ops.advanced_settings.list",
		Module: "ops",
		Title:  "Get Advanced Settings",
		Description: "GetAdvancedSettings returns the service's current in-memory retention and observation " +
			"preferences after checking monitoring. The getter reads its runtime snapshot; the handler's " +
			"error branch uses a generic internal-server response. This operation is available through " +
			"read_operation.",
		Method:      "GET",
		Path:        "/api/v1/admin/ops/advanced-settings",
		ReadOnly:    true,
		Response:    "json",
		BrowserPath: "/admin/ops",
		Input:       adminapi.Arguments("/api/v1/admin/ops/advanced-settings", []string{}, nil),
	}),
	"ops.advanced_settings.update": adminapi.Define(adminapi.Operation{
		ID:     "ops.advanced_settings.update",
		Module: "ops",
		Title:  "Update Advanced Settings",
		Description: "UpdateAdvancedSettings binds advanced Ops preferences and delegates validation, persistence, " +
			"and runtime refresh after the monitoring check. Invalid JSON and service errors return bad " +
			"request; this body uses ordinary Gin binding. Omitted body fields follow the HTTP handler " +
			"defaults; explicit null and empty collections retain their documented meanings.",
		Method:      "PUT",
		Path:        "/api/v1/admin/ops/advanced-settings",
		Response:    "json",
		BrowserPath: "/admin/ops",
		Input:       adminapi.Arguments("/api/v1/admin/ops/advanced-settings", []string{}, (*ops.OpsAdvancedSettings)(nil)),
	}),
	"ops.alert_events.get": adminapi.Define(adminapi.Operation{
		ID:     "ops.alert_events.get",
		Module: "ops",
		Title:  "Get Alert Event",
		Description: "GetAlertEvent loads a positive decimal event identity after checking monitoring. Invalid " +
			"route IDs return bad request, and missing-event or storage errors are mapped by the service " +
			"and shared response helper. This operation is available through read_operation.",
		Method:      "GET",
		Path:        "/api/v1/admin/ops/alert-events/:id",
		ReadOnly:    true,
		Response:    "json",
		BrowserPath: "/admin/ops",
		Input:       adminapi.Arguments("/api/v1/admin/ops/alert-events/:id", []string{}, nil),
	}),
	"ops.alert_events.list": adminapi.Define(adminapi.Operation{
		ID:     "ops.alert_events.list",
		Module: "ops",
		Title:  "List Alert Events",
		Description: "ListAlertEvents returns filtered alert events after checking monitoring, using limit twenty " +
			"when omitted and requiring a positive supplied limit. PostgreSQL subsequently caps the " +
			"requested limit at five hundred.  The cursor requires before_fired_at and positive before_id " +
			"together, and its timestamp accepts the two RFC3339 layouts. email_sent accepts case-folded " +
			"true/1 or false/0. Status and severity are trimmed without local enum validation. Optional " +
			"time parameters use the shared twenty-four-hour parser, but absent parameters leave both " +
			"time fields unset; platform and a positive optional group are independent filters. This " +
			"operation is available through read_operation.",
		Method:      "GET",
		Path:        "/api/v1/admin/ops/alert-events",
		ReadOnly:    true,
		Response:    "json",
		BrowserPath: "/admin/ops",
		Input:       adminapi.Arguments("/api/v1/admin/ops/alert-events", []string{"before_fired_at", "before_id", "email_sent", "end_time", "group_id", "limit", "platform", "request_id", "severity", "start_time", "status", "time_range"}, nil),
	}),
	"ops.alert_events.status.update": adminapi.Define(adminapi.Operation{
		ID:     "ops.alert_events.status.update",
		Module: "ops",
		Title:  "Update Alert Event Status",
		Description: "UpdateAlertEventStatus accepts resolved or manual_resolved for a positive event ID after " +
			"checking monitoring. It trims but does not fold status case, assigns current UTC resolution " +
			"time, and returns service errors through the shared response helper. Omitted body fields " +
			"follow the HTTP handler defaults; explicit null and empty collections retain their " +
			"documented meanings.",
		Method:      "PUT",
		Path:        "/api/v1/admin/ops/alert-events/:id/status",
		Response:    "json",
		BrowserPath: "/admin/ops",
		Input:       adminapi.Arguments("/api/v1/admin/ops/alert-events/:id/status", []string{}, (*UpdateAlertEventStatusRequest)(nil)),
	}),
	"ops.alert_rules.create": adminapi.Define(adminapi.Operation{
		ID:     "ops.alert_rules.create",
		Module: "ops",
		Title:  "Create Alert Rule",
		Description: "bindOpsAlertRule decodes a complete rule with validated controls for creation or " +
			"replacement. It writes a bad-request response and returns nil when either binding or " +
			"validation fails. The raw-member binding runs first so omitted controls can select defaults " +
			"and member errors precede typed-field errors.  Gin caches the body for the second binding, " +
			"which retains description, filters, identity, and metadata from the request. Normalized " +
			"controls then replace the decoded values, including defaults for omitted members. Both " +
			"binding failures use the generic body message; validation uses its first member error text. " +
			"Omitted body fields follow the HTTP handler defaults; explicit null and empty collections " +
			"retain their documented meanings.",
		Method:      "POST",
		Path:        "/api/v1/admin/ops/alert-rules",
		Response:    "json",
		BrowserPath: "/admin/ops",
		Input:       alertRuleArguments("/api/v1/admin/ops/alert-rules"),
	}),
	"ops.alert_rules.delete": adminapi.Define(adminapi.Operation{
		ID:     "ops.alert_rules.delete",
		Module: "ops",
		Title:  "Delete Alert Rule",
		Description: "DeleteAlertRule deletes a positive decimal route identity after checking monitoring. Invalid " +
			"IDs return bad request; service failures use application-error mapping, and a successful " +
			"call returns deleted=true.",
		Method:      "DELETE",
		Path:        "/api/v1/admin/ops/alert-rules/:id",
		Response:    "json",
		BrowserPath: "/admin/ops",
		Input:       adminapi.Arguments("/api/v1/admin/ops/alert-rules/:id", []string{}, nil),
	}),
	"ops.alert_rules.list": adminapi.Define(adminapi.Operation{
		ID:     "ops.alert_rules.list",
		Module: "ops",
		Title:  "List Alert Rules",
		Description: "ListAlertRules returns the service's saved rule list after checking monitoring. A missing " +
			"service is unavailable; the service can return an allocated empty list when its repository " +
			"is absent. This operation is available through read_operation.",
		Method:      "GET",
		Path:        "/api/v1/admin/ops/alert-rules",
		ReadOnly:    true,
		Response:    "json",
		BrowserPath: "/admin/ops",
		Input:       adminapi.Arguments("/api/v1/admin/ops/alert-rules", []string{}, nil),
	}),
	"ops.alert_rules.update": adminapi.Define(adminapi.Operation{
		ID:     "ops.alert_rules.update",
		Module: "ops",
		Title:  "Update Alert Rule",
		Description: "bindOpsAlertRule decodes a complete rule with validated controls for creation or " +
			"replacement. It writes a bad-request response and returns nil when either binding or " +
			"validation fails. The raw-member binding runs first so omitted controls can select defaults " +
			"and member errors precede typed-field errors.  Gin caches the body for the second binding, " +
			"which retains description, filters, identity, and metadata from the request. Normalized " +
			"controls then replace the decoded values, including defaults for omitted members. Both " +
			"binding failures use the generic body message; validation uses its first member error text. " +
			"Omitted body fields follow the HTTP handler defaults; explicit null and empty collections " +
			"retain their documented meanings.",
		Method:      "PUT",
		Path:        "/api/v1/admin/ops/alert-rules/:id",
		Response:    "json",
		BrowserPath: "/admin/ops",
		Input:       alertRuleArguments("/api/v1/admin/ops/alert-rules/:id"),
	}),
	"ops.alert_silences.create": adminapi.Define(adminapi.Operation{
		ID:     "ops.alert_silences.create",
		Module: "ops",
		Title:  "Create Alert Silence",
		Description: "CreateAlertSilence parses a trimmed RFC3339 deadline and forwards rule, platform, group, " +
			"region, and reason after checking monitoring. A context subject supplies CreatedBy without a " +
			"positivity check here; service validation requires a positive rule and nonblank platform but " +
			"no future-deadline check. Omitted body fields follow the HTTP handler defaults; explicit " +
			"null and empty collections retain their documented meanings.",
		Method:      "POST",
		Path:        "/api/v1/admin/ops/alert-silences",
		Response:    "json",
		BrowserPath: "/admin/ops",
		Input:       adminapi.Arguments("/api/v1/admin/ops/alert-silences", []string{}, (*CreateAlertSilenceRequest)(nil)),
	}),
	"ops.auth_cache_invalidation.health.list": adminapi.Define(adminapi.Operation{
		ID:     "ops.auth_cache_invalidation.health.list",
		Module: "ops",
		Title:  "Get Auth Cache Invalidation Health",
		Description: "GetAuthCacheInvalidationHealth returns the combined authentication-cache diagnostics after " +
			"checking monitoring is enabled. A missing Ops service returns service unavailable; the " +
			"request context governs the persisted outbox health read. This operation is available " +
			"through read_operation.",
		Method:      "GET",
		Path:        "/api/v1/admin/ops/auth-cache-invalidation/health",
		ReadOnly:    true,
		Response:    "json",
		BrowserPath: "/admin/ops",
		Input:       adminapi.Arguments("/api/v1/admin/ops/auth-cache-invalidation/health", []string{}, nil),
	}),
	"ops.concurrency.list": adminapi.Define(adminapi.Operation{
		ID:     "ops.concurrency.list",
		Module: "ops",
		Title:  "Get Concurrency Stats",
		Description: "GetConcurrencyStats returns sampled platform, group, and provider concurrency after the " +
			"overall monitoring check. Disabled realtime returns empty maps before filter parsing; " +
			"enabled requests trim platform, require positive group IDs, and include a collected " +
			"timestamp when available. This operation is available through read_operation.",
		Method:      "GET",
		Path:        "/api/v1/admin/ops/concurrency",
		ReadOnly:    true,
		Response:    "json",
		BrowserPath: "/admin/ops",
		Input:       adminapi.Arguments("/api/v1/admin/ops/concurrency", []string{"group_id", "platform"}, nil),
	}),
	"ops.dashboard.error_distribution.list": adminapi.Define(adminapi.Operation{
		ID:     "ops.dashboard.error_distribution.list",
		Module: "ops",
		Title:  "Get Dashboard Error Distribution",
		Description: "GetDashboardErrorDistribution returns the service's error classification and status " +
			"distributions for the selected window. It checks monitoring, defaults to one hour, and " +
			"rejects nonpositive group filters before the repository query. This operation is available " +
			"through read_operation.",
		Method:      "GET",
		Path:        "/api/v1/admin/ops/dashboard/error-distribution",
		ReadOnly:    true,
		Response:    "json",
		BrowserPath: "/admin/ops",
		Input:       adminapi.Arguments("/api/v1/admin/ops/dashboard/error-distribution", []string{"end_time", "group_id", "platform", "request_id", "start_time", "time_range"}, nil),
	}),
	"ops.dashboard.error_trend.list": adminapi.Define(adminapi.Operation{
		ID:     "ops.dashboard.error_trend.list",
		Module: "ops",
		Title:  "Get Dashboard Error Trend",
		Description: "GetDashboardErrorTrend returns windowed final-client, SLA, business-limit, and upstream " +
			"error series. It uses the same span-based bucket selection as throughput and checks " +
			"monitoring and positive optional group IDs before requesting the trend. This operation is " +
			"available through read_operation.",
		Method:      "GET",
		Path:        "/api/v1/admin/ops/dashboard/error-trend",
		ReadOnly:    true,
		Response:    "json",
		BrowserPath: "/admin/ops",
		Input:       adminapi.Arguments("/api/v1/admin/ops/dashboard/error-trend", []string{"end_time", "group_id", "platform", "request_id", "start_time", "time_range"}, nil),
	}),
	"ops.dashboard.latency_histogram.list": adminapi.Define(adminapi.Operation{
		ID:     "ops.dashboard.latency_histogram.list",
		Module: "ops",
		Title:  "Get Dashboard Latency Histogram",
		Description: "GetDashboardLatencyHistogram returns duration counts from saved usage rows using validated " +
			"millisecond cutoffs. Monitoring and time/group validation precede cutoff parsing; an omitted " +
			"cutoff list uses the shared defaults, and invalid values return bad request. This operation " +
			"is available through read_operation.",
		Method:      "GET",
		Path:        "/api/v1/admin/ops/dashboard/latency-histogram",
		ReadOnly:    true,
		Response:    "json",
		BrowserPath: "/admin/ops",
		Input:       adminapi.Arguments("/api/v1/admin/ops/dashboard/latency-histogram", []string{"bucket_boundaries_ms", "end_time", "group_id", "platform", "request_id", "start_time", "time_range"}, nil),
	}),
	"ops.dashboard.openai_token_stats.list": adminapi.Define(adminapi.Operation{
		ID:     "ops.dashboard.openai_token_stats.list",
		Module: "ops",
		Title:  "Get Dashboard Token Stats",
		Description: "GetDashboardTokenStats returns model-grouped usage statistics with either a leading-model " +
			"limit or pagination. It checks monitoring before parsing the preset range and ranking " +
			"parameters, and maps parsing errors to bad request. This operation is available through " +
			"read_operation.",
		Method:      "GET",
		Path:        "/api/v1/admin/ops/dashboard/openai-token-stats",
		ReadOnly:    true,
		Response:    "json",
		BrowserPath: "/admin/ops",
		Input:       adminapi.Arguments("/api/v1/admin/ops/dashboard/openai-token-stats", []string{"group_id", "page", "page_size", "platform", "time_range", "top_n"}, nil),
	}),
	"ops.dashboard.overview.list": adminapi.Define(adminapi.Operation{
		ID:     "ops.dashboard.overview.list",
		Module: "ops",
		Title:  "Get Dashboard Overview",
		Description: "GetDashboardOverview returns window counts, rates, and available system observations after " +
			"the monitoring check. It defaults the time range to one hour, trims platform, and requires a " +
			"positive supplied group ID before querying the service. This operation is available through " +
			"read_operation.",
		Method:      "GET",
		Path:        "/api/v1/admin/ops/dashboard/overview",
		ReadOnly:    true,
		Response:    "json",
		BrowserPath: "/admin/ops",
		Input:       adminapi.Arguments("/api/v1/admin/ops/dashboard/overview", []string{"end_time", "group_id", "platform", "request_id", "start_time", "time_range"}, nil),
	}),
	"ops.dashboard.snapshot_v2.list": adminapi.Define(adminapi.Operation{
		ID:     "ops.dashboard.snapshot_v2.list",
		Module: "ops",
		Title:  "Get Dashboard Snapshot V2",
		Description: "GetDashboardSnapshotV2 returns the combined dashboard snapshot or a bodyless conditional " +
			"response. It validates monitoring, a time range defaulting to one hour, and a positive " +
			"optional group ID before querying the service.  The ETag hashes the returned payload's JSON " +
			"representation. If hashing succeeds, a cache hit with a matching If-None-Match condition " +
			"returns 304 before the cache diagnostic header is set. A miss bypasses conditional handling, " +
			"as does an absent tag. Full-response handling reports the service's cache-hit flag through " +
			"X-Snapshot-Cache before passing the payload to the JSON writer. This operation is available " +
			"through read_operation.",
		Method:      "GET",
		Path:        "/api/v1/admin/ops/dashboard/snapshot-v2",
		ReadOnly:    true,
		Response:    "json",
		BrowserPath: "/admin/ops",
		Input:       adminapi.Arguments("/api/v1/admin/ops/dashboard/snapshot-v2", []string{"end_time", "group_id", "platform", "request_id", "start_time", "time_range"}, nil),
	}),
	"ops.dashboard.throughput_trend.list": adminapi.Define(adminapi.Operation{
		ID:     "ops.dashboard.throughput_trend.list",
		Module: "ops",
		Title:  "Get Dashboard Throughput Trend",
		Description: "GetDashboardThroughputTrend returns request and token rates in buckets selected from the " +
			"requested window length. It checks monitoring first, uses a one-hour default, and forwards " +
			"trimmed platform and positive optional group filters. This operation is available through " +
			"read_operation.",
		Method:      "GET",
		Path:        "/api/v1/admin/ops/dashboard/throughput-trend",
		ReadOnly:    true,
		Response:    "json",
		BrowserPath: "/admin/ops",
		Input:       adminapi.Arguments("/api/v1/admin/ops/dashboard/throughput-trend", []string{"end_time", "group_id", "platform", "request_id", "start_time", "time_range"}, nil),
	}),
	"ops.dashboard.token_stats.list": adminapi.Define(adminapi.Operation{
		ID:     "ops.dashboard.token_stats.list",
		Module: "ops",
		Title:  "Get Dashboard Token Stats",
		Description: "GetDashboardTokenStats returns model-grouped usage statistics with either a leading-model " +
			"limit or pagination. It checks monitoring before parsing the preset range and ranking " +
			"parameters, and maps parsing errors to bad request. This operation is available through " +
			"read_operation.",
		Method:      "GET",
		Path:        "/api/v1/admin/ops/dashboard/token-stats",
		ReadOnly:    true,
		Response:    "json",
		BrowserPath: "/admin/ops",
		Input:       adminapi.Arguments("/api/v1/admin/ops/dashboard/token-stats", []string{"group_id", "page", "page_size", "platform", "time_range", "top_n"}, nil),
	}),
	"ops.email_notification.config.list": adminapi.Define(adminapi.Operation{
		ID:     "ops.email_notification.config.list",
		Module: "ops",
		Title:  "Get Email Notification Config",
		Description: "GetEmailNotificationConfig returns the service's saved or default email preferences after " +
			"checking monitoring. A missing service returns unavailable, and a configuration-read error " +
			"becomes a generic internal-server response rather than exposing that error text. This " +
			"operation is available through read_operation.",
		Method:      "GET",
		Path:        "/api/v1/admin/ops/email-notification/config",
		ReadOnly:    true,
		Response:    "json",
		BrowserPath: "/admin/ops",
		Input:       adminapi.Arguments("/api/v1/admin/ops/email-notification/config", []string{}, nil),
	}),
	"ops.email_notification.config.update": adminapi.Define(adminapi.Operation{
		ID:     "ops.email_notification.config.update",
		Module: "ops",
		Title:  "Update Email Notification Config",
		Description: "UpdateEmailNotificationConfig strictly decodes email settings after checking monitoring, " +
			"rejecting unknown struct members and trailing JSON. The service merges supplied alert/report " +
			"sections and validates them; any service error is returned as bad request, including storage " +
			"errors. Omitted body fields follow the HTTP handler defaults; explicit null and empty " +
			"collections retain their documented meanings.",
		Method:      "PUT",
		Path:        "/api/v1/admin/ops/email-notification/config",
		Response:    "json",
		BrowserPath: "/admin/ops",
		Input:       adminapi.Arguments("/api/v1/admin/ops/email-notification/config", []string{}, (*ops.OpsEmailNotificationConfigUpdateRequest)(nil)),
	}),
	"ops.errors.get": adminapi.Define(adminapi.Operation{
		ID:     "ops.errors.get",
		Module: "ops",
		Title:  "Get Error Log By ID",
		Description: "GetErrorLogByID returns the shared stored Ops detail for a positive decimal row ID after " +
			"service and monitoring checks. Call through the protected admin router with a usable Gin " +
			"request; the getter does not restrict the record to a particular error phase. This operation " +
			"is available through read_operation.",
		Method:      "GET",
		Path:        "/api/v1/admin/ops/errors/:id",
		ReadOnly:    true,
		Response:    "json",
		BrowserPath: "/admin/ops",
		Input:       adminapi.Arguments("/api/v1/admin/ops/errors/:id", []string{}, nil),
	}),
	"ops.errors.list": adminapi.Define(adminapi.Operation{
		ID:     "ops.errors.list",
		Module: "ops",
		Title:  "Get Error Logs",
		Description: "GetErrorLogs returns a paginated legacy error list after service and monitoring checks, " +
			"parsing a one-hour default window and capping the page at five hundred. Positive identity " +
			"filters, resolved spellings, and nonnegative comma-separated statuses are validated before " +
			"querying. Category alternatives intersect the supplied phase and other filters, and model " +
			"selection stays exact. This operation is available through read_operation.",
		Method:      "GET",
		Path:        "/api/v1/admin/ops/errors",
		ReadOnly:    true,
		Response:    "json",
		BrowserPath: "/admin/ops",
		Input:       adminapi.Arguments("/api/v1/admin/ops/errors", []string{"api_key_id", "category", "end_time", "error_owner", "error_source", "group_id", "model", "page", "page_size", "phase", "platform", "provider_id", "q", "request_id", "resolved", "sort_by", "sort_order", "start_time", "status_codes", "time_range", "user_id", "user_query", "view"}, nil),
	}),
	"ops.errors.resolve.update": adminapi.Define(adminapi.Operation{
		ID:     "ops.errors.resolve.update",
		Module: "ops",
		Title:  "Update Error Resolution",
		Description: "UpdateErrorResolution sets the submitted state for a positive Ops row ID after " +
			"service/monitoring checks and a positive authenticated user subject. It binds the JSON flag " +
			"and passes that operator to the shared core. Call from the protected admin group; " +
			"phase-specific aliases do not add record-phase restrictions. Omitted body fields follow the " +
			"HTTP handler defaults; explicit null and empty collections retain their documented meanings.",
		Method:      "PUT",
		Path:        "/api/v1/admin/ops/errors/:id/resolve",
		Response:    "json",
		BrowserPath: "/admin/ops",
		Input:       adminapi.Arguments("/api/v1/admin/ops/errors/:id/resolve", []string{}, (*opsResolveRequest)(nil)),
	}),
	"ops.ingress_rejections.health.list": adminapi.Define(adminapi.Operation{
		ID:     "ops.ingress_rejections.health.list",
		Module: "ops",
		Title:  "Get Ingress Reject Health",
		Description: "GetIngressRejectHealth returns aggregator capacity, backlog, and counters after checking " +
			"monitoring is enabled. A missing Ops service returns service unavailable; an unbound " +
			"aggregator returns the service's capacity-only health value. This operation is available " +
			"through read_operation.",
		Method:      "GET",
		Path:        "/api/v1/admin/ops/ingress-rejections/health",
		ReadOnly:    true,
		Response:    "json",
		BrowserPath: "/admin/ops",
		Input:       adminapi.Arguments("/api/v1/admin/ops/ingress-rejections/health", []string{}, nil),
	}),
	"ops.ingress_rejections.list": adminapi.Define(adminapi.Operation{
		ID:     "ops.ingress_rejections.list",
		Module: "ops",
		Title:  "List Ingress Rejects",
		Description: "ListIngressRejects validates aggregate filters and returns their paginated repository " +
			"result. It reports an unavailable Ops service first, defaults the time range to one hour " +
			"through parseOpsTimeRange, and limits page size to two hundred.  Enum values are trimmed and " +
			"matched case-sensitively. Client IP is parsed, unmapped, and reduced to /64 for IPv6 to " +
			"match middleware aggregation; user and API-key filters accept positive decimal IDs. The " +
			"service performs the monitoring check after parameter validation, and repository failures " +
			"use the shared error response. This operation is available through read_operation.",
		Method:      "GET",
		Path:        "/api/v1/admin/ops/ingress-rejections",
		ReadOnly:    true,
		Response:    "json",
		BrowserPath: "/admin/ops",
		Input:       adminapi.Arguments("/api/v1/admin/ops/ingress-rejections", []string{"client_ip", "end_time", "page", "page_size", "request_id", "start_time", "time_range"}, nil),
	}),
	"ops.provider_availability.list": adminapi.Define(adminapi.Operation{
		ID:     "ops.provider_availability.list",
		Module: "ops",
		Title:  "Get Provider Availability",
		Description: "GetProviderAvailability returns sampled availability grouped by platform, group, and " +
			"provider. Disabled realtime succeeds with empty maps before filter validation, while enabled " +
			"requests pass trimmed platform and positive optional group IDs to the service. This " +
			"operation is available through read_operation.",
		Method:      "GET",
		Path:        "/api/v1/admin/ops/provider-availability",
		ReadOnly:    true,
		Response:    "json",
		BrowserPath: "/admin/ops",
		Input:       adminapi.Arguments("/api/v1/admin/ops/provider-availability", []string{"group_id", "platform"}, nil),
	}),
	"ops.realtime_traffic.list": adminapi.Define(adminapi.Operation{
		ID:     "ops.realtime_traffic.list",
		Module: "ops",
		Title:  "Get Realtime Traffic Summary",
		Description: "GetRealtimeTrafficSummary returns current, peak, and average QPS/TPS for a preset window " +
			"ending at one sampled UTC time. It validates the window and positive optional group before " +
			"checking realtime; disabled results retain that range with zero rates, while enabled queries " +
			"force raw mode. This operation is available through read_operation.",
		Method:      "GET",
		Path:        "/api/v1/admin/ops/realtime-traffic",
		ReadOnly:    true,
		Response:    "json",
		BrowserPath: "/admin/ops",
		Input:       adminapi.Arguments("/api/v1/admin/ops/realtime-traffic", []string{"group_id", "platform", "window"}, nil),
	}),
	"ops.request_errors.get": adminapi.Define(adminapi.Operation{
		ID:     "ops.request_errors.get",
		Module: "ops",
		Title:  "Get Request Error",
		Description: "GetRequestError delegates the request-error detail URL to GetErrorLogByID using its numeric " +
			"row ID and request context. Service, monitoring, and ID errors follow that handler; the " +
			"alias adds no request-phase validation. This operation is available through read_operation.",
		Method:      "GET",
		Path:        "/api/v1/admin/ops/request-errors/:id",
		ReadOnly:    true,
		Response:    "json",
		BrowserPath: "/admin/ops",
		Input:       adminapi.Arguments("/api/v1/admin/ops/request-errors/:id", []string{}, nil),
	}),
	"ops.request_errors.list": adminapi.Define(adminapi.Operation{
		ID:     "ops.request_errors.list",
		Module: "ops",
		Title:  "List Request Errors",
		Description: "ListRequestErrors queries the same error store with a one-hour default window, SLA view, " +
			"category, status, group, and provider conditions. It checks service/monitoring and caps five " +
			"hundred rows. user_id and api_key_id are not read here, while user_query remains an " +
			"email-search input and result pagination comes from the core. This operation is available " +
			"through read_operation.",
		Method:      "GET",
		Path:        "/api/v1/admin/ops/request-errors",
		ReadOnly:    true,
		Response:    "json",
		BrowserPath: "/admin/ops",
		Input:       adminapi.Arguments("/api/v1/admin/ops/request-errors", []string{"api_key_id", "category", "end_time", "error_owner", "error_source", "group_id", "model", "page", "page_size", "phase", "platform", "provider_id", "q", "request_id", "resolved", "sort_by", "sort_order", "start_time", "status_codes", "time_range", "user_id", "user_query", "view"}, nil),
	}),
	"ops.request_errors.resolve.update": adminapi.Define(adminapi.Operation{
		ID:     "ops.request_errors.resolve.update",
		Module: "ops",
		Title:  "Resolve Request Error",
		Description: "UpdateErrorResolution sets the submitted state for a positive Ops row ID after " +
			"service/monitoring checks and a positive authenticated user subject. It binds the JSON flag " +
			"and passes that operator to the shared core. Call from the protected admin group; " +
			"phase-specific aliases do not add record-phase restrictions. Omitted body fields follow the " +
			"HTTP handler defaults; explicit null and empty collections retain their documented meanings.",
		Method:      "PUT",
		Path:        "/api/v1/admin/ops/request-errors/:id/resolve",
		Response:    "json",
		BrowserPath: "/admin/ops",
		Input:       adminapi.Arguments("/api/v1/admin/ops/request-errors/:id/resolve", []string{}, (*opsResolveRequest)(nil)),
	}),
	"ops.request_errors.upstream_errors.list": adminapi.Define(adminapi.Operation{
		ID:     "ops.request_errors.upstream_errors.list",
		Module: "ops",
		Title:  "List Request Error Upstream Errors",
		Description: "ListRequestErrorUpstreamErrors lists provider-owned upstream/provider_auth observations " +
			"using correlation keys from a stored Ops row. Service/monitoring and a positive row ID are " +
			"checked first. An origin with no saved request or client ID returns an empty page with fixed " +
			"page one/size ten before additional query parsing.  The saved request ID overrides query " +
			"request_id when present; otherwise a saved client ID becomes an equality condition while any " +
			"query request_id remains. Storage decides lookup aliases and intersects those conditions. " +
			"The default window is thirty days unless explicit/ID-query range selection changes it, and " +
			"recovered provider rows are requested with the all view.  Trimmed include_detail values 1, " +
			"true, or yes trigger per-row detail reads; true and yes ignore case. Nil list items, failed " +
			"detail reads, and nil detail results are skipped, while the original total/page metadata is " +
			"retained. The resulting expanded page can therefore contain fewer items than its reported " +
			"total describes. This operation is available through read_operation.",
		Method:      "GET",
		Path:        "/api/v1/admin/ops/request-errors/:id/upstream-errors",
		ReadOnly:    true,
		Response:    "json",
		BrowserPath: "/admin/ops",
		Input:       adminapi.Arguments("/api/v1/admin/ops/request-errors/:id/upstream-errors", []string{"api_key_id", "category", "end_time", "error_owner", "error_source", "group_id", "include_detail", "model", "page", "page_size", "phase", "platform", "provider_id", "q", "request_id", "resolved", "sort_by", "sort_order", "start_time", "status_codes", "time_range", "user_id", "user_query", "view"}, nil),
	}),
	"ops.requests.list": adminapi.Define(adminapi.Operation{
		ID:     "ops.requests.list",
		Module: "ops",
		Title:  "List Request Details",
		Description: "ListRequestDetails returns combined success/error observations under a one-hour default " +
			"range and a hundred-row HTTP page cap. It validates positive IDs and nonnegative duration " +
			"values. Storage validates kind/sort, compares a lowercased platform without a whitelist, and " +
			"combines independent duration bounds; reversed min/max values can yield an empty result. " +
			"SLAOnly recognizes one or case-insensitive true, with other nonblank text selecting false. " +
			"Absent storage can supply a fixed successful empty page from the core. Other query errors " +
			"containing invalid in lowercase text receive HTTP 400, and remaining causes receive a " +
			"generic HTTP 500. Successful response pagination uses core metadata, including its " +
			"absent-storage defaults. This operation is available through read_operation.",
		Method:      "GET",
		Path:        "/api/v1/admin/ops/requests",
		ReadOnly:    true,
		Response:    "json",
		BrowserPath: "/admin/ops",
		Input:       adminapi.Arguments("/api/v1/admin/ops/requests", []string{"api_key_id", "end_time", "group_id", "kind", "max_duration_ms", "min_duration_ms", "model", "page", "page_size", "platform", "provider_id", "q", "request_id", "sla_only", "sort", "start_time", "time_range", "user_id"}, nil),
	}),
	"ops.runtime.alert.list": adminapi.Define(adminapi.Operation{
		ID:     "ops.runtime.alert.list",
		Module: "ops",
		Title:  "Get Alert Runtime Settings",
		Description: "GetAlertRuntimeSettings returns the service's alert interval, leadership, and silence " +
			"configuration after the monitoring check. A missing service is unavailable, and read failure " +
			"produces the endpoint's generic internal-server message. This operation is available through " +
			"read_operation.",
		Method:      "GET",
		Path:        "/api/v1/admin/ops/runtime/alert",
		ReadOnly:    true,
		Response:    "json",
		BrowserPath: "/admin/ops",
		Input:       adminapi.Arguments("/api/v1/admin/ops/runtime/alert", []string{}, nil),
	}),
	"ops.runtime.alert.update": adminapi.Define(adminapi.Operation{
		ID:     "ops.runtime.alert.update",
		Module: "ops",
		Title:  "Update Alert Runtime Settings",
		Description: "UpdateAlertRuntimeSettings binds the supplied evaluator configuration and delegates " +
			"normalization, validation, and storage after checking monitoring. Invalid JSON and any " +
			"service failure become bad request; this body uses ordinary Gin binding. Omitted body fields " +
			"follow the HTTP handler defaults; explicit null and empty collections retain their " +
			"documented meanings.",
		Method:      "PUT",
		Path:        "/api/v1/admin/ops/runtime/alert",
		Response:    "json",
		BrowserPath: "/admin/ops",
		Input:       adminapi.Arguments("/api/v1/admin/ops/runtime/alert", []string{}, (*ops.OpsAlertRuntimeSettings)(nil)),
	}),
	"ops.runtime.logging.list": adminapi.Define(adminapi.Operation{
		ID:     "ops.runtime.logging.list",
		Module: "ops",
		Title:  "Get Runtime Log Config",
		Description: "GetRuntimeLogConfig returns the service's effective logging configuration after checking " +
			"monitoring. The service handles saved overrides and baseline values; read errors become a " +
			"generic internal-server response at this endpoint. This operation is available through " +
			"read_operation.",
		Method:      "GET",
		Path:        "/api/v1/admin/ops/runtime/logging",
		ReadOnly:    true,
		Response:    "json",
		BrowserPath: "/admin/ops",
		Input:       adminapi.Arguments("/api/v1/admin/ops/runtime/logging", []string{}, nil),
	}),
	"ops.runtime.logging.reset.create": adminapi.Define(adminapi.Operation{
		ID:     "ops.runtime.logging.reset.create",
		Module: "ops",
		Title:  "Reset Runtime Log Config",
		Description: "ResetRuntimeLogConfig requires monitoring and a positive authenticated subject before asking " +
			"the service to remove its runtime override. The subject is passed as operator metadata, and " +
			"every service failure is returned as bad request.",
		Method:      "POST",
		Path:        "/api/v1/admin/ops/runtime/logging/reset",
		Response:    "json",
		BrowserPath: "/admin/ops",
		Input:       adminapi.Arguments("/api/v1/admin/ops/runtime/logging/reset", []string{}, nil),
	}),
	"ops.runtime.logging.update": adminapi.Define(adminapi.Operation{
		ID:     "ops.runtime.logging.update",
		Module: "ops",
		Title:  "Update Runtime Log Config",
		Description: "UpdateRuntimeLogConfig binds a full logging configuration, then requires a positive " +
			"authenticated subject after the monitoring check. It passes that subject ID to the service's " +
			"live apply and persistence path; all service errors become bad request. Omitted body fields " +
			"follow the HTTP handler defaults; explicit null and empty collections retain their " +
			"documented meanings.",
		Method:      "PUT",
		Path:        "/api/v1/admin/ops/runtime/logging",
		Response:    "json",
		BrowserPath: "/admin/ops",
		Input:       adminapi.Arguments("/api/v1/admin/ops/runtime/logging", []string{}, (*ops.OpsRuntimeLogConfig)(nil)),
	}),
	"ops.settings.metric_thresholds.list": adminapi.Define(adminapi.Operation{
		ID:     "ops.settings.metric_thresholds.list",
		Module: "ops",
		Title:  "Get Metric Thresholds",
		Description: "GetMetricThresholds returns saved or default SLA, first-token latency, request-error, and " +
			"upstream-error cutoffs after checking monitoring. A settings-read failure becomes this " +
			"endpoint's generic internal-server response. This operation is available through " +
			"read_operation.",
		Method:      "GET",
		Path:        "/api/v1/admin/ops/settings/metric-thresholds",
		ReadOnly:    true,
		Response:    "json",
		BrowserPath: "/admin/ops",
		Input:       adminapi.Arguments("/api/v1/admin/ops/settings/metric-thresholds", []string{}, nil),
	}),
	"ops.settings.metric_thresholds.update": adminapi.Define(adminapi.Operation{
		ID:     "ops.settings.metric_thresholds.update",
		Module: "ops",
		Title:  "Update Metric Thresholds",
		Description: "UpdateMetricThresholds binds SLA, first-token latency, request-error, and upstream-error " +
			"cutoffs and delegates their validation and storage after checking monitoring. Invalid JSON " +
			"or any service failure returns bad request. Omitted body fields follow the HTTP handler " +
			"defaults; explicit null and empty collections retain their documented meanings.",
		Method:      "PUT",
		Path:        "/api/v1/admin/ops/settings/metric-thresholds",
		Response:    "json",
		BrowserPath: "/admin/ops",
		Input:       adminapi.Arguments("/api/v1/admin/ops/settings/metric-thresholds", []string{}, (*ops.OpsMetricThresholds)(nil)),
	}),
	"ops.system_logs.cleanup.create": adminapi.Define(adminapi.Operation{
		ID:     "ops.system_logs.cleanup.create",
		Module: "ops",
		Title:  "Cleanup System Logs",
		Description: "CleanupSystemLogs binds deletion filters after monitoring and positive context-subject " +
			"checks, then returns the affected-row count. Blank time strings omit bounds, invalid RFC3339 " +
			"text or nonpositive Key IDs fail locally, and service/storage own effective filtering and " +
			"separate audit attempts. Omitted body fields follow the HTTP handler defaults; explicit null " +
			"and empty collections retain their documented meanings.",
		Method:      "POST",
		Path:        "/api/v1/admin/ops/system-logs/cleanup",
		Response:    "json",
		BrowserPath: "/admin/ops",
		Input:       adminapi.Arguments("/api/v1/admin/ops/system-logs/cleanup", []string{}, (*opsSystemLogCleanupRequest)(nil)),
	}),
	"ops.system_logs.health.list": adminapi.Define(adminapi.Operation{
		ID:     "ops.system_logs.health.list",
		Module: "ops",
		Title:  "Get System Log Ingestion Health",
		Description: "GetSystemLogIngestionHealth returns the bound sink's health after checking monitoring. " +
			"Missing Ops service state is unavailable; an unbound sink returns zero-valued health through " +
			"the service accessor. This operation is available through read_operation.",
		Method:      "GET",
		Path:        "/api/v1/admin/ops/system-logs/health",
		ReadOnly:    true,
		Response:    "json",
		BrowserPath: "/admin/ops",
		Input:       adminapi.Arguments("/api/v1/admin/ops/system-logs/health", []string{}, nil),
	}),
	"ops.system_logs.list": adminapi.Define(adminapi.Operation{
		ID:     "ops.system_logs.list",
		Module: "ops",
		Title:  "List System Logs",
		Description: "ListSystemLogs returns a paginated indexed-log response after monitoring checks. It parses a " +
			"one-hour default range, caps size at two hundred, trims text filters, and requires positive " +
			"supplied user, Key, and provider IDs. This operation is available through read_operation.",
		Method:      "GET",
		Path:        "/api/v1/admin/ops/system-logs",
		ReadOnly:    true,
		Response:    "json",
		BrowserPath: "/admin/ops",
		Input:       adminapi.Arguments("/api/v1/admin/ops/system-logs", []string{"api_key_id", "client_request_id", "component", "end_time", "host", "level", "model", "page", "page_size", "platform", "provider_id", "q", "request_id", "start_time", "time_range", "user_id"}, nil),
	}),
	"ops.upstream_errors.get": adminapi.Define(adminapi.Operation{
		ID:     "ops.upstream_errors.get",
		Module: "ops",
		Title:  "Get Upstream Error",
		Description: "GetUpstreamError delegates the upstream detail URL to the shared numeric-ID detail handler. " +
			"It inherits service, monitoring, and ID errors from GetErrorLogByID and adds no " +
			"upstream-phase validation to the stored record lookup. This operation is available through " +
			"read_operation.",
		Method:      "GET",
		Path:        "/api/v1/admin/ops/upstream-errors/:id",
		ReadOnly:    true,
		Response:    "json",
		BrowserPath: "/admin/ops",
		Input:       adminapi.Arguments("/api/v1/admin/ops/upstream-errors/:id", []string{}, nil),
	}),
	"ops.upstream_errors.list": adminapi.Define(adminapi.Operation{
		ID:     "ops.upstream_errors.list",
		Module: "ops",
		Title:  "List Upstream Errors",
		Description: "ListUpstreamErrors queries provider-owned upstream/provider_auth observations and opts into " +
			"recovered attempts after service/monitoring checks. It parses a one-hour default range, caps " +
			"five hundred rows, and validates group/provider, resolved, and status selectors. Its " +
			"selected SLA view remains an additional condition despite recovered-row inclusion. This " +
			"operation is available through read_operation.",
		Method:      "GET",
		Path:        "/api/v1/admin/ops/upstream-errors",
		ReadOnly:    true,
		Response:    "json",
		BrowserPath: "/admin/ops",
		Input:       adminapi.Arguments("/api/v1/admin/ops/upstream-errors", []string{"api_key_id", "category", "end_time", "error_owner", "error_source", "group_id", "model", "page", "page_size", "phase", "platform", "provider_id", "q", "request_id", "resolved", "sort_by", "sort_order", "start_time", "status_codes", "time_range", "user_id", "user_query", "view"}, nil),
	}),
	"ops.upstream_errors.resolve.update": adminapi.Define(adminapi.Operation{
		ID:     "ops.upstream_errors.resolve.update",
		Module: "ops",
		Title:  "Resolve Upstream Error",
		Description: "UpdateErrorResolution sets the submitted state for a positive Ops row ID after " +
			"service/monitoring checks and a positive authenticated user subject. It binds the JSON flag " +
			"and passes that operator to the shared core. Call from the protected admin group; " +
			"phase-specific aliases do not add record-phase restrictions. Omitted body fields follow the " +
			"HTTP handler defaults; explicit null and empty collections retain their documented meanings.",
		Method:      "PUT",
		Path:        "/api/v1/admin/ops/upstream-errors/:id/resolve",
		Response:    "json",
		BrowserPath: "/admin/ops",
		Input:       adminapi.Arguments("/api/v1/admin/ops/upstream-errors/:id/resolve", []string{}, (*opsResolveRequest)(nil)),
	}),
	"ops.user_concurrency.list": adminapi.Define(adminapi.Operation{
		ID:     "ops.user_concurrency.list",
		Module: "ops",
		Title:  "Get User Concurrency Stats",
		Description: "GetUserConcurrencyStats returns sampled users with running or waiting requests after the " +
			"monitoring check. Disabled realtime reports an empty map and current timestamp; enabled " +
			"results include the service's collected timestamp when available. This operation is " +
			"available through read_operation.",
		Method:      "GET",
		Path:        "/api/v1/admin/ops/user-concurrency",
		ReadOnly:    true,
		Response:    "json",
		BrowserPath: "/admin/ops",
		Input:       adminapi.Arguments("/api/v1/admin/ops/user-concurrency", []string{}, nil),
	}),
	"ops.ws.qps.list": adminapi.Define(adminapi.Operation{
		ID:      "ops.ws.qps.list",
		Module:  "ops",
		Title:   "Query realtime traffic",
		TitleZH: "查询实时流量",
		Description: "QPSWSHandler admits an enabled realtime socket and serves the shared cached traffic payload. " +
			"It requires a usable Gin request before checking a nil handler or service. Route middleware " +
			"supplies administrator authentication.  Disabled realtime attempts an upgrade followed by " +
			"application close 4001. Handshake rejection can already write an HTTP error before that failure " +
			"branch checks whether to write its JSON 404 response. Enabled requests prepare the runtime before reserving " +
			"counts, so a refused admission can still start or keep its sampler active.  Positive global " +
			"and known-address limits are reserved before upgrade and released by deferred handler " +
			"cleanup after socket handling returns. Nonpositive global limits bypass reservation although " +
			"ReleaseTotal still runs. Authorized Server-Timing headers are copied onto the response " +
			"writer before completing the 101 handshake. This operation is available through read_operation.",
		Method:      "GET",
		Path:        "/api/v1/admin/ops/ws/qps",
		ReadOnly:    true,
		Response:    "json",
		BrowserPath: "/admin/ops",
		Alias:       "ops.realtime_traffic.list",
		HTTPUpgrade: "websocket",
		Input:       adminapi.Arguments("/api/v1/admin/ops/ws/qps", []string{"window", "platform", "group_id"}, nil),
	}),
	"system.check_updates.list": adminapi.Define(adminapi.Operation{
		ID:     "system.check_updates.list",
		Module: "system",
		Title:  "Check Updates",
		Description: "CheckUpdates returns update information or the query's error text as an internal-server " +
			"response. force is true only for the exact query spelling true; whitespace, different case, " +
			"and other truth-like text do not enable it. Browser confirmation by the token owner is " +
			"required.",
		Method:      "GET",
		Path:        "/api/v1/admin/system/check-updates",
		Sensitive:   true,
		Response:    "json",
		BrowserPath: "/admin/system",
		Input:       adminapi.Arguments("/api/v1/admin/system/check-updates", []string{"force"}, nil),
	}),
	"system.restart.create": adminapi.Define(adminapi.Operation{
		ID:     "system.restart.create",
		Module: "system",
		Title:  "Restart Service",
		Description: "RestartService builds a restart operation identity and submits its operation_id payload " +
			"through administrator idempotency. Operations.Restart owns lease acquisition and the " +
			"optional restart request; a successful response does not itself prove process exit. Browser " +
			"confirmation by the token owner is required.",
		Method:      "POST",
		Path:        "/api/v1/admin/system/restart",
		Sensitive:   true,
		Response:    "json",
		BrowserPath: "/admin/system",
		Idempotent:  true,
		Input:       adminapi.Arguments("/api/v1/admin/system/restart", []string{}, nil),
	}),
	"system.rollback.create": adminapi.Define(adminapi.Operation{
		ID:     "system.rollback.create",
		Module: "system",
		Title:  "Rollback",
		Description: "Rollback submits local-backup restoration or a selected target through administrator " +
			"idempotency and Operations. A nil body or zero ContentLength skips binding; unknown length " +
			"still binds, and EOF is accepted as an empty request.  Target text is trimmed. A nonempty " +
			"target contributes to the operation identity as well as the version payload, so different " +
			"targets produce different identity inputs before service validation. Invalid JSON other than " +
			"EOF returns bad request. The handler performs no semantic version check itself; downloaded " +
			"eligibility and the local installer outcome belong to the maintained update service. Omitted " +
			"body fields follow the HTTP handler defaults; explicit null and empty collections retain " +
			"their documented meanings. Browser confirmation by the token owner is required.",
		Method:      "POST",
		Path:        "/api/v1/admin/system/rollback",
		Sensitive:   true,
		Response:    "json",
		BrowserPath: "/admin/system",
		Idempotent:  true,
		Input:       adminapi.Arguments("/api/v1/admin/system/rollback", []string{}, (*SystemRollbackRequest)(nil)),
	}),
	"system.rollback_versions.list": adminapi.Define(adminapi.Operation{
		ID:     "system.rollback_versions.list",
		Module: "system",
		Title:  "Get Rollback Versions",
		Description: "GetRollbackVersions returns the update service's allowed candidate entries inside a versions " +
			"payload. Query failure becomes an internal-server response using its error text; a " +
			"successful nil slice is passed through as supplied. This operation is available through " +
			"read_operation.",
		Method:      "GET",
		Path:        "/api/v1/admin/system/rollback-versions",
		ReadOnly:    true,
		Response:    "json",
		BrowserPath: "/admin/system",
		Input:       adminapi.Arguments("/api/v1/admin/system/rollback-versions", []string{}, nil),
	}),
	"system.update.create": adminapi.Define(adminapi.Operation{
		ID:     "system.update.create",
		Module: "system",
		Title:  "Perform Update",
		Description: "PerformUpdate builds an update identity and submits its operation_id payload through the " +
			"administrator idempotency helper. The callback delegates to Operations.Update; accepted " +
			"lease and work-context handling belong there, and helper errors determine the HTTP response. " +
			"Browser confirmation by the token owner is required.",
		Method:      "POST",
		Path:        "/api/v1/admin/system/update",
		Sensitive:   true,
		Response:    "json",
		BrowserPath: "/admin/system",
		Idempotent:  true,
		Input:       adminapi.Arguments("/api/v1/admin/system/update", []string{}, nil),
	}),
	"system.version.list": adminapi.Define(adminapi.Operation{
		ID:     "system.version.list",
		Module: "system",
		Title:  "Get Version",
		Description: "GetVersion queries updateSvc with force false, discards its error, and returns " +
			"CurrentVersion in the success payload. Its dependency must supply a nonnil UpdateInfo even " +
			"for an error result; a nil pointer is dereferenced here. This operation is available through " +
			"read_operation.",
		Method:      "GET",
		Path:        "/api/v1/admin/system/version",
		ReadOnly:    true,
		Response:    "json",
		BrowserPath: "/admin/system",
		Input:       adminapi.Arguments("/api/v1/admin/system/version", []string{}, nil),
	}),
}

// SystemRollbackRequest carries the administrative request decoded by its existing HTTP handler.
type SystemRollbackRequest struct {
	// Version selects a downloaded rollback when trimmed nonempty. Omitted, null,
	// or blank input selects the retained local-backup path.
	Version string `json:"version"` // selected rollback target version
}

// CreateAlertSilenceRequest carries the administrative request decoded by its existing HTTP handler.
type CreateAlertSilenceRequest struct {
	RuleID   int64  `json:"rule_id"`  // target alert rule identity
	Platform string `json:"platform"` // requested silence platform scope
	// GroupID is passed directly to the saved scope; nil leaves that scope's
	// group identity absent rather than selecting every group.
	GroupID *int64 `json:"group_id"` // optional silence group identity
	// Region is passed through without HTTP trimming; PostgreSQL later normalizes
	// its optional string when storing the exact silence scope.
	Region *string `json:"region"` // optional silence region text
	Until  string  `json:"until"`  // RFC3339
	Reason string  `json:"reason"` // administrator silence reason text
}

// UpdateAlertEventStatusRequest carries the administrative request decoded by its existing HTTP handler.
type UpdateAlertEventStatusRequest struct {
	Status string `json:"status"` // requested resolution state
}

// alertRuleArguments supplements the persisted rule type with HTTP raw-member rules.
// Required member presence and explicit null differ from the typed Go defaults;
// the handler still validates ranges, supported metrics, and nonempty required strings.
func alertRuleArguments(route string) *jsonschema.Schema {
	input := adminapi.Arguments(route, nil, (*ops.OpsAlertRule)(nil))
	body := input.Properties["body"]
	body.Required = []string{"name", "metric_type", "operator", "threshold"}
	input.Required = append(input.Required, "body")
	for _, name := range []string{"enabled", "notify_email", "severity", "description", "cooldown_minutes", "threshold"} {
		body.Properties[name] = &jsonschema.Schema{AnyOf: []*jsonschema.Schema{body.Properties[name], {Type: "null"}}}
	}
	return input
}
