package httpapi

import (
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/gorilla/websocket"

	"github.com/TokenFlux/TokenRouter/internal/identity/httpapi/authctx"
	"github.com/TokenFlux/TokenRouter/internal/ops"
	response "github.com/TokenFlux/TokenRouter/internal/server/httpx"
)

// ops_handler.go serves request-error and upstream-error administration,
// including filtered lists, drilldown, and resolution.
// Administrator error URLs share stored Ops rows, so request and upstream
// detail/resolution aliases delegate to the same numeric-ID handlers. Lists add
// different identity, phase, recovered-provider, and SLA-view filters, while
// category alternatives intersect the other selected conditions. Exact model
// filtering retains stored requested-model precedence even when display logic
// falls back from an empty value.
//
// Provider drilldown prefers an origin saved request ID over its client ID and
// can expand selected rows through separate detail reads. Failed expansions are
// skipped while pagination totals remain from the list query. Time selection
// distinguishes explicit endpoints, duration presets, and epoch-based ID
// lookup; those branches have different missing-value and range-cap rules.
// Protected parent middleware supplies administrator access, and resolution
// commands separately require a positive authenticated operator.

// Ops list views select SLA-related SQL predicates while leaving independent
// time, identity, category, and qualified recovered-provider filters to their
// own conditions.
const (
	opsListViewErrors   = "errors"   // SLA-countable listing view
	opsListViewExcluded = "excluded" // business-limited listing view
	opsListViewAll      = "all"      // omit SLA view condition
)

// OpsHandler adapts administrator and realtime Ops requests to the shared core
// service.
type OpsHandler struct {
	wsProxy    OpsWSProxyConfig   // immutable handshake proxy policy
	wsLimits   opsWSRuntimeLimits // immutable connection admission limits
	wsUpgrader websocket.Upgrader // instance-owned websocket handshake adapter

	// opsService is the borrowed core retained at construction. App binds its
	// dependencies and lifecycle; endpoints forward request contexts and leave
	// shared resources with that owner.
	opsService *ops.OpsService
}

// opsResolveRequest supplies the desired resolution flag in administrator error
// commands.
type opsResolveRequest struct {
	// Resolved is the requested state, remaining false when a valid JSON object
	// omits the property. UpdateErrorResolution forwards that value with the
	// authenticated operator ID.
	Resolved bool `json:"resolved"`
}

// NewOpsHandler retains the supplied Ops core for administrator and realtime
// endpoint calls, creating no worker or storage resource. Nil core is retained
// for isolated HTTP fixtures; endpoint-specific service checks decide their
// unavailable responses.
func NewOpsHandler(opsService *ops.OpsService, options OpsWSOptions) *OpsHandler {
	handler := &OpsHandler{opsService: opsService}
	handler.configureWebSocket(options)
	return handler
}

// GetErrorLogByID returns the shared stored Ops detail for a positive decimal
// row ID after service and monitoring checks. Call through the protected admin
// router with a usable Gin request; the getter does not restrict the record to
// a particular error phase.
// GET /api/v1/admin/ops/errors/:id
func (h *OpsHandler) GetErrorLogByID(c *gin.Context) {
	if h.opsService == nil {
		response.Error(c, http.StatusServiceUnavailable, "Ops service not available")
		return
	}
	if err := h.opsService.RequireMonitoringEnabled(c.Request.Context()); err != nil {
		response.ErrorFrom(c, err)
		return
	}

	idStr := strings.TrimSpace(c.Param("id"))
	id, err := strconv.ParseInt(idStr, 10, 64)
	if err != nil || id <= 0 {
		response.BadRequest(c, "Invalid error id")
		return
	}

	detail, err := h.opsService.GetErrorLogByID(c.Request.Context(), id)
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}

	response.Success(c, detail)
}

// parseOpsViewParam returns a normalized list-selector string after trimming
// and lowercasing. Blank or unknown text selects errors, while excluded/all are
// retained. Nil context returns empty text for the downstream storage default.
func parseOpsViewParam(c *gin.Context) string {
	if c == nil {
		return ""
	}
	v := strings.ToLower(strings.TrimSpace(c.Query("view")))
	switch v {
	case "", opsListViewErrors:
		return opsListViewErrors
	case opsListViewExcluded:
		return opsListViewExcluded
	case opsListViewAll:
		return opsListViewAll
	default:
		return opsListViewErrors
	}
}

// applyOpsErrorSortParams forwards query sort text through
// OpsErrorLogFilter.SetSort, which trims fields while retaining case. Supply
// usable context/filter; PostgreSQL later folds case, defaults an unknown
// column to created_at, and selects ascending only for asc, with row ID
// breaking ties in the same direction.
func applyOpsErrorSortParams(c *gin.Context, filter *ops.OpsErrorLogFilter) {
	filter.SetSort(c.Query("sort_by"), c.Query("sort_order"))
}

// GetErrorLogs returns a paginated legacy error list after service and
// monitoring checks, parsing a one-hour default window and capping the page at
// five hundred. Positive identity filters, resolved spellings, and nonnegative
// comma-separated statuses are validated before querying. Category alternatives
// intersect the supplied phase and other filters, and model selection stays
// exact.
// GET /api/v1/admin/ops/errors
func (h *OpsHandler) GetErrorLogs(c *gin.Context) {
	if h.opsService == nil {
		response.Error(c, http.StatusServiceUnavailable, "Ops service not available")
		return
	}
	if err := h.opsService.RequireMonitoringEnabled(c.Request.Context()); err != nil {
		response.ErrorFrom(c, err)
		return
	}

	page, pageSize := response.ParsePagination(c)
	// Keep the error-list HTTP page within the storage five-hundred-row cap.
	if pageSize > 500 {
		pageSize = 500
	}

	startTime, endTime, err := parseOpsTimeRange(c, "1h")
	if err != nil {
		response.BadRequest(c, err.Error())
		return
	}

	filter := &ops.OpsErrorLogFilter{Page: page, PageSize: pageSize}

	if !startTime.IsZero() {
		filter.StartTime = &startTime
	}
	if !endTime.IsZero() {
		filter.EndTime = &endTime
	}
	filter.View = parseOpsViewParam(c)
	filter.Phase = strings.TrimSpace(c.Query("phase"))
	filter.Owner = strings.TrimSpace(c.Query("error_owner"))
	filter.Source = strings.TrimSpace(c.Query("error_source"))
	filter.RequestID = strings.TrimSpace(c.Query("request_id"))
	filter.Query = strings.TrimSpace(c.Query("q"))
	filter.UserQuery = strings.TrimSpace(c.Query("user_query"))
	// ModelFuzzy remains false for these administrator lists. SQL compares
	// requested_model before model even when requested_model is a nonnull empty
	// string, unlike display fallback handling.
	filter.Model = strings.TrimSpace(c.Query("model"))

	// Categories use the same phase/type alternatives as /usage/errors.
	// Unknown categories add no constraint; category and phase filters combine with AND.
	if cat := strings.TrimSpace(c.Query("category")); cat != "" {
		phases, types := ops.CategoryToFilter(cat)
		filter.ErrorPhasesAny = phases
		filter.ErrorTypesAny = types
	}

	if platform := strings.TrimSpace(c.Query("platform")); platform != "" {
		filter.Platform = platform
	}
	if v := strings.TrimSpace(c.Query("group_id")); v != "" {
		id, err := strconv.ParseInt(v, 10, 64)
		if err != nil || id <= 0 {
			response.BadRequest(c, "Invalid group_id")
			return
		}
		filter.GroupID = &id
	}
	if v := strings.TrimSpace(c.Query("provider_id")); v != "" {
		id, err := strconv.ParseInt(v, 10, 64)
		if err != nil || id <= 0 {
			response.BadRequest(c, "Invalid provider_id")
			return
		}
		filter.ProviderID = &id
	}

	if v := strings.TrimSpace(c.Query("user_id")); v != "" {
		id, err := strconv.ParseInt(v, 10, 64)
		if err != nil || id <= 0 {
			response.BadRequest(c, "Invalid user_id")
			return
		}
		filter.UserID = &id
	}
	if v := strings.TrimSpace(c.Query("api_key_id")); v != "" {
		id, err := strconv.ParseInt(v, 10, 64)
		if err != nil || id <= 0 {
			response.BadRequest(c, "Invalid api_key_id")
			return
		}
		filter.APIKeyID = &id
	}
	if v := strings.TrimSpace(c.Query("resolved")); v != "" {
		switch strings.ToLower(v) {
		case "1", "true", "yes":
			b := true
			filter.Resolved = &b
		case "0", "false", "no":
			b := false
			filter.Resolved = &b
		default:
			response.BadRequest(c, "Invalid resolved")
			return
		}
	}
	// Parse statuses as nonnegative integers, preserving duplicates and zero.
	// Blank comma parts disappear; an all-blank list adds no status condition when
	// storage reads it.
	if statusCodesStr := strings.TrimSpace(c.Query("status_codes")); statusCodesStr != "" {
		parts := strings.Split(statusCodesStr, ",")
		out := make([]int, 0, len(parts))
		for _, part := range parts {
			p := strings.TrimSpace(part)
			if p == "" {
				continue
			}
			n, err := strconv.Atoi(p)
			if err != nil || n < 0 {
				response.BadRequest(c, "Invalid status_codes")
				return
			}
			out = append(out, n)
		}
		filter.StatusCodes = out
	}

	applyOpsErrorSortParams(c, filter)

	result, err := h.opsService.GetErrorLogs(c.Request.Context(), filter)
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Paginated(c, result.Errors, int64(result.Total), result.Page, result.PageSize)
}

// ListRequestErrors queries the same error store with a one-hour default
// window, SLA view, category, status, group, and provider conditions. It checks
// service/monitoring and caps five hundred rows. user_id and api_key_id are not
// read here, while user_query remains an email-search input and result
// pagination comes from the core.
// GET /api/v1/admin/ops/request-errors
func (h *OpsHandler) ListRequestErrors(c *gin.Context) {
	if h.opsService == nil {
		response.Error(c, http.StatusServiceUnavailable, "Ops service not available")
		return
	}
	if err := h.opsService.RequireMonitoringEnabled(c.Request.Context()); err != nil {
		response.ErrorFrom(c, err)
		return
	}

	page, pageSize := response.ParsePagination(c)
	// Keep the error-list HTTP page within the storage five-hundred-row cap.
	if pageSize > 500 {
		pageSize = 500
	}
	startTime, endTime, err := parseOpsTimeRange(c, "1h")
	if err != nil {
		response.BadRequest(c, err.Error())
		return
	}

	filter := &ops.OpsErrorLogFilter{Page: page, PageSize: pageSize}
	if !startTime.IsZero() {
		filter.StartTime = &startTime
	}
	if !endTime.IsZero() {
		filter.EndTime = &endTime
	}
	filter.View = parseOpsViewParam(c)
	filter.Phase = strings.TrimSpace(c.Query("phase"))
	filter.Owner = strings.TrimSpace(c.Query("error_owner"))
	filter.Source = strings.TrimSpace(c.Query("error_source"))
	filter.RequestID = strings.TrimSpace(c.Query("request_id"))
	filter.Query = strings.TrimSpace(c.Query("q"))
	filter.UserQuery = strings.TrimSpace(c.Query("user_query"))
	// ModelFuzzy remains false for these administrator lists. SQL compares
	// requested_model before model even when requested_model is a nonnull empty
	// string, unlike display fallback handling.
	filter.Model = strings.TrimSpace(c.Query("model"))

	// Categories use the same phase/type alternatives as /usage/errors.
	// Unknown categories add no constraint; category and phase filters combine with AND.
	if cat := strings.TrimSpace(c.Query("category")); cat != "" {
		phases, types := ops.CategoryToFilter(cat)
		filter.ErrorPhasesAny = phases
		filter.ErrorTypesAny = types
	}

	if platform := strings.TrimSpace(c.Query("platform")); platform != "" {
		filter.Platform = platform
	}
	if v := strings.TrimSpace(c.Query("group_id")); v != "" {
		id, err := strconv.ParseInt(v, 10, 64)
		if err != nil || id <= 0 {
			response.BadRequest(c, "Invalid group_id")
			return
		}
		filter.GroupID = &id
	}
	if v := strings.TrimSpace(c.Query("provider_id")); v != "" {
		id, err := strconv.ParseInt(v, 10, 64)
		if err != nil || id <= 0 {
			response.BadRequest(c, "Invalid provider_id")
			return
		}
		filter.ProviderID = &id
	}

	if v := strings.TrimSpace(c.Query("resolved")); v != "" {
		switch strings.ToLower(v) {
		case "1", "true", "yes":
			b := true
			filter.Resolved = &b
		case "0", "false", "no":
			b := false
			filter.Resolved = &b
		default:
			response.BadRequest(c, "Invalid resolved")
			return
		}
	}
	// Parse statuses as nonnegative integers, preserving duplicates and zero.
	// Blank comma parts disappear; an all-blank list adds no status condition when
	// storage reads it.
	if statusCodesStr := strings.TrimSpace(c.Query("status_codes")); statusCodesStr != "" {
		parts := strings.Split(statusCodesStr, ",")
		out := make([]int, 0, len(parts))
		for _, part := range parts {
			p := strings.TrimSpace(part)
			if p == "" {
				continue
			}
			n, err := strconv.Atoi(p)
			if err != nil || n < 0 {
				response.BadRequest(c, "Invalid status_codes")
				return
			}
			out = append(out, n)
		}
		filter.StatusCodes = out
	}

	applyOpsErrorSortParams(c, filter)

	result, err := h.opsService.GetErrorLogs(c.Request.Context(), filter)
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Paginated(c, result.Errors, int64(result.Total), result.Page, result.PageSize)
}

// GetRequestError delegates the request-error detail URL to GetErrorLogByID
// using its numeric row ID and request context. Service, monitoring, and ID
// errors follow that handler; the alias adds no request-phase validation.
// GET /api/v1/admin/ops/request-errors/:id
func (h *OpsHandler) GetRequestError(c *gin.Context) {
	h.GetErrorLogByID(c)
}

// ListRequestErrorUpstreamErrors lists provider-owned upstream/provider_auth
// observations using correlation keys from a stored Ops row. Service/monitoring
// and a positive row ID are checked first. An origin with no saved request or
// client ID returns an empty page with fixed page one/size ten before
// additional query parsing.
//
// The saved request ID overrides query request_id when present; otherwise a
// saved client ID becomes an equality condition while any query request_id
// remains. Storage decides lookup aliases and intersects those conditions. The
// default window is thirty days unless explicit/ID-query range selection
// changes it, and recovered provider rows are requested with the all view.
//
// Trimmed include_detail values 1, true, or yes trigger per-row detail reads;
// true and yes ignore case. Nil
// list items, failed detail reads, and nil detail results are skipped, while
// the original total/page metadata is retained. The resulting expanded page can
// therefore contain fewer items than its reported total describes.
// GET /api/v1/admin/ops/request-errors/:id/upstream-errors
func (h *OpsHandler) ListRequestErrorUpstreamErrors(c *gin.Context) {
	if h.opsService == nil {
		response.Error(c, http.StatusServiceUnavailable, "Ops service not available")
		return
	}
	if err := h.opsService.RequireMonitoringEnabled(c.Request.Context()); err != nil {
		response.ErrorFrom(c, err)
		return
	}

	idStr := strings.TrimSpace(c.Param("id"))
	id, err := strconv.ParseInt(idStr, 10, 64)
	if err != nil || id <= 0 {
		response.BadRequest(c, "Invalid error id")
		return
	}

	// The route ID identifies an Ops row, not a request identifier. Load its
	// retained platform/client correlation text before constructing the
	// provider-observation query.
	detail, err := h.opsService.GetErrorLogByID(c.Request.Context(), id)
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}

	requestID := strings.TrimSpace(detail.RequestID)
	clientRequestID := strings.TrimSpace(detail.ClientRequestID)
	// Without either saved identifier, no correlation query can be formed. Return
	// the fixed empty page before parsing optional pagination or time-range
	// inputs.
	if requestID == "" && clientRequestID == "" {
		response.Paginated(c, []*ops.OpsErrorLog{}, 0, 1, 10)
		return
	}

	page, pageSize := response.ParsePagination(c)
	// Keep the error-list HTTP page within the storage five-hundred-row cap.
	if pageSize > 500 {
		pageSize = 500
	}

	// Use a thirty-day default for related provider observations. Explicit
	// endpoint or query-ID handling still belongs to the shared range selector and
	// can choose a different span.
	startTime, endTime, err := parseOpsTimeRange(c, "30d")
	if err != nil {
		response.BadRequest(c, err.Error())
		return
	}

	filter := &ops.OpsErrorLogFilter{Page: page, PageSize: pageSize}
	if !startTime.IsZero() {
		filter.StartTime = &startTime
	}
	if !endTime.IsZero() {
		filter.EndTime = &endTime
	}
	filter.View = "all"
	filter.ErrorPhasesAny = []string{"upstream", "provider_auth"}
	// Provider-phase observations can exist with a successful client status after
	// recovery. Opt into those rows while retaining owner, time, correlation, and
	// selected SLA-view conditions.
	filter.IncludeRecoveredUpstream = true
	filter.Owner = "provider"
	filter.Source = strings.TrimSpace(c.Query("error_source"))
	filter.RequestID = strings.TrimSpace(c.Query("request_id"))
	filter.Query = strings.TrimSpace(c.Query("q"))

	if platform := strings.TrimSpace(c.Query("platform")); platform != "" {
		filter.Platform = platform
	}

	// Prefer the origin saved request ID and overwrite the query selector only in
	// that branch. Client-ID fallback leaves a supplied query request_id active,
	// making storage intersect both conditions.
	if requestID != "" {
		filter.RequestID = requestID
	} else {
		filter.ClientRequestID = clientRequestID
	}

	applyOpsErrorSortParams(c, filter)

	result, err := h.opsService.GetErrorLogs(c.Request.Context(), filter)
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}

	// Expanded output reads each selected detail separately after the list query.
	// Failed or absent detail reads are omitted, and the list total/page metadata
	// is kept for that response.
	includeDetail := strings.TrimSpace(c.Query("include_detail"))
	if includeDetail == "1" || strings.EqualFold(includeDetail, "true") || strings.EqualFold(includeDetail, "yes") {
		details := make([]*ops.OpsErrorLogDetail, 0, len(result.Errors))
		for _, item := range result.Errors {
			if item == nil {
				continue
			}
			d, err := h.opsService.GetErrorLogByID(c.Request.Context(), item.ID)
			// A row can disappear or fail to load after listing. Skip its expansion
			// while retaining the original list total and continuing to read later rows.
			if err != nil || d == nil {
				continue
			}
			details = append(details, d)
		}
		response.Paginated(c, details, int64(result.Total), result.Page, result.PageSize)
		return
	}

	response.Paginated(c, result.Errors, int64(result.Total), result.Page, result.PageSize)
}

// ResolveRequestError forwards the submitted resolution state through
// UpdateErrorResolution for the request-error command URL. It inherits
// positive-subject, monitoring, numeric-ID, JSON, and storage handling, and
// adds no record-phase restriction or inversion of the current state.
// PUT /api/v1/admin/ops/request-errors/:id/resolve
func (h *OpsHandler) ResolveRequestError(c *gin.Context) {
	h.UpdateErrorResolution(c)
}

// ListUpstreamErrors queries provider-owned upstream/provider_auth observations
// and opts into recovered attempts after service/monitoring checks. It parses a
// one-hour default range, caps five hundred rows, and validates group/provider,
// resolved, and status selectors. Its selected SLA view remains an additional
// condition despite recovered-row inclusion.
// GET /api/v1/admin/ops/upstream-errors
func (h *OpsHandler) ListUpstreamErrors(c *gin.Context) {
	if h.opsService == nil {
		response.Error(c, http.StatusServiceUnavailable, "Ops service not available")
		return
	}
	if err := h.opsService.RequireMonitoringEnabled(c.Request.Context()); err != nil {
		response.ErrorFrom(c, err)
		return
	}

	page, pageSize := response.ParsePagination(c)
	// Keep the error-list HTTP page within the storage five-hundred-row cap.
	if pageSize > 500 {
		pageSize = 500
	}
	startTime, endTime, err := parseOpsTimeRange(c, "1h")
	if err != nil {
		response.BadRequest(c, err.Error())
		return
	}

	filter := &ops.OpsErrorLogFilter{Page: page, PageSize: pageSize}
	if !startTime.IsZero() {
		filter.StartTime = &startTime
	}
	if !endTime.IsZero() {
		filter.EndTime = &endTime
	}

	filter.View = parseOpsViewParam(c)
	filter.ErrorPhasesAny = []string{"upstream", "provider_auth"}
	// Provider-phase observations can exist with a successful client status after
	// recovery. Opt into those rows while retaining owner, time, correlation, and
	// selected SLA-view conditions.
	filter.IncludeRecoveredUpstream = true
	filter.Owner = "provider"
	filter.Source = strings.TrimSpace(c.Query("error_source"))
	filter.RequestID = strings.TrimSpace(c.Query("request_id"))
	filter.Query = strings.TrimSpace(c.Query("q"))

	if platform := strings.TrimSpace(c.Query("platform")); platform != "" {
		filter.Platform = platform
	}
	if v := strings.TrimSpace(c.Query("group_id")); v != "" {
		id, err := strconv.ParseInt(v, 10, 64)
		if err != nil || id <= 0 {
			response.BadRequest(c, "Invalid group_id")
			return
		}
		filter.GroupID = &id
	}
	if v := strings.TrimSpace(c.Query("provider_id")); v != "" {
		id, err := strconv.ParseInt(v, 10, 64)
		if err != nil || id <= 0 {
			response.BadRequest(c, "Invalid provider_id")
			return
		}
		filter.ProviderID = &id
	}

	if v := strings.TrimSpace(c.Query("resolved")); v != "" {
		switch strings.ToLower(v) {
		case "1", "true", "yes":
			b := true
			filter.Resolved = &b
		case "0", "false", "no":
			b := false
			filter.Resolved = &b
		default:
			response.BadRequest(c, "Invalid resolved")
			return
		}
	}
	// Validate integer status spelling without imposing an HTTP-code upper bound.
	// Blank components are skipped and duplicate/nonnegative values remain for the
	// storage condition.
	if statusCodesStr := strings.TrimSpace(c.Query("status_codes")); statusCodesStr != "" {
		parts := strings.Split(statusCodesStr, ",")
		out := make([]int, 0, len(parts))
		for _, part := range parts {
			p := strings.TrimSpace(part)
			if p == "" {
				continue
			}
			n, err := strconv.Atoi(p)
			if err != nil || n < 0 {
				response.BadRequest(c, "Invalid status_codes")
				return
			}
			out = append(out, n)
		}
		filter.StatusCodes = out
	}

	applyOpsErrorSortParams(c, filter)

	result, err := h.opsService.GetErrorLogs(c.Request.Context(), filter)
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Paginated(c, result.Errors, int64(result.Total), result.Page, result.PageSize)
}

// GetUpstreamError delegates the upstream detail URL to the shared numeric-ID
// detail handler. It inherits service, monitoring, and ID errors from
// GetErrorLogByID and adds no upstream-phase validation to the stored record
// lookup.
// GET /api/v1/admin/ops/upstream-errors/:id
func (h *OpsHandler) GetUpstreamError(c *gin.Context) {
	h.GetErrorLogByID(c)
}

// ResolveUpstreamError delegates the upstream command URL to
// UpdateErrorResolution with its submitted JSON state. It retains the shared
// operator, monitoring, ID, and storage checks; this alias does not enforce an
// upstream-phase condition.
// PUT /api/v1/admin/ops/upstream-errors/:id/resolve
func (h *OpsHandler) ResolveUpstreamError(c *gin.Context) {
	h.UpdateErrorResolution(c)
}

// ListRequestDetails returns combined success/error observations under a
// one-hour default range and a hundred-row HTTP page cap. It validates positive
// IDs and nonnegative duration values. Storage validates kind/sort, compares a
// lowercased platform without a whitelist, and combines independent duration
// bounds; reversed min/max values can yield an empty result. SLAOnly recognizes
// one or case-insensitive true, with other nonblank text selecting false.
//
// Absent storage can supply a fixed successful empty page from the core. Other
// query errors containing invalid in lowercase text receive HTTP 400, and
// remaining causes receive a generic HTTP 500. Successful response pagination
// uses core metadata, including its absent-storage defaults.
// GET /api/v1/admin/ops/requests
func (h *OpsHandler) ListRequestDetails(c *gin.Context) {
	if h.opsService == nil {
		response.Error(c, http.StatusServiceUnavailable, "Ops service not available")
		return
	}
	if err := h.opsService.RequireMonitoringEnabled(c.Request.Context()); err != nil {
		response.ErrorFrom(c, err)
		return
	}

	page, pageSize := response.ParsePagination(c)
	// Bound one drilldown page to a hundred returned observations.
	if pageSize > 100 {
		pageSize = 100
	}

	startTime, endTime, err := parseOpsTimeRange(c, "1h")
	if err != nil {
		response.BadRequest(c, err.Error())
		return
	}

	filter := &ops.OpsRequestDetailFilter{
		Page:      page,
		PageSize:  pageSize,
		StartTime: &startTime,
		EndTime:   &endTime,
	}

	filter.Kind = strings.TrimSpace(c.Query("kind"))
	filter.Platform = strings.TrimSpace(c.Query("platform"))
	filter.Model = strings.TrimSpace(c.Query("model"))
	filter.RequestID = strings.TrimSpace(c.Query("request_id"))
	filter.RequestID = strings.TrimSpace(c.Query("request_id"))
	filter.Query = strings.TrimSpace(c.Query("q"))
	filter.Sort = strings.TrimSpace(c.Query("sort"))
	if v := strings.TrimSpace(c.Query("sla_only")); v != "" {
		filter.SLAOnly = v == "1" || strings.EqualFold(v, "true")
	}

	if v := strings.TrimSpace(c.Query("user_id")); v != "" {
		id, err := strconv.ParseInt(v, 10, 64)
		if err != nil || id <= 0 {
			response.BadRequest(c, "Invalid user_id")
			return
		}
		filter.UserID = &id
	}
	if v := strings.TrimSpace(c.Query("api_key_id")); v != "" {
		id, err := strconv.ParseInt(v, 10, 64)
		if err != nil || id <= 0 {
			response.BadRequest(c, "Invalid api_key_id")
			return
		}
		filter.APIKeyID = &id
	}
	if v := strings.TrimSpace(c.Query("provider_id")); v != "" {
		id, err := strconv.ParseInt(v, 10, 64)
		if err != nil || id <= 0 {
			response.BadRequest(c, "Invalid provider_id")
			return
		}
		filter.ProviderID = &id
	}
	if v := strings.TrimSpace(c.Query("group_id")); v != "" {
		id, err := strconv.ParseInt(v, 10, 64)
		if err != nil || id <= 0 {
			response.BadRequest(c, "Invalid group_id")
			return
		}
		filter.GroupID = &id
	}

	if v := strings.TrimSpace(c.Query("min_duration_ms")); v != "" {
		parsed, err := strconv.Atoi(v)
		if err != nil || parsed < 0 {
			response.BadRequest(c, "Invalid min_duration_ms")
			return
		}
		filter.MinDurationMs = &parsed
	}
	if v := strings.TrimSpace(c.Query("max_duration_ms")); v != "" {
		parsed, err := strconv.Atoi(v)
		if err != nil || parsed < 0 {
			response.BadRequest(c, "Invalid max_duration_ms")
			return
		}
		filter.MaxDurationMs = &parsed
	}

	out, err := h.opsService.ListRequestDetails(c.Request.Context(), filter)
	if err != nil {
		// Classify this service failure by its message substring. Any lowercase text
		// containing invalid returns the original message as HTTP 400; other causes
		// receive the generic server response.
		if strings.Contains(strings.ToLower(err.Error()), "invalid") {
			response.BadRequest(c, err.Error())
			return
		}
		response.Error(c, http.StatusInternalServerError, "Failed to list request details")
		return
	}

	response.Paginated(c, out.Items, out.Total, out.Page, out.PageSize)
}

// UpdateErrorResolution sets the submitted state for a positive Ops row ID
// after service/monitoring checks and a positive authenticated user subject. It
// binds the JSON flag and passes that operator to the shared core. Call from
// the protected admin group; phase-specific aliases do not add record-phase
// restrictions.
// PUT /api/v1/admin/ops/errors/:id/resolve
func (h *OpsHandler) UpdateErrorResolution(c *gin.Context) {
	if h.opsService == nil {
		response.Error(c, http.StatusServiceUnavailable, "Ops service not available")
		return
	}
	if err := h.opsService.RequireMonitoringEnabled(c.Request.Context()); err != nil {
		response.ErrorFrom(c, err)
		return
	}

	subject, ok := authctx.GetAuthSubjectFromContext(c)
	if !ok || subject.UserID <= 0 {
		response.Error(c, http.StatusUnauthorized, "Unauthorized")
		return
	}

	idStr := strings.TrimSpace(c.Param("id"))
	id, err := strconv.ParseInt(idStr, 10, 64)
	if err != nil || id <= 0 {
		response.BadRequest(c, "Invalid error id")
		return
	}

	var req opsResolveRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, "Invalid request: "+err.Error())
		return
	}
	uid := subject.UserID
	if err := h.opsService.UpdateErrorResolution(c.Request.Context(), id, req.Resolved, &uid); err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Success(c, gin.H{"ok": true})
}

// parseOpsTimeRange selects endpoints from explicit timestamps, an ID query, or
// a duration preset. Supply a usable Gin request and recognized defaultRange.
// Timestamp decoding tries RFC3339Nano before RFC3339; nonempty endpoint
// strings take precedence over time_range, while a parsed zero time is treated
// as missing.
//
// For an explicit/partial range, missing end becomes now and missing start is
// inferred backward from the caller default. Equality is accepted here;
// reversed ranges or spans above thirty elapsed twenty-four-hour days fail. An
// invalid caller default supplies zero duration because its validity result is
// discarded.
//
// With both endpoint strings blank, nonblank request_id and literally empty raw
// time_range select UTC Unix epoch through current UTC time, returning before
// the thirty-day cap. Other requests use a trimmed, case-sensitive preset or
// the caller default for unknown text, ending at now. Storage readers can
// impose their own narrower or positive-range rules.
func parseOpsTimeRange(c *gin.Context, defaultRange string) (time.Time, time.Time, error) {
	startStr := strings.TrimSpace(c.Query("start_time"))
	endStr := strings.TrimSpace(c.Query("end_time"))

	parseTS := func(s string) (time.Time, error) {
		if s == "" {
			return time.Time{}, nil
		}
		// Try the nanosecond layout first, then return the classic RFC3339 parse
		// result. Both attempts concern the same supplied timestamp and leave their
		// accepted offset information intact.
		if t, err := time.Parse(time.RFC3339Nano, s); err == nil {
			return t, nil
		}
		return time.Parse(time.RFC3339, s)
	}

	start, err := parseTS(startStr)
	if err != nil {
		return time.Time{}, time.Time{}, err
	}
	end, err := parseTS(endStr)
	if err != nil {
		return time.Time{}, time.Time{}, err
	}

	// Any supplied endpoint string selects this branch before preset or ID-history
	// handling. Parsed zero times use the same missing-endpoint inference as
	// absent strings.
	if startStr != "" || endStr != "" {
		if end.IsZero() {
			end = time.Now()
		}
		if start.IsZero() {
			// Infer an absent/zero start from the selected end and caller preset. Its
			// ignored validity flag makes an unrecognized default a zero-width interval.
			dur, _ := parseOpsDuration(defaultRange)
			start = end.Add(-dur)
		}
		if start.After(end) {
			return time.Time{}, time.Time{}, fmt.Errorf("invalid time range: start_time must be <= end_time")
		}
		// The explicit-range cap is thirty elapsed twenty-four-hour days, independent
		// of calendar shifts.
		if end.Sub(start) > 30*24*time.Hour {
			return time.Time{}, time.Time{}, fmt.Errorf("invalid time range: max window is 30 days")
		}
		return start, end, nil
	}

	// An ID lookup with no endpoint text and literally empty raw time_range uses
	// the epoch-to-now UTC span. It returns before the normal thirty-day range
	// check.
	if strings.TrimSpace(c.Query("request_id")) != "" && c.Query("time_range") == "" {
		return time.Unix(0, 0).UTC(), time.Now().UTC(), nil
	}
	tr := strings.TrimSpace(c.Query("time_range"))
	if tr == "" {
		tr = defaultRange
	}
	dur, ok := parseOpsDuration(tr)
	// Unknown preset text selects the caller default rather than a validation
	// error. Its validity flag is ignored, so an unrecognized default still yields
	// zero duration.
	if !ok {
		dur, _ = parseOpsDuration(defaultRange)
	}

	end = time.Now()
	start = end.Add(-dur)
	// The preset-path cap uses elapsed time rather than calendar-day counting.
	if end.Sub(start) > 30*24*time.Hour {
		return time.Time{}, time.Time{}, fmt.Errorf("invalid time range: max window is 30 days")
	}
	return start, end, nil
}

// parseOpsDuration returns the fixed elapsed duration for the recognized Ops
// preset spellings after trimming whitespace. Accepted values are 5m, 30m, 1h,
// 6h, 24h, 7d, and 30d; unknown or differently cased text returns zero and
// false.
func parseOpsDuration(v string) (time.Duration, bool) {
	// Presets define fixed elapsed Ops query windows. Day choices use exact
	// twenty-four-hour multiples, so timezone or daylight-saving transitions do
	// not change their duration.
	switch strings.TrimSpace(v) {
	case "5m":
		return 5 * time.Minute, true
	case "30m":
		return 30 * time.Minute, true
	case "1h":
		return time.Hour, true
	case "6h":
		return 6 * time.Hour, true
	case "24h":
		return 24 * time.Hour, true
	case "7d":
		return 7 * 24 * time.Hour, true
	case "30d":
		return 30 * 24 * time.Hour, true
	default:
		return 0, false
	}
}
