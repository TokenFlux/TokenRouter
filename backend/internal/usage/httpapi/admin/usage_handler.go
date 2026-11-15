package admin

import (
	"context"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"

	idemhttp "github.com/TokenFlux/TokenRouter/internal/idempotency/httpapi"
	"github.com/TokenFlux/TokenRouter/internal/identity/httpapi/authctx"
	logger "github.com/TokenFlux/TokenRouter/internal/infra/telemetry/logging"
	"github.com/TokenFlux/TokenRouter/internal/pkg/pagination"
	"github.com/TokenFlux/TokenRouter/internal/pkg/timezone"
	response "github.com/TokenFlux/TokenRouter/internal/server/httpx"
	"github.com/TokenFlux/TokenRouter/internal/usage"
	"github.com/TokenFlux/TokenRouter/internal/usage/httpapi/dto"
	"github.com/TokenFlux/TokenRouter/internal/usage/httpapi/ports"
)

// usage_handler.go handles administrator usage searches, statistics, timing
// enrichment, cleanup commands, and background export-task dependencies.
// Administrator usage queries combine record selectors with optional
// retained-history request identity lookup. List accepts independent time
// bounds, while Stats uses both explicit dates together or a period default,
// then removes default bounds for a request-ID query that supplied no dates
// or period.
//
// Cleanup commands create persisted tasks with inclusive end instants and
// operator attribution. Their date-only end expansion uses a fixed
// twenty-four hours minus one nanosecond, while report dates use calendar-day
// exclusive ends. Detailed timing enrichment joins aligned record and
// response slices by trimmed local identity, including a historical client:
// prefix, and leaves timing absent when observation lookup fails.

// UsageHandler serves administrator usage reads, searches, cleanup-task
// commands, and persisted CSV export-task commands.
type UsageHandler struct {
	idemhttp.Executor // administrator command idempotency helpers

	calendar      timezone.Calendar   // usage query parsing calendar
	usageService  *usage.UsageService // usage record and statistics service
	apiKeyService ports.KeyReader     // key filter suggestion reader
	adminService  ports.UserReader    // account filter suggestion reader
	// cleanupService handles persisted cleanup commands and pages. Nil produces
	// a service-unavailable response from cleanup routes while ordinary usage
	// reads can remain configured.
	cleanupService *usage.UsageCleanupService // optional cleanup task service
	// exportService handles persisted background CSV exports and signed download
	// tickets. Nil produces a service-unavailable response from export routes
	// while ordinary usage and cleanup routes remain operational.
	exportService *usage.UsageExportService // optional background export service
}

// CreateUsageCleanupTaskRequest carries administrator deletion dates,
// optional equality selectors and a parsing timezone.
type CreateUsageCleanupTaskRequest struct {
	RequestSource string `json:"request_source"` // optional customer or system source selector
	// StartDate and EndDate are required date-only text parsed in Timezone or
	// the server calendar. Creation expands EndDate by fixed elapsed-day
	// arithmetic to an inclusive instant.
	StartDate  string  `json:"start_date"`  // required cleanup start date
	EndDate    string  `json:"end_date"`    // required cleanup end date
	UserID     *int64  `json:"user_id"`     // optional actor user selector
	APIKeyID   *int64  `json:"api_key_id"`  // optional key equality selector
	ProviderID *int64  `json:"provider_id"` // optional provider equality selector
	GroupID    *int64  `json:"group_id"`    // optional group equality selector
	TeamID     *int64  `json:"team_id"`     // optional team equality selector
	Model      *string `json:"model"`       // optional stored model selector
	// RequestType takes precedence when its pointer is supplied. Creation parses
	// the label and clears Stream before forwarding filters, returning a
	// bad-request response for invalid category text.
	RequestType *string `json:"request_type"` // optional request category text
	Stream      *bool   `json:"stream"`       // optional legacy stream selector
	BillingType *int8   `json:"billing_type"` // optional funding category selector
	Timezone    string  `json:"timezone"`     // requested date parsing timezone
}

// NewUsageHandler binds administrator usage, search, cleanup and timing
// readers for HTTP routes.
func NewUsageHandler(
	usageService *usage.UsageService,
	apiKeyService ports.KeyReader,
	adminService ports.UserReader,
	cleanupService *usage.UsageCleanupService,

	calendar timezone.Calendar,
) *UsageHandler {
	return &UsageHandler{
		calendar:       calendar,
		usageService:   usageService,
		apiKeyService:  apiKeyService,
		adminService:   adminService,
		cleanupService: cleanupService,
	}
}

// SetExportService attaches the background CSV export service used by
// administrator export-task endpoints. Call during handler assembly before
// serving HTTP requests; nil leaves export routes unavailable.
func (h *UsageHandler) SetExportService(exportService *usage.UsageExportService) {
	if h == nil {
		return
	}
	h.exportService = exportService
}

// List returns an administrator usage page under validated scalar selectors
// and optional independent time bounds. Request category takes precedence
// over stream, date-only ends advance by calendar day, and exact_total
// requests the repository's full count.
func (h *UsageHandler) List(c *gin.Context) {
	requestSource := strings.TrimSpace(c.Query("request_source"))
	if !usage.ValidRequestSource(requestSource) {
		response.BadRequest(c, "Invalid request_source")
		return
	}

	page, pageSize := response.ParsePagination(c)
	exactTotal := false
	if exactTotalRaw := strings.TrimSpace(c.Query("exact_total")); exactTotalRaw != "" {
		parsed, err := strconv.ParseBool(exactTotalRaw)
		if err != nil {
			response.BadRequest(c, "Invalid exact_total value, use true or false")
			return
		}
		exactTotal = parsed
	}

	var userID, apiKeyID, providerID, groupID, teamID int64
	if userIDStr := c.Query("user_id"); userIDStr != "" {
		id, err := strconv.ParseInt(userIDStr, 10, 64)
		if err != nil {
			response.BadRequest(c, "Invalid user_id")
			return
		}
		userID = id
	}

	if apiKeyIDStr := c.Query("api_key_id"); apiKeyIDStr != "" {
		id, err := strconv.ParseInt(apiKeyIDStr, 10, 64)
		if err != nil {
			response.BadRequest(c, "Invalid api_key_id")
			return
		}
		apiKeyID = id
	}

	if providerIDStr := c.Query("provider_id"); providerIDStr != "" {
		id, err := strconv.ParseInt(providerIDStr, 10, 64)
		if err != nil {
			response.BadRequest(c, "Invalid provider_id")
			return
		}
		providerID = id
	}

	if groupIDStr := c.Query("group_id"); groupIDStr != "" {
		id, err := strconv.ParseInt(groupIDStr, 10, 64)
		if err != nil {
			response.BadRequest(c, "Invalid group_id")
			return
		}
		groupID = id
	}
	if teamIDStr := c.Query("team_id"); teamIDStr != "" {
		id, err := strconv.ParseInt(teamIDStr, 10, 64)
		if err != nil {
			response.BadRequest(c, "Invalid team_id")
			return
		}
		teamID = id
	}

	model := c.Query("model")
	requestID := strings.TrimSpace(c.Query("request_id"))
	billingMode := strings.TrimSpace(c.Query("billing_mode"))

	var requestType *int16
	var stream *bool
	var nativeCompactionV2 *bool
	if requestTypeStr := strings.TrimSpace(c.Query("request_type")); requestTypeStr != "" {
		parsed, err := usage.ParseUsageRequestType(requestTypeStr)
		if err != nil {
			response.BadRequest(c, err.Error())
			return
		}
		value := int16(parsed)
		requestType = &value
	} else if streamStr := c.Query("stream"); streamStr != "" {
		val, err := strconv.ParseBool(streamStr)
		if err != nil {
			response.BadRequest(c, "Invalid stream value, use true or false")
			return
		}
		stream = &val
	}
	if raw := strings.TrimSpace(c.Query("native_compaction_v2")); raw != "" {
		val, err := strconv.ParseBool(raw)
		if err != nil {
			response.BadRequest(c, "Invalid native_compaction_v2 value, use true or false")
			return
		}
		nativeCompactionV2 = &val
	}

	var billingType *int8
	if billingTypeStr := c.Query("billing_type"); billingTypeStr != "" {
		val, err := strconv.ParseInt(billingTypeStr, 10, 8)
		if err != nil {
			response.BadRequest(c, "Invalid billing_type")
			return
		}
		bt := int8(val)
		billingType = &bt
	}

	var startTime, endTime *time.Time
	userTZ := c.Query("timezone")
	if startDateStr := c.Query("start_date"); startDateStr != "" {
		t, _, err := h.calendar.ParseDateTimeInUserLocation(startDateStr, userTZ)
		if err != nil {
			response.BadRequest(c, "Invalid start_date format, use YYYY-MM-DD or YYYY-MM-DDTHH:mm:ss")
			return
		}
		startTime = &t
	}

	if endDateStr := c.Query("end_date"); endDateStr != "" {
		t, dateOnly, err := h.calendar.ParseDateTimeInUserLocation(endDateStr, userTZ)
		if err != nil {
			response.BadRequest(c, "Invalid end_date format, use YYYY-MM-DD or YYYY-MM-DDTHH:mm:ss")
			return
		}
		if dateOnly {
			// Calendar expansion includes the selected local date even when its DST
			// transition makes the elapsed day shorter or longer than twenty-four hours.
			t = t.AddDate(0, 0, 1)
		}
		endTime = &t
	}

	params := pagination.PaginationParams{
		Page:      page,
		PageSize:  pageSize,
		SortBy:    c.DefaultQuery("sort_by", "created_at"),
		SortOrder: c.DefaultQuery("sort_order", "desc"),
	}
	filters := usage.UsageLogFilters{
		UserID:             userID,
		APIKeyID:           apiKeyID,
		ProviderID:         providerID,
		GroupID:            groupID,
		TeamID:             teamID,
		RequestID:          requestID,
		Model:              model,
		ModelFilterSource:  usage.ModelSourceRequested,
		RequestType:        requestType,
		RequestSource:      requestSource,
		Stream:             stream,
		NativeCompactionV2: nativeCompactionV2,
		BillingType:        billingType,
		BillingMode:        billingMode,
		StartTime:          startTime,
		EndTime:            endTime,
		ExactTotal:         exactTotal,
	}

	records, result, err := h.usageService.ListWithFilters(c.Request.Context(), params, filters)
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}

	out := make([]dto.AdminUsageLog, 0, len(records))
	for i := range records {
		out = append(out, *dto.FromUsageAdmin(&records[i]))
	}
	response.Paginated(c, out, result.Total, page, pageSize)
}

// Stats returns filtered administrator totals and endpoint charts, optionally
// using the statistics cache. Both explicit dates select a parsed range;
// other cases use the requested today, week or month period through the
// current instant.
//
// A request-ID query supplying neither dates nor period clears the default
// bounds for retained-history lookup. Endpoint-source validation selects all
// or one chart, and request category takes precedence over legacy stream.
// nocache bypasses lookup and insertion for this response; it leaves existing
// entries available to later cached requests.
func (h *UsageHandler) Stats(c *gin.Context) {
	requestSource := strings.TrimSpace(c.Query("request_source"))
	if !usage.ValidRequestSource(requestSource) {
		response.BadRequest(c, "Invalid request_source")
		return
	}

	var userID, apiKeyID, providerID, groupID, teamID int64
	if userIDStr := c.Query("user_id"); userIDStr != "" {
		id, err := strconv.ParseInt(userIDStr, 10, 64)
		if err != nil {
			response.BadRequest(c, "Invalid user_id")
			return
		}
		userID = id
	}

	if apiKeyIDStr := c.Query("api_key_id"); apiKeyIDStr != "" {
		id, err := strconv.ParseInt(apiKeyIDStr, 10, 64)
		if err != nil {
			response.BadRequest(c, "Invalid api_key_id")
			return
		}
		apiKeyID = id
	}

	if providerIDStr := c.Query("provider_id"); providerIDStr != "" {
		id, err := strconv.ParseInt(providerIDStr, 10, 64)
		if err != nil {
			response.BadRequest(c, "Invalid provider_id")
			return
		}
		providerID = id
	}

	if groupIDStr := c.Query("group_id"); groupIDStr != "" {
		id, err := strconv.ParseInt(groupIDStr, 10, 64)
		if err != nil {
			response.BadRequest(c, "Invalid group_id")
			return
		}
		groupID = id
	}
	if teamIDStr := c.Query("team_id"); teamIDStr != "" {
		id, err := strconv.ParseInt(teamIDStr, 10, 64)
		if err != nil {
			response.BadRequest(c, "Invalid team_id")
			return
		}
		teamID = id
	}

	model := c.Query("model")
	billingMode := strings.TrimSpace(c.Query("billing_mode"))

	var requestType *int16
	var stream *bool
	var nativeCompactionV2 *bool
	if requestTypeStr := strings.TrimSpace(c.Query("request_type")); requestTypeStr != "" {
		parsed, err := usage.ParseUsageRequestType(requestTypeStr)
		if err != nil {
			response.BadRequest(c, err.Error())
			return
		}
		value := int16(parsed)
		requestType = &value
	} else if streamStr := c.Query("stream"); streamStr != "" {
		val, err := strconv.ParseBool(streamStr)
		if err != nil {
			response.BadRequest(c, "Invalid stream value, use true or false")
			return
		}
		stream = &val
	}
	if raw := strings.TrimSpace(c.Query("native_compaction_v2")); raw != "" {
		val, err := strconv.ParseBool(raw)
		if err != nil {
			response.BadRequest(c, "Invalid native_compaction_v2 value, use true or false")
			return
		}
		nativeCompactionV2 = &val
	}

	var billingType *int8
	if billingTypeStr := c.Query("billing_type"); billingTypeStr != "" {
		val, err := strconv.ParseInt(billingTypeStr, 10, 8)
		if err != nil {
			response.BadRequest(c, "Invalid billing_type")
			return
		}
		bt := int8(val)
		billingType = &bt
	}

	userTZ := c.Query("timezone")
	now := h.calendar.NowInUserLocation(userTZ)
	var startTime, endTime time.Time

	startDateStr := c.Query("start_date")
	endDateStr := c.Query("end_date")

	if startDateStr != "" && endDateStr != "" {
		var err error
		startTime, _, err = h.calendar.ParseDateTimeInUserLocation(startDateStr, userTZ)
		if err != nil {
			response.BadRequest(c, "Invalid start_date format, use YYYY-MM-DD or YYYY-MM-DDTHH:mm:ss")
			return
		}
		var dateOnly bool
		endTime, dateOnly, err = h.calendar.ParseDateTimeInUserLocation(endDateStr, userTZ)
		if err != nil {
			response.BadRequest(c, "Invalid end_date format, use YYYY-MM-DD or YYYY-MM-DDTHH:mm:ss")
			return
		}
		if dateOnly {
			// Calendar expansion matches the repository's exclusive bound while covering
			// the selected local date across DST days with different elapsed durations.
			endTime = endTime.AddDate(0, 0, 1)
		}
	} else {
		// Explicit dates select a range only as a pair. A missing partner uses the
		// endpoint's period window, including its default today start, through the current instant.
		period := c.DefaultQuery("period", "today")
		switch period {
		case "today":
			startTime = h.calendar.StartOfDayInUserLocation(now, userTZ)
		case "week":
			startTime = now.AddDate(0, 0, -7)
		case "month":
			startTime = now.AddDate(0, -1, 0)
		default:
			startTime = h.calendar.StartOfDayInUserLocation(now, userTZ)
		}
		endTime = now
	}

	endpointSource := c.Query("endpoint_source")
	switch endpointSource {
	case "all":
		endpointSource = ""
	case "", "inbound", "upstream", "path":
	default:
		response.BadRequest(c, "Invalid endpoint_source")
		return
	}
	filters := usage.UsageLogFilters{
		EndpointSource:     endpointSource,
		RequestID:          strings.TrimSpace(c.Query("request_id")),
		UserID:             userID,
		APIKeyID:           apiKeyID,
		ProviderID:         providerID,
		GroupID:            groupID,
		TeamID:             teamID,
		Model:              model,
		ModelFilterSource:  usage.ModelSourceRequested,
		RequestType:        requestType,
		RequestSource:      requestSource,
		Stream:             stream,
		NativeCompactionV2: nativeCompactionV2,
		BillingType:        billingType,
		BillingMode:        billingMode,
		StartTime:          &startTime,
		EndTime:            &endTime,
	}

	// Exact request lookup needs all retained history when the caller supplied no
	// range choice. Remove period defaults while retaining the remaining administrator selectors.
	if filters.RequestID != "" && startDateStr == "" && endDateStr == "" && c.Query("period") == "" {
		filters.StartTime = nil
		filters.EndTime = nil
	}

	var stats *usage.UsageStats
	// nocache bypasses this response's lookup and insertion. Existing snapshots remain
	// available for later callers, so this flag is a read choice rather than cache eviction.
	if parseBoolQueryWithDefault(c.Query("nocache"), false) {
		s, err := h.usageService.GetStatsWithFilters(c.Request.Context(), filters)
		if err != nil {
			response.ErrorFrom(c, err)
			return
		}
		stats = s
		c.Header("X-Usage-Stats-Cache", "bypass")
	} else {
		s, hit, err := h.getStatsCached(c.Request.Context(), filters)
		if err != nil {
			response.ErrorFrom(c, err)
			return
		}
		stats = s
		c.Header("X-Usage-Stats-Cache", cacheStatusValue(hit))
	}

	response.Success(c, stats)
}

// SearchUsers returns up to thirty email-ordered account suggestions for
// nonempty query text. It includes deleted accounts and exposes their
// deletion flag, while empty query text returns an empty successful list.
func (h *UsageHandler) SearchUsers(c *gin.Context) {
	keyword := c.Query("q")
	if keyword == "" {
		response.Success(c, []any{})
		return
	}

	// Retained usage can belong to deleted accounts. Include those identities in
	// the bounded suggestion page so administrators can still select their historical records.
	users, _, err := h.adminService.ListUsers(c.Request.Context(), 1, 30, ports.UserListFilters{Search: keyword, IncludeDeleted: true}, "email", "asc")
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}

	// SimpleUser carries the suggestion identity and deletion flag exposed by
	// administrator search.
	type SimpleUser struct {
		ID      int64  `json:"id"`      // suggested account identifier
		Email   string `json:"email"`   // suggested account contact email
		Deleted bool   `json:"deleted"` // stored soft deletion flag
	}

	result := make([]SimpleUser, len(users))
	for i, u := range users {
		result[i] = SimpleUser{
			ID:      u.ID,
			Email:   u.Email,
			Deleted: u.DeletedAt != nil,
		}
	}

	response.Success(c, result)
}

// SearchAPIKeys returns up to thirty key suggestions under an optional
// actor-user filter. Invalid user-ID text returns bad request; each result
// exposes the key ID, name and owner for filter selection.
func (h *UsageHandler) SearchAPIKeys(c *gin.Context) {
	userIDStr := c.Query("user_id")
	keyword := c.Query("q")

	var userID int64
	if userIDStr != "" {
		id, err := strconv.ParseInt(userIDStr, 10, 64)
		if err != nil {
			response.BadRequest(c, "Invalid user_id")
			return
		}
		userID = id
	}

	keys, err := h.apiKeyService.SearchAPIKeys(c.Request.Context(), userID, keyword, 30)
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}

	// SimpleAPIKey carries key identity and actor ownership for administrator
	// filter suggestions.
	type SimpleAPIKey struct {
		ID     int64  `json:"id"`      // suggested key identifier
		Name   string `json:"name"`    // suggested key display name
		UserID int64  `json:"user_id"` // key actor owner identifier
	}

	result := make([]SimpleAPIKey, len(keys))
	for i, k := range keys {
		result[i] = SimpleAPIKey{
			ID:     k.ID,
			Name:   k.Name,
			UserID: k.UserID,
		}
	}

	response.Success(c, result)
}

// ListCleanupTasks returns the persisted cleanup page formatted through
// CleanupFromUsage. A missing cleanup service returns unavailable;
// administrator authentication belongs to the registered parent route, and
// operator identity is recorded for diagnostics.
func (h *UsageHandler) ListCleanupTasks(c *gin.Context) {
	if h.cleanupService == nil {
		response.Error(c, http.StatusServiceUnavailable, "Usage cleanup service unavailable")
		return
	}
	operator := int64(0)
	if subject, ok := authctx.GetAuthSubjectFromContext(c); ok {
		operator = subject.UserID
	}
	page, pageSize := response.ParsePagination(c)
	logger.LegacyPrintf("handler.admin.usage", "[UsageCleanup] Listing cleanup tasks: operator=%d page=%d page_size=%d", operator, page, pageSize)
	params := pagination.PaginationParams{Page: page, PageSize: pageSize}
	tasks, result, err := h.cleanupService.ListTasks(c.Request.Context(), params)
	if err != nil {
		logger.LegacyPrintf("handler.admin.usage", "[INFO] [UsageCleanup] Failed to list cleanup tasks: operator=%d page=%d page_size=%d err=%v", operator, page, pageSize, err)
		response.ErrorFrom(c, err)
		return
	}
	out := make([]dto.UsageCleanupTask, 0, len(tasks))
	for i := range tasks {
		out = append(out, *dto.CleanupFromUsage(&tasks[i]))
	}
	logger.LegacyPrintf("handler.admin.usage", "[UsageCleanup] Returning cleanup task list: operator=%d total=%d items=%d page=%d page_size=%d", operator, result.Total, len(out), page, pageSize)
	response.Paginated(c, out, result.Total, page, pageSize)
}

// CreateCleanupTask validates date-only bounds and creates a persisted
// administrator deletion task through the idempotency executor. A positive
// authenticated operator is required, and the request category takes
// precedence over its legacy stream selector.
//
// The end date becomes an inclusive instant by adding a fixed twenty-four
// hours minus one nanosecond. The idempotency payload includes the operator
// and date-trimmed request body, while UsageCleanupService sanitizes stored
// selectors and dispatches execution after persistence. The response
// represents accepted task creation, with deletion progress available through
// later task reads.
func (h *UsageHandler) CreateCleanupTask(c *gin.Context) {
	if h.cleanupService == nil {
		response.Error(c, http.StatusServiceUnavailable, "Usage cleanup service unavailable")
		return
	}
	subject, ok := authctx.GetAuthSubjectFromContext(c)
	if !ok || subject.UserID <= 0 {
		response.Unauthorized(c, "Unauthorized")
		return
	}

	var req CreateUsageCleanupTaskRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, "Invalid request: "+err.Error())
		return
	}
	req.StartDate = strings.TrimSpace(req.StartDate)
	req.EndDate = strings.TrimSpace(req.EndDate)
	if req.StartDate == "" || req.EndDate == "" {
		response.BadRequest(c, "start_date and end_date are required")
		return
	}

	startTime, err := h.calendar.ParseInUserLocation("2006-01-02", req.StartDate, req.Timezone)
	if err != nil {
		response.BadRequest(c, "Invalid start_date format, use YYYY-MM-DD")
		return
	}
	endTime, err := h.calendar.ParseInUserLocation("2006-01-02", req.EndDate, req.Timezone)
	if err != nil {
		response.BadRequest(c, "Invalid end_date format, use YYYY-MM-DD")
		return
	}
	// Cleanup SQL includes its end instant. This fixed elapsed-day expansion can
	// cross a local midnight differently on DST dates than calendar next-day construction.
	endTime = endTime.Add(24*time.Hour - time.Nanosecond)

	var requestType *int16
	stream := req.Stream
	if !usage.ValidRequestSource(req.RequestSource) {
		response.BadRequest(c, "Invalid request_source")
		return
	}
	if req.RequestType != nil {
		parsed, err := usage.ParseUsageRequestType(*req.RequestType)
		if err != nil {
			response.BadRequest(c, err.Error())
			return
		}
		value := int16(parsed)
		requestType = &value
		stream = nil
	}

	filters := usage.UsageCleanupFilters{
		StartTime:     startTime,
		EndTime:       endTime,
		UserID:        req.UserID,
		APIKeyID:      req.APIKeyID,
		ProviderID:    req.ProviderID,
		GroupID:       req.GroupID,
		TeamID:        req.TeamID,
		Model:         req.Model,
		RequestType:   requestType,
		RequestSource: req.RequestSource,
		Stream:        stream,
		BillingType:   req.BillingType,
	}

	var userID any
	if filters.UserID != nil {
		userID = *filters.UserID
	}
	var apiKeyID any
	if filters.APIKeyID != nil {
		apiKeyID = *filters.APIKeyID
	}
	var providerID any
	if filters.ProviderID != nil {
		providerID = *filters.ProviderID
	}
	var groupID any
	if filters.GroupID != nil {
		groupID = *filters.GroupID
	}
	var model any
	if filters.Model != nil {
		model = *filters.Model
	}
	var streamValue any
	if filters.Stream != nil {
		streamValue = *filters.Stream
	}
	var requestTypeName any
	if filters.RequestType != nil {
		requestTypeName = usage.RequestTypeFromInt16(*filters.RequestType).String()
	}
	var billingType any
	if filters.BillingType != nil {
		billingType = *filters.BillingType
	}

	idempotencyPayload := struct {
		OperatorID int64                         `json:"operator_id"` // authenticated administrator user identifier
		Body       CreateUsageCleanupTaskRequest `json:"body"`        // date trimmed cleanup request body
	}{
		OperatorID: subject.UserID,
		Body:       req,
	}
	// The persisted task is created inside the idempotent command callback before
	// the response DTO is returned, allowing command replay to reuse the recorded result.
	h.ExecuteAdminIdempotentJSON(c, "admin.usage.cleanup_tasks.create", idempotencyPayload, h.DefaultWriteIdempotencyTTL(), func(ctx context.Context) (any, error) {
		logger.LegacyPrintf("handler.admin.usage", "[UsageCleanup] Creating cleanup task: operator=%d start=%s end=%s user_id=%v api_key_id=%v provider_id=%v group_id=%v model=%v request_type=%v stream=%v billing_type=%v tz=%q",
			subject.UserID,
			filters.StartTime.Format(time.RFC3339),
			filters.EndTime.Format(time.RFC3339),
			userID,
			apiKeyID,
			providerID,
			groupID,
			model,
			requestTypeName,
			streamValue,
			billingType,
			req.Timezone,
		)

		task, err := h.cleanupService.CreateTask(ctx, filters, subject.UserID)
		if err != nil {
			logger.LegacyPrintf("handler.admin.usage", "[INFO] [UsageCleanup] Failed to create cleanup task: operator=%d err=%v", subject.UserID, err)
			return nil, err
		}
		logger.LegacyPrintf("handler.admin.usage", "[UsageCleanup] Cleanup task created: task=%d operator=%d status=%s", task.ID, subject.UserID, task.Status)
		return dto.CleanupFromUsage(task), nil
	})
}

// CancelCleanupTask records cancellation for a positive task ID and
// authenticated operator. A missing cleanup service returns unavailable; the
// service checks eligible states and the response reports canceled after its
// conditional update succeeds.
func (h *UsageHandler) CancelCleanupTask(c *gin.Context) {
	if h.cleanupService == nil {
		response.Error(c, http.StatusServiceUnavailable, "Usage cleanup service unavailable")
		return
	}
	subject, ok := authctx.GetAuthSubjectFromContext(c)
	if !ok || subject.UserID <= 0 {
		response.Unauthorized(c, "Unauthorized")
		return
	}
	idStr := strings.TrimSpace(c.Param("id"))
	taskID, err := strconv.ParseInt(idStr, 10, 64)
	if err != nil || taskID <= 0 {
		response.BadRequest(c, "Invalid task id")
		return
	}
	logger.LegacyPrintf("handler.admin.usage", "[UsageCleanup] Cancelling cleanup task: task=%d operator=%d", taskID, subject.UserID)
	if err := h.cleanupService.CancelTask(c.Request.Context(), taskID, subject.UserID); err != nil {
		logger.LegacyPrintf("handler.admin.usage", "[INFO] [UsageCleanup] Failed to cancel cleanup task: task=%d operator=%d err=%v", taskID, subject.UserID, err)
		response.ErrorFrom(c, err)
		return
	}
	logger.LegacyPrintf("handler.admin.usage", "[UsageCleanup] Cleanup task cancelled: task=%d operator=%d", taskID, subject.UserID)
	response.Success(c, gin.H{"id": taskID, "status": usage.UsageCleanupStatusCanceled})
}

// getStatsCached returns administrator statistics and cache-hit status from
// UsageService. Callers supply a nonnil context and immutable filter pointers
// while a matching shared load can continue after one waiter cancels.
func (h *UsageHandler) getStatsCached(ctx context.Context, f usage.UsageLogFilters) (*usage.UsageStats, bool, error) {
	return h.usageService.GetStatsCached(ctx, f)
}
