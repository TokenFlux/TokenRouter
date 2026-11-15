package httpapi

import (
	"context"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/TokenFlux/TokenRouter/internal/billing/pricing"
	"github.com/TokenFlux/TokenRouter/internal/identity/httpapi/authctx"
	infraerrors "github.com/TokenFlux/TokenRouter/internal/pkg/apperror"
	"github.com/TokenFlux/TokenRouter/internal/pkg/locale"
	"github.com/TokenFlux/TokenRouter/internal/pkg/pagination"
	response "github.com/TokenFlux/TokenRouter/internal/server/httpx"
	"github.com/TokenFlux/TokenRouter/internal/usage"
	"github.com/TokenFlux/TokenRouter/internal/usage/httpapi/dto"
)

// export_handler.go serves authenticated user usage-export task endpoints and
// public signed-ticket CSV downloads.
// Regular users create, inspect, cancel, and download background CSV exports
// bounded by explicit half-open timestamps and validated IANA timezones. Task
// creation runs through the user idempotency executor and verifies optional API
// key existence before delegating ownership pinning and active-task admission
// limits to UsageExportService.
//
// Browser clients exchange an authenticated POST request for a 60-second signed
// download ticket and navigate directly to the streaming download route so
// multi-megabyte CSV files never buffer inside JavaScript memory. Direct
// bearer-authenticated downloads share the same streaming response writer and
// Content-Disposition sanitization for API callers.

// Download path and timestamp format constants standardize ticket URL building
// and local second-precision timestamp parsing across export HTTP handlers.
const (
	exportDownloadPathPrefix = "/api/v1/usage/export-downloads/" // relative public signed-ticket download prefix
	exportLocalISOLayout     = "2006-01-02T15:04:05"             // offset-free ISO local second layout
	exportLocalSpaceLayout   = "2006-01-02 15:04:05"             // offset-free space-separated second layout
)

// CreateUsageExportTaskRequest carries second-precision half-open time bounds,
// an explicit IANA timezone, optional locale, and user usage filters for task creation.
type CreateUsageExportTaskRequest struct {
	// StartTime and EndTime accept RFC3339 timestamps or offset-free
	// YYYY-MM-DDTHH:mm:ss text interpreted in Timezone as [StartTime, EndTime).
	StartTime string `json:"start_time"`       // required inclusive start timestamp
	EndTime   string `json:"end_time"`         // required exclusive end timestamp
	Timezone  string `json:"timezone"`         // required IANA timezone identifier
	Locale    string `json:"locale,omitempty"` // optional zh or en CSV header locale

	APIKeyID     *int64  `json:"api_key_id,omitempty"`    // optional API key selector
	GroupID      *int64  `json:"group_id,omitempty"`      // optional routed group selector
	TeamID       *int64  `json:"team_id,omitempty"`       // optional team selector
	PersonalOnly bool    `json:"personal_only,omitempty"` // restrict to personal non-team usage
	RequestID    string  `json:"request_id,omitempty"`    // optional local or upstream request ID
	Model        *string `json:"model,omitempty"`         // optional requested model selector
	// RequestType takes precedence when supplied; parsing converts the category
	// label and clears Stream before forwarding filters to UsageExportService.
	RequestType        *string `json:"request_type,omitempty"`         // optional request category label
	Stream             *bool   `json:"stream,omitempty"`               // optional legacy stream transport flag
	NativeCompactionV2 *bool   `json:"native_compaction_v2,omitempty"` // optional remote compaction v2 flag
	BillingType        *int8   `json:"billing_type,omitempty"`         // optional funding category selector
	BillingMode        *string `json:"billing_mode,omitempty"`         // optional pricing mode selector
}

// ListExportTasks returns a paginated list of background export tasks owned by
// the authenticated user, ordered newest first, along with scope-wide active IDs
// and optional off-page watch_ids snapshots.
func (h *UsageHandler) ListExportTasks(c *gin.Context) {
	if h.exportService == nil || !h.exportService.IsEnabled() {
		WriteExportDisabledResponse(c)
		return
	}
	subject, ok := authctx.GetAuthSubjectFromContext(c)
	if !ok || subject.UserID <= 0 {
		response.Unauthorized(c, "User not authenticated")
		return
	}

	watchIDs, ok := ParseExportWatchIDsParam(c)
	if !ok {
		return
	}
	page, pageSize := response.ParsePagination(c)
	params := pagination.PaginationParams{Page: page, PageSize: pageSize}
	result, err := h.exportService.ListUserTasks(c.Request.Context(), subject.UserID, params, watchIDs...)
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}

	response.Success(c, dto.ExportPageFromUsage(result, page, pageSize))
}

// CreateExportTask validates explicit half-open timestamps, IANA timezone, and
// optional user usage selectors before creating a persisted user export task
// through the user idempotency executor.
//
// Optional API key filters verify key existence through apiKeyService while
// UsageExportService pins UserID and owned-team scope on the persisted filter
// snapshot. Replayed idempotency keys return the previously recorded task DTO.
func (h *UsageHandler) CreateExportTask(c *gin.Context) {
	if h.exportService == nil || !h.exportService.IsEnabled() {
		WriteExportDisabledResponse(c)
		return
	}
	subject, ok := authctx.GetAuthSubjectFromContext(c)
	if !ok || subject.UserID <= 0 {
		response.Unauthorized(c, "User not authenticated")
		return
	}

	var req CreateUsageExportTaskRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, "Invalid request: "+err.Error())
		return
	}

	loc, canonicalTZ, err := usage.LoadUsageExportLocation(req.Timezone)
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	startTime, err := ParseExportTimeBound(req.StartTime, loc)
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	endTime, err := ParseExportTimeBound(req.EndTime, loc)
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	if !endTime.After(startTime) {
		response.ErrorFrom(c, infraerrors.BadRequest("INVALID_TIME_RANGE", "end_time must be after start_time"))
		return
	}

	if req.APIKeyID != nil && *req.APIKeyID > 0 {
		if h.apiKeyService == nil {
			response.InternalError(c, "API key service not available")
			return
		}
		if _, err := h.apiKeyService.GetByID(c.Request.Context(), *req.APIKeyID); err != nil {
			response.ErrorFrom(c, err)
			return
		}
	}

	requestType, stream, ok := ParseOptionalExportRequestType(c, req.RequestType, req.Stream)
	if !ok {
		return
	}
	billingMode, ok := ParseOptionalExportBillingMode(c, req.BillingMode)
	if !ok {
		return
	}

	req.StartTime = strings.TrimSpace(req.StartTime)
	req.EndTime = strings.TrimSpace(req.EndTime)
	req.Timezone = canonicalTZ
	resolvedLocale := ResolveExportRequestLocale(c, req.Locale)

	filters := usage.UsageExportFilters{
		StartTime:          startTime,
		EndTime:            endTime,
		Timezone:           canonicalTZ,
		Locale:             resolvedLocale,
		APIKeyID:           req.APIKeyID,
		GroupID:            req.GroupID,
		TeamID:             req.TeamID,
		PersonalOnly:       req.PersonalOnly,
		RequestID:          strings.TrimSpace(req.RequestID),
		Model:              req.Model,
		ModelFilterSource:  usage.ModelSourceRequested,
		RequestType:        requestType,
		Stream:             stream,
		NativeCompactionV2: req.NativeCompactionV2,
		BillingType:        req.BillingType,
		BillingMode:        billingMode,
	}

	idempotencyPayload := struct {
		UserID int64                        `json:"user_id"` // authenticated creator user identifier
		Body   CreateUsageExportTaskRequest `json:"body"`    // normalized export creation payload
	}{
		UserID: subject.UserID,
		Body:   req,
	}
	h.ExecuteUserIdempotentJSON(c, "user.usage.export_tasks.create", idempotencyPayload, h.DefaultWriteIdempotencyTTL(), func(ctx context.Context) (any, error) {
		task, createErr := h.exportService.CreateUserTask(ctx, subject.UserID, filters)
		if createErr != nil {
			return nil, createErr
		}
		return dto.ExportFromUsage(task), nil
	})
}

// CancelExportTask cancels a pending or running user export task owned by the
// authenticated requester and returns the updated task DTO.
func (h *UsageHandler) CancelExportTask(c *gin.Context) {
	if h.exportService == nil || !h.exportService.IsEnabled() {
		WriteExportDisabledResponse(c)
		return
	}
	subject, ok := authctx.GetAuthSubjectFromContext(c)
	if !ok || subject.UserID <= 0 {
		response.Unauthorized(c, "User not authenticated")
		return
	}
	taskID, ok := ParseExportTaskIDParam(c)
	if !ok {
		return
	}

	task, err := h.exportService.CancelUserTask(c.Request.Context(), subject.UserID, taskID)
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Success(c, dto.ExportFromUsage(task))
}

// CreateExportDownloadTicket issues a 60-second HMAC-signed download ticket and
// relative streaming URL for a succeeded user export task owned by the caller.
func (h *UsageHandler) CreateExportDownloadTicket(c *gin.Context) {
	if h.exportService == nil || !h.exportService.IsEnabled() {
		WriteExportDisabledResponse(c)
		return
	}
	subject, ok := authctx.GetAuthSubjectFromContext(c)
	if !ok || subject.UserID <= 0 {
		response.Unauthorized(c, "User not authenticated")
		return
	}
	taskID, ok := ParseExportTaskIDParam(c)
	if !ok {
		return
	}

	ticket, expiresAt, err := h.exportService.IssueUserDownloadTicket(c.Request.Context(), subject.UserID, taskID)
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Success(c, BuildExportDownloadTicketResponse(ticket, expiresAt))
}

// DownloadExportTask streams a succeeded user CSV artifact directly to an
// authenticated API client using bearer credentials.
func (h *UsageHandler) DownloadExportTask(c *gin.Context) {
	if h.exportService == nil || !h.exportService.IsEnabled() {
		WriteExportDisabledResponse(c)
		return
	}
	subject, ok := authctx.GetAuthSubjectFromContext(c)
	if !ok || subject.UserID <= 0 {
		response.Unauthorized(c, "User not authenticated")
		return
	}
	taskID, ok := ParseExportTaskIDParam(c)
	if !ok {
		return
	}

	stream, err := h.exportService.OpenUserDownload(c.Request.Context(), subject.UserID, taskID)
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	WriteCSVDownloadStream(c, stream)
}

// DownloadExportByTicket verifies a short-lived signed browser download ticket
// and streams the corresponding user or administrator CSV artifact from disk.
// Invalid or expired tickets return 401, while expired export files return 410.
func (h *UsageHandler) DownloadExportByTicket(c *gin.Context) {
	if h.exportService == nil || !h.exportService.IsEnabled() {
		WriteExportDisabledResponse(c)
		return
	}
	ticket := strings.TrimSpace(c.Param("ticket"))
	stream, err := h.exportService.OpenDownloadByTicket(c.Request.Context(), ticket)
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	WriteCSVDownloadStream(c, stream)
}

// ParseExportTimeBound parses an export start or end timestamp as either an
// explicit RFC3339 instant or an offset-free second-precision local timestamp
// in loc. Blank or malformed text returns a 400 INVALID_TIME_RANGE error.
func ParseExportTimeBound(raw string, loc *time.Location) (time.Time, error) {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return time.Time{}, infraerrors.BadRequest("INVALID_TIME_RANGE", "start_time and end_time are required")
	}
	if parsed, err := time.Parse(time.RFC3339Nano, trimmed); err == nil {
		return parsed, nil
	}
	if loc == nil {
		loc = time.UTC
	}
	if parsed, err := time.ParseInLocation(exportLocalISOLayout, trimmed, loc); err == nil {
		return parsed, nil
	}
	if parsed, err := time.ParseInLocation(exportLocalSpaceLayout, trimmed, loc); err == nil {
		return parsed, nil
	}
	return time.Time{}, infraerrors.BadRequest("INVALID_TIME_RANGE", "invalid timestamp format, use RFC3339 or YYYY-MM-DDTHH:mm:ss")
}

// ResolveExportRequestLocale selects the requested export locale when provided,
// otherwise falling back to the request context locale and Accept-Language header.
func ResolveExportRequestLocale(c *gin.Context, requested string) string {
	if trimmed := strings.TrimSpace(requested); trimmed != "" {
		return usage.NormalizeUsageExportLocale(trimmed)
	}
	if c != nil && c.Request != nil {
		if ctxLocale := strings.TrimSpace(locale.FromContext(c.Request.Context())); ctxLocale != "" {
			return usage.NormalizeUsageExportLocale(ctxLocale)
		}
		negotiated := locale.Negotiate(c.GetHeader("Accept-Language"), locale.Default())
		return usage.NormalizeUsageExportLocale(negotiated)
	}
	return usage.UsageExportLocaleZH
}

// BuildExportDownloadTicketResponse wraps a signed download ticket and expiry
// timestamp with its canonical public download path.
func BuildExportDownloadTicketResponse(ticket string, expiresAt time.Time) dto.UsageExportDownloadTicket {
	return dto.UsageExportDownloadTicket{
		Ticket:      ticket,
		DownloadURL: exportDownloadPathPrefix + url.PathEscape(ticket),
		ExpiresAt:   expiresAt.UTC(),
	}
}

// WriteCSVDownloadStream streams an opened CSV export artifact with no-store
// caching, Content-Type nosniff protection, and sanitized attachment headers,
// closing stream.Reader before returning.
func WriteCSVDownloadStream(c *gin.Context, stream *usage.UsageExportDownloadStream) {
	if stream == nil || stream.Reader == nil {
		response.InternalError(c, "Export artifact stream unavailable")
		return
	}
	defer func() { _ = stream.Reader.Close() }()

	// Use unknown content length (-1) when metadata reports zero so Gin copies all
	// bytes with io.Copy instead of truncating output via io.CopyN(..., 0).
	contentLength := stream.FileSizeBytes
	if contentLength <= 0 {
		contentLength = -1
	}
	extraHeaders := map[string]string{
		"Content-Disposition":    formatCSVAttachmentDisposition(stream.FileName),
		"Cache-Control":          "no-store",
		"X-Content-Type-Options": "nosniff",
	}
	c.DataFromReader(http.StatusOK, contentLength, "text/csv; charset=utf-8", stream.Reader, extraHeaders)
}

// WriteExportDisabledResponse writes the structured HTTP 503 USAGE_EXPORT_DISABLED
// error response when background exports are unconfigured or turned off.
func WriteExportDisabledResponse(c *gin.Context) {
	response.ErrorFrom(c, infraerrors.ServiceUnavailable("USAGE_EXPORT_DISABLED", "usage export is disabled"))
}

// ParseExportWatchIDsParam parses an optional comma-separated watch_ids query
// string into positive int64 task identifiers, writing a 400 response when any
// non-empty token is malformed.
func ParseExportWatchIDsParam(c *gin.Context) ([]int64, bool) {
	raw := strings.TrimSpace(c.Query("watch_ids"))
	if raw == "" {
		return nil, true
	}
	parts := strings.Split(raw, ",")
	ids := make([]int64, 0, len(parts))
	for _, part := range parts {
		token := strings.TrimSpace(part)
		if token == "" {
			continue
		}
		id, err := strconv.ParseInt(token, 10, 64)
		if err != nil || id <= 0 {
			response.BadRequest(c, "Invalid watch_ids")
			return nil, false
		}
		ids = append(ids, id)
	}
	return ids, true
}

// ParseExportTaskIDParam extracts and validates a positive integer :id path
// parameter, writing a 400 Bad Request response when parsing fails.
func ParseExportTaskIDParam(c *gin.Context) (int64, bool) {
	idStr := strings.TrimSpace(c.Param("id"))
	taskID, err := strconv.ParseInt(idStr, 10, 64)
	if err != nil || taskID <= 0 {
		response.BadRequest(c, "Invalid task id")
		return 0, false
	}
	return taskID, true
}

// ParseOptionalExportRequestType validates an optional request_type label and
// clears stream when a valid category is supplied. Invalid labels write a 400
// response and return false.
func ParseOptionalExportRequestType(c *gin.Context, rawRequestType *string, stream *bool) (*int16, *bool, bool) {
	if rawRequestType == nil || strings.TrimSpace(*rawRequestType) == "" {
		return nil, stream, true
	}
	parsed, err := usage.ParseUsageRequestType(strings.TrimSpace(*rawRequestType))
	if err != nil {
		response.BadRequest(c, err.Error())
		return nil, nil, false
	}
	value := int16(parsed)
	return &value, nil, true
}

// ParseOptionalExportBillingMode validates an optional billing_mode filter
// against supported usage modes, writing a 400 response when unrecognized.
func ParseOptionalExportBillingMode(c *gin.Context, rawBillingMode *string) (*string, bool) {
	if rawBillingMode == nil || strings.TrimSpace(*rawBillingMode) == "" {
		return nil, true
	}
	trimmed := strings.ToLower(strings.TrimSpace(*rawBillingMode))
	if !pricing.BillingMode(trimmed).IsValidUsageFilter() {
		response.BadRequest(c, "Invalid billing_mode")
		return nil, false
	}
	return &trimmed, true
}

// formatCSVAttachmentDisposition builds an injection-safe Content-Disposition
// attachment header with both quoted ASCII fallback and RFC 5987 UTF-8 encoding.
func formatCSVAttachmentDisposition(fileName string) string {
	trimmed := strings.TrimSpace(fileName)
	if trimmed == "" {
		trimmed = "usage_export.csv"
	}
	var ascii strings.Builder
	ascii.Grow(len(trimmed))
	for _, r := range trimmed {
		switch {
		case r < 0x20 || r == 0x7f || r == '"' || r == '\\' || r == ';' || r == '/' || r > 0x7e:
			_ = ascii.WriteByte('_')
		default:
			_, _ = ascii.WriteRune(r)
		}
	}
	fallback := ascii.String()
	if fallback == "" {
		fallback = "usage_export.csv"
	}
	return `attachment; filename="` + fallback + `"; filename*=UTF-8''` + url.PathEscape(trimmed)
}
