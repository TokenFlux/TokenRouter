package ops

import (
	"context"
	"time"

	"github.com/TokenFlux/TokenRouter/internal/infra/telemetry/logging"
	infraerrors "github.com/TokenFlux/TokenRouter/internal/pkg/apperror"
)

// dashboard.go queries dashboard traffic totals and attaches available system
// metrics, job diagnostics, and health scores.
// Dashboard traffic totals can remain useful when system samples or
// background-job diagnostics cannot be read. The service resolves the requested
// query mode and SLA exclusions before storage runs, then attaches available
// health observations and computes the displayed diagnostic score.

// GetDashboardOverview returns traffic totals with available system and job
// diagnostics after monitoring, storage, and time validation. It resolves
// QueryMode and IgnoredStatusCodes in the supplied filter, and retries an
// automatic-mode error with a shallow raw-mode copy. Optional diagnostic read
// failures keep the traffic result; repository success requires a nonnil
// overview and a nonnil sample when the metrics read succeeds.
// @project-doc docs/operations/pre_aggregation.md#query_routing_and_fallback
func (s *OpsService) GetDashboardOverview(ctx context.Context, filter *OpsDashboardFilter) (*OpsDashboardOverview, error) {
	if err := s.validateDashboardQuery(ctx, filter); err != nil {
		return nil, err
	}

	s.resolveOpsQueryModeWithIgnoredStatusCodes(ctx, filter)

	overview, err := s.opsRepo.GetDashboardOverview(ctx, filter)
	// An automatic-mode error starts another complete query. The raw copy keeps
	// the same dimensions and SLA exclusions, and the failed first result is
	// discarded.
	if err != nil && ShouldFallbackOpsPreagg(filter, err) {
		rawFilter := CloneOpsFilterWithMode(filter, OpsQueryModeRaw)
		overview, err = s.opsRepo.GetDashboardOverview(ctx, rawFilter)
	}
	if err != nil {
		return nil, err
	}

	// Traffic totals have already been obtained. Treat a missing or failed
	// system-sample lookup as absent diagnostics; the optional read cannot replace
	// a successful traffic response.
	if metrics, err := s.opsRepo.GetLatestSystemMetrics(ctx, 1); err == nil {
		// Positive deployment pool limits supplement the measured sample so the
		// administrator view can pair current usage with configured capacity.
		if s != nil && s.cfg != nil {
			if s.cfg.Database.MaxOpenConns > 0 {
				metrics.DBMaxOpenConns = intPtr(s.cfg.Database.MaxOpenConns)
			}
			if s.cfg.Redis.PoolSize > 0 {
				metrics.RedisPoolSize = intPtr(s.cfg.Redis.PoolSize)
			}
		}
		overview.SystemMetrics = metrics
	} else if err != nil && !s.isNotFound(err) {
		logging.LegacyPrintf("service.ops", "[Ops] GetLatestSystemMetrics failed: %v", err)
	}

	// Job diagnostics can fail independently of the system sample. Health scoring
	// keeps its default job component when the heartbeat read leaves no entries.
	if heartbeats, err := s.opsRepo.ListJobHeartbeats(ctx); err == nil {
		overview.JobHeartbeats = heartbeats
	} else {
		logging.LegacyPrintf("service.ops", "[Ops] ListJobHeartbeats failed: %v", err)
	}

	overview.HealthScore = computeDashboardHealthScore(time.Now().UTC(), overview)

	return overview, nil
}

// resolveOpsQueryMode returns the source preference allowed by the current
// aggregation settings. A requested raw mode wins; an injected settings reader
// controls other requests, otherwise disabled deployment capabilities select
// raw and an absent configuration defaults to auto.
func (s *OpsService) resolveOpsQueryMode(ctx context.Context, requested OpsQueryMode) OpsQueryMode {
	// Realtime and alert callers can request raw observations directly; this
	// preference takes precedence over the injected aggregation settings reader.
	if requested == OpsQueryModeRaw {
		return OpsQueryModeRaw
	}
	if s != nil && s.preAggregationSettings != nil {
		if s.preAggregationSettings.OpsEnabled(ctx) {
			return OpsQueryModeAuto
		}
		return OpsQueryModeRaw
	}
	if s != nil && s.cfg != nil && (!s.cfg.Ops.Enabled || !s.cfg.Ops.Aggregation.Enabled) {
		return OpsQueryModeRaw
	}
	return OpsQueryModeAuto
}

// validateDashboardQuery checks monitoring, repository availability, and
// required time endpoints in that order. It rejects a nil filter, zero
// endpoints, or a reversed range, while equal endpoints are accepted and fields
// remain unchanged.
func (s *OpsService) validateDashboardQuery(ctx context.Context, filter *OpsDashboardFilter) error {
	if err := s.RequireMonitoringEnabled(ctx); err != nil {
		return err
	}
	if s.opsRepo == nil {
		return infraerrors.ServiceUnavailable("OPS_REPO_UNAVAILABLE", "Ops repository not available")
	}
	if filter == nil {
		return infraerrors.BadRequest("OPS_FILTER_REQUIRED", "filter is required")
	}
	if filter.StartTime.IsZero() || filter.EndTime.IsZero() {
		return infraerrors.BadRequest("OPS_TIME_RANGE_REQUIRED", "start_time/end_time are required")
	}
	if filter.StartTime.After(filter.EndTime) {
		return infraerrors.BadRequest("OPS_TIME_RANGE_INVALID", "start_time must be <= end_time")
	}
	return nil
}
