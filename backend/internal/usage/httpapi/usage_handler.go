package httpapi

import (
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/TokenFlux/TokenRouter/internal/billing/pricing"
	idemhttp "github.com/TokenFlux/TokenRouter/internal/idempotency/httpapi"
	"github.com/TokenFlux/TokenRouter/internal/identity/httpapi/authctx"
	"github.com/TokenFlux/TokenRouter/internal/ops"
	"github.com/TokenFlux/TokenRouter/internal/pkg/locale"
	"github.com/TokenFlux/TokenRouter/internal/pkg/pagination"
	"github.com/TokenFlux/TokenRouter/internal/pkg/timezone"
	response "github.com/TokenFlux/TokenRouter/internal/server/httpx"
	"github.com/TokenFlux/TokenRouter/internal/usage"
	"github.com/TokenFlux/TokenRouter/internal/usage/httpapi/dto"
	"github.com/TokenFlux/TokenRouter/internal/usage/httpapi/ports"
)

// usage_handler.go serves authenticated user usage history, rankings, error
// records, dashboard reports, and export-task handler dependencies.
// User usage reports bind visibility to the authenticated actor and include
// its owned team's records through repository scope. Direct record detail is
// stricter: the stored actor must match the requester before request metadata
// is exposed.
//
// Optional list bounds remain absent until supplied, while chart defaults
// select a calendar or period range. Request-ID queries keep omitted filter
// bounds unrestricted and supply epoch-to-now scalar bounds for charts. User
// formatting exposes charged costs and measured usage while omitting provider
// costs and outbound breakdowns. Ranking response pointers keep visible zero
// metrics distinct from fields hidden by the supplied visibility settings.

// Daily key-usage limits bound the default calendar-day count and largest
// accepted user or public report range.
const (
	defaultAPIKeyDailyUsageDays = 30 // default daily report day count
	maxAPIKeyDailyUsageDays     = 90 // maximum daily report day count
)

// userUsageFilters carries scoped repository selectors and scalar bounds
// needed by user chart calls.
type userUsageFilters struct {
	// Filters retains optional time pointers, including nil when request lookup
	// supplied no dates. Chart methods separately receive scalar StartTime and
	// EndTime values for their query signatures.
	Filters usage.UsageLogFilters // scoped repository query selectors
	// StartTime and EndTime hold chart bounds. Request-ID queries with omitted
	// dates use epoch and current UTC time here while corresponding Filters time
	// pointers remain nil.
	StartTime time.Time // scalar chart start instant
	EndTime   time.Time // scalar chart end instant
}

// userModelStat carries model usage and charged amounts selected for user
// response encoding.
type userModelStat struct {
	Model               string  `json:"model"`                 // internal request model label
	Requests            int64   `json:"requests"`              // matching usage record count
	InputTokens         int64   `json:"input_tokens"`          // measured input token count
	OutputTokens        int64   `json:"output_tokens"`         // measured output token count
	CacheCreationTokens int64   `json:"cache_creation_tokens"` // cache creation token count
	CacheReadTokens     int64   `json:"cache_read_tokens"`     // cache read token count
	TotalTokens         int64   `json:"total_tokens"`          // combined general token total
	Cost                float64 `json:"cost"`                  // standard cost total USD
	ActualCost          float64 `json:"actual_cost"`           // user charge total USD
}

// userGroupStat carries group identity, measured usage and charges selected
// for user response encoding.
type userGroupStat struct {
	GroupID     int64   `json:"group_id"`     // selected group identifier
	GroupName   string  `json:"group_name"`   // current group display name
	Requests    int64   `json:"requests"`     // matching usage record count
	TotalTokens int64   `json:"total_tokens"` // combined general token total
	Cost        float64 `json:"cost"`         // standard cost total USD
	ActualCost  float64 `json:"actual_cost"`  // user charge total USD
}

// usageRankingItemResponse carries payer identity and optional metrics
// permitted by ranking visibility settings.
type usageRankingItemResponse struct {
	Rank        int    `json:"rank"`         // one based payer position
	UserID      int64  `json:"user_id"`      // ranked payer user identifier
	DisplayName string `json:"display_name"` // public payer display name
	AvatarURL   string `json:"avatar_url"`   // public payer avatar URL
	// Requests is populated when ShowRequests is true. A new pointer retains
	// visible zero counts, while nil omits that metric from the encoded row.
	Requests *int64 `json:"requests,omitempty"` // optional visible request total
	// InputTokens, OutputTokens, CacheCreationTokens, CacheReadTokens and
	// TotalTokens are populated together when ShowTotalTokens is true. Separate
	// pointers retain visible zero values; nil leaves the hidden metric fields
	// omitted.
	InputTokens         *int64 `json:"input_tokens,omitempty"`          // optional input token total
	OutputTokens        *int64 `json:"output_tokens,omitempty"`         // optional output token total
	CacheCreationTokens *int64 `json:"cache_creation_tokens,omitempty"` // optional cache creation token total
	CacheReadTokens     *int64 `json:"cache_read_tokens,omitempty"`     // optional cache read token total
	TotalTokens         *int64 `json:"total_tokens,omitempty"`          // optional combined token total
	// ActualCost is populated when ShowActualCost is true. A new pointer retains
	// visible zero charges, while nil omits that metric from the encoded row.
	ActualCost *float64 `json:"actual_cost,omitempty"` // optional user charge total USD
}

// usageRankingResponse carries public payer rows, optional totals and applied
// ranking display metadata.
type usageRankingResponse struct {
	Ranking []usageRankingItemResponse `json:"ranking"` // ordered visible payer rows
	// TotalRequests, TotalTokens and TotalActualCost follow ShowRequests,
	// ShowTotalTokens and ShowActualCost respectively. Nil suppresses hidden
	// totals while a nonnil pointer retains each visible zero total during JSON
	// encoding.
	TotalRequests   *int64   `json:"total_requests,omitempty"`    // optional visible request total
	TotalTokens     *int64   `json:"total_tokens,omitempty"`      // optional visible token total
	TotalActualCost *float64 `json:"total_actual_cost,omitempty"` // optional visible user charge USD
	SortBy          string   `json:"sort_by"`                     // applied ranking order metric
	ShowTotalTokens bool     `json:"show_total_tokens"`           // token metric visibility flag
	ShowRequests    bool     `json:"show_requests"`               // request metric visibility flag
	ShowActualCost  bool     `json:"show_actual_cost"`            // charge metric visibility flag
	StartDate       string   `json:"start_date"`                  // formatted applied start date
	EndDate         string   `json:"end_date"`                    // formatted last included date
	Limit           int      `json:"limit"`                       // configured ranking row limit
}

// UsageHandler serves authenticated usage reports, rankings, key summaries,
// failed-request views, and background user export tasks.
type UsageHandler struct {
	idemhttp.Executor // user command idempotency helpers

	calendar      timezone.Calendar   // user query range calendar
	usageService  *usage.UsageService // usage record and report service
	apiKeyService ports.KeyReader     // key lookup and ownership reader
	// opsService supplies scoped failed-request reads. Nil makes the error
	// routes return unavailable after their server-side permission check.
	opsService ports.UserErrors // optional user failed request reader
	// settingService supplies ranking configuration and error-view permission.
	// Ranking needs a bound reader, while error endpoints reject access when
	// this reader is nil.
	settingService ports.Settings // ranking and error visibility reader
	// exportService manages persisted background CSV exports and download
	// tickets. Nil makes export routes return service unavailable while
	// leaving usage list and report routes operational.
	exportService *usage.UsageExportService // optional background export service
}

// BatchAPIKeysUsageRequest carries key identifiers submitted for the
// authenticated user's batch report.
type BatchAPIKeysUsageRequest struct {
	APIKeyIDs []int64 `json:"api_key_ids" binding:"required"` // submitted key identifier list
}

// usageRankingInt64Ptr returns independent pointer storage for a visible
// integer ranking metric.
func usageRankingInt64Ptr(value int64) *int64 {
	return &value
}

// usageRankingFloat64Ptr returns independent pointer storage for a visible
// floating-point ranking metric.
func usageRankingFloat64Ptr(value float64) *float64 {
	return &value
}

// projectUsageRankingResponse formats a nonnil ranking under the supplied
// visibility settings. It copies payer identity and initializes an empty row
// slice, then includes aggregate and row metrics whose visibility flags are
// true.
//
// Optional pointers distinguish visible zero values from hidden fields during
// JSON encoding. Token visibility controls all five token fields together,
// while requests and charged cost use separate flags. Every populated metric
// receives independent pointer storage; settings normalization belongs to the
// caller's settings reader.
func projectUsageRankingResponse(ranking *usage.UsageRankingResponse, settings usage.UsageRankingSettings) usageRankingResponse {
	result := usageRankingResponse{
		Ranking:         make([]usageRankingItemResponse, 0, len(ranking.Ranking)),
		SortBy:          string(settings.SortBy),
		ShowTotalTokens: settings.ShowTotalTokens,
		ShowRequests:    settings.ShowRequests,
		ShowActualCost:  settings.ShowActualCost,
	}
	if settings.ShowRequests {
		result.TotalRequests = usageRankingInt64Ptr(ranking.TotalRequests)
	}
	if settings.ShowTotalTokens {
		result.TotalTokens = usageRankingInt64Ptr(ranking.TotalTokens)
	}
	if settings.ShowActualCost {
		result.TotalActualCost = usageRankingFloat64Ptr(ranking.TotalActualCost)
	}
	for _, row := range ranking.Ranking {
		item := usageRankingItemResponse{
			Rank:        row.Rank,
			UserID:      row.UserID,
			DisplayName: row.DisplayName,
			AvatarURL:   row.AvatarURL,
		}
		if settings.ShowRequests {
			item.Requests = usageRankingInt64Ptr(row.Requests)
		}
		if settings.ShowTotalTokens {
			item.InputTokens = usageRankingInt64Ptr(row.InputTokens)
			item.OutputTokens = usageRankingInt64Ptr(row.OutputTokens)
			item.CacheCreationTokens = usageRankingInt64Ptr(row.CacheCreationTokens)
			item.CacheReadTokens = usageRankingInt64Ptr(row.CacheReadTokens)
			item.TotalTokens = usageRankingInt64Ptr(row.TotalTokens)
		}
		if settings.ShowActualCost {
			item.ActualCost = usageRankingFloat64Ptr(row.ActualCost)
		}
		result.Ranking = append(result.Ranking, item)
	}
	return result
}

// NewUsageHandler binds user report, key, error and settings readers with the
// query calendar.
func NewUsageHandler(
	usageService *usage.UsageService,
	apiKeyService ports.KeyReader,
	opsService ports.UserErrors,
	settingService ports.Settings,
	calendar timezone.Calendar,
) *UsageHandler {
	return &UsageHandler{
		calendar:       calendar,
		usageService:   usageService,
		apiKeyService:  apiKeyService,
		opsService:     opsService,
		settingService: settingService,
	}
}

// SetExportService attaches the background export service used by user export
// task endpoints and public signed-ticket downloads. Call during handler
// assembly before serving HTTP requests; nil leaves export routes unavailable.
func (h *UsageHandler) SetExportService(exportService *usage.UsageExportService) {
	if h == nil {
		return
	}
	h.exportService = exportService
}

// parseUserUsageFilters returns actor-scoped selectors and scalar chart
// bounds, writing an error response when validation fails. Authentication
// supplies UserID and IncludeOwnedTeam is true; key lookup checks existence
// while storage applies record visibility.
//
// Request category takes precedence over stream. Supplied dates form optional
// half-open filter bounds, with date-only ends advanced by calendar day. When
// requireRange is true and request ID is absent, omitted sides receive period
// or seven-date chart defaults.
//
// Request lookup keeps omitted filter time pointers nil for retained-history
// lists and totals, while scalar chart bounds become epoch through current
// UTC time. Explicit dates remain in both representations. A false result
// means callers stop because this helper already wrote the rejection.
func (h *UsageHandler) parseUserUsageFilters(c *gin.Context, requireRange bool) (*userUsageFilters, bool) {
	subject, ok := authctx.GetAuthSubjectFromContext(c)
	if !ok {
		response.Unauthorized(c, "User not authenticated")
		return nil, false
	}

	var apiKeyID int64
	if apiKeyIDStr := strings.TrimSpace(c.Query("api_key_id")); apiKeyIDStr != "" {
		id, err := strconv.ParseInt(apiKeyIDStr, 10, 64)
		if err != nil {
			response.BadRequest(c, "Invalid api_key_id")
			return nil, false
		}
		if h.apiKeyService == nil {
			response.InternalError(c, "API key service not available")
			return nil, false
		}
		_, err = h.apiKeyService.GetByID(c.Request.Context(), id)
		if err != nil {
			response.ErrorFrom(c, err)
			return nil, false
		}
		// Lookup establishes key existence. Repository actor and owned-team scope
		// still restricts records while owners select another member's team key for reports.
		apiKeyID = id
	}

	var groupID int64
	if groupIDStr := strings.TrimSpace(c.Query("group_id")); groupIDStr != "" {
		id, err := strconv.ParseInt(groupIDStr, 10, 64)
		if err != nil {
			response.BadRequest(c, "Invalid group_id")
			return nil, false
		}
		groupID = id
	}

	var requestType *int16
	var stream *bool
	var nativeCompactionV2 *bool
	if requestTypeStr := strings.TrimSpace(c.Query("request_type")); requestTypeStr != "" {
		parsed, err := usage.ParseUsageRequestType(requestTypeStr)
		if err != nil {
			response.BadRequest(c, err.Error())
			return nil, false
		}
		value := int16(parsed)
		requestType = &value
	} else if streamStr := strings.TrimSpace(c.Query("stream")); streamStr != "" {
		val, err := strconv.ParseBool(streamStr)
		if err != nil {
			response.BadRequest(c, "Invalid stream value, use true or false")
			return nil, false
		}
		stream = &val
	}
	if raw := strings.TrimSpace(c.Query("native_compaction_v2")); raw != "" {
		val, err := strconv.ParseBool(raw)
		if err != nil {
			response.BadRequest(c, "Invalid native_compaction_v2 value, use true or false")
			return nil, false
		}
		nativeCompactionV2 = &val
	}

	var billingType *int8
	if billingTypeStr := strings.TrimSpace(c.Query("billing_type")); billingTypeStr != "" {
		val, err := strconv.ParseInt(billingTypeStr, 10, 8)
		if err != nil {
			response.BadRequest(c, "Invalid billing_type")
			return nil, false
		}
		bt := int8(val)
		billingType = &bt
	}

	billingMode := strings.TrimSpace(c.Query("billing_mode"))
	if billingMode != "" && !pricing.BillingMode(billingMode).IsValidUsageFilter() {
		response.BadRequest(c, "Invalid billing_mode")
		return nil, false
	}

	userTZ := c.Query("timezone")
	now := h.calendar.NowInUserLocation(userTZ)
	var startTime, endTime time.Time
	var startPtr, endPtr *time.Time
	startDateStr := strings.TrimSpace(c.Query("start_date"))
	endDateStr := strings.TrimSpace(c.Query("end_date"))

	if startDateStr != "" {
		t, _, err := h.calendar.ParseDateTimeInUserLocation(startDateStr, userTZ)
		if err != nil {
			response.BadRequest(c, "Invalid start_date format, use YYYY-MM-DD or YYYY-MM-DDTHH:mm:ss")
			return nil, false
		}
		startTime = t
		startPtr = &startTime
	}
	if endDateStr != "" {
		t, dateOnly, err := h.calendar.ParseDateTimeInUserLocation(endDateStr, userTZ)
		if err != nil {
			response.BadRequest(c, "Invalid end_date format, use YYYY-MM-DD or YYYY-MM-DDTHH:mm:ss")
			return nil, false
		}
		if dateOnly {
			// Calendar expansion covers the selected local date across DST transitions
			// while retaining the repository's exclusive end comparison for these optional bounds.
			t = t.AddDate(0, 0, 1)
		}
		endTime = t
		endPtr = &endTime
	}

	// Chart callers need bounds, but request identity already selects retained
	// history. Fill ordinary defaults when that lookup intent was not supplied by a request ID.
	if requireRange && strings.TrimSpace(c.Query("request_id")) == "" {
		if startPtr == nil {
			switch c.DefaultQuery("period", "") {
			case "today":
				startTime = h.calendar.StartOfDayInUserLocation(now, userTZ)
			case "week":
				startTime = now.AddDate(0, 0, -7)
			case "month":
				startTime = now.AddDate(0, -1, 0)
			default:
				startTime = h.calendar.StartOfDayInUserLocation(now.AddDate(0, 0, -7), userTZ)
			}
			startPtr = &startTime
		}
		if endPtr == nil {
			if strings.TrimSpace(c.Query("period")) != "" {
				endTime = now
			} else {
				endTime = h.calendar.StartOfDayInUserLocation(now.AddDate(0, 0, 1), userTZ)
			}
			endPtr = &endTime
		}
	}

	// Omitted filter pointers keep lists and totals unrestricted. Scalar chart calls
	// still need epoch-to-now time arguments for the same request identity lookup.
	if strings.TrimSpace(c.Query("request_id")) != "" {
		if startPtr == nil {
			startTime = time.Unix(0, 0).UTC()
		} else {
			startTime = *startPtr
		}
		if endPtr == nil {
			endTime = time.Now().UTC()
		} else {
			endTime = *endPtr
		}
	}
	return &userUsageFilters{
		Filters: usage.UsageLogFilters{
			RequestID:          strings.TrimSpace(c.Query("request_id")),
			UserID:             subject.UserID,
			APIKeyID:           apiKeyID,
			GroupID:            groupID,
			IncludeOwnedTeam:   true,
			Model:              strings.TrimSpace(c.Query("model")),
			ModelFilterSource:  usage.ModelSourceRequested,
			RequestType:        requestType,
			Stream:             stream,
			NativeCompactionV2: nativeCompactionV2,
			BillingType:        billingType,
			BillingMode:        billingMode,
			StartTime:          startPtr,
			EndTime:            endPtr,
		},
		StartTime: startTime,
		EndTime:   endTime,
	}, true
}

// List returns an authenticated actor and owned-team usage page under
// optional filters. Missing dates remain unrestricted, sorting is passed to
// storage, and nested DTO display uses the request locale.
func (h *UsageHandler) List(c *gin.Context) {
	page, pageSize := response.ParsePagination(c)
	parsed, ok := h.parseUserUsageFilters(c, false)
	if !ok {
		return
	}

	params := pagination.PaginationParams{
		Page:      page,
		PageSize:  pageSize,
		SortBy:    c.DefaultQuery("sort_by", "created_at"),
		SortOrder: c.DefaultQuery("sort_order", "desc"),
	}

	records, result, err := h.usageService.ListWithFilters(c.Request.Context(), params, parsed.Filters)
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}

	out := make([]dto.UsageLog, 0, len(records))
	for i := range records {
		out = append(out, *dto.FromUsage(&records[i], locale.FromContext(c.Request.Context())))
	}
	response.Paginated(c, out, result.Total, page, pageSize)
}

// ListErrors returns redacted failed requests after server-side error-view
// permission is checked. It caps pages at one hundred, accepts date-only
// ranges and optional error selectors, and requires an available Ops reader.
func (h *UsageHandler) ListErrors(c *gin.Context) {
	subject, ok := authctx.GetAuthSubjectFromContext(c)
	if !ok {
		response.Unauthorized(c, "User not authenticated")
		return
	}

	// Check permission on each server request. A hidden browser entry cannot
	// authorize these scoped observations when user error visibility is disabled in settings.
	if h.settingService == nil || !h.settingService.IsUserErrorViewAllowed(c.Request.Context()) {
		response.Forbidden(c, "Error requests view is disabled")
		return
	}
	if h.opsService == nil {
		response.Error(c, http.StatusServiceUnavailable, "Ops service not available")
		return
	}

	page, pageSize := response.ParsePagination(c)
	// Bound the failed-request rows fetched for a single user page.
	if pageSize > 100 {
		pageSize = 100
	}

	filter := &ops.OpsErrorLogFilter{Page: page, PageSize: pageSize, RequestID: strings.TrimSpace(c.Query("request_id"))}

	userTZ := c.Query("timezone")
	if startDateStr := c.Query("start_date"); startDateStr != "" {
		t, err := h.calendar.ParseInUserLocation("2006-01-02", startDateStr, userTZ)
		if err != nil {
			response.BadRequest(c, "Invalid start_date format, use YYYY-MM-DD")
			return
		}
		filter.StartTime = &t
	}
	if endDateStr := c.Query("end_date"); endDateStr != "" {
		t, err := h.calendar.ParseInUserLocation("2006-01-02", endDateStr, userTZ)
		if err != nil {
			response.BadRequest(c, "Invalid end_date format, use YYYY-MM-DD")
			return
		}
		// Calendar expansion includes the selected error date across DST shifts while
		// matching the owning Ops reader's exclusive end comparison for these date-only bounds.
		t = t.AddDate(0, 0, 1)
		filter.EndTime = &t
	}

	filter.Model = strings.TrimSpace(c.Query("model"))

	if k := strings.TrimSpace(c.Query("api_key_id")); k != "" {
		n, err := strconv.ParseInt(k, 10, 64)
		if err != nil || n < 0 {
			response.BadRequest(c, "Invalid api_key_id")
			return
		}
		if n > 0 {
			filter.APIKeyID = &n
		}
	}

	if sc := strings.TrimSpace(c.Query("status_code")); sc != "" {
		n, err := strconv.Atoi(sc)
		if err != nil || n < 0 {
			response.BadRequest(c, "Invalid status_code")
			return
		}
		filter.StatusCodes = []int{n}
	}

	if cat := strings.TrimSpace(c.Query("category")); cat != "" {
		phases, types := ops.CategoryToFilter(cat)
		filter.ErrorPhasesAny = phases
		filter.ErrorTypesAny = types
	}

	// SetSort trims the requested strings. The PostgreSQL reader later chooses
	// a permitted column and direction before using them in this user-scoped
	// query.
	filter.SetSort(c.Query("sort_by"), c.Query("sort_order"))

	result, err := h.opsService.ListUserErrorRequests(c.Request.Context(), subject.UserID, filter)
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Paginated(c, result.Items, int64(result.Total), result.Page, result.PageSize)
}

// GetErrorDetail returns one redacted failure under the authenticated user
// scope. Server-side visibility is checked first, positive IDs are required,
// and an unavailable Ops reader returns a service-unavailable response.
func (h *UsageHandler) GetErrorDetail(c *gin.Context) {
	subject, ok := authctx.GetAuthSubjectFromContext(c)
	if !ok {
		response.Unauthorized(c, "User not authenticated")
		return
	}
	if h.settingService == nil || !h.settingService.IsUserErrorViewAllowed(c.Request.Context()) {
		response.Forbidden(c, "Error requests view is disabled")
		return
	}
	if h.opsService == nil {
		response.Error(c, http.StatusServiceUnavailable, "Ops service not available")
		return
	}
	id, err := strconv.ParseInt(strings.TrimSpace(c.Param("id")), 10, 64)
	if err != nil || id <= 0 {
		response.BadRequest(c, "Invalid id")
		return
	}
	detail, err := h.opsService.GetUserErrorRequestDetail(c.Request.Context(), subject.UserID, id)
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Success(c, detail)
}

// GetByID returns request metadata only when the record's acting user matches
// the requester. Lookup errors propagate through HTTP mapping; other actors
// receive forbidden even when broader reports include owned-team records.
func (h *UsageHandler) GetByID(c *gin.Context) {
	subject, ok := authctx.GetAuthSubjectFromContext(c)
	if !ok {
		response.Unauthorized(c, "User not authenticated")
		return
	}

	usageID, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil {
		response.BadRequest(c, "Invalid usage ID")
		return
	}

	record, err := h.usageService.GetByID(c.Request.Context(), usageID)
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}

	// Detail exposes request metadata. Require its recorded actor even when broader
	// actor-plus-owned-team reporting scope can include this row for another authenticated requester.
	if record.UserID != subject.UserID {
		response.Forbidden(c, "Not authorized to access this record")
		return
	}

	response.Success(c, dto.FromUsage(record, locale.FromContext(c.Request.Context())))
}

// Ranking returns payer rows using configured enablement, limit, order and
// metric visibility. It requires authentication, rejects disabled ranking
// before querying, and validates a strictly increasing range in the selected
// calendar location.
func (h *UsageHandler) Ranking(c *gin.Context) {
	if _, ok := authctx.GetAuthSubjectFromContext(c); !ok {
		response.Unauthorized(c, "User not authenticated")
		return
	}

	rankingSettings, err := h.settingService.GetUsageRankingSettings(c.Request.Context())
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	if !rankingSettings.Enabled {
		response.Forbidden(c, "Usage ranking is currently disabled")
		return
	}

	userTZ := c.Query("timezone")
	now := h.calendar.NowInUserLocation(userTZ)
	startTime, endTime, err := parseUsageRankingTimeRange(c, now, userTZ, h.calendar)
	if err != nil {
		response.BadRequest(c, err.Error())
		return
	}

	ranking, err := h.usageService.GetUsageRanking(c.Request.Context(), startTime, endTime, rankingSettings.Limit, rankingSettings.SortBy)
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	projected := projectUsageRankingResponse(ranking, rankingSettings)
	projected.StartDate = startTime.Format("2006-01-02")
	projected.EndDate = usageRankingDisplayEndDate(endTime)
	projected.Limit = rankingSettings.Limit

	response.Success(c, projected)
}

// parseUsageRankingTimeRange returns a strictly increasing ranking range with
// today-to-tomorrow defaults. Supplied sides replace defaults independently;
// date-only ends advance one local calendar date, while datetime bounds
// retain the parsed instant.
func parseUsageRankingTimeRange(c *gin.Context, now time.Time, userTZ string, calendar timezone.Calendar) (time.Time, time.Time, error) {
	startDateStr := c.Query("start_date")
	endDateStr := c.Query("end_date")
	defaultStart := calendar.StartOfDayInUserLocation(now, userTZ)
	defaultEnd := defaultStart.AddDate(0, 0, 1)
	if startDateStr == "" && endDateStr == "" {
		return defaultStart, defaultEnd, nil
	}

	startTime := defaultStart
	endTime := defaultEnd
	if startDateStr != "" {
		parsed, _, err := calendar.ParseDateTimeInUserLocation(startDateStr, userTZ)
		if err != nil {
			return time.Time{}, time.Time{}, errors.New("invalid start_date format, use YYYY-MM-DD or YYYY-MM-DDTHH:mm:ss")
		}
		startTime = parsed
	}
	if endDateStr != "" {
		parsed, dateOnly, err := calendar.ParseDateTimeInUserLocation(endDateStr, userTZ)
		if err != nil {
			return time.Time{}, time.Time{}, errors.New("invalid end_date format, use YYYY-MM-DD or YYYY-MM-DDTHH:mm:ss")
		}
		if dateOnly {
			// A date names a complete local day. Calendar expansion includes it across
			// DST shifts while explicitly timed end values retain their supplied instant.
			parsed = parsed.AddDate(0, 0, 1)
		}
		endTime = parsed
	}
	if !endTime.After(startTime) {
		return time.Time{}, time.Time{}, errors.New("end_date must be later than start_date")
	}
	return startTime, endTime, nil
}

// usageRankingDisplayEndDate formats the date containing the final included
// instant of an exclusive range. Subtracting one nanosecond lets midnight
// bounds display the preceding local date across DST transitions.
func usageRankingDisplayEndDate(endTime time.Time) string {
	return endTime.Add(-time.Nanosecond).Format("2006-01-02")
}

// Stats returns visible totals under authenticated actor and owned-team
// filters. It uses range defaults when needed, then clears provider cost and
// outbound endpoint breakdowns before encoding the user response.
func (h *UsageHandler) Stats(c *gin.Context) {
	parsed, ok := h.parseUserUsageFilters(c, true)
	if !ok {
		return
	}

	stats, err := h.usageService.GetUserStatsWithFilters(c.Request.Context(), parsed.Filters)
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	// User totals expose charges and inbound breakdowns. Remove provider cost and
	// outbound chart fields before the scoped statistics are encoded for this requester.
	stats.TotalProviderCost = nil
	stats.UpstreamEndpoints = nil
	stats.EndpointPaths = nil

	response.Success(c, stats)
}

// parseAPIKeyDailyUsageDays returns thirty for empty or whitespace-only text
// and otherwise parses the integer as supplied. Values outside one through
// ninety or text rejected by Atoi return a false validation result.
func parseAPIKeyDailyUsageDays(raw string) (int, bool) {
	if strings.TrimSpace(raw) == "" {
		return defaultAPIKeyDailyUsageDays, true
	}
	days, err := strconv.Atoi(raw)
	if err != nil || days <= 0 || days > maxAPIKeyDailyUsageDays {
		return 0, false
	}
	return days, true
}

// apiKeyDailyUsageRange returns calendar midnights covering days dates
// through today in the selected zone. Callers validate days first; an
// unavailable user zone uses the server calendar and the end is tomorrow
// midnight.
func apiKeyDailyUsageRange(days int, userTZ string, calendar timezone.Calendar) (time.Time, time.Time) {
	now := calendar.NowInUserLocation(userTZ)
	startTime := calendar.StartOfDayInUserLocation(now.AddDate(0, 0, -(days-1)), userTZ)
	endTime := calendar.StartOfDayInUserLocation(now.AddDate(0, 0, 1), userTZ)
	return startTime, endTime
}

// DashboardStats returns the authenticated user's dashboard counters through
// UsageService. Missing authentication rejects the read; storage determines
// retained totals, today's values, platform breakdowns and the current rate
// observations.
func (h *UsageHandler) DashboardStats(c *gin.Context) {
	subject, ok := authctx.GetAuthSubjectFromContext(c)
	if !ok {
		response.Unauthorized(c, "User not authenticated")
		return
	}

	stats, err := h.usageService.GetUserDashboardStats(c.Request.Context(), subject.UserID)
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}

	response.Success(c, stats)
}

// DashboardTrend returns usage buckets under authenticated actor and
// owned-team filters. It supplies scalar chart bounds from
// parseUserUsageFilters, passes granularity to storage, and displays the
// range's final included date.
func (h *UsageHandler) DashboardTrend(c *gin.Context) {
	parsed, ok := h.parseUserUsageFilters(c, true)
	if !ok {
		return
	}
	granularity := c.DefaultQuery("granularity", "day")

	trend, err := h.usageService.GetUsageTrendWithFilters(c.Request.Context(), parsed.StartTime, parsed.EndTime, granularity, parsed.Filters)
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}

	response.Success(c, gin.H{
		"trend":       trend,
		"start_date":  parsed.StartTime.Format("2006-01-02"),
		"end_date":    usageFilterDisplayEndDate(parsed.EndTime),
		"granularity": granularity,
	})
}

// DashboardModels returns internal requested-model grouping within
// authenticated visibility and filters. Empty or requested model_source is
// accepted, other sources are rejected, and userModelStatsFromUsageStats
// formats the permitted metric fields.
func (h *UsageHandler) DashboardModels(c *gin.Context) {
	parsed, ok := h.parseUserUsageFilters(c, true)
	if !ok {
		return
	}

	modelSource := strings.TrimSpace(c.Query("model_source"))
	if modelSource != "" && modelSource != usage.ModelSourceRequested {
		response.BadRequest(c, "Invalid model_source, user usage only supports requested")
		return
	}

	stats, err := h.usageService.GetModelStatsWithFiltersBySource(c.Request.Context(), parsed.StartTime, parsed.EndTime, parsed.Filters, usage.ModelSourceRequested)
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}

	response.Success(c, gin.H{
		"models":     userModelStatsFromUsageStats(stats),
		"start_date": parsed.StartTime.Format("2006-01-02"),
		"end_date":   usageFilterDisplayEndDate(parsed.EndTime),
	})
}

// DashboardSnapshotV2 returns enabled user chart sections under one parsed
// scope and range. Inclusion flags use strict boolean parsing, granularity
// selects hour or day, and any enabled-query failure rejects the combined
// response.
func (h *UsageHandler) DashboardSnapshotV2(c *gin.Context) {
	parsed, ok := h.parseUserUsageFilters(c, true)
	if !ok {
		return
	}

	granularity := strings.TrimSpace(c.DefaultQuery("granularity", "day"))
	if granularity != "hour" {
		granularity = "day"
	}
	includeTrend, ok := parseBoolQueryWithDefault(c, "include_trend", true)
	if !ok {
		return
	}
	includeModels, ok := parseBoolQueryWithDefault(c, "include_model_stats", true)
	if !ok {
		return
	}
	includeGroups, ok := parseBoolQueryWithDefault(c, "include_group_stats", false)
	if !ok {
		return
	}

	resp := gin.H{
		"generated_at": time.Now().UTC().Format(time.RFC3339),
		"start_date":   parsed.StartTime.Format("2006-01-02"),
		"end_date":     usageFilterDisplayEndDate(parsed.EndTime),
		"granularity":  granularity,
	}

	if includeTrend {
		trend, err := h.usageService.GetUsageTrendWithFilters(c.Request.Context(), parsed.StartTime, parsed.EndTime, granularity, parsed.Filters)
		if err != nil {
			response.ErrorFrom(c, err)
			return
		}
		resp["trend"] = trend
	}
	if includeModels {
		models, err := h.usageService.GetModelStatsWithFiltersBySource(c.Request.Context(), parsed.StartTime, parsed.EndTime, parsed.Filters, usage.ModelSourceRequested)
		if err != nil {
			response.ErrorFrom(c, err)
			return
		}
		resp["models"] = userModelStatsFromUsageStats(models)
	}
	if includeGroups {
		groups, err := h.usageService.GetGroupStatsWithFilters(c.Request.Context(), parsed.StartTime, parsed.EndTime, parsed.Filters)
		if err != nil {
			response.ErrorFrom(c, err)
			return
		}
		resp["groups"] = userGroupStatsFromUsageStats(groups)
	}

	response.Success(c, resp)
}

// userModelStatsFromUsageStats copies permitted model metrics into the user
// response slice. Nil input produces an allocated empty result, and provider
// costs have no field in this returned response type.
func userModelStatsFromUsageStats(stats []usage.ModelStat) []userModelStat {
	out := make([]userModelStat, 0, len(stats))
	for _, stat := range stats {
		out = append(out, userModelStat{
			Model:               stat.Model,
			Requests:            stat.Requests,
			InputTokens:         stat.InputTokens,
			OutputTokens:        stat.OutputTokens,
			CacheCreationTokens: stat.CacheCreationTokens,
			CacheReadTokens:     stat.CacheReadTokens,
			TotalTokens:         stat.TotalTokens,
			Cost:                stat.Cost,
			ActualCost:          stat.ActualCost,
		})
	}
	return out
}

// userGroupStatsFromUsageStats copies permitted group metrics into the user
// response slice. Nil input produces an allocated empty result, and provider
// costs have no field in this returned response type.
func userGroupStatsFromUsageStats(stats []usage.GroupStat) []userGroupStat {
	out := make([]userGroupStat, 0, len(stats))
	for _, stat := range stats {
		out = append(out, userGroupStat{
			GroupID:     stat.GroupID,
			GroupName:   stat.GroupName,
			Requests:    stat.Requests,
			TotalTokens: stat.TotalTokens,
			Cost:        stat.Cost,
			ActualCost:  stat.ActualCost,
		})
	}
	return out
}

// parseBoolQueryWithDefault returns fallback for empty or whitespace-only
// query text and otherwise parses raw boolean text. Invalid values write bad
// request; callers stop when the second result is false.
func parseBoolQueryWithDefault(c *gin.Context, key string, fallback bool) (bool, bool) {
	raw := c.Query(key)
	if strings.TrimSpace(raw) == "" {
		return fallback, true
	}
	parsed, err := strconv.ParseBool(raw)
	if err != nil {
		response.BadRequest(c, "Invalid "+key+" value, use true or false")
		return false, false
	}
	return parsed, true
}

// usageFilterDisplayEndDate formats the final included date from an exclusive
// end. Zero returns empty text; other bounds subtract one nanosecond before
// formatting in their retained location.
func usageFilterDisplayEndDate(endTime time.Time) string {
	if endTime.IsZero() {
		return ""
	}
	return endTime.Add(-time.Nanosecond).Format("2006-01-02")
}

// DashboardAPIKeysUsage queries charges for the authenticated actor's owned
// IDs from a bounded list. More than one hundred inputs is rejected, empty
// owned subsets return empty stats, and storage receives zero range defaults.
func (h *UsageHandler) DashboardAPIKeysUsage(c *gin.Context) {
	subject, ok := authctx.GetAuthSubjectFromContext(c)
	if !ok {
		response.Unauthorized(c, "User not authenticated")
		return
	}

	var req BatchAPIKeysUsageRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, "Invalid request: "+err.Error())
		return
	}

	if len(req.APIKeyIDs) == 0 {
		response.Success(c, gin.H{"stats": map[string]any{}})
		return
	}

	// Bound batch size to limit query parameters and work per request.
	if len(req.APIKeyIDs) > 100 {
		response.BadRequest(c, "Too many API key IDs (maximum 100 allowed)")
		return
	}

	validAPIKeyIDs, err := h.apiKeyService.VerifyOwnership(c.Request.Context(), subject.UserID, req.APIKeyIDs)
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}

	if len(validAPIKeyIDs) == 0 {
		response.Success(c, gin.H{"stats": map[string]any{}})
		return
	}

	stats, err := h.usageService.GetBatchAPIKeyUsageStats(c.Request.Context(), validAPIKeyIDs, time.Time{}, time.Time{})
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}

	response.Success(c, gin.H{"stats": stats})
}

// GetMyAPIKeyDailyUsage returns daily usage after key lookup proves actor
// ownership. It validates one through ninety days, requires a key reader, and
// queries local-calendar bounds using the authenticated user and requested
// key.
func (h *UsageHandler) GetMyAPIKeyDailyUsage(c *gin.Context) {
	subject, ok := authctx.GetAuthSubjectFromContext(c)
	if !ok {
		response.Unauthorized(c, "User not authenticated")
		return
	}

	apiKeyID, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil {
		response.BadRequest(c, "Invalid API key ID")
		return
	}

	days, ok := parseAPIKeyDailyUsageDays(c.DefaultQuery("days", ""))
	if !ok {
		response.BadRequest(c, "Invalid days, allowed range is 1-90")
		return
	}

	if h.apiKeyService == nil {
		response.InternalError(c, "API key service is not configured")
		return
	}

	apiKey, err := h.apiKeyService.GetByID(c.Request.Context(), apiKeyID)
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	if apiKey.UserID != subject.UserID {
		response.Forbidden(c, "Not authorized to access this API key's usage")
		return
	}

	userTZ := c.Query("timezone")
	startTime, endTime := apiKeyDailyUsageRange(days, userTZ, h.calendar)
	items, err := h.usageService.GetAPIKeyDailyUsage(c.Request.Context(), subject.UserID, apiKeyID, startTime, endTime)
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}

	response.Success(c, gin.H{
		"items":      items,
		"days":       days,
		"start_date": startTime.Format("2006-01-02"),
		"end_date":   endTime.AddDate(0, 0, -1).Format("2006-01-02"),
	})
}

// ParseAPIKeyDailyUsageDays exposes user daily-range validation to the public
// usage endpoint. Blank input selects thirty days, while unsupported integers
// or text return false so that endpoint can reject the request.
func ParseAPIKeyDailyUsageDays(raw string) (int, bool) { return parseAPIKeyDailyUsageDays(raw) }
