package admin

import (
	"context"
	"strings"

	"github.com/gin-gonic/gin"

	"github.com/TokenFlux/TokenRouter/internal/identity/httpapi/authctx"
	infraerrors "github.com/TokenFlux/TokenRouter/internal/pkg/apperror"
	"github.com/TokenFlux/TokenRouter/internal/pkg/pagination"
	response "github.com/TokenFlux/TokenRouter/internal/server/httpx"
	"github.com/TokenFlux/TokenRouter/internal/usage"
	usagehttp "github.com/TokenFlux/TokenRouter/internal/usage/httpapi"
	"github.com/TokenFlux/TokenRouter/internal/usage/httpapi/dto"
)

// export_handler.go serves administrator usage-export task commands, progress
// queries, signed download ticket exchange, and streaming CSV downloads.
// Administrators submit background CSV exports bounded by explicit half-open
// timestamps and validated IANA timezones across optional user, provider, API
// key, group, team, model, request-source, and billing dimensions. Creation
// executes through the administrator idempotency executor before delegating
// active-task quota enforcement and worker wakeup to UsageExportService.
//
// Browser clients exchange an authenticated POST request for a short-lived
// signed download ticket targeting the shared streaming download route, while
// API and MCP callers stream the committed CSV directly over the protected
// administrator download endpoint without buffering large artifacts in memory.

// CreateAdminUsageExportTaskRequest carries second-precision half-open time
// bounds, an explicit IANA timezone, optional locale, and administrator usage
// selectors for persisted export task creation.
type CreateAdminUsageExportTaskRequest struct {
	// StartTime and EndTime accept RFC3339 timestamps or offset-free
	// YYYY-MM-DDTHH:mm:ss text interpreted in Timezone as [StartTime, EndTime).
	StartTime     string `json:"start_time"`               // required inclusive start timestamp
	EndTime       string `json:"end_time"`                 // required exclusive end timestamp
	Timezone      string `json:"timezone"`                 // required IANA timezone identifier
	Locale        string `json:"locale,omitempty"`         // optional zh or en CSV header locale
	RequestSource string `json:"request_source,omitempty"` // optional customer or system source selector

	UserID       *int64  `json:"user_id,omitempty"`       // optional acting user selector
	APIKeyID     *int64  `json:"api_key_id,omitempty"`    // optional API key selector
	ProviderID   *int64  `json:"provider_id,omitempty"`   // optional upstream provider selector
	GroupID      *int64  `json:"group_id,omitempty"`      // optional routed group selector
	TeamID       *int64  `json:"team_id,omitempty"`       // optional team selector
	PersonalOnly bool    `json:"personal_only,omitempty"` // restrict to personal non-team usage
	RequestID    string  `json:"request_id,omitempty"`    // optional local or upstream request ID
	Model        *string `json:"model,omitempty"`         // optional model selector
	// ModelFilterSource selects requested, upstream, or mapping model matching
	// when Model is provided; blank defaults to requested.
	ModelFilterSource string `json:"model_filter_source,omitempty"` // optional model matching dimension
	// RequestType takes precedence when supplied; parsing converts the category
	// label and clears Stream before forwarding filters to UsageExportService.
	RequestType        *string `json:"request_type,omitempty"`         // optional request category label
	Stream             *bool   `json:"stream,omitempty"`               // optional legacy stream transport flag
	NativeCompactionV2 *bool   `json:"native_compaction_v2,omitempty"` // optional remote compaction v2 flag
	BillingType        *int8   `json:"billing_type,omitempty"`         // optional funding category selector
	BillingMode        *string `json:"billing_mode,omitempty"`         // optional pricing mode selector
}

// ListExportTasks returns a paginated list of administrator-scoped background
// CSV export tasks ordered newest first along with scope-wide active IDs and
// optional off-page watch_ids snapshots.
func (h *UsageHandler) ListExportTasks(c *gin.Context) {
	if h.exportService == nil || !h.exportService.IsEnabled() {
		usagehttp.WriteExportDisabledResponse(c)
		return
	}

	watchIDs, ok := usagehttp.ParseExportWatchIDsParam(c)
	if !ok {
		return
	}
	page, pageSize := response.ParsePagination(c)
	params := pagination.PaginationParams{Page: page, PageSize: pageSize}
	result, err := h.exportService.ListAdminTasks(c.Request.Context(), params, watchIDs...)
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}

	response.Success(c, dto.ExportPageFromUsage(result, page, pageSize))
}

// CreateExportTask validates explicit half-open timestamps, IANA timezone, and
// administrator usage selectors before creating a persisted administrator export
// task through the administrator idempotency executor.
//
// Replayed idempotency keys reuse the previously recorded task response, while
// per-administrator active task quotas reject excess concurrent exports with 429.
func (h *UsageHandler) CreateExportTask(c *gin.Context) {
	if h.exportService == nil || !h.exportService.IsEnabled() {
		usagehttp.WriteExportDisabledResponse(c)
		return
	}
	subject, ok := authctx.GetAuthSubjectFromContext(c)
	if !ok || subject.UserID <= 0 {
		response.Unauthorized(c, "Unauthorized")
		return
	}

	var req CreateAdminUsageExportTaskRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, "Invalid request: "+err.Error())
		return
	}

	req.RequestSource = strings.TrimSpace(req.RequestSource)
	if !usage.ValidRequestSource(req.RequestSource) {
		response.BadRequest(c, "Invalid request_source")
		return
	}

	modelSource := strings.ToLower(strings.TrimSpace(req.ModelFilterSource))
	if modelSource == "" {
		modelSource = usage.ModelSourceRequested
	} else if !usage.IsValidModelSource(modelSource) {
		response.BadRequest(c, "Invalid model_filter_source")
		return
	}

	loc, canonicalTZ, err := usage.LoadUsageExportLocation(req.Timezone)
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	startTime, err := usagehttp.ParseExportTimeBound(req.StartTime, loc)
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	endTime, err := usagehttp.ParseExportTimeBound(req.EndTime, loc)
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	if !endTime.After(startTime) {
		response.ErrorFrom(c, infraerrors.BadRequest("INVALID_TIME_RANGE", "end_time must be after start_time"))
		return
	}

	requestType, stream, ok := usagehttp.ParseOptionalExportRequestType(c, req.RequestType, req.Stream)
	if !ok {
		return
	}
	billingMode, ok := usagehttp.ParseOptionalExportBillingMode(c, req.BillingMode)
	if !ok {
		return
	}

	req.StartTime = strings.TrimSpace(req.StartTime)
	req.EndTime = strings.TrimSpace(req.EndTime)
	req.Timezone = canonicalTZ
	req.ModelFilterSource = modelSource
	resolvedLocale := usagehttp.ResolveExportRequestLocale(c, req.Locale)

	filters := usage.UsageExportFilters{
		StartTime:          startTime,
		EndTime:            endTime,
		Timezone:           canonicalTZ,
		Locale:             resolvedLocale,
		RequestSource:      req.RequestSource,
		UserID:             req.UserID,
		APIKeyID:           req.APIKeyID,
		ProviderID:         req.ProviderID,
		GroupID:            req.GroupID,
		TeamID:             req.TeamID,
		PersonalOnly:       req.PersonalOnly,
		RequestID:          strings.TrimSpace(req.RequestID),
		Model:              req.Model,
		ModelFilterSource:  modelSource,
		RequestType:        requestType,
		Stream:             stream,
		NativeCompactionV2: req.NativeCompactionV2,
		BillingType:        req.BillingType,
		BillingMode:        billingMode,
	}

	idempotencyPayload := struct {
		OperatorID int64                             `json:"operator_id"` // authenticated administrator user identifier
		Body       CreateAdminUsageExportTaskRequest `json:"body"`        // normalized export creation payload
	}{
		OperatorID: subject.UserID,
		Body:       req,
	}
	h.ExecuteAdminIdempotentJSON(c, "admin.usage.export_tasks.create", idempotencyPayload, h.DefaultWriteIdempotencyTTL(), func(ctx context.Context) (any, error) {
		task, createErr := h.exportService.CreateAdminTask(ctx, subject.UserID, filters)
		if createErr != nil {
			return nil, createErr
		}
		return dto.ExportFromUsage(task), nil
	})
}

// CancelExportTask cancels a pending or running administrator export task and
// returns the updated task DTO.
func (h *UsageHandler) CancelExportTask(c *gin.Context) {
	if h.exportService == nil || !h.exportService.IsEnabled() {
		usagehttp.WriteExportDisabledResponse(c)
		return
	}
	subject, ok := authctx.GetAuthSubjectFromContext(c)
	if !ok || subject.UserID <= 0 {
		response.Unauthorized(c, "Unauthorized")
		return
	}
	taskID, ok := usagehttp.ParseExportTaskIDParam(c)
	if !ok {
		return
	}

	task, err := h.exportService.CancelAdminTask(c.Request.Context(), subject.UserID, taskID)
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Success(c, dto.ExportFromUsage(task))
}

// CreateExportDownloadTicket issues a 60-second HMAC-signed download ticket and
// relative streaming URL for a succeeded administrator export task.
func (h *UsageHandler) CreateExportDownloadTicket(c *gin.Context) {
	if h.exportService == nil || !h.exportService.IsEnabled() {
		usagehttp.WriteExportDisabledResponse(c)
		return
	}
	subject, ok := authctx.GetAuthSubjectFromContext(c)
	if !ok || subject.UserID <= 0 {
		response.Unauthorized(c, "Unauthorized")
		return
	}
	taskID, ok := usagehttp.ParseExportTaskIDParam(c)
	if !ok {
		return
	}

	ticket, expiresAt, err := h.exportService.IssueAdminDownloadTicket(c.Request.Context(), subject.UserID, taskID)
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Success(c, usagehttp.BuildExportDownloadTicketResponse(ticket, expiresAt))
}

// DownloadExportTask streams a succeeded administrator CSV artifact directly to
// an authenticated API or MCP client.
func (h *UsageHandler) DownloadExportTask(c *gin.Context) {
	if h.exportService == nil || !h.exportService.IsEnabled() {
		usagehttp.WriteExportDisabledResponse(c)
		return
	}
	subject, ok := authctx.GetAuthSubjectFromContext(c)
	if !ok || subject.UserID <= 0 {
		response.Unauthorized(c, "Unauthorized")
		return
	}
	taskID, ok := usagehttp.ParseExportTaskIDParam(c)
	if !ok {
		return
	}

	stream, err := h.exportService.OpenAdminDownload(c.Request.Context(), subject.UserID, taskID)
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	usagehttp.WriteCSVDownloadStream(c, stream)
}
