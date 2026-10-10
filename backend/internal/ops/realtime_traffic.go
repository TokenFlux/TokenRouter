package ops

import (
	"context"
	"time"

	infraerrors "github.com/TokenFlux/TokenRouter/internal/pkg/apperror"
)

// OpsRealtimeTrafficSummary is a lightweight summary used by the Ops dashboard "Realtime Traffic" card.
// It reports QPS/TPS current/peak/avg for the requested time window.
type OpsRealtimeTrafficSummary struct {
	// Window is a normalized label (e.g. "1min", "5min", "30min", "1h").
	Window string `json:"window"`

	StartTime time.Time `json:"start_time"`
	EndTime   time.Time `json:"end_time"`

	Platform string `json:"platform"`
	GroupID  *int64 `json:"group_id"`

	QPS OpsRateSummary `json:"qps"`
	TPS OpsRateSummary `json:"tps"`
}

// GetRealtimeTrafficSummary returns QPS/TPS current/peak/avg for the provided window.
// This is used by the Ops dashboard "Realtime Traffic" card and is intentionally lightweight.
func (s *OpsService) GetRealtimeTrafficSummary(ctx context.Context, filter *OpsDashboardFilter) (*OpsRealtimeTrafficSummary, error) {
	if err := s.validateDashboardQuery(ctx, filter); err != nil {
		return nil, err
	}
	if filter.EndTime.Sub(filter.StartTime) > time.Hour {
		return nil, infraerrors.BadRequest("OPS_TIME_RANGE_TOO_LARGE", "invalid time range: max window is 1 hour")
	}

	// Realtime traffic summary always uses raw logs (minute granularity peaks).
	filter.QueryMode = OpsQueryModeRaw
	s.applyOpsIgnoredStatusCodes(ctx, filter)

	return s.opsRepo.GetRealtimeTrafficSummary(ctx, filter)
}

// GetWindowStats 返回指定窗口的请求数和 token 数，供 WebSocket 等实时采样使用。
func (s *OpsService) GetWindowStats(ctx context.Context, startTime, endTime time.Time) (*OpsWindowStats, error) {
	if err := s.RequireMonitoringEnabled(ctx); err != nil {
		return nil, err
	}
	if s.opsRepo == nil {
		return nil, infraerrors.ServiceUnavailable("OPS_REPO_UNAVAILABLE", "Ops repository not available")
	}
	filter := &OpsDashboardFilter{
		StartTime: startTime,
		EndTime:   endTime,
	}
	s.applyOpsIgnoredStatusCodes(ctx, filter)
	return s.opsRepo.GetWindowStats(ctx, filter)
}
