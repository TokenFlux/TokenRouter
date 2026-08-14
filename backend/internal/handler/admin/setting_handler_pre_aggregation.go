package admin

import (
	"errors"
	"time"

	"github.com/BrandonVee/TokenRouter/internal/pkg/response"
	"github.com/BrandonVee/TokenRouter/internal/service"
	"github.com/gin-gonic/gin"
)

type preAggregationAvailabilityResponse struct {
	UsageAvailable          bool   `json:"usage_available"`
	UsageDisabledReason     string `json:"usage_disabled_reason,omitempty"`
	OpsAvailable            bool   `json:"ops_available"`
	OpsDisabledReason       string `json:"ops_disabled_reason,omitempty"`
	ManualBackfillAvailable bool   `json:"manual_backfill_available"`
	ManualBackfillMaxDays   int    `json:"manual_backfill_max_days"`
}

type preAggregationSettingsResponse struct {
	Settings     service.PreAggregationSettings      `json:"settings"`
	Availability preAggregationAvailabilityResponse  `json:"availability"`
	UsageStatus  service.PreAggregationRuntimeStatus `json:"usage_status"`
	OpsStatus    service.PreAggregationRuntimeStatus `json:"ops_status"`
}

type preAggregationBackfillRequest struct {
	Days int `json:"days"`
}

// GetPreAggregationSettings 返回统一设置、部署能力和任务运行状态。
func (h *SettingHandler) GetPreAggregationSettings(c *gin.Context) {
	if h.preAggregationSettings == nil {
		response.InternalError(c, "Pre-aggregation settings service not available")
		return
	}
	response.Success(c, h.buildPreAggregationSettingsResponse(c))
}

// UpdatePreAggregationSettings 完整更新唯一的运行时预聚合配置。
func (h *SettingHandler) UpdatePreAggregationSettings(c *gin.Context) {
	if h.preAggregationSettings == nil {
		response.InternalError(c, "Pre-aggregation settings service not available")
		return
	}
	var request service.PreAggregationSettings
	if err := c.ShouldBindJSON(&request); err != nil {
		response.BadRequest(c, "Invalid request body")
		return
	}
	if request.Usage.IntervalSeconds < service.PreAggregationMinIntervalSeconds || request.Usage.IntervalSeconds > service.PreAggregationMaxIntervalSeconds {
		response.BadRequest(c, "Usage aggregation interval must be between 30 and 3600 seconds")
		return
	}
	availability := h.preAggregationSettings.Availability()
	if request.Usage.Enabled && !availability.UsageAvailable {
		response.BadRequest(c, "Usage pre-aggregation is disabled by deployment configuration")
		return
	}
	if request.Ops.Enabled && !availability.OpsAvailable {
		response.BadRequest(c, "Ops pre-aggregation is disabled by deployment configuration")
		return
	}
	if _, err := h.preAggregationSettings.Update(c.Request.Context(), request); err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Success(c, h.buildPreAggregationSettingsResponse(c))
}

// BackfillPreAggregation 按最近天数异步回填使用记录聚合。
func (h *SettingHandler) BackfillPreAggregation(c *gin.Context) {
	if h.preAggregationSettings == nil || h.dashboardAggregation == nil {
		response.InternalError(c, "Usage pre-aggregation service not available")
		return
	}
	var request preAggregationBackfillRequest
	if err := c.ShouldBindJSON(&request); err != nil {
		response.BadRequest(c, "Invalid request body")
		return
	}
	availability := h.preAggregationSettings.Availability()
	if !availability.ManualBackfillAvailable {
		response.Forbidden(c, "Manual pre-aggregation backfill is disabled")
		return
	}
	if request.Days < 1 || request.Days > availability.ManualBackfillMaxDays {
		response.BadRequest(c, "Backfill days exceed the configured limit")
		return
	}
	if !h.preAggregationSettings.UsageEnabled(c.Request.Context()) {
		response.BadRequest(c, "Usage pre-aggregation is disabled")
		return
	}
	end := time.Now().UTC()
	start := end.AddDate(0, 0, -request.Days)
	if err := h.dashboardAggregation.TriggerBackfill(start, end); err != nil {
		if errors.Is(err, service.ErrDashboardBackfillDisabled) {
			response.Forbidden(c, "Manual pre-aggregation backfill is disabled")
			return
		}
		if errors.Is(err, service.ErrDashboardBackfillTooLarge) {
			response.BadRequest(c, "Backfill days exceed the configured limit")
			return
		}
		response.ErrorFrom(c, err)
		return
	}
	response.Accepted(c, gin.H{"status": "accepted", "days": request.Days})
}

func (h *SettingHandler) buildPreAggregationSettingsResponse(c *gin.Context) preAggregationSettingsResponse {
	ctx := c.Request.Context()
	availability := h.preAggregationSettings.Availability()
	result := preAggregationSettingsResponse{
		Settings: h.preAggregationSettings.Resolve(ctx),
		Availability: preAggregationAvailabilityResponse{
			UsageAvailable:          availability.UsageAvailable,
			UsageDisabledReason:     availability.UsageDisabledReason,
			OpsAvailable:            availability.OpsAvailable,
			OpsDisabledReason:       availability.OpsDisabledReason,
			ManualBackfillAvailable: availability.ManualBackfillAvailable,
			ManualBackfillMaxDays:   availability.ManualBackfillMaxDays,
		},
		UsageStatus: service.PreAggregationRuntimeStatus{Phase: "unavailable"},
		OpsStatus:   service.PreAggregationRuntimeStatus{Phase: "unavailable"},
	}
	if h.dashboardAggregation != nil {
		result.UsageStatus = h.dashboardAggregation.RuntimeStatus(ctx)
	}
	if h.opsAggregation != nil {
		result.OpsStatus = h.opsAggregation.RuntimeStatus(ctx)
	}
	return result
}
