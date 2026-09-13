// HTTP 仅保留过滤与缓存响应协议，数据缓存由 Ops 持有。
package httpapi

import (
	"net/http"
	"strconv"
	"strings"

	"github.com/TokenFlux/TokenRouter/internal/ops"
	response "github.com/TokenFlux/TokenRouter/internal/server/httpx"

	"github.com/gin-gonic/gin"
)

// GetDashboardSnapshotV2 returns ops dashboard core snapshot in one request.
// GET /api/v1/admin/ops/dashboard/snapshot-v2
func (h *OpsHandler) GetDashboardSnapshotV2(c *gin.Context) {
	if h.opsService == nil {
		response.Error(c, http.StatusServiceUnavailable, "Ops service not available")
		return
	}
	if err := h.opsService.RequireMonitoringEnabled(c.Request.Context()); err != nil {
		response.ErrorFrom(c, err)
		return
	}

	startTime, endTime, err := parseOpsTimeRange(c, "1h")
	if err != nil {
		response.BadRequest(c, err.Error())
		return
	}

	filter := &ops.OpsDashboardFilter{
		StartTime: startTime,
		EndTime:   endTime,
		Platform:  strings.TrimSpace(c.Query("platform")),
	}
	if v := strings.TrimSpace(c.Query("group_id")); v != "" {
		id, err := strconv.ParseInt(v, 10, 64)
		if err != nil || id <= 0 {
			response.BadRequest(c, "Invalid group_id")
			return
		}
		filter.GroupID = &id
	}
	bucketSeconds := pickThroughputBucketSeconds(endTime.Sub(startTime))

	resp, hit, err := h.opsService.CachedDashboardSnapshot(c.Request.Context(), filter, bucketSeconds)
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	etag := response.BuildETagFromAny(resp)
	if etag != "" {
		c.Header("ETag", etag)
		c.Header("Vary", "If-None-Match")
		if hit && response.IfNoneMatchMatched(c.GetHeader("If-None-Match"), etag) {
			c.Status(http.StatusNotModified)
			return
		}
	}
	if hit {
		c.Header("X-Snapshot-Cache", "hit")
	} else {
		c.Header("X-Snapshot-Cache", "miss")
	}
	response.Success(c, resp)
}
