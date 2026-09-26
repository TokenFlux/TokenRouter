package httpapi

import (
	"log/slog"
	"net/http"
	"sort"
	"strings"

	"github.com/TokenFlux/TokenRouter/internal/billing/pricing"
	"github.com/TokenFlux/TokenRouter/internal/server/httpx"
	"github.com/gin-gonic/gin"
)

// UpdateDefaultPricing 只在管理员明确更新时重新加载目录，普通查询不触发同步。
// @project-doc docs/interfaces/model_catalog_and_marketplace.md#gateway_default_pricing
func (h *PricingHandler) UpdateDefaultPricing(c *gin.Context) {
	if h.catalog == nil || h.catalog.Update == nil {
		httpx.Error(c, http.StatusServiceUnavailable, "pricing catalog updater is unavailable")
		return
	}
	if err := h.catalog.Update(); err != nil {
		slog.Warn("failed to update pricing catalog", "error", err)
		httpx.Error(c, http.StatusBadGateway, "failed to update pricing catalog")
		return
	}
	httpx.Success(c, gin.H{"updated": true})
}

// @project-doc docs/interfaces/model_catalog_and_marketplace.md#gateway_default_pricing
// ListDefaultPricing 只查询网关已加载的默认价，不触发目录同步。
func (h *PricingHandler) ListDefaultPricing(c *gin.Context) {
	snapshot := h.catalog.Snapshot()
	platformSet := make(map[string]struct{})
	for _, row := range snapshot.Prices {
		platformSet[row.Platform] = struct{}{}
	}
	platforms := make([]string, 0, len(platformSet))
	for platform := range platformSet {
		platforms = append(platforms, platform)
	}
	sort.Strings(platforms)
	page, pageSize := httpx.ParsePagination(c)
	platform := strings.ToLower(strings.TrimSpace(c.Query("platform")))
	search := strings.ToLower(strings.TrimSpace(c.Query("search")))
	mode := c.Query("billing_mode")
	rows := make([]pricing.DefaultModelPrice, 0)
	for _, row := range snapshot.Prices {
		if platform != "" && row.Platform != platform {
			continue
		}
		if search != "" && !strings.Contains(strings.ToLower(row.Model), search) {
			continue
		}
		if mode != "" && row.BillingMode != mode {
			continue
		}
		rows = append(rows, row)
	}
	sort.Slice(rows, func(i, j int) bool {
		if rows[i].Model != rows[j].Model {
			return rows[i].Model < rows[j].Model
		}
		return rows[i].Platform < rows[j].Platform
	})
	start := len(rows)
	if page-1 <= len(rows)/pageSize {
		start = (page - 1) * pageSize
	}
	end := min(start+pageSize, len(rows))
	httpx.Success(c, gin.H{"items": rows[start:end], "total": len(rows), "last_updated": snapshot.UpdatedAt, "platforms": platforms})
}
