// 本文件由 billing 拥有资金契约与规则；旧入口仅作过渡适配。
package httpapi

import (
	errors "errors"
	strconv "strconv"
	time "time"

	authctx "github.com/TokenFlux/TokenRouter/internal/identity/httpapi/authctx"

	billing "github.com/TokenFlux/TokenRouter/internal/billing"
	timezone "github.com/TokenFlux/TokenRouter/internal/pkg/timezone"
	response "github.com/TokenFlux/TokenRouter/internal/server/httpx"
	gin "github.com/gin-gonic/gin"
)

// QuotaHandler 只读取请求身份、解码和序列化，管理写入由完整权益用例负责。
type QuotaHandler struct {
	quotas   *billing.PlatformQuotas
	calendar timezone.Calendar
	now      func() time.Time
}

func NewQuotaHandler(quotas *billing.PlatformQuotas, calendar timezone.Calendar, now func() time.Time) *QuotaHandler {
	return &QuotaHandler{quotas: quotas, calendar: calendar, now: now}
}
func (h *QuotaHandler) usecase() *billing.PlatformQuotas {
	if h == nil {
		return nil
	}
	return h.quotas
}
func (h *QuotaHandler) respond(c *gin.Context, records []billing.UserPlatformQuotaRecord, admin bool) {
	now := time.Now().UTC()
	calendar := timezone.NewCalendar(time.Local)
	if h != nil {
		now = h.now().UTC()
		calendar = h.calendar
	}
	out := make([]map[string]any, 0, len(records))
	for _, record := range records {
		out = append(out, QuotaResponse(billing.ProjectPlatformQuota(record, now, calendar), admin))
	}
	response.Success(c, map[string]any{"platform_quotas": out})
}
func (h *QuotaHandler) GetMyPlatformQuotas(c *gin.Context) {
	subject, ok := authctx.GetAuthSubjectFromContext(c)
	if !ok {
		response.Unauthorized(c, "User not authenticated")
		return
	}
	records, err := h.usecase().ListForUser(c.Request.Context(), subject.UserID)
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	h.respond(c, records, false)
}
func (h *QuotaHandler) GetUserPlatformQuotas(c *gin.Context) {
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil {
		response.BadRequest(c, "invalid user id")
		return
	}
	records, err := h.usecase().ListForAdmin(c.Request.Context(), id)
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	h.respond(c, records, true)
}
func (h *QuotaHandler) UpdateUserPlatformQuotas(c *gin.Context) {
	if !h.usecase().Available() {
		response.Error(c, 503, "platform quota service not available")
		return
	}
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil {
		response.BadRequest(c, "Invalid user ID")
		return
	}
	var req UpdateUserPlatformQuotasRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, "Invalid request: "+err.Error())
		return
	}
	records := make([]billing.UserPlatformQuotaRecord, 0, len(req.Quotas))
	for _, q := range req.Quotas {
		records = append(records, billing.UserPlatformQuotaRecord{UserID: id, Platform: q.Platform, DailyLimitUSD: q.DailyLimitUSD, WeeklyLimitUSD: q.WeeklyLimitUSD, MonthlyLimitUSD: q.MonthlyLimitUSD})
	}
	current, err := h.usecase().Replace(c.Request.Context(), adminID(c), id, records)
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	h.respond(c, current, true)
}
func (h *QuotaHandler) ResetUserPlatformQuotaWindow(c *gin.Context) {
	if !h.usecase().Available() {
		response.Error(c, 503, "platform quota service not available")
		return
	}
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil {
		response.BadRequest(c, "Invalid user ID")
		return
	}
	var req ResetUserPlatformQuotaWindowRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, "Invalid request: "+err.Error())
		return
	}
	records, err := h.usecase().Reset(c.Request.Context(), adminID(c), id, req.Platform, req.Window)
	if errors.Is(err, billing.ErrUserPlatformQuotaNotFound) {
		response.NotFound(c, "user platform quota not found")
		return
	}
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	h.respond(c, records, true)
}
func adminID(c *gin.Context) int64 {
	subject, ok := authctx.GetAuthSubjectFromContext(c)
	if !ok {
		return 0
	}
	return subject.UserID
}

// UpdateUserPlatformQuotasRequest 是 PUT /admin/users/:id/platform-quotas 的请求体。
type UpdateUserPlatformQuotasRequest struct {
	Quotas []PlatformQuotaInput `json:"quotas" binding:"required"`
}

// PlatformQuotaInput 表示单平台限额输入；limit 字段为 nil 表示不限制。
type PlatformQuotaInput struct {
	Platform        string   `json:"platform" binding:"required"`
	DailyLimitUSD   *float64 `json:"daily_limit_usd"`
	WeeklyLimitUSD  *float64 `json:"weekly_limit_usd"`
	MonthlyLimitUSD *float64 `json:"monthly_limit_usd"`
}

// ResetUserPlatformQuotaWindowRequest 是 POST /admin/users/:id/platform-quotas/reset 的请求体。
type ResetUserPlatformQuotaWindowRequest struct {
	Platform string `json:"platform" binding:"required"`
	Window   string `json:"window" binding:"required"`
}
