//go:build integration

package ops_test

import (
	"context"
	"database/sql"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"

	"github.com/TokenFlux/TokenRouter/internal/infra/postgres"
	"github.com/TokenFlux/TokenRouter/internal/ops"
	"github.com/TokenFlux/TokenRouter/internal/ops/httpapi"
	opsstore "github.com/TokenFlux/TokenRouter/internal/ops/postgres"
)

// dashboard_latency_integration_test.go exercises ops_repo_dashboard.go,
// ops_dashboard_handler.go, and ops_snapshot_v2_handler.go with lib/pq and a real
// PostgreSQL backend. An isolated usage view delays the timings column while
// counts and trends still read the same fixture rows.

// TestDashboardLatencyQueryPreservesSamples checks the raw aggregates with
// first-event and first-output samples, a zero measurement, and missing historical
// timings. Group, platform, and exclusive-end filters leave three matching rows.
func TestDashboardLatencyQueryPreservesSamples(t *testing.T) {
	repo := opsstore.NewOpsRepository(openDashboardLatencyFixture(t))
	start := time.Date(2024, 3, 10, 13, 0, 0, 0, time.UTC)
	end := start.Add(20 * time.Minute)
	groupID := int64(7)
	filter := &ops.OpsDashboardFilter{
		StartTime: start, EndTime: end, GroupID: &groupID, Platform: "openai", QueryMode: ops.OpsQueryModeRaw,
	}
	overview, err := repo.GetDashboardOverview(t.Context(), filter)
	require.NoError(t, err)
	require.EqualValues(t, 3, overview.SuccessCount)
	require.Equal(t, ops.OpsPercentiles{
		P50: new(200), P90: new(280), P95: new(290), P99: new(298),
		Avg: new(200), Max: new(300),
	}, overview.Duration)
	require.Equal(t, ops.OpsPercentiles{
		P50: new(110), P90: new(198), P95: new(209), P99: new(218),
		Avg: new(110), Max: new(220),
	}, overview.TTFT)
}

// TestDashboardLatencyTimeoutReturnsHTTP200 exercises both dashboard handlers
// while the latency query exceeds its real two-second deadline. Each response
// preserves traffic totals and the snapshot also preserves both trends.
func TestDashboardLatencyTimeoutReturnsHTTP200(t *testing.T) {
	repo := opsstore.NewOpsRepository(openDashboardLatencyFixture(t))
	service := ops.BuildOpsService(repo, nil, nil, nil, nil, nil, nil, nil)
	handler := httpapi.NewOpsHandler(service, httpapi.OpsWSOptions{})
	router := gin.New()
	const dashboard = "/api/v1/admin/ops/dashboard/"
	router.GET(dashboard+"overview", handler.GetDashboardOverview)
	router.GET(dashboard+"snapshot-v2", handler.GetDashboardSnapshotV2)
	const filters = "?start_time=2024-03-10T11:30:00Z&end_time=2024-03-10T12:30:00Z&platform=openai&group_id=7"
	for _, endpoint := range []string{"overview", "snapshot-v2"} {
		t.Run(endpoint, func(t *testing.T) {
			request := httptest.NewRequestWithContext(t.Context(), http.MethodGet, dashboard+endpoint+filters, nil)
			response := httptest.NewRecorder()
			started := time.Now()
			router.ServeHTTP(response, request)
			require.Equal(t, http.StatusOK, response.Code, response.Body.String())
			require.GreaterOrEqual(t, time.Since(started), 2*time.Second)

			var envelope struct {
				Code int             `json:"code"` // application response code
				Data json.RawMessage `json:"data"` // endpoint-specific dashboard payload
			}
			require.NoError(t, json.Unmarshal(response.Body.Bytes(), &envelope))
			require.Zero(t, envelope.Code)
			var overview *ops.OpsDashboardOverview
			if endpoint == "snapshot-v2" {
				var snapshot ops.DashboardSnapshot
				require.NoError(t, json.Unmarshal(envelope.Data, &snapshot))
				require.NotNil(t, snapshot.ThroughputTrend)
				require.NotEmpty(t, snapshot.ThroughputTrend.Points)
				require.NotNil(t, snapshot.ErrorTrend)
				require.NotEmpty(t, snapshot.ErrorTrend.Points)
				overview = snapshot.Overview
			} else {
				require.NoError(t, json.Unmarshal(envelope.Data, &overview))
			}
			require.NotNil(t, overview)
			require.EqualValues(t, 1, overview.RequestCountTotal)
			require.EqualValues(t, 30, overview.TokenConsumed)
			require.Equal(t, ops.OpsPercentiles{}, overview.Duration)
			require.Equal(t, ops.OpsPercentiles{}, overview.TTFT)
		})
	}
}

// TestDashboardLatencyTimeoutPreservesRawFragmentCounts combines a saved hour
// with a raw segment whose latency times out. The overview includes both sets of
// traffic totals and reports only the saved hour's latency values.
func TestDashboardLatencyTimeoutPreservesRawFragmentCounts(t *testing.T) {
	db := openDashboardLatencyFixture(t)
	repo := opsstore.NewOpsRepository(db)
	start := time.Date(2024, 3, 10, 11, 0, 0, 0, time.UTC)
	_, err := db.ExecContext(t.Context(), `
INSERT INTO ops_metrics_hourly (bucket_start, success_count, token_consumed, duration_p50_ms, ttft_p50_ms, ttft_sample_count)
VALUES ($1, 4, 50, 300, 150, 4)`, start)
	require.NoError(t, err)
	filter := &ops.OpsDashboardFilter{
		StartTime: start, EndTime: start.Add(90 * time.Minute), QueryMode: ops.OpsQueryModeAuto,
	}
	overview, err := repo.GetDashboardOverview(t.Context(), filter)
	require.NoError(t, err)
	require.EqualValues(t, 5, overview.SuccessCount)
	require.EqualValues(t, 80, overview.TokenConsumed)
	require.Equal(t, ops.OpsPercentiles{P50: new(300)}, overview.Duration)
	require.Equal(t, ops.OpsPercentiles{P50: new(150)}, overview.TTFT)
}

// TestDashboardLatencyPreservesOtherCancellations exercises a shorter caller
// deadline and a PostgreSQL cancellation with an active request. Both errors
// must propagate without becoming the dashboard's optional latency timeout.
func TestDashboardLatencyPreservesOtherCancellations(t *testing.T) {
	repo := opsstore.NewOpsRepository(openDashboardLatencyFixture(t))
	start := time.Date(2024, 3, 10, 12, 0, 0, 0, time.UTC)
	filter := &ops.OpsDashboardFilter{
		StartTime: start, EndTime: start.Add(30 * time.Minute), QueryMode: ops.OpsQueryModeRaw,
	}

	// Expire the request before the latency budget so the inherited cancellation
	// cannot leave the rest of the request usable.
	ctx, cancel := context.WithTimeout(t.Context(), 250*time.Millisecond)
	defer cancel()
	overview, err := repo.GetDashboardOverview(ctx, filter)
	require.Error(t, err)
	require.Nil(t, overview)
	require.ErrorIs(t, ctx.Err(), context.DeadlineExceeded)

	filter.StartTime = filter.EndTime
	filter.EndTime = filter.EndTime.Add(30 * time.Minute)
	overview, err = repo.GetDashboardOverview(t.Context(), filter)
	require.Equal(t, "57014", postgres.SQLState(err))
	require.Nil(t, overview)
}

// openDashboardLatencyFixture returns a migrated database clone with a usage
// view that can delay or cancel specific observations. Each test owns its clone,
// so the view can replace the normal table name across all pooled connections.
func openDashboardLatencyFixture(t *testing.T) *sql.DB {
	t.Helper()
	db := dashboardLatencySuite.New(t)
	// A stable function lets PostgreSQL skip the view's timing expression for
	// counts and trends, so only latency aggregation exceeds its deadline.
	_, err := db.ExecContext(t.Context(), `
ALTER TABLE usage_logs RENAME TO original_usage_logs;
CREATE FUNCTION observed_timings(value jsonb, behavior text) RETURNS jsonb
LANGUAGE plpgsql STABLE AS $$
BEGIN
  IF behavior = 'slow' THEN
    PERFORM pg_sleep(8);
  ELSIF behavior = 'canceled' THEN
    RAISE query_canceled;
  END IF;
  RETURN value;
END;
$$;
CREATE VIEW usage_logs AS
SELECT created_at, group_id, platform, 'user'::text AS request_source,
       20 AS input_tokens, 10 AS output_tokens, 0 AS cache_creation_tokens, 0 AS cache_read_tokens,
       duration_ms, observed_timings(recorded_timings, behavior) AS timings
FROM (VALUES
  ('2024-03-10 12:15Z'::timestamptz, 7::bigint, 'openai', 420, '{"upstream_stream":true,"values":{"upstream_started_ms":100,"upstream_first_output_ms":200}}'::jsonb, 'slow'),
  ('2024-03-10 12:45Z', 7, 'openai', 420, NULL, 'canceled'),
  ('2024-03-10 13:15Z', 7, 'openai', 100, '{"upstream_stream":true,"ttft_mode":"first_event","values":{"upstream_started_ms":100,"upstream_first_event_ms":100,"upstream_first_output_ms":150}}', ''),
  ('2024-03-10 13:16Z', 7, 'openai', 300, '{"upstream_stream":true,"values":{"upstream_started_ms":100,"upstream_first_output_ms":320}}', ''),
  ('2024-03-10 13:17Z', 7, 'openai', NULL, NULL, ''),
  ('2024-03-10 13:18Z', 8, 'openai', 900, NULL, ''),
  ('2024-03-10 13:19Z', 7, 'gemini', 900, NULL, ''),
  ('2024-03-10 13:20Z', 7, 'openai', 900, NULL, '')
) AS samples(created_at, group_id, platform, duration_ms, recorded_timings, behavior);
`)
	require.NoError(t, err)
	return db
}
