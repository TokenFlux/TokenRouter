// 本文件维护 httpapi 的所属能力；兼容入口复用唯一实现。
package httpapi

import (
	"github.com/TokenFlux/TokenRouter/internal/usage"

	context "context"

	response "github.com/TokenFlux/TokenRouter/internal/server/httpx"

	gin "github.com/gin-gonic/gin"

	strconv "strconv"

	time "time"
)

// AccountReportOptions 仅保留 S08 的详细用量只读投影，HTTP 不接触仓储。
type AccountReportOptions struct {
	Now        func() time.Time
	StartOfDay func(time.Time) time.Time
	Query      func(context.Context, int64, time.Time, time.Time) (*usage.AccountUsageStatsResponse, error)
}

// GetStats handles getting account statistics
// GET /api/v1/admin/accounts/:id/stats
func (h *ManagementHandler) GetStats(c *gin.Context) {
	accountID, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil {
		response.BadRequest(c, "Invalid account ID")
		return
	}

	// 保留 1—90 天的请求范围与默认 30 天。
	days := 30
	if daysStr := c.Query("days"); daysStr != "" {
		if d, err := strconv.Atoi(daysStr); err == nil && d > 0 && d <= 90 {
			days = d
		}
	}

	// 日界仍按应用装配的同一时区计算。
	now := h.reports.Now()
	endTime := h.reports.StartOfDay(now.AddDate(0, 0, 1))
	startTime := h.reports.StartOfDay(now.AddDate(0, 0, -days+1))

	stats, err := h.reports.Query(c.Request.Context(), accountID, startTime, endTime)
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}

	response.Success(c, stats)
}
