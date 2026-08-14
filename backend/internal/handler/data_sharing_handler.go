package handler

import (
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/BrandonVee/TokenRouter/internal/pkg/pagination"
	"github.com/BrandonVee/TokenRouter/internal/pkg/response"
	middleware2 "github.com/BrandonVee/TokenRouter/internal/server/middleware"
	"github.com/BrandonVee/TokenRouter/internal/service"

	"github.com/gin-gonic/gin"
	"github.com/klauspost/compress/zstd"
)

// DataSharingHandler 处理用户侧数据共享须知、session 查询和导出。
type DataSharingHandler struct {
	dataSharingService *service.DataSharingService
}

// NewDataSharingHandler 创建用户侧数据共享处理器。
func NewDataSharingHandler(dataSharingService *service.DataSharingService) *DataSharingHandler {
	return &DataSharingHandler{dataSharingService: dataSharingService}
}

// dataShareSessionResponse 是前端列表和详情共用的数据共享 session 响应。
type dataShareSessionResponse struct {
	ID                 int64            `json:"id"`
	TrajectoryID       string           `json:"trajectory_id"`
	SessionID          string           `json:"session_id"`
	Dataset            string           `json:"dataset"`
	Provider           string           `json:"provider"`
	Model              string           `json:"model"`
	RequestPath        string           `json:"request_path"`
	UserAgent          string           `json:"user_agent"`
	Status             string           `json:"status"`
	IsFinalSnapshot    bool             `json:"is_final_snapshot"`
	SourceRequestCount int              `json:"source_request_count"`
	SystemPrompt       *string          `json:"system_prompt,omitempty"`
	Tools              []map[string]any `json:"tools,omitempty"`
	Messages           []map[string]any `json:"messages,omitempty"`
	Usage              map[string]any   `json:"usage,omitempty"`
	Meta               map[string]any   `json:"meta,omitempty"`
	SessionJSON        map[string]any   `json:"session_json,omitempty"`
	PayloadEncoding    string           `json:"payload_encoding,omitempty"`
	PayloadBytes       int64            `json:"payload_bytes,omitempty"`
	Exportable         bool             `json:"exportable"`
	QualityStatus      string           `json:"quality_status"`
	QualityErrors      []string         `json:"quality_errors"`
	StorageBytes       int64            `json:"storage_bytes"`
	InputTokens        int64            `json:"input_tokens"`
	OutputTokens       int64            `json:"output_tokens"`
	TotalTokens        int64            `json:"total_tokens"`
	UserID             int64            `json:"user_id"`
	UserName           string           `json:"user_name,omitempty"`
	UserEmail          string           `json:"user_email,omitempty"`
	APIKeyID           int64            `json:"api_key_id"`
	APIKeyName         string           `json:"api_key_name,omitempty"`
	GroupID            int64            `json:"group_id"`
	GroupName          string           `json:"group_name,omitempty"`
	CreatedAt          time.Time        `json:"created_at"`
	EndedAt            *time.Time       `json:"ended_at,omitempty"`
	UpdatedAt          time.Time        `json:"updated_at"`
}

// dataShareConfirmRequest 是用户点击须知确认按钮后的版本校验请求。
type dataShareConfirmRequest struct {
	GroupID int64 `json:"group_id"`
	Version int   `json:"version" binding:"required"`
}

// dataShareExportTicketResponse 是前端触发浏览器原生下载所需的票据。
type dataShareExportTicketResponse struct {
	Token       string    `json:"token"`
	DownloadURL string    `json:"download_url"`
	Filename    string    `json:"filename"`
	Encoding    string    `json:"encoding"`
	ExpiresAt   time.Time `json:"expires_at"`
}

// GetNotice 返回当前数据共享须知。
func (h *DataSharingHandler) GetNotice(c *gin.Context) {
	notice, err := h.dataSharingService.GetNotice(c.Request.Context())
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Success(c, notice)
}

// ConfirmNotice 校验当前须知版本，真正改组仍由 API Key 更新接口完成。
func (h *DataSharingHandler) ConfirmNotice(c *gin.Context) {
	var req dataShareConfirmRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, "Invalid request: "+err.Error())
		return
	}
	notice, err := h.dataSharingService.ConfirmNotice(c.Request.Context(), req.Version)
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Success(c, gin.H{
		"confirmed":    true,
		"group_id":     req.GroupID,
		"version":      notice.Version,
		"confirmed_at": time.Now(),
	})
}

// ListSessions 查询当前用户自己的数据共享 session。
func (h *DataSharingHandler) ListSessions(c *gin.Context) {
	subject, ok := middleware2.GetAuthSubjectFromContext(c)
	if !ok {
		response.Unauthorized(c, "User not authenticated")
		return
	}
	page, pageSize := response.ParsePagination(c)
	filters, ok := parseDataShareSessionFilters(c)
	if !ok {
		return
	}
	filters.UserID = subject.UserID
	params := pagination.PaginationParams{
		Page:      page,
		PageSize:  pageSize,
		SortBy:    c.DefaultQuery("sort_by", "created_at"),
		SortOrder: c.DefaultQuery("sort_order", "desc"),
	}
	items, result, err := h.dataSharingService.ListSessions(c.Request.Context(), params, filters)
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	out := make([]dataShareSessionResponse, 0, len(items))
	for i := range items {
		out = append(out, dataShareSessionToResponse(&items[i], false))
	}
	total := int64(0)
	if result != nil {
		total = result.Total
	}
	response.Paginated(c, out, total, page, pageSize)
}

// FilterOptions 返回当前用户数据共享列表的全量筛选选项。
func (h *DataSharingHandler) FilterOptions(c *gin.Context) {
	subject, ok := middleware2.GetAuthSubjectFromContext(c)
	if !ok {
		response.Unauthorized(c, "User not authenticated")
		return
	}
	options, err := h.dataSharingService.FilterOptions(c.Request.Context(), service.DataShareSessionFilters{UserID: subject.UserID})
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Success(c, options)
}

// GetSession 返回当前用户自己的单条数据共享 session 详情。
func (h *DataSharingHandler) GetSession(c *gin.Context) {
	subject, ok := middleware2.GetAuthSubjectFromContext(c)
	if !ok {
		response.Unauthorized(c, "User not authenticated")
		return
	}
	id, err := parseDataShareIDParam(c)
	if err != nil {
		response.BadRequest(c, "Invalid session ID")
		return
	}
	session, err := h.dataSharingService.GetSession(c.Request.Context(), id, subject.UserID)
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Success(c, dataShareSessionToResponse(session, true))
}

// CreateExportTicket 为当前用户自己的数据共享 session 签发短期下载票据。
func (h *DataSharingHandler) CreateExportTicket(c *gin.Context) {
	subject, ok := middleware2.GetAuthSubjectFromContext(c)
	if !ok {
		response.Unauthorized(c, "User not authenticated")
		return
	}
	filters, ok := parseDataShareSessionFilters(c)
	if !ok {
		return
	}
	filters.UserID = subject.UserID
	if filters.SelectAll {
		filters.IDs = nil
	} else if len(filters.IDs) == 0 {
		response.BadRequest(c, "ids or select_all is required")
		return
	}
	ticket, err := h.dataSharingService.CreateExportTicket(c.Request.Context(), service.DataShareExportTicketRequest{
		Scope:    service.DataShareExportScopeUser,
		UserID:   subject.UserID,
		Filters:  filters,
		Filename: fmt.Sprintf("my-data-sharing-%s", time.Now().Format("20060102-150405")),
	})
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Success(c, dataShareExportTicketToResponse(ticket))
}

// CreateSessionExportTicket 为当前用户自己的单条 session 签发短期下载票据。
func (h *DataSharingHandler) CreateSessionExportTicket(c *gin.Context) {
	subject, ok := middleware2.GetAuthSubjectFromContext(c)
	if !ok {
		response.Unauthorized(c, "User not authenticated")
		return
	}
	id, err := parseDataShareIDParam(c)
	if err != nil {
		response.BadRequest(c, "Invalid session ID")
		return
	}
	ticket, err := h.dataSharingService.CreateExportTicket(c.Request.Context(), service.DataShareExportTicketRequest{
		Scope:    service.DataShareExportScopeUser,
		UserID:   subject.UserID,
		Filters:  service.DataShareSessionFilters{IDs: []int64{id}, UserID: subject.UserID},
		Filename: fmt.Sprintf("data-sharing-session-%d", id),
		Encoding: service.DataShareExportEncodingJSON,
	})
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Success(c, dataShareExportTicketToResponse(ticket))
}

// DownloadExport 使用短期票据下载 JSONL 或 zstd 压缩后的 JSONL。
func (h *DataSharingHandler) DownloadExport(c *gin.Context) {
	claims, err := h.dataSharingService.ParseExportTicket(c.Request.Context(), service.DataShareExportScopeUser, strings.TrimSpace(c.Query("ticket")))
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	if claims.Encoding == service.DataShareExportEncodingJSON {
		writeDataSharePlainJSON(c, claims.Filename, func() error {
			return h.dataSharingService.ExportJSONL(c.Request.Context(), c.Writer, claims.Filters, false)
		})
		return
	}
	if claims.Encoding == service.DataShareExportEncodingJSONL {
		writeDataSharePlainJSONL(c, claims.Filename, func() error {
			return h.dataSharingService.ExportJSONL(c.Request.Context(), c.Writer, claims.Filters, false)
		})
		return
	}
	writeDataShareZstdJSONL(c, claims.Filename, func(zw *zstd.Encoder) error {
		return h.dataSharingService.ExportJSONL(c.Request.Context(), zw, claims.Filters, false)
	})
}

func parseDataShareIDParam(c *gin.Context) (int64, error) {
	return strconv.ParseInt(c.Param("id"), 10, 64)
}

func parseDataShareSessionFilters(c *gin.Context) (service.DataShareSessionFilters, bool) {
	var filters service.DataShareSessionFilters
	ids, ok := parseDataShareIDsQuery(c)
	if !ok {
		return filters, false
	}
	filters.IDs = ids
	for _, item := range []struct {
		key string
		set func(int64)
	}{
		{key: "api_key_id", set: func(v int64) { filters.APIKeyID = v }},
		{key: "group_id", set: func(v int64) { filters.GroupID = v }},
	} {
		raw := strings.TrimSpace(c.Query(item.key))
		if raw == "" {
			continue
		}
		v, err := strconv.ParseInt(raw, 10, 64)
		if err != nil {
			response.BadRequest(c, "Invalid "+item.key)
			return filters, false
		}
		item.set(v)
	}
	filters.Provider = strings.TrimSpace(c.Query("provider"))
	filters.Model = strings.TrimSpace(c.Query("model"))
	filters.RequestPath = strings.TrimSpace(c.Query("request_path"))
	filters.UserAgent = strings.TrimSpace(c.Query("user_agent"))
	filters.Search = strings.TrimSpace(c.Query("search"))
	filters.APIKeyName = strings.TrimSpace(c.Query("api_key_name"))
	filters.GroupName = strings.TrimSpace(c.Query("group_name"))
	if selectAll, ok := parseDataShareBoolQuery(c, "select_all"); ok {
		filters.SelectAll = selectAll
	} else {
		return filters, false
	}
	excludeIDs, ok := parseDataShareIDsQueryKey(c, "exclude_ids")
	if !ok {
		return filters, false
	}
	filters.ExcludeIDs = excludeIDs
	if raw := strings.TrimSpace(c.Query("quality_status")); raw != "" && raw != "all" {
		filters.QualityStatus = raw
	}
	if raw := strings.TrimSpace(c.Query("exportable")); raw != "" && raw != "all" {
		v, err := strconv.ParseBool(raw)
		if err != nil {
			response.BadRequest(c, "Invalid exportable value, use true or false")
			return filters, false
		}
		filters.Exportable = &v
	}
	start, err := parseDataShareTimeQuery(c, "start_date", "start_at")
	if err != nil {
		response.BadRequest(c, "Invalid start_date format")
		return filters, false
	}
	end, err := parseDataShareTimeQuery(c, "end_date", "end_at")
	if err != nil {
		response.BadRequest(c, "Invalid end_date format")
		return filters, false
	}
	filters.StartTime = start
	filters.EndTime = end
	return filters, true
}

func parseDataShareIDsQuery(c *gin.Context) ([]int64, bool) {
	return parseDataShareIDsQueryKey(c, "ids")
}

func parseDataShareIDsQueryKey(c *gin.Context, key string) ([]int64, bool) {
	rawValues := c.QueryArray(key)
	seen := make(map[int64]struct{}, len(rawValues))
	ids := make([]int64, 0, len(rawValues))
	for _, rawValue := range rawValues {
		for _, raw := range strings.Split(rawValue, ",") {
			raw = strings.TrimSpace(raw)
			if raw == "" {
				continue
			}
			id, err := strconv.ParseInt(raw, 10, 64)
			if err != nil || id <= 0 {
				response.BadRequest(c, "Invalid "+key)
				return nil, false
			}
			if _, ok := seen[id]; ok {
				continue
			}
			seen[id] = struct{}{}
			ids = append(ids, id)
		}
	}
	return ids, true
}

func parseDataShareBoolQuery(c *gin.Context, key string) (bool, bool) {
	raw := strings.TrimSpace(c.Query(key))
	if raw == "" {
		return false, true
	}
	v, err := strconv.ParseBool(raw)
	if err != nil {
		response.BadRequest(c, "Invalid "+key)
		return false, false
	}
	return v, true
}

func parseDataShareTimeQuery(c *gin.Context, keys ...string) (*time.Time, error) {
	raw := ""
	for _, key := range keys {
		raw = strings.TrimSpace(c.Query(key))
		if raw != "" {
			break
		}
	}
	if raw == "" {
		return nil, nil
	}
	for _, layout := range []string{time.RFC3339Nano, time.RFC3339, "2006-01-02"} {
		t, err := time.Parse(layout, raw)
		if err == nil {
			if layout == "2006-01-02" && strings.Contains(keys[0], "end") {
				t = t.AddDate(0, 0, 1)
			}
			return &t, nil
		}
	}
	return nil, fmt.Errorf("invalid time")
}

func dataShareSessionToResponse(session *service.DataShareSession, includePayload bool) dataShareSessionResponse {
	if session == nil {
		return dataShareSessionResponse{}
	}
	resp := dataShareSessionResponse{
		ID:                 session.ID,
		TrajectoryID:       session.TrajectoryID,
		SessionID:          session.SessionID,
		Dataset:            session.Dataset,
		Provider:           session.Provider,
		Model:              session.Model,
		RequestPath:        session.RequestPath,
		UserAgent:          session.UserAgent,
		Status:             session.Status,
		IsFinalSnapshot:    session.IsFinalSnapshot,
		SourceRequestCount: session.SourceRequestCount,
		SystemPrompt:       session.SystemPrompt,
		PayloadEncoding:    session.PayloadEncoding,
		PayloadBytes:       session.PayloadBytes,
		Exportable:         session.Exportable,
		QualityStatus:      session.QualityStatus,
		QualityErrors:      session.QualityErrors,
		StorageBytes:       session.StorageBytes,
		InputTokens:        session.InputTokens,
		OutputTokens:       session.OutputTokens,
		TotalTokens:        session.TotalTokens,
		UserID:             session.UserID,
		UserName:           session.UserName,
		UserEmail:          session.UserEmail,
		APIKeyID:           session.APIKeyID,
		APIKeyName:         session.APIKeyName,
		GroupID:            session.GroupID,
		GroupName:          session.GroupName,
		CreatedAt:          session.CreatedAt,
		EndedAt:            session.EndedAt,
		UpdatedAt:          session.UpdatedAt,
	}
	if includePayload {
		resp.Tools = session.Tools
		resp.Messages = session.Messages
		resp.Usage = session.Usage
		resp.Meta = service.PublicDataShareSessionMeta(session.Meta)
		resp.SessionJSON = service.PublicDataShareSessionPayload(session.SessionJSON)
	}
	return resp
}

func dataShareExportTicketToResponse(ticket *service.DataShareExportTicket) dataShareExportTicketResponse {
	if ticket == nil {
		return dataShareExportTicketResponse{}
	}
	return dataShareExportTicketResponse{
		Token:       ticket.Token,
		DownloadURL: ticket.DownloadURL,
		Filename:    ticket.Filename,
		Encoding:    ticket.Encoding,
		ExpiresAt:   ticket.ExpiresAt,
	}
}

func writeDataSharePlainJSON(c *gin.Context, filename string, write func() error) {
	if filename == "" {
		filename = fmt.Sprintf("data-sharing-%s.json", time.Now().Format("20060102-150405"))
	}
	c.Header("Content-Type", "application/json; charset=utf-8")
	c.Header("Content-Disposition", fmt.Sprintf(`attachment; filename="%s"`, filename))
	c.Header("Cache-Control", "no-store")
	c.Header("X-Accel-Buffering", "no")
	c.Status(http.StatusOK)
	if err := write(); err != nil {
		_ = c.Error(err)
	}
}

func writeDataSharePlainJSONL(c *gin.Context, filename string, write func() error) {
	if filename == "" {
		filename = fmt.Sprintf("data-sharing-%s.jsonl", time.Now().Format("20060102-150405"))
	}
	c.Header("Content-Type", "application/x-ndjson; charset=utf-8")
	c.Header("Content-Disposition", fmt.Sprintf(`attachment; filename="%s"`, filename))
	c.Header("Cache-Control", "no-store")
	c.Header("X-Accel-Buffering", "no")
	c.Status(http.StatusOK)
	if err := write(); err != nil {
		_ = c.Error(err)
	}
}

func writeDataShareZstdJSONL(c *gin.Context, filename string, write func(*zstd.Encoder) error) {
	if filename == "" {
		filename = fmt.Sprintf("data-sharing-%s.jsonl.zst", time.Now().Format("20060102-150405"))
	}
	c.Header("Content-Type", "application/zstd")
	c.Header("Content-Disposition", fmt.Sprintf(`attachment; filename="%s"`, filename))
	c.Header("Cache-Control", "no-store")
	c.Header("X-Accel-Buffering", "no")
	c.Status(http.StatusOK)

	zw, err := zstd.NewWriter(c.Writer)
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	if err := write(zw); err != nil {
		_ = zw.Close()
		_ = c.Error(err)
		return
	}
	if err := zw.Close(); err != nil {
		_ = c.Error(err)
		return
	}
}
