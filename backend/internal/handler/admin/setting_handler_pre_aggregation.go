package admin

import (
	settingshttp "github.com/TokenFlux/TokenRouter/internal/settings/httpapi"
	"github.com/gin-gonic/gin"
)

// preAggregationHTTP 为旧直接构造夹具提供窄接口，生产由 app 构造唯一处理器。
func (h *SettingHandler) preAggregationHTTP() *settingshttp.PreAggregationHandler {
	var usage settingshttp.UsageBackfill
	var ops settingshttp.AggregationStatus
	if h.dashboardAggregation != nil {
		usage = h.dashboardAggregation.DashboardAggregationService
	}
	if h.opsAggregation != nil {
		ops = h.opsAggregation
	}
	return settingshttp.NewPreAggregationHandler(h.preAggregationSettings, usage, ops)
}
func (h *SettingHandler) GetPreAggregationSettings(c *gin.Context) {
	h.preAggregationHTTP().GetPreAggregationSettings(c)
}
func (h *SettingHandler) UpdatePreAggregationSettings(c *gin.Context) {
	h.preAggregationHTTP().UpdatePreAggregationSettings(c)
}
func (h *SettingHandler) BackfillPreAggregation(c *gin.Context) {
	h.preAggregationHTTP().BackfillPreAggregation(c)
}
